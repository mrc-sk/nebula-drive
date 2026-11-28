package middleware

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// 安全响应头的默认值，与历史行为一致。
const (
	defaultCSP        = "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self' ws: wss:"
	defaultFrameOpts  = "SAMEORIGIN"
	defaultReferrer   = "strict-origin-when-cross-origin"
	defaultHSTSMaxAge = 31536000
)

// setHeaderIfEnabled 设置响应头；值为 "off" 时跳过（管理员可关闭特定头）。
func setHeaderIfEnabled(c *gin.Context, key, header, def string) {
	v := settingStr(key, def)
	if v != "off" {
		c.Header(header, v)
	}
}

// SecurityHeaders 安全响应头中间件：CSP / 防点击劫持 / MIME 嗅探 / XSS / Referrer / HSTS。
// 除 nosniff / XSS 固定外，其余均可在后台「安全」页配置。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		setHeaderIfEnabled(c, "security.csp", "Content-Security-Policy", defaultCSP)
		setHeaderIfEnabled(c, "security.frame_options", "X-Frame-Options", defaultFrameOpts)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-XSS-Protection", "1; mode=block")
		setHeaderIfEnabled(c, "security.referrer_policy", "Referrer-Policy", defaultReferrer)
		if age := settingInt("security.hsts_max_age", defaultHSTSMaxAge); age > 0 {
			c.Header("Strict-Transport-Security", "max-age="+strconv.Itoa(age)+"; includeSubDomains")
		}
		c.Next()
	}
}
