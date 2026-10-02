package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/internal/cryptox"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// findAccessToken 按令牌查找 OAuth2 access token，查不到返回 nil。
//
// 令牌在库中以单向哈希存储，因此查询必须对输入先哈希。
// 第二段的明文查询是**迁移兜底**：若存量数据尚未被迁移脚本转换，
// 宁可临时放过一条明文令牌，也不能让迁移失败导致所有 API 调用全部失效。
// 迁移完成后（models.MigrateSensitiveFields）这段自然不会再命中。
func findAccessToken(tok string) *models.AccessToken {
	var at models.AccessToken
	if err := db.Get().Where("token = ?", cryptox.TokenHash(tok)).First(&at).Error; err == nil {
		return &at
	}
	if err := db.Get().Where("token = ?", tok).First(&at).Error; err == nil {
		return &at
	}
	return nil
}

// findPAT 按令牌查找 Personal Access Token，查不到返回 nil。
// 迁移兜底逻辑同 findAccessToken。
func findPAT(tok string) *models.PersonalAccessToken {
	var pat models.PersonalAccessToken
	if err := db.Get().Where("token = ?", cryptox.TokenHash(tok)).First(&pat).Error; err == nil {
		return &pat
	}
	if err := db.Get().Where("token = ?", tok).First(&pat).Error; err == nil {
		return &pat
	}
	return nil
}

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
		if at := findAccessToken(tok); at != nil {
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
		if pat := findPAT(tok); pat != nil {
			if pat.ExpiresAt != nil && pat.ExpiresAt.Before(time.Now()) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "token expired"})
				return
			}
			var u models.User
			if err := db.Get().First(&u, pat.UserID).Error; err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "user not found"})
				return
			}
			db.Get().Model(pat).Update("last_used_at", time.Now())
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
