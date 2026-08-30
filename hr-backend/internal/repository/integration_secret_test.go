// Package repository_test 对 integration_secret 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并在测试
// *gorm.DB 上内联注册 Create 回调处理 IntegrationSecret。
// 覆盖 Get/Update 的真实 SQL 行为，重点断言 BR1 单例恒有数据与 BR2 更新即覆盖
// （含空串覆盖场景，验证 map Updates 不忽略 zero-value）。
package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newIntegrationSecretTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate
// IntegrationSecret 表并内联注册简化版雪花 Create 回调，与生产 model 包等效。
func newIntegrationSecretTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_is_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if s, ok := tx.Statement.Dest.(*domain.IntegrationSecret); ok && s.ID == 0 {
			s.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.IntegrationSecret{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedIntegrationSecret 直接经 db 写入单例 seed 行（mimic migrateDB 首启空密钥），
// 返回带 ID 的实体供后续断言。empty=true 时写入空 cipher/masked（首启状态）。
func seedIntegrationSecret(t *testing.T, db *gorm.DB, cipher, masked string) domain.IntegrationSecret {
	t.Helper()
	s := domain.IntegrationSecret{
		SecretCipher: cipher,
		SecretMasked: masked,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed integration_secret: %v", err)
	}
	return s
}

// TestIntegrationSecretGet_ReturnsSeededEmptyRow 验证 Get 返回首启 seed 空行：
// SecretCipher==""（BR1：单例恒有数据 + 首启 seed 空密钥表示未配置）。
func TestIntegrationSecretGet_ReturnsSeededEmptyRow(t *testing.T) {
	db := newIntegrationSecretTestDB(t)
	seeded := seedIntegrationSecret(t, db, "", "")

	repo := repository.NewIntegrationSecretRepository(db)
	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != seeded.ID {
		t.Fatalf("ID want %d, got %d", seeded.ID, got.ID)
	}
	if got.SecretCipher != "" {
		t.Fatalf("SecretCipher want empty (首启未配置), got %q", got.SecretCipher)
	}
	if got.SecretMasked != "" {
		t.Fatalf("SecretMasked want empty, got %q", got.SecretMasked)
	}
}

// TestIntegrationSecretUpdate_OverwritesValues 验证 Update 写入非空 cipher/masked 后 Get 读回新值，
// 且 version 自增（BR2：更新即覆盖 + 乐观锁自增）。
func TestIntegrationSecretUpdate_OverwritesValues(t *testing.T) {
	db := newIntegrationSecretTestDB(t)
	seeded := seedIntegrationSecret(t, db, "", "")

	repo := repository.NewIntegrationSecretRepository(db)
	seeded.SecretCipher = "abc123nonce:ciphertext"
	seeded.SecretMasked = "sk-****abcd"
	affected, err := repo.Update(context.Background(), &seeded)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected want 1, got %d", affected)
	}

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.SecretCipher != "abc123nonce:ciphertext" {
		t.Fatalf("SecretCipher want abc123nonce:ciphertext, got %q", got.SecretCipher)
	}
	if got.SecretMasked != "sk-****abcd" {
		t.Fatalf("SecretMasked want sk-****abcd, got %q", got.SecretMasked)
	}
	if got.ID != seeded.ID {
		t.Fatalf("ID should remain %d, got %d", seeded.ID, got.ID)
	}
	if got.Version != seeded.Version+1 {
		t.Fatalf("Version want %d (old %d + 1), got %d", seeded.Version+1, seeded.Version, got.Version)
	}
}

// TestIntegrationSecretUpdate_EmptyStringPersists 验证 Update 写空串后 Get 读回
// SecretCipher==""（BR2 关键点：map Updates 不忽略 zero-value，支持清空场景）。
func TestIntegrationSecretUpdate_EmptyStringPersists(t *testing.T) {
	db := newIntegrationSecretTestDB(t)
	seeded := seedIntegrationSecret(t, db, "oldnonce:oldcipher", "sk-****old")

	repo := repository.NewIntegrationSecretRepository(db)
	seeded.SecretCipher = ""
	seeded.SecretMasked = ""
	affected, err := repo.Update(context.Background(), &seeded)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected want 1, got %d", affected)
	}

	got, err := repo.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.SecretCipher != "" {
		t.Fatalf("SecretCipher want empty (map Updates 不忽略 zero-value), got %q", got.SecretCipher)
	}
	if got.SecretMasked != "" {
		t.Fatalf("SecretMasked want empty, got %q", got.SecretMasked)
	}
}

// TestIntegrationSecretUpdate_VersionMismatchZeroAffected 验证 version 不匹配时 affected==0（乐观锁并发冲突）。
// 模拟 A.Get → B.Update 成功（version 升至 2）→ A 用旧 version=1 提交，0 行命中。
func TestIntegrationSecretUpdate_VersionMismatchZeroAffected(t *testing.T) {
	db := newIntegrationSecretTestDB(t)
	seeded := seedIntegrationSecret(t, db, "old", "o****d")

	repo := repository.NewIntegrationSecretRepository(db)
	// 另一个请求先成功更新一次，version 由 seeded.Version 升至 +1。
	stale := seeded
	stale.SecretCipher = "winner"
	stale.SecretMasked = "w****r"
	if affected, err := repo.Update(context.Background(), &stale); err != nil || affected != 1 {
		t.Fatalf("first Update: affected=%d err=%v", affected, err)
	}
	// 此时 DB 的 version 已是 seeded.Version+1，A 仍持旧 version=seeded.Version 提交。
	stale.SecretCipher = "loser"
	stale.SecretMasked = "l****r"
	affected, err := repo.Update(context.Background(), &stale)
	if err != nil {
		t.Fatalf("second Update err: %v", err)
	}
	if affected != 0 {
		t.Fatalf("affected want 0 on version mismatch, got %d", affected)
	}
	// 确认落库的仍是 winner，未被 lost-update 覆盖。
	got, _ := repo.Get(context.Background())
	if got.SecretCipher != "winner" {
		t.Fatalf("SecretCipher want winner (lost-update prevented), got %q", got.SecretCipher)
	}
}

// TestIntegrationSecretGet_EmptyTableNotFound 验证空表（未 seed）时 Get 返回 ErrRecordNotFound。
// 生产 migrateDB 保证恒有数据，此用例覆盖 repository 层的容错边界。
func TestIntegrationSecretGet_EmptyTableNotFound(t *testing.T) {
	db := newIntegrationSecretTestDB(t)
	repo := repository.NewIntegrationSecretRepository(db)
	if _, err := repo.Get(context.Background()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound on empty table, got %v", err)
	}
}
