// Command collab-redirect 是一个 NebulaDrive 插件：把协作编辑请求 302 跳转到
// 外部编辑器（OnlyOffice / Collabora / 自建编辑页）。
//
// # 它在链路里的位置
//
// 宿主在 GET /api/files/:id/collab（backend/controllers/collab.go 的 CollabOpen）
// 里触发 onCollabOpen 钩子，然后读 ctx["url"]：
//
//   - 非空字符串 → 302 跳转；
//   - 没有 url   → 本站自己的 md/txt 走 yjs 协作，其它文件返回 no collab handler。
//
// 所以本插件只做一件事：判断"这个文件该不该交给外部编辑器"，该交就写入 url。
// **不该交的时候什么都不写** —— 这不是保守，而是功能正确性的底线：
// 无条件写 url 会把本站自己的 md/txt 协作也一起劫持走（见 matches 的注释）。
//
// # 它不做什么
//
// 不读取文件内容、不代理下载、不签发 token、不做鉴权、不做任何网络请求。
// onCollabOpen 是**同步**钩子：宿主会阻塞等待本插件的返回值（默认 2s 超时），
// 在这里做 I/O 或网络请求会让每个用户"打开文档"变慢甚至超时。
// 外部编辑器需要自己去取文件——那套下载/回调地址是编辑器侧的事，
// 本插件只负责把浏览器带过去（见 README 的"为什么必须用外部编辑器自己的地址"）。
//
// # 配置
//
//	{
//	  "editorURL": "https://office.example.com/editor?fileId={{fileId}}&name={{fileName}}&user={{userName}}",
//	  "extensions": ["docx", "xlsx", "pptx", "odt", "ods", "odp", "doc", "xls", "ppt"],
//	  "mimePrefixes": ["application/vnd.openxmlformats", "application/vnd.oasis.opendocument"],
//	  "appendUserQuery": false,
//	  "openInNewWindow": true
//	}
//
// editorURL 为空 = 插件完全不生效（卸载/停用前的安全默认值）：
// 管理员清空配置后，本站行为立即回到"没装这个插件"。
//
// # 构建
//
//	go build -o collab-redirect .
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把
// 整个后端拖进依赖图，也会造成版本耦合。代价是这段脚手架要抄一遍。
// 本文件只使用标准库。

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int64     `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type initParams struct {
	HostVersion        string          `json:"hostVersion"`
	HostProtocol       int             `json:"hostProtocol"`
	PluginName         string          `json:"pluginName"`
	DataDir            string          `json:"dataDir"`
	Config             json.RawMessage `json:"config"`
	StartTimeoutMillis int             `json:"startTimeoutMillis"`
	SiteURL            string          `json:"siteUrl,omitempty"`
}

type initResult struct {
	OK      bool     `json:"ok"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Hooks   []string `json:"hooks"`
	Error   string   `json:"error,omitempty"`
}

type fireParams struct {
	Hook string         `json:"hook"`
	Ctx  map[string]any `json:"ctx"`
	Seq  int64          `json:"seq"`
}

type fireResult struct {
	CtxPatch map[string]any `json:"ctxPatch,omitempty"`
	Error    string         `json:"error,omitempty"`
}

type logLine struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    int64  `json:"time"`
}

const (
	// version 必须与 plugin.json 的 version 一致：管理端展示清单里的值，
	// 日志里打的是这个常量，两者不一致会让排查时对不上号。
	version = "1.0.0"
	// pluginName 同理，必须与 plugin.json 的 name 一致。
	pluginName = "collab-redirect"

	hookCollabOpen = "onCollabOpen"
	hookCLI        = "onCLI"

	// cliCommand 是 `nebula plugin <cmd>` 里的命令名。
	cliCommand = "collab-redirect"
)

// placeholderNames 是模板里唯一被识别的占位符集合。
//
// 未知占位符**原样保留**而不是替换成空串：模板写错（比如 {{fileNamee}}）
// 时，空串会悄悄生成一个看似正常的 URL，管理员很难发现参数丢了；
// 原样保留的 {{...}} 会明显暴露在 URL 里，同时 init 阶段还会在 stderr 警告。
var placeholderNames = []string{
	"fileId", "fileName", "userName", "userId", "mimeType", "ip",
}

