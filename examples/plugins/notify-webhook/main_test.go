package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// 这些测试用 httptest.NewServer 做真实的 HTTP 往返，而不是 mock
// http.Client：
//   - 载荷是否真的按平台格式发出、自定义头是否到位，只有读到线上
//     请求才能确认；
//   - 5xx 重试 / 4xx 不重试必须靠服务端计数器验证，mock 很容易把
//     “调用了几次”写错；
//   - 日志脱敏必须真的捕获 stderr，不能只信“代码里看起来没打印 URL”。

type capturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type capture struct {
	mu   sync.Mutex
	reqs []capturedRequest
}

func (c *capture) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, capturedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
		Body:   body,
	})
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func (c *capture) one(t *testing.T) capturedRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) != 1 {
		t.Fatalf("服务端收到 %d 个请求，want 1", len(c.reqs))
	}
	return c.reqs[0]
}

// newCaptureServer 返回固定状态码的假 webhook 服务器。
func newCaptureServer(t *testing.T, status int) (*httptest.Server, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.add(r)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// captureLogs 接管 stderr 日志。测试自身串行执行，不会和别的用例抢全局目标。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := setLogOutput(&buf)
	t.Cleanup(func() { setLogOutput(old) })
	return &buf
}

func testNotifier(srvURL, format string) *notifier {
	return newNotifier(Config{
		Webhooks: []WebhookConfig{{
			URL:            srvURL,
			Format:         format,
			TimeoutSeconds: 2,
		}},
		IncludeBody:  true,
		MaxBodyChars: 2000,
	})
}

// TestPayloadFormatsAreExact 逐字段断言五种平台的载荷。
func TestPayloadFormatsAreExact(t *testing.T) {
	const (
		to      = "user@example.com"
		subject = "备份完成"
		body    = "文件已经备份到 /backup。\n状态：成功"
	)
	expectedText := subject + "\n" + body

	cases := []struct {
		format string
		check  func(t *testing.T, m map[string]any)
	}{
		{
			format: "generic",
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				if len(m) != 4 {
					t.Errorf("generic 顶层字段数 = %d, want 4: %v", len(m), m)
				}
				if got := m["to"]; got != to {
					t.Errorf("to = %#v, want %q", got, to)
				}
				if got := m["subject"]; got != subject {
					t.Errorf("subject = %#v, want %q", got, subject)
				}
				if got := m["body"]; got != body {
					t.Errorf("body = %#v, want %q", got, body)
				}
				ts, ok := m["time"].(string)
				if !ok {
					t.Fatalf("time 不是字符串: %#v", m["time"])
				}
				if _, err := time.Parse(time.RFC3339, ts); err != nil {
					t.Errorf("time=%q 不是 RFC3339: %v", ts, err)
				}
			},
		},
		{
			format: "slack",
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				if len(m) != 1 {
					t.Errorf("slack 顶层字段数 = %d, want 1: %v", len(m), m)
				}
				if got := m["text"]; got != expectedText {
					t.Errorf("text = %#v, want %q", got, expectedText)
				}
			},
		},
		{
			format: "feishu",
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				if len(m) != 2 || m["msg_type"] != "text" {
					t.Fatalf("feishu 顶层 = %v", m)
				}
				content, ok := m["content"].(map[string]any)
				if !ok {
					t.Fatalf("content 不是对象: %#v", m["content"])
				}
				if len(content) != 1 || content["text"] != expectedText {
					t.Errorf("content = %#v, want text=%q", content, expectedText)
				}
			},
		},
		{
			format: "dingtalk",
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				if len(m) != 2 || m["msgtype"] != "text" {
					t.Fatalf("dingtalk 顶层 = %v", m)
				}
				text, ok := m["text"].(map[string]any)
				if !ok {
					t.Fatalf("text 不是对象: %#v", m["text"])
				}
				if len(text) != 1 || text["content"] != expectedText {
					t.Errorf("text = %#v, want content=%q", text, expectedText)
				}
			},
		},
		{
			format: "discord",
			check: func(t *testing.T, m map[string]any) {
				t.Helper()
				if len(m) != 1 {
					t.Errorf("discord 顶层字段数 = %d, want 1: %v", len(m), m)
				}
				if got := m["content"]; got != expectedText {
					t.Errorf("content = %#v, want %q", got, expectedText)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			srv, cap := newCaptureServer(t, http.StatusOK)
			n := testNotifier(srv.URL, tc.format)

			results := n.sendAll(context.Background(), notification{
				To: to, Subject: subject, Body: body,
			})
			if len(results) != 1 || !results[0].OK {
				t.Fatalf("投递失败: %+v", results)
			}

			req := cap.one(t)
			if req.Method != http.MethodPost {
				t.Errorf("method = %q, want POST", req.Method)
			}
			if ct := req.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var m map[string]any
			if err := json.Unmarshal(req.Body, &m); err != nil {
				t.Fatalf("请求体不是合法 JSON: %v\n%s", err, req.Body)
			}
			tc.check(t, m)
		})
	}
}

