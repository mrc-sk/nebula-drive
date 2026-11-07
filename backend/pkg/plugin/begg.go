package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// co.BEGG 是 v2 插件的「运行时 + 页面引导 + UI 扩展点」声明文件。
//
// 与 plugin.json（身份/钩子/权限）、register.go（纯声明、不编译）并列，
// 三者构成 v2 插件的最小契约。目录层级不限，宿主递归定位。
//
// 设计要点：
//   - PHP 只是 runtime 的一种。宿主不内置任何解释器，只做 LookPath 探测；
//     缺哪个就在装载时明确拒绝并报出名字。
//   - 插件声明 listen=0.0.0.0 时，宿主**实际只绑 127.0.0.1**（收紧），
//     0.0.0.0 仅作为「需要替换成对外地址」的标记，用来生成前端可访问 URL。
//   - 外部 https:// 页面不反代，由前端 iframe 直连。

// BeggFileName co.BEGG 的固定文件名。
const BeggFileName = "co.BEGG"

// Begg co.BEGG 的内容（JSON）。
type Begg struct {
	// Runtime 运行方式：php | python | node | go | binary | external。
	// binary 表示 entry 本身就是可执行文件；external 表示纯外部页面。
	Runtime string `json:"runtime"`

	// Entry 入口文件（相对插件根目录）。runtime=php/python/node 时是脚本，
	// runtime=binary 时是可执行文件。
	Entry string `json:"entry,omitempty"`

	// Home 主页文件（相对插件根目录），如 "ASS/in.php"。
	// 决定侧边栏点开后访问哪个页面。
	Home string `json:"home,omitempty"`

	// Listen 声明的监听地址。0.0.0.0 会按「域名 → 绑定 IP → 默认 IP」替换。
	// 也可以直接写 https://external.example.com/app 表示外部页面。
	Listen string `json:"listen,omitempty"`

	// Port 固定端口；0（默认）表示由宿主动态分配空闲端口。
	Port int `json:"port,omitempty"`

	// Requires 解释器要求，如 ["php>=8.1", "python3>=3.9", "node"]。
	// 宿主用 LookPath 探测，缺失即拒绝装载。
	Requires []string `json:"requires,omitempty"`

	// Command 启动命令模板，支持占位符：
	//   {port} {host} {dir} {entry} {home} {static}
	// 留空时按 Runtime 生成默认命令（见 DefaultCommand）。
	Command string `json:"command,omitempty"`

	// Static 静态资源根目录（php -S 的 -t 参数），默认取 Home 所在目录。
	Static string `json:"static,omitempty"`

	// Env 附加环境变量（不用于传密钥，插件配置走 init 的 Config）。
	Env map[string]string `json:"env,omitempty"`

	// Pages 插件注册的 HTTP 页面。
	Pages []PageDecl `json:"pages,omitempty"`

	// UI 侧边栏 / 主题 / 设置面板声明。
	UI *UIDecl `json:"ui,omitempty"`

	// Providers 静态注册的注册型能力（也可由 init 握手动态上报）。
	Providers []Registration `json:"providers,omitempty"`
}

// RuntimeBin 把逻辑 runtime 名映射到要 LookPath 的可执行文件。
// binary / external 不需要解释器。
var RuntimeBin = map[string][]string{
	"php":      {"php"},
	"python":   {"python3", "python"},
	"node":     {"node"},
	"go":       {"go"},
	"binary":   {},
	"external": {},
}

// ParseBegg 解析并校验 co.BEGG 内容。
func ParseBegg(raw []byte) (*Begg, error) {
	if len(raw) == 0 {
		return nil, errors.New("co.BEGG 为空")
	}
	if len(raw) > 64<<10 {
		return nil, errors.New("co.BEGG 超过 64KB 上限")
	}
	var b Begg
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("co.BEGG 解析失败: %w", err)
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &b, nil
}

// Validate 校验字段合法性（不涉及文件系统）。
func (b *Begg) Validate() error {
	if b.Runtime == "" {
		return errors.New("co.BEGG 缺少 runtime（php|python|node|go|binary|external）")
	}
	if _, ok := RuntimeBin[b.Runtime]; !ok {
		return fmt.Errorf("co.BEGG 的 runtime %q 不支持，可选 php|python|node|go|binary|external", b.Runtime)
	}
	if b.Port < 0 || b.Port > 65535 {
		return fmt.Errorf("co.BEGG 的 port %d 非法", b.Port)
	}
	if b.Runtime == "external" && len(b.Pages) == 0 {
		return errors.New("runtime=external 时必须在 pages 里声明带 URL 的页面")
	}
	for _, path := range []string{b.Entry, b.Home, b.Static} {
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) || strings.Contains(path, "..") {
			return fmt.Errorf("co.BEGG 路径 %q 必须是插件目录内的相对路径", path)
		}
	}
	for _, p := range b.Pages {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("co.BEGG pages: %w", err)
		}
	}
	for _, r := range b.Providers {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("co.BEGG providers: %w", err)
		}
	}
	return nil
}

