package controllers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nebula-drive/nebula/conf"
	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/db"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// InstallRequest 安装向导提交体（基础项 + 高级项）
type InstallRequest struct {
	DB     conf.DBConfig     `json:"db" binding:"required"`
	System conf.SystemConfig `json:"system" binding:"required"`
	Admin  conf.AdminConfig  `json:"admin" binding:"required"`
	Mail   conf.MailConfig   `json:"mail"`
	Redis  conf.RedisConfig  `json:"redis"`
	// advanced 标记：前端勾选"高级选项"时才提交 Mail/Redis 等非基础项
	Advanced bool `json:"advanced"`
}

// InstallStatus 安装状态
func InstallStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"installed": conf.IsInstalled(),
	})
}

// Install 执行安装
func Install(c *gin.Context) {
	var req InstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	// 1. 测试数据库连接
	if err := db.Init(&req.DB); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "db connect failed: " + err.Error()})
		return
	}
	// 2. 自动建表
	if err := models.AutoMigrate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "migrate failed: " + err.Error()})
		return
	}
	// 3. 创建默认用户组
	ensureDefaultGroup()
	// 4. 创建管理员
	if err := createAdmin(req.Admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "create admin failed: " + err.Error()})
		return
	}
	// 5. 创建默认本地存储策略
	ensureDefaultPolicy(req.System.UploadPath)
	// 6. 持久化配置（敏感字段加密 env + ini 辅助）
	cfg := &conf.Config{
		DB:     req.DB,
		System: req.System,
		Admin:  req.Admin,
		Mail:   req.Mail,
		Redis:  req.Redis,
	}
	if err := conf.Save(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "save config failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "installed"})
}

// TestDB 测试数据库连接（安装向导中"测试连接"按钮）
func TestDB(c *gin.Context) {
	var dc conf.DBConfig
	if err := c.ShouldBindJSON(&dc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if err := db.Init(&dc); err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
}

func ensureDefaultGroup() {
	var g models.Group
	if err := db.Get().First(&g, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Group{
			ID:            1,
			Name:          "default",
			MaxStorage:    10 * 1024 * 1024 * 1024, // 10GB
			ShareEnabled:  true,
			WebDAVEnabled: true,
		})
		db.Get().Create(&models.Group{
			ID:         2,
			Name:       "admin",
			MaxStorage: -1,
		})
	}
}

func createAdmin(a conf.AdminConfig) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u := models.User{
		UserName: a.UserName,
		Email:    a.Email,
		Password: string(hash),
		GroupID:  2,
		IsAdmin:  true,
		NickName: a.UserName,
	}
	return db.Get().Create(&u).Error
}

func ensureDefaultPolicy(uploadPath string) {
	if uploadPath == "" {
		uploadPath = "uploads"
	}
	var p models.Policy
	if err := db.Get().First(&p, 1).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		db.Get().Create(&models.Policy{
			ID:        1,
			Name:      "本地存储",
			Type:      "local",
			Config:    `{"path":"` + uploadPath + `"}`,
			IsDefault: true,
		})
	}
}
