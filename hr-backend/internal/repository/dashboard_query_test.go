// Package repository_test 对团队看板域两个新仓储做黑盒集成测试。
//
// 覆盖 TeamTrainingSuggestionRepository 建议行状态机读写（specs P2_TMD_001
// §5.1.2 步2、§5.1.4 规则2/5：幂等建行/重置续作/拾取锁/条件写守卫）与
// DashboardQueryRepository 聚合只读查询（§5.2.2、§4.1.4 规则6：区间并集、
// 多区间批量取行、tick 拾取扫描、批次终态时间）。SQLite :memory: 种子行
// 形态参照 profile_query_test.go。
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

// tmdbBaseUnix 团队看板测试基准 Unix 秒。
const tmdbBaseUnix = int64(1761000000)

// newDashboardTestDB 构造独立 :memory: SQLite，AutoMigrate 看板域消费五表，
// 雪花回调覆盖全部种子模型（单行与切片两种 Dest）。
func newDashboardTestDB(t *testing.T) *gorm.DB {
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
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_dashboard_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assign := func(id *int64) {
			if *id == 0 {
				*id = snowflake.NextID()
			}
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.TeamTrainingSuggestion:
			assign(&dest.ID)
		case *domain.ActivityStat:
			assign(&dest.ID)
		case *domain.DimensionScore:
			assign(&dest.ID)
		case *domain.AggregateScore:
			assign(&dest.ID)
		case *domain.AssessmentBatch:
			assign(&dest.ID)
		case *[]domain.TeamTrainingSuggestion:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.ActivityStat:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.DimensionScore:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.AggregateScore:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		}
	})
	if err := db.AutoMigrate(
		&domain.TeamTrainingSuggestion{},
		&domain.ActivityStat{},
		&domain.DimensionScore{},
		&domain.AggregateScore{},
		&domain.AssessmentBatch{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeSuggestionRow 构造建议行：generating 形态字段占位，终态字段由测试自行覆盖。
func makeSuggestionRow(batchNo string, start, end int64, status string) domain.TeamTrainingSuggestion {
	return domain.TeamTrainingSuggestion{
		BatchNo:       batchNo,
		PeriodStartAt: time.Unix(start, 0).UTC(),
		PeriodEndAt:   time.Unix(end, 0).UTC(),
		Status:        status,
		ModulesJSON:   "",
		Summary:       "",
		ModelName:     "",
		PromptVersion: "v1",
		ErrorSummary:  "",
	}
}

// makeDashboardBatch 构造批次行：triggeredAt/finishedAt 由测试另行覆盖。
func makeDashboardBatch(batchNo, triggerType, status string, start, end, triggeredAt int64) domain.AssessmentBatch {
	return domain.AssessmentBatch{
		BatchNo:         batchNo,
		TriggerType:     triggerType,
		TargetMode:      domain.BatchTargetAll,
		TargetNamesJSON: `[]`,
		TotalCount:      3,
		Status:          status,
		PeriodStartAt:   time.Unix(start, 0).UTC(),
		PeriodEndAt:     time.Unix(end, 0).UTC(),
		TriggeredAt:     time.Unix(triggeredAt, 0).UTC(),
	}
}

// loadSuggestionRow 按 period 双界取库内建议行（绕过被测仓储验证落库真值）。
func loadSuggestionRow(t *testing.T, db *gorm.DB, start, end int64) domain.TeamTrainingSuggestion {
	t.Helper()
	var row domain.TeamTrainingSuggestion
	if err := db.Where("period_start_at = ? AND period_end_at = ?",
		time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).First(&row).Error; err != nil {
		t.Fatalf("load suggestion row: %v", err)
	}
	return row
}

// ---- DashboardQueryRepository：区间并集与批量取行 ----

// TestListPeriodsUnionAndOrder 核心断言：activity_stats/dimension_scores/
// aggregate_scores 三表分别落 (W1,W2)、(W2,W3)、(W3,W4) 时返回并集新到旧
// [W4,W3,W2,W1]（specs §5.2.2 步1 区间列表推导）。
func TestListPeriodsUnionAndOrder(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2, w3, w4 := w1+7*86400, w1+14*86400, w1+21*86400

	activity := makeActivityRow("张三", w1, w2, 5)
	if err := db.Create(&activity).Error; err != nil {
		t.Fatalf("seed activity: %v", err)
	}
	score := makeScoreRow("张三", "AI_A", w2, w3, 70)
	if err := db.Create(&score).Error; err != nil {
		t.Fatalf("seed score: %v", err)
	}
	agg := makeAggRow("张三", domain.ModuleAIUsage, w3, w4, floatPtr(72.5))
	if err := db.Create(&agg).Error; err != nil {
		t.Fatalf("seed agg: %v", err)
	}

	bounds, err := repo.ListPeriods(ctx)
	if err != nil {
		t.Fatalf("ListPeriods: %v", err)
	}
	want := []struct{ start, end int64 }{{w3, w4}, {w2, w3}, {w1, w2}}
	if len(bounds) != len(want) {
		t.Fatalf("返回区间数 want %d, got %d: %+v", len(want), len(bounds), bounds)
	}
	for i, w := range want {
		if !bounds[i].StartAt.Equal(time.Unix(w.start, 0).UTC()) || !bounds[i].EndAt.Equal(time.Unix(w.end, 0).UTC()) {
			t.Fatalf("区间[%d] want (%d,%d), got (%v,%v)", i, w.start, w.end, bounds[i].StartAt, bounds[i].EndAt)
		}
	}
}

// TestListPeriodsEmpty 三表全空返回空列表非 error（看板空态语义，specs §5.2.4 规则2）。
func TestListPeriodsEmpty(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	bounds, err := repo.ListPeriods(context.Background())
	if err != nil {
		t.Fatalf("空表 want nil error, got %v", err)
	}
	if len(bounds) != 0 {
		t.Fatalf("空表 want 空列表, got %d 项", len(bounds))
	}
}

// TestListActivityByPeriod 双界精确匹配全行，他区间行隔离（specs §5.2.2 步1 三态计数取数）。
func TestListActivityByPeriod(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	seeds := []domain.ActivityStat{
		makeActivityRow("张三", start, end, 12),
		makeActivityRow("李四", start, end, 2),
		makeActivityRow("王五", end, end+7*86400, 8), // 他区间：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	list, err := repo.ListActivityByPeriod(ctx, start, end)
	if err != nil {
		t.Fatalf("ListActivityByPeriod: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2, got %d", len(list))
	}
	for _, row := range list {
		if !row.PeriodStartAt.Equal(time.Unix(start, 0).UTC()) || !row.PeriodEndAt.Equal(time.Unix(end, 0).UTC()) {
			t.Fatalf("混入非本区间行: %v~%v", row.PeriodStartAt, row.PeriodEndAt)
		}
	}
}

// TestListDimScoresByPeriod 双界精确匹配全公司行，含 failed 行不过滤
//（过滤在 service 层，specs §5.2.2 步1）。
func TestListDimScoresByPeriod(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	failedRow := makeScoreRow("李四", "AI_B", start, end, 0)
	failedRow.Status = domain.ScoreStatusFailed
	seeds := []domain.DimensionScore{
		makeScoreRow("张三", "AI_A", start, end, 80),
		failedRow,
		makeScoreRow("王五", "AI_C", end, end+7*86400, 90), // 他区间：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	list, err := repo.ListDimScoresByPeriod(ctx, start, end)
	if err != nil {
		t.Fatalf("ListDimScoresByPeriod: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2（含 failed）, got %d", len(list))
	}
	hasFailed := false
	for _, row := range list {
		if row.Status == domain.ScoreStatusFailed {
			hasFailed = true
		}
		if !row.PeriodStartAt.Equal(time.Unix(start, 0).UTC()) {
			t.Fatalf("混入非本区间行: %v", row.PeriodStartAt)
		}
	}
	if !hasFailed {
		t.Fatal("want failed 行一并返回不过滤")
	}
}

// TestListDimScoresByPeriods 窗口多区间批量取行，窗口外区间隔离
//（specs §5.2.4 规则1：批量 IN + 内存聚合，禁止逐区间查询）。
func TestListDimScoresByPeriods(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2, w3, w4 := w1+7*86400, w1+14*86400, w1+21*86400

	seeds := []domain.DimensionScore{
		makeScoreRow("张三", "AI_A", w1, w2, 60),
		makeScoreRow("张三", "AI_B", w1, w2, 65),
		makeScoreRow("李四", "AI_A", w2, w3, 70), // 窗口外：隔离
		makeScoreRow("张三", "AI_A", w3, w4, 75),
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	bounds := []repository.PeriodBound{
		{StartAt: time.Unix(w1, 0).UTC(), EndAt: time.Unix(w2, 0).UTC()},
		{StartAt: time.Unix(w3, 0).UTC(), EndAt: time.Unix(w4, 0).UTC()},
	}
	list, err := repo.ListDimScoresByPeriods(ctx, bounds)
	if err != nil {
		t.Fatalf("ListDimScoresByPeriods: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("返回行数 want 3, got %d: %+v", len(list), list)
	}
	for _, row := range list {
		if row.PeriodStartAt.Equal(time.Unix(w2, 0).UTC()) {
			t.Fatalf("窗口外区间行不应返回: %v", row.PeriodStartAt)
		}
	}
}

// TestListDimScoresByPeriods_EmptyBounds 空窗口返回空切片非 error。
func TestListDimScoresByPeriods_EmptyBounds(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	list, err := repo.ListDimScoresByPeriods(context.Background(), nil)
	if err != nil {
		t.Fatalf("空入参 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空入参 want 空列表, got %d 行", len(list))
	}
}

// TestListModuleAggScoresByPeriods 多区间模块聚合行，overview 哨兵行排除、
// 窗口外隔离（specs §5.1.2 步3 模块聚合分素材）。
func TestListModuleAggScoresByPeriods(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2, w3, w4 := w1+7*86400, w1+14*86400, w1+21*86400

	seeds := []domain.AggregateScore{
		makeAggRow("张三", domain.ModuleAIUsage, w1, w2, floatPtr(70.0)),
		makeAggRow("张三", domain.ModuleAIMgmt, w1, w2, floatPtr(65.0)),
		newOverviewRow("张三", w1, w2, 68.0), // overview 行：排除
		makeAggRow("李四", domain.ModuleAIUsage, w2, w3, floatPtr(80.0)), // 窗口外：隔离
		makeAggRow("张三", domain.ModuleAIUsage, w3, w4, floatPtr(75.0)),
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	bounds := []repository.PeriodBound{
		{StartAt: time.Unix(w1, 0).UTC(), EndAt: time.Unix(w2, 0).UTC()},
		{StartAt: time.Unix(w3, 0).UTC(), EndAt: time.Unix(w4, 0).UTC()},
	}
	list, err := repo.ListModuleAggScoresByPeriods(ctx, bounds)
	if err != nil {
		t.Fatalf("ListModuleAggScoresByPeriods: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("返回行数 want 3, got %d: %+v", len(list), list)
	}
	for _, row := range list {
		if row.Module == domain.ModuleOverview {
			t.Fatalf("overview 行不应返回: %+v", row)
		}
		if row.PeriodStartAt.Equal(time.Unix(w2, 0).UTC()) {
			t.Fatalf("窗口外区间行不应返回: %v", row.PeriodStartAt)
		}
	}
}

// TestFindLatestFinishedAt 核心断言：同区间两条终态批次取 finished_at 最新非空值，
// running 行（finished_at NULL）不参与，无匹配返 (nil, nil)（03 §1.8）。
func TestFindLatestFinishedAt(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	early := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, start)
	earlyFinished := time.Unix(start+3600, 0).UTC()
	early.FinishedAt = &earlyFinished
	late := makeDashboardBatch("B002", domain.BatchTriggerManual, domain.BatchStatusSuccess, start, end, start+60)
	lateFinished := time.Unix(start+7200, 0).UTC()
	late.FinishedAt = &lateFinished
	running := makeDashboardBatch("B003", domain.BatchTriggerScheduled, domain.BatchStatusRunning, start, end, start+120)
	for _, b := range []domain.AssessmentBatch{early, late, running} {
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("seed %s: %v", b.BatchNo, err)
		}
	}

	got, err := repo.FindLatestFinishedAt(ctx, start, end)
	if err != nil {
		t.Fatalf("FindLatestFinishedAt: %v", err)
	}
	if got == nil || !got.Equal(lateFinished) {
		t.Fatalf("want 最新非空 %v, got %v", lateFinished, got)
	}

	none, err := repo.FindLatestFinishedAt(ctx, end, end+7*86400)
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if none != nil {
		t.Fatalf("无匹配 want nil, got %v", none)
	}
}

// ---- DashboardQueryRepository：tick 拾取扫描 ----

// TestFindEarliestPendingSuggestBatch_NoRow 核心断言：周期批次终态且该 period
// 无建议行时命中（specs §5.1.4 规则1/2 判定 a：失败与部分失败批次同样命中）。
func TestFindEarliestPendingSuggestBatch_NoRow(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	success := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, start)
	if err := db.Create(&success).Error; err != nil {
		t.Fatalf("seed success: %v", err)
	}
	partial := makeDashboardBatch("B010", domain.BatchTriggerScheduled, domain.BatchStatusPartialFailed, end, end+7*86400, start+86400)
	if err := db.Create(&partial).Error; err != nil {
		t.Fatalf("seed partial: %v", err)
	}

	// success 批次无建议行：命中。
	got, err := repo.FindEarliestPendingSuggestBatch(ctx, nil)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch: %v", err)
	}
	if got == nil || got.BatchNo != "B001" {
		t.Fatalf("want 命中 B001, got %+v", got)
	}

	// B001 建议行已存在后，无建议行的 partial_failed 批次同样命中（规则1）。
	got2, err := repo.FindEarliestPendingSuggestBatch(ctx, []domain.TeamTrainingSuggestion{
		makeSuggestionRow("B001", start, end, domain.SuggestionStatusGenerated),
	})
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch(带建议行): %v", err)
	}
	if got2 == nil || got2.BatchNo != "B010" {
		t.Fatalf("want 命中无建议行的 partial_failed 批次 B010, got %+v", got2)
	}
}

// TestFindEarliestPendingSuggestBatch_Rerun 核心断言：建议行属 B1，同 period
// 存在 triggered_at 更晚的 B2 success 时命中 B2；B2 为 partial_failed 时返 nil；
// B2 更早（非重跑）时返 nil（specs §5.1.4 规则2 判定 b）。
func TestFindEarliestPendingSuggestBatch_Rerun(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400
	t1, t2, t3 := start, start+86400, start+2*86400

	b1 := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, t1)
	if err := db.Create(&b1).Error; err != nil {
		t.Fatalf("seed B001: %v", err)
	}
	rows := []domain.TeamTrainingSuggestion{
		makeSuggestionRow("B001", start, end, domain.SuggestionStatusGenerated),
	}

	// B2 success 更晚：命中 B2。
	b2 := makeDashboardBatch("B002", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, t2)
	if err := db.Create(&b2).Error; err != nil {
		t.Fatalf("seed B002: %v", err)
	}
	got, err := repo.FindEarliestPendingSuggestBatch(ctx, rows)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch: %v", err)
	}
	if got == nil || got.BatchNo != "B002" {
		t.Fatalf("want 命中重跑 success 批次 B002, got %+v", got)
	}

	// 换 B2 为 partial_failed：不覆盖既有建议，返 nil。
	db2 := newDashboardTestDB(t)
	repo2 := repository.NewDashboardQueryRepository(db2)
	if err := db2.Create(&b1).Error; err != nil {
		t.Fatalf("seed B001(库2): %v", err)
	}
	b2Partial := makeDashboardBatch("B002", domain.BatchTriggerScheduled, domain.BatchStatusPartialFailed, start, end, t2)
	if err := db2.Create(&b2Partial).Error; err != nil {
		t.Fatalf("seed B002 partial: %v", err)
	}
	got2, err := repo2.FindEarliestPendingSuggestBatch(ctx, rows)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch(partial): %v", err)
	}
	if got2 != nil {
		t.Fatalf("partial_failed 重跑不覆盖, want nil, got %+v", got2)
	}

	// B2 更早于建议行所属批次（非重跑）：返 nil。
	db3 := newDashboardTestDB(t)
	repo3 := repository.NewDashboardQueryRepository(db3)
	b1Late := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, t3)
	if err := db3.Create(&b1Late).Error; err != nil {
		t.Fatalf("seed B001(库3): %v", err)
	}
	b2Early := makeDashboardBatch("B002", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, t2)
	if err := db3.Create(&b2Early).Error; err != nil {
		t.Fatalf("seed B002 early: %v", err)
	}
	got3, err := repo3.FindEarliestPendingSuggestBatch(ctx, rows)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch(early): %v", err)
	}
	if got3 != nil {
		t.Fatalf("更早批次非重跑, want nil, got %+v", got3)
	}
}

