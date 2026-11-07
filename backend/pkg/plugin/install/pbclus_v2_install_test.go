package install

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// 安装器侧的平台过滤与签名策略测试。
//
// 这两件事都必须**在安装器这一层**验证，而不是只测格式包：
// 格式包只负责"如实描述包"，而"当前这台机器该不该装、装出来的东西对不对"
// 是安装器的判断。之前的跨平台 bug 就出在这一层
// （包里只有 foo.exe，Linux 上以"入口不存在"失败）。

// makeMultiPkg 造一个多平台包，各平台入口内容不同，便于断言"装的是哪一份"。
func makeMultiPkg(t *testing.T, targets []string) string {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, "plugin.json", `{
  "name": "mpdemo",
  "title": "多平台演示",
  "version": "1.0.0",
  "author": "tester",
  "description": "平台过滤测试",
  "entry": "mpdemo",
  "protocolVersion": 1,
  "hooks": ["onAntiLeech"]
}`)
	// 通用文件
	writeTree(t, src, "assets/note.txt", "通用素材")
	for _, p := range targets {
		goos, _, _ := strings.Cut(p, "/")
		name := "mpdemo"
		if goos == "windows" {
			name = "mpdemo.exe"
		}
		writeTree(t, src, filepath.Join(pbclus.DistDirName, pbclus.DirName(p), name),
			"BINARY-FOR-"+p)
	}
	out := filepath.Join(t.TempDir(), "mpdemo-1.0.0"+pbclus.Extension)
	if _, err := pbclus.PackDir(out, src, pbclus.PackOptions{Platforms: targets, Tool: "test"}); err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	return out
}

func writeTree(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o750); err != nil {
		t.Fatal(err)
	}
}

// TestInstallMultiPlatformPicksRightBinary 核心：装出来的必须是**本平台**那份。
func TestInstallMultiPlatformPicksRightBinary(t *testing.T) {
	targets := []string{"linux/amd64", "windows/amd64", "darwin/arm64"}
	pkg := makeMultiPkg(t, targets)

	for _, platform := range targets {
		t.Run(platform, func(t *testing.T) {
			inst := New(filepath.Join(t.TempDir(), "plugins"))
			inst.Platform = platform

			res, err := inst.InstallFromPackage(pkg, "test")
			if err != nil {
				t.Fatalf("在 %s 上安装失败: %v", platform, err)
			}
			if res.Platform != platform {
				t.Errorf("Result.Platform = %q, want %q", res.Platform, platform)
			}
			if len(res.Platforms) != 3 {
				t.Errorf("Result.Platforms = %v", res.Platforms)
			}
			// 另外两个平台的二进制应被跳过（每一个平台各 1 个条目）
			if res.Skipped != 2 {
				t.Errorf("应跳过 2 个其它平台的条目，实际 %d", res.Skipped)
			}
			// 通用文件必须在
			if _, err := os.Stat(filepath.Join(res.Dir, "assets", "note.txt")); err != nil {
				t.Errorf("通用文件应被解出: %v", err)
			}
			// 本平台入口内容必须对
			name := "mpdemo"
			if strings.HasPrefix(platform, "windows/") {
				name = "mpdemo.exe"
			}
			b, err := os.ReadFile(filepath.Join(res.Dir, name))
			if err != nil {
				t.Fatalf("本平台入口 %s 不在: %v", name, err)
			}
			if string(b) != "BINARY-FOR-"+platform {
				t.Errorf("装错平台了：入口内容 %q，应为 %q", b, "BINARY-FOR-"+platform)
			}
			// 别的平台的二进制不该出现
			for _, other := range targets {
				if other == platform {
					continue
				}
				on := "mpdemo"
				if strings.HasPrefix(other, "windows/") {
					on = "mpdemo.exe"
				}
				if _, err := os.Stat(filepath.Join(res.Dir, on)); err == nil && on != name {
					t.Errorf("不该出现 %s 平台的入口 %s", other, on)
				}
			}
		})
	}
}

// TestInstallRejectsUnsupportedPlatform 不支持的平台要在**解包前**被拒，
// 且错误信息要能照着做。
func TestInstallRejectsUnsupportedPlatform(t *testing.T) {
	pkg := makeMultiPkg(t, []string{"linux/amd64", "windows/amd64"})
	pluginsDir := filepath.Join(t.TempDir(), "plugins")
	inst := New(pluginsDir)
	inst.Platform = "darwin/arm64"

	_, err := inst.InstallFromPackage(pkg, "test")
	if err == nil {
		t.Fatal("不支持的平台应被拒绝")
	}
	msg := err.Error()
	for _, want := range []string{"darwin/arm64", "linux/amd64", "windows/amd64", "--targets"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应含 %q，实际: %v", want, msg)
		}
	}
	// 关键：不该留下任何插件目录（先拒绝再解包）
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "mpdemo")); statErr == nil {
		t.Error("平台不匹配的包不该留下插件目录")
	}
}

