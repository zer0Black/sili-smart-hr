// Package repository_test 对个人画像只读查询扩展做黑盒集成测试。
//
// 覆盖 DimensionScoreRepository 三个画像查询：ListByToken 按人全量各期行
// （specs P2_PRF_001 §5.2.2 步骤4 走势组装）、ListLatestByTokens 每人每模块
// 各自最新聚合周期批量取行（§5.1.2 步骤4，BR1/BR2）、ListByPeriodAllCompany
// 双界精确匹配全公司行含 insufficient/failed 不过滤（§5.2.2 步骤5，BR3，过滤在
// service 层做）；AggregateScoreRepository 两查询（§4.1.2 B、§5.2.2 步骤2/4）、
// ActivityStatRepository 两查询（§5.2.2 步骤2/3）、AssessmentTestResultRepository
// 判型批量查询（§5.1.2 步骤4、03 §1.8）。复用既有测试设施。
package repository_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// TestDimensionScore_ListByToken 核心断言：同人 3 期各 2 维度行全量返回 6 行且按
// period_start_at ASC；含全部 status/source 行（failed 与 active_test 行不剔除），
// 他人行隔离（specs §5.2.2 步骤4）。
func TestDimensionScore_ListByToken(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	p1, p2, p3 := scoreBaseUnix, scoreBaseUnix+7*86400, scoreBaseUnix+14*86400

	failedRow := makeScoreRow("张敏", "AI_F", p2, p2+7*86400, 0)
	failedRow.Status = domain.ScoreStatusFailed
	failedRow.ErrorCode = "ErrLLMEvalUpstream"
	testRow := makeScoreRow("张敏", "AI_T", p2, p2+7*86400, 75)
	testRow.Source = domain.ScoreSourceActiveTest

	// 倒序插入，验证 ASC 由 ORDER BY 保证而非插入顺序。
	seeds := []domain.DimensionScore{
		testRow,
		makeScoreRow("张敏", "AI_B", p3, p3+7*86400, 70),
		makeScoreRow("张敏", "AI_A", p3, p3+7*86400, 80),
		failedRow,
		makeScoreRow("张敏", "AI_B", p1, p1+7*86400, 50),
		makeScoreRow("张敏", "AI_A", p1, p1+7*86400, 60),
		makeScoreRow("李四", "AI_A", p1, p1+7*86400, 40), // 他人：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seeds[i].DimensionCode, err)
		}
	}

	list, err := repo.ListByToken(ctx, "张敏")
	if err != nil {
		t.Fatalf("ListByToken: %v", err)
	}
	if len(list) != 6 {
		t.Fatalf("返回行数 want 6, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i].PeriodStartAt.Before(list[i-1].PeriodStartAt) {
			t.Fatalf("period_start_at want ASC, got [%d]=%v < [%d]=%v", i, list[i].PeriodStartAt, i-1, list[i-1].PeriodStartAt)
		}
	}
	if !list[0].PeriodStartAt.Equal(time.Unix(p1, 0).UTC()) {
		t.Fatalf("首行周期 want p1, got %v", list[0].PeriodStartAt)
	}
	if !list[len(list)-1].PeriodStartAt.Equal(time.Unix(p3, 0).UTC()) {
		t.Fatalf("末行周期 want p3, got %v", list[len(list)-1].PeriodStartAt)
	}
	// 全 status/source 口径：failed 行与 active_test 行均在结果内。
	hasFailed, hasTest := false, false
	for _, row := range list {
		if row.Status == domain.ScoreStatusFailed {
			hasFailed = true
		}
		if row.Source == domain.ScoreSourceActiveTest {
			hasTest = true
		}
		if row.TokenName != "张敏" {
			t.Fatalf("混入他人行: %s", row.TokenName)
		}
	}
	if !hasFailed || !hasTest {
		t.Fatalf("want 含 failed 与 active_test 行, got failed=%v active_test=%v", hasFailed, hasTest)
	}
}

