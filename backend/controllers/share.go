package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

type shareReq struct {
	FileID      uint       `json:"fileId" binding:"required"`
	Password    string     `json:"password"`
	ExtractCode string     `json:"extractCode"`
	ExpireAt    *time.Time `json:"expireAt"`
}

type shareAccessReq struct {
	Password    string `json:"password"`
	ExtractCode string `json:"extractCode"`
}

func readShareAccessParams(c *gin.Context) (id int, pwd, extract string) {
	id, _ = strconv.Atoi(c.Param("id"))
	// Query params（GET）优先
	pwd = c.Query("password")
	extract = c.Query("extract")
	// Header 补充
	if pwd == "" {
		pwd = c.GetHeader("X-Share-Pwd")
	}
	if extract == "" {
		extract = c.GetHeader("X-Share-Extract")
	}
	// POST JSON body（兼容前端 POST /api/shares/:id）
	if c.Request.Method == http.MethodPost {
		var body shareAccessReq
		if raw, err := c.GetRawData(); err == nil && len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
			if pwd == "" {
				pwd = body.Password
			}
			if extract == "" {
				extract = body.ExtractCode
			}
			// GetRawData 会消费 Body，回填供后续读取（目前此 Controller 不再读 body，故无需）
		}
	}
	return
}

// CreateShare 创建分享
func CreateShare(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req shareReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	var f models.File
	if err := db.Get().First(&f, req.FileID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "文件不存在"})
		return
	}
	if !u.IsAdmin && f.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "无权操作"})
		return
	}
	// 组开关 ShareEnabled 生效：非管理员禁用时无法创建分享
	if !u.IsAdmin {
		var g models.Group
		if err := db.Get().First(&g, u.GroupID).Error; err == nil && !g.ShareEnabled {
			c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "所属用户组已禁用分享功能"})
			return
		}
	}
	s := models.Share{
		FileID: req.FileID, OwnerID: u.ID, Password: req.Password,
		ExtractCode: req.ExtractCode, ExpireAt: req.ExpireAt, IsDir: f.IsDir,
	}
	db.Get().Create(&s)

	ctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"shareId":  s.ID,
		"fileId":   f.ID,
		"fileName": f.Name,
	}
	plugin.Fire(plugin.HookRateLimit, ctx)

	db.Get().Create(&models.AuditLog{
		UserID:   u.ID,
		UserName: u.UserName,
		Action:   "share_create",
		Target:   f.Name,
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   "shareId=" + strconv.FormatUint(uint64(s.ID), 10),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": s})
}

// GetShare 获取分享（公开，校验密码/过期/提取码）。同时根据 file 目录或单文件构造 meta + tree 返回前端，避免前端 404 兜底假数据。
func GetShare(c *gin.Context) {
	id, pwd, extract := readShareAccessParams(c)
	if pwd == "" {
		pwd = c.Query("password")
	}
	if pwd == "" {
		pwd = c.GetHeader("X-Share-Pwd")
	}
	if extract == "" {
		extract = c.Query("extract")
	}
	if extract == "" {
		extract = c.GetHeader("X-Share-Extract")
	}

	var s models.Share
	if err := db.Get().First(&s, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "分享不存在"})
		return
	}
	if s.ExpireAt != nil && s.ExpireAt.Before(time.Now()) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "分享已过期"})
		return
	}
	if s.Password != "" && s.Password != pwd {
		c.JSON(http.StatusOK, gin.H{"code": 2, "message": "需要密码", "requirePwd": true})
		return
	}
	if s.ExtractCode != "" && s.ExtractCode != extract {
		c.JSON(http.StatusOK, gin.H{"code": 3, "message": "需要提取码", "requireExtractCode": true})
		return
	}
	db.Get().Model(&s).UpdateColumn("views", s.Views+1)
	CreateNotification(s.OwnerID, "分享被访问", "你的分享被访问", "info", "share")
	var f models.File
	db.Get().First(&f, s.FileID)

	// 生成 meta + tree：与前端期望字段对齐，不再让前端 catch 后渲染假数据
	meta := gin.H{
		"title":         f.Name,
		"expireAt":      expireOrNil(s.ExpireAt),
		"viewTimes":     s.Views,
		"downloadTimes": s.Downloads,
		"size":          totalSizeOfShare(&s, &f),
		"share":         s,
		"file":          f,
	}
	tree := buildShareTree(&f)

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"meta": meta,
		"tree": tree,
		"share": s,
		"file":  f,
	}})
}

// buildShareTree：把分享根（单文件或目录）递归转成前端 ShareNode 数组
func buildShareTree(root *models.File) []gin.H {
	if !root.IsDir {
		return []gin.H{{
			"id": root.ID, "name": root.Name, "type": "file",
			"size": root.Size, "mime": root.MimeType,
		}}
	}
	// 目录：取一级子项 + 子目录递归 2 层（避免大量数据）
	children := listChildrenLimited(root.OwnerID, &root.ID, 2)
	return children
}

// listChildrenLimited 递归列出子项，最多 depth 层
func listChildrenLimited(ownerID uint, parentID *uint, depth int) []gin.H {
	if depth <= 0 {
		return nil
	}
	var files []models.File
	q := db.Get().Where("owner_id = ? AND deleted_at IS NULL", ownerID)
	if parentID == nil {
		q = q.Where("parent_id IS NULL")
	} else {
		q = q.Where("parent_id = ?", *parentID)
	}
	q.Order("is_dir desc, name asc").Find(&files)
	out := make([]gin.H, 0, len(files))
	for _, f := range files {
		node := gin.H{
			"id": f.ID, "name": f.Name,
			"type": func() string { if f.IsDir { return "dir" } else { return "file" } }(),
			"size": f.Size, "mime": f.MimeType,
		}
		if f.IsDir {
			node["children"] = listChildrenLimited(ownerID, &f.ID, depth-1)
		}
		out = append(out, node)
	}
	return out
}

// totalSizeOfShare：根是单文件就 f.Size；是目录则粗略算一级子文件（避免 O(N) 深递归）
func totalSizeOfShare(s *models.Share, f *models.File) int64 {
	if !f.IsDir {
		return f.Size
	}
	var total int64
	db.Get().Model(&models.File{}).
		Where("owner_id = ? AND parent_id = ? AND deleted_at IS NULL AND is_dir = ?", f.OwnerID, f.ID, false).
		Select("COALESCE(SUM(size),0)").Row().Scan(&total)
	return total
}

// expireOrNil：前端需要 ISO 字符串或 null（map 里零值会出错），转成 string ptr
func expireOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

// ListShares 我的分享列表
func ListShares(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var shares []models.Share
	db.Get().Where("owner_id = ?", u.ID).Order("id desc").Find(&shares)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": shares})
}

// DeleteShare 删除分享
func DeleteShare(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	var s models.Share
	if err := db.Get().First(&s, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "分享不存在"})
		return
	}
	if !u.IsAdmin && s.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "无权操作"})
		return
	}
	db.Get().Delete(&s)

	ctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"shareId":  id,
	}
	plugin.Fire(plugin.HookRateLimit, ctx)

	db.Get().Create(&models.AuditLog{
		UserID:   u.ID,
		UserName: u.UserName,
		Action:   "share_delete",
		Target:   strconv.Itoa(id),
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0})
}
