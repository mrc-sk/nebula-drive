package routers

import (
	"net/http"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/controllers"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/webdav"
)

// davAllMethods Gin r.Any 只覆盖 9 个 HTTP 方法，不含 WebDAV 扩展方法（PROPFIND/MKCOL/MOVE/COPY/LOCK/UNLOCK/PROPPATCH）。
// 这里显式列出所有方法，确保 WebDAV 客户端（Windows/Finder/RaiDrive）能真实挂载。
var davAllMethods = []string{
	"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS",
	"PROPFIND", "PROPPATCH", "MKCOL", "MOVE", "COPY", "LOCK", "UNLOCK",
}

func Setup() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.GzipMiddleware())
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     append([]string{}, davAllMethods...),
		AllowHeaders:     []string{"*"},
		AllowCredentials: false,
	}))

	r.Use(middleware.IPGuard())
	r.Use(middleware.InstallGuard())
	r.Use(middleware.StaticCache())

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "installed": conf.IsInstalled()})
	})

	api := r.Group("/api")
	{
		api.GET("/install/status", controllers.InstallStatus)
		inst := api.Group("", middleware.InstalledBlock())
		{
			inst.POST("/install", controllers.Install)
			inst.POST("/install/test-db", controllers.TestDB)
		}

		auth := api.Group("/auth")
		{
			auth.POST("/login", middleware.RateLimit("login"), controllers.Login)
			auth.POST("/register", controllers.Register)
		}
		authAuthed := api.Group("/auth", middleware.Auth(true))
		{
			authAuthed.POST("/logout", controllers.Logout)
			authAuthed.GET("/me", controllers.Me)
			authAuthed.POST("/2fa/setup", controllers.Setup2FA)
			authAuthed.POST("/2fa/confirm", controllers.Confirm2FA)
			authAuthed.PUT("/password", controllers.ChangePassword)
			authAuthed.PUT("/profile", controllers.UpdateProfile)
		}

		admin := api.Group("/admin", middleware.Auth(true), middleware.AdminOnly())
		{
			admin.GET("/users", controllers.ListUsers)
			admin.POST("/users", controllers.CreateUser)
			admin.PUT("/users/:id", controllers.UpdateUser)
			admin.DELETE("/users/:id", middleware.RequireConfirm(), controllers.DeleteUser)
			admin.GET("/groups", controllers.ListGroups)
			admin.POST("/groups", controllers.CreateGroup)
			admin.PUT("/groups/:id", controllers.UpdateGroup)
			admin.GET("/plugins", controllers.ListPlugins)
			admin.POST("/plugins/:id/toggle", controllers.TogglePlugin)
			admin.GET("/plugins/store", controllers.StorePluginCatalog)
			admin.GET("/plugins/hooks", controllers.ListPluginHooks)
			admin.GET("/policies", controllers.ListPolicies)
			admin.POST("/policies", controllers.CreatePolicy)
			admin.PUT("/policies/:id", controllers.UpdatePolicy)
			admin.DELETE("/policies/:id", controllers.DeletePolicy)
			admin.GET("/settings", controllers.GetSettings)
			admin.POST("/settings", controllers.SaveSettings)
			admin.GET("/dashboard", controllers.Dashboard)
			admin.POST("/upload-cert", controllers.UploadCert)
			admin.GET("/ip-bans", controllers.ListIPBans)
			admin.POST("/ip-bans", controllers.AddIPBan)
			admin.DELETE("/ip-bans/:id", controllers.DeleteIPBan)
			// 付费体系
			admin.GET("/plans", controllers.ListPlans)
			admin.POST("/plans", controllers.CreatePlan)
			admin.PUT("/plans/:id", controllers.UpdatePlan)
			admin.DELETE("/plans/:id", controllers.DeletePlan)
			admin.GET("/codes", controllers.ListCodes)
			admin.POST("/codes/generate", controllers.GenerateCodes)
			admin.PUT("/codes/:id/disable", controllers.DisableCode)
			admin.DELETE("/codes/:id", controllers.DeleteCode)
			admin.GET("/subscriptions", controllers.ListSubscriptions)
			admin.GET("/redemption-logs", controllers.ListRedemptionLogs)
		}

		files := api.Group("/files", middleware.Auth(true))
		{
			files.GET("", controllers.List)
			files.GET("/tags", controllers.ListTags)
			files.POST("/batch-move", controllers.BatchMove)
			files.POST("/batch-copy", controllers.BatchCopy)
			files.POST("/batch-policy", controllers.BatchPolicy)
			files.POST("/batch-delete", middleware.RequireConfirm(), controllers.BatchDelete)
			files.GET("/breadcrumb/:id", controllers.Breadcrumb)
			files.POST("/mkdir", controllers.Mkdir)
			files.POST("/rapid", controllers.Rapid)
			files.POST("/upload", middleware.RateLimit("upload"), controllers.Upload)
			files.POST("/chunk-init", middleware.RateLimit("upload"), controllers.ChunkInit)
			files.POST("/chunk-upload", middleware.RateLimit("upload"), controllers.ChunkUpload)
			files.POST("/chunk-merge", middleware.RateLimit("upload"), controllers.ChunkMerge)
			files.GET("/:id/download", controllers.Download)
			files.PUT("/:id/rename", controllers.Rename)
			files.PUT("/:id/move", controllers.Move)
			files.DELETE("/:id", middleware.RequireConfirm(), controllers.Delete)
			files.POST("/:id/restore", controllers.Restore)
			files.POST("/:id/purge", controllers.Purge)
			files.GET("/:id/preview", controllers.Preview)
			files.GET("/:id/thumb", controllers.Thumb)
			// 文件版本控制
			files.POST("/:id/upload-version", controllers.UploadVersion)
			files.GET("/:id/versions", controllers.ListVersions)
			files.GET("/:id/versions/:vid/download", controllers.DownloadVersion)
			files.POST("/:id/versions/:vid/restore", controllers.RestoreVersion)
			files.DELETE("/:id/versions/:vid", controllers.DeleteVersion)
			// 文件标签
			files.POST("/:id/tags", controllers.AddTag)
			files.DELETE("/:id/tags/:tag", controllers.RemoveTag)
			// 协作编辑
			files.GET("/:id/collab", controllers.CollabOpen)
			files.POST("/:id/collab/save", controllers.CollabSave)
		}

		// Yjs CRDT 同步 WebSocket
		api.GET("/collab/ws/:docId", middleware.Auth(true), controllers.CollabWS)

		shares := api.Group("/shares", middleware.Auth(true))
		{
			shares.GET("", controllers.ListShares)
			shares.POST("", controllers.CreateShare)
			shares.DELETE("/:id", controllers.DeleteShare)
		}
		// 公开分享页读取：既支持前端 GET query 参数，也兼容前端 POST body（password/extractCode）
		api.GET("/shares/:id", controllers.GetShare)
		api.POST("/shares/:id", controllers.GetShare)

		tasks := api.Group("/tasks", middleware.Auth(true))
		{
			tasks.GET("", controllers.ListTasks)
			tasks.POST("", controllers.CreateTask)
			tasks.POST("/:id/cancel", controllers.CancelTask)
		}

		notifications := api.Group("/notifications", middleware.Auth(true))
		{
			notifications.GET("", controllers.ListNotifications)
			notifications.GET("/unread-count", controllers.UnreadCount)
			notifications.PUT("/:id/read", controllers.MarkRead)
			notifications.PUT("/read-all", controllers.MarkAllRead)
			notifications.DELETE("/:id", controllers.DeleteNotification)
		}

		api.GET("/ws", middleware.Auth(true), controllers.WSHandler)

		// OAuth2.0
		oauth := api.Group("/oauth")
		{
			oauth.POST("/apps", middleware.Auth(true), middleware.AdminOnly(), controllers.CreateOAuthApp)
			oauth.GET("/apps", middleware.Auth(true), controllers.ListOAuthApps)
			oauth.DELETE("/apps/:id", middleware.Auth(true), controllers.DeleteOAuthApp)
			oauth.GET("/authorize", middleware.Auth(true), controllers.OAuthAuthorize)
			oauth.POST("/authorize", middleware.Auth(true), controllers.OAuthAuthorizeConfirm)
			oauth.POST("/token", controllers.OAuthToken)
			oauth.GET("/userinfo", middleware.APIAuth(), controllers.OAuthUserInfo)
		}

		// 个人访问令牌
		pat := api.Group("/pat", middleware.Auth(true))
		{
			pat.POST("", controllers.CreatePAT)
			pat.GET("", controllers.ListPATs)
			pat.DELETE("/:id", controllers.DeletePAT)
		}

		// 付费体系（用户端）
		api.GET("/plans", controllers.GetPlans)
		planUser := api.Group("/plan", middleware.Auth(true))
		{
			planUser.GET("/my", controllers.GetMySubscription)
			planUser.POST("/redeem", controllers.RedeemCode)
		}

		// 开放 API v1（OAuth Token / PAT 鉴权）
		v1 := api.Group("/v1", middleware.APIAuth())
		{
			v1.GET("/files", controllers.List)
			v1.POST("/files/upload", middleware.RateLimit("upload"), controllers.Upload)
			v1.GET("/files/:id/download", controllers.Download)
		}
	}

	r.Match(davAllMethods, "/dav/*p", func(c *gin.Context) {
		if !conf.IsInstalled() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "system not installed"})
			return
		}
		webdav.Handler()(c)
	})

	return r
}

// InitWebDAVRoutes 在 db 初始化后注册自定义 WebDAV 路径路由
func InitWebDAVRoutes(r *gin.Engine) {
	if db.Get() == nil {
		return
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", "webdav.path").First(&s).Error; err == nil && s.Value != "" && s.Value != "/dav" {
		p := s.Value
		if !strings.HasSuffix(p, "/") {
			p += "/"
		}
		p += "*p"
		r.Match(davAllMethods, p, func(c *gin.Context) {
			if !conf.IsInstalled() {
				c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503})
				return
			}
			webdav.Handler()(c)
		})
	}
}
