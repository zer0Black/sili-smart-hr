// Package repository_test 对大模型配置仓储做黑盒集成测试。
//
// 测试用独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并
// 在测试 *gorm.DB 上内联注册 Create 回调（与生产 model 包的全量反射版等效），
// 验证 List/FindByID/Create/Update/Delete/Count/FindFirstEnabled/
// FindFirstByIDOrder/EnableExclusive 的真实 SQL 行为。
package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newLLMTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate LLMConfig 并内联注册雪花 ID 回调。
// 每个测试拿独立 DB 实例避免数据相互污染。
func newLLMTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 内联注册简化版雪花 Create 回调（仅处理 *domain.LLMConfig），与生产 model 包的全量反射版等效。
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_llm_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if cfg, ok := tx.Statement.Dest.(*domain.LLMConfig); ok && cfg.ID == 0 {
			cfg.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.LLMConfig{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedLLMConfig 直接经 db 写入一条配置，返回带 ID 的实体。enabled 决定启用状态。
func seedLLMConfig(t *testing.T, db *gorm.DB, name, provider, modelID string, enabled bool) domain.LLMConfig {
	t.Helper()
	cfg := domain.LLMConfig{
		Name:         name,
		Provider:     provider,
		ModelID:      modelID,
		APIURL:       "https://api.example.com",
		APIKeyCipher: "nonce:cipher",
		APIKeyMasked: "sk-****1234",
		Enabled:      enabled,
		Version:      1,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed llm_config %s: %v", name, err)
	}
	return cfg
}

// seedLLMConfigAt 写入并强制覆盖 CreatedAt，用于排序稳定化
// （SQLite 默认时间精度与连续插入可能令 created_at 数值相同，破坏 ORDER BY 稳定性）。
func seedLLMConfigAt(t *testing.T, db *gorm.DB, name, provider, modelID string, enabled bool, createdAt time.Time) domain.LLMConfig {
	t.Helper()
	cfg := domain.LLMConfig{
		Name:         name,
		Provider:     provider,
		ModelID:      modelID,
		APIURL:       "",
		APIKeyCipher: "nonce:cipher",
		APIKeyMasked: "sk-****1234",
		Enabled:      enabled,
		CreatedAt:    createdAt,
		Version:      1,
	}
	if err := db.Create(&cfg).Error; err != nil {
		t.Fatalf("seed llm_config %s: %v", name, err)
	}
	return cfg
}

// TestLLMConfigList_EmptyKeyword 验证空 keyword 返回全部（核心断言：插入 3 条后长度 3）。
func TestLLMConfigList_EmptyKeyword(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	seedLLMConfigAt(t, db, "主力", "deepseek", "deepseek-chat", true, base.Add(-2*time.Hour))
	seedLLMConfigAt(t, db, "备用", "openai", "gpt-4o", false, base.Add(-time.Hour))
	seedLLMConfigAt(t, db, "实验", "anthropic", "claude-3", false, base)

	repo := repository.NewLLMConfigRepository(db)
	list, err := repo.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list len want 3, got %d", len(list))
	}
	// created_at 升序：先插入的在前
	if list[0].Name != "主力" {
		t.Fatalf("list[0].Name want 主力 (asc), got %s", list[0].Name)
	}
}

// TestLLMConfigList_Keyword 验证 keyword 命中 name 或 model_id 子集（BR3：模糊搜索）。
func TestLLMConfigList_Keyword(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	seedLLMConfigAt(t, db, "主力模型", "deepseek", "deepseek-chat", true, base.Add(-2*time.Hour))
	seedLLMConfigAt(t, db, "OpenAI", "openai", "gpt-4o", false, base.Add(-time.Hour))
	seedLLMConfigAt(t, db, "Anthropic", "anthropic", "claude-3", false, base)

	repo := repository.NewLLMConfigRepository(db)
	// keyword=deep 命中 name「主力模型」（provider=deepseek）与 model_id「deepseek-chat」
	list, err := repo.List(context.Background(), "deep")
	if err != nil {
		t.Fatalf("List deep: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("deep match: list len want 1, got %d (%+v)", len(list), list)
	}
	if list[0].Name != "主力模型" {
		t.Fatalf("deep match: list[0].Name want 主力模型, got %s", list[0].Name)
	}

	// keyword=gpt 仅命中 model_id「gpt-4o」
	list2, err := repo.List(context.Background(), "gpt")
	if err != nil {
		t.Fatalf("List gpt: %v", err)
	}
	if len(list2) != 1 || list2[0].Name != "OpenAI" {
		t.Fatalf("gpt match: want OpenAI, got %+v", list2)
	}
}

// TestLLMConfigList_KeywordEscape 验证 keyword 含 % 与 _ 通配符时按字面匹配（BR3）。
func TestLLMConfigList_KeywordEscape(t *testing.T) {
	db := newLLMTestDB(t)
	seedLLMConfig(t, db, "model_v1", "deepseek", "deepseek-chat", true)
	seedLLMConfig(t, db, "modelXv1", "openai", "gpt-4o", false)

	repo := repository.NewLLMConfigRepository(db)
	// 字面 "model_v1" 只应命中 name=model_v1 一条，不把 _ 当作单字符通配
	list, err := repo.List(context.Background(), "model_v1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "model_v1" {
		t.Fatalf("escape: want exactly model_v1, got %+v", list)
	}
}

// TestLLMConfigFindByID 验证 FindByID 存在与不存在两条路径。
func TestLLMConfigFindByID(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := seedLLMConfig(t, db, "主力", "deepseek", "deepseek-chat", true)

	got, err := repo.FindByID(context.Background(), cfg.ID)
	if err != nil {
		t.Fatalf("FindByID exists: %v", err)
	}
	if got.ID != cfg.ID || got.Name != "主力" {
		t.Fatalf("FindByID mismatch: got %+v", got)
	}

	// 不存在返回 ErrRecordNotFound（service 层映射 1301）
	if _, err := repo.FindByID(context.Background(), 999999); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("FindByID missing want ErrRecordNotFound, got %v", err)
	}
}

// TestLLMConfigCreate_And_Count 验证 Create 后 Count==1。
func TestLLMConfigCreate_And_Count(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := &domain.LLMConfig{
		Name:         "主力",
		Provider:     "deepseek",
		ModelID:      "deepseek-chat",
		APIKeyCipher: "nonce:cipher",
		APIKeyMasked: "sk-****1234",
		Enabled:      true,
	}
	if err := repo.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if cfg.ID == 0 {
		t.Fatal("ID want non-zero snowflake, got 0")
	}

	n, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count want 1, got %d", n)
	}
}

