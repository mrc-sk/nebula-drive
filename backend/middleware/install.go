package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
)

// InstallGuard 未安装时只放行安装向导相关接口与静态资源，其余强制跳转
func InstallGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if conf.IsInstalled() {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		// 放行安装向导 API 与健康检查
		if path == "/api/install" || path == "/api/install/status" || path == "/api/health" {
			c.Next()
			return
		}
		// 放行前端静态资源（让 SPA 渲染安装页）
		if len(path) >= 4 && path[:4] == "/ass" { // /assets
			c.Next()
			return
		}
		if path == "/" || path == "/index.html" {
			c.Next()
			return
		}
		// API 请求返回未安装
		if len(path) >= 4 && path[:4] == "/api" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code":    503,
				"message": "system not installed",
			})
			return
		}
		c.Next()
	}
}

// InstalledBlock 已安装后禁止再次调用安装接口
func InstalledBlock() gin.HandlerFunc {
	return func(c *gin.Context) {
		if conf.IsInstalled() {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "system already installed",
			})
			return
		}
		c.Next()
	}
}
