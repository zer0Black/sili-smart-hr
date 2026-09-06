package scorer

// scorer_test.go 契约测试：多维打分与聚合（specs §2.4 能力5 / §5.1 聚合用例逐条锚定）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/repository"
)

// ---- fake 依赖 ----

// fakeScoreRepo 评分行仓储 fake：内存承载双界精确过滤读。
type fakeScoreRepo struct {
	existing []domain.DimensionScore
	listErr  error
	listArg  struct {
		token string
		start int64
		end   int64
	}
	listCalls int
}

func (f *fakeScoreRepo) ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error) {
	f.listCalls++
	f.listArg.token = tokenName
	f.listArg.start = start
	f.listArg.end = end
	if f.listErr != nil {
		return nil, f.listErr
	}
	startAt := time.Unix(start, 0).UTC()
	endAt := time.Unix(end, 0).UTC()
	out := make([]domain.DimensionScore, 0, len(f.existing))
	for _, r := range f.existing {
		if r.TokenName == tokenName && r.PeriodStartAt.Equal(startAt) && r.PeriodEndAt.Equal(endAt) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeScoreRepo) DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error) {
	return 0, nil
}

func (f *fakeScoreRepo) SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error {
	return nil
}

var _ repository.DimensionScoreRepository = (*fakeScoreRepo)(nil)

// upsertRecord 单次 UpsertAll 收到的行集快照。
type upsertRecord struct {
	token string
	start int64
	end   int64
	rows  []domain.AggregateScore
}

// fakeAggRepo 聚合行仓储 fake：记录每次 UpsertAll 的入参快照。
type fakeAggRepo struct {
	upserts     []upsertRecord
	upsertCalls int
	err         error
}

func (f *fakeAggRepo) UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error {
	f.upsertCalls++
	if f.err != nil {
		return f.err
	}
	snap := make([]domain.AggregateScore, len(rows))
	copy(snap, rows)
	f.upserts = append(f.upserts, upsertRecord{token: tokenName, start: start, end: end, rows: snap})
	return nil
}

var _ repository.AggregateScoreRepository = (*fakeAggRepo)(nil)

// ---- 测试辅助 ----

func testPeriod() activity.Period {
	return activity.Period{Start: 1754000000, End: 1754000000 + 7*24*3600}
}

// buildEvidenceJSON 构造最小 evidence_json：只承载口径摘要（结构字段与生产侧键对齐）。
func buildEvidenceJSON(snaps []specSnapshot) string {
	raw, _ := json.Marshal(evidenceDoc{DimensionSpecs: snaps})
	return string(raw)
}

// threeUsageSpecs AI_USAGE 三维口径 50/30/20。
func threeUsageSpecs() []specSnapshot {
	return []specSnapshot{
		{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true},
		{Code: "AI_VALUE", Weight: 30, InOverview: true},
		{Code: "AI_REVIEW", Weight: 20, InOverview: true},
	}
}

// scoreRow 组装一条评分行。
func scoreRow(p activity.Period, code, module string, score int, insufficient bool, source, status string, evidence string) domain.DimensionScore {
	return domain.DimensionScore{
		TokenName:     "张三",
		PeriodStartAt: time.Unix(p.Start, 0).UTC(),
		PeriodEndAt:   time.Unix(p.End, 0).UTC(),
		DimensionCode: code,
		Module:        module,
		Score:         score,
		Insufficient:  insufficient,
		EvidenceJSON:  evidence,
		Source:        source,
		Status:        status,
	}
}

// usageRows 标准 3 维评分行：80/60/40 权重 50/30/20，60 维 insufficient。
func usageRows(p activity.Period) []domain.DimensionScore {
	ev := buildEvidenceJSON(threeUsageSpecs())
	return []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "AI_VALUE", "AI_USAGE", 60, true, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "AI_REVIEW", "AI_USAGE", 40, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}
}

// aggRowByModule 按模块取聚合行。
func aggRowByModule(t *testing.T, rows []domain.AggregateScore, module string) domain.AggregateScore {
	t.Helper()
	for _, r := range rows {
		if r.Module == module {
			return r
		}
	}
	t.Fatalf("聚合行缺模块 %s: %+v", module, rows)
	return domain.AggregateScore{}
}

// runAggregate 执行一次 Aggregate 并断言无错。
func runAggregate(t *testing.T, s *Scorer) *AggregateResult {
	t.Helper()
	res, err := s.Aggregate(context.Background(), "张三", testPeriod())
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res == nil {
		t.Fatal("Aggregate 返回 nil")
	}
	return res
}