// TestDimensionScore_ListByTokenNoMatch 无落库行返回空列表非 error。
func TestDimensionScore_ListByTokenNoMatch(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	list, err := repo.ListByToken(context.Background(), "无人")
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(list))
	}
}

// TestDimensionScore_ListLatestByTokens 核心断言：每人每模块各自取最新聚合周期
// （两模块周期可错位），旧期行不返回；批量 IN 圈人，名单外人员行隔离
// （specs §5.1.2 步骤4、§4.1.4 规则5）。
func TestDimensionScore_ListLatestByTokens(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	oldStart, newStart := scoreBaseUnix, scoreBaseUnix+7*86400
	oldEnd, newEnd := oldStart+7*86400, newStart+7*86400

	zmAiOld := []domain.DimensionScore{
		makeScoreRow("张敏", "AI_U1", oldStart, oldEnd, 60),
		makeScoreRow("张敏", "AI_U2", oldStart, oldEnd, 65),
	}
	zmAiNew := []domain.DimensionScore{
		makeScoreRow("张敏", "AI_U1", newStart, newEnd, 70),
		makeScoreRow("张敏", "AI_U2", newStart, newEnd, 75),
		makeScoreRow("张敏", "AI_U3", newStart, newEnd, 80),
	}
	zmMgmt := []domain.DimensionScore{
		makeScoreRow("张敏", "AI_M1", newStart, newEnd, 66),
		makeScoreRow("张敏", "AI_M2", newStart, newEnd, 68),
	}
	for i := range zmMgmt {
		zmMgmt[i].Module = domain.ModuleAIMgmt
	}
	liOnlyOld := []domain.DimensionScore{
		makeScoreRow("李四", "AI_U1", oldStart, oldEnd, 55),
	}
	outsider := makeScoreRow("王五", "AI_U1", newStart, newEnd, 90) // 名单外：隔离

	seeds := append(append(append(zmAiOld, zmAiNew...), append(zmMgmt, liOnlyOld...)...), outsider)
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seeds[i].DimensionCode, err)
		}
	}

	list, err := repo.ListLatestByTokens(ctx, []string{"张敏", "李四"})
	if err != nil {
		t.Fatalf("ListLatestByTokens: %v", err)
	}
	if len(list) != 6 {
		t.Fatalf("返回行数 want 6, got %d: %+v", len(list), list)
	}
	type key struct{ token, module, code, start string }
	got := map[key]bool{}
	for _, row := range list {
		k := key{row.TokenName, row.Module, row.DimensionCode, row.PeriodStartAt.Format(time.RFC3339)}
		got[k] = true
		if row.TokenName == "张敏" && row.Module == domain.ModuleAIUsage && row.PeriodStartAt.Equal(time.Unix(oldStart, 0).UTC()) {
			t.Fatalf("张敏 AI_USAGE 旧期行不应返回: %s %v", row.DimensionCode, row.PeriodStartAt)
		}
		if row.TokenName == "王五" {
			t.Fatal("名单外人员行不应返回")
		}
	}
	for _, row := range append(zmAiNew, append(zmMgmt, liOnlyOld...)...) {
		k := key{row.TokenName, row.Module, row.DimensionCode, row.PeriodStartAt.Format(time.RFC3339)}
		if !got[k] {
			t.Fatalf("want 最新周期行 %s/%s/%s@%v 在结果内", row.TokenName, row.Module, row.DimensionCode, row.PeriodStartAt)
		}
	}
}

// TestDimensionScore_ListLatestByTokens_EmptyInput 核心断言：空 tokenNames 直接
// 返回空切片且无 error（不发起 SQL）。
func TestDimensionScore_ListLatestByTokens_EmptyInput(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	seed := makeScoreRow("张敏", "AI_U1", scoreBaseUnix, scoreBaseUnix+7*86400, 70)
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, err := repo.ListLatestByTokens(context.Background(), []string{})
	if err != nil {
		t.Fatalf("空入参 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空入参 want 空切片, got %d 行", len(list))
	}
	if list == nil {
		t.Fatal("空入参 want 非 nil 空切片")
	}
}

