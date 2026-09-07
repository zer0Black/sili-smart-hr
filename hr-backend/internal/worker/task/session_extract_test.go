package task

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
)

// handler 测试经真实 extractor.Extractor 驱动（契约签名收 *Extractor，无法函数注入），
// 依赖全部 fake：拉取器回放详情或错误，repo 内存落行。参数读取走空集 fake（回退出厂）。

// stubFetcher 回放固定详情或错误。
type stubFetcher struct {
	detail *conversationlog.SessionDetail
	err    error
}

func (f *stubFetcher) GetSessionDetail(ctx context.Context, secret, sessionKey string) (*conversationlog.SessionDetail, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.detail, nil
}

// stubRepo 内存落行，记录 Save 调用次数。
type stubRepo struct {
	saves int
	rows  []*domain.SessionFeature
}

func (r *stubRepo) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	return nil, nil
}
func (r *stubRepo) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	r.saves++
	cp := *rec
	r.rows = append(r.rows, &cp)
	return false, nil
}
func (r *stubRepo) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	return nil, nil
}

// stubParams 空集参数源：ReadStringArray 恒返回空（Trim/Redact 走出厂分支）。
type stubParams struct{}

func (stubParams) ReadStringArray(key string) ([]string, error) { return nil, nil }

func (stubParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// stubSecrets 固定明文密钥。
func stubSecrets() extractor.SecretProvider {
	return func(ctx context.Context) (string, error) { return "test-secret", nil }
}

// okDetail 构造放行会话详情（1 真实输入 + 5 工具 + 1 叙述，过零响应判定）。
func okDetail(key string) *conversationlog.SessionDetail {
	msgs := []conversationlog.Message{
		{Role: "user", Kind: "text", Text: "帮我修复登录超时问题"},
		{Role: "tool", Kind: "tool_use", Text: "Read args=111"},
		{Role: "tool", Kind: "tool_use", Text: "Edit args=2055"},
		{Role: "tool", Kind: "tool_use", Text: "Bash command=ls"},
		{Role: "tool", Kind: "tool_use", Text: "Read args=52"},
		{Role: "tool", Kind: "tool_use", Text: "Edit args=88"},
		{Role: "assistant", Kind: "text", Text: "已完成修复并补齐测试。"},
	}
	return &conversationlog.SessionDetail{
		Session: conversationlog.SessionSummary{
			SessionKey: key, FirstTurnTime: 1700000000, LastTurnTime: 1700000300, TurnCount: 4, TokenName: "张三",
		},
		Messages: msgs,
	}
}

// llmResp 是合法 LLM 三块输出（与 extractor 包 schema 契约对齐的最小合法样本）。
const llmResp = `{"Summary":"会话围绕支付模块错误处理重构，产出回归测试。","Instruction":[{"Text":"帮我修复登录超时问题"}],"Behavior":{"instruction_specificity":"high","interrupt_style":"rare","review_ratio":"medium","paste_scale":"moderate"}}`

// okLLM 回放固定合法应答的 llm.Client fake。
type okLLM struct{}

func (s *okLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	return &okStream{content: llmResp}, nil
}

// okStream 单块回放。
type okStream struct {
	content string
	done    bool
}

func (s *okStream) Recv() (llm.StreamChunk, error) {
	if s.done {
		return llm.StreamChunk{}, io.EOF
	}
	s.done = true
	return llm.StreamChunk{Content: s.content}, nil
}
func (s *okStream) Close() error      { return nil }
func (s *okStream) Usage() (int, int) { return 0, 0 }

// mkExtractor 组装被测 Extractor。
func mkExtractor(fetcher extractor.ConversationlogDetailFetcher, repo *stubRepo, llmClient llm.Client) *extractor.Extractor {
	return extractor.New(llmClient, fetcher, repo, stubParams{}, stubSecrets())
}

// TestSessionExtractHandlerBadPayload 核心锚点：非法 JSON 与空 session_key/token_name
// 均返回 nil（确定性构造错误，重试恒失败，任务丢弃记 ERROR，03 §3.2，BR6）。
func TestSessionExtractHandlerBadPayload(t *testing.T) {
	h := NewSessionExtractHandler(nil)
	cases := []struct {
		name    string
		payload string
	}{
		{"非法JSON", "{not json"},
		{"空session_key", `{"session_key":"","token_name":"张三"}`},
		{"空token_name", `{"session_key":"abc","token_name":""}`},
		{"空JSON对象", `{}`},
	}
	for _, tc := range cases {
		if err := h(context.Background(), asynq.NewTask(TypeSessionExtract, []byte(tc.payload))); err != nil {
			t.Errorf("%s: 确定性坏 payload 应丢弃返回 nil, got %v", tc.name, err)
		}
	}
}

// TestSessionExtractHandlerDispatch 核心锚点：正常 payload 正确转发，
// Skipped 结果返回 nil（03 §3.3，BR4）。
func TestSessionExtractHandlerDispatch(t *testing.T) {
	repo := &stubRepo{}
	// 空壳详情：零输入保留类 5 条 < MinKeptMessages → Skipped。
	shell := &conversationlog.SessionDetail{
		Session: conversationlog.SessionSummary{SessionKey: "s-skip", TurnCount: 2},
		Messages: []conversationlog.Message{
			{Role: "tool", Kind: "tool_use", Text: "Read args=1"},
			{Role: "tool", Kind: "tool_use", Text: "Read args=2"},
			{Role: "tool", Kind: "tool_use", Text: "Read args=3"},
			{Role: "assistant", Kind: "text", Text: "先分析日志。"},
			{Role: "assistant", Kind: "text", Text: "再定位根因。"},
		},
	}
	h := NewSessionExtractHandler(mkExtractor(&stubFetcher{detail: shell}, repo, nil))

	err := h(context.Background(), asynq.NewTask(TypeSessionExtract,
		[]byte(`{"session_key":"s-skip","token_name":"张三"}`)))
	if err != nil {
		t.Fatalf("Skipped 终态应返回 nil: %v", err)
	}
	if repo.saves != 1 {
		t.Errorf("skipped 元数据行落库次数 = %d, want 1", repo.saves)
	}
	if repo.rows[0].Status != domain.FeatureStatusSkipped || repo.rows[0].TokenName != "张三" {
		t.Errorf("落行 = %+v, want skipped 且归属张三", repo.rows[0])
	}
}

// TestSessionExtractHandlerErrorRetries 核心锚点：ExtractByKey err 非 nil → 透传交重试（BR4）。
func TestSessionExtractHandlerErrorRetries(t *testing.T) {
	repo := &stubRepo{}
	fetcher := &stubFetcher{err: errors.New("net down")}
	h := NewSessionExtractHandler(mkExtractor(fetcher, repo, nil))

	err := h(context.Background(), asynq.NewTask(TypeSessionExtract,
		[]byte(`{"session_key":"s-2","token_name":"李四"}`)))
	if err == nil {
		t.Fatal("err 非 nil 应透传交 Asynq 重试")
	}
	if repo.saves != 0 {
		t.Errorf("失败路径不应落行, got %d", repo.saves)
	}
}

// TestSessionExtractHandlerTerminalStates 边界补充：success 终态返回 nil（BR4）。
func TestSessionExtractHandlerTerminalStates(t *testing.T) {
	repo := &stubRepo{}
	h := NewSessionExtractHandler(mkExtractor(&stubFetcher{detail: okDetail("s-ok")}, repo, &okLLM{}))

	if err := h(context.Background(), asynq.NewTask(TypeSessionExtract,
		[]byte(`{"session_key":"s-ok","token_name":"王五"}`))); err != nil {
		t.Fatalf("success 终态应返回 nil: %v", err)
	}
	if len(repo.rows) != 1 || repo.rows[0].Status != domain.FeatureStatusSuccess {
		t.Errorf("落行 = %+v, want success", repo.rows)
	}
}

// TestExtractTaskPayloadJSON 边界补充：payload 字段 json tag 与 03 §3.2 schema 对齐。
func TestExtractTaskPayloadJSON(t *testing.T) {
	raw := `{"session_key":"3f7c2a90","token_name":"张三"}`
	var p ExtractTaskPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.SessionKey != "3f7c2a90" || p.TokenName != "张三" {
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

// TestSessionExtractTimeoutConst 核心锚点：超时常量精确等于 600s（550s 最坏
// 全链 + 50s 落库冗余，BR1/BR2，推导见 sessionExtractTimeout 注释）。
func TestSessionExtractTimeoutConst(t *testing.T) {
	if sessionExtractTimeout != 600*time.Second {
		t.Errorf("sessionExtractTimeout = %v, want 600s", sessionExtractTimeout)
	}
}

// TestNewMuxSessionExtractTimeout 核心锚点：NewMux 注册的 TypeSessionExtract
// 携带 550s 任务级超时（BR1）。asynq v0.26.0 ServeMux 无 options 注册 API
// （HandleFuncWithOptions 属更高版本），mux 无内省口，故以行为断言锚定：
// 注入探针 handler，验证经 mux 派发后任务 ctx 被挂上 now+550s deadline。
func TestNewMuxSessionExtractTimeout(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(inner, nil)
	h, pattern := mux.Handler(asynq.NewTask(TypeSessionExtract, []byte(`{}`)))
	if pattern != TypeSessionExtract {
		t.Fatalf("pattern = %q, want %q", pattern, TypeSessionExtract)
	}

	before := time.Now()
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypeSessionExtract, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	after := time.Now()
	if !hasDeadline {
		t.Fatal("注册的任务应携带任务级超时：handler ctx 应有 deadline")
	}
	lo, hi := before.Add(sessionExtractTimeout-time.Second), after.Add(sessionExtractTimeout+time.Second)
	if gotDeadline.Before(lo) || gotDeadline.After(hi) {
		t.Errorf("deadline = %v, want [%v, %v]", gotDeadline, lo, hi)
	}
}

// TestNewMuxSessionExtractTimeoutRespectsParent 边界补充：父 ctx 自带更短
// deadline 时取更早者，包装不得延长父级预算（context.WithTimeout 语义）。
func TestNewMuxSessionExtractTimeoutRespectsParent(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(inner, nil)
	h, _ := mux.Handler(asynq.NewTask(TypeSessionExtract, []byte(`{}`)))

	parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before := time.Now()
	if err := h.ProcessTask(parent, asynq.NewTask(TypeSessionExtract, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	if !hasDeadline {
		t.Fatal("handler ctx 应保留 deadline")
	}
	if gotDeadline.After(before.Add(10 * time.Second)) {
		t.Errorf("更短父 deadline 不应被 550s 包装延长: deadline = %v", gotDeadline)
	}
}

// TestWithTimeoutCancelsOnExpiry 边界补充：到期后取消传播进 handler（超时闭环
// 的核心行为，此前零覆盖）：withTimeout 以纳秒级预算包装，handler 内观察 ctx
// 被 Done 且错误为 DeadlineExceeded，证明到期会打断在跑任务交 Asynq 重试。
func TestWithTimeoutCancelsOnExpiry(t *testing.T) {
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		<-ctx.Done()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Errorf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
		}
		return nil
	})
	h := withTimeout(inner, time.Nanosecond)
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypeSessionExtract, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
}

// TestNewMuxRegisters 核心锚点：mux 已注册 TypeSessionExtract，
// 经一条真实 asynq.Task 验证路由命中。
func TestNewMuxRegisters(t *testing.T) {
	repo := &stubRepo{}
	mux := NewMux(NewSessionExtractHandler(mkExtractor(&stubFetcher{detail: okDetail("s-mux")}, repo, &okLLM{})), nil)

	err := mux.ProcessTask(context.Background(),
		asynq.NewTask(TypeSessionExtract, []byte(`{"session_key":"s-mux","token_name":"赵六"}`)))
	if err != nil {
		t.Fatalf("mux 应路由到 session-extract handler: %v", err)
	}
	if len(repo.rows) != 1 || repo.rows[0].SessionKey != "s-mux" || repo.rows[0].TokenName != "赵六" {
		t.Errorf("handler 收到的会话 = %+v, want s-mux 赵六", repo.rows)
	}

	// 未注册类型路由应落 asynq 默认 NotFound 行为（报错），证明 mux 边界清晰。
	if err := mux.ProcessTask(context.Background(),
		asynq.NewTask("unknown:type", []byte("{}"))); err == nil {
		t.Error("未注册类型应报错而非静默成功")
	}
}
