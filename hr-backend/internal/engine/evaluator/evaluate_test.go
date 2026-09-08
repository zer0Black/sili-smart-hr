package evaluator

// evaluate_test.go 契约测试：Evaluate 主流程与评分行落库（specs §2.4 能力1 / §5.1 逐条锚定）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// ---- fake 依赖（扩展 types_test.go 既有 fakeSpecReader/fakeThresholds/fakeSysParams、
// digest_test.go 既有 fakeFeatureRepo）----

// scriptedChunk 可编程多轮返回的 LLM fake：每轮返回一段回复（按 chunk 切分），
// 阻塞模式在 StreamChat 处挂起等 ctx 取消。
type fakeLLM struct {
	mu        sync.Mutex
	responses []string // 逐次调用依次消费，耗尽后重复末项
	calls     int
	prompts   []string // 每次调用的 user prompt 全文
	blocks    bool     // true 时 StreamChat 阻塞至 ctx 取消
	blockErr  error    // 阻塞模式的返回错误（默认 ctx.Err）
	lastReq   llm.ChatRequest
}

type fakeStream struct {
	segments []string
	pos      int
}

func (s *fakeStream) Recv() (llm.StreamChunk, error) {
	if s.pos >= len(s.segments) {
		return llm.StreamChunk{}, io.EOF
	}
	seg := s.segments[s.pos]
	s.pos++
	return llm.StreamChunk{Content: seg}, nil
}

func (s *fakeStream) Close() error { return nil }

func (s *fakeStream) Usage() (int, int) { return 0, 0 }

func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.mu.Lock()
	f.calls++
	f.lastReq = req
	prompt := ""
	for _, m := range req.Messages {
		prompt += m.Content + "\n"
	}
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	if f.blocks {
		<-ctx.Done()
		err := ctx.Err()
		if f.blockErr != nil {
			err = f.blockErr
		}
		return nil, err
	}
	f.mu.Lock()
	idx := f.calls - 1
	if idx >= len(f.responses) {
		idx = len(f.responses) - 1
	}
	resp := f.responses[idx]
	f.mu.Unlock()
	// 每 32 字符切一个 chunk，验证流式读全。
	var segs []string
	for i := 0; i < len(resp); i += 32 {
		end := i + 32
		if end > len(resp) {
			end = len(resp)
		}
		segs = append(segs, resp[i:end])
	}
	if len(segs) == 0 {
		segs = []string{""}
	}
	return &fakeStream{segments: segs}, nil
}

var _ llm.Client = (*fakeLLM)(nil)

// fakeModelProvider 排他启用模型解析 fake。
type fakeModelProvider struct {
	cfg llm.ModelConfig
	err error
}

func (f *fakeModelProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	if f.err != nil {
		return llm.ModelConfig{}, f.err
	}
	return f.cfg, nil
}

var _ llm.EnabledModelProvider = (*fakeModelProvider)(nil)

// fakeScoreRepo 评分行仓储 fake：内存承载 SaveAll 行，探针记录调用。
type fakeScoreRepo struct {
	mu          sync.Mutex
	existing    []domain.DimensionScore
	saved       []domain.DimensionScore
	saveErr     error
	listErr     error
	deleteErr   error
	listCalls   int
	deleteCalls int
	saveCalls   int
	lastListArg struct {
		token string
		start int64
		end   int64
	}
	lastDeleteArg struct {
		token string
		start int64
		end   int64
	}
}

func (f *fakeScoreRepo) ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.lastListArg.token = tokenName
	f.lastListArg.start = start
	f.lastListArg.end = end
	if f.listErr != nil {
		return nil, f.listErr
	}
	// 双界精确过滤，对齐真实仓储语义（同 start 不同 end 的行不返回）。
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.lastDeleteArg.token = tokenName
	f.lastDeleteArg.start = start
	f.lastDeleteArg.end = end
	if f.deleteErr != nil {
		return 0, f.deleteErr
	}
	kept := f.existing[:0]
	var n int64
	for _, r := range f.existing {
		// 与真实仓储同语义：failed 行与 skip_no_llm 标记 success 行均删。
		if r.Source == domain.ScoreSourceConversation &&
			(r.Status == domain.ScoreStatusFailed || r.ErrorCode == domain.ErrorCodeSkipNoLLM) {
			n++
			continue
		}
		kept = append(kept, r)
	}
	f.existing = kept
	return n, nil
}

func (f *fakeScoreRepo) SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	for i := range rows {
		rows[i].TokenName = tokenName
		rows[i].PeriodStartAt = time.Unix(start, 0).UTC()
		rows[i].PeriodEndAt = time.Unix(end, 0).UTC()
	}
	f.saved = append(f.saved, rows...)
	return nil
}

var _ repository.DimensionScoreRepository = (*fakeScoreRepo)(nil)

// ---- 测试辅助 ----

// evalFixture 聚合一次 Evaluate 测试的全部注入件。
type evalFixture struct {
	llm      *fakeLLM
	provider *fakeModelProvider
	features *fakeFeatureRepo
	specs    *fakeSpecReader
	th       *fakeThresholds
	scores   *fakeScoreRepo
	params   *fakeSysParams
	ev       *Evaluator
}

