// Command notify-webhook 是一个 NebulaDrive 插件：把站内通知转发到外部 webhook。
//
// 支持 Slack / 飞书 / 钉钉 / Discord / 通用 JSON 五种载荷。
//
// # 几个刻意的设计
//
//   - **只走 http/https。** 配置里写 file://、ftp:// 之类时直接拒绝，
//     连请求都不构造。否则管理员的一次配置笔误就可能把插件变成读取
//     本机文件的通道。
//   - **URL 绝不原样进日志。** webhook URL 通常本身就是凭据：Slack 的
//     token 藏在路径里，通用 webhook 可能写成 https://user:pass@host/...。
//     插件日志会被宿主收集并展示在管理端，所以这里只打印
//     scheme://host/…，路径、查询串、用户名、密码一律不出现。
//   - **onEmail 永远不返回错误。** 它是异步通知钩子，宿主不读结果；
//     返回含 "blocked" 的错误反而会被误判成拦截，让站内通知创建失败。
//     发送失败只写 warn 日志。
//   - **单个 webhook 失败不影响其它 webhook。** 一次通知并发投递，
//     每个目标独立超时、独立重试，最后汇总。
//   - **重试只对网络错误与 5xx 做一次。** 4xx 表示请求本身有问题，
//     重试只会放大无效流量；无限重试则会拖住整个插件。
//   - **最多处理 10 个 webhook。** 配置写错（比如复制出上百条）时，
//     不能让一条站内通知打出上千个外发请求。
//
// # 配置
//
//	{
//	  "webhooks": [
//	    {
//	      "url": "https://hooks.slack.com/services/...",
//	      "format": "slack",
//	      "headers": {"X-Custom": "value"},
//	      "timeoutSeconds": 5
//	    }
//	  ],
//	  "includeBody": true,
//	  "maxBodyChars": 2000,
//	  "ignoreSubjects": ["验证码"]
//	}
//
// # 构建
//
//	go build -o notify-webhook .
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---- 协议结构（与 pkg/plugin 保持一致）----
//
// 插件不 import 宿主包：宿主与插件是独立进程，import 宿主会把整个
// 后端拖进依赖图，也会造成版本耦合。协议结构在此独立声明。

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
	HostVersion  string          `json:"hostVersion"`
	HostProtocol int             `json:"hostProtocol"`
	PluginName   string          `json:"pluginName"`
	DataDir      string          `json:"dataDir"`
	Config       json.RawMessage `json:"config"`
	SiteURL      string          `json:"siteUrl,omitempty"`
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
	// 日志和 User-Agent 用的是这个常量。
	version    = "1.0.0"
	pluginName = "notify-webhook"

	hookEmail = "onEmail"
	hookCLI   = "onCLI"

	// maxWebhooks 是单次通知最多投递的目标数。配置再长也只取前 10 个，
	// 这是防配置事故的硬上限，不是建议值。
	maxWebhooks = 10

	// maxAttempts=2 表示首次请求 + 一次重试。
	maxAttempts = 2

	defaultTimeoutSeconds = 5
	defaultMaxBodyChars   = 2000

	// truncationMarker 会占用 maxBodyChars 的预算，而不是额外追加：
	// 否则“正文最多 N 字符”的承诺在带标记时就会被打破。
	truncationMarker = "\n…（正文已截断）"
)

// ---- 配置 ----

// Config 插件配置，字段与文件头的示例一一对应。
type Config struct {
	Webhooks []WebhookConfig `json:"webhooks"`
	// IncludeBody 是否把通知正文带进 webhook。默认 true。
	// 解析时先放默认值再 Unmarshal，因此显式 false 仍然有效。
	IncludeBody bool `json:"includeBody"`
	// MaxBodyChars 正文最大字符数（按 Unicode 字符计，不是字节）。
	// 超过时截断并注明；默认 2000。
	MaxBodyChars int `json:"maxBodyChars"`
	// IgnoreSubjects 标题子串忽略列表。命中任意一条就不发送。
	IgnoreSubjects []string `json:"ignoreSubjects"`
}

