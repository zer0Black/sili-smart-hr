// Package repository_test 对 dimension 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并在测试
// *gorm.DB 上内联注册 Create 回调（model.registerSnowflakeIDCallback 私有不可调），
// AutoMigrate 同时建 Dimension 与 DimensionSetting 两表。
// 覆盖 ListAll/FindByID/FindByCodeExcludingDeleted/Create/UpdateWithVersion/
// SoftDeleteWithVersion/GetActivitySetting/UpdateActivitySetting 的真实 SQL 行为。
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

// newDimensionTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate Dimension 与
// DimensionSetting 两表，并内联注册简化版雪花 Create 回调（仅处理 *domain.Dimension）。
// 每个测试拿独立 DB 实例避免数据相互污染。
func newDimensionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 内联注册简化版雪花 Create 回调，与生产 model 包的全量反射版等效。
	// DimensionSetting 的 ID 在测试里显式赋值（单行表固定一行），回调不处理它。
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_dim_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if d, ok := tx.Statement.Dest.(*domain.Dimension); ok && d.ID == 0 {
			d.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.Dimension{}, &domain.DimensionSetting{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedDimension 直接经 db 构造并写入一个维度，返回带 ID 的实体。
// 新建维度 version 置 1（业务约定），其余字段由调用方传入。
func seedDimension(t *testing.T, db *gorm.DB, code, name, module string, enabled bool) domain.Dimension {
	t.Helper()
	d := domain.Dimension{
		Code:        code,
		Name:        name,
		ModuleCode:  module,
		DataSource:  domain.SourceRule,
		Anchor:      "锚点",
		Weight:      10,
		Enabled:     enabled,
		Version:     1,
		Description: "desc-" + code,
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("seed dimension %s: %v", code, err)
	}
	return d
}

// seedSetting 写入一行 DimensionSetting（系统级单例），返回带 ID 的实体。
func seedSetting(t *testing.T, db *gorm.DB, active, low int) domain.DimensionSetting {
	t.Helper()
	s := domain.DimensionSetting{
		ID:                    snowflake.NextID(),
		ActiveThreshold:       active,
		LowFrequencyThreshold: low,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}
	return s
}

// TestListAll_Order verifies ListAll returns non-deleted dimensions ordered by code ASC (BR §4.1.5).
func TestListAll_Order(t *testing.T) {
	db := newDimensionTestDB(t)
	// 故意乱序插入，验证 code ASC 排序而非插入序。
	seedDimension(t, db, "C_THREE", "丙", domain.ModuleActivity, true)
	seedDimension(t, db, "A_ONE", "甲", domain.ModuleActivity, true)
	seedDimension(t, db, "B_TWO", "乙", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	list, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list len want 3, got %d", len(list))
	}
	wantCodes := []string{"A_ONE", "B_TWO", "C_THREE"}
	for i, want := range wantCodes {
		if list[i].Code != want {
			t.Fatalf("list[%d].Code want %s, got %s", i, want, list[i].Code)
		}
	}
}

// TestListAll_ExcludesSoftDeleted verifies ListAll filters out soft-deleted rows (BR2 §6.1).
func TestListAll_ExcludesSoftDeleted(t *testing.T) {
	db := newDimensionTestDB(t)
	seedDimension(t, db, "A", "甲", domain.ModuleActivity, true)
	b := seedDimension(t, db, "B", "乙", domain.ModuleActivity, true)

	if err := db.Delete(&b).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewDimensionRepository(db)
	list, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(list) != 1 || list[0].Code != "A" {
		t.Fatalf("ListAll want only [A], got %+v", list)
	}
}

// TestFindByID verifies FindByID returns the dimension by primary key.
func TestFindByID(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "F1", "查", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	got, err := repo.FindByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != d.ID || got.Code != "F1" {
		t.Fatalf("FindByID want %d/F1, got %d/%s", d.ID, got.ID, got.Code)
	}
}

// TestFindByID_SoftDeleted verifies FindByID filters soft-deleted rows (BR2).
func TestFindByID_SoftDeleted(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "FD", "删", domain.ModuleActivity, true)
	if err := db.Delete(&d).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewDimensionRepository(db)
	if _, err := repo.FindByID(context.Background(), d.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("FindByID after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestFindByCodeExcludingDeleted_Available verifies a non-existing code returns ErrRecordNotFound.
func TestFindByCodeExcludingDeleted_Available(t *testing.T) {
	db := newDimensionTestDB(t)
	repo := repository.NewDimensionRepository(db)
	if _, err := repo.FindByCodeExcludingDeleted(context.Background(), "MISSING"); err != gorm.ErrRecordNotFound {
		t.Fatalf("missing code want ErrRecordNotFound, got %v", err)
	}
}

// TestFindByCodeExcludingDeleted_Exists verifies an existing non-deleted code returns the row.
func TestFindByCodeExcludingDeleted_Exists(t *testing.T) {
	db := newDimensionTestDB(t)
	seedDimension(t, db, "CODE_X", "名", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	got, err := repo.FindByCodeExcludingDeleted(context.Background(), "CODE_X")
	if err != nil {
		t.Fatalf("FindByCodeExcludingDeleted: %v", err)
	}
	if got.Code != "CODE_X" {
		t.Fatalf("got.Code want CODE_X, got %s", got.Code)
	}
}

// TestFindByCodeExcludingDeleted_AfterDelete verifies a soft-deleted code returns
// ErrRecordNotFound (code reusable after soft delete).
func TestFindByCodeExcludingDeleted_AfterDelete(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "DUP", "重", domain.ModuleActivity, true)
	if err := db.Delete(&d).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewDimensionRepository(db)
	if _, err := repo.FindByCodeExcludingDeleted(context.Background(), "DUP"); err != gorm.ErrRecordNotFound {
		t.Fatalf("FindByCodeExcludingDeleted after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestDimensionCreate_AssignsSnowflakeID verifies Create inserts and the inline callback assigns a snowflake ID.
func TestDimensionCreate_AssignsSnowflakeID(t *testing.T) {
	db := newDimensionTestDB(t)
	repo := repository.NewDimensionRepository(db)
	d := &domain.Dimension{
		Code:       "NEW",
		Name:       "新",
		ModuleCode: domain.ModuleActivity,
		DataSource: domain.SourceRule,
		Anchor:     "锚",
		Weight:     5,
		Enabled:    true,
		Version:    1,
	}
	if err := repo.Create(context.Background(), d); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("ID want non-zero snowflake, got 0")
	}
	got, err := repo.FindByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("FindByID after Create: %v", err)
	}
	if got.Code != "NEW" || got.Version != 1 {
		t.Fatalf("after Create want Code=NEW Version=1, got %+v", got)
	}
}

// TestUpdateWithVersion_VersionMatch verifies a matching version updates the row and returns
// RowsAffected=1, and version is bumped to +1 (BR1 §4.1.4 规则9).
func TestUpdateWithVersion_VersionMatch(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "U1", "改", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), d.ID, 1, map[string]any{
		"name":             "改名",
		"weight":           50,
		"include_overview": true,
		"enabled":          false,
		"description":      "改后说明",
		"prompt":           "p",
		"anchor":           "新锚",
	})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1 (version match), got %d", rows)
	}

	got, err := repo.FindByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "改名" || got.Weight != 50 || got.IncludeOverview != true ||
		got.Enabled != false || got.Description != "改后说明" || got.Prompt != "p" || got.Anchor != "新锚" {
		t.Fatalf("fields not updated: %+v", got)
	}
	if got.Version != 2 {
		t.Fatalf("version want 2 after update, got %d", got.Version)
	}
	// 不可变字段保持原值：code/module_code/group_code/data_source 不在 updates。
	if got.Code != "U1" || got.ModuleCode != domain.ModuleActivity || got.DataSource != domain.SourceRule {
		t.Fatalf("immutable fields changed: %+v", got)
	}
}

// TestUpdateWithVersion_VersionMismatch verifies a stale version returns RowsAffected=0
// with no error (optimistic-lock conflict), row unchanged (BR1).
func TestUpdateWithVersion_VersionMismatch(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "U2", "冲", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), d.ID, 99, map[string]any{
		"name": "不应写入",
	})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (version conflict), got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "冲" || got.Version != 1 {
		t.Fatalf("row should be untouched on conflict, got %+v", got)
	}
}

