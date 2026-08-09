package controllers

import (
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

// GetShare 获取分享（公开，校验密码/过期/提取码）
func GetShare(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	pwd := c.Query("password")
	if pwd == "" {
		pwd = c.GetHeader("X-Share-Pwd")
	}
	extract := c.Query("extract")
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
	// 通知分享所有者被访问
	CreateNotification(s.OwnerID, "分享被访问", "你的分享被访问", "info", "share")
	var f models.File
	db.Get().First(&f, s.FileID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"share": s, "file": f}})
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