// TestCustomHeadersAreSent 自定义头必须原样带上。
func TestCustomHeadersAreSent(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	n := newNotifier(Config{
		Webhooks: []WebhookConfig{{
			URL:            srv.URL,
			Format:         "generic",
			Headers:        map[string]string{"X-Custom": "value", "Authorization": "Bearer test-token"},
			TimeoutSeconds: 2,
		}},
		IncludeBody:  true,
		MaxBodyChars: 2000,
	})

	if results := n.sendAll(context.Background(), notification{To: "u", Subject: "s", Body: "b"}); len(results) != 1 || !results[0].OK {
		t.Fatalf("投递失败: %+v", results)
	}

	req := cap.one(t)
	if got := req.Header.Get("X-Custom"); got != "value" {
		t.Errorf("X-Custom = %q, want value", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", got)
	}
}

// TestRejectsNonHTTPScheme 非 http/https 必须直接拒绝，绝不能发起请求。
func TestRejectsNonHTTPScheme(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	badURL := strings.Replace(srv.URL, "http://", "ftp://", 1)

	n := testNotifier(badURL, "generic")
	results := n.sendAll(context.Background(), notification{To: "u", Subject: "s", Body: "b"})
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(results))
	}
	if results[0].OK {
		t.Fatalf("ftp:// 不应被当作成功: %+v", results[0])
	}
	if results[0].Attempts != 0 {
		t.Errorf("被拒绝的 URL 不应发出请求，Attempts=%d", results[0].Attempts)
	}
	if !strings.Contains(results[0].Err, "scheme") {
		t.Errorf("错误应说明 scheme 问题，实际 %q", results[0].Err)
	}
	if got := cap.count(); got != 0 {
		t.Errorf("拒绝的 URL 仍向服务器发了 %d 个请求", got)
	}
}

// TestIgnoreSubjectsNoRequest 命中忽略列表时完全不发送。
func TestIgnoreSubjectsNoRequest(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	n := newNotifier(Config{
		Webhooks: []WebhookConfig{{
			URL: srv.URL, Format: "generic", TimeoutSeconds: 2,
		}},
		IncludeBody:    true,
		MaxBodyChars:   2000,
		IgnoreSubjects: []string{"验证码"},
	})

	res := n.forwardEmail(context.Background(), notification{
		To: "u@example.com", Subject: "您的验证码是 123456", Body: "ignored",
	})
	if got := res.CtxPatch["notifyWebhook"]; got != "ignored" {
		t.Errorf("notifyWebhook = %#v, want ignored", got)
	}
	if got := cap.count(); got != 0 {
		t.Fatalf("命中 ignoreSubjects 仍发送了 %d 个请求", got)
	}

	// 反向确认：没命中时仍会发送。否则上面的零请求也可能来自别的原因。
	res = n.forwardEmail(context.Background(), notification{
		To: "u@example.com", Subject: "备份完成", Body: "ok",
	})
	if got := res.CtxPatch["notifyWebhook"]; got != "sent" {
		t.Errorf("未命中时 notifyWebhook = %#v, want sent", got)
	}
	if got := cap.count(); got != 1 {
		t.Errorf("未命中时请求数 = %d, want 1", got)
	}
}

