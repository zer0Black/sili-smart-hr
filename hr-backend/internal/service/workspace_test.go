// workspace_test 对工作台域业务层做黑盒单元测试（specs P2_WRK_001 §4.1/§5.1，03 W1）。
//
// fake 仓储 + 既有 fakeUserapiClient/fakeSecretRepo 驱动，不依赖真实 DB / 外部 HTTP。覆盖：
//   - 态势卡批次定位（区间终态优先/无终态触发最新/三表空回退/批次表空空态，03 §1.4）
//   - 演进区综合分序列（4 期窗口、断点、环比取整差、全窗口无行全 nil，03 §1.8）
//   - 活跃率 pp 环比一位小数与未使用计数差（03 §1.6）
//   - 关注人群短板前 5 升序 + 未使用前 5 天数降序、两类互斥（specs §4.1.4 规则3）
//   - 画像速览精简投影（雷达/短板清单/九型恒 9 项/研判透传，03 §1.7）
//   - 降级矩阵（名单/ListPeriods/suggestions/dims 失败区块降级整体不失败，specs §5.1.4 规则2）
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
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeWorkspaceQueries 是 repository.WorkspaceQueryRepository 的测试假实现（逐方法独立错误）。
type fakeWorkspaceQueries struct {
	latestBatch *domain.AssessmentBatch
	bounds      []domain.AssessmentBatch // ListByPeriodBounds 直通（忽略双界）
	boundsIn    [2]int64                 // ListByPeriodBounds 入参探针
	alertExists bool
	alertIDsIn  []int64 // ExistsAlertByBatchIDs 入参探针
	expired     int64
	allActivity []domain.ActivityStat
	scored      map[string]domain.AssessmentTestResult

	latestErr, boundsErr, alertErr, expiredErr, activityErr, scoredErr error
}

func (f *fakeWorkspaceQueries) ListLatestBatch(context.Context) (*domain.AssessmentBatch, error) {
	return f.latestBatch, f.latestErr
}
func (f *fakeWorkspaceQueries) ListByPeriodBounds(_ context.Context, start, end int64) ([]domain.AssessmentBatch, error) {
	f.boundsIn = [2]int64{start, end}
	return f.bounds, f.boundsErr
}
func (f *fakeWorkspaceQueries) ExistsAlertByBatchIDs(_ context.Context, ids []int64) (bool, error) {
	f.alertIDsIn = ids
	return f.alertExists, f.alertErr
}
func (f *fakeWorkspaceQueries) CountExpiredTasks(context.Context) (int64, error) {
	return f.expired, f.expiredErr
}
func (f *fakeWorkspaceQueries) ListAllActivity(context.Context) ([]domain.ActivityStat, error) {
	return f.allActivity, f.activityErr
}
func (f *fakeWorkspaceQueries) ListAllLatestScored(context.Context) (map[string]domain.AssessmentTestResult, error) {
	return f.scored, f.scoredErr
}

var _ repository.WorkspaceQueryRepository = (*fakeWorkspaceQueries)(nil)

// fakeWorkspaceDashboardQueries 是 DashboardQueryRepository 的工作台测试假实现（独立命名防互扰）。
type fakeWorkspaceDashboardQueries struct {
	periods     []repository.PeriodBound
	periodsErr  error
	activity    map[[2]int64][]domain.ActivityStat
	activityErr error // ListActivityByPeriod 统一错误
	dimByPer    map[[2]int64][]domain.DimensionScore
	dimWindow   []domain.DimensionScore
	windowErr   error // ListDimScoresByPeriods
	aggWindow   []domain.AggregateScore
	aggErr      error // ListModuleAggScoresByPeriods
	finished    map[[2]int64]*time.Time
	finishedErr error
}

func (f *fakeWorkspaceDashboardQueries) ListPeriods(context.Context) ([]repository.PeriodBound, error) {
	return f.periods, f.periodsErr
}
func (f *fakeWorkspaceDashboardQueries) ListActivityByPeriod(_ context.Context, start, end int64) ([]domain.ActivityStat, error) {
	if f.activity == nil {
		return nil, f.activityErr
	}
	return f.activity[[2]int64{start, end}], f.activityErr
}
func (f *fakeWorkspaceDashboardQueries) ListDimScoresByPeriod(_ context.Context, start, end int64) ([]domain.DimensionScore, error) {
	if f.dimByPer == nil {
		return nil, nil
	}
	return f.dimByPer[[2]int64{start, end}], nil
}
func (f *fakeWorkspaceDashboardQueries) ListDimScoresByPeriods(context.Context, []repository.PeriodBound) ([]domain.DimensionScore, error) {
	return f.dimWindow, f.windowErr
}
func (f *fakeWorkspaceDashboardQueries) ListModuleAggScoresByPeriods(context.Context, []repository.PeriodBound) ([]domain.AggregateScore, error) {
	return f.aggWindow, f.aggErr
}
func (f *fakeWorkspaceDashboardQueries) FindLatestFinishedAt(_ context.Context, start, end int64) (*time.Time, error) {
	if f.finished == nil {
		return nil, f.finishedErr
	}
	return f.finished[[2]int64{start, end}], f.finishedErr
}
func (f *fakeWorkspaceDashboardQueries) FindEarliestPendingSuggestBatch(context.Context, []domain.TeamTrainingSuggestion) (*domain.AssessmentBatch, error) {
	return nil, nil
}

var _ repository.DashboardQueryRepository = (*fakeWorkspaceDashboardQueries)(nil)

