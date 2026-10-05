package task

// suggest_task_test.go 契约测试：dashboard:suggest-tick / dashboard:suggest-generate
// 两任务 handler 与 NewMux 注册（specs P2_TMD_001 §5.1.2 步1-2/步6、§5.1.4
// 规则3/规则5，03 §4.1/§4.2）。Runner 窄接口 fake 注入（T6 *SuggestService
// 鸭子满足）；asynq 重试元数据复用 test_grade_test 的 withRetryBudget 注入。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// ---- fake 基建 ----

// fakeSuggestTickRunner tick 行为 fake：记录调用次数，err 可注入。
type fakeSuggestTickRunner struct {
	calls int
	err   error
}

var _ SuggestTickRunner = (*fakeSuggestTickRunner)(nil)

func (f *fakeSuggestTickRunner) TickScan(ctx context.Context, now time.Time) error {
	f.calls++
	return f.err
}

// fakeSuggestGenerateRunner 生成行为 fake：记录 Generate 与 MarkFailedIfExhausted 调用。
type fakeSuggestGenerateRunner struct {
	genCalls   int
	gotBatchID int64
	genErr     error

	exhaustCalls int
	exhaustIDs   []int64
	exhaustReas  []string
	exhaustErr   error
}

var _ SuggestGenerateRunner = (*fakeSuggestGenerateRunner)(nil)

func (f *fakeSuggestGenerateRunner) Generate(ctx context.Context, batchID int64) error {
	f.genCalls++
	f.gotBatchID = batchID
	return f.genErr
}

func (f *fakeSuggestGenerateRunner) MarkFailedIfExhausted(ctx context.Context, batchID int64, reason string) error {
	f.exhaustCalls++
	f.exhaustIDs = append(f.exhaustIDs, batchID)
	f.exhaustReas = append(f.exhaustReas, reason)
	return f.exhaustErr
}

func runSuggestTask(h asynq.HandlerFunc, typ, payload string) error {
	return h(context.Background(), asynq.NewTask(typ, []byte(payload)))
}

// ---- tick 核心断言 ----

// TestSuggestTickDispatches 核心锚点：handler 透传 TickScan 后返回 nil
//（specs §5.1.2 步1，03 §4.1 零 payload 极简形态）。
func TestSuggestTickDispatches(t *testing.T) {
	r := &fakeSuggestTickRunner{}
	h := NewSuggestTickHandler(r)
	if err := runSuggestTask(h, TypeSuggestTick, ""); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("TickScan 应被调 1 次, got %d", r.calls)
	}
}

// TestSuggestTickErrorPropagation 边界补充：TickScan err 非 nil 透传交 Asynq
// 任务级重试（03 §4.1 投递失败 err 透传口径）。
func TestSuggestTickErrorPropagation(t *testing.T) {
	wantErr := errors.New("scan down")
	h := NewSuggestTickHandler(&fakeSuggestTickRunner{err: wantErr})
	if got := runSuggestTask(h, TypeSuggestTick, ""); !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 透传 %v", got, wantErr)
	}
}

// TestSuggestTickTimeoutConst 核心锚点（BR2）：tick 任务级超时精确 60s（03 §4.1）。
func TestSuggestTickTimeoutConst(t *testing.T) {
	if suggestTickTimeout != 60*time.Second {
		t.Errorf("suggestTickTimeout = %v, want 60s", suggestTickTimeout)
	}
}

// ---- generate 核心断言 ----