// TestDimensionScore_ListByPeriodAllCompany 核心断言：按双界精确匹配取全公司
// 维度行，insufficient 与 failed 行一并返回不过滤（过滤在 service 层做），
// 其他区间行隔离（specs §5.2.2 步骤5）。
func TestDimensionScore_ListByPeriodAllCompany(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	insufficientRow := makeScoreRow("张三", "AI_B", start, end, 0)
	insufficientRow.Insufficient = true
	failedRow := makeScoreRow("李四", "EN_E", start, end, 0)
	failedRow.Module = domain.ModuleEnneagram
	failedRow.Status = domain.ScoreStatusFailed
	failedRow.ErrorCode = "ErrLLMEvalUpstream"

	seeds := []domain.DimensionScore{
		makeScoreRow("张三", "AI_A", start, end, 80),
		insufficientRow,
		failedRow,
		makeScoreRow("王五", "AI_C", start, end, 75),
		makeScoreRow("赵六", "AI_D", end, end+7*86400, 90), // 其他区间：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seeds[i].DimensionCode, err)
		}
	}

	list, err := repo.ListByPeriodAllCompany(ctx, start, end)
	if err != nil {
		t.Fatalf("ListByPeriodAllCompany: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("返回行数 want 4, got %d", len(list))
	}
	hasInsufficient, hasFailed := false, false
	for _, row := range list {
		if !row.PeriodStartAt.Equal(time.Unix(start, 0).UTC()) || !row.PeriodEndAt.Equal(time.Unix(end, 0).UTC()) {
			t.Fatalf("混入非本区间行: %v~%v", row.PeriodStartAt, row.PeriodEndAt)
		}
		if row.DimensionCode == "AI_B" && row.Insufficient {
			hasInsufficient = true
		}
		if row.DimensionCode == "EN_E" && row.Status == domain.ScoreStatusFailed {
			hasFailed = true
		}
		if row.DimensionCode == "AI_D" {
			t.Fatal("其他区间行不应返回")
		}
	}
	if !hasInsufficient || !hasFailed {
		t.Fatalf("want insufficient 与 failed 行不过滤, got insufficient=%v failed=%v", hasInsufficient, hasFailed)
	}
}

// TestDimensionScore_ListByPeriodAllCompanyNoMatch 无落库行返回空列表非 error。
func TestDimensionScore_ListByPeriodAllCompanyNoMatch(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	list, err := repo.ListByPeriodAllCompany(context.Background(), scoreBaseUnix, scoreBaseUnix+3600)
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(list))
	}
}

// TestDimensionScoreListQueryErrorClosedConnection 连接关闭后三方法错误透传。
func TestDimensionScoreListQueryErrorClosedConnection(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	ctx := context.Background()
	if _, err := repo.ListByToken(ctx, "张三"); err == nil {
		t.Fatal("连接关闭后 ListByToken want error")
	}
	if _, err := repo.ListLatestByTokens(ctx, []string{"张三"}); err == nil {
		t.Fatal("连接关闭后 ListLatestByTokens want error")
	}
	if _, err := repo.ListByPeriodAllCompany(ctx, 0, 1); err == nil {
		t.Fatal("连接关闭后 ListByPeriodAllCompany want error")
	}
}

// ---- AggregateScoreRepository 画像只读查询 ----