// TestFindEarliestPendingSuggestBatch_MultiCandidate 多候选命中时取
// triggered_at 最早一条；manual 批次更早不参与（specs §5.1.2 步1「按批次时间最早」）。
func TestFindEarliestPendingSuggestBatch_MultiCandidate(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2, w3, w4 := w1+7*86400, w1+14*86400, w1+21*86400

	manual := makeDashboardBatch("B000", domain.BatchTriggerManual, domain.BatchStatusSuccess, w3, w4, w1)
	early := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusFailed, w1, w2, w1+3600)
	late := makeDashboardBatch("B002", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, w3, w4, w1+7200)
	for _, b := range []domain.AssessmentBatch{late, early, manual} {
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("seed %s: %v", b.BatchNo, err)
		}
	}

	got, err := repo.FindEarliestPendingSuggestBatch(ctx, nil)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch: %v", err)
	}
	if got == nil || got.BatchNo != "B001" {
		t.Fatalf("want 取 triggered_at 最早的周期批次 B001, got %+v", got)
	}
}

// TestFindEarliestPendingSuggestBatch_GeneratingLock 核心断言：建议行为
// generating（生成中行即拾取锁）时该 period 恒不命中，即便存在更新的 success
// 重跑批次（specs §5.1.4 规则5）。
func TestFindEarliestPendingSuggestBatch_GeneratingLock(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	b1 := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, start)
	if err := db.Create(&b1).Error; err != nil {
		t.Fatalf("seed B001: %v", err)
	}
	b2 := makeDashboardBatch("B002", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, start+86400)
	if err := db.Create(&b2).Error; err != nil {
		t.Fatalf("seed B002: %v", err)
	}
	generating := makeSuggestionRow("B001", start, end, domain.SuggestionStatusGenerating)

	got, err := repo.FindEarliestPendingSuggestBatch(ctx, []domain.TeamTrainingSuggestion{generating})
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch: %v", err)
	}
	if got != nil {
		t.Fatalf("生成中行存在期间扫描不命中, want nil, got %+v", got)
	}
}