// TestLLMConfigUpdate 验证乐观锁 Update 按 id+version 匹配更新元数据列，
// 成功返 RowsAffected=1 且 version 自增；Enabled 不在更新列不被覆盖（防 lost update）。
func TestLLMConfigUpdate(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := seedLLMConfig(t, db, "原", "deepseek", "deepseek-chat", false)

	// service.Update 只改元数据，Enabled 不在 Select 名单，即便快照为 true 也不落库。
	cfg.Name = "改"
	cfg.Provider = "openai"
	cfg.ModelID = "gpt-4o"
	cfg.APIURL = "https://api.openai.com"
	cfg.Enabled = true // 刻意置 true 验证不被写入
	affected, err := repo.Update(context.Background(), &cfg)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected want 1, got %d", affected)
	}
	got, err := repo.FindByID(context.Background(), cfg.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Name != "改" || got.Provider != "openai" || got.ModelID != "gpt-4o" || got.APIURL != "https://api.openai.com" {
		t.Fatalf("Update metadata not applied: %+v", got)
	}
	if got.Enabled {
		t.Fatalf("Enabled must not be touched by Update (seed false), got true")
	}
	if got.Version != 2 {
		t.Fatalf("version should auto-increment to 2, got %d", got.Version)
	}
}

// TestLLMConfigUpdate_VersionConflict 验证 version 不匹配时 affected==0（乐观锁冲突，service 映射 1308）。
func TestLLMConfigUpdate_VersionConflict(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := seedLLMConfig(t, db, "原", "deepseek", "deepseek-chat", false)
	// seed 后 version=1，刻意用 version=99 触发不匹配。
	stale := domain.LLMConfig{
		ID:      cfg.ID,
		Name:    "改",
		Version: 99,
	}
	affected, err := repo.Update(context.Background(), &stale)
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if affected != 0 {
		t.Fatalf("version mismatch: affected want 0, got %d", affected)
	}
	// 原行未被改动
	got, _ := repo.FindByID(context.Background(), cfg.ID)
	if got.Name != "原" {
		t.Fatalf("row should remain unchanged, got name=%s", got.Name)
	}
}

