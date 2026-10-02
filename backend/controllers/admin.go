package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"golang.org/x/crypto/bcrypt"
)

// ---- 用户管理（管理员）----

// ListUsers 用户列表
func ListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	db.Get().Model(&models.User{}).Count(&total)
	var users []models.User
	db.Get().Order("id desc").Offset((page - 1) * size).Limit(size).Find(&users)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"total": total, "list": users}})
}

type createUserReq struct {
	UserName string `json:"userName" binding:"required"`
	Password string `json:"password" binding:"required"`
	Email    string `json:"email"`
	NickName string `json:"nickName"`
	GroupID  uint   `json:"groupId"`
	IsAdmin  bool   `json:"isAdmin"`
}

// CreateUser 管理员创建账号（关闭公开注册，账号归管理员管理）
func CreateUser(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.GroupID == 0 {
		req.GroupID = 1
	}
	// 密码策略必须在这里校验，与注册（auth.go Register）和改密码
	// （auth.go ChangePassword）保持一致。此前只有 binding:"required"（非空），
	// 意味着管理端可以创建 "123" 这种弱密码账号，绕过系统密码策略。
	if err := validatePassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	u := models.User{
		UserName: req.UserName, Email: req.Email, Password: string(hash),
		GroupID: req.GroupID, IsAdmin: req.IsAdmin, NickName: req.NickName,
	}
	if err := db.Get().Create(&u).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	// 欢迎通知
	CreateNotification(u.ID, "欢迎加入", "欢迎加入 NebulaDrive，你的账号已由管理员创建。", "info", "system")
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": u})
}

type updateUserReq struct {
	Status   *int   `json:"status"`
	GroupID  *uint  `json:"groupId"`
	IsAdmin  *bool  `json:"isAdmin"`
	NickName string `json:"nickName"`
	Storage  *int64 `json:"storage"`
}

// UpdateUser 更新用户
func UpdateUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req updateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	updates := map[string]any{}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.GroupID != nil {
		updates["group_id"] = *req.GroupID
	}
	if req.IsAdmin != nil {
		updates["is_admin"] = *req.IsAdmin
	}
	if req.NickName != "" {
		updates["nick_name"] = req.NickName
	}
	if req.Storage != nil {
		updates["storage"] = *req.Storage
	}
	if len(updates) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	db.Get().Model(&models.User{}).Where("id = ?", id).Updates(updates)

	cur := middleware.CurrentUser(c)
	detail, _ := json.Marshal(updates)
	db.Get().Create(&models.AuditLog{
		UserID:   cur.ID,
		UserName: cur.UserName,
		Action:   "admin_update_user",
		Target:   strconv.Itoa(id),
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   string(detail),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteUser 删除用户
func DeleteUser(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	cur := middleware.CurrentUser(c)
	if cur != nil && uint(id) == cur.ID {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "不能删除自己"})
		return
	}
	db.Get().Delete(&models.User{}, id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 用户组管理 ----

// ListGroups 用户组列表
func ListGroups(c *gin.Context) {
	var groups []models.Group
	db.Get().Order("id asc").Find(&groups)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": groups})
}

type groupReq struct {
	Name          string `json:"name" binding:"required"`
	MaxStorage    int64  `json:"maxStorage"`
	ShareEnabled  bool   `json:"shareEnabled"`
	WebDAVEnabled bool   `json:"webdavEnabled"`
	SpeedLimit    int    `json:"speedLimit"`
}

// CreateGroup 创建用户组
func CreateGroup(c *gin.Context) {
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	g := models.Group{
		Name: req.Name, MaxStorage: req.MaxStorage,
		ShareEnabled: req.ShareEnabled, WebDAVEnabled: req.WebDAVEnabled, SpeedLimit: req.SpeedLimit,
	}
	db.Get().Create(&g)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": g})
}

// UpdateGroup 更新用户组：严格校验 ID、返回真实错误、零行影响返回 code=1（避免"假成功"）
func UpdateGroup(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid group id"})
		return
	}
	var req groupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	tx := db.Get().Model(&models.Group{}).Where("id = ?", uint(id)).Updates(map[string]any{
		"name":            req.Name,
		"max_storage":     req.MaxStorage,
		"share_enabled":   req.ShareEnabled,
		"web_dav_enabled": req.WebDAVEnabled,
		"speed_limit":     req.SpeedLimit,
	})
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "update failed: " + tx.Error.Error()})
		return
	}
	if tx.RowsAffected == 0 {
		// 可能：组不存在，或者新值与旧值完全一致 → 校验存在性后再决定返回
		var cnt int64
		if db.Get().Model(&models.Group{}).Where("id = ?", uint(id)).Count(&cnt).Error != nil || cnt == 0 {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "group not found"})
			return
		}
	}

	cur := middleware.CurrentUser(c)
	detail, _ := json.Marshal(req)
	db.Get().Create(&models.AuditLog{
		UserID:   cur.ID,
		UserName: cur.UserName,
		Action:   "admin_update_group",
		Target:   c.Param("id"),
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   string(detail),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 插件管理（轻量）----

// ListPlugins 插件列表
func ListPlugins(c *gin.Context) {
	var plugins []models.Plugin
	db.Get().Order("id asc").Find(&plugins)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": plugins})
}

// TogglePlugin 启用/禁用插件
func TogglePlugin(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var p models.Plugin
	if err := db.Get().First(&p, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1, "message": "not found"})
		return
	}
	db.Get().Model(&p).Update("enabled", !p.Enabled)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
