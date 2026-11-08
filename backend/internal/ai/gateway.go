package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// Result 一次模型调用的归一化结果。
type Result struct {
	Text             string `json:"text"`
	Provider         string `json:"provider"` // plugin:<name> / gateway / local
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	LatencyMs        int64  `json:"latencyMs"`
	// Fallback=true 表示没有任何可用模型，调用方应改用 L0 本地算法。
	Fallback bool `json:"fallback"`
}

var errRateLimited = errors.New("AI 请求过于频繁，请稍后再试")

// ---- 轻量滑动窗口限流（按用户，内存态，重启即清空，够用且不引入依赖） ----

var (
	rateMu   sync.Mutex
	rateHits = map[uint][]time.Time{}
)

func allow(userID uint, limit int) bool {
	if limit <= 0 {
		return true
	}
	now := time.Now()
	rateMu.Lock()
	defer rateMu.Unlock()
	hits := rateHits[userID]
	kept := hits[:0]
	for _, t := range hits {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		rateHits[userID] = kept
		return false
	}
	rateHits[userID] = append(kept, now)
	// 防止长期不活跃用户占着 map 不放。
	if len(rateHits) > 4096 {
		for k, v := range rateHits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > 2*time.Minute {
				delete(rateHits, k)
			}
		}
	}
	return true
}

// Complete 走完整三档：插件接管 → AI 网关 → 本地兜底。
//
// 调用方拿到 Fallback=true 时，应改用 local.go 的本地算法。
// 只有「钩子明确拦截」或「超过配额」才返回 error。
func Complete(ctx context.Context, userID uint, scene, prompt string, maxTokens int) (Result, error) {
	cfg := LoadConfig()
	started := time.Now()
	if maxTokens <= 0 || maxTokens > cfg.MaxTokens {
		maxTokens = cfg.MaxTokens
	}

	// ---- onAIRequest：可拦截、可改写请求 ----
	reqCtx := map[string]any{
		"userId": userID, "scene": scene, "provider": cfg.Provider,
		"model": cfg.Model, "promptChars": len(prompt), "codeSpace": scene == SceneComplete,
		"prompt": prompt,
	}
	if errs := plugin.FireIf(plugin.HookAIRequest, reqCtx); len(errs) > 0 {
		if ok, reason := plugin.DecisionOf(errs); !ok {
			return Result{}, fmt.Errorf("AI 请求被插件拦截：%s", reason)
		}
	}
	if s := ctxStr(reqCtx, "prompt"); s != "" {
		prompt = s
	}
	if s := ctxStr(reqCtx, "model"); s != "" {
		cfg.Model = s
	}

	if !allow(userID, cfg.RateLimit) {
		return Result{}, errRateLimited
	}

	var res Result
	var err error

	// ---- L3：插件接管（订阅型 + 点名 Provider 两条路） ----
	if cfg.Enabled && (cfg.Provider != "" || plugin.HasSubscribers(plugin.HookAIProviderResolve)) {
		if r, handled := callPlugin(userID, scene, prompt, maxTokens, cfg); handled {
			res = r
		}
	}

	// ---- L1：内置 OpenAI 兼容网关 ----
	if res.Text == "" && cfg.Enabled && cfg.SceneEnabled(scene) && cfg.APIKey != "" {
		res, err = callGateway(ctx, cfg, prompt, maxTokens)
		if err != nil {
			// 模型挂了不让功能报错，降级到 L0，错误进审计。
			res = Result{}
			writeAudit(userID, scene, "gateway", cfg.Model, len(prompt), 0, 0,
				time.Since(started).Milliseconds(), false, err.Error())
			err = nil
		}
	}

	if err != nil {
		return Result{}, err
	}

	// ---- L0：没有可用模型，交给调用方本地算法 ----
	if res.Text == "" {
		res = Result{Provider: "local", Model: "local", Fallback: true,
			LatencyMs: time.Since(started).Milliseconds()}
	} else {
		res.LatencyMs = time.Since(started).Milliseconds()
		writeAudit(userID, scene, res.Provider, res.Model, len(prompt),
			res.PromptTokens, res.CompletionTokens, res.LatencyMs, true, "")
	}

	// ---- onAIResponse：纯通知，异步派发不阻塞 ----
	respCtx := map[string]any{
		"userId": userID, "scene": scene, "provider": res.Provider, "model": res.Model,
		"success": res.Text != "" && !res.Fallback, "latencyMs": res.LatencyMs,
		"promptTokens": res.PromptTokens, "completionTokens": res.CompletionTokens,
		"error": "",
	}
	plugin.FireIf(plugin.HookAIResponse, respCtx)

	return res, nil
}

