package database

import (
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"sync"

	"github.com/thun888/apibox/internal/config"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var logger = slog.New(slog.NewTextHandler(os.Stdout, nil)).With("module", "database")

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
	logger.Info("Database connected", "driver", cfg.Driver, "dsn", maskDSN(cfg.Driver, cfg.DSN))

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
		logger.Info("No models registered for migration")
		return nil
	}

	if err := DB.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	logger.Info("Auto migrate completed", "count", len(models))
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

// defaultNaming 默认命名策略（与 GORM 默认一致：蛇形 + 复数 + 小写）
var defaultNaming = schema.NamingStrategy{}

// BuildTableName 调用 GORM 默认命名策略生成表名，并添加模块前缀
//
//	BuildTableName(&Vote{}, "starvote_")  →  "starvote_votes"
//	BuildTableName(&Rating{}, "starvote_") →  "starvote_ratings"
func BuildTableName(model interface{}, prefix string) string {
	t := reflect.TypeOf(model)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return prefix + defaultNaming.TableName(t.Name())
}
