// Command referer-guard 是一个 NebulaDrive 示例插件：防盗链。
//
// 它实现 onAntiLeech 钩子 —— 目前宿主唯一下载拦截点。
// 当下载请求的 Referer 不在允许列表内时，插件返回含 "blocked"
// 的 error，请求被宿主以 403 拒绝。
//
// 用法（安装后配置 JSON）：
//
//	{
//	  "allowedReferers": ["https://example.com", "https://*.corp.example.com"],
//	  "blockEmptyReferer": true,
//	  "allowedExtensions": ["zip", "mp4"],
//	  "exemptUsers": [1],
//	  "logDownloads": true
//	}
//
// 构建：go build -o referer-guard .
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把
// 整个后端拖进依赖图，也会造成版本耦合。协议结构在此处独立声明。

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
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type initParams struct {
	HostVersion        string          `json:"hostVersion"`
	HostProtocol       int             `json:"hostProtocol"`
	PluginName         string          `json:"pluginName"`
	DataDir            string          `json:"dataDir"`
	Config             json.RawMessage `json:"config"`
	StartTimeoutMillis int             `json:"startTimeoutMillis"`
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
	version   = "1.0.0"
	hookAnti  = "onAntiLeech"
	// logFile 记录下载审计的文件名（在插件自己的数据目录里）
	logFile = "downloads.log"
)

// Config 插件配置。
type Config struct {
	// AllowedReferers 允许的 Referer 前缀/通配模式。为空则不检查。
	AllowedReferers []string `json:"allowedReferers"`
	// BlockEmptyReferer 无 Referer 时是否拦截。
	// 默认（false）放行：很多下载工具（curl/wget）不带 Referer，
	// 一律拦截会打断管理员自己用命令行取文件。
	BlockEmptyReferer bool `json:"blockEmptyReferer"`
	// AllowedExtensions 仅这些扩展名受检查。为空表示全部检查。
	// 例：只想保护视频与压缩包，就填 ["mp4","mkv","zip"]。
	AllowedExtensions []string `json:"allowedExtensions"`
	// ExemptUsers 这些用户 ID 永不被拦（放行下载任意文件的管理员）。
	ExemptUsers []uint `json:"exemptUsers"`
	// LogDownloads 是否把每次判定写进插件数据目录的 downloads.log。
	LogDownloads bool `json:"logDownloads"`
}

// compiled 预编译好的匹配器（避免每请求重编译正则 —— 那是 DoS 面）。
type compiled struct {
	cfg       Config
	patterns  []*regexp.Regexp
	exts      map[string]bool
	exempt    map[uint64]bool
	logHandle *os.File
}

var g *compiled

func main() {
	g = &compiled{}

	sc := bufio.NewScanner(os.Stdin)
	// 上限 8MB：正常 RPC 行只有几百字节
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
			g.closeLog()
			return
		default:
			replyErr(req, -32601, "不支持的方法: "+req.Method)
		}
	}
	g.closeLog()
}

func handleInit(req request) {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return
	}
	if p.HostProtocol != 1 {
		// 明确失败比"勉强跑起来"好：协议不匹配时静默降级会出难查的问题
		reply(req, initResult{OK: false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol)}, nil)
		return
	}

	cfg := defaultConfig()
	if len(p.Config) > 0 {
		// 配置非法时退回默认而不是拒绝启动：插件不该因为配置笔误
		// 就完全不可用。但要在日志里说清楚。
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			logf("warn", "配置解析失败，使用默认配置: %v", err)
			cfg = defaultConfig()
		}
	}

	c, err := build(&cfg)
	if err != nil {
		reply(req, initResult{OK: false, Error: "配置无效: " + err.Error()}, nil)
		return
	}
	g = c

	if c.cfg.LogDownloads {
		f, err := os.OpenFile(filepath.Join(p.DataDir, logFile),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			// 审计日志开不了不该阻断防盗链本身，降级为仅 stderr
			logf("warn", "无法打开审计日志（仍会拦截，仅缺审计记录）: %v", err)
		} else {
			c.logHandle = f
		}
	}

	logf("info", "referer-guard 已就绪：%d 条 Referer 规则, %d 个扩展名, %d 个豁免用户",
		len(c.patterns), len(c.exts), len(c.exempt))

	reply(req, initResult{
		OK:      true,
		Name:    "referer-guard",
		Version: version,
		Hooks:   []string{hookAnti},
	}, nil)
}

func handleFire(req request) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}
	if p.Hook != hookAnti {
		reply(req, fireResult{}, nil)
		return
	}
	verdict, msg := g.check(p.Ctx)

	if g.cfg.LogDownloads {
		g.audit(p.Ctx, verdict, msg)
	}
	if verdict != allow {
		logf("warn", "拦截下载 %v（user=%v ip=%v ref=%q）: %s",
			str(p.Ctx["fileName"]), str(p.Ctx["userId"]),
			str(p.Ctx["ip"]), str(p.Ctx["referer"]), msg)
	}

	res := fireResult{}
	if verdict == block {
		// 关键：必须含 "blocked" 子串，宿主据此返回 403
		res.Error = "blocked by referer-guard: " + msg
	}
	// 告诉宿主判定结果，便于管理端观测（不影响拦截逻辑）
	res.CtxPatch = map[string]any{"refererGuard": verdict.String()}
	reply(req, res, nil)
}