func isKnownPlaceholder(name string) bool {
	for _, n := range placeholderNames {
		if n == name {
			return true
		}
	}
	return false
}

// ---- 配置 ----

// Config 插件配置，字段与文件头的示例一一对应。
type Config struct {
	// EditorURL 外部编辑器地址模板。为空表示插件不生效。
	EditorURL string `json:"editorURL"`
	// Extensions 需要交给外部编辑器的扩展名（小写、不含点）。
	Extensions []string `json:"extensions"`
	// MimePrefixes 同上，按 MIME 前缀匹配。与扩展名是"或"的关系。
	MimePrefixes []string `json:"mimePrefixes"`
	// AppendUserQuery 是否在最终 URL 后追加 ndUser / ndIp。
	// 默认关闭：不是所有编辑器都需要这两个参数，而 URL 会进浏览器历史、
	// 编辑器日志与 Referer，能少带身份信息就少带。
	AppendUserQuery bool `json:"appendUserQuery"`
	// OpenInNewWindow 仅作为配置记录与 CLI 展示。
	// 宿主的跳转实现是 HTTP 302，浏览器必然在当前标签页打开；
	// 本插件无法强制新窗口（见 README 的"已知边界"）。
	OpenInNewWindow bool `json:"openInNewWindow"`
}

func defaultConfig() Config {
	return Config{
		Extensions: []string{"docx", "xlsx", "pptx", "odt", "ods", "odp", "doc", "xls", "ppt"},
		MimePrefixes: []string{
			"application/vnd.openxmlformats",
			"application/vnd.oasis.opendocument",
		},
		AppendUserQuery: false,
		OpenInNewWindow: true,
	}
}

// withDefaults 只补 nil 切片。
//
// 这里用 nil 而不是 len==0 判断，是为了保留管理员显式写 "extensions": []
// 的语义："我只要 MIME 匹配，不要扩展名匹配"。
// 而字段缺失时 json.Unmarshal 不会覆盖 defaultConfig 里已填好的值；
// 显式 null 会置 nil，按"回到默认"处理。
func (c Config) withDefaults() Config {
	d := defaultConfig()
	if c.Extensions == nil {
		c.Extensions = d.Extensions
	}
	if c.MimePrefixes == nil {
		c.MimePrefixes = d.MimePrefixes
	}
	return c
}

// ---- 插件实例 ----

// plugin 保存编译后的配置。
//
// 匹配规则在 init 时就编译成 map/切片：onCollabOpen 在用户打开文档的关键路径上，
// 不该在那里重复做字符串规范化。
type plugin struct {
	cfg          Config
	exts         map[string]bool
	mimePrefixes []string
	// unknown 记录模板里不认识的占位符，供 init 阶段告警。
	unknown []string
}

func newPlugin(cfg Config) *plugin {
	cfg = cfg.withDefaults()
	p := &plugin{
		cfg:  cfg,
		exts: make(map[string]bool, len(cfg.Extensions)),
	}
	for _, e := range cfg.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		e = strings.TrimPrefix(e, ".")
		if e != "" {
			p.exts[e] = true
		}
	}
	for _, m := range cfg.MimePrefixes {
		m = strings.ToLower(strings.TrimSpace(m))
		if m != "" {
			p.mimePrefixes = append(p.mimePrefixes, m)
		}
	}
	// 扫描一次模板，把写错的占位符记下来（真正的告警在 init 里统一发）。
	_, p.unknown = expandTemplate(cfg.EditorURL, func(name string) (string, bool) {
		return "", isKnownPlaceholder(name)
	})
	return p
}

