package evaluator

// evaluate_person_test.go 契约测试：EvaluatePerson 原子入口组合编排
//（specs §2.4 能力6 / §5.1 组合用例 / §5.2 集成用例锚定）。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// ---- 组合链 fake（activity 仓储 / 列表 fetcher / scorer 仓储）----

// fakeActivityRepo 活跃度行仓储 fake：内存承载 upsert 快照（同键去重）。
type fakeActivityRepo struct {
	mu       sync.Mutex
	upserts  []*domain.ActivityStat
	upserted map[string]bool // (token, period_start) 键
	err      error
}

func newFakeActivityRepo() *fakeActivityRepo {
	return &fakeActivityRepo{upserted: map[string]bool{}}
}

func (f *fakeActivityRepo) Upsert(ctx context.Context, rec *domain.ActivityStat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	key := fmt.Sprintf("%s@%d", rec.TokenName, rec.PeriodStartAt.Unix())
	if !f.upserted[key] {
		f.upserted[key] = true
		f.upserts = append(f.upserts, rec)
	}
	return nil
}

var _ repository.ActivityStatRepository = (*fakeActivityRepo)(nil)

// fakeListFetcher 会话列表拉取 fake：可配置行集与错误，探针记录调用与窗口。
type fakeListFetcher struct {
	mu      sync.Mutex
	rows    []conversationlog.SessionSummary
	err     error
	calls   int
	lastWin struct{ start, end int64 }
}

func (f *fakeListFetcher) ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastWin.start = req.StartTime
	f.lastWin.end = req.EndTime
	if f.err != nil {
		return nil, 0, f.err
	}
	if req.Page > 1 {
		return nil, int64(len(f.rows)), nil
	}
	return f.rows, int64(len(f.rows)), nil
}

var _ activity.SessionListFetcher = (*fakeListFetcher)(nil)

// fakeAggRepo 聚合行仓储 fake：记录每次 UpsertAll 入参快照。
type fakeAggRepo struct {
	mu          sync.Mutex
	upserts     []aggUpsertRecord
	upsertCalls int
	err         error
}

type aggUpsertRecord struct {
	token string
	start int64
	end   int64
	rows  []domain.AggregateScore
}

func (f *fakeAggRepo) UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	if f.err != nil {
		return f.err
	}
	snap := make([]domain.AggregateScore, len(rows))
	copy(snap, rows)
	f.upserts = append(f.upserts, aggUpsertRecord{token: tokenName, start: start, end: end, rows: snap})
	return nil
}

var _ repository.AggregateScoreRepository = (*fakeAggRepo)(nil)

// ---- 调用序探针包装 ----

// probeActivity 包装真实 Activity，在窄面方法入口记录调用名（组合入口顺序同步
// 执行，调用序唯一确定）。实现组合窄面接口，探针与适配一体。
type probeActivity struct {
	act      ActivityStatComponent
	recorder *callRecorder
}

func (p *probeActivity) StatPersonByKeyWithSessions(ctx context.Context, tokenName string, period activity.Period) (*activity.ActivityStat, []conversationlog.SessionSummary, []activity.ProfileDigest, error) {
	p.recorder.record("StatPersonByKey")
	return p.act.StatPersonByKeyWithSessions(ctx, tokenName, period)
}

func (p *probeActivity) StatPerson(ctx context.Context, sessions []conversationlog.SessionSummary, tokenName string, period activity.Period) (*activity.ActivityStat, []activity.ProfileDigest, error) {
	p.recorder.record("StatPerson")
	return p.act.StatPerson(ctx, sessions, tokenName, period)
}

// callRecorder 顺序记录组件调用名。
type callRecorder struct {
	mu    sync.Mutex
	names []string
}

func (c *callRecorder) record(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = append(c.names, name)
}

func (c *callRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.names))
	copy(out, c.names)
	return out
}

// ---- 组合 fixture ----

// personFixture 聚合一次 EvaluatePerson 测试的全部注入件。
type personFixture struct {
	llm      *fakeLLM
	provider *fakeModelProvider
	features *fakeFeatureRepo
	specs    *fakeSpecReader
	th       *fakeThresholds
	scores   *fakeScoreRepo
	params   *fakeSysParams
	list     *fakeListFetcher
	actRepo  *fakeActivityRepo
	aggRepo  *fakeAggRepo
	recorder *callRecorder
	ev       *Evaluator
}