// TestLLMConfigCreateExclusiveFirst_Empty 验证空表首条创建自动启用（排他启用不变量）。
func TestLLMConfigCreateExclusiveFirst_Empty(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := &domain.LLMConfig{
		Name:         "主力",
		Provider:     "deepseek",
		ModelID:      "deepseek-chat",
		APIKeyCipher: "nonce:cipher",
		APIKeyMasked: "sk-****1234",
		Version:      1,
	}
	if err := repo.CreateExclusiveFirst(context.Background(), cfg); err != nil {
		t.Fatalf("CreateExclusiveFirst: %v", err)
	}
	if cfg.ID == 0 {
		t.Fatal("ID want non-zero snowflake, got 0")
	}
	if !cfg.Enabled {
		t.Fatal("first config should be auto enabled")
	}
	// 全表仅 1 条 enabled=true
	var enabledCount int64
	db.Model(&domain.LLMConfig{}).Where("enabled = ?", true).Count(&enabledCount)
	if enabledCount != 1 {
		t.Fatalf("exclusive: enabled count want 1, got %d", enabledCount)
	}
}

// TestLLMConfigCreateExclusiveFirst_NonEmpty 验证非空表按传入 Enabled 创建（不自动启用）。
func TestLLMConfigCreateExclusiveFirst_NonEmpty(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	// 先 seed 一条启用项作为既有数据。
	seedLLMConfig(t, db, "主力", "deepseek", "deepseek-chat", true)

	cfg := &domain.LLMConfig{
		Name:         "备选",
		Provider:     "openai",
		ModelID:      "gpt-4o",
		APIKeyCipher: "nonce:cipher2",
		APIKeyMasked: "sk-****5678",
		Enabled:      false, // 非首条，按传入值落库
		Version:      1,
	}
	if err := repo.CreateExclusiveFirst(context.Background(), cfg); err != nil {
		t.Fatalf("CreateExclusiveFirst: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("non-first config should remain disabled as passed in")
	}
	// 原启用项保持不变（不被新 Create 触动的停用 UPDATE 收敛，因为 count>0 跳过该步）
	got, err := repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled: %v", err)
	}
	if got.Name != "主力" {
		t.Fatalf("original enabled should remain, got %s", got.Name)
	}
}

// TestLLMConfigDelete_Physical 验证 Delete 为物理删除：FindByID 返回 ErrRecordNotFound，
// 原始行不残留（与 account 软删除不同，specs §1.3、04 §3.2.1）。
func TestLLMConfigDelete_Physical(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	cfg := seedLLMConfig(t, db, "删", "deepseek", "deepseek-chat", false)

	if err := repo.Delete(context.Background(), cfg.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.FindByID(context.Background(), cfg.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("FindByID after delete want ErrRecordNotFound, got %v", err)
	}

	// 物理删除：Unscoped 查询也不可见（区别于 account 的软删除）
	var rawCount int64
	if err := db.Unscoped().Model(&domain.LLMConfig{}).Where("id = ?", cfg.ID).Count(&rawCount).Error; err != nil {
		t.Fatalf("unscoped count: %v", err)
	}
	if rawCount != 0 {
		t.Fatalf("physical delete: raw row should not exist, want count=0 got %d", rawCount)
	}
}

// TestLLMConfigEnableExclusive 验证 EnableExclusive 事务内排他启用：
// 调用后目标为唯一 enabled=true，FindFirstEnabled 返回该 id（BR1）。
func TestLLMConfigEnableExclusive(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, base.Add(-2*time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base.Add(-time.Hour))
	c := seedLLMConfigAt(t, db, "C", "anthropic", "claude-3", false, base)

	// 初始 a 启用
	repo := repository.NewLLMConfigRepository(db)
	got, err := repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled initial: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("initial enabled want a, got id=%d", got.ID)
	}

	// 排他启用 c
	if err := repo.EnableExclusive(context.Background(), c.ID); err != nil {
		t.Fatalf("EnableExclusive c: %v", err)
	}
	got, err = repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled after c: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("after EnableExclusive(c) want c, got id=%d", got.ID)
	}

	// a、b 必须为 false
	aAfter, _ := repo.FindByID(context.Background(), a.ID)
	bAfter, _ := repo.FindByID(context.Background(), b.ID)
	if aAfter.Enabled || bAfter.Enabled {
		t.Fatalf("exclusive violated: a.Enabled=%v b.Enabled=%v", aAfter.Enabled, bAfter.Enabled)
	}

	// 全表仅 1 条 enabled=true（用 Count 验证总数 + FindFirstEnabled 不可重复）
	var enabledCount int64
	db.Model(&domain.LLMConfig{}).Where("enabled = ?", true).Count(&enabledCount)
	if enabledCount != 1 {
		t.Fatalf("exclusive: enabled count want 1, got %d", enabledCount)
	}
}