// matches 判断这个文件是否应该交给外部编辑器。
//
// **不命中时 caller 必须什么都不写。** 宿主只有在 ctx["url"] 为空时才
// 回退到本站的 yjs 协作；插件一旦无条件写 url，等于把 md/txt 的在线协作
// 全部劫持到外部编辑器（而外部编辑器未必支持这些格式）。
func (p *plugin) matches(ctx map[string]any) bool {
	// editorURL 为空 → 插件不生效，与"没安装"完全等价。
	if strings.TrimSpace(p.cfg.EditorURL) == "" {
		return false
	}
	name := str(ctx["fileName"])
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext != "" && p.exts[ext] {
		return true
	}
	mime := strings.ToLower(strings.TrimSpace(str(ctx["mimeType"])))
	for _, prefix := range p.mimePrefixes {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	return false
}

// render 展开模板并做安全校验，返回最终跳转地址。
//
// 每一个占位符的值都经 url.QueryEscape 转义。这不是可选项：
// 文件名里完全可能有 &、空格、中文、#。以 & 为例，不转义会把
// "file=报告 & 附表.docx" 变成两个查询参数（附表.docx 成为一个新参数），
// 甚至让攻击者构造文件名注入 editorURL 里的其它参数。
// # 不转义时浏览器还会把 # 之后当成 fragment，参数直接被截断。
func (p *plugin) render(ctx map[string]any) (string, error) {
	if strings.TrimSpace(p.cfg.EditorURL) == "" {
		return "", errors.New("editorURL 未配置")
	}
	rendered, _ := expandTemplate(p.cfg.EditorURL, func(name string) (string, bool) {
		if !isKnownPlaceholder(name) {
			// 返回 false = 原样保留，留给 init 阶段的告警去暴露问题。
			return "", false
		}
		return url.QueryEscape(placeholderValue(name, ctx)), true
	})

	if p.cfg.AppendUserQuery {
		// 用原始值；转义在 appendUserQuery 内部做，保证与占位符同一套规则。
		rendered = appendUserQuery(rendered, str(ctx["userName"]), str(ctx["ip"]))
	}

	if err := validateHTTPURL(rendered); err != nil {
		return "", err
	}
	return rendered, nil
}

// placeholderValue 取占位符对应的原始值（未转义）。
//
// fileId / userId 必须是纯数字：ctx 经 JSON 往返后是 float64，
// 直接 fmt.Sprint 在大 ID 上会输出 "1e+06" 这类科学计数法，
// 外部编辑器拿到的 fileId 就成了错的。
func placeholderValue(name string, ctx map[string]any) string {
	switch name {
	case "fileId":
		return idString(ctx["fileId"])
	case "userId":
		return idString(ctx["userId"])
	case "fileName":
		return str(ctx["fileName"])
	case "userName":
		return str(ctx["userName"])
	case "mimeType":
		return str(ctx["mimeType"])
	case "ip":
		return str(ctx["ip"])
	default:
		return ""
	}
}

// appendUserQuery 在最终 URL 后追加 ndUser / ndIp。
//
// 两个细节：
//  1. 已有查询串用 &，否则用 ?；
//  2. 追加位置在 fragment 之前 —— 若直接拼在末尾，
//     "...#frag" 之后的参数会被浏览器当作 fragment 的一部分，服务端收不到。
func appendUserQuery(raw, userName, ip string) string {
	main, frag := raw, ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		main, frag = raw[:i], raw[i:]
	}
	sep := "?"
	if strings.Contains(main, "?") {
		sep = "&"
	}
	// 模板以 ? 或 & 结尾时不要再补分隔符，避免出现 "?&ndUser="。
	if strings.HasSuffix(main, "?") || strings.HasSuffix(main, "&") {
		sep = ""
	}
	return main + sep + "ndUser=" + url.QueryEscape(userName) +
		"&ndIp=" + url.QueryEscape(ip) + frag
}

