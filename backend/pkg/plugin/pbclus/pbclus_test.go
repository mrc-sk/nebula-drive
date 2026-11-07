package pbclus

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 这组测试的重点不是"正常路径能跑通"，而是**恶意容器能不能被挡住**。
//
// 关键手法：rawContainer 组装一个"结构完全合法、摘要全部正确"的容器，
// 但清单内容是我故意构造的（越界路径、重复条目、撒谎的长度……）。
// 这样测的才是解包侧的防线，而不是打包侧的校验 ——
// 攻击者当然不会用我们的打包器，他会手工拼一个包。

// rawContainer 按格式规则拼一个容器：头部 → 清单 → 载荷 → 尾部。
//
// 摘要是"正确计算"的，所以它一定能通过结构性校验，
// 从而把测试压力精确地压在**清单语义校验**上。
func rawContainer(t *testing.T, man Manifest, payload []byte) []byte {
	t.Helper()
	man.Format = FormatName
	man.Version = Version
	// 只在调用方没显式设置时补默认值。
	// 早期版本这里无条件覆盖，结果"清单声明 7 个条目但只列了 1 个"
	// 那个用例被脚手架悄悄修好，安全检查其实从没被触发过。
	if man.EntryCount == 0 {
		man.EntryCount = len(man.Entries)
	}
	if man.CreatedAt == "" {
		man.CreatedAt = "2024-01-01T00:00:00Z"
	}
	if man.Tool == "" {
		man.Tool = "test"
	}
	mb, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	msha := sha256.Sum256(mb)
	psha := sha256.Sum256(payload)
	h := header{
		FormatVersion: Version,
		EntryCount:    uint32(man.EntryCount),
		ManifestOff:   HeaderSize,
		ManifestLen:   uint64(len(mb)),
		PayloadOff:    uint64(HeaderSize + len(mb)),
		PayloadLen:    uint64(len(payload)),
		ManifestSHA:   msha,
		PayloadSHA:    psha,
	}
	var buf bytes.Buffer
	buf.Write(h.marshal())
	buf.Write(mb)
	buf.Write(payload)
	fsha := sha256.Sum256(buf.Bytes())
	buf.Write(marshalFooter(fsha, uint64(buf.Len()+FooterSize)))
	return buf.Bytes()
}

// rawEntry 造一个 raw 编解码的条目。
func rawEntry(p string, data []byte, offset int64) Entry {
	s := sha256.Sum256(data)
	return Entry{
		Path: p, Size: int64(len(data)), Offset: offset,
		PackedSize: int64(len(data)), Codec: CodecRaw,
		SHA256: hex.EncodeToString(s[:]),
	}
}

// writeFile 在临时目录里造一个源文件。
func writeFile(t *testing.T, dir, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o640); err != nil {
		t.Fatal(err)
	}
}

// ---------- 正向：往返 ----------

func TestPackExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	// 可压缩（大量重复）
	compressible := bytes.Repeat([]byte("NebulaDrive plugin payload "), 4000)
	// 不可压缩（伪随机，用固定序列保证可复现）
	incompressible := make([]byte, 8192)
	x := uint32(0x12345678)
	for i := range incompressible {
		x = x*1664525 + 1013904223
		incompressible[i] = byte(x >> 24)
	}
	writeFile(t, src, "plugin.json", []byte(`{"name":"demo"}`))
	writeFile(t, src, "demo", []byte("#!/bin/sh\necho hi\n"))
	writeFile(t, src, "assets/big.txt", compressible)
	writeFile(t, src, "assets/random.bin", incompressible)
	writeFile(t, src, "lib/nested/deep/x.dat", []byte("deep"))

	out := filepath.Join(t.TempDir(), "demo.pbclus")
	man, err := PackDir(out, src, PackOptions{Universal: true, Tool: "test"})
	if err != nil {
		t.Fatalf("打包失败: %v", err)
	}
	if man.Format != FormatName || man.Version != Version {
		t.Fatalf("清单标识不对: %+v", man)
	}
	if man.EntryCount != 5 {
		t.Fatalf("条目数 = %d, want 5", man.EntryCount)
	}
	// 条目按路径排序，输出才是可复现的
	for i := 1; i < len(man.Entries); i++ {
		if man.Entries[i-1].Path >= man.Entries[i].Path {
			t.Errorf("条目未按路径排序: %q 在 %q 之前", man.Entries[i-1].Path, man.Entries[i].Path)
		}
	}
	// 可压缩的那个应该用了 deflate，随机那个应该回退成 raw
	codecOf := map[string]string{}
	for _, e := range man.Entries {
		codecOf[e.Path] = e.Codec
	}
	if codecOf["assets/big.txt"] != CodecDeflate {
		t.Errorf("高度可压缩文件应用 deflate，实际 %q", codecOf["assets/big.txt"])
	}
	if codecOf["assets/random.bin"] != CodecRaw {
		t.Errorf("不可压缩文件应回退 raw（压了反而更大），实际 %q", codecOf["assets/random.bin"])
	}

	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() >= int64(len(compressible)) {
		t.Errorf("包大小 %d 不小于未压缩内容，压缩没生效", st.Size())
	}

	// 打开 + 完整校验
	rd, err := Open(out)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		t.Fatalf("Verify 在合法包上失败: %v", err)
	}
	if !rd.Info().Compressed {
		t.Error("Info 应报告容器使用了压缩")
	}

	// 解包并逐字节比对
	dst := t.TempDir()
	rep, err := rd.Extract(dst)
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if rep.Files != 5 || rep.Verified != 5 {
		t.Fatalf("解包统计不对: %+v", rep)
	}
	for rel, want := range map[string][]byte{
		"plugin.json":            []byte(`{"name":"demo"}`),
		"demo":                   []byte("#!/bin/sh\necho hi\n"),
		"assets/big.txt":         compressible,
		"assets/random.bin":      incompressible,
		"lib/nested/deep/x.dat":  []byte("deep"),
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读回 %q 失败: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%q 内容不一致（长度 %d vs %d）", rel, len(got), len(want))
		}
	}
	// 解包出的文件必须带 owner 执行位（插件可执行文件要能跑）。
	//
	// 只在 POSIX 上断言：Windows 没有 POSIX 权限位，Go 在那里
	// 只能表达只读标志，Mode().Perm() 会报 0666 之类。
	// zip 安装路径写 0o750 也是同样的表现，所以这不是 PBClus 特有的差异。
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dst, "demo"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("解包文件缺少 owner 执行位: %v", fi.Mode())
		}
	}
}

