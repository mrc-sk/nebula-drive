// Command smtp-email 是一个 NebulaDrive 插件：把站内通知通过 SMTP 投递到用户邮箱。
//
// # 状态：已实现真实发信
//
// 宿主本身**不含任何发信实现**（全仓没有 net/smtp 的调用），
// onEmail 是邮件外发的唯一入口 —— 没有本插件，站内通知永远到不了邮箱。
//
// 三种 TLS 模式：
//
//	starttls  587，先明文握手、EHLO 后升级（默认）
//	implicit  465，一上来就 TLS
//	none      25，明文（认证时 net/smtp 拒绝在非加密连接上发密码）
//
// # 几处刻意的设计
//
//   - **绝不假装成功。** 投递失败会记 warn 并把 smtpEmail=failed 写回 ctx；
//     失败原因里带上服务端的原始响应。静默吞掉失败会让管理员对着
//     "配置看起来没问题"白排查几小时。
//   - **fire 永远不返回 error。** onEmail 是异步、不可拦截的钩子，
//     邮件发不出去不该让站内通知创建失败；返回含 "blocked" 的 error
//     更会误伤（宿主会当成拦截）。
//   - **配置不全时 fail closed**：init 阶段只警告（管理员可能在逐步补），
//     但真要投递时缺 host/from 直接失败，不会发出去一封注定被拒的信。
//   - **头注入防护**：Subject/To 的值来自通知内容与用户邮箱，
//     一律剥掉 CR/LF，否则可以伪造 Bcc 把通知抄送给第三方。
//
// # 配置（安装后在插件页填写）
//
//	{
//	  "host": "smtp.example.com",
//	  "port": 587,
//	  "username": "noreply@example.com",
//	  "password": "********",
//	  "from": "noreply@example.com",
//	  "fromName": "NebulaDrive",
//	  "tls": "starttls",
//	  "timeoutSeconds": 10
//	}
//
// # 构建
//
//	go build -o smtp-email .
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把
// 整个后端拖进依赖图，也会造成版本耦合。协议结构在此处独立声明。
//
// 代价是每个插件都要抄一遍这段脚手架。将来值得抽一个
// plugin-sdk module（不在本次范围内）。

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
	// version 必须与 plugin.json 的 version 保持一致：管理端展示清单里的值，
	// 日志里打的是这个常量，两者不一致会让排查时对不上号。
	version  = "1.0.0"
	hookMail = "onEmail"
	// pluginName 与 plugin.json 的 name 必须一致，否则宿主日志里对不上号。
	pluginName = "smtp-email"
)

// ---- 配置 ----

// Config 插件配置，字段与文件头的示例一一对应。
type Config struct {
	Host string `json:"host"`
	// Port 587=STARTTLS，465=implicit TLS，25=明文。默认 587。
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// From 发件人地址。多数 SMTP 服务要求它与认证账号一致。
	From     string `json:"from"`
	FromName string `json:"fromName"`
	// TLS 取值 none / starttls / implicit。默认 starttls。
	TLS string `json:"tls"`
	// TimeoutSeconds 单封邮件的投递超时。默认 10。
	TimeoutSeconds int `json:"timeoutSeconds"`
}

func defaultConfig() Config {
	return Config{
		Port: 587,
		TLS:  "starttls",
		// 默认超时压得比宿主的 fire 超时（2s）短是没意义的 ——
		// onEmail 是异步钩子，宿主不等结果，所以这里可以给到 10s。
		TimeoutSeconds: 10,
		FromName:       "NebulaDrive",
	}
}

// redacted 返回可安全写进日志的配置摘要。
//
// 密码永远不进日志：插件日志会被宿主收进环形缓冲并展示在管理端，
// 把 SMTP 密码打进去等于把它送给每一个能打开插件页的人。
func (c Config) redacted() string {
	return fmt.Sprintf("host=%q port=%d from=%q tls=%q password=%s",
		c.Host, c.Port, c.From, c.TLS, maskSecret(c.Password))
}

// maskSecret 只暴露"有没有值"和长度，不暴露内容。
func maskSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	return fmt.Sprintf("<set:%d chars>", len(s))
}

