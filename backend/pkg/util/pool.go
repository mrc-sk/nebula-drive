package util

import "sync"

// bufferSize 复用缓冲区大小：32KB
const bufferSize = 32 * 1024

// bufPool 32KB buffer 复用池，减少 Upload/Download 时的 GC 压力
var bufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, bufferSize)
		return &b
	},
}

// GetBuffer 取一个 32KB buffer（*[]byte，避免拷贝）
func GetBuffer() *[]byte {
	return bufPool.Get().(*[]byte)
}

// PutBuffer 归还 buffer。nil 忽略
func PutBuffer(b *[]byte) {
	if b == nil {
		return
	}
	bufPool.Put(b)
}

// BufferSize 返回 buffer 大小
func BufferSize() int { return bufferSize }
