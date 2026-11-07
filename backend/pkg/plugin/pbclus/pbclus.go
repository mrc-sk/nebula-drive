// Package pbclus 实现 NebulaDrive 的插件包容器格式 **PBClus**。
//
// # 为什么需要一个自己的格式，而不是 zip 改后缀
//
// 改后缀只是换个名字，格式仍然是 zip：解析规则、攻击面、允许的结构全都照旧。
// 文件名叫 .pbclus 但内容是 zip，等于既没得到新格式，又让"看到 .pbclus
// 就知道怎么处理"这件事变得不可靠。所以这里定义的是一个真正的容器：
//
//   - **格式层规定路径**：条目路径必须是规范化、相对、纯正斜杠的，
//     `../`、绝对路径、`a//b`、`./a`、反斜杠在**打包时**就被拒绝，
//     解包时再校验一次。zip 把这件事留给解压方，每个解压方各防一遍。
//   - **只有普通文件与目录**：没有符号链接、设备文件、FIFO 这些条目类型，
//     直接消灭一整类攻击（zip 的 symlink 条目能让解压结果指向宿主任意文件）。
//   - **拒绝重复路径**：zip 允许同名条目，解压结果取决于实现顺序。
//   - **可整体校验**：头部带清单/载荷摘要，尾部带整文件摘要与总长，
//     每个条目还有自己的 SHA-256。损坏或截断能被精确定位到某个文件。
//   - **能明确说"这不是 PBClus"**：靠魔术字判断，不靠扩展名。
//
// # 文件布局
//
//	┌────────────┬──────────────┬───────────┬──────────┐
//	│ Header 128 │ Manifest(JSON)│ Payload   │ Footer 48│
//	└────────────┴──────────────┴───────────┴──────────┘
//
// 全部整数小端。Payload 是各条目压缩后字节的**顺序拼接**，
// 每个条目在清单里记录自己的 offset / packedSize。
//
// # 完整性 ≠ 真实性（必须说清楚）
//
// 这里的 CRC 与 SHA-256 只保证"内容没被意外损坏/截断"，
// **不提供任何防伪能力**：任何人都能重新打包并算出正确的摘要。
// v1 没有签名，装插件时请把包当成不可信输入 —— 这不是缺陷，
// 而是不要在文档里制造"校验通过 = 安全"的错觉。
// 头部的 flags 与保留位留给将来的签名扩展。
package pbclus

