package database

import (
	"fmt"
	"log"
	"sync"

	"github.com/thun888/apibox/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DB 全局数据库实例，模块通过 database.DB 直接使用
var DB *gorm.DB

var (
	migrators   []interface{}
	migratorsMu sync.Mutex
)

// RegisterModel 收集各模块的 GORM Model（在模块 init() 中调用，在 DB.Init 之前）
func RegisterModel(model interface{}) {
	migratorsMu.Lock()
	migrators = append(migrators, model)
	migratorsMu.Unlock()
}

// Init 初始化数据库连接
func Init(cfg config.DatabaseConfig) error {
	level := parseLogLevel(cfg.LogLevel)
	dialector, err := resolveDialector(cfg.Driver, cfg.DSN)
	if err != nil {
		return fmt.Errorf("resolve driver: %w", err)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormlogger.Default.LogMode(level),
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	DB = db
	log.Printf("Database connected: driver=%s dsn=%s", cfg.Driver, maskDSN(cfg.Driver, cfg.DSN))

	if cfg.AutoMigrate {
		if err := autoMigrate(); err != nil {
			return fmt.Errorf("auto migrate: %w", err)
		}
	}

	return nil
}

// Close 关闭数据库连接
func Close() error {
	if DB == nil {
		return nil
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// autoMigrate 统一迁移所有已注册的 Model
func autoMigrate() error {
	migratorsMu.Lock()
	models := make([]interface{}, len(migrators))
	copy(models, migrators)
	migratorsMu.Unlock()

	if len(models) == 0 {
		log.Println("No models registered for migration")
		return nil
	}

	if err := DB.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	log.Printf("Auto migrate completed: %d model(s)", len(models))
	return nil
}

func resolveDialector(driver, dsn string) (gorm.Dialector, error) {
	switch driver {
	case "mysql":
		return mysql.Open(dsn), nil
	case "postgres":
		return postgres.Open(dsn), nil
	case "sqlite":
		return sqlite.Open(dsn), nil
	default:
		return nil, fmt.Errorf("unsupported driver: %s", driver)
	}
}

func parseLogLevel(level string) gormlogger.LogLevel {
	switch level {
	case "silent":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "warn":
		return gormlogger.Warn
	case "info":
		return gormlogger.Info
	default:
		return gormlogger.Warn
	}
}

func maskDSN(driver, dsn string) string {
	if driver == "sqlite" {
		return dsn
	}
	return "***"
}
