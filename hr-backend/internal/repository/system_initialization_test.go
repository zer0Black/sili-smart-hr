// Package repository_test 对 system_initialization 仓储做黑盒集成测试。
//
// Exists 是本仓储唯一方法，仅判定 system_initializations 表是否存在记录（04 §5）。
// 每测试独立 :memory: SQLite 实例隔离，AutoMigrate 单表（migrateDB 私有不可达）。
package repository_test

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// newSystemInitTestDB 构造独立 :memory: SQLite gorm.DB 并 AutoMigrate SystemInitialization。
// 与 account_test.go 的 newTestDB 同属 package repository_test 但互不共享 DB 实例。
func newSystemInitTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.SystemInitialization{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// TestSystemInitRepo_Exists_Empty 验证空表 Exists 返回 (false, nil)（BR1：记录存在即已初始化）。
func TestSystemInitRepo_Exists_Empty(t *testing.T) {
	db := newSystemInitTestDB(t)
	repo := repository.NewSystemInitializationRepository(db)

	got, err := repo.Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists on empty table: unexpected error: %v", err)
	}
	if got {
		t.Fatalf("Exists on empty table want false, got true")
	}
}

// TestSystemInitRepo_Exists_AfterInsert 验证写入一行后 Exists 返回 (true, nil)（BR1）。
// 雪花回调在测试独立 DB 上未注册，ID 零值由 SQLite 主键自增兜底，不影响存在性判定。
func TestSystemInitRepo_Exists_AfterInsert(t *testing.T) {
	db := newSystemInitTestDB(t)
	rec := &domain.SystemInitialization{
		CreatorAccountID: 1,
		DBType:           "sqlite",
	}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("seed system_initialization: %v", err)
	}

	repo := repository.NewSystemInitializationRepository(db)
	got, err := repo.Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists after insert: unexpected error: %v", err)
	}
	if !got {
		t.Fatalf("Exists after insert want true, got false")
	}
}
