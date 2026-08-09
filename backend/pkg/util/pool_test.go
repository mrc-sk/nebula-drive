package util

import "testing"

func TestBufferSize(t *testing.T) {
	if BufferSize() != 32*1024 {
		t.Fatalf("unexpected buffer size: %d", BufferSize())
	}
}

func TestBufferPool(t *testing.T) {
	for i := 0; i < 10; i++ {
		b := GetBuffer()
		if b == nil {
			t.Fatal("nil buffer")
		}
		if len(*b) != BufferSize() {
			t.Fatalf("invalid buffer len: %d", len(*b))
		}
		if cap(*b) < BufferSize() {
			t.Fatalf("invalid buffer cap: %d", cap(*b))
		}
		(*b)[0] = byte(i)
		(*b)[BufferSize()-1] = byte(i)
		PutBuffer(b)
	}
	// nil 不应 panic
	PutBuffer(nil)
}

func TestBufferPoolReusable(t *testing.T) {
	// 连续 Get/Put 通常能复用同一对象
	b := GetBuffer()
	(*b)[0] = 0x7F
	PutBuffer(b)
	b2 := GetBuffer()
	if (*b2)[0] == 0x7F {
		// 复用成功
	}
	PutBuffer(b2)
}
