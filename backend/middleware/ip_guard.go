package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

const (
	loginFailWindow      = 5 * time.Minute
	loginFailThreshold   = 10 // 兜底默认，实际读 security.login_max_attempts
	captchaFailThreshold = 3
)

// loginMaxAttempts 从设置读取登录失败锁定阈值；管理员在后台可调。
func loginMaxAttempts() int { return settingInt("security.login_max_attempts", loginFailThreshold) }

// banHoursFromSetting 封禁时长（小时），默认 24（对齐后台设置页旧默认值）。
func banHoursFromSetting() int { return settingInt("security.ban_duration_hours", 24) }

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

// isManuallyBanned 检查管理员在后台填的手动黑名单（security.ip_blacklist），
// 每行一个 IP 或 CIDR，# 开头为注释。
func isManuallyBanned(ip string) bool {
	raw := settingStr("security.ip_blacklist", "")
	if raw == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, line := range strings.Split(raw, "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, netw, err := net.ParseCIDR(entry); err == nil && netw.Contains(parsed) {
				return true
			}
		} else if net.ParseIP(entry) != nil && net.ParseIP(entry).Equal(parsed) {
			return true
		}
	}
	return false
}

// IsIPBanned 检查 IP 是否被封禁（自动封禁记录或管理员手动黑名单）
func IsIPBanned(ip string) bool {
	if db.Get() == nil || ip == "" {
		return false
	}
	var ban models.IPBan
	if err := db.Get().Where("ip = ?", ip).First(&ban).Error; err == nil {
		if ban.ExpiresAt == nil || ban.ExpiresAt.After(time.Now()) {
			return true
		}
	}
	return isManuallyBanned(ip)
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
	exceeded := count >= loginMaxAttempts()
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
	exp := time.Now().Add(time.Duration(banHoursFromSetting()) * time.Hour)
	db.Get().Create(&models.IPBan{
		IP:        ip,
		Reason:    "too many login failures",
		ExpiresAt: &exp,
		Auto:      true,
	})
}
