// Package migrations 提供数据库版本化迁移。
// 在 AutoMigrate 之后运行：维护 schema_migrations 表，按序执行未执行的迁移。
package migrations

import (
	"errors"
	"fmt"
	"time"

	"github.com/nebula-drive/nebula/models"
	"github.com/nebula-drive/nebula/pkg/plugin"
	"gorm.io/gorm"
)

// SchemaMigration 迁移版本记录表
type SchemaMigration struct {
	Version   int       `gorm:"primarykey"`
	Name      string    `gorm:"size:128"`
	AppliedAt time.Time `json:"appliedAt"`
}

// Migration 单个迁移
type Migration struct {
	Version int
	Name    string
	Up      func(db *gorm.DB) error
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "init",
		Up:      func(db *gorm.DB) error { return nil },
	},
	{
		Version: 2,
		Name:    "add_user_preferences",
		Up: func(db *gorm.DB) error {
			// 用户偏好字段（PreferLang, ThemeMode, LogoEgg, SelectMode, TwoFactorHinted）
			return db.AutoMigrate(&models.User{})
		},
	},
	{
		Version: 3,
		Name:    "add_notifications",
		Up: func(db *gorm.DB) error {
			return db.AutoMigrate(&models.Notification{})
		},
	},
	{
		Version: 4,
		Name:    "add_oauth_and_pat",
		Up: func(db *gorm.DB) error {
			return db.AutoMigrate(
				&models.OAuthApp{},
				&models.OAuthCode{},
				&models.AccessToken{},
				&models.PersonalAccessToken{},
			)
		},
	},
	{
		Version: 5,
		Name:    "add_rate_limit_defaults",
		Up: func(db *gorm.DB) error {
			defaults := map[string]string{
				"rate_limit.login_per_min":   "10",
				"rate_limit.upload_per_min":  "60",
				"rate_limit.default_per_min": "600",
			}
			for k, v := range defaults {
				var s models.Setting
				if err := db.Where("`key` = ?", k).First(&s).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					if err := db.Create(&models.Setting{Key: k, Value: v}).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
}

// Run 执行所有未执行的迁移
func Run(d *gorm.DB) error {
	if d == nil {
		return errors.New("db is nil")
	}
	if err := d.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	var latest SchemaMigration
	curVersion := 0
	if err := d.Order("version desc").First(&latest).Error; err == nil {
		curVersion = latest.Version
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	for _, m := range migrations {
		if m.Version <= curVersion {
			continue
		}
		if err := m.Up(d); err != nil {
			return fmt.Errorf("migration %d (%s) failed: %w", m.Version, m.Name, err)
		}
		if err := d.Create(&SchemaMigration{
			Version:   m.Version,
			Name:      m.Name,
			AppliedAt: time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("record migration %d: %w", m.Version, err)
		}
	}

	plugin.Fire(plugin.HookDBMigrate, map[string]any{
		"fromVersion": curVersion,
		"toVersion":   latestMigrationVersion(),
	})
	return nil
}

func latestMigrationVersion() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].Version
}
