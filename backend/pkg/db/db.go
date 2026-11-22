package db

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/nebula-drive/nebula/conf"
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
		// 相对路径基于数据目录解析，并确保父目录存在。
		//
		// 为什么必须这么做：安装向导的默认值是相对路径 "data/nebula.db"，
		// 而 SQLite 会按**进程当前工作目录**解释它。容器里 WORKDIR 是 /data，
		// 于是它实际去找 /data/data/nebula.db —— 目录不存在，SQLite 报
		// "unable to open database file: out of memory (14)"（错误码 14 是
		// CANTOPEN，"out of memory" 只是 SQLite 的误导性默认文案）。
		//
		// 解析到数据目录后，路径与工作目录无关：裸机、容器、systemd
		// 三种部署下都指向同一个数据目录，用户填相对或绝对路径都对。
		if !filepath.IsAbs(dsn) {
			dsn = filepath.Join(conf.DataDir(), dsn)
		}
		if dir := filepath.Dir(dsn); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("创建数据库目录失败 %s: %w", dir, err)
			}
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
