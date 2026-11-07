// Command hmac-auth 是一个 NebulaDrive 插件：为开放 API 提供 HMAC 签名鉴权。
//
// # 状态：已实现
//
// 宿主侧 onApiAuth 在 middleware/api_auth.go 有真实调用点（内置鉴权
// PAT / OAuth2 失败后触发），本插件负责校验签名并声明调用者身份。
//
// # 待签名规范串（**与客户端必须完全一致**）
//
//	METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
//
// 五行，缺一不可 —— 少任何一行，攻击者就能在不改签名的前提下替换那部分内容。
// METHOD 统一大写；BODY_SHA256_HEX 是请求体 sha256 的小写十六进制
// （空 body 也要算，即 e3b0c442…）。
// 签名 = hex(HMAC-SHA256(sharedSecret, 规范串))，放在 signatureHeader 里。
//
// # 三条安全红线
//
//  1. **未配置 sharedSecret / principalUserId 时一律拒绝** ——
//     没有密钥就不存在鉴权，放行等于敞开；没有 principalUserId
//     就无法认定身份，而这比"随便挑一个用户"安全得多。
//  2. **常量时间比较**（hmac.Equal）。用 `!=` 会在第一个不同字节处返回，
//     攻击者可以逐字节猜出正确签名。
//  3. **缺签名头也拒绝**，而不是"没意见"地退回 401 ——
//     否则任何人都能靠不带签名头绕过这层保护。
//     需要放行的端点请显式写进 exemptPaths。
//
// # 时间窗是防重放的唯一手段
//
// 签名本身是静态的：没有时间窗，截获的签名可以永远重放。
// toleranceSeconds 默认 300，设 0 表示不校验（不建议）。
//
// # 配置（安装后在插件页填写）
//
//	{
//	  "sharedSecret": "********",
//	  "principalUserId": 1,
//	  "signatureHeader": "X-Nebula-Signature",
//	  "timestampHeader": "X-Nebula-Timestamp",
//	  "algorithm": "sha256",
//	  "toleranceSeconds": 300,
//	  "exemptPaths": ["/api/v1/health"]
//	}
//
// # 构建
//
//	go build -o hmac-auth .
package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把
// 整个后端拖进依赖图，也会造成版本耦合。
//
// 代价是每个插件都要抄一遍这段脚手架。将来值得抽一个
// plugin-sdk module（不属于今天占位的范围）。

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
	version     = "0.1.0"
	hookAPIAuth = "onApiAuth"
	pluginName  = "hmac-auth"
)

// ---- 配置 ----

// Config 插件配置，字段与文件头的示例一一对应。
type Config struct {
	// SharedSecret 与调用方共享的密钥。
	//
	// 不进日志：插件日志会被宿主收进环形缓冲并展示在管理端。
	// 为空时**一律拒绝**（没有密钥就不存在鉴权，放行等于敞开）。
	SharedSecret string `json:"sharedSecret"`
	// PrincipalUserID 签名通过后代表哪个用户。
	//
	// HMAC 只能证明"持有密钥"，证明不了"是谁"。要让宿主认定调用者身份，
	// 必须显式指定一个用户；为空时同样拒绝 —— 否则就没法放行，
	// 而这比"随便挑一个用户"安全得多。
	PrincipalUserID uint `json:"principalUserId"`
	// SignatureHeader 签名所在的请求头。默认 X-Nebula-Signature（十六进制）。
	SignatureHeader string `json:"signatureHeader"`
	// TimestampHeader 时间戳所在的请求头，Unix 秒。默认 X-Nebula-Timestamp。
	TimestampHeader string `json:"timestampHeader"`
	// Algorithm 摘要算法，首批只支持 sha256。
	Algorithm string `json:"algorithm"`
	// ToleranceSeconds 允许的时钟偏移（防重放）。默认 300；设为 0 表示不校验时间。
	ToleranceSeconds int `json:"toleranceSeconds"`
	// ExemptPaths 免签的路径（或其子路径），便于放行健康检查等公开端点。
	//
	// 匹配按**路径段边界**：配置 /api/v1/health 能匹配 /api/v1/health 与
	// /api/v1/health/live，但**不**匹配 /api/v1/healthcheck。
	ExemptPaths []string `json:"exemptPaths"`
}

