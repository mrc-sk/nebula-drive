package pbclus

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// crc32Of 重算头部 CRC（测试里改过头部字节后要同步它，
// 否则会先被 CRC 拦下，测不到想测的那条检查）。
func crc32Of(b []byte) uint32 { return crc32.ChecksumIEEE(b) }

// 多平台与签名的测试。
//
// 这两件事都是 v2 新增的，也都是"错了会静默出问题"的类型：
//   - 平台标错 → 装上一个架构不对的二进制，表现为插件一启用就崩；
//   - 签名判错 → 要么把不可信的包当可信（严重），
//     要么把可信的包当不可信（烦人，但至少安全）。
// 所以两类都要同时有正向与反向用例。

// ---------- 多平台 ----------

// multiSrc 造一个跨平台源码目录：dist/<平台>/ 放各平台二进制，根部放通用文件。
func multiSrc(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"multi","entry":"multi"}`))
	writeFile(t, src, "README.md", []byte("通用文件"))
	writeFile(t, src, "dist/linux-amd64/multi", []byte("ELF-LINUX-BINARY"))
	writeFile(t, src, "dist/windows-amd64/multi.exe", []byte("PE-WINDOWS-BINARY"))
	writeFile(t, src, "dist/darwin-arm64/multi", []byte("MACHO-DARWIN-BINARY"))
	return src
}

func TestPackAndExtractMultiPlatform(t *testing.T) {
	src := multiSrc(t)
	out := filepath.Join(t.TempDir(), "multi.pbclus")
	targets := []string{"linux/amd64", "windows/amd64", "darwin/arm64"}

	man, err := PackDir(out, src, PackOptions{Platforms: targets, Tool: "test"})
	if err != nil {
		t.Fatalf("多平台打包失败: %v", err)
	}
	if strings.Join(man.Platforms, ",") != "darwin/arm64,linux/amd64,windows/amd64" {
		t.Fatalf("platforms = %v", man.Platforms)
	}
	if man.Version != 2 {
		t.Errorf("多平台包应写成 v2，实际 %d", man.Version)
	}

	rd, err := Open(out)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		t.Fatalf("Verify 失败: %v", err)
	}

	// 三个平台都声明支持；第四个不支持
	for _, p := range targets {
		if !rd.Supports(p) {
			t.Errorf("应支持 %s", p)
		}
	}
	if rd.Supports("freebsd/amd64") {
		t.Error("不该支持未打包的平台")
	}
	// 不支持时必须给出**能照着做**的错误
	_, err = rd.ExtractForPlatform(t.TempDir(), "freebsd/amd64")
	if err == nil {
		t.Fatal("不支持的平台应报错")
	}
	msg := err.Error()
	for _, want := range []string{"freebsd/amd64", "--targets"} {
		if !strings.Contains(msg, want) {
			t.Errorf("平台不匹配的错误应含 %q，实际: %v", want, err)
		}
	}

	// 逐平台解包：只该拿到本平台二进制 + 通用文件
	cases := map[string]string{
		"linux/amd64":   "ELF-LINUX-BINARY",
		"windows/amd64": "PE-WINDOWS-BINARY",
		"darwin/arm64":  "MACHO-DARWIN-BINARY",
	}
	for platform, wantContent := range cases {
		t.Run(platform, func(t *testing.T) {
			dst := t.TempDir()
			rep, err := rd.ExtractForPlatform(dst, platform)
			if err != nil {
				t.Fatalf("解包 %s 失败: %v", platform, err)
			}
			if rep.Platform != platform {
				t.Errorf("报告里的平台 = %q", rep.Platform)
			}
			// 另外两个平台的二进制应被跳过
			if rep.Skipped != 2 {
				t.Errorf("应跳过 2 个其它平台的条目，实际 %d", rep.Skipped)
			}
			// 通用文件必须在
			if _, err := os.Stat(filepath.Join(dst, "plugin.json")); err != nil {
				t.Errorf("通用文件 plugin.json 应被解出: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dst, "README.md")); err != nil {
				t.Errorf("通用文件 README.md 应被解出: %v", err)
			}
			// 本平台入口必须在，内容是本平台那份
			name := "multi"
			if strings.HasPrefix(platform, "windows/") {
				name = "multi.exe"
			}
			got, err := os.ReadFile(filepath.Join(dst, name))
			if err != nil {
				t.Fatalf("本平台入口 %s 不在: %v", name, err)
			}
			if string(got) != wantContent {
				t.Errorf("入口内容是 %q，应为 %q（装错平台的二进制是最危险的情况）", got, wantContent)
			}
			// dist/ 这一层不该出现在解包结果里
			if _, err := os.Stat(filepath.Join(dst, DistDirName)); err == nil {
				t.Error("dist/ 布局目录不该出现在解包结果里")
			}
		})
	}
}

// TestPackSinglePlatformModes 单平台模式的两种声明方式。
func TestPackSinglePlatformModes(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"single"}`))
	writeFile(t, src, "single", []byte("BINARY"))

	t.Run("显式平台", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "s.pbclus")
		man, err := PackDir(out, src, PackOptions{Platform: "windows/amd64"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(man.Platforms, ",") != "windows/amd64" {
			t.Fatalf("platforms = %v", man.Platforms)
		}
		// 单平台模式下所有条目都标上该平台
		for _, e := range man.Entries {
			if e.Platform != "windows/amd64" {
				t.Errorf("条目 %q 的平台 = %q", e.Path, e.Platform)
			}
		}
		rd, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer rd.Close()
		if rd.Supports("linux/amd64") {
			t.Error("标了 windows/amd64 的包不该声明支持 linux")
		}
		if got := rd.RequiresPlatforms(); len(got) != 1 || got[0] != "windows/amd64" {
			t.Errorf("RequiresPlatforms = %v", got)
		}
	})

	t.Run("通用包", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "u.pbclus")
		man, err := PackDir(out, src, PackOptions{Universal: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(man.Platforms, ",") != PlatformAny {
			t.Fatalf("platforms = %v", man.Platforms)
		}
		rd, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer rd.Close()
		for _, p := range []string{"linux/amd64", "windows/amd64", "darwin/arm64", "freebsd/386"} {
			if !rd.Supports(p) {
				t.Errorf("通用包应支持 %s", p)
			}
		}
		// 通用包解包时不该跳过任何东西
		rep, err := rd.ExtractForPlatform(t.TempDir(), "linux/riscv64")
		if err != nil {
			t.Fatal(err)
		}
		if rep.Skipped != 0 {
			t.Errorf("通用包不该跳过条目，实际 %d", rep.Skipped)
		}
	})

	t.Run("既没声明平台也没声明通用", func(t *testing.T) {
		// 这是一个必须由调用方做的决定：默认成"通用"会让带原生二进制的包
		// 在别的平台上以"入口不存在"失败，真实原因却是平台不匹配。
		_, err := PackDir(filepath.Join(t.TempDir(), "x.pbclus"), src, PackOptions{})
		if err == nil {
			t.Fatal("应要求显式声明平台或通用")
		}
		if !strings.Contains(err.Error(), "通用包") {
			t.Errorf("错误信息应指出怎么办，实际: %v", err)
		}
	})
}

