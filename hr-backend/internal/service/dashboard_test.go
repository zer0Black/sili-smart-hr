// dashboard_test 对团队看板域业务层做黑盒单元测试（specs P2_TMD_001 §4.1/§4.2/§5.2）。
//
// fake 仓储 + 既有 fakeUserapiClient/fakeSecretRepo 驱动，不依赖真实 DB / 外部 HTTP。覆盖：
//   - 空态跳过 userapi（03 §1.6）、参数 1400 / 区间未落库 2101、上游失败 1305（specs §5.2.4 规则2）
//   - 三态计数无行计未使用、占比一位小数、环比取上一落库区间差（specs §4.1.4 规则2）
//   - 均分剔除 insufficient/failed、< 3 人置空、参考线未取整均值取整（specs §4.1.4 规则3）
//   - 共性短板最低 2 维并列至多 3 项（specs §4.1.4 规则4）
//   - 九型恒 9 项、并列按型别序号升序（specs §4.1.4 规则5）
//   - 建议四态映射、非 generated 内容空值（specs §4.1.4 规则6）
//   - 趋势窗口近 8 期旧到新、置空断线、综合分取整分差（specs §4.2.4 规则1/2/3）
package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeDashboardQueries 是 repository.DashboardQueryRepository 的测试假实现。
type fakeDashboardQueries struct {
	periods   []repository.PeriodBound
	activity  map[[2]int64][]domain.ActivityStat // key: (startUnix, endUnix)
	dimByPer  map[[2]int64][]domain.DimensionScore
	dimWindow []domain.DimensionScore // ListDimScoresByPeriods 直通
	finished  map[[2]int64]*time.Time // FindLatestFinishedAt
	boundsIn  [][2]int64              // ListDimScoresByPeriods 入参探针

	periodsCalled bool
	err           error
}

func (f *fakeDashboardQueries) ListPeriods(context.Context) ([]repository.PeriodBound, error) {
	f.periodsCalled = true
	return f.periods, f.err
}

func (f *fakeDashboardQueries) ListActivityByPeriod(_ context.Context, start, end int64) ([]domain.ActivityStat, error) {
	if f.activity == nil {
		return nil, f.err
	}
	return f.activity[[2]int64{start, end}], f.err
}

func (f *fakeDashboardQueries) ListDimScoresByPeriod(_ context.Context, start, end int64) ([]domain.DimensionScore, error) {
	if f.dimByPer == nil {
		return nil, f.err
	}
	return f.dimByPer[[2]int64{start, end}], f.err
}

func (f *fakeDashboardQueries) ListDimScoresByPeriods(_ context.Context, bounds []repository.PeriodBound) ([]domain.DimensionScore, error) {
	for _, b := range bounds {
		f.boundsIn = append(f.boundsIn, [2]int64{b.StartAt.Unix(), b.EndAt.Unix()})
	}
	return f.dimWindow, f.err
}

func (f *fakeDashboardQueries) ListModuleAggScoresByPeriods(context.Context, []repository.PeriodBound) ([]domain.AggregateScore, error) {
	return nil, nil
}

func (f *fakeDashboardQueries) FindLatestFinishedAt(_ context.Context, start, end int64) (*time.Time, error) {
	if f.finished == nil {
		return nil, f.err
	}
	return f.finished[[2]int64{start, end}], f.err
}

func (f *fakeDashboardQueries) FindEarliestPendingSuggestBatch(context.Context, []domain.TeamTrainingSuggestion) (*domain.AssessmentBatch, error) {
	return nil, nil
}

var _ repository.DashboardQueryRepository = (*fakeDashboardQueries)(nil)

// fakeDashboardSuggestions 是 repository.TeamTrainingSuggestionRepository 的测试假实现。
type fakeDashboardSuggestions struct {
	latest *domain.TeamTrainingSuggestion
	called bool
	err    error
}

func (f *fakeDashboardSuggestions) EnsureGenerating(context.Context, domain.TeamTrainingSuggestion) (bool, error) {
	return false, nil
}

func (f *fakeDashboardSuggestions) FindLatest(context.Context) (*domain.TeamTrainingSuggestion, error) {
	f.called = true
	return f.latest, f.err
}

func (f *fakeDashboardSuggestions) FindAll(context.Context) ([]domain.TeamTrainingSuggestion, error) {
	return nil, f.err
}

func (f *fakeDashboardSuggestions) GetByPeriod(context.Context, int64, int64) (*domain.TeamTrainingSuggestion, error) {
	return nil, nil
}

func (f *fakeDashboardSuggestions) MarkGenerated(context.Context, int64, string, string, string, string, time.Time) error {
	return nil
}

func (f *fakeDashboardSuggestions) MarkFailed(context.Context, int64, string) error {
	return nil
}

var _ repository.TeamTrainingSuggestionRepository = (*fakeDashboardSuggestions)(nil)

// fakeDashboardDimRepo 复用 fakeProfileDimensionRepo 形态，独立命名防与 profile 测试互扰。
type fakeDashboardDimRepo struct {
	all []domain.Dimension
	err error
}