// threeSpecs 三维度口径（含 1 个空提示词维度由用例自行改写）。
func threeSpecs() []DimensionSpec {
	return []DimensionSpec{
		{Code: "AI_INSTRUCTION", Name: "指令能力", Module: "AI_USAGE", PromptText: "提示词一", AnchorText: "锚点一", Weight: 30, InOverview: true},
		{Code: "AI_VALUE", Name: "价值产出", Module: "AI_USAGE", PromptText: "提示词二", AnchorText: "锚点二", Weight: 40, InOverview: true},
		{Code: "AI_REVIEW", Name: "审查把关", Module: "AI_USAGE", PromptText: "提示词三", AnchorText: "锚点三", Weight: 30, InOverview: false},
	}
}

// successProfileJSON success 行档案 JSON（四块结构最小形态）。
func successProfileJSON(summary string) string {
	return `{"Stats":{"UserMsgCount":5,"ToolCounts":{"bash":2},"PasteCharCount":0,"InterruptCount":0,"TurnKindCounts":{},"SpecFingerprints":[],"CmdReuseHashes":[],"ContinuationHit":false},"Summary":"` + summary + `","Instruction":[],"Behavior":{"NarrativeAbsent":false}}`
}

// newFixture 标准三依赖注入：2 条 success 档案 + 3 维口径 + 默认阈值。
func newFixture(t *testing.T) *evalFixture {
	t.Helper()
	p := testPeriod()
	f := &evalFixture{
		llm:      &fakeLLM{},
		provider: &fakeModelProvider{cfg: llm.ModelConfig{Provider: "openai", ModelID: "gpt-test", BaseURL: "http://x", APIKey: "k"}},
		features: &fakeFeatureRepo{rows: []domain.SessionFeature{
			{SessionKey: "sess-a", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("会话A摘要")},
			{SessionKey: "sess-b", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+300, 0).UTC(), LastTurnAt: time.Unix(p.Start+400, 0).UTC(), ProfileJSON: successProfileJSON("会话B摘要")},
		}},
		specs:  &fakeSpecReader{specs: threeSpecs()},
		th:     &fakeThresholds{},
		scores: &fakeScoreRepo{},
		params: &fakeSysParams{},
	}
	f.ev = New(f.llm, f.provider, f.features, f.specs, f.th, f.scores, f.params, nil, nil)
	return f
}

// goodScoreJSON 合法 dimensions 输出：3 维全供（AI_REVIEW 落 insufficient）。
func goodScoreJSON() string {
	s72, s85 := 72, 85
	return `{"dimensions":[` +
		dimJSON("AI_INSTRUCTION", &s72, false, "指令表述清晰") + `,` +
		dimJSON("AI_VALUE", &s85, false, "产出信号充分") + `,` +
		dimJSON("AI_REVIEW", nil, true, "审查证据不足") + `]}`
}

// scoreRowByCode 落库行按维度编码取行。
func scoreRowByCode(t *testing.T, rows []domain.DimensionScore, code string) domain.DimensionScore {
	t.Helper()
	for _, r := range rows {
		if r.DimensionCode == code {
			return r
		}
	}
	t.Fatalf("落库行缺维度 %s: %+v", code, rows)
	return domain.DimensionScore{}
}

// evidence 解析 EvidenceJSON 的通用 map 视图。
func evidence(t *testing.T, row domain.DimensionScore) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(row.EvidenceJSON), &m); err != nil {
		t.Fatalf("evidence_json 非合法 JSON: %v\n%s", err, row.EvidenceJSON)
	}
	return m
}

// specSummary evidence 内维度口径摘要条目。
func specSummary(t *testing.T, row domain.DimensionScore, code string) map[string]any {
	t.Helper()
	ev := evidence(t, row)
	arr, ok := ev["dimension_specs"].([]any)
	if !ok {
		t.Fatalf("evidence 缺 dimension_specs 数组: %v", ev)
	}
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok && m["code"] == code {
			return m
		}
	}
	t.Fatalf("dimension_specs 缺维度 %s: %v", code, arr)
	return nil
}

// ---- specs §5.1 评估用例 ----