type verdict int

const (
	allow verdict = iota
	block
)

func (v verdict) String() string {
	if v == block {
		return "blocked"
	}
	return "allowed"
}

func (c *compiled) check(ctx map[string]any) (verdict, string) {
	// 1. 用户豁免
	//
	// 注意类型：ctx 经 JSON 往返后数字一律是 float64，
	// 这里断言 uint 会永远失败（看起来"豁免不生效"）。
	// 兼容 float64 / int / json.Number 三种来源。
	if id, ok := numOf(ctx["userId"]); ok {
		if c.exempt[id] {
			return allow, "用户豁免"
		}
	}
	// 2. 扩展名范围
	name := str(ctx["fileName"])
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), "."); ext != "" {
		if len(c.exts) > 0 && !c.exts[ext] {
			return allow, "扩展名不在检查范围"
		}
	}
	// 3. 无 Referer
	ref := strings.TrimSpace(str(ctx["referer"]))
	if ref == "" {
		if c.cfg.BlockEmptyReferer {
			return block, "缺少 Referer 头"
		}
		return allow, "无 Referer，按配置放行"
	}
	// 4. 规则匹配
	if len(c.patterns) == 0 {
		return allow, "未配置 Referer 规则"
	}
	for _, re := range c.patterns {
		if re.MatchString(ref) {
			return allow, "命中允许规则"
		}
	}
	return block, "Referer 不在允许列表: " + ref
}

// numOf 把 ctx 里的数字字段转成 uint64。
//
// ctx 是 map[string]any，经 JSON 反序列化后数字全是 float64。
// 直接写 ctx["userId"].(uint) 会永远失败 —— 这类"静默不生效"的 bug
// 极难察觉：代码没报错，功能就是不工作。
func numOf(v any) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case uint64:
		return n, true
	case uint:
		return uint64(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= 0 {
			return uint64(i), true
		}
	}
	return 0, false
}

func (c *compiled) audit(ctx map[string]any, v verdict, msg string) {
	if c.logHandle == nil {
		return
	}
	rec := map[string]any{
		"time":     time.Now().Format(time.RFC3339),
		"userId":   ctx["userId"],
		"userName": ctx["userName"],
		"ip":       ctx["ip"],
		"fileId":   ctx["fileId"],
		"fileName": ctx["fileName"],
		"referer":  ctx["referer"],
		"verdict":  v.String(),
		"reason":   msg,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	c.logHandle.Write(append(b, '\n'))
}

func (c *compiled) closeLog() {
	if c.logHandle != nil {
		c.logHandle.Close()
		c.logHandle = nil
	}
}

// build 把配置编译成匹配器。
func build(cfg *Config) (*compiled, error) {
	c := &compiled{cfg: *cfg, exts: map[string]bool{}, exempt: map[uint64]bool{}}
	for _, p := range cfg.AllowedReferers {
		if strings.TrimSpace(p) == "" {
			continue
		}
		re, err := compilePattern(p)
		if err != nil {
			return nil, fmt.Errorf("allowedReferers 项 %q 无效: %w", p, err)
		}
		c.patterns = append(c.patterns, re)
	}
	for _, e := range cfg.AllowedExtensions {
		e = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(e, ".")))
		if e != "" {
			c.exts[e] = true
		}
	}
	for _, u := range cfg.ExemptUsers {
		c.exempt[uint64(u)] = true
	}
	return c, nil
}

// compilePattern 支持三种写法：
//   - "https://a.com"           → 前缀匹配
//   - "*.a.com"                 → 匹配 a.com 本身与任意深度子域
//   - "re:^https://.*\.a\.com$" → 显式正则
func compilePattern(p string) (*regexp.Regexp, error) {
	if strings.HasPrefix(p, "re:") {
		return regexp.Compile(p[3:])
	}
	if strings.HasPrefix(p, "*.") {
		// "*.a.com" → 匹配 a.com、x.a.com、x.y.a.com，可带端口与路径
		host := regexp.QuoteMeta(strings.TrimPrefix(p[1:], "."))
		return regexp.Compile(`^(https?://)?([^/]*\.)?` + host + `(:\d+)?(/.*)?$`)
	}
	// 前缀匹配：转义后加 .* 尾巴
	return regexp.Compile(`^` + regexp.QuoteMeta(p) + `.*$`)
}

// defaultConfig 默认放行本机（便于开箱可用）。
//
// 这里用 "*.localhost" 与显式端口，而不是 "http://localhost:*" ——
// 前者是本文件支持的通配写法；后者里的 * 会被当作正则元字符
// （* 前无元素 = 无效正则），编译期不报错、运行时永不匹配。
// 这类"看起来合理实际不工作"的配置最容易浪费排查时间。
func defaultConfig() Config {
	return Config{
		AllowedReferers:  []string{"*.localhost", "127.0.0.1", "*.127.0.0.1"},
		BlockEmptyReferer: false,
		LogDownloads:     true,
	}
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
	// 日志必须走 stderr：stdout 是 RPC 专用通道
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
