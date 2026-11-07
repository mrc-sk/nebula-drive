package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 这组测试覆盖 HMAC 签名校验。重点不是"能通过"，而是**每一个被签名的
// 组成部分都真的被覆盖到** —— 少了任何一个，攻击者就能在不改签名的
// 前提下替换那部分内容，而系统看起来一切正常。
//
// 所以核心用例是"篡改表"：签好名之后逐个字段改一下，每一个都必须被拒。

func testConfig() Config {
	return Config{
		SharedSecret:     "test-shared-secret",
		PrincipalUserID:  42,
		SignatureHeader:  "X-Nebula-Signature",
		TimestampHeader:  "X-Nebula-Timestamp",
		Algorithm:        "sha256",
		ToleranceSeconds: 300,
	}
}

func bodyHashOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// signedCtx 按规范串生成一个**可通过校验**的 ctx，ct 是请求体内容。
//
// headers 用 map[string]string，模拟进程内形态；另有用例单独覆盖
// JSON 往返后的 map[string]any。
func signedCtx(t *testing.T, cfg Config, method, path, query, body string) map[string]any {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	bh := bodyHashOf(body)
	sig := hmacHex(cfg.SharedSecret, canonicalString(method, path, query, ts, bh))
	return map[string]any{
		"method":           method,
		"path":             path,
		"query":            query,
		"ip":               "203.0.113.7",
		"ua":               "test-agent",
		"hasAuthorization": true,
		"contentType":      "application/json",
		"contentLength":    0,
		"bodyHash":         "sha256:" + bh,
		"bodyTruncated":    false,
		"headers": map[string]string{
			cfg.SignatureHeader: sig,
			cfg.TimestampHeader: ts,
		},
	}
}

// TestVerifyAcceptsValidSignature 基线：正确签名必须通过。
func TestVerifyAcceptsValidSignature(t *testing.T) {
	cfg := testConfig()
	ctx := signedCtx(t, cfg, "POST", "/api/v1/files", "a=1&b=2", `{"k":"v"}`)
	if reason, ok := cfg.verify(ctx); !ok {
		t.Fatalf("正确签名被拒: %s", reason)
	}
}

// TestVerifyRejectsTampering 篡改表：签好之后逐项改动，每一项都必须被拒。
//
// 这张表就是"规范串里为什么一个字段都不能省"的证明：
// 若某字段没进规范串，改动它不会让签名失配，这一行就会 FAIL。
func TestVerifyRejectsTampering(t *testing.T) {
	cfg := testConfig()

	cases := []struct {
		name   string
		mutate func(ctx map[string]any)
	}{
		{"改方法", func(ctx map[string]any) { ctx["method"] = "DELETE" }},
		{"改路径", func(ctx map[string]any) { ctx["path"] = "/api/v1/admin/users" }},
		{"改查询串", func(ctx map[string]any) { ctx["query"] = "a=1&b=999" }},
		{"改请求体摘要", func(ctx map[string]any) {
			ctx["bodyHash"] = "sha256:" + bodyHashOf(`{"k":"evil"}`)
		}},
		{"改时间戳", func(ctx map[string]any) {
			hs := ctx["headers"].(map[string]string)
			hs[cfg.TimestampHeader] = strconv.FormatInt(time.Now().Unix()-10, 10)
		}},
		{"改签名", func(ctx map[string]any) {
			hs := ctx["headers"].(map[string]string)
			sig := hs[cfg.SignatureHeader]
			// 只改最后一个字符
			last := "0"
			if strings.HasSuffix(sig, "0") {
				last = "1"
			}
			hs[cfg.SignatureHeader] = sig[:len(sig)-1] + last
		}},
		{"签名头用别的密钥签", func(ctx map[string]any) {
			other := testConfig()
			other.SharedSecret = "wrong-secret"
			hs := ctx["headers"].(map[string]string)
			hs[cfg.SignatureHeader] = hmacHex(other.SharedSecret, canonicalString(
				"POST", "/api/v1/files", "a=1&b=2",
				hs[cfg.TimestampHeader], bodyHashOf(`{"k":"v"}`)))
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := signedCtx(t, cfg, "POST", "/api/v1/files", "a=1&b=2", `{"k":"v"}`)
			c.mutate(ctx)
			if reason, ok := cfg.verify(ctx); ok {
				t.Fatalf("%s 之后签名仍然通过 —— 该字段没有被纳入规范串", c.name)
			} else if reason == "" {
				t.Fatal("拒绝时必须给出原因")
			}
		})
	}
}