func defaultConfig() Config {
	return Config{
		SignatureHeader:  "X-Nebula-Signature",
		TimestampHeader:  "X-Nebula-Timestamp",
		Algorithm:        "sha256",
		ToleranceSeconds: 300,
	}
}

func (c Config) withDefaults() Config {
	d := defaultConfig()
	if c.SignatureHeader == "" {
		c.SignatureHeader = d.SignatureHeader
	}
	if c.TimestampHeader == "" {
		c.TimestampHeader = d.TimestampHeader
	}
	if c.Algorithm == "" {
		c.Algorithm = d.Algorithm
	}
	return c
}

// redacted 返回可安全写进日志的配置摘要。
func (c Config) redacted() string {
	return fmt.Sprintf("sigHeader=%q tsHeader=%q algo=%q tolerance=%ds principalUserId=%d secret=%s exemptPaths=%d 条",
		c.SignatureHeader, c.TimestampHeader, c.Algorithm, c.ToleranceSeconds,
		c.PrincipalUserID, maskSecret(c.SharedSecret), len(c.ExemptPaths))
}

func maskSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	return fmt.Sprintf("<set:%d chars>", len(s))
}

// validate 返回配置里缺失或可疑的项（只报告，不阻止启动）。
//
// 只报告不失败的理由：管理员可能正在逐步补配置，不该因此启不起来
// （那样连日志都看不到）。真正的兜底在 verify() —— 投递时 fail closed。
func (c Config) validate() []string {
	var problems []string
	if strings.TrimSpace(c.SharedSecret) == "" {
		problems = append(problems, "缺 sharedSecret（没有密钥就无法校验签名，所有请求都会被拒）")
	}
	if c.PrincipalUserID == 0 {
		problems = append(problems, "缺 principalUserId（无法确定签名代表哪个用户，所有请求都会被拒）")
	}
	if c.Algorithm != "sha256" {
		problems = append(problems, fmt.Sprintf("algorithm=%q 暂不支持（首批仅 sha256）", c.Algorithm))
	}
	if c.ToleranceSeconds < 0 {
		problems = append(problems, fmt.Sprintf("toleranceSeconds=%d 非法", c.ToleranceSeconds))
	}
	return problems
}

// ---- 签名校验 ----

// canonicalString 组装待签名的规范串。
//
// 每行一个字段，顺序固定。**任何字段都不能省略** ——
// 尤其是 bodyHash：省掉它，攻击者就能把请求体换成任意内容而签名依然成立。
//
// 调用方（客户端）必须用完全相同的规则拼串：
//
//	METHOD\nPATH\nQUERY\nTIMESTAMP\nBODY_SHA256_HEX
//
// 字段内部不允许出现 \n（method/path/query 天然不含，timestamp 是数字）。
func canonicalString(method, path, query, timestamp, bodyHashHex string) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		query,
		timestamp,
		bodyHashHex,
	}, "\n")
}

