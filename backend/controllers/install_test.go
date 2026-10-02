package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/pkg/db"
)

func TestInstallStatus(t *testing.T) {
	conf.SetDataDir(t.TempDir())
	_ = conf.Load()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s", InstallStatus)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestTestDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/tdb", TestDB)
	dbFile := filepath.Join(t.TempDir(), "x.db")
	// 注意顺序：t.Cleanup 为 LIFO，必须在 t.TempDir() **之后**注册，
	// 这样「关闭连接」才会先于「删除临时目录」执行。
	t.Cleanup(func() { _ = db.Close() })
	body, _ := json.Marshal(map[string]any{"type": "sqlite", "file": dbFile})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tdb", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestTestDBBadType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/tdb", TestDB)
	body, _ := json.Marshal(map[string]any{"type": "oracle"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/tdb", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	// TestDB 返回 200 + code:1（连接失败）
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestInstallFlow(t *testing.T) {
	conf.SetDataDir(t.TempDir())
	_ = conf.Load()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/install", Install)
	dbFile := filepath.Join(t.TempDir(), "installed.db")
	// 注意顺序：t.Cleanup 为 LIFO，需在 t.TempDir() 之后注册，
	// 保证「关闭连接」先于「删除临时目录」执行。
	t.Cleanup(func() { _ = db.Close() })
	body, _ := json.Marshal(map[string]any{
		"db":     map[string]any{"type": "sqlite", "file": dbFile},
		"system": map[string]any{"siteName": "N", "listen": ":1"},
		"admin":  map[string]any{"userName": "admin", "password": "Abcdefg1", "email": "a@b.com"},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/install", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("install status = %d body=%s", w.Code, w.Body.String())
	}
	if !conf.IsInstalled() {
		t.Fatal("should be installed")
	}
	// 管理员已创建
	var u struct{ ID uint }
	db.Get().Table("users").Where("user_name = ?", "admin").First(&u)
	if u.ID == 0 {
		t.Fatal("admin not created")
	}
}

func TestInstallBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/install", Install)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/install", bytes.NewReader([]byte("{}")))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}