// WebhookConfig 单个目标。字段名是宿主配置 schema 的一部分，不能改。
type WebhookConfig struct {
	URL string `json:"url"`
	// Format 取值 generic / slack / feishu / dingtalk / discord。
	Format string `json:"format"`
	// Headers 原样附加到请求上。值可能是 Authorization 这类凭据，
	// 所以任何情况下都不打印。
	Headers map[string]string `json:"headers"`
	// TimeoutSeconds 单次 HTTP 尝试的超时，默认 5 秒。
	TimeoutSeconds int `json:"timeoutSeconds"`
}

func defaultConfig() Config {
	return Config{
		IncludeBody:  true,
		MaxBodyChars: defaultMaxBodyChars,
	}
}

// withDefaults 只补可安全补默认值的字段。
//
// 注意 IncludeBody 不能在这里用“等于零值就设 true”的方式补：false 是
// 合法配置，那样会把管理员显式关闭正文的配置又打开。它靠 defaultConfig
// 作为 Unmarshal 的初值来保留“未填写=true、显式false=false”的语义。
func (c Config) withDefaults() Config {
	if c.MaxBodyChars <= 0 {
		c.MaxBodyChars = defaultMaxBodyChars
	}
	return c
}

// validate 返回可展示在日志里的配置问题。
//
// 只报告、不拒绝启动：管理员可能正在逐项填配置，插件完全起不来时
// 连日志都看不到。真正的发送兜底在每个目标的解析与投递阶段。
func (c Config) validate() []string {
	var problems []string
	if len(c.Webhooks) == 0 {
		problems = append(problems, "未配置 webhooks")
	}
	if len(c.Webhooks) > maxWebhooks {
		problems = append(problems,
			fmt.Sprintf("配置了 %d 个 webhook，超过上限 %d；只会处理前 %d 个",
				len(c.Webhooks), maxWebhooks, maxWebhooks))
	}
	// 只检查实际会被处理的那些，避免一条超长错误配置在启动时刷满日志。
	limit := min(len(c.Webhooks), maxWebhooks)
	for i := 0; i < limit; i++ {
		if _, err := c.Webhooks[i].target(); err != nil {
			// 错误文本由 target 构造，里面不会再带原始 URL。
			problems = append(problems,
				fmt.Sprintf("webhooks[%d]（%s）: %v", i, displayURLString(c.Webhooks[i].URL), err))
		}
	}
	return problems
}

// webhookTarget 是校验并规范化后的目标。所有外发代码只接触它，
// 不再直接使用配置里的原始字符串，减少凭据被顺手打进日志的机会。
type webhookTarget struct {
	url     *url.URL
	display string
	format  string
	headers map[string]string
	timeout time.Duration
}

// target 校验并规范化单个 webhook。
func (w WebhookConfig) target() (webhookTarget, error) {
	raw := strings.TrimSpace(w.URL)
	u, err := url.Parse(raw)
	if err != nil {
		return webhookTarget{}, errors.New("URL 解析失败")
	}
	if u.Host == "" || u.Hostname() == "" {
		return webhookTarget{}, errors.New("URL 缺少主机名")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		// 安全边界：只允许 HTTP(S)。不要把 file/ftp/gopher 交给
		// http.Client；某些 scheme 会触发完全不同的本地行为。
		return webhookTarget{}, fmt.Errorf("不支持的 URL scheme %q，只允许 http/https", u.Scheme)
	}
	u.Scheme = scheme
	// fragment 不会发给服务端，留着只会让同一 URL 出现两种写法。
	u.Fragment = ""

	format := strings.ToLower(strings.TrimSpace(w.Format))
	if format == "" {
		// 只配 URL 的人显然想要通用 JSON。给一个明确默认值，比拒绝发送好。
		format = "generic"
	}
	switch format {
	case "generic", "slack", "feishu", "dingtalk", "discord":
	default:
		return webhookTarget{}, fmt.Errorf("不支持的 format %q", format)
	}

	for k, v := range w.Headers {
		// CR/LF 会造成头注入。net/http 最终也会拒绝，但在构造请求前
		// 明确拒绝能给出可定位的配置错误，且不会误触发一次重试。
		if strings.ContainsAny(k, "\r\n:") || strings.ContainsAny(v, "\r\n") {
			return webhookTarget{}, errors.New("headers 的名称或值含非法换行/冒号")
		}
	}

	timeout := defaultTimeoutSeconds
	if w.TimeoutSeconds > 0 {
		timeout = w.TimeoutSeconds
	}

	return webhookTarget{
		url:     u,
		display: displayURL(u),
		format:  format,
		headers: w.Headers,
		timeout: time.Duration(timeout) * time.Second,
	}, nil
}

