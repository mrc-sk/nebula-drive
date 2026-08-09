package jwt

import (
	"testing"
	"time"

	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestSignParse(t *testing.T) {
	secret = []byte("test-secret-key")
	tok, err := Sign(42, "sess-1", time.Hour)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	c, err := Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.UserID != 42 || c.SessionID != "sess-1" {
		t.Fatalf("claims = %+v", c)
	}
}

func TestParseInvalid(t *testing.T) {
	secret = []byte("test-secret-key")
	if _, err := Parse("not-a-token"); err == nil {
		t.Fatal("expected error for invalid token")
	}
	if _, err := Parse(""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestParseWrongSecret(t *testing.T) {
	secret = []byte("secret-a")
	tok, _ := Sign(1, "s", time.Hour)
	secret = []byte("secret-b")
	if _, err := Parse(tok); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestInitFromDB(t *testing.T) {
	testutil.SetupDB(t)
	if err := Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(secret) == 0 {
		t.Fatal("secret not set after init")
	}
	// 二次 Init 应复用已存在的 secret
	prev := string(secret)
	if err := Init(); err != nil {
		t.Fatalf("init2: %v", err)
	}
	if string(secret) != prev {
		t.Fatal("secret changed on second init")
	}
}