import (
	"bytes"
	"compress/flate"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 格式常量。
const (
	// Extension 是插件包的标准扩展名。
	Extension = ".pbclus"
	// FormatName 写入清单，供解析方确认自己在读什么。
	FormatName = "PBClus"

	// Version 是**写出**时使用的格式版本。
	//
	// v2 相对 v1 增加了两样东西：条目平台标签（一个包带多平台二进制）
	// 与可选签名块。
	//
	// 为什么必须升版本而不是"加个可选字段"：v1 的解析方不认识平台字段，
	// 会把所有平台的二进制都解到同一个路径上，最后写进去的那个胜出 ——
	// 于是**静默装上一个架构不对的二进制**，表现为"插件启用后立刻崩"。
	// 升版本让老解析方直接拒绝，而不是做错事。
	// 这正是版本字段存在的意义。
	Version = 2

	// MinReadVersion 是能**读取**的最低版本。
	//
	// v1 包继续支持：它的条目天然都是通用文件（无平台标签、无签名），
	// v2 的读法能完整覆盖 v1 的语义，所以不需要保留两套解析逻辑。
	// 下限设 1 而不是"忽略版本"，是因为低于 1 的号没有定义过。
	MinReadVersion = 1

	// HeaderSize 头部固定长度。
	HeaderSize = 128
	// FooterSize 尾部固定长度。
	FooterSize = 48

	// SigBlockSize 签名块固定长度。
	//
	// 定长是刻意的：Ed25519 的签名恒为 64 字节、公钥恒为 32 字节，
	// 所以"有没有签名"只需要一个 flags 位就能判断，
	// 不必在头部再开一个长度字段（那会让头部布局为签名而改动）。
	SigBlockSize = 108

	// MaxPathLen 单个条目路径长度上限。
	MaxPathLen = 512
	// MaxEntries 条目数上限。
	MaxEntries = 2000
	// MaxFileBytes 单条目解压后大小上限（与 zip 安装路径一致）。
	MaxFileBytes = 64 << 20
	// MaxExtractedBytes 解压后总大小上限（与 zip 安装路径一致）。
	MaxExtractedBytes = 256 << 20
	// MaxPackageBytes 容器文件本身大小上限。
	//
	// 与安装器的 MaxZipBytes/MaxPackageBytes 保持一致（都是 64MB）：
	// 格式层与安装层的上限不同会让人反复确认"到底哪个说了算"。
	// 对插件包来说 64MB 很宽裕 —— Go 编译出的插件二进制通常只有几 MB。
	MaxPackageBytes = 64 << 20
	// MaxTargets 一个包最多带几个平台。
	MaxTargets = 8
)

// PlatformAny 表示"与平台无关"，可以装在任意平台上。
//
// 空字符串也按此处理（v1 包的条目就是空的）——但清单里的 platforms
// 字段必须显式写 "any"，因为"没写"与"写了 any"在语义上要说清楚。
const PlatformAny = "any"

// Magic 是容器魔术字。
//
// 第 7、8 字节用 0x1A（DOS 的 EOF 字符）与 0x0A：这是从 PNG 学来的老办法 ——
// 某些工具用文本模式处理文件时会在 EOF 处截断，或在传输时改写换行，
// 带这两个字节能让"文件被文本模式糟蹋过"立刻暴露，而不是解出一堆乱码。
var Magic = [8]byte{'P', 'B', 'C', 'l', 'u', 's', 0x1A, 0x0A}

// 条目压缩方式。
const (
	// CodecRaw 原样存储。
	CodecRaw = "raw"
	// CodecDeflate 原始 DEFLATE 流（无 zlib/gzip 外层头）。
	CodecDeflate = "deflate"
)

// 头部 flags 位。v1 只用 bit0。
const (
	// FlagDeflate 表示容器内至少有一个条目用了 DEFLATE。
	FlagDeflate uint32 = 1 << 0
	// FlagSigned 表示文件带签名块（紧随载荷之后、尾部之前）。
	FlagSigned uint32 = 1 << 1
)

// Kind 是包类型识别结果。
type Kind int

const (
	// KindUnknown 既不是 PBClus 也不是 zip。
	KindUnknown Kind = iota
	// KindPBClus 是 PBClus 容器。
	KindPBClus
	// KindZip 是 zip（无论扩展名写的是什么）。
	KindZip
)

func (k Kind) String() string {
	switch k {
	case KindPBClus:
		return "PBClus"
	case KindZip:
		return "zip"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// 头部
// ---------------------------------------------------------------------------

// header 是容器头部的内存表示。
type header struct {
	// FormatVersion 从头部读到的格式版本（1 或 2）。
	FormatVersion uint16
	Flags         uint32
	EntryCount    uint32
	ManifestOff   uint64
	ManifestLen   uint64
	PayloadOff    uint64
	PayloadLen    uint64
	ManifestSHA   [32]byte
	PayloadSHA    [32]byte
}

// marshal 序列化为 128 字节。headerCRC 覆盖前 116 字节，
// 之后 8 字节保留（当前全 0）—— 保留区不参与 CRC，
// 这样将来在其中追加字段不会破坏老解析方。
func (h header) marshal() []byte {
	b := make([]byte, HeaderSize)
	copy(b[0:8], Magic[:])
	// 写字段而不是包级常量：写常量会让"设置 FormatVersion 却不生效"
	// 变成一个静默的坑（测试里构造旧版本容器时就会踩到）。
	v := h.FormatVersion
	if v == 0 {
		v = Version
	}
	binary.LittleEndian.PutUint16(b[8:], v)
	binary.LittleEndian.PutUint16(b[10:], HeaderSize)
	binary.LittleEndian.PutUint32(b[12:], h.Flags)
	binary.LittleEndian.PutUint32(b[16:], h.EntryCount)
	binary.LittleEndian.PutUint64(b[20:], h.ManifestOff)
	binary.LittleEndian.PutUint64(b[28:], h.ManifestLen)
	binary.LittleEndian.PutUint64(b[36:], h.PayloadOff)
	binary.LittleEndian.PutUint64(b[44:], h.PayloadLen)
	copy(b[52:84], h.ManifestSHA[:])
	copy(b[84:116], h.PayloadSHA[:])
	binary.LittleEndian.PutUint32(b[116:], crc32.ChecksumIEEE(b[:116]))
	return b
}

func parseHeader(b []byte) (header, error) {
	var h header
	if len(b) < HeaderSize {
		return h, errors.New("文件太小，连头部都不完整")
	}
	if !bytes.Equal(b[0:8], Magic[:]) {
		return h, fmt.Errorf("魔术字不匹配：这不是 %s 容器（前 8 字节 % x）", FormatName, b[0:8])
	}
	if v := binary.LittleEndian.Uint16(b[8:]); v < MinReadVersion || v > Version {
		return h, fmt.Errorf("容器版本 %d 不受支持（本程序支持 %d~%d；"+
			"更高版本可能带了本程序不认识的平台标签或签名，硬读会装错二进制）",
			v, MinReadVersion, Version)
	}
	if hs := binary.LittleEndian.Uint16(b[10:]); hs != HeaderSize {
		return h, fmt.Errorf("头部长度 %d 异常，应为 %d", hs, HeaderSize)
	}
	// CRC 先于其它字段信任：它保证"头部没被改坏"，
	// 之后再用头里的偏移量去读别的区域才有意义。
	want := binary.LittleEndian.Uint32(b[116:120])
	if got := crc32.ChecksumIEEE(b[:116]); got != want {
		return h, fmt.Errorf("头部校验失败（CRC32 不匹配）：文件已损坏")
	}
	h.FormatVersion = binary.LittleEndian.Uint16(b[8:])
	h.Flags = binary.LittleEndian.Uint32(b[12:])
	h.EntryCount = binary.LittleEndian.Uint32(b[16:])
	h.ManifestOff = binary.LittleEndian.Uint64(b[20:])
	h.ManifestLen = binary.LittleEndian.Uint64(b[28:])
	h.PayloadOff = binary.LittleEndian.Uint64(b[36:])
	h.PayloadLen = binary.LittleEndian.Uint64(b[44:])
	copy(h.ManifestSHA[:], b[52:84])
	copy(h.PayloadSHA[:], b[84:116])
	return h, nil
}

// footer 序列化为 48 字节：fileSHA(32) + Magic(8) + totalLen(8)。
func marshalFooter(fileSHA [32]byte, totalLen uint64) []byte {
	b := make([]byte, FooterSize)
	copy(b[0:32], fileSHA[:])
	copy(b[32:40], Magic[:])
	binary.LittleEndian.PutUint64(b[40:], totalLen)
	return b
}

// ---------------------------------------------------------------------------
// 清单
// ---------------------------------------------------------------------------

// Entry 是清单里的一个文件条目。
type Entry struct {
	// Path 容器内相对路径，纯正斜杠。
	Path string `json:"path"`
	// Platform 标明该条目属于哪个平台，格式 "<goos>/<goarch>"。
	//
	// 空字符串表示**通用**（任何平台都要解出来：plugin.json、脚本、素材）。
	// 同一条路径可以有多份不同平台的条目 —— 这是"一个包装多平台"的实现方式，
	// 解包时只取当前平台那份。为此唯一性检查是 (path, platform) 而不是 path。
	Platform string `json:"platform,omitempty"`
	// Size 解压后字节数。
	Size int64 `json:"size"`
	// Offset 相对载荷起点的偏移。
	Offset int64 `json:"offset"`
	// PackedSize 容器内实际占用字节数（压缩后大小；raw 时等于 Size）。
	PackedSize int64 `json:"packedSize"`
	// Codec 取值 CodecRaw 或 CodecDeflate。
	Codec string `json:"codec"`
	// SHA256 是解压后内容的十六进制摘要。
	SHA256 string `json:"sha256"`
}

// PluginInfo 是打包时从 plugin.json 提取的摘要，便于不开包就能看到关键信息。
//
// ⚠ 这些字段**只用于展示**。安装时的权威来源始终是包内的 plugin.json ——
// 清单可以被重新打包的人随便填，就像商店条目的元数据一样不可信。
type PluginInfo struct {
	Name            string   `json:"name"`
	Title           string   `json:"title,omitempty"`
	Version         string   `json:"version,omitempty"`
	Author          string   `json:"author,omitempty"`
	Description     string   `json:"description,omitempty"`
	Entry           string   `json:"entry,omitempty"`
	Hooks           []string `json:"hooks,omitempty"`
	ProtocolVersion int      `json:"protocolVersion,omitempty"`
}

// SignatureBlock 是签名块的内存表示。
//
// 公钥**内嵌在包里**：这样任何拿到包的人都能验证"内容与签名一致"，
// 也能看到"是谁签的"。但内嵌公钥**本身不提供任何信任** ——
// 攻击者当然可以用自己的密钥签一个包，再把公钥一起放进去，
// 签名照样"验证通过"。
//
// 所以安全语义只来自宿主侧的**信任库**：只有 keyId 在信任库里的包才算可信。
// 内嵌公钥的作用是让信任得以建立（首次信任时管理员能看到并比对 keyId），
// 以及让"这个包是谁签的"可以被审计。
type SignatureBlock struct {
	// Alg 恒为 SigAlgEd25519（当前只定义了这一种）。
	Alg uint8
	// KeyID 是公钥 SHA-256 的前 8 字节，十六进制（16 字符）。
	KeyID string
	// PublicKey 是 32 字节 Ed25519 公钥。
	PublicKey [32]byte
	// Signature 是 64 字节 Ed25519 签名。
	Signature [64]byte
}

// Manifest 是容器清单。
type Manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// Platforms 声明这个包支持哪些平台。
	//
	//   - ["any"] 表示与平台无关（v1 包与纯脚本插件属于此类）
	//   - 否则是 "<goos>/<goarch>" 列表，如 ["linux/amd64","windows/amd64"]
	//
	// 与条目级 platform 的关系：条目级是"这个文件给谁"，
	// 这里是"整个包面向谁"。声明式的好处是**不下载、不解包也能判断兼容性**，
	// 安装器可以据此在解包前就给出"这个包不支持当前平台"的明确错误，
	// 而不是解到一半发现入口文件不存在。
	Platforms  []string    `json:"platforms,omitempty"`
	CreatedAt  string      `json:"createdAt"`
	Tool       string      `json:"tool"`
	Signed     bool        `json:"signed,omitempty"`
	Plugin     *PluginInfo `json:"plugin,omitempty"`
	EntryCount int         `json:"entryCount"`
	TotalSize  int64       `json:"totalSize"`
	Entries    []Entry     `json:"entries"`
}

// ---------------------------------------------------------------------------
// 路径校验（格式规则）
// ---------------------------------------------------------------------------

// windowsReserved 是 Windows 保留设备名集合。
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// ValidatePath 校验条目路径是否符合格式规则。
//
// 规则（打包与解包两侧都调用同一个函数，避免两套规则漂移）：
//
//  1. 非空，长度 ≤ MaxPathLen；
//  2. 只允许正斜杠（反斜杠一律拒绝 —— 否则 Windows 上会被解释成分隔符，
//     让"路径合法"这件事变成平台相关）；
//  3. 相对路径：不以 / 开头，不含盘符（`:`），不含 NUL 或控制字符；
//  4. 逐个路径段检查：`.` 与 `..` 段直接点出来（安全相关的问题就该说得直白），
//     空段（`a//b`、结尾 `/`）拒绝；
//  5. 每个路径段都不是 Windows 保留设备名；
//  6. 最后用 path.Clean 兜底：规范化后必须等于原串。
//
// 检查顺序是刻意的：把 `a/../x` 报成"含 .. 路径段"比报成
// "应写成 x" 有用得多 —— 后者等于把一次目录穿越轻描淡写成格式问题。
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("条目路径为空")
	}
	if len(p) > MaxPathLen {
		return fmt.Errorf("条目路径过长（%d > %d 字节）", len(p), MaxPathLen)
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("条目路径含 NUL 字符")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("条目路径含控制字符 %q", string(r))
		}
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("条目路径含反斜杠 %q：PBClus 只允许正斜杠", p)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("条目路径不能是绝对路径：%q", p)
	}
	if strings.Contains(p, ":") {
		return fmt.Errorf("条目路径含冒号 %q：不允许盘符或 NTFS 数据流", p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			return fmt.Errorf("条目路径含空路径段（连续斜杠或结尾斜杠）：%q", p)
		case ".":
			return fmt.Errorf("条目路径含非法路径段 %q：%s", ".", p)
		case "..":
			return fmt.Errorf("条目路径含非法路径段 %q（不允许向上跳）：%s", "..", p)
		}
		if err := checkReservedSegment(seg); err != nil {
			return err
		}
	}
	if clean := path.Clean(p); clean != p {
		return fmt.Errorf("条目路径未规范化：%q 应写成 %q", p, clean)
	}
	return nil
}

