package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestIsIPBannedNoDB(t *testing.T) {
	db.DB = nil
	if IsIPBanned("1.2.3.4") {
		t.Fatal("should not be banned when db nil")
	}
	if IsIPBanned("") {
		t.Fatal("empty ip should not be banned")
	}
}

func TestIPGuardBlocksBanned(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	db.Get().Create(&models.IPBan{IP: "9.9.9.9", Reason: "test"})

	r := gin.New()
	r.Use(IPGuard())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 命中封禁 → 403
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("banned ip status = %d, want 403", w.Code)
	}

	// 未封禁 → 200
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "8.8.8.8:1234"
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("normal ip status = %d, want 200", w2.Code)
	}
}

func TestIsIPBannedExpired(t *testing.T) {
	testutil.SetupDB(t)
	exp := time.Now().Add(-time.Hour)
	db.Get().Create(&models.IPBan{IP: "7.7.7.7", ExpiresAt: &exp})
	if IsIPBanned("7.7.7.7") {
		t.Fatal("expired ban should not block")
	}
}

func TestRecordLoginFailureAndAutoBan(t *testing.T) {
	testutil.SetupDB(t)
	ip := "6.6.6.6"
	ClearLoginFailure(ip)
	// 连续失败达阈值（10）后自动封禁
	for i := 0; i < 10; i++ {
		RecordLoginFailure(ip)
	}
	if !IsIPBanned(ip) {
		t.Fatal("ip should be auto-banned after 10 failures")
	}
	// 已存在封禁则不覆盖
	RecordLoginFailure(ip)
}

func TestRequireCaptcha(t *testing.T) {
	ip := "5.5.5.5"
	ClearLoginFailure(ip)
	if RequireCaptcha(ip) {
		t.Fatal("should not require captcha initially")
	}
	for i := 0; i < 3; i++ {
		RecordLoginFailure(ip)
	}
	if !RequireCaptcha(ip) {
		t.Fatal("should require captcha after 3 failures")
	}
	ClearLoginFailure(ip)
	if RequireCaptcha(ip) {
		t.Fatal("should not require captcha after clear")
	}
}

func TestRequireCaptchaEmptyIP(t *testing.T) {
	if RequireCaptcha("") {
		t.Fatal("empty ip should not require captcha")
	}
}

func TestRecordLoginFailureEmptyIP(t *testing.T) {
	RecordLoginFailure("") // 不 panic
}
