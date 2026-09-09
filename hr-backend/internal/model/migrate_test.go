package model

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/pkg/snowflake"
)

// model 包直连 extractor 出厂常量（EscapeLike 上提 pkg 后无 import 环），
// seed 值即生产真值，无需测试注入假出厂集；出厂集与回退集的同源性由
// extractor 包守护测试 TestRedactPatternsMatchDefaults 覆盖。

// TestMigrateCreatesSessionFeatures 验证 extractor 域两表迁移与参数 seed（BR2）：
// 两表、索引与 session_features 全列建出，system_params 恰好 seed 两参数键；
// inject_prefixes 为追加语义空数组 seed（04 §4），redact_patterns 为出厂全集序列化。
func TestMigrateCreatesSessionFeatures(t *testing.T) {
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
	if !db.Migrator().HasTable("session_features") {
		t.Fatal("expected session_features table exists")
	}
	if !db.Migrator().HasTable("system_params") {
		t.Fatal("expected system_params table exists")
	}

	// session_features 全列（specs §2.3 档案表列集合 + client 探测列，BR2/BR6）。
	sfCols := []string{
		"id", "session_key", "token_name", "status", "client", "turn_count",
		"first_turn_at", "last_turn_at", "profile_json", "error_code",
		"created_at", "updated_at",
	}
	for _, col := range sfCols {
		if !db.Migrator().HasColumn(&domain.SessionFeature{}, col) {
			t.Fatalf("expected session_features.%s column exists", col)
		}
	}

	// 索引：uk_session_key 唯一、idx_token_first_turn 组合（BR2）。
	if !db.Migrator().HasIndex(&domain.SessionFeature{}, "uk_session_key") {
		t.Fatal("expected uk_session_key index exists")
	}
	if !db.Migrator().HasIndex(&domain.SessionFeature{}, "idx_token_first_turn") {
		t.Fatal("expected idx_token_first_turn index exists")
	}
	if !db.Migrator().HasIndex(&domain.SystemParam{}, "uk_param_key") {
		t.Fatal("expected uk_param_key index exists")
	}

	// system_params seed：2 行（两参数键）。inject_prefixes 追加语义空数组（04 §4），
	// redact_patterns 出厂全集序列化。Order by param_key：inject_prefixes < redact_patterns。
	var params []domain.SystemParam
	if err := db.Order("param_key").Find(&params).Error; err != nil {
		t.Fatalf("find system_params: %v", err)
	}
	if len(params) != 2 {
		t.Fatalf("expected 2 system_params rows, got %d", len(params))
	}

	if params[0].ParamKey != extractor.ParamKeyInjectPrefixes {
		t.Fatalf("param_key[0] = %q, want %s", params[0].ParamKey, extractor.ParamKeyInjectPrefixes)
	}
	if params[0].ParamValue != "[]" {
		t.Fatalf("inject_prefixes value mismatch: got %s want []", params[0].ParamValue)
	}
	if params[0].Version != 1 {
		t.Fatalf("inject_prefixes version = %d, want 1", params[0].Version)
	}
	// 出厂并集仍在代码中（04 §4：配置集不承载出厂前缀，非出厂集被删）。
	if len(extractor.InjectPrefixes()) == 0 {
		t.Fatal("InjectPrefixes() 出厂并集为空，追加语义下出厂集应仍在代码中")
	}

	wantPatterns, err := json.Marshal(extractor.RedactPatterns())
	if err != nil {
		t.Fatalf("marshal patterns: %v", err)
	}
	if params[1].ParamKey != extractor.ParamKeyRedactPatterns {
		t.Fatalf("param_key[1] = %q, want %s", params[1].ParamKey, extractor.ParamKeyRedactPatterns)
	}
	if params[1].ParamValue != string(wantPatterns) {
		t.Fatalf("redact_patterns value mismatch: got %s want %s", params[1].ParamValue, wantPatterns)
	}
	if params[1].Version != 1 {
		t.Fatalf("redact_patterns version = %d, want 1", params[1].Version)
	}
}

