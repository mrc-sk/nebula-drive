package controllers

import (
	"encoding/json"
	"testing"
)

func TestAria2ToInt(t *testing.T) {
	if got := aria2ToInt("123"); got != 123 {
		t.Fatalf("got %d", got)
	}
	if got := aria2ToInt(float64(456)); got != 456 {
		t.Fatalf("got %d", got)
	}
	if got := aria2ToInt("abc"); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := aria2ToInt(nil); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := aria2ToInt(json.Number("789")); got != 789 {
		t.Fatalf("got %d", got)
	}
}

func TestAria2Progress(t *testing.T) {
	if got := aria2Progress(map[string]any{"completedLength": "50", "totalLength": "200"}); got != 25 {
		t.Fatalf("got %d", got)
	}
	if got := aria2Progress(map[string]any{"totalLength": "0"}); got != 0 {
		t.Fatalf("got %d", got)
	}
	if got := aria2Progress(map[string]any{}); got != 0 {
		t.Fatalf("got %d", got)
	}
	// 超过 100 截断
	if got := aria2Progress(map[string]any{"completedLength": "300", "totalLength": "100"}); got != 100 {
		t.Fatalf("got %d", got)
	}
}

func TestAria2FirstPath(t *testing.T) {
	if got := aria2FirstPath(map[string]any{"files": []any{map[string]any{"path": "/a/b.txt"}}}); got != "/a/b.txt" {
		t.Fatalf("got %q", got)
	}
	if got := aria2FirstPath(map[string]any{}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := aria2FirstPath(map[string]any{"files": []any{}}); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := aria2FirstPath(map[string]any{"files": []any{"not-a-map"}}); got != "" {
		t.Fatalf("got %q", got)
	}
}
