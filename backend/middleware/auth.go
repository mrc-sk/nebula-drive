package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/jwt"
)

// CtxUserKey 当前用户在 context 中的键
const CtxUserKey = "current_user"

// Auth 认证中间件。required=true 时未登录返回 401
func Auth(required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := extractToken(c)
		if tok == "" {
			if required {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
				return
			}
			c.Next()
			return
		}
		claims, err := jwt.Parse(tok)
		if err != nil {
			if required {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid token"})
				return
			}
			c.Next()
			return
		}
		// 校验会话仍有效（单设备登录核心：被踢的旧 token 的 session 已删）
		var sess models.Session
		if err := db.Get().Where("session_id = ? AND expires_at > ?", claims.SessionID, time.Now()).First(&sess).Error; err != nil {
			if required {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "session expired"})
				return
			}
			c.Next()
			return
		}
		var u models.User
		if err := db.Get().First(&u, claims.UserID).Error; err != nil {
			if required {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
				return
			}
			c.Next()
			return
		}
		c.Set(CtxUserKey, &u)
		c.Next()
	}
}

func extractToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t, err := c.Cookie("nebula_token"); err == nil {
		return t
	}
	return ""
}

// CurrentUser 取当前登录用户
func CurrentUser(c *gin.Context) *models.User {
	v, ok := c.Get(CtxUserKey)
	if !ok {
		return nil
	}
	u, _ := v.(*models.User)
	return u
}

// AdminOnly 仅管理员
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil || !u.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "admin only"})
			return
		}
		c.Next()
	}
}
