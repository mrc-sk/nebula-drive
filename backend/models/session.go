package models

import (
	"strings"
	"time"
)

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

// Plugin 已安装的插件。
//
// 一个 Plugin 记录 = 一个插件目录 + 一个可独立启停的子进程。
// 热加载的含义：改 enabled 只是开关子进程，宿主服务不重启。
type Plugin struct {
	ID      uint   `gorm:"primarykey" json:"id"`
	Name    string `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Title   string `gorm:"size:128" json:"title"`
	Enabled bool   `gorm:"default:false" json:"enabled"`
	// Config 插件配置（JSON 字符串），在 init 握手下发给插件。
	// 不加 json:"-"：管理端需要能读写它（此前该字段完全不可见，
	// 插件作者配不了任何东西）。
	Config string `gorm:"type:text" json:"config"`
	// 冗余自 manifest —— 列表页不想为了显示版本号去读磁盘
	Version     string `gorm:"size:32" json:"version"`
	Author      string `gorm:"size:64" json:"author"`
	Description string `gorm:"size:512" json:"description"`
	License     string `gorm:"size:64" json:"license"`
	Homepage    string `gorm:"size:256" json:"homepage"`
	// Manifest 原始 JSON 文本，保留原样以便重载时重解析。
	Manifest string `gorm:"type:text" json:"manifest"`
	// Hooks 声明的钩子，逗号分隔。用于列表页展示与重载时避免依赖磁盘。
	Hooks string `gorm:"size:512" json:"hooks"`
	// InstallSource 来源标识：local / url:<host> / store:<id>，便于追溯。
	InstallSource string `gorm:"size:128" json:"installSource"`
	// InstalledBy 安装者用户 ID。
	InstalledBy uint `gorm:"index" json:"installedBy"`
	// LastError 最近一次启动/运行错误，供管理端展示。
	LastError string `gorm:"type:text" json:"lastError"`
	// Restarts 累计自动重启次数（便于发现不稳定插件）。
	Restarts int `gorm:"default:0" json:"restarts"`
	// AgreementVersion 安装时管理员同意的插件协议版本。
	// 协议升级后需要重新同意才能启用 —— 见 controllers 的 requireAgreement。
	AgreementVersion int `gorm:"default:0" json:"agreementVersion"`
	InstalledAt time.Time `json:"installedAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

// PluginHookList 返回 Hooks 字段的切片形式。
func (p *Plugin) PluginHookList() []string {
	if p.Hooks == "" {
		return []string{}
	}
	return splitAndTrim(p.Hooks)
}

// Plugin 安装来源常量。
const (
	PluginSourceLocal = "local"
	PluginSourceURL   = "url"
	PluginSourceStore = "store"
)

// splitAndTrim 逗号分隔字符串 → 去空白的切片。
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
