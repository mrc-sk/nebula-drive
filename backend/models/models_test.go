package models_test

import (
	"testing"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestAutoMigrate(t *testing.T) {
	g := testutil.SetupDB(t)
	if g == nil || db.Get() == nil {
		t.Fatal("db not initialized")
	}
	// 表应已创建：插入并查询 IPBan
	ban := models.IPBan{IP: "1.1.1.1", Reason: "test"}
	if err := db.Get().Create(&ban).Error; err != nil {
		t.Fatalf("create ban: %v", err)
	}
	if ban.ID == 0 {
		t.Fatal("id not set")
	}
	var got models.IPBan
	if err := db.Get().First(&got, ban.ID).Error; err != nil {
		t.Fatalf("query ban: %v", err)
	}
	if got.IP != "1.1.1.1" {
		t.Fatalf("ip = %q", got.IP)
	}
}

func TestAutoMigrateNotReady(t *testing.T) {
	db.DB = nil
	if err := models.AutoMigrate(); err == nil {
		t.Fatal("expected error when db nil")
	}
}
