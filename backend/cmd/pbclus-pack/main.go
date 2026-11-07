// Command pbclus-pack 是 NebulaDrive 插件包（.pbclus）的打包、签名与检查工具。
//
// 用法：
//
//	pbclus-pack pack <目录> [--targets a,b] [--build] [--sign 密钥] [-o 输出]
//	pbclus-pack inspect <包.pbclus> [--json]
//	pbclus-pack verify <包.pbclus> [--trusted-keys <信任库>]
//	pbclus-pack unpack <包.pbclus> -o <目录>
//	pbclus-pack keygen [-o 密钥文件]
//	pbclus-pack trust list|add|remove
//	pbclus-pack info
//
// 退出码：0 成功 / 1 失败 / 2 用法错误。
// 这个约定让打包能安全地写进脚本（`pbclus-pack verify x && deploy x`）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nebula-drive/nebula/pkg/plugin/install"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

const toolVersion = "1.0.0"

// 退出码。
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 是命令入口，返回退出码。
//
// 不做 os.Exit 是为了可测：测试直接调 run 就能覆盖全部命令分支，
// 不需要编译出二进制再起子进程。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "pack":
		return cmdPack(args[1:], stdout, stderr)
	case "inspect":
		return cmdInspect(args[1:], stdout, stderr)
	case "verify":
		return cmdVerify(args[1:], stdout, stderr)
	case "unpack":
		return cmdUnpack(args[1:], stdout, stderr)
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr)
	case "trust":
		return cmdTrust(args[1:], stdout, stderr)
	case "info":
		return cmdInfo(stdout)
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "pbclus-pack %s\n", toolVersion)
		return exitOK
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `pbclus-pack —— NebulaDrive 插件包（PBClus）打包工具

用法:
  pbclus-pack pack <目录> [选项]
  pbclus-pack inspect <包.pbclus> [--json]     查看包内容
  pbclus-pack verify <包.pbclus> [--trusted-keys <信任库>]
  pbclus-pack unpack <包.pbclus> -o <目录>     解包（检查用）
  pbclus-pack keygen [选项]                    生成签名密钥
  pbclus-pack trust list|add|remove [选项]     管理信任库
  pbclus-pack info                             打印格式说明

pack 的平台选项（三者不要同时用）:
  --targets linux/amd64,windows/amd64,darwin/arm64
        打**跨平台包**：各平台文件放 dist/<goos>-<goarch>/
  --targets ... --build
        让工具用 go build 交叉编译各平台入口（需要源目录有 go.mod）
  --platform windows/amd64
        单平台包：显式标注平台
  （都不给）
        自动判定：入口不是原生可执行文件 → 通用包；
        是原生可执行文件且与当前主机同类型 → 标注当前平台；
        类型与主机不一致 → 报错要求显式指定（不猜）

pack 的其它选项:
  -o <文件>        输出路径（默认 <插件名>-<版本>.pbclus）
  --sign <密钥>    用密钥文件对包签名（先 keygen 生成）
  --store          不压缩   --level 1~9  压缩级别
  --include-all    不排除任何文件
  --force          覆盖已存在的输出     --quiet  只输出结果路径

打包:
  <目录> 里必须有 plugin.json。工具会用与宿主**同一套**校验检查它，
  清单有问题在打包阶段就报错，而不是等管理员安装时才发现。

  默认排除: .git .svn .hg node_modules __pycache__ .DS_Store Thumbs.db
            desktop.ini *.pbclus *.zip *.log *.tmp *.swp *.bak *~
  用 --include-all 关闭排除。

签名与信任（重要）:
  签名证明"内容与签名自洽"，**但内嵌公钥意味着任何人都能签一个合法的包**。
  安全语义只来自宿主的信任库：只有 keyId 在信任库里的包才算"受信任"。
  宿主没有信任库时**不会**信任任何签名（不会退化成信任所有签名）。

退出码: 0 成功 / 1 失败 / 2 用法错误
`)
}