// displayURL 返回可安全写进日志的 URL 摘要。
//
// 只保留 scheme、host（含端口）和固定的 “/…” 占位：
//   - userinfo 不能出现（可能是账号密码）；
//   - 路径不能出现（Slack 等平台的 token 就在路径里）；
//   - query 不能出现（常有 access_token / signature）。
func displayURL(u *url.URL) string {
	if u == nil || u.Scheme == "" || u.Host == "" {
		return "<invalid-url>"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// displayURLString 在 URL 尚未解析成功时也绝不回显原文。
func displayURLString(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "<invalid-url>"
	}
	return displayURL(u)
}

// ---- 载荷 ----

type notification struct {
	To      string
	Subject string
	Body    string
}

type genericPayload struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Time    string `json:"time"`
}

type slackPayload struct {
	Text string `json:"text"`
}

type textContent struct {
	Text string `json:"text"`
}

type feishuPayload struct {
	MsgType string      `json:"msg_type"`
	Content textContent `json:"content"`
}

type dingtalkText struct {
	Content string `json:"content"`
}

type dingtalkPayload struct {
	MsgType string       `json:"msgtype"`
	Text    dingtalkText `json:"text"`
}

type discordPayload struct {
	Content string `json:"content"`
}

// buildPayload 生成某个格式的 JSON 请求体。
//
// at 由调用方传入而不是在这里取 time.Now()，这样同一次通知发往多个
// webhook 时携带的是同一个时间点。
func buildPayload(format string, msg notification, includeBody bool, maxBodyChars int, at time.Time) ([]byte, error) {
	body := msg.Body
	if !includeBody {
		body = ""
	} else {
		body = truncateBody(body, maxBodyChars)
	}

	switch format {
	case "generic":
		return json.Marshal(genericPayload{
			To:      msg.To,
			Subject: msg.Subject,
			Body:    body,
			Time:    at.UTC().Format(time.RFC3339),
		})
	case "slack":
		return json.Marshal(slackPayload{Text: msg.Subject + "\n" + body})
	case "feishu":
		return json.Marshal(feishuPayload{
			MsgType: "text",
			Content: textContent{Text: msg.Subject + "\n" + body},
		})
	case "dingtalk":
		return json.Marshal(dingtalkPayload{
			MsgType: "text",
			Text:    dingtalkText{Content: msg.Subject + "\n" + body},
		})
	case "discord":
		return json.Marshal(discordPayload{Content: msg.Subject + "\n" + body})
	default:
		return nil, fmt.Errorf("不支持的 format %q", format)
	}
}

// truncateBody 按 Unicode 字符截断，并在字符预算内追加说明。
//
// 用 []rune 而不是 s[:n]：后者会从 UTF-8 字符中间切断，产生非法字节，
// 对方平台看到的会是乱码。
func truncateBody(s string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	runes := []rune(s)
	marker := []rune(truncationMarker)
	if maxChars <= len(marker) {
		// 预算连说明都放不下。宁可只截断，也不突破字符上限。
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-len(marker)]) + truncationMarker
}

// ---- 投递 ----

type deliveryResult struct {
	Index      int
	DisplayURL string
	Format     string
	OK         bool
	Skipped    bool
	Attempts   int
	Err        string
}

type notifier struct {
	cfg Config
}

func newNotifier(cfg Config) *notifier { return &notifier{cfg: cfg} }