// TestPackRejectsDistLayoutInSingleMode 单平台模式下存在 dist/<平台>/ 时要报错。
//
// 静默忽略的后果是打出一个只含 plugin.json 的包 —— 装上去必然失败，
// 而失败信息（入口不存在）与真实原因（漏了 --targets）相距很远。
func TestPackRejectsDistLayoutInSingleMode(t *testing.T) {
	src := multiSrc(t)
	_, err := PackDir(filepath.Join(t.TempDir(), "x.pbclus"), src, PackOptions{Universal: true})
	if err == nil {
		t.Fatal("有 dist/<平台>/ 布局时应报错")
	}
	if !strings.Contains(err.Error(), "--targets") {
		t.Errorf("错误信息应提示用 --targets，实际: %v", err)
	}
}

// TestPackMultiPlatformRequiresEveryTarget 声明了平台却没有对应目录要报错。
func TestPackMultiPlatformRequiresEveryTarget(t *testing.T) {
	src := multiSrc(t)
	_, err := PackDir(filepath.Join(t.TempDir(), "x.pbclus"), src,
		PackOptions{Platforms: []string{"linux/amd64", "linux/arm64"}})
	if err == nil {
		t.Fatal("缺少 linux/arm64 目录时应报错")
	}
	if !strings.Contains(err.Error(), "linux/arm64") {
		t.Errorf("错误信息应指明缺哪个平台，实际: %v", err)
	}
}