func TestPackDirRejectsEmptyAndOversize(t *testing.T) {
	empty := t.TempDir()
	if _, err := PackDir(filepath.Join(t.TempDir(), "x.pbclus"), empty, PackOptions{Universal: true}); err == nil {
		t.Fatal("空目录应被拒绝（打一个没有内容的包没有意义）")
	}

	big := t.TempDir()
	f, err := os.Create(filepath.Join(big, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// 稀疏文件：不必真写 64MB 数据
	if err := f.Truncate(MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := PackDir(filepath.Join(t.TempDir(), "y.pbclus"), big, PackOptions{Universal: true}); err == nil {
		t.Fatal("超过单文件上限应被拒绝")
	}
}

// ---------- 格式识别 ----------

func TestSniffDistinguishesPBClusAndZip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte("{}"))
	good := filepath.Join(t.TempDir(), "good.pbclus")
	if _, err := PackDir(good, src, PackOptions{Universal: true}); err != nil {
		t.Fatal(err)
	}
	if k, err := SniffFile(good); err != nil || k != KindPBClus {
		t.Fatalf("SniffFile(pbclus) = %v, %v", k, err)
	}

	// 造一个真 zip，并把后缀改成 .pbclus —— 这正是要挡住的情况
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("plugin.json")
	w.Write([]byte("{}"))
	zw.Close()
	fake := filepath.Join(t.TempDir(), "fake.pbclus")
	if err := os.WriteFile(fake, zbuf.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}

	k, err := SniffFile(fake)
	if err != nil {
		t.Fatal(err)
	}
	if k != KindZip {
		t.Fatalf("后缀是 .pbclus 但内容是 zip，必须识别为 zip，实际 %v", k)
	}
	if !IsZipFile(fake) {
		t.Error("IsZipFile 应为 true")
	}

	// 用 PBClus 解析器去开它，必须明确说出"这是 zip"，
	// 而不是含糊的"文件损坏"—— 用户改后缀的意图很明确，
	// 错误信息要直接回应这个意图
	_, err = Open(fake)
	if err == nil {
		t.Fatal("Open 不应接受一个 zip 文件")
	}
	if !strings.Contains(err.Error(), "zip") {
		t.Errorf("错误信息应指出这是 zip，实际: %v", err)
	}
	if strings.Contains(err.Error(), "损坏") {
		t.Errorf("不该含糊地说'损坏'，应直说是 zip: %v", err)
	}

	// 未知内容
	junk := filepath.Join(t.TempDir(), "junk.pbclus")
	os.WriteFile(junk, []byte("not a container at all"), 0o640)
	if k, _ := SniffFile(junk); k != KindUnknown {
		t.Errorf("垃圾内容应识别为 unknown，实际 %v", k)
	}
	// 垃圾内容的报错要走"魔术字不匹配"这条路
	if _, err := Open(junk); err == nil || !strings.Contains(err.Error(), "魔术字") {
		t.Errorf("垃圾内容应报魔术字不匹配，实际: %v", err)
	}
}

// ---------- 损坏检测 ----------

func TestTamperDetection(t *testing.T) {
	t.Run("载荷被改", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, src, "plugin.json", []byte(`{"name":"demo","version":"1.0.0"}`))
		writeFile(t, src, "demo", bytes.Repeat([]byte("A"), 4096))
		out := filepath.Join(t.TempDir(), "demo.pbclus")
		if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		// 只改载荷里的一个字节：位置在载荷区间内
		rd, err := Open(out)
		if err != nil {
			t.Fatal(err)
		}
		payloadStart := 0
		for _, e := range rd.Manifest().Entries {
			if e.Path == "demo" {
				payloadStart = int(int64(rd.h.PayloadOff) + e.Offset)
			}
		}
		rd.Close()
		if payloadStart == 0 {
			t.Fatal("没找到 demo 条目")
		}
		b[payloadStart+10] ^= 0xFF
		tampered := filepath.Join(t.TempDir(), "tampered.pbclus")
		if err := os.WriteFile(tampered, b, 0o640); err != nil {
			t.Fatal(err)
		}

		// Open 只看结构，所以还能打开
		rd2, err := Open(tampered)
		if err != nil {
			t.Fatalf("改载荷不该影响结构性打开: %v", err)
		}
		defer rd2.Close()
		// Verify 必须发现
		if err := rd2.Verify(); err == nil {
			t.Fatal("Verify 应发现载荷被篡改")
		}
		// Extract 也必须发现，并且不留下坏文件
		dst := t.TempDir()
		if _, err := rd2.Extract(dst); err == nil {
			t.Fatal("Extract 应发现内容摘要不匹配")
		}
		if _, err := os.Stat(filepath.Join(dst, "demo")); err == nil {
			t.Error("校验失败的条目不该留在目标目录里")
		}
	})

	t.Run("清单被改", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, src, "plugin.json", []byte(`{"name":"demo"}`))
		out := filepath.Join(t.TempDir(), "demo.pbclus")
		if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(out)
		// 清单紧随头部，改其中一个字节
		b[HeaderSize+5] ^= 0x01
		tampered := filepath.Join(t.TempDir(), "m.pbclus")
		os.WriteFile(tampered, b, 0o640)
		if _, err := Open(tampered); err == nil {
			t.Fatal("清单被改后 Open 应失败（清单摘要不匹配）")
		}
	})

	t.Run("头部被改", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, src, "plugin.json", []byte(`{"name":"demo"}`))
		out := filepath.Join(t.TempDir(), "demo.pbclus")
		// 必须检查错误：忽略它会让下面的 b[20] 在空切片上 panic，
		// 于是"打包失败"伪装成"测试崩了"，掩盖真实原因。
		if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
			t.Fatalf("打包失败: %v", err)
		}
		b, _ := os.ReadFile(out)
		b[20] ^= 0x01 // 改 manifestOff 字段
		tampered := filepath.Join(t.TempDir(), "h.pbclus")
		os.WriteFile(tampered, b, 0o640)
		_, err := Open(tampered)
		if err == nil {
			t.Fatal("头部被改后 Open 应失败")
		}
		if !strings.Contains(err.Error(), "CRC32") {
			t.Errorf("应报告头部 CRC 失败（而不是别的更晚的检查），实际: %v", err)
		}
	})

	t.Run("截断", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, src, "plugin.json", []byte(`{"name":"demo"}`))
		writeFile(t, src, "demo", bytes.Repeat([]byte("B"), 1000))
		out := filepath.Join(t.TempDir(), "demo.pbclus")
		if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
			t.Fatalf("打包失败: %v", err)
		}
		b, _ := os.ReadFile(out)
		trunc := filepath.Join(t.TempDir(), "t.pbclus")
		os.WriteFile(trunc, b[:len(b)-20], 0o640)
		if _, err := Open(trunc); err == nil {
			t.Fatal("截断文件应被拒绝")
		}
	})

	t.Run("尾部追加垃圾", func(t *testing.T) {
		src := t.TempDir()
		writeFile(t, src, "plugin.json", []byte(`{"name":"demo"}`))
		out := filepath.Join(t.TempDir(), "demo.pbclus")
		if _, err := PackDir(out, src, PackOptions{Universal: true}); err != nil {
			t.Fatalf("打包失败: %v", err)
		}
		b, _ := os.ReadFile(out)
		app := filepath.Join(t.TempDir(), "a.pbclus")
		os.WriteFile(app, append(b, []byte("trailing junk")...), 0o640)
		_, err := Open(app)
		if err == nil {
			t.Fatal("尾部被追加内容后应被拒绝（长度不符）")
		}
	})
}