// checkReservedSegment 拒绝 Windows 保留设备名。
//
// 取第一个点之前的部分比较：`NUL.txt` 在 Windows 上同样是设备，
// 只比整个段名会漏掉这种写法。
func checkReservedSegment(seg string) error {
	base := seg
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	if windowsReserved[strings.ToUpper(base)] {
		return fmt.Errorf("条目路径含 Windows 保留设备名 %q，装到 Windows 上会出问题", seg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 打包
// ---------------------------------------------------------------------------

// PackOptions 控制打包行为。
type PackOptions struct {
	// Store 为 true 时不做压缩（所有条目 codec=raw）。
	Store bool
	// Level 是 DEFLATE 压缩级别（flate.BestSpeed..BestCompression）。
	// 0 表示默认级别。
	Level int
	// Tool 写入清单的打包工具标识。
	Tool string
	// Now 用于测试固定 createdAt；零值取当前时间。
	Now time.Time
	// Exclude 返回 true 表示跳过该相对路径（目录也适用）。
	Exclude func(rel string) bool
	// Plugin 为 nil 时不写入 plugin 摘要字段。
	Plugin *PluginInfo

	// Platforms 非空时进入**多平台模式**：源目录按
	// `dist/<goos>-<goarch>/**` 布局提供各平台的文件，
	// 其余文件（除 dist/）作为通用条目。
	//
	// 空值时进入单平台模式，见 Universal。
	Platforms []string
	// Universal 在单平台模式下为 true 时，清单声明 platforms=["any"]。
	//
	// 单平台模式下 Universal 为 false 时必须给 Platform：
	// 那样清单会声明这一个平台，条目也都标上它。
	// 这个区分是刻意的 —— 一个带原生二进制的包如果标成"通用"，
	// 装到别的平台上会以"入口文件不存在"失败，而真实原因是平台不匹配。
	Universal bool
	// Platform 是单平台模式下的平台标签。
	Platform string

	// SourcePlatforms 覆盖多平台模式下"从 dist/ 实际发现的平台"。
	// 仅供调用方在先把二进制构建到别处、再补进 dist/ 时使用。
	SourcePlatforms map[string]string // 平台 -> 该平台的源目录（绝对路径）

	// SignKey 非 nil 时对包签名。
	SignKey *SigningKey
}

// packItem 是待打包的一个文件。
//
// 提到包级而不是函数内局部类型：pathsOf 需要接收它，
// 而 Go 的切片赋值是"元素类型必须完全相同"，局部命名类型传不出去。
type packItem struct {
	rel      string
	abs      string
	size     int64
	platform string
}

// PackDir 把目录打包成 PBClus 容器写入 outPath。
//
// 采用"两段写"：先把每个条目压缩进临时 blob 并记录偏移/摘要，
// 再依次写出 头部 → 清单 → 载荷 →（签名块）→ 尾部。这样打包是流式的，
// 不需要把整个包读进内存，也不需要回填（seek）任何字段 ——
// 头部需要的所有信息在写它之前都已经确定。
func PackDir(outPath, srcDir string, opt PackOptions) (*Manifest, error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	if opt.Tool == "" {
		opt.Tool = "pbclus"
	}

	// ---- 1. 收集条目并确定路径与平台 ----
	items, platforms, err := collectPackItems(srcDir, opt)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("目录里没有可打包的文件")
	}
	if len(items) > MaxEntries {
		return nil, fmt.Errorf("条目数 %d 超过上限 %d", len(items), MaxEntries)
	}

	// 排序：让同样的输入产生同样的字节布局（便于比对与复现）。
	// 平台参与排序键，这样多平台包里同一路径的多个版本会相邻，
	// 出问题时一眼能看出"这个文件有哪几个平台"。
	sort.Slice(items, func(i, j int) bool {
		if items[i].rel != items[j].rel {
			return items[i].rel < items[j].rel
		}
		return items[i].platform < items[j].platform
	})

	// 去重按 (路径, 平台)：多平台包同路径多版本是正常的，
	// 同平台同路径出现两次才是打包器自己的 bug。
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		key := it.platform + "\x00" + it.rel
		if seen[key] {
			return nil, fmt.Errorf("条目重复: %q（平台 %q）", it.rel, it.platform)
		}
		seen[key] = true
	}
	if err := checkPrefixConflicts(pathsOf(items)); err != nil {
		return nil, err
	}

	// ---- 2. 压缩到临时 blob，记录偏移与摘要 ----
	blob, err := os.CreateTemp("", "pbclus-payload-*")
	if err != nil {
		return nil, err
	}
	blobPath := blob.Name()
	defer func() {
		blob.Close()
		os.Remove(blobPath)
	}()

	payloadHash := sha256.New()
	var (
		entries []Entry
		offset  int64
		total   int64
		usedDef bool
		level   = opt.Level
	)
	if level == 0 {
		level = flate.DefaultCompression
	}

	for _, it := range items {
		e, err := writeEntryBlob(blob, payloadHash, it.abs, it.rel, it.size, offset, opt.Store, level)
		if err != nil {
			return nil, err
		}
		e.Platform = it.platform
		if e.Codec == CodecDeflate {
			usedDef = true
		}
		entries = append(entries, e)
		offset += e.PackedSize
		total += e.Size
		if total > MaxExtractedBytes {
			return nil, fmt.Errorf("内容总大小 %d 超过上限 %d", total, int64(MaxExtractedBytes))
		}
	}
	var payloadSHA [32]byte
	copy(payloadSHA[:], payloadHash.Sum(nil))

	// ---- 3. 组清单 ----
	man := &Manifest{
		Format:     FormatName,
		Version:    Version,
		Platforms:  platforms,
		CreatedAt:  opt.Now.UTC().Format(time.RFC3339),
		Tool:       opt.Tool,
		Signed:     opt.SignKey != nil,
		Plugin:     opt.Plugin,
		EntryCount: len(entries),
		TotalSize:  total,
		Entries:    entries,
	}
	manBytes, err := json.Marshal(man)
	if err != nil {
		return nil, err
	}
	if len(manBytes) > MaxPackageBytes {
		return nil, errors.New("清单异常大")
	}
	var manifestSHA [32]byte
	copy(manifestSHA[:], sha256Sum(manBytes))

	// ---- 4. 顺序写出：头部 → 清单 → 载荷 →（签名块）→ 尾部 ----
	h := header{
		FormatVersion: Version,
		EntryCount:    uint32(len(entries)),
		ManifestOff:   HeaderSize,
		ManifestLen:   uint64(len(manBytes)),
		PayloadOff:    uint64(HeaderSize + len(manBytes)),
		PayloadLen:    uint64(offset),
		ManifestSHA:   manifestSHA,
		PayloadSHA:    payloadSHA,
	}
	if usedDef {
		h.Flags |= FlagDeflate
	}
	if opt.SignKey != nil {
		h.Flags |= FlagSigned
	}

	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return nil, err
	}
	defer out.Close()

	// 边写边算整文件摘要：fileSHA 覆盖 头部+清单+载荷，
	// 不包含签名块与尾部（签名块含签名本身，算进去就自指了）。
	fileHash := sha256.New()
	mw := io.MultiWriter(out, fileHash)

	headerBytes := h.marshal()
	if _, err := mw.Write(headerBytes); err != nil {
		return nil, err
	}
	if _, err := mw.Write(manBytes); err != nil {
		return nil, err
	}
	if _, err := blob.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := io.Copy(mw, blob); err != nil {
		return nil, err
	}
	var fileSHA [32]byte
	copy(fileSHA[:], fileHash.Sum(nil))

	// 签名块写在载荷之后、尾部之前。它不在 fileSHA 覆盖范围内，
	// 所以"先算摘要再签名"不会形成循环依赖。
	extra := uint64(0)
	if opt.SignKey != nil {
		priv, err := opt.SignKey.ParsePrivateKey()
		if err != nil {
			return nil, err
		}
		pub := priv.Public().(ed25519.PublicKey)
		block := &SignatureBlock{
			Alg:       SigAlgEd25519,
			KeyID:     KeyID(pub),
			PublicKey: [32]byte(pub),
			Signature: [64]byte(ed25519.Sign(priv, signedMessage(fileSHA[:]))),
		}
		if _, err := out.Write(marshalSignatureBlock(block)); err != nil {
			return nil, err
		}
		extra = SigBlockSize
	}

	totalLen := uint64(HeaderSize+len(manBytes)) + uint64(offset) + extra + FooterSize
	if _, err := out.Write(marshalFooter(fileSHA, totalLen)); err != nil {
		return nil, err
	}
	if err := out.Sync(); err != nil {
		return nil, err
	}
	return man, nil
}

