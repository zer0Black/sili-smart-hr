package task

// test_grade_test.go 契约测试：assessment:test-grade handler 与 NewMux 注册
// （specs P2_TST_001 §5.2.1/§5.2.4/§5.2.5、03 §4.4/§4.5）。Grader 与仓储经窄
// 接口 fake 注入（本包不可 import grading：grading→pipeline→task 依赖链）；
// asynq 重试元数据经 retryBudget 变量替换注入（asynq 无公开 API 构造带
// metadata 的 ctx）。

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/domain"
)

// ---- fake 基建 ----

// fakeTestGrader 阅卷执行 fake：记录调用与 taskID，err 可注入。
type fakeTestGrader struct {
	err   error
	calls int
	gotID int64
}

var _ TestGrader = (*fakeTestGrader)(nil)

func (f *fakeTestGrader) Run(_ context.Context, taskID int64) error {
	f.calls++
	f.gotID = taskID
	return f.err
}

// fakeTerminalDegrader 降级窄面 fake：返回预置任务行（终态推进已收敛进
// fakeResultWriter.DegradeTask，本 fake 只承担读）。
type fakeTerminalDegrader struct {
	task   *domain.AssessmentTestTask
	getErr error
}

var _ TerminalDegrader = (*fakeTerminalDegrader)(nil)

func (f *fakeTerminalDegrader) GetByID(_ context.Context, _ int64) (*domain.AssessmentTestTask, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.task, nil
}

// fakeResultWriter 判型降级 fake：捕获 DegradeTask 调用（taskID 与 enneagram 标记）。
type fakeResultWriter struct {
	err     error
	calls   int
	taskIDs []int64
	enns    []bool
}

var _ TestGradeResultRepo = (*fakeResultWriter)(nil)

func (f *fakeResultWriter) DegradeTask(_ context.Context, taskID int64, enneagram bool) error {
	f.calls++
	f.taskIDs = append(f.taskIDs, taskID)
	f.enns = append(f.enns, enneagram)
	return f.err
}

// withRetryBudget 替换 asynq 重试元数据探测（t 结束还原）。
func withRetryBudget(t *testing.T, retried, maxRetry int) {
	t.Helper()
	old := retryBudget
	retryBudget = func(context.Context) (int, int) { return retried, maxRetry }
	t.Cleanup(func() { retryBudget = old })
}

func enneagramGradeTask() *domain.AssessmentTestTask {
	return &domain.AssessmentTestTask{
		ID: 42, TaskNo: "E202609280001", TestType: domain.TestTypeEnneagram,
		Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusGrading,
	}
}

func runGradeTask(h asynq.HandlerFunc, payload string) error {
	return h(context.Background(), asynq.NewTask(TypeTestGrade, []byte(payload)))
}

// ---- 核心断言 ----

// TestTestGradePayloadInvalid 核心锚点：空 payload/非数字/≤0 三形态 handler
// 返 nil 丢弃且 grader 未被调（03 §4.2 构造侧确定性错误）。
func TestTestGradePayloadInvalid(t *testing.T) {
	grader := &fakeTestGrader{}
	h := NewTestGradeHandler(grader, &fakeTerminalDegrader{}, &fakeResultWriter{})
	cases := []struct{ name, payload string }{
		{"空payload", `{}`},
		{"空task_id", `{"task_id":""}`},
		{"非数字", `{"task_id":"abc"}`},
		{"零", `{"task_id":"0"}`},
		{"负数", `{"task_id":"-9"}`},
		{"非法JSON", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runGradeTask(h, tc.payload); err != nil {
				t.Errorf("确定性坏 payload 应丢弃返回 nil, got %v", err)
			}
		})
	}
	if grader.calls != 0 {
		t.Fatalf("坏 payload 不应触达 grader, 实调 %d 次", grader.calls)
	}
}

