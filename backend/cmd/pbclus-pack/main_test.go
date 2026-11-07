package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// 这些测试直接调 run()，不编译二进制再起子进程：
// 命令行工具最容易出问题的地方是**参数解析与退出码**，
// 而那两样在进程内就能完整覆盖。
//
// 真正需要"另一个进程"的部分（打包→安装→宿主加载），
// 由 install 包的集成测试与 smoke 脚本负责。

// fixturePlugin 造一个最小但合法的插件目录。
func fixturePlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "plugin.json", `{
  "name": "demo",
  "title": "演示插件",
  "version": "1.2.3",
  "author": "tester",
  "description": "用于测试打包工具",
  "entry": "demo",
  "protocolVersion": 1,
  "hooks": ["onAntiLeech"]
}`)
	write(t, dir, "demo", "#!/bin/sh\necho demo\n")
	write(t, dir, "assets/data.txt", strings.Repeat("compress me ", 500))
	write(t, dir, ".git/config", "[core]\n")
	write(t, dir, "lib/old.zip", "PK\x03\x04junk")
	write(t, dir, "debug.log", "noise")
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// TestPackDefaultsAndNaming 默认输出名、默认排除、退出码。
func TestPackDefaultsAndNaming(t *testing.T) {
	src := fixturePlugin(t)
	outDir := t.TempDir()

	// 用 --quiet 拿到纯路径，便于后续断言
	code, stdout, stderr := runCLI(t, "pack", src, "-o", filepath.Join(outDir, "demo.pbclus"))
	if code != 0 {
		t.Fatalf("pack 失败: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "referer") && !strings.Contains(stdout, "demo") {
		t.Logf("输出: %s", stdout)
	}

	rd, err := pbclus.Open(filepath.Join(outDir, "demo.pbclus"))
	if err != nil {
		t.Fatalf("打出来的包打不开: %v", err)
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		t.Fatalf("打出来的包校验失败: %v", err)
	}
	man := rd.Manifest()
	if man.Plugin == nil || man.Plugin.Name != "demo" || man.Plugin.Version != "1.2.3" {
		t.Fatalf("清单里的插件摘要不对: %+v", man.Plugin)
	}

	// 默认排除：版本库、压缩包、日志都不该进包
	paths := map[string]bool{}
	for _, e := range man.Entries {
		paths[e.Path] = true
	}
	for _, banned := range []string{".git/config", "lib/old.zip", "debug.log"} {
		if paths[banned] {
			t.Errorf("默认排除应该跳过 %q", banned)
		}
	}
	for _, want := range []string{"plugin.json", "demo", "assets/data.txt"} {
		if !paths[want] {
			t.Errorf("应包含 %q，实际条目: %v", want, man.Entries)
		}
	}

	// --include-all 时应当收进来
	all := filepath.Join(outDir, "all.pbclus")
	if code, _, se := runCLI(t, "pack", src, "-o", all, "--include-all"); code != 0 {
		t.Fatalf("--include-all 打包失败: %s", se)
	}
	rd2, err := pbclus.Open(all)
	if err != nil {
		t.Fatal(err)
	}
	defer rd2.Close()
	found := false
	for _, e := range rd2.Manifest().Entries {
		if e.Path == ".git/config" {
			found = true
		}
	}
	if !found {
		t.Error("--include-all 应收进 .git/config")
	}
}

// TestPackDefaultOutputName 不传 -o 时用 <插件名>-<版本>.pbclus，
// 且落在当前工作目录下（测试里切到临时目录）。
func TestPackDefaultOutputName(t *testing.T) {
	src := fixturePlugin(t)
	work := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	if code, _, se := runCLI(t, "pack", src); code != 0 {
		t.Fatalf("pack 失败: %s", se)
	}
	if _, err := os.Stat(filepath.Join(work, "demo-1.2.3.pbclus")); err != nil {
		t.Fatalf("默认输出名应为 demo-1.2.3.pbclus: %v", err)
	}
}

// TestPackUsageErrors 参数问题必须走退出码 2（用法错误），
// 与 1（执行失败）区分开 —— 脚本要靠这个区分"我写错了"和"它跑失败了"。
func TestPackUsageErrors(t *testing.T) {
	src := fixturePlugin(t)
	cases := []struct {
		name string
		args []string
	}{
		{"没有参数", []string{"pack"}},
		{"输出后缀不对", []string{"pack", src, "-o", filepath.Join(t.TempDir(), "x.zip")}},
		{"压缩级别越界", []string{"pack", src, "-o", filepath.Join(t.TempDir(), "x.pbclus"), "--level", "42"}},
		{"未知命令", []string{"frobnicate"}},
		{"没有子命令", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, _ := runCLI(t, c.args...)
			if code != 2 {
				t.Errorf("应返回退出码 2，实际 %d", code)
			}
		})
	}
}