// collectPackItems 按模式收集待打包文件，并算出清单的 platforms。
func collectPackItems(srcDir string, opt PackOptions) ([]packItem, []string, error) {
	multi := len(opt.Platforms) > 0

	// 多平台模式下 dist/ 是"各平台专属文件"的约定位置，
	// 它自己不该作为通用目录被打进去。
	// 单平台模式下若存在 dist/<平台>/ 布局，直接报错而不是静默忽略 ——
	// 静默忽略会打出一个"只含 plugin.json 的包"，装上去必然失败。
	if !multi {
		if has, _ := HasPlatformLayout(srcDir); has {
			return nil, nil, fmt.Errorf("源目录里有 %s/<平台>/ 布局，说明这是跨平台包："+
				"请用 --targets 指定平台列表，否则会漏掉各平台的文件", DistDirName)
		}
	}

	var items []packItem

	if multi {
		// 校验声明的平台，并确认每个平台都有目录
		declared := make([]string, 0, len(opt.Platforms))
		seenP := map[string]bool{}
		for _, p := range opt.Platforms {
			if err := ValidatePlatform(p); err != nil {
				return nil, nil, err
			}
			if p == PlatformAny {
				return nil, nil, errors.New("多平台模式不接受 \"any\" 平台标签")
			}
			if seenP[p] {
				continue
			}
			seenP[p] = true
			declared = append(declared, p)
		}
		if len(declared) > MaxTargets {
			return nil, nil, fmt.Errorf("平台数 %d 超过上限 %d", len(declared), MaxTargets)
		}
		sort.Strings(declared)

		// dist/<平台>/ 下的文件 → 该平台专属
		for _, p := range declared {
			dir := ""
			if opt.SourcePlatforms != nil {
				dir = opt.SourcePlatforms[p]
			}
			if dir == "" {
				dir = filepath.Join(srcDir, DistDirName, DirName(p))
			}
			st, err := os.Stat(dir)
			if err != nil || !st.IsDir() {
				return nil, nil, fmt.Errorf("平台 %s 缺少文件目录：期望 %s（用 --build 交叉编译，"+
					"或把预编译产物放到该目录）", p, dir)
			}
			n, err := walkInto(dir, func(rel, abs string, info os.FileInfo) error {
				items = append(items, packItem{rel: rel, abs: abs, size: info.Size(), platform: p})
				return nil
			}, nil)
			if err != nil {
				return nil, nil, err
			}
			if n == 0 {
				return nil, nil, fmt.Errorf("平台 %s 的目录 %s 是空的", p, dir)
			}
		}

		// srcDir 下除 dist/ 之外的文件 → 通用
		_, err := walkInto(srcDir, func(rel, abs string, info os.FileInfo) error {
			items = append(items, packItem{rel: rel, abs: abs, size: info.Size(), platform: ""})
			return nil
		}, opt.Exclude, DistDirName)
		if err != nil {
			return nil, nil, err
		}
		return items, declared, nil
	}

	// 单平台模式
	platform := ""
	platforms := []string{PlatformAny}
	if !opt.Universal {
		if opt.Platform == "" {
			return nil, nil, errors.New("单平台模式需要指定平台（或显式声明为通用包）")
		}
		if err := ValidatePlatform(opt.Platform); err != nil {
			return nil, nil, err
		}
		if opt.Platform == PlatformAny {
			return nil, nil, errors.New("Platform 不能是 \"any\"，通用包请用 Universal=true")
		}
		platform = opt.Platform
		platforms = []string{opt.Platform}
	}
	_, err := walkInto(srcDir, func(rel, abs string, info os.FileInfo) error {
		items = append(items, packItem{rel: rel, abs: abs, size: info.Size(), platform: platform})
		return nil
	}, opt.Exclude, DistDirName)
	if err != nil {
		return nil, nil, err
	}
	return items, platforms, nil
}

