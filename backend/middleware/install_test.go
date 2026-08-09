package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
)

func TestInstallGuardNotInstalled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf.SetDataDir("/tmp/nebula-install-test-notexist")
	r := gin.New()
	r.Use(InstallGuard())
	r.GET("/api/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/api/install", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/api/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/assets/x.js", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// 未安装：普通 API → 503
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("api status = %d, want 503", w.Code)
	}
	// 安装接口放行
	for _, p := range []string{"/api/install", "/api/health", "/assets/x.js", "/"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("path %s status = %d, want 200", p, w.Code)
		}
	}
}

func TestInstalledBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟已安装：直接构造已安装场景不易（依赖 conf 状态），这里仅测试未安装时放行
	r.Use(InstalledBlock())
	r.GET("/api/install", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/install", nil)
	r.ServeHTTP(w, req)
	// 未安装时应放行
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
