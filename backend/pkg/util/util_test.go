package util

import (
	"strings"
	"testing"
)

func TestUUID(t *testing.T) {
	a := UUID()
	b := UUID()
	if a == "" || b == "" {
		t.Fatal("empty uuid")
	}
	if a == b {
		t.Fatalf("uuid collision: %s", a)
	}
	if len(a) != 36 {
		t.Fatalf("unexpected uuid length: %d", len(a))
	}
}

func TestRandomCode(t *testing.T) {
	c := RandomCode(6)
	if len(c) != 6 {
		t.Fatalf("expected 6 digits, got %d", len(c))
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			t.Fatalf("non-digit in code: %q", c)
		}
	}
	if RandomCode(0) != "" {
		t.Fatal("expected empty for 0")
	}
}

func TestRandomStr(t *testing.T) {
	const set = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	s := RandomStr(16)
	if len(s) != 16 {
		t.Fatalf("expected 16, got %d", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(set, r) {
			t.Fatalf("unexpected char: %q", r)
		}
	}
	// 不同次生成不同（极大概率）
	if s2 := RandomStr(16); s == s2 {
		t.Fatal("random str collision")
	}
}