// TestMigrateCreatesEvalTables 验证评估域三表迁移（04 §3）：三表、全列与三个
// upsert 幂等唯一索引建出，观测索引兜底存在性。三表首启为空无 seed 断言。
func TestMigrateCreatesEvalTables(t *testing.T) {
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

	// 三表存在。
	for _, table := range []string{"dimension_scores", "aggregate_scores", "activity_stats"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected %s table exists", table)
		}
	}

	// dimension_scores 全列（04 §3.1.1）。
	dsCols := []string{
		"id", "token_name", "period_start_at", "period_end_at", "dimension_code",
		"module", "score", "rationale", "insufficient", "evidence_json",
		"source", "model_name", "prompt_version", "status", "error_code",
		"created_at", "updated_at",
	}
	for _, col := range dsCols {
		if !db.Migrator().HasColumn(&domain.DimensionScore{}, col) {
			t.Fatalf("expected dimension_scores.%s column exists", col)
		}
	}

	// aggregate_scores 全列（04 §3.2.1）。
	asCols := []string{
		"id", "token_name", "period_start_at", "period_end_at", "module",
		"module_score", "overview_score", "included_json", "excluded_json",
		"created_at", "updated_at",
	}
	for _, col := range asCols {
		if !db.Migrator().HasColumn(&domain.AggregateScore{}, col) {
			t.Fatalf("expected aggregate_scores.%s column exists", col)
		}
	}

	// activity_stats 全列（04 §3.3.1）。
	atCols := []string{
		"id", "token_name", "period_start_at", "period_end_at",
		"session_count", "valid_session_count", "skipped_count", "total_turns",
		"active_level", "population_note", "client_dist_json",
		"created_at", "updated_at",
	}
	for _, col := range atCols {
		if !db.Migrator().HasColumn(&domain.ActivityStat{}, col) {
			t.Fatalf("expected activity_stats.%s column exists", col)
		}
	}

	// 三表 upsert 幂等唯一索引（04 §5）。
	if !db.Migrator().HasIndex(&domain.DimensionScore{}, "uk_person_period_dim") {
		t.Fatal("expected uk_person_period_dim index exists")
	}
	if !db.Migrator().HasIndex(&domain.AggregateScore{}, "uk_person_period_module") {
		t.Fatal("expected uk_person_period_module index exists")
	}
	if !db.Migrator().HasIndex(&domain.ActivityStat{}, "uk_person_period") {
		t.Fatal("expected uk_person_period index exists")
	}

	// 观测与画像读取路径索引。
	for _, idx := range []struct {
		model any
		name  string
	}{
		{&domain.DimensionScore{}, "idx_period_status"},
		{&domain.DimensionScore{}, "idx_module_insufficient"},
		{&domain.ActivityStat{}, "idx_period_level"},
		{&domain.ActivityStat{}, "idx_period_note"},
	} {
		if !db.Migrator().HasIndex(idx.model, idx.name) {
			t.Fatalf("expected %s index exists", idx.name)
		}
	}
}

// TestSeedParamsIdempotent 验证参数 seed 幂等：二次 migrateDB 后两行 param_value 不变且无重复行；
// 参数页改过（行已存在）不覆盖。
func TestSeedParamsIdempotent(t *testing.T) {
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

	// 记录首启后的值，并模拟参数页改值（seed 不得覆盖）。
	var before domain.SystemParam
	if err := db.Where("param_key = ?", extractor.ParamKeyInjectPrefixes).First(&before).Error; err != nil {
		t.Fatalf("load seeded param: %v", err)
	}
	edited := `["edited-by-admin"]`
	if err := db.Model(&domain.SystemParam{}).Where("param_key = ?", extractor.ParamKeyInjectPrefixes).
		Update("param_value", edited).Error; err != nil {
		t.Fatalf("simulate param edit: %v", err)
	}

	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// 行数仍 =2（两参数键），已编辑值不被覆盖，另一键值不变。
	var count int64
	if err := db.Model(&domain.SystemParam{}).Count(&count).Error; err != nil {
		t.Fatalf("count system_params: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 system_params rows after idempotent rerun, got %d", count)
	}
	var after domain.SystemParam
	if err := db.Where("param_key = ?", extractor.ParamKeyInjectPrefixes).First(&after).Error; err != nil {
		t.Fatalf("reload param: %v", err)
	}
	if after.ParamValue != edited {
		t.Fatalf("seed overwrote edited param_value: got %s", after.ParamValue)
	}
	wantPatterns, err := json.Marshal(extractor.RedactPatterns())
	if err != nil {
		t.Fatalf("marshal patterns: %v", err)
	}
	var redact domain.SystemParam
	if err := db.Where("param_key = ?", extractor.ParamKeyRedactPatterns).First(&redact).Error; err != nil {
		t.Fatalf("reload redact param: %v", err)
	}
	if redact.ParamValue != string(wantPatterns) {
		t.Fatalf("redact_patterns changed on rerun: got %s", redact.ParamValue)
	}
}

