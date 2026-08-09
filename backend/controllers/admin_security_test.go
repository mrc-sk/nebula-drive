package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestIPBanManagement(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/bans", ListIPBans)
	r.POST("/bans", AddIPBan)
	r.DELETE("/bans/:id", DeleteIPBan)

	// 添加
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"ip": "3.3.3.3", "reason": "manual"})
	req := httptest.NewRequest(http.MethodPost, "/bans", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add status = %d", w.Code)
	}
	var addResp struct {
		Data models.IPBan `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &addResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id := addResp.Data.ID
	if id == 0 {
		t.Fatal("id not set")
	}

	// 列表
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/bans", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list status = %d", w2.Code)
	}

	// 删除
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodDelete, "/bans/"+strconv.Itoa(int(id)), nil)
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w3.Code)
	}
	var bans []models.IPBan
	db.Get().Find(&bans)
	if len(bans) != 0 {
		t.Fatalf("ban not deleted, count=%d", len(bans))
	}
}

func TestAddIPBanBadBody(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/bans", AddIPBan)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bans", bytes.NewReader([]byte("{}")))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUploadCertMissingFiles(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/cert", UploadCert)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/cert", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}
