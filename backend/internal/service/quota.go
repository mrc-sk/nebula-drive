package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/nebula-drive/nebula/models"
	"gorm.io/gorm"
)

// ---- 配额：单一实现，HTTP / WebDAV / 协作保存全部复用 ----

// QuotaProvider 由调用方注入解析用户有效配额的能力（避免 service 依赖 controllers）。
// 返回 (maxStorage, label)，maxStorage == -1 表示无限。
type QuotaProvider func(tx *gorm.DB, userID uint) (int64, string)

var quotaProvider QuotaProvider

// SetQuotaProvider 注入配额解析实现
func SetQuotaProvider(p QuotaProvider) { quotaProvider = p }

// defaultQuota 未注入时的兜底：直接按用户组额度算
func defaultQuota(tx *gorm.DB, userID uint) (int64, string) {
	var u models.User
	if err := tx.Select("group_id").First(&u, userID).Error; err != nil {
		return -1, "unknown"
	}
	var g models.Group
	if err := tx.First(&g, u.GroupID).Error; err != nil {
		return -1, "group:missing"
	}
	return g.MaxStorage, "group:" + g.Name
}

// resolveQuota 取用户的 (额度, 来源标签)
func resolveQuota(tx *gorm.DB, userID uint) (int64, string) {
	if quotaProvider != nil {
		return quotaProvider(tx, userID)
	}
	return defaultQuota(tx, userID)
}

// ErrQuotaExceeded 超出存储配额
var ErrQuotaExceeded = errors.New("quota exceeded")

// ReserveQuota 原子地为 userID 预留 delta 字节额度。
//
// 与旧的「先读 storage → 比较 → 再写」不同，这里把校验与写入合并为一条条件 UPDATE，
// 由数据库保证原子性，从而消除 TOCTOU 竞态（并发多请求同时通过校验导致超额）。
//
// delta 为负数时表示释放额度，此时不做上限校验（永远允许）。
// 返回错误时表示额度不足，调用方不应继续写入。
func ReserveQuota(tx *gorm.DB, userID uint, delta int64) error {
	if delta <= 0 {
		// 释放或零变更：直接执行，负数不会造成超额
		if delta == 0 {
			return nil
		}
		return tx.Model(&models.User{}).Where("id = ?", userID).
			UpdateColumn("storage", gorm.Expr("storage + ?", delta)).Error
	}
	maxStorage, label := resolveQuota(tx, userID)
	if maxStorage == -1 {
		// 无限额度：直接累加
		return tx.Model(&models.User{}).Where("id = ?", userID).
			UpdateColumn("storage", gorm.Expr("storage + ?", delta)).Error
	}
	res := tx.Model(&models.User{}).
		Where("id = ? AND (storage + ?) <= ?", userID, delta, maxStorage).
		UpdateColumn("storage", gorm.Expr("storage + ?", delta))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// 区分「条件不满足」与「用户不存在」
		var u models.User
		if err := tx.Select("storage").First(&u, userID).Error; err != nil {
			return err
		}
		return fmt.Errorf("%w: have %d bytes, limit %d bytes (%s), tried to add %d bytes",
			ErrQuotaExceeded, u.Storage, maxStorage, label, delta)
	}
	return nil
}

// CheckQuota 只做校验不写入，用于"预检"场景（如上传前先挡住明显超额的请求）。
// 注意：预检不能替代 ReserveQuota，真正的额度占用必须走 ReserveQuota。
func CheckQuota(tx *gorm.DB, userID uint, additionalBytes int64) error {
	if additionalBytes <= 0 {
		return nil
	}
	maxStorage, label := resolveQuota(tx, userID)
	if maxStorage == -1 {
		return nil
	}
	var u models.User
	if err := tx.Select("storage").First(&u, userID).Error; err != nil {
		return err
	}
	if u.Storage+additionalBytes > maxStorage {
		return fmt.Errorf("%w: have %d bytes, limit %d bytes (%s), tried to add %d bytes",
			ErrQuotaExceeded, u.Storage, maxStorage, label, additionalBytes)
	}
	return nil
}

// GroupOf 取用户组（缺失时返回宽松的兜底组）
func GroupOf(tx *gorm.DB, userID uint) models.Group {
	var u models.User
	if tx.Select("group_id").First(&u, userID).Error != nil {
		return models.Group{ID: 0, Name: "fallback", MaxStorage: -1, ShareEnabled: true, WebDAVEnabled: true}
	}
	var g models.Group
	if tx.First(&g, u.GroupID).Error == nil {
		return g
	}
	return models.Group{ID: 1, Name: "default", MaxStorage: -1, ShareEnabled: true, WebDAVEnabled: true}
}

// MaxStorageOf 便捷取用户有效额度（含套餐）
func MaxStorageOf(tx *gorm.DB, userID uint) (int64, string) {
	return resolveQuota(tx, userID)
}

// PlanActive 判断用户当前是否有活跃套餐
func PlanActive(u *models.User) bool {
	return u.PlanID > 0 && u.PlanExpireAt != nil && u.PlanExpireAt.After(time.Now())
}

// AddStorageTx 在配额约束下原子增减用户已用容量。
// delta > 0：受额度上限约束，超额返回 ErrQuotaExceeded；
// delta <= 0：直接释放，不做上限校验。
// 这是 addStorage 的安全替代品，用于所有「写入完成后记账」的位置。
func AddStorageTx(tx *gorm.DB, userID uint, delta int64) error {
	if delta == 0 {
		return nil
	}
	if delta < 0 {
		return tx.Model(&models.User{}).Where("id = ?", userID).
			UpdateColumn("storage", gorm.Expr("storage + ?", delta)).Error
	}
	maxStorage, label := resolveQuota(tx, userID)
	if maxStorage == -1 {
		return tx.Model(&models.User{}).Where("id = ?", userID).
			UpdateColumn("storage", gorm.Expr("storage + ?", delta)).Error
	}
	res := tx.Model(&models.User{}).
		Where("id = ? AND (storage + ?) <= ?", userID, delta, maxStorage).
		UpdateColumn("storage", gorm.Expr("storage + ?", delta))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var u models.User
		if err := tx.Select("storage").First(&u, userID).Error; err != nil {
			return err
		}
		return fmt.Errorf("%w: have %d bytes, limit %d bytes (%s), tried to add %d bytes",
			ErrQuotaExceeded, u.Storage, maxStorage, label, delta)
	}
	return nil
}
