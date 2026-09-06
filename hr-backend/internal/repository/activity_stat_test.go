// Package repository_test 对 activity_stat 仓储做黑盒集成测试。
//
// 覆盖 Upsert 唯一索引幂等覆盖（specs TECH_005 §2.4 能力3：同人同周期重跑按
// 唯一索引更新，历史周期行不动）、零值列覆盖与错误透传。每测试独立 :memory: SQLite。
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

// newActivityTestDB 复用评分表测试的 SQLite 构造范式，AutoMigrate ActivityStat。
func newActivityTestDB(t *testing.T) *gorm.DB {
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
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_act_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if rec, ok := tx.Statement.Dest.(*domain.ActivityStat); ok && rec.ID == 0 {
			rec.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.ActivityStat{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeActivityRow 构造字段完整的活跃度行。
func makeActivityRow(token string, start, end int64, valid int) domain.ActivityStat {
	return domain.ActivityStat{
		TokenName:         token,
		PeriodStartAt:     time.Unix(start, 0).UTC(),
		PeriodEndAt:       time.Unix(end, 0).UTC(),
		SessionCount:      valid + 3,
		ValidSessionCount: valid,
		SkippedCount:      2,
		TotalTurns:        40,
		ActiveLevel:       domain.ActiveLevelActive,
		PopulationNote:    "",
		ClientDistJSON:    `{"claude_code":10}`,
	}
}

// countActivityRows 数表内总行数。
func countActivityRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&domain.ActivityStat{}).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// loadActivityRow 直接按唯一键取库内行。
func loadActivityRow(t *testing.T, db *gorm.DB, token string, start int64) domain.ActivityStat {
	t.Helper()
	var row domain.ActivityStat
	if err := db.Where("token_name = ? AND period_start_at = ?",
		token, time.Unix(start, 0).UTC()).First(&row).Error; err != nil {
		t.Fatalf("load activity row: %v", err)
	}
	return row
}

// TestActivityStatUpsertIdempotent 核心断言：二次 Upsert 行数不增、字段更新
//（specs §2.4 能力3 幂等覆盖）。
func TestActivityStatUpsertIdempotent(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeActivityRow("张三", start, end, 12)
	if err := repo.Upsert(ctx, &first); err != nil {
		t.Fatalf("首次 Upsert: %v", err)
	}
	firstID := loadActivityRow(t, db, "张三", start).ID

	// 重跑：有效数 12→15、分级与分布变化、note 从空串变为枚举键（零值→非零同样须覆盖）。
	second := makeActivityRow("张三", start, end, 15)
	second.TotalTurns = 66
	second.ActiveLevel = domain.ActiveLevelActive
	second.PopulationNote = "evidence_note.auto_client"
	if err := repo.Upsert(ctx, &second); err != nil {
		t.Fatalf("二次 Upsert: %v", err)
	}

	if n := countActivityRows(t, db); n != 1 {
		t.Fatalf("二次 Upsert 后表行数 want 1, got %d", n)
	}
	got := loadActivityRow(t, db, "张三", start)
	if got.ValidSessionCount != 15 || got.TotalTurns != 66 {
		t.Fatalf("字段未更新: valid=%d turns=%d", got.ValidSessionCount, got.TotalTurns)
	}
	if got.PopulationNote != "evidence_note.auto_client" {
		t.Fatalf("population_note want 覆盖为枚举键, got %q", got.PopulationNote)
	}
	if got.ID != firstID {
		t.Fatalf("覆盖应保留原行 ID: want %d, got %d", firstID, got.ID)
	}
}

// TestActivityStatUpsertZeroValueOverwrite 零值列覆盖：note 与分布重置为空串、
// 计数降为 0 时照常写入（map 形态防 struct Updates 跳零值）。
func TestActivityStatUpsertZeroValueOverwrite(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeActivityRow("李四", start, end, 12)
	first.PopulationNote = "evidence_note.bypass"
	first.ClientDistJSON = `{"workbuddy":9}`
	first.SkippedCount = 4
	if err := repo.Upsert(ctx, &first); err != nil {
		t.Fatalf("首次 Upsert: %v", err)
	}

	second := makeActivityRow("李四", start, end, 0)
	second.SessionCount = 0
	second.TotalTurns = 0
	second.ActiveLevel = domain.ActiveLevelUnused
	second.PopulationNote = ""
	second.ClientDistJSON = ""
	if err := repo.Upsert(ctx, &second); err != nil {
		t.Fatalf("二次 Upsert: %v", err)
	}

	got := loadActivityRow(t, db, "李四", start)
	if got.PopulationNote != "" || got.ClientDistJSON != "" {
		t.Fatalf("零值列未覆盖: note=%q dist=%q", got.PopulationNote, got.ClientDistJSON)
	}
	if got.SessionCount != 0 || got.TotalTurns != 0 || got.ActiveLevel != domain.ActiveLevelUnused {
		t.Fatalf("计数/分级未覆盖: session=%d turns=%d level=%q",
			got.SessionCount, got.TotalTurns, got.ActiveLevel)
	}
}

// TestActivityStatUpsertHistoricalPeriodUntouched 历史周期行不动：同人不同
// period_start 的行在重跑时保持原值（specs 能力3）。
func TestActivityStatUpsertHistoricalPeriodUntouched(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400
	prevStart, prevEnd := start-7*86400, start

	prev := makeActivityRow("张三", prevStart, prevEnd, 8)
	if err := repo.Upsert(ctx, &prev); err != nil {
		t.Fatalf("历史周期 Upsert: %v", err)
	}

	curr := makeActivityRow("张三", start, end, 20)
	if err := repo.Upsert(ctx, &curr); err != nil {
		t.Fatalf("当期 Upsert: %v", err)
	}

	if n := countActivityRows(t, db); n != 2 {
		t.Fatalf("表行数 want 2, got %d", n)
	}
	prevRow := loadActivityRow(t, db, "张三", prevStart)
	if prevRow.ValidSessionCount != 8 {
		t.Fatalf("历史周期行被改写: valid=%d", prevRow.ValidSessionCount)
	}
	if !prevRow.UpdatedAt.Equal(prevRow.CreatedAt) {
		t.Fatalf("历史周期行 updated_at 不应刷新: created=%v updated=%v", prevRow.CreatedAt, prevRow.UpdatedAt)
	}
}

// TestActivityStatUpsertPeriodEndNotInKey 定向窄窗口与批量窗口起点重合时按唯一键
// 覆盖：同 (token, period_start) 不同 period_end 的后写覆盖前写（specs 能力6 规则3）。
func TestActivityStatUpsertPeriodEndNotInKey(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	batch := makeActivityRow("王五", start, end, 10)
	if err := repo.Upsert(ctx, &batch); err != nil {
		t.Fatalf("批量窗口 Upsert: %v", err)
	}

	// 定向窄窗口同起点不同终点：唯一键命中覆盖（period_end 取后写行值）。
	narrow := makeActivityRow("王五", start, start+86400, 3)
	if err := repo.Upsert(ctx, &narrow); err != nil {
		t.Fatalf("定向窗口 Upsert: %v", err)
	}

	if n := countActivityRows(t, db); n != 1 {
		t.Fatalf("同起点不同终点 want 覆盖为 1 行, got %d", n)
	}
	got := loadActivityRow(t, db, "王五", start)
	if got.ValidSessionCount != 3 {
		t.Fatalf("覆盖后 valid want 3, got %d", got.ValidSessionCount)
	}
	if !got.PeriodEndAt.Equal(time.Unix(start+86400, 0).UTC()) {
		t.Fatalf("period_end want 后写行值, got %v", got.PeriodEndAt)
	}
}

// TestActivityStatUpsertErrorClosedConnection 连接关闭后错误透传。
func TestActivityStatUpsertErrorClosedConnection(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	if err := repo.Upsert(context.Background(), &domain.ActivityStat{TokenName: "张三"}); err == nil {
		t.Fatal("连接关闭后 Upsert want error")
	}
}