// walkInto 遍历目录，对每个普通文件回调。
//
// 跳过符号链接与非普通文件：格式层面就不存在这类条目，
// 让解包方去处理是错的分工。skipTop 里的顶层目录名整棵跳过。
func walkInto(dir string, fn func(rel, abs string, info os.FileInfo) error, exclude func(string) bool, skipTop ...string) (int, error) {
	skip := map[string]bool{}
	for _, s := range skipTop {
		skip[s] = true
	}
	n := 0
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		top := rel
		if i := strings.IndexByte(top, '/'); i >= 0 {
			top = top[:i]
		}
		if skip[top] {
			if info.IsDir() && rel == top {
				return filepath.SkipDir
			}
			return nil
		}
		if exclude != nil && exclude(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := ValidatePath(rel); err != nil {
			return fmt.Errorf("源文件 %q 不满足格式要求: %w", rel, err)
		}
		if info.Size() > MaxFileBytes {
			return fmt.Errorf("文件 %q 大小 %d 超过单文件上限 %d", rel, info.Size(), int64(MaxFileBytes))
		}
		if err := fn(rel, p, info); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// pathsOf 取出待打包条目的相对路径。
func pathsOf(items []packItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.rel)
	}
	return out
}

// checkPrefixConflicts 拒绝"一个路径既是文件又是另一个路径的目录"。
//
// 例如同时存在 `lib` 与 `lib/x.so`：解包时先写 lib 文件就无法建 lib 目录。
// zip 允许这种包，结果取决于解压顺序 —— 这种不确定性不该带进新格式。
func checkPrefixConflicts(paths []string) error {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if strings.HasPrefix(cur, prev+"/") {
			return fmt.Errorf("条目路径冲突：%q 与 %q 无法同时作为文件存在", prev, cur)
		}
	}
	return nil
}

// writeEntryBlob 把一个文件压缩写进 blob，返回其清单条目。
//
// 压缩后反而更大时存原始字节：打包一个已压缩过的二进制（.exe/.so）
// 时，DEFLATE 经常略微变大，硬压会让包更大还更慢。
func writeEntryBlob(blob *os.File, payloadHash hash.Hash, abs, rel string, size, offset int64, store bool, level int) (Entry, error) {
	src, err := os.Open(abs)
	if err != nil {
		return Entry{}, err
	}
	defer src.Close()

	contentHash := sha256.New()
	// 压缩器直接写进 blob；同时用 TeeReader 把落盘的字节喂给载荷摘要，
	// 保证 payloadSHA 与文件里真实字节一致。
	bw := io.MultiWriter(blob, payloadHash)
	var packed int64

	if store {
		n, err := io.Copy(bw, io.TeeReader(src, contentHash))
		if err != nil {
			return Entry{}, fmt.Errorf("写入条目 %q 失败: %w", rel, err)
		}
		packed = n
	} else {
		// 先压进内存缓冲看效果，再决定用不用它。
		// 单文件上限 64MB，最坏情况内存里同时留一份压缩结果 —— 可接受，
		// 换来的是"压缩没用就不压"这个明确的取舍。
		var buf bytes.Buffer
		fw, err := flate.NewWriter(&buf, level)
		if err != nil {
			return Entry{}, err
		}
		if _, err := io.Copy(fw, io.TeeReader(src, contentHash)); err != nil {
			return Entry{}, fmt.Errorf("压缩条目 %q 失败: %w", rel, err)
		}
		if err := fw.Close(); err != nil {
			return Entry{}, err
		}
		if buf.Len() < int(size) {
			n, err := bw.Write(buf.Bytes())
			if err != nil {
				return Entry{}, err
			}
			packed = int64(n)
		} else {
			// 压缩没收益 → 回退成原样存
			if _, err := src.Seek(0, io.SeekStart); err != nil {
				return Entry{}, err
			}
			contentHash.Reset()
			n, err := io.Copy(bw, io.TeeReader(src, contentHash))
			if err != nil {
				return Entry{}, fmt.Errorf("写入条目 %q 失败: %w", rel, err)
			}
			packed = n
			store = true
		}
	}

	codec := CodecDeflate
	if store {
		codec = CodecRaw
	}
	if contentHash.Size() == 0 { // 理论上不会发生，留个明确断言
		return Entry{}, errors.New("内部错误：内容摘要不可用")
	}
	return Entry{
		Path:       rel,
		Size:       size,
		Offset:     offset,
		PackedSize: packed,
		Codec:      codec,
		SHA256:     hex.EncodeToString(contentHash.Sum(nil)),
	}, nil
}

// ---------------------------------------------------------------------------
// 读取
// ---------------------------------------------------------------------------