// defaultExcludes 是打包时默认跳过的路径/文件名。
//
// 用"名字匹配"而不是完整 glob：插件目录通常很小，
// 这里的目标是挡住常见的垃圾（版本库、依赖目录、编辑器临时文件），
// 不是实现一套完整的忽略规则语言。
var defaultExcludes = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "__pycache__": true,
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true,
}

var defaultExcludeExts = map[string]bool{
	".pbclus": true, ".zip": true, ".log": true,
	".tmp": true, ".swp": true, ".bak": true, "~": true,
}

func defaultExcluder(includeAll bool) func(string) bool {
	if includeAll {
		return nil
	}
	return func(rel string) bool {
		for _, seg := range strings.Split(rel, "/") {
			if defaultExcludes[seg] {
				return true
			}
		}
		base := filepath.Base(rel)
		ext := strings.ToLower(filepath.Ext(base))
		if defaultExcludeExts[ext] {
			return true
		}
		// 编辑器备份文件：foo.txt~
		if strings.HasSuffix(base, "~") {
			return true
		}
		// 其它包的残留
		if strings.HasPrefix(base, ".") && strings.HasSuffix(base, ".pbclus") {
			return true
		}
		return false
	}
}

// reorderArgs 把 flag 挪到位置参数之前。
//
// 为什么需要它：Go 的 flag 包在遇到第一个非 flag 参数后就停止解析，
// 于是 `pack . -o out.pbclus` 里的 `-o` 会被当成位置参数，
// 报"用法错误"。而"先是对象、再是选项"恰恰是人最自然的写法，
// 所以这里自己重排，而不是要求用户把选项写在前面。
//
// boolFlags 列出哪些 flag 不取值（取值的 flag 会把下一个参数一起搬过去）。
func reorderArgs(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue // -o=x 自带值
		}
		if boolFlags[name] {
			continue
		}
		// 取值 flag：把它的值一起搬到前面
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return append(flags, positional...)
}

// ---------- inspect ----------

func cmdInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"json": true})); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法: pbclus-pack inspect <包.pbclus> [--json]")
		return exitUsage
	}
	rd, err := pbclus.Open(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "打开失败: %v\n", err)
		return exitFail
	}
	defer rd.Close()

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{
			"info":     rd.Info(),
			"manifest": rd.Manifest(),
		}); err != nil {
			fmt.Fprintf(stderr, "输出失败: %v\n", err)
			return exitFail
		}
		return exitOK
	}

	man := rd.Manifest()
	info := rd.Info()
	fmt.Fprintf(stdout, "包        %s\n", rest[0])
	fmt.Fprintf(stdout, "格式      %s v%d（%d 字节）\n", man.Format, man.Version, info.Size)
	fmt.Fprintf(stdout, "平台      %s\n", strings.Join(rd.Platforms(), "、"))
	fmt.Fprintf(stdout, "构建      %s  工具 %s\n", man.CreatedAt, man.Tool)
	// 签名状态：这里用 nil 信任库，只报"签名是否自洽"。
	// 是否**可信**取决于宿主的信任库，工具不替它下结论。
	st := rd.Status(nil)
	switch {
	case !st.Present:
		fmt.Fprintln(stdout, "签名      无")
	case st.Valid:
		fmt.Fprintf(stdout, "签名      Ed25519 有效，keyId %s（是否受信任取决于宿主的信任库）\n", st.KeyID)
	default:
		fmt.Fprintf(stdout, "签名      无效！keyId %s —— %s\n", st.KeyID, st.Err)
	}
	if man.Plugin != nil {
		fmt.Fprintf(stdout, "插件      %s", man.Plugin.Name)
		if man.Plugin.Version != "" {
			fmt.Fprintf(stdout, " %s", man.Plugin.Version)
		}
		fmt.Fprintln(stdout)
		if man.Plugin.Title != "" {
			fmt.Fprintf(stdout, "标题      %s\n", man.Plugin.Title)
		}
		if man.Plugin.Author != "" {
			fmt.Fprintf(stdout, "作者      %s\n", man.Plugin.Author)
		}
		if man.Plugin.Entry != "" {
			fmt.Fprintf(stdout, "入口      %s\n", man.Plugin.Entry)
		}
		if len(man.Plugin.Hooks) > 0 {
			fmt.Fprintf(stdout, "钩子      %s\n", strings.Join(man.Plugin.Hooks, ", "))
		}
	}
	compressed := 0
	var packed int64
	for _, e := range man.Entries {
		packed += e.PackedSize
		if e.Codec == pbclus.CodecDeflate {
			compressed++
		}
	}
	fmt.Fprintf(stdout, "条目      %d 个（%d 个已压缩），内容 %.1f KiB，载荷 %.1f KiB\n",
		man.EntryCount, compressed, float64(man.TotalSize)/1024, float64(packed)/1024)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "  大小        压缩后  方式     平台               路径")
	for _, e := range man.Entries {
		p := e.Platform
		if p == "" {
			p = pcAny
		}
		fmt.Fprintf(stdout, "  %9d %9d  %-8s %-18s %s\n", e.Size, e.PackedSize, e.Codec, p, e.Path)
	}
	fmt.Fprintln(stdout, "\n提示: 用 pbclus-pack verify 校验每个条目的内容摘要")
	return exitOK
}