// TestFindEarliestPendingSuggestBatch_NoTerminal 周期批次未终态（running）不参与。
func TestFindEarliestPendingSuggestBatch_NoTerminal(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewDashboardQueryRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	running := makeDashboardBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusRunning, start, end, start)
	if err := db.Create(&running).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := repo.FindEarliestPendingSuggestBatch(ctx, nil)
	if err != nil {
		t.Fatalf("FindEarliestPendingSuggestBatch: %v", err)
	}
	if got != nil {
		t.Fatalf("running 批次不参与, want nil, got %+v", got)
	}
}

// ---- TeamTrainingSuggestionRepository ----

// TestEnsureGenerating 核心断言三分支：无行新建返 true；终态行重置续作返 true
// 且 status 回 generating、batch_no 更新、内容列置空、generated_at 置 NULL；
// generating 行返 false 沿用拾取锁（specs §5.1.2 步2、§5.1.4 规则2/5）。
func TestEnsureGenerating(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	// 分支1：无行新建返 true，落 generating 行。
	first := makeSuggestionRow("B001", start, end, domain.SuggestionStatusGenerating)
	created, err := repo.EnsureGenerating(ctx, first)
	if err != nil {
		t.Fatalf("无行建行: %v", err)
	}
	if !created {
		t.Fatal("无行 want 返 true")
	}
	row := loadSuggestionRow(t, db, start, end)
	if row.Status != domain.SuggestionStatusGenerating || row.BatchNo != "B001" {
		t.Fatalf("建行 want generating/B001, got %s/%s", row.Status, row.BatchNo)
	}
	firstID := row.ID

	// 分支3：已存在 generating 行返 false（沿用拾取锁），行不被覆盖。
	second := makeSuggestionRow("B002", start, end, domain.SuggestionStatusGenerating)
	reused, err := repo.EnsureGenerating(ctx, second)
	if err != nil {
		t.Fatalf("generating 行再入: %v", err)
	}
	if reused {
		t.Fatal("generating 行 want 返 false（拾取锁）")
	}
	row = loadSuggestionRow(t, db, start, end)
	if row.BatchNo != "B001" {
		t.Fatalf("拾取锁期间行应沿用, want B001, got %s", row.BatchNo)
	}

	// 分支2：终态行（generated 带内容与生成时间）重置续作返 true。
	generatedAt := time.Unix(tmdbBaseUnix+3600, 0).UTC()
	if err := db.Model(&domain.TeamTrainingSuggestion{}).Where("id = ?", row.ID).
		Updates(map[string]any{
			"status":        domain.SuggestionStatusGenerated,
			"modules_json":  `[{"module":"AI_USAGE","suggestions":[]}]`,
			"summary":       "既有研判",
			"model_name":    "old-model",
			"error_summary": "",
			"generated_at":  &generatedAt,
		}).Error; err != nil {
		t.Fatalf("推终态: %v", err)
	}
	third := makeSuggestionRow("B003", start, end, domain.SuggestionStatusGenerating)
	reset, err := repo.EnsureGenerating(ctx, third)
	if err != nil {
		t.Fatalf("终态行重置: %v", err)
	}
	if !reset {
		t.Fatal("终态行重置 want 返 true")
	}
	row = loadSuggestionRow(t, db, start, end)
	if row.Status != domain.SuggestionStatusGenerating {
		t.Fatalf("重置后 status want generating, got %s", row.Status)
	}
	if row.BatchNo != "B003" {
		t.Fatalf("重置后 batch_no want B003, got %s", row.BatchNo)
	}
	if row.ModulesJSON != "" || row.Summary != "" || row.ModelName != "" {
		t.Fatalf("重置后内容列 want 置空, got modules=%q summary=%q model=%q", row.ModulesJSON, row.Summary, row.ModelName)
	}
	if row.ErrorSummary != "" {
		t.Fatalf("重置后 error_summary want 空串, got %q", row.ErrorSummary)
	}
	if row.GeneratedAt != nil {
		t.Fatalf("重置后 generated_at want NULL, got %v", row.GeneratedAt)
	}
	if row.ID != firstID {
		t.Fatalf("重置沿用既有行, want ID %d, got %d", firstID, row.ID)
	}
}