func (f *fakeDashboardDimRepo) ListAll(context.Context) ([]domain.Dimension, error) {
	return f.all, f.err
}
func (f *fakeDashboardDimRepo) FindByID(context.Context, int64) (*domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) FindByCodeExcludingDeleted(context.Context, string) (*domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) Create(context.Context, *domain.Dimension) error { return nil }
func (f *fakeDashboardDimRepo) UpdateWithVersion(context.Context, int64, int, map[string]any) (int64, error) {
	return 0, nil
}
func (f *fakeDashboardDimRepo) SoftDeleteWithVersion(context.Context, int64, int) (int64, error) {
	return 0, nil
}
func (f *fakeDashboardDimRepo) GetActivitySetting(context.Context) (*domain.DimensionSetting, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) UpdateActivitySetting(context.Context, int, int) error { return nil }
func (f *fakeDashboardDimRepo) ListEnabledFullByDataSource(context.Context, string) ([]domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) ListFullByCodesUnscoped(context.Context, []string) ([]domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) CountEnabledByGroupCode(context.Context, string) (map[string]int, error) {
	return nil, nil
}
func (f *fakeDashboardDimRepo) ListNamesByIDsUnscoped(context.Context, []int64) (map[int64]string, error) {
	return nil, nil
}

var _ repository.DimensionRepository = (*fakeDashboardDimRepo)(nil)

// fakeDashboardResults 是 repository.AssessmentTestResultRepository 的假实现（九型判型集合）。
type fakeDashboardResults struct {
	byStaff map[string]domain.AssessmentTestResult
	called  bool
	err     error
}

func (f *fakeDashboardResults) UpsertByTaskID(context.Context, *domain.AssessmentTestResult) error {
	return nil
}
func (f *fakeDashboardResults) DegradeTask(context.Context, int64, bool) error { return nil }
func (f *fakeDashboardResults) ListLatestScoredByStaffNames(_ context.Context, _ []string) (map[string]domain.AssessmentTestResult, error) {
	f.called = true
	return f.byStaff, f.err
}

var _ repository.AssessmentTestResultRepository = (*fakeDashboardResults)(nil)

// dashWeek 第 n 周测试周期（n=0 本期，负数往前推），与落库行同构（Local 零点 .UTC()）。
func dashWeek(n int) repository.PeriodBound {
	return repository.PeriodBound{
		StartAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
		EndAt:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
	}
}

// weekKey PeriodBound → (startUnix, endUnix) 复合键。
func weekKey(b repository.PeriodBound) [2]int64 { return [2]int64{b.StartAt.Unix(), b.EndAt.Unix()} }

// weekDates 第 n 周的展示口径日期对（period_start, period_end，含止日）。
func weekDates(n int) (string, string) {
	b := dashWeek(n)
	return b.StartAt.In(time.Local).Format("2006-01-02"), b.EndAt.AddDate(0, 0, -1).In(time.Local).Format("2006-01-02")
}

// dashStaffNames 构造 n 人名单（员001..员NNN）。
func dashStaffNames(n int) []userapi.Staff {
	staffs := make([]userapi.Staff, 0, n)
	for i := 1; i <= n; i++ {
		staffs = append(staffs, userapi.Staff{StaffID: fmt.Sprintf("%d", i), StaffName: fmt.Sprintf("员%03d", i)})
	}
	return staffs
}

// newDashboardSvc 用 fake 仓储 + 已配置密钥构造被测 service。
func newDashboardSvc(q *fakeDashboardQueries, sug *fakeDashboardSuggestions, dims *fakeDashboardDimRepo,
	rs *fakeDashboardResults, ua *fakeUserapiClient) service.DashboardService {
	encKey := crypto.DeriveKey("test-dashboard")
	cipher, err := crypto.Encrypt(encKey, "dashboard-secret")
	if err != nil {
		panic(err)
	}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	return service.NewDashboardService(q, sug, dims, rs, ua, secretRepo, encKey)
}

// ===== A1 Overview =====

// TestOverviewEmptyState 验证三表无区间时空态结构且跳过 userapi（03 §1.6、specs §4.1.5）。
func TestOverviewEmptyState(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{}}
	ua := &fakeUserapiClient{}
	sug := &fakeDashboardSuggestions{}
	svc := newDashboardSvc(q, sug, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua)

	res, err := svc.Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("空态应为合法返回, got err %v", err)
	}
	if res.Periods == nil || len(res.Periods) != 0 {
		t.Fatalf("periods want 非 nil 空数组, got %+v", res.Periods)
	}
	if res.SelectedPeriod != nil || res.DataUpdatedAt != nil {
		t.Fatalf("selected/data_updated_at want nil, got %+v %+v", res.SelectedPeriod, res.DataUpdatedAt)
	}
	if res.StaffTotal != 0 {
		t.Fatalf("staff_total want 0, got %d", res.StaffTotal)
	}
	if res.Activity != nil {
		t.Fatalf("activity want nil, got %+v", res.Activity)
	}
	if res.Modules == nil || len(res.Modules) != 0 {
		t.Fatalf("modules want 非 nil 空数组, got %+v", res.Modules)
	}
	if res.Enneagram != nil {
		t.Fatalf("enneagram want nil, got %+v", res.Enneagram)
	}
	if res.Suggestion.Status != "none" {
		t.Fatalf("suggestion.status want none, got %s", res.Suggestion.Status)
	}
	if ua.called || sug.called {
		t.Fatalf("空态应跳过 userapi 与建议查询: ua=%v sug=%v", ua.called, sug.called)
	}
}

// TestOverviewParamBad 验证只传一端或日期非法返 1400（03 A1 查询参数）。
func TestOverviewParamBad(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{dashWeek(0), dashWeek(-1)}}
	ua := &fakeUserapiClient{}
	svc := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua)
	for _, c := range [][2]string{
		{"2026-09-22", ""},
		{"", "2026-09-28"},
		{"2026/09/22", "2026-09-28"},
		{"2026-09-22", "09-28-2026"},
	} {
		_, err := svc.Overview(context.Background(), c[0], c[1])
		wantCode(t, err, errcode.BadRequest)
	}
	if ua.called {
		t.Fatal("参数校验失败不应触达上游")
	}
}

