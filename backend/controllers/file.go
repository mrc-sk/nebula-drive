package controllers

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/filesystem"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"github.com/nebula-drive/nebula/pkg/storage"
	"github.com/nebula-drive/nebula/pkg/util"
	"gorm.io/gorm"
)

// handlerForFile 根据文件的存储策略取得 handler
func handlerForFile(f *models.File) (filesystem.Handler, error) {
	var p models.Policy
	if err := db.Get().First(&p, f.PolicyID).Error; err != nil {
		return nil, err
	}
	return filesystem.New(p.Type, p.Config)
}

// handlerForPolicyID 按 policy id 取 handler
func handlerForPolicyID(pid uint) (filesystem.Handler, *models.Policy, error) {
	var p models.Policy
	if err := db.Get().First(&p, pid).Error; err != nil {
		return nil, nil, err
	}
	h, err := filesystem.New(p.Type, p.Config)
	return h, &p, err
}

// ownOrAdmin 校验文件归属
func ownOrAdmin(c *gin.Context, f *models.File) bool {
	u := middleware.CurrentUser(c)
	return u != nil && (u.IsAdmin || f.OwnerID == u.ID)
}

// groupOf 通过 userId 取到对应 Group，不存在时返回默认组（MaxStorage=-1 无限）
func groupOf(userID uint) models.Group {
	var u models.User
	if db.Get().Select("group_id").First(&u, userID).Error != nil {
		return models.Group{ID: 0, Name: "fallback", MaxStorage: -1, ShareEnabled: true, WebDAVEnabled: true, SpeedLimit: 0}
	}
	var g models.Group
	if db.Get().First(&g, u.GroupID).Error == nil {
		return g
	}
	return models.Group{ID: 1, Name: "default", MaxStorage: -1, ShareEnabled: true, WebDAVEnabled: true, SpeedLimit: 0}
}

// effectiveMaxStorage 取用户有效存储上限：有活跃套餐优先用套餐额度，否则用用户组额度
func effectiveMaxStorage(userID uint) (int64, string) {
	var u models.User
	if err := db.Get().Select("storage, plan_id, plan_expire_at").First(&u, userID).Error; err != nil {
		return -1, "group"
	}
	// 检查是否有活跃套餐
	if u.PlanID > 0 && u.PlanExpireAt != nil && u.PlanExpireAt.After(time.Now()) {
		var plan models.Plan
		if db.Get().Select("max_storage, display_name").First(&plan, u.PlanID).Error == nil {
			return plan.MaxStorage, "plan:" + plan.DisplayName
		}
	}
	// 回退到用户组
	g := groupOf(userID)
	return g.MaxStorage, "group:" + g.Name
}

// ensureQuota 校验 user 再写入 additionalBytes 后是否仍在配额内。
// 优先检查用户套餐额度，无套餐则用用户组额度。MaxStorage == -1 视为无限。
func ensureQuota(userID uint, additionalBytes int64) error {
	maxStorage, source := effectiveMaxStorage(userID)
	if maxStorage == -1 {
		return nil
	}
	var u models.User
	if err := db.Get().Select("storage").First(&u, userID).Error; err != nil {
		return err
	}
	if u.Storage+additionalBytes > maxStorage {
		return fmt.Errorf("quota exceeded: have %d bytes, limit %d bytes (%s), tried to add %d bytes",
			u.Storage, maxStorage, source, additionalBytes)
	}
	return nil
}

// List 列目录
func List(c *gin.Context) {
	u := middleware.CurrentUser(c)
	parentID := c.Query("parent")
	trash := c.Query("trash") == "1"
	tag := c.Query("tag")

	q := db.Get().Model(&models.File{}).Where("owner_id = ?", u.ID)
	if trash {
		q = q.Unscoped().Where("deleted_at IS NOT NULL")
	} else if tag != "" {
		// 按标签筛选：忽略 parent，跨目录返回带该标签的文件
		q = q.Joins("JOIN file_tags ON file_tags.file_id = files.id").
			Where("file_tags.tag = ? AND file_tags.owner_id = ?", tag, u.ID)
	} else {
		var pid *uint
		if parentID != "" && parentID != "0" {
			id, _ := strconv.ParseUint(parentID, 10, 64)
			uid := uint(id)
			pid = &uid
		}
		q = q.Where("parent_id IS ? AND deleted_at IS NULL", pid)
	}
	var files []models.File
	q.Order("is_dir desc, name asc").Find(&files)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": files})
}

type mkdirReq struct {
	Name     string `json:"name" binding:"required"`
	ParentID *uint  `json:"parentId"`
}