// TestMigrateDimensionDeletedCode 存量库 code 唯一索引落地前的数据收敛：
// 软删行改写占位码（原code__D<id>）、活跃重复 code 加 __DUP<id> 后缀（保首行）、
// 幂等重跑不叠加后缀，收敛后建 uk_dimension_code 不再被脏数据卡死。
func TestMigrateDimensionDeletedCode(t *testing.T) {
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.Dimension{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	// 先拆唯一索引模拟旧表结构（无 uk_dimension_code 的存量库），再插脏数据：
	// 软删行与活跃行同 code + 两活跃行同 code（TOCTOU 时期脏数据）。
	if err := db.Migrator().DropIndex(&domain.Dimension{}, "uk_dimension_code"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	del := &domain.Dimension{Code: "ACT_X", Name: "旧", ModuleCode: domain.ModuleActivity,
		DataSource: domain.SourceRule, Anchor: "a", Weight: 1, Enabled: false, Version: 1}
	if err := db.Create(del).Error; err != nil {
		t.Fatalf("seed del: %v", err)
	}
	if err := db.Delete(del).Error; err != nil { // GORM 软删
		t.Fatalf("soft delete: %v", err)
	}
	a := &domain.Dimension{Code: "ACT_X", Name: "甲", ModuleCode: domain.ModuleActivity,
		DataSource: domain.SourceRule, Anchor: "a", Weight: 1, Enabled: true, Version: 1}
	b := &domain.Dimension{Code: "ACT_X", Name: "乙", ModuleCode: domain.ModuleActivity,
		DataSource: domain.SourceRule, Anchor: "a", Weight: 1, Enabled: true, Version: 1}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := db.Create(b).Error; err != nil {
		t.Fatalf("seed b: %v", err)
	}

	// 钩子收敛后重建唯一索引成功，幂等重跑不叠加后缀。
	if err := migrateDimensionDeletedCode(db); err != nil {
		t.Fatalf("migrateDimensionDeletedCode: %v", err)
	}
	if err := migrateDimensionDeletedCode(db); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	if err := db.Migrator().CreateIndex(&domain.Dimension{}, "uk_dimension_code"); err != nil {
		t.Fatalf("recreate unique index: %v", err)
	}

	var codes []string
	if err := db.Unscoped().Model(&domain.Dimension{}).Order("id ASC").Pluck("code", &codes).Error; err != nil {
		t.Fatalf("pluck codes: %v", err)
	}
	uniq := map[string]bool{}
	for _, c := range codes {
		if uniq[c] {
			t.Fatalf("duplicate code after migration: %v", codes)
		}
		uniq[c] = true
	}
	// 期望形态：软删行占位码 ACT_X__D<delID>、活跃首行保留 ACT_X、重复活跃行 ACT_X__DUP<bID>。
	wantB := "ACT_X__DUP" + strconv.FormatInt(b.ID, 10)
	wantDel := "ACT_X__D" + strconv.FormatInt(del.ID, 10)
	got := map[string]bool{}
	for _, c := range codes {
		got[c] = true
	}
	if !got["ACT_X"] || !got[wantB] || !got[wantDel] {
		t.Fatalf("want codes {%s %s %s}, got %v", "ACT_X", wantB, wantDel, codes)
	}
}

// 全集 JSON）一次性重置为空数组；运维改过的行（值偏离全集）不触碰；重置后
// 重跑幂等。守护追加语义升级时参数页不呈现 59 条来历不明的追加条目。
func TestMigrateInjectPrefixesAppendSemantics(t *testing.T) {
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

	legacyJSON, err := json.Marshal(extractor.InjectPrefixes())
	if err != nil {
		t.Fatalf("marshal legacy value: %v", err)
	}
	// 场景1：模拟 TECH_003 替换语义存量行（旧 seed 写入出厂全集）。
	if err := db.Model(&domain.SystemParam{}).Where("param_key = ?", extractor.ParamKeyInjectPrefixes).
		Update("param_value", string(legacyJSON)).Error; err != nil {
		t.Fatalf("simulate legacy row: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var row domain.SystemParam
	if err := db.Where("param_key = ?", extractor.ParamKeyInjectPrefixes).First(&row).Error; err != nil {
		t.Fatalf("reload param: %v", err)
	}
	if row.ParamValue != "[]" {
		t.Fatalf("legacy full-set value not reset: got %s", row.ParamValue)
	}

	// 场景2：运维追加过的行（部分追加集）不被迁移触碰。
	admin := `["custom-prefix-1"]`
	if err := db.Model(&domain.SystemParam{}).Where("param_key = ?", extractor.ParamKeyInjectPrefixes).
		Update("param_value", admin).Error; err != nil {
		t.Fatalf("simulate admin edit: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
	if err := db.Where("param_key = ?", extractor.ParamKeyInjectPrefixes).First(&row).Error; err != nil {
		t.Fatalf("reload param: %v", err)
	}
	if row.ParamValue != admin {
		t.Fatalf("admin-edited value must be kept: got %s", row.ParamValue)
	}
}