// TestSignaturePolicy 三种策略 × 三种包状态的组合。
func TestSignaturePolicy(t *testing.T) {
	// 三种包：未签名 / 用未受信任的密钥签名 / 用受信任的密钥签名
	mkPkg := func(t *testing.T, key *pbclus.SigningKey) string {
		src := t.TempDir()
		writeTree(t, src, "plugin.json", `{
  "name": "sigdemo", "title": "签名演示", "version": "1.0.0",
  "author": "t", "description": "d", "entry": "sigdemo",
  "protocolVersion": 1, "hooks": ["onAntiLeech"]}`)
		writeTree(t, src, "sigdemo", "binary payload here")
		out := filepath.Join(t.TempDir(), "sigdemo-1.0.0"+pbclus.Extension)
		if _, err := pbclus.PackDir(out, src, pbclus.PackOptions{
			Universal: true, SignKey: key,
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	trustedKey, err := pbclus.GenerateSigningKey("可信发布者")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := pbclus.GenerateSigningKey("陌生发布者")
	if err != nil {
		t.Fatal(err)
	}

	ring := pbclus.NewEmptyKeyRing()
	ring.Add(pbclus.TrustedKey{KeyID: trustedKey.KeyID, Name: trustedKey.Name, PublicKey: trustedKey.PublicKey})

	unsigned := mkPkg(t, nil)
	signedOther := mkPkg(t, otherKey)
	signedTrusted := mkPkg(t, trustedKey)

	cases := []struct {
		name     string
		mode     string
		pkg      string
		wantErr  string // 非空表示应安装失败，且错误里要含这个关键词
		wantWarn string // 非空表示应安装成功但带这个告警关键词
	}{
		{"off 不看签名（未签名）", SignatureOff, unsigned, "", ""},
		{"warn 未签名：装但告警", SignatureWarn, unsigned, "", "没有签名"},
		{"warn 陌生密钥：装但告警", SignatureWarn, signedOther, "", "不在信任库"},
		{"warn 受信任：装且提示", SignatureWarn, signedTrusted, "", "受信任"},
		{"require 未签名：拒绝", SignatureRequire, unsigned, "没有签名", ""},
		{"require 陌生密钥：拒绝", SignatureRequire, signedOther, "不在信任库", ""},
		{"require 受信任：放行", SignatureRequire, signedTrusted, "", "受信任"},
		{"空策略按 warn（未签名装但告警）", "", unsigned, "", "没有签名"},
		{"策略写错按 require（fail closed）", "Require!", unsigned, "require", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inst := New(filepath.Join(t.TempDir(), "plugins"))
			inst.SignatureMode = c.mode
			inst.TrustStore = ring
			inst.Platform = pbclus.HostPlatform()

			res, err := inst.InstallFromPackage(c.pkg, "test")
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("应被拒绝（策略 %q）", c.mode)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("错误应含 %q，实际: %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("应安装成功，却失败: %v", err)
			}
			if res.Signature == nil {
				t.Fatal("Result 应带上签名状态，供界面如实展示")
			}
			joined := strings.Join(res.Warnings, " | ")
			if c.wantWarn != "" && !strings.Contains(joined, c.wantWarn) {
				t.Errorf("告警应含 %q，实际: %v", c.wantWarn, res.Warnings)
			}
			if c.wantWarn == "" && joined != "" {
				// off 策略下不该有签名相关的告警
				if c.mode == SignatureOff && strings.Contains(joined, "签名") {
					t.Errorf("off 策略不该产生签名告警: %v", res.Warnings)
				}
			}
		})
	}
}

// TestNoTrustStoreMeansNothingTrusted 没有信任库时**不会**信任任何签名。
//
// 这是最容易写错也最危险的一条：把"没有信任库"实现成"跳过校验"
// 等于给所有人开了后门。
func TestNoTrustStoreMeansNothingTrusted(t *testing.T) {
	key, err := pbclus.GenerateSigningKey("某人")
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeTree(t, src, "plugin.json", `{
  "name": "ntdemo", "title": "t", "version": "1.0.0",
  "author": "a", "description": "d", "entry": "ntdemo",
  "protocolVersion": 1, "hooks": ["onAntiLeech"]}`)
	writeTree(t, src, "ntdemo", "payload")
	pkg := filepath.Join(t.TempDir(), "ntdemo.pbclus")
	if _, err := pbclus.PackDir(pkg, src, pbclus.PackOptions{Universal: true, SignKey: key}); err != nil {
		t.Fatal(err)
	}

	inst := New(filepath.Join(t.TempDir(), "plugins"))
	inst.SignatureMode = SignatureRequire
	inst.TrustStore = nil // 没有信任库
	inst.Platform = pbclus.HostPlatform()

	_, err = inst.InstallFromPackage(pkg, "test")
	if err == nil {
		t.Fatal("没有信任库时，require 策略必须拒绝（不能默认信任）")
	}
	if !strings.Contains(err.Error(), "不在信任库") {
		t.Errorf("错误应说明密钥未被信任，实际: %v", err)
	}
}

// TestTrustStoreFileRoundTrip 信任库文件的读写与坏文件处理。
func TestTrustStoreFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := TrustStorePath(dir)

	// 文件不存在 → 空信任库，且不是错误
	ring, err := LoadTrustStore(path)
	if err != nil {
		t.Fatalf("文件不存在不该报错: %v", err)
	}
	if len(ring.Keys) != 0 {
		t.Errorf("应是空信任库: %+v", ring)
	}
	if _, ok := ring.Lookup("0000000000000000"); ok {
		t.Error("空信任库不该信任任何 keyId")
	}

	// 写入后读回
	key, err := pbclus.GenerateSigningKey("发布者A")
	if err != nil {
		t.Fatal(err)
	}
	ring.Add(pbclus.TrustedKey{KeyID: key.KeyID, Name: "发布者A", PublicKey: key.PublicKey})
	if err := SaveTrustStore(path, ring); err != nil {
		t.Fatal(err)
	}
	back, err := LoadTrustStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := back.Lookup(key.KeyID); !ok || n != "发布者A" {
		t.Errorf("读回失败: %q %v", n, ok)
	}

	// 坏文件 → 报错（不静默当空库，否则"我明明配了信任库"会变成查不出原因的问题）
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrustStore(bad); err == nil {
		t.Error("坏信任库文件应报错")
	}
	// 自身不一致（公钥与 keyId 不匹配）也要报错
	other, _ := pbclus.GenerateSigningKey("B")
	inconsistent := filepath.Join(dir, "inconsistent.json")
	content := `{"keys":[{"keyId":"` + key.KeyID + `","name":"冒名","publicKey":"` + other.PublicKey + `"}]}`
	if err := os.WriteFile(inconsistent, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTrustStore(inconsistent); err == nil {
		t.Error("公钥与 keyId 不匹配的信任库必须被拒绝")
	}
}

// TestZipInstallHasNoPlatformOrSignatureCalls zip 路径不该受这两项影响。
//
// zip 没有平台与签名概念：给它硬套策略会让存量插件突然装不上。
func TestZipInstallHasNoPlatformOrSignatureCalls(t *testing.T) {
	zipPath := makeZipPlugin(t, "zipdemo")
	inst := New(filepath.Join(t.TempDir(), "plugins"))
	inst.SignatureMode = SignatureRequire // 即使要求签名
	inst.Platform = "nonexistent/arch"

	res, err := inst.InstallFromPackage(zipPath, "test")
	if err != nil {
		t.Fatalf("zip 安装不该被平台/签名策略拦住: %v", err)
	}
	if res.Platform != "" || res.Platforms != nil || res.Signature != nil {
		t.Errorf("zip 安装不该报告平台或签名: %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("zip 安装不该产生签名告警: %v", res.Warnings)
	}
}

// makeZipPlugin 造一个最小可安装的 zip 插件包。
func makeZipPlugin(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, "plugin.json", `{
  "name": "`+name+`", "title": "zip 演示", "version": "1.0.0",
  "author": "t", "description": "d", "entry": "`+name+`",
  "protocolVersion": 1, "hooks": ["onAntiLeech"]}`)
	writeTree(t, dir, name, "zip payload")
	return makeZip(t, dir)
}

// makeZip 把一个插件目录打成 zip，返回 zip 路径。
func makeZip(t *testing.T, srcDir string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	err = filepath.Walk(srcDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}