// assemble 真实组件装配：真实 Activity（fake 依赖）+ 真实 Scorer（fake 仓储）。
func (f *personFixture) assemble() (*activity.Activity, *scorer.Scorer) {
	realAct := activity.New(f.list, f.features, f.th, f.actRepo, func(ctx context.Context) (string, error) { return "secret", nil })
	realSc := scorer.New(f.scores, f.aggRepo)
	return realAct, realSc
}

// newPersonFixture 标准组合注入：2 条 success 档案 + 3 维口径 + 默认阈值。
func newPersonFixture(t *testing.T) *personFixture {
	t.Helper()
	p := testPeriod()
	f := &personFixture{
		llm:      &fakeLLM{},
		provider: &fakeModelProvider{cfg: llm.ModelConfig{Provider: "openai", ModelID: "gpt-test", BaseURL: "http://x", APIKey: "k"}},
		features: &fakeFeatureRepo{rows: []domain.SessionFeature{
			{SessionKey: "sess-a", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("会话A摘要")},
			{SessionKey: "sess-b", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+300, 0).UTC(), LastTurnAt: time.Unix(p.Start+400, 0).UTC(), ProfileJSON: successProfileJSON("会话B摘要")},
		}},
		specs:    &fakeSpecReader{specs: threeSpecs()},
		th:       &fakeThresholds{},
		scores:   &fakeScoreRepo{},
		params:   &fakeSysParams{},
		list:     &fakeListFetcher{},
		actRepo:  newFakeActivityRepo(),
		aggRepo:  &fakeAggRepo{},
		recorder: &callRecorder{},
	}
	f.ev = f.newEvaluator()
	return f
}

// newEvaluator 用真实组件构造 Evaluator（act 直接挂真实 Activity）。
func (f *personFixture) newEvaluator() *Evaluator {
	act, sc := f.assemble()
	return New(f.llm, f.provider, f.features, f.specs, f.th, f.scores, f.params, act, sc)
}

// newProbingEvaluator 用探针包装组合窄面构造 Evaluator（调用序断言用）。
func (f *personFixture) newProbingEvaluator() *Evaluator {
	act, sc := f.assemble()
	probed := &probeActivity{act: act, recorder: f.recorder}
	return New(f.llm, f.provider, f.features, f.specs, f.th, f.scores, f.params, probed, sc)
}

// sessionsFor 张三窗口内两条会话列表。
func sessionsFor(token string) []conversationlog.SessionSummary {
	p := testPeriod()
	return []conversationlog.SessionSummary{
		{SessionKey: "sess-a", TokenName: token, TurnCount: 3, FirstTurnTime: p.Start + 100, LastTurnTime: p.Start + 200},
		{SessionKey: "sess-b", TokenName: token, TurnCount: 2, FirstTurnTime: p.Start + 300, LastTurnTime: p.Start + 400},
	}
}

// equalNames 调用序比较。
func equalNames(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---- specs §5.1 组合入口用例 ----

// TestEvaluatePersonSequence 锚点：fake 全链路（探针记录调用序）→ 调用序为
// [StatPerson, Evaluate, Aggregate]，三表 fake 各收到写入（活跃度/评分/聚合非空）。
func TestEvaluatePersonSequence(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.responses = []string{goodScoreJSON()}
	f.list.rows = sessionsFor("张三")
	f.ev = f.newProbingEvaluator()

	res, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), nil)
	if err != nil {
		t.Fatalf("EvaluatePerson: %v", err)
	}
	got := f.recorder.snapshot()
	want := []string{"StatPersonByKey"}
	if !equalNames(got, want) {
		t.Errorf("activity 调用序 = %v, want %v", got, want)
	}
	if f.llm.calls != 1 {
		t.Errorf("Evaluate 段 LLM 调用 %d 次, want 1", f.llm.calls)
	}
	// 三表写入齐备。
	if len(f.actRepo.upserts) != 1 {
		t.Fatalf("活跃度行 %d, want 1", len(f.actRepo.upserts))
	}
	if res.Activity == nil || res.Activity.SessionCount != 2 {
		t.Errorf("res.Activity = %+v, want SessionCount=2", res.Activity)
	}
	if len(f.scores.saved) != 3 || len(res.Scores) != 3 {
		t.Errorf("评分行 saved=%d res=%d, want 3/3", len(f.scores.saved), len(res.Scores))
	}
	if f.aggRepo.upsertCalls != 1 || res.Aggregate == nil {
		t.Fatalf("聚合行 upsert=%d res=%v, want 1/非nil", f.aggRepo.upsertCalls, res.Aggregate)
	}
	if res.Skipped || res.Reused {
		t.Errorf("Skipped/Reused 应为 false")
	}
}

