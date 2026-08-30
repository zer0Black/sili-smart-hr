package model

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
)

// setCurrent 在测试里临时切换主库类型，测试结束自动还原。
func setCurrent(t *testing.T, dbType DatabaseType) {
	t.Helper()
	prev := current
	current = dbType
	t.Cleanup(func() { current = prev })
}

func TestUsing(t *testing.T) {
	setCurrent(t, DBPostgres)
	if !Using(DBPostgres) {
		t.Fatal("expected Using(DBPostgres) true")
	}
	if Using(DBMySQL) {
		t.Fatal("expected Using(DBMySQL) false")
	}
}

func TestQuoteIdent(t *testing.T) {
	setCurrent(t, DBPostgres)
	if got := QuoteIdent("order"); got != "\"order\"" {
		t.Fatalf("postgres quote: %q", got)
	}
	setCurrent(t, DBMySQL)
	if got := QuoteIdent("order"); got != "`order`" {
		t.Fatalf("mysql quote: %q", got)
	}
}

func TestBoolLit(t *testing.T) {
	setCurrent(t, DBPostgres)
	if BoolLit(true) != "true" || BoolLit(false) != "false" {
		t.Fatalf("postgres bool lit mismatch: %q/%q", BoolLit(true), BoolLit(false))
	}
	setCurrent(t, DBSQLite)
	if BoolLit(true) != "1" || BoolLit(false) != "0" {
		t.Fatalf("sqlite bool lit mismatch: %q/%q", BoolLit(true), BoolLit(false))
	}
}