// ValidateFiles 校验页面文件确实存在于 pageDir（co.BEGG 所在目录）内（防越界）。
//
// 故意不校验 entry：它是 JSON-RPC 子进程入口，由 Manifest.ExecPath 相对
// **插件安装根目录** 解析，和页面文件的基准目录不是同一个锚点。
// 混在一起校验会把「co.BEGG 放在 ASS/、可执行文件放在插件根」的合法布局误判为损坏。
func (b *Begg) ValidateFiles(pageDir string) error {
	for _, f := range []struct{ label, path string }{
		{"home", b.Home}, {"static", b.Static},
	} {
		label, path := f.label, f.path
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(pageDir, path))
		if err != nil {
			return fmt.Errorf("co.BEGG %s 路径解析失败: %w", label, err)
		}
		root, _ := filepath.Abs(pageDir)
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return fmt.Errorf("co.BEGG %s 越界: %q", label, path)
		}
		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("co.BEGG %s 指向的文件不存在: %q（相对 co.BEGG 所在目录）", label, path)
		}
	}
	return nil
}

// requireRe 解析 "php>=8.1" / "python3" / "node>=18"。
var requireRe = regexp.MustCompile(`^([a-zA-Z0-9_.-]+)\s*(>=|>|==)?\s*([0-9][0-9.]*)?$`)

// ProbeRuntime 探测解释器是否可用。
//
// 按用户要求：系统里有解释器就放行，没有就**在装载时直接报告缺哪个**，
// 不静默降级、不尝试自动安装。版本约束只做尽力检查（能取到版本才比），
// 取不到版本不影响装载 —— 避免不同解释器 --version 输出格式差异导致误拒。
func (b *Begg) ProbeRuntime() (map[string]string, error) {
	if b.Runtime == "binary" || b.Runtime == "external" {
		return map[string]string{}, nil
	}
	found := map[string]string{}
	candidates := RuntimeBin[b.Runtime]
	for _, req := range b.Requires {
		m := requireRe.FindStringSubmatch(strings.TrimSpace(req))
		if m == nil {
			return nil, fmt.Errorf("co.BEGG requires 项 %q 格式非法（示例 php>=8.1 / python3 / node>=18）", req)
		}
		name := m[1]
		path, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("缺少解释器 %q（co.BEGG requires: %s）。"+
				"请在服务器安装后重新启用插件；宿主不会自动安装，也不做静默降级", name, req)
		}
		found[name] = path
	}
	// 没有显式 requires 时，runtime 自身对应的可执行文件必须存在。
	if len(b.Requires) == 0 {
		var lastErr error
		for _, name := range candidates {
			if path, err := exec.LookPath(name); err == nil {
				found[name] = path
				lastErr = nil
				break
			} else {
				lastErr = err
			}
		}
		if lastErr != nil && len(found) == 0 {
			return nil, fmt.Errorf("缺少解释器：runtime=%s 需要 %v 之一，"+
				"当前系统均不可用。请在服务器安装后重新启用插件", b.Runtime, candidates)
		}
	}
	return found, nil
}

// StaticDir 返回插件 HTTP 服务的文档根目录（相对 co.BEGG 所在目录）。
//
// 未显式声明 static 时取 home 所在目录；两者都为空则取 co.BEGG 所在目录本身。
func (b *Begg) StaticDir() string {
	if b.Static != "" {
		return filepath.ToSlash(filepath.Clean(b.Static))
	}
	if b.Home != "" {
		d := filepath.ToSlash(filepath.Clean(filepath.Dir(b.Home)))
		if d != "" {
			return d
		}
	}
	return "."
}

// PagePath 把「相对 co.BEGG 所在目录」的页面文件路径，
// 转换成「相对 HTTP 文档根」的 URL 路径。
//
// 为什么需要这一步：PHP 用 `php -S ... -t ASS` 启动后，文档根已经是 ASS/，
// 此时浏览器请求 /ASS/in.php 会在 ASS/ASS/in.php 找文件，必然 404。
// 对外 URL 与反代路径必须都去掉 static 前缀。
//
//	home="ASS/in.php", static="ASS"  → "/in.php"
//	home="in.php",     static="."    → "/in.php"
//	home=""                          → "/"
func (b *Begg) PagePath(home string) string {
	if home == "" {
		return "/"
	}
	rel := filepath.ToSlash(filepath.Clean(home))
	static := b.StaticDir()
	if static != "." {
		prefix := strings.TrimSuffix(static, "/") + "/"
		switch {
		case strings.HasPrefix(rel, prefix):
			rel = strings.TrimPrefix(rel, prefix)
		case rel == strings.TrimSuffix(static, "/"):
			rel = "."
		}
	}
	if rel == "" || rel == "." {
		return "/"
	}
	return "/" + strings.TrimPrefix(rel, "/")
}