// callPlugin 让社区插件提供模型能力。
// 支持两种接入：订阅 onAIProviderResolve 写 handled/result，或在 co.BEGG
// 里注册 kind=ai 的能力再由后台点名。
func callPlugin(userID uint, scene, prompt string, maxTokens int, cfg Config) (Result, bool) {
	try := func(name string) (Result, bool) {
		pctx := map[string]any{
			"userId": userID, "scene": scene, "prompt": prompt,
			"maxTokens": maxTokens, "model": cfg.Model, "handled": false,
		}
		if name != "" {
			plugin.CallProvider(plugin.ProviderAI, name, pctx)
		} else {
			plugin.FireIf(plugin.HookAIProviderResolve, pctx)
		}
		if !ctxBool(pctx, "handled") {
			// 兼容只写 result 不写 handled 的插件。
			if ctxStr(pctx, "result") == "" {
				return Result{}, false
			}
		}
		text := ctxStr(pctx, "result")
		if text == "" {
			text = ctxStr(pctx, "answer")
		}
		if text == "" {
			return Result{}, false
		}
		p := ctxStr(pctx, "provider")
		if p == "" {
			p = "plugin:" + name
		}
		return Result{Text: text, Provider: p, Model: ctxStr(pctx, "model")}, true
	}
	// 先订阅型钩子（所有订阅者共享 ctx，后面的能覆盖前面的）。
	if plugin.HasSubscribers(plugin.HookAIProviderResolve) {
		if r, ok := try(""); ok {
			return r, true
		}
	}
	// 再点名 Provider。
	if cfg.Provider != "" {
		if r, ok := try(cfg.Provider); ok {
			return r, true
		}
	}
	return Result{}, false
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
		Text    string      `json:"text"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func callGateway(ctx context.Context, cfg Config, prompt string, maxTokens int) (Result, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Result{}, fmt.Errorf("AI baseUrl 非法（必须是 http/https 完整地址）：%q", cfg.BaseURL)
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	body, _ := json.Marshal(chatRequest{
		Model: cfg.Model, Messages: []chatMessage{{Role: "user", Content: prompt}},
		MaxTokens: maxTokens, Temperature: cfg.Temperature, Stream: false,
	})

	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return Result{}, fmt.Errorf("模型返回 HTTP %d：%s", resp.StatusCode, msg)
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Result{}, fmt.Errorf("模型响应解析失败：%v", err)
	}
	if len(cr.Error) > 0 && string(cr.Error) != "null" {
		return Result{}, fmt.Errorf("模型返回错误：%s", string(cr.Error))
	}
	text := ""
	if len(cr.Choices) > 0 {
		text = cr.Choices[0].Message.Content
		if text == "" {
			text = cr.Choices[0].Text
		}
	}
	if text == "" {
		return Result{}, errors.New("模型返回空内容")
	}
	return Result{
		Text: text, Provider: "gateway", Model: cfg.Model,
		PromptTokens: cr.Usage.PromptTokens, CompletionTokens: cr.Usage.CompletionTokens,
	}, nil
}

// Test 后台「测试连通性」：用一份临时配置直连模型，不改落库配置。
func Test(ctx context.Context, cfg Config) (string, error) {
	cfg = cfg.normalize()
	if cfg.APIKey == MaskPlaceholder || cfg.APIKey == "" {
		cfg.APIKey = LoadConfig().APIKey
	}
	if cfg.APIKey == "" {
		return "", errors.New("尚未填写 API Key")
	}
	r, err := callGateway(ctx, cfg, "请只回复两个字：正常", 32)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(r.Text), nil
}

func writeAudit(userID uint, scene, provider, model string, promptChars,
	promptTokens, completionTokens int, latencyMs int64, success bool, errMsg string) {
	d := db.Get()
	if d == nil {
		return
	}
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	// 基础日志：只记长度与统计，不记录 prompt 正文，避免文件内容落审计表。
	_ = d.Create(&models.AIAuditLog{
		UserID: userID, Scene: scene, Provider: provider, Model: model,
		PromptChars: promptChars, PromptTokens: promptTokens,
		CompletionTokens: completionTokens, LatencyMs: latencyMs,
		Success: success, Error: errMsg,
	}).Error
}

// ---- ctx 读取助手（插件通过 JSON 回写，类型会退化） ----

func ctxStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%v", v)
	case int:
		return fmt.Sprintf("%d", v)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func ctxBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "yes"
	case float64:
		return v != 0
	}
	return false
}

func ctxStrSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	out := []string{}
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