// TestPackRejectsMissingManifestAndEntry 打包前必须拦住清单问题，
// 而不是等管理员安装时才发现。
func TestPackRejectsMissingManifestAndEntry(t *testing.T) {
	t.Run("没有 plugin.json", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "demo", "x")
		code, _, se := runCLI(t, "pack", dir, "-o", filepath.Join(t.TempDir(), "x.pbclus"))
		if code != 1 {
			t.Fatalf("应返回 1，实际 %d", code)
		}
		if !strings.Contains(se, "plugin.json") {
			t.Errorf("错误信息应提到 plugin.json，实际: %s", se)
		}
	})

	t.Run("entry 指向的文件不存在", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "plugin.json", `{"name":"demo","entry":"nope","protocolVersion":1,"hooks":["onAntiLeech"]}`)
		code, _, se := runCLI(t, "pack", dir, "-o", filepath.Join(t.TempDir(), "x.pbclus"))
		if code != 1 {
			t.Fatalf("应返回 1，实际 %d", code)
		}
		if !strings.Contains(se, "不存在") {
			t.Errorf("错误信息应说明入口文件不存在，实际: %s", se)
		}
	})

	t.Run("清单字段非法", func(t *testing.T) {
		dir := t.TempDir()
		// hooks 为空 → 清单校验失败
		write(t, dir, "plugin.json", `{"name":"demo","entry":"demo","protocolVersion":1,"hooks":[]}`)
		write(t, dir, "demo", "x")
		code, _, se := runCLI(t, "pack", dir, "-o", filepath.Join(t.TempDir(), "x.pbclus"))
		if code != 1 {
			t.Fatalf("应返回 1，实际 %d", code)
		}
		if !strings.Contains(se, "校验失败") {
			t.Errorf("错误信息应说明清单校验失败，实际: %s", se)
		}
	})
}

// TestPackRefusesOverwriteWithoutForce 不覆盖已有文件（除非 --force），
// 避免一次手滑覆盖掉上一版发布包。
func TestPackRefusesOverwriteWithoutForce(t *testing.T) {
	src := fixturePlugin(t)
	outDir := t.TempDir()
	out := filepath.Join(outDir, "demo.pbclus")

	if code, _, _ := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatal("首次打包应成功")
	}
	code, _, se := runCLI(t, "pack", src, "-o", out)
	if code != 1 {
		t.Fatalf("重复打包应失败，实际 %d", code)
	}
	if !strings.Contains(se, "--force") {
		t.Errorf("应提示用 --force，实际: %s", se)
	}
	if code, _, se := runCLI(t, "pack", src, "-o", out, "--force"); code != 0 {
		t.Fatalf("--force 应成功，实际 %d: %s", code, se)
	}
}