// TestAggregateScore_ListByToken 核心断言：同人 2 期 × 3 行（AI_USAGE/AI_MGMT/
// overview）全量返回 6 行且 period ASC，含 overview 行（specs §5.2.2 步骤2 区间
// 并集、步骤4 较上期），他人行隔离。
func TestAggregateScore_ListByToken(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	p1, p2 := scoreBaseUnix, scoreBaseUnix+7*86400

	p1Overview := makeAggRow("张敏", domain.ModuleOverview, p1, p1+7*86400, nil)
	p1Overview.OverviewScore = floatPtr(60.0)
	p2Overview := makeAggRow("张敏", domain.ModuleOverview, p2, p2+7*86400, nil)
	p2Overview.OverviewScore = floatPtr(70.0)

	// 倒序插入，验证 ASC 由 ORDER BY 保证而非插入顺序。
	seeds := []domain.AggregateScore{
		p2Overview,
		makeAggRow("张敏", domain.ModuleAIMgmt, p2, p2+7*86400, floatPtr(68.0)),
		makeAggRow("张敏", domain.ModuleAIUsage, p2, p2+7*86400, floatPtr(78.0)),
		p1Overview,
		makeAggRow("张敏", domain.ModuleAIMgmt, p1, p1+7*86400, floatPtr(65.0)),
		makeAggRow("张敏", domain.ModuleAIUsage, p1, p1+7*86400, floatPtr(75.0)),
		makeAggRow("李四", domain.ModuleAIUsage, p1, p1+7*86400, floatPtr(40.0)), // 他人：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s@%d: %v", seeds[i].Module, seeds[i].PeriodStartAt.Unix(), err)
		}
	}

	list, err := repo.ListByToken(ctx, "张敏")
	if err != nil {
		t.Fatalf("ListByToken: %v", err)
	}
	if len(list) != 6 {
		t.Fatalf("返回行数 want 6, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i].PeriodStartAt.Before(list[i-1].PeriodStartAt) {
			t.Fatalf("period_start_at want ASC, got [%d]=%v < [%d]=%v", i, list[i].PeriodStartAt, i-1, list[i-1].PeriodStartAt)
		}
	}
	overviewCount := 0
	for _, row := range list {
		if row.TokenName != "张敏" {
			t.Fatalf("混入他人行: %s", row.TokenName)
		}
		if row.Module == domain.ModuleOverview {
			overviewCount++
		}
	}
	if overviewCount != 2 {
		t.Fatalf("overview 行 want 每期 1 行共 2, got %d", overviewCount)
	}
	if !list[0].PeriodStartAt.Equal(time.Unix(p1, 0).UTC()) || !list[len(list)-1].PeriodStartAt.Equal(time.Unix(p2, 0).UTC()) {
		t.Fatalf("首末行周期 want p1..p2, got %v..%v", list[0].PeriodStartAt, list[len(list)-1].PeriodStartAt)
	}
}

// TestAggregateScore_ListByTokenNoMatch 无落库行返回空列表非 error。
func TestAggregateScore_ListByTokenNoMatch(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	list, err := repo.ListByToken(context.Background(), "无人")
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(list))
	}
}