// withDefaults 填充未设置（零值）的字段。
//
// 与 validate 的分工：这里是"没填就用默认值"，validate 是"填错了要报错"。
// 没有这一步的话，管理员只写 host/from 就会拿到 timeoutSeconds=0，
// 而 context.WithTimeout(0) 是**立即过期**——症状是"配了却永远发不出去"。
func (c Config) withDefaults() Config {
	d := defaultConfig()
	if c.Port == 0 {
		c.Port = d.Port
	}
	if c.TLS == "" {
		c.TLS = d.TLS
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = d.TimeoutSeconds
	}
	if c.FromName == "" {
		c.FromName = d.FromName
	}
	return c
}

// validate 返回配置里缺失或可疑的项。
//
// 刻意**只报告、不阻止启动**：管理员可能正在逐步补配置，
// 不该因为配置不全就完全启不起来（那样连日志都看不到）。
// 真正的兜底在 Send 的 checkSendable —— 投递时 fail closed。
func (c Config) validate() []string {
	var problems []string
	if strings.TrimSpace(c.Host) == "" {
		problems = append(problems, "缺 host")
	}
	if strings.TrimSpace(c.From) == "" {
		problems = append(problems, "缺 from")
	}
	switch c.TLS {
	case "none", "starttls", "implicit":
	default:
		problems = append(problems, fmt.Sprintf("tls=%q 不是 none/starttls/implicit 之一", c.TLS))
	}
	if c.Port <= 0 || c.Port > 65535 {
		problems = append(problems, fmt.Sprintf("port=%d 非法", c.Port))
	}
	if c.TimeoutSeconds <= 0 {
		problems = append(problems, fmt.Sprintf("timeoutSeconds=%d 非法", c.TimeoutSeconds))
	}
	return problems
}

// ---- 投递 ----

// mail 一封待投递的邮件。
type mail struct {
	To      string
	Subject string
	Body    string
}

// tlsConfigForHost 构造 TLS 配置，单独抽成变量是为了**测试可注入**：
// implicit TLS（465）要求校验服务器证书，而测试里的自签证书必然校验失败。
//
// 生产路径仍是严格默认值：只设 ServerName（按系统根证书校验主机名），
// 并把最低版本钉在 TLS 1.2。**没有** InsecureSkipVerify 之类的开关 ——
// 一个"为了测试方便"的跳过校验选项，最终一定会被用到生产上。
var tlsConfigForHost = func(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

// mailer 邮件投递器。
type mailer struct {
	cfg Config
}

func newMailer(cfg Config) *mailer { return &mailer{cfg: cfg} }

// checkSendable 投递前的硬性检查。
//
// init 阶段对配置不全只警告不失败（管理员可能在逐步补配置），
// 但真要投递时必须 fail closed：缺 from 时发出去的信会被多数服务端拒收，
// 而"连上了、也写完了"很容易被误判成成功。
func (c Config) checkSendable() error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("未配置 SMTP host")
	}
	if strings.TrimSpace(c.From) == "" {
		return errors.New("未配置发件人 from")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("端口 %d 非法", c.Port)
	}
	switch c.TLS {
	case "none", "starttls", "implicit":
	default:
		return fmt.Errorf("tls=%q 不是 none/starttls/implicit 之一", c.TLS)
	}
	return nil
}

// Send 投递一封邮件。
//
// 返回的 error 一定不含 "blocked"：宿主把含该子串的错误当拦截，
// 而邮件失败绝不该拦下站内通知。（handleFire 里也会再确认一次语义。）
func (m *mailer) Send(ctx context.Context, msg mail) error {
	cfg := m.cfg
	if err := cfg.checkSendable(); err != nil {
		return err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	c, err := dialSMTP(ctx, cfg, addr, timeout)
	if err != nil {
		return err
	}
	// Quit 会发 QUIT 并关连接；出错路径上也保证连接被关闭。
	defer func() { _ = c.Close() }()

	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return fmt.Errorf("服务器未声明 AUTH，但配置了 username=%q（请清空 username，或换用支持认证的服务器）",
				cfg.Username)
		}
		// PlainAuth 自身会在非 TLS 且非本机的连接上拒绝发送密码 ——
		// 这正好挡住"用 none 模式明文传密码"。
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}

	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM <%s> 被拒: %w", cfg.From, err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("RCPT TO <%s> 被拒: %w", msg.To, err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA 被拒: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg, msg)); err != nil {
		_ = w.Close()
		return fmt.Errorf("写入报文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交报文失败: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("QUIT 失败: %w", err)
	}
	return nil
}