// Reader 是一个已打开的 PBClus 容器。
//
// 它同时保存 ra（可随机读的数据源）与 f（当数据源是文件时用于关闭）。
// 分成两个字段而不是接口断言：NewReader 允许传入内存数据（测试用），
// 那种情况下没有可关闭的东西，"要不要关"必须显式表达。
type Reader struct {
	ra    io.ReaderAt
	f     *os.File
	size  int64
	h     header
	man   *Manifest
	owned bool
	// sig 非 nil 表示容器带签名块（已解析，但**未验证**）。
	sig *SignatureBlock
	// contentEnd 是"头部+清单+载荷"的结束偏移。
	// 它同时是整文件摘要与签名覆盖范围的右边界 ——
	// 签名块自己不在被签内容里（否则自指）。
	contentEnd int64
}

// Open 打开容器文件。
//
// 只做**结构性**检查（魔术字、版本、头部 CRC、清单摘要、尾部魔术字与总长），
// 不遍历载荷 —— 那件事交给 Verify 或 Extract。这样打开一个 300MB 的包
// 也不会先卡一遍全文件读。
func Open(p string) (*Reader, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	rd, err := NewReader(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	rd.f = f
	rd.owned = true
	return rd, nil
}

// NewReader 从任意可随机读的对象构造 Reader（便于测试与内存数据）。
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	// 先验魔术字、再查长度。顺序很重要：一个 144 字节的 zip 改成 .pbclus 时，
	// 应该告诉用户"这是 zip，不是 PBClus 容器"，而不是含糊的"文件太小"——
	// 后者会让人以为是文件损坏，然后去反复重新下载。
	probe := make([]byte, len(Magic))
	n, err := r.ReadAt(probe, 0)
	if n < len(Magic) || !bytes.Equal(probe[:len(Magic)], Magic[:]) {
		if k := SniffBytes(probe[:max(n, 0)]); k == KindZip {
			return nil, fmt.Errorf("这是 zip 文件，不是 %s 容器（把后缀改成 .pbclus 不会改变它的格式，"+
				"请用 pbclus-pack 重新打包）", FormatName)
		}
		return nil, fmt.Errorf("魔术字不匹配：这不是 %s 容器（前 %d 字节 % x）", FormatName, n, probe[:max(n, 0)])
	}
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("读取头部失败: %w", err)
	}
	if size < HeaderSize+FooterSize {
		return nil, fmt.Errorf("文件只有 %d 字节，%s 容器至少需要 %d 字节（头部 %d + 尾部 %d）",
			size, FormatName, HeaderSize+FooterSize, HeaderSize, FooterSize)
	}
	if size > MaxPackageBytes {
		return nil, fmt.Errorf("容器 %d 字节，超过上限 %d", size, int64(MaxPackageBytes))
	}
	head := make([]byte, HeaderSize)
	if _, err := r.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("读取头部失败: %w", err)
	}
	h, err := parseHeader(head)
	if err != nil {
		return nil, err
	}

	// 布局不变量：清单紧接头部，载荷紧接清单。
	if h.ManifestOff != HeaderSize {
		return nil, fmt.Errorf("清单偏移 %d 异常，应为 %d", h.ManifestOff, HeaderSize)
	}
	if h.PayloadOff != h.ManifestOff+h.ManifestLen {
		return nil, errors.New("载荷偏移与清单长度不一致，容器结构异常")
	}
	if h.ManifestLen == 0 || h.ManifestLen > MaxPackageBytes {
		return nil, fmt.Errorf("清单长度 %d 异常", h.ManifestLen)
	}
	end := h.PayloadOff + h.PayloadLen
	// 签名块（若有）紧跟在载荷之后、尾部之前。
	sigLen := uint64(0)
	if h.Flags&FlagSigned != 0 {
		sigLen = SigBlockSize
	}
	if end+sigLen+FooterSize != uint64(size) {
		return nil, fmt.Errorf("声明的数据长度与文件实际大小不符（声明到 %d，文件 %d）—— "+
			"文件被截断或追加过内容", end+sigLen+FooterSize, size)
	}

	// 清单
	manBytes := make([]byte, h.ManifestLen)
	if _, err := r.ReadAt(manBytes, int64(h.ManifestOff)); err != nil {
		return nil, fmt.Errorf("读取清单失败: %w", err)
	}
	if got := sha256Sum(manBytes); !bytes.Equal(got, h.ManifestSHA[:]) {
		return nil, errors.New("清单摘要不匹配：清单已损坏或被篡改")
	}
	var man Manifest
	dec := json.NewDecoder(bytes.NewReader(manBytes))
	if err := dec.Decode(&man); err != nil {
		return nil, fmt.Errorf("清单不是合法 JSON: %w", err)
	}
	if man.Format != FormatName {
		return nil, fmt.Errorf("清单 format 字段为 %q，应为 %q", man.Format, FormatName)
	}
	if man.Version < MinReadVersion || man.Version > Version {
		return nil, fmt.Errorf("清单版本 %d 不受支持（支持 %d~%d）", man.Version, MinReadVersion, Version)
	}
	// 头部与清单的版本必须一致：不一致说明文件被拼过
	if int(h.FormatVersion) != man.Version {
		return nil, fmt.Errorf("头部版本 %d 与清单版本 %d 不一致", h.FormatVersion, man.Version)
	}
	// 签名标记的两处表述也必须一致
	if (h.Flags&FlagSigned != 0) != man.Signed {
		return nil, errors.New("头部签名标记与清单 signed 字段不一致")
	}

	// 尾部
	foot := make([]byte, FooterSize)
	if _, err := r.ReadAt(foot, size-FooterSize); err != nil {
		return nil, fmt.Errorf("读取尾部失败: %w", err)
	}
	if !bytes.Equal(foot[32:40], Magic[:]) {
		return nil, errors.New("尾部标记缺失：文件结尾不是有效的 PBClus 尾部")
	}
	if tl := binary.LittleEndian.Uint64(foot[40:48]); tl != uint64(size) {
		return nil, fmt.Errorf("尾部记录的总长 %d 与文件大小 %d 不符", tl, size)
	}

	rd := &Reader{ra: r, size: size, h: h, man: &man, contentEnd: int64(end)}

	// 签名块：只解析，不在这里验证。
	//
	// 分成"解析"与"验证"两件事，是因为两者的代价与用途不同：
	// 解析只是为了能告诉管理员"这个包是谁签的"（很便宜），
	// 验证要重算摘要（要读一遍载荷）。
	if h.Flags&FlagSigned != 0 {
		raw := make([]byte, SigBlockSize)
		if _, err := r.ReadAt(raw, int64(end)); err != nil {
			return nil, fmt.Errorf("读取签名块失败: %w", err)
		}
		sig, err := parseSignatureBlock(raw)
		if err != nil {
			return nil, err
		}
		rd.sig = sig
	}
	return rd, nil
}

