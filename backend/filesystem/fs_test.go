package filesystem_test

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/models"
)

func TestNewUnsupported(t *testing.T) {
	if _, err := filesystem.New("unknown", `{}`); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestLocalHandler(t *testing.T) {
	dir := t.TempDir()
	h, err := filesystem.New("local", models.LocalPolicyConfig(dir))
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

// TestLocalConfigJSONEscaping 回归：Windows 路径含反斜杠，直接字符串拼出的
// {"path":"C:/Users/..."} 是**非法 JSON**（\U 不是合法转义）。旧实现忽略
// Unmarshal 错误，cfg.Path 为空后静默回落到相对目录 "uploads"，把用户文件写进
// 进程 CWD —— 落盘位置错误且极易丢失。这里锁死两种行为：
//  1. 正规构造器产出的 config 一定能被正确解析并写进目标目录；
//  2. 手工拼出的畸形 config 不得再静默变成相对目录。
func TestLocalConfigJSONEscaping(t *testing.T) {
	dir := t.TempDir()

	// 1) 构造器路径：反斜杠被正确转义，根目录就是 dir 本身。
	h, err := filesystem.New("local", models.LocalPolicyConfig(dir))
	if err != nil {
		t.Fatalf("New with escaped config: %v", err)
	}
	if err := h.Put(bytes.NewReader([]byte("payload")), "esc.txt", 7); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := filepath.EvalSymlinks(filepath.Join(dir, "esc.txt"))
	if err != nil {
		t.Fatalf("file must land in the configured dir: %v", err)
	}
	if wantDir, _ := filepath.EvalSymlinks(dir); filepath.Dir(got) != wantDir {
		t.Fatalf("file landed in %q, want under %q", got, wantDir)
	}

	// 2) 畸形 config：未转义反斜杠。必须报错，而不是回落到相对目录 uploads。
	//    （conf 未加载时 confUploadPath() 为空，因此这里期望 error）
	broken := `{"path":"` + dir + `"}`
	if _, err := filesystem.New("local", broken); err == nil {
		t.Fatal("malformed config must return an error, not silently fall back to a relative dir")
	}
}
