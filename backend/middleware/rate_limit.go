package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

const rateLimitWindow = time.Minute

// 滑动窗口：key -> 时间戳列表
type slidingWindowLimiter struct {
	mu      sync.Mutex
	records map[string][]time.Time
}

var limiter = &slidingWindowLimiter{records: map[string][]time.Time{}}

// allow 判断 key 在窗口内是否允许第 limit 次请求
func (l *slidingWindowLimiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)
	ts := l.records[key]
	kept := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.records[key] = kept
		return false
	}
	kept = append(kept, now)
	l.records[key] = kept
	return true
}

// settingInt 从 settings 表读取整数配置，缺失或非法时返回默认值
func settingInt(key string, def int) int {
	if db.Get() == nil {
		return def
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s.Value))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// limitForCategory 根据 category 返回每分钟限额
func limitForCategory(category string) int {
	switch category {
	case "login":
		return settingInt("rate_limit.login_per_min", 10)
	case "upload":
		return settingInt("rate_limit.upload_per_min", 60)
	default:
		return settingInt("rate_limit.default_per_min", 600)
	}
}

// RateLimit 速率限制中间件。category: login|upload|default。
//   - 先触发 plugin.HookRateLimit，若返回含 "blocked" 的 error 则 403
//   - 再做内存滑动窗口限流，超限返回 429
func RateLimit(category string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		var key string
		if u != nil {
			key = category + ":u:" + strconv.Itoa(int(u.ID))
		} else {
			key = category + ":ip:" + c.ClientIP()
		}

		ctx := map[string]any{
			"category": category,
			"ip":       c.ClientIP(),
			"ua":       c.Request.UserAgent(),
			"path":     c.Request.URL.Path,
		}
		if u != nil {
			ctx["userId"] = u.ID
			ctx["userName"] = u.UserName
		}
		if errs := plugin.Fire(plugin.HookRateLimit, ctx); len(errs) > 0 {
			for _, e := range errs {
				if strings.Contains(e.Error(), "blocked") {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "message": "blocked by rate limit plugin"})
					return
				}
			}
		}

		limit := limitForCategory(category)
		if !limiter.allow(key, limit) {
			c.Header("Retry-After", strconv.Itoa(int(rateLimitWindow.Seconds())))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "too many requests",
				"limit":   limit,
				"window":  "60s",
			})
			return
		}
		c.Next()
	}
}