// TestEvaluatePersonSessionsInjected 锚点：传入 sessions → act.StatPerson 被调
// （StatPersonByKey 零调用）、fake 列表 fetcher 零调用。
func TestEvaluatePersonSessionsInjected(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.responses = []string{goodScoreJSON()}
	f.ev = f.newProbingEvaluator()

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err != nil {
		t.Fatalf("EvaluatePerson: %v", err)
	}
	got := f.recorder.snapshot()
	if !equalNames(got, []string{"StatPerson"}) {
		t.Fatalf("调用序 = %v, want 仅 [StatPerson]（StatPersonByKey 不触发）", got)
	}
	if f.list.calls != 0 {
		t.Errorf("列表 fetcher 调用 %d 次, want 0", f.list.calls)
	}
	if len(f.actRepo.upserts) != 1 || f.actRepo.upserts[0].SessionCount != 2 {
		t.Errorf("活跃度行 = %+v, want SessionCount=2", f.actRepo.upserts)
	}
}

// TestEvaluatePersonSessionsEmptyFetch 锚点：sessions=nil → StatPersonByKey 路径
// 拉取列表后统计。
func TestEvaluatePersonSessionsEmptyFetch(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.responses = []string{goodScoreJSON()}
	f.list.rows = sessionsFor("张三")
	f.ev = f.newProbingEvaluator()

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), nil)
	if err != nil {
		t.Fatalf("EvaluatePerson: %v", err)
	}
	got := f.recorder.snapshot()
	if !equalNames(got, []string{"StatPersonByKey"}) {
		t.Fatalf("调用序 = %v, want [StatPersonByKey]（内部 StatPerson 直调不经探针）", got)
	}
	if f.list.calls != 1 {
		t.Errorf("列表 fetcher 调用 %d 次, want 1", f.list.calls)
	}
	if f.list.lastWin.start != testPeriod().Start || f.list.lastWin.end != testPeriod().End {
		t.Errorf("拉取窗口 = %d~%d, want 周期窗口", f.list.lastWin.start, f.list.lastWin.end)
	}
	if len(f.actRepo.upserts) != 1 || f.actRepo.upserts[0].SessionCount != 2 {
		t.Errorf("活跃度行 = %+v, want 列表过滤后 SessionCount=2", f.actRepo.upserts)
	}
}

// TestEvaluatePersonDedupListBeforeSignature 补充：列表含重复 session_key（跨页
// 重复）→ 透出列表先去重再进签名识别，活跃度行与评分侧签名口径一致（specs
// §2.4 能力4 两处调用共用同一集合口径）。
func TestEvaluatePersonDedupListBeforeSignature(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.responses = []string{goodScoreJSON()}
	dup := sessionsFor("张三")
	dup = append(dup, dup[0]) // 同键重复行
	f.list.rows = dup
	f.ev = f.newProbingEvaluator()

	res, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), nil)
	if err != nil {
		t.Fatalf("EvaluatePerson: %v", err)
	}
	if len(f.actRepo.upserts) != 1 || f.actRepo.upserts[0].SessionCount != 2 {
		t.Errorf("活跃度行 SessionCount = %d, want 2（重复键已去重）", len(f.actRepo.upserts))
	}
	if res.Activity == nil || res.Activity.SessionCount != 2 {
		t.Errorf("res.Activity = %+v, want SessionCount=2", res.Activity)
	}
	if res.Skipped || res.Reused {
		t.Errorf("正常档案不应跳过: Skipped=%v", res.Skipped)
	}
}

