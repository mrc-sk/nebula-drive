package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

const (
	loginFailWindow      = 5 * time.Minute
	loginFailThreshold   = 10
	captchaFailThreshold = 3
)

type loginFailRecord struct {
	count int
	first time.Time
}

var (
	loginFailMu   sync.Mutex
	loginFailures = make(map[string]*loginFailRecord)
)

// IPGuard IP 黑名单中间件：请求 IP 命中未过期封禁则 403
func IPGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if IsIPBanned(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "ip banned"})
			return
		}
		c.Next()
	}
}

// IsIPBanned 检查 IP 是否被封禁（命中且未过期）
func IsIPBanned(ip string) bool {
	if db.Get() == nil || ip == "" {
		return false
	}
	var ban models.IPBan
	if err := db.Get().Where("ip = ?", ip).First(&ban).Error; err != nil {
		return false
	}
	if ban.ExpiresAt != nil && ban.ExpiresAt.Before(time.Now()) {
		return false
	}
	return true
}

// RequireCaptcha 连续失败 captchaFailThreshold 次后强制要求验证码
func RequireCaptcha(ip string) bool {
	if ip == "" {
		return false
	}
	loginFailMu.Lock()
	defer loginFailMu.Unlock()
	rec, ok := loginFailures[ip]
	if !ok {
		return false
	}
	if time.Since(rec.first) > loginFailWindow {
		return false
	}
	return rec.count >= captchaFailThreshold
}

// RecordLoginFailure 记录登录失败，5 分钟内达 10 次自动封禁 1 小时
func RecordLoginFailure(ip string) {
	if ip == "" {
		return
	}
	loginFailMu.Lock()
	now := time.Now()
	rec, ok := loginFailures[ip]
	if !ok || now.Sub(rec.first) > loginFailWindow {
		rec = &loginFailRecord{count: 1, first: now}
		loginFailures[ip] = rec
	} else {
		rec.count++
	}
	count := rec.count
	exceeded := count >= loginFailThreshold
	if exceeded {
		delete(loginFailures, ip)
	}
	loginFailMu.Unlock()

	if exceeded {
		autoBanIP(ip)
	}
}

// ClearLoginFailure 登录成功后清除失败计数
func ClearLoginFailure(ip string) {
	loginFailMu.Lock()
	delete(loginFailures, ip)
	loginFailMu.Unlock()
}

// autoBanIP 自动封禁 IP（已存在记录则不覆盖）
func autoBanIP(ip string) {
	if db.Get() == nil {
		return
	}
	var existing models.IPBan
	if err := db.Get().Where("ip = ?", ip).First(&existing).Error; err == nil {
		return
	}
	exp := time.Now().Add(time.Hour)
	db.Get().Create(&models.IPBan{
		IP:        ip,
		Reason:    "too many login failures",
		ExpiresAt: &exp,
		Auto:      true,
	})
}
