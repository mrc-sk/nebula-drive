package storage

import (
	"errors"

	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/gorm"
)

// RetainObject 创建或增加物理文件对象的引用计数。
// - 若 (PolicyID, SourceName) 已存在：refs+=1，不变更 hash/size。
// - 否则插入新记录 refs=1，使用传入的 size/hash。
// 秒传/上传成功/复制文件/版本归档后都应调用一次。
func RetainObject(policyID uint, sourceName string, size int64, hash string) error {
	if policyID == 0 || sourceName == "" {
		return errors.New("invalid policyID/sourceName")
	}
	var obj models.FileObject
	err := db.Get().Where("policy_id = ? AND source_name = ?", policyID, sourceName).First(&obj).Error
	if err == nil {
		return db.Get().Model(&obj).UpdateColumn("refs", gorm.Expr("refs + ?", 1)).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	obj = models.FileObject{
		PolicyID:   policyID,
		SourceName: sourceName,
		Size:       size,
		Hash:       hash,
		Refs:       1,
	}
	return db.Get().Create(&obj).Error
}

// ReleaseObject 释放物理文件对象引用：refs 减 1。若 refs 变为 0，
// 通过传入的 handler 删除对应物理文件，并删除 FileObject 行。
// Purge、批量彻底删除、WebDAV 删除、版本删除后都应调用。
// 返回 shouldHaveDeleted=true 仅表示按引用计数逻辑「应已」删了物理文件；
// 物理文件删除失败返回 non-nil error，但 FileObject 行不会被删（以便下次重试）。
func ReleaseObject(policyID uint, sourceName string, h filesystem.Handler) (bool, error) {
	if policyID == 0 || sourceName == "" {
		return false, nil
	}
	var obj models.FileObject
	err := db.Get().Where("policy_id = ? AND source_name = ?", policyID, sourceName).First(&obj).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 旧数据（V1-0.0.1 写入的），没有 FileObject，兜底仍删物理
		if h != nil {
			_ = h.Delete(sourceName)
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	newRefs := obj.Refs - 1
	if newRefs > 0 {
		return false, db.Get().Model(&obj).UpdateColumn("refs", newRefs).Error
	}
	// refs == 0：先删物理，成功后删 FileObject 行
	if h != nil {
		if err := h.Delete(sourceName); err != nil {
			return false, err
		}
	}
	return true, db.Get().Delete(&obj).Error
}
