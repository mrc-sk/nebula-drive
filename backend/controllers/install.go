package controllers

import (
	"errors"
	"fmt"
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
	// 1. 测试数据库连接（连接本身不是数据变更，不进事务）
	if err := db.Init(&req.DB); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "db connect failed: " + err.Error()})
		return
	}
	// 2. 自动建表
	//
	// 注意：DDL **不能**放进事务。MySQL 的 CREATE/ALTER TABLE 会隐式提交当前事务，
	// 把它包进 db.Transaction 只会得到一个「看起来有事务、实际没有」的假象。
	if err := models.AutoMigrate(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "migrate failed: " + err.Error()})
		return
	}
	// 3~5.5 初始化数据：全部放进一个事务
	//
	// 修复（对应 CODE_REVIEW P1-4）：原实现是 4 次彼此独立的写入，
	// 任一步失败都会留下「半安装」状态 —— 比如组和策略建好了、管理员没建成功。
	// 更糟的是 createAdmin 不幂等：用户看到失败后重试安装，会因用户名唯一索引冲突而
	// 永久卡死，只能手工删库。
	//
	// 现在要么全部成功、要么全部回滚；配合下面各函数的幂等化，
	// 即使 conf.Save 阶段失败，用户重试也能成功。
	if err := db.Get().Transaction(func(tx *gorm.DB) error {
		if err := ensureDefaultGroup(tx); err != nil {
			return fmt.Errorf("创建默认用户组失败: %w", err)
		}
		if err := createAdmin(tx, req.Admin); err != nil {
			return fmt.Errorf("创建管理员失败: %w", err)
		}
		if err := ensureDefaultPolicy(tx, req.System.UploadPath); err != nil {
			return fmt.Errorf("创建默认存储策略失败: %w", err)
		}
		if err := seedDefaultPlans(tx); err != nil {
			return fmt.Errorf("初始化默认套餐失败: %w", err)
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	// 6. 持久化配置（敏感字段加密 env + ini 辅助）
	//    文件写入无法参与数据库回滚，因此放在事务提交之后。
	//    此步失败时数据库已就绪，用户重试安装可安全通过（依赖上面的幂等化）。
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

// ensureDefaultGroup 创建默认用户组 1 与 2（幂等，在给定事务内执行）
//
// 只检查组 1 是不够的：如果上次安装建好了组 1、建组 2 时失败，
// 「组 1 已存在」会让整个函数直接跳过，组 2 就永远缺失了。这里逐个确保。
func ensureDefaultGroup(tx *gorm.DB) error {
	if err := ensureGroup(tx, 1, "default", 10*1024*1024*1024, true, true); err != nil {
		return err
	}
	return ensureGroup(tx, 2, "admin", -1, true, true)
}

func ensureGroup(tx *gorm.DB, id uint, name string, maxStorage int64, share, webdav bool) error {
	var g models.Group
	err := tx.First(&g, id).Error
	if err == nil {
		return nil // 已存在，幂等跳过
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&models.Group{
		ID:            id,
		Name:          name,
		MaxStorage:    maxStorage, // -1 表示无限
		ShareEnabled:  share,
		WebDAVEnabled: webdav,
	}).Error
}

// createAdmin 创建管理员（幂等，在给定事务内执行）
//
// 幂等策略：同名用户已存在时**更新**为本次提交的信息，而不是返回冲突错误。
// 因为能走到这里说明 conf.IsInstalled() 仍为 false（否则被 InstalledBlock 拦下），
// 即系统处于「上次安装中断」的状态，此时更新才是符合安装意图的行为。
func createAdmin(tx *gorm.DB, a conf.AdminConfig) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var existing models.User
	err = tx.Where("user_name = ?", a.UserName).First(&existing).Error
	if err == nil {
		return tx.Model(&models.User{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"password":  string(hash),
			"email":     a.Email,
			"group_id":  2,
			"is_admin":  true,
			"nick_name": a.UserName,
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
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
	return tx.Create(&u).Error
}

// ensureDefaultPolicy 创建默认本地存储策略（幂等，在给定事务内执行）
func ensureDefaultPolicy(tx *gorm.DB, uploadPath string) error {
	if uploadPath == "" {
		uploadPath = "uploads"
	}
	var p models.Policy
	err := tx.First(&p, 1).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&models.Policy{
		ID:        1,
		Name:      "本地存储",
		Type:      "local",
		Config:    models.From(`{"path":"` + uploadPath + `"}`),
		IsDefault: true,
	}).Error
}