// Mkdir 新建目录
func Mkdir(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req mkdirReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	f := models.File{OwnerID: u.ID, Name: req.Name, ParentID: req.ParentID, IsDir: true}
	if err := db.Get().Create(&f).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// Rapid 秒传检查：hash 命中则复用物理文件
type rapidReq struct {
	Hash     string `json:"hash" binding:"required"`
	Name     string `json:"name" binding:"required"`
	ParentID *uint  `json:"parentId"`
	Size     int64  `json:"size"`
}

func Rapid(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req rapidReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	// 秒传也是“新增一份文件记录 + 计占用”，先过配额
	if req.Size > 0 {
		if err := ensureQuota(u.ID, req.Size); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}
	var exist models.File
	err := db.Get().Where("hash = ? AND owner_id = ? AND is_dir = ?", req.Hash, u.ID, false).First(&exist).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "不可秒传"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	f := models.File{
		OwnerID: u.ID, Name: req.Name, ParentID: req.ParentID,
		Size: exist.Size, PolicyID: exist.PolicyID, SourceName: exist.SourceName,
		Extension: exist.Extension, MimeType: exist.MimeType, Hash: req.Hash,
	}
	db.Get().Create(&f)
	addStorage(u.ID, f.Size)
	// 秒传命中：复用已有物理文件，引用计数 +1
	storage.Retain(exist.PolicyID, exist.SourceName, exist.Size, exist.Hash, "rapid")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f, "rapid": true})
}

// Upload 上传（含秒传：前端传 hash 命中则复用）
func Upload(c *gin.Context) {
	u := middleware.CurrentUser(c)
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "未接收到文件"})
		return
	}
	// 配额预检：在整文件读之前先挡住超配额请求
	if file.Size > 0 {
		if err := ensureQuota(u.ID, file.Size); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}
	hash := c.PostForm("hash")
	parentStr := c.PostForm("parentId")
	var parentID *uint
	if parentStr != "" && parentStr != "0" {
		id, _ := strconv.ParseUint(parentStr, 10, 64)
		uid := uint(id)
		parentID = &uid
	}

	// 文件类型白名单
	ext := trimDot(filepath.Ext(file.Filename))
	if !isAllowedExtension(ext) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "不支持的文件类型: " + ext})
		return
	}

	// 秒传命中
	if hash != "" {
		var exist models.File
		if e := db.Get().Where("hash = ? AND owner_id = ? AND is_dir = ?", hash, u.ID, false).First(&exist).Error; e == nil {
			f := models.File{
				OwnerID: u.ID, Name: file.Filename, ParentID: parentID,
				Size: exist.Size, PolicyID: exist.PolicyID, SourceName: exist.SourceName,
				Extension: exist.Extension, MimeType: exist.MimeType, Hash: hash,
			}
			db.Get().Create(&f)
			addStorage(u.ID, f.Size)
			// 秒传：引用计数 +1
			storage.Retain(exist.PolicyID, exist.SourceName, exist.Size, exist.Hash, "upload-rapid")
			c.JSON(http.StatusOK, gin.H{"code": 0, "data": f, "rapid": true})
			return
		}
	}

	// 取默认存储策略
	h, p, err := handlerForPolicyID(1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 魔术字节校验（图片类型）
	if getSettingBool("upload.enable_magic_check", true) && isImageExt(ext) {
		if mf, mErr := file.Open(); mErr == nil {
			head := make([]byte, 512)
			n, _ := mf.Read(head)
			mf.Close()
			if n > 0 && !strings.HasPrefix(http.DetectContentType(head[:n]), "image/") {
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "文件内容与扩展名不符"})
				return
			}
		}
	}
	// 第一遍：计算哈希（使用 buffer 池）
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	hw := md5.New()
	buf := util.GetBuffer()
	if _, err := io.CopyBuffer(hw, src, *buf); err != nil {
		util.PutBuffer(buf)
		src.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	util.PutBuffer(buf)
	src.Close()
	realHash := hex.EncodeToString(hw.Sum(nil))
	if hash == "" {
		hash = realHash
	}

	// 第二遍：写入存储
	src, err = file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer src.Close()
	sourceName := util.UUID() + filepath.Ext(file.Filename)
	if err := h.Put(src, sourceName, file.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	mimeType := mime.TypeByExtension(filepath.Ext(file.Filename))
	f := models.File{
		OwnerID: u.ID, Name: file.Filename, ParentID: parentID,
		Size: file.Size, PolicyID: p.ID, SourceName: sourceName,
		Extension: ext, MimeType: mimeType, Hash: hash,
	}
	db.Get().Create(&f)
	addStorage(u.ID, f.Size)
	// 新物理文件：建立 FileObject refs=1
	storage.Retain(p.ID, sourceName, file.Size, hash, "upload")

	uctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"fileId":   f.ID,
		"fileName": f.Name,
		"size":     f.Size,
	}
	plugin.Fire(plugin.HookRateLimit, uctx)

	db.Get().Create(&models.AuditLog{
		UserID:   u.ID,
		UserName: u.UserName,
		Action:   "upload",
		Target:   f.Name,
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   fmt.Sprintf("size=%d,policyId=%d", f.Size, f.PolicyID),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// Download 下载
func Download(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) || f.IsDir {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	u := middleware.CurrentUser(c)
	dctx := map[string]any{
		"userId":   u.ID,
		"userName": u.UserName,
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"fileId":   f.ID,
		"fileName": f.Name,
		"referer":  c.GetHeader("Referer"),
	}
	if errs := plugin.Fire(plugin.HookAntiLeech, dctx); len(errs) > 0 {
		for _, e := range errs {
			if strings.Contains(e.Error(), "blocked") {
				c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "blocked by anti-leech"})
				return
			}
		}
	}
	plugin.Fire(plugin.HookRateLimit, dctx)

	h, err := handlerForFile(&f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	db.Get().Create(&models.AuditLog{
		UserID:   u.ID,
		UserName: u.UserName,
		Action:   "download",
		Target:   f.Name,
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   fmt.Sprintf("fileId=%d,size=%d", f.ID, f.Size),
	})

	// 对象存储：优先签名 URL 302 跳转，避免服务器代理
	var p models.Policy
	policyType := ""
	if err := db.Get().First(&p, f.PolicyID).Error; err == nil {
		policyType = p.Type
	}
	if policyType == "s3" || policyType == "oss" || policyType == "cos" {
		if url, perr := h.PresignGet(f.SourceName, 5*time.Minute); perr == nil && url != "" {
			c.Redirect(http.StatusFound, url)
			return
		}
	}

	// Range 请求：返回 206 Partial Content
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" {
		if offset, length, ok := parseRange(rangeHeader, f.Size); ok {
			rc, rerr := h.GetRange(f.SourceName, offset, length)
			if rerr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": rerr.Error()})
				return
			}
			defer rc.Close()
			c.Header("Content-Disposition", "attachment; filename=\""+path.Base(f.Name)+"\"")
			if f.MimeType != "" {
				c.Header("Content-Type", f.MimeType)
			}
			c.Header("Accept-Ranges", "bytes")
			c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, f.Size))
			c.Header("Content-Length", strconv.FormatInt(length, 10))
			c.Status(http.StatusPartialContent)
			rbuf := util.GetBuffer()
			io.CopyBuffer(c.Writer, rc, *rbuf)
			util.PutBuffer(rbuf)
			return
		}
	}

	// 普通流式
	rc, err := h.Get(f.SourceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer rc.Close()

	c.Header("Content-Disposition", "attachment; filename=\""+path.Base(f.Name)+"\"")
	if f.MimeType != "" {
		c.Header("Content-Type", f.MimeType)
	}
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Length", strconv.FormatInt(f.Size, 10))
	nbuf := util.GetBuffer()
	io.CopyBuffer(c.Writer, rc, *nbuf)
	util.PutBuffer(nbuf)
}