// TestEvaluateNormalFlow 锚点：合法 dimensions JSON（含 1 个 insufficient 维）→
// 落库行 score 正确、insufficient 维 Score=0 且标记、Rationale 落库、EvidenceJSON 含 session_key。
func TestEvaluateNormalFlow(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("Skipped=%v Reused=%v, want false/false", res.Skipped, res.Reused)
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	instr := scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION")
	if instr.Score != 72 || instr.Insufficient {
		t.Errorf("AI_INSTRUCTION score=%d insufficient=%v, want 72/false", instr.Score, instr.Insufficient)
	}
	if instr.Rationale != "指令表述清晰" {
		t.Errorf("rationale = %q, want 指令表述清晰", instr.Rationale)
	}
	if instr.Status != domain.ScoreStatusSuccess || instr.Source != domain.ScoreSourceConversation {
		t.Errorf("status/source = %s/%s, want success/conversation", instr.Status, instr.Source)
	}
	if instr.ModelName != "gpt-test" {
		t.Errorf("model_name = %q, want gpt-test", instr.ModelName)
	}
	if instr.PromptVersion != PromptVersion {
		t.Errorf("prompt_version = %q, want %s", instr.PromptVersion, PromptVersion)
	}
	if instr.ErrorCode != "" {
		t.Errorf("success 行 error_code 应为空串, got %q", instr.ErrorCode)
	}
	review := scoreRowByCode(t, f.scores.saved, "AI_REVIEW")
	if review.Score != 0 || !review.Insufficient {
		t.Errorf("AI_REVIEW score=%d insufficient=%v, want 0/true", review.Score, review.Insufficient)
	}
	// EvidenceJSON 含 session_key 清单与口径摘要。
	ev := evidence(t, instr)
	keys, _ := ev["session_keys"].([]any)
	if len(keys) != 2 || keys[0] != "sess-a" || keys[1] != "sess-b" {
		t.Errorf("session_keys = %v, want [sess-a sess-b]", keys)
	}
	sum := specSummary(t, instr, "AI_INSTRUCTION")
	if sum["weight"] != float64(30) || sum["in_overview"] != true {
		t.Errorf("口径摘要 = %v, want weight=30 in_overview=true", sum)
	}
	// 周期回填与 token 回填。
	if instr.PeriodStartAt.Unix() != testPeriod().Start || instr.PeriodEndAt.Unix() != testPeriod().End {
		t.Errorf("period = %v~%v, want %d~%d", instr.PeriodStartAt.Unix(), instr.PeriodEndAt.Unix(), testPeriod().Start, testPeriod().End)
	}
	if instr.TokenName != "张三" {
		t.Errorf("token_name = %q, want 张三", instr.TokenName)
	}
}

