package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// APIAuth 开放 API 鉴权：从 Authorization: Bearer xxx 提取 token，
// 先查 AccessToken 表（OAuth2），再查 PersonalAccessToken 表（PAT），
// 验证有效期后设置当前用户到 context。
func APIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := extractBearerToken(c)
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "missing token"})
			return
		}
		// 1. OAuth2 access token
		var at models.AccessToken
		if err := db.Get().Where("token = ?", tok).First(&at).Error; err == nil {
			if at.ExpiresAt.Before(time.Now()) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token expired"})
				return
			}
			var u models.User
			if err := db.Get().First(&u, at.UserID).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
				return
			}
			c.Set(CtxUserKey, &u)
			c.Set("api_scope", at.Scope)
			c.Set("api_token_type", "oauth")
			c.Next()
			return
		}
		// 2. Personal Access Token
		var pat models.PersonalAccessToken
		if err := db.Get().Where("token = ?", tok).First(&pat).Error; err == nil {
			if pat.ExpiresAt != nil && pat.ExpiresAt.Before(time.Now()) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token expired"})
				return
			}
			var u models.User
			if err := db.Get().First(&u, pat.UserID).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
				return
			}
			now := time.Now()
			db.Get().Model(&pat).Update("last_used_at", now)
			c.Set(CtxUserKey, &u)
			c.Set("api_scope", pat.Scope)
			c.Set("api_token_type", "pat")
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid token"})
	}
}

// extractBearerToken 从 Authorization 头提取 Bearer token
func extractBearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}