// TestVerifyRejectsTimeSkew 时间窗：过旧或过新的时间戳都必须被拒。
//
// 这是防重放的唯一手段：签名本身是静态的，没有时间窗就能被无限重放。
func TestVerifyRejectsTimeSkew(t *testing.T) {
	cfg := testConfig()
	skews := []struct {
		name string
		secs int64
	}{
		{"超过容差的过去", -3600},
		{"超过容差的未来", 3600},
	}
	for _, s := range skews {
		t.Run(s.name, func(t *testing.T) {
			ts := strconv.FormatInt(time.Now().Unix()+s.secs, 10)
			bh := bodyHashOf("")
			ctx := map[string]any{
				"method": "GET", "path": "/api/v1/files", "query": "",
				"bodyHash": "sha256:" + bh, "bodyTruncated": false,
				"headers": map[string]string{
					cfg.SignatureHeader: hmacHex(cfg.SharedSecret,
						canonicalString("GET", "/api/v1/files", "", ts, bh)),
					cfg.TimestampHeader: ts,
				},
			}
			if _, ok := cfg.verify(ctx); ok {
				t.Fatalf("偏差 %ds 应被拒", s.secs)
			}
		})
	}
}

// TestVerifyFailsClosed 配置不全时必须拒绝，绝不能默认放行。
//
// 红线：没密钥就等于没鉴权；没有 principalUserId 就无法认定身份。
func TestVerifyFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"缺 sharedSecret", func() Config { c := testConfig(); c.SharedSecret = ""; return c }()},
		{"缺 principalUserId", func() Config { c := testConfig(); c.PrincipalUserID = 0; return c }()},
		{"algorithm 不支持", func() Config { c := testConfig(); c.Algorithm = "md5"; return c }()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 即便请求本身"看起来"带齐了头，也必须拒绝
			ctx := map[string]any{
				"method": "GET", "path": "/api/v1/files", "query": "",
				"bodyHash": "sha256:" + bodyHashOf(""), "bodyTruncated": false,
				"headers": map[string]string{
					c.cfg.SignatureHeader: "deadbeef",
					c.cfg.TimestampHeader: strconv.FormatInt(time.Now().Unix(), 10),
				},
			}
			if _, ok := c.cfg.verify(ctx); ok {
				t.Fatal("配置不完整时必须拒绝（fail closed）")
			}
		})
	}
}

// TestVerifyRejectsMissingHeaders 缺签名头 / 缺时间戳头都要拒绝并说明原因。
func TestVerifyRejectsMissingHeaders(t *testing.T) {
	cfg := testConfig()

	t.Run("缺签名头", func(t *testing.T) {
		ctx := signedCtx(t, cfg, "GET", "/api/v1/files", "", "")
		delete(ctx["headers"].(map[string]string), cfg.SignatureHeader)
		reason, ok := cfg.verify(ctx)
		if ok {
			t.Fatal("缺签名头应被拒")
		}
		if !strings.Contains(reason, cfg.SignatureHeader) {
			t.Errorf("原因应点明缺哪个头，实际: %s", reason)
		}
	})

	t.Run("缺时间戳头", func(t *testing.T) {
		ctx := signedCtx(t, cfg, "GET", "/api/v1/files", "", "")
		delete(ctx["headers"].(map[string]string), cfg.TimestampHeader)
		if _, ok := cfg.verify(ctx); ok {
			t.Fatal("缺时间戳头应被拒")
		}
	})

	t.Run("时间戳不是整数", func(t *testing.T) {
		ctx := signedCtx(t, cfg, "GET", "/api/v1/files", "", "")
		ctx["headers"].(map[string]string)[cfg.TimestampHeader] = "not-a-number"
		if _, ok := cfg.verify(ctx); ok {
			t.Fatal("非整数时间戳应被拒")
		}
	})
}

// TestVerifyRejectsTruncatedBody 宿主没读完 body 时必须拒绝。
//
// 放行等于允许攻击者篡改请求体而签名依然通过 —— 正是 bodyHash 要防的事。
func TestVerifyRejectsTruncatedBody(t *testing.T) {
	cfg := testConfig()
	ctx := signedCtx(t, cfg, "POST", "/api/v1/files", "", "small")
	ctx["bodyTruncated"] = true
	reason, ok := cfg.verify(ctx)
	if ok {
		t.Fatal("bodyTruncated 时必须拒绝")
	}
	if !strings.Contains(reason, "body") {
		t.Errorf("原因应点明是请求体问题，实际: %s", reason)
	}
}

// TestVerifyAcceptsUppercaseHexSignature 十六进制大小写不应影响判定。
func TestVerifyAcceptsUppercaseHexSignature(t *testing.T) {
	cfg := testConfig()
	ctx := signedCtx(t, cfg, "GET", "/api/v1/files", "", "")
	hs := ctx["headers"].(map[string]string)
	hs[cfg.SignatureHeader] = strings.ToUpper(hs[cfg.SignatureHeader])
	if reason, ok := cfg.verify(ctx); !ok {
		t.Fatalf("大写十六进制签名应被接受: %s", reason)
	}
}