// TestOverviewPeriodInvalid 验证传入未落库区间返 2101。
func TestOverviewPeriodInvalid(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{dashWeek(0), dashWeek(-1)}}
	svc := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, &fakeUserapiClient{})
	_, err := svc.Overview(context.Background(), "2026-09-01", "2026-09-07")
	wantCode(t, err, errcode.DashboardPeriodInvalid)
}

// TestOverviewActivityMom 验证三态计数、占比一位小数与环比取上一落库区间差（specs §4.1.4 规则2）：
// 名单 120 人，W1 active=50/low=30/unused=30、W2 active=45 → active.ratio=41.7、mom.active_change=5。
func TestOverviewActivityMom(t *testing.T) {
	w0, w1 := dashWeek(0), dashWeek(-1)
	w0Rows := make([]domain.ActivityStat, 0, 110)
	for i := 1; i <= 50; i++ {
		w0Rows = append(w0Rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	for i := 51; i <= 80; i++ {
		w0Rows = append(w0Rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelLowFreq, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	for i := 81; i <= 110; i++ {
		w0Rows = append(w0Rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	w1Rows := make([]domain.ActivityStat, 0, 45)
	for i := 1; i <= 45; i++ {
		w1Rows = append(w1Rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w1.StartAt, PeriodEndAt: w1.EndAt})
	}
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0, w1},
		activity: map[[2]int64][]domain.ActivityStat{weekKey(w0): w0Rows, weekKey(w1): w1Rows},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(120), total: 120}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.StaffTotal != 120 {
		t.Fatalf("staff_total want 120, got %d", res.StaffTotal)
	}
	act := res.Activity
	if act == nil {
		t.Fatal("activity want 非 nil")
	}
	if act.Active.Count != 50 || act.Active.Ratio != 41.7 {
		t.Fatalf("active want 50/41.7, got %+v", act.Active)
	}
	if act.LowFreq.Count != 30 || act.LowFreq.Ratio != 25.0 {
		t.Fatalf("low_freq want 30/25.0, got %+v", act.LowFreq)
	}
	if act.Unused.Count != 40 || act.Unused.Ratio != 33.3 {
		t.Fatalf("unused want 40/33.3（30 判未使用 + 10 无行）, got %+v", act.Unused)
	}
	if act.Mom == nil || act.Mom.ActiveChange != 5 {
		t.Fatalf("mom.active_change want 5（50-45）, got %+v", act.Mom)
	}

	// 所选为最早区间 → mom=nil。
	w1Start, w1End := weekDates(-1)
	res, err = newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), w1Start, w1End)
	if err != nil {
		t.Fatalf("Overview w-1: %v", err)
	}
	if res.SelectedPeriod == nil || res.SelectedPeriod.PeriodStart != w1Start {
		t.Fatalf("selected want w-1, got %+v", res.SelectedPeriod)
	}
	if res.Activity == nil || res.Activity.Mom != nil {
		t.Fatalf("最早区间 mom want nil, got %+v", res.Activity.Mom)
	}
}

// TestOverviewUnusedIncludesNoRow 验证无统计行人员并入未使用（specs §4.1.4 规则2）：
// 名单 120 人仅 100 人有统计行（其中 20 行判未使用）→ unused=20+20=40、ratio=33.3。
func TestOverviewUnusedIncludesNoRow(t *testing.T) {
	w0 := dashWeek(0)
	rows := make([]domain.ActivityStat, 0, 100)
	for i := 1; i <= 80; i++ {
		rows = append(rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	for i := 81; i <= 100; i++ {
		rows = append(rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
			ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		activity: map[[2]int64][]domain.ActivityStat{weekKey(w0): rows},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(120), total: 120}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.Activity.Unused.Count != 40 {
		t.Fatalf("unused.count want 40, got %d", res.Activity.Unused.Count)
	}
	if res.Activity.Unused.Ratio != 33.3 {
		t.Fatalf("unused.ratio want 33.3, got %v", res.Activity.Unused.Ratio)
	}
}

// dashDimRows 构造单人单维度 success 行便捷助手。
func dashDimRows(week repository.PeriodBound, module, source, code string, scores []int) []domain.DimensionScore {
	rows := make([]domain.DimensionScore, 0, len(scores))
	for i, sc := range scores {
		rows = append(rows, domain.DimensionScore{
			TokenName: fmt.Sprintf("员%03d", i+1), Module: module, DimensionCode: code, Score: sc,
			Status: domain.ScoreStatusSuccess, Source: source,
			PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
		})
	}
	return rows
}

// TestOverviewWeaknessTieCap 验证共性短板并列截断与参考线口径（specs §4.1.4 规则3/规则4）：
// 3 维并列最低均分 → 恰 3 项 is_weakness=true；任一维度置空时 overall_avg=nil。
func TestOverviewWeaknessTieCap(t *testing.T) {
	w0 := dashWeek(0)
	rows := []domain.DimensionScore{}
	// A/B/C 三维并列均分 60（各 4 人），D 维均分 80：短板应恰 A/B/C 三项 true。
	for _, code := range []string{"A", "B", "C"} {
		rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, code, []int{60, 60, 60, 60})...)
	}
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "D", []int{80, 80, 80, 80})...)
	// E 维仅 2 人 → 置空（< 3 人），AI_MGMT 侧 M1 同样置空。
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "E", []int{90, 90})...)
	rows = append(rows, dashDimRows(w0, domain.ModuleAIMgmt, domain.ScoreSourceActiveTest, "M1", []int{70, 70})...)
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度A"},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度B"},
		{Code: "C", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度C"},
		{Code: "D", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度D"},
		{Code: "E", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度E"},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true, Name: "子能力M1"},
	}}
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		dimByPer: map[[2]int64][]domain.DimensionScore{weekKey(w0): rows},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(4), total: 4}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(res.Modules) != 2 {
		t.Fatalf("modules 恒两行, got %d", len(res.Modules))
	}
	usage := res.Modules[0]
	if usage.Module != domain.ModuleAIUsage {
		t.Fatalf("modules[0] want AI_USAGE, got %s", usage.Module)
	}
	if len(usage.Dimensions) != 5 {
		t.Fatalf("AI_USAGE dimensions want 5, got %d", len(usage.Dimensions))
	}
	weakCount := 0
	for _, d := range usage.Dimensions {
		if d.DimensionCode == "E" {
			if d.AvgScore != nil || d.LowRatio != nil {
				t.Fatalf("E < 3 人应置 nil, got %+v", d)
			}
			continue
		}
		if d.AvgScore == nil || *d.AvgScore != 60 && d.DimensionCode != "D" {
			t.Fatalf("%s avg_score 异常: %+v", d.DimensionCode, d.AvgScore)
		}
		if d.IsWeakness {
			weakCount++
		}
	}
	if weakCount != 3 {
		t.Fatalf("并列 3 维短板 want 恰 3 项 true, got %d", weakCount)
	}
	// E 置空 → overall_avg=nil；D 均分 80。
	if usage.OverallAvg != nil {
		t.Fatalf("任一维度置空 overall_avg want nil, got %v", *usage.OverallAvg)
	}
	mgmt := res.Modules[1]
	if mgmt.OverallAvg != nil || (len(mgmt.Dimensions) != 1 || mgmt.Dimensions[0].AvgScore != nil) {
		t.Fatalf("AI_MGMT M1 < 3 人应置 nil 且 overall nil, got %+v", mgmt)
	}

	// E 维补足 3 人后参考线恢复：A/B/C/D/E 未取整均分 (60+60+60+80+90)/5=70。
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "E", []int{90})...)
	q.dimByPer[weekKey(w0)] = rows
	res, err = newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.Modules[0].OverallAvg == nil || *res.Modules[0].OverallAvg != 70 {
		t.Fatalf("overall_avg want 70, got %+v", res.Modules[0].OverallAvg)
	}
}