// validateHTTPURL 是 302 安全的最后一道闸。
//
// 模板来自管理员配置，写错的可能性很高（比如误填 javascript:、
// 漏了 https://、粘成 //host/path）。宿主拿到 url 会直接
// http.Redirect：javascript: 会被浏览器执行、data: 可以承载 HTML，
// 这些都是把"打开文档"变成 XSS/钓鱼入口。因此只放行
// http/https 且 host 非空的绝对 URL，其余一律不跳转并记 stderr。
//
// 注意：这里**不能**把 url.Parse 的原始 error 写进日志 ——
// *url.Error 的文本会带上完整 URL，可能含 token 等查询参数。
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("渲染结果无法解析: %s", urlErrReason(err))
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("渲染结果的 scheme=%q 不是 http/https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("渲染结果缺少 host（不是绝对 URL）")
	}
	return nil
}

// urlErrReason 只取 url 解析失败的内在原因，避开会带完整 URL 的外层错误。
func urlErrReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return "格式不合法"
}

// expandTemplate 单遍扫描 {{name}}。
//
// 未知占位符与未闭合的 "{{" 都原样保留：宁可让管理员在 URL 里看到
// 明显的 {{foo}}，也不要静默生成一个参数缺失的地址。
// repl 返回 false 表示"不替换"。
func expandTemplate(tmpl string, repl func(name string) (string, bool)) (string, []string) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	var b strings.Builder
	var unknown []string
	seen := map[string]bool{}
	rest := tmpl

	for {
		i := strings.Index(rest, "{{")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		rest = rest[i:]

		j := strings.Index(rest, "}}")
		if j < 0 {
			// 没有闭合，后面不可能再有合法占位符了：整段原样输出。
			b.WriteString(rest)
			break
		}

		name := rest[2:j]
		if v, ok := repl(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString(rest[:j+2])
			if !seen[name] {
				seen[name] = true
				unknown = append(unknown, name)
			}
		}
		rest = rest[j+2:]
	}
	return b.String(), unknown
}

// ---- 钩子处理 ----

// fire 按钩子名分发。宿主只会派发 manifest 里声明过的钩子，
// 但多派了也只回空结果，不算错误。
func (p *plugin) fire(hook string, ctx map[string]any) fireResult {
	switch hook {
	case hookCollabOpen:
		return p.collabOpen(ctx)
	case hookCLI:
		return p.cli(ctx)
	default:
		return fireResult{}
	}
}

// collabOpen 是 onCollabOpen 的实现。
//
// 同步钩子：这里只做纯内存的字符串判断与拼接，绝不做 I/O/网络。
func (p *plugin) collabOpen(ctx map[string]any) fireResult {
	if !p.matches(ctx) {
		// 关键：不命中就返回空结果，ctxPatch 里连 url 键都不能出现。
		return fireResult{}
	}
	target, err := p.render(ctx)
	if err != nil {
		// 配置坏掉时宁可退回本站行为，也不能 302 到不安全/错误的地方。
		logf("error", "onCollabOpen 拒绝跳转（fileId=%s）：%v",
			idString(ctx["fileId"]), err)
		return fireResult{}
	}
	// 日志只打 host 与 path，查询串整体脱敏：editorURL 里可能带 token。
	logf("info", "onCollabOpen 跳转：fileId=%s userId=%s -> %s",
		idString(ctx["fileId"]), idString(ctx["userId"]), redactURL(target))
	return fireResult{CtxPatch: map[string]any{"url": target}}
}

// cli 是 onCLI 的实现：打印生效配置与一个渲染示例，方便管理员核对模板。
func (p *plugin) cli(ctx map[string]any) fireResult {
	if str(ctx["command"]) != cliCommand {
		return fireResult{}
	}
	return fireResult{CtxPatch: map[string]any{"cliOutput": p.describe()}}
}

