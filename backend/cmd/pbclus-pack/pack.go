package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/plugin/pbclus"
)

// pack 命令：把插件目录打成 .pbclus。
//
// # 平台这件事在打包侧是怎么定的
//
// 带原生二进制的插件**本来就是平台相关的**。把它标成"通用"会在别的平台上
// 以"入口文件不存在"失败 —— 一个与真实原因相距很远的错误。
// 所以这里的原则是：**要么如实标注平台，要么明确声明通用**，不猜。
//
// 三种模式：
//
//	--targets linux/amd64,windows/amd64   多平台包（一个包装多个平台）
//	--platform windows/amd64              单平台包（显式标注）
//	（都不给）                              自动判定，见 decidePlatform
//
// 自动判定的规则刻意保守：只有当入口确实是个原生可执行文件、
// 且它的类型与当前主机一致时才自动标注；不一致时**报错要求显式指定**。
// 猜错的代价是装上一个跑不起来的二进制，而报错的代价只是多打一行参数。
func cmdPack(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "输出文件名（默认 <插件名>-<版本>.pbclus）")
	targets := fs.String("targets", "", "多平台：逗号分隔的 goos/goarch 列表")
	platform := fs.String("platform", "", "单平台：显式标注平台（如 windows/amd64）")
	build := fs.Bool("build", false, "多平台模式下用 go build 交叉编译各平台二进制")
	store := fs.Bool("store", false, "不压缩（所有条目原样存储）")
	level := fs.Int("level", 0, "压缩级别 1(最快)~9(最小)，默认 6")
	includeAll := fs.Bool("include-all", false, "不排除任何文件")
	force := fs.Bool("force", false, "覆盖已存在的输出文件")
	quiet := fs.Bool("quiet", false, "只输出结果路径")
	signKey := fs.String("sign", "", "用该密钥文件对包签名")
	if err := fs.Parse(reorderArgs(args, map[string]bool{
		"store": true, "include-all": true, "force": true, "quiet": true, "build": true,
	})); err != nil {
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(stderr, "用法: pbclus-pack pack <目录> [-o 输出.pbclus] [--targets ...]")
		return exitUsage
	}
	srcDir := rest[0]
	if abs, err := filepath.Abs(srcDir); err == nil {
		srcDir = abs
	}
	st, err := os.Stat(srcDir)
	if err != nil {
		fmt.Fprintf(stderr, "无法访问 %s: %v\n", srcDir, err)
		return exitFail
	}
	if !st.IsDir() {
		fmt.Fprintf(stderr, "%s 不是目录\n", srcDir)
		return exitFail
	}
	if *level != 0 && (*level < 1 || *level > 9) {
		fmt.Fprintln(stderr, "--level 取值范围是 1~9")
		return exitUsage
	}
	if *targets != "" && *platform != "" {
		fmt.Fprintln(stderr, "--targets 与 --platform 不能同时用（前者是多平台包，后者是单平台包）")
		return exitUsage
	}

	// 清单必须先在源目录里通过校验 —— 用与宿主同一套解析规则。
	//
	// 这里用 plugin.ParseManifest 而不是 install.ReadManifest：
	// 后者会顺带强制"入口文件必须已存在"，而 --build 模式下入口正是
	// **待编译产物**。入口存在性检查放在下面按平台分别做（见 checkEntry）。
	rawManifest, err := os.ReadFile(filepath.Join(srcDir, plugin.ManifestFileName))
	if err != nil {
		fmt.Fprintf(stderr, "读取 plugin.json 失败: %v\n", err)
		return exitFail
	}
	man, err := plugin.ParseManifest(rawManifest)
	if err != nil {
		fmt.Fprintf(stderr, "plugin.json 校验失败: %v\n", err)
		return exitFail
	}

	outPath := *out
	if outPath == "" {
		outPath = defaultOutName(srcDir, man)
	}
	if !strings.EqualFold(filepath.Ext(outPath), pbclus.Extension) {
		fmt.Fprintf(stderr, "输出文件名必须以 %s 结尾: %s\n", pbclus.Extension, outPath)
		return exitUsage
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		fmt.Fprintf(stderr, "输出路径无效: %v\n", err)
		return exitFail
	}
	if !*force {
		if _, err := os.Stat(absOut); err == nil {
			fmt.Fprintf(stderr, "%s 已存在（用 --force 覆盖）\n", absOut)
			return exitFail
		}
	}

	opt := pbclus.PackOptions{
		Store:   *store,
		Level:   *level,
		Tool:    "pbclus-pack/" + toolVersion,
		Exclude: defaultExcluder(*includeAll),
		Plugin: &pbclus.PluginInfo{
			Name: man.Name, Title: man.Title, Version: man.Version,
			Author: man.Author, Description: man.Description,
			Entry: man.Entry, Hooks: man.Hooks, ProtocolVersion: man.ProtocolVersion,
		},
	}

	// 签名密钥
	if *signKey != "" {
		key, err := pbclus.LoadSigningKey(*signKey)
		if err != nil {
			fmt.Fprintf(stderr, "读取签名密钥失败: %v\n", err)
			return exitFail
		}
		if _, err := key.ParsePrivateKey(); err != nil {
			fmt.Fprintf(stderr, "签名密钥不可用: %v\n", err)
			return exitFail
		}
		opt.SignKey = key
	}

	var notes []string
	packSrc := srcDir
	if *targets != "" {
		list, err := parseTargets(*targets)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return exitUsage
		}
		opt.Platforms = list

		if *build {
			// 交叉编译到**临时目录**再打包，不污染作者的源码目录。
			stage, err := buildTargets(srcDir, man, list, stderr)
			if err != nil {
				fmt.Fprintf(stderr, "交叉编译失败: %v\n", err)
				return exitFail
			}
			defer os.RemoveAll(stage)
			packSrc = stage
			notes = append(notes, fmt.Sprintf("已为 %d 个平台交叉编译入口 %s", len(list), man.Entry))
		} else {
			notes = append(notes, "使用 dist/<平台>/ 下已有的二进制（未传 --build，不做编译）")
			// 没编译就要确认每个平台目录里真的有入口 ——
			// 否则会打出一个某个平台缺二进制的包，装上去才发现。
			for _, p := range list {
				dir := filepath.Join(srcDir, pbclus.DistDirName, pbclus.DirName(p))
				if _, ok := entryInDir(dir, man.Entry, goosOf(p)); !ok {
					fmt.Fprintf(stderr, "平台 %s 的目录 %s 里没有入口 %s（或其 .exe）\n"+
						"  用 --build 让工具交叉编译，或把产物放进该目录\n", p, dir, man.Entry)
					return exitFail
				}
			}
		}
	} else {
		// 单平台 / 通用
		decision, note, err := decidePlatform(srcDir, man, *platform)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return exitFail
		}
		opt.Universal = decision == ""
		opt.Platform = decision
		if note != "" {
			notes = append(notes, note)
		}
		// 单平台模式下的入口检查：按**目标平台**的语义找，
		// 而不是按当前主机的语义 —— 在 Linux 上打 windows/amd64 包时，
		// 入口本来就该是 foo.exe。
		if decision != "" {
			if _, ok := entryInDir(srcDir, man.Entry, goosOf(decision)); !ok {
				fmt.Fprintf(stderr, "目录里没有平台 %s 的入口 %s（或其 .exe）\n", decision, man.Entry)
				return exitFail
			}
		}
	}

	res, err := pbclus.PackDir(absOut, packSrc, opt)
	if err != nil {
		fmt.Fprintf(stderr, "打包失败: %v\n", err)
		os.Remove(absOut) // 不留半成品
		return exitFail
	}

	if *quiet {
		fmt.Fprintln(stdout, absOut)
		return exitOK
	}
	fi, _ := os.Stat(absOut)
	fmt.Fprintf(stdout, "已生成 %s\n", absOut)
	fmt.Fprintf(stdout, "  插件      %s %s\n", res.Plugin.Name, res.Plugin.Version)
	fmt.Fprintf(stdout, "  平台      %s\n", strings.Join(res.Platforms, "、"))
	fmt.Fprintf(stdout, "  条目      %d 个，内容 %.1f KiB\n", res.EntryCount, float64(res.TotalSize)/1024)
	if fi != nil {
		ratio := 0.0
		if res.TotalSize > 0 {
			ratio = float64(fi.Size()) / float64(res.TotalSize) * 100
		}
		fmt.Fprintf(stdout, "  容器      %.1f KiB（%.0f%%）\n", float64(fi.Size())/1024, ratio)
	}
	fmt.Fprintf(stdout, "  入口      %s\n", res.Plugin.Entry)
	if len(res.Plugin.Hooks) > 0 {
		fmt.Fprintf(stdout, "  钩子      %s\n", strings.Join(res.Plugin.Hooks, ", "))
	}
	if res.Signed {
		fmt.Fprintf(stdout, "  签名      %s\n", opt.SignKey.KeyID)
	} else {
		fmt.Fprintln(stdout, "  签名      无（宿主在 require 策略下会拒绝；警告策略下会提示）")
	}
	for _, n := range notes {
		fmt.Fprintf(stdout, "  说明      %s\n", n)
	}
	warnPlatformEntry(stdout, res)
	fmt.Fprintln(stdout, "\n提示: 用 pbclus-pack verify 可以再校验一次完整性")
	return exitOK
}