// TestCanonicalStringOrderAndSeparator 规范串的格式是**对外约定**：
// 顺序或分隔符改动会让所有既有客户端签名失效，且现象是"签名全不匹配"。
func TestCanonicalStringOrderAndSeparator(t *testing.T) {
	got := canonicalString("post", "/api/v1/files", "a=1", "1700000000", "abcd")
	want := "POST\n/api/v1/files\na=1\n1700000000\nabcd"
	if got != want {
		t.Fatalf("规范串格式变了:\n got=%q\nwant=%q", got, want)
	}
	// 方法统一大写：客户端传 post 与 POST 必须得到同一个串
	if canonicalString("POST", "/p", "", "1", "h") != got[:0]+"POST\n/p\n\n1\nh" {
		t.Fatal("方法大小写未归一化")
	}
}

// TestIsExemptUsesSegmentBoundary 免签路径必须按**路径段边界**匹配。
//
// 裸 strings.HasPrefix 会让 /api/v1/health 意外放行 /api/v1/healthcheck ——
// 表面只是"多放行一个端点"，实际是放行了一个**没人打算公开**的端点。
func TestIsExemptUsesSegmentBoundary(t *testing.T) {
	cfg := testConfig()
	cfg.ExemptPaths = []string{"/api/v1/health"}

	allow := []string{"/api/v1/health", "/api/v1/health/live", "/api/v1/health/ready"}
	deny := []string{"/api/v1/healthcheck", "/api/v1/health-secret", "/api/v1/files", "/api/v1"}

	for _, p := range allow {
		if !cfg.isExempt(p) {
			t.Errorf("%s 应命中免签", p)
		}
	}
	for _, p := range deny {
		if cfg.isExempt(p) {
			t.Errorf("%s 不该命中免签（路径段边界不匹配）", p)
		}
	}
}

// TestHeaderValueHandlesBothMapTypes headerValue 必须兼容两种形态：
// 进程内是 map[string]string，进程外插件经 JSON 往返后是 map[string]any。
// 只认一种的后果是"永远读不到头"，而且不会有任何报错。
func TestHeaderValueHandlesBothMapTypes(t *testing.T) {
	ctxStr := map[string]any{"headers": map[string]string{"X-A": "1"}}
	ctxAny := map[string]any{"headers": map[string]any{"X-A": "1"}}
	ctxCS := map[string]any{"headers": map[string]any{"x-a": "1"}} // 大小写不敏感

	for name, ctx := range map[string]map[string]any{
		"map[string]string": ctxStr, "map[string]any": ctxAny, "大小写不敏感": ctxCS,
	} {
		if v, ok := headerValue(ctx, "X-A"); !ok || v != "1" {
			t.Errorf("%s: headerValue = (%q,%v)", name, v, ok)
		}
	}

	// 没有 headers 字段、或类型完全不对时，不能 panic，返回未命中即可
	for _, ctx := range []map[string]any{{}, {"headers": "not-a-map"}, {"headers": nil}} {
		if _, ok := headerValue(ctx, "X-A"); ok {
			t.Errorf("异常 headers=%#v 时不该命中", ctx["headers"])
		}
	}
}

// TestHandleFireBlocksByDefault 端到端地确认默认结果是"拦截"。
//
// 这里直接调 verify 与 isExempt 的组合语义（handleFire 只负责把它们
// 组装成 RPC 响应，真正的判定在这两个函数里）。**缺签名头时也必须拒绝**：
// 若"没带头"就悄悄退回 401，任何人都能靠不带签名头绕过这层保护。
func TestVerifyMissingSignatureIsDenied(t *testing.T) {
	cfg := testConfig()
	ctx := map[string]any{
		"method": "GET", "path": "/api/v1/files", "query": "",
		"bodyHash": "sha256:" + bodyHashOf(""), "bodyTruncated": false,
		"headers": map[string]string{}, // 完全没有签名头
	}
	if _, ok := cfg.verify(ctx); ok {
		t.Fatal("没有签名头时必须拒绝（否则不带签名就能绕过这层保护）")
	}
}

// TestRedactedNeverLeaksSecret 日志摘要不得包含密钥内容。
func TestRedactedNeverLeaksSecret(t *testing.T) {
	cfg := testConfig()
	cfg.SharedSecret = "super-secret-value-123"
	out := cfg.redacted()
	if strings.Contains(out, cfg.SharedSecret) {
		t.Fatalf("日志摘要泄露了密钥: %s", out)
	}
	if strings.Contains(out, "super-secret") {
		t.Fatalf("日志摘要泄露了密钥片段: %s", out)
	}
	if !strings.Contains(out, "<set:") {
		t.Errorf("应说明密钥已设置但不显示内容: %s", out)
	}
}
