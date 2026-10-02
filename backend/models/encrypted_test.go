package models_test

import (
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// rawColumn 绕过 GORM 的 sql.Scanner，直接读数据库里的原始字符串。
//
// 这个辅助函数是本文件存在的原因：如果只用 GORM 读回再断言，
// 即使 Valuer 根本没生效（库里就是明文），Scanner 也会"正确"地
// 返回原文，测试照样全绿 —— 变成假阳性。必须直接看库里的字节。
func rawColumn(t *testing.T, table, column string, id uint) string {
	t.Helper()
	var raw string
	row := db.Get().Raw("SELECT "+column+" FROM "+table+" WHERE id = ?", id).Row()
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("raw read %s.%s: %v", table, column, err)
	}
	return raw
}

const rawPrefix = "enc:v1:"

// TestEncryptedRoundTrip 核心用例：写入后库里必须是密文，业务层读回必须是明文。
func TestEncryptedRoundTrip(t *testing.T) {
	testutil.SetupDB(t)

	const secret = "JBSWY3DPEHPK3PXP"
	u := models.User{UserName: "alice", Password: "x", TwoFactor: models.From(secret)}
	if err := db.Get().Create(&u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	raw := rawColumn(t, "users", "two_factor", u.ID)
	if !strings.HasPrefix(raw, rawPrefix) {
		t.Fatalf("落库应为密文（%s 前缀），实际 = %q", rawPrefix, raw)
	}
	if strings.Contains(raw, secret) {
		t.Fatal("密文中不应出现明文 TOTP secret")
	}

	var got models.User
	if err := db.Get().First(&got, u.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.TwoFactor.String() != secret {
		t.Fatalf("业务层读回应为明文，实际 = %q", got.TwoFactor.String())
	}
}

// TestEncryptedPolicyConfig 存储策略配置含 SFTP 密码 / 云厂商密钥，同样必须加密。
func TestEncryptedPolicyConfig(t *testing.T) {
	testutil.SetupDB(t)

	const cfg = `{"path":"uploads","password":"sftp-super-secret"}`
	p := models.Policy{ID: 1, Name: "本地", Type: "sftp", Config: models.From(cfg)}
	if err := db.Get().Create(&p).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}

	raw := rawColumn(t, "policies", "config", p.ID)
	if !strings.HasPrefix(raw, rawPrefix) {
		t.Fatalf("Policy.Config 落库应为密文，实际 = %q", raw)
	}
	if strings.Contains(raw, "sftp-super-secret") {
		t.Fatal("密文中不应出现明文 SFTP 密码")
	}

	var got models.Policy
	if err := db.Get().First(&got, p.ID).Error; err != nil {
		t.Fatalf("read policy: %v", err)
	}
	if got.Config.String() != cfg {
		t.Fatalf("读回应还原为原始 JSON，实际 = %q", got.Config.String())
	}
}

// TestEncryptedLegacyPlaintext 存量数据（升级前写入的明文）必须仍可正常读出。
// 这是"平滑升级"的前提：不能因为加了加密就让老用户的 2FA 全部失效。
func TestEncryptedLegacyPlaintext(t *testing.T) {
	testutil.SetupDB(t)

	// 用原生 SQL 写入，模拟升级前的明文数据
	if err := db.Get().Exec(
		"INSERT INTO users (user_name, password, two_factor) VALUES (?, ?, ?)",
		"legacy", "x", "LEGACYSECRET").Error; err != nil {
		t.Fatalf("insert legacy: %v", err)
	}

	var u models.User
	if err := db.Get().Where("user_name = ?", "legacy").First(&u).Error; err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if u.TwoFactor.String() != "LEGACYSECRET" {
		t.Fatalf("存量明文应原样读出，实际 = %q", u.TwoFactor.String())
	}
}

// TestEncryptedUpdateMapPath 验证 Update("col", val) 这类"值来自 map"的写入路径。
//
// 这是选用 driver.Valuer 而非 GORM hook 的原因：hook 只对 struct 字段生效，
// 下面这条语句如果传裸 string，hook 完全拦不住，会静默写入明文。
// 项目里 2FA 的开启/关闭恰好就是这个写法。
func TestEncryptedUpdateMapPath(t *testing.T) {
	testutil.SetupDB(t)

	u := models.User{UserName: "bob", Password: "x"}
	if err := db.Get().Create(&u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := db.Get().Model(&u).Update("two_factor", models.From("NEWSECRET")).Error; err != nil {
		t.Fatalf("update: %v", err)
	}

	raw := rawColumn(t, "users", "two_factor", u.ID)
	if !strings.HasPrefix(raw, rawPrefix) {
		t.Fatalf("Update 路径同样必须加密，实际 = %q", raw)
	}
}

// TestEncryptedIdempotent 重复加密不应叠加（Encrypted 类型可能被复用同一变量多次保存）。
func TestEncryptedIdempotent(t *testing.T) {
	testutil.SetupDB(t)

	const secret = "IDEMPOTENT"
	e := models.From(secret)
	// 模拟同一字段被反复赋值后再保存
	e = models.From(e.String())

	u := models.User{UserName: "carol", Password: "x", TwoFactor: e}
	if err := db.Get().Create(&u).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got models.User
	if err := db.Get().First(&got, u.ID).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.TwoFactor.String() != secret {
		t.Fatalf("期望 %q，实际 %q", secret, got.TwoFactor.String())
	}
}