// TestOverviewModuleSourceFilter 验证维度行按 source 分模块聚合（specs §4.1.4 规则3）：
// AI_USAGE 维度只取 conversation 行、AI_MGMT 只取 active_test 行。
func TestOverviewModuleSourceFilter(t *testing.T) {
	w0 := dashWeek(0)
	rows := dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{70, 70, 70})
	// 同 code 走 active_test source 的行不应计入 AI_USAGE 聚合。
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceActiveTest, "A", []int{100, 100, 100})...)
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
	}}
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		dimByPer: map[[2]int64][]domain.DimensionScore{weekKey(w0): rows},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(3), total: 3}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	d := res.Modules[0].Dimensions[0]
	if d.AvgScore == nil || *d.AvgScore != 70 {
		t.Fatalf("AI_USAGE 维度应只取 conversation 行均分 70, got %+v", d.AvgScore)
	}
}

// TestOverviewWeaknessExact2 验证无并列时恰取最低 2 维（specs §4.1.4 规则4）。
func TestOverviewWeaknessExact2(t *testing.T) {
	w0 := dashWeek(0)
	var rows []domain.DimensionScore
	for _, tc := range []struct {
		code   string
		scores []int
	}{
		{"A", []int{40, 40, 40}},
		{"B", []int{50, 50, 50}},
		{"C", []int{60, 60, 60}},
		{"D", []int{90, 90, 90}},
	} {
		rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, tc.code, tc.scores)...)
	}
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "C", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "D", ModuleCode: domain.ModuleAIUsage, Enabled: true},
	}}
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		dimByPer: map[[2]int64][]domain.DimensionScore{weekKey(w0): rows},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(3), total: 3}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	byCode := map[string]service.DashboardDimItem{}
	for _, d := range res.Modules[0].Dimensions {
		byCode[d.DimensionCode] = d
	}
	if !byCode["A"].IsWeakness || !byCode["B"].IsWeakness {
		t.Fatalf("最低 2 维 A/B 应为短板, got A=%v B=%v", byCode["A"].IsWeakness, byCode["B"].IsWeakness)
	}
	if byCode["C"].IsWeakness || byCode["D"].IsWeakness {
		t.Fatal("C/D 不应为短板")
	}
	// 低分占比：A 三人全 < 60 → 100.0。
	if byCode["A"].LowRatio == nil || *byCode["A"].LowRatio != 100.0 {
		t.Fatalf("A low_ratio want 100.0, got %+v", byCode["A"].LowRatio)
	}
	if byCode["D"].LowRatio == nil || *byCode["D"].LowRatio != 0.0 {
		t.Fatalf("D low_ratio want 0.0, got %+v", byCode["D"].LowRatio)
	}
}

