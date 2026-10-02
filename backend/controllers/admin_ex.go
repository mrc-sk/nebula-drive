package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/middleware"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"github.com/nebula-drive/nebula/pkg/plugin"
)

// ---- 存储策略 ----

type policyReq struct {
	Name      string `json:"name" binding:"required"`
	Type      string `json:"type" binding:"required"` // local/s3/oss/cos
	Config    string `json:"config"`
	IsDefault bool   `json:"isDefault"`
}

// ListPolicies 存储策略列表
func ListPolicies(c *gin.Context) {
	var policies []models.Policy
	db.Get().Order("id asc").Find(&policies)
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": policies})
}

// CreatePolicy 创建策略
func CreatePolicy(c *gin.Context) {
	var req policyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.IsDefault {
		db.Get().Model(&models.Policy{}).Where("1=1").Update("is_default", false)
	}
	p := models.Policy{Name: req.Name, Type: req.Type, Config: models.From(req.Config), IsDefault: req.IsDefault}
	if err := db.Get().Create(&p).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": p})
}

// UpdatePolicy 更新策略
func UpdatePolicy(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req policyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if req.IsDefault {
		db.Get().Model(&models.Policy{}).Where("id <> ?", id).Update("is_default", false)
	}
	db.Get().Model(&models.Policy{}).Where("id = ?", id).Updates(map[string]any{
		"name": req.Name, "type": req.Type, "config": req.Config, "is_default": req.IsDefault,
	})

	cur := middleware.CurrentUser(c)
	db.Get().Create(&models.AuditLog{
		UserID:   cur.ID,
		UserName: cur.UserName,
		Action:   "admin_update_policy",
		Target:   req.Name,
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   "policyId=" + strconv.Itoa(id) + ",type=" + req.Type,
	})

	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeletePolicy 删除策略
func DeletePolicy(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "不能删除默认本地策略"})
		return
	}
	db.Get().Delete(&models.Policy{}, id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 系统设置 ----

// GetSettings 所有设置
func GetSettings(c *gin.Context) {
	var settings []models.Setting
	db.Get().Find(&settings)
	m := map[string]string{}
	for _, s := range settings {
		m[s.Key] = s.Value
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": m})
}

// SaveSettings 批量保存设置
func SaveSettings(c *gin.Context) {
	var body map[string]string
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	for k, v := range body {
		var s models.Setting
		if err := db.Get().Where("`key` = ?", k).First(&s).Error; err != nil {
			db.Get().Create(&models.Setting{Key: k, Value: v})
		} else {
			db.Get().Model(&s).Update("value", v)
		}
	}

	cur := middleware.CurrentUser(c)
	detail, _ := json.Marshal(body)
	db.Get().Create(&models.AuditLog{
		UserID:   cur.ID,
		UserName: cur.UserName,
		Action:   "admin_update_settings",
		Target:   "settings",
		IP:       c.ClientIP(),
		UA:       c.Request.UserAgent(),
		Detail:   string(detail),
	})

	c.JSON(http.StatusOK, gin.H{"code": 0})
}

var _ = json.Marshal

// ---- 统计 ----

// Dashboard 首页统计
func Dashboard(c *gin.Context) {
	var userCount int64
	var fileCount int64
	var shareCount int64
	var storageUsed int64
	db.Get().Model(&models.User{}).Count(&userCount)
	db.Get().Model(&models.File{}).Count(&fileCount)
	db.Get().Model(&models.Share{}).Count(&shareCount)
	db.Get().Model(&models.User{}).Select("COALESCE(SUM(storage),0)").Scan(&storageUsed)

	var recentUsers []models.User
	db.Get().Order("id desc").Limit(8).Find(&recentUsers)
	var recentFiles []models.File
	db.Get().Order("id desc").Limit(8).Find(&recentFiles)

	// 运行时状态
	cfg := conf.Current()
	info := map[string]any{
		"siteName":  nil,
		"installed": conf.IsInstalled(),
	}
	if cfg != nil {
		info["siteName"] = cfg.System.SiteName
		info["dbType"] = cfg.DB.Type
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"userCount":   userCount,
			"fileCount":   fileCount,
			"shareCount":  shareCount,
			"storageUsed": storageUsed,
			"recentUsers": recentUsers,
			"recentFiles": recentFiles,
			"info":        info,
		},
	})
}

// StorePluginCatalog 获取插件商店目录
func StorePluginCatalog(c *gin.Context) {
	var s models.Setting
	storeURL := ""
	if err := db.Get().Where("`key` = ?", "plugin.store.url").First(&s).Error; err == nil {
		storeURL = s.Value
	}
	if storeURL == "" {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []any{}})
		return
	}
	if !strings.HasSuffix(storeURL, "/") {
		storeURL += "/"
	}
	storeURL += "com.json"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(storeURL)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []any{}})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "data": []any{}})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// ListPluginHooks 插件钩子列表（含文档描述）
func ListPluginHooks(c *gin.Context) {
	counts := plugin.List()
	type hookItem struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
		Doc   string `json:"doc"`
	}
	list := []hookItem{}
	names := []plugin.HookName{
		plugin.HookEmail, plugin.HookBackup, plugin.HookRestore, plugin.HookApiAuth,
		plugin.HookDBMigrate, plugin.HookRateLimit, plugin.HookAntiLeech, plugin.HookCLI,
		plugin.HookCollabOpen, plugin.HookCollabSave,
	}
	for _, n := range names {
		list = append(list, hookItem{
			Name:  string(n),
			Count: counts[n],
			Doc:   plugin.HookDocs[n],
		})
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": list})
}
