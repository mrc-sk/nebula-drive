package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"gorm.io/gorm"
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
	// 自增 views 后必须把新值读回来：下面 meta.viewTimes 直接读 s.Views，
	// 不回读的话第一次访问显示 0、第二次仍显示 1……永远慢一拍。
	//
	// 用 gorm.Expr 让数据库侧自增（并发访问不会互相覆盖），
	// 再单独回读 views 列 —— 不能再用 s.Views++ 补：
	// UpdateColumn 其实已经把新值写回内存结构体了，再 ++ 一次就变成二次自增。
	db.Get().Model(&models.Share{}).Where("id = ?", s.ID).
		UpdateColumn("views", gorm.Expr("views + ?", 1))
	db.Get().Model(&s).Select("views").First(&s)
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
		"meta":  meta,
		"tree":  tree,
		"share": s,
		"file":  f,
	}})
}

// DownloadShare 通过分享链接下载内容（公开，无需登录）。
//
// 可选 query fileId —— 下载分享目录内的某个子文件；不给则下载分享根本身。
func DownloadShare(c *gin.Context) {
	serveShareFile(c, dispAttachment)
}

// PreviewShare 通过分享链接内联预览内容（公开，无需登录）。
//
// 与 DownloadShare 的区别有两处，且都是必须的：
//  1. 用 Content-Disposition: inline，否则 <img src> 只会触发下载框，预览是空的；
//  2. 不自增 downloads、不记 share_download 审计 —— 预览不是下载。
//     两者混在一起会让所有者看到的下载量虚高，也不好排查。
func PreviewShare(c *gin.Context) {
	serveShareFile(c, dispInline)
}

// serveShareFile 是分享下载/预览的共享实现。disp 决定响应的
// Content-Disposition，同时也决定要不要计入下载次数。
//
// 安全边界（这里是本函数最要紧的部分）：
//
//	fileId 是客户端传的，不能只信它。否则任何人都能拿一个有效分享 ID
//	构造 ?fileId=<任意文件ID>，把受害者账号下的任意文件拖走 ——
//	分享链接会变成一个越权读取入口。
//
//	所以必须逐级向上验证 fileId 的祖先链确实通向分享根：
//	每一层都要满足 owner_id == 分享所有者 && 未删除，最后落到分享根 ID。
//	只查一层是不够的（孙目录/更深层同样能被构造出来）。
func serveShareFile(c *gin.Context, disp string) {
	shareID, pwd, extract := readShareAccessParams(c)

	var s models.Share
	if err := db.Get().First(&s, shareID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "分享不存在"})
		return
	}
	if s.ExpireAt != nil && s.ExpireAt.Before(time.Now()) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "分享已过期"})
		return
	}
	if s.Password != "" && s.Password != pwd {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2, "message": "需要密码", "requirePwd": true})
		return
	}
	if s.ExtractCode != "" && s.ExtractCode != extract {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 3, "message": "需要提取码", "requireExtractCode": true})
		return
	}

	var root models.File
	if err := db.Get().First(&root, s.FileID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "分享的文件已不存在"})
		return
	}

	// 定位目标文件：不给 fileId 就是分享根本身
	target := root
	if fid := c.Query("fileId"); fid != "" {
		fileID, err := strconv.Atoi(fid)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "fileId 非法"})
			return
		}
		var f models.File
		if err := db.Get().First(&f, fileID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "文件不存在"})
			return
		}
		if !fileWithinShare(&f, &root) {
			// 不区分「不存在」与「越权」：告诉攻击者哪个 ID 存在会泄露信息
			c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "文件不存在"})
			return
		}
		target = f
	}

	if target.IsDir {
		// 目录不能当文件流返回。浏览器无法把多文件打包成一个响应，
		// 与其在这里悄悄返回目录列表（前端会当成文件下载出错），
		// 不如明确告诉前端「这个节点不可下载」。
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "目录不可下载，请选择具体文件"})
		return
	}

	h, err := handlerForFile(&target)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	// 下载次数在这里自增 —— 这是全项目唯一该写这个字段的地方。
	// 用 gorm.Expr 让数据库侧自增，避免并发下载时互相覆盖。
	// 分享所有者记审计日志（访问者是匿名的，没有 userID 可记）。
	if disp == dispAttachment {
		db.Get().Model(&models.Share{}).Where("id = ?", s.ID).
			UpdateColumn("downloads", gorm.Expr("downloads + ?", 1))
		db.Get().Create(&models.AuditLog{
			UserID:   s.OwnerID,
			UserName: "share-guest",
			Action:   "share_download",
			Target:   target.Name,
			IP:       c.ClientIP(),
			UA:       c.Request.UserAgent(),
			Detail:   fmt.Sprintf("shareId=%d,fileId=%d,size=%d", s.ID, target.ID, target.Size),
		})
	}
	plugin.Fire(plugin.HookRateLimit, map[string]any{
		"userId":   s.OwnerID,
		"userName": "",
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"shareId":  s.ID,
	})

	// 预览走 inline，且强制 no-store：内容是受密码/提取码保护的，
	// 留在浏览器缓存里等于绕过校验就能再取一次。
	if disp == dispInline {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
	}

	streamFile(c, &target, h, disp)
}

// fileWithinShare 校验 f 是否在 root 这棵子树内。
//
// 逐级向上走 parent_id，每层都要满足：属于同一所有者、未被软删除。
// 任何一层不满足就返回 false —— 这保证 fileId 无法被构造成跳出分享范围的路径，
// 也顺带挡住了环状parent 数据导致的死循环（层数设上限）。
func fileWithinShare(f, root *models.File) bool {
	const maxDepth = 64 // 正常目录树远小于此；设上限是为了防parent 成环时无限回溯
	cur := *f
	for i := 0; i < maxDepth; i++ {
		if cur.ID == root.ID {
			return true
		}
		if cur.ParentID == nil {
			return false
		}
		var parent models.File
		if err := db.Get().First(&parent, *cur.ParentID).Error; err != nil {
			return false
		}
		// 每一层都必须同属分享所有者：只比ID 不够，
		// 别的账号下的文件理论上可能挂在同一个 parentID下（数据异常时）
		if parent.OwnerID != root.OwnerID {
			return false
		}
		cur = parent
	}
	return false
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
			"type": func() string {
				if f.IsDir {
					return "dir"
				} else {
					return "file"
				}
			}(),
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