// TestEvaluatePersonLLMFailNotBlockAggregate 锚点：fake LLM 持续失败 → 评分行
// failed、聚合照常执行（Aggregate 被调）、活跃度行照常、err=nil。
func TestEvaluatePersonLLMFailNotBlockAggregate(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.responses = []string{"bad json", "bad json"}
	f.ev = f.newProbingEvaluator()

	res, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err != nil {
		t.Fatalf("LLM 失败是降级业务态, got err=%v", err)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("Skipped/Reused 应为 false")
	}
	for _, r := range f.scores.saved {
		if r.Status != domain.ScoreStatusFailed {
			t.Errorf("维度 %s status=%s, want failed", r.DimensionCode, r.Status)
		}
	}
	if f.aggRepo.upsertCalls != 1 {
		t.Fatalf("聚合应照常执行, upsert=%d 次", f.aggRepo.upsertCalls)
	}
	// 全 failed 维度被剔除：总览行落库且 OverviewScore nil。
	for _, rec := range f.aggRepo.upserts {
		for _, r := range rec.rows {
			if r.Module == domain.ModuleOverview && r.OverviewScore != nil {
				t.Errorf("总览分 = %v, want nil（failed 维剔除）", *r.OverviewScore)
			}
		}
	}
	if len(f.actRepo.upserts) != 1 {
		t.Errorf("活跃度行 %d, want 1（不受 LLM 失败影响）", len(f.actRepo.upserts))
	}
}

// TestEvaluatePersonReusedPath 锚点：预置全 success 行 → Reused=true、LLM 零调用、
// 活跃度与聚合照常重算。
func TestEvaluatePersonReusedPath(t *testing.T) {
	f := newPersonFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_REVIEW", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
	}
	f.llm.responses = []string{goodScoreJSON()}
	f.ev = f.newProbingEvaluator()

	res, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err != nil {
		t.Fatalf("EvaluatePerson: %v", err)
	}
	if !res.Reused {
		t.Fatalf("Reused 应为 true")
	}
	if f.llm.calls != 0 {
		t.Errorf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if len(f.actRepo.upserts) != 1 {
		t.Errorf("活跃度应照常重算, got %d 行", len(f.actRepo.upserts))
	}
	if f.aggRepo.upsertCalls != 1 {
		t.Errorf("聚合应照常重算, got %d 次 upsert", f.aggRepo.upsertCalls)
	}
}

// TestEvaluatePersonInfrastructureError 锚点：fake 活跃度落库 error → err 非 nil
// 透传、Evaluate 零调用。
func TestEvaluatePersonInfrastructureError(t *testing.T) {
	f := newPersonFixture(t)
	f.actRepo.err = errors.New("db down")
	f.llm.responses = []string{goodScoreJSON()}
	f.ev = f.newProbingEvaluator()

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err == nil {
		t.Fatal("活跃度落库失败应透传 err")
	}
	if f.llm.calls != 0 {
		t.Errorf("Evaluate 段应零调用, LLM = %d 次", f.llm.calls)
	}
	if f.aggRepo.upsertCalls != 0 {
		t.Errorf("聚合应零执行, got %d 次", f.aggRepo.upsertCalls)
	}
}

// ---- specs §5.2 集成形态 ----

// TestEvaluatePersonBatchLoop 锚点（周期批量形态）：预置多人数据（含 1 人 fake
// LLM 持续失败），模拟 T6 主循环逐人调 EvaluatePerson → 各人三表行独立落库，
// 失败者评分行 failed 且他人行不受影响。
func TestEvaluatePersonBatchLoop(t *testing.T) {
	f := newPersonFixture(t)
	p := testPeriod()
	addRows := []domain.SessionFeature{
		{SessionKey: "l4-1", TokenName: "李四", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("李四摘要")},
		{SessionKey: "w5-1", TokenName: "王五", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("王五摘要")},
	}
	f.features.rows = append(f.features.rows, addRows...)

	byPerson := map[string][]conversationlog.SessionSummary{
		"张三": sessionsFor("张三"),
		"李四": {{SessionKey: "l4-1", TokenName: "李四", TurnCount: 2, FirstTurnTime: p.Start + 100, LastTurnTime: p.Start + 200}},
		"王五": {{SessionKey: "w5-1", TokenName: "王五", TurnCount: 4, FirstTurnTime: p.Start + 100, LastTurnTime: p.Start + 200}},
	}

	for _, token := range []string{"张三", "李四", "王五"} {
		if token == "李四" {
			f.llm.responses = []string{"bad", "bad"} // 李四 LLM 持续失败
		} else {
			f.llm.responses = []string{goodScoreJSON()}
		}
		res, err := f.ev.EvaluatePerson(context.Background(), token, p, byPerson[token])
		if err != nil {
			t.Fatalf("%s EvaluatePerson: %v", token, err)
		}
		if token == "李四" && (res.Skipped || res.Reused) {
			t.Fatalf("李四应走 failed 降级, Skipped=%v Reused=%v", res.Skipped, res.Reused)
		}
	}

	// 各人三表独立落库。
	if len(f.actRepo.upserts) != 3 {
		t.Fatalf("活跃度行 %d, want 3（各人一行）", len(f.actRepo.upserts))
	}
	byToken := map[string][]domain.DimensionScore{}
	for _, r := range f.scores.saved {
		byToken[r.TokenName] = append(byToken[r.TokenName], r)
	}
	for token := range byPerson {
		if len(byToken[token]) != 3 {
			t.Errorf("%s 评分行 %d, want 3", token, len(byToken[token]))
		}
	}
	// 失败者评分行 failed、他人 success 不受影响。
	for _, r := range byToken["李四"] {
		if r.Status != domain.ScoreStatusFailed {
			t.Errorf("李四维度 %s status=%s, want failed", r.DimensionCode, r.Status)
		}
	}
	for _, r := range byToken["张三"] {
		if r.Status != domain.ScoreStatusSuccess {
			t.Errorf("张三维度 %s status=%s, want success（不受他人失败影响）", r.DimensionCode, r.Status)
		}
	}
	if f.aggRepo.upsertCalls != 3 {
		t.Errorf("聚合 upsert %d 次, want 3（各人一次）", f.aggRepo.upsertCalls)
	}
}

