package controllers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
)

// Search 全局文件搜索（10 维度）
// GET /api/files/search?keyword=xxx&type=doc&ext=pdf&owner=me&minSize=1024&maxSize=1048576
//   &after=2026-01-01&before=2026-12-31&tag=重要&trash=1&share=1&sort=size&order=desc&page=1&size=20
func Search(c *gin.Context) {
	u := middleware.CurrentUser(c)

	// 分页
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}

	q := db.Get().Model(&models.File{})

	// 维度 a: 文件名模糊匹配（大小写不敏感）
	if keyword := strings.TrimSpace(c.Query("keyword")); keyword != "" {
		// SQLite 用 LIKE 大小写不敏感（默认对 ASCII）；MySQL 用 LOWER
		like := "%" + keyword + "%"
		q = q.Where("name LIKE ?", like)
	}

	// 维度 c: 按扩展名/类型过滤
	if ext := c.Query("ext"); ext != "" {
		q = q.Where("extension = ?", strings.ToLower(strings.TrimPrefix(ext, ".")))
	}
	if ftype := c.Query("type"); ftype != "" {
		mimePatterns := typeToMimePatterns(ftype)
		if len(mimePatterns) > 0 {
			ors := make([]string, len(mimePatterns))
			args := make([]any, len(mimePatterns))
			for i, p := range mimePatterns {
				ors[i] = "mime_type LIKE ?"
				args[i] = p
			}
			q = q.Where(strings.Join(ors, " OR "), args...)
		}
	}

	// 维度 d: 按所有者筛选（admin 可查任意用户，普通用户只能查自己）
	if owner := c.Query("owner"); owner != "" {
		if owner == "me" || owner == strconv.FormatUint(uint64(u.ID), 10) {
			q = q.Where("owner_id = ?", u.ID)
		} else if u.IsAdmin {
			if oid, err := strconv.ParseUint(owner, 10, 64); err == nil {
				q = q.Where("owner_id = ?", uint(oid))
			}
		} else {
			q = q.Where("owner_id = ?", u.ID)
		}
	} else {
		// 默认只搜自己的
		q = q.Where("owner_id = ?", u.ID)
	}

	// 维度 e: 按大小区间筛选
	if minSize := c.Query("minSize"); minSize != "" {
		if ms, err := strconv.ParseInt(minSize, 10, 64); err == nil {
			q = q.Where("size >= ?", ms)
		}
	}
	if maxSize := c.Query("maxSize"); maxSize != "" {
		if ms, err := strconv.ParseInt(maxSize, 10, 64); err == nil {
			q = q.Where("size <= ?", ms)
		}
	}

	// 维度 f: 按修改时间区间筛选
	if after := c.Query("after"); after != "" {
		if t, err := time.Parse("2006-01-02", after); err == nil {
			q = q.Where("updated_at >= ?", t)
		}
	}
	if before := c.Query("before"); before != "" {
		if t, err := time.Parse("2006-01-02", before); err == nil {
			q = q.Where("updated_at <= ?", t.Add(24*time.Hour))
		}
	}

	// 维度 g: 按标签筛选（可多标签逗号分隔，OR 逻辑）
	if tagParam := c.Query("tag"); tagParam != "" {
		tags := strings.Split(tagParam, ",")
		q = q.Joins("JOIN file_tags ON file_tags.file_id = files.id").
			Where("file_tags.tag IN ? AND file_tags.owner_id ?", tags, u.ID)
	}

	// 维度 h: 只搜回收站
	if c.Query("trash") == "1" {
		q = q.Unscoped().Where("deleted_at IS NOT NULL")
	} else {
		q = q.Where("deleted_at IS NULL")
	}

	// 维度 i: 只搜分享项
	if c.Query("share") == "1" {
		q = q.Joins("JOIN shares ON shares.file_id = files.id").
			Where("shares.deleted_at IS NULL")
	}

	// 排除目录（搜索文件时通常不搜目录）
	if c.Query("includeDir") != "1" {
		q = q.Where("is_dir = ?", false)
	}

	// 排序
	sortBy := c.DefaultQuery("sort", "updated_at")
	sortOrder := c.DefaultQuery("order", "desc")
	allowedSort := map[string]bool{
		"name": true, "size": true, "updated_at": true, "created_at": true,
	}
	if !allowedSort[sortBy] {
		sortBy = "updated_at"
	}
	if sortOrder != "asc" {
		sortOrder = "desc"
	}
	q = q.Order(sortBy + " " + sortOrder)

	// 总数
	var total int64
	q.Count(&total)

	// 查询
	var files []models.File
	q.Offset((page - 1) * size).Limit(size).Find(&files)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total": total,
			"list":  files,
			"page":  page,
			"size":  size,
		},
	})
}

