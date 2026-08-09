// Package testutil 提供跨包测试用的辅助函数（仅被 _test.go 引用）
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SetupDB 初始化一个基于临时文件的 SQLite 测试库并迁移表结构，赋值给全局 db.DB
func SetupDB(t *testing.T) *gorm.DB {
	t.Helper()
	g, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.DB = g
	if err := models.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return g
}

// SeedSetting 写入一条设置（已存在则更新）
func SeedSetting(key, value string) {
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		db.Get().Create(&models.Setting{Key: key, Value: value})
		return
	}
	db.Get().Model(&s).Update("value", value)
}
