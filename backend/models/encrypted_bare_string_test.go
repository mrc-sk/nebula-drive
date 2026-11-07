package models_test

import (
	"strings"
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

// TestEncryptedBareStringIsNotIntercepted 固化一条**库层面**的约束，而不是业务行为：
//
//	只有实现了 driver.Valuer 的**值**才会被加密。
//
// models.Encrypted 的 Value() 靠 database/sql 在绑定参数时调用。若调用方
// 把裸 string 塞进 map 传给 Update/Updates，Valuer 根本不参与，明文直接落库。
// 这类失败**没有任何报错**：接口返回 200，GORM 也不抱怨，只有直接看库里的
// 字节才发现。所以这里刻意把"裸 string 不加密"断言下来 ——
// 它的作用是阻止后来者把 models.From(...) 当作冗余包装删掉。
//
// 对应的真实事故与修复见 controllers/admin_ex.go 的 UpdatePolicy。
func TestEncryptedBareStringIsNotIntercepted(t *testing.T) {
	testutil.SetupDB(t)

	p := models.Policy{ID: 1, Name: "本地", Type: "sftp",
		Config: models.From(`{"password":"initial"}`)}
	if err := db.Get().Create(&p).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	if raw := rawColumn(t, "policies", "config", p.ID); !strings.HasPrefix(raw, rawPrefix) {
		t.Fatalf("Create 路径应加密，实际 = %q", raw)
	}

	// 裸 string —— Valuer 不参与，明文落库。这是被测的既定事实。
	const plain = `{"password":"bare-string-leak"}`
	if err := db.Get().Model(&models.Policy{}).Where("id = ?", p.ID).
		Updates(map[string]any{"config": plain}).Error; err != nil {
		t.Fatalf("update bare: %v", err)
	}
	raw := rawColumn(t, "policies", "config", p.ID)
	if strings.HasPrefix(raw, rawPrefix) {
		t.Fatal("前提变了：裸 string 现在也会被加密 —— " +
			"说明 GORM/database-sql 行为已变，本文件与 admin_ex.go 的注释需要重写")
	}
	if !strings.Contains(raw, "bare-string-leak") {
		t.Fatalf("期望库里是明文，实际 = %q", raw)
	}

	// 传 Encrypted 类型 —— 这是唯一正确的写法。
	if err := db.Get().Model(&models.Policy{}).Where("id = ?", p.ID).
		Updates(map[string]any{"config": models.From(`{"password":"typed"}`)}).Error; err != nil {
		t.Fatalf("update typed: %v", err)
	}
	if raw := rawColumn(t, "policies", "config", p.ID); !strings.HasPrefix(raw, rawPrefix) {
		t.Fatalf("传 Encrypted 类型必须加密，实际 = %q", raw)
	}
}