// TestOverviewEnneagramTie 验证九型恒 9 项、覆盖率与并列按型别序号升序（specs §4.1.4 规则5）：
// 型 3 与型 2 各 4 人并列最高时 dominant_type="2"。
func TestOverviewEnneagramTie(t *testing.T) {
	w0 := dashWeek(0)
	byStaff := map[string]domain.AssessmentTestResult{}
	for i := 1; i <= 4; i++ {
		byStaff[fmt.Sprintf("员%03d", i)] = domain.AssessmentTestResult{MainType: "2"}
	}
	for i := 5; i <= 8; i++ {
		byStaff[fmt.Sprintf("员%03d", i)] = domain.AssessmentTestResult{MainType: "3"}
	}
	for i := 9; i <= 10; i++ {
		byStaff[fmt.Sprintf("员%03d", i)] = domain.AssessmentTestResult{MainType: "5"}
	}
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{w0}}
	rs := &fakeDashboardResults{byStaff: byStaff}
	ua := &fakeUserapiClient{staffs: dashStaffNames(12), total: 12}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, rs, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	enn := res.Enneagram
	if enn == nil {
		t.Fatal("enneagram want 非 nil")
	}
	if enn.ScoredCount != 10 {
		t.Fatalf("scored_count want 10, got %d", enn.ScoredCount)
	}
	if enn.CoverageRatio != 83.3 {
		t.Fatalf("coverage_ratio want 83.3（10/12）, got %v", enn.CoverageRatio)
	}
	if len(enn.Distribution) != 9 {
		t.Fatalf("distribution 恒 9 项, got %d", len(enn.Distribution))
	}
	if enn.Distribution[0].Type != "1" || enn.Distribution[8].Type != "9" {
		t.Fatalf("distribution 应按型别 1-9 升序, got %s..%s", enn.Distribution[0].Type, enn.Distribution[8].Type)
	}
	if enn.Distribution[0].Count != 0 || enn.Distribution[0].Ratio != 0.0 {
		t.Fatalf("无人型别 count/ratio want 0/0.0, got %+v", enn.Distribution[0])
	}
	if enn.DominantType != "2" || enn.DominantRatio != 40.0 {
		t.Fatalf("并列最高应取型别序号小者: want 2/40.0, got %s/%v", enn.DominantType, enn.DominantRatio)
	}
	if enn.SecondaryType != "3" || enn.SecondaryRatio != 40.0 {
		t.Fatalf("次主导 want 3/40.0, got %s/%v", enn.SecondaryType, enn.SecondaryRatio)
	}
}

// TestOverviewEnneagramNil 验证无判型行（名单空或无判型）时 enneagram=nil。
func TestOverviewEnneagramNil(t *testing.T) {
	w0 := dashWeek(0)
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{w0}}
	rs := &fakeDashboardResults{byStaff: map[string]domain.AssessmentTestResult{}}
	ua := &fakeUserapiClient{staffs: dashStaffNames(3), total: 3}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, rs, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.Enneagram != nil {
		t.Fatalf("无判型行 enneagram want nil, got %+v", res.Enneagram)
	}
}

// TestOverviewSuggestionStates 验证建议四态映射（specs §4.1.4 规则6）：
// generating/failed/nil → 对应 status 且 Modules 为空数组；generated 展开内容。
func TestOverviewSuggestionStates(t *testing.T) {
	w0 := dashWeek(0)
	ps, pe := weekDates(0)
	generatedAt := time.Date(2026, 9, 29, 3, 0, 0, 0, time.Local)
	states := []struct {
		row   *domain.TeamTrainingSuggestion
		want  string
		empty bool
	}{
		{&domain.TeamTrainingSuggestion{Status: domain.SuggestionStatusGenerating,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt}, "generating", true},
		{&domain.TeamTrainingSuggestion{Status: domain.SuggestionStatusFailed,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt}, "failed", true},
		{nil, "none", true},
	}
	for _, st := range states {
		q := &fakeDashboardQueries{periods: []repository.PeriodBound{w0}}
		sug := &fakeDashboardSuggestions{latest: st.row}
		res, err := newDashboardSvc(q, sug, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, &fakeUserapiClient{staffs: dashStaffNames(1), total: 1}).
			Overview(context.Background(), "", "")
		if err != nil {
			t.Fatalf("Overview(%s): %v", st.want, err)
		}
		if res.Suggestion.Status != st.want {
			t.Fatalf("status want %s, got %s", st.want, res.Suggestion.Status)
		}
		if res.Suggestion.Modules == nil || len(res.Suggestion.Modules) != 0 {
			t.Fatalf("非 generated Modules want 空 数组, got %+v", res.Suggestion.Modules)
		}
		if res.Suggestion.PeriodStart != nil || res.Suggestion.PeriodEnd != nil || res.Suggestion.GeneratedAt != nil || res.Suggestion.Summary != "" {
			t.Fatalf("非 generated 内容字段 want 空值, got %+v", res.Suggestion)
		}
	}

	// generated 态展开。
	modulesJSON := `[{"module":"AI_USAGE","suggestions":[{"name":"提示词工程专项训练营","description":"针对任务规划维度低分"}]},{"module":"AI_MGMT","suggestions":[{"name":"授权分工案例研讨","description":"研讨"}]}]`
	sug := &fakeDashboardSuggestions{latest: &domain.TeamTrainingSuggestion{
		Status: domain.SuggestionStatusGenerated, ModulesJSON: modulesJSON, Summary: "团队整体稳健",
		PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt, GeneratedAt: &generatedAt,
	}}
	res, err := newDashboardSvc(&fakeDashboardQueries{periods: []repository.PeriodBound{w0}}, sug,
		&fakeDashboardDimRepo{}, &fakeDashboardResults{}, &fakeUserapiClient{staffs: dashStaffNames(1), total: 1}).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview(generated): %v", err)
	}
	sg := res.Suggestion
	if sg.Status != "generated" || sg.Summary != "团队整体稳健" {
		t.Fatalf("generated 态内容异常: %+v", sg)
	}
	if sg.PeriodStart == nil || *sg.PeriodStart != ps || sg.PeriodEnd == nil || *sg.PeriodEnd != pe {
		t.Fatalf("建议区间 want %s~%s, got %+v", ps, pe, sg)
	}
	if sg.GeneratedAt == nil || *sg.GeneratedAt != generatedAt.Local().Format("2006-01-02 15:04") {
		t.Fatalf("generated_at 异常: %+v", sg.GeneratedAt)
	}
	if len(sg.Modules) != 2 || len(sg.Modules[0].Suggestions) != 1 ||
		sg.Modules[0].Suggestions[0].Name != "提示词工程专项训练营" {
		t.Fatalf("modules 展开异常: %+v", sg.Modules)
	}
}