// describe 生成 CLI 输出。
//
// 这里会打印完整模板与渲染示例：调用者是能执行 `nebula plugin` 的管理员，
// 不看全就无法核对占位符与转义。但**日志**（stderr）仍然只打 host/path，
// 两者面向的读者不同，脱敏策略也不同。
func (p *plugin) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s v%s\n", pluginName, version)
	b.WriteString("生效配置：\n")
	editor := p.cfg.EditorURL
	if strings.TrimSpace(editor) == "" {
		editor = "<未配置>"
	}
	fmt.Fprintf(&b, "  editorURL        : %s\n", editor)
	fmt.Fprintf(&b, "  extensions       : %s\n", joinOrNone(p.cfg.Extensions))
	fmt.Fprintf(&b, "  mimePrefixes     : %s\n", joinOrNone(p.cfg.MimePrefixes))
	fmt.Fprintf(&b, "  appendUserQuery  : %v\n", p.cfg.AppendUserQuery)
	fmt.Fprintf(&b, "  openInNewWindow  : %v（仅记录；宿主 302 固定在当前窗口打开）\n",
		p.cfg.OpenInNewWindow)
	b.WriteString("\n")

	if strings.TrimSpace(p.cfg.EditorURL) == "" {
		b.WriteString("状态：editorURL 为空，插件不生效。\n")
		b.WriteString("      不会改写任何协作打开请求，本站 md/txt 的 yjs 协作保持原样。\n")
		b.WriteString("      填好 editorURL 并重启插件后生效。\n")
		return b.String()
	}

	target, err := p.render(sampleCtx())
	if err != nil {
		fmt.Fprintf(&b, "渲染示例：失败（%v）\n", err)
		b.WriteString("  → 当前配置下 onCollabOpen 不会跳转。\n")
		b.WriteString("  → editorURL 必须是 http/https 绝对地址且 host 非空。\n")
		return b.String()
	}

	b.WriteString("渲染示例（示例 ctx：fileId=42, userId=7, fileName=报告 & 附表.docx）：\n")
	fmt.Fprintf(&b, "  %s\n", target)
	b.WriteString("\n提示：占位符的值按查询参数规则做 URL 转义（空格 → +，& → %26，中文 → %XX），\n")
	b.WriteString("      因此占位符只适合放在查询串里，不要拼进 host 或 path。\n")
	return b.String()
}

// ---- 初始化 ----

// initPlugin 解析宿主下发的初始化参数。
//
// 返回的 *plugin 为 nil 表示初始化失败（宿主会卸载插件）。
func initPlugin(p initParams) (initResult, *plugin) {
	if p.HostProtocol != 1 {
		// 明确失败比"勉强跑起来"好：协议不匹配时静默降级会出难查的问题。
		return initResult{OK: false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol)}, nil
	}

	cfg := defaultConfig()
	if len(p.Config) > 0 {
		// 配置非法时退回默认而不是拒绝启动：插件不该因为一个字段笔误
		// 就完全不可用。但必须说清楚"现在不生效"。
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			logf("warn", "配置解析失败，使用默认配置（editorURL 为空 → 插件不生效）: %v", err)
			cfg = defaultConfig()
		}
	}
	pl := newPlugin(cfg)

	logf("info", "初始化：hostVersion=%s dataDir=%s", p.HostVersion, p.DataDir)

	if strings.TrimSpace(pl.cfg.EditorURL) == "" {
		logf("warn", "editorURL 为空：collab-redirect 不生效（不会改写任何协作打开请求）")
	} else {
		if len(pl.unknown) > 0 {
			logf("warn", "editorURL 含未知占位符 %s，将原样保留。可用占位符：%s",
				"{{"+strings.Join(pl.unknown, "}}、{{")+"}}",
				strings.Join(placeholderNames, ", "))
		}
		// 用示例 ctx 预热一次：模板写错（漏 scheme、js 伪协议、host 为空）
		// 在 init 阶段就能看到，而不是等第一个用户点开文档才失败。
		if sample, err := pl.render(sampleCtx()); err != nil {
			logf("error", "editorURL 的渲染示例不可用，onCollabOpen 将一律不跳转: %v", err)
		} else {
			logf("info", "示例跳转地址校验通过：%s（查询串已脱敏）", redactURL(sample))
		}
	}

	logf("info", "collab-redirect 已就绪：extensions=%d 个，mimePrefixes=%d 条，"+
		"appendUserQuery=%v，openInNewWindow=%v",
		len(pl.exts), len(pl.mimePrefixes), pl.cfg.AppendUserQuery, pl.cfg.OpenInNewWindow)

	return initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookCollabOpen, hookCLI},
	}, pl
}