// TestEvaluateCodeHallucination 锚点：未知 code + 缺 1 维 + 越界 score →
// 未知丢弃、缺失补行、越界触发重试后（第二次合法）落库。
// 越界 score 须落在白名单内 code 上才触发校验（未知 code 先被收敛丢弃）。
func TestEvaluateCodeHallucination(t *testing.T) {
	f := newFixture(t)
	bad := 105
	first := `{"dimensions":[` +
		dimJSON("AI_HALLUCINATED", &bad, false, "未知维度") + `,` +
		dimJSON("AI_INSTRUCTION", &bad, false, "指令尚可") + `]}`
	f.llm.responses = []string{first, goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if f.llm.calls != 2 {
		t.Fatalf("LLM 调用 %d 次, want 2（越界触发重试）", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	for _, code := range []string{"AI_INSTRUCTION", "AI_VALUE", "AI_REVIEW"} {
		scoreRowByCode(t, f.scores.saved, code)
	}
	if scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION").Score != 72 {
		t.Errorf("重试后应采信第二次输出 score=72")
	}
	if res.Skipped || res.Reused {
		t.Errorf("Skipped/Reused 应为 false")
	}
}

// TestEvaluateSchemaRetryThenFailed 锚点：持续坏 JSON → 重试一次后落全维度
// failed 行（Status=failed、ErrorCode="ErrSchemaInvalid"），err=nil。
func TestEvaluateSchemaRetryThenFailed(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{"这不是 JSON", "仍然不是 JSON"}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("schema 耗尽应落 failed 行 err=nil, got %v", err)
	}
	if f.llm.calls != 2 {
		t.Fatalf("LLM 调用 %d 次, want 2（重试一次）", f.llm.calls)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("Skipped/Reused 应为 false")
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	for _, r := range f.scores.saved {
		if r.Status != domain.ScoreStatusFailed {
			t.Errorf("维度 %s status=%s, want failed", r.DimensionCode, r.Status)
		}
		if r.ErrorCode != "ErrSchemaInvalid" {
			t.Errorf("维度 %s error_code=%q, want ErrSchemaInvalid", r.DimensionCode, r.ErrorCode)
		}
		if r.Score != 0 {
			t.Errorf("failed 行 score 应落 0, got %d", r.Score)
		}
	}
}

// TestEvaluateRationaleRedacted 锚点：rationale 含 sk-abc123 与 C:\Users\x →
// 落库行 Rationale 含 [SECRET]/[PATH] 占位符、不含原文。
func TestEvaluateRationaleRedacted(t *testing.T) {
	f := newFixture(t)
	s50 := 50
	raw := `{"dimensions":[` +
		dimJSON("AI_INSTRUCTION", &s50, false, "密钥 sk-abc123 泄露与路径 C:\\Users\\x 引用") + `,` +
		dimJSON("AI_VALUE", &s50, false, "正常理由") + `,` +
		dimJSON("AI_REVIEW", nil, true, "证据不足") + `]}`
	f.llm.responses = []string{raw}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	r := scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION")
	if !strings.Contains(r.Rationale, "[SECRET]") || !strings.Contains(r.Rationale, "[PATH]") {
		t.Errorf("rationale 缺占位符: %q", r.Rationale)
	}
	if strings.Contains(r.Rationale, "sk-abc123") || strings.Contains(r.Rationale, `C:\Users`) {
		t.Errorf("rationale 残留原文: %q", r.Rationale)
	}
}

// TestEvaluateZeroValidProfilesSkipped 锚点：全 skipped 档案 → Skipped=true 且
// err=nil、fake LLM 零调用、全维度 insufficient 行落库（Status=success）。
func TestEvaluateZeroValidProfilesSkipped(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.features.rows = []domain.SessionFeature{
		{SessionKey: "sk-1", TokenName: "张三", Status: domain.FeatureStatusSkipped, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC()},
		{SessionKey: "sk-2", TokenName: "张三", Status: domain.FeatureStatusSkipped, Client: "claude_code", FirstTurnAt: time.Unix(p.Start+300, 0).UTC(), LastTurnAt: time.Unix(p.Start+400, 0).UTC()},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("零有效档案是业务态, got err=%v", err)
	}
	if !res.Skipped {
		t.Fatalf("Skipped 应为 true")
	}
	if f.llm.calls != 0 {
		t.Fatalf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	for _, r := range f.scores.saved {
		if !r.Insufficient || r.Score != 0 || r.Status != domain.ScoreStatusSuccess {
			t.Errorf("维度 %s = %+v, want insufficient/0/success", r.DimensionCode, r)
		}
		if r.Rationale == "" {
			t.Errorf("维度 %s rationale 应落缺省文案", r.DimensionCode)
		}
	}
}

// TestEvaluateSignatureSkipLLM 锚点：success 档案 2 行（占比 < 20%）+ 列表量 20 +
// client=workbuddy → Skipped=true 且 err=nil、fake LLM 零调用、全维度 insufficient
// 行落库、EvidenceJSON 含签名 kind。
func TestEvaluateSignatureSkipLLM(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	// 11 行档案：2 success + 9 skipped，success 占比 2/11≈18% < 20%，client 全 workbuddy。
	rows := []domain.SessionFeature{
		{SessionKey: "ok-1", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("摘要一")},
		{SessionKey: "ok-2", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+300, 0).UTC(), LastTurnAt: time.Unix(p.Start+400, 0).UTC(), ProfileJSON: successProfileJSON("摘要二")},
	}
	for i := 0; i < 9; i++ {
		rows = append(rows, domain.SessionFeature{
			SessionKey: fmt.Sprintf("skip-%d", i), TokenName: "张三", Status: domain.FeatureStatusSkipped,
			Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+500, 0).UTC(), LastTurnAt: time.Unix(p.Start+600, 0).UTC(),
		})
	}
	f.features.rows = rows
	sessions := make([]conversationlog.SessionSummary, 20)
	for i := range sessions {
		sessions[i] = conversationlog.SessionSummary{SessionKey: fmt.Sprintf("s-%d", i), TurnCount: 1, TokenName: "张三"}
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), sessions, nil)
	if err != nil {
		t.Fatalf("签名命中跳过是业务态, got err=%v", err)
	}
	if !res.Skipped {
		t.Fatalf("Skipped 应为 true")
	}
	if f.llm.calls != 0 {
		t.Fatalf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	for _, r := range f.scores.saved {
		if !r.Insufficient || r.Status != domain.ScoreStatusSuccess {
			t.Errorf("维度 %s = %+v, want insufficient/success", r.DimensionCode, r)
		}
	}
	ev := evidence(t, f.scores.saved[0])
	if ev["signature_kind"] != "bypass_orchestrator" {
		t.Errorf("signature_kind = %v, want bypass_orchestrator", ev["signature_kind"])
	}
}

// TestEvaluateNoDimensions 锚点：fake specs 空 → errors.Is(err, ErrNoDimensions)、
// fake scoreRepo 零写入。
func TestEvaluateNoDimensions(t *testing.T) {
	f := newFixture(t)
	f.specs.specs = []DimensionSpec{}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if !errors.Is(err, ErrNoDimensions) {
		t.Fatalf("err = %v, want ErrNoDimensions", err)
	}
	if f.scores.saveCalls != 0 {
		t.Errorf("scoreRepo 零写入, got %d 次 SaveAll", f.scores.saveCalls)
	}
}

// TestEvaluatePartialMissingPrompt 锚点：1 维 PromptText 空，fake LLM 对该维返回
// 分数（75）→ 该维强制收敛 Insufficient 行（Score=0、缺省文案、EvidenceJSON 含
// config_missing）、其余照常评分。
func TestEvaluatePartialMissingPrompt(t *testing.T) {
	f := newFixture(t)
	specs := threeSpecs()
	specs[2].PromptText = "" // AI_REVIEW 缺提示词
	f.specs.specs = specs
	s75, s80 := 75, 80
	raw := `{"dimensions":[` +
		dimJSON("AI_INSTRUCTION", &s80, false, "指令明确") + `,` +
		dimJSON("AI_VALUE", &s80, false, "价值充分") + `,` +
		dimJSON("AI_REVIEW", &s75, false, "LLM 仍给了分") + `]}`
	f.llm.responses = []string{raw}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("缺提示词不阻断其余维度, Skipped 应为 false")
	}
	review := scoreRowByCode(t, f.scores.saved, "AI_REVIEW")
	if review.Score != 0 || !review.Insufficient {
		t.Errorf("AI_REVIEW score=%d insufficient=%v, want 0/true（代码级强制覆盖）", review.Score, review.Insufficient)
	}
	if review.Rationale != insufficientDefaultRationale {
		t.Errorf("AI_REVIEW rationale = %q, want 缺省文案", review.Rationale)
	}
	sum := specSummary(t, review, "AI_REVIEW")
	if sum["config_missing"] != true {
		t.Errorf("AI_REVIEW 口径摘要缺 config_missing=true: %v", sum)
	}
	if scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION").Score != 80 {
		t.Errorf("其余维度照常评分失败")
	}
}