// parseSignatureBlock 解析 108 字节签名块。
//
// 布局：
//
//	偏移  长度  字段
//	   0     1  算法（1 = Ed25519）
//	   1     1  保留
//	   2     2  保留
//	   4     8  keyId（公钥 SHA-256 前 8 字节）
//	  12    64  签名
//	  76    32  公钥
func parseSignatureBlock(b []byte) (*SignatureBlock, error) {
	if len(b) != SigBlockSize {
		return nil, fmt.Errorf("签名块长度 %d 异常，应为 %d", len(b), SigBlockSize)
	}
	sig := &SignatureBlock{Alg: b[0], KeyID: hex.EncodeToString(b[4:12])}
	if sig.Alg != SigAlgEd25519 {
		return nil, fmt.Errorf("未知签名算法 %d（本程序只支持 Ed25519 = %d）", sig.Alg, SigAlgEd25519)
	}
	copy(sig.Signature[:], b[12:76])
	copy(sig.PublicKey[:], b[76:108])
	// keyId 必须与内嵌公钥自洽：否则"这个包是谁签的"两处说法不一致，
	// 攻击者可以把一个可信 keyId 贴到自己的公钥前面来冒充。
	if got := KeyID(sig.PublicKey[:]); got != sig.KeyID {
		return nil, fmt.Errorf("签名块的 keyId(%s) 与内嵌公钥(%s) 不匹配", sig.KeyID, got)
	}
	return sig, nil
}

// marshalSignatureBlock 序列化为 108 字节。
func marshalSignatureBlock(s *SignatureBlock) []byte {
	b := make([]byte, SigBlockSize)
	b[0] = s.Alg
	keyID, _ := hex.DecodeString(s.KeyID)
	copy(b[4:12], keyID)
	copy(b[12:76], s.Signature[:])
	copy(b[76:108], s.PublicKey[:])
	return b
}

// Manifest 返回容器清单（只读，请勿修改）。
func (rd *Reader) Manifest() *Manifest { return rd.man }

// Header 信息（供 inspect 展示）。
type Info struct {
	Size        int64  `json:"size"`
	Version     int    `json:"version"`
	Flags       uint32 `json:"flags"`
	Compressed  bool   `json:"compressed"`
	EntryCount  int    `json:"entryCount"`
	PayloadSize int64  `json:"payloadSize"`
}

// Info 返回容器结构信息。
func (rd *Reader) Info() Info {
	return Info{
		Size:        rd.size,
		Version:     Version,
		Flags:       rd.h.Flags,
		Compressed:  rd.h.Flags&FlagDeflate != 0,
		EntryCount:  int(rd.h.EntryCount),
		PayloadSize: int64(rd.h.PayloadLen),
	}
}

// Close 关闭底层文件（若由 Open 打开）。
func (rd *Reader) Close() error {
	if rd.owned && rd.f != nil {
		return rd.f.Close()
	}
	return nil
}

// Verify 做**完整**完整性校验：载荷摘要、尾部整文件摘要、逐条目内容摘要。
//
// 对安装流程来说这是可选的（Extract 会逐条目校验），
// 但对"先告诉用户包坏在哪，再决定要不要解"更有用：
// 一个 200MB 的包损坏时，Verify 能给出具体是哪个文件坏了。
func (rd *Reader) Verify() error {
	if err := rd.verifyLayout(); err != nil {
		return err
	}
	// 载荷摘要
	payload := io.NewSectionReader(rd.ra, int64(rd.h.PayloadOff), int64(rd.h.PayloadLen))
	ph := sha256.New()
	if _, err := io.Copy(ph, payload); err != nil {
		return fmt.Errorf("读取载荷失败: %w", err)
	}
	if !bytes.Equal(ph.Sum(nil), rd.h.PayloadSHA[:]) {
		return errors.New("载荷摘要不匹配：容器内容已损坏或被修改")
	}
	// 整文件摘要（覆盖 头部+清单+载荷，即 [0, contentEnd)）
	//
	// 注意右边界是 contentEnd 而不是 size-FooterSize：带签名时
	// 两者之间还夹着签名块，而签名块**不该**被算进摘要
	// （它包含签名本身，算进去就自指了）。
	fh := sha256.New()
	full := io.NewSectionReader(rd.ra, 0, rd.contentEnd)
	if _, err := io.Copy(fh, full); err != nil {
		return fmt.Errorf("读取容器失败: %w", err)
	}
	foot := make([]byte, FooterSize)
	if _, err := rd.ra.ReadAt(foot, rd.size-FooterSize); err != nil {
		return err
	}
	if !bytes.Equal(fh.Sum(nil), foot[0:32]) {
		return errors.New("整文件摘要不匹配：容器已损坏或被修改")
	}
	// 逐条目
	for _, e := range rd.man.Entries {
		rc, err := rd.EntryReader(e)
		if err != nil {
			return err
		}
		eh := sha256.New()
		n, err := io.Copy(eh, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("校验条目 %q 失败: %w", e.Path, err)
		}
		if n != e.Size {
			return fmt.Errorf("条目 %q 实际 %d 字节，清单声明 %d", e.Path, n, e.Size)
		}
		if hex.EncodeToString(eh.Sum(nil)) != e.SHA256 {
			return fmt.Errorf("条目 %q 内容摘要不匹配", e.Path)
		}
	}
	return nil
}

