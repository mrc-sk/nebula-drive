package controllers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"golang.org/x/crypto/bcrypt"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func doJSON(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestValidatePasswordDefaults(t *testing.T) {
	db.DB = nil // 走默认值
	if err := validatePassword("Ab1"); err == nil {
		t.Fatal("short pwd should fail")
	}
	if err := validatePassword("abcdefg1"); err == nil {
		t.Fatal("no upper should fail")
	}
	if err := validatePassword("Abcdefgh"); err == nil {
		t.Fatal("no digit should fail")
	}
	if err := validatePassword("Abcdefg1"); err != nil {
		t.Fatalf("valid pwd failed: %v", err)
	}
}

func TestValidatePasswordWithSettings(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("security.password_min_length", "12")
	testutil.SeedSetting("security.password_require_special", "true")
	if err := validatePassword("Ab1"); err == nil {
		t.Fatal("short should fail")
	}
	if err := validatePassword("Abcdefghij1"); err == nil {
		t.Fatal("no special should fail")
	}
	if err := validatePassword("Abcdefghij1!"); err != nil {
		t.Fatalf("valid failed: %v", err)
	}
}

func TestHasSpecialChar(t *testing.T) {
	if !hasSpecialChar("abc!def") {
		t.Fatal("expected true for !")
	}
	if hasSpecialChar("abcdef") {
		t.Fatal("expected false")
	}
}

func TestRegisterDisabled(t *testing.T) {
	testutil.SetupDB(t)
	// 默认未开放注册
	r := newTestRouter()
	r.POST("/register", Register)
	w := doJSON(r, http.MethodPost, "/register", map[string]string{"userName": "u", "password": "Abcdefg1"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestRegisterWeakPassword(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("security.allow_register", "true")
	r := newTestRouter()
	r.POST("/register", Register)
	w := doJSON(r, http.MethodPost, "/register", map[string]string{"userName": "newuser", "password": "weak"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestRegisterSuccessAndDuplicate(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("security.allow_register", "true")
	r := newTestRouter()
	r.POST("/register", Register)
	body := map[string]string{"userName": "newuser", "password": "Abcdefg1", "email": "u@e.com"}

	w := doJSON(r, http.MethodPost, "/register", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	// 重复注册
	w2 := doJSON(r, http.MethodPost, "/register", body)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d, want 400", w2.Code)
	}
}

func TestRegisterBadBody(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("security.allow_register", "true")
	r := newTestRouter()
	r.POST("/register", Register)
	w := doJSON(r, http.MethodPost, "/register", map[string]string{"userName": "u"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	testutil.SetupDB(t)
	if err := jwt.Init(); err != nil {
		t.Fatalf("jwt init: %v", err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("Abcdefg1"), bcrypt.MinCost)
	db.Get().Create(&models.User{UserName: "alice", Password: string(hash)})

	r := newTestRouter()
	r.POST("/login", Login)
	w := doJSON(r, http.MethodPost, "/login", map[string]string{"userName": "alice", "password": "wrong"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestLoginUnknownUser(t *testing.T) {
	testutil.SetupDB(t)
	jwt.Init()
	r := newTestRouter()
	r.POST("/login", Login)
	w := doJSON(r, http.MethodPost, "/login", map[string]string{"userName": "nobody", "password": "whatever1"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	testutil.SetupDB(t)
	if err := jwt.Init(); err != nil {
		t.Fatalf("jwt init: %v", err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("Abcdefg1"), bcrypt.MinCost)
	db.Get().Create(&models.User{UserName: "bob", Password: string(hash)})

	r := newTestRouter()
	r.POST("/login", Login)
	w := doJSON(r, http.MethodPost, "/login", map[string]string{"userName": "bob", "password": "Abcdefg1"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 || resp.Data.Token == "" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestLoginBannedUser(t *testing.T) {
	testutil.SetupDB(t)
	jwt.Init()
	hash, _ := bcrypt.GenerateFromPassword([]byte("Abcdefg1"), bcrypt.MinCost)
	db.Get().Create(&models.User{UserName: "ban", Password: string(hash), Status: 1})
	r := newTestRouter()
	r.POST("/login", Login)
	w := doJSON(r, http.MethodPost, "/login", map[string]string{"userName": "ban", "password": "Abcdefg1"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestLoginBadBody(t *testing.T) {
	testutil.SetupDB(t)
	jwt.Init()
	r := newTestRouter()
	r.POST("/login", Login)
	w := doJSON(r, http.MethodPost, "/login", map[string]string{"userName": "x"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestChangePassword(t *testing.T) {
	testutil.SetupDB(t)
	jwt.Init()
	hash, _ := bcrypt.GenerateFromPassword([]byte("OldPass1"), bcrypt.MinCost)
	u := &models.User{UserName: "carol", Password: string(hash)}
	db.Get().Create(u)

	r := newTestRouter()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.POST("/pwd", ChangePassword)

	// 错误旧密码
	w := doJSON(r, http.MethodPost, "/pwd", map[string]string{"old": "wrong", "new": "NewPass1"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong old status = %d, want 401", w.Code)
	}
	// 弱新密码
	w2 := doJSON(r, http.MethodPost, "/pwd", map[string]string{"old": "OldPass1", "new": "weak"})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("weak new status = %d, want 400", w2.Code)
	}
	// 成功
	w3 := doJSON(r, http.MethodPost, "/pwd", map[string]string{"old": "OldPass1", "new": "NewPass1"})
	if w3.Code != http.StatusOK {
		t.Fatalf("change status = %d, want 200", w3.Code)
	}
}

func TestChangePasswordBadBody(t *testing.T) {
	testutil.SetupDB(t)
	jwt.Init()
	u := &models.User{UserName: "dave", Password: "x"}
	db.Get().Create(u)
	r := newTestRouter()
	r.Use(func(c *gin.Context) { c.Set(middleware.CtxUserKey, u) })
	r.POST("/pwd", ChangePassword)
	w := doJSON(r, http.MethodPost, "/pwd", map[string]string{"old": "x"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