// forwardEmail 处理 onEmail 的转发逻辑。
//
// 它的返回值只用于可观测性；onEmail 是异步钩子，宿主不会读取。
// 任何失败都只写日志，绝不在这里构造拦截错误。
func (n *notifier) forwardEmail(ctx context.Context, msg notification) fireResult {
	if hit, pattern := ignoredSubject(msg.Subject, n.cfg.IgnoreSubjects); hit {
		logf("info", "标题命中 ignoreSubjects，跳过转发（pattern=%q subject=%q）",
			pattern, msg.Subject)
		return fireResult{CtxPatch: map[string]any{"notifyWebhook": "ignored"}}
	}
	if len(n.cfg.Webhooks) == 0 {
		logf("warn", "收到站内通知但未配置任何 webhook，跳过转发（subject=%q）", msg.Subject)
		return fireResult{CtxPatch: map[string]any{"notifyWebhook": "skipped-no-webhooks"}}
	}

	results := n.sendAll(ctx, msg)
	ok, failed := countResults(results)
	switch {
	case failed == 0:
		logf("info", "已把通知转发到 %d 个 webhook（subject=%q）", ok, msg.Subject)
		return fireResult{CtxPatch: map[string]any{"notifyWebhook": "sent"}}
	case ok == 0:
		logf("warn", "通知转发全部失败（%d 个 webhook，subject=%q）", failed, msg.Subject)
		return fireResult{CtxPatch: map[string]any{"notifyWebhook": "failed"}}
	default:
		logf("warn", "通知转发部分失败（成功 %d，失败 %d，subject=%q）", ok, failed, msg.Subject)
		return fireResult{CtxPatch: map[string]any{"notifyWebhook": "partial"}}
	}
}

// sendAll 并发投递到所有目标（最多前 10 个）。
//
// 并发而不是串行：10 个目标各自 5 秒超时，串行最坏会拖到 50 秒以上。
// 并发后总耗时约等于单个目标的最坏耗时，且一个慢目标不影响其它目标。
func (n *notifier) sendAll(ctx context.Context, msg notification) []deliveryResult {
	if ctx == nil {
		ctx = context.Background()
	}
	configured := n.cfg.Webhooks
	if len(configured) > maxWebhooks {
		logf("warn", "配置了 %d 个 webhook，超过上限 %d，本次忽略后 %d 个",
			len(configured), maxWebhooks, len(configured)-maxWebhooks)
		configured = configured[:maxWebhooks]
	}

	at := time.Now()
	results := make([]deliveryResult, len(configured))
	var wg sync.WaitGroup
	for i := range configured {
		results[i] = deliveryResult{
			Index:      i,
			DisplayURL: displayURLString(configured[i].URL),
		}
		target, err := configured[i].target()
		if err != nil {
			results[i].Err = err.Error()
			logf("warn", "webhook %s 配置无效，跳过: %v", results[i].DisplayURL, err)
			continue
		}
		results[i].DisplayURL = target.display
		results[i].Format = target.format

		payload, err := buildPayload(target.format, msg, n.cfg.IncludeBody,
			n.cfg.MaxBodyChars, at)
		if err != nil {
			results[i].Err = err.Error()
			logf("warn", "webhook %s 载荷生成失败: %v", target.display, err)
			continue
		}

		wg.Add(1)
		go func(i int, target webhookTarget, payload []byte) {
			defer wg.Done()
			attempts, err := postWebhook(ctx, target, payload)
			results[i].Attempts = attempts
			if err != nil {
				results[i].Err = err.Error()
				logf("warn", "webhook %s（format=%s）发送失败，尝试 %d 次: %v",
					target.display, target.format, attempts, err)
				return
			}
			results[i].OK = true
			logf("info", "webhook %s（format=%s）发送成功，尝试 %d 次",
				target.display, target.format, attempts)
		}(i, target, payload)
	}
	wg.Wait()
	return results
}

