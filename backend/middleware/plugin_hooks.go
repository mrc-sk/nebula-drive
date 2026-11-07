package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/hooks"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// PluginRequestHooks 覆盖所有 /api 请求的插件边界钩子。
//
// 挂在 api.Group("/api") 上，因此不需要逐个 controller 接线就能让
// onRequestBefore / onRequestAfter / onAuditLog 真实生效。
// 所有派发都走 FireIf 轻量门：没有插件订阅时零 IPC、零额外分配。
func PluginRequestHooks() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		extras := map[string]any{}
		if u != nil {
			extras["userId"] = u.ID
			extras["userName"] = u.UserName
		}
		before := hooks.ReqCtx(c, extras)

		allowed, reason, mutated := hooks.DecideMutate(plugin.HookRequestBefore, before)
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 403, "message": defaultMsg(reason, "请求被插件拦截"),
			})
			return
		}
		// 插件可通过 ctx.headers 注入响应头
		if hdr, ok := mutated["headers"].(map[string]any); ok {
			for k, v := range hdr {
				if s, ok := v.(string); ok {
					c.Header(k, s)
				}
			}
		}

		start := time.Now()
		c.Next()

		afterExtras := map[string]any{
			"status":     c.Writer.Status(),
			"durationMs": time.Since(start).Milliseconds(),
		}
		if u != nil {
			afterExtras["userId"] = u.ID
			afterExtras["userName"] = u.UserName
		}
		hooks.Emit(plugin.HookRequestAfter, hooks.ReqCtx(c, afterExtras))

		// 写操作审计外送（宿主自身 audit_logs 不受影响）
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			audit := map[string]any{
				"action": c.Request.Method, "target": c.FullPath(),
				"result": c.Writer.Status(), "ip": c.ClientIP(),
				"ua": c.GetHeader("User-Agent"), "requestId": c.GetHeader("X-Request-Id"),
			}
			if u != nil {
				audit["userId"] = u.ID
				audit["userName"] = u.UserName
			}
			hooks.Emit(plugin.HookAuditLog, audit)
		}
	}
}

// PluginOpenAPIHooks /api/v1/* 专用：APIAuth 之后、业务之前。
func PluginOpenAPIHooks() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		extras := map[string]any{"ip": c.ClientIP()}
		if u != nil {
			extras["userId"] = u.ID
			extras["clientId"] = ""
		}
		if v, ok := c.Get("api_scope"); ok {
			extras["scope"] = v
		}
		if v, ok := c.Get("api_token_type"); ok {
			extras["tokenType"] = v
		}
		allowed, reason := hooks.Decide(plugin.HookOpenAPIRequest, hooks.ReqCtx(c, extras))
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": 403, "message": defaultMsg(reason, "开放 API 请求被插件拦截"),
			})
			return
		}
		c.Next()
	}
}

func defaultMsg(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