func TestEscapeLike(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},                   // 无通配符原样返回
		{"a_b", `a\_b`},                  // 下划线转义
		{"a%c", `a\%c`},                  // 百分号转义
		{`a\b`, `a\\b`},                  // 反斜杠先转义，避免二次替换
		{"50%", `50\%`},                  // 含百分号的字面搜索（如折扣）
		{`a\_b`, `a\\\_b`},               // 反斜杠与下划线同时出现，反斜杠先翻倍再转义下划线
	}
	for _, c := range cases {
		if got := EscapeLike(c.in); got != c.want {
			t.Fatalf("EscapeLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnsureParseTime(t *testing.T) {
	cases := []struct{ in, want string }{
		// 无 query 串：补 ?parseTime=true
		{"user:pass@tcp(host:3306)/db", "user:pass@tcp(host:3306)/db?parseTime=true"},
		// 已有 query 串：补 &parseTime=true
		{"user:pass@tcp(host:3306)/db?charset=utf8mb4", "user:pass@tcp(host:3306)/db?charset=utf8mb4&parseTime=true"},
		// 已含 parseTime：不动
		{"user:pass@tcp(host:3306)/db?parseTime=true", "user:pass@tcp(host:3306)/db?parseTime=true"},
	}
	for _, c := range cases {
		if got := ensureParseTime(c.in); got != c.want {
			t.Fatalf("ensureParseTime(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestMigrateDBIdempotent(t *testing.T) {
	// migrateDB 的 dimension_settings seed 调 snowflake.NextID()，须先初始化节点。
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// 再次迁移应幂等，不报错。
	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !db.Migrator().HasTable(&domain.Account{}) {
		t.Fatal("expected accounts table exists")
	}
	if !db.Migrator().HasTable(&domain.SystemInitialization{}) {
		t.Fatal("expected system_initializations table exists")
	}
}

// TestMigrateDimensions 验证 dimension 域两表建表、全列存在与首启 seed。
// 覆盖 BR1（dimensions 含 deleted_at 软删除列）、BR2（seed 阈值 10/5）、BR3（anchor not null 列存在）。
func TestMigrateDimensions(t *testing.T) {
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 两表存在。
	if !db.Migrator().HasTable(&domain.Dimension{}) {
		t.Fatal("expected dimensions table exists")
	}
	if !db.Migrator().HasTable(&domain.DimensionSetting{}) {
		t.Fatal("expected dimension_settings table exists")
	}

	// dimensions 全列（含 deleted_at 软删除列，BR1）。
	dimCols := []string{
		"id", "code", "name", "module_code", "group_code", "data_source",
		"prompt", "anchor", "weight", "include_overview", "enabled",
		"is_reference", "description", "version", "deleted_at", "created_at", "updated_at",
	}
	for _, col := range dimCols {
		if !db.Migrator().HasColumn(&domain.Dimension{}, col) {
			t.Fatalf("expected dimensions.%s column exists", col)
		}
	}

	// dimension_settings 全列。
	setCols := []string{
		"id", "active_threshold", "low_frequency_threshold", "created_at", "updated_at",
	}
	for _, col := range setCols {
		if !db.Migrator().HasColumn(&domain.DimensionSetting{}, col) {
			t.Fatalf("expected dimension_settings.%s column exists", col)
		}
	}

	// seed：1 行，阈值 10/5（BR2）。再次 migrateDB 验证幂等不重复写入。
	var setting domain.DimensionSetting
	if err := db.First(&setting).Error; err != nil {
		t.Fatalf("expected seeded dimension_settings row, got: %v", err)
	}
	if setting.ActiveThreshold != 10 || setting.LowFrequencyThreshold != 5 {
		t.Fatalf("seed thresholds: active=%d low=%d, want 10/5", setting.ActiveThreshold, setting.LowFrequencyThreshold)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var count int64
	if err := db.Model(&domain.DimensionSetting{}).Count(&count).Error; err != nil {
		t.Fatalf("count dimension_settings: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 dimension_settings row after idempotent rerun, got %d", count)
	}
}

// TestMigrateSessionFeatureClientOnLegacyTable 存量库升级回归：旧结构表（无
// client 列）含数据行时 migrateDB 不得因 NOT NULL 加列失败，且加列后旧行
// client 为空串、migrateDB 可重跑（幂等）。
func TestMigrateSessionFeatureClientOnLegacyTable(t *testing.T) {
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 构造旧结构表：无 client 列 + 一行存量数据（升级前已跑过 TECH_003 抽取的形态）。
	if err := db.Exec(`CREATE TABLE session_features (
		id integer PRIMARY KEY, session_key text NOT NULL, token_name text NOT NULL,
		status text NOT NULL, turn_count integer NOT NULL,
		first_turn_at datetime NOT NULL, last_turn_at datetime NOT NULL,
		profile_json text NOT NULL, error_code text NOT NULL,
		created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`INSERT INTO session_features (id, session_key, token_name, status,
		turn_count, first_turn_at, last_turn_at, profile_json, error_code)
		VALUES (1, 's-legacy', '张三', 'success', 5, '2026-01-01 00:00:00',
		'2026-01-01 01:00:00', '{}', '')`).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := migrateDB(db); err != nil {
		t.Fatalf("存量表迁移失败（NOT NULL 加列须有钩子兜底）: %v", err)
	}
	var client string
	if err := db.Raw("SELECT client FROM session_features WHERE id = 1").Scan(&client).Error; err != nil {
		t.Fatalf("读回 client 列: %v", err)
	}
	if client != "" {
		t.Errorf("存量行 client = %q, want 空串（无探测输入的历史行）", client)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate 应幂等: %v", err)
	}
}

// TestProcessConstants 固化进程级常量的默认值与零值语义：
// Version 预留 ldflags 覆盖入口（默认 v0.1.0），StartedAt 零值表示 main 尚未赋值。
func TestProcessConstants(t *testing.T) {
	if Version != "v0.1.0" {
		t.Fatalf("Version = %q, want v0.1.0", Version)
	}
	if !StartedAt.IsZero() {
		t.Fatalf("StartedAt = %v, want zero before main assigns", StartedAt)
	}
}

// TestChooseDBDialectors 按 SQL_DSN 前缀选库的分派逻辑（不真连库，只断言类型归属）。
// SQLite 分支会建 data 目录，chdir 临时目录防污染包工作区。
func TestChooseDBDialectors(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	cases := []struct {
		name string
		dsn  string
		want DatabaseType
	}{
		{"空 DSN 回退 SQLite", "", DBSQLite},
		{"local 前缀 SQLite", "local", DBSQLite},
		{"postgres 前缀 PG", "postgres://u:p@h/db", DBPostgres},
		{"postgresql 前缀 PG", "postgresql://u:p@h/db", DBPostgres},
		{"其余走 MySQL", "user:pass@tcp(h:3306)/db", DBMySQL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got, err := chooseDB(&config.Config{SQL: config.SQLConfig{DSN: c.dsn}})
			if err != nil {
				t.Fatalf("chooseDB: %v", err)
			}
			if got != c.want {
				t.Errorf("dbType = %q, want %q", got, c.want)
			}
		})
	}
}

// TestInitDBSQLite 全链初始化：SQLite DSN（local 前缀）下 InitDB 建库建表成功，
// 且雪花 Create 回调挂载生效（零值 ID 模型 Create 后取得非零雪花 ID）。
// SQLite 路径固定 data/sili-smart-hr.db，用 t.Chdir 隔离到临时目录防污染工作区。
func TestInitDBSQLite(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	db, err := InitDB(&config.Config{
		SQL:       config.SQLConfig{DSN: "local"},
		Snowflake: config.SnowflakeConfig{NodeID: 1},
	})
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// InitDB 切换进程级 current，测试结束还原（包内约定，防顺序污染后续用例）。
	t.Cleanup(func() { current = DBSQLite })
	if Current() != DBSQLite {
		t.Errorf("Current() = %q, want sqlite", Current())
	}
	if !db.Migrator().HasTable(&domain.Account{}) {
		t.Error("expected accounts table after InitDB")
	}
	// 雪花回调：零值 ID 模型 Create 后由回调赋雪花 ID。
	acct := domain.Account{Username: "u1", PasswordHash: "x", Enabled: true}
	if err := db.Create(&acct).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	if acct.ID == 0 {
		t.Error("snowflake callback 未生效：Create 后 ID 仍为 0")
	}
	// 切片承载与无 ID 结构不 panic（assignSnowflakeID 分支覆盖）。
	accounts := []domain.Account{{Username: "u2", PasswordHash: "x"}}
	if err := db.Create(&accounts).Error; err != nil {
		t.Fatalf("create slice: %v", err)
	}
	if accounts[0].ID == 0 {
		t.Error("切片元素未取得雪花 ID")
	}
	assignSnowflakeID(domain.SystemParam{ParamKey: "no-id-field"}) // 无 ID 字段类型不 panic
	assignSnowflakeID(nil)                                         // nil 防御分支

	// SQLite 落盘文件被连接持有，Windows 下 TempDir 清理会因文件占用失败：
	// 显式关底层句柄释放文件锁后再返回（清理顺序在 t.Cleanup 之前）。
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// TestApplyPool 连接池参数下放到底层 sql.DB（SQLite 内存库取底层句柄即可验证）。
func TestApplyPool(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	cfg := &config.Config{SQL: config.SQLConfig{
		MaxIdleConns: 3, MaxOpenConns: 7, MaxLifetime: "90s",
	}}
	if err := applyPool(db, cfg); err != nil {
		t.Fatalf("applyPool: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	// MaxOpenConns 可经 Stats 读回；idle 与 lifetime 无独立读口，合法参数路径不报错即可。
	if n := sqlDB.Stats().MaxOpenConnections; n != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", n)
	}
	// 非法时长串静默跳过（不报错不设值），零值字段不触碰池参数。
	if err := applyPool(db, &config.Config{SQL: config.SQLConfig{MaxLifetime: "bad"}}); err != nil {
		t.Errorf("非法时长应静默跳过: %v", err)
	}
}