// TestLLMConfigEnableExclusive_FromDisabled 验证从全停态启用某项仍生效。
func TestLLMConfigEnableExclusive_FromDisabled(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", false, base.Add(-time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base)

	repo := repository.NewLLMConfigRepository(db)
	// 全停态（service 层不应允许，但 repository 容错）
	if err := repo.EnableExclusive(context.Background(), b.ID); err != nil {
		t.Fatalf("EnableExclusive b: %v", err)
	}
	got, err := repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled: %v", err)
	}
	if got.ID != b.ID {
		t.Fatalf("want b enabled, got id=%d", got.ID)
	}
	aAfter, _ := repo.FindByID(context.Background(), a.ID)
	if aAfter.Enabled {
		t.Fatal("a should remain disabled")
	}
}

// TestLLMConfigFindFirstByIDOrder 验证删除转启候选：
// 插入 2 条后 Delete 一个，FindFirstByIDOrder(被删id) 返回剩余项（BR2）。
func TestLLMConfigFindFirstByIDOrder(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, base.Add(-time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base)

	repo := repository.NewLLMConfigRepository(db)
	// 删除 a（启用项）
	if err := repo.Delete(context.Background(), a.ID); err != nil {
		t.Fatalf("Delete a: %v", err)
	}

	// 转启候选：排除被删的 a.id，按列表顺序取首个剩余 → b
	got, err := repo.FindFirstByIDOrder(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("FindFirstByIDOrder: %v", err)
	}
	if got.ID != b.ID {
		t.Fatalf("transfer candidate want b, got id=%d", got.ID)
	}
}

// TestLLMConfigFindFirstByIDOrder_OrderConsistency 验证转启候选按 created_at 升序取首个，
// 三条中删最早一条，剩余按序取最早的那条（BR2：列表顺序稳定）。
func TestLLMConfigFindFirstByIDOrder_OrderConsistency(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, base.Add(-2*time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base.Add(-time.Hour))
	c := seedLLMConfigAt(t, db, "C", "anthropic", "claude-3", false, base)

	repo := repository.NewLLMConfigRepository(db)
	// 删 a（最早、启用项）→ 剩余按 created_at 升序首个是 b
	if err := repo.Delete(context.Background(), a.ID); err != nil {
		t.Fatalf("Delete a: %v", err)
	}
	got, err := repo.FindFirstByIDOrder(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("FindFirstByIDOrder: %v", err)
	}
	if got.ID != b.ID {
		t.Fatalf("transfer candidate want b (next in asc order), got id=%d", got.ID)
	}
	_ = c
}