// TestMaxBodyCharsTruncates 截断按 Unicode 字符计时必须注意多字节字符。
func TestMaxBodyCharsTruncates(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK)
	n := newNotifier(Config{
		Webhooks: []WebhookConfig{{
			URL: srv.URL, Format: "generic", TimeoutSeconds: 2,
		}},
		IncludeBody:  true,
		MaxBodyChars: 32,
	})
	original := strings.Repeat("汉", 200)

	if results := n.sendAll(context.Background(), notification{
		To: "u@example.com", Subject: "长正文", Body: original,
	}); len(results) != 1 || !results[0].OK {
		t.Fatalf("投递失败: %+v", results)
	}

	req := cap.one(t)
	var m map[string]any
	if err := json.Unmarshal(req.Body, &m); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v", err)
	}
	got, _ := m["body"].(string)
	if got == original {
		t.Fatal("正文没有被截断")
	}
	if n := utf8.RuneCountInString(got); n > 32 {
		t.Errorf("截断后正文 %d 字符，超过 maxBodyChars=32", n)
	}
	if !strings.Contains(got, "已截断") {
		t.Errorf("截断后必须注明，实际 %q", got)
	}
	if !strings.HasPrefix(got, "汉") {
		t.Errorf("截断必须保留开头内容，实际 %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("截断产生了非法 UTF-8: %q", got)
	}
}

// Test5xxRetriesOnce 5xx 必须总共请求 2 次（首次 + 1 次重试）。
func Test5xxRetriesOnce(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	n := testNotifier(srv.URL, "generic")
	results := n.sendAll(context.Background(), notification{To: "u", Subject: "s", Body: "b"})
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(results))
	}
	if results[0].OK {
		t.Fatal("5xx 不应被当作成功")
	}
	if got := count.Load(); got != 2 {
		t.Errorf("5xx 总请求次数 = %d, want 2（首次 + 1 次重试）", got)
	}
	if results[0].Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", results[0].Attempts)
	}
}

// Test4xxDoesNotRetry 4xx 重试没有意义，必须只请求 1 次。
func Test4xxDoesNotRetry(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	n := testNotifier(srv.URL, "generic")
	results := n.sendAll(context.Background(), notification{To: "u", Subject: "s", Body: "b"})
	if len(results) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(results))
	}
	if results[0].OK {
		t.Fatal("4xx 不应被当作成功")
	}
	if got := count.Load(); got != 1 {
		t.Errorf("4xx 总请求次数 = %d, want 1", got)
	}
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", results[0].Attempts)
	}
}

// TestLogsRedactURLCredentials 这是安全红线的回归测试：
// 带 user:pass 与路径 token 的 URL 绝不能以任何形式进入 stderr。
func TestLogsRedactURLCredentials(t *testing.T) {
	srv, _ := newCaptureServer(t, http.StatusInternalServerError)
	logs := captureLogs(t)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析测试 URL 失败: %v", err)
	}
	u.User = url.UserPassword("alice", "sup3rpass")
	u.Path = "/services/T000/B000/topsecret-token"

	n := testNotifier(u.String(), "generic")
	results := n.sendAll(context.Background(), notification{To: "u", Subject: "s", Body: "b"})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("预期失败结果，实际 %+v", results)
	}

	out := logs.String()
	if out == "" {
		t.Fatal("没有捕获到任何日志，测试本身失效了")
	}
	for _, secret := range []string{"sup3rpass", "pass", "topsecret-token", "alice"} {
		if strings.Contains(out, secret) {
			t.Errorf("stderr 泄露了 %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "http://127.0.0.1:") {
		t.Errorf("脱敏后的日志应保留 host 便于排查:\n%s", out)
	}
	if !strings.Contains(out, "/…") {
		t.Errorf("脱敏后的日志应使用 scheme://host/… 形式:\n%s", out)
	}
}

// TestOnCLICommandDispatch 命令名匹配与否的行为必须区分清楚。
func TestOnCLICommandDispatch(t *testing.T) {
	t.Run("命令名不匹配时无输出", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		logs := captureLogs(t)
		n := testNotifier(srv.URL, "generic")

		res := n.cliResult(map[string]any{"command": "some-other-command"})
		if len(res.CtxPatch) != 0 {
			t.Errorf("不应写 ctxPatch: %#v", res.CtxPatch)
		}
		if res.Error != "" {
			t.Errorf("不应返回错误: %q", res.Error)
		}
		if got := cap.count(); got != 0 {
			t.Errorf("命令不匹配时不应发请求，实际 %d 个", got)
		}
		if logs.Len() != 0 {
			t.Errorf("命令不匹配时不应有日志: %s", logs.String())
		}
	})

	t.Run("匹配时发送测试消息并回传cliOutput", func(t *testing.T) {
		srv, cap := newCaptureServer(t, http.StatusOK)
		n := testNotifier(srv.URL, "slack")

		res := fire(n, fireParams{Hook: hookCLI, Ctx: map[string]any{
			"command": pluginName, "args": []any{}, "argsRaw": "",
		}}, context.Background())
		out, ok := res.CtxPatch["cliOutput"].(string)
		if !ok || strings.TrimSpace(out) == "" {
			t.Fatalf("cliOutput 为空: %#v", res.CtxPatch)
		}
		if _, exists := res.CtxPatch["cliExitCode"]; exists {
			t.Errorf("全部成功时不应设置 cliExitCode: %#v", res.CtxPatch)
		}
		if !strings.Contains(out, "成功 1 个") {
			t.Errorf("cliOutput 应报告成功数:\n%s", out)
		}
		if got := cap.count(); got != 1 {
			t.Errorf("自测请求数 = %d, want 1", got)
		}
	})

	t.Run("失败时cliExitCode为1", func(t *testing.T) {
		srv, _ := newCaptureServer(t, http.StatusBadRequest)
		n := testNotifier(srv.URL, "generic")

		res := n.cliResult(map[string]any{"command": pluginName})
		if got := res.CtxPatch["cliExitCode"]; got != 1 {
			t.Errorf("cliExitCode = %#v, want 1", got)
		}
		out, _ := res.CtxPatch["cliOutput"].(string)
		if !strings.Contains(out, "失败 1 个") {
			t.Errorf("cliOutput 应报告失败数:\n%s", out)
		}
	})

	t.Run("没配webhook时能看出来", func(t *testing.T) {
		n := newNotifier(defaultConfig())
		res := n.cliResult(map[string]any{"command": pluginName})
		out, _ := res.CtxPatch["cliOutput"].(string)
		if !strings.Contains(out, "未配置任何 webhook") {
			t.Errorf("cliOutput 应明确说明未配置 webhook:\n%s", out)
		}
		if got := res.CtxPatch["cliExitCode"]; got != 1 {
			t.Errorf("cliExitCode = %#v, want 1", got)
		}
	})
}