// TestEnsureGenerating_FailedReset failed 终态行同样重置续作（含 error_summary 清空）。
func TestEnsureGenerating_FailedReset(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	failed := makeSuggestionRow("B001", start, end, domain.SuggestionStatusFailed)
	failed.ErrorSummary = "LLM upstream timeout"
	if err := db.Create(&failed).Error; err != nil {
		t.Fatalf("seed failed 行: %v", err)
	}

	reset, err := repo.EnsureGenerating(ctx, makeSuggestionRow("B002", start, end, domain.SuggestionStatusGenerating))
	if err != nil {
		t.Fatalf("failed 行重置: %v", err)
	}
	if !reset {
		t.Fatal("failed 终态行重置 want 返 true")
	}
	row := loadSuggestionRow(t, db, start, end)
	if row.Status != domain.SuggestionStatusGenerating || row.BatchNo != "B002" {
		t.Fatalf("want generating/B002, got %s/%s", row.Status, row.BatchNo)
	}
	if row.ErrorSummary != "" {
		t.Fatalf("重置后 error_summary want 空串, got %q", row.ErrorSummary)
	}
}

// TestMarkGeneratedGuard 核心断言：行已 failed 时 MarkGenerated 落不下
//（status 守卫 affected=0 幂等返回 nil）；正路径 generating 行 MarkGenerated
// 落 generated 值齐全、MarkFailed 守卫同款。
func TestMarkGeneratedGuard(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	failedRow := makeSuggestionRow("B001", start, end, domain.SuggestionStatusFailed)
	failedRow.ErrorSummary = "exhausted"
	if err := db.Create(&failedRow).Error; err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := repo.MarkGenerated(ctx, failedRow.ID, "B001", `[{"module":"AI_USAGE"}]`, "研判", "m1", time.Unix(tmdbBaseUnix+3600, 0).UTC()); err != nil {
		t.Fatalf("MarkGenerated want 幂等 nil error, got %v", err)
	}
	row := loadSuggestionRow(t, db, start, end)
	if row.Status != domain.SuggestionStatusFailed {
		t.Fatalf("failed 行 MarkGenerated 后 want 仍 failed, got %s", row.Status)
	}
	if row.ErrorSummary != "exhausted" {
		t.Fatalf("failed 行 want 原值保留, got %q", row.ErrorSummary)
	}

	// 正路径：generating 行 MarkGenerated 落 generated 全字段。
	generatingRow := makeSuggestionRow("B002", end, end+7*86400, domain.SuggestionStatusGenerating)
	if err := db.Create(&generatingRow).Error; err != nil {
		t.Fatalf("seed generating: %v", err)
	}
	generatedAt := time.Unix(tmdbBaseUnix+7200, 0).UTC()
	if err := repo.MarkGenerated(ctx, generatingRow.ID, "B002", `[{"module":"AI_USAGE","suggestions":[]}]`, "综合研判", "kimi", generatedAt); err != nil {
		t.Fatalf("MarkGenerated 正路径: %v", err)
	}
	done := loadSuggestionRow(t, db, end, end+7*86400)
	if done.Status != domain.SuggestionStatusGenerated || done.BatchNo != "B002" {
		t.Fatalf("want generated/B002, got %s/%s", done.Status, done.BatchNo)
	}
	if done.ModulesJSON == "" || done.Summary == "" || done.ModelName != "kimi" {
		t.Fatalf("内容列未落全: modules=%q summary=%q model=%q", done.ModulesJSON, done.Summary, done.ModelName)
	}
	if done.GeneratedAt == nil || !done.GeneratedAt.Equal(generatedAt) {
		t.Fatalf("generated_at want %v, got %v", generatedAt, done.GeneratedAt)
	}

	// generated 终态后再 MarkFailed：守卫拦下，状态不变。
	if err := repo.MarkFailed(ctx, done.ID, "误投递"); err != nil {
		t.Fatalf("MarkFailed want 幂等 nil error, got %v", err)
	}
	done = loadSuggestionRow(t, db, end, end+7*86400)
	if done.Status != domain.SuggestionStatusGenerated || done.ErrorSummary != "" {
		t.Fatalf("generated 行 MarkFailed 后 want 原值保留, got %s/%q", done.Status, done.ErrorSummary)
	}
}