// DefaultCommand 按 runtime 生成默认启动命令。
//
// 模板里的 {static} 等占位符由 RenderCommand 统一展开（它会调用
// StaticDir()），这里只负责选模板。
func (b *Begg) DefaultCommand() string {
	switch b.Runtime {
	case "php":
		return "php -S {host}:{port} -t {static}"
	case "python":
		return "python3 {entry}"
	case "node":
		return "node {entry}"
	case "go":
		return "go run {entry}"
	case "binary":
		return "{entry}"
	default:
		return ""
	}
}

// RenderCommand 展开命令模板占位符，返回 (程序名, 参数列表)。
func (b *Begg) RenderCommand(dir string, port int, host string) (string, []string, error) {
	tpl := b.Command
	if tpl == "" {
		tpl = b.DefaultCommand()
	}
	if tpl == "" {
		return "", nil, errors.New("co.BEGG 未提供 command 且 runtime 无默认命令")
	}
	replacer := strings.NewReplacer(
		"{port}", strconv.Itoa(port),
		"{host}", host,
		"{dir}", dir,
		"{entry}", b.Entry,
		"{home}", b.Home,
		"{static}", b.StaticDir(),
	)
	rendered := replacer.Replace(tpl)
	parts := strings.Fields(rendered)
	if len(parts) == 0 {
		return "", nil, errors.New("co.BEGG command 展开后为空")
	}
	return parts[0], parts[1:], nil
}

// AllocatePort 由宿主分配一个空闲的本地端口。
//
// 插件始终绑 127.0.0.1 —— 即便它声明 0.0.0.0。声明里的 0.0.0.0
// 只用于生成对外 URL，不用于实际监听，避免插件端口直接暴露公网。
func AllocatePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// PluginEnv 下发给插件进程的环境变量。
// 插件通过这些变量得知宿主分配的端口与自己的页面信息。
func PluginEnv(name, dir, home, siteURL string, port int) []string {
	base := os.Environ()
	return append(base,
		"NB_PLUGIN_NAME="+name,
		"NB_PLUGIN_DIR="+dir,
		"NB_PLUGIN_HOME="+home,
		"NB_PLUGIN_SITE_URL="+siteURL,
		"NB_PLUGIN_PORT="+strconv.Itoa(port),
		"NB_PLUGIN_HOST=127.0.0.1",
		"NB_PLUGIN_PROTOCOL="+strconv.Itoa(ProtocolVersion),
	)
}

// IsExternal 判断 listen 是否为外部 https/http 页面（不反代）。
func (b *Begg) IsExternal() bool {
	return b.Runtime == "external" ||
		strings.HasPrefix(b.Listen, "http://") ||
		strings.HasPrefix(b.Listen, "https://")
}

// PublicHost 按用户的三条规则选对外主机名：
//
//  1. 后台绑定了域名 → 用域名；
//  2. 只绑定了 IP → 用 IP；
//  3. 什么都没绑定 → 默认使用 IP（127.0.0.1）。
//
// 0.0.0.0 是通配绑定地址、浏览器无法访问，因此只作为「需要替换」的标记。
func PublicHost(domain, boundIP string) string {
	domain = normalizeHost(domain)
	if domain != "" {
		return domain
	}
	boundIP = normalizeHost(boundIP)
	if boundIP != "" {
		return boundIP
	}
	return "127.0.0.1"
}

func normalizeHost(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// ResolveExternalBase 生成插件页面对浏览器可见的基址。
//
// 返回值 base 形如 "https://drive.example.com/ext/ass"，
// external=true 表示这是外部 URL、宿主不需要反代。
//
// siteURL 由 pluginset.siteURL() 提供（后台绑定域名时最准）；
// listenAddr 是宿主自己的监听地址，用于从中取端口。
func ResolveExternalBase(b *Begg, pluginName, siteURL, domain, boundIP, listenAddr string) (base string, external bool) {
	if b.IsExternal() && strings.HasPrefix(b.Listen, "http") {
		return strings.TrimRight(b.Listen, "/"), true
	}
	host := PublicHost(domain, boundIP)
	port := ""
	if i := strings.LastIndexByte(listenAddr, ':'); i >= 0 {
		port = listenAddr[i:]
	}
	scheme := "http"
	if strings.HasPrefix(siteURL, "https://") {
		scheme = "https"
	}
	if siteURL != "" {
		// 后台已配站点根 URL：直接用它，避免端口被重复拼
		return strings.TrimRight(siteURL, "/") + "/ext/" + pluginName, false
	}
	if strings.Contains(host, ":") {
		port = ""
	}
	return scheme + "://" + host + port + "/ext/" + pluginName, false
}