// TestLLMConfigFindFirstByIDOrder_NotFound 验证剩余为空时返回 ErrRecordNotFound。
func TestLLMConfigFindFirstByIDOrder_NotFound(t *testing.T) {
	db := newLLMTestDB(t)
	a := seedLLMConfig(t, db, "A", "deepseek", "deepseek-chat", true)

	repo := repository.NewLLMConfigRepository(db)
	// 唯一行被排除后无剩余
	if _, err := repo.FindFirstByIDOrder(context.Background(), a.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

// TestLLMConfigFindFirstEnabled_NoneEnabled 验证无启用项时 FindFirstEnabled 返回 ErrRecordNotFound。
func TestLLMConfigFindFirstEnabled_NoneEnabled(t *testing.T) {
	db := newLLMTestDB(t)
	seedLLMConfig(t, db, "A", "deepseek", "deepseek-chat", false)

	repo := repository.NewLLMConfigRepository(db)
	if _, err := repo.FindFirstEnabled(context.Background()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

// TestLLMConfigFindFirstEnabled_EmptyTable 验证空表 FindFirstEnabled 返回 ErrRecordNotFound。
func TestLLMConfigFindFirstEnabled_EmptyTable(t *testing.T) {
	db := newLLMTestDB(t)
	repo := repository.NewLLMConfigRepository(db)
	if _, err := repo.FindFirstEnabled(context.Background()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

// TestLLMConfigDeleteAndTransferEnable_Transfer 验证删启用态事务内 Delete + 排他启用候选（03 T2 契约）。
// 三条记录删首条（启用项），事务落库后：被删行消失、候选为唯一 enabled=true、其余全停用。
func TestLLMConfigDeleteAndTransferEnable_Transfer(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, base.Add(-2*time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base.Add(-time.Hour))
	c := seedLLMConfigAt(t, db, "C", "anthropic", "claude-3", false, base)

	repo := repository.NewLLMConfigRepository(db)
	if err := repo.DeleteAndTransferEnable(context.Background(), a.ID, b.ID); err != nil {
		t.Fatalf("DeleteAndTransferEnable: %v", err)
	}

	// a 已被物理删除
	if _, err := repo.FindByID(context.Background(), a.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("a should be deleted, got err=%v", err)
	}
	// b 为唯一启用项
	got, err := repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled: %v", err)
	}
	if got.ID != b.ID {
		t.Fatalf("enabled want b, got id=%d", got.ID)
	}
	// c 必须停用
	cAfter, _ := repo.FindByID(context.Background(), c.ID)
	if cAfter.Enabled {
		t.Fatal("c should be disabled after transfer")
	}
	// 全表仅 1 条 enabled=true
	var enabledCount int64
	db.Model(&domain.LLMConfig{}).Where("enabled = ?", true).Count(&enabledCount)
	if enabledCount != 1 {
		t.Fatalf("exclusive: enabled count want 1, got %d", enabledCount)
	}
}

// TestLLMConfigDeleteAndTransferEnable_NoEnable 验证 enableID=0 时只删不动 enabled（非启用态删除分支）。
func TestLLMConfigDeleteAndTransferEnable_NoEnable(t *testing.T) {
	db := newLLMTestDB(t)
	base := time.Now()
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, base.Add(-time.Hour))
	b := seedLLMConfigAt(t, db, "B", "openai", "gpt-4o", false, base)

	repo := repository.NewLLMConfigRepository(db)
	// 删非启用项 b，enableID=0：不应动 a 的启用状态
	if err := repo.DeleteAndTransferEnable(context.Background(), b.ID, 0); err != nil {
		t.Fatalf("DeleteAndTransferEnable: %v", err)
	}
	if _, err := repo.FindByID(context.Background(), b.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("b should be deleted, got err=%v", err)
	}
	got, err := repo.FindFirstEnabled(context.Background())
	if err != nil {
		t.Fatalf("FindFirstEnabled: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("a should remain enabled, got id=%d", got.ID)
	}
}

// TestLLMConfigDeleteAndTransferEnable_DeleteMissing 验证 deleteID 不存在（RowsAffected==0）时
// 事务返 ErrRecordNotFound 并回滚，使 service 能映射 1301 而非静默成功（FindByID 后并发删除场景）。
func TestLLMConfigDeleteAndTransferEnable_DeleteMissing(t *testing.T) {
	db := newLLMTestDB(t)
	a := seedLLMConfigAt(t, db, "A", "deepseek", "deepseek-chat", true, time.Now())

	repo := repository.NewLLMConfigRepository(db)
	// deleteID 用不存在的雪花值，Delete 命中 0 行
	if err := repo.DeleteAndTransferEnable(context.Background(), 999999, 0); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("delete missing want ErrRecordNotFound, got %v", err)
	}
	// 事务回滚：a 不受影响
	got, err := repo.FindByID(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("a should remain: %v", err)
	}
	if !got.Enabled {
		t.Fatal("a should remain enabled after rollback")
	}
}