// wrkWeek 第 n 周测试周期（n=0 本期 2026-09-28~10-05，负数前推），Local 零点 .UTC() 落库同构。
func wrkWeek(n int) repository.PeriodBound {
	return repository.PeriodBound{
		StartAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
		EndAt:   time.Date(2026, 10, 5, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
	}
}

func wrkKey(b repository.PeriodBound) [2]int64 { return [2]int64{b.StartAt.Unix(), b.EndAt.Unix()} }

// wrkStaffNames 构造 n 人名单（员001..员NNN）。
func wrkStaffNames(n int) []userapi.Staff {
	staffs := make([]userapi.Staff, 0, n)
	for i := 1; i <= n; i++ {
		staffs = append(staffs, userapi.Staff{StaffID: fmt.Sprintf("%d", i), StaffName: fmt.Sprintf("员%03d", i)})
	}
	return staffs
}

// newWorkspaceSvc 用 fake 仓储 + 已配置密钥构造被测 service，now 锚定 2026-10-07 10:00（周三）。
func newWorkspaceSvc(q repository.WorkspaceQueryRepository, dq repository.DashboardQueryRepository,
	sug repository.TeamTrainingSuggestionRepository, dims repository.DimensionRepository,
	cfg repository.AssessmentConfigRepository, ua *fakeUserapiClient) service.WorkspaceService {
	encKey := crypto.DeriveKey("test-workspace")
	cipher, err := crypto.Encrypt(encKey, "workspace-secret")
	if err != nil {
		panic(err)
	}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.Local)
	return service.NewWorkspaceService(q, dq, sug, dims, cfg, ua, secretRepo, encKey, func() time.Time { return now })
}

// wkBaseDeps 常规依赖组：weekly/23:00 配置 + 空 dims/suggestion/userapi。
func wkBaseDeps() (repository.AssessmentConfigRepository, *fakeDashboardDimRepo, *fakeDashboardSuggestions, *fakeUserapiClient) {
	cfg := &fakeAssessmentConfigRepo{cfg: &domain.AssessmentConfig{ID: 1, Period: "weekly", TriggerTime: "23:00"}}
	return cfg, &fakeDashboardDimRepo{}, &fakeDashboardSuggestions{}, &fakeUserapiClient{}
}

// ===== 态势卡 =====

// TestWorkspaceOverview_BatchSituation 验证区间批次定位与各字段（03 §1.4、specs §4.1.2 A）：
// running+success 两批次取终态 success；alert_count 按 ExistsAlertByBatchIDs 0/1；
// next_trigger_at 格式 yyyy-MM-dd HH:mm；data_updated_at 取最新终态。
func TestWorkspaceOverview_BatchSituation(t *testing.T) {
	w0 := wrkWeek(0)
	fin := time.Date(2026, 10, 3, 2, 0, 0, 0, time.UTC)
	q := &fakeWorkspaceQueries{
		latestBatch: &domain.AssessmentBatch{ID: 2, Status: domain.BatchStatusSuccess},
		bounds: []domain.AssessmentBatch{
			{ID: 1, Status: domain.BatchStatusRunning,
				PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt,
				TriggeredAt: time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC)},
			{ID: 2, Status: domain.BatchStatusSuccess,
				PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt,
				TriggeredAt: time.Date(2026, 10, 2, 23, 0, 0, 0, time.UTC), FinishedAt: &fin},
		},
		alertExists: true,
		expired:     3,
	}
	dq := &fakeWorkspaceDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		finished: map[[2]int64]*time.Time{wrkKey(w0): &fin},
	}
	cfg, dims, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(1)
	ua.total = 1
	res, err := newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	b := res.Batch
	if b == nil {
		t.Fatal("batch 恒返回对象")
	}
	if b.Status == nil || *b.Status != "success" {
		t.Fatalf("status want success（终态优先）, got %+v", b.Status)
	}
	if b.NextTriggerAt == nil || *b.NextTriggerAt != "2026-10-11 23:00" {
		t.Fatalf("next_trigger_at want 2026-10-11 23:00, got %+v", b.NextTriggerAt)
	}
	if b.AlertCount == nil || *b.AlertCount != 1 {
		t.Fatalf("alert_count want 1, got %+v", b.AlertCount)
	}
	if len(q.alertIDsIn) != 2 { // 区间批次 ID 集整体判定
		t.Fatalf("ExistsAlertByBatchIDs 应收区间批次 ID 集, got %v", q.alertIDsIn)
	}
	if b.OverdueCount == nil || *b.OverdueCount != 3 {
		t.Fatalf("overdue_count want 3, got %+v", b.OverdueCount)
	}
	if b.DataUpdatedAt == nil || *b.DataUpdatedAt != fin.Local().Format("2006-01-02 15:04") {
		t.Fatalf("data_updated_at want 批次终态, got %+v", b.DataUpdatedAt)
	}
	if q.boundsIn != wrkKey(w0) {
		t.Fatalf("ListByPeriodBounds 应传最新落库区间, got %v", q.boundsIn)
	}
	// 无告警行存在性 false → 0。
	q.alertExists = false
	res, err = newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 无告警: %v", err)
	}
	if res.Batch.AlertCount == nil || *res.Batch.AlertCount != 0 {
		t.Fatalf("alert_count want 0, got %+v", res.Batch.AlertCount)
	}
}