// postWebhook 投递一次，网络错误或 5xx 时最多再试一次。
//
// 4xx 不重试：认证失败、载荷格式错、webhook 被删除，重试都只会得到
// 同样的结果，还会把限流配额打光。3xx 也不重试，因为重定向被禁用了
// （见 newHTTPClient），它在这里就是一个配置错误。
func postWebhook(ctx context.Context, target webhookTarget, payload []byte) (int, error) {
	client := newHTTPClient(target.timeout)
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			target.url.String(), bytes.NewReader(payload))
		if err != nil {
			// 构造失败通常是 URL 本身有问题，重试没有意义。
			return attempt, fmt.Errorf("构造请求失败: %s", safeErrorText(err, target.url))
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", "NebulaDrive-notify-webhook/"+version)
		// 自定义头放在内置头之后：管理员需要覆盖 Content-Type 时仍可覆盖。
		for k, v := range target.headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("网络请求失败: %s", safeErrorText(err, target.url))
			if attempt < maxAttempts && ctx.Err() == nil {
				continue
			}
			return attempt, lastErr
		}

		// 必须先读一点再关，否则连接无法复用；同时限制读取量，
		// 避免恶意/异常服务端用巨大响应体拖住插件。
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return attempt, nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		// 只重试 5xx。响应体内容不写日志：它可能回显请求 URL 或凭据。
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 && attempt < maxAttempts &&
			ctx.Err() == nil {
			continue
		}
		return attempt, lastErr
	}
	return maxAttempts, lastErr
}

// newHTTPClient 创建只用于本目标的客户端。
//
// 两个关键点：
//   - 显式 Timeout，绝不使用 http.DefaultClient 的“无限等待”；
//   - 禁用重定向。否则一个 302 可以把 POST 悄悄带到另一个地址，绕过
//     我们对初始 URL 的 scheme 检查，也容易把自定义认证头带到意外目标。
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// safeErrorText 处理网络错误，确保不会把含凭据的 URL 带进日志。
//
// http.Client 返回的 *url.Error 会把完整 URL 拼进 Error()；网络错误
// 甚至可能连带路径里的 token。这里先剥掉外层 URL 包装，再对残余文本
// 做一次凭据替换，最后才允许它进入日志。
func safeErrorText(err error, u *url.URL) string {
	if err == nil {
		return "未知错误"
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err
	}
	msg := err.Error()
	if u != nil {
		// 先换整条 URL，再换单独的凭据片段。顺序反过来会先把密码改掉，
		// 导致完整 URL 匹配不上，路径里的 token 反而漏出去。
		msg = strings.ReplaceAll(msg, u.String(), displayURL(u))
		if u.User != nil {
			if pw, ok := u.User.Password(); ok && pw != "" {
				msg = strings.ReplaceAll(msg, pw, "***")
			}
			if name := u.User.Username(); name != "" {
				msg = strings.ReplaceAll(msg, name, "***")
			}
			msg = strings.ReplaceAll(msg, u.User.String(), "***")
		}
	}
	if msg == "" {
		return "未知错误"
	}
	// 错误文本只用于排查，不承载业务信息；过长时截断，避免刷爆日志。
	const maxErrChars = 300
	if utf8.RuneCountInString(msg) > maxErrChars {
		msg = string([]rune(msg)[:maxErrChars]) + "…"
	}
	return msg
}

// ignoredSubject 返回命中的子串。空/纯空白模式会被忽略：strings.Contains
// 对阵空串恒为真，若不排除，管理员误留一个 "" 就会静默关闭所有通知。
func ignoredSubject(subject string, patterns []string) (bool, string) {
	for _, p := range patterns {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if strings.Contains(subject, p) {
			return true, p
		}
	}
	return false, ""
}

func countResults(results []deliveryResult) (ok, failed int) {
	for _, r := range results {
		if r.OK {
			ok++
		} else {
			failed++
		}
	}
	return ok, failed
}

// ---- CLI ----

