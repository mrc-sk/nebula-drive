package storage

import (
	"errors"
	"fmt"
	"log"

	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/gorm"
)

// Retain 等价于 RetainObject，但失败会 log 错误。适合"文件已经落盘，引用计数失败只能降级兜底"的调用方。
// 此时 ReleaseObject 会通过 files 表计数的安全分支兜底，不会误删物理文件。
func Retain(policyID uint, sourceName string, size int64, hash string, action string) {
	if err := RetainObject(policyID, sourceName, size, hash); err != nil {
		log.Printf("[refcnt] RetainObject failed (%s policy=%d src=%s size=%d): %v",
			action, policyID, sourceName, size, err)
	}
}

// RetainObject 原子性地创建或增加物理文件对象引用计数（UPSERT 风格）。
// - 若 (PolicyID, SourceName) 已存在：refs += 1（唯一键冲突后 UPDATE，原子无竞态）
// - 否则插入新记录 refs = 1，使用传入的 size/hash
// 秒传/上传成功/复制文件/版本归档/WebDAV PUT 后都应调用一次。
func RetainObject(policyID uint, sourceName string, size int64, hash string) error {
	if policyID == 0 || sourceName == "" {
		return errors.New("invalid policyID/sourceName")
	}
	d := db.Get()
	// 1) 先尝试原子自增：若记录存在，则 refs+1（WHERE 命中即原子修改，无并发先查后改问题）
	tx := d.Model(&models.FileObject{}).
		Where("policy_id = ? AND source_name = ?", policyID, sourceName).
		UpdateColumn("refs", gorm.Expr("refs + ?", 1))
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected > 0 {
		return nil
	}
	// 2) RowsAffected=0：记录不存在，尝试 INSERT。并发下 INSERT 可能撞唯一索引 → 再 UPDATE 一次即可。
	obj := models.FileObject{
		PolicyID:   policyID,
		SourceName: sourceName,
		Size:       size,
		Hash:       hash,
		Refs:       1,
	}
	if err := d.Create(&obj).Error; err == nil {
		return nil
	}
	// 唯一键冲突：并发情况下另一个请求先创建了 → 安全 refs+1 兜底。
	tx2 := d.Model(&models.FileObject{}).
		Where("policy_id = ? AND source_name = ?", policyID, sourceName).
		UpdateColumn("refs", gorm.Expr("refs + ?", 1))
	if tx2.Error != nil {
		return fmt.Errorf("retain upsert fallback failed: %w", tx2.Error)
	}
	if tx2.RowsAffected == 0 {
		return errors.New("retain object failed: no row inserted or updated (race?)")
	}
	return nil
}

// ReleaseObject 释放物理文件对象引用：refs 减 1。若 refs 变为 0，
// 通过传入的 handler 删除对应物理文件，并删除 FileObject 行。
// Purge、批量彻底删除、WebDAV 删除、版本删除后都应调用。
//
// 「升级路径安全」——V1-0.0.1 秒传/复制遗留的文件没有 FileObject 记录。
// 对这类旧数据，我们不再兜底直接删物理，而是先到 files 表查引用，
// 只有确认「没有其他 File 行仍引用同一 (policy_id, source_name)」时才真正删除物理文件，
// 避免老用户升级后第一次删除副本就破坏其他副本，导致下载 500。
func ReleaseObject(policyID uint, sourceName string, h filesystem.Handler) (bool, error) {
	if policyID == 0 || sourceName == "" {
		return false, nil
	}
	d := db.Get()
	var obj models.FileObject
	err := d.Where("policy_id = ? AND source_name = ?", policyID, sourceName).First(&obj).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// ---- 旧数据兜底：以 files 表引用为准，安全删除 ----
		return releaseLegacy(policyID, sourceName, h)
	}

	newRefs := obj.Refs - 1
	if newRefs > 0 {
		return false, d.Model(&obj).UpdateColumn("refs", newRefs).Error
	}
	// refs == 0：先删物理，成功后删 FileObject 行
	if h != nil {
		if err := h.Delete(sourceName); err != nil {
			return false, err
		}
	}
	return true, d.Delete(&obj).Error
}

