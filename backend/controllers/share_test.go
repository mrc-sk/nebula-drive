package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func setupShareTest(t *testing.T) (*models.User, *models.File) {
	t.Helper()
	testutil.SetupDB(t)
	u := &models.User{UserName: "sharer"}
	db.Get().Create(u)
	f := &models.File{OwnerID: u.ID, Name: "share.txt", Extension: "txt"}
	db.Get().Create(f)
	return u, f
}

func TestCreateShare(t *testing.T) {
	u, f := setupShareTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.POST("/s", CreateShare)
	body, _ := json.Marshal(map[string]any{"fileId": f.ID})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/s", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateShareNotFound(t *testing.T) {
	u, _ := setupShareTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.POST("/s", CreateShare)
	body, _ := json.Marshal(map[string]any{"fileId": 9999})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/s", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCreateShareBadBody(t *testing.T) {
	u, _ := setupShareTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.POST("/s", CreateShare)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/s", bytes.NewReader([]byte("{}")))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetShareWithPassword(t *testing.T) {
	u, f := setupShareTest(t)
	s := models.Share{FileID: f.ID, OwnerID: u.ID, Password: "pw"}
	db.Get().Create(&s)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s/:id", GetShare)
	// 无密码 → 需要密码
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s/"+strconv.Itoa(int(s.ID)), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// 正确密码
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/s/"+strconv.Itoa(int(s.ID))+"?password=pw", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d", w2.Code)
	}
}

func TestGetShareWithExtractCode(t *testing.T) {
	u, f := setupShareTest(t)
	s := models.Share{FileID: f.ID, OwnerID: u.ID, ExtractCode: "ec"}
	db.Get().Create(&s)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s/:id", GetShare)
	// 正确提取码
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s/"+strconv.Itoa(int(s.ID))+"?extract=ec", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetShareExpired(t *testing.T) {
	u, f := setupShareTest(t)
	exp := time.Now().Add(-time.Hour)
	s := models.Share{FileID: f.ID, OwnerID: u.ID, ExpireAt: &exp}
	db.Get().Create(&s)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s/:id", GetShare)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s/"+strconv.Itoa(int(s.ID)), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGetShareNotFound(t *testing.T) {
	setupShareTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/s/:id", GetShare)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s/9999", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestListAndDeleteShare(t *testing.T) {
	u, f := setupShareTest(t)
	s := models.Share{FileID: f.ID, OwnerID: u.ID}
	db.Get().Create(&s)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.GET("/s", ListShares)
	r.DELETE("/s/:id", DeleteShare)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/s", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/s/"+strconv.Itoa(int(s.ID)), nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w2.Code)
	}
}