// dialSMTP 按 tls 模式建立连接。三种模式的差别只在"TLS 从哪一步开始"。
//
// 每一步都设了截止时间：没有 deadline 的 SMTP 往返会把插件的 goroutine
// 永久挂住 —— 宿主侧既收不到成功也收不到失败，表现为"插件没反应"。
func dialSMTP(ctx context.Context, cfg Config, addr string, timeout time.Duration) (*smtp.Client, error) {
	d := &net.Dialer{Timeout: timeout}

	if cfg.TLS == "implicit" {
		conn, err := tls.DialWithDialer(d, "tcp", addr, tlsConfigForHost(cfg.Host))
		if err != nil {
			return nil, fmt.Errorf("TLS 连接 %s 失败: %w", addr, err)
		}
		_ = conn.SetDeadline(time.Now().Add(timeout))
		c, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("SMTP 握手失败: %w", err)
		}
		return c, nil
	}

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SMTP 握手失败: %w", err)
	}

	if cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			_ = c.Close()
			return nil, fmt.Errorf("%s 未声明 STARTTLS（若服务器确实不支持，请把 tls 改为 none 或 implicit）", addr)
		}
		if err := c.StartTLS(tlsConfigForHost(cfg.Host)); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("STARTTLS 升级失败: %w", err)
		}
	}
	return c, nil
}

// buildMessage 组装 RFC 5322 报文。
//
// 两处中文必须处理，否则收件人看到的就是乱码或截断：
//   - Subject 按 RFC 2047 编码（=?UTF-8?Q?...?=）；
//   - 正文非 ASCII 时声明 charset=UTF-8 并做 base64 传输编码
//     （不是所有中继都愿意转发 8bit 内容）。
func buildMessage(cfg Config, msg mail) []byte {
	var b strings.Builder
	writeHeader(&b, "From", formatAddress(cfg.FromName, cfg.From))
	writeHeader(&b, "To", msg.To)
	writeHeader(&b, "Subject", encodeHeader(msg.Subject))
	writeHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")

	// 换行必须先归一化，**两条编码路径都要**。
	//
	// base64 是对字节做编码：正文里的裸 LF 会被原样编进去，
	// 收件人解码后拿到的仍是 LF —— 而 RFC 5321 要求报文行尾是 CRLF。
	// 早先只在 7bit 路径做了归一化，base64 路径漏了，是单测里
	// "中文正文解码后应等于 toCRLF(原文)" 这条断言把它抓出来的。
	body := toCRLF(msg.Body)

	if isASCII(body) {
		writeHeader(&b, "Content-Type", "text/plain; charset=UTF-8")
		// 纯 ASCII 用 7bit：内容本来就只占 7 位，声明 8bit 只会给
		// 保守的中继添麻烦。
		writeHeader(&b, "Content-Transfer-Encoding", "7bit")
		b.WriteString("\r\n")
		b.WriteString(body)
		return []byte(b.String())
	}

	writeHeader(&b, "Content-Type", "text/plain; charset=UTF-8")
	writeHeader(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString("\r\n")
	b.WriteString(foldBase64(base64.StdEncoding.EncodeToString([]byte(body))))
	return []byte(b.String())
}

// writeHeader 写一行邮件头，并剥掉值里的 CR/LF。
//
// 这不是洁癖，是**头注入防护**：Subject 与 To 的值来自站内通知内容与
// 用户邮箱。若原样带 CRLF 写进去，攻击者可以往报文里注入任意头
// （最典型的是伪造 Bcc，把通知静默抄送给第三方）。
// 正确的修法是把换行替换掉，而不是指望调用方都记得过滤。
func writeHeader(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(strings.NewReplacer("\r", " ", "\n", " ").Replace(value))
	b.WriteString("\r\n")
}

// formatAddress 组装 From 头：显示名非 ASCII 时按 RFC 2047 编码。
func formatAddress(name, addr string) string {
	if strings.TrimSpace(name) == "" {
		return addr
	}
	return encodeHeader(name) + " <" + addr + ">"
}

// encodeHeader 非 ASCII 时按 RFC 2047 编码，纯 ASCII 原样返回（更可读）。
//
// 注意：mime.QEncoding 对超长输入会插入 `\r\n ` 折行，而调用方
// （writeHeader）会把 CR/LF 替换成空格 —— 结果是 `=?...?= =?...?=`。
// 按 RFC 2047，相邻 encoded-word 之间的线性空白在解码时会被忽略，
// 所以语义不变。main_test.go 里用长中文主题专门验证了这一点。
func encodeHeader(s string) string {
	if isASCII(s) {
		return s
	}
	return mime.QEncoding.Encode("UTF-8", s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// toCRLF 归一化换行。SMTP 要求 CRLF，裸 LF 可能被中继改写或拒收。
func toCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// foldBase64 按 RFC 2045 每 76 列折行。
func foldBase64(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteString("\r\n")
		s = s[76:]
	}
	b.WriteString(s)
	return b.String()
}

// ---- 主循环 ----

func main() {
	cfg := defaultConfig()
	sc := bufio.NewScanner(os.Stdin)
	// 上限 8MB：正常 RPC 行只有几百字节，但 onEmail 的 ctx.body
	// 是通知正文，留出余量。
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
			handleFire(req, newMailer(cfg))
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

// handleInit 处理宿主下发的初始化参数，返回生效的配置。
func handleInit(req request) Config {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		reply(req, initResult{OK: false, Error: "init 参数解析失败: " + err.Error()}, nil)
		return defaultConfig()
	}
	if p.HostProtocol != 1 {
		// 明确失败比"勉强跑起来"好：协议不匹配时静默降级会出难查的问题
		reply(req, initResult{OK: false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol)}, nil)
		return defaultConfig()
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
	cfg = cfg.withDefaults()

	logf("info", "宿主版本 %s，站点 %s", p.HostVersion, p.SiteURL)
	logf("info", "配置: %s", cfg.redacted())
	if problems := cfg.validate(); len(problems) > 0 {
		// 只警告不失败：管理员可能正在逐步补配置，不该因此完全不可用。
		// 真要投递时 Send 会 fail closed（见 checkSendable），不会发出去
		// 一封注定被拒的信。
		logf("warn", "配置不完整（%s）—— 补齐后在插件页重启即可生效；当前状态下邮件不会发出",
			strings.Join(problems, "、"))
	}
	logf("info", "smtp-email 已就绪：tls=%s，收件人来自 onEmail 事件的邮箱字段；"+
		"投递失败会记 warn 并回写 smtpEmail=failed", cfg.TLS)

	reply(req, initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookMail},
	}, nil)
	return cfg
}