// TestOverviewStaffFails1305 验证 userapi 失败整体 1305（specs §5.2.4 规则2）。
func TestOverviewStaffFails1305(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{dashWeek(0)}}
	ua := &fakeUserapiClient{err: errors.New("upstream timeout")}
	rs := &fakeDashboardResults{}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, rs, ua).
		Overview(context.Background(), "", "")
	wantCode(t, err, errcode.StaffListUnavailable)
	if res != nil {
		t.Fatalf("失败时应无结果, got %+v", res)
	}
	if rs.called {
		t.Fatal("上游失败后不应执行九型查询")
	}
}

// TestOverviewDataUpdatedAt 验证数据更新时间口径（03 §1.8）：有批次取最新终态、
// 无批次取区间 period_end_at（右开界）本地时区 Format。
func TestOverviewDataUpdatedAt(t *testing.T) {
	w0 := dashWeek(0)
	finished := time.Date(2026, 9, 28, 23, 12, 0, 0, time.UTC)
	q := &fakeDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		finished: map[[2]int64]*time.Time{weekKey(w0): &finished},
	}
	ua := &fakeUserapiClient{staffs: dashStaffNames(1), total: 1}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.DataUpdatedAt == nil || *res.DataUpdatedAt != finished.Local().Format("2006-01-02 15:04") {
		t.Fatalf("data_updated_at want 批次终态时间, got %+v", res.DataUpdatedAt)
	}

	// 无批次 → 区间右开界（EndAt 本身）。
	q2 := &fakeDashboardQueries{periods: []repository.PeriodBound{w0}}
	res, err = newDashboardSvc(q2, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview 无批次: %v", err)
	}
	if res.DataUpdatedAt == nil || *res.DataUpdatedAt != w0.EndAt.Local().Format("2006-01-02 15:04") {
		t.Fatalf("data_updated_at want 区间右开界, got %+v", res.DataUpdatedAt)
	}
}

// TestOverviewPeriodsOrdering 验证区间列表新到旧、is_current 仅最新项、selected 默认首项。
func TestOverviewPeriodsOrdering(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{dashWeek(0), dashWeek(-1), dashWeek(-2)}}
	ua := &fakeUserapiClient{staffs: dashStaffNames(1), total: 1}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, &fakeDashboardDimRepo{}, &fakeDashboardResults{}, ua).
		Overview(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(res.Periods) != 3 {
		t.Fatalf("periods want 3, got %d", len(res.Periods))
	}
	w0s, _ := weekDates(0)
	if res.Periods[0].PeriodStart != w0s || !res.Periods[0].IsCurrent {
		t.Fatalf("periods[0] want %s/is_current=true, got %+v", w0s, res.Periods[0])
	}
	if res.Periods[1].IsCurrent || res.Periods[2].IsCurrent {
		t.Fatal("仅最新项 is_current=true")
	}
	if res.SelectedPeriod == nil || res.SelectedPeriod.PeriodStart != w0s {
		t.Fatalf("selected 默认最新区间, got %+v", res.SelectedPeriod)
	}
}

// ===== A2 Trend =====

// trendEnv 趋势测试环境：单 AI_USAGE 维度 A + 单 AI_MGMT 维度 M1。
func trendEnv() *fakeDashboardDimRepo {
	return &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度A"},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true, Name: "子能力M1"},
	}}
}

// TestTrendTypeFallback 验证 type 非法按 use 兜底（specs §5.2.2 步2）。
func TestTrendTypeFallback(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{dashWeek(0)}}
	svc := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{})
	res, err := svc.Trend(context.Background(), "xyz")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if res.Type != "use" || res.Module != domain.ModuleAIUsage {
		t.Fatalf("type 兜底 want use/AI_USAGE, got %s/%s", res.Type, res.Module)
	}

	res, err = svc.Trend(context.Background(), "manage")
	if err != nil {
		t.Fatalf("Trend(manage): %v", err)
	}
	if res.Type != "manage" || res.Module != domain.ModuleAIMgmt {
		t.Fatalf("want manage/AI_MGMT, got %s/%s", res.Type, res.Module)
	}
}