// TestNarrowPeriodCoexist 锚点（定向分析与周期并存）：对同人先跑周期窗口再跑窄
// 窗口定向分析 → 两周期行按 period_start 隔离并存、互不覆盖。
func TestNarrowPeriodCoexist(t *testing.T) {
	f := newPersonFixture(t)
	week := testPeriod()
	narrow := activity.Period{Start: week.Start + 2*24*3600, End: week.Start + 3*24*3600}
	// 窄窗口内补一条档案行（原 2 条落在窗口头部，窄窗口外）。
	f.features.rows = append(f.features.rows, domain.SessionFeature{
		SessionKey: "sess-narrow", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "claude_code",
		FirstTurnAt: time.Unix(narrow.Start+100, 0).UTC(), LastTurnAt: time.Unix(narrow.Start+200, 0).UTC(),
		ProfileJSON: successProfileJSON("窄窗摘要"),
	})
	narrowSessions := []conversationlog.SessionSummary{
		{SessionKey: "sess-narrow", TokenName: "张三", TurnCount: 1, FirstTurnTime: narrow.Start + 100, LastTurnTime: narrow.Start + 200},
	}
	f.llm.responses = []string{goodScoreJSON()}

	// 第一轮：周期批量窗口。
	if _, err := f.ev.EvaluatePerson(context.Background(), "张三", week, sessionsFor("张三")); err != nil {
		t.Fatalf("周期窗口评估: %v", err)
	}
	weekScoreRows := 0
	for _, r := range f.scores.saved {
		if r.PeriodStartAt.Unix() == week.Start {
			weekScoreRows++
		}
	}

	// 第二轮：窄窗口定向分析。
	f.llm.responses = []string{goodScoreJSON()}
	if _, err := f.ev.EvaluatePerson(context.Background(), "张三", narrow, narrowSessions); err != nil {
		t.Fatalf("窄窗口评估: %v", err)
	}

	// 两周期评分行并存：周期行仍在（未被窄窗删除/覆盖）。
	weekRows, narrowRows := 0, 0
	for _, r := range f.scores.saved {
		switch r.PeriodStartAt.Unix() {
		case week.Start:
			weekRows++
		case narrow.Start:
			narrowRows++
		}
	}
	if weekRows != weekScoreRows || weekRows != 3 {
		t.Errorf("周期行 %d, want 3（不被窄窗覆盖）", weekRows)
	}
	if narrowRows != 3 {
		t.Errorf("窄窗行 %d, want 3", narrowRows)
	}
	// 活跃度两行并存（唯一索引 token+period_start）。
	if len(f.actRepo.upserts) != 2 {
		t.Fatalf("活跃度行 %d, want 2（两周期并存）", len(f.actRepo.upserts))
	}
	// 聚合两轮 upsert 且 period_start 不同。
	if f.aggRepo.upsertCalls != 2 {
		t.Fatalf("聚合 upsert %d 次, want 2", f.aggRepo.upsertCalls)
	}
	if f.aggRepo.upserts[0].start == f.aggRepo.upserts[1].start {
		t.Errorf("两轮聚合 period_start 相同, want 隔离")
	}
}