// ---------- 恶意清单：解包侧防线 ----------

func TestExtractRejectsMaliciousManifests(t *testing.T) {
	cases := []struct {
		name string
		man  Manifest
		want string // 期望错误里出现的关键词
	}{
		{
			name: "父目录穿越",
			man:  Manifest{Entries: []Entry{rawEntry("../escape.txt", []byte("x"), 0)}},
			want: "..",
		},
		{
			name: "深层穿越",
			man:  Manifest{Entries: []Entry{rawEntry("a/b/../../../escape.txt", []byte("x"), 0)}},
			want: "路径",
		},
		{
			name: "绝对路径",
			man:  Manifest{Entries: []Entry{rawEntry("/etc/passwd", []byte("x"), 0)}},
			want: "绝对路径",
		},
		{
			name: "反斜杠",
			man:  Manifest{Entries: []Entry{rawEntry(`..\..\escape.txt`, []byte("x"), 0)}},
			want: "反斜杠",
		},
		{
			name: "未规范化（连续斜杠）",
			man:  Manifest{Entries: []Entry{rawEntry("a//b.txt", []byte("x"), 0)}},
			want: "空路径段",
		},
		{
			name: "当前目录段",
			man:  Manifest{Entries: []Entry{rawEntry("./a.txt", []byte("x"), 0)}},
			want: "非法路径段",
		},
		{
			name: "结尾斜杠",
			man:  Manifest{Entries: []Entry{rawEntry("a/", []byte("x"), 0)}},
			want: "空路径段",
		},
		{
			name: "Windows 保留名",
			man:  Manifest{Entries: []Entry{rawEntry("NUL", []byte("x"), 0)}},
			want: "保留设备名",
		},
		{
			name: "保留名带扩展",
			man:  Manifest{Entries: []Entry{rawEntry("dir/COM1.txt", []byte("x"), 0)}},
			want: "保留设备名",
		},
		{
			name: "重复路径",
			man: Manifest{Entries: []Entry{
				rawEntry("a.txt", []byte("x"), 0),
				rawEntry("a.txt", []byte("y"), 1),
			}},
			want: "重复",
		},
		{
			name: "文件与目录冲突",
			man: Manifest{Entries: []Entry{
				rawEntry("lib", []byte("x"), 0),
				rawEntry("lib/x.so", []byte("y"), 1),
			}},
			want: "冲突",
		},
		{
			name: "声明大小超上限",
			man: Manifest{Entries: []Entry{{
				Path: "big.bin", Size: MaxFileBytes + 1, Offset: 0,
				PackedSize: 1, Codec: CodecRaw, SHA256: strings.Repeat("0", 64),
			}}},
			want: "上限",
		},
		{
			name: "偏移越出载荷",
			man: Manifest{Entries: []Entry{{
				Path: "a.bin", Size: 4, Offset: 9999, PackedSize: 4,
				Codec: CodecRaw, SHA256: strings.Repeat("0", 64),
			}}},
			want: "超出载荷",
		},
		{
			name: "未知压缩方式",
			man: Manifest{Entries: []Entry{{
				Path: "a.bin", Size: 4, Offset: 0, PackedSize: 4,
				Codec: "lzma", SHA256: strings.Repeat("0", 64),
			}}},
			want: "未知压缩方式",
		},
		{
			name: "摘要不是十六进制",
			man: Manifest{Entries: []Entry{{
				Path: "a.bin", Size: 4, Offset: 0, PackedSize: 4,
				Codec: CodecRaw, SHA256: strings.Repeat("z", 64),
			}}},
			want: "十六进制",
		},
		{
			name: "条目数与声明不符",
			man: Manifest{
				EntryCount: 7,
				Entries:    []Entry{rawEntry("a.txt", []byte("x"), 0)},
			},
			want: "实际列了",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 负载长度按条目声明"对齐"，这样每个用例只会触发**它想测的那一个**
			// 缺陷。否则布局校验（条目合计 != 载荷长度）会先报错，
			// 把"路径穿越被挡住"这类断言变成假通过 —— 挡住的其实是别的东西。
			var payloadLen int64
			for _, e := range c.man.Entries {
				payloadLen += e.PackedSize
			}
			if payloadLen < 1 {
				payloadLen = 1
			}
			data := rawContainer(t, c.man, bytes.Repeat([]byte("x"), int(payloadLen)))
			// 结构性打开会成功（摘要都是"正确"算出来的），
			// 所以防线必须在清单语义校验上 —— 这正是本用例要证明的。
			rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("结构性打开不该失败（摘要是对的）: %v", err)
			}
			defer rd.Close()

			dst := t.TempDir()
			_, err = rd.Extract(dst)
			if err == nil {
				t.Fatalf("恶意清单必须被拒绝，但它通过了")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误信息应包含 %q，实际: %v", c.want, err)
			}
			// 关键：不许有任何文件落到目标目录（更不许落到目录外）
			entries, _ := os.ReadDir(dst)
			if len(entries) != 0 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("被拒绝的包不该写出任何文件，实际写了: %v", names)
			}
		})
	}
}

