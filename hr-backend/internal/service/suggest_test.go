// suggest_test 对建议生成编排做黑盒单元测试（specs P2_TMD_001 §5.1.2/§5.1.4/§5.1.5）。
//
// fake 仓储 + fake 生成器经 SuggestGeneratorInjector 注入，不依赖真实 DB / LLM。
// 覆盖：tick 拾取最早批次、无命中空转、投递失败行保持 generating、generate
// 竞态残留幂等、批次数据异常落 failed、仅 activity 有行照常生成、素材汇总口径
// 同看板、成功路径终态落库、LLM 失败 err 透传、重试耗尽钩子幂等。
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/suggestgen"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeSuggestSuggestions 是 repository.TeamTrainingSuggestionRepository 的测试假实现。
type fakeSuggestSuggestions struct {
	rows        []domain.TeamTrainingSuggestion // FindAll 全量（含状态）
	byPer       map[[2]int64]*domain.TeamTrainingSuggestion
	ensureRows  []domain.TeamTrainingSuggestion // EnsureGenerating 收到的行探针
	ensureCalls int
	generated   *suggestGeneratedCall
	failed      *suggestFailedCall
	touched     *suggestTouchedCall
	err         error
}

type suggestGeneratedCall struct {
	id            int64
	batchNo       string
	modulesJSON   string
	summary       string
	modelName     string
	promptVersion string
}

type suggestFailedCall struct {
	id     int64
	reason string
}

type suggestTouchedCall struct {
	id int64
	at time.Time
}

func (f *fakeSuggestSuggestions) EnsureGenerating(_ context.Context, row domain.TeamTrainingSuggestion) (bool, error) {
	f.ensureCalls++
	f.ensureRows = append(f.ensureRows, row)
	return true, f.err
}

func (f *fakeSuggestSuggestions) FindLatest(context.Context) (*domain.TeamTrainingSuggestion, error) {
	return nil, nil
}

func (f *fakeSuggestSuggestions) FindAll(context.Context) ([]domain.TeamTrainingSuggestion, error) {
	return f.rows, f.err
}

func (f *fakeSuggestSuggestions) GetByPeriod(_ context.Context, start, end int64) (*domain.TeamTrainingSuggestion, error) {
	if f.byPer == nil {
		return nil, f.err
	}
	return f.byPer[[2]int64{start, end}], f.err
}

func (f *fakeSuggestSuggestions) MarkGenerated(_ context.Context, id int64, batchNo, modulesJSON, summary, modelName, promptVersion string, _ time.Time) error {
	f.generated = &suggestGeneratedCall{id: id, batchNo: batchNo, modulesJSON: modulesJSON, summary: summary, modelName: modelName, promptVersion: promptVersion}
	return nil
}

func (f *fakeSuggestSuggestions) MarkFailed(_ context.Context, id int64, errSummary string) error {
	f.failed = &suggestFailedCall{id: id, reason: errSummary}
	return nil
}

func (f *fakeSuggestSuggestions) TouchGenerating(_ context.Context, id int64, at time.Time) error {
	f.touched = &suggestTouchedCall{id: id, at: at}
	return nil
}

var _ repository.TeamTrainingSuggestionRepository = (*fakeSuggestSuggestions)(nil)

// fakeSuggestQueries 是 DashboardQueryRepository 的建议链路假实现。
type fakeSuggestQueries struct {
	pending  *domain.AssessmentBatch
	scanRows []domain.TeamTrainingSuggestion // FindEarliestPendingSuggestBatch 入参探针
	dimByPer map[[2]int64][]domain.DimensionScore
	aggByPer map[[2]int64][]domain.AggregateScore
	actByPer map[[2]int64][]domain.ActivityStat
	err      error
	dimCalls int
	aggCalls int
	actCalls int
}

func (f *fakeSuggestQueries) ListPeriods(context.Context) ([]repository.PeriodBound, error) {
	return nil, nil
}

func (f *fakeSuggestQueries) ListActivityByPeriod(_ context.Context, start, end int64) ([]domain.ActivityStat, error) {
	f.actCalls++
	return f.actByPer[[2]int64{start, end}], f.err
}

func (f *fakeSuggestQueries) ListDimScoresByPeriod(_ context.Context, start, end int64) ([]domain.DimensionScore, error) {
	f.dimCalls++
	return f.dimByPer[[2]int64{start, end}], f.err
}

func (f *fakeSuggestQueries) ListDimScoresByPeriods(context.Context, []repository.PeriodBound) ([]domain.DimensionScore, error) {
	return nil, nil
}

