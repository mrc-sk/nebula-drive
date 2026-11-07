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

// 这组测试守住插件配置的**静态加密**。
//
// 为什么必须单独测：Plugin.Config 的加密靠 models.Encrypted 的
// driver.Valuer，而 database/sql **只对带类型的值**调用它。
// 项目里写配置走的是 `Update("config", v)` 这类"值来自 map"的路径，
// 传裸 string 时 Valuer 完全不参与 —— 明文直接落库，
// 接口返回 200、GORM 不报错、日志无异常。
//
// 这不是假想：Policy.Config 已经在完全相同的写法上踩过这个坑
// （编辑一次存储策略，SFTP 密码就变成明文；见 FIXLOG 第 17 节）。
// 插件配置里装的是 SMTP 密码、HMAC 共享密钥，风险性质完全相同。
//
// 断言直接读**数据库原始列**而不是用 GORM 读回：后者会经 Scanner
// 把明文"正确"还原，测试变成假阳性。

// seedPlugin 插入一条插件记录（用原生 SQL，避免测试本身依赖被测的加密路径）。
func seedPlugin(t *testing.T, name string) {
	t.Helper()
	if err := db.Get().Exec(
		"INSERT INTO plugins (name, title, enabled, config, version, hooks, installed_at, created_at) "+
			"VALUES (?, 'Test Plugin', 0, '', '1.0.0', 'onEmail', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		name,
	).Error; err != nil {
		t.Fatalf("seed plugin: %v", err)
	}
}

func rawPluginConfig(t *testing.T, name string) string {
	t.Helper()
	var raw string
	if err := db.Get().Raw("SELECT config FROM plugins WHERE name = ?", name).Row().Scan(&raw); err != nil {
		t.Fatalf("raw read plugins.config: %v", err)
	}
	return raw
}

// TestUpdatePluginConfigStaysEncrypted 核心用例：
// 通过管理端接口改插件配置后，库里必须是密文。
func TestUpdatePluginConfigStaysEncrypted(t *testing.T) {
	testutil.SetupDB(t)
	seedPlugin(t, "smtp-email")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// UpdatePluginConfig 会写审计日志，需要当前用户
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.PUT("/plugins/:name/config", UpdatePluginConfig)

	// 注意 body 里的 config 是**字符串形态的 JSON**（与前端一致）
	body := []byte(`{"config":"{\"host\":\"smtp.example.com\",\"password\":\"PLUGIN_SMTP_SECRET\"}"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/plugins/smtp-email/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	raw := rawPluginConfig(t, "smtp-email")
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("plugins.config 必须加密落库，实际 = %q（明文泄露）", raw)
	}
	if strings.Contains(raw, "PLUGIN_SMTP_SECRET") {
		t.Fatalf("密文中不应出现明文密钥，实际 = %q", raw)
	}

	// 业务层（宿主下发配置时）必须仍能读出明文 JSON，
	// 否则插件会拿到一串密文、解析失败后静默用默认值。
	var p models.Plugin
	if err := db.Get().Where("name = ?", "smtp-email").First(&p).Error; err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	if !strings.Contains(p.Config.String(), "PLUGIN_SMTP_SECRET") {
		t.Errorf("读回应还原为明文 JSON，实际 = %q", p.Config.String())
	}
}

// TestPluginConfigEmptyStaysEmpty 清空配置时不该写入一个"空值的密文"。
//
// Encrypted.Value() 对空串直接返回空串（幂等），所以库里应仍是空。
// 若这里变成了非空密文，说明有人给空配置也走了加密路径 ——
// 后果是"管理员清空配置"之后，宿主下发的是一个空字符串的密文，
// 插件解析失败却看不出原因。
func TestPluginConfigEmptyStaysEmpty(t *testing.T) {
	testutil.SetupDB(t)
	seedPlugin(t, "hmac-auth")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.PUT("/plugins/:name/config", UpdatePluginConfig)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/plugins/hmac-auth/config",
		bytes.NewReader([]byte(`{"config":""}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	if raw := rawPluginConfig(t, "hmac-auth"); raw != "" {
		t.Fatalf("空配置落库应为空串，实际 = %q", raw)
	}
}

// TestUpdatePluginConfigRejectsBadJSON 非法 JSON 不该入库
// （否则插件拿到坏配置后只能报错或静默降级）。
func TestUpdatePluginConfigRejectsBadJSON(t *testing.T) {
	testutil.SetupDB(t)
	seedPlugin(t, "smtp-email")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.CtxUserKey, &models.User{ID: 1, UserName: "admin", IsAdmin: true})
		c.Next()
	})
	r.PUT("/plugins/:name/config", UpdatePluginConfig)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/plugins/smtp-email/config",
		bytes.NewReader([]byte(`{"config":"not json"}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，实际 %d", w.Code)
	}
	if raw := rawPluginConfig(t, "smtp-email"); raw != "" {
		t.Errorf("非法配置不该入库，实际 = %q", raw)
	}
}
