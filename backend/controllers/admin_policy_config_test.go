package controllers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// TestUpdatePolicyConfigStaysEncrypted 锁死一条曾经真实存在的数据泄露路径。
//
// 背景：UpdatePolicy 用 Updates(map[string]any{...}) 写 policies.config。
// 若 map 里放裸 string，models.Encrypted 的 driver.Valuer 不会被调用，
// SFTP 密码 / OSS / COS 密钥会以明文落库 —— 而接口照样返回 200，
// 没有任何报错。只有直接读数据库原始列才看得到。
//
// 这里刻意绕过 GORM 读回、直接查原始列：用 GORM 读回时 Scanner 会把
// 明文"正确"还原成明文，断言会变成假阳性。这与
// models/encrypted_test.go 里 rawColumn 存在的理由是同一个。
func TestUpdatePolicyConfigStaysEncrypted(t *testing.T) {
	testutil.SetupDB(t)
	gin.SetMode(gin.TestMode)

	p := models.Policy{ID: 1, Name: "本地", Type: "sftp",
		Config: models.From(`{"password":"initial"}`)}
	if err := db.Get().Create(&p).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	r := gin.New()
	// UpdatePolicy 会读 CurrentUser 写审计日志，不注入就会空指针 panic。
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.PUT("/policies/:id", UpdatePolicy)

	body := []byte(`{"name":"本地","type":"sftp","config":"{\"password\":\"edited-by-admin\"}"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/policies/1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var raw string
	if err := db.Get().Raw("SELECT config FROM policies WHERE id = ?", 1).Row().Scan(&raw); err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("policies.config 必须加密落库，实际 = %q（明文泄露）", raw)
	}
	if strings.Contains(raw, "edited-by-admin") {
		t.Fatalf("密文中不应出现明文密钥，实际 = %q", raw)
	}
}
