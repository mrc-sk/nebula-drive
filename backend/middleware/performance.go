package middleware

import (
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

// GzipMiddleware gzip 压缩中间件
func GzipMiddleware() gin.HandlerFunc {
	return gzip.Gzip(gzip.DefaultCompression)
}

// StaticCache 静态资源缓存：/assets/ 路径加 Cache-Control + ETag
func StaticCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Next()
			return
		}
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		sum := sha1.Sum([]byte(c.Request.URL.Path))
		etag := "\"" + hex.EncodeToString(sum[:]) + "\""
		c.Header("ETag", etag)
		if match := c.GetHeader("If-None-Match"); match != "" && strings.Contains(match, etag) {
			c.AbortWithStatus(http.StatusNotModified)
			return
		}
		c.Next()
	}
}