// TestEvaluateTokenNameStripped 锚点：捕获喂 LLM 的 prompt → 全文不含 tokenName。
func TestEvaluateTokenNameStripped(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if f.llm.calls == 0 || len(f.llm.prompts) == 0 {
		t.Fatal("未捕获到 prompt")
	}
	if strings.Contains(f.llm.prompts[0], "张三") {
		t.Errorf("prompt 泄露人名: %.200s", f.llm.prompts[0])
	}
}

// TestEvaluateMissingPromptNotInContext 锚点（prompt v2 分流）：空提示词维度
// 不进 LLM 上下文（prompt 不含该维度编码），其行由代码确定性落 insufficient，
// LLM 对其余维度的输出不受占位维度畸形输出的连带（specs §3.2 不阻断其余维度）。
func TestEvaluateMissingPromptNotInContext(t *testing.T) {
	f := newFixture(t)
	specs := threeSpecs()
	specs[2].PromptText = "" // AI_REVIEW 缺提示词
	f.specs.specs = specs
	s80 := 80
	// LLM 只返回 2 维（AI_REVIEW 本就不在维度段，缺失补行路径接管）。
	f.llm.responses = []string{`{"dimensions":[` +
		dimJSON("AI_INSTRUCTION", &s80, false, "指令明确") + `,` +
		dimJSON("AI_VALUE", &s80, false, "价值充分") + `]}`}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if f.llm.calls != 1 || len(f.llm.prompts) != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
	if strings.Contains(f.llm.prompts[0], "AI_REVIEW") {
		t.Errorf("空提示词维度不应进 LLM 上下文: %s", f.llm.prompts[0][:200])
	}
	review := scoreRowByCode(t, f.scores.saved, "AI_REVIEW")
	if !review.Insufficient || review.Score != 0 || review.Status != domain.ScoreStatusSuccess {
		t.Errorf("AI_REVIEW = %+v, want insufficient/0/success（确定性补行）", review)
	}
}

// TestEvaluateSessionsDedupBeforeSignature 锚点：含跨页重复 session_key 的列表
// 传入 Evaluate → 签名识别按去重后列表判据（与 StatPersonByKey 路径同口径），
// 正常人群不因分母膨胀误判 auto_client。
func TestEvaluateSessionsDedupBeforeSignature(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	// 档案趋零形态：2 success + 9 skipped（占比 < 20%），client 全 workbuddy。
	rows := []domain.SessionFeature{
		{SessionKey: "ok-1", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+100, 0).UTC(), LastTurnAt: time.Unix(p.Start+200, 0).UTC(), ProfileJSON: successProfileJSON("摘要一")},
		{SessionKey: "ok-2", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+300, 0).UTC(), LastTurnAt: time.Unix(p.Start+400, 0).UTC(), ProfileJSON: successProfileJSON("摘要二")},
	}
	for i := 0; i < 9; i++ {
		rows = append(rows, domain.SessionFeature{
			SessionKey: fmt.Sprintf("skip-%d", i), TokenName: "张三", Status: domain.FeatureStatusSkipped,
			Client: "workbuddy", FirstTurnAt: time.Unix(p.Start+500, 0).UTC(), LastTurnAt: time.Unix(p.Start+600, 0).UTC(),
		})
	}
	f.features.rows = rows
	// 列表仅 4 条真实会话（< lowFreq=5 门槛）：去重后不满足旁路签名的列表量判据，
	// 未去重的 8 条（重复键翻倍）会跨过门槛误判 bypass_orchestrator。
	sessions := []conversationlog.SessionSummary{
		{SessionKey: "s-1", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-2", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-3", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-4", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-1", TurnCount: 1, TokenName: "张三"}, // 跨页重复
		{SessionKey: "s-2", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-3", TurnCount: 1, TokenName: "张三"},
		{SessionKey: "s-4", TurnCount: 1, TokenName: "张三"},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), sessions, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped {
		t.Fatal("去重后列表量 4 < lowFreq 5，不满足旁路签名门槛，不应跳过 LLM")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1（正常走 LLM）", f.llm.calls)
	}
}

