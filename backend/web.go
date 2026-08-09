package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:frontend_dist
var frontendFS embed.FS

// registerFrontend 注册内嵌前端静态资源与 SPA fallback
func registerFrontend(r *gin.Engine) {
	dist, err := fs.Sub(frontendFS, "frontend_dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API / WebDAV 走 JSON 404
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/dav/") {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
			return
		}

		// 存在的静态资源直接返回
		if path != "/" {
			rel := strings.TrimPrefix(path, "/")
			if f, err := dist.Open(rel); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		// 其余一律返回 index.html，交给前端路由（SPA）
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
