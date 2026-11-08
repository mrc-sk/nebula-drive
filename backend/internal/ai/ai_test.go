package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/pkg/plugin"
)

// ---- L0 本地算法 ----

func TestLocalParseQueryChinese(t *testing.T) {
	cases := []struct {
		q       string
		ext     string
		typ     string
		minSize int64
		sort    string
	}{
		{"找出所有大于 10MB 的 PDF", "pdf", "", 10 << 20, ""},
		{"最近一周改过的 go 代码", "go", "", 0, ""},
		{"把最大的图片列出来", "", "img", 0, "size"},
		{"按名字排序文档", "", "doc", 0, "name"},
	}
	for _, c := range cases {
		f := LocalParseQuery(c.q)
		if c.ext != "" && f.Ext != c.ext {
			t.Errorf("%q: ext=%q want %q", c.q, f.Ext, c.ext)
		}
		if c.typ != "" && f.Type != c.typ {
			t.Errorf("%q: type=%q want %q", c.q, f.Type, c.typ)
		}
		if c.minSize > 0 && f.MinSize != c.minSize {
			t.Errorf("%q: minSize=%d want %d", c.q, f.MinSize, c.minSize)
		}
		if c.sort != "" && f.Sort != c.sort {
			t.Errorf("%q: sort=%q want %q", c.q, f.Sort, c.sort)
		}
	}
	if f := LocalParseQuery("最近 3 天上传的文件"); f.After == "" {
		t.Error("应解析出时间条件 after")
	}
}

func TestLocalCompletion(t *testing.T) {
	doc := `function calculateTotal(price, count) {
  const total = price * count;
  return total;
}
const calculator = new Calculator();
`
	items := LocalCompletion(doc, "cal", "javascript", 10)
	found := map[string]bool{}
	for _, it := range items {
		found[it.Label] = true
		if it.InsertText == "" {
			t.Errorf("补全项 %q 缺 insertText", it.Label)
		}
	}
	if !found["calculateTotal"] && !found["calculator"] {
		t.Errorf("应从当前文档抽到标识符，实际 %v", found)
	}
	// 关键字补全：前缀 "con" 应命中 const / console
	kw := LocalCompletion("", "con", "javascript", 20)
	labels := map[string]bool{}
	for _, it := range kw {
		labels[it.Label] = true
	}
	if !labels["const"] || !labels["console"] {
		t.Errorf("关键字补全缺失，实际 %v", labels)
	}
	if len(LocalCompletion(doc, "zzzz", "go", 5)) != 0 {
		t.Error("无匹配前缀时应返回空候选")
	}
}

func TestLocalSuggest(t *testing.T) {
	history := []string{"年度报告.pdf", "项目计划.md"}
	names := []string{"年度报告.pdf", "配置.json", "README.md"}
	out := LocalSuggest(history, names, "报", 5)
	if len(out) == 0 || !strings.Contains(out[0], "报") {
		t.Errorf("子串联想失败: %v", out)
	}
	if got := LocalSuggest(history, names, "年度", 5); len(got) == 0 || got[0] != "年度报告.pdf" {
		t.Errorf("前缀命中应排最前: %v", got)
	}
	if got := LocalSuggest(nil, nil, "", 3); len(got) != 0 {
		t.Errorf("空输入不应产生联想: %v", got)
	}
}

func TestLocalSummaryAndTags(t *testing.T) {
	text := "NebulaDrive 是一个私有网盘。NebulaDrive 支持插件。插件可以扩展能力。插件可以扩展能力。"
	s, tags := LocalSummary(text, 100)
	if s == "" {
		t.Error("摘要不应为空")
	}
	if len([]rune(s)) > 110 {
		t.Errorf("摘要超长: %d", len([]rune(s)))
	}
	if len(tags) == 0 {
		t.Error("应提取到高频词标签")
	}
	t.Logf("summary=%q tags=%v", s, tags)
}

// ---- 配置与三档降级 ----

func TestDefaultConfigIsLocalAndOff(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled || c.CodeSpaceEnable {
		t.Error("AI 与代码空间必须默认关闭")
	}
	if ModeOf(c) != "local" {
		t.Errorf("默认档位应为 local，实际 %s", ModeOf(c))
	}
	if c.SceneEnabled(SceneAsk) {
		t.Error("总开关关闭时任何场景都不应允许调模型")
	}
}

func TestNormalizeClampsValues(t *testing.T) {
	c := Config{TimeoutSec: 99999, MaxTokens: -1, Temperature: 9, RateLimit: 0,
		BaseURL: "https://example.com/v1///", Scenes: map[string]bool{SceneAsk: false}}.normalize()
	if c.TimeoutSec != 30 || c.MaxTokens != 1024 || c.Temperature != 0.3 || c.RateLimit != 30 {
		t.Errorf("归一化未生效: %+v", c)
	}
	if c.BaseURL != "https://example.com/v1" {
		t.Errorf("尾部斜杠未裁剪: %q", c.BaseURL)
	}
	if c.Scenes[SceneAsk] || !c.Scenes[SceneSearch] {
		t.Errorf("场景补全错误: %v", c.Scenes)
	}
}

func TestCompleteFallsBackWithoutModel(t *testing.T) {
	// 无 DB、无 key、无插件订阅 → 必须安静降级到 L0，不能报错。
	invalidate()
	r, err := Complete(context.Background(), 1, SceneAsk, "你好", 64)
	if err != nil {
		t.Fatalf("无模型时不应返回错误: %v", err)
	}
	if !r.Fallback || r.Provider != "local" {
		t.Errorf("应降级到 local，实际 %+v", r)
	}
}

func TestGatewayCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("鉴权头错误: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"正常"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, APIKey: "test-key", Model: "m", TimeoutSec: 5, MaxTokens: 64}
	r, err := callGateway(context.Background(), cfg, "ping", 32)
	if err != nil {
		t.Fatalf("网关调用失败: %v", err)
	}
	if r.Text != "正常" || r.PromptTokens != 10 || r.CompletionTokens != 2 {
		t.Errorf("解析结果错误: %+v", r)
	}
}

func TestGatewayRejectsBadConfig(t *testing.T) {
	cfg := Config{BaseURL: "ftp://bad", APIKey: "x", Model: "m", TimeoutSec: 5}
	if _, err := callGateway(context.Background(), cfg, "ping", 32); err == nil {
		t.Error("非 http(s) 地址必须拒绝")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer bad.Close()
	cfg.BaseURL = bad.URL
	if _, err := callGateway(context.Background(), cfg, "ping", 32); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("应报出 HTTP 401，实际 %v", err)
	}
}

// ---- 协议层一致性 ----

func TestAIHooksRegisteredAndWired(t *testing.T) {
	want := []plugin.HookName{
		plugin.HookAIRequest, plugin.HookAIResponse, plugin.HookAIProviderResolve,
		plugin.HookAIComplete, plugin.HookAIRecommend, plugin.HookAISearch,
		plugin.HookAISummary, plugin.HookAIAsk, plugin.HookAIConfigChange,
	}
	for _, h := range want {
		m, ok := plugin.MetaOf(h)
		if !ok {
			t.Errorf("钩子 %s 未登记", h)
			continue
		}
		if !m.Wired {
			t.Errorf("钩子 %s 未接线", h)
		}
		if m.Group != plugin.GroupAI {
			t.Errorf("钩子 %s 分组应为 ai，实际 %s", h, m.Group)
		}
	}
	if _, ok := plugin.DefaultProviderHook[plugin.ProviderAI]; !ok {
		t.Error("ai Provider 缺默认钩子")
	}
}