func (f *fakeSuggestQueries) ListModuleAggScoresByPeriods(_ context.Context, bounds []repository.PeriodBound) ([]domain.AggregateScore, error) {
	f.aggCalls++
	// 单区间调用：按首区间归并行。
	if len(bounds) == 0 {
		return nil, f.err
	}
	key := [2]int64{bounds[0].StartAt.Unix(), bounds[0].EndAt.Unix()}
	var out []domain.AggregateScore
	for _, r := range f.aggByPer[key] {
		if r.PeriodStartAt.Unix() == key[0] && r.PeriodEndAt.Unix() == key[1] {
			out = append(out, r)
		}
	}
	return out, f.err
}

func (f *fakeSuggestQueries) FindLatestFinishedAt(context.Context, int64, int64) (*time.Time, error) {
	return nil, nil
}

func (f *fakeSuggestQueries) FindEarliestPendingSuggestBatch(_ context.Context, rows []domain.TeamTrainingSuggestion) (*domain.AssessmentBatch, error) {
	f.scanRows = rows
	return f.pending, f.err
}

var _ repository.DashboardQueryRepository = (*fakeSuggestQueries)(nil)

// fakeSuggestEnqueuer 是 service.SuggestEnqueuer 的假实现。
type fakeSuggestEnqueuer struct {
	batchIDs []int64
	err      error
	called   bool
}

func (f *fakeSuggestEnqueuer) EnqueueSuggestGenerate(_ context.Context, batchID int64) error {
	f.called = true
	f.batchIDs = append(f.batchIDs, batchID)
	return f.err
}

// fakeSuggestGenerator 是建议生成器假实现（鸭子满足 service.SuggestGenerator 接口）。
type fakeSuggestGenerator struct {
	called   bool
	material suggestgen.Material
	out      suggestgen.Output
	model    string
	err      error
}

func (f *fakeSuggestGenerator) Generate(_ context.Context, m suggestgen.Material) (suggestgen.Output, string, error) {
	f.called = true
	f.material = m
	return f.out, f.model, f.err
}

// newSuggestSvc 通用构造器：gen 非 nil 时经注入器替换生成器（fake 注入）。
func newSuggestSvc(sug *fakeSuggestSuggestions, q *fakeSuggestQueries, b *fakeBatchRepo,
	gen *fakeSuggestGenerator, enq *fakeSuggestEnqueuer, dims *fakeDashboardDimRepo, ua *fakeUserapiClient) *service.SuggestService {
	encKey := crypto.DeriveKey("test-suggest")
	cipher, err := crypto.Encrypt(encKey, "suggest-secret")
	if err != nil {
		panic(err)
	}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	var inject service.SuggestGeneratorInjector
	if gen != nil {
		inject = func() service.SuggestGenerator { return gen }
	}
	return service.NewSuggestService(sug, q, b, dims, nil, enq, secretRepo, encKey, ua, inject)
}

