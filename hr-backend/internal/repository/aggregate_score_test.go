// Package repository_test 对 aggregate_score 仓储做黑盒集成测试。
//
// 覆盖 UpsertAll 唯一索引 upsert 全列覆盖与 nil 显式落 NULL（specs TECH_005 §2.4
// 能力5 规则5/6：同人同周期同模块覆盖更新、全剔除时旧值不保留）、历史周期行不动。
// 每测试独立 :memory: SQLite 实例隔离。
package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newAggregateTestDB 复用评分表测试的 SQLite 构造范式，AutoMigrate AggregateScore。
func newAggregateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_agg_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if rows, ok := tx.Statement.Dest.(*[]domain.AggregateScore); ok {
			for i := range *rows {
				if (*rows)[i].ID == 0 {
					(*rows)[i].ID = snowflake.NextID()
				}
			}
		}
	})
	if err := db.AutoMigrate(&domain.AggregateScore{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeAggRow 构造聚合行：业务模块行 module_score 落值、overview_score nil。
func makeAggRow(token, module string, start, end int64, score *float64) domain.AggregateScore {
	return domain.AggregateScore{
		TokenName:     token,
		PeriodStartAt: time.Unix(start, 0).UTC(),
		PeriodEndAt:   time.Unix(end, 0).UTC(),
		Module:        module,
		ModuleScore:   score,
		IncludedJSON:  `{"AI_USAGE":[{"code":"AI_A","weight":60}]}`,
		ExcludedJSON:  `{"AI_USAGE":["AI_B"]}`,
	}
}

// loadAggRow 直接按唯一键取库内行。
func loadAggRow(t *testing.T, db *gorm.DB, token, module string, start int64) domain.AggregateScore {
	t.Helper()
	var row domain.AggregateScore
	if err := db.Where("token_name = ? AND period_start_at = ? AND module = ?",
		token, time.Unix(start, 0).UTC(), module).First(&row).Error; err != nil {
		t.Fatalf("load agg row %s: %v", module, err)
	}
	return row
}

func floatPtr(f float64) *float64 { return &f }

// TestAggregateScoreUpsertAllUpsert 同 (token, period, module) 二次 UpsertAll：
// 行数不增、新值覆盖旧值（specs 能力5 规则5）。
func TestAggregateScoreUpsertAllUpsert(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeAggRow("张三", domain.ModuleAIUsage, start, end, floatPtr(72.5))
	if err := repo.UpsertAll(ctx, "张三", start, end, []domain.AggregateScore{first}); err != nil {
		t.Fatalf("首次 UpsertAll: %v", err)
	}
	firstID := loadAggRow(t, db, "张三", domain.ModuleAIUsage, start).ID

	second := makeAggRow("张三", domain.ModuleAIUsage, start, end, floatPtr(80.0))
	second.ExcludedJSON = `{"AI_USAGE":["AI_B","AI_C"]}`
	if err := repo.UpsertAll(ctx, "张三", start, end, []domain.AggregateScore{second}); err != nil {
		t.Fatalf("二次 UpsertAll: %v", err)
	}

	var n int64
	if err := db.Model(&domain.AggregateScore{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("upsert 后表行数 want 1, got %d", n)
	}
	got := loadAggRow(t, db, "张三", domain.ModuleAIUsage, start)
	if got.ModuleScore == nil || *got.ModuleScore != 80.0 {
		t.Fatalf("module_score want 覆盖为 80.0, got %v", got.ModuleScore)
	}
	if got.ExcludedJSON != `{"AI_USAGE":["AI_B","AI_C"]}` {
		t.Fatalf("excluded_json 未覆盖: %q", got.ExcludedJSON)
	}
	if got.ID != firstID {
		t.Fatalf("覆盖应保留原行 ID: want %d, got %d", firstID, got.ID)
	}
}

// TestAggregateScoreUpsertNullOverwrite 核心断言：先落 module_score=68.6 行，再 upsert
// 同键 nil 值行，DB 内值为 NULL（全剔除覆盖，旧值不保留，specs 能力5 规则6）。
func TestAggregateScoreUpsertNullOverwrite(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeAggRow("李四", domain.ModuleAIMgmt, start, end, floatPtr(68.6))
	if err := repo.UpsertAll(ctx, "李四", start, end, []domain.AggregateScore{first}); err != nil {
		t.Fatalf("首次 UpsertAll: %v", err)
	}

	// 全剔除形态：module_score nil 显式落空。
	second := makeAggRow("李四", domain.ModuleAIMgmt, start, end, nil)
	if err := repo.UpsertAll(ctx, "李四", start, end, []domain.AggregateScore{second}); err != nil {
		t.Fatalf("二次 UpsertAll: %v", err)
	}

	got := loadAggRow(t, db, "李四", domain.ModuleAIMgmt, start)
	if got.ModuleScore != nil {
		t.Fatalf("module_score want NULL 覆盖, got %v", *got.ModuleScore)
	}
}

// TestAggregateScoreUpsertOverviewRow 总览行 module=overview：overview_score 落值、
// module_score 恒 nil，与业务模块行并存于同周期。
func TestAggregateScoreUpsertOverviewRow(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	moduleRow := makeAggRow("王五", domain.ModuleAIUsage, start, end, floatPtr(75.0))
	overviewRow := makeAggRow("王五", domain.ModuleOverview, start, end, nil)
	overviewRow.OverviewScore = floatPtr(75.0)
	if err := repo.UpsertAll(ctx, "王五", start, end, []domain.AggregateScore{moduleRow, overviewRow}); err != nil {
		t.Fatalf("UpsertAll: %v", err)
	}

	var n int64
	if err := db.Model(&domain.AggregateScore{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("表行数 want 2（业务模块行 + 总览行）, got %d", n)
	}
	overview := loadAggRow(t, db, "王五", domain.ModuleOverview, start)
	if overview.OverviewScore == nil || *overview.OverviewScore != 75.0 {
		t.Fatalf("总览行 overview_score want 75.0, got %v", overview.OverviewScore)
	}
	if overview.ModuleScore != nil {
		t.Fatalf("总览行 module_score want nil, got %v", *overview.ModuleScore)
	}
}

// TestAggregateScoreUpsertHistoricalPeriodUntouched 历史周期行不动：同人同模块
// 不同 period_start 的行在重算时保持原值（specs 能力5 规则5）。
func TestAggregateScoreUpsertHistoricalPeriodUntouched(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400
	prevStart, prevEnd := start-7*86400, start

	prev := makeAggRow("张三", domain.ModuleAIUsage, prevStart, prevEnd, floatPtr(60.0))
	if err := repo.UpsertAll(ctx, "张三", prevStart, prevEnd, []domain.AggregateScore{prev}); err != nil {
		t.Fatalf("历史周期 UpsertAll: %v", err)
	}

	curr := makeAggRow("张三", domain.ModuleAIUsage, start, end, floatPtr(88.0))
	if err := repo.UpsertAll(ctx, "张三", start, end, []domain.AggregateScore{curr}); err != nil {
		t.Fatalf("当期 UpsertAll: %v", err)
	}

	prevRow := loadAggRow(t, db, "张三", domain.ModuleAIUsage, prevStart)
	if prevRow.ModuleScore == nil || *prevRow.ModuleScore != 60.0 {
		t.Fatalf("历史周期行被改写: module_score=%v", prevRow.ModuleScore)
	}
	if !prevRow.UpdatedAt.Equal(prevRow.CreatedAt) {
		t.Fatalf("历史周期行 updated_at 不应刷新: created=%v updated=%v", prevRow.CreatedAt, prevRow.UpdatedAt)
	}
}

// TestAggregateScoreUpsertAllEmpty 空切片入参：不报错不落行。
func TestAggregateScoreUpsertAllEmpty(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	if err := repo.UpsertAll(context.Background(), "张三", scoreBaseUnix, scoreBaseUnix+3600, nil); err != nil {
		t.Fatalf("空切片 UpsertAll want nil error, got %v", err)
	}
	var n int64
	if err := db.Model(&domain.AggregateScore{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("空切片 want 0 行, got %d", n)
	}
}

// TestAggregateScoreUpsertErrorClosedConnection 连接关闭后错误透传。
func TestAggregateScoreUpsertErrorClosedConnection(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	if err := repo.UpsertAll(context.Background(), "张三", 0, 1, []domain.AggregateScore{makeAggRow("张三", domain.ModuleAIUsage, 0, 1, nil)}); err == nil {
		t.Fatal("连接关闭后 UpsertAll want error")
	}
}