// TestAggregateScore_ListLatestModuleRowsByTokens 核心断言：每人每模块各自取最新
// 聚合周期（两模块可错位），不含 overview 行；张敏 AI_USAGE 两期取新期、AI_MGMT 仅
// 旧期照返回（各自模块最新）；李四无行时不出现；名单外人员行隔离（specs §4.1.2 B、
// §4.1.4 规则5、§5.1.4 规则2）。
func TestAggregateScore_ListLatestModuleRowsByTokens(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	ctx := context.Background()
	oldStart, newStart := scoreBaseUnix, scoreBaseUnix+7*86400
	oldEnd, newEnd := oldStart+7*86400, newStart+7*86400

	seeds := []domain.AggregateScore{
		makeAggRow("张敏", domain.ModuleAIUsage, oldStart, oldEnd, floatPtr(75.0)),
		makeAggRow("张敏", domain.ModuleAIUsage, newStart, newEnd, floatPtr(78.0)),
		makeAggRow("张敏", domain.ModuleAIMgmt, oldStart, oldEnd, floatPtr(65.0)), // 仅旧期：旧期即最新
		newOverviewRow("张敏", newStart, newEnd, 70.0),                          // overview 行：排除
		makeAggRow("王五", domain.ModuleAIUsage, newStart, newEnd, floatPtr(90.0)), // 名单外：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s@%d: %v", seeds[i].Module, seeds[i].PeriodStartAt.Unix(), err)
		}
	}

	list, err := repo.ListLatestModuleRowsByTokens(ctx, []string{"张敏", "李四"})
	if err != nil {
		t.Fatalf("ListLatestModuleRowsByTokens: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2, got %d: %+v", len(list), list)
	}
	type key struct{ token, module string }
	got := map[key]float64{}
	for _, row := range list {
		if row.Module == domain.ModuleOverview {
			t.Fatalf("overview 行不应返回: %+v", row)
		}
		if row.TokenName == "李四" {
			t.Fatal("无行人（李四）不应出现")
		}
		if row.TokenName == "王五" {
			t.Fatal("名单外人员行不应返回")
		}
		if row.ModuleScore != nil {
			got[key{row.TokenName, row.Module}] = *row.ModuleScore
		}
	}
	if v, ok := got[key{"张敏", domain.ModuleAIUsage}]; !ok || v != 78.0 {
		t.Fatalf("张敏 AI_USAGE want 新期 78.0, got %v ok=%v", v, ok)
	}
	if v, ok := got[key{"张敏", domain.ModuleAIMgmt}]; !ok || v != 65.0 {
		t.Fatalf("张敏 AI_MGMT want 旧期 65.0（该模块最新）, got %v ok=%v", v, ok)
	}
}

// TestAggregateScore_ListLatestModuleRowsByTokens_EmptyInput 空入参返回空切片
// 非 nil 且无 error（不发起 SQL）。
func TestAggregateScore_ListLatestModuleRowsByTokens_EmptyInput(t *testing.T) {
	db := newAggregateTestDB(t)
	repo := repository.NewAggregateScoreRepository(db)
	seed := makeAggRow("张敏", domain.ModuleAIUsage, scoreBaseUnix, scoreBaseUnix+7*86400, floatPtr(70.0))
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, err := repo.ListLatestModuleRowsByTokens(context.Background(), []string{})
	if err != nil {
		t.Fatalf("空入参 want nil error, got %v", err)
	}
	if len(list) != 0 || list == nil {
		t.Fatalf("空入参 want 非 nil 空切片, got %v", list)
	}
}

// newOverviewRow 构造总览行（module=overview，overview_score 落值）。
func newOverviewRow(token string, start, end int64, score float64) domain.AggregateScore {
	row := makeAggRow(token, domain.ModuleOverview, start, end, nil)
	row.OverviewScore = floatPtr(score)
	return row
}

// ---- ActivityStatRepository 画像只读查询 ----

// TestActivityStat_ListByToken 核心断言：同人两期行全量返回且 period ASC，
// 他人行隔离（specs §5.2.2 步骤2 区间并集、步骤3 所选区间行）。
func TestActivityStat_ListByToken(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	p1, p2 := scoreBaseUnix, scoreBaseUnix+7*86400

	// 倒序插入，验证 ASC 由 ORDER BY 保证而非插入顺序。
	seeds := []domain.ActivityStat{
		makeActivityRow("张敏", p2, p2+7*86400, 20),
		makeActivityRow("张敏", p1, p1+7*86400, 8),
		makeActivityRow("李四", p1, p1+7*86400, 5), // 他人：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s@%d: %v", seeds[i].TokenName, seeds[i].PeriodStartAt.Unix(), err)
		}
	}

	list, err := repo.ListByToken(ctx, "张敏")
	if err != nil {
		t.Fatalf("ListByToken: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2, got %d", len(list))
	}
	if !list[0].PeriodStartAt.Equal(time.Unix(p1, 0).UTC()) || !list[1].PeriodStartAt.Equal(time.Unix(p2, 0).UTC()) {
		t.Fatalf("周期 want p1,p2 ASC, got %v,%v", list[0].PeriodStartAt, list[1].PeriodStartAt)
	}
	for _, row := range list {
		if row.TokenName != "张敏" {
			t.Fatalf("混入他人行: %s", row.TokenName)
		}
	}
}

