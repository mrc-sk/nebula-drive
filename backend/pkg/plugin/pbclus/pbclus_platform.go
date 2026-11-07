package pbclus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// 平台标签与"这个包能不能装在这台机器上"的判断。
//
// 平台串用 "<goos>/<goarch>"（如 linux/amd64、windows/amd64、darwin/arm64）——
// 与 Go 的 GOOS/GOARCH 一一对应，也与 Docker 的写法一致，不需要额外解释。
// 目录名里不能有斜杠，所以磁盘上的约定用短横线（dist/linux-amd64/）。

// HostPlatform 返回当前运行平台的标签。
func HostPlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// ValidatePlatform 校验平台标签。
//
// 只接受 "<goos>/<goarch>" 两段、小写字母数字下划线数字的形状。
// 不做"合法 GOOS 列表"白名单：那会让格式随 Go 版本更新而失效
// （Go 每次新增一个 GOOS，老解析方就拒绝新包）。
// 形状校验挡住的是注入与拼写错误，那才是这里的职责。
func ValidatePlatform(p string) error {
	if p == "" {
		return errors.New("平台标签为空")
	}
	if p == PlatformAny {
		return nil
	}
	if len(p) > 40 {
		return fmt.Errorf("平台标签过长：%q", p)
	}
	parts := strings.Split(p, "/")
	if len(parts) != 2 {
		return fmt.Errorf("平台标签 %q 形状不对，应为 <goos>/<goarch>（如 linux/amd64）", p)
	}
	for _, seg := range parts {
		if seg == "" {
			return fmt.Errorf("平台标签 %q 含空段", p)
		}
		for _, r := range seg {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
			if !ok {
				return fmt.Errorf("平台标签 %q 含非法字符 %q", p, string(r))
			}
		}
	}
	return nil
}

// DirName 把平台标签转成磁盘目录名（linux/amd64 → linux-amd64）。
func DirName(platform string) string {
	return strings.ReplaceAll(platform, "/", "-")
}

// PlatformFromDirName 把磁盘目录名转回平台标签（linux-amd64 → linux/amd64）。
func PlatformFromDirName(dir string) (string, bool) {
	i := strings.Index(dir, "-")
	if i <= 0 || i == len(dir)-1 {
		return "", false
	}
	p := dir[:i] + "/" + dir[i+1:]
	if err := ValidatePlatform(p); err != nil {
		return "", false
	}
	return p, true
}

// Platforms 返回清单声明的平台列表（已排序、去重）。
//
// 没有声明时返回 ["any"]：v1 包天然与平台无关。
func (rd *Reader) Platforms() []string {
	list := rd.man.Platforms
	if len(list) == 0 {
		return []string{PlatformAny}
	}
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}

// Supports 报告某个平台上**是否存在可用内容**。
//
// 判定依据是声明式清单，所以不需要解包就能回答 ——
// 这一点很重要：安装器要能在"解包到一半才发现入口不存在"之前就拒绝。
func (rd *Reader) Supports(platform string) bool {
	for _, p := range rd.Platforms() {
		if p == PlatformAny || p == platform {
			return true
		}
	}
	return false
}

// RequiresPlatforms 返回"这个包需要的平台"（排除 any）。
func (rd *Reader) RequiresPlatforms() []string {
	var out []string
	for _, p := range rd.Platforms() {
		if p != PlatformAny {
			out = append(out, p)
		}
	}
	return out
}

// PlatformError 构造"平台不匹配"的错误信息。
//
// 单独写一个函数是因为这句话会被三种场景复用（安装/解包/工具检查），
// 而它必须把三件事都讲清楚：包支持什么、当前是什么、以及怎么办。
// 只说"不支持"会让作者去翻格式文档。
func PlatformError(pkgPlatforms []string, host string) error {
	return fmt.Errorf("这个插件包不支持当前平台：包内含 %s，当前是 %s。"+
		"用 pbclus-pack pack --targets %s,%s 重新打包即可做成跨平台包",
		strings.Join(pkgPlatforms, "、"), host, host, otherPlatformHint(host))
}

// otherPlatformHint 给一个"另一个常见平台"作为示例，让提示能直接抄。
func otherPlatformHint(host string) string {
	if strings.HasPrefix(host, "windows/") {
		return "linux/amd64"
	}
	return "windows/amd64"
}

