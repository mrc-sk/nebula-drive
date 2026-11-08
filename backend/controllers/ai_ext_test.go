package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/ai"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// 智能时代接口的端到端测试：走真实 gin 路由 + 真实 SQLite，验证
// 「默认关闭」「无模型时本地兜底」「配置脱敏」「代码空间门禁」四条关键行为。

func aiRouter(t *testing.T) (*gin.Engine, *models.User) {
	t.Helper()
	testutil.SetupDB(t)
	ai.InvalidateCache()
	u := makeUser(t, "aiuser", true)
	r := userRouter(u)
	r.GET("/api/ai/status", AIStatus)
	r.POST("/api/ai/search", AISearch)
	r.POST("/api/ai/ask", AIAsk)
	r.POST("/api/ai/complete", AIComplete)
	r.POST("/api/admin/ai/config", SaveAIConfig)
	r.GET("/api/admin/ai/config", GetAIConfig)
	r.GET("/api/files/suggest", SuggestFiles)
	r.GET("/api/files/recent", RecentFiles)
	r.GET("/api/files/hot", HotFiles)
	r.GET("/api/files/:id/content", GetFileContent)
	r.PUT("/api/files/:id/content", SaveFileContent)
	return r, u
}

func TestAIStatusDefaultOff(t *testing.T) {
	r, _ := aiRouter(t)
	w := doJSON(r, http.MethodGet, "/api/ai/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var res struct {
		Code int `json:"code"`
		Data struct {
			Enabled          bool   `json:"enabled"`
			CodeSpaceEnabled bool   `json:"codeSpaceEnabled"`
			Mode             string `json:"mode"`
		} `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &res)
	if res.Data.Enabled || res.Data.CodeSpaceEnabled {
		t.Errorf("AI 与代码空间必须默认关闭: %+v", res.Data)
	}
	if res.Data.Mode != "local" {
		t.Errorf("默认档位应为 local，实际 %s", res.Data.Mode)
	}
}

func TestSuggestAndRecentEmptyAreSafe(t *testing.T) {
	r, _ := aiRouter(t)
	for _, path := range []string{"/api/files/suggest?q=ab", "/api/files/recent", "/api/files/hot"} {
		w := doJSON(r, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	// 空库时三个接口都不能 panic / 500 —— 这是「新装站点第一次打开搜索框」的路径。
}

func TestAISearchLocalFallback(t *testing.T) {
	r, _ := aiRouter(t)
	w := doJSON(r, http.MethodPost, "/api/ai/search",
		map[string]any{"query": "找出所有大于 10MB 的 PDF", "limit": 10})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var res struct {
		Code int `json:"code"`
		Data struct {
			Answer  string         `json:"answer"`
			Mode    string         `json:"mode"`
			Filters map[string]any `json:"filters"`
			List    []any          `json:"list"`
		} `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &res)
	if res.Data.Mode != "local" {
		t.Errorf("无模型时应为 local，实际 %s", res.Data.Mode)
	}
	if res.Data.Filters["ext"] != "pdf" {
		t.Errorf("L0 应解析出 ext=pdf，实际 %v", res.Data.Filters)
	}
	if res.Data.Answer == "" {
		t.Error("应给出可执行的回答")
	}
}

func TestAICompleteReturnsLocalCandidates(t *testing.T) {
	r, _ := aiRouter(t)
	w := doJSON(r, http.MethodPost, "/api/ai/complete", map[string]any{
		"fileName": "main.go", "language": "go",
		"prefix": "fun", "doc": "func main() { fmt.Println(1) }",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var res struct {
		Data struct {
			Items []struct {
				Label string `json:"label"`
			} `json:"items"`
			Mode string `json:"mode"`
		} `json:"data"`
	}
	mustUnmarshal(t, w.Body.Bytes(), &res)
	if res.Data.Mode != "local" || len(res.Data.Items) == 0 {
		t.Errorf("应返回本地补全候选，实际 %+v", res.Data)
	}
}

func TestAIConfigSaveMasksKey(t *testing.T) {
	r, _ := aiRouter(t)
	body := map[string]any{
		"enabled": true, "baseUrl": "https://example.com/v1",
		"apiKey": "sk-secret-123", "model": "test-model",
		"timeoutSec": 12, "codeSpaceEnabled": true,
	}
	w := doJSON(r, http.MethodPost, "/api/admin/ai/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", w.Code, w.Body.String())
	}
	ai.InvalidateCache()
	w2 := doJSON(r, http.MethodGet, "/api/admin/ai/config", nil)
	var res struct {
		Data struct {
			Enabled          bool   `json:"enabled"`
			APIKey           string `json:"apiKey"`
			APIKeySet        bool   `json:"apiKeySet"`
			CodeSpaceEnabled bool   `json:"codeSpaceEnabled"`
			Model            string `json:"model"`
		} `json:"data"`
	}
	mustUnmarshal(t, w2.Body.Bytes(), &res)
	if !res.Data.Enabled || !res.Data.CodeSpaceEnabled || res.Data.Model != "test-model" {
		t.Errorf("配置未生效: %+v", res.Data)
	}
	if !res.Data.APIKeySet || res.Data.APIKey != ai.MaskPlaceholder {
		t.Errorf("API Key 必须脱敏回显，实际 %+v", res.Data)
	}
	// 落库的是真 key，但对外永远不回显。
	var row struct{ Value string }
	_ = db.Get().Table("settings").Where("`key` = ?", ai.KeyAPIKey).Scan(&row).Error
	if row.Value != "sk-secret-123" {
		t.Errorf("落库 key 错误: %q", row.Value)
	}
}

func TestCodeSpaceDisabledByDefault(t *testing.T) {
	r, _ := aiRouter(t)
	// 未开启时接口必须 404，且不会去读文件内容。
	w := doJSON(r, http.MethodGet, "/api/files/1/content", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("代码空间默认关闭时应 404，实际 %d body=%s", w.Code, w.Body.String())
	}
	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/files/1/content",
		strings.NewReader(`{"content":"x"}`))
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusNotFound {
		t.Errorf("写入也应被门禁拦下，实际 %d", w2.Code)
	}
}
