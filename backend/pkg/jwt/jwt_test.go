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

// alg=none 攻击：伪造一个 unsigned token。
// 库里 golang-jwt/v5 本身会拒绝，但显式白名单是第二道防线——
// 换库版本或未来重构时不至于静默失守。
func TestParseRejectsAlgNone(t *testing.T) {
	secret = []byte("test-secret-key")
	// header {"alg":"none","typ":"JWT"} + payload + 空签名
	tok := "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." +
		"eyJ1aWQiOjQyfQ."
	if _, err := Parse(tok); err == nil {
		t.Fatal("alg=none token 应被拒绝")
	}
}

// alg 混淆攻击：把 alg 改成 RS256，企图用公钥当 HMAC 密钥。
func TestParseRejectsAlgMismatch(t *testing.T) {
	secret = []byte("test-secret-key")
	// header {"alg":"RS256","typ":"JWT"}
	tok := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJ1aWQiOjQyfQ." + "c2ln"
	if _, err := Parse(tok); err == nil {
		t.Fatal("alg=RS256 的 token 应被拒绝")
	}
}
