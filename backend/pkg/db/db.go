package db

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nebula-drive/nebula/conf"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
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
	// 覆盖全局实例前先关闭旧的连接池。
	// 安装向导的「测试连接」会反复调用 Init（每次换一个 DSN 试），
	// 旧实现直接赋值 DB = g，旧连接池既不关闭也不释放 → 每次点击泄漏一个池。
	if DB != nil {
		if old, oErr := DB.DB(); oErr == nil && old != nil {
			_ = old.Close()
		}
	}
	DB = g
	return nil
}

// Get 返回 DB
func Get() *gorm.DB { return DB }

// Close 关闭当前数据库连接池。
// 用途：测试清理（Windows 上句柄不释放会导致 t.TempDir() 删除失败，
// 使断言全过的测试被判 FAIL），以及进程优雅退出。
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	if sqlDB == nil {
		return nil
	}
	return sqlDB.Close()
}