// requireFloat 断言浮点近似相等（1e-9）。
func requireFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// ---- specs §5.1 聚合用例 ----

// TestAggregateWeightedAndExcluded 锚点：3 维 80/60/40 权重 50/30/20，60 维
// insufficient → 模块分 = (80×50+40×20)/70 = 68.571… → 68.6；Excluded 含该维
// code；落库行 included_json 反序列化含 [{code,weight}]。
func TestAggregateWeightedAndExcluded(t *testing.T) {
	scores := &fakeScoreRepo{existing: usageRows(testPeriod())}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 68.6)
	if len(res.Excluded["AI_USAGE"]) != 1 || res.Excluded["AI_USAGE"][0] != "AI_VALUE" {
		t.Errorf("Excluded[AI_USAGE] = %v, want [AI_VALUE]", res.Excluded["AI_USAGE"])
	}
	if agg.upsertCalls != 1 {
		t.Fatalf("UpsertAll 调用 %d 次, want 1", agg.upsertCalls)
	}
	row := aggRowByModule(t, agg.upserts[0].rows, "AI_USAGE")
	var included []IncludedWeight
	if err := json.Unmarshal([]byte(row.IncludedJSON), &included); err != nil {
		t.Fatalf("included_json 非法: %v", err)
	}
	if len(included) != 2 {
		t.Fatalf("included 维度数 %d, want 2（AI_INSTRUCTION/AI_REVIEW）", len(included))
	}
	if included[0].Code != "AI_INSTRUCTION" || included[0].Weight != 50 {
		t.Errorf("included[0] = %+v, want {AI_INSTRUCTION 50}", included[0])
	}
	if included[1].Code != "AI_REVIEW" || included[1].Weight != 20 {
		t.Errorf("included[1] = %+v, want {AI_REVIEW 20}", included[1])
	}
	// 总览单模块场景与模块分同构。
	requireFloat(t, "总览分", *res.Overview, 68.6)
}

// TestAggregateAllExcludedModule 锚点：某模块全维 insufficient → 该模块分 nil、
// 其余模块照常、总览按其余模块参与维计算。
func TestAggregateAllExcludedModule(t *testing.T) {
	p := testPeriod()
	usageEv := buildEvidenceJSON([]specSnapshot{
		{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true},
		{Code: "AI_VALUE", Weight: 50, InOverview: true},
	})
	mgmtEv := buildEvidenceJSON([]specSnapshot{{Code: "AI_MGMT_PLAN", Weight: 40, InOverview: true}})
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, usageEv),
		scoreRow(p, "AI_VALUE", "AI_USAGE", 60, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, usageEv),
		// AI_MGMT 全维 insufficient。
		scoreRow(p, "AI_MGMT_PLAN", "AI_MGMT", 0, true, domain.ScoreSourceActiveTest, domain.ScoreStatusSuccess, mgmtEv),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	got, ok := res.ModuleScores["AI_MGMT"]
	if !ok || got != nil {
		t.Errorf("AI_MGMT 模块分 = %v (present=%v), want 键存在且值 nil", got, ok)
	}
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 70)
	// 总览 = (80×50+60×50)/100 = 70，只按 AI_USAGE 参与维。
	requireFloat(t, "总览分", *res.Overview, 70)
	row := aggRowByModule(t, agg.upserts[0].rows, "AI_MGMT")
	if row.ModuleScore != nil {
		t.Errorf("AI_MGMT 落库行 ModuleScore = %v, want nil", *row.ModuleScore)
	}
}

// TestAggregateAllExcludedOverview 锚点：全部维度 insufficient → Overview nil、
// 模块分全 nil、聚合行照常落库（nil 显式落空，fake 断言 UpsertAll 收到 nil 指针）。
func TestAggregateAllExcludedOverview(t *testing.T) {
	p := testPeriod()
	ev := buildEvidenceJSON([]specSnapshot{{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true}})
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 0, true, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	if res.Overview != nil {
		t.Errorf("Overview = %v, want nil", *res.Overview)
	}
	if got := res.ModuleScores["AI_USAGE"]; got != nil {
		t.Errorf("AI_USAGE 模块分 = %v, want nil", *got)
	}
	if agg.upsertCalls != 1 {
		t.Fatalf("全剔除也须落库, UpsertAll 调用 %d 次, want 1", agg.upsertCalls)
	}
	overRow := aggRowByModule(t, agg.upserts[0].rows, domain.ModuleOverview)
	if overRow.OverviewScore != nil {
		t.Errorf("overview 行 OverviewScore = %v, want nil 指针显式落空", *overRow.OverviewScore)
	}
	if overRow.ModuleScore != nil {
		t.Errorf("overview 行 ModuleScore 应恒 nil, got %v", *overRow.ModuleScore)
	}
	modRow := aggRowByModule(t, agg.upserts[0].rows, "AI_USAGE")
	if modRow.ModuleScore != nil {
		t.Errorf("AI_USAGE 行 ModuleScore 应为 nil 指针, got %v", *modRow.ModuleScore)
	}
}

