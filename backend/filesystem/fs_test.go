package filesystem_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/nebula-drive/nebula/filesystem"
)

func TestNewUnsupported(t *testing.T) {
	if _, err := filesystem.New("unknown", `{}`); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestLocalHandler(t *testing.T) {
	dir := t.TempDir()
	h, err := filesystem.New("local", `{"path":"`+dir+`"}`)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	content := []byte("hello world")
	if err := h.Put(bytes.NewReader(content), "a/b.txt", int64(len(content))); err != nil {
		t.Fatalf("put: %v", err)
	}
	sz, err := h.Size("a/b.txt")
	if err != nil || sz != int64(len(content)) {
		t.Fatalf("size = %d err=%v", sz, err)
	}
	rc, err := h.Get("a/b.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: %q", got)
	}
	// GetRange
	rc2, err := h.GetRange("a/b.txt", 6, 5)
	if err != nil {
		t.Fatalf("getrange: %v", err)
	}
	got2, _ := io.ReadAll(rc2)
	rc2.Close()
	if !bytes.Equal(got2, []byte("world")) {
		t.Fatalf("range mismatch: %q", got2)
	}
	// Copy
	if err := h.Copy("a/b.txt", "a/c.txt"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	// PresignGet（本地返回空）
	if u, err := h.PresignGet("a/b.txt", 0); err != nil || u != "" {
		t.Fatalf("presign: u=%q err=%v", u, err)
	}
	// Delete
	if err := h.Delete("a/b.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// 删除不存在的文件不报错
	if err := h.Delete("a/b.txt"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestNewS3Validation(t *testing.T) {
	cases := []string{
		`{}`,
		`{"bucket":"b"}`,
		`{"bucket":"b","region":"r"}`,
		`{"bucket":"b","region":"r","accessKey":"a"}`,
	}
	for _, c := range cases {
		if _, err := filesystem.New("s3", c); err == nil {
			t.Fatalf("expected error for s3 config %s", c)
		}
	}
}

func TestNewS3Handler(t *testing.T) {
	h, err := filesystem.New("s3", `{"bucket":"b","region":"us-east-1","accessKey":"a","secretKey":"s"}`)
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	// PresignGet 不发起网络请求
	if u, err := h.PresignGet("obj", 0); err != nil || u == "" {
		t.Fatalf("presign: u=%q err=%v", u, err)
	}
}

func TestNewOSSValidation(t *testing.T) {
	if _, err := filesystem.New("oss", `{}`); err == nil {
		t.Fatal("expected error for empty oss config")
	}
}

func TestNewOSSHandler(t *testing.T) {
	h, err := filesystem.New("oss", `{"endpoint":"oss-cn-hangzhou.aliyuncs.com","bucket":"mybucket","accessKeyId":"a","accessKeySecret":"s"}`)
	if err != nil {
		t.Fatalf("new oss: %v", err)
	}
	if u, err := h.PresignGet("obj", 0); err != nil || u == "" {
		t.Fatalf("presign: u=%q err=%v", u, err)
	}
}

func TestNewCOSValidation(t *testing.T) {
	if _, err := filesystem.New("cos", `{}`); err == nil {
		t.Fatal("expected error for empty cos config")
	}
}

func TestNewCOSHandler(t *testing.T) {
	h, err := filesystem.New("cos", `{"region":"ap-guangzhou","bucket":"b","secretId":"a","secretKey":"s"}`)
	if err != nil {
		t.Fatalf("new cos: %v", err)
	}
	if u, err := h.PresignGet("obj", 0); err != nil || u == "" {
		t.Fatalf("presign: u=%q err=%v", u, err)
	}
}
