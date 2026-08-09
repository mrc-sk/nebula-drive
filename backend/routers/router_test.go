package routers_test

import (
	"testing"

	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
	"github.com/nebula-drive/nebula/routers"
)

func TestSetupReturnsEngine(t *testing.T) {
	r := routers.Setup()
	if r == nil {
		t.Fatal("nil engine")
	}
}

func TestInitWebDAVRoutesNilDB(t *testing.T) {
	db.DB = nil
	r := routers.Setup()
	// db 未初始化时应直接返回，不 panic
	routers.InitWebDAVRoutes(r)
}

func TestInitWebDAVRoutesWithSetting(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("webdav.path", "/mydav")
	r := routers.Setup()
	routers.InitWebDAVRoutes(r)
}
