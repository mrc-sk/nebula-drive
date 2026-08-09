package db

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nebula-drive/nebula/conf"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 全局 DB 实例
var DB *gorm.DB

// Init 根据配置建立数据库连接
func Init(c *conf.DBConfig) error {
	if c == nil {
		return errors.New("db config is nil")
	}
	var dialector gorm.Dialector
	switch c.Type {
	case "sqlite":
		dsn := c.File
		if dsn == "" {
			dsn = "data/nebula.db"
		}
		dialector = sqlite.Open(dsn)
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			c.User, c.Password, c.Host, c.Port, c.Name)
		dialector = mysql.Open(dsn)
	case "postgres":
		dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=Asia/Shanghai",
			c.Host, c.Port, c.User, c.Password, c.Name)
		dialector = postgres.Open(dsn)
	default:
		return fmt.Errorf("unsupported db type: %s", c.Type)
	}
	g, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond, // 慢查询阈值 200ms
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			},
		),
	})
	if err != nil {
		return err
	}
	// 连接池调优
	if sqlDB, sErr := g.DB(); sErr == nil && sqlDB != nil {
		sqlDB.SetMaxOpenConns(50)
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
	}
	DB = g
	return nil
}

// Get 返回 DB
func Get() *gorm.DB { return DB }