// TestMarkFailedPath generating 行 MarkFailed 落 failed 与原因。
func TestMarkFailedPath(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	start, end := tmdbBaseUnix, tmdbBaseUnix+7*86400

	row := makeSuggestionRow("B001", start, end, domain.SuggestionStatusGenerating)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := repo.MarkFailed(ctx, row.ID, "LLM upstream error"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got := loadSuggestionRow(t, db, start, end)
	if got.Status != domain.SuggestionStatusFailed || got.ErrorSummary != "LLM upstream error" {
		t.Fatalf("want failed/LLM upstream error, got %s/%q", got.Status, got.ErrorSummary)
	}
}

// TestSuggestionFindLatest 核心断言：FindLatest 按 period 起止新到旧取第一条，
// 不随生成时间（specs §4.1.4 规则6：排序键为 period 而非生成时间）；
// 无行返 (nil, nil)。
func TestSuggestionFindLatest(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2, w3 := w1+7*86400, w1+14*86400

	// 旧区间行生成时间晚于新区间行（补生成场景），取行仍按 period 新到旧。
	oldLate := makeSuggestionRow("B001", w1, w2, domain.SuggestionStatusGenerated)
	newEarly := makeSuggestionRow("B002", w2, w3, domain.SuggestionStatusGenerated)
	failedRow := makeSuggestionRow("B003", w1+86400*14, w3+7*86400, domain.SuggestionStatusFailed) // 最新区间 failed 行
	for _, row := range []domain.TeamTrainingSuggestion{oldLate, newEarly, failedRow} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", row.BatchNo, err)
		}
	}

	got, err := repo.FindLatest(ctx)
	if err != nil {
		t.Fatalf("FindLatest: %v", err)
	}
	if got == nil || got.BatchNo != "B003" {
		t.Fatalf("want period 最新行 B003, got %+v", got)
	}

	empty := newDashboardTestDB(t)
	emptyRepo := repository.NewTeamTrainingSuggestionRepository(empty)
	none, err := emptyRepo.FindLatest(ctx)
	if err != nil {
		t.Fatalf("无行 want nil error, got %v", err)
	}
	if none != nil {
		t.Fatalf("无行 want nil, got %+v", none)
	}
}