// cliResult 处理 onCLI。命令名不是 notify-webhook 时返回空结果，
// 不写 cliOutput，也不发任何请求。
func (n *notifier) cliResult(ctx map[string]any) fireResult {
	if str(ctx["command"]) != pluginName {
		return fireResult{}
	}

	if len(n.cfg.Webhooks) == 0 {
		logf("warn", "notify-webhook 自测失败：未配置任何 webhook")
		return fireResult{CtxPatch: map[string]any{
			"cliOutput": "notify-webhook：未配置任何 webhook。\n" +
				"请先在插件配置中填写 webhooks 数组，再执行 nebula plugin notify-webhook。\n",
			"cliExitCode": 1,
		}}
	}

	msg := notification{
		Subject: "NebulaDrive notify-webhook 自测",
		Body: "这是一条由 nebula plugin notify-webhook 发出的测试消息。\n" +
			"时间：" + time.Now().Format(time.RFC3339) + "\n" +
			"收到这条消息说明 webhook 地址与网络是通的。",
	}
	results := n.sendAll(context.Background(), msg)
	ok, failed := countResults(results)

	var b strings.Builder
	b.WriteString("notify-webhook 自测：")
	fmt.Fprintf(&b, "成功 %d 个，失败 %d 个\n", ok, failed)
	if len(n.cfg.Webhooks) > maxWebhooks {
		fmt.Fprintf(&b, "注意：配置了 %d 个 webhook，超过上限 %d，只处理前 %d 个\n",
			len(n.cfg.Webhooks), maxWebhooks, maxWebhooks)
	}
	for _, r := range results {
		status := "成功"
		if r.Skipped {
			status = "跳过"
		} else if !r.OK {
			status = "失败：" + r.Err
		}
		format := r.Format
		if format == "" {
			format = "unknown"
		}
		fmt.Fprintf(&b, "  [%d] %s（%s）: %s\n",
			r.Index+1, r.DisplayURL, format, status)
	}

	patch := map[string]any{"cliOutput": b.String()}
	if failed > 0 {
		patch["cliExitCode"] = 1
	}
	return fireResult{CtxPatch: patch}
}

// ---- 主循环 ----

func main() {
	serve(os.Stdin, os.Stdout)
}

// serve 是 stdio JSON-RPC 主循环。
//
// stdout 是协议专用通道，只能是“一行一个 RPC 响应”；日志全部走 stderr。
func serve(in io.Reader, out io.Writer) {
	cfg := defaultConfig()
	sc := bufio.NewScanner(in)
	// 通知正文可能较大，留 8MB 余量；正常 RPC 行只有几百字节。
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			// 解析不了的请求没有可用的 id，无法回响应；但插件不能退出，
			// 否则宿主只能看到“插件已死”，看不到真正原因。
			logf("error", "无法解析请求: %v", err)
			continue
		}

		switch req.Method {
		case "init":
			cfg = handleInit(req, out)
		case "fire":
			handleFire(req, out, newNotifier(cfg))
		case "ping":
			replyTo(out, req, map[string]any{"pong": true}, nil)
		case "shutdown":
			replyTo(out, req, map[string]any{"bye": true}, nil)
			return
		default:
			replyErrTo(out, req, -32601, "不支持的方法: "+req.Method)
		}
	}
	if err := sc.Err(); err != nil {
		logf("error", "读取 stdin 失败: %v", err)
	}
}