// releaseLegacy 处理无 FileObject 记录（V1-0.0.1 遗留或未初始化）的 (policy, source)：
// 按 files 表统计引用，只有引用数 <= 1 才真正删除物理文件；
// 为避免下一次再走同样分支，会在 safe 场景下回填一条 FileObject(refs = count-1，因为当前是正在释放一份)。
func releaseLegacy(policyID uint, sourceName string, h filesystem.Handler) (bool, error) {
	d := db.Get()
	var refs int64
	// 计算所有「非软删除」的 File 行引用同一物理的数量
	if err := d.Model(&models.File{}).
		Where("policy_id = ? AND source_name = ? AND deleted_at IS NULL AND is_dir = ? AND source_name <> ?",
			policyID, sourceName, false, "").
		Count(&refs).Error; err != nil {
		// 查不到引用计数就保守不删，避免误删
		log.Printf("[refcnt] legacy refs query failed, skip delete to be safe (policy=%d src=%s): %v",
			policyID, sourceName, err)
		return false, err
	}

	// 若 files 仍有多份引用（refs >= 2），绝对不删物理。回填 FileObject.refs = refs-1（因为当前这份被释放）
	if refs >= 2 {
		remain := refs - 1
		var cnt int64
		_ = d.Model(&models.FileObject{}).
			Where("policy_id = ? AND source_name = ?", policyID, sourceName).
			Count(&cnt).Error
		if cnt == 0 {
			_ = d.Create(&models.FileObject{
				PolicyID:   policyID,
				SourceName: sourceName,
				Size:       0,
				Hash:       "",
				Refs:       remain,
			}).Error
		}
		return false, nil
	}

	// refs <= 1：当前释放后只剩 0（真的是最后一份引用），安全删物理
	if h != nil {
		_ = h.Delete(sourceName)
	}
	return true, nil
}

// BackfillFileObjects 启动时一次性扫描 files 表，把遗留的 (policy_id, source_name)
// 回填到 file_objects 表，避免之后删除时进入 releaseLegacy。
// - 幂等：UNIQUE 冲突时跳过（不会覆盖现有的正确 refs）
// - 不阻塞启动：错误仅 log，不 fatal
func BackfillFileObjects() {
	d := db.Get()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[refcnt] backfill panicked: %v", r)
		}
	}()

	// SQLite / MySQL / PG 通用：用子查询聚合后 NOT EXISTS 冲突判定再 INSERT（UNIQUE 冲突自动丢弃）
	type agg struct {
		PolicyID   uint
		SourceName string
		Refs       int64
		Size       int64
	}
	var rows []agg
	err := d.Model(&models.File{}).
		Select("policy_id, source_name, COUNT(*) AS refs, MAX(size) AS size").
		Where("source_name <> '' AND is_dir = ? AND deleted_at IS NULL", false).
		Group("policy_id, source_name").
		Scan(&rows).Error
	if err != nil {
		log.Printf("[refcnt] backfill aggregate skipped: %v", err)
		return
	}

	inserted := 0
	for _, r := range rows {
		var cnt int64
		_ = d.Model(&models.FileObject{}).
			Where("policy_id = ? AND source_name = ?", r.PolicyID, r.SourceName).
			Count(&cnt).Error
		if cnt > 0 {
			continue // 已经回填/初始化过的，不动现有 refs（避免重置回错误值）
		}
		if d.Create(&models.FileObject{
			PolicyID:   r.PolicyID,
			SourceName: r.SourceName,
			Size:       r.Size,
			Hash:       "",
			Refs:       r.Refs,
		}).Error == nil {
			inserted++
		}
	}
	if inserted > 0 {
		log.Printf("[refcnt] backfill done: inserted %d new file_object rows for legacy files", inserted)
	}
}
