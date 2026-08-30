package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
)

// TestMigrateSysConfigSeed 验证系统参数与大模型配置域四表的迁移与 seed：
//   - assessment_configs / assessment_config_members / llm_configs / integration_secrets 四表建出
//   - assessment_configs 单例 seed：period=weekly、trigger_time=23:00、target_mode=all、version=1
//   - integration_secrets 单例 seed：空密钥行（secret_cipher=""、secret_masked=""，BR2）
//   - llm_configs / assessment_config_members 首启为空表（不 seed）
//   - 重跑 migrateDB 幂等：两单例表仍各只 1 行
//
// 覆盖 BR1（密文列 SecretCipher json:"-"、密文落库）通过 seed 写入空串验证列存在与默认态；
// BR2（空密钥首启默认）直接断言空串。
func TestMigrateSysConfigSeed(t *testing.T) {
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

	// 四表存在。
	if !db.Migrator().HasTable(&domain.AssessmentConfig{}) {
		t.Fatal("expected assessment_configs table exists")
	}
	if !db.Migrator().HasTable(&domain.AssessmentConfigMember{}) {
		t.Fatal("expected assessment_config_members table exists")
	}
	if !db.Migrator().HasTable(&domain.LLMConfig{}) {
		t.Fatal("expected llm_configs table exists")
	}
	if !db.Migrator().HasTable(&domain.IntegrationSecret{}) {
		t.Fatal("expected integration_secrets table exists")
	}

	// assessment_configs seed：1 行，默认值。
	var cfg domain.AssessmentConfig
	if err := db.First(&cfg).Error; err != nil {
		t.Fatalf("expected seeded assessment_configs row, got: %v", err)
	}
	if cfg.Period != "weekly" {
		t.Fatalf("seed period = %q, want weekly", cfg.Period)
	}
	if cfg.TriggerTime != "23:00" {
		t.Fatalf("seed trigger_time = %q, want 23:00", cfg.TriggerTime)
	}
	if cfg.TargetMode != "all" {
		t.Fatalf("seed target_mode = %q, want all", cfg.TargetMode)
	}
	if cfg.Version != 1 {
		t.Fatalf("seed version = %d, want 1", cfg.Version)
	}

	// integration_secrets seed：1 行，空密钥（BR2）。
	var secret domain.IntegrationSecret
	if err := db.First(&secret).Error; err != nil {
		t.Fatalf("expected seeded integration_secrets row, got: %v", err)
	}
	if secret.SecretCipher != "" {
		t.Fatalf("seed secret_cipher = %q, want empty", secret.SecretCipher)
	}
	if secret.SecretMasked != "" {
		t.Fatalf("seed secret_masked = %q, want empty", secret.SecretMasked)
	}

	// llm_configs 与 assessment_config_members 首启为空表。
	var llms []domain.LLMConfig
	if err := db.Find(&llms).Error; err != nil {
		t.Fatalf("find llm_configs: %v", err)
	}
	if len(llms) != 0 {
		t.Fatalf("expected empty llm_configs, got %d rows", len(llms))
	}
	var members []domain.AssessmentConfigMember
	if err := db.Find(&members).Error; err != nil {
		t.Fatalf("find assessment_config_members: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("expected empty assessment_config_members, got %d rows", len(members))
	}

	// 重跑 migrateDB 幂等：两单例表仍各只 1 行。
	if err := migrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var cfgCount int64
	if err := db.Model(&domain.AssessmentConfig{}).Count(&cfgCount).Error; err != nil {
		t.Fatalf("count assessment_configs: %v", err)
	}
	if cfgCount != 1 {
		t.Fatalf("expected 1 assessment_configs row after idempotent rerun, got %d", cfgCount)
	}
	var secretCount int64
	if err := db.Model(&domain.IntegrationSecret{}).Count(&secretCount).Error; err != nil {
		t.Fatalf("count integration_secrets: %v", err)
	}
	if secretCount != 1 {
		t.Fatalf("expected 1 integration_secrets row after idempotent rerun, got %d", secretCount)
	}
}