// TestEvaluateIdempotentReuse 锚点：预置全 success 评分行重跑 → Reused=true、
// fake LLM 零调用。
func TestEvaluateIdempotentReuse(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_REVIEW", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Reused {
		t.Fatalf("Reused 应为 true")
	}
	if f.llm.calls != 0 {
		t.Fatalf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if f.scores.saveCalls != 0 {
		t.Errorf("复用路径零落库, got %d 次 SaveAll", f.scores.saveCalls)
	}
}

// TestEvaluateFailedRerun 锚点：预置 failed 行 + fake LLM 恢复 → 旧 failed 行删除
// （fake DeleteConversationFailed 被调）、新行落库。
func TestEvaluateFailedRerun(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusFailed, ErrorCode: "ErrLLMEvalUpstream"},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused || res.Skipped {
		t.Fatalf("failed 重评路径 Reused/Skipped 应为 false")
	}
	if f.scores.deleteCalls != 1 {
		t.Fatalf("DeleteConversationFailed 调用 %d 次, want 1", f.scores.deleteCalls)
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	for _, r := range f.scores.saved {
		if r.Status != domain.ScoreStatusSuccess {
			t.Errorf("重评后应落 success 行, got %s", r.Status)
		}
	}
}

// TestEvaluatePeriodExactMatch 锚点：预置同 start 不同 end 的 success 行，传入窄
// period → 不命中复用、正常走 LLM（双界精确匹配）。
func TestEvaluatePeriodExactMatch(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	// 宽窗口行：同 start、end 更晚。
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End+24*3600, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End+24*3600, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End+24*3600, 0).UTC(), DimensionCode: "AI_REVIEW", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
	}
	f.llm.responses = []string{goodScoreJSON()}

	narrow := activity.Period{Start: p.Start, End: p.Start + 24*3600}
	// 档案行在窄窗口内也可见（last_turn 落首日内）。
	res, err := f.ev.Evaluate(context.Background(), "张三", narrow, nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatalf("同 start 不同 end 不应命中复用（双界精确匹配）")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1（正常走 LLM）", f.llm.calls)
	}
}

// TestEvaluateStoreWriteError 锚点：fake scoreRepo.SaveAll 返回 error →
// err 非 nil 且 wrap ErrStoreWrite。
func TestEvaluateStoreWriteError(t *testing.T) {
	f := newFixture(t)
	f.scores.saveErr = errors.New("db down")
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err == nil {
		t.Fatal("SaveAll 失败应上抛错误")
	}
	if !errors.Is(err, ErrStoreWrite) {
		t.Fatalf("err = %v, want wrap ErrStoreWrite", err)
	}
}

// TestEvaluateCtxCancel 锚点：ctx 已取消 + fake LLM 阻塞 → err 非 nil
// （基础设施通道）、零落库。
func TestEvaluateCtxCancel(t *testing.T) {
	f := newFixture(t)
	f.llm.blocks = true

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.ev.Evaluate(ctx, "张三", testPeriod(), nil, nil)
	if err == nil {
		t.Fatal("ctx 取消应走基础设施 error 通道")
	}
	if errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("ctx 取消不应落 schema 降级: %v", err)
	}
	if f.scores.saveCalls != 0 {
		t.Errorf("ctx 取消应零落库, got %d 次 SaveAll", f.scores.saveCalls)
	}
}

// TestEpochRowsExcluded 锚点（specs §5.2 与 T4 档案表联动）：预置含 epoch 时间列
// （last_turn 1970-01-01）的档案行走 Evaluate → epoch 行被窗口过滤自然排除、
// 评分与签名判据均无该行参与。
func TestEpochRowsExcluded(t *testing.T) {
	f := newFixture(t)
	// epoch 行：last_turn = 1970-01-01，恒落窗外。
	f.features.rows = append(f.features.rows, domain.SessionFeature{
		SessionKey: "epoch-row", TokenName: "张三", Status: domain.FeatureStatusSuccess, Client: "workbuddy",
		FirstTurnAt: time.Unix(0, 0).UTC(), LastTurnAt: time.Unix(0, 0).UTC(), ProfileJSON: successProfileJSON("epoch 摘要"),
	})
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("正常评估路径 Skipped/Reused 应为 false")
	}
	ev := evidence(t, scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION"))
	keys, _ := ev["session_keys"].([]any)
	if len(keys) != 2 {
		t.Fatalf("session_keys = %v, want 仅 2 条窗口内行（epoch 行排除）", keys)
	}
	for _, k := range keys {
		if k == "epoch-row" {
			t.Errorf("epoch 行泄漏进评分证据: %v", keys)
		}
	}
	// epoch 行的 client=workbuddy 不参与签名判据：2 条 claude_code success 正常评分。
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
}

// ---- 补充边界与异常 ----

// TestEvaluateMaxTokensSet 补充：ChatRequest.MaxTokens 逐请求设置为 evalMaxTokens
//（按 20 维最坏合法输出标定，见 evaluate.go 常量注释）。
func TestEvaluateMaxTokensSet(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if f.llm.lastReq.MaxTokens != evalMaxTokens {
		t.Errorf("MaxTokens = %d, want %d", f.llm.lastReq.MaxTokens, evalMaxTokens)
	}
}