// TestAggregateCrossSource 锚点：conversation 3 维 + active_test 2 维 → 统一纳入
// （模块分含 active_test 维度）、Included 快照含两 source 维度。
func TestAggregateCrossSource(t *testing.T) {
	p := testPeriod()
	usageEv := buildEvidenceJSON([]specSnapshot{
		{Code: "AI_INSTRUCTION", Weight: 60, InOverview: true},
		{Code: "AI_VALUE", Weight: 40, InOverview: true},
	})
	mgmtEv := buildEvidenceJSON([]specSnapshot{
		{Code: "AI_MGMT_PLAN", Weight: 50, InOverview: true},
		{Code: "AI_MGMT_RISK", Weight: 50, InOverview: true},
	})
	rows := []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, usageEv),
		scoreRow(p, "AI_VALUE", "AI_USAGE", 60, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, usageEv),
		scoreRow(p, "AI_MGMT_PLAN", "AI_MGMT", 70, false, domain.ScoreSourceActiveTest, domain.ScoreStatusSuccess, mgmtEv),
		scoreRow(p, "AI_MGMT_RISK", "AI_MGMT", 90, false, domain.ScoreSourceActiveTest, domain.ScoreStatusSuccess, mgmtEv),
	}
	scores := &fakeScoreRepo{existing: rows}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	// AI_MGMT 模块分 = (70×50+90×50)/100 = 80，含 active_test 维度。
	requireFloat(t, "AI_MGMT 模块分", *res.ModuleScores["AI_MGMT"], 80)
	if len(res.Included["AI_MGMT"]) != 2 {
		t.Fatalf("Included[AI_MGMT] = %v, want 2 维", res.Included["AI_MGMT"])
	}
	// 总览跨模块 = (80×60+60×40+70×50+90×50)/200 = 76。
	requireFloat(t, "总览分", *res.Overview, 76)
	// Included 快照含两 source 的模块维度。
	if len(res.Included["AI_USAGE"]) != 2 {
		t.Errorf("Included[AI_USAGE] = %v, want 2 维", res.Included["AI_USAGE"])
	}
}

// TestAggregateWeightOffHundred 锚点：权重合计 80 → 照常按实际权重归一化，无报错。
func TestAggregateWeightOffHundred(t *testing.T) {
	p := testPeriod()
	ev := buildEvidenceJSON([]specSnapshot{
		{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true},
		{Code: "AI_REVIEW", Weight: 30, InOverview: true}, // 合计 80
	})
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "AI_REVIEW", "AI_USAGE", 40, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	// (80×50+40×30)/80 = 65。
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 65)
	requireFloat(t, "总览分", *res.Overview, 65)
}

// TestAggregateFloatPrecision 锚点：(80×50+40×20)/70 = 68.571… → 68.6（round half up）。
func TestAggregateFloatPrecision(t *testing.T) {
	scores := &fakeScoreRepo{existing: usageRows(testPeriod())}
	s := New(scores, &fakeAggRepo{})

	res := runAggregate(t, s)
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 68.6)
}

// TestAggregateIdempotent 锚点：同输入二跑 → fake aggRepo UpsertAll 两次收到同键行集。
func TestAggregateIdempotent(t *testing.T) {
	scores := &fakeScoreRepo{existing: usageRows(testPeriod())}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	runAggregate(t, s)
	runAggregate(t, s)
	if agg.upsertCalls != 2 {
		t.Fatalf("UpsertAll 调用 %d 次, want 2", agg.upsertCalls)
	}
	if len(agg.upserts[0].rows) != len(agg.upserts[1].rows) {
		t.Fatalf("两轮行数不等: %d vs %d", len(agg.upserts[0].rows), len(agg.upserts[1].rows))
	}
	// 逐行比较（模块、分数、快照 JSON 全等）。
	for i := range agg.upserts[0].rows {
		a, b := agg.upserts[0].rows[i], agg.upserts[1].rows[i]
		if a.Module != b.Module || a.IncludedJSON != b.IncludedJSON || a.ExcludedJSON != b.ExcludedJSON {
			t.Errorf("两轮行 %d 不一致: %+v vs %+v", i, a, b)
		}
		if (a.ModuleScore == nil) != (b.ModuleScore == nil) ||
			(a.ModuleScore != nil && *a.ModuleScore != *b.ModuleScore) {
			t.Errorf("两轮行 %d ModuleScore 不一致", i)
		}
		if (a.OverviewScore == nil) != (b.OverviewScore == nil) ||
			(a.OverviewScore != nil && *a.OverviewScore != *b.OverviewScore) {
			t.Errorf("两轮行 %d OverviewScore 不一致", i)
		}
	}
}