// sugWeek 建议链路测试周期（与 dashWeek 同构，独立命名防串扰）。
func sugWeek(n int) repository.PeriodBound {
	return repository.PeriodBound{
		StartAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
		EndAt:   time.Date(2026, 9, 29, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
	}
}

// sugBatch 构造周期终态批次（triggered_at 偏移 days 天）。
func sugBatch(id int64, batchNo string, week repository.PeriodBound, days int) *domain.AssessmentBatch {
	triggered := time.Date(2026, 9, 22, 23, 0, 0, 0, time.UTC).AddDate(0, 0, days)
	finished := triggered.Add(2 * time.Hour)
	return &domain.AssessmentBatch{
		ID: id, BatchNo: batchNo, TriggerType: domain.BatchTriggerScheduled,
		Status:        domain.BatchStatusSuccess,
		PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
		TriggeredAt: triggered, FinishedAt: &finished,
	}
}

// ===== TickScan =====

// TestTickScan_PicksEarliest 验证命中时 EnsureGenerating 收到拾取批次
//（triggered_at 最早者，由仓储拾取扫描圈定）的批号与 period 双界、
// status=generating、内容列占位空串（specs §5.1.2 步1/步2）。
func TestTickScan_PicksEarliest(t *testing.T) {
	w0 := sugWeek(0)
	late := sugBatch(101, "B-101", w0, 3)
	sug := &fakeSuggestSuggestions{}
	q := &fakeSuggestQueries{pending: late}
	enq := &fakeSuggestEnqueuer{}
	svc := newSuggestSvc(sug, q, &fakeBatchRepo{}, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	if err := svc.TickScan(context.Background(), time.Now()); err != nil {
		t.Fatalf("TickScan: %v", err)
	}
	if sug.ensureCalls != 1 {
		t.Fatalf("EnsureGenerating want 恰一次, got %d", sug.ensureCalls)
	}
	row := sug.ensureRows[0]
	if row.BatchNo != "B-101" {
		t.Fatalf("batch_no want B-101, got %s", row.BatchNo)
	}
	if row.PeriodStartAt.Unix() != w0.StartAt.Unix() || row.PeriodEndAt.Unix() != w0.EndAt.Unix() {
		t.Fatalf("period 双界异常: %v~%v", row.PeriodStartAt, row.PeriodEndAt)
	}
	if row.Status != domain.SuggestionStatusGenerating {
		t.Fatalf("status want generating, got %s", row.Status)
	}
	if row.ModulesJSON != "" || row.Summary != "" || row.ModelName != "" || row.ErrorSummary != "" || row.GeneratedAt != nil {
		t.Fatalf("建行内容列应占位空值, got %+v", row)
	}
	if len(enq.batchIDs) != 1 || enq.batchIDs[0] != 101 {
		t.Fatalf("投递 want batchID=101 一次, got %v", enq.batchIDs)
	}
}

// TestTickScan_NoHit 验证无终态周期批次或建议行 generating 中（仓储拾取扫描
// 不命中）时返回 nil 且不建行不投递（specs §5.1.4 规则5 拾取锁、03 §4.1 步2）。
func TestTickScan_NoHit(t *testing.T) {
	cases := []struct {
		name  string
		rows  []domain.TeamTrainingSuggestion
		batch *domain.AssessmentBatch
	}{
		{"无终态周期批次", nil, nil},
		{"建议行生成中即拾取锁", []domain.TeamTrainingSuggestion{{
			ID: 9, BatchNo: "B-1", Status: domain.SuggestionStatusGenerating,
			PeriodStartAt: sugWeek(0).StartAt, PeriodEndAt: sugWeek(0).EndAt,
		}}, nil},
	}
	for _, c := range cases {
		sug := &fakeSuggestSuggestions{rows: c.rows}
		q := &fakeSuggestQueries{pending: c.batch}
		enq := &fakeSuggestEnqueuer{}
		svc := newSuggestSvc(sug, q, &fakeBatchRepo{}, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})
		if err := svc.TickScan(context.Background(), time.Now()); err != nil {
			t.Fatalf("%s: TickScan want nil, got %v", c.name, err)
		}
		if len(q.scanRows) != len(c.rows) || sug.ensureCalls != 0 || enq.called {
			t.Fatalf("%s: 建行/投递不应发生: ensure=%d enqueue=%v", c.name, sug.ensureCalls, enq.called)
		}
	}
}

// TestTickScan_EnqueueFailKeepsRow 验证投递失败 err 透传且建行保持 generating
//（specs §5.1.4 规则5、03 §4.1 步4：行不回滚，重试 tick 沿既有生成中行续作）。
func TestTickScan_EnqueueFailKeepsRow(t *testing.T) {
	w0 := sugWeek(0)
	sug := &fakeSuggestSuggestions{}
	q := &fakeSuggestQueries{pending: sugBatch(55, "B-55", w0, 0)}
	enq := &fakeSuggestEnqueuer{err: errors.New("redis down")}
	svc := newSuggestSvc(sug, q, &fakeBatchRepo{}, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	err := svc.TickScan(context.Background(), time.Now())
	if err == nil {
		t.Fatal("投递失败 err 应透传")
	}
	if sug.ensureCalls != 1 || sug.ensureRows[0].Status != domain.SuggestionStatusGenerating {
		t.Fatalf("建行已发生且形态为 generating: %+v", sug.ensureRows)
	}
	if sug.failed != nil {
		t.Fatalf("投递失败不应触发 MarkFailed, got %+v", sug.failed)
	}
}

// ===== TickScan 滞留续投（specs §5.1.4 规则5 滞留恢复、03 §4.1 步4） =====

// stuckRow 构造滞留场景生成中建议行（UpdatedAt 偏移 dur）。
func stuckRow(id int64, batchNo string, week repository.PeriodBound, updatedAgo time.Duration) domain.TeamTrainingSuggestion {
	return domain.TeamTrainingSuggestion{
		ID: id, BatchNo: batchNo, Status: domain.SuggestionStatusGenerating,
		PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
		UpdatedAt: time.Now().Add(-updatedAgo),
	}
}

// TestTickScan_RetryStuckReenqueues 验证过期 generating 行且批次存在终态时
// 续投：Enqueuer 收到该批次 ID、行 updated_at 被刷新、不建新行（specs §5.1.5 滞留恢复）。
func TestTickScan_RetryStuckReenqueues(t *testing.T) {
	w0 := sugWeek(0)
	now := time.Now()
	sug := &fakeSuggestSuggestions{rows: []domain.TeamTrainingSuggestion{
		stuckRow(9, "B-9", w0, 25*time.Minute),
	}}
	q := &fakeSuggestQueries{} // 拾取扫描无命中（生成中行即拾取锁）
	b := &fakeBatchRepo{byBatchNo: sugBatch(9, "B-9", w0, 0)}
	enq := &fakeSuggestEnqueuer{}
	svc := newSuggestSvc(sug, q, b, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	if err := svc.TickScan(context.Background(), now); err != nil {
		t.Fatalf("TickScan: %v", err)
	}
	if !enq.called || len(enq.batchIDs) != 1 || enq.batchIDs[0] != 9 {
		t.Fatalf("续投 want batchID=9 一次, got %v", enq.batchIDs)
	}
	if sug.touched == nil || sug.touched.id != 9 || !sug.touched.at.Equal(now) {
		t.Fatalf("续投后应刷新行 updated_at, got %+v", sug.touched)
	}
	if sug.ensureCalls != 0 || sug.failed != nil {
		t.Fatalf("续投不应建行或 MarkFailed: ensure=%d failed=%+v", sug.ensureCalls, sug.failed)
	}
}

// TestTickScan_FreshGeneratingSkipped 验证未过期 generating 行（合法在途窗口内）
// 不续投：Enqueuer 未被调用、返回 nil（specs §5.1.4 规则4 成本口径防重复投递）。
func TestTickScan_FreshGeneratingSkipped(t *testing.T) {
	w0 := sugWeek(0)
	sug := &fakeSuggestSuggestions{rows: []domain.TeamTrainingSuggestion{
		stuckRow(9, "B-9", w0, 5*time.Minute),
	}}
	q := &fakeSuggestQueries{}
	b := &fakeBatchRepo{byBatchNo: sugBatch(9, "B-9", w0, 0)}
	enq := &fakeSuggestEnqueuer{}
	svc := newSuggestSvc(sug, q, b, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	if err := svc.TickScan(context.Background(), time.Now()); err != nil {
		t.Fatalf("TickScan want nil, got %v", err)
	}
	if enq.called || sug.touched != nil || sug.ensureCalls != 0 {
		t.Fatalf("未过期行不应续投: enq=%v touched=%+v ensure=%d", enq.called, sug.touched, sug.ensureCalls)
	}
}

// TestTickScan_StuckBatchMissingFails 验证过期行但批次已删（GetByBatchNo 无行）
// 时 MarkFailed 记因非空、不投递（specs §5.1.5 滞留行恢复的批次缺失分支）。
func TestTickScan_StuckBatchMissingFails(t *testing.T) {
	w0 := sugWeek(0)
	sug := &fakeSuggestSuggestions{rows: []domain.TeamTrainingSuggestion{
		stuckRow(11, "B-GONE", w0, 30*time.Minute),
	}}
	q := &fakeSuggestQueries{}
	b := &fakeBatchRepo{}
	enq := &fakeSuggestEnqueuer{}
	svc := newSuggestSvc(sug, q, b, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	if err := svc.TickScan(context.Background(), time.Now()); err != nil {
		t.Fatalf("TickScan: %v", err)
	}
	if sug.failed == nil || sug.failed.id != 11 || sug.failed.reason == "" {
		t.Fatalf("批次缺失应 MarkFailed 且记因非空, got %+v", sug.failed)
	}
	if enq.called || sug.touched != nil {
		t.Fatalf("批次缺失不应投递或刷新: enq=%v touched=%+v", enq.called, sug.touched)
	}
}

// TestTickScan_PickBeatsStuckRetry 验证拾取扫描命中新批次时优先正常拾取路径，
// 续投仅在扫描无命中分支执行（specs §5.1.4 规则5：续投与拾取判定解耦）。
func TestTickScan_PickBeatsStuckRetry(t *testing.T) {
	w0 := sugWeek(0)
	w1 := sugWeek(1)
	sug := &fakeSuggestSuggestions{rows: []domain.TeamTrainingSuggestion{
		stuckRow(9, "B-9", w0, 25*time.Minute), // 过期滞留行
	}}
	q := &fakeSuggestQueries{pending: sugBatch(77, "B-77", w1, 0)} // 扫描命中新批次
	b := &fakeBatchRepo{byBatchNo: sugBatch(9, "B-9", w0, 0)}
	enq := &fakeSuggestEnqueuer{}
	svc := newSuggestSvc(sug, q, b, nil, enq, &fakeDashboardDimRepo{}, &fakeUserapiClient{})

	if err := svc.TickScan(context.Background(), time.Now()); err != nil {
		t.Fatalf("TickScan: %v", err)
	}
	if sug.ensureCalls != 1 || sug.ensureRows[0].BatchNo != "B-77" {
		t.Fatalf("命中批次应走正常拾取建行, got ensure=%d rows=%+v", sug.ensureCalls, sug.ensureRows)
	}
	if len(enq.batchIDs) != 1 || enq.batchIDs[0] != 77 {
		t.Fatalf("投递应只含拾取批次 77, got %v", enq.batchIDs)
	}
	if sug.touched != nil {
		t.Fatalf("正常拾取时不应触发滞留续投刷新, got %+v", sug.touched)
	}
}

// ===== Generate =====

// suggestGenEnv 生成路径测试环境：建议行 generating + 批次 success + 单维度素材底座。
func suggestGenEnv(week repository.PeriodBound) (*fakeSuggestSuggestions, *fakeSuggestQueries, *fakeBatchRepo) {
	row := &domain.TeamTrainingSuggestion{
		ID: 7, BatchNo: "B-7", Status: domain.SuggestionStatusGenerating,
		PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
	}
	sug := &fakeSuggestSuggestions{byPer: map[[2]int64]*domain.TeamTrainingSuggestion{
		{week.StartAt.Unix(), week.EndAt.Unix()}: row,
	}}
	rows := make([]domain.DimensionScore, 0, 3)
	for i := 0; i < 3; i++ {
		rows = append(rows, domain.DimensionScore{
			TokenName: "alpha", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 60,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
		})
	}
	agg := []domain.AggregateScore{{
		TokenName: "alpha", Module: domain.ModuleAIUsage,
		PeriodStartAt: week.StartAt, PeriodEndAt: week.EndAt,
	}}
	q := &fakeSuggestQueries{
		dimByPer: map[[2]int64][]domain.DimensionScore{{week.StartAt.Unix(), week.EndAt.Unix()}: rows},
		aggByPer: map[[2]int64][]domain.AggregateScore{{week.StartAt.Unix(), week.EndAt.Unix()}: agg},
	}
	b := &fakeBatchRepo{byID: sugBatch(7, "B-7", week, 0)}
	return sug, q, b
}

// suggestTestDims 两模块维度全集（含评分锚点，AI_USAGE 含 A/B/C 三维）。
func suggestTestDims() *fakeDashboardDimRepo {
	return &fakeDashboardDimRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度A", Anchor: "锚点A"},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度B", Anchor: "锚点B"},
		{Code: "C", ModuleCode: domain.ModuleAIUsage, Enabled: true, Name: "维度C", Anchor: "锚点C"},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true, Name: "子能力M1", Anchor: "锚点M1"},
	}}
}

// suggestSuccScore 便捷取浮点指针。
func suggestSuccScore(v float64) *float64 { return &v }

// TestGenerate_MissingRowIdempotent 验证批次无行或建议行缺失/非 generating 时
// 返回 nil 且不触生成器（specs §5.1.2 步2 竞态残留幂等、03 §4.2 步1）。
func TestGenerate_MissingRowIdempotent(t *testing.T) {
	w0 := sugWeek(0)
	// 批次无行（byID 为 nil，fake GetByID 不看入参）。
	sug, q, b := suggestGenEnv(w0)
	b.byID = nil
	gen := &fakeSuggestGenerator{}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(), &fakeUserapiClient{})
	if err := svc.Generate(context.Background(), 404); err != nil {
		t.Fatalf("批次无行 want nil, got %v", err)
	}
	if gen.called {
		t.Fatal("批次无行不应触生成器")
	}

	// 建议行缺失（独立环境，防终态分支的行映射影响生成器判定）。
	sug2, q2, b2 := suggestGenEnv(w0)
	sug2.byPer = nil
	gen2 := &fakeSuggestGenerator{}
	svc2 := newSuggestSvc(sug2, q2, b2, gen2, &fakeSuggestEnqueuer{}, suggestTestDims(), &fakeUserapiClient{})
	if err := svc2.Generate(context.Background(), 7); err != nil {
		t.Fatalf("建议行缺失 want nil, got %v", err)
	}
	if gen2.called {
		t.Fatal("建议行缺失不应触生成器")
	}

	// 建议行已终态（竞态残留）。
	done := &domain.TeamTrainingSuggestion{ID: 7, Status: domain.SuggestionStatusGenerated,
		PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt}
	sug.byPer = map[[2]int64]*domain.TeamTrainingSuggestion{{w0.StartAt.Unix(), w0.EndAt.Unix()}: done}
	if err := svc.Generate(context.Background(), 7); err != nil {
		t.Fatalf("非 generating 行 want nil, got %v", err)
	}
	if gen.called || sug.failed != nil {
		t.Fatal("非 generating 行不应触生成器或 MarkFailed")
	}
}

