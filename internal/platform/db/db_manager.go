package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"antigravity-proxy/internal/platform/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	GlobalDB *gorm.DB
	dbMutex  sync.Mutex
)

// Config 封装数据库初始化所需的配置
type Config struct {
	DataDir        string
	RemoteEnabled  bool
	RemoteHost     string
	RemotePort     string
	RemoteUser     string 
	RemotePassword string
	RemoteDBName   string
	DSN            string
}

// InitDB 根据配置初始化平台数据库连接
func InitDB(cfg Config) error {
	dbMutex.Lock()
	defer dbMutex.Unlock()

	if GlobalDB != nil {
		return nil
	}

	var db *gorm.DB
	var err error

	// 远程 MySQL 模式: 用户指定了 RemoteEnabled，或指定了 DSN/环境变量
	isMySQL := cfg.RemoteEnabled || cfg.DSN != "" || os.Getenv("ANTIGRAVITY_MYSQL_ENABLED") == "true" || os.Getenv("ANTIGRAVITY_MYSQL_DSN") != ""
	if isMySQL {
		var dsn string
		if cfg.DSN != "" {
			dsn = cfg.DSN
		} else if envDsn := os.Getenv("ANTIGRAVITY_MYSQL_DSN"); envDsn != "" {
			dsn = envDsn
		} else {
			host := cfg.RemoteHost
			if host == "" {
				host = os.Getenv("ANTIGRAVITY_MYSQL_HOST")
			}
			if host == "" {
				host = "127.0.0.1"
			}
			port := cfg.RemotePort
			if port == "" {
				port = os.Getenv("ANTIGRAVITY_MYSQL_PORT")
			}
			if port == "" {
				port = "39306"
			}
			user := cfg.RemoteUser
			if user == "" {
				user = os.Getenv("ANTIGRAVITY_MYSQL_USER")
			}
			if user == "" {
				user = "root"
			}
			pwd := cfg.RemotePassword
			if pwd == "" {
				pwd = os.Getenv("ANTIGRAVITY_MYSQL_PASSWORD")
			}
			if pwd == "" {
				pwd = "ProxySub2026SecDbPass99"
			}
			dbName := cfg.RemoteDBName
			if dbName == "" {
				dbName = os.Getenv("ANTIGRAVITY_MYSQL_DB")
			}
			if dbName == "" {
				dbName = "antigravity_platform"
			}

			dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
				user, pwd, host, port, dbName)
			log.Printf("Connecting to MySQL: %s:%s/%s", host, port, dbName)
		}

		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Warn),
		})
		if err != nil {
			return fmt.Errorf("failed to connect to mysql: %w", err)
		}
	} else {
		// 回退使用本地 SQLite 模式
		dbPath := filepath.Join(cfg.DataDir, "antigravity_platform.db")
		log.Printf("Using local SQLite: %s", dbPath)

		db, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Warn),
		})
		if err != nil {
			return fmt.Errorf("failed to connect to local sqlite: %w", err)
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// 设置连接池参数
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 自动迁移
	err = db.AutoMigrate(
		&model.User{},
		&model.Plan{},
		&model.Order{},
		&model.APIKey{},
		&model.Setting{},
		&model.RequestLog{},
	)
	if err != nil {
		return fmt.Errorf("failed to auto migrate tables: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping platform database: %w", err)
	}

	GlobalDB = db
	log.Println("Platform database initialized successfully")
	return nil
}

// CloseDB 关闭数据库连接
func CloseDB() {
	dbMutex.Lock()
	defer dbMutex.Unlock()
	if GlobalDB != nil {
		sqlDB, err := GlobalDB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		GlobalDB = nil
	}
}