// TestAggregateFailedExcluded 锚点：1 维 failed 行 → Excluded 含该维、聚合照常出分。
func TestAggregateFailedExcluded(t *testing.T) {
	p := testPeriod()
	ev := buildEvidenceJSON(threeUsageSpecs())
	failed := scoreRow(p, "AI_VALUE", "AI_USAGE", 0, false, domain.ScoreSourceConversation, domain.ScoreStatusFailed, ev)
	failed.ErrorCode = "ErrLLMEvalUpstream"
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		failed,
		scoreRow(p, "AI_REVIEW", "AI_USAGE", 40, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	// failed 维剔除后 (80×50+40×20)/70 = 68.6，failed 行不阻断重算。
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 68.6)
	found := false
	for _, code := range res.Excluded["AI_USAGE"] {
		if code == "AI_VALUE" {
			found = true
		}
	}
	if !found {
		t.Errorf("Excluded[AI_USAGE] = %v, want 含 failed 维 AI_VALUE", res.Excluded["AI_USAGE"])
	}
}

// TestAggregateReferenceDimExcluded 锚点：in_overview=false 维度 → 不进 Included
// 也不进 Excluded、不影响分数。
func TestAggregateReferenceDimExcluded(t *testing.T) {
	p := testPeriod()
	ev := buildEvidenceJSON([]specSnapshot{
		{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true},
		{Code: "ENNEAGRAM_TYPE", Weight: 50, InOverview: false},
	})
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "ENNEAGRAM_TYPE", "ENNEAGRAM", 90, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	// 参考维不进聚合：总览只剩 AI_INSTRUCTION 自身 = 80。
	requireFloat(t, "总览分", *res.Overview, 80)
	for _, w := range res.Included["ENNEAGRAM"] {
		if w.Code == "ENNEAGRAM_TYPE" {
			t.Errorf("参考维不应进 Included: %v", res.Included["ENNEAGRAM"])
		}
	}
	for _, code := range res.Excluded["ENNEAGRAM"] {
		if code == "ENNEAGRAM_TYPE" {
			t.Errorf("参考维不应进 Excluded: %v", res.Excluded["ENNEAGRAM"])
		}
	}
	// ENNEAGRAM 模块全为参考维：无参与维，模块分 nil。
	if got := res.ModuleScores["ENNEAGRAM"]; got != nil {
		t.Errorf("ENNEAGRAM 模块分 = %v, want nil（无参与维）", *got)
	}
}

// TestAggregatePerf 锚点：20 维评分行 → 耗时 < 100ms（specs §3.1）。
func TestAggregatePerf(t *testing.T) {
	p := testPeriod()
	snaps := make([]specSnapshot, 20)
	for i := range snaps {
		snaps[i] = specSnapshot{Code: fmt.Sprintf("AI_DIM_%02d", i), Weight: 5, InOverview: true}
	}
	ev := buildEvidenceJSON(snaps)
	rows := make([]domain.DimensionScore, 20)
	for i := range rows {
		rows[i] = scoreRow(p, snaps[i].Code, "AI_USAGE", 50+(i%51), false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev)
	}
	scores := &fakeScoreRepo{existing: rows}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	start := time.Now()
	res, err := s.Aggregate(context.Background(), "张三", p)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if res.Overview == nil {
		t.Fatal("20 维参与 Overview 应出分")
	}
	if elapsed >= 100*time.Millisecond {
		t.Errorf("聚合耗时 %v, want < 100ms", elapsed)
	}
}

// ---- 补充边界与异常 ----

// TestAggregateScoreReadFail 补充：读评分行失败 → wrap ErrScoreRead 上抛。
func TestAggregateScoreReadFail(t *testing.T) {
	scores := &fakeScoreRepo{listErr: errors.New("db down")}
	s := New(scores, &fakeAggRepo{})

	_, err := s.Aggregate(context.Background(), "张三", testPeriod())
	if !errors.Is(err, ErrScoreRead) {
		t.Fatalf("err = %v, want wrap ErrScoreRead", err)
	}
}