// pcAny 是 inspect 表格里"通用条目"的显示值。
// 显示成 "any" 而不是空白：空白会让人以为那一列没数据。
const pcAny = "any"

// ---------- verify ----------

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	quiet := fs.Bool("quiet", false, "只返回退出码，不打印")
	keys := fs.String("trusted-keys", "", "信任库文件（给了就同时报告签名是否受信任）")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"quiet": true})); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法: pbclus-pack verify <包.pbclus> [--trusted-keys <信任库>]")
		return exitUsage
	}
	rd, err := pbclus.Open(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "校验失败: %v\n", err)
		return exitFail
	}
	defer rd.Close()
	if err := rd.Verify(); err != nil {
		fmt.Fprintf(stderr, "校验失败: %v\n", err)
		return exitFail
	}

	// 信任库是可选参数：不传时只报"签名是否自洽"，
	// 传了才能回答"是否受信任"。这两件事必须分开说 ——
	// 把"签名有效"说成"可信"是这类工具最危险的表述。
	var ring *pbclus.KeyRing
	if *keys != "" {
		ring, err = install.LoadTrustStore(*keys)
		if err != nil {
			fmt.Fprintf(stderr, "读取信任库失败: %v\n", err)
			return exitFail
		}
	}
	st := rd.Status(ring)

	if !*quiet {
		fmt.Fprintf(stdout, "校验通过: %d 个条目全部匹配，平台 %s\n",
			rd.Manifest().EntryCount, strings.Join(rd.Platforms(), "、"))
		if ring == nil {
			switch {
			case !st.Present:
				fmt.Fprintln(stdout, "签名: 无")
			case st.Valid:
				fmt.Fprintf(stdout, "签名: Ed25519 有效（keyId %s）—— 未提供信任库，无法判断是否可信\n", st.KeyID)
			default:
				// 签名无效是**校验失败**：包虽然自洽，但签名与内容对不上，
				// 说明它被改动过。这种情况必须返回非 0，否则脚本会把它当成功。
				fmt.Fprintf(stderr, "签名无效: keyId %s —— %s\n", st.KeyID, st.Err)
				return exitFail
			}
		} else {
			switch {
			case !st.Present:
				fmt.Fprintln(stdout, "签名: 无（该包没有签名）")
			case !st.Valid:
				fmt.Fprintf(stderr, "签名无效: keyId %s —— %s\n", st.KeyID, st.Err)
				return exitFail
			case st.Trusted:
				fmt.Fprintf(stdout, "签名: 受信任（%s，keyId %s）\n", st.KeyName, st.KeyID)
			default:
				// 签名自洽但不在信任库里：不是"校验失败"，
				// 而是"这个发布者你不认识"。用 warnings 语义：打印但返回 0。
				fmt.Fprintf(stdout, "签名: 有效但**未受信任**（keyId %s 不在信任库里）\n", st.KeyID)
			}
		}
	} else if st.Present && !st.Valid {
		return exitFail
	}
	return exitOK
}