// TestExtractDoesNotEscapeTargetDir 端到端确认"目录外没有东西被创建"。
func TestExtractDoesNotEscapeTargetDir(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "inner")
	canary := filepath.Join(base, "CANARY.txt")

	data := rawContainer(t, Manifest{Entries: []Entry{
		rawEntry("../CANARY.txt", []byte("owned"), 0),
	}}, []byte("owned"))

	rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if _, err := rd.Extract(dst); err == nil {
		t.Fatal("越界路径必须被拒绝")
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatal("目录外的文件被创建了 —— 路径穿越防线失效")
	}
}

// TestExtractDetectsLyingSize 清单把 size 说小，实际负载更长：
// 必须被发现，而不是"写一半就当成功"。
func TestExtractDetectsLyingSize(t *testing.T) {
	payload := []byte("0123456789ABCDEF") // 16 字节
	e := rawEntry("a.txt", payload[:10], 0)
	e.SHA256 = func() string { s := sha256.Sum256(payload); return hex.EncodeToString(s[:]) }()
	// 摘要按完整 16 字节算，但声明 size=10、packedSize=16 → 两种不一致都会被抓到
	e.Size = 10
	e.PackedSize = 16

	data := rawContainer(t, Manifest{Entries: []Entry{e}}, payload)
	rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if _, err := rd.Extract(t.TempDir()); err == nil {
		t.Fatal("声明长度与实际内容不符必须被发现")
	}
}