// TestTrendWindowSize8 验证窗口取前 8 期反转为旧到新、最新期 IsCurrent=true（specs §4.2.4 规则1）。
func TestTrendWindowSize8(t *testing.T) {
	periods := make([]repository.PeriodBound, 0, 10)
	for i := 0; i >= -9; i-- {
		periods = append(periods, dashWeek(i))
	}
	q := &fakeDashboardQueries{periods: periods}
	svc := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{})
	res, err := svc.Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if len(res.Periods) != 8 {
		t.Fatalf("periods want 8, got %d", len(res.Periods))
	}
	// 旧到新：首项为 w-7，末项为 w0（IsCurrent）。
	wantFirst, _ := weekDates(-7)
	wantLast, _ := weekDates(0)
	if res.Periods[0].PeriodStart != wantFirst {
		t.Fatalf("periods[0] want %s（最旧窗口项）, got %s", wantFirst, res.Periods[0].PeriodStart)
	}
	if res.Periods[7].PeriodStart != wantLast || !res.Periods[7].IsCurrent {
		t.Fatalf("periods[7] want %s/is_current, got %+v", wantLast, res.Periods[7])
	}
	for i := 0; i < 7; i++ {
		if res.Periods[i].IsCurrent {
			t.Fatal("仅最新期 IsCurrent=true")
		}
	}
	if res.CurrentPeriod == nil || res.CurrentPeriod.PeriodStart != wantLast {
		t.Fatalf("current_period want %s, got %+v", wantLast, res.CurrentPeriod)
	}
}

// TestTrendEmptyWindow 验证窗口空时 periods 空数组、current/composite nil、dimensions 空数组。
func TestTrendEmptyWindow(t *testing.T) {
	q := &fakeDashboardQueries{periods: []repository.PeriodBound{}}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if res.Periods == nil || len(res.Periods) != 0 {
		t.Fatalf("periods want 空 数组, got %+v", res.Periods)
	}
	if res.CurrentPeriod != nil || res.Composite != nil {
		t.Fatalf("current/composite want nil, got %+v %+v", res.CurrentPeriod, res.Composite)
	}
	if res.Dimensions == nil || len(res.Dimensions) != 0 {
		t.Fatalf("dimensions want 空 数组, got %+v", res.Dimensions)
	}
}

// TestTrendNullBreak 验证某维度中间期 < 3 人时该期 history 项 Score=nil 且 Change=nil（specs §4.2.4 规则2）。
func TestTrendNullBreak(t *testing.T) {
	w0, w1, w2 := dashWeek(0), dashWeek(-1), dashWeek(-2)
	rows := dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{80, 80, 80})
	// w-1 仅 2 人 → 置空。
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{70, 70})...)
	rows = append(rows, dashDimRows(w2, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{60, 60, 60})...)
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0, w1, w2},
		dimWindow: rows,
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if len(res.Dimensions) != 1 {
		t.Fatalf("dimensions want 1, got %d", len(res.Dimensions))
	}
	d := res.Dimensions[0]
	if len(d.History) != 3 {
		t.Fatalf("history 与窗口一一对应 want 3, got %d", len(d.History))
	}
	if d.History[0].Score == nil || *d.History[0].Score != 60 {
		t.Fatalf("history[0] want 60, got %+v", d.History[0].Score)
	}
	if d.History[1].Score != nil {
		t.Fatalf("w-1 < 3 人 history[1].Score want nil, got %+v", d.History[1].Score)
	}
	if d.History[2].Score == nil || *d.History[2].Score != 80 {
		t.Fatalf("history[2] want 80, got %+v", d.History[2].Score)
	}
	if d.CurrentScore == nil || *d.CurrentScore != 80 {
		t.Fatalf("current_score want 80, got %+v", d.CurrentScore)
	}
	if d.PrevScore != nil {
		t.Fatalf("上期置空 prev_score want nil, got %+v", d.PrevScore)
	}
	if d.Change != nil {
		t.Fatalf("任一期缺失 change want nil, got %+v", d.Change)
	}
	// 综合分：w0 维度均分 80 → score=80；上期（w-1）置空 → change_vs_prev=nil。
	if res.Composite == nil || res.Composite.Score != 80 || res.Composite.ChangeVsPrev != nil {
		t.Fatalf("composite want 80/nil, got %+v", res.Composite)
	}
	if res.Composite.DimensionCount != 1 {
		t.Fatalf("dimension_count want 1, got %d", res.Composite.DimensionCount)
	}
}

// TestTrendComposite 验证综合分与环比（specs §4.2.4 规则2）：本期 2 维均分 74.6/73.4
// （取整综合分 74）、上期取整 71 → Score=74、ChangeVsPrev=3、DimensionCount=2。
func TestTrendComposite(t *testing.T) {
	w0, w1 := dashWeek(0), dashWeek(-1)
	rows := []domain.DimensionScore{}
	// 本期 A: (80+75+69)/3=74.666…、B: (75+72+73.4→整型不可，用 (75+72+73)/3=73.333)。
	// 目标断言取整综合分 74：(74.666+73.333)/2=74.0。
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{80, 75, 69})...)
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "B", []int{75, 72, 73})...)
	// 上期综合分取整 71：A (70+70+70)/3=70、B (72+72+71)/3=71.667 → (70+71.667)/2=70.83→71。
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{70, 70, 70})...)
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "B", []int{72, 72, 71})...)
	dims := trendEnv()
	dims.all = append(dims.all, domain.Dimension{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度B"})
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0, w1},
		dimWindow: rows,
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if res.Composite == nil {
		t.Fatal("composite want 非 nil")
	}
	if res.Composite.Score != 74 {
		t.Fatalf("composite.score want 74, got %d", res.Composite.Score)
	}
	if res.Composite.ChangeVsPrev == nil || *res.Composite.ChangeVsPrev != 3 {
		t.Fatalf("change_vs_prev want 3（74-71）, got %+v", res.Composite.ChangeVsPrev)
	}
	if res.Composite.DimensionCount != 2 {
		t.Fatalf("dimension_count want 2, got %d", res.Composite.DimensionCount)
	}
	// history/current/prev/change 冗余直出（change 按取整分差 75-71=4）。
	byCode := map[string]service.DashboardTrendDim{}
	for _, d := range res.Dimensions {
		byCode[d.DimensionCode] = d
	}
	a := byCode["A"]
	if a.CurrentScore == nil || *a.CurrentScore != 75 {
		t.Fatalf("A current_score want 75（74.666→75）, got %+v", a.CurrentScore)
	}
	if a.PrevScore == nil || *a.PrevScore != 70 {
		t.Fatalf("A prev_score want 70, got %+v", a.PrevScore)
	}
	if a.Change == nil || *a.Change != 5 {
		t.Fatalf("A change want 5, got %+v", a.Change)
	}
}

