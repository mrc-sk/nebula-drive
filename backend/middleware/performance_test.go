package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestStaticCacheHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(StaticCache())
	r.GET("/assets/*p", func(c *gin.Context) { c.String(http.StatusOK, "asset") })

	// 首次请求：带 ETag 与 Cache-Control
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing etag")
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=31536000") {
		t.Fatalf("cache-control = %q", cc)
	}

	// 二次请求带 If-None-Match → 304
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req2.Header.Set("If-None-Match", etag)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", w2.Code)
	}
}

func TestStaticCacheNonAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(StaticCache())
	r.GET("/api/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Header().Get("ETag") != "" {
		t.Fatal("non-asset should not get etag")
	}
}

func TestGzipMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GzipMiddleware())
	body := strings.Repeat("a", 2048)
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, body) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r.ServeHTTP(w, req)
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", w.Header().Get("Content-Encoding"))
	}
}
