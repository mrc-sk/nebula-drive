package util

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

// UUID 生成 UUID
func UUID() string { return uuid.NewString() }

// RandomCode 生成数字验证码
func RandomCode(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		b, _ := rand.Int(rand.Reader, big.NewInt(10))
		sb.WriteByte(byte('0' + b.Int64()))
	}
	return sb.String()
}

// RandomStr 生成随机字母数字串
func RandomStr(n int) string {
	const set = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		b, _ := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		sb.WriteByte(set[b.Int64()])
	}
	return sb.String()
}