// TestSuggestGenerateBadPayload 核心锚点：非 JSON/缺 batch_id/非数字/负数
// 四形态 handler 返回 nil 丢弃且 Runner 未被调用（03 §4.2 构造侧确定性错误，
// 防毒丸）。
func TestSuggestGenerateBadPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"非JSON", `{not json`},
		{"缺batch_id", `{}`},
		{"空batch_id", `{"batch_id":""}`},
		{"非数字", `{"batch_id":"abc"}`},
		{"负数", `{"batch_id":"-9"}`},
		{"零", `{"batch_id":"0"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeSuggestGenerateRunner{}
			h := NewSuggestGenerateHandler(r)
			if err := runSuggestTask(h, TypeSuggestGenerate, tc.payload); err != nil {
				t.Errorf("确定性坏 payload 应丢弃返回 nil, got %v", err)
			}
			if r.genCalls != 0 || r.exhaustCalls != 0 {
				t.Errorf("Runner 不应被调用, Generate %d 次 / MarkFailedIfExhausted %d 次",
					r.genCalls, r.exhaustCalls)
			}
		})
	}
}

// TestSuggestGenerateDispatches 核心锚点：合法 {"batch_id":"123"} 时
// Generate 收到 int64(123)。
func TestSuggestGenerateDispatches(t *testing.T) {
	r := &fakeSuggestGenerateRunner{}
	h := NewSuggestGenerateHandler(r)
	if err := runSuggestTask(h, TypeSuggestGenerate, `{"batch_id":"123"}`); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	if r.genCalls != 1 || r.gotBatchID != 123 {
		t.Errorf("Generate 收到 %d (calls=%d), want 123", r.gotBatchID, r.genCalls)
	}
}

// TestSuggestGenerateErrorPropagatesBeforeExhausted 核心锚点：未耗尽时
// Generate err 原样上抛交 Asynq 任务级重试，MarkFailedIfExhausted 不被调
//（specs §5.1.4 规则3 前半）。
func TestSuggestGenerateErrorPropagatesBeforeExhausted(t *testing.T) {
	withRetryBudget(t, 1, 3)
	wantErr := errors.New("llm down")
	r := &fakeSuggestGenerateRunner{genErr: wantErr}
	h := NewSuggestGenerateHandler(r)
	got := runSuggestTask(h, TypeSuggestGenerate, `{"batch_id":"123"}`)
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 原样上抛 %v", got, wantErr)
	}
	if r.exhaustCalls != 0 {
		t.Fatalf("未耗尽不应触达 MarkFailedIfExhausted, 实调 %d 次", r.exhaustCalls)
	}
}

// TestSuggestGenerateRetryExhausted 核心锚点（BR3）：GetRetryCount >=
// suggestMaxRetry 时吞错调 MarkFailedIfExhausted，handler 返回 nil 终结
//（specs §5.1.4 规则3，03 §4.2 末段）。
func TestSuggestGenerateRetryExhausted(t *testing.T) {
	withRetryBudget(t, suggestMaxRetry, suggestMaxRetry)
	r := &fakeSuggestGenerateRunner{genErr: errors.New("llm down")}
	h := NewSuggestGenerateHandler(r)
	if err := runSuggestTask(h, TypeSuggestGenerate, `{"batch_id":"123"}`); err != nil {
		t.Fatalf("耗尽落库后应返回 nil 终结: %v", err)
	}
	if r.exhaustCalls != 1 || len(r.exhaustIDs) != 1 || r.exhaustIDs[0] != 123 {
		t.Fatalf("MarkFailedIfExhausted 应被调一次携 123, got calls=%d ids=%v",
			r.exhaustCalls, r.exhaustIDs)
	}
	if r.exhaustReas[0] == "" {
		t.Error("失败原因应非空（落库记因，specs §5.1.4 规则3）")
	}
}

// TestSuggestGenerateExhaustedMarkFailsStillErrors 边界补充：耗尽但落库失败时
// 错误上抛保留下次执行收敛（落库失败 err 透传口径，specs §5.1.5 表）。
func TestSuggestGenerateExhaustedMarkFailsStillErrors(t *testing.T) {
	withRetryBudget(t, suggestMaxRetry, suggestMaxRetry)
	markErr := errors.New("db down")
	r := &fakeSuggestGenerateRunner{genErr: errors.New("llm down"), exhaustErr: markErr}
	h := NewSuggestGenerateHandler(r)
	if got := runSuggestTask(h, TypeSuggestGenerate, `{"batch_id":"123"}`); !errors.Is(got, markErr) {
		t.Fatalf("落库失败应上抛, got %v", got)
	}
}

// TestSuggestGenerateTimeoutConst 核心锚点（BR2）：generate 任务级超时精确
// 240s（specs §5.1.4 规则5 初值）。
func TestSuggestGenerateTimeoutConst(t *testing.T) {
	if suggestGenerateTimeout != 240*time.Second {
		t.Errorf("suggestGenerateTimeout = %v, want 240s", suggestGenerateTimeout)
	}
}

// TestSuggestMaxRetryConst 核心锚点（BR3）：任务级重试基准精确 3 次
//（specs §5.1.4 规则3）。
func TestSuggestMaxRetryConst(t *testing.T) {
	if suggestMaxRetry != 3 {
		t.Errorf("suggestMaxRetry = %d, want 3", suggestMaxRetry)
	}
}

// ---- mux 注册断言 ----

// TestNewMuxSuggestRegistrations 核心锚点：mux 两新类型均注册且路由可达
//（NewMux 九参化后新增任务类型）。
func TestNewMuxSuggestRegistrations(t *testing.T) {
	mux := NewMux(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	for _, typ := range []string{TypeSuggestTick, TypeSuggestGenerate} {
		if h, pattern := mux.Handler(asynq.NewTask(typ, []byte(`{}`))); pattern != typ || h == nil {
			t.Errorf("任务类型 %q 未注册: pattern = %q", typ, pattern)
		}
	}
}

// TestNewMuxSuggestGenerateTimeout 核心锚点（BR2）：注册 handler 挂 240s
// 任务级超时（questionbank:generate 同款 deadline 行为断言）。
func TestNewMuxSuggestGenerateTimeout(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(nil, nil, nil, nil, nil, nil, nil, nil, inner)
	h, pattern := mux.Handler(asynq.NewTask(TypeSuggestGenerate, []byte(`{}`)))
	if pattern != TypeSuggestGenerate {
		t.Fatalf("pattern = %q, want %q", pattern, TypeSuggestGenerate)
	}
	before := time.Now()
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypeSuggestGenerate, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	after := time.Now()
	if !hasDeadline {
		t.Fatal("注册的任务应携带任务级超时：handler ctx 应有 deadline")
	}
	lo, hi := before.Add(suggestGenerateTimeout-time.Second), after.Add(suggestGenerateTimeout+time.Second)
	if gotDeadline.Before(lo) || gotDeadline.After(hi) {
		t.Errorf("deadline = %v, want [%v, %v]", gotDeadline, lo, hi)
	}
}

// ---- payload schema 边界补充 ----

// TestSuggestGeneratePayloadJSON 边界补充：payload json tag 与投递侧 schema
// 对齐（03 §4.2，雪花 ID 十进制字符串）。
func TestSuggestGeneratePayloadJSON(t *testing.T) {
	raw := `{"batch_id":"1790000000000003001"}`
	var p SuggestGeneratePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.BatchID != "1790000000000003001" {
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