// TestEvaluateModelResolveFailureNonBlocking 补充：GetEnabledModel 失败 →
// ModelName 落空串、评分照常。
func TestEvaluateModelResolveFailureNonBlocking(t *testing.T) {
	f := newFixture(t)
	f.provider.err = errors.New("no model")
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("模型解析失败不阻断: %v", err)
	}
	instr := scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION")
	if instr.ModelName != "" {
		t.Errorf("model_name = %q, want 空串", instr.ModelName)
	}
}

// TestEvaluateThresholdReadFail 补充：thresholds 读取失败上抛（生产装配下
// 适配层已 wrap ErrDimensionConfigRead；fake 直返原始错误，透传语义不二次包装）。
func TestEvaluateThresholdReadFail(t *testing.T) {
	f := newFixture(t)
	f.th.err = errors.New("dim read down")

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "dim read down") {
		t.Fatalf("err = %v, want 透传底层错误", err)
	}
}

// TestEvaluateScoreListFail 补充：幂等预检读取失败 → 错误上抛（ErrScoreRead 语义）。
func TestEvaluateScoreListFail(t *testing.T) {
	f := newFixture(t)
	f.scores.listErr = errors.New("db down")

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err == nil {
		t.Fatal("ListByPersonPeriodExact 失败应上抛")
	}
	if f.llm.calls != 0 {
		t.Errorf("预检失败零 LLM 调用, got %d", f.llm.calls)
	}
}

// TestEvaluateEmptyProfilesSkipped 补充：空档案集（0 行）同样走零档案跳过路径。
func TestEvaluateEmptyProfilesSkipped(t *testing.T) {
	f := newFixture(t)
	f.features.rows = nil

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("空档案集是业务态: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("Skipped 应为 true")
	}
	if f.llm.calls != 0 {
		t.Errorf("LLM 调用 %d 次, want 0", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Errorf("落库行数 %d, want 3", len(f.scores.saved))
	}
}

// TestEvaluateEvidenceSummarySnapshot 补充：EvidenceJSON 统计摘要快照含关键计数
// （sessions_total 与 success 数）。
func TestEvaluateEvidenceSummarySnapshot(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{goodScoreJSON()}

	_, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	ev := evidence(t, scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION"))
	sum, ok := ev["summary"].(map[string]any)
	if !ok {
		t.Fatalf("evidence 缺 summary 快照: %v", ev)
	}
	if sum["sessions_total"] != float64(2) {
		t.Errorf("sessions_total = %v, want 2", sum["sessions_total"])
	}
	if sum["sessions_valid"] != float64(2) {
		t.Errorf("sessions_valid = %v, want 2", sum["sessions_valid"])
	}
}

// TestEvaluateSignatureNormalProceeds 补充：normal / work_tc1 / threshold 签名
// 不触发跳过，正常走 LLM。
func TestEvaluateSignatureNormalProceeds(t *testing.T) {
	f := newFixture(t)
	f.llm.responses = []string{goodScoreJSON()}

	// fixture 档案：2 条 success、client=claude_code、sessions 空（退化分支）。
	// success 占比 100% ≥ 20% → work_tc1 判据分母为 0 落 normal。
	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped {
		t.Fatalf("normal 签名不应跳过 LLM")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
}

// TestEvaluateAllSpecsSuccessReuseRequiresFullSet 补充：success 行只覆盖 2/3 维
// → 维度集合不齐 → 不复用，正常走 LLM。
func TestEvaluateAllSpecsSuccessReuseRequiresFullSet(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: PromptVersion},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatalf("维度集合不齐不应复用")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
}

// TestEvaluateActiveTestRowsNotInReuse 补充：F7 active_test 行不参与 conversation
// 幂等判定（同键同界 active_test success 行不能触发复用）。
func TestEvaluateActiveTestRowsNotInReuse(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_MGMT_PLAN", Source: domain.ScoreSourceActiveTest, Status: domain.ScoreStatusSuccess},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatalf("active_test 行不参与复用判定")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1", f.llm.calls)
	}
}

// TestEvaluateSkipRowsNotReused 锚点：零 LLM 跳过路径落的 insufficient(success)
// 行带 skip_no_llm 标记，重跑（档案已就位）不进复用判定，先删后评正常走 LLM
// （评估先于抽取落库的自愈通道）。
func TestEvaluateSkipRowsNotReused(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, Insufficient: true, ErrorCode: domain.ErrorCodeSkipNoLLM},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, Insufficient: true, ErrorCode: domain.ErrorCodeSkipNoLLM},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_REVIEW", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, Insufficient: true, ErrorCode: domain.ErrorCodeSkipNoLLM},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatal("skip_no_llm 标记行不应触发复用")
	}
	if f.scores.deleteCalls != 1 {
		t.Fatalf("DeleteConversationFailed 调用 %d 次, want 1（先删后评）", f.scores.deleteCalls)
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1（档案就位后正常评估）", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3", len(f.scores.saved))
	}
	instr := scoreRowByCode(t, f.scores.saved, "AI_INSTRUCTION")
	if instr.Score != 72 || instr.ErrorCode != "" {
		t.Errorf("重评后应为 LLM 产出行 score=72 无标记, got %+v", instr)
	}
}