// TestTestGradeHappyPath 核心锚点：payload 合法时 grader.Run 被调且 taskID
// 正确，nil 透传，不触达降级。
func TestTestGradeHappyPath(t *testing.T) {
	grader := &fakeTestGrader{}
	degrader := &fakeTerminalDegrader{}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(grader, degrader, results)
	const id int64 = 1790000000000000001
	if err := runGradeTask(h, `{"task_id":"1790000000000000001"}`); err != nil {
		t.Fatalf("正常路径应 nil: %v", err)
	}
	if grader.calls != 1 || grader.gotID != id {
		t.Fatalf("grader.Run 调用 %d 次 taskID %d, want 1 次 %d", grader.calls, grader.gotID, id)
	}
	if results.calls != 0 {
		t.Fatal("正常路径不应触达降级")
	}
}

// TestTestGradeDegradeOnExhausted 核心锚点（BR3/BR4/BR5）：重试预算耗尽
// （retried>=maxRetry）时吞掉 grader 错误走降级：handler 返 nil、DegradeTask
// 被调一次且 enneagram=true（仓储侧单事务落降级行并推任务行 degraded，
// specs §5.2.5、04 §3.3 降级行形态）。
func TestTestGradeDegradeOnExhausted(t *testing.T) {
	withRetryBudget(t, 25, 25)
	grader := &fakeTestGrader{err: errors.New("llm down")}
	degrader := &fakeTerminalDegrader{task: enneagramGradeTask()}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(grader, degrader, results)

	if err := runGradeTask(h, `{"task_id":"42"}`); err != nil {
		t.Fatalf("耗尽降级后应返回 nil: %v", err)
	}
	if results.calls != 1 || len(results.taskIDs) != 1 || results.taskIDs[0] != 42 {
		t.Fatalf("DegradeTask 应被调一次携 task 42, got calls=%d ids=%v", results.calls, results.taskIDs)
	}
	if !results.enns[0] {
		t.Error("enneagram 任务降级应携 enneagram=true（仓储侧落降级行）")
	}
}

// TestTestGradeErrorPropagatesBeforeExhausted 核心锚点：未耗尽（retried<maxRetry）
// 时 grader 错误原样上抛交 Asynq 重试，降级接口不被调（BR3）。
func TestTestGradeErrorPropagatesBeforeExhausted(t *testing.T) {
	withRetryBudget(t, 3, 25)
	wantErr := errors.New("upstream 503")
	grader := &fakeTestGrader{err: wantErr}
	degrader := &fakeTerminalDegrader{}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(grader, degrader, results)

	got := runGradeTask(h, `{"task_id":"42"}`)
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 原样上抛 %v", got, wantErr)
	}
	if results.calls != 0 {
		t.Fatal("未耗尽不应触达降级")
	}
}

// TestTestGradeDegradeFailsStillErrors 核心锚点：耗尽但降级路径失败时错误上抛
// （保留下次执行/补偿再投递收敛，specs §5.2.5）。
func TestTestGradeDegradeFailsStillErrors(t *testing.T) {
	withRetryBudget(t, 25, 25)
	degradeErr := errors.New("db down")
	h := NewTestGradeHandler(
		&fakeTestGrader{err: errors.New("llm down")},
		&fakeTerminalDegrader{task: enneagramGradeTask()},
		&fakeResultWriter{err: degradeErr},
	)
	if got := runGradeTask(h, `{"task_id":"42"}`); !errors.Is(got, degradeErr) {
		t.Fatalf("降级失败应上抛, got %v", got)
	}
}

// TestTestGradeTimeoutConst 核心锚点（BR1）：任务级超时精确 300s（specs
// §5.2.4 规则3 初值）。
func TestTestGradeTimeoutConst(t *testing.T) {
	if testGradeTimeout != 300*time.Second {
		t.Errorf("testGradeTimeout = %v, want 300s", testGradeTimeout)
	}
}