// parseRange 解析 Range 头，返回 (offset, length)。仅支持单段 bytes=START-END / bytes=START- / bytes=-N
func parseRange(h string, total int64) (int64, int64, bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(h, "bytes=")
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i]
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	startStr, endStr := parts[0], parts[1]
	var start, end int64
	if startStr == "" {
		// 后缀：bytes=-N（最后 N 字节）
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > total {
			n = total
		}
		start = total - n
		end = total - 1
	} else {
		s, err := strconv.ParseInt(startStr, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		start = s
		if endStr == "" {
			end = total - 1
		} else {
			e, err := strconv.ParseInt(endStr, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			end = e
		}
	}
	if start < 0 || start >= total || end < start {
		return 0, 0, false
	}
	if end >= total {
		end = total - 1
	}
	return start, end - start + 1, true
}

type renameReq struct {
	Name string `json:"name" binding:"required"`
}

// Rename 重命名
func Rename(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var req renameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	db.Get().Model(&f).Update("name", req.Name)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type moveReq struct {
	ParentID *uint `json:"parentId"`
}

// Move 移动
func Move(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var req moveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	db.Get().Model(&f).Update("parent_id", req.ParentID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Delete 删除（软删除进回收站）
func Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	db.Get().Delete(&f)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Restore 从回收站恢复
func Restore(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	var f models.File
	if err := db.Get().Unscoped().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !u.IsAdmin && f.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	now := time.Now()
	folderName := fmt.Sprintf("恢复的文件-%s", now.Format("20060102-150405"))
	restoreFolder := models.File{
		OwnerID: u.ID,
		Name:    folderName,
		IsDir:   true,
	}
	if err := db.Get().Create(&restoreFolder).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	newParentID := restoreFolder.ID
	db.Get().Unscoped().Model(&f).Updates(map[string]any{
		"deleted_at": nil,
		"parent_id":  &newParentID,
	})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Purge 彻底删除（从回收站清空）
func Purge(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	u := middleware.CurrentUser(c)
	var f models.File
	if err := db.Get().Unscoped().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !u.IsAdmin && f.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	if !f.IsDir {
		if h, err := handlerForFile(&f); err == nil {
			// 引用计数减 1，到 0 才真正删物理文件
			_, _ = storage.ReleaseObject(f.PolicyID, f.SourceName, h)
		}
	}
	addStorage(f.OwnerID, -f.Size)
	db.Get().Unscoped().Delete(&f)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// addStorage 增减用户已用容量
func addStorage(userID uint, delta int64) {
	db.Get().Model(&models.User{}).Where("id = ?", userID).
		UpdateColumn("storage", gorm.Expr("storage + ?", delta))
}

// Breadcrumb 路径面包屑（从当前目录向上）
func Breadcrumb(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var crumbs []models.File
	cur := uint(id)
	for cur != 0 {
		var f models.File
		if err := db.Get().First(&f, cur).Error; err != nil {
			break
		}
		crumbs = append([]models.File{f}, crumbs...)
		if f.ParentID == nil {
			break
		}
		cur = *f.ParentID
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": crumbs})
}

// ---- 分片上传 ----

const chunkSize = 20 * 1024 * 1024 // 20MB

type chunkUploadMeta struct {
	UploadID   string
	Hash       string
	Name       string
	Size       int64
	ParentID   *uint
	OwnerID    uint
	ChunkCount int
	Chunks     map[int]string // 索引 -> 临时文件路径
	CreatedAt  time.Time
}

var (
	chunkUploadsMu sync.Mutex
	chunkUploads   = make(map[string]*chunkUploadMeta)
)

func chunksDir() string {
	return "uploads/chunks"
}

type chunkInitReq struct {
	Hash     string `json:"hash" binding:"required"`
	Name     string `json:"name" binding:"required"`
	Size     int64  `json:"size" binding:"required"`
	ParentID *uint  `json:"parentId"`
}

// ChunkInit 初始化分片上传
func ChunkInit(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req chunkInitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	var exist models.File
	err := db.Get().Where("hash = ? AND owner_id = ? AND is_dir = ?", req.Hash, u.ID, false).First(&exist).Error
	if err == nil {
		f := models.File{
			OwnerID: u.ID, Name: req.Name, ParentID: req.ParentID,
			Size: exist.Size, PolicyID: exist.PolicyID, SourceName: exist.SourceName,
			Extension: exist.Extension, MimeType: exist.MimeType, Hash: req.Hash,
		}
		db.Get().Create(&f)
		addStorage(u.ID, f.Size)
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
			"uploadId":  "",
			"chunkSize": chunkSize,
			"exists":    true,
			"file":      f,
		}})
		return
	}
	uploadID := util.UUID()
	chunkCount := int((req.Size + chunkSize - 1) / chunkSize)
	if req.Size == 0 {
		chunkCount = 1
	}
	meta := &chunkUploadMeta{
		UploadID:   uploadID,
		Hash:       req.Hash,
		Name:       req.Name,
		Size:       req.Size,
		ParentID:   req.ParentID,
		OwnerID:    u.ID,
		ChunkCount: chunkCount,
		Chunks:     make(map[int]string),
		CreatedAt:  time.Now(),
	}
	chunkUploadsMu.Lock()
	chunkUploads[uploadID] = meta
	chunkUploadsMu.Unlock()
	_ = os.MkdirAll(chunksDir(), 0o755)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{
		"uploadId":  uploadID,
		"chunkSize": chunkSize,
		"exists":    false,
	}})
}

// ChunkUpload 上传单个分片
func ChunkUpload(c *gin.Context) {
	u := middleware.CurrentUser(c)
	uploadID := c.PostForm("uploadId")
	idxStr := c.PostForm("index")
	if uploadID == "" || idxStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "uploadId and index required"})
		return
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid index"})
		return
	}
	chunkUploadsMu.Lock()
	meta, ok := chunkUploads[uploadID]
	chunkUploadsMu.Unlock()
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "upload session not found"})
		return
	}
	if meta.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	file, err := c.FormFile("chunk")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "chunk file required"})
		return
	}
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer src.Close()
	chunkPath := filepath.Join(chunksDir(), fmt.Sprintf("%s-%d", uploadID, idx))
	dst, err := os.Create(chunkPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	chunkUploadsMu.Lock()
	meta.Chunks[idx] = chunkPath
	chunkUploadsMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"received": len(meta.Chunks), "total": meta.ChunkCount}})
}

