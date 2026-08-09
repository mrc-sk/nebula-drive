package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// RequireConfirm 敏感操作二次确认：校验请求头 X-Confirm-Password 是否匹配当前用户密码（bcrypt）
func RequireConfirm() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "unauthorized"})
			return
		}
		pwd := c.GetHeader("X-Confirm-Password")
		if pwd == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "敏感操作需要二次确认密码"})
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(pwd)); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "二次确认密码错误"})
			return
		}
		c.Next()
	}
}
