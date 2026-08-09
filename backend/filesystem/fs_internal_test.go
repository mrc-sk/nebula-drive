package filesystem

import "testing"

func TestS3UploadConfig(t *testing.T) {
	if ps, c := s3UploadConfig(1024); ps != 0 || c != 0 {
		t.Fatalf("small: ps=%d c=%d", ps, c)
	}
	if ps, c := s3UploadConfig(6 * 1024 * 1024 * 1024); ps != 20*1024*1024 || c != 16 {
		t.Fatalf("large: ps=%d c=%d", ps, c)
	}
	if ps, c := s3UploadConfig(200 * 1024 * 1024); ps != 10*1024*1024 || c != 5 {
		t.Fatalf("mid: ps=%d c=%d", ps, c)
	}
}

func TestOSSUploadConfig(t *testing.T) {
	if ps, c := ossUploadConfig(1024); ps != 10*1024*1024 || c != 5 {
		t.Fatalf("small: ps=%d c=%d", ps, c)
	}
	if ps, c := ossUploadConfig(6 * 1024 * 1024 * 1024); ps != 20*1024*1024 || c != 16 {
		t.Fatalf("large: ps=%d c=%d", ps, c)
	}
}

func TestCOSUploadConfig(t *testing.T) {
	if ps, c := cosUploadConfig(1024); ps != 10*1024*1024 || c != 5 {
		t.Fatalf("small: ps=%d c=%d", ps, c)
	}
	if ps, c := cosUploadConfig(6 * 1024 * 1024 * 1024); ps != 20*1024*1024 || c != 16 {
		t.Fatalf("large: ps=%d c=%d", ps, c)
	}
}

func TestStrconvInt64(t *testing.T) {
	if n, _ := strconvInt64(""); n != 0 {
		t.Fatalf("empty: %d", n)
	}
	if n, _ := strconvInt64("12345"); n != 12345 {
		t.Fatalf("got %d", n)
	}
}
