// Package repository_test 对 account 仓储做黑盒集成测试。
//
// 测试用独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并
// 在测试 *gorm.DB 上内联注册 Create 回调（model.registerSnowflakeIDCallback 私有不可调），
// 验证 ListAccounts/Create/Update/Delete/UpdateEnabled 的真实 SQL 行为。
package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate Account 并内联注册雪花 ID 回调。
// 每个测试拿独立 DB 实例避免数据相互污染，回调名不冲突（注册到不同 db）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// snowflake 是进程级全局节点，多次 Init 幂等；未初始化时 NextID panic。
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 内联注册简化版雪花 Create 回调（仅处理 *domain.Account），与生产 model 包的全量反射版等效。
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if acc, ok := tx.Statement.Dest.(*domain.Account); ok && acc.ID == 0 {
			acc.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.Account{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedAccount 直接经 db 构造并写入一个账号，返回带 ID 的实体，供测试断言使用。
// GORM autoCreateTime 在 CreatedAt 非零值时保留原值（文档化行为），借此显式控制 created_at 顺序。
func seedAccount(t *testing.T, db *gorm.DB, username, name string, enabled bool) domain.Account {
	t.Helper()
	acc := domain.Account{
		Username:     username,
		PasswordHash: "$2a$04$placeholderhashforsonlytestpurpose",
		Name:         name,
		Enabled:      enabled,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("seed account %s: %v", username, err)
	}
	return acc
}

// seedAccountAt 写入账号并强制覆盖 CreatedAt 为指定值，用于排序测试稳定化
// （SQLite 默认时间精度与连续插入可能令 created_at 数值相同，破坏 ORDER BY 稳定性）。
func seedAccountAt(t *testing.T, db *gorm.DB, username, name string, enabled bool, createdAt time.Time) domain.Account {
	t.Helper()
	acc := domain.Account{
		Username:     username,
		PasswordHash: "$2a$04$placeholderhashforsonlytestpurpose",
		Name:         name,
		Enabled:      enabled,
		CreatedAt:    createdAt,
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("seed account %s: %v", username, err)
	}
	return acc
}

// TestListAccounts_Keyword 验证 keyword 在 username 与 name 上的模糊匹配，
// 并确认 total 与 list 用同一 where 条件（BR4：模糊匹配 username 与 name）。
func TestListAccounts_Keyword(t *testing.T) {
	db := newTestDB(t)
	seedAccount(t, db, "admin", "张三", true)
	seedAccount(t, db, "ops", "李四", true)

	repo := repository.NewAccountRepository(db)
	list, total, err := repo.ListAccounts(context.Background(), "adm", 1, 10)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if total != 1 {
		t.Fatalf("total want 1, got %d", total)
	}
	if len(list) != 1 {
		t.Fatalf("list len want 1, got %d", len(list))
	}
	if list[0].Username != "admin" {
		t.Fatalf("list[0].Username want admin, got %s", list[0].Username)
	}

	// keyword 命中 name 分支
	list2, total2, err := repo.ListAccounts(context.Background(), "李", 1, 10)
	if err != nil {
		t.Fatalf("ListAccounts by name: %v", err)
	}
	if total2 != 1 || len(list2) != 1 || list2[0].Name != "李四" {
		t.Fatalf("name match: want ops/李四, got total=%d list=%+v", total2, list2)
	}
}

// TestListAccounts_Order 验证 created_at DESC 排序：后插入的排在前面（BR5）。
// 用显式 CreatedAt（GORM autoCreateTime 非零保留）规避连续插入 created_at 同值导致排序不稳定。
func TestListAccounts_Order(t *testing.T) {
	db := newTestDB(t)
	base := time.Now()
	seedAccountAt(t, db, "admin", "管理员", true, base.Add(-time.Hour)) // 更早
	seedAccountAt(t, db, "zhangsan", "张三", true, base)               // 更晚

	repo := repository.NewAccountRepository(db)
	list, _, err := repo.ListAccounts(context.Background(), "", 1, 10)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len want 2, got %d", len(list))
	}
	if list[0].Username != "zhangsan" {
		t.Fatalf("list[0].Username want zhangsan (newer first), got %s", list[0].Username)
	}
	if list[1].Username != "admin" {
		t.Fatalf("list[1].Username want admin, got %s", list[1].Username)
	}
}

// TestListAccounts_Pagination 验证分页参数：page=1,size=1 拿首条，page=2,size=1 拿次条，
// total 反映全量。显式 created_at 保证排序稳定，进而保证 OFFSET/LIMIT 取到确定行。
func TestListAccounts_Pagination(t *testing.T) {
	db := newTestDB(t)
	base := time.Now()
	seedAccountAt(t, db, "a", "甲", true, base.Add(-2*time.Hour))
	seedAccountAt(t, db, "b", "乙", true, base.Add(-1*time.Hour))
	seedAccountAt(t, db, "c", "丙", true, base)

	repo := repository.NewAccountRepository(db)
	list, total, err := repo.ListAccounts(context.Background(), "", 1, 1)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 3 || len(list) != 1 {
		t.Fatalf("page1: total want 3 got %d, len want 1 got %d", total, len(list))
	}
	if list[0].Username != "c" {
		t.Fatalf("page1 list[0].Username want c (newest first), got %s", list[0].Username)
	}
	list2, _, err := repo.ListAccounts(context.Background(), "", 2, 1)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(list2) != 1 || list2[0].Username != "b" {
		t.Fatalf("page2 list[0].Username want b, got %+v", list2)
	}
}

// TestCreate_AssignsSnowflakeID 验证 Create 后雪花 ID 由全局回调赋值（BR 相关：雪花 ID 策略）。
func TestCreate_AssignsSnowflakeID(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	acc := &domain.Account{
		Username:     "x",
		PasswordHash: "h",
		Name:         "n",
		Enabled:      true,
	}
	if err := repo.Create(context.Background(), acc); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if acc.ID == 0 {
		t.Fatal("ID want non-zero snowflake, got 0")
	}
}

// TestDelete_SoftDelete 验证 Delete 为软删除：FindByID 返回 ErrRecordNotFound，
// 但 Unscoped 查询仍能数到原始行（BR3）。
func TestDelete_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	acc := seedAccount(t, db, "del", "待删", true)

	if err := repo.Delete(context.Background(), acc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// 软删除后 FindByID 走 DeletedAt 自动过滤，应返回 ErrRecordNotFound
	if _, err := repo.FindByID(context.Background(), acc.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("FindByID after delete want ErrRecordNotFound, got %v", err)
	}

	// 软删除后 ListAccounts 也应过滤掉该行
	list, total, err := repo.ListAccounts(context.Background(), "", 1, 10)
	if err != nil {
		t.Fatalf("ListAccounts after delete: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("after delete want empty list, got total=%d len=%d", total, len(list))
	}

	// 原始行仍在表中（仅 deleted_at 被赋值），Unscoped 绕过软删除过滤
	var rawCount int64
	if err := db.Unscoped().Model(&domain.Account{}).Where("id = ?", acc.ID).Count(&rawCount).Error; err != nil {
		t.Fatalf("unscoped count: %v", err)
	}
	if rawCount != 1 {
		t.Fatalf("raw row should still exist, want count=1 got %d", rawCount)
	}
}

// TestUpdate_TogglesFields 验证 Update 整行写入，覆盖 Enabled/Name/PasswordHash。
func TestUpdate_TogglesFields(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	acc := seedAccount(t, db, "u", "原", true)

	acc.Name = "改"
	acc.Enabled = false
	acc.PasswordHash = "newhash"
	if err := repo.Update(context.Background(), &acc); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.FindByID(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "改" || got.Enabled != false || got.PasswordHash != "newhash" {
		t.Fatalf("Update not applied: %+v", got)
	}
}

// TestUpdateEnabled 验证 UpdateEnabled 仅切 enabled 列。
func TestUpdateEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	acc := seedAccount(t, db, "e", "启", true)

	if err := repo.UpdateEnabled(context.Background(), acc.ID, false); err != nil {
		t.Fatalf("UpdateEnabled: %v", err)
	}
	got, err := repo.FindByID(context.Background(), acc.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Enabled {
		t.Fatal("Enabled want false, got true")
	}
}

// TestFindByUsername_ExcludesSoftDeleted 验证软删除账号经 FindByUsername 不可见（BR1：唯一性校验排除软删除）。
func TestFindByUsername_ExcludesSoftDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	acc := seedAccount(t, db, "dup", "重复", true)

	if err := repo.Delete(context.Background(), acc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindByUsername(context.Background(), "dup"); err != gorm.ErrRecordNotFound {
		t.Fatalf("FindByUsername after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestDemoteEnabledIfNotLast_WhenMultipleEnabled 验证启用总数 > 1 时降停 id 账号：
// RowsAffected==1，目标账号 enabled 被切为 false。
func TestDemoteEnabledIfNotLast_WhenMultipleEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", true)
	seedAccount(t, db, "b", "乙", true)

	rows, err := repo.DemoteEnabledIfNotLast(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DemoteEnabledIfNotLast: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1, got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Enabled {
		t.Fatal("Enabled want false after demote, got true")
	}
}

// TestDemoteEnabledIfNotLast_WhenLastEnabled 验证仅一个启用账号时降停它：
// RowsAffected==0（守卫触发），账号保持启用。
func TestDemoteEnabledIfNotLast_WhenLastEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", true)
	seedAccount(t, db, "b", "乙", false) // 仅 a 启用

	rows, err := repo.DemoteEnabledIfNotLast(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DemoteEnabledIfNotLast: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (last enabled guard), got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !got.Enabled {
		t.Fatal("Enabled should remain true when last enabled guard triggers")
	}
}

// TestDemoteEnabledIfNotLast_AlreadyDisabled 验证目标账号已禁用时：
// RowsAffected==0（WHERE enabled=true 不匹配），账号保持禁用。
func TestDemoteEnabledIfNotLast_AlreadyDisabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", false)
	seedAccount(t, db, "b", "乙", true)

	rows, err := repo.DemoteEnabledIfNotLast(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DemoteEnabledIfNotLast: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (already disabled), got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Enabled {
		t.Fatal("Enabled want false (already disabled), got true")
	}
}

// TestDeleteIfNotLastEnabled_Disabled 验证删禁用账号：RowsAffected==1（删除不影响启用数），
// FindByID 返回 ErrRecordNotFound。
func TestDeleteIfNotLastEnabled_Disabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", false)
	seedAccount(t, db, "b", "乙", true) // 仅 b 启用

	rows, err := repo.DeleteIfNotLastEnabled(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DeleteIfNotLastEnabled: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1, got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), a.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestDeleteIfNotLastEnabled_WhenMultipleEnabled 验证启用总数 > 1 时删启用账号：
// RowsAffected==1，目标账号被软删除。
func TestDeleteIfNotLastEnabled_WhenMultipleEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", true)
	seedAccount(t, db, "b", "乙", true)

	rows, err := repo.DeleteIfNotLastEnabled(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DeleteIfNotLastEnabled: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1, got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), a.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestDeleteIfNotLastEnabled_WhenLastEnabled 验证仅一个启用账号时删它：
// RowsAffected==0（守卫触发），账号仍存在未删。
func TestDeleteIfNotLastEnabled_WhenLastEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := repository.NewAccountRepository(db)
	a := seedAccount(t, db, "a", "甲", true)
	seedAccount(t, db, "b", "乙", false) // 仅 a 启用

	rows, err := repo.DeleteIfNotLastEnabled(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("DeleteIfNotLastEnabled: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (last enabled guard), got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), a.ID); err != nil {
		t.Fatalf("account should still exist when guard triggers, got %v", err)
	}
}
