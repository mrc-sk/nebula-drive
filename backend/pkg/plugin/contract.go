package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// v2 插件三文件契约：
//
//   plugin.json   身份 / 协议版本 / hooks / 权限 / pages / providers
//   register.go   纯声明（不编译）：版本、安装内容、hooks、providers
//   co.BEGG       JSON：runtime、解释器要求、主页、listen、UI 扩展点
//
// 三者的目录层级完全不限：宿主从安装根目录递归定位（深度 <= 5），
// 所以 ASS/ 里塞一堆 PHP + 一个 register.go 也能被正确识别。
//
// v1 插件（protocolVersion=1）不要求后两个文件，保持向后兼容。

// MaxContractDepth 递归定位声明文件的最大深度。
const MaxContractDepth = 5

// Contract 一个插件包的完整契约。
//
// 三个「目录」字段职责不同，别混用（目录形式不限定时尤其容易踩坑）：
//
//	Dir         插件安装根目录 plugins/<name>/ —— JSON-RPC entry、ExecPath、
//	            NB_PLUGIN_DIR 的基准，始终等于调用方传入的 dir。
//	ManifestDir plugin.json 实际所在目录（可能在任意子目录）。
//	BeggDir     co.BEGG 实际所在目录 —— 页面服务的工作目录，也是 co.BEGG 里
//	            home/entry/static 等相对路径的基准。用户把 co.BEGG 和一堆
//	            .php 一起放进 ASS/ 时，home 直接写 "in.php" 即可。
type Contract struct {
	Dir         string
	ManifestDir string
	BeggDir     string
	Manifest    *Manifest
	Decl        *GoDecl // v1 为 nil
	Begg        *Begg   // v1 为 nil
	V1          bool
	GoDeclPath  string
	BeggPath    string
	RuntimeBins map[string]string // 探测到的解释器路径
}

// RequiredV2Files v2 插件必需的三个文件，供错误提示与文档生成。
var RequiredV2Files = []string{ManifestFileName, GoDeclFileName, BeggFileName}

// LoadContract 加载并交叉校验插件契约。
//
// probeRuntime=true 时会做解释器探测；缺解释器直接返回错误
// （用户要求：装载时就报告，不静默降级、不自动安装）。
func LoadContract(dir string, probeRuntime bool) (*Contract, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("插件目录不存在或不是目录: %s", dir)
	}

	manifestPath, err := locateFile(dir, ManifestFileName)
	if err != nil {
		return nil, fmt.Errorf("找不到 %s（v2 插件必需文件：%s）: %w",
			ManifestFileName, strings.Join(RequiredV2Files, " / "), err)
	}
	manifestDir := filepath.Dir(manifestPath)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", ManifestFileName, err)
	}
	man, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}

	c := &Contract{
		Dir: dir, ManifestDir: manifestDir, Manifest: man,
		RuntimeBins: map[string]string{},
	}

	// v1 自动兼容：钩子名与语义冻结，不要求 register.go / co.BEGG。
	if man.ProtocolVersion < ProtocolVersion {
		c.V1 = true
		return c, nil
	}

	// 三个文件都从插件根目录递归定位，彼此可以在完全不同的子目录里。
	goPath, err := locateFile(dir, GoDeclFileName)
	if err != nil {
		return nil, fmt.Errorf("v2 插件缺少 %s（纯声明文件，宿主只读不编译）: %w", GoDeclFileName, err)
	}
	beggPath, err := locateFile(dir, BeggFileName)
	if err != nil {
		return nil, fmt.Errorf("v2 插件缺少 %s（JSON：runtime/主页/UI 扩展点）: %w", BeggFileName, err)
	}
	c.GoDeclPath, c.BeggPath = goPath, beggPath
	c.BeggDir = filepath.Dir(beggPath)

	decl, err := ParseGoDecl(goPath)
	if err != nil {
		return nil, err
	}
	beggRaw, err := os.ReadFile(beggPath)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", BeggFileName, err)
	}
	begg, err := ParseBegg(beggRaw)
	if err != nil {
		return nil, err
	}
	if err := begg.ValidateFiles(c.BeggDir); err != nil {
		return nil, err
	}
	c.Decl, c.Begg = decl, begg

	// 三份声明交叉校验：防止版本错配、钩子漏声明、name 不一致。
	if err := crossCheck(man, decl, begg); err != nil {
		return nil, err
	}

	if probeRuntime {
		bins, err := begg.ProbeRuntime()
		if err != nil {
			return nil, err
		}
		c.RuntimeBins = bins
	}
	return c, nil
}

// crossCheck 比对 plugin.json / register.go / co.BEGG 的关键字段。
func crossCheck(m *Manifest, d *GoDecl, b *Begg) error {
	if d.Name != m.Name {
		return fmt.Errorf("声明不一致：register.go 的 Name=%q，plugin.json 的 name=%q", d.Name, m.Name)
	}
	if d.Version != "" && m.Version != "" && d.Version != m.Version {
		return fmt.Errorf("声明不一致：register.go 的 Version=%q，plugin.json 的 version=%q", d.Version, m.Version)
	}
	if d.ProtocolVersion != 0 && d.ProtocolVersion != m.ProtocolVersion {
		return fmt.Errorf("声明不一致：register.go 的 ProtocolVersion=%d，plugin.json 的 protocolVersion=%d",
			d.ProtocolVersion, m.ProtocolVersion)
	}
	// hooks 集合必须一致（两边都为空也一致，纯页面插件走这条路）
	a := append([]string(nil), m.Hooks...)
	c := append([]string(nil), d.Hooks...)
	sort.Strings(a)
	sort.Strings(c)
	if strings.Join(a, ",") != strings.Join(c, ",") {
		return fmt.Errorf("声明不一致：hooks 两边不一致 —— plugin.json=%v，register.go=%v。"+
			"请让 register.go 与 plugin.json 声明同一份钩子清单", a, c)
	}
	if d.Runtime != "" && d.Runtime != b.Runtime {
		return fmt.Errorf("声明不一致：register.go 的 Runtime=%q，co.BEGG 的 runtime=%q", d.Runtime, b.Runtime)
	}
	if d.Home != "" && b.Home != "" && d.Home != b.Home {
		return fmt.Errorf("声明不一致：register.go 的 Home=%q，co.BEGG 的 home=%q", d.Home, b.Home)
	}
	return nil
}

// locateFile 在 root 下递归查找指定文件名。
// 跳过隐藏目录、依赖目录与过深层级；找到第一个即返回。
func locateFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(os.PathSeparator)) + 1
		}
		if d.IsDir() {
			if depth > MaxContractDepth {
				return fs.SkipDir
			}
			base := d.Name()
			if p != root && (strings.HasPrefix(base, ".") ||
				base == "node_modules" || base == "__pycache__" || base == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == name {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("递归查找无结果")
	}
	return found, nil
}

// MissingFiles 返回某目录缺失的 v2 必需文件（供安装时给出精确错误）。
func MissingFiles(dir string) []string {
	var missing []string
	for _, f := range RequiredV2Files {
		if _, err := locateFile(dir, f); err != nil {
			missing = append(missing, f)
		}
	}
	return missing
}
