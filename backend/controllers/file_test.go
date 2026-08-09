package controllers

import (
	"testing"

	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/testutil"
)

func TestParseRange(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		total      int64
		wantOffset int64
		wantLength int64
		wantOk     bool
	}{
		{"bytes start-end", "bytes=0-99", 200, 0, 100, true},
		{"bytes start-", "bytes=100-", 200, 100, 100, true},
		{"bytes suffix", "bytes=-50", 200, 150, 50, true},
		{"invalid prefix", "items=0-99", 200, 0, 0, false},
		{"bad spec", "bytes=abc", 200, 0, 0, false},
		{"start beyond total", "bytes=300-", 200, 0, 0, false},
		{"end before start", "bytes=100-50", 200, 0, 0, false},
		{"multi first", "bytes=0-10,20-30", 200, 0, 11, true},
		{"suffix larger than total", "bytes=-500", 200, 0, 200, true},
		{"empty", "", 200, 0, 0, false},
		{"negative suffix", "bytes=-0", 200, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			off, length, ok := parseRange(tt.header, tt.total)
			if ok != tt.wantOk {
				t.Fatalf("ok=%v want %v (off=%d len=%d)", ok, tt.wantOk, off, length)
			}
			if ok && (off != tt.wantOffset || length != tt.wantLength) {
				t.Fatalf("off=%d len=%d, want off=%d len=%d", off, length, tt.wantOffset, tt.wantLength)
			}
		})
	}
}

func TestIsImageExt(t *testing.T) {
	for _, e := range []string{"jpg", "jpeg", "png", "gif", "webp", "JPG"} {
		if !isImageExt(e) {
			t.Fatalf("%s should be image", e)
		}
	}
	for _, e := range []string{"txt", "pdf", "", "go"} {
		if isImageExt(e) {
			t.Fatalf("%s should not be image", e)
		}
	}
}

func TestTrimDot(t *testing.T) {
	if got := trimDot(".jpg"); got != "jpg" {
		t.Fatalf("got %q", got)
	}
	if got := trimDot("jpg"); got != "jpg" {
		t.Fatalf("got %q", got)
	}
	if got := trimDot("..x"); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanName(t *testing.T) {
	if got := cleanName("http://x.com/a/b.txt"); got != "b.txt" {
		t.Fatalf("got %q", got)
	}
	if got := cleanName("plain"); got != "plain" {
		t.Fatalf("got %q", got)
	}
	// 最后一个分隔符 '=' 之后为 "e"
	if got := cleanName("http://x.com/a?b=c&d=e"); got != "e" {
		t.Fatalf("got %q", got)
	}
}

func TestMimeTypeByExt(t *testing.T) {
	if got := mimeTypeByExt(".zip"); got != "application/x-archive" {
		t.Fatalf("got %q", got)
	}
	if got := mimeTypeByExt(".txt"); got != "text/plain" {
		t.Fatalf("got %q", got)
	}
	if got := mimeTypeByExt(".png"); got != "image/png" {
		t.Fatalf("got %q", got)
	}
	if got := mimeTypeByExt(".mp4"); got != "video/mp4" {
		t.Fatalf("got %q", got)
	}
	if got := mimeTypeByExt(".unknownext"); got != "application/octet-stream" {
		t.Fatalf("got %q", got)
	}
}

func TestIsAllowedExtensionDefaults(t *testing.T) {
	db.DB = nil // 走默认白名单
	for _, e := range []string{"jpg", "png", "pdf", "go", "JS"} {
		if !isAllowedExtension(e) {
			t.Fatalf("%s should be allowed", e)
		}
	}
	for _, e := range []string{"exe", "bat", "sh", ""} {
		if isAllowedExtension(e) {
			t.Fatalf("%s should not be allowed", e)
		}
	}
}

func TestIsAllowedExtensionWithSetting(t *testing.T) {
	testutil.SetupDB(t)
	testutil.SeedSetting("upload.allowed_extensions", "txt,md")
	if !isAllowedExtension("txt") {
		t.Fatal("txt should be allowed")
	}
	if isAllowedExtension("jpg") {
		t.Fatal("jpg should not be allowed")
	}
}

func TestDefaultAllowedExtensions(t *testing.T) {
	if defaultAllowedExtensions() == "" {
		t.Fatal("empty default extensions")
	}
}
