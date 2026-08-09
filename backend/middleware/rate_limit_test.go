package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestSlidingWindowLimiter(t *testing.T) {
	l := &slidingWindowLimiter{records: map[string][]time.Time{}}
	for i := 0; i < 3; i++ {
		if !l.allow("k", 3) {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if l.allow("k", 3) {
		t.Fatal("4th request should be blocked")
	}
	// 不同 key 互不影响
	if !l.allow("k2", 3) {
		t.Fatal("k2 should be allowed")
	}
}

func TestSettingIntNoDB(t *testing.T) {
	// db 未初始化返回默认值
	if got := settingInt("any.key", 42); got != 42 {
		t.Fatalf("expected default 42, got %d", got)
	}
}

func TestLimitForCategoryDefaults(t *testing.T) {
	// db 未初始化走默认值
	if got := limitForCategory("login"); got != 10 {
		t.Fatalf("login default = %d, want 10", got)
	}
	if got := limitForCategory("upload"); got != 60 {
		t.Fatalf("upload default = %d, want 60", got)
	}
	if got := limitForCategory("unknown"); got != 600 {
		t.Fatalf("default = %d, want 600", got)
	}
}

func TestRateLimitAllowsByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit("default"))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRateLimitReturns429(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("rate_limit.default_per_min", "2")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit("default"))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	ip := "10.0.0.9:1234"
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("req %d status = %d, want 200", i, w.Code)
		}
	}
	// 第 3 次应 429
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}
