// Package repository_test 对 assessment_config 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并在测试
// *gorm.DB 上内联注册 Create 回调处理 AssessmentConfig 与 AssessmentConfigMember（含 slice 途径）。
// 覆盖 Get/UpdateWithVersion/ListMembers/ReplaceMembers/UpdateWithMembers 的真实 SQL 行为。
//
// 重点断言（BR1 乐观锁 + BR2 关联表全量覆盖）：
//   - UpdateWithVersion 传正确 version 返回 1、version 自增、字段更新
//   - UpdateWithVersion 传错误 version 返回 0（并发冲突）
//   - ReplaceMembers 全量覆盖：传 [2 个] 后长度 2；再传 [] 后长度 0
//   - UpdateWithMembers 单事务：正确 version + 2 members 返回 1 且 Get/ListMembers 反映新值
//   - UpdateWithMembers 事务回滚：错误 version 时 members 保持原样（不被部分覆盖）
package repository_test

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newAssessmentConfigTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate
// AssessmentConfig 与 AssessmentConfigMember 两表，并内联注册简化版雪花 Create 回调。
// 回调处理 *domain.AssessmentConfig、*domain.AssessmentConfigMember 与两者的 slice 途径，
// 与生产 model 包的全量反射版等效。
func newAssessmentConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_ac_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.AssessmentConfig:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *domain.AssessmentConfigMember:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *[]domain.AssessmentConfigMember:
			for i := range *dest {
				if (*dest)[i].ID == 0 {
					(*dest)[i].ID = snowflake.NextID()
				}
			}
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentConfig{}, &domain.AssessmentConfigMember{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedAssessmentConfig 直接经 db 写入单例 seed 行（mimic migrateDB：weekly/23:00/all/version=1），
// 返回带 ID 的实体供后续断言。与生产 migrateDB C 段保持参数一致。
func seedAssessmentConfig(t *testing.T, db *gorm.DB) domain.AssessmentConfig {
	t.Helper()
	cfg := domain.AssessmentConfig{
		Period:      "weekly",
		TriggerTime: "23:00",
		TargetMode:  "all",
		Version:     1,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed assessment_config: %v", err)
	}
	return cfg
}

// TestGet_ReturnsSeededRow 验证 Get 返回 migrateDB seed 的单例行（BR：单例恒有数据）。
func TestGet_ReturnsSeededRow(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != seeded.ID {
		t.Fatalf("ID want %d, got %d", seeded.ID, got.ID)
	}
	if got.Period != "weekly" {
		t.Fatalf("Period want weekly, got %s", got.Period)
	}
	if got.TriggerTime != "23:00" {
		t.Fatalf("TriggerTime want 23:00, got %s", got.TriggerTime)
	}
	if got.TargetMode != "all" {
		t.Fatalf("TargetMode want all, got %s", got.TargetMode)
	}
	if got.Version != 1 {
		t.Fatalf("Version want 1, got %d", got.Version)
	}
}

// TestUpdateWithVersion_CorrectVersion 验证乐观锁成功：传当前 version 返回 affected==1，
// 字段被更新且 Version 自增（BR1：保存用乐观锁 UPDATE WHERE id AND version，成功 version 自增）。
func TestUpdateWithVersion_CorrectVersion(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	update := seeded
	update.Period = "monthly"
	update.TriggerTime = "03:00"
	update.TargetMode = "specified"

	rows, err := repo.UpdateWithVersion(context.Background(), &update)
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 1 {
		t.Fatalf("affected want 1, got %d", rows)
	}

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Period != "monthly" {
		t.Fatalf("Period want monthly, got %s", got.Period)
	}
	if got.TriggerTime != "03:00" {
		t.Fatalf("TriggerTime want 03:00, got %s", got.TriggerTime)
	}
	if got.TargetMode != "specified" {
		t.Fatalf("TargetMode want specified, got %s", got.TargetMode)
	}
	if got.Version != seeded.Version+1 {
		t.Fatalf("Version want %d (self-incremented), got %d", seeded.Version+1, got.Version)
	}
}

// TestUpdateWithVersion_StaleVersion 验证乐观锁冲突：传旧 version 返回 affected==0，
// 字段未被改动（BR1：affected=0 由 service 映射 1306）。
func TestUpdateWithVersion_StaleVersion(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	// 先正常更新一次，Version 变 2。
	update := seeded
	update.Period = "monthly"
	if rows, err := repo.UpdateWithVersion(context.Background(), &update); err != nil || rows != 1 {
		t.Fatalf("first UpdateWithVersion: rows=%d err=%v", rows, err)
	}

	// 用 stale version=1 再更新，应失败。
	stale := seeded
	stale.Period = "daily"
	rows, err := repo.UpdateWithVersion(context.Background(), &stale)
	if err != nil {
		t.Fatalf("UpdateWithVersion stale: %v", err)
	}
	if rows != 0 {
		t.Fatalf("affected want 0 (version conflict), got %d", rows)
	}

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Period != "monthly" {
		t.Fatalf("Period should remain monthly, got %s", got.Period)
	}
	if got.Version != 2 {
		t.Fatalf("Version want 2, got %d", got.Version)
	}
}

// TestReplaceMembers_Overwrite 验证 ReplaceMembers 全量覆盖：
// 传 2 个 member 后 ListMembers 长度 2；再传空 slice 后长度 0（BR2：事务内先删后插）。
func TestReplaceMembers_Overwrite(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	members := []domain.AssessmentConfigMember{
		{AssessmentConfigID: seeded.ID, StaffID: "usr_1", StaffName: "甲"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_2", StaffName: "乙"},
	}
	if err := repo.ReplaceMembers(context.Background(), seeded.ID, members); err != nil {
		t.Fatalf("ReplaceMembers first: %v", err)
	}
	got, err := repo.ListMembers(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("ListMembers first: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len want 2, got %d", len(got))
	}

	// 再次 ReplaceMembers 传空 slice，应清空（all 模式场景）。
	if err := repo.ReplaceMembers(context.Background(), seeded.ID, nil); err != nil {
		t.Fatalf("ReplaceMembers clear: %v", err)
	}
	got2, err := repo.ListMembers(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("ListMembers clear: %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("len after clear want 0, got %d", len(got2))
	}
}

// TestReplaceMembers_OverwriteExisting 验证 ReplaceMembers 对已有成员做覆盖替换而非追加
// （先插 3 个，再插 2 个，最终长度仍 2，不是 5）。
func TestReplaceMembers_OverwriteExisting(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	first := []domain.AssessmentConfigMember{
		{AssessmentConfigID: seeded.ID, StaffID: "usr_1", StaffName: "甲"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_2", StaffName: "乙"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_3", StaffName: "丙"},
	}
	if err := repo.ReplaceMembers(context.Background(), seeded.ID, first); err != nil {
		t.Fatalf("ReplaceMembers first: %v", err)
	}

	second := []domain.AssessmentConfigMember{
		{AssessmentConfigID: seeded.ID, StaffID: "usr_4", StaffName: "丁"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_5", StaffName: "戊"},
	}
	if err := repo.ReplaceMembers(context.Background(), seeded.ID, second); err != nil {
		t.Fatalf("ReplaceMembers second: %v", err)
	}
	got, err := repo.ListMembers(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len after overwrite want 2 (not %d), got %d", len(first), len(got))
	}
}

// TestUpdateWithMembers_Success 验证单事务内乐观锁更新 + 人员全量覆盖：
// 正确 version + 2 members 返回 affected==1，Get 得新字段，ListMembers 长度 2。
func TestUpdateWithMembers_Success(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	// 先放一个 member 验证事务会覆盖。
	_ = db.Create(&domain.AssessmentConfigMember{
		AssessmentConfigID: seeded.ID,
		StaffID:            "usr_old",
		StaffName:          "旧",
	}).Error

	cfg := seeded
	cfg.Period = "daily"
	cfg.TriggerTime = "09:00"
	cfg.TargetMode = "specified"
	members := []domain.AssessmentConfigMember{
		{AssessmentConfigID: seeded.ID, StaffID: "usr_1", StaffName: "甲"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_2", StaffName: "乙"},
	}

	rows, err := repo.UpdateWithMembers(context.Background(), &cfg, members)
	if err != nil {
		t.Fatalf("UpdateWithMembers: %v", err)
	}
	if rows != 1 {
		t.Fatalf("affected want 1, got %d", rows)
	}

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Period != "daily" || got.TriggerTime != "09:00" || got.TargetMode != "specified" {
		t.Fatalf("config not applied: %+v", got)
	}
	if got.Version != seeded.Version+1 {
		t.Fatalf("Version want %d, got %d", seeded.Version+1, got.Version)
	}

	gotMembers, err := repo.ListMembers(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(gotMembers) != 2 {
		t.Fatalf("members len want 2, got %d", len(gotMembers))
	}
}

// TestUpdateWithMembers_StaleVersionRollback 验证错误 version 下事务回滚：
// affected==0，乐观锁失败且 members 未被覆盖（事务原子性，避免 version 未变但 members 被清空）。
func TestUpdateWithMembers_StaleVersionRollback(t *testing.T) {
	db := newAssessmentConfigTestDB(t)
	seeded := seedAssessmentConfig(t, db)

	repo := repository.NewAssessmentConfigRepository(db)
	// 先放一个旧 member，事务回滚时它应仍在。
	_ = db.Create(&domain.AssessmentConfigMember{
		AssessmentConfigID: seeded.ID,
		StaffID:            "usr_old",
		StaffName:          "旧",
	}).Error

	// 用不存在的 version=999 触发乐观锁失败。
	stale := seeded
	stale.Version = 999
	stale.Period = "daily"
	members := []domain.AssessmentConfigMember{
		{AssessmentConfigID: seeded.ID, StaffID: "usr_1", StaffName: "甲"},
		{AssessmentConfigID: seeded.ID, StaffID: "usr_2", StaffName: "乙"},
	}

	rows, err := repo.UpdateWithMembers(context.Background(), &stale, members)
	if err != nil {
		t.Fatalf("UpdateWithMembers stale: %v", err)
	}
	if rows != 0 {
		t.Fatalf("affected want 0 (version conflict), got %d", rows)
	}

	// 乐观锁未更新：原 cfg 仍在。
	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Period != "weekly" || got.Version != 1 {
		t.Fatalf("config should remain seed state, got %+v", got)
	}

	// 事务回滚：旧 member 仍在，新 members 未写入。
	gotMembers, err := repo.ListMembers(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(gotMembers) != 1 {
		t.Fatalf("members len want 1 (rollback kept old), got %d", len(gotMembers))
	}
	if gotMembers[0].StaffID != "usr_old" {
		t.Fatalf("StaffID want usr_old, got %s", gotMembers[0].StaffID)
	}
}