// hmacHex 计算 HMAC-SHA256 并输出小写十六进制。
func hmacHex(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// isExempt 判断路径是否命中免签列表。
//
// 按**路径段边界**匹配，而不是裸 strings.HasPrefix：
// 后者会让配置 /api/v1/health 意外放行 /api/v1/healthcheck ——
// 一个看起来只是"多放行一个端点"、实际是**放行了未知端点**的错误。
func (c Config) isExempt(path string) bool {
	for _, p := range c.ExemptPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

// verify 校验签名。返回 (拒绝原因, 是否通过)。
//
// 失败原因会进日志并与 "blocked" 一起返回给宿主，所以要写清
// **是哪一步不过**：签名不对、时间超窗、缺头，处理方式完全不同。
func (c Config) verify(ctx map[string]any) (string, bool) {
	if strings.TrimSpace(c.SharedSecret) == "" {
		return "未配置 sharedSecret，拒绝（没有密钥就不存在鉴权）", false
	}
	if c.PrincipalUserID == 0 {
		return "未配置 principalUserId，拒绝（无法确定签名代表哪个用户）", false
	}
	if c.Algorithm != "sha256" {
		return fmt.Sprintf("algorithm=%q 不受支持", c.Algorithm), false
	}

	sig, _ := headerValue(ctx, c.SignatureHeader)
	if strings.TrimSpace(sig) == "" {
		return "缺少签名头 " + c.SignatureHeader, false
	}
	tsStr, _ := headerValue(ctx, c.TimestampHeader)
	if strings.TrimSpace(tsStr) == "" {
		return "缺少时间戳头 " + c.TimestampHeader, false
	}
	tsStr = strings.TrimSpace(tsStr)

	// 防重放：签名是一次性的，允许的时钟偏移必须有限，
	// 否则截获的签名可以永远重放。
	if c.ToleranceSeconds > 0 {
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			return "时间戳不是整数: " + tsStr, false
		}
		skew := time.Now().Unix() - ts
		if skew < 0 {
			skew = -skew
		}
		if skew > int64(c.ToleranceSeconds) {
			// 报出偏差值便于调用方校时；这不泄露密钥。
			return fmt.Sprintf("时间戳超出容差（偏差 %ds > 允许 %ds）", skew, c.ToleranceSeconds), false
		}
	}

	// 宿主没读完 body 时无法对完整请求体做完整性校验。
	// 这种情况**不能放行** —— 放行等于允许攻击者篡改请求体而签名依然通过。
	if truncated, _ := ctx["bodyTruncated"].(bool); truncated {
		return "请求体超过宿主摘要上限（bodyTruncated），无法校验完整性", false
	}
	bodyHash := strings.TrimPrefix(str(ctx["bodyHash"]), "sha256:")

	want := hmacHex(c.SharedSecret, canonicalString(
		str(ctx["method"]), str(ctx["path"]), str(ctx["query"]), tsStr, bodyHash))
	// 常量时间比较：字符串 != 会在第一个不同字节处返回，
	// 让攻击者可以逐字节猜签名。hmac.Equal 不会。
	if !hmac.Equal([]byte(strings.ToLower(strings.TrimSpace(sig))), []byte(want)) {
		return "签名不匹配", false
	}
	return "", true
}

// contractFields 是宿主在 onApiAuth 的 ctx 里**实际提供**的字段
// （已与 middleware/api_auth.go 的实现核对过）。
//
// 缺任何一个，签名校验都无法完成，所以单独列出来而不是埋在注释里。
var contractFields = []string{
	"method",           // HTTP 方法，参与签名
	"path",             // 请求路径（不含 query），参与签名
	"query",            // 原始 query 串（未规范化），参与签名
	"ip",               // 来源 IP，用于审计与可选的来源限制
	"ua",               // User-Agent，用于审计
	"headers",          // 全部请求头（键为规范形式），**不含 Authorization**
	"hasAuthorization", // 是否带了下 Authorization（内置凭据不下发，只给这个布尔）
	"contentType",      // Content-Type
	"contentLength",    // Content-Length
	"bodyHash",         // "sha256:<hex>"；bodyTruncated 为 true 时它是空串
	"bodyTruncated",    // body 超过 64KB 时宿主不读全文，只标记此位
}

// credentialHeaders 是本插件**自己定义**的凭据头，保存在这里只为文档价值 ——
// 它们不在契约字段里：宿主不知道也不该知道插件用什么头，
// 而是把全部请求头（除 Authorization）原样放在 ctx["headers"] 里传过来，
// 由插件自己取（见 headerValue）。这样任意签名方案都能实现，
// 不需要宿主为每种方案改代码。
//
// 实际生效的头名来自配置（signatureHeader / timestampHeader），
// 下面是它们的默认值。
var credentialHeaders = []string{
	"X-Nebula-Signature",
	"X-Nebula-Timestamp",
}

// headerValue 从 ctx["headers"] 里取一个头。
//
// 必须处理两种形态：进程内 handler 传的是 map[string]string，
// 而进程外插件经 JSON 往返后是 map[string]any。
// 只认一种的后果是"永远读不到头"，而且不会有任何报错。
func headerValue(ctx map[string]any, name string) (string, bool) {
	switch hs := ctx["headers"].(type) {
	case map[string]string:
		for k, v := range hs {
			if strings.EqualFold(k, name) {
				return v, true
			}
		}
	case map[string]any:
		for k, v := range hs {
			if strings.EqualFold(k, name) {
				return str(v), true
			}
		}
	}
	return "", false
}

// str 把 ctx 里的值转成字符串。
func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// ---- 主循环 ----

func main() {
	cfg := defaultConfig()
	sc := bufio.NewScanner(os.Stdin)
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
			cfg = handleInit(req)
		case "fire":
			handleFire(req, cfg)
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

func handleInit(req request) Config {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return defaultConfig()
	}
	if p.HostProtocol != 1 {
		reply(req, initResult{OK: false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol)}, nil)
		return defaultConfig()
	}

	cfg := defaultConfig()
	if len(p.Config) > 0 {
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			logf("warn", "配置解析失败，使用默认配置: %v", err)
			cfg = defaultConfig()
		}
	}
	cfg = cfg.withDefaults()

	logf("info", "宿主版本 %s", p.HostVersion)
	logf("info", "配置: %s", cfg.redacted())
	if problems := cfg.validate(); len(problems) > 0 {
		logf("warn", "配置不完整（%s）—— 补齐后在插件页重启即可生效；"+
			"当前状态下所有 /api/v1 请求都会被拒绝（fail closed）",
			strings.Join(problems, "、"))
	}
	logf("info", "hmac-auth 已就绪：签名头 %s、时间戳头 %s、容差 %ds、免签路径 %d 条；"+
		"校验通过后认定调用者为 userId=%d",
		cfg.SignatureHeader, cfg.TimestampHeader, cfg.ToleranceSeconds,
		len(cfg.ExemptPaths), cfg.PrincipalUserID)
	logf("info", "待签名规范串为 METHOD\\nPATH\\nQUERY\\nTIMESTAMP\\nBODY_SHA256_HEX"+
		"（与方法/路径/查询串/时间戳/请求体摘要一一对应，缺一不可）")

	reply(req, initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookAPIAuth},
	}, nil)
	return cfg
}