// TestValidatePlatform 平台标签形状。
func TestValidatePlatform(t *testing.T) {
	for _, ok := range []string{"linux/amd64", "windows/386", "darwin/arm64", "linux/riscv64", "js/wasm", PlatformAny} {
		if err := ValidatePlatform(ok); err != nil {
			t.Errorf("ValidatePlatform(%q) 应通过: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "linux", "linux/", "/amd64", "linux/amd64/extra", "Linux/AMD64", "linux amd64", "linux/amd64;rm -rf /", strings.Repeat("a", 50) + "/x"} {
		if err := ValidatePlatform(bad); err == nil {
			t.Errorf("ValidatePlatform(%q) 应被拒绝", bad)
		}
	}
}

// TestDirNameRoundTrip 平台标签与目录名互转。
func TestDirNameRoundTrip(t *testing.T) {
	for _, p := range []string{"linux/amd64", "windows/amd64", "darwin/arm64"} {
		d := DirName(p)
		if strings.Contains(d, "/") {
			t.Errorf("目录名不该含斜杠: %q", d)
		}
		got, ok := PlatformFromDirName(d)
		if !ok || got != p {
			t.Errorf("往返失败: %q → %q → %q", p, d, got)
		}
	}
	for _, bad := range []string{"nodash", "-amd64", "linux-", "linux-amd64-extra"} {
		if p, ok := PlatformFromDirName(bad); ok {
			t.Errorf("%q 不该被当成平台目录（得到 %q）", bad, p)
		}
	}
}

// TestMaliciousPlatformManifests 清单里的平台声明也必须自洽。
//
// 与路径一样，平台字段来自不可信输入。
func TestMaliciousPlatformManifests(t *testing.T) {
	data0 := []byte("XX")
	cases := []struct {
		name string
		man  Manifest
		want string
	}{
		{
			name: "条目平台不在 platforms 里",
			man: Manifest{
				Platforms: []string{"linux/amd64"},
				Entries:   []Entry{withPlatform(rawEntry("a", data0, 0), "windows/amd64")},
			},
			want: "不在清单 platforms 里",
		},
		{
			name: "platforms 声明 any 却有带平台的条目",
			man: Manifest{
				Platforms: []string{PlatformAny},
				Entries:   []Entry{withPlatform(rawEntry("a", data0, 0), "linux/amd64")},
			},
			want: "自相矛盾",
		},
		{
			name: "platforms 同时有 any 与具体平台",
			man: Manifest{
				Platforms: []string{PlatformAny, "linux/amd64"},
				Entries:   []Entry{withPlatform(rawEntry("a", data0, 0), "linux/amd64")},
			},
			want: "不能同时声明",
		},
		{
			name: "声明的平台没有对应条目",
			man: Manifest{
				Platforms: []string{"linux/amd64"},
				Entries:   []Entry{rawEntry("a", data0, 0)},
			},
			want: "没有任何条目属于它",
		},
		{
			name: "同平台同路径重复",
			man: Manifest{
				Platforms: []string{"linux/amd64"},
				Entries: []Entry{
					withPlatform(rawEntry("a", data0, 0), "linux/amd64"),
					withPlatform(rawEntry("a", data0, 2), "linux/amd64"),
				},
			},
			want: "条目重复",
		},
		{
			name: "平台标签形状非法",
			man: Manifest{
				Platforms: []string{"linux-amd64"},
				Entries:   []Entry{withPlatform(rawEntry("a", data0, 0), "linux-amd64")},
			},
			want: "形状不对",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var payloadLen int64
			for _, e := range c.man.Entries {
				payloadLen += e.PackedSize
			}
			if payloadLen < 1 {
				payloadLen = 1
			}
			data := rawContainer(t, c.man, bytes.Repeat([]byte("x"), int(payloadLen)))
			rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("结构性打开不该失败: %v", err)
			}
			defer rd.Close()
			_, err = rd.ExtractForPlatform(t.TempDir(), "linux/amd64")
			if err == nil {
				t.Fatal("非法平台声明必须被拒绝")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误应含 %q，实际: %v", c.want, err)
			}
		})
	}
}

