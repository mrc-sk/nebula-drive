package install

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// PBClus 安装路径的测试。
//
// 关注两件事：
//  1. .pbclus 包能真的装上（走完识别 → 校验 → 解包 → InstallFromDir）；
//  2. 把 zip 改成 .pbclus **不会**被当成 PBClus 接受 ——
//     这是"格式真的换了"与"只是改了个后缀"的分界线。

// makePluginDir 造一个可安装的插件目录。
func makePluginDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeF := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeF("plugin.json", `{
  "name": "pbclusdemo",
  "title": "PBClus 演示",
  "version": "1.0.0",
  "author": "tester",
  "description": "用于安装路径测试",
  "entry": "pbclusdemo",
  "protocolVersion": 1,
  "hooks": ["onAntiLeech"]
}`)
	writeF("pbclusdemo", "#!/bin/sh\necho ok\n")
	writeF("lib/data.txt", strings.Repeat("hello ", 200))
	return dir
}

// packFixture 把插件目录打成一个 PBClus 包，返回包路径。
func packFixture(t *testing.T) string {
	t.Helper()
	src := makePluginDir(t)
	out := filepath.Join(t.TempDir(), "pbclusdemo-1.0.0"+pbclus.Extension)
	if _, err := pbclus.PackDir(out, src, pbclus.PackOptions{Universal: true, Tool: "test"}); err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	return out
}