// handleFire 处理钩子调用：校验签名，通过则声明调用者身份，否则拦截。
//
// 三种结果：
//   - 命中 exemptPaths → 放行（不写 principalUserId，即不认定身份）；
//   - 校验通过 → 在 ctx 写 principalUserId，宿主据此认定调用者；
//   - 其余一律返回含 "blocked" 的 error → 宿主 403。
//
// 注意最后一条是**默认结果**：拿不到签名头也拒绝，而不是"没意见"地退回 401。
// 理由：装上本插件表达的就是"开放 API 要签名"。如果缺头就悄悄放过，
// 那么任何人都能靠"不带签名头"把这层保护绕过去。
// 需要放行的公开端点请显式写进 exemptPaths。
func handleFire(req request, cfg Config) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}
	if p.Hook != hookAPIAuth {
		reply(req, fireResult{}, nil)
		return
	}

	method := str(p.Ctx["method"])
	path := str(p.Ctx["path"])
	ip := str(p.Ctx["ip"])

	// 契约缺口要让作者看得见，但不影响判定 —— 判定只依赖实际用到的字段。
	if missing := missingFields(p.Ctx); len(missing) > 0 {
		logf("warn", "ctx 缺少契约字段: %s", strings.Join(missing, ", "))
	}

	if cfg.isExempt(path) {
		logf("info", "%s %s（ip=%s）命中 exemptPaths，放行且不参与签名校验", method, path, ip)
		reply(req, fireResult{CtxPatch: map[string]any{"hmacAuth": "exempt"}}, nil)
		return
	}

	if reason, ok := cfg.verify(p.Ctx); !ok {
		// 记 warn 便于管理员定位；**不记录签名值本身**（可重放的凭据）。
		logf("warn", "拒绝 %s %s（ip=%s）: %s", method, path, ip, reason)
		reply(req, fireResult{
			CtxPatch: map[string]any{"hmacAuth": "denied"},
			// 必须含 "blocked"：宿主据此返回 403（见 middleware/api_auth.go）
			Error: "blocked by hmac-auth: " + reason,
		}, nil)
		return
	}

	logf("info", "放行 %s %s（ip=%s），认定调用者为 userId=%d",
		method, path, ip, cfg.PrincipalUserID)
	reply(req, fireResult{CtxPatch: map[string]any{
		"hmacAuth": "granted",
		// 经 JSON 往返到宿主后是 float64；宿主侧的正整数解析兼容这点。
		"principalUserId": cfg.PrincipalUserID,
	}}, nil)
}

// missingFields 返回契约里有、ctx 里没有的字段名。
func missingFields(ctx map[string]any) []string {
	var out []string
	for _, f := range contractFields {
		if _, ok := ctx[f]; !ok {
			out = append(out, f)
		}
	}
	return out
}

// ---- 协议输出 ----

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
