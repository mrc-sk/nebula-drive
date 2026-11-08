package ai

import (
	"sync"
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// 文件访问埋点。异步写入 + 60 秒内同用户同文件同动作去重，
// 避免预览/下载这类高频路径被埋点拖慢，也避免表被刷爆。

type accessKey struct {
	userID uint
	fileID uint
	action string
}

var (
	accessMu   sync.Mutex
	accessLast = map[accessKey]time.Time{}
	accessN    int
)

// RecordAccess 记录一次文件访问。调用方无需等待，失败静默忽略（埋点不能影响主流程）。
func RecordAccess(userID, fileID uint, action string) {
	if userID == 0 || fileID == 0 {
		return
	}
	if action == "" {
		action = "open"
	}
	key := accessKey{userID, fileID, action}
	now := time.Now()
	accessMu.Lock()
	if t, ok := accessLast[key]; ok && now.Sub(t) < time.Minute {
		accessMu.Unlock()
		return
	}
	accessLast[key] = now
	accessN++
	needCleanup := accessN%200 == 0
	if len(accessLast) > 8192 {
		for k, v := range accessLast {
			if now.Sub(v) > 5*time.Minute {
				delete(accessLast, k)
			}
		}
	}
	accessMu.Unlock()

	go func() {
		d := db.Get()
		if d == nil {
			return
		}
		_ = d.Create(&models.FileAccess{
			UserID: userID, FileID: fileID, Action: action, CreatedAt: now,
		}).Error
		if needCleanup {
			// 只保留 90 天，避免埋点表无限增长。
			_ = d.Where("created_at < ?", now.AddDate(0, 0, -90)).
				Delete(&models.FileAccess{}).Error
		}
	}()
}

// AccessRow 一条聚合后的访问记录。
type AccessRow struct {
	FileID       uint      `json:"fileId" gorm:"column:file_id"`
	Count        int       `json:"count" gorm:"column:c"`
	LastAccessed time.Time `json:"lastAccessed" gorm:"column:last_at"`
}

// RecentAccess 按「最后访问时间」取最近打开的文件 ID。
func RecentAccess(userID uint, limit int) []AccessRow {
	return queryAccess(userID, limit, "MAX(created_at) DESC")
}

// HotAccess 按「访问次数」取最常用的文件 ID。
func HotAccess(userID uint, limit int) []AccessRow {
	return queryAccess(userID, limit, "c DESC, MAX(created_at) DESC")
}

func queryAccess(userID uint, limit int, order string) []AccessRow {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	d := db.Get()
	if d == nil {
		return []AccessRow{}
	}
	out := []AccessRow{}
	_ = d.Model(&models.FileAccess{}).
		Select("file_id, COUNT(*) AS c, MAX(created_at) AS last_at").
		Where("user_id = ?", userID).
		Group("file_id").Order(order).Limit(limit).Scan(&out).Error
	return out
}

// AccessScore 返回 fileId → 访问次数，供搜索结果智能排序加权。
func AccessScore(userID uint) map[uint]int {
	rows := queryAccess(userID, 500, "c DESC")
	out := make(map[uint]int, len(rows))
	for _, r := range rows {
		out[r.FileID] = r.Count
	}
	return out
}

// ClearAccess 清空某用户的访问埋点（隐私设置用）。
func ClearAccess(userID uint) {
	if d := db.Get(); d != nil {
		_ = d.Where("user_id = ?", userID).Delete(&models.FileAccess{}).Error
	}
	accessMu.Lock()
	for k := range accessLast {
		if k.userID == userID {
			delete(accessLast, k)
		}
	}
	accessMu.Unlock()
}