// goosOf 取平台标签里的 goos 段。
func goosOf(platform string) string {
	goos, _, _ := strings.Cut(platform, "/")
	return goos
}

// entryInDir 在目录里按**目标平台**的语义找入口文件。
//
// 不能用 manifest.ExecPath：它的 .exe 补全只在**当前主机**是 Windows 时生效
// （那是为了"同一份 plugin.json 跨平台通用"）。而打包时要判定的是
// "目标平台是 Windows 时该找 .exe" —— 在 Linux 上打 Windows 包时，
// ExecPath 会因为拿不到 .exe 补全而误报"入口不存在"。
func entryInDir(dir, entry, goos string) (string, bool) {
	cands := []string{entry}
	if goos == "windows" {
		// 目标平台是 Windows：优先 .exe，但也接受无后缀（有人会这么打）
		cands = []string{entry + ".exe", entry}
	}
	for _, c := range cands {
		p := filepath.Join(dir, c)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// parseTargets 解析并去重 --targets。
func parseTargets(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, ",") {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if err := pbclus.ValidatePlatform(p); err != nil {
			return nil, fmt.Errorf("--targets 里的 %q 不合法: %w", p, err)
		}
		if p == pbclus.PlatformAny {
			return nil, fmt.Errorf("--targets 不接受 \"any\"（要通用包就别传 --targets）")
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--targets 为空")
	}
	sort.Strings(out)
	return out, nil
}

// decidePlatform 在没给平台参数时自动判定，返回平台标签
// （空串表示通用包）与一条说明。
//
// 规则刻意保守：
//   - 入口不是原生可执行文件（脚本/无扩展名文本）→ 通用
//   - 入口是原生可执行文件且类型与当前主机一致 → 标注当前平台
//   - 类型与当前主机不一致 → **报错**，要求显式指定
//
// 最后一条是关键：在 Linux 上打一个 Windows 的 .exe，自动标成
// "linux/amd64" 会让包带着错误信息流通出去；报错只让你多打一行参数。
func decidePlatform(srcDir string, man *plugin.Manifest, explicit string) (string, string, error) {
	if explicit != "" {
		if err := pbclus.ValidatePlatform(explicit); err != nil {
			return "", "", err
		}
		if explicit == pbclus.PlatformAny {
			return "", "", fmt.Errorf("--platform 不接受 \"any\"（要通用包就别传 --platform）")
		}
		return explicit, "已显式标注为单平台包", nil
	}

	// 用宿主自己的入口解析（含 Windows 的 .exe 补全）——
	// 平台后缀解析只有一处实现，就只有一处。
	execPath, err := man.ExecPath(srcDir)
	if err != nil {
		return "", "", fmt.Errorf("清单入口解析失败: %w", err)
	}
	if _, err := os.Stat(execPath); err != nil {
		return "", "", fmt.Errorf("清单声明的入口文件 %q 在目录里不存在（找的是 %s）", man.Entry, execPath)
	}

	kind := pbclus.SniffExecutable(execPath)
	goos := pbclus.KindOfPlatform(kind)
	if goos == "" {
		// 不是原生可执行文件：脚本、字节码等，跨平台可用
		return "", "入口不是原生可执行文件，按通用包处理（可在任意平台安装）", nil
	}
	if goos != runtime.GOOS {
		return "", "", fmt.Errorf(
			"入口 %s 是 %s 平台的可执行文件，但当前主机是 %s —— 无法自动判断该标哪个平台。\n"+
				"  请显式指定：--platform %s/%s\n"+
				"  或者打成跨平台包：--targets %s/%s,%s/amd64 --build",
			filepath.Base(execPath), goos, runtime.GOOS,
			goos, runtime.GOARCH, runtime.GOOS, runtime.GOARCH, goos)
	}
	return pbclus.HostPlatform(),
		fmt.Sprintf("入口是 %s 原生可执行文件，已标注为 %s（跨平台请用 --targets）", goos, pbclus.HostPlatform()), nil
}

// buildTargets 在临时目录里为每个平台交叉编译入口文件。
//
// 为什么用临时目录而不是直接写进作者的 dist/：打包工具不该修改源码目录。
// 作者想保留产物可以自己 go build，工具只负责"打出一个能用的包"。
//
// 目录结构（临时目录内）：<src> 的副本（不含 dist/）+ dist/<平台>/<entry>
func buildTargets(srcDir string, man *plugin.Manifest, targets []string, stderr io.Writer) (string, error) {
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		return "", fmt.Errorf("源目录里没有 go.mod，无法用 --build 交叉编译。"+
			"非 Go 插件请把各平台产物放到 dist/<goos>-<goarch>/ 后不带 --build 打包")
	}
	stage, err := os.MkdirTemp("", "pbclus-build-")
	if err != nil {
		return "", err
	}
	// 复制源码（排除 dist/ —— 那里是"已有产物"，不该混进新构建）
	if err := copyTreeExcept(srcDir, stage, pbclus.DistDirName); err != nil {
		os.RemoveAll(stage)
		return "", err
	}

	entryName := man.Entry
	for _, p := range targets {
		goos, goarch, _ := strings.Cut(p, "/")
		dir := filepath.Join(stage, pbclus.DistDirName, pbclus.DirName(p))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
		// Windows 目标要带 .exe：宿主会补后缀，但产物本身得有正确名字
		bin := entryName
		if goos == "windows" && !strings.HasSuffix(strings.ToLower(bin), ".exe") {
			bin += ".exe"
		}
		out := filepath.Join(dir, bin)
		fmt.Fprintf(stderr, "  正在为 %s 编译 %s ...\n", p, bin)
		cmd := exec.Command("go", "build", "-trimpath", "-o", out, ".")
		cmd.Dir = stage
		cmd.Env = append(os.Environ(),
			"GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		if outBytes, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(stage)
			return "", fmt.Errorf("为 %s 编译失败: %v\n%s", p, err, outBytes)
		}
		if st, err := os.Stat(out); err != nil || st.Size() == 0 {
			os.RemoveAll(stage)
			return "", fmt.Errorf("为 %s 编译后没有产物: %s", p, out)
		}
	}
	return stage, nil
}

// copyTreeExcept 复制目录，跳过指定的顶层目录名。
func copyTreeExcept(src, dst, skipTop string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		top := rel
		if i := strings.IndexAny(top, `/\`); i >= 0 {
			top = top[:i]
		}
		if top == skipTop {
			if info.IsDir() && rel == top {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o750)
	})
}

// defaultOutName 生成默认输出名：<插件名>-<版本>.pbclus。
func defaultOutName(srcDir string, man *plugin.Manifest) string {
	name := man.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(srcDir))
	}
	if man.Version != "" {
		return fmt.Sprintf("%s-%s%s", name, man.Version, pbclus.Extension)
	}
	return name + pbclus.Extension
}

// warnPlatformEntry 提示"包里只有某个平台的二进制"。
//
// 多平台包不该出现这种情况（每个平台都有自己的目录），
// 但单平台包里很常见：在 Windows 上打了包，包里只有 foo.exe。
// 清单里的 entry 不带后缀是对的（宿主在 Windows 上会自动补），
// 但**在 Linux/macOS 上宿主会找 foo 并失败** ——
// 而错误信息里只有文件名，很容易被当成"打包漏了文件"。
func warnPlatformEntry(w io.Writer, res *pbclus.Manifest) {
	entry := res.Plugin.Entry
	if entry == "" || len(res.Platforms) > 1 {
		return
	}
	var hasPlain, hasExe bool
	for _, e := range res.Entries {
		if e.Platform != "" && e.Platform != res.Platforms[0] && res.Platforms[0] != pbclus.PlatformAny {
			continue
		}
		switch e.Path {
		case entry:
			hasPlain = true
		case entry + ".exe":
			hasExe = true
		}
	}
	if hasExe && !hasPlain {
		fmt.Fprintf(w, "\n注意: 包里只有 %s.exe，没有 %s。\n"+
			"      清单里的 entry 不带后缀是对的（宿主在 Windows 上会自动补 .exe），\n"+
			"      但**在 Linux/macOS 上宿主会找 %s 并失败**。\n"+
			"      要跨平台分发，请用 --targets 打一个多平台包。\n", entry, entry, entry)
	}
}