// TestVerifyRejectsPayloadGapOrOverlap 载荷有空洞（条目没有紧密排列）应被拒绝。
func TestVerifyRejectsPayloadGapOrOverlap(t *testing.T) {
	a := []byte("AAAA")
	b := []byte("BBBB")
	e1 := rawEntry("a.txt", a, 0)
	e2 := rawEntry("b.txt", b, 6) // 空洞：应该在 4
	data := rawContainer(t, Manifest{Entries: []Entry{e1, e2}}, append(a, b...))
	rd, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if err := rd.Verify(); err == nil {
		t.Fatal("载荷有空洞应被拒绝")
	}
}

// ---------- 路径规则 ----------

func TestValidatePath(t *testing.T) {
	valid := []string{
		"plugin.json",
		"demo",
		"demo.exe",
		"lib/x.so",
		"a/b/c/d.txt",
		"assets/图片 名字.png",
		"dir/with-dash_and.dot",
	}
	for _, p := range valid {
		if err := ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) 应通过，实际: %v", p, err)
		}
	}

	invalid := map[string]string{
		"":                 "空",
		"../x":             "..",
		"a/../x":           "非法路径段",
		"/abs":             "绝对路径",
		"C:/x":             "冒号",
		`a\b`:              "反斜杠",
		"a//b":             "空路径段",
		"./a":              "非法路径段",
		"a/":               "空路径段",
		"a/./b":            "非法路径段",
		"NUL":              "保留设备名",
		"con.txt":          "保留设备名",
		"a/CON":            "保留设备名",
		"a\x00b":           "NUL",
		"a\nb":             "控制字符",
		strings.Repeat("a", MaxPathLen+1): "过长",
	}
	for p, want := range invalid {
		err := ValidatePath(p)
		if err == nil {
			t.Errorf("ValidatePath(%q) 应被拒绝", p)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidatePath(%q) 错误应含 %q，实际: %v", p, want, err)
		}
	}
}

// TestPackRejectsSymlinks 源目录里的符号链接不该被打进包。
func TestPackRejectsSymlinks(t *testing.T) {
	if testing.Short() {
		t.Skip("需要创建符号链接")
	}
	src := t.TempDir()
	writeFile(t, src, "plugin.json", []byte("{}"))
	target := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(target, []byte("secret"), 0o600)
	if err := os.Symlink(target, filepath.Join(src, "link.txt")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	out := filepath.Join(t.TempDir(), "s.pbclus")
	man, err := PackDir(out, src, PackOptions{Universal: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range man.Entries {
		if e.Path == "link.txt" {
			t.Fatal("符号链接不该被打进包（格式层面不存在这种条目）")
		}
	}
}