// ---- 补充边界与异常 ----

// TestEvaluatePersonEmptySessionsEmptyList 补充：sessions=nil 且拉回空列表 →
// 组合照常走完（活跃度 unused、评分跳过路径 Skipped、聚合照常）。
func TestEvaluatePersonEmptySessionsEmptyList(t *testing.T) {
	f := newPersonFixture(t)
	f.features.rows = nil
	f.ev = f.newProbingEvaluator()

	res, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), nil)
	if err != nil {
		t.Fatalf("空列表是业务态: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("零有效档案应 Skipped=true")
	}
	if f.llm.calls != 0 {
		t.Errorf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if res.Activity == nil || res.Activity.ActiveLevel != domain.ActiveLevelUnused {
		t.Errorf("活跃度 = %+v, want unused", res.Activity)
	}
	if f.aggRepo.upsertCalls != 1 {
		t.Errorf("跳过路径聚合照常, got %d 次", f.aggRepo.upsertCalls)
	}
}

// TestEvaluatePersonListFetchError 补充：列表拉取失败 → err 非 nil（基础设施
// 通道）、Evaluate 零调用、零落库。
func TestEvaluatePersonListFetchError(t *testing.T) {
	f := newPersonFixture(t)
	f.list.err = errors.New("upstream down")
	f.llm.responses = []string{goodScoreJSON()}
	f.ev = f.newProbingEvaluator()

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), nil)
	if err == nil {
		t.Fatal("列表拉取失败应上抛")
	}
	if f.llm.calls != 0 {
		t.Errorf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if len(f.scores.saved) != 0 || len(f.actRepo.upserts) != 0 || f.aggRepo.upsertCalls != 0 {
		t.Errorf("失败路径应零落库")
	}
}

// TestEvaluatePersonEvaluateErrorStopsBeforeAggregate 补充：Evaluate 段基础设施
// 错误（评分行读取失败）→ err 透传、聚合不执行。
func TestEvaluatePersonEvaluateErrorStopsBeforeAggregate(t *testing.T) {
	f := newPersonFixture(t)
	f.scores.listErr = errors.New("db down")
	f.llm.responses = []string{goodScoreJSON()}
	f.ev = f.newProbingEvaluator()

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err == nil {
		t.Fatal("Evaluate 段错误应透传")
	}
	if f.aggRepo.upsertCalls != 0 {
		t.Errorf("聚合应零执行, got %d 次", f.aggRepo.upsertCalls)
	}
}

// TestEvaluatePersonAggregateError 补充：聚合落库失败 → err 非 nil（基础设施
// 通道）、活跃度与评分行已落库（不回滚，幂等重跑收敛）。
func TestEvaluatePersonAggregateError(t *testing.T) {
	f := newPersonFixture(t)
	f.aggRepo.err = errors.New("db down")
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err == nil {
		t.Fatal("聚合落库失败应上抛")
	}
	if len(f.actRepo.upserts) != 1 || len(f.scores.saved) != 3 {
		t.Errorf("前序已落行不应回滚: activity=%d scores=%d", len(f.actRepo.upserts), len(f.scores.saved))
	}
}

// TestEvaluatePersonActivityStatFailZeroWrite 补充：活跃度段失败时后续零写入
// （执行序列①失败即止）。
func TestEvaluatePersonActivityStatFailZeroWrite(t *testing.T) {
	f := newPersonFixture(t)
	f.actRepo.err = errors.New("db down")
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.EvaluatePerson(context.Background(), "张三", testPeriod(), sessionsFor("张三"))
	if err == nil {
		t.Fatal("活跃度落库失败应上抛")
	}
	if len(f.scores.saved) != 0 || f.aggRepo.upsertCalls != 0 {
		t.Errorf("活跃度段失败后续零落库")
	}
}

// TestEvaluatePersonCtxCancel 补充：ctx 已取消 → err 非 nil（基础设施通道）。
func TestEvaluatePersonCtxCancel(t *testing.T) {
	f := newPersonFixture(t)
	f.llm.blocks = true
	f.ev = f.newProbingEvaluator()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.ev.EvaluatePerson(ctx, "张三", testPeriod(), sessionsFor("张三"))
	if err == nil {
		t.Fatal("ctx 取消应走基础设施 error 通道")
	}
}
