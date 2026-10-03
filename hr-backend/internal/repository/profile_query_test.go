// Package repository_test 对个人画像只读查询扩展做黑盒集成测试。
//
// 覆盖 DimensionScoreRepository 三个画像查询：ListByToken 按人全量各期行
// （specs P2_PRF_001 §5.2.2 步骤4 走势组装）、ListLatestByTokens 每人每模块
// 各自最新聚合周期批量取行（§5.1.2 步骤4，BR1/BR2）、ListByPeriodAllCompany
// 双界精确匹配全公司行含 insufficient/failed 不过滤（§5.2.2 步骤5，BR3，过滤在
// service 层做）。复用 dimension_score_test.go 的测试设施。
package repository_test

import (
	"context"
	"testing"
	"time"

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