type chunkMergeReq struct {
	UploadID string `json:"uploadId" binding:"required"`
}

// ChunkMerge 合并分片并创建文件记录
func ChunkMerge(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req chunkMergeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	chunkUploadsMu.Lock()
	meta, ok := chunkUploads[req.UploadID]
	chunkUploadsMu.Unlock()
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "upload session not found"})
		return
	}
	if meta.OwnerID != u.ID {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	if len(meta.Chunks) != meta.ChunkCount {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": fmt.Sprintf("chunk incomplete: %d/%d", len(meta.Chunks), meta.ChunkCount)})
		return
	}
	// 配额预检（用 meta.Size）
	if meta.Size > 0 {
		if err := ensureQuota(u.ID, meta.Size); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}
	h, p, err := handlerForPolicyID(1)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	sourceName := util.UUID() + filepath.Ext(meta.Name)
	mergedSize := int64(0)
	hw := md5.New()
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for i := 0; i < meta.ChunkCount; i++ {
			cp, ok := meta.Chunks[i]
			if !ok {
				continue
			}
			cf, err := os.Open(cp)
			if err != nil {
				continue
			}
			buf := make([]byte, 64*1024)
			for {
				n, rerr := cf.Read(buf)
				if n > 0 {
					pw.Write(buf[:n])
				}
				if rerr != nil {
					break
				}
			}
			cf.Close()
		}
	}()
	tee := io.TeeReader(pr, hw)
	if err := h.Put(tee, sourceName, meta.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	realHash := hex.EncodeToString(hw.Sum(nil))
	finalHash := meta.Hash
	if finalHash == "" {
		finalHash = realHash
	}
	mergedSize = meta.Size
	ext := strings.TrimPrefix(filepath.Ext(meta.Name), ".")
	mimeType := mime.TypeByExtension(filepath.Ext(meta.Name))
	f := models.File{
		OwnerID: u.ID, Name: meta.Name, ParentID: meta.ParentID,
		Size: mergedSize, PolicyID: p.ID, SourceName: sourceName,
		Extension: ext, MimeType: mimeType, Hash: finalHash,
	}
	db.Get().Create(&f)
	addStorage(u.ID, f.Size)
	// 分片合并：新物理文件 refs=1
	storage.Retain(p.ID, sourceName, mergedSize, finalHash, "chunk-merge")
	for _, cp := range meta.Chunks {
		os.Remove(cp)
	}
	chunkUploadsMu.Lock()
	delete(chunkUploads, req.UploadID)
	chunkUploadsMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// 防止 time 未用
var _ = time.Now

// ---- 设置读取辅助 ----

func getSetting(key string) string {
	if db.Get() == nil {
		return ""
	}
	var s models.Setting
	if err := db.Get().Where("`key` = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

func getSettingInt(key string, def int) int {
	v := strings.TrimSpace(getSetting(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getSettingBool(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(getSetting(key))) {
	case "":
		return def
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// defaultAllowedExtensions 默认允许上传的扩展名
func defaultAllowedExtensions() string {
	return "jpg,jpeg,png,gif,webp,mp4,webm,mp3,wav,pdf,doc,docx,xls,xlsx,ppt,pptx,zip,rar,7z,tar,gz,txt,md,go,py,js,ts,json,yaml,yml,xml,csv"
}

// isAllowedExtension 扩展名是否在上传白名单
func isAllowedExtension(ext string) bool {
	if ext == "" {
		return false
	}
	allowed := getSetting("upload.allowed_extensions")
	if strings.TrimSpace(allowed) == "" {
		allowed = defaultAllowedExtensions()
	}
	for _, e := range strings.Split(allowed, ",") {
		if strings.EqualFold(strings.TrimSpace(e), ext) {
			return true
		}
	}
	return false
}

// isImageExt 是否图片扩展名
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

// handlerForVersion 按版本记录的 policyId 取 handler
func handlerForVersion(v *models.FileVersion) (filesystem.Handler, error) {
	var p models.Policy
	if err := db.Get().First(&p, v.PolicyID).Error; err != nil {
		return nil, err
	}
	return filesystem.New(p.Type, p.Config)
}

// ---- 文件版本控制 ----

// UploadVersion 上传新版本：归档当前版本到 FileVersion，更新 File，淘汰超出 max_versions 的最旧版本
func UploadVersion(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) || f.IsDir {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "未接收到文件"})
		return
	}
	// 配额：版本上传只计「新旧大小差」，若差为正则先预检
	delta := file.Size - f.Size
	if delta > 0 {
		if err := ensureQuota(u.ID, delta); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
			return
		}
	}

	// 选择存储 handler：可选 policyId 表单字段，否则用文件当前策略
	var h filesystem.Handler
	newPolicyID := f.PolicyID
	if pidStr := c.PostForm("policyId"); pidStr != "" {
		if pid, perr := strconv.ParseUint(pidStr, 10, 64); perr == nil && pid != 0 {
			if hh, _, e := handlerForPolicyID(uint(pid)); e == nil {
				h = hh
				newPolicyID = uint(pid)
			}
		}
	}
	if h == nil {
		hh, e := handlerForFile(&f)
		if e != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": e.Error()})
			return
		}
		h = hh
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	sourceName := util.UUID() + filepath.Ext(file.Filename)
	hw := md5.New()
	tee := io.TeeReader(src, hw)
	if err := h.Put(tee, sourceName, file.Size); err != nil {
		src.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	src.Close()
	realHash := hex.EncodeToString(hw.Sum(nil))

	// 归档当前版本
	var last models.FileVersion
	db.Get().Where("file_id = ?", f.ID).Order("version desc").First(&last)
	hv := models.FileVersion{
		FileID: f.ID, Version: last.Version + 1,
		Size: f.Size, Hash: f.Hash, SourceName: f.SourceName, PolicyID: f.PolicyID,
	}
	if err := db.Get().Create(&hv).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}

	oldSize := f.Size
	oldPol := f.PolicyID
	oldSrc := f.SourceName
	f.Size = file.Size
	f.Hash = realHash
	f.SourceName = sourceName
	f.PolicyID = newPolicyID
	db.Get().Model(&f).Updates(map[string]any{
		"size": f.Size, "hash": realHash, "source_name": sourceName, "policy_id": newPolicyID,
	})
	addStorage(f.OwnerID, f.Size-oldSize)
	// 新版本文件：refs=1
	storage.Retain(newPolicyID, sourceName, file.Size, realHash, "upload-version")
	// 原文件的 Source 交给版本归档（hv 持有），引用计数不变（File→FileVersion 等价交接），
	// 版本被删时走 DeleteVersion Release；此处释放旧 Source 若其 FileObject 还被复用（不应出现）也安全。

	// 淘汰超出 max_versions 的最旧版本
	maxVersions := getSettingInt("file.max_versions", 10)
	if maxVersions > 0 {
		var versions []models.FileVersion
		db.Get().Where("file_id = ?", f.ID).Order("version asc").Find(&versions)
		if len(versions) > maxVersions {
			for i := 0; i < len(versions)-maxVersions; i++ {
				v := versions[i]
				if hh, e := handlerForVersion(&v); e == nil {
					_, _ = storage.ReleaseObject(v.PolicyID, v.SourceName, hh)
				}
				db.Get().Delete(&v)
			}
		}
	}
	// 占位用（避免 oldPol/oldSrc 不被用到告警，保留便于未来细粒度逻辑）
	_ = oldPol
	_ = oldSrc

	db.Get().Create(&models.AuditLog{
		UserID: u.ID, UserName: u.UserName,
		Action: "upload_version", Target: f.Name,
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
		Detail: fmt.Sprintf("fileId=%d,version=%d,size=%d", f.ID, hv.Version, file.Size),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// ListVersions 列出文件所有历史版本
func ListVersions(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var versions []models.FileVersion
	db.Get().Where("file_id = ?", f.ID).Order("version desc").Find(&versions)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": versions})
}

// DownloadVersion 下载指定历史版本
func DownloadVersion(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	vid, _ := strconv.Atoi(c.Param("vid"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) || f.IsDir {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var v models.FileVersion
	if err := db.Get().Where("file_id = ? AND id = ?", f.ID, vid).First(&v).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "version not found"})
		return
	}
	h, err := handlerForVersion(&v)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	rc, err := h.Get(v.SourceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	defer rc.Close()
	c.Header("Content-Disposition", "attachment; filename=\""+path.Base(f.Name)+"\"")
	if f.MimeType != "" {
		c.Header("Content-Type", f.MimeType)
	}
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Length", strconv.FormatInt(v.Size, 10))
	io.Copy(c.Writer, rc)
}

// RestoreVersion 恢复到指定版本：当前版本存为历史，指定版本设为当前
func RestoreVersion(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	vid, _ := strconv.Atoi(c.Param("vid"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var v models.FileVersion
	if err := db.Get().Where("file_id = ? AND id = ?", f.ID, vid).First(&v).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "version not found"})
		return
	}
	// 归档当前版本为新的历史
	var last models.FileVersion
	db.Get().Where("file_id = ?", f.ID).Order("version desc").First(&last)
	hv := models.FileVersion{
		FileID: f.ID, Version: last.Version + 1,
		Size: f.Size, Hash: f.Hash, SourceName: f.SourceName, PolicyID: f.PolicyID,
	}
	if err := db.Get().Create(&hv).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 把指定版本设为当前
	oldSize := f.Size
	f.Size = v.Size
	f.Hash = v.Hash
	f.SourceName = v.SourceName
	f.PolicyID = v.PolicyID
	db.Get().Model(&f).Updates(map[string]any{
		"size": v.Size, "hash": v.Hash, "source_name": v.SourceName, "policy_id": v.PolicyID,
	})
	addStorage(f.OwnerID, f.Size-oldSize)
	// v.SourceName 之前仅被版本持有；现在 File 也持有它，引用计数 +1
	storage.Retain(v.PolicyID, v.SourceName, v.Size, v.Hash, "rollback-version")
	// 从历史中移除已恢复的版本（其存储现由当前文件引用，不可删）
	db.Get().Delete(&v)

	db.Get().Create(&models.AuditLog{
		UserID: f.OwnerID, Action: "restore_version", Target: f.Name,
		IP: c.ClientIP(), UA: c.Request.UserAgent(),
		Detail: fmt.Sprintf("fileId=%d,versionId=%d", f.ID, vid),
	})
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": f})
}

// DeleteVersion 删除指定历史版本（同时删除其存储层文件）
func DeleteVersion(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	vid, _ := strconv.Atoi(c.Param("vid"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var v models.FileVersion
	if err := db.Get().Where("file_id = ? AND id = ?", f.ID, vid).First(&v).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "version not found"})
		return
	}
	if h, err := handlerForVersion(&v); err == nil {
		_, _ = storage.ReleaseObject(v.PolicyID, v.SourceName, h)
	}
	db.Get().Delete(&v)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 文件标签 ----

type tagReq struct {
	Tag   string `json:"tag" binding:"required"`
	Color string `json:"color"`
}

// AddTag 给文件打标签
func AddTag(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	var req tagReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	t := models.FileTag{FileID: f.ID, OwnerID: u.ID, Tag: req.Tag, Color: req.Color}
	if err := db.Get().Create(&t).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": t})
}

// RemoveTag 移除文件标签
func RemoveTag(c *gin.Context) {
	u := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	tag := c.Param("tag")
	var f models.File
	if err := db.Get().First(&f, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	if !ownOrAdmin(c, &f) {
		c.JSON(http.StatusForbidden, gin.H{"code": 1, "message": "forbidden"})
		return
	}
	db.Get().Where("file_id = ? AND owner_id = ? AND tag = ?", f.ID, u.ID, tag).Delete(&models.FileTag{})
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ListTags 列出当前用户所有标签（聚合 distinct tag+color+count）
func ListTags(c *gin.Context) {
	u := middleware.CurrentUser(c)
	type tagAgg struct {
		Tag   string `json:"tag"`
		Color string `json:"color"`
		Count int    `json:"count"`
	}
	var tags []tagAgg
	db.Get().Model(&models.FileTag{}).
		Select("tag, color, COUNT(*) as count").
		Where("owner_id = ?", u.ID).
		Group("tag, color").
		Order("count desc").
		Scan(&tags)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": tags})
}

// ---- 批量操作 ----

type batchMoveReq struct {
	FileIDs        []uint `json:"fileIds"`
	TargetParentID uint   `json:"targetParentId"`
}

// BatchMove 批量移动
func BatchMove(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req batchMoveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if len(req.FileIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "fileIds required"})
		return
	}
	var pid *uint
	if req.TargetParentID != 0 {
		pid = &req.TargetParentID
	}
	for _, fid := range req.FileIDs {
		var f models.File
		if err := db.Get().First(&f, fid).Error; err != nil {
			continue
		}
		if !u.IsAdmin && f.OwnerID != u.ID {
			continue
		}
		db.Get().Model(&f).Update("parent_id", pid)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type batchCopyReq struct {
	FileIDs        []uint `json:"fileIds"`
	TargetParentID uint   `json:"targetParentId"`
}

// BatchCopy 批量复制（复制 File 记录 + 复制存储层文件）
func BatchCopy(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req batchCopyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if len(req.FileIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "fileIds required"})
		return
	}
	var pid *uint
	if req.TargetParentID != 0 {
		pid = &req.TargetParentID
	}
	for _, fid := range req.FileIDs {
		var f models.File
		if err := db.Get().First(&f, fid).Error; err != nil {
			continue
		}
		if !u.IsAdmin && f.OwnerID != u.ID {
			continue
		}
		if f.IsDir {
			continue
		}
		h, err := handlerForFile(&f)
		if err != nil {
			continue
		}
		// 复制新增一份占用，先过配额
		if f.Size > 0 {
			if err := ensureQuota(u.ID, f.Size); err != nil {
				continue
			}
		}
		newSource := util.UUID() + filepath.Ext(f.SourceName)
		if err := h.Copy(f.SourceName, newSource); err != nil {
			continue
		}
		nf := f
		nf.ID = 0
		nf.SourceName = newSource
		nf.ParentID = pid
		if err := db.Get().Create(&nf).Error; err == nil {
			addStorage(u.ID, nf.Size)
			// 复制产生的新物理文件：refs=1
			storage.Retain(nf.PolicyID, newSource, nf.Size, nf.Hash, "batch-copy")
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type batchPolicyReq struct {
	FileIDs  []uint `json:"fileIds"`
	PolicyID uint   `json:"policyId"`
}

// BatchPolicy 批量更改存储策略（后台 goroutine 实际搬运文件）
func BatchPolicy(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req batchPolicyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.PolicyID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "policyId required"})
		return
	}
	// 同步校验归属，过滤出可操作的文件 id
	ids := make([]uint, 0, len(req.FileIDs))
	for _, fid := range req.FileIDs {
		var f models.File
		if err := db.Get().First(&f, fid).Error; err != nil {
			continue
		}
		if !u.IsAdmin && f.OwnerID != u.ID {
			continue
		}
		ids = append(ids, fid)
	}
	go batchPolicyMigrate(ids, req.PolicyID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "migrating in background"})
}

// batchPolicyMigrate 后台搬运：旧 handler Get → 新 handler Put → 更新 File → 删除旧文件
func batchPolicyMigrate(ids []uint, policyID uint) {
	newH, _, err := handlerForPolicyID(policyID)
	if err != nil {
		return
	}
	for _, fid := range ids {
		var f models.File
		if err := db.Get().First(&f, fid).Error; err != nil {
			continue
		}
		if f.IsDir {
			continue
		}
		oldH, err := handlerForFile(&f)
		if err != nil {
			continue
		}
		newSource := util.UUID() + filepath.Ext(f.SourceName)
		rc, err := oldH.Get(f.SourceName)
		if err != nil {
			continue
		}
		if err := newH.Put(rc, newSource, f.Size); err != nil {
			rc.Close()
			continue
		}
		rc.Close()
		oldSource := f.SourceName
		oldPolicy := f.PolicyID
		db.Get().Model(&f).Updates(map[string]any{
			"source_name": newSource, "policy_id": policyID,
		})
		// 新物理文件
		storage.Retain(policyID, newSource, f.Size, f.Hash, "move-copy-fallback")
		// 旧物理文件：引用减 1
		if oldH != nil {
			_, _ = storage.ReleaseObject(oldPolicy, oldSource, oldH)
		}
	}
}

type batchDeleteReq struct {
	FileIDs []uint `json:"fileIds"`
}

// BatchDelete 批量删除到回收站
func BatchDelete(c *gin.Context) {
	u := middleware.CurrentUser(c)
	var req batchDeleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if len(req.FileIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "fileIds required"})
		return
	}
	for _, fid := range req.FileIDs {
		var f models.File
		if err := db.Get().First(&f, fid).Error; err != nil {
			continue
		}
		if !u.IsAdmin && f.OwnerID != u.ID {
			continue
		}
		db.Get().Delete(&f) // 软删除进回收站
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 回收站自动清理 ----

// StartTrashCleanup 启动回收站自动清理 goroutine，每 6 小时扫描一次
func StartTrashCleanup() {
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		cleanupTrash() // 启动时先执行一次
		for range ticker.C {
			cleanupTrash()
		}
	}()
}

// cleanupTrash 扫描软删除超过 retention_days 的文件，永久删除存储层 + DB 记录
func cleanupTrash() {
	if db.Get() == nil {
		return
	}
	retention := getSettingInt("trash.retention_days", 30)
	if retention <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retention) * 24 * time.Hour)
	var files []models.File
	db.Get().Unscoped().Where("deleted_at IS NOT NULL AND deleted_at < ?", cutoff).Find(&files)
	for _, f := range files {
		if !f.IsDir {
			if h, err := handlerForFile(&f); err == nil {
				_, _ = storage.ReleaseObject(f.PolicyID, f.SourceName, h)
			}
		}
		addStorage(f.OwnerID, -f.Size)
		db.Get().Unscoped().Delete(&f)
	}
}
