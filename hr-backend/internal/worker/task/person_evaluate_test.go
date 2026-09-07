package task

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/evaluator"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// person_evaluate_test.go 契约测试：单人评估 Asynq 任务 handler 与 NewMux 注册
//（03 §3.1-3.3 锚定）。evaluator 契约签名收 *evaluator.Evaluator，无法函数注入，
// 但坏 payload 丢弃分支在调用组件之前返回，传 nil 即可驱动（session_extract 同款）。

// evalCalls 记录 fake 评估链路收到的调用（token/period/sessions 探针）。
type evalCalls struct {
	calls    int
	token    string
	period   activity.Period
	sessions []conversationlog.SessionSummary
}

// runHandler 以 TypePersonEvaluate 驱动 handler。
func runHandler(h asynq.HandlerFunc, payload string) error {
	return h(context.Background(), asynq.NewTask(TypePersonEvaluate, []byte(payload)))
}

// evalFixture 单人评估 handler 测试注入件：fake 全链（真实 Evaluator 组合
// 真实 Activity/Scorer + fake 依赖，evaluate_person_test 同构裁剪），listFetcher
// 兼作 sessions 探针（EvaluatePerson sessions 空时列表由 StatPersonByKey 拉取，
// 拉到的窗口内列表注入 Evaluate，故窗口断言锚定 fetcher 收到的 period）。
type evalFixture struct {
	list     *evalListFetcher
	features *evalFeatureRepo
	specs    *evalSpecReader
	th       *evalThresholds
	scores   *evalScoreRepo
	params   *evalSysParams
	actRepo  *evalActRepo
	aggRepo  *evalAggRepo
	provider *evalModelProvider
	llm      *evalLLM
}

// newEvalFixture 空集 fixture：维度 1 条、阈值 (20,5)、无档案无列表。
func newEvalFixture() *evalFixture {
	return &evalFixture{
		list:     &evalListFetcher{},
		features: &evalFeatureRepo{},
		specs:    &evalSpecReader{specs: []evaluator.DimensionSpec{{Code: "AI_D1", Name: "维度一", Module: "AI_USAGE", AnchorText: "a", Weight: 100, InOverview: true}}},
		th:       &evalThresholds{active: 20, lowFreq: 5},
		scores:   &evalScoreRepo{},
		params:   &evalSysParams{},
		actRepo:  &evalActRepo{},
		aggRepo:  &evalAggRepo{},
		provider: &evalModelProvider{},
		llm:      &evalLLM{},
	}
}

// evaluator 组装被测 Evaluator（真实 Activity/Scorer 组合 fake 依赖）。
func (f *evalFixture) evaluator() *evaluator.Evaluator {
	secrets := activity.SecretProvider(func(ctx context.Context) (string, error) { return "s", nil })
	act := activity.New(f.list, f.features, f.th, f.actRepo, secrets)
	sc := scorer.New(f.scores, f.aggRepo)
	return evaluator.New(f.llm, f.provider, f.features, f.specs, f.th, f.scores, f.params,
		act, sc)
}

// probe 组装探针 handler：fetcher 记录 token/period（零档案走 Skipped 终态，
// LLM 不被触达，断言聚焦 EvaluatePerson 的参数透传）。
func (f *evalFixture) probe() *evalCalls {
	c := &evalCalls{}
	f.list.probe = c
	return c
}

// ---- fake 依赖 ----

// evalListFetcher 列表拉取 fake：probe 非 nil 时记录调用并恒返回空列表。
type evalListFetcher struct {
	probe *evalCalls
}

func (f *evalListFetcher) ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error) {
	if f.probe != nil {
		f.probe.calls++
		f.probe.token = "<by-key>"
		f.probe.period = activity.Period{Start: req.StartTime, End: req.EndTime}
		f.probe.sessions = nil
	}
	return nil, 0, nil
}

// evalFeatureRepo 档案仓储 fake：恒空（零档案 → Skipped 终态 err=nil）。
type evalFeatureRepo struct{}

func (r *evalFeatureRepo) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	return nil, nil
}
func (r *evalFeatureRepo) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	return false, nil
}
func (r *evalFeatureRepo) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	return nil, nil
}

// evalSpecReader 维度口径 fake。
type evalSpecReader struct {
	specs []evaluator.DimensionSpec
}

func (r *evalSpecReader) ListEnabledConversationSpecs(ctx context.Context) ([]evaluator.DimensionSpec, error) {
	return r.specs, nil
}

