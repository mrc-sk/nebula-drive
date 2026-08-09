package models

import "time"

// Session 登录会话（支持单设备登录：登录时清除该用户其他会话）
type Session struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"userId"`
	Token     string    `gorm:"uniqueIndex;size:255;not null" json:"-"`
	SessionID string    `gorm:"uniqueIndex;size:64;not null" json:"sessionId"`
	IP        string    `gorm:"size:64" json:"ip"`
	UA        string    `gorm:"size:255" json:"ua"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// EmailCode 邮箱验证码（注册激活/找回密码/换绑）
type EmailCode struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Email     string    `gorm:"index;size:128;not null" json:"email"`
	Code      string    `gorm:"size:16;not null" json:"-"`
	Purpose   string    `gorm:"size:32;not null" json:"purpose"` // activate|reset|bind
	ExpiresAt time.Time `json:"expiresAt"`
	Used      bool      `gorm:"default:false" json:"used"`
	CreatedAt time.Time `json:"createdAt"`
}

// Plugin 后台插件登记（轻量插件机制：事件钩子 + 开关）
type Plugin struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Name      string    `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Title     string    `gorm:"size:128" json:"title"`
	Enabled   bool      `gorm:"default:false" json:"enabled"`
	Config    string    `gorm:"type:text" json:"-"` // JSON
	CreatedAt time.Time `json:"createdAt"`
}