// TestUpdateWithVersion_DeletedRow verifies a soft-deleted row returns RowsAffected=0
// (deleted_at IS NULL clause filters it).
func TestUpdateWithVersion_DeletedRow(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "UD", "已删", domain.ModuleActivity, true)
	if err := db.Delete(&d).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewDimensionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), d.ID, 1, map[string]any{
		"name": "不应写入",
	})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (deleted), got %d", rows)
	}
}

// TestSoftDeleteWithVersion_VersionMatch verifies soft delete on matching version returns
// RowsAffected=1, and the row becomes invisible to FindByID (BR1 + BR2).
func TestSoftDeleteWithVersion_VersionMatch(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "D1", "删", domain.ModuleActivity, false)

	repo := repository.NewDimensionRepository(db)
	rows, err := repo.SoftDeleteWithVersion(context.Background(), d.ID, 1)
	if err != nil {
		t.Fatalf("SoftDeleteWithVersion: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1 (version match), got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), d.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("FindByID after soft delete want ErrRecordNotFound, got %v", err)
	}
}

// TestSoftDeleteWithVersion_VersionMismatch verifies soft delete on a stale version returns
// RowsAffected=0 (optimistic-lock conflict), row still exists (BR1).
func TestSoftDeleteWithVersion_VersionMismatch(t *testing.T) {
	db := newDimensionTestDB(t)
	d := seedDimension(t, db, "D2", "留", domain.ModuleActivity, false)

	repo := repository.NewDimensionRepository(db)
	rows, err := repo.SoftDeleteWithVersion(context.Background(), d.ID, 99)
	if err != nil {
		t.Fatalf("SoftDeleteWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 (version conflict), got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), d.ID); err != nil {
		t.Fatalf("row should still exist after conflict, got %v", err)
	}
}

// TestGetActivitySetting_ReturnsFirstRow verifies GetActivitySetting reads the single-row setting.
func TestGetActivitySetting_ReturnsFirstRow(t *testing.T) {
	db := newDimensionTestDB(t)
	seedSetting(t, db, 100, 30)

	repo := repository.NewDimensionRepository(db)
	got, err := repo.GetActivitySetting(context.Background())
	if err != nil {
		t.Fatalf("GetActivitySetting: %v", err)
	}
	if got.ActiveThreshold != 100 || got.LowFrequencyThreshold != 30 {
		t.Fatalf("setting want active=100 low=30, got %+v", got)
	}
}

// TestGetActivitySetting_NotFound verifies GetActivitySetting on empty table returns ErrRecordNotFound.
// 生产环境 migrateDB 会 seed 一行，service 层据此安全调用；未 seed 时显式报错。
func TestGetActivitySetting_NotFound(t *testing.T) {
	db := newDimensionTestDB(t)
	repo := repository.NewDimensionRepository(db)
	if _, err := repo.GetActivitySetting(context.Background()); err != gorm.ErrRecordNotFound {
		t.Fatalf("empty setting want ErrRecordNotFound, got %v", err)
	}
}

// TestUpdateActivitySetting verifies UpdateActivitySetting overwrites the single-row setting.
func TestUpdateActivitySetting(t *testing.T) {
	db := newDimensionTestDB(t)
	seedSetting(t, db, 50, 10)

	repo := repository.NewDimensionRepository(db)
	if err := repo.UpdateActivitySetting(context.Background(), 200, 80); err != nil {
		t.Fatalf("UpdateActivitySetting: %v", err)
	}
	got, err := repo.GetActivitySetting(context.Background())
	if err != nil {
		t.Fatalf("GetActivitySetting: %v", err)
	}
	if got.ActiveThreshold != 200 || got.LowFrequencyThreshold != 80 {
		t.Fatalf("after update want active=200 low=80, got %+v", got)
	}
}

// TestUpdateActivitySetting_NoRow verifies UpdateActivitySetting on empty table affects 0 rows
// without error (save-overwrite semantics, no optimistic lock).
func TestUpdateActivitySetting_NoRow(t *testing.T) {
	db := newDimensionTestDB(t)
	repo := repository.NewDimensionRepository(db)
	// 表空，UPDATE 影响 0 行，方法不返回错误。
	if err := repo.UpdateActivitySetting(context.Background(), 100, 20); err != nil {
		t.Fatalf("UpdateActivitySetting on empty: %v", err)
	}
	got, err := repo.GetActivitySetting(context.Background())
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("empty setting still want ErrRecordNotFound, got got=%+v err=%v", got, err)
	}
}

