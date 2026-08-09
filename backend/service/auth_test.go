package service_test

import (
	"testing"

	"github.com/nebula-drive/nebula/pkg/testutil"
	"github.com/nebula-drive/nebula/service"
)

func TestValidateTOTPEmpty(t *testing.T) {
	if service.ValidateTOTP("", "123") {
		t.Fatal("empty secret should fail")
	}
	if service.ValidateTOTP("secret", "") {
		t.Fatal("empty code should fail")
	}
}

func TestNewTOTPSecret(t *testing.T) {
	s, err := service.NewTOTPSecret("alice")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if s == "" {
		t.Fatal("empty secret")
	}
	if service.ValidateTOTP(s, "") {
		t.Fatal("empty code should fail")
	}
}

func TestEmailCode(t *testing.T) {
	testutil.SetupDB(t)
	code, err := service.GenEmailCode("a@b.com", "activate")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if code == "" {
		t.Fatal("empty code")
	}
	// 错误验证码
	if service.VerifyEmailCode("a@b.com", "activate", "wrong") {
		t.Fatal("wrong code should fail")
	}
	// 正确验证码
	if !service.VerifyEmailCode("a@b.com", "activate", code) {
		t.Fatal("correct code should pass")
	}
	// 已使用，再次校验失败
	if service.VerifyEmailCode("a@b.com", "activate", code) {
		t.Fatal("used code should fail")
	}
}
