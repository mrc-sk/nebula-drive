// Package testutil 提供跨包测试用的辅助函数（仅被 _test.go 引用）
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/gorm"
)

// SetupDB 初始化一个基于临时文件的 SQLite 测试库并迁移表结构，赋值给全局 db.DB
//
// 驱动选择说明：这里必须用 github.com/glebarez/sqlite（纯 Go）而**不是**
// gorm.io/driver/sqlite（依赖 mattn/go-sqlite3，需要 CGO）。原因有两条：
//
//  1. 与实际生产一致 —— pkg/db/db.go 里 sqlite 分支用的就是 glebarez/sqlite。
//     测试驱动与线上驱动不一致时，"测试通过"不代表"线上通过"，反之亦然。
//     本项目曾因此让一个 MySQL 才暴露的 SQL 错误（DELETE 子查询引用同表，ER_1093）
//     长期潜伏 —— SQLite 对此宽容，本地测试全绿，一上 MySQL 就炸。
//
//  2. 无需 CGO —— 原本依赖 gcc，导致 Windows 默认环境（CGO_ENABLED=0）和
//     精简 CI 容器里整个测试套件都跑不起来。
func SetupDB(t *testing.T) *gorm.DB {
	t.Helper()

	// 加载 conf 以启用敏感字段加密。
	//
	// 必要性：models.Encrypted 走 driver.Valuer 加密，而密钥来自 conf 的
	// data/secret.key。未加载 conf 时 EncryptString 会返回 "secret key not loaded"，
	// Valuer 于是把**整个写操作置为失败**（这是有意的 fail closed 设计），
	// 结果是所有创建 User / Policy 的测试全部报错。
	//
	// 每个测试用独立的 t.TempDir()，因此各自持有独立密钥，互不干扰。
	conf.SetDataDir(t.TempDir())
	if err := conf.Load(); err != nil {
		t.Fatalf("conf.Load: %v", err)
	}

	g, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.DB = g
	if err := models.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 注册连接清理。
	//
	// 必要性（Windows 特有）：t.TempDir() 在测试结束时删除临时目录，但 SQLite 的
	// 文件句柄如果还开着，Windows 会拒绝删除并报
	//   TempDir RemoveAll cleanup: unlinkat ...: The process cannot access the file
	// 使一个断言全过的测试被判为 FAIL。
	//
	// t.Cleanup 是 LIFO 执行：TempDir 的清理在 SetupDB 内被更早注册，因此这里注册的
	// Close 会**先**执行，从而保证「先关连接、再删目录」的正确顺序。
	if sqlDB, err := g.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
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