// handleInit 处理宿主下发的初始化参数，返回生效的配置。
func handleInit(req request, out io.Writer) Config {
	var p initParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyTo(out, req, initResult{OK: false, Error: "init 参数解析失败"}, nil)
		return defaultConfig()
	}
	if p.HostProtocol != 1 {
		// 协议不匹配时明确失败，不做“尽力兼容”：静默降级会把字段
		// 语义差异藏到运行期，排查成本远高于启动时直接报错。
		replyTo(out, req, initResult{
			OK:    false,
			Error: fmt.Sprintf("协议版本不匹配：宿主 %d，插件只支持 1", p.HostProtocol),
		}, nil)
		return defaultConfig()
	}

	cfg := defaultConfig()
	if len(p.Config) > 0 {
		// 配置坏了就退回零配置继续启动，管理员还能看到插件与管理页，
		// 也能看到这条日志；直接退出会让插件页只剩“启动失败”。
		if err := json.Unmarshal(p.Config, &cfg); err != nil {
			logf("warn", "配置解析失败，按零配置启动（不会发送任何通知）: %v", err)
			cfg = defaultConfig()
		}
	}
	cfg = cfg.withDefaults()

	logf("info", "宿主版本 %s，站点 %s", p.HostVersion, p.SiteURL)
	logf("info", "notify-webhook 配置：%d 个 webhook，includeBody=%t，maxBodyChars=%d",
		len(cfg.Webhooks), cfg.IncludeBody, cfg.MaxBodyChars)

	limit := min(len(cfg.Webhooks), maxWebhooks)
	for i := 0; i < limit; i++ {
		target, err := cfg.Webhooks[i].target()
		if err != nil {
			logf("warn", "webhook[%d]（%s）不可用: %v",
				i, displayURLString(cfg.Webhooks[i].URL), err)
			continue
		}
		// 只打印脱敏后的地址和格式；headers 里的 Authorization 等
		// 凭据以及路径里的 token 永远不出现。
		logf("info", "webhook[%d] → %s（format=%s，timeout=%s）",
			i, target.display, target.format, target.timeout)
	}
	for _, problem := range cfg.validate() {
		logf("warn", "配置问题: %s", problem)
	}

	replyTo(out, req, initResult{
		OK:      true,
		Name:    pluginName,
		Version: version,
		Hooks:   []string{hookEmail, hookCLI},
	}, nil)
	return cfg
}

// handleFire 处理钩子调用。
func handleFire(req request, out io.Writer, n *notifier) {
	var p fireParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		replyErrTo(out, req, -32602, "fire 参数解析失败: "+err.Error())
		return
	}
	replyTo(out, req, fire(n, p, context.Background()), nil)
}

// fire 是 handleFire 的纯逻辑部分，方便单元测试直接断言 hook 行为。
func fire(n *notifier, p fireParams, ctx context.Context) fireResult {
	switch p.Hook {
	case hookEmail:
		msg := notification{
			To:      str(p.Ctx["to"]),
			Subject: str(p.Ctx["subject"]),
			Body:    str(p.Ctx["body"]),
		}
		return n.forwardEmail(ctx, msg)
	case hookCLI:
		return n.cliResult(p.Ctx)
	default:
		// 宿主只派发 manifest 里声明过的钩子；多派了也回空结果。
		return fireResult{}
	}
}

// ---- 协议输出 ----

func replyTo(out io.Writer, req request, result any, rerr *rpcError) {
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
	b, err := json.Marshal(resp)
	if err != nil {
		logf("error", "序列化响应失败: %v", err)
		return
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		logf("error", "写入响应失败: %v", err)
	}
}

func replyErrTo(out io.Writer, req request, code int, msg string) {
	replyTo(out, req, nil, &rpcError{Code: code, Message: msg})
}

// ---- 日志 ----

var (
	logMu     sync.Mutex
	logOutput io.Writer = os.Stderr
)

// setLogOutput 替换日志目标并返回旧值。生产代码不会调用它；
// 测试用它捕获 stderr 以验证脱敏。
func setLogOutput(w io.Writer) io.Writer {
	logMu.Lock()
	defer logMu.Unlock()
	old := logOutput
	logOutput = w
	return old
}

// logf 把一行结构化日志写到 stderr。
//
// 日志不能走 stdout：stdout 上混入非 RPC 行会直接破坏宿主与本插件的
// 协议解析。所有调用方都必须只传已经脱敏的内容。
func logf(level, format string, args ...any) {
	rec := logLine{
		Level:   level,
		Message: fmt.Sprintf(format, args...),
		Time:    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"level":%q,"message":"日志序列化失败"}`, level))
	}
	b = append(b, '\n')

	logMu.Lock()
	defer logMu.Unlock()
	w := logOutput
	if w == nil {
		w = os.Stderr
	}
	_, _ = w.Write(b)
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