// TestTrendWeaknessCurrentOnly 验证短板标识按本期口径、历史期次不回溯（specs §4.2.4 规则3）：
// 本期 A=40/C=60/B=80 → 短板 {A,C}；历史期 A 最高（若回溯 A 不标），仍按本期标 A 不标 B。
func TestTrendWeaknessCurrentOnly(t *testing.T) {
	w0, w1 := dashWeek(0), dashWeek(-1)
	rows := dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{40, 40, 40})
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "B", []int{80, 80, 80})...)
	rows = append(rows, dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "C", []int{60, 60, 60})...)
	// 历史期 A 最高、B 最低（若按历史回溯会标 B 不标 A），本期口径应标 A/C。
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{90, 90, 90})...)
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "B", []int{40, 40, 40})...)
	rows = append(rows, dashDimRows(w1, domain.ModuleAIUsage, domain.ScoreSourceConversation, "C", []int{70, 70, 70})...)
	dims := trendEnv()
	dims.all = append(dims.all,
		domain.Dimension{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		domain.Dimension{Code: "C", ModuleCode: domain.ModuleAIUsage, Enabled: true})
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0, w1},
		dimWindow: rows,
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, dims, &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	byCode := map[string]service.DashboardTrendDim{}
	for _, d := range res.Dimensions {
		byCode[d.DimensionCode] = d
	}
	if !byCode["A"].IsWeakness {
		t.Fatal("本期最低维 A 应标短板（历史期最高不改变本期标识）")
	}
	if byCode["B"].IsWeakness {
		t.Fatal("B 本期非最低 2 维不应标短板（历史期最低不回溯）")
	}
	if !byCode["C"].IsWeakness {
		t.Fatal("本期次低维 C 应标短板")
	}
}

// TestTrendSourceFilter 验证趋势窗口行按模块映射 source：use→conversation、manage→active_test。
func TestTrendSourceFilter(t *testing.T) {
	w0 := dashWeek(0)
	rows := dashDimRows(w0, domain.ModuleAIMgmt, domain.ScoreSourceActiveTest, "M1", []int{70, 70, 70})
	rows = append(rows, dashDimRows(w0, domain.ModuleAIMgmt, domain.ScoreSourceConversation, "M1", []int{10, 10, 10})...)
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0},
		dimWindow: rows,
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "manage")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	d := res.Dimensions[0]
	if d.CurrentScore == nil || *d.CurrentScore != 70 {
		t.Fatalf("manage 应只取 active_test 行均分 70, got %+v", d.CurrentScore)
	}
	if res.Composite == nil || res.Composite.Score != 70 {
		t.Fatalf("composite.score want 70, got %+v", res.Composite)
	}
}

// TestTrendAllNullCompositeNil 验证本期该模块全部维度置空时 composite=nil（03 A2 空态语义）。
func TestTrendAllNullCompositeNil(t *testing.T) {
	w0 := dashWeek(0)
	// 仅 2 人（< 3）→ 维度置空。
	rows := dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{70, 70})
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0},
		dimWindow: rows,
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if res.Composite != nil {
		t.Fatalf("全维度置空 composite want nil, got %+v", res.Composite)
	}
	if len(res.Dimensions) != 1 || res.Dimensions[0].CurrentScore != nil {
		t.Fatalf("维度走势仍返回但置空, got %+v", res.Dimensions)
	}
}

// TestTrendDataUpdatedAt 验证趋势 data_updated_at 取本期区间口径（同 Overview）；
// 模块窗口无落库行时按 03 A2 空态语义为 null。
func TestTrendDataUpdatedAt(t *testing.T) {
	w0 := dashWeek(0)
	finished := time.Date(2026, 9, 28, 23, 12, 0, 0, time.UTC)
	q := &fakeDashboardQueries{
		periods:   []repository.PeriodBound{w0},
		finished:  map[[2]int64]*time.Time{weekKey(w0): &finished},
		dimWindow: dashDimRows(w0, domain.ModuleAIUsage, domain.ScoreSourceConversation, "A", []int{70, 70, 70}),
	}
	res, err := newDashboardSvc(q, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend: %v", err)
	}
	if res.DataUpdatedAt == nil || *res.DataUpdatedAt != finished.Local().Format("2006-01-02 15:04") {
		t.Fatalf("data_updated_at want 批次终态时间, got %+v", res.DataUpdatedAt)
	}

	// 模块无落库行（空态）→ null。
	q2 := &fakeDashboardQueries{periods: []repository.PeriodBound{w0}}
	res, err = newDashboardSvc(q2, &fakeDashboardSuggestions{}, trendEnv(), &fakeDashboardResults{}, &fakeUserapiClient{}).
		Trend(context.Background(), "use")
	if err != nil {
		t.Fatalf("Trend 空态: %v", err)
	}
	if res.DataUpdatedAt != nil {
		t.Fatalf("模块无落库行 data_updated_at want nil, got %+v", res.DataUpdatedAt)
	}
}