// TestEvaluateSkippedRowsCarryMarker 锚点：零档案跳过路径落的全维 insufficient 行
// 携带 skip_no_llm 标记（success 行 error_code 非空仅此形态与 failed 行）。
func TestEvaluateSkippedRowsCarryMarker(t *testing.T) {
	f := newFixture(t)
	f.features.rows = nil

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("空档案集是业务态: %v", err)
	}
	if !res.Skipped {
		t.Fatal("Skipped 应为 true")
	}
	for _, r := range f.scores.saved {
		if r.Status != domain.ScoreStatusSuccess || r.ErrorCode != domain.ErrorCodeSkipNoLLM {
			t.Errorf("skip 行应为 success + skip_no_llm 标记, got status=%s error_code=%q", r.Status, r.ErrorCode)
		}
	}
}

// TestEvaluateAllPromptEmptyShortCircuit 锚点：全部启用维度 prompt 为空（DB 直改
// 绕过 service 校验）→ 零 LLM 调用直接短路落全量 insufficient 行。
func TestEvaluateAllPromptEmptyShortCircuit(t *testing.T) {
	f := newFixture(t)
	specs := threeSpecs()
	for i := range specs {
		specs[i].PromptText = ""
	}
	f.specs.specs = specs

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Skipped || res.Reused {
		t.Fatalf("全空提示词短路 Skipped/Reused 应为 false")
	}
	if f.llm.calls != 0 {
		t.Fatalf("LLM 调用 %d 次, want 0（零维度 prompt 无评分意义）", f.llm.calls)
	}
	if len(f.scores.saved) != 3 {
		t.Fatalf("落库行数 %d, want 3（全量 insufficient）", len(f.scores.saved))
	}
	for _, r := range f.scores.saved {
		if !r.Insufficient || r.Status != domain.ScoreStatusSuccess {
			t.Errorf("空提示词维度应落 insufficient success 行: %+v", r)
		}
	}
}

// TestEvaluateMissingPromptRowsCarryMarker 补充：空提示词维度的 insufficient 行
// 带 skip_no_llm 标记（与 skipRows 同款），补全提示词后重跑走先删后评自愈。
func TestEvaluateMissingPromptRowsCarryMarker(t *testing.T) {
	f := newFixture(t)
	specs := threeSpecs()
	specs[2].PromptText = "" // AI_REVIEW 缺提示词
	f.specs.specs = specs
	s80 := 80
	f.llm.responses = []string{`{"dimensions":[` +
		dimJSON("AI_INSTRUCTION", &s80, false, "指令明确") + `,` +
		dimJSON("AI_VALUE", &s80, false, "价值充分") + `]}`}

	if _, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	review := scoreRowByCode(t, f.scores.saved, "AI_REVIEW")
	if review.ErrorCode != domain.ErrorCodeSkipNoLLM {
		t.Fatalf("空提示词行 ErrorCode = %q, want skip_no_llm（自愈标记）", review.ErrorCode)
	}

	// 补全提示词后重跑：无标记 success 行集会误判复用，带标记行触发先删后评。
	// 先把首轮落库行搬进 existing 模拟真实链路（DB 落库后重跑读到既有行）。
	f.scores.existing = append(f.scores.existing, f.scores.saved...)
	f.scores.saved = nil
	specs[2].PromptText = "补全的提示词"
	f.specs.specs = specs
	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("重跑 Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatal("skip_no_llm 标记行不应触发复用")
	}
	if f.scores.deleteCalls != 1 {
		t.Fatalf("DeleteConversationFailed 调用 %d 次, want 1（先删后评自愈）", f.scores.deleteCalls)
	}
}

// TestEvaluatePromptVersionMismatchRerun 补充：存量行 prompt_version 落后于当前
// 常量（模板升级发版）→ 不复用，重评后 upsert 覆盖为新版本行。
func TestEvaluatePromptVersionMismatchRerun(t *testing.T) {
	f := newFixture(t)
	p := testPeriod()
	f.scores.existing = []domain.DimensionScore{
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_INSTRUCTION", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: "v1"},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_VALUE", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: "v1"},
		{TokenName: "张三", PeriodStartAt: time.Unix(p.Start, 0).UTC(), PeriodEndAt: time.Unix(p.End, 0).UTC(), DimensionCode: "AI_REVIEW", Source: domain.ScoreSourceConversation, Status: domain.ScoreStatusSuccess, PromptVersion: "v1"},
	}
	f.llm.responses = []string{goodScoreJSON()}

	res, err := f.ev.Evaluate(context.Background(), "张三", testPeriod(), nil, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Reused {
		t.Fatalf("旧版本行不应复用（模板升级须重评）")
	}
	if f.llm.calls != 1 {
		t.Fatalf("LLM 调用 %d 次, want 1（重评）", f.llm.calls)
	}
	for _, r := range f.scores.saved {
		if r.PromptVersion != PromptVersion {
			t.Errorf("重评行 prompt_version = %q, want %s", r.PromptVersion, PromptVersion)
		}
	}
}