// TestAggregateStoreWriteFail 补充：聚合落库失败 → wrap ErrStoreWrite 上抛。
func TestAggregateStoreWriteFail(t *testing.T) {
	scores := &fakeScoreRepo{existing: usageRows(testPeriod())}
	agg := &fakeAggRepo{err: errors.New("db down")}
	s := New(scores, agg)

	_, err := s.Aggregate(context.Background(), "张三", testPeriod())
	if !errors.Is(err, ErrStoreWrite) {
		t.Fatalf("err = %v, want wrap ErrStoreWrite", err)
	}
}

// TestAggregateEmptyRows 补充：零评分行 → 无报错、Overview nil、仍落 overview 行。
func TestAggregateEmptyRows(t *testing.T) {
	scores := &fakeScoreRepo{}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	if res.Overview != nil {
		t.Errorf("Overview = %v, want nil", *res.Overview)
	}
	if len(res.ModuleScores) != 0 {
		t.Errorf("ModuleScores = %v, want 空", res.ModuleScores)
	}
	if agg.upsertCalls != 1 {
		t.Fatalf("空集也须落 overview 行, UpsertAll 调用 %d 次, want 1", agg.upsertCalls)
	}
	if len(agg.upserts[0].rows) != 1 || agg.upserts[0].rows[0].Module != domain.ModuleOverview {
		t.Fatalf("空集应只落 overview 行, got %+v", agg.upserts[0].rows)
	}
}

// TestAggregateBadEvidenceRow 补充：evidence_json 非法行按剔除处理进 Excluded，
// 不阻断聚合（防御坏行）。
func TestAggregateBadEvidenceRow(t *testing.T) {
	p := testPeriod()
	ev := buildEvidenceJSON(threeUsageSpecs())
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "AI_VALUE", "AI_USAGE", 60, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, "not-json"),
		scoreRow(p, "AI_REVIEW", "AI_USAGE", 40, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	// 坏行按剔除：(80×50+40×20)/70 = 68.6。
	requireFloat(t, "AI_USAGE 模块分", *res.ModuleScores["AI_USAGE"], 68.6)
	found := false
	for _, code := range res.Excluded["AI_USAGE"] {
		if code == "AI_VALUE" {
			found = true
		}
	}
	if !found {
		t.Errorf("Excluded[AI_USAGE] = %v, want 含坏行维度 AI_VALUE", res.Excluded["AI_USAGE"])
	}
}

// TestAggregateMissingSpecEntry 补充：evidence_json 合法但口径摘要缺本维度条目 →
// 该行按剔除处理（无权重不可参与）。
func TestAggregateMissingSpecEntry(t *testing.T) {
	p := testPeriod()
	// 摘要只含 AI_INSTRUCTION，AI_VALUE 行缺条目。
	ev := buildEvidenceJSON([]specSnapshot{{Code: "AI_INSTRUCTION", Weight: 50, InOverview: true}})
	scores := &fakeScoreRepo{existing: []domain.DimensionScore{
		scoreRow(p, "AI_INSTRUCTION", "AI_USAGE", 80, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
		scoreRow(p, "AI_VALUE", "AI_USAGE", 60, false, domain.ScoreSourceConversation, domain.ScoreStatusSuccess, ev),
	}}
	agg := &fakeAggRepo{}
	s := New(scores, agg)

	res := runAggregate(t, s)
	requireFloat(t, "总览分", *res.Overview, 80)
	found := false
	for _, code := range res.Excluded["AI_USAGE"] {
		if code == "AI_VALUE" {
			found = true
		}
	}
	if !found {
		t.Errorf("缺条目维度应进 Excluded: %v", res.Excluded["AI_USAGE"])
	}
}

// TestAggregatePeriodExactRead 补充：读数按双界精确匹配，窄窗口行不混入。
func TestAggregatePeriodExactRead(t *testing.T) {
	p := testPeriod()
	scores := &fakeScoreRepo{}
	s := New(scores, &fakeAggRepo{})

	_, err := s.Aggregate(context.Background(), "张三", p)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if scores.listArg.token != "张三" || scores.listArg.start != p.Start || scores.listArg.end != p.End {
		t.Errorf("读数入参 = %v, want %s/%d/%d", scores.listArg, "张三", p.Start, p.End)
	}
}

// TestRoundHalfUp 补充：roundHalfUp 的进位边界。
func TestRoundHalfUp(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{68.57142857142857, 68.6},
		{70.25, 70.3},
		{70.15, 70.2},
		{70.04, 70},
		{80, 80},
		{0, 0},
		{99.949, 99.9},
		{99.95, 100},
	}
	for _, c := range cases {
		requireFloat(t, fmt.Sprintf("roundHalfUp(%v)", c.in), roundHalfUp(c.in), c.want)
	}
}