// TestGenerate_DataAnomalyFails 验证该 period 三素材表均无行时建议行落 failed、
// 记因非空、不触生成器、返回 nil（specs §5.1.5 末行：避免 tick 反复重扫）。
func TestGenerate_DataAnomalyFails(t *testing.T) {
	w0 := sugWeek(0)
	sug, q, b := suggestGenEnv(w0)
	q.dimByPer = map[[2]int64][]domain.DimensionScore{}
	q.aggByPer = map[[2]int64][]domain.AggregateScore{}
	gen := &fakeSuggestGenerator{}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(), &fakeUserapiClient{})

	if err := svc.Generate(context.Background(), 7); err != nil {
		t.Fatalf("数据异常 want nil, got %v", err)
	}
	if gen.called {
		t.Fatal("数据异常不应触生成器")
	}
	if sug.failed == nil {
		t.Fatal("数据异常应 MarkFailed")
	}
	if sug.failed.id != 7 || sug.failed.reason == "" {
		t.Fatalf("MarkFailed id=7 且记因非空, got %+v", sug.failed)
	}
	if q.actCalls == 0 {
		t.Fatal("异常判定应覆盖活跃度表查询（三表口径）")
	}
}

// TestGenerate_ActivityOnlyStillRuns 验证仅 activity_stats 有行时照常生成
//（specs §5.1.5 全员未使用场景：活跃度三态计数即素材）。
func TestGenerate_ActivityOnlyStillRuns(t *testing.T) {
	w0 := sugWeek(0)
	sug, q, b := suggestGenEnv(w0)
	q.dimByPer = map[[2]int64][]domain.DimensionScore{}
	q.aggByPer = map[[2]int64][]domain.AggregateScore{}
	key := [2]int64{w0.StartAt.Unix(), w0.EndAt.Unix()}
	q.actByPer = map[[2]int64][]domain.ActivityStat{key: {
		{TokenName: "a", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		{TokenName: "b", ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	}}
	gen := &fakeSuggestGenerator{out: suggestgen.Output{Summary: "s"}, model: "m-1"}
	ua := &fakeUserapiClient{staffs: dashStaffNames(2), total: 2}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(), ua)

	if err := svc.Generate(context.Background(), 7); err != nil {
		t.Fatalf("仅活跃度素材 want nil, got %v", err)
	}
	if !gen.called {
		t.Fatal("仅活跃度素材应照常生成")
	}
	if gen.material.Activity.StaffTotal != 2 || gen.material.Activity.Active != 1 || gen.material.Activity.Unused != 1 {
		t.Fatalf("活跃度三态素材异常: %+v", gen.material.Activity)
	}
	if sug.generated == nil {
		t.Fatal("应落 generated 终态")
	}
}

// TestGenerate_MarksGenerated 验证成功路径 MarkGenerated 收到 modules_json 与
// model_name、batch_no 对位（specs §5.1.2 步5）。
func TestGenerate_MarksGenerated(t *testing.T) {
	sug, q, b := suggestGenEnv(sugWeek(0))
	gen := &fakeSuggestGenerator{
		out: suggestgen.Output{
			Modules: []suggestgen.ModuleOutput{{Module: domain.ModuleAIUsage, Suggestions: []suggestgen.SuggestionOutput{
				{Name: "提示词训练", Description: "针对低分维度"},
			}}},
			Summary: "整体稳健",
		},
		model: "kimi-k2.7",
	}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(),
		&fakeUserapiClient{staffs: dashStaffNames(1), total: 1})

	if err := svc.Generate(context.Background(), 7); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if sug.generated == nil {
		t.Fatal("应落 generated 终态")
	}
	if sug.generated.id != 7 || sug.generated.batchNo != "B-7" {
		t.Fatalf("终态 id/batch_no 异常: %+v", sug.generated)
	}
	if sug.generated.modelName != "kimi-k2.7" {
		t.Fatalf("model_name want kimi-k2.7, got %s", sug.generated.modelName)
	}
	if sug.generated.promptVersion != suggestgen.PromptVersion {
		t.Fatalf("prompt_version want %s, got %s", suggestgen.PromptVersion, sug.generated.promptVersion)
	}
	if sug.generated.summary != "整体稳健" {
		t.Fatalf("summary want 整体稳健, got %s", sug.generated.summary)
	}
	wantJSON := `[{"module":"AI_USAGE","suggestions":[{"name":"提示词训练","description":"针对低分维度"}]}]`
	if sug.generated.modulesJSON != wantJSON {
		t.Fatalf("modules_json 异常:\n got %s\nwant %s", sug.generated.modulesJSON, wantJSON)
	}
	if sug.failed != nil {
		t.Fatalf("成功路径不应 MarkFailed, got %+v", sug.failed)
	}
}