// TestSamePathDifferentPlatformIsAllowed 同路径多平台是**正常**的，
// 不能因为"路径重复"被误拒。
func TestSamePathDifferentPlatformIsAllowed(t *testing.T) {
	linux := []byte("LINUX-BIN")
	windows := []byte("WIN-BIN")
	payload := append(append([]byte(nil), linux...), windows...)
	man := Manifest{
		Platforms: []string{"linux/amd64", "windows/amd64"},
		Entries: []Entry{
			withPlatform(rawEntry("multi", linux, 0), "linux/amd64"),
			withPlatform(rawEntry("multi", windows, int64(len(linux))), "windows/amd64"),
		},
	}
	data := rawContainer(t, man, payload)
	rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		t.Fatalf("同路径多平台不该被当成重复: %v", err)
	}
	dst := t.TempDir()
	if _, err := rd.ExtractForPlatform(dst, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "multi"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "LINUX-BIN" {
		t.Errorf("解出来的应是 linux 那份，实际 %q", got)
	}
}

func withPlatform(e Entry, p string) Entry { e.Platform = p; return e }

// ---------- 签名 ----------

func mustKey(t *testing.T) *SigningKey {
	t.Helper()
	k, err := GenerateSigningKey("测试发布者")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"signed"}`))
	writeFile(t, src, "signed", []byte("BINARY-CONTENT"))
	key := mustKey(t)

	out := filepath.Join(t.TempDir(), "signed.pbclus")
	man, err := PackDir(out, src, PackOptions{Universal: true, SignKey: key})
	if err != nil {
		t.Fatalf("签名打包失败: %v", err)
	}
	if !man.Signed {
		t.Error("清单应标记 signed")
	}

	rd, err := Open(out)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		t.Fatalf("签名包的 Verify 应通过: %v", err)
	}

	// 没给信任库 → 签名有效但**不可信**
	st := rd.Status(nil)
	if !st.Present || !st.Valid {
		t.Fatalf("签名应存在且有效: %+v", st)
	}
	if st.Trusted {
		t.Error("没有信任库时不该判为可信（不能默认信任）")
	}
	if st.KeyID != key.KeyID {
		t.Errorf("keyId = %q, want %q", st.KeyID, key.KeyID)
	}

	// 给了信任库 → 可信，并带出名字
	ring := NewEmptyKeyRing()
	ring.Add(TrustedKey{KeyID: key.KeyID, Name: "测试发布者", PublicKey: key.PublicKey})
	if err := ring.Validate(); err != nil {
		t.Fatalf("信任库自身应合法: %v", err)
	}
	st = rd.Status(ring)
	if !st.Valid || !st.Trusted {
		t.Errorf("应判为可信: %+v", st)
	}
	if st.KeyName != "测试发布者" {
		t.Errorf("应带出信任库里的名字，实际 %q", st.KeyName)
	}
}

// resyncAllIntegrity 模拟"攻击者把所有**不依赖密钥**的校验数据全部改对"。
//
// 这是理解"为什么必须有签名"的关键实验：格式里所有的摘要、CRC、长度
// 都是公开可算的，攻击者改完内容之后完全可以把它们全部同步一遍。
// 同步之后：
//   - Verify() 通过 —— 因为它检查的本来就是"自洽性"；
//   - 只有签名会失败 —— 因为签名里含一个攻击者没有的私钥。
//
// 如果签名只是"和摘要比一比"，下面这条用例就会漏掉。
//
// 要求包用 Store:true 打包：那样条目内容是原文，
// 可以直接在原字节上重算摘要，不必模拟解压。
func resyncAllIntegrity(t *testing.T, b []byte) {
	t.Helper()
	h, err := parseHeader(b[:HeaderSize])
	if err != nil {
		t.Fatal(err)
	}
	manStart, manLen := int(h.ManifestOff), int(h.ManifestLen)
	payloadStart := int(h.PayloadOff)
	contentEnd := payloadStart + int(h.PayloadLen)

	var man Manifest
	if err := json.Unmarshal(b[manStart:manStart+manLen], &man); err != nil {
		t.Fatal(err)
	}
	// 1) 逐条目重算内容摘要
	for i := range man.Entries {
		e := &man.Entries[i]
		if e.Codec != CodecRaw {
			t.Fatalf("本用例要求 Store 打包（条目 %q 用了 %s）", e.Path, e.Codec)
		}
		sum := sha256.Sum256(b[payloadStart+int(e.Offset) : payloadStart+int(e.Offset)+int(e.PackedSize)])
		e.SHA256 = hex.EncodeToString(sum[:])
	}
	mb, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	// 摘要都是定长十六进制，所以重算后清单长度不变，偏移不必调整
	if len(mb) != manLen {
		t.Fatalf("清单长度变了（%d → %d），本用例依赖长度不变", manLen, len(mb))
	}
	copy(b[manStart:], mb)

	// 2) 清单摘要、载荷摘要、头部 CRC
	msha := sha256.Sum256(mb)
	copy(b[52:84], msha[:])
	psha := sha256.Sum256(b[payloadStart:contentEnd])
	copy(b[84:116], psha[:])
	binary.LittleEndian.PutUint32(b[116:120], crc32.ChecksumIEEE(b[:116]))

	// 3) 尾部整文件摘要（签名覆盖的就是它）
	fsha := sha256.Sum256(b[:contentEnd])
	copy(b[len(b)-FooterSize:len(b)-FooterSize+32], fsha[:])
}

// TestSignatureCatchesWhatIntegrityCannot 这是签名真正不可替代的场景。
//
// 攻击者改了内容，**并把所有不依赖密钥的校验数据都改对** ——
// 此时完整性检查全部通过（Verify 成功），因为它本来就是自洽的。
// 只有签名能发现内容与签名不再匹配。
// 如果签名只做"和摘要比一比"，这条用例就会漏掉。
func TestSignatureCatchesWhatIntegrityCannot(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"signed"}`))
	writeFile(t, src, "signed", []byte("ORIGINAL-BINARY-CONTENT"))
	key := mustKey(t)
	out := filepath.Join(t.TempDir(), "signed.pbclus")
	if _, err := PackDir(out, src, PackOptions{Universal: true, Store: true, SignKey: key}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseHeader(b[:HeaderSize])
	if err != nil {
		t.Fatal(err)
	}
	// 改载荷里的一个字节
	changed := false
	for i := int(h.PayloadOff); i < int(h.PayloadOff+h.PayloadLen); i++ {
		if b[i] == 'O' {
			b[i] = 'X'
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("测试自身没改到内容")
	}
	// 攻击者把所有公开可算的校验数据全部同步 → 完整性检查将全部通过
	resyncAllIntegrity(t, b)

	tampered := filepath.Join(t.TempDir(), "t.pbclus")
	if err := os.WriteFile(tampered, b, 0o640); err != nil {
		t.Fatal(err)
	}
	rd, err := Open(tampered)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()

	// 完整性：自洽，所以通过 —— 这正说明"完整性"不等于"没被改过"
	if err := rd.Verify(); err != nil {
		t.Fatalf("篡改者已同步全部公开校验数据，完整性检查本应通过: %v", err)
	}
	// 签名：必须发现
	st := rd.Status(nil)
	if !st.Present {
		t.Fatal("签名应仍然存在")
	}
	if st.Valid {
		t.Fatal("内容被改后签名必须判为无效 —— 否则签名毫无意义")
	}
	if st.Err == "" {
		t.Error("应给出签名失败的原因")
	}
}

// TestSignatureStripping 把签名摘掉之后，状态是"没有签名"，
// 而不是"签名有效"。策略为 require 的宿主据此拒绝，不存在降级空间。
func TestSignatureStripping(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"signed"}`))
	key := mustKey(t)

	signed := filepath.Join(t.TempDir(), "signed.pbclus")
	if _, err := PackDir(signed, src, PackOptions{Universal: true, Store: true, SignKey: key}); err != nil {
		t.Fatal(err)
	}
	unsigned := filepath.Join(t.TempDir(), "unsigned.pbclus")
	if _, err := PackDir(unsigned, src, PackOptions{Universal: true, Store: true}); err != nil {
		t.Fatal(err)
	}

	// 未签名的包：Present/Valid/Trusted 全为 false
	rd2, err := Open(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	st := rd2.Status(nil)
	rd2.Close()
	if st.Present || st.Valid || st.Trusted {
		t.Errorf("未签名的包不该报签名状态: %+v", st)
	}

	sb, _ := os.ReadFile(signed)
	ub, _ := os.ReadFile(unsigned)
	sh, _ := parseHeader(sb[:HeaderSize])
	// 签名包多出来的部分 = 签名块 + 清单里多出的 "signed":true 字段。
	// 只断言"至少多出签名块"，不写死总数：清单字段长度会随实现微调。
	if diff := len(sb) - len(ub); diff < SigBlockSize {
		t.Errorf("签名包至少应多出 %d 字节，实际差 %d", SigBlockSize, diff)
	}
	if sh.Flags&FlagSigned == 0 {
		t.Error("签名包的头部应置 FlagSigned")
	}

	// 半吊子地删签名块：只截掉尾部附近 108 字节，不改头部标记与尾部总长 → 长度不符，必须拒绝
	broken := filepath.Join(t.TempDir(), "broken.pbclus")
	if err := os.WriteFile(broken, sb[:len(sb)-SigBlockSize], 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(broken); err == nil {
		t.Error("删掉签名块却不改标记与总长，必须被长度检查拒绝")
	}
}

// TestSignatureBlockTamper 改签名块里的任何一处都要被发现。
func TestSignatureBlockTamper(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"signed"}`))
	key := mustKey(t)
	out := filepath.Join(t.TempDir(), "signed.pbclus")
	if _, err := PackDir(out, src, PackOptions{Universal: true, SignKey: key}); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(out)
	h, _ := parseHeader(orig[:HeaderSize])
	sigAt := int(h.PayloadOff + h.PayloadLen)

	cases := []struct {
		name string
		at   int
	}{
		{"改签名本身", sigAt + 20},
		{"改内嵌公钥", sigAt + 90},
		{"改 keyId", sigAt + 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := append([]byte(nil), orig...)
			b[c.at] ^= 0xFF
			bad := filepath.Join(t.TempDir(), "bad.pbclus")
			if err := os.WriteFile(bad, b, 0o640); err != nil {
				t.Fatal(err)
			}
			// 改签名本身/公钥都可能让结构性打开就失败，
			// 所以两条路都要处理；关键是**绝不能判为有效**。
			rd, oerr := Open(bad)
			if oerr != nil {
				// keyId / 公钥被改会与内嵌公钥不自洽，Open 阶段就该拒绝
				if !strings.Contains(oerr.Error(), "keyId") && !strings.Contains(oerr.Error(), "算法") {
					t.Errorf("错误应指出签名块的问题，实际: %v", oerr)
				}
				return
			}
			// 必须显式关闭：Windows 上未关闭的句柄会让 t.TempDir 清理失败，
			// 把一个断言全过的用例变成一个清理报错。
			st := rd.Status(nil)
			rd.Close()
			if st.Valid {
				t.Fatal("签名块被改后不该判为有效")
			}
		})
	}
}