// ExtractForPlatform 解包某个平台所需的文件。
//
// 规则：通用条目（platform 为空）+ 该平台条目会被解出来，其余**跳过**。
// 跳过而不是报错：一个包装了三个平台，在其中一个平台上只用得上其一，
// 这是正常情况而不是异常。
//
// 返回值里的 Skipped 让调用方能如实报告"包里还有别的平台的内容"。
func (rd *Reader) ExtractForPlatform(dst, platform string) (*ExtractReport, error) {
	if platform == "" {
		return nil, errors.New("未指定平台（应为 <goos>/<goarch>）")
	}
	// 先校验清单，再判断平台支持。
	//
	// 顺序有实际影响：清单里的 platforms 若是畸形（比如 "linux-amd64"
	// 少了斜杠），先做 Supports 会得到"不支持当前平台"——
	// 而真实原因是清单写错了。把畸形清单报成平台不匹配，
	// 会让人去改平台标记，越改越远。
	if err := rd.verifyLayout(); err != nil {
		return nil, err
	}
	if !rd.Supports(platform) {
		return nil, PlatformError(rd.RequiresPlatforms(), platform)
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absDst, 0o750); err != nil {
		return nil, err
	}

	rep := &ExtractReport{Platform: platform}
	for _, e := range rd.man.Entries {
		if e.Platform != "" && e.Platform != platform {
			rep.Skipped++
			continue
		}
		if err := extractEntry(rd, e, absDst); err != nil {
			return nil, err
		}
		rep.Files++
		rep.Bytes += e.Size
		rep.Entries = append(rep.Entries, e.Path)
		rep.Verified++
		if rep.Bytes > MaxExtractedBytes {
			return nil, fmt.Errorf("解包总量 %d 超过上限 %d", rep.Bytes, int64(MaxExtractedBytes))
		}
	}
	sort.Strings(rep.Entries)
	return rep, nil
}

// extractEntry 解出一个条目并校验内容摘要。
func extractEntry(rd *Reader, e Entry, absDst string) error {
	// 条目路径用 path（正斜杠）语义转换，避免 Windows 上
	// filepath.Clean 把反斜杠当分隔符而产生第二套解释。
	rel := filepath.FromSlash(e.Path)
	if filepath.IsAbs(rel) {
		return fmt.Errorf("条目 %q 是绝对路径，已拒绝", e.Path)
	}
	target := filepath.Join(absDst, rel)
	if !strings.HasPrefix(target, absDst+string(filepath.Separator)) {
		return fmt.Errorf("条目 %q 解析后越出目标目录，已拒绝", e.Path)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}

	rc, err := rd.EntryReader(e)
	if err != nil {
		return err
	}
	// 0o750：与 zip 安装路径保持一致（插件可执行文件需要 owner 执行位）。
	// PBClus 有意不携带权限位 —— 让"包能决定宿主的文件权限"
	// 这件事根本不存在。
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o750)
	if err != nil {
		rc.Close()
		return err
	}
	h := sha256.New()
	// 多读 1 字节：清单说 size 但实际更长时能发现
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(rc, e.Size+1))
	rc.Close()
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(target)
		return fmt.Errorf("解包条目 %q 失败: %w", e.Path, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if n != e.Size {
		os.Remove(target)
		return fmt.Errorf("条目 %q 实际 %d 字节，清单声明 %d", e.Path, n, e.Size)
	}
	if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		os.Remove(target)
		return fmt.Errorf("条目 %q 内容摘要不匹配（包已损坏）", e.Path)
	}
	return nil
}

// HasPlatformLayout 报告源目录里是否存在 dist/<goos>-<goarch>/ 多平台布局。
func HasPlatformLayout(srcDir string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(srcDir, DistDirName))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, ok := PlatformFromDirName(e.Name()); ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// DistDirName 是多平台二进制在源码目录里的约定位置。
const DistDirName = "dist"

// ExecutableKind 是按文件头猜出的可执行文件类型。
type ExecutableKind int

const (
	// ExecUnknown 认不出来（脚本、数据文件等，按"通用"处理）。
	ExecUnknown ExecutableKind = iota
	// ExecPE Windows PE。
	ExecPE
	// ExecELF Linux/BSD ELF。
	ExecELF
	// ExecMachO macOS Mach-O。
	ExecMachO
)

// KindOfPlatform 把文件头类型映射到平台标签。
//
// 注意只映射到 **goos**，架构认不出来就不认 —— 宁可当成通用文件
// 让作者显式指定，也不要猜错架构装一个跑不起来的二进制。
func KindOfPlatform(k ExecutableKind) string {
	switch k {
	case ExecPE:
		return "windows"
	case ExecELF:
		return "linux"
	case ExecMachO:
		return "darwin"
	default:
		return ""
	}
}

// SniffExecutable 依据文件头判断可执行文件类型。
//
// 存在的意义是让 pack 能**如实标注**单平台包：只带 Windows 二进制的包
// 若标成"通用"，装到 Linux 上会以"入口文件不存在"失败 ——
// 一个与真实原因相距很远的错误信息。
func SniffExecutable(path string) ExecutableKind {
	f, err := os.Open(path)
	if err != nil {
		return ExecUnknown
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil {
		return ExecUnknown
	}
	switch {
	case bytes.Equal(head, []byte{'M', 'Z', 0x90, 0x00}) || (head[0] == 'M' && head[1] == 'Z'):
		return ExecPE
	case bytes.Equal(head, []byte{0x7f, 'E', 'L', 'F'}):
		return ExecELF
	case isMachO(head):
		return ExecMachO
	}
	return ExecUnknown
}

// isMachO 识别 Mach-O（含 32/64 位与大小端变体）。
func isMachO(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	switch v {
	case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE, 0xCAFEBABE:
		return true
	}
	return false
}
