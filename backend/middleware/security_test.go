package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	h := w.Header()
	checks := map[string]string{
		"Content-Security-Policy":   "default-src 'self'",
		"X-Frame-Options":           "SAMEORIGIN",
		"X-Content-Type-Options":    "nosniff",
		"X-XSS-Protection":          "1; mode=block",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Strict-Transport-Security": "max-age=31536000",
	}
	for k, v := range checks {
		got := h.Get(k)
		if got == "" {
			t.Errorf("missing header %s", k)
		} else if !strings.Contains(got, v) {
			t.Errorf("header %s = %q, want contains %q", k, got, v)
		}
	}
}

// TestSecurityHeadersFromSettings 验证后台可配置安全头：自定义值生效，"off" 关闭该头。
func TestSecurityHeadersFromSettings(t *testing.T) {
	testutil.SetupDB(t)
	defer func() {
		testutil.SeedSetting("security.frame_options", "SAMEORIGIN")
		testutil.SeedSetting("security.referrer_policy", "strict-origin-when-cross-origin")
		testutil.SeedSetting("security.hsts_max_age", "31536000")
	}()
	testutil.SeedSetting("security.frame_options", "DENY")
	testutil.SeedSetting("security.referrer_policy", "off")
	testutil.SeedSetting("security.hsts_max_age", "60")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "" {
		t.Errorf("Referrer-Policy 设为 off 应不发该头，got %q", got)
	}
	if got := w.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=60") {
		t.Errorf("HSTS = %q, want max-age=60", got)
	}
}