// TestInstallFromPackagePBClus 端到端：打包 → 安装 → 文件就位。
func TestInstallFromPackagePBClus(t *testing.T) {
	pkg := packFixture(t)
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	inst := New(pluginsDir)

	res, err := inst.InstallFromPackage(pkg, "test:pbclus")
	if err != nil {
		t.Fatalf("安装失败: %v", err)
	}
	if res.Name != "pbclusdemo" {
		t.Errorf("插件名 = %q, want pbclusdemo", res.Name)
	}
	if res.Manifest.Version != "1.0.0" {
		t.Errorf("版本 = %q", res.Manifest.Version)
	}
	if res.Files != 3 {
		t.Errorf("安装文件数 = %d, want 3", res.Files)
	}

	// 目录里该有的都得在，且内容一致
	for _, rel := range []string{"plugin.json", "pbclusdemo", "lib/data.txt"} {
		if _, err := os.Stat(filepath.Join(res.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("缺少 %q: %v", rel, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(res.Dir, "lib", "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.Repeat("hello ", 200) {
		t.Error("解包后内容与源不一致")
	}

	// 装完之后宿主必须能解析清单并定位入口（这是"真的装上了"的判据）
	man, err := ReadManifest(res.Dir)
	if err != nil {
		t.Fatalf("安装后清单不可读: %v", err)
	}
	if _, err := man.ExecPath(res.Dir); err != nil {
		t.Fatalf("安装后入口不可定位: %v", err)
	}
}

// TestInstallFromPBClusRejectsCorrupt 损坏的包不该装出半成品。
//
// 两种损坏要分开测，因为它们由**不同的检查**发现：
//   - 清单被改 → Open 阶段就拒绝（清单摘要）
//   - 载荷被改 → Open 过得去，Verify 阶段拒绝（载荷/条目摘要）
//
// 只翻中间一个字节是不够的：翻到哪一段决定了命中哪个检查，
// 那样的测试会随文件大小变化而"碰巧通过"。
func TestInstallFromPBClusRejectsCorrupt(t *testing.T) {
	pkg := packFixture(t)
	orig, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}

	// 用解析出来的偏移精确定位到载荷内部
	rd, err := pbclus.Open(pkg)
	if err != nil {
		t.Fatal(err)
	}
	payloadAt := int64(pbclus.HeaderSize) + int64(len(mustManifestBytes(t, rd)))
	firstEntryAt := payloadAt // 载荷起点就是第一个条目的起点
	rd.Close()
	if firstEntryAt <= 0 || firstEntryAt >= int64(len(orig)) {
		t.Fatalf("算出来的载荷偏移 %d 不合理", firstEntryAt)
	}

	cases := []struct {
		name     string
		at       int
		wantWord string
	}{
		{"载荷被改", int(firstEntryAt) + 1, "校验失败"},
		{"清单被改", pbclus.HeaderSize + 2, "无法解析"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := append([]byte(nil), orig...)
			b[c.at] ^= 0xFF
			bad := filepath.Join(t.TempDir(), "bad"+pbclus.Extension)
			if err := os.WriteFile(bad, b, 0o640); err != nil {
				t.Fatal(err)
			}
			pluginsDir := filepath.Join(t.TempDir(), "plugins")
			inst := New(pluginsDir)
			_, err := inst.InstallFromPackage(bad, "test")
			if err == nil {
				t.Fatal("损坏的包应被拒绝")
			}
			if !strings.Contains(err.Error(), c.wantWord) {
				t.Errorf("错误应含 %q，实际: %v", c.wantWord, err)
			}
			// 关键：不该留下插件目录（半成品会被后续加载，比直接失败更难查）
			if _, statErr := os.Stat(filepath.Join(pluginsDir, "pbclusdemo")); statErr == nil {
				t.Error("安装失败不该留下插件目录")
			}
		})
	}
}

// mustManifestBytes 取回清单的原始 JSON 字节长度（用于定位载荷起点）。
func mustManifestBytes(t *testing.T, rd *pbclus.Reader) []byte {
	t.Helper()
	// 清单是按 json.Marshal 写出的紧凑 JSON；这里重新序列化同结构的清单
	// 得到的长度与文件里的一致（字段顺序由结构体定义固定）。
	b, err := json.Marshal(rd.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestInstallRejectsZipRenamedToPbclus 这条是"格式真的换了"的核心断言。
//
// 把 zip 改名成 .pbclus 必须被明确拒绝，而不是被当成 PBClus 接受、
// 也不是含糊地报"未知格式"。用户改后缀的意图很明确，错误信息要正面回应它。
func TestInstallRejectsZipRenamedToPbclus(t *testing.T) {
	// 造一个内容合法的 zip
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"plugin.json": `{"name":"zdemo","title":"z","description":"z","entry":"zdemo",
			"protocolVersion":1,"hooks":["onAntiLeech"]}`,
		"zdemo": "bin",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	renamed := filepath.Join(t.TempDir(), "zdemo"+pbclus.Extension)
	if err := os.WriteFile(renamed, buf.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}

	inst := New(filepath.Join(t.TempDir(), "plugins"))
	_, err := inst.InstallFromPackage(renamed, "test")
	if err == nil {
		t.Fatal("zip 改名成 .pbclus 必须被拒绝")
	}
	msg := err.Error()
	if !strings.Contains(msg, "zip") {
		t.Errorf("错误应指出这是 zip，实际: %v", err)
	}
	if !strings.Contains(msg, "后缀") {
		t.Errorf("错误应指出改后缀没有用，实际: %v", err)
	}

	// 反面对照：同样的内容用 .zip 名字就该正常安装 ——
	// 否则上面那条可能只是"zip 装不上"而已
	zipPath := filepath.Join(t.TempDir(), "zdemo.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	res, err := inst.InstallFromPackage(zipPath, "test")
	if err != nil {
		t.Fatalf("正常命名的 zip 应能安装（兼容历史格式）: %v", err)
	}
	if res.Name != "zdemo" {
		t.Errorf("装出来的插件名 = %q", res.Name)
	}
}

// TestSniffRoutesByContentNotExtension 识别必须看内容而不是扩展名。
func TestSniffRoutesByContentNotExtension(t *testing.T) {
	pkg := packFixture(t)
	// PBClus 内容配 .zip 后缀 → 仍按 PBClus 处理
	misnamed := filepath.Join(t.TempDir(), "wrong.zip")
	b, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(misnamed, b, 0o640); err != nil {
		t.Fatal(err)
	}
	inst := New(filepath.Join(t.TempDir(), "plugins"))
	res, err := inst.InstallFromPackage(misnamed, "test")
	if err != nil {
		t.Fatalf("内容识别应优先于扩展名: %v", err)
	}
	if res.Name != "pbclusdemo" {
		t.Errorf("插件名 = %q", res.Name)
	}
}

// TestInstallFromPackageRejectsUnknown 垃圾文件要报"无法识别"，
// 并把前几个字节打出来（否则用户只能猜）。
func TestInstallFromPackageRejectsUnknown(t *testing.T) {
	junk := filepath.Join(t.TempDir(), "junk.pbclus")
	if err := os.WriteFile(junk, []byte("this is not a package at all"), 0o640); err != nil {
		t.Fatal(err)
	}
	inst := New(filepath.Join(t.TempDir(), "plugins"))
	_, err := inst.InstallFromPackage(junk, "test")
	if err == nil {
		t.Fatal("垃圾文件应被拒绝")
	}
	if !strings.Contains(err.Error(), "无法识别") {
		t.Errorf("错误应说明无法识别，实际: %v", err)
	}
	if !strings.Contains(err.Error(), pbclus.FormatName) {
		t.Errorf("错误应提到 %s，实际: %v", pbclus.FormatName, err)
	}
}

// TestInstallFromPackageRejectsDir 目录走目录的入口，别在这里静默成功。
func TestInstallFromPackageRejectsDir(t *testing.T) {
	inst := New(filepath.Join(t.TempDir(), "plugins"))
	if _, err := inst.InstallFromPackage(t.TempDir(), "test"); err == nil {
		t.Fatal("目录不该被当成包文件接受")
	}
}

// TestExecPathWithDotDir "." 作为插件目录时必须能定位入口。
//
// 回归测试：早期 ExecPath 只用 filepath.Clean 算前缀，dir="." 时
// base="." 而 p="entry"，前缀判断失败并误报 "entry 越界"。
// 这直接影响 pbclus-pack（最自然的用法就是 `pack .`）。
func TestExecPathWithDotDir(t *testing.T) {
	dir := makePluginDir(t)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	man, err := ReadManifest(".")
	if err != nil {
		t.Fatalf("ReadManifest(\".\") 失败: %v", err)
	}
	if _, err := man.ExecPath("."); err != nil {
		t.Fatalf("ExecPath(\".\") 应能定位入口，实际: %v", err)
	}
	// "./" 与绝对路径也应得到同样结果
	if _, err := man.ExecPath("./"); err != nil {
		t.Errorf("ExecPath(\"./\") 失败: %v", err)
	}
	if _, err := man.ExecPath(dir); err != nil {
		t.Errorf("ExecPath(绝对路径) 失败: %v", err)
	}
}