// TestGenerate_MaterialAggregation 验证素材汇总口径同看板（specs §5.1.3、§5.1.2 步3）：
// 均分/低分占比按模块 source 聚合且剔除 failed 行、聚合分取 module_score 平均、
// 短板按模块内最低维、维度口径快照含锚点、staff_total 取 userapi 名单计数。
func TestGenerate_MaterialAggregation(t *testing.T) {
	w0 := sugWeek(0)
	sug, q, b := suggestGenEnv(w0)
	key := [2]int64{w0.StartAt.Unix(), w0.EndAt.Unix()}
	// 追加素材：AI_USAGE B 维 3 人均 80；AI_MGMT M1 维 3 人均 60（另 1 人 failed 应剔除）。
	// 底座已有 A 维 3 人均 60。
	q.dimByPer[key] = append(q.dimByPer[key],
		domain.DimensionScore{	TokenName: "u1", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 80,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "u2", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 80,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "u3", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 80,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "m1", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 60,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceActiveTest,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "m2", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 60,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceActiveTest,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "m3", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 60,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceActiveTest,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "m4", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 100,
			Status: domain.ScoreStatusFailed, Source: domain.ScoreSourceActiveTest,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "c1", Module: domain.ModuleAIUsage, DimensionCode: "C", Score: 90,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "c2", Module: domain.ModuleAIUsage, DimensionCode: "C", Score: 90,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		domain.DimensionScore{	TokenName: "c3", Module: domain.ModuleAIUsage, DimensionCode: "C", Score: 90,
			Status: domain.ScoreStatusSuccess, Source: domain.ScoreSourceConversation,
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	)
	q.aggByPer[key] = []domain.AggregateScore{
		{TokenName: "u1", Module: domain.ModuleAIUsage, ModuleScore: suggestSuccScore(70),
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
		{TokenName: "u2", Module: domain.ModuleAIUsage, ModuleScore: suggestSuccScore(90),
			PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	}
	q.actByPer = map[[2]int64][]domain.ActivityStat{key: {
		{TokenName: "u1", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt},
	}}
	gen := &fakeSuggestGenerator{out: suggestgen.Output{Summary: "ok"}, model: "m"}
	ua := &fakeUserapiClient{staffs: dashStaffNames(3), total: 3}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(), ua)

	if err := svc.Generate(context.Background(), 7); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	m := gen.material
	if len(m.Modules) != 2 {
		t.Fatalf("素材恒两模块, got %d", len(m.Modules))
	}
	usage := m.Modules[0]
	if usage.Module != domain.ModuleAIUsage {
		t.Fatalf("modules[0] want AI_USAGE, got %s", usage.Module)
	}
	if usage.AggScore == nil || *usage.AggScore != 80.0 {
		t.Fatalf("AI_USAGE 聚合分 want 80.0（(70+90)/2）, got %+v", usage.AggScore)
	}
	byCode := map[string]suggestgen.DimAvg{}
	for _, d := range usage.DimAverages {
		byCode[d.Code] = d
	}
	if a := byCode["A"]; a.Avg == nil || *a.Avg != 60 || a.LowRatio == nil || *a.LowRatio != 0.0 {
		t.Fatalf("维度 A 均 60/低分占比 0.0（60 分不低于低分线 60）, got %+v", a)
	}
	if bb := byCode["B"]; bb.Avg == nil || *bb.Avg != 80 || bb.LowRatio == nil || *bb.LowRatio != 0.0 {
		t.Fatalf("维度 B 均 80/低分占比 0.0, got %+v", bb)
	}
	mgmt := m.Modules[1]
	if mgmt.AggScore != nil {
		t.Fatalf("AI_MGMT 无聚合行素材应 nil, got %+v", mgmt.AggScore)
	}
	if len(mgmt.DimAverages) != 1 || mgmt.DimAverages[0].Avg == nil || *mgmt.DimAverages[0].Avg != 60 {
		t.Fatalf("M1 均 60（failed 行剔除）, got %+v", mgmt.DimAverages)
	}
	if len(m.WeakDimensions) != 3 {
		t.Fatalf("两模块短板 want AI_USAGE{A,B} + AI_MGMT{M1} 共 3 项, got %+v", m.WeakDimensions)
	}
	weakByMod := map[string]map[string]bool{}
	for _, w := range m.WeakDimensions {
		if weakByMod[w.Module] == nil {
			weakByMod[w.Module] = map[string]bool{}
		}
		weakByMod[w.Module][w.Code] = true
	}
	if !weakByMod[domain.ModuleAIUsage]["A"] || !weakByMod[domain.ModuleAIUsage]["B"] || weakByMod[domain.ModuleAIUsage]["C"] {
		t.Fatalf("AI_USAGE 短板 want {A,B}（C 均 90 非最低 2 维）, got %+v", weakByMod[domain.ModuleAIUsage])
	}
	if !weakByMod[domain.ModuleAIMgmt]["M1"] {
		t.Fatalf("AI_MGMT 唯一维 M1 应入短板, got %+v", weakByMod[domain.ModuleAIMgmt])
	}
	if len(m.DimSpecs) != 4 {
		t.Fatalf("维度口径快照 want 4 项（两模块全集）, got %+v", m.DimSpecs)
	}
	if m.Activity.Active != 1 || m.Activity.StaffTotal != 3 {
		t.Fatalf("活跃度素材异常: %+v", m.Activity)
	}
}

// TestGenerate_LLMFailPropagates 验证生成器返错时 err 上抛且建议行保持 generating
//（specs §5.1.5 LLM 失败路径：交 Asynq 任务级重试，行不落终态）。
func TestGenerate_LLMFailPropagates(t *testing.T) {
	sug, q, b := suggestGenEnv(sugWeek(0))
	gen := &fakeSuggestGenerator{err: errors.New("llm timeout")}
	svc := newSuggestSvc(sug, q, b, gen, &fakeSuggestEnqueuer{}, suggestTestDims(),
		&fakeUserapiClient{staffs: dashStaffNames(1), total: 1})

	err := svc.Generate(context.Background(), 7)
	if err == nil {
		t.Fatal("LLM 失败 err 应上抛")
	}
	if sug.generated != nil || sug.failed != nil {
		t.Fatalf("失败路径不应落终态: generated=%+v failed=%+v", sug.generated, sug.failed)
	}
}

// ===== MarkFailedIfExhausted =====

// TestMarkFailedIfExhausted 验证重试耗尽钩子对 generating 行落 failed 记因、
// 对终态行与批次缺失幂等返回（specs §5.1.4 规则3、03 §4.2 末段）。
func TestMarkFailedIfExhausted(t *testing.T) {
	w0 := sugWeek(0)
	sug, q, b := suggestGenEnv(w0)
	svc := newSuggestSvc(sug, q, b, nil, &fakeSuggestEnqueuer{}, suggestTestDims(), &fakeUserapiClient{})

	if err := svc.MarkFailedIfExhausted(context.Background(), 7, "重试耗尽"); err != nil {
		t.Fatalf("MarkFailedIfExhausted: %v", err)
	}
	if sug.failed == nil || sug.failed.id != 7 || sug.failed.reason != "重试耗尽" {
		t.Fatalf("generating 行应落 failed 记因, got %+v", sug.failed)
	}

	// 终态行：幂等返回且不再 MarkFailed。
	sug.failed = nil
	done := &domain.TeamTrainingSuggestion{ID: 7, Status: domain.SuggestionStatusGenerated,
		PeriodStartAt: w0.StartAt, PeriodEndAt: w0.EndAt}
	sug.byPer[[2]int64{w0.StartAt.Unix(), w0.EndAt.Unix()}] = done
	if err := svc.MarkFailedIfExhausted(context.Background(), 7, "重试耗尽"); err != nil {
		t.Fatalf("终态行 want nil, got %v", err)
	}
	if sug.failed != nil {
		t.Fatalf("终态行应幂等跳过, got %+v", sug.failed)
	}

	// 批次无行：幂等返回。
	sug.byPer = nil
	if err := svc.MarkFailedIfExhausted(context.Background(), 404, "重试耗尽"); err != nil {
		t.Fatalf("批次无行 want nil, got %v", err)
	}
	if sug.failed != nil {
		t.Fatalf("批次无行应幂等跳过, got %+v", sug.failed)
	}
}
