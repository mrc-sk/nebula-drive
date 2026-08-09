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

func TestExtractBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer tok123")
	if got := extractBearerToken(c); got != "tok123" {
		t.Fatalf("got %q", got)
	}
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := extractBearerToken(c2); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestAPIAuthNoToken(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAPIAuthOAuthToken(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "apiuser"}
	db.Get().Create(u)
	db.Get().Create(&models.AccessToken{Token: "oauth-tok", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) {
		if CurrentUser(c) == nil {
			c.String(http.StatusInternalServerError, "no user")
			return
		}
		c.String(http.StatusOK, "ok")
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer oauth-tok")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAPIAuthOAuthExpired(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "expired"}
	db.Get().Create(u)
	db.Get().Create(&models.AccessToken{Token: "exp-tok", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Hour)})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer exp-tok")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAPIAuthPAT(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "patuser"}
	db.Get().Create(u)
	db.Get().Create(&models.PersonalAccessToken{Token: "pat-tok", UserID: u.ID, Prefix: "pat-tok"})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer pat-tok")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAPIAuthPATExpired(t *testing.T) {
	testutil.SetupDB(t)
	u := &models.User{UserName: "patexp"}
	db.Get().Create(u)
	exp := time.Now().Add(-time.Hour)
	db.Get().Create(&models.PersonalAccessToken{Token: "pat-exp", UserID: u.ID, ExpiresAt: &exp})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer pat-exp")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestAPIAuthInvalidToken(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APIAuth())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer nope")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}