// TestListEnabledFullByDataSource 核心断言：预置启用/停用/软删除/不同 data_source 各行，
// 只返回启用未删指定 source 行，且含 Prompt/Anchor 字段值（评分口径快照需要全字段）。
func TestListEnabledFullByDataSource(t *testing.T) {
	db := newDimensionTestDB(t)
	// 命中行：启用 + CONVERSATION + 未删，故意乱序插入验证 code ASC。
	seedConversation(t, db, "AI_B", true)
	seedConversation(t, db, "AI_A", true)
	// 停用行：不返回。
	seedConversation(t, db, "AI_OFF", false)
	// 软删除行：不返回。
	del := seedConversation(t, db, "AI_DEL", true)
	if err := db.Delete(&del).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	// 不同 data_source 行：不返回。
	seedDimension(t, db, "RULE_X", "规则维", domain.ModuleActivity, true)

	repo := repository.NewDimensionRepository(db)
	list, err := repo.ListEnabledFullByDataSource(context.Background(), domain.SourceConversation)
	if err != nil {
		t.Fatalf("ListEnabledFullByDataSource: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len want 2, got %d (%+v)", len(list), list)
	}
	if list[0].Code != "AI_A" || list[1].Code != "AI_B" {
		t.Fatalf("want code ASC [AI_A AI_B], got [%s %s]", list[0].Code, list[1].Code)
	}
	// 全字段断言：含 Prompt/Anchor 长文本列（与 ListAll 的 brief 投影相区分）。
	for _, d := range list {
		if d.Prompt != "提示词-"+d.Code || d.Anchor != "锚点-"+d.Code {
			t.Fatalf("全字段未返回: code=%s prompt=%q anchor=%q", d.Code, d.Prompt, d.Anchor)
		}
	}
}

// seedConversation 写入 CONVERSATION 来源维度，带 Prompt/Anchor 全字段。
func seedConversation(t *testing.T, db *gorm.DB, code string, enabled bool) domain.Dimension {
	t.Helper()
	d := domain.Dimension{
		Code:        code,
		Name:        "名-" + code,
		ModuleCode:  domain.ModuleAIUsage,
		DataSource:  domain.SourceConversation,
		Prompt:      "提示词-" + code,
		Anchor:      "锚点-" + code,
		Weight:      10,
		Enabled:     enabled,
		Version:     1,
		Description: "desc-" + code,
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("seed conversation %s: %v", code, err)
	}
	return d
}

// TestListEnabledFullByDataSource_NoMatch verifies no matching rows returns an empty list without error.
func TestListEnabledFullByDataSource_NoMatch(t *testing.T) {
	db := newDimensionTestDB(t)
	repo := repository.NewDimensionRepository(db)
	list, err := repo.ListEnabledFullByDataSource(context.Background(), domain.SourceTest)
	if err != nil {
		t.Fatalf("no match want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("no match want empty list, got %d rows", len(list))
	}
}
