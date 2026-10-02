package models_test

import (
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/internal/cryptox"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// seedLegacyPlaintext 用原生 SQL 写入"升级前"形态的敏感数据。
// 全程绕过 Encrypted 的 Valuer / Scanner，模拟历史版本留下的明文行。
func seedLegacyPlaintext(t *testing.T) {
	t.Helper()
	stmts := []string{
		"INSERT INTO users (user_name, password, two_factor) VALUES ('legacy2fa', 'x', 'LEGACY2FA')",
		"INSERT INTO o_auth_apps (client_id, client_secret, name, redirect_uris, user_id, created_at) VALUES ('nd_legacy', 'LEGACYSECRET', 'legacy', '[]', 1, CURRENT_TIMESTAMP)",
		"INSERT INTO policies (name, type, config, is_default, created_at, updated_at) VALUES ('legacy-sftp', 'sftp', '{\"password\":\"LEGACYSFTP\"}', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)",
		"INSERT INTO access_tokens (token, user_id, app_id, scope, expires_at, created_at) VALUES ('LEGACYACCESSTOKEN', 1, 0, 'profile', '2999-01-01 00:00:00', CURRENT_TIMESTAMP)",
		"INSERT INTO personal_access_tokens (user_id, name, token, prefix, scope, created_at) VALUES (1, 'legacy', 'LEGACYPATTOKEN', 'nd_pat_LE', 'profile', CURRENT_TIMESTAMP)",
	}
	for i, s := range stmts {
		if err := db.Get().Exec(s).Error; err != nil {
			t.Fatalf("seed stmt[%d]: %v", i, err)
		}
	}
}

func rawString(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := db.Get().Raw(query, args...).Row().Scan(&s); err != nil {
		t.Fatalf("raw query failed: %v", err)
	}
	return s
}

func countPlaintext(t *testing.T, table, column string) int64 {
	t.Helper()
	var n int64
	if err := db.Get().Table(table).Where(column+" NOT LIKE ?", cryptox.Prefix()+"%").
		Where(column + " <> ''").Count(&n).Error; err != nil {
		t.Fatalf("count %s.%s: %v", table, column, err)
	}
	return n
}

// TestMigrateSensitiveFields 端到端验证：造存量明文 → 跑迁移 → 确认全部转换。
func TestMigrateSensitiveFields(t *testing.T) {
	testutil.SetupDB(t)
	seedLegacyPlaintext(t)

	// 迁移前：全是明文
	if n := countPlaintext(t, "users", "two_factor"); n != 1 {
		t.Fatalf("迁移前 users.two_factor 应有 1 条明文，实际 %d", n)
	}

	models.MigrateSensitiveFields()

	// 迁移后：可逆加密字段都带上了 enc:v1: 前缀
	for _, c := range []struct{ table, column string }{
		{"users", "two_factor"},
		{"o_auth_apps", "client_secret"},
		{"policies", "config"},
	} {
		if n := countPlaintext(t, c.table, c.column); n != 0 {
			t.Errorf("%s.%s 迁移后仍有 %d 条明文", c.table, c.column, n)
		}
	}

	// 迁移后：明文内容不得出现在库里
	raw := rawString(t, "SELECT two_factor FROM users WHERE user_name = 'legacy2fa'")
	if strings.Contains(raw, "LEGACY2FA") {
		t.Error("users.two_factor 仍含明文 TOTP secret")
	}
	raw = rawString(t, "SELECT config FROM policies WHERE name = 'legacy-sftp'")
	if strings.Contains(raw, "LEGACYSFTP") {
		t.Error("policies.config 仍含明文 SFTP 密码")
	}

	// 迁移后：业务层仍能读出原文（否则老用户会直接掉线）
	var u models.User
	if err := db.Get().Where("user_name = ?", "legacy2fa").First(&u).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if u.TwoFactor.String() != "LEGACY2FA" {
		t.Errorf("迁移后 TOTP secret 应仍为 %q，实际 %q", "LEGACY2FA", u.TwoFactor.String())
	}
	var app models.OAuthApp
	if err := db.Get().Where("client_id = ?", "nd_legacy").First(&app).Error; err != nil {
		t.Fatalf("read app: %v", err)
	}
	if app.ClientSecret.String() != "LEGACYSECRET" {
		t.Errorf("迁移后 ClientSecret 应仍为 %q，实际 %q", "LEGACYSECRET", app.ClientSecret.String())
	}
}

// TestMigrateAccessTokenToHash 令牌应转为 64 位哈希，且哈希值不等于明文。
func TestMigrateAccessTokenToHash(t *testing.T) {
	testutil.SetupDB(t)
	seedLegacyPlaintext(t)

	models.MigrateSensitiveFields()

	raw := rawString(t, "SELECT token FROM access_tokens WHERE token <> ''")
	if raw == "LEGACYACCESSTOKEN" {
		t.Fatal("access token 未被哈希化")
	}
	if !cryptox.IsTokenHash(raw) {
		t.Fatalf("access token 应为 64 位哈希，实际 %q（长度 %d）", raw, len(raw))
	}
	if raw != cryptox.TokenHash("LEGACYACCESSTOKEN") {
		t.Error("哈希值与预期不符")
	}

	rawPat := rawString(t, "SELECT token FROM personal_access_tokens WHERE token <> ''")
	if !cryptox.IsTokenHash(rawPat) {
		t.Fatalf("PAT 应为 64 位哈希，实际 %q", rawPat)
	}
}

// TestMigrateIdempotent 迁移必须幂等：重复执行不报错、不重复转换。
func TestMigrateIdempotent(t *testing.T) {
	testutil.SetupDB(t)
	seedLegacyPlaintext(t)

	models.MigrateSensitiveFields()
	first := rawString(t, "SELECT two_factor FROM users WHERE user_name = 'legacy2fa'")

	models.MigrateSensitiveFields() // 第二次
	second := rawString(t, "SELECT two_factor FROM users WHERE user_name = 'legacy2fa'")

	if first != second {
		t.Errorf("重复迁移导致值变化：%q → %q", first, second)
	}
	if !cryptox.IsEncrypted(second) {
		t.Error("重复迁移后不应丢失密文前缀")
	}
}

// TestMigrateKeepsNewFormatData 已加密的数据不应被二次处理。
func TestMigrateKeepsNewFormatData(t *testing.T) {
	testutil.SetupDB(t)

	// 通过正常路径写入（已经是密文）
	u := models.User{UserName: "new", Password: "x", TwoFactor: models.From("NEWSECRET")}
	if err := db.Get().Create(&u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	before := rawString(t, "SELECT two_factor FROM users WHERE user_name = 'new'")

	models.MigrateSensitiveFields()

	after := rawString(t, "SELECT two_factor FROM users WHERE user_name = 'new'")
	if before != after {
		t.Errorf("新格式数据被迁移改动：%q → %q", before, after)
	}
}

func TestSensitiveFieldsEncryptedProbe(t *testing.T) {
	testutil.SetupDB(t)
	seedLegacyPlaintext(t)

	if ok, detail := models.SensitiveFieldsEncrypted(); ok {
		t.Fatal("迁移前探针不应报告全部加密")
	} else {
		t.Logf("迁移前探针输出（预期）: %s", detail)
	}

	models.MigrateSensitiveFields()

	ok, detail := models.SensitiveFieldsEncrypted()
	if !ok {
		t.Fatalf("迁移后探针应报告全部加密，实际: %s", detail)
	}
}