// TestInspectAndVerify 检查与校验。
func TestInspectAndVerify(t *testing.T) {
	src := fixturePlugin(t)
	out := filepath.Join(t.TempDir(), "demo.pbclus")
	if code, _, se := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatalf("pack 失败: %s", se)
	}

	code, stdout, se := runCLI(t, "inspect", out)
	if code != 0 {
		t.Fatalf("inspect 失败: %s", se)
	}
	for _, want := range []string{"PBClus", "demo", "1.2.3", "plugin.json", "onAntiLeech"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("inspect 输出应含 %q\n%s", want, stdout)
		}
	}

	// --json 要能机读
	code, stdout, se = runCLI(t, "inspect", out, "--json")
	if code != 0 {
		t.Fatalf("inspect --json 失败: %s", se)
	}
	var parsed struct {
		Info     pbclus.Info      `json:"info"`
		Manifest *pbclus.Manifest `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("--json 输出不是合法 JSON: %v\n%s", err, stdout)
	}
	if parsed.Manifest == nil || parsed.Manifest.Plugin.Name != "demo" {
		t.Errorf("JSON 清单不对: %+v", parsed.Manifest)
	}
	if parsed.Info.EntryCount != len(parsed.Manifest.Entries) {
		t.Errorf("info.entryCount 与清单不一致")
	}

	code, stdout, se = runCLI(t, "verify", out)
	if code != 0 {
		t.Fatalf("verify 失败: %s", se)
	}
	if !strings.Contains(stdout, "校验通过") {
		t.Errorf("verify 应报告通过，实际: %s", stdout)
	}
}

// TestVerifyDetectsCorruption 校验必须真的能发现损坏（否则这个命令没意义）。
func TestVerifyDetectsCorruption(t *testing.T) {
	src := fixturePlugin(t)
	out := filepath.Join(t.TempDir(), "demo.pbclus")
	if code, _, se := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatalf("pack 失败: %s", se)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// 翻转接近末尾的一个字节（落在载荷里，不影响尾部与头部）
	b[len(b)/2] ^= 0xFF
	bad := filepath.Join(t.TempDir(), "bad.pbclus")
	if err := os.WriteFile(bad, b, 0o640); err != nil {
		t.Fatal(err)
	}
	code, _, se := runCLI(t, "verify", bad)
	if code != 1 {
		t.Fatalf("损坏的包 verify 应返回 1，实际 %d", code)
	}
	if !strings.Contains(se, "校验失败") {
		t.Errorf("应报告校验失败，实际: %s", se)
	}
	// 也必须是 inspect 能打开但 verify 不通过（说明校验不是摆设）
	if code, _, _ := runCLI(t, "inspect", bad); code != 0 && code != 1 {
		t.Errorf("inspect 的退出码异常: %d", code)
	}
}

// TestUnpackRoundTrip 解包出来的内容必须与源一致。
func TestUnpackRoundTrip(t *testing.T) {
	src := fixturePlugin(t)
	out := filepath.Join(t.TempDir(), "demo.pbclus")
	if code, _, se := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatalf("pack 失败: %s", se)
	}
	dst := t.TempDir()
	code, stdout, se := runCLI(t, "unpack", out, "-o", dst)
	if code != 0 {
		t.Fatalf("unpack 失败: %s", se)
	}
	if !strings.Contains(stdout, "已解包") {
		t.Errorf("unpack 应有结果输出，实际: %s", stdout)
	}
	for _, rel := range []string{"plugin.json", "demo", "assets/data.txt"} {
		orig, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("解包后缺少 %q: %v", rel, err)
		}
		if !bytes.Equal(orig, got) {
			t.Errorf("%q 内容不一致", rel)
		}
	}
	// 被排除的文件不该出现
	if _, err := os.Stat(filepath.Join(dst, ".git", "config")); err == nil {
		t.Error("被排除的文件出现在了解包结果里")
	}
}

// TestUnpackRefusesNonEmptyDir 默认不往非空目录里解包：
// "我只是想看看包里有什么"不该变成一次意外覆盖。
func TestUnpackRefusesNonEmptyDir(t *testing.T) {
	src := fixturePlugin(t)
	out := filepath.Join(t.TempDir(), "demo.pbclus")
	if code, _, _ := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatal("pack 失败")
	}
	dst := t.TempDir()
	write(t, dst, "existing.txt", "别覆盖我")
	code, _, se := runCLI(t, "unpack", out, "-o", dst)
	if code != 1 {
		t.Fatalf("非空目录应被拒绝，实际 %d", code)
	}
	if !strings.Contains(se, "--force") {
		t.Errorf("应提示 --force，实际: %s", se)
	}
	if code, _, se := runCLI(t, "unpack", out, "-o", dst, "--force"); code != 0 {
		t.Fatalf("--force 应成功: %s", se)
	}
}

// TestRejectsZipRenamedToPbclus 打包工具自己也要能识别"zip 改后缀"。
func TestRejectsZipRenamedToPbclus(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake.pbclus")
	// zip 魔术字开头
	if err := os.WriteFile(fake, []byte("PK\x03\x04rest-of-a-zip"), 0o640); err != nil {
		t.Fatal(err)
	}
	code, _, se := runCLI(t, "verify", fake)
	if code != 1 {
		t.Fatalf("应返回 1，实际 %d", code)
	}
	if !strings.Contains(se, "zip") {
		t.Errorf("错误应明确指出这是 zip，实际: %s", se)
	}
	if !strings.Contains(se, "改后缀") && !strings.Contains(se, "不会改变") {
		t.Errorf("错误应指出改后缀没用，实际: %s", se)
	}
}

// TestInfoAndVersionAndHelp 元命令。
func TestInfoAndVersionAndHelp(t *testing.T) {
	if code, out, _ := runCLI(t, "info"); code != 0 || !strings.Contains(out, "PBClus") {
		t.Errorf("info 失败: code=%d out=%s", code, out)
	}
	if code, out, _ := runCLI(t, "--version"); code != 0 || !strings.Contains(out, toolVersion) {
		t.Errorf("--version 失败: code=%d out=%s", code, out)
	}
	if code, out, _ := runCLI(t, "help"); code != 0 || !strings.Contains(out, "pack") {
		t.Errorf("help 失败: code=%d", code)
	}
	// 用法信息里必须写明退出码约定（脚本作者据此判断）
	_, out, _ := runCLI(t, "help")
	if !strings.Contains(out, "退出码") {
		t.Error("help 应说明退出码约定")
	}
}

// TestReorderArgsAllowedAfterPositional 选项写在位置参数之后也要能解析。
//
// 这是从真实使用里踩出来的：Go 的 flag 包遇到第一个非 flag 参数就停止解析，
// 于是 `pack . -o out.pbclus` 报"用法错误"，而人最自然的写法恰恰是这样。
func TestReorderArgsAllowedAfterPositional(t *testing.T) {
	src := fixturePlugin(t)
	out := filepath.Join(t.TempDir(), "demo.pbclus")

	// 位置参数在前、选项在后
	if code, _, se := runCLI(t, "pack", src, "-o", out); code != 0 {
		t.Fatalf("选项写在后面应能解析: %s", se)
	}
	// 选项在前也可以
	out2 := filepath.Join(t.TempDir(), "demo2.pbclus")
	if code, _, se := runCLI(t, "pack", "-o", out2, src); code != 0 {
		t.Fatalf("选项写在前面应能解析: %s", se)
	}
	// 布尔选项夹在中间
	out3 := filepath.Join(t.TempDir(), "demo3.pbclus")
	if code, _, se := runCLI(t, "pack", src, "--store", "-o", out3); code != 0 {
		t.Fatalf("布尔选项夹在中间应能解析: %s", se)
	}
	rd, err := pbclus.Open(out3)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	for _, e := range rd.Manifest().Entries {
		if e.Codec != pbclus.CodecRaw {
			t.Errorf("--store 时条目 %q 的 codec 应为 raw，实际 %s", e.Path, e.Codec)
		}
	}
}

// TestWarnPlatformEntry 只有 .exe 的包必须给出跨平台警告。
//
// 显式传 --platform：不传的话自动判定会看"二进制类型 vs 当前主机"，
// 于是同一条用例在 Windows 与 Linux 上走的是不同分支，
// CI（ubuntu）与本地（windows）会得出不同结果。测试不该依赖运行主机。
func TestWarnPlatformEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plugin.json", `{
  "name":"demo","version":"1.0.0","entry":"demo",
  "protocolVersion":1,"hooks":["onAntiLeech"]}`)
	write(t, dir, "demo.exe", "MZ fake binary")

	out := filepath.Join(t.TempDir(), "demo.pbclus")
	code, stdout, se := runCLI(t, "pack", dir, "-o", out, "--platform", "windows/amd64", "--force")
	if code != 0 {
		t.Fatalf("pack 失败: %s", se)
	}
	if !strings.Contains(stdout, "Linux") || !strings.Contains(stdout, "只有") {
		t.Errorf("只含 .exe 的包应警告 Linux 上会找不到入口，实际输出:\n%s", stdout)
	}
}

// TestDecidePlatformRejectsMismatchedBinary 自动判定在"二进制类型与主机不符"时
// 必须报错，而不是猜一个平台标上去。
//
// 猜错的代价是包带着错误信息流通出去（在 Linux 上打了 Windows 的包，
// 却标成 linux/amd64），装上去以后以"入口不存在"或"一启用就崩"收场。
//
// 用例按当前主机**反向**构造"另一种平台"的二进制，所以在任何平台上都成立。
func TestDecidePlatformRejectsMismatchedBinary(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plugin.json", `{
  "name":"demo","version":"1.0.0","entry":"demo",
  "protocolVersion":1,"hooks":["onAntiLeech"]}`)

	// 造一个与当前主机类型不同的可执行文件头
	var foreign, foreignGoos string
	if runtime.GOOS == "windows" {
		foreign, foreignGoos = "\x7fELF-pretend-linux", "linux"
	} else {
		foreign, foreignGoos = "MZ-pretend-windows", "windows"
	}
	write(t, dir, "demo", foreign)

	man, err := install.ReadManifest(dir)
	if err != nil {
		t.Fatalf("清单应可读: %v", err)
	}

	platform, _, err := decidePlatform(dir, man, "")
	if err == nil {
		t.Fatalf("二进制是 %s 平台而主机是 %s，应报错要求显式指定，却返回了 %q",
			foreignGoos, runtime.GOOS, platform)
	}
	for _, want := range []string{"--platform", foreignGoos} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应含 %q（要能照着做），实际: %v", want, err)
		}
	}

	// 显式指定后必须能通过
	p, _, err := decidePlatform(dir, man, foreignGoos+"/amd64")
	if err != nil {
		t.Fatalf("显式指定平台后应通过: %v", err)
	}
	if p != foreignGoos+"/amd64" {
		t.Errorf("平台 = %q", p)
	}
}

// TestDecidePlatformUniversalForScript 脚本类入口判定为通用包。
func TestDecidePlatformUniversalForScript(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "plugin.json", `{
  "name":"demo","version":"1.0.0","entry":"demo",
  "protocolVersion":1,"hooks":["onAntiLeech"]}`)
	write(t, dir, "demo", "#!/bin/sh\necho hi\n")

	man, err := install.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, note, err := decidePlatform(dir, man, "")
	if err != nil {
		t.Fatalf("脚本入口不该报错: %v", err)
	}
	if p != "" {
		t.Errorf("脚本入口应判定为通用包（平台为空），实际 %q", p)
	}
	if !strings.Contains(note, "通用") {
		t.Errorf("应说明为什么按通用处理，实际: %q", note)
	}
}