// TestSuggestionGetByPeriodAndFindAll GetByPeriod 双界取行与无行 nil；
// FindAll 全量行（tick 拾取判定的建议行输入）。
func TestSuggestionGetByPeriodAndFindAll(t *testing.T) {
	db := newDashboardTestDB(t)
	repo := repository.NewTeamTrainingSuggestionRepository(db)
	ctx := context.Background()
	w1 := tmdbBaseUnix
	w2 := w1 + 7*86400

	rows := []domain.TeamTrainingSuggestion{
		makeSuggestionRow("B001", w1, w2, domain.SuggestionStatusGenerated),
		makeSuggestionRow("B002", w2, w2+7*86400, domain.SuggestionStatusGenerating),
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	got, err := repo.GetByPeriod(ctx, w2, w2+7*86400)
	if err != nil {
		t.Fatalf("GetByPeriod: %v", err)
	}
	if got == nil || got.BatchNo != "B002" {
		t.Fatalf("want B002, got %+v", got)
	}
	none, err := repo.GetByPeriod(ctx, w2+7*86400, w2+14*86400)
	if err != nil {
		t.Fatalf("无行 GetByPeriod want nil error, got %v", err)
	}
	if none != nil {
		t.Fatalf("无行 want nil, got %+v", none)
	}

	all, err := repo.FindAll(ctx)
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("FindAll want 2 行, got %d", len(all))
	}
}