// verifyLayout 校验清单自身的合法性与偏移范围。
//
// 这一步是**安全关键**：清单里的路径、平台与偏移全部来自不可信输入，
// 在用它去拼目标路径或 seek 之前必须先证明它们合法。
func (rd *Reader) verifyLayout() error {
	m := rd.man
	if len(m.Entries) != m.EntryCount {
		return fmt.Errorf("清单声明 %d 个条目，实际列了 %d 个", m.EntryCount, len(m.Entries))
	}
	if len(m.Entries) > MaxEntries {
		return fmt.Errorf("条目数 %d 超过上限 %d", len(m.Entries), MaxEntries)
	}

	// 平台声明自洽性
	declared := map[string]bool{}
	anyDeclared := false
	for _, p := range m.Platforms {
		if p == PlatformAny {
			anyDeclared = true
			continue
		}
		if err := ValidatePlatform(p); err != nil {
			return err
		}
		if declared[p] {
			return fmt.Errorf("清单 platforms 里 %q 重复", p)
		}
		declared[p] = true
	}
	if len(declared) > MaxTargets {
		return fmt.Errorf("平台数 %d 超过上限 %d", len(declared), MaxTargets)
	}
	if anyDeclared && len(declared) > 0 {
		return errors.New("清单 platforms 不能同时声明 \"any\" 与具体平台")
	}

	seen := make(map[string]bool, len(m.Entries))
	paths := make([]string, 0, len(m.Entries))
	platformsUsed := map[string]bool{}
	var total, cursor int64
	for i, e := range m.Entries {
		if err := ValidatePath(e.Path); err != nil {
			return fmt.Errorf("第 %d 个条目路径非法: %w", i, err)
		}
		// 唯一性按 (路径, 平台) 判定：同一条路径可以有多个平台的版本，
		// 这是多平台包的核心机制。但同一平台下同名出现两次仍然拒绝 ——
		// 那会让解包结果取决于顺序。
		key := e.Platform + "\x00" + e.Path
		if seen[key] {
			return fmt.Errorf("条目重复（路径 %q 平台 %q 出现两次）", e.Path, e.Platform)
		}
		seen[key] = true
		paths = append(paths, e.Path)

		if e.Platform != "" {
			if err := ValidatePlatform(e.Platform); err != nil {
				return fmt.Errorf("条目 %q: %w", e.Path, err)
			}
			if anyDeclared {
				return fmt.Errorf("条目 %q 标了平台 %q，但清单声明 platforms=[\"any\"]，自相矛盾",
					e.Path, e.Platform)
			}
			if !declared[e.Platform] {
				return fmt.Errorf("条目 %q 的平台 %q 不在清单 platforms 里", e.Path, e.Platform)
			}
			platformsUsed[e.Platform] = true
		}

		switch e.Codec {
		case CodecRaw, CodecDeflate:
		default:
			return fmt.Errorf("条目 %q 使用了未知压缩方式 %q", e.Path, e.Codec)
		}
		if e.Size < 0 || e.PackedSize < 0 || e.Offset < 0 {
			return fmt.Errorf("条目 %q 的长度或偏移为负数", e.Path)
		}
		if e.Size > MaxFileBytes {
			return fmt.Errorf("条目 %q 声明 %d 字节，超过单文件上限 %d", e.Path, e.Size, int64(MaxFileBytes))
		}
		if e.Codec == CodecRaw && e.PackedSize != e.Size {
			return fmt.Errorf("条目 %q 未压缩但 packedSize(%d) != size(%d)", e.Path, e.PackedSize, e.Size)
		}
		if e.Offset+e.PackedSize > int64(rd.h.PayloadLen) {
			return fmt.Errorf("条目 %q 的偏移超出载荷范围", e.Path)
		}
		// 条目必须紧密排列且不重叠：正常打包器就是这么写的，
		// 出现空洞或重叠说明包被手工拼过。
		if e.Offset != cursor {
			return fmt.Errorf("条目 %q 的偏移 %d 与预期 %d 不符（载荷有空洞或重叠）", e.Path, e.Offset, cursor)
		}
		cursor += e.PackedSize
		total += e.Size
		if len(e.SHA256) != 64 {
			return fmt.Errorf("条目 %q 的摘要长度不对", e.Path)
		}
		if _, err := hex.DecodeString(e.SHA256); err != nil {
			return fmt.Errorf("条目 %q 的摘要不是十六进制", e.Path)
		}
		if total > MaxExtractedBytes {
			return fmt.Errorf("清单声明的解压总量 %d 超过上限 %d", total, int64(MaxExtractedBytes))
		}
	}
	if cursor != int64(rd.h.PayloadLen) {
		return fmt.Errorf("条目合计 %d 字节，载荷声明 %d 字节，不一致", cursor, rd.h.PayloadLen)
	}
	// 声明了某平台却没有任何该平台的条目 → 包是坏的（打包器不会这么写）
	for p := range declared {
		if !platformsUsed[p] {
			return fmt.Errorf("清单声明支持平台 %q，但没有任何条目属于它", p)
		}
	}
	if err := checkPrefixConflicts(paths); err != nil {
		return err
	}
	return nil
}

// EntryReader 返回某个条目的解压流。
//
// 解压读受限：即使清单里 size 撒谎，读取端也被 MaxFileBytes 卡住 ——
// 这是防"解压炸弹"的关键一环（清单是不可信输入）。
func (rd *Reader) EntryReader(e Entry) (io.ReadCloser, error) {
	sec := io.NewSectionReader(rd.ra, int64(rd.h.PayloadOff)+e.Offset, e.PackedSize)
	switch e.Codec {
	case CodecRaw:
		return io.NopCloser(io.LimitReader(sec, MaxFileBytes+1)), nil
	case CodecDeflate:
		// fr 自身是可关闭的（关闭会释放 flate 内部状态），
		// 所以用 limitedReadCloser 把"限流 + 关闭"一起带上，
		// 而不是 NopCloser —— 后者会把 Close 变成空操作。
		fr := flate.NewReader(sec)
		return &limitedReadCloser{r: io.LimitReader(fr, MaxFileBytes+1), c: fr}, nil
	default:
		return nil, fmt.Errorf("条目 %q 使用了未知压缩方式 %q", e.Path, e.Codec)
	}
}

// ExtractReport 是解包结果统计。
type ExtractReport struct {
	Files   int      `json:"files"`
	Bytes   int64    `json:"bytes"`
	Entries []string `json:"entries"`
	// Digests 是逐条目摘要校验结果（全部通过才会返回成功）。
	Verified int `json:"verified"`
	// Platform 本次解包面向的平台。
	Platform string `json:"platform,omitempty"`
	// Skipped 因平台不匹配而跳过的条目数。
	// 如实报出来：管理员看到"包里还有 6 个别的平台的文件被跳过"才不会困惑。
	Skipped int `json:"skipped,omitempty"`
}

// Extract 解包**当前平台**所需的文件。
//
// 这是最常用的入口：单平台包与多平台包都能用，
// 多平台包会自动只取当前平台的那一份。
func (rd *Reader) Extract(dst string) (*ExtractReport, error) {
	return rd.ExtractForPlatform(dst, HostPlatform())
}

// ---------------------------------------------------------------------------
// 格式识别
// ---------------------------------------------------------------------------

// SniffBytes 依据魔术字判断包类型。
//
// 只看内容不看扩展名：扩展名是给人看的提示，不是格式的判据。
// 一个 zip 改成 .pbclus 必须被识别为 zip —— 这样安装器才能给出
// "这是 zip 改的后缀"这种明确提示，而不是笼统的"格式错误"。
func SniffBytes(b []byte) Kind {
	if len(b) >= len(Magic) && bytes.Equal(b[:len(Magic)], Magic[:]) {
		return KindPBClus
	}
	if len(b) >= 4 {
		switch {
		case b[0] == 'P' && b[1] == 'K' && b[2] == 0x03 && b[3] == 0x04, // 普通
			b[0] == 'P' && b[1] == 'K' && b[2] == 0x05 && b[3] == 0x06, // 空归档
			b[0] == 'P' && b[1] == 'K' && b[2] == 0x07 && b[3] == 0x08: // 分卷
			return KindZip
		}
	}
	return KindUnknown
}

// SniffFile 读取文件头部判断类型。
func SniffFile(p string) (Kind, error) {
	f, err := os.Open(p)
	if err != nil {
		return KindUnknown, err
	}
	defer f.Close()
	buf := make([]byte, 8)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return KindUnknown, err
	}
	return SniffBytes(buf[:n]), nil
}

// IsZipFile 便捷判断（供安装器给"zip 改后缀"这种具体提示）。
func IsZipFile(p string) bool {
	k, err := SniffFile(p)
	return err == nil && k == KindZip
}

// ---------------------------------------------------------------------------
// 内部小工具
// ---------------------------------------------------------------------------

// limitedReadCloser 让解压流可关闭。
type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