// TestTrustStoreValidation 信任库自身必须自洽。
//
// "信任库里 keyId 写的是可信发布者、公钥却是别人的" 等于信任了一个冒充者。
func TestTrustStoreValidation(t *testing.T) {
	k1 := mustKey(t)
	k2 := mustKey(t)

	t.Run("合法", func(t *testing.T) {
		r := NewEmptyKeyRing()
		r.Add(TrustedKey{KeyID: k1.KeyID, Name: "A", PublicKey: k1.PublicKey})
		r.Add(TrustedKey{KeyID: k2.KeyID, Name: "B"}) // 不填公钥也允许
		if err := r.Validate(); err != nil {
			t.Fatalf("应合法: %v", err)
		}
		if n, ok := r.Lookup(k1.KeyID); !ok || n != "A" {
			t.Errorf("Lookup 失败: %q %v", n, ok)
		}
		if _, ok := r.Lookup("0000000000000000"); ok {
			t.Error("未登记的 keyId 不该被信任")
		}
	})

	t.Run("公钥与 keyId 不匹配", func(t *testing.T) {
		r := NewEmptyKeyRing()
		r.Add(TrustedKey{KeyID: k1.KeyID, Name: "冒充", PublicKey: k2.PublicKey})
		err := r.Validate()
		if err == nil {
			t.Fatal("公钥与 keyId 不匹配必须被拒绝")
		}
		if !strings.Contains(err.Error(), "不匹配") {
			t.Errorf("错误信息应说明不匹配，实际: %v", err)
		}
	})

	t.Run("keyId 长度不对", func(t *testing.T) {
		r := NewEmptyKeyRing()
		r.Add(TrustedKey{KeyID: "abc", Name: "x"})
		if err := r.Validate(); err == nil {
			t.Fatal("keyId 长度不对应被拒绝")
		}
	})

	t.Run("keyId 重复", func(t *testing.T) {
		r := NewEmptyKeyRing()
		r.Add(TrustedKey{KeyID: k1.KeyID, Name: "A"})
		r.Keys = append(r.Keys, TrustedKey{KeyID: strings.ToUpper(k1.KeyID), Name: "B"})
		if err := r.Validate(); err == nil {
			t.Fatal("keyId 重复应被拒绝")
		}
	})

	t.Run("Remove", func(t *testing.T) {
		r := NewEmptyKeyRing()
		r.Add(TrustedKey{KeyID: k1.KeyID, Name: "A"})
		if !r.Remove(k1.KeyID) {
			t.Error("Remove 应返回 true")
		}
		if _, ok := r.Lookup(k1.KeyID); ok {
			t.Error("删除后不该还在")
		}
		if r.Remove(k1.KeyID) {
			t.Error("重复删除应返回 false")
		}
	})
}

