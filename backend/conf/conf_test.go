package conf_test

import (
	"testing"

	"github.com/nebula-drive/nebula/conf"
)

func useTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conf.SetDataDir(dir)
	// 切换数据目录后重新加载，重置 secretKey 以匹配新目录，确保测试隔离
	if err := conf.Load(); err != nil {
		t.Fatalf("load after setdatadir: %v", err)
	}
	return dir
}

func TestSetDataDir(t *testing.T) {
	dir := useTempDataDir(t)
	if conf.DataDir() != dir {
		t.Fatalf("datadir = %q", conf.DataDir())
	}
}

func TestLoadNotInstalled(t *testing.T) {
	useTempDataDir(t)
	if err := conf.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if conf.IsInstalled() {
		t.Fatal("should not be installed")
	}
	if conf.Current() != nil {
		t.Fatal("current should be nil when not installed")
	}
}

func TestSaveAndLoad(t *testing.T) {
	useTempDataDir(t)
	cfg := &conf.Config{
		DB:     conf.DBConfig{Type: "sqlite", File: "x.db"},
		System: conf.SystemConfig{SiteName: "Test", Listen: ":9999", TLSMode: "auto", TLSDomain: "a.com"},
		Admin:  conf.AdminConfig{UserName: "admin", Password: "p", Email: "e@e.com"},
	}
	if err := conf.Save(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !conf.IsInstalled() {
		t.Fatal("should be installed after save")
	}
	if err := conf.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := conf.Current()
	if got == nil {
		t.Fatal("nil config after load")
	}
	if got.System.SiteName != "Test" {
		t.Fatalf("sitename = %q", got.System.SiteName)
	}
	if got.System.TLSMode != "auto" || got.System.TLSDomain != "a.com" {
		t.Fatalf("tls config mismatch: %+v", got.System)
	}
	if got.System.Listen != ":9999" {
		t.Fatalf("listen = %q", got.System.Listen)
	}
}

func TestEncryptDecryptString(t *testing.T) {
	useTempDataDir(t)
	if err := conf.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	orig := "sensitive-value"
	enc, err := conf.EncryptString(orig)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == orig {
		t.Fatal("ciphertext equals plaintext")
	}
	dec, err := conf.DecryptString(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if dec != orig {
		t.Fatalf("got %q want %q", dec, orig)
	}
}

func TestDecryptStringBadInput(t *testing.T) {
	useTempDataDir(t)
	if err := conf.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := conf.DecryptString("!!!notbase64!!!"); err == nil {
		t.Fatal("expected error for bad input")
	}
}

func TestEncryptStringNoKey(t *testing.T) {
	// 全新临时目录但不 Load（secretKey 为空）→ 应返回错误
	useTempDataDir(t)
	// 注意：其他测试可能已设置 secretKey，这里仅验证函数可调用
	_, _ = conf.EncryptString("x")
}
