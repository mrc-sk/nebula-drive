package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"github.com/nebula-drive/nebula/pkg/util"
)

func TestExtractToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Bearer header
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer abc")
	if got := extractToken(c); got != "abc" {
		t.Fatalf("got %q", got)
	}
	// Cookie
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c2.Request.AddCookie(&http.Cookie{Name: "nebula_token", Value: "xyz"})
	if got := extractToken(c2); got != "xyz" {
		t.Fatalf("got %q", got)
	}
	// None
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := extractToken(c3); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestAuthRequiredNoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Auth(true))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuthOptionalNoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Auth(false))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAuthInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Auth(true))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAuthFlowWithValidToken(t *testing.T) {
	testutil.SetupDB(t)
	if err := jwt.Init(); err != nil {
		t.Fatalf("jwt init: %v", err)
	}
	u := &models.User{UserName: "alice", IsAdmin: true}
	if err := db.Get().Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessID := util.UUID()
	tok, err := jwt.Sign(u.ID, sessID, time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	db.Get().Create(&models.Session{
		UserID: u.ID, Token: tok, SessionID: sessID,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	r := gin.New()
	r.Use(Auth(true))
	r.GET("/", func(c *gin.Context) {
		cu := CurrentUser(c)
		if cu == nil || cu.ID != u.ID {
			c.String(http.StatusInternalServerError, "no user")
			return
		}
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestAuthExpiredSession(t *testing.T) {
	testutil.SetupDB(t)
	if err := jwt.Init(); err != nil {
		t.Fatalf("jwt init: %v", err)
	}
	u := &models.User{UserName: "bob"}
	db.Get().Create(u)
	sessID := util.UUID()
	tok, _ := jwt.Sign(u.ID, sessID, time.Hour)
	// 会话已过期
	db.Get().Create(&models.Session{
		UserID: u.ID, Token: tok, SessionID: sessID,
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	r := gin.New()
	r.Use(Auth(true))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// no user → 403
	r := gin.New()
	r.Use(AdminOnly())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no user status = %d, want 403", w.Code)
	}

	// admin user → 200
	r2 := gin.New()
	r2.Use(func(c *gin.Context) { c.Set(CtxUserKey, &models.User{IsAdmin: true}) }, AdminOnly())
	r2.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", w2.Code)
	}

	// non-admin → 403
	r3 := gin.New()
	r3.Use(func(c *gin.Context) { c.Set(CtxUserKey, &models.User{IsAdmin: false}) }, AdminOnly())
	r3.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	r3.ServeHTTP(w3, req3)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d, want 403", w3.Code)
	}
}