// TestSigningKeyRoundTripAndMismatch 密钥文件的读回与自洽检查。
func TestSigningKeyRoundTripAndMismatch(t *testing.T) {
	dir := t.TempDir()
	k, err := GenerateSigningKey("发布者")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "key.json")
	if err := k.Save(p); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Errorf("私钥文件权限过宽: %v（应 0600）", st.Mode())
	}
	back, err := LoadSigningKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.KeyID != k.KeyID {
		t.Errorf("keyId 不一致: %q vs %q", back.KeyID, k.KeyID)
	}
	if _, err := back.ParsePrivateKey(); err != nil {
		t.Fatalf("私钥应可解析: %v", err)
	}

	// 手改公钥 → 必须被发现，而不是"签出来的包验证不过"这种绕远路的症状
	other, _ := GenerateSigningKey("别的")
	bad := *back
	bad.PublicKey = other.PublicKey
	if _, err := bad.ParsePrivateKey(); err == nil {
		t.Fatal("私钥与公钥不匹配应被拒绝")
	}
	bad2 := *back
	bad2.KeyID = other.KeyID
	if _, err := bad2.ParsePrivateKey(); err == nil {
		t.Fatal("keyId 与公钥不匹配应被拒绝")
	}
	// 私钥被截断
	bad3 := *back
	bad3.PrivateKey = "AAAA"
	if _, err := bad3.ParsePrivateKey(); err == nil {
		t.Fatal("长度不对的私钥应被拒绝")
	}
}

// TestUnknownVersionRejected 高版本必须被拒绝。
//
// v1 解析方读 v2 包会把所有平台的二进制解到同一路径，
// 静默装上一个架构不对的二进制 —— 这正是必须升版本的原因。
func TestUnknownVersionRejected(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte(`{"name":"x"}`))
	out := filepath.Join(t.TempDir(), "x.pbclus")
	if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	// 头部版本改成 99
	binary.LittleEndian.PutUint16(b[8:], 99)
	// 头部 CRC 要跟着改，否则会先报 CRC 失败（那也在拦，但测不到版本检查）
	crc := crc32Of(b[:116])
	binary.LittleEndian.PutUint32(b[116:], crc)
	bad := filepath.Join(t.TempDir(), "v99.pbclus")
	if err := os.WriteFile(bad, b, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := Open(bad)
	if err == nil {
		t.Fatal("未知版本必须被拒绝")
	}
	if !strings.Contains(err.Error(), "版本") {
		t.Errorf("错误应指出是版本问题，实际: %v", err)
	}
}
