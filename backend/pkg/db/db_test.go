package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/pkg/db"
)

func TestInitNilConfig(t *testing.T) {
	if err := db.Init(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestInitUnsupportedType(t *testing.T) {
	if err := db.Init(&conf.DBConfig{Type: "oracle"}); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestInitSQLite(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "test.db")
	if err := db.Init(&conf.DBConfig{Type: "sqlite", File: dsn}); err != nil {
		t.Fatalf("init sqlite: %v", err)
	}
	if db.Get() == nil {
		t.Fatal("db nil after init")
	}
	sqlDB, err := db.Get().DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if sqlDB == nil {
		t.Fatal("nil sql db")
	}
	// 关闭连接，否则 Windows 上 TempDir 清理会因文件句柄占用而失败
	// （t.Cleanup 为 LIFO：TempDir 的清理在前，故本 Close 会先执行）
	t.Cleanup(func() { _ = sqlDB.Close() })
	// 连接池参数应已设置
	stat := sqlDB.Stats()
	if stat.MaxOpenConnections <= 0 {
		t.Fatalf("max open conns not set: %d", stat.MaxOpenConnections)
	}
}

// TestInitSQLiteRelativePathResolvesToDataDir 验证相对路径基于数据目录解析。
//
// 背景：容器里 WORKDIR 是 /data，而 SQLite 按**进程工作目录**解释相对路径，
// 于是 "data/nebula.db" 会变成 /data/data/nebula.db —— 目录不存在，
// 报 "unable to open database file: out of memory (14)"（14 是 CANTOPEN，
// "out of memory" 只是 SQLite 的误导性默认文案）。
//
// 修复后相对路径锚定到 conf.DataDir()，与工作目录无关，且自动建父目录。
func TestInitSQLiteRelativePathResolvesToDataDir(t *testing.T) {
	dataDir := t.TempDir()
	conf.SetDataDir(dataDir)
	t.Cleanup(func() { conf.SetDataDir("data") })

	if err := db.Init(&conf.DBConfig{Type: "sqlite", File: "nebula.db"}); err != nil {
		t.Fatalf("init sqlite with relative path: %v", err)
	}
	sqlDB, err := db.Get().DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	// 文件应落在数据目录下
	want := filepath.Join(dataDir, "nebula.db")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("数据库文件应创建在数据目录 %s: %v", want, err)
	}
	// 且不应出现在 dataDir/data/ 这种多套一层的路径下
	if _, err := os.Stat(filepath.Join(dataDir, "data", "nebula.db")); err == nil {
		t.Error("不应在 dataDir 下再套一层 data/ 目录")
	}
}

// TestInitSQLiteAbsolutePathUnchanged 绝对路径必须原样使用，不被改写。
func TestInitSQLiteAbsolutePathUnchanged(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "abs.db")
	conf.SetDataDir(t.TempDir()) // 与目标路径无关
	t.Cleanup(func() { conf.SetDataDir("data") })

	if err := db.Init(&conf.DBConfig{Type: "sqlite", File: abs}); err != nil {
		t.Fatalf("init sqlite with absolute path: %v", err)
	}
	sqlDB, err := db.Get().DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("数据库文件应创建在绝对路径 %s: %v", abs, err)
	}
}