// typeToMimePatterns 将类型分类转为 MIME LIKE 模式
func typeToMimePatterns(ftype string) []string {
	switch strings.ToLower(ftype) {
	case "doc", "document":
		return []string{"text/%", "application/pdf", "application/msword",
			"application/vnd.openxmlformats%", "application/vnd.ms-%"}
	case "img", "image":
		return []string{"image/%"}
	case "video":
		return []string{"video/%"}
	case "audio":
		return []string{"audio/%"}
	case "archive", "zip":
		return []string{"application/zip", "application/x-rar%", "application/x-7z%",
			"application/x-tar", "application/gzip"}
	case "code":
		return []string{"text/javascript", "text/x-go", "text/x-python",
			"text/x-java", "text/x-c%", "application/json", "text/yaml",
			"text/xml", "text/css", "text/html"}
	default:
		return nil
	}
}

// ---- 搜索历史 ----

// ListSearchHistory 用户搜索历史（最近 20 条）
func ListSearchHistory(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var history []models.SearchHistory
	db.Get().Where("user_id = ?", u.ID).Order("id desc").Limit(20).Find(&history)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": history})
}

// SaveSearchHistory 保存搜索历史
func SaveSearchHistory(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req struct {
		Keyword string `json:"keyword"`
		Filters string `json:"filters"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if strings.TrimSpace(req.Keyword) == "" {
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	db.Get().Create(&models.SearchHistory{UserID: u.ID, Keyword: req.Keyword, Filters: req.Filters})
	// 只保留最近 20 条
	db.Get().Where("user_id = ? AND id NOT IN (SELECT id FROM search_histories WHERE user_id = ? ORDER BY id DESC LIMIT 20)",
		u.ID, u.ID).Delete(&models.SearchHistory{})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ClearSearchHistory 清空搜索历史
func ClearSearchHistory(c *gin.Context) {
	u := middleware.CurrentUser(c)
	db.Get().Where("user_id = ?", u.ID).Delete(&models.SearchHistory{})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 标签增强 ----

// UpdateTag 更新标签（改名/改色）
func UpdateTag(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req struct {
		OldTag  string `json:"oldTag" binding:"required"`
		NewTag  string `json:"newTag"`
		NewColor string `json:"newColor"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	updates := map[string]any{}
	if req.NewTag != "" {
		updates["tag"] = req.NewTag
	}
	if req.NewColor != "" {
		updates["color"] = req.NewColor
	}
	if len(updates) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	db.Get().Model(&models.FileTag{}).
		Where("tag = ? AND owner_id = ?", req.OldTag, u.ID).
		Updates(updates)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ListFileTags 列出指定文件的所有标签
func ListFileTags(c *gin.Context) {
	u := middleware.CurrentUser(c)
	fileID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var tags []models.FileTag
	db.Get().Where("file_id = ? AND owner_id = ?", uint(fileID), u.ID).Find(&tags)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": tags})
}

// ownFileByID 检查用户是否拥有指定文件（或管理员）
func ownFileByID(u *models.User, fileID uint) bool {
	if u == nil {
		return false
	}
	if u.IsAdmin {
		return true
	}
	var f models.File
	if db.Get().Select("owner_id").First(&f, fileID).Error != nil {
		return false
	}
	return f.OwnerID == u.ID
}

// BatchAddTag 批量加标签
func BatchAddTag(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req struct {
		FileIDs []uint `json:"fileIds" binding:"required"`
		Tag     string `json:"tag" binding:"required"`
		Color   string `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	added := 0
	for _, fid := range req.FileIDs {
		if !ownFileByID(u, fid) {
			continue
		}
		// 去重：同一文件同一标签不重复
		var cnt int64
		db.Get().Model(&models.FileTag{}).Where("file_id = ? AND owner_id = ? AND tag = ?", fid, u.ID, req.Tag).Count(&cnt)
		if cnt > 0 {
			continue
		}
		db.Get().Create(&models.FileTag{FileID: fid, OwnerID: u.ID, Tag: req.Tag, Color: req.Color})
		added++
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"added": added}})
}

// BatchRename 批量重命名（编号模式：前缀_001, 前缀_002, ...）
func BatchRename(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req struct {
		FileIDs []uint `json:"fileIds" binding:"required"`
		Prefix  string `json:"prefix" binding:"required"`
		Start   int    `json:"start"`
		Padding int    `json:"padding"` // 编号位数，默认 3
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.Padding < 1 {
		req.Padding = 3
	}
	renamed := 0
	for i, fid := range req.FileIDs {
		if !ownFileByID(u, fid) {
			continue
		}
		var f models.File
		if db.Get().First(&f, fid).Error != nil {
			continue
		}
		num := req.Start + i
		newName := fmt.Sprintf("%s_%0*d", req.Prefix, req.Padding, num)
		if f.Extension != "" {
			newName += "." + f.Extension
		}
		db.Get().Model(&f).Update("name", newName)
		renamed++
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"renamed": renamed}})
}
