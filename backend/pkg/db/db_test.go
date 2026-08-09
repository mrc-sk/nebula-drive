package db_test

import (
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
	// 连接池参数应已设置
	stat := sqlDB.Stats()
	if stat.MaxOpenConnections <= 0 {
		t.Fatalf("max open conns not set: %d", stat.MaxOpenConnections)
	}
}