// ---------- unpack ----------

func cmdUnpack(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unpack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "输出目录（必须显式指定）")
	force := fs.Bool("force", false, "允许写入非空目录")
	if err := fs.Parse(reorderArgs(args, map[string]bool{"force": true})); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 || *out == "" {
		fmt.Fprintln(stderr, "用法: pbclus-pack unpack <包.pbclus> -o <目录>")
		return exitUsage
	}
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 && !*force {
		// 解包会把同名文件覆盖掉。默认拒绝非空目录是为了避免
		// "我只是想看看包里有什么"变成一次意外的覆盖。
		fmt.Fprintf(stderr, "输出目录 %s 不是空的（用 --force 允许覆盖）\n", *out)
		return exitFail
	}
	rd, err := pbclus.Open(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "打开失败: %v\n", err)
		return exitFail
	}
	defer rd.Close()
	rep, err := rd.Extract(*out)
	if err != nil {
		fmt.Fprintf(stderr, "解包失败: %v\n", err)
		return exitFail
	}
	names := append([]string(nil), rep.Entries...)
	sort.Strings(names)
	fmt.Fprintf(stdout, "已解包 %d 个文件（%.1f KiB）到 %s\n",
		rep.Files, float64(rep.Bytes)/1024, *out)
	for _, n := range names {
		fmt.Fprintf(stdout, "  %s\n", n)
	}
	return exitOK
}

// ---------- info ----------

func cmdInfo(stdout io.Writer) int {
	fmt.Fprintf(stdout, `PBClus 插件包格式 v%d

布局（整数小端）:
  [头部 %d 字节][清单 JSON][载荷][尾部 %d 字节]

头部:
  偏移  长度  字段
     0     8  魔术字 "PBClus" + 0x1A 0x0A
     8     2  格式版本
    10     2  头部长度
    12     4  flags（bit0 = 载荷含 DEFLATE 压缩）
    16     4  条目数
    20     8  清单偏移
    28     8  清单长度
    36     8  载荷偏移
    44     8  载荷长度
    52    32  清单 SHA-256
    84    32  载荷 SHA-256
   116     4  头部 CRC32（覆盖前 116 字节）
   120     8  保留

尾部:
     0    32  整文件 SHA-256（覆盖 头部+清单+载荷）
    32     8  魔术字
    40     8  文件总长度

限额:
  单个文件   %d MiB
  解压总量   %d MiB
  条目数     %d
  容器大小   %d MiB
  单条路径   %d 字节

识别依据是**魔术字**，不是扩展名。把 zip 改名成 %s 会被识别为 zip
并明确报错 —— 改后缀不会改变格式。

校验边界:
  CRC 与 SHA-256 只保证完整性（没损坏/没截断），**不提供防伪**。
  任何人都能重新打包并算出正确摘要。v1 没有签名。

条目路径规则:
  必须相对、规范化、纯正斜杠；拒绝 . 与 .. 段、空段、盘符、
  控制字符、Windows 保留设备名（CON/NUL/COM1…）。

详细规范见 docs/PBClus-格式规范.md
`,
		pbclus.Version, pbclus.HeaderSize, pbclus.FooterSize,
		pbclus.MaxFileBytes>>20, pbclus.MaxExtractedBytes>>20,
		pbclus.MaxEntries, pbclus.MaxPackageBytes>>20, pbclus.MaxPathLen,
		pbclus.Extension)
	return exitOK
}