// sampleCtx 是 CLI 渲染示例用的 ctx，刻意用 float64 模拟 JSON 往返，
// 并让文件名带上 & 与空格，好让管理员一眼看出转义是否正确。
func sampleCtx() map[string]any {
	return map[string]any{
		"userId":   float64(7),
		"userName": "张三",
		"fileId":   float64(42),
		"fileName": "报告 & 附表.docx",
		"mimeType": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"ip":       "203.0.113.7",
	}
}

// ---- 主循环 ----

// active 是当前生效的插件实例。协议循环是单线程的，init 只会在启动时到达，
// 因此不需要额外加锁。init 之前用默认配置（editorURL 为空）兜底。
var active = newPlugin(defaultConfig())

func main() {
	sc := bufio.NewScanner(os.Stdin)
	// 上限 8MB：正常 RPC 行只有几百字节，留足余量。
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			// 宿主写出的行必须是合法 JSON；解析不了说明协议层有问题，
			// 但插件不能因此退出（否则宿主看不到原因）。
			logf("error", "无法解析请求: %v", err)
			continue
		}
		switch req.Method {
		case "init":
			handleInit(req)
		case "fire":
			handleFire(req)
		case "ping":
			reply(req, map[string]any{"pong": true}, nil)
		case "shutdown":
			reply(req, map[string]any{"bye": true}, nil)
			return
		default:
			replyErr(req, -32601, "不支持的方法: "+req.Method)
		}
	}
}

func handleInit(req request) {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return
	}
	res, pl := initPlugin(p)
	if pl != nil {
		active = pl
	}
	reply(req, res, nil)
}

func handleFire(req request) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}
	reply(req, active.fire(p.Hook, p.Ctx), nil)
}

// ---- 工具 ----

// idString 把 ctx 里的数字 ID 转成十进制字符串。
//
// ctx 经 JSON 往返后数字一律是 float64。直接 fmt.Sprint 在 1e6 以上的 ID
// 会输出 "1e+06"；先转整数再格式化才稳定。
func idString(v any) string {
	switch n := v.(type) {
	case float64:
		if n >= 0 && n == math.Trunc(n) && n <= 1<<53 {
			return strconv.FormatInt(int64(n), 10)
		}
	case int:
		return strconv.FormatInt(int64(n), 10)
	case int64:
		return strconv.FormatInt(n, 10)
	case uint:
		return strconv.FormatUint(uint64(n), 10)
	case uint64:
		return strconv.FormatUint(n, 10)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return strconv.FormatInt(i, 10)
		}
	}
	return str(v)
}

// redactURL 生成可写进日志的地址：只保留 scheme://host/path，查询串与
// fragment 整体脱敏。editorURL 里常常带 token/signature，整条打日志
// 等于把凭据交给每一个能看插件日志的人。
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable-url>"
	}
	out := u.Scheme + "://" + u.Host + u.Path
	if u.RawQuery != "" {
		out += "?<redacted>"
	}
	if u.Fragment != "" {
		out += "#<redacted>"
	}
	return out
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "<空>"
	}
	return strings.Join(items, ", ")
}

func reply(req request, result any, rerr *rpcError) {
	out := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	b, err := json.Marshal(out)
	if err != nil {
		logf("error", "序列化响应失败: %v", err)
		return
	}
	os.Stdout.Write(append(b, '\n'))
}

func replyErr(req request, code int, msg string) {
	reply(req, nil, &rpcError{Code: code, Message: msg})
}

func logf(level, format string, args ...any) {
	// 日志必须走 stderr：stdout 是 RPC 专用通道。
	rec := logLine{
		Level:   level,
		Message: fmt.Sprintf(format, args...),
		Time:    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", level, fmt.Sprintf(format, args...))
		return
	}
	os.Stderr.Write(append(b, '\n'))
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