// TestInitProtocolMismatch 协议不匹配必须返回 ok:false，而不是勉强启动。
func TestInitProtocolMismatch(t *testing.T) {
	_ = captureLogs(t)
	var out bytes.Buffer
	cfg := handleInit(request{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "init",
		Params: json.RawMessage(`{
			"hostVersion":"test","hostProtocol":2,"pluginName":"notify-webhook",
			"dataDir":"/tmp/x","config":{"webhooks":[]}
		}`),
	}, &out)

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  struct {
			OK    bool     `json:"ok"`
			Name  string   `json:"name"`
			Hooks []string `json:"hooks"`
			Error string   `json:"error"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout 不是合法 JSON: %v\n%s", err, out.String())
	}
	if resp.JSONRPC != "2.0" || resp.ID != 7 {
		t.Errorf("响应头不对: %+v", resp)
	}
	if resp.Result.OK {
		t.Fatal("hostProtocol != 1 时必须 ok:false")
	}
	if !strings.Contains(resp.Result.Error, "协议版本不匹配") {
		t.Errorf("error = %q，应说明协议版本问题", resp.Result.Error)
	}
	if len(cfg.Webhooks) != 0 {
		t.Errorf("握手失败后应使用零配置，实际 %+v", cfg)
	}
}

// TestInitConfigParseFailureStartsEmpty 配置坏了要能启动，但必须零 webhook。
func TestInitConfigParseFailureStartsEmpty(t *testing.T) {
	logs := captureLogs(t)
	var out bytes.Buffer
	cfg := handleInit(request{
		JSONRPC: "2.0",
		ID:      8,
		Method:  "init",
		Params: json.RawMessage(`{
			"hostVersion":"test","hostProtocol":1,"pluginName":"notify-webhook",
			"dataDir":"/tmp/x","config":{"webhooks":"not-an-array"}
		}`),
	}, &out)

	if len(cfg.Webhooks) != 0 {
		t.Errorf("配置解析失败后应零配置，实际 %+v", cfg)
	}
	if !strings.Contains(logs.String(), "配置解析失败") {
		t.Errorf("应记录配置解析失败日志: %s", logs.String())
	}
	var resp struct {
		Result struct {
			OK bool `json:"ok"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout 不是合法 JSON: %v", err)
	}
	if !resp.Result.OK {
		t.Fatal("配置解析失败不应阻止插件启动")
	}
}