// TestWorkspaceOverview_BatchFallback 验证三表无区间与批次表空两级回退（03 §1.4、specs §4.1.4 规则4）。
func TestWorkspaceOverview_BatchFallback(t *testing.T) {
	cfg, dims, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(1)

	// 三表无区间但批次表有 running 行：status 回退 running、alert_count=0、data_updated_at=nil。
	q := &fakeWorkspaceQueries{latestBatch: &domain.AssessmentBatch{ID: 9, Status: domain.BatchStatusRunning}}
	dq := &fakeWorkspaceDashboardQueries{periods: []repository.PeriodBound{}}
	res, err := newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 回退: %v", err)
	}
	if res.Batch.Status == nil || *res.Batch.Status != "running" {
		t.Fatalf("回退 status want running, got %+v", res.Batch.Status)
	}
	if res.Batch.AlertCount == nil || *res.Batch.AlertCount != 0 {
		t.Fatalf("回退 alert_count want 0, got %+v", res.Batch.AlertCount)
	}
	if res.Batch.DataUpdatedAt != nil {
		t.Fatalf("running 行 data_updated_at want nil, got %+v", res.Batch.DataUpdatedAt)
	}
	if res.CurrentPeriod != nil || res.Trend != nil || res.Profile != nil || res.Attention != nil {
		t.Fatalf("无区间下游区块 want 全 nil, got %+v", res)
	}

	// 回退行有终态：data_updated_at 取其 finished_at。
	fin := time.Date(2026, 10, 1, 1, 30, 0, 0, time.UTC)
	q = &fakeWorkspaceQueries{latestBatch: &domain.AssessmentBatch{ID: 9, Status: domain.BatchStatusFailed, FinishedAt: &fin}}
	res, err = newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 回退终态: %v", err)
	}
	if res.Batch.DataUpdatedAt == nil || *res.Batch.DataUpdatedAt != fin.Local().Format("2006-01-02 15:04") {
		t.Fatalf("回退终态 data_updated_at 异常: %+v", res.Batch.DataUpdatedAt)
	}

	// 批次表空：规则4 空态，status=nil 且四区块全 nil。
	q = &fakeWorkspaceQueries{}
	res, err = newWorkspaceSvc(q, &fakeWorkspaceDashboardQueries{periods: []repository.PeriodBound{}}, sug, dims, cfg, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 空态: %v", err)
	}
	if res.Batch.Status != nil {
		t.Fatalf("空态 status want nil, got %+v", res.Batch.Status)
	}
	if res.Batch.AlertCount == nil || *res.Batch.AlertCount != 0 {
		t.Fatalf("空态 alert_count want 0, got %+v", res.Batch.AlertCount)
	}
	if res.CurrentPeriod != nil || res.Trend != nil || res.Profile != nil || res.Attention != nil {
		t.Fatalf("空态下游区块 want 全 nil, got %+v", res)
	}
}

// ===== 演进区 =====

// wrkDimRow 构造单人单维度 success 行便捷助手。
func wrkDimRow(week repository.PeriodBound, staff string, module, source, code string, score int) domain.DimensionScore {
	return domain.DimensionScore{
		TokenName: staff, Module: module, DimensionCode: code, Score: score,
		Status: domain.ScoreStatusSuccess, Source: source,
		PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
	}
}

