// Package model 负责数据库初始化与迁移：切库、建表、方言兼容工具。
//
// 主键策略：全系统雪花 ID（应用层生成，去自增）。InitDB 注册全局 GORM Create 回调，
// 为任何带 ID int64 且为 0 的模型透明赋值。
package model

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/pkg/likeescape"
	"sili-smart-hr/backend/internal/pkg/snowflake"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	sqlitePath = "data/sili-smart-hr.db"
)

type DatabaseType string

const (
	DBSQLite   DatabaseType = "sqlite"
	DBPostgres DatabaseType = "postgres"
	DBMySQL    DatabaseType = "mysql"
)

// current 是启动时锁定的主库类型，进程级不可变。
var current DatabaseType

func Current() DatabaseType    { return current }
func Using(t DatabaseType) bool { return current == t }

// 进程级运行时常量，供系统状态摘要（GET /api/system/status）消费。
// StartedAt 由 main 在启动序列赋值，零值表示尚未赋值；Version 预留 ldflags 覆盖：
//
//	go build -ldflags "-X sili-smart-hr/backend/internal/model.Version=v1.0.0"
var (
	StartedAt time.Time
	Version   = "v0.1.0"
)

// InitDB 按配置切库、建表。
func InitDB(cfg *config.Config) (*gorm.DB, error) {
	if err := snowflake.Init(cfg.Snowflake.NodeID); err != nil {
		return nil, fmt.Errorf("init snowflake: %w", err)
	}

	dialector, dbType, err := chooseDB(cfg)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Warn),
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	current = dbType
	registerSnowflakeIDCallback(db)

	// SQLite 不设连接池，保留单写特性。
	if dbType != DBSQLite {
		if err := applyPool(db, cfg); err != nil {
			return nil, err
		}
	}

	if err := migrateDB(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	slog.Info("database ready", "dialect", dbType, "snowflake_node", cfg.Snowflake.NodeID)
	return db, nil
}

// chooseDB 按 SQL_DSN 前缀选 dialector。PG 关 PreferSimpleProtocol 规避 stale prepared plan；
// MySQL 自动补 parseTime 以扫描 time.Time。
func chooseDB(cfg *config.Config) (gorm.Dialector, DatabaseType, error) {
	switch {
	case cfg.IsSQLite():
		if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
			return nil, "", fmt.Errorf("mkdir data dir: %w", err)
		}
		return sqlite.Open(sqlitePath), DBSQLite, nil
	case cfg.IsPostgres():
		return postgres.New(postgres.Config{
			DSN:                  cfg.SQL.DSN,
			PreferSimpleProtocol: true,
		}), DBPostgres, nil
	default:
		return mysql.Open(ensureParseTime(cfg.SQL.DSN)), DBMySQL, nil
	}
}

// ensureParseTime 保证 MySQL DSN 含 parseTime=true。
func ensureParseTime(dsn string) string {
	if strings.Contains(dsn, "parseTime") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&parseTime=true"
	}
	return dsn + "?parseTime=true"
}

func applyPool(db *gorm.DB, cfg *config.Config) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get underlying sql.DB: %w", err)
	}
	if cfg.SQL.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.SQL.MaxIdleConns)
	}
	if cfg.SQL.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.SQL.MaxOpenConns)
	}
	if cfg.SQL.MaxLifetime != "" {
		if d, perr := time.ParseDuration(cfg.SQL.MaxLifetime); perr == nil {
			sqlDB.SetConnMaxLifetime(d)
		}
	}
	return nil
}

// QuoteIdent 按方言包裹标识符（PG 双引号，SQLite/MySQL 反引号），仅手写含保留字列名的原生 SQL 时用。
func QuoteIdent(name string) string {
	if Using(DBPostgres) {
		return "\"" + name + "\""
	}
	return "`" + name + "`"
}

// BoolLit 返回方言布尔字面量，优先用 GORM Where("col = ?", true)，仅手写原生 SQL 时用。
func BoolLit(b bool) string {
	if Using(DBPostgres) {
		if b {
			return "true"
		}
		return "false"
	}
	if b {
		return "1"
	}
	return "0"
}

// EscapeLike 转义用户输入里的 LIKE 通配符（%、_、\），配合 ESCAPE '\' 字面匹配。
// 顺序敏感：先转义反斜杠自身，再转义 % 与 _，避免二次替换。调用方必须在 LIKE 后写 ESCAPE '\'。
// 存在历史兼容别名：repository 曾经 import model 仅为这一个函数，后上提 pkg/likeescape
// 断掉 model→repository 反向依赖的环；保留本别名让既有调用方零改动。
func EscapeLike(s string) string {
	return likeescape.EscapeLike(s)
}

// registerSnowflakeIDCallback 注册全局 Create 回调，在 gorm:create 之前为 ID int64 且为 0 的模型赋雪花 ID。
func registerSnowflakeIDCallback(db *gorm.DB) {
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignSnowflakeID(tx.Statement.Dest)
	})
}

func assignSnowflakeID(dest any) {
	v := reflect.ValueOf(dest)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		setInt64IDIfZero(v)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				setInt64IDIfZero(elem)
			}
		}
	}
}

// setInt64IDIfZero 在 ID 字段为 int64 且为 0 时赋雪花 ID。
func setInt64IDIfZero(v reflect.Value) {
	f := v.FieldByName("ID")
	if !f.IsValid() || !f.CanSet() || f.Kind() != reflect.Int64 {
		return
	}
	if f.Int() == 0 {
		f.SetInt(snowflake.NextID())
	}
}
