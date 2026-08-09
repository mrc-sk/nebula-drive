package models

import (
	"time"

	"github.com/nebula-drive/nebula/pkg/db"
	"gorm.io/gorm"
)

// User 用户
type User struct {
	ID              uint           `gorm:"primarykey" json:"id"`
	UserName        string         `gorm:"uniqueIndex;size:64;not null" json:"userName"`
	Email           string         `gorm:"index;size:128" json:"email"`
	Password        string         `gorm:"size:128;not null" json:"-"` // bcrypt
	Status          int            `gorm:"default:0" json:"status"`    // 0 正常 1 封禁
	GroupID         uint           `gorm:"default:1" json:"groupId"`
	Storage         int64          `gorm:"default:0" json:"storage"` // 已用字节
	TwoFactor       string         `gorm:"size:64" json:"-"`         // TOTP secret，加密
	TwoFactorHinted bool           `gorm:"default:false" json:"-"`   // 是否提醒过开启2FA
	Avatar          string         `gorm:"size:255" json:"avatar"`
	NickName        string         `gorm:"size:64" json:"nickName"`
	PreferLang      string         `gorm:"size:16;default:'zh-CN'" json:"preferLang"`
	ThemeMode       string         `gorm:"size:16;default:'auto'" json:"themeMode"`
	LogoEgg         bool           `gorm:"default:false" json:"logoEgg"`
	SelectMode      string         `gorm:"size:16;default:'context'" json:"selectMode"`
	IsAdmin         bool           `gorm:"default:false" json:"isAdmin"`
	PlanID          uint           `gorm:"default:0" json:"planId"`          // 当前套餐 0=免费
	PlanExpireAt    *time.Time     `json:"planExpireAt"`                     // 套餐到期时间
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}

// Group 用户组
type Group struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	Name          string    `gorm:"size:64;not null" json:"name"`
	MaxStorage    int64     `json:"maxStorage"` // 最大存储字节 -1 无限
	ShareEnabled  bool      `gorm:"default:true" json:"shareEnabled"`
	WebDAVEnabled bool      `gorm:"default:true" json:"webdavEnabled"`
	SpeedLimit    int       `gorm:"default:0" json:"speedLimit"` // KB/s 0 不限
	CreatedAt     time.Time `json:"createdAt"`
}

// File 文件/目录
type File struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	OwnerID    uint           `gorm:"index;not null" json:"ownerId"`
	Name       string         `gorm:"size:255;not null" json:"name"`
	ParentID   *uint          `gorm:"index" json:"parentId"`
	IsDir      bool           `gorm:"default:false" json:"isDir"`
	Size       int64          `gorm:"default:0" json:"size"`
	PolicyID   uint           `gorm:"default:1" json:"policyId"`  // 存储策略
	SourceName string         `gorm:"size:255" json:"sourceName"` // 实际存储名（指向 FileObject.SourceName）
	Extension  string         `gorm:"size:32" json:"extension"`
	MimeType   string         `gorm:"size:128" json:"mimeType"`
	Hash       string         `gorm:"index;size:64" json:"-"` // 用于秒传
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// FileObject 物理文件对象：唯一由 (PolicyID, SourceName) 确定，维护引用计数。
// 当多条 File 记录（秒传/复制）共享同一物理存储时，refs>1；删除时仅减计数，到 0 才真正删物理文件。
type FileObject struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	PolicyID   uint      `gorm:"index:idx_obj_policy_src,unique;not null" json:"policyId"`
	SourceName string    `gorm:"index:idx_obj_policy_src,unique;size:255;not null" json:"sourceName"`
	Size       int64     `gorm:"default:0" json:"size"`
	Hash       string    `gorm:"index;size:64" json:"hash"` // 用于创建时的快速查找
	Refs       int64     `gorm:"default:1" json:"refs"`     // 引用计数
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// Folder 目录（与 File 分离，便于目录树查询）—— 这里用 File.IsDir 统一，Folder 仅作元数据
type Folder struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	FileID    uint      `gorm:"uniqueIndex;not null" json:"fileId"`
	CreatedAt time.Time `json:"createdAt"`
}

// FileVersion 文件历史版本
type FileVersion struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	FileID     uint      `gorm:"index;not null" json:"fileId"`
	Version    int       `gorm:"not null" json:"version"`
	Size       int64     `json:"size"`
	Hash       string    `gorm:"size:64" json:"hash"`
	SourceName string    `gorm:"size:255" json:"sourceName"`
	PolicyID   uint      `json:"policyId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// FileTag 文件标签
type FileTag struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	FileID    uint      `gorm:"index;not null" json:"fileId"`
	OwnerID   uint      `gorm:"index;not null" json:"ownerId"`
	Tag       string    `gorm:"index;size:64;not null" json:"tag"`
	Color     string    `gorm:"size:16" json:"color"`
	CreatedAt time.Time `json:"createdAt"`
}

// Share 分享
type Share struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	FileID      uint           `gorm:"index;not null" json:"fileId"`
	OwnerID     uint           `gorm:"index;not null" json:"ownerId"`
	Password    string         `gorm:"size:64" json:"-"` // 空则公开
	ExtractCode string         `gorm:"size:32;index" json:"extractCode"`
	Downloads   int            `gorm:"default:0" json:"downloads"`
	Views       int            `gorm:"default:0" json:"views"`
	ExpireAt    *time.Time     `json:"expireAt"`
	IsDir       bool           `json:"isDir"`
	CreatedAt   time.Time      `json:"createdAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// AuditLog 审计日志
type AuditLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"userId"`
	UserName  string    `gorm:"size:64" json:"userName"`
	Action    string    `gorm:"index;size:64" json:"action"`
	Target    string    `gorm:"size:256" json:"target"`
	IP        string    `gorm:"size:64" json:"ip"`
	UA        string    `gorm:"size:256" json:"ua"`
	Detail    string    `gorm:"type:text" json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

// Policy 存储策略
type Policy struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	Name      string         `gorm:"size:64;not null" json:"name"`
	Type      string         `gorm:"size:32;not null" json:"type"` // local|s3|oss|cos
	Config    string         `gorm:"type:text" json:"-"`           // JSON 加密
	IsDefault bool           `gorm:"default:false" json:"isDefault"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Setting 动态设置（KV）
type Setting struct {
	ID    uint   `gorm:"primarykey" json:"id"`
	Key   string `gorm:"uniqueIndex;size:128;not null" json:"key"`
	Value string `gorm:"type:text" json:"value"`
}

// Task 离线下载任务
type Task struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	OwnerID   uint           `gorm:"index;not null" json:"ownerId"`
	Type      string         `gorm:"size:16" json:"type"` // http|bt
	URL       string         `gorm:"type:text" json:"url"`
	Status    int            `gorm:"default:0" json:"status"` // 0等待 1进行 2完成 3失败
	Progress  int            `gorm:"default:0" json:"progress"`
	ParentID  *uint          `gorm:"index" json:"parentId"`
	Error     string         `gorm:"type:text" json:"error"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Notification 通知
type Notification struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	Title     string    `gorm:"size:255" json:"title"`
	Body      string    `gorm:"type:text" json:"body"`
	Type      string    `gorm:"size:32" json:"type"`     // info|success|warning|error
	Category  string    `gorm:"size:32" json:"category"` // system|task|share|security
	Read      bool      `gorm:"default:false" json:"read"`
	Meta      string    `gorm:"type:text" json:"meta"` // JSON
	CreatedAt time.Time `json:"createdAt"`
}

// OAuthApp OAuth2 应用
type OAuthApp struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	ClientID     string    `gorm:"uniqueIndex;size:64;not null" json:"clientId"`
	ClientSecret string    `gorm:"size:128;not null" json:"-"`
	Name         string    `gorm:"size:128" json:"name"`
	RedirectURIs string    `gorm:"type:text" json:"redirectUris"` // JSON array
	UserID       uint      `gorm:"index" json:"userId"`
	CreatedAt    time.Time `json:"createdAt"`
}

// OAuthCode 授权码
type OAuthCode struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	Code        string    `gorm:"uniqueIndex;size:64;not null" json:"code"`
	AppID       uint      `gorm:"index;not null" json:"appId"`
	UserID      uint      `gorm:"not null" json:"userId"`
	Scope       string    `gorm:"size:256" json:"scope"`
	RedirectURI string    `gorm:"size:512" json:"redirectUri"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Used        bool      `gorm:"default:false" json:"used"`
}

// AccessToken OAuth2 访问令牌
type AccessToken struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Token     string    `gorm:"uniqueIndex;size:128;not null" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	AppID     uint      `gorm:"index" json:"appId"`
	Scope     string    `gorm:"size:256" json:"scope"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// PersonalAccessToken 个人访问令牌
type PersonalAccessToken struct {
	ID         uint       `gorm:"primarykey" json:"id"`
	UserID     uint       `gorm:"index;not null" json:"userId"`
	Name       string     `gorm:"size:128" json:"name"`
	Token      string     `gorm:"uniqueIndex;size:128;not null" json:"-"`
	Prefix     string     `gorm:"size:16" json:"prefix"` // 显示前8位
	Scope      string     `gorm:"size:256" json:"scope"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// IPBan IP 黑名单