// evalThresholds 阈值 fake。
type evalThresholds struct {
	active  int
	lowFreq int
}

func (r *evalThresholds) ActivityThresholds(ctx context.Context) (int, int, error) {
	return r.active, r.lowFreq, nil
}

// evalScoreRepo 评分仓储 fake：ListByPersonPeriodExact 错误可注入（errScore
// 非 nil 时恒返回，驱动 EvaluatePerson 上抛路径）。
type evalScoreRepo struct {
	errScore error
}

func (r *evalScoreRepo) ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error) {
	if r.errScore != nil {
		return nil, r.errScore
	}
	return nil, nil
}
func (r *evalScoreRepo) DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error) {
	return 0, nil
}
func (r *evalScoreRepo) SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error {
	return nil
}

// evalSysParams 参数读取 fake：空集（脱敏走 Redact 出厂回退）。
type evalSysParams struct{}

func (r *evalSysParams) ReadStringArray(key string) ([]string, error) { return nil, nil }
func (r *evalSysParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// evalActRepo 活跃度仓储 fake。
type evalActRepo struct{}

func (r *evalActRepo) Upsert(ctx context.Context, rec *domain.ActivityStat) error { return nil }

// evalAggRepo 聚合仓储 fake。
type evalAggRepo struct{}

func (r *evalAggRepo) UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error {
	return nil
}

// evalModelProvider 启用模型 fake：恒错误（零档案路径不解析模型）。
type evalModelProvider struct{}

func (p *evalModelProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	return llm.ModelConfig{}, errors.New("no model")
}

// evalLLM LLM fake：零档案路径不触达，触达即测试失败探针。
type evalLLM struct{}

func (c *evalLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	return nil, errors.New("unexpected llm call")
}

// 编译期接口断言：fake 集合满足各消费窄面。
var (
	_ activity.SessionListFetcher         = (*evalListFetcher)(nil)
	_ repository.SessionFeatureRepository = (*evalFeatureRepo)(nil)
	_ evaluator.DimensionSpecReader       = (*evalSpecReader)(nil)
	_ activity.ThresholdReader            = (*evalThresholds)(nil)
	_ repository.DimensionScoreRepository = (*evalScoreRepo)(nil)
	_ repository.ActivityStatRepository   = (*evalActRepo)(nil)
	_ repository.AggregateScoreRepository = (*evalAggRepo)(nil)
	_ repository.SystemParamReader        = (*evalSysParams)(nil)
	_ llm.EnabledModelProvider            = (*evalModelProvider)(nil)
	_ llm.Client                          = (*evalLLM)(nil)
)

// TestPersonEvaluatePayloadInvalid 核心锚点：坏 JSON / 空 token_name /
// period_end ≤ period_start 三种 payload → 返回 nil 丢弃且 evaluator 零调用
// （03 §3.2，BR1/BR2）。
func TestPersonEvaluatePayloadInvalid(t *testing.T) {
	h := NewPersonEvaluateHandler(nil)
	cases := []struct {
		name    string
		payload string
	}{
		{"非法JSON", "{not json"},
		{"空token_name", `{"token_name":"","period_start":100,"period_end":200}`},
		{"end等于start", `{"token_name":"李雪涛","period_start":200,"period_end":200}`},
		{"end小于start", `{"token_name":"李雪涛","period_start":300,"period_end":200}`},
		{"空JSON对象", `{}`},
	}
	for _, tc := range cases {
		if err := runHandler(h, tc.payload); err != nil {
			t.Errorf("%s: 确定性坏 payload 应丢弃返回 nil, got %v", tc.name, err)
		}
	}
}

// TestPersonEvaluateNormalDispatch 核心锚点：合法 payload 透传 EvaluatePerson，
// sessions 传 nil（03 §3.3，BR3）。探针锚定 fetcher 收到的拉取窗口 = payload
// period（sessions 空路径由 StatPersonByKey 内部拉取，等价断言参数透传正确）。
func TestPersonEvaluateNormalDispatch(t *testing.T) {
	f := newEvalFixture()
	c := f.probe()
	h := NewPersonEvaluateHandler(f.evaluator())
	if err := runHandler(h, `{"token_name":"李雪涛","period_start":1756560000,"period_end":1757164800}`); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	if c.calls != 1 {
		t.Fatalf("EvaluatePerson 触发列表拉取次数 = %d, want 1", c.calls)
	}
	if c.period != (activity.Period{Start: 1756560000, End: 1757164800}) {
		t.Errorf("period = %+v, want {1756560000 1757164800}", c.period)
	}
	if c.sessions != nil {
		t.Errorf("sessions 入参应 nil 透传（内部拉取路径）")
	}
}

// TestPersonEvaluateErrorPropagation 核心锚点：EvaluatePerson err 非 nil → 透传（BR3）。
// errScore 注入使 Evaluate 幂等预检读失败 → ErrScoreRead 上抛。
func TestPersonEvaluateErrorPropagation(t *testing.T) {
	f := newEvalFixture()
	f.probe()
	f.scores.errScore = errors.New("db down")
	h := NewPersonEvaluateHandler(f.evaluator())
	if err := runHandler(h, `{"token_name":"李雪涛","period_start":100,"period_end":200}`); err == nil {
		t.Fatal("err 非 nil 应透传交 Asynq 重试")
	}
}

// TestPersonEvaluateTimeoutConst 核心锚点：超时常量精确 1050s（BR4）。
func TestPersonEvaluateTimeoutConst(t *testing.T) {
	if personEvaluateTimeout != 1050*time.Second {
		t.Errorf("personEvaluateTimeout = %v, want 1050s", personEvaluateTimeout)
	}
}

// TestNewMuxRegistersPersonEvaluate 核心锚点：mux 已注册 TypePersonEvaluate。
func TestNewMuxRegistersPersonEvaluate(t *testing.T) {
	f := newEvalFixture()
	c := f.probe()
	mux := NewMux(nil, NewPersonEvaluateHandler(f.evaluator()))
	err := mux.ProcessTask(context.Background(),
		asynq.NewTask(TypePersonEvaluate, []byte(`{"token_name":"赵六","period_start":100,"period_end":200}`)))
	if err != nil {
		t.Fatalf("mux 应路由到 person-evaluate handler: %v", err)
	}
	if c.calls != 1 {
		t.Errorf("handler 触发列表拉取次数 = %d, want 1", c.calls)
	}
	if c.period != (activity.Period{Start: 100, End: 200}) {
		t.Errorf("period = %+v, want {100 200}", c.period)
	}
}

// TestNewMuxPersonEvaluateTimeout 核心锚点：注册 handler 挂 1050s 超时
// （TestNewMuxSessionExtractTimeout 同款 deadline 行为断言，BR4）。
func TestNewMuxPersonEvaluateTimeout(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(nil, inner)
	h, pattern := mux.Handler(asynq.NewTask(TypePersonEvaluate, []byte(`{}`)))
	if pattern != TypePersonEvaluate {
		t.Fatalf("pattern = %q, want %q", pattern, TypePersonEvaluate)
	}
	before := time.Now()
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypePersonEvaluate, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	after := time.Now()
	if !hasDeadline {
		t.Fatal("注册的任务应携带任务级超时：handler ctx 应有 deadline")
	}
	lo, hi := before.Add(personEvaluateTimeout-time.Second), after.Add(personEvaluateTimeout+time.Second)
	if gotDeadline.Before(lo) || gotDeadline.After(hi) {
		t.Errorf("deadline = %v, want [%v, %v]", gotDeadline, lo, hi)
	}
}

// TestPersonEvaluatePayloadJSON 边界补充：payload json tag 与 03 §3.2 schema 对齐。
func TestPersonEvaluatePayloadJSON(t *testing.T) {
	raw := `{"token_name":"李雪涛","period_start":1756560000,"period_end":1757164800}`
	var p PersonEvaluateTaskPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.TokenName != "李雪涛" || p.PeriodStart != 1756560000 || p.PeriodEnd != 1757164800 {
		t.Errorf("payload = %+v", p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != raw {
		t.Errorf("序列化 = %s, want %s", out, raw)
	}
}

// TestNewMuxSessionExtractStillRegistered 回归锚点：NewMux 二参化后
// session-extract 路由仍可达。
func TestNewMuxSessionExtractStillRegistered(t *testing.T) {
	mux := NewMux(nil, nil)
	if _, pattern := mux.Handler(asynq.NewTask(TypeSessionExtract, []byte(`{}`))); pattern != TypeSessionExtract {
		t.Errorf("session-extract 路由丢失: pattern = %q", pattern)
	}
}