// TestActivityStat_ListByTokenNoMatch 无落库行返回空列表非 error。
func TestActivityStat_ListByTokenNoMatch(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	list, err := repo.ListByToken(context.Background(), "无人")
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(list))
	}
}

// TestActivityStat_ListLatestByTokens 核心断言：同人两期仅最新 1 行返回，一人一行；
// 名单外人员行隔离（specs §4.1.2 B 活跃度列、§5.1.4 规则2）。
func TestActivityStat_ListLatestByTokens(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	ctx := context.Background()
	p1, p2 := scoreBaseUnix, scoreBaseUnix+7*86400

	latest := makeActivityRow("张敏", p2, p2+7*86400, 20)
	latest.ActiveLevel = domain.ActiveLevelActive
	old := makeActivityRow("张敏", p1, p1+7*86400, 5)
	old.ActiveLevel = domain.ActiveLevelLowFreq
	outsider := makeActivityRow("王五", p2, p2+7*86400, 30) // 名单外：隔离

	for i, row := range []domain.ActivityStat{latest, old, outsider} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	list, err := repo.ListLatestByTokens(ctx, []string{"张敏", "李四"})
	if err != nil {
		t.Fatalf("ListLatestByTokens: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("返回行数 want 1, got %d: %+v", len(list), list)
	}
	if list[0].TokenName != "张敏" || list[0].ActiveLevel != domain.ActiveLevelActive {
		t.Fatalf("want 张敏最新期 active 行, got %s/%s", list[0].TokenName, list[0].ActiveLevel)
	}
	if !list[0].PeriodStartAt.Equal(time.Unix(p2, 0).UTC()) {
		t.Fatalf("want 最新周期 p2, got %v", list[0].PeriodStartAt)
	}
}

// TestActivityStat_ListLatestByTokens_EmptyInput 空入参返回空切片非 nil 且无 error。
func TestActivityStat_ListLatestByTokens_EmptyInput(t *testing.T) {
	db := newActivityTestDB(t)
	repo := repository.NewActivityStatRepository(db)
	seed := makeActivityRow("张敏", scoreBaseUnix, scoreBaseUnix+7*86400, 5)
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, err := repo.ListLatestByTokens(context.Background(), []string{})
	if err != nil {
		t.Fatalf("空入参 want nil error, got %v", err)
	}
	if len(list) != 0 || list == nil {
		t.Fatalf("空入参 want 非 nil 空切片, got %v", list)
	}
}

// ---- AssessmentTestResultRepository 画像只读查询 ----

// seedEnneagramTask 落一条 enneagram 任务行并返回主键。
func seedEnneagramTask(t *testing.T, db *gorm.DB, staffName string, createdAt time.Time) domain.AssessmentTestTask {
	t.Helper()
	task := domain.AssessmentTestTask{
		TaskNo: fmt.Sprintf("E%d", createdAt.UnixNano()), TestType: domain.TestTypeEnneagram,
		StaffID: staffName, StaffName: staffName,
		Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusScored,
		QuestionIDsJSON: "[]", ScaleKey: "enneagram", DimensionCodesJSON: "[]",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task %s: %v", staffName, err)
	}
	// 结果行最新判定走 created_at，任务行显式回填制造「新任务晚于旧任务」。
	if err := db.Model(&domain.AssessmentTestTask{}).Where("id = ?", task.ID).
		Updates(map[string]any{"created_at": createdAt, "updated_at": createdAt}).Error; err != nil {
		t.Fatalf("backdate task %s: %v", staffName, err)
	}
	return task
}

// makeResultRow 构造判型结果行。
func makeResultRow(taskID int64, mainType, grading string, createdAt time.Time) domain.AssessmentTestResult {
	return domain.AssessmentTestResult{
		TaskID: taskID, MainType: mainType, WingType: "2",
		DistributionJSON: `{"3":45.5}`, Rationale: "判型依据",
		ModelName: "test-model", PromptVersion: "v1", GradingStatus: grading,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

// TestResult_ListLatestScoredByStaffNames 核心断言：张敏两任务（旧任务 scored、新任务
// degraded 占位），map["张敏"] 取旧 scored 行（跳过更新的 degraded 行，03 §1.8）；
// 李四无任务不在 map；空入参返回空 map（specs §5.1.2 步骤4、§4.1.2 B）。
func TestResult_ListLatestScoredByStaffNames(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	ctx := context.Background()
	oldAt := time.Unix(scoreBaseUnix, 0).UTC()
	newAt := oldAt.Add(7 * 86400 * time.Second)

	oldTask := seedEnneagramTask(t, db, "张敏", oldAt)
	newTask := seedEnneagramTask(t, db, "张敏", newAt)
	oldRow := makeResultRow(oldTask.ID, "3", domain.GradingStatusScored, oldAt)
	newRow := makeResultRow(newTask.ID, "", domain.GradingStatusDegraded, newAt) // 降级占位：过滤
	if err := db.Create(&oldRow).Error; err != nil {
		t.Fatalf("seed old result: %v", err)
	}
	if err := db.Create(&newRow).Error; err != nil {
		t.Fatalf("seed new result: %v", err)
	}
	// ai_mgmt 任务同名同人：JOIN 圈任务时排除。
	aiMgmt := domain.AssessmentTestTask{
		TaskNo: fmt.Sprintf("T%d", newAt.UnixNano()), TestType: domain.TestTypeAIMgmt,
		StaffID: "张敏", StaffName: "张敏",
		Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusScored,
		QuestionIDsJSON: "[]", DimensionCodesJSON: "[]",
	}
	if err := db.Create(&aiMgmt).Error; err != nil {
		t.Fatalf("seed ai_mgmt task: %v", err)
	}

	got, err := repo.ListLatestScoredByStaffNames(ctx, []string{"张敏", "李四"})
	if err != nil {
		t.Fatalf("ListLatestScoredByStaffNames: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("map 大小 want 1, got %d: %+v", len(got), got)
	}
	row, ok := got["张敏"]
	if !ok {
		t.Fatal("map[张敏] 不存在")
	}
	if row.TaskID != oldTask.ID || row.MainType != "3" {
		t.Fatalf("want 旧任务 scored 行 (task=%d main=3), got task=%d main=%q", oldTask.ID, row.TaskID, row.MainType)
	}
	if _, exists := got["李四"]; exists {
		t.Fatal("李四无任务不应在 map 内")
	}
}

// TestResult_ListLatestScoredByStaffNames_MultiScored 同人多任务均 scored 取
// created_at 最新（旧 3 型 → 新 5 型，最新覆盖）。
func TestResult_ListLatestScoredByStaffNames_MultiScored(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	ctx := context.Background()
	oldAt := time.Unix(scoreBaseUnix, 0).UTC()
	newAt := oldAt.Add(7 * 86400 * time.Second)

	oldTask := seedEnneagramTask(t, db, "张敏", oldAt)
	newTask := seedEnneagramTask(t, db, "张敏", newAt)
	oldRow := makeResultRow(oldTask.ID, "3", domain.GradingStatusScored, oldAt)
	newRow := makeResultRow(newTask.ID, "5", domain.GradingStatusScored, newAt)
	if err := db.Create(&oldRow).Error; err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := db.Create(&newRow).Error; err != nil {
		t.Fatalf("seed new: %v", err)
	}

	got, err := repo.ListLatestScoredByStaffNames(ctx, []string{"张敏"})
	if err != nil {
		t.Fatalf("ListLatestScoredByStaffNames: %v", err)
	}
	row, ok := got["张敏"]
	if !ok || row.MainType != "5" {
		t.Fatalf("want 最新 scored 行 main=5, got %+v ok=%v", row, ok)
	}
}

// TestResult_ListLatestScoredByStaffNames_EmptyInput 空入参返回空 map 非 nil
// 且无 error（不发起 SQL）。
func TestResult_ListLatestScoredByStaffNames_EmptyInput(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	task := seedEnneagramTask(t, db, "张敏", time.Unix(scoreBaseUnix, 0).UTC())
	scoredRow := makeResultRow(task.ID, "3", domain.GradingStatusScored, time.Unix(scoreBaseUnix, 0).UTC())
	if err := db.Create(&scoredRow).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := repo.ListLatestScoredByStaffNames(context.Background(), []string{})
	if err != nil {
		t.Fatalf("空入参 want nil error, got %v", err)
	}
	if len(got) != 0 || got == nil {
		t.Fatalf("空入参 want 非 nil 空 map, got %v", got)
	}
}

// TestResult_ListLatestScoredByStaffNames_EmptyMainType scored 但 main_type 空串
// 的行同样过滤（main_type <> '' 条件独立生效，03 §1.8）。
func TestResult_ListLatestScoredByStaffNames_EmptyMainType(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	ctx := context.Background()
	oldAt := time.Unix(scoreBaseUnix, 0).UTC()
	newAt := oldAt.Add(7 * 86400 * time.Second)

	oldTask := seedEnneagramTask(t, db, "张敏", oldAt)
	newTask := seedEnneagramTask(t, db, "张敏", newAt)
	oldRow := makeResultRow(oldTask.ID, "3", domain.GradingStatusScored, oldAt)
	newRow := makeResultRow(newTask.ID, "", domain.GradingStatusScored, newAt) // scored 但空主型
	if err := db.Create(&oldRow).Error; err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := db.Create(&newRow).Error; err != nil {
		t.Fatalf("seed new: %v", err)
	}

	got, err := repo.ListLatestScoredByStaffNames(ctx, []string{"张敏"})
	if err != nil {
		t.Fatalf("ListLatestScoredByStaffNames: %v", err)
	}
	row, ok := got["张敏"]
	if !ok || row.MainType != "3" {
		t.Fatalf("want 空主型行被过滤取旧行 main=3, got %+v ok=%v", row, ok)
	}
}

// TestProfileListQueryErrorClosedConnection 连接关闭后本批新增六方法错误透传。
func TestProfileListQueryErrorClosedConnection(t *testing.T) {
	aggDB := newAggregateTestDB(t)
	aggRepo := repository.NewAggregateScoreRepository(aggDB)
	actDB := newActivityTestDB(t)
	actRepo := repository.NewActivityStatRepository(actDB)
	resDB := newTestResultDB(t)
	resRepo := repository.NewAssessmentTestResultRepository(resDB)
	for _, db := range []*gorm.DB{aggDB, actDB, resDB} {
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("取底层连接: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("关闭连接: %v", err)
		}
	}
	ctx := context.Background()
	if _, err := aggRepo.ListByToken(ctx, "张三"); err == nil {
		t.Fatal("连接关闭后 AggregateScore.ListByToken want error")
	}
	if _, err := aggRepo.ListLatestModuleRowsByTokens(ctx, []string{"张三"}); err == nil {
		t.Fatal("连接关闭后 AggregateScore.ListLatestModuleRowsByTokens want error")
	}
	if _, err := actRepo.ListByToken(ctx, "张三"); err == nil {
		t.Fatal("连接关闭后 ActivityStat.ListByToken want error")
	}
	if _, err := actRepo.ListLatestByTokens(ctx, []string{"张三"}); err == nil {
		t.Fatal("连接关闭后 ActivityStat.ListLatestByTokens want error")
	}
	if _, err := resRepo.ListLatestScoredByStaffNames(ctx, []string{"张三"}); err == nil {
		t.Fatal("连接关闭后 Result.ListLatestScoredByStaffNames want error")
	}
}