type IPBan struct {
	ID        uint       `gorm:"primarykey" json:"id"`
	IP        string     `gorm:"uniqueIndex;size:64;not null" json:"ip"`
	Reason    string     `gorm:"size:255" json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Auto      bool       `gorm:"default:false" json:"auto"`
	CreatedAt time.Time  `json:"createdAt"`
}

// ---- 付费体系 ----

// Plan 套餐定义
type Plan struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	Name           string    `gorm:"size:64;not null" json:"name"`            // ultra|pro|promax
	DisplayName    string    `gorm:"size:128" json:"displayName"`              // Ultra / Pro / Pro Max
	Price          int       `gorm:"not null" json:"price"`                    // 分为单位：5900=59元
	Currency       string    `gorm:"size:8;default:'CNY'" json:"currency"`
	DurationMonths int       `gorm:"not null" json:"durationMonths"`           // 12 / 12 / 36
	MaxStorage     int64     `gorm:"default:-1" json:"maxStorage"`             // -1 无限
	ShareEnabled   bool      `gorm:"default:true" json:"shareEnabled"`
	WebDAVEnabled  bool      `gorm:"default:true" json:"webdavEnabled"`
	SpeedLimit     int       `gorm:"default:0" json:"speedLimit"`              // KB/s 0 不限
	Features       string    `gorm:"type:text" json:"-"`                      // JSON: 功能列表
	IsActive       bool      `gorm:"default:true" json:"isActive"`
	SortOrder      int       `gorm:"default:0" json:"sortOrder"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// RedemptionCode 兑换码
type RedemptionCode struct {
	ID        uint       `gorm:"primarykey" json:"id"`
	Code      string     `gorm:"uniqueIndex;size:32;not null" json:"code"` // NB-XXXX-XXXX-XXXX
	PlanID    uint       `gorm:"index;not null" json:"planId"`
	MaxUses   int        `gorm:"default:0" json:"maxUses"`  // 0=不限次数
	UsedCount int        `gorm:"default:0" json:"usedCount"`
	IsActive  bool       `gorm:"default:true" json:"isActive"`
	CreatedBy uint       `json:"createdBy"`
	Note      string     `gorm:"size:255" json:"note"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Subscription 用户订阅（一次兑换一条记录）
type Subscription struct {
	ID        uint       `gorm:"primarykey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"userId"`
	UserName  string     `gorm:"size:64" json:"userName"`
	PlanID    uint       `gorm:"index;not null" json:"planId"`
	PlanName  string     `gorm:"size:64" json:"planName"`
	CodeID    uint       `gorm:"index" json:"codeId"`
	Code      string     `gorm:"size:32" json:"code"`
	StartTime time.Time  `json:"startTime"`
	EndTime   time.Time  `json:"endTime"`
	Status    string     `gorm:"size:16;default:'active'" json:"status"` // active|expired|cancelled
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// RedemptionLog 兑换审计日志
type RedemptionLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CodeID    uint      `gorm:"index" json:"codeId"`
	Code      string    `gorm:"size:32;index" json:"code"`
	UserID    uint      `gorm:"index" json:"userId"`
	UserName  string    `gorm:"size:64" json:"userName"`
	PlanID    uint      `json:"planId"`
	PlanName  string    `gorm:"size:64" json:"planName"`
	IP        string    `gorm:"size:64" json:"ip"`
	UA        string    `gorm:"size:256" json:"ua"`
	CreatedAt time.Time `json:"createdAt"`
}

// AutoMigrate 自动建表
func AutoMigrate() error {
	if db.DB == nil {
		return errDBNotReady
	}
	if err := db.DB.AutoMigrate(
		&User{}, &Group{}, &File{}, &FileObject{}, &Folder{}, &FileVersion{}, &FileTag{}, &Share{}, &Policy{}, &Setting{}, &Task{},
		&Session{}, &EmailCode{}, &Plugin{}, &AuditLog{},
		&Notification{}, &OAuthApp{}, &OAuthCode{}, &AccessToken{}, &PersonalAccessToken{},
		&IPBan{},
		&Plan{}, &RedemptionCode{}, &Subscription{}, &RedemptionLog{},
	); err != nil {
		return err
	}
	createAuditLogRetention()
	return nil
}

// createAuditLogRetention MySQL/SQLite 30 天过期任务（尽力而为，失败忽略）
func createAuditLogRetention() {
	type dbConf struct{ Type string }
	sql, _ := db.DB.DB()
	if sql == nil {
		return
	}
	driver := db.DB.Dialector.Name()
	switch driver {
	case "mysql":
		db.DB.Exec(`CREATE EVENT IF NOT EXISTS audit_log_retention ON SCHEDULE EVERY 1 DAY STARTS CURRENT_TIMESTAMP ON COMPLETION PRESERVE DO DELETE FROM audit_logs WHERE created_at < DATE_SUB(NOW(), INTERVAL 30 DAY)`)
	case "sqlite":
		db.DB.Exec(`CREATE TRIGGER IF NOT EXISTS audit_log_retention_insert AFTER INSERT ON audit_logs BEGIN DELETE FROM audit_logs WHERE created_at < datetime('now', '-30 days'); END`)
	}
}

var errDBNotReady = gorm.ErrInvalidDB
