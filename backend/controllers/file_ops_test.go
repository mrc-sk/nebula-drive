package controllers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// setupFileTest 初始化 DB、本地存储策略、用户
func setupFileTest(t *testing.T) *models.User {
	t.Helper()
	testutil.SetupDB(t)
	upDir := t.TempDir()
	db.Get().Create(&models.Policy{ID: 1, Name: "local", Type: "local", Config: models.From(models.LocalPolicyConfig(upDir)), IsDefault: true})
	u := &models.User{UserName: "fuser", GroupID: 1}
	db.Get().Create(u)
	return u
}

func withUser(r *gin.Engine, u *models.User) {
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
}

func TestMkdir(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/mkdir", Mkdir)
	body, _ := json.Marshal(map[string]string{"name": "docs"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mkdir", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var f models.File
	db.Get().Where("name = ?", "docs").First(&f)
	if f.ID == 0 || !f.IsDir {
		t.Fatalf("dir not created: %+v", f)
	}
}

func TestMkdirBadBody(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/mkdir", Mkdir)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mkdir", bytes.NewReader([]byte("{}")))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestList(t *testing.T) {
	u := setupFileTest(t)
	db.Get().Create(&models.File{OwnerID: u.ID, Name: "a.txt", Extension: "txt"})
	db.Get().Create(&models.File{OwnerID: u.ID, Name: "b.txt", Extension: "txt"})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.GET("/list", List)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/list", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRenameAndMove(t *testing.T) {
	u := setupFileTest(t)
	f := models.File{OwnerID: u.ID, Name: "old.txt", Extension: "txt"}
	db.Get().Create(&f)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.PUT("/f/:id/rename", Rename)
	r.PUT("/f/:id/move", Move)

	// rename
	body, _ := json.Marshal(map[string]string{"name": "new.txt"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/f/"+strconv.Itoa(int(f.ID))+"/rename", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rename status = %d", w.Code)
	}

	// move
	body2, _ := json.Marshal(map[string]any{})
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPut, "/f/"+strconv.Itoa(int(f.ID))+"/move", bytes.NewReader(body2))
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("move status = %d", w2.Code)
	}
}

func TestRenameNotFound(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.PUT("/f/:id/rename", Rename)
	body, _ := json.Marshal(map[string]string{"name": "x"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/f/9999/rename", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	u := setupFileTest(t)
	f := models.File{OwnerID: u.ID, Name: "del.txt", Extension: "txt"}
	db.Get().Create(&f)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.DELETE("/f/:id", Delete)
	r.POST("/f/:id/restore", Restore)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/f/"+strconv.Itoa(int(f.ID)), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d", w.Code)
	}
	// 软删除后 restore
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/f/"+strconv.Itoa(int(f.ID))+"/restore", nil)
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("restore status = %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestDeleteNotFound(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.DELETE("/f/:id", Delete)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/f/9999", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestPurge(t *testing.T) {
	u := setupFileTest(t)
	f := models.File{OwnerID: u.ID, Name: "purge.txt", Extension: "txt", PolicyID: 1, SourceName: "purge-src.txt"}
	db.Get().Create(&f)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/f/:id/purge", Purge)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/f/"+strconv.Itoa(int(f.ID))+"/purge", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("purge status = %d", w.Code)
	}
}

func TestBreadcrumb(t *testing.T) {
	u := setupFileTest(t)
	parent := models.File{OwnerID: u.ID, Name: "p", IsDir: true}
	db.Get().Create(&parent)
	child := models.File{OwnerID: u.ID, Name: "c", ParentID: &parent.ID}
	db.Get().Create(&child)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.GET("/bc/:id", Breadcrumb)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bc/"+strconv.Itoa(int(child.ID)), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("breadcrumb status = %d", w.Code)
	}
}

func TestUploadText(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/upload", Upload)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatalf("create form: %v", err)
	}
	part.Write([]byte("hello world"))
	writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", w.Code, w.Body.String())
	}
	var f models.File
	db.Get().Where("name = ?", "hello.txt").First(&f)
	if f.ID == 0 {
		t.Fatal("file not created")
	}
}

func TestUploadBadExtension(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/upload", Upload)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "evil.exe")
	part.Write([]byte("x"))
	writer.Close()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestUploadNoFile(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/upload", Upload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownload(t *testing.T) {
	u := setupFileTest(t)
	h, _, err := handlerForPolicyID(1)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	srcName := "dl-src.txt"
	content := []byte("download me!")
	if err := h.Put(bytes.NewReader(content), srcName, int64(len(content))); err != nil {
		t.Fatalf("put: %v", err)
	}
	f := models.File{OwnerID: u.ID, Name: "dl.txt", Extension: "txt", PolicyID: 1, SourceName: srcName, Size: int64(len(content)), MimeType: "text/plain"}
	db.Get().Create(&f)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.GET("/f/:id/download", Download)

	// 全量下载
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/f/"+strconv.Itoa(int(f.ID))+"/download", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("download status = %d", w.Code)
	}
	if w.Body.Len() != len(content) {
		t.Fatalf("body len = %d, want %d", w.Body.Len(), len(content))
	}

	// Range 下载
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/f/"+strconv.Itoa(int(f.ID))+"/download", nil)
	req2.Header.Set("Range", "bytes=0-4")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d", w2.Code)
	}
}

func TestRapidMiss(t *testing.T) {
	u := setupFileTest(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withUser(r, u)
	r.POST("/rapid", Rapid)
	body, _ := json.Marshal(map[string]any{"hash": "noexist", "name": "a.txt"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rapid", bytes.NewReader(body))
	r.ServeHTTP(w, req)
	// 不可秒传 → 200 + code:1
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}