// TestWorkspaceOverview_TrendSeries 验证综合分序列口径（03 §1.8）：
// 4 期窗口 AI_USAGE [58,62,65,67]、change=2；AI_MGMT 前两期 nil 断点；
// 某模块全窗口无行时 scores 全 nil 数组且 current_score=nil。
func TestWorkspaceOverview_TrendSeries(t *testing.T) {
	w0, wm1, wm2, wm3 := wrkWeek(0), wrkWeek(-1), wrkWeek(-2), wrkWeek(-3)
	periods := []repository.PeriodBound{w0, wm1, wm2, wm3}
	var rows []domain.DimensionScore
	for i, sc := range []int{58, 62, 65, 67} {
		w := []repository.PeriodBound{wm3, wm2, wm1, w0}[i]
		for n := 1; n <= 3; n++ {
			rows = append(rows, wrkDimRow(w, fmt.Sprintf("员%03d", n), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U1", sc))
		}
	}
	// AI_MGMT 仅后两期有行（59/61）。
	for n := 1; n <= 3; n++ {
		rows = append(rows, wrkDimRow(wm1, fmt.Sprintf("员%03d", n), domain.ModuleAIMgmt, domain.ScoreSourceActiveTest, "M1", 59))
		rows = append(rows, wrkDimRow(w0, fmt.Sprintf("员%03d", n), domain.ModuleAIMgmt, domain.ScoreSourceActiveTest, "M1", 61))
	}
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "U1", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U1"},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true, Name: "子能力M1"},
	}}
	dq := &fakeWorkspaceDashboardQueries{periods: periods, dimWindow: rows,
		dimByPer: map[[2]int64][]domain.DimensionScore{wrkKey(w0): rows}}
	cfg, _, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(3)
	ua.total = 3
	res, err := newWorkspaceSvc(&fakeWorkspaceQueries{}, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	tr := res.Trend
	if tr == nil {
		t.Fatal("trend want 非 nil")
	}
	if len(tr.Periods) != 4 {
		t.Fatalf("periods want 4, got %d", len(tr.Periods))
	}
	// 旧到新：首项最旧区间。
	if tr.Periods[0].PeriodStart != wm3.StartAt.Local().Format("2006-01-02") {
		t.Fatalf("periods[0] want 最旧区间起日, got %s", tr.Periods[0].PeriodStart)
	}
	if tr.Periods[3].PeriodEnd != w0.EndAt.AddDate(0, 0, -1).Local().Format("2006-01-02") {
		t.Fatalf("periods[3] period_end want 含止日, got %s", tr.Periods[3].PeriodEnd)
	}
	if len(tr.Series) != 2 {
		t.Fatalf("series 恒两行, got %d", len(tr.Series))
	}
	usage := tr.Series[0]
	if usage.Module != domain.ModuleAIUsage {
		t.Fatalf("series[0] want AI_USAGE, got %s", usage.Module)
	}
	wantScores := []int{58, 62, 65, 67}
	for i, w := range wantScores {
		if usage.Scores[i] == nil || *usage.Scores[i] != w {
			t.Fatalf("AI_USAGE scores[%d] want %d, got %+v", i, w, usage.Scores[i])
		}
	}
	if usage.CurrentScore == nil || *usage.CurrentScore != 67 {
		t.Fatalf("AI_USAGE current_score want 67, got %+v", usage.CurrentScore)
	}
	if usage.ChangeVsPrev == nil || *usage.ChangeVsPrev != 2 {
		t.Fatalf("AI_USAGE change_vs_prev want 2, got %+v", usage.ChangeVsPrev)
	}
	mgmt := tr.Series[1]
	if mgmt.Module != domain.ModuleAIMgmt {
		t.Fatalf("series[1] want AI_MGMT, got %s", mgmt.Module)
	}
	if mgmt.Scores[0] != nil || mgmt.Scores[1] != nil {
		t.Fatalf("AI_MGMT 前两期 want nil 断点, got %+v", mgmt.Scores[:2])
	}
	if mgmt.Scores[2] == nil || *mgmt.Scores[2] != 59 || mgmt.Scores[3] == nil || *mgmt.Scores[3] != 61 {
		t.Fatalf("AI_MGMT 后两期 want 59/61, got %+v", mgmt.Scores[2:])
	}
	if mgmt.ChangeVsPrev == nil || *mgmt.ChangeVsPrev != 2 {
		t.Fatalf("AI_MGMT change_vs_prev want 2, got %+v", mgmt.ChangeVsPrev)
	}

	// AI_MGMT 全窗口无行：scores 全 nil 数组、current/change nil、series 行仍保留。
	dq2 := &fakeWorkspaceDashboardQueries{periods: periods, dimWindow: rows,
		dimByPer: map[[2]int64][]domain.DimensionScore{wrkKey(w0): rows}}
	dq2.dimByPer[wrkKey(w0)] = func() []domain.DimensionScore {
		out := []domain.DimensionScore{}
		for _, r := range rows {
			if r.Module != domain.ModuleAIMgmt {
				out = append(out, r)
			}
		}
		return out
	}()
	// 窗口行剔除 AI_MGMT。
	filtered := []domain.DimensionScore{}
	for _, r := range rows {
		if r.Module != domain.ModuleAIMgmt {
			filtered = append(filtered, r)
		}
	}
	dq2.dimWindow = filtered
	res, err = newWorkspaceSvc(&fakeWorkspaceQueries{}, dq2, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 无 M 行: %v", err)
	}
	m2 := res.Trend.Series[1]
	if len(m2.Scores) != 4 || m2.Scores[0] != nil || m2.Scores[3] != nil {
		t.Fatalf("全窗口无行 scores want 长度 4 全 nil, got %+v", m2.Scores)
	}
	if m2.CurrentScore != nil || m2.ChangeVsPrev != nil {
		t.Fatalf("全窗口无行 current/change want nil, got %+v %+v", m2.CurrentScore, m2.ChangeVsPrev)
	}
}

// TestWorkspaceOverview_ActivityPP 验证活跃率 pp 环比与未使用计数差（03 §1.6）：
// 名单 120 人，本期 active=50/low=30/unused 行=20 + 无行 20；上期 active=47/unused=43。
func TestWorkspaceOverview_ActivityPP(t *testing.T) {
	w0, wm1 := wrkWeek(0), wrkWeek(-1)
	mk := func(w repository.PeriodBound, from, to int, level string) []domain.ActivityStat {
		rows := []domain.ActivityStat{}
		for i := from; i <= to; i++ {
			rows = append(rows, domain.ActivityStat{TokenName: fmt.Sprintf("员%03d", i),
				ActiveLevel: level, PeriodStartAt: w.StartAt, PeriodEndAt: w.EndAt})
		}
		return rows
	}
	w0Rows := append(append(append([]domain.ActivityStat{},
		mk(w0, 1, 50, domain.ActiveLevelActive)...),
		mk(w0, 51, 80, domain.ActiveLevelLowFreq)...),
		mk(w0, 81, 100, domain.ActiveLevelUnused)...)
	// 上期全员 120 行：47 active + 30 low + 43 unused。
	wm1Rows := append(append(append([]domain.ActivityStat{},
		mk(wm1, 1, 47, domain.ActiveLevelActive)...),
		mk(wm1, 48, 77, domain.ActiveLevelLowFreq)...),
		mk(wm1, 78, 120, domain.ActiveLevelUnused)...)
	dq := &fakeWorkspaceDashboardQueries{
		periods:  []repository.PeriodBound{w0, wm1},
		activity: map[[2]int64][]domain.ActivityStat{wrkKey(w0): w0Rows, wrkKey(wm1): wm1Rows},
	}
	cfg, dims, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(120)
	ua.total = 120
	res, err := newWorkspaceSvc(&fakeWorkspaceQueries{allActivity: append(append([]domain.ActivityStat{}, w0Rows...), wm1Rows...)},
		dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	act := res.Trend.Activity
	if act == nil {
		t.Fatal("activity want 非 nil")
	}
	if act.ActiveRatio != 41.7 {
		t.Fatalf("active_ratio want 41.7, got %v", act.ActiveRatio)
	}
	if act.ActiveChangePP == nil || *act.ActiveChangePP != 2.5 {
		t.Fatalf("active_change_pp want 2.5（41.7-39.2）, got %+v", act.ActiveChangePP)
	}
	if act.UnusedCount != 40 {
		t.Fatalf("unused_count want 40（20 行 + 20 无行）, got %d", act.UnusedCount)
	}
	if act.UnusedChange == nil || *act.UnusedChange != -3 {
		t.Fatalf("unused_change want -3, got %+v", act.UnusedChange)
	}

	// 窗口仅本期（最早区间）：两个环比字段 nil。
	dq1 := &fakeWorkspaceDashboardQueries{periods: []repository.PeriodBound{w0},
		activity: map[[2]int64][]domain.ActivityStat{wrkKey(w0): w0Rows}}
	res, err = newWorkspaceSvc(&fakeWorkspaceQueries{allActivity: w0Rows}, dq1, sug, dims, cfg, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 单期: %v", err)
	}
	act = res.Trend.Activity
	if act.ActiveChangePP != nil || act.UnusedChange != nil {
		t.Fatalf("最早区间环比 want nil, got %+v %+v", act.ActiveChangePP, act.UnusedChange)
	}
}

// ===== 关注人群 =====

// TestWorkspaceOverview_Attention 验证两类人群判定排序与载体字段（specs §4.1.4 规则3、03 §1.5）。
func TestWorkspaceOverview_Attention(t *testing.T) {
	w0, wm1 := wrkWeek(0), wrkWeek(-1)
	name := func(n int) string { return fmt.Sprintf("员%03d", n) }

	// 本期聚合行：6 人 AI_USAGE 取整后 < 60（40/46/50/56/59/59），第 6 人被前 5 截断。
	aggScores := []float64{40.4, 45.5, 50.2, 55.7, 58.9, 59.4}
	aggRows := []domain.AggregateScore{}
	for i, sc := range aggScores {
		v := sc
		aggRows = append(aggRows, domain.AggregateScore{
			TokenName: name(i + 1), Module: domain.ModuleAIUsage, ModuleScore: &v,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt,
			IncludedJSON: `[{"code":"U1","weight":50},{"code":"U2","weight":50}]`,
		})
	}
	// 本期维度行（weak_dims 载体）：员001 U1=30/U2=50（仅 U1）；员005 U1=42/U2=42（并列全选）。
	curDimRows := []domain.DimensionScore{
		wrkDimRow(w0, name(1), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U1", 30),
		wrkDimRow(w0, name(1), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U2", 50),
		wrkDimRow(w0, name(5), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U1", 42),
		wrkDimRow(w0, name(5), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U2", 42),
	}
	// 本期 activity：员007 unused（无聚合行）；员008 无任何统计行。
	w0Act := []domain.ActivityStat{
		{	TokenName: name(1), ActiveLevel: domain.ActiveLevelLowFreq, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		{	TokenName: name(2), ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	}
	for n := 3; n <= 6; n++ {
		w0Act = append(w0Act, domain.ActivityStat{TokenName: name(n),
			ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	}
	w0Act = append(w0Act, domain.ActivityStat{TokenName: name(7),
		ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt})
	// 员007 在 w-1 有一条 active 行（最近非未使用行 → days=7）；员008 全表无行（自最旧区间起点 → days=14）。
	allAct := append(append([]domain.ActivityStat{}, w0Act...),
		[]domain.ActivityStat{{TokenName: name(7), ActiveLevel: domain.ActiveLevelActive,
			PeriodStartAt: wm1.StartAt, PeriodEndAt: wm1.EndAt}}...)

	q := &fakeWorkspaceQueries{allActivity: allAct}
	dq := &fakeWorkspaceDashboardQueries{
		periods:   []repository.PeriodBound{w0, wm1},
		activity:  map[[2]int64][]domain.ActivityStat{wrkKey(w0): w0Act},
		dimByPer:  map[[2]int64][]domain.DimensionScore{wrkKey(w0): curDimRows},
		dimWindow: curDimRows, aggWindow: aggRows,
	}
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "U1", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U1"},
		{Code: "U2", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U2"},
	}}
	cfg, _, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(8)
	ua.total = 8
	res, err := newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	rows := res.Attention
	if rows == nil {
		t.Fatal("attention want 非 nil")
	}
	if len(rows) != 7 {
		t.Fatalf("attention want 7 行（短板 5 + 未使用 2）, got %d", len(rows))
	}
	// 短板在前，按取整最低总分升序：员001..员005（40/46/50/56/59）。
	wantWeak := []string{name(1), name(2), name(3), name(4), name(5)}
	for i, w := range wantWeak {
		r := rows[i]
		if r.Category != "weak" || r.StaffName != w {
			t.Fatalf("rows[%d] want weak/%s, got %s/%s", i, w, r.Category, r.StaffName)
		}
		if r.DaysSinceActive != nil {
			t.Fatalf("weak 行 days_since_active want nil, got %+v", r.DaysSinceActive)
		}
		if r.AIUsageScore == nil || *r.AIUsageScore != []int{40, 46, 50, 56, 59}[i] {
			t.Fatalf("%s ai_usage_score want 取整分, got %+v", w, r.AIUsageScore)
		}
	}
	// 员006 不上榜（前 5 截断）。
	for _, r := range rows {
		if r.StaffName == name(6) {
			t.Fatal("员006 应被前 5 截断")
		}
	}
	// weak_dims：员001 仅 U1（最低 30）；员005 并列 U1/U2。
	if wm := rows[0].WeakModules; len(wm) != 1 || wm[0].Module != domain.ModuleAIUsage || wm[0].Score != 40 ||
		len(wm[0].WeakDims) != 1 || wm[0].WeakDims[0] != "U1" {
		t.Fatalf("员001 weak_modules want AI_USAGE/40/[U1], got %+v", wm)
	}
	if wm := rows[4].WeakModules; len(wm[0].WeakDims) != 2 {
		t.Fatalf("员005 并列短板 want [U1 U2], got %+v", wm[0].WeakDims)
	}
	// 活跃度：员001 low_freq、员002 active。
	if rows[0].ActivityLevel != domain.ActiveLevelLowFreq || rows[1].ActivityLevel != domain.ActiveLevelActive {
		t.Fatalf("activity_level 异常: %s/%s", rows[0].ActivityLevel, rows[1].ActivityLevel)
	}
	// 未使用在后，天数降序：员008（14，无统计行自最旧区间起点）在前、员007（7）在后。
	u1, u2 := rows[5], rows[6]
	if u1.Category != "unused" || u1.StaffName != name(8) {
		t.Fatalf("rows[5] want unused/员008, got %s/%s", u1.Category, u1.StaffName)
	}
	if u1.DaysSinceActive == nil || *u1.DaysSinceActive != 14 {
		t.Fatalf("员008 days_since_active want 14（自最旧区间起点）, got %+v", u1.DaysSinceActive)
	}
	if u2.StaffName != name(7) || u2.DaysSinceActive == nil || *u2.DaysSinceActive != 7 {
		t.Fatalf("员007 want 7 天, got %s %+v", u2.StaffName, u2.DaysSinceActive)
	}
	for _, u := range []service.WorkspaceAttentionRow{u1, u2} {
		if u.AIUsageScore != nil || u.AIMGMTScore != nil {
			t.Fatalf("未使用行总分 want nil, got %+v/%+v", u.AIUsageScore, u.AIMGMTScore)
		}
		if u.WeakModules == nil || len(u.WeakModules) != 0 {
			t.Fatalf("未使用行 weak_modules want 空数组, got %+v", u.WeakModules)
		}
		if u.ActivityLevel != domain.ActiveLevelUnused {
			t.Fatalf("未使用行 activity_level want unused, got %s", u.ActivityLevel)
		}
	}
}

// TestWorkspaceOverview_AttentionEmpty 验证无命中人群时 attention 为空数组（specs §4.1.4 规则3）：
// 全员本期 active 且无低分聚合行。
func TestWorkspaceOverview_AttentionEmpty(t *testing.T) {
	w0 := wrkWeek(0)
	act := []domain.ActivityStat{
		{	TokenName: "员001", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		{	TokenName: "员002", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	}
	q := &fakeWorkspaceQueries{allActivity: act}
	dq := &fakeWorkspaceDashboardQueries{
		periods:  []repository.PeriodBound{w0},
		activity: map[[2]int64][]domain.ActivityStat{wrkKey(w0): act},
	}
	cfg, dims, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(2)
	ua.total = 2
	res, err := newWorkspaceSvc(q, dq, sug, dims, cfg, ua).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if res.Attention == nil || len(res.Attention) != 0 {
		t.Fatalf("无命中人群 attention want 空数组, got %+v", res.Attention)
	}
}

// ===== 画像速览区 =====

// TestWorkspaceOverview_Profile 验证精简投影（03 §1.7）：雷达恒两行、weaknesses 合并清单带 low_ratio、
// 九型恒 9 项主导并列按序号升序、研判 generated 态 summary 透传。
func TestWorkspaceOverview_Profile(t *testing.T) {
	w0 := wrkWeek(0)
	name := func(n int) string { return fmt.Sprintf("员%03d", n) }
	// AI_USAGE 三维：U1 均分 75、U2 均分 55（low 2/3）、U3 均分 65 → 短板 {U2,U3}；AI_MGMT 单维 M1 均分 63.33（low 1/3）。
	curRows := []domain.DimensionScore{}
	for n := 1; n <= 3; n++ {
		curRows = append(curRows, wrkDimRow(w0, name(n), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U1", []int{80, 76, 69}[n-1]))
		curRows = append(curRows, wrkDimRow(w0, name(n), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U2", []int{55, 60, 50}[n-1]))
		curRows = append(curRows, wrkDimRow(w0, name(n), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U3", 65))
		curRows = append(curRows, wrkDimRow(w0, name(n), domain.ModuleAIMgmt, domain.ScoreSourceActiveTest, "M1", []int{70, 62, 58}[n-1]))
	}
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "U1", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U1"},
		{Code: "U2", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U2"},
		{Code: "U3", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U3"},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true, Name: "子能力M1"},
	}}
	// 九型：型 2 与型 3 各 4 人并列最高 → dominant="2"；分母 scored=8。
	scored := map[string]domain.AssessmentTestResult{}
	for n := 1; n <= 4; n++ {
		scored[name(n)] = domain.AssessmentTestResult{MainType: "2"}
		scored[name(n+4)] = domain.AssessmentTestResult{MainType: "3"}
	}
	q := &fakeWorkspaceQueries{scored: scored}
	dq := &fakeWorkspaceDashboardQueries{
		periods:   []repository.PeriodBound{w0},
		dimByPer:  map[[2]int64][]domain.DimensionScore{wrkKey(w0): curRows},
		dimWindow: curRows,
	}
	_, _, sug, ua := wkBaseDeps()
	ua.staffs = wrkStaffNames(12)
	ua.total = 12
	res, err := newWorkspaceSvc(q, dq, sug, dims, &fakeAssessmentConfigRepo{cfg: &domain.AssessmentConfig{ID: 1, Period: "weekly", TriggerTime: "23:00"}}, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	p := res.Profile
	if p == nil {
		t.Fatal("profile want 非 nil")
	}
	if len(p.Modules) != 2 {
		t.Fatalf("modules 恒两行, got %d", len(p.Modules))
	}
	if p.Modules[0].Module != domain.ModuleAIUsage || p.Modules[1].Module != domain.ModuleAIMgmt {
		t.Fatalf("模块顺序 want AI_USAGE/AI_MGMT, got %s/%s", p.Modules[0].Module, p.Modules[1].Module)
	}
	if p.Modules[0].OverallAvg == nil || *p.Modules[0].OverallAvg != 65 {
		t.Fatalf("AI_USAGE overall_avg want 65, got %+v", p.Modules[0].OverallAvg)
	}
	if p.Modules[1].OverallAvg == nil || *p.Modules[1].OverallAvg != 63 {
		t.Fatalf("AI_MGMT overall_avg want 63, got %+v", p.Modules[1].OverallAvg)
	}
	// 工作台维度条目不含 low_ratio 字段（编译期约束），is_weakness 映射正确。
	byCode := map[string]service.WorkspaceDimItemDTO{}
	for _, d := range p.Modules[0].Dimensions {
		byCode[d.DimensionCode] = d
	}
	if byCode["U2"].IsWeakness != true || byCode["U3"].IsWeakness != true || byCode["U1"].IsWeakness != false {
		t.Fatalf("AI_USAGE 短板 want {U2,U3}, got %+v", byCode)
	}
	if byCode["U2"].AvgScore == nil || *byCode["U2"].AvgScore != 55 {
		t.Fatalf("U2 avg_score want 55, got %+v", byCode["U2"].AvgScore)
	}
	// weaknesses：两模块 is_weakness 合并清单（U2/U3/M1），low_ratio 一位小数。
	if len(p.Weaknesses) != 3 {
		t.Fatalf("weaknesses want 3 条, got %+v", p.Weaknesses)
	}
	wk := map[string]service.WorkspaceWeaknessDTO{}
	for _, w := range p.Weaknesses {
		wk[w.DimensionCode] = w
	}
	if wk["U2"].LowRatio != 66.7 || wk["U3"].LowRatio != 0.0 || wk["M1"].LowRatio != 33.3 {
		t.Fatalf("low_ratio want 66.7/0.0/33.3, got %+v", p.Weaknesses)
	}
	if wk["U2"].Module != domain.ModuleAIUsage || wk["M1"].Module != domain.ModuleAIMgmt {
		t.Fatalf("weaknesses module 异常: %+v", p.Weaknesses)
	}
	// 九型：恒 9 项、分布分母 scored、主导并列按序号升序。
	enn := p.Enneagram
	if enn == nil {
		t.Fatal("enneagram want 非 nil")
	}
	if len(enn.Distribution) != 9 || enn.Distribution[0].Type != "1" || enn.Distribution[8].Type != "9" {
		t.Fatalf("distribution 恒 9 项按 1-9 升序, got %+v", enn.Distribution)
	}
	if enn.Distribution[1].Count != 4 || enn.Distribution[1].Ratio != 50.0 {
		t.Fatalf("型 2 want 4/50.0, got %+v", enn.Distribution[1])
	}
	if enn.DominantType != "2" || enn.DominantRatio != 50.0 {
		t.Fatalf("并列主导 want 2/50.0, got %s/%v", enn.DominantType, enn.DominantRatio)
	}
	// 研判 generated 态 summary 透传。
	sug2 := &fakeDashboardSuggestions{latest: &domain.TeamTrainingSuggestion{
		Status: domain.SuggestionStatusGenerated, Summary: "团队整体稳健，任务规划为共性短板",
		PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt,
	}}
	res, err = newWorkspaceSvc(q, dq, sug2, dims, &fakeAssessmentConfigRepo{cfg: &domain.AssessmentConfig{Period: "weekly", TriggerTime: "23:00"}}, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview generated: %v", err)
	}
	if res.Profile.Suggestion.Status != "generated" || res.Profile.Suggestion.Summary != "团队整体稳健，任务规划为共性短板" {
		t.Fatalf("suggestion generated 透传异常: %+v", res.Profile.Suggestion)
	}
	// 九型无判型行 → enneagram nil；建议 generating → 非 generated 空 summary。
	q2 := &fakeWorkspaceQueries{}
	sug3 := &fakeDashboardSuggestions{latest: &domain.TeamTrainingSuggestion{Status: domain.SuggestionStatusGenerating}}
	res, err = newWorkspaceSvc(q2, dq, sug3, dims, &fakeAssessmentConfigRepo{cfg: &domain.AssessmentConfig{Period: "weekly", TriggerTime: "23:00"}}, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview 空九型: %v", err)
	}
	if res.Profile.Enneagram != nil {
		t.Fatalf("无判型行 enneagram want nil, got %+v", res.Profile.Enneagram)
	}
	if res.Profile.Suggestion.Status != "generating" || res.Profile.Suggestion.Summary != "" {
		t.Fatalf("generating 态 want generating/空串, got %+v", res.Profile.Suggestion)
	}
}

// ===== 降级矩阵 =====

// TestWorkspaceOverview_Degradation 验证任一数据源失败对应区块空标记、接口整体不失败（specs §5.1.4 规则2、03 §1.3）。
func TestWorkspaceOverview_Degradation(t *testing.T) {
	w0, wm1 := wrkWeek(0), wrkWeek(-1)
	// 通用窗口数据：AI_USAGE 单维 3 人 70 分（series/profile 可组装）。
	name := func(n int) string { return fmt.Sprintf("员%03d", n) }
	curRows := []domain.DimensionScore{}
	for n := 1; n <= 3; n++ {
		curRows = append(curRows, wrkDimRow(w0, name(n), domain.ModuleAIUsage, domain.ScoreSourceConversation, "U1", 70))
	}
	dims := &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "U1", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度U1"},
	}}
	buildDQ := func() *fakeWorkspaceDashboardQueries {
		return &fakeWorkspaceDashboardQueries{
			periods: []repository.PeriodBound{w0, wm1},
			activity: map[[2]int64][]domain.ActivityStat{wrkKey(w0): {
				{	TokenName: name(1), ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
			}},
			dimByPer:  map[[2]int64][]domain.DimensionScore{wrkKey(w0): curRows},
			dimWindow: curRows,
		}
	}
	cfgRepo := &fakeAssessmentConfigRepo{cfg: &domain.AssessmentConfig{Period: "weekly", TriggerTime: "23:00"}}

	// 场景 1：名单失败 → trend.activity=nil + attention=nil，series/profile 照常，err=nil。
	ua := &fakeUserapiClient{err: errors.New("upstream timeout")}
	res, err := newWorkspaceSvc(&fakeWorkspaceQueries{}, buildDQ(), &fakeDashboardSuggestions{}, dims, cfgRepo, ua).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("名单失败整体应不报错: %v", err)
	}
	if res.Trend == nil || res.Trend.Activity != nil {
		t.Fatalf("名单失败 trend.activity want nil 且 series 照常, got %+v", res.Trend)
	}
	if res.Trend.Series[0].CurrentScore == nil || *res.Trend.Series[0].CurrentScore != 70 {
		t.Fatalf("名单失败 series 照常 want 70, got %+v", res.Trend.Series[0].CurrentScore)
	}
	if res.Attention != nil {
		t.Fatalf("名单失败 attention want nil, got %+v", res.Attention)
	}
	if res.Profile == nil {
		t.Fatal("名单失败 profile 照常")
	}

	// 场景 2：ListPeriods 失败 → 三区块 nil 且 batch 正常返回（status 走回退行）。
	dq := buildDQ()
	dq.periods = nil
	dq.periodsErr = errors.New("union failed")
	q := &fakeWorkspaceQueries{latestBatch: &domain.AssessmentBatch{ID: 5, Status: domain.BatchStatusSuccess}}
	ua2 := &fakeUserapiClient{staffs: wrkStaffNames(3), total: 3}
	res, err = newWorkspaceSvc(q, dq, &fakeDashboardSuggestions{}, dims, cfgRepo, ua2).Overview(context.Background())
	if err != nil {
		t.Fatalf("ListPeriods 失败整体应不报错: %v", err)
	}
	if res.CurrentPeriod != nil || res.Trend != nil || res.Profile != nil || res.Attention != nil {
		t.Fatalf("ListPeriods 失败三区块 want 全 nil, got %+v", res)
	}
	if res.Batch.Status == nil || *res.Batch.Status != "success" {
		t.Fatalf("batch 应走回退行 status, got %+v", res.Batch.Status)
	}

	// 场景 3：suggestions 失败 → suggestion 降级 none/空串，其余照常。
	sugErr := &fakeDashboardSuggestions{err: errors.New("suggestion query failed")}
	res, err = newWorkspaceSvc(&fakeWorkspaceQueries{}, buildDQ(), sugErr, dims, cfgRepo, ua2).Overview(context.Background())
	if err != nil {
		t.Fatalf("suggestions 失败整体应不报错: %v", err)
	}
	if res.Profile.Suggestion.Status != "none" || res.Profile.Suggestion.Summary != "" {
		t.Fatalf("suggestions 失败 want none/空串, got %+v", res.Profile.Suggestion)
	}

	// 场景 4：维度配置失败 → trend/profile/attention 全 nil（03 §1.3 dimensions 行）。
	dimsErr := &fakeDashboardDimRepo{err: errors.New("dims failed")}
	res, err = newWorkspaceSvc(&fakeWorkspaceQueries{}, buildDQ(), &fakeDashboardSuggestions{}, dimsErr, cfgRepo, ua2).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("dims 失败整体应不报错: %v", err)
	}
	if res.Trend != nil || res.Profile != nil || res.Attention != nil {
		t.Fatalf("dims 失败三区块 want 全 nil, got %+v", res)
	}
	if res.CurrentPeriod == nil {
		t.Fatal("dims 失败 current_period 照常")
	}

	// 场景 5：配置读取失败 → 仅 next_trigger_at nil，态势卡其余照常。
	cfgErr := &fakeAssessmentConfigRepo{cfgErr: errors.New("config failed")}
	res, err = newWorkspaceSvc(&fakeWorkspaceQueries{}, buildDQ(), &fakeDashboardSuggestions{}, dims, cfgErr, ua2).
		Overview(context.Background())
	if err != nil {
		t.Fatalf("config 失败整体应不报错: %v", err)
	}
	if res.Batch.NextTriggerAt != nil {
		t.Fatalf("config 失败 next_trigger_at want nil, got %+v", res.Batch.NextTriggerAt)
	}
	if res.Batch.OverdueCount == nil {
		t.Fatal("config 失败 overdue_count 照常")
	}
}