// handleFire 处理钩子调用。
func handleFire(req request, m *mailer) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErr(req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}
	if p.Hook != hookMail {
		// 宿主只派发 manifest 里声明过的钩子；走到这里说明宿主多派了，
		// 回一个空结果即可，不算错误。
		reply(req, fireResult{}, nil)
		return
	}

	msg := mail{
		To:      str(p.Ctx["to"]),
		Subject: str(p.Ctx["subject"]),
		Body:    str(p.Ctx["body"]),
	}
	// 宿主在用户邮箱为空时不会触发本钩子（见 CreateNotification），
	// 但插件不该依赖这个前提 —— 依赖它意味着宿主那边一旦放宽条件，
	// 这里就会对空收件人发起投递（某些服务器会当成"发给自己"）。
	if strings.TrimSpace(msg.To) == "" {
		reply(req, fireResult{CtxPatch: map[string]any{"smtpEmail": "skipped-no-recipient"}}, nil)
		return
	}

	timeout := time.Duration(m.cfg.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// onEmail 是异步、不可拦截的钩子（见宿主 hooks.go）：宿主不等结果、
	// 也只把 Error 里的 "blocked" 当成拦截。所以这里**永远不返回 Error**
	// —— 邮件发不出去不该让站内通知创建失败。
	err := m.Send(ctx, msg)
	if err != nil {
		// 投递失败：明确写日志（带上服务端原始响应），绝不静默吞掉。
		// 静默吞掉的表现是"配置看起来没问题但收不到信"，
		// 会让管理员对着 SMTP 配置白排查几小时。
		logf("warn", "邮件投递失败（to=%s subject=%q）: %v", msg.To, msg.Subject, err)
		reply(req, fireResult{CtxPatch: map[string]any{"smtpEmail": "failed"}}, nil)
		return
	}

	logf("info", "已投递邮件: to=%s subject=%q", msg.To, msg.Subject)
	reply(req, fireResult{CtxPatch: map[string]any{"smtpEmail": "sent"}}, nil)
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

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