// TestNewMuxRegistersTestGrade 核心锚点：mux 已注册 TypeTestGrade 且路由可达
// （NewMux 七参化新增任务类型）。
func TestNewMuxRegistersTestGrade(t *testing.T) {
	mux := NewMux(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if h, pattern := mux.Handler(asynq.NewTask(TypeTestGrade, []byte(`{}`))); pattern != TypeTestGrade || h == nil {
		t.Errorf("assessment:test-grade 路由未注册: pattern = %q", pattern)
	}
}

// TestNewMuxTestGradeTimeout 核心锚点（BR1）：注册 handler 挂 300s 任务级超时
// （questionbank:generate 同款 deadline 行为断言）。
func TestNewMuxTestGradeTimeout(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(nil, nil, nil, nil, nil, nil, inner, nil, nil, nil)
	h, pattern := mux.Handler(asynq.NewTask(TypeTestGrade, []byte(`{}`)))
	if pattern != TypeTestGrade {
		t.Fatalf("pattern = %q, want %q", pattern, TypeTestGrade)
	}
	before := time.Now()
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypeTestGrade, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	after := time.Now()
	if !hasDeadline {
		t.Fatal("注册的任务应携带任务级超时：handler ctx 应有 deadline")
	}
	lo, hi := before.Add(testGradeTimeout-time.Second), after.Add(testGradeTimeout+time.Second)
	if gotDeadline.Before(lo) || gotDeadline.After(hi) {
		t.Errorf("deadline = %v, want [%v, %v]", gotDeadline, lo, hi)
	}
}

// ---- 边界补充 ----

// TestTestGradeDegradeAIMgmtNoResultRow 边界补充：ai_mgmt 耗尽 DegradeTask
// 携 enneagram=false（仓储侧仅推任务行，无判型降级行，04 §3.3）。
func TestTestGradeDegradeAIMgmtNoResultRow(t *testing.T) {
	withRetryBudget(t, 25, 25)
	degrader := &fakeTerminalDegrader{task: &domain.AssessmentTestTask{
		ID: 7, TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt,
		GradingStatus: domain.GradingStatusGrading,
	}}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(&fakeTestGrader{err: errors.New("llm down")}, degrader, results)

	if err := runGradeTask(h, `{"task_id":"7"}`); err != nil {
		t.Fatalf("ai_mgmt 耗尽降级应 nil: %v", err)
	}
	if results.calls != 1 || results.enns[0] {
		t.Fatalf("ai_mgmt 降级应携 enneagram=false, got calls=%d enns=%v", results.calls, results.enns)
	}
}

// TestTestGradeDegradeLoadFailsStillErrors 边界补充：降级前读任务行失败时按
// task_type 未知口径仍执行降级（仅推任务行不落降级行），DegradeTask 失败上抛
// 保留收敛；读行错误本身已被错误路径吞掉记日志，不阻断终态推进。
func TestTestGradeDegradeLoadFailsStillErrors(t *testing.T) {
	withRetryBudget(t, 25, 25)
	degrader := &fakeTerminalDegrader{getErr: errors.New("db down")}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(
		&fakeTestGrader{err: errors.New("llm down")},
		degrader,
		results,
	)
	if err := runGradeTask(h, `{"task_id":"42"}`); err != nil {
		t.Fatalf("读行失败降级仍应执行返 nil: %v", err)
	}
	if results.calls != 1 || results.enns[0] {
		t.Fatalf("task_type 未知按 ai_mgmt 口径仅推任务行, got calls=%d enns=%v", results.calls, results.enns)
	}
}

// TestTestGradeNoMetadataDegrades 边界补充：ctx 无 asynq 元数据（ok=false 按
// m=0 处理）视同耗尽走降级（与 MaxRetry(0) 投递形态同行为）。
func TestTestGradeNoMetadataDegrades(t *testing.T) {
	degrader := &fakeTerminalDegrader{task: enneagramGradeTask()}
	results := &fakeResultWriter{}
	h := NewTestGradeHandler(&fakeTestGrader{err: errors.New("llm down")}, degrader, results)
	if err := runGradeTask(h, `{"task_id":"42"}`); err != nil {
		t.Fatalf("无元数据视同耗尽应降级返 nil: %v", err)
	}
	if results.calls != 1 || !results.enns[0] {
		t.Fatalf("应走降级（enneagram=true）, got calls=%d enns=%v", results.calls, results.enns)
	}
}

// TestTestGradePayloadJSON 边界补充：payload json tag 与投递侧 schema 对齐
// （03 §4.2，雪花 ID 十进制字符串）。
func TestTestGradePayloadJSON(t *testing.T) {
	raw := `{"task_id":"1790000000000000001"}`
	var p TestGradePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.TaskID != "1790000000000000001" {
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
