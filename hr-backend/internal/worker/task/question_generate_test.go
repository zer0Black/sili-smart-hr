package task

// question_generate_test.go 契约测试：questionbank:generate 任务 handler 与
// NewMux 注册（specs 4.3.3、4.3.4 规则 1/3）。handler 契约签名收
// *questiongen.Generator 具体类型无法 fake：坏 payload 分支传 nil 驱动
//（person_evaluate 同款），合法路径用真 Generator 组合内存库 repo +
// fake LLM/维度读通道（questiongen_test 同构裁剪）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/questiongen"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// ---- 内存库与 fake 基建 ----

func newQGTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_qg_task_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignQGTestID(tx.Statement.Dest)
	})
	if err := db.AutoMigrate(&domain.Question{}, &domain.QuestionBatch{}, &domain.QuestionGeneration{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func assignQGTestID(dest any) {
	v := reflect.ValueOf(dest)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		setQGTestID(v)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				setQGTestID(elem)
			}
		}
	}
}

func setQGTestID(v reflect.Value) {
	f := v.FieldByName("ID")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 && f.Int() == 0 {
		f.SetInt(snowflake.NextID())
	}
}

// fakeQGLLM 可编程 LLM：fn 每次调用返回一段回复或错误。
type fakeQGLLM struct {
	fn    func(call int) (string, error)
	calls int
}

func (f *fakeQGLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.calls++
	if f.fn == nil {
		return nil, errors.New("fake llm not programmed")
	}
	resp, err := f.fn(f.calls)
	if err != nil {
		return nil, err
	}
	return &fakeQGStream{content: resp}, nil
}

type fakeQGStream struct {
	done    bool
	content string
}

func (s *fakeQGStream) Recv() (llm.StreamChunk, error) {
	if s.done {
		return llm.StreamChunk{}, io.EOF
	}
	s.done = true
	return llm.StreamChunk{Content: s.content}, nil
}
func (s *fakeQGStream) Close() error      { return nil }
func (s *fakeQGStream) Usage() (int, int) { return 0, 0 }

var _ llm.Client = (*fakeQGLLM)(nil)

// fakeQGDims 出题维度读通道 fake：err 非 nil 模拟读通道基础设施故障。
type fakeQGDims struct {
	err error
}

func (f *fakeQGDims) ListSpecsByIDs(ctx context.Context, ids []int64) ([]questiongen.DimensionSpec, error) {
	if f.err != nil {
		return nil, f.err
	}
	specs := make([]questiongen.DimensionSpec, 0, len(ids))
	for _, id := range ids {
		specs = append(specs, questiongen.DimensionSpec{ID: id, Name: fmt.Sprintf("维度-%d", id), Description: "考察出题"})
	}
	return specs, nil
}

var _ questiongen.DimensionSpecReader = (*fakeQGDims)(nil)

// qgFixture 生成任务 handler 测试注入件：真 Generator 组合内存库与 fake 依赖。
type qgFixture struct {
	db   *gorm.DB
	llm  *fakeQGLLM
	dims *fakeQGDims
	gen  *questiongen.Generator
}

func newQGFixture(t *testing.T) *qgFixture {
	t.Helper()
	db := newQGTestDB(t)
	f := &qgFixture{db: db, llm: &fakeQGLLM{}, dims: &fakeQGDims{}}
	f.gen = questiongen.New(f.llm, repository.NewQuestionGenerationRepository(db), nil, f.dims)
	return f
}

// seedQG 写一行 QUEUED 生成会话，返回行 ID 与配套 payload。
func (f *qgFixture) seedQG(t *testing.T, count int) (int64, string) {
	t.Helper()
	g := domain.QuestionGeneration{
		DimensionIDs: "[101]",
		Count:        count,
		Status:       domain.QuestionGenStatusQueued,
	}
	if err := f.db.Create(&g).Error; err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	return g.ID, fmt.Sprintf(`{"generation_id":"%d"}`, g.ID)
}

func qgValidReply(n int) string {
	return fmt.Sprintf(`{"scenario":"情境-%d：团队接到紧急交付任务。","requirement":"要求-%d：请选出最贴近你做法的一项。\nA. 亲自裁定\nB. 授权试验\nC. 上报决策\nD. 暂缓等待","focus_point":"考察-%d"}`, n, n, n)
}

func runQGTask(h asynq.HandlerFunc, payload string) error {
	return h(context.Background(), asynq.NewTask(TypeQuestionGenerate, []byte(payload)))
}

// ---- 核心断言 ----

// TestQuestionGenerateBadPayload 核心锚点：坏 JSON/空 ID/非数字/零/负数
// 返回 nil 丢弃且不调 Generator（nil Generator 意外触 Run 即 panic）。
func TestQuestionGenerateBadPayload(t *testing.T) {
	h := NewQuestionGenerateHandler(nil)
	cases := []struct {
		name    string
		payload string
	}{
		{"空payload", `{}`},
		{"空generation_id", `{"generation_id":""}`},
		{"非数字", `{"generation_id":"abc"}`},
		{"零", `{"generation_id":"0"}`},
		{"负数", `{"generation_id":"-9"}`},
		{"非法JSON", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := runQGTask(h, tc.payload); err != nil {
				t.Errorf("确定性坏 payload 应丢弃返回 nil, got %v", err)
			}
		})
	}
}

// TestQuestionGenerateDelegates 核心锚点：合法 payload 透传 generation_id 调 Run
//（ID 命中 seed 行 MarkRunning 才成功，LLM 被触即透传正确），正常完成返回 nil。
func TestQuestionGenerateDelegates(t *testing.T) {
	f := newQGFixture(t)
	id, payload := f.seedQG(t, 1)
	f.llm.fn = func(call int) (string, error) { return qgValidReply(call), nil }

	h := NewQuestionGenerateHandler(f.gen)
	if err := runQGTask(h, payload); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	if f.llm.calls != 1 {
		t.Fatalf("Run 应被透传触发单题 LLM 生成, 实调 %d 次", f.llm.calls)
	}
	var row domain.QuestionGeneration
	if err := f.db.First(&row, id).Error; err != nil {
		t.Fatalf("load generation: %v", err)
	}
	if row.Status != domain.QuestionGenStatusCompleted {
		t.Errorf("status = %s, want COMPLETED（Run 全链执行）", row.Status)
	}
}

// TestQuestionGenerateErrorPropagation 核心锚点：Run 上抛的基础设施错误
//（维度读通道故障）原样返回交 Asynq 重试，错误链保留。
func TestQuestionGenerateErrorPropagation(t *testing.T) {
	f := newQGFixture(t)
	_, payload := f.seedQG(t, 1)
	dimsErr := errors.New("dimension store down")
	f.dims.err = dimsErr

	h := NewQuestionGenerateHandler(f.gen)
	err := runQGTask(h, payload)
	if err == nil {
		t.Fatal("Run 错误应原样返回交 Asynq 重试")
	}
	if !errors.Is(err, dimsErr) {
		t.Errorf("错误链应保留底层错误, got %v", err)
	}
}

// TestQuestionGenerateLLMFailTerminalNoRetry 核心锚点（BR2）：LLM 业务失败由
// Run 终态化后返回 nil，任务不重试，FAILED + LLM_FAILED 且 questions 零残留。
func TestQuestionGenerateLLMFailTerminalNoRetry(t *testing.T) {
	f := newQGFixture(t)
	id, payload := f.seedQG(t, 2)
	f.llm.fn = func(call int) (string, error) {
		return "", &llm.Error{Code: "ErrRetryExhausted", Msg: "upstream down"}
	}

	h := NewQuestionGenerateHandler(f.gen)
	if err := runQGTask(h, payload); err != nil {
		t.Fatalf("业务失败终态化后任务不重试, 应 nil, got %v", err)
	}
	var row domain.QuestionGeneration
	if err := f.db.First(&row, id).Error; err != nil {
		t.Fatalf("load generation: %v", err)
	}
	if row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorLLMFailed {
		t.Fatalf("want FAILED + LLM_FAILED, got %s/%s", row.Status, row.ErrorCode)
	}
	var n int64
	if err := f.db.Model(&domain.Question{}).Count(&n).Error; err != nil {
		t.Fatalf("count questions: %v", err)
	}
	if n != 0 {
		t.Fatalf("失败零残留, questions 应 0 行, got %d", n)
	}
}

// TestQuestionGenerateTimeoutConst 核心锚点（BR1）：超时常量精确 2h
//（满额 30 题预算推导，超时 ctx 取消走 FAILED 整批作废）。
func TestQuestionGenerateTimeoutConst(t *testing.T) {
	if questionGenerateTimeout != 2*time.Hour {
		t.Errorf("questionGenerateTimeout = %v, want 2h", questionGenerateTimeout)
	}
}

// TestNewMuxRegistersQuestionGenerate 核心锚点：mux 已注册 TypeQuestionGenerate
// 且路由可达（NewMux 五参化后新增任务类型）。
func TestNewMuxRegistersQuestionGenerate(t *testing.T) {
	mux := NewMux(nil, nil, nil, nil, nil)
	if h, pattern := mux.Handler(asynq.NewTask(TypeQuestionGenerate, []byte(`{}`))); pattern != TypeQuestionGenerate || h == nil {
		t.Errorf("questionbank:generate 路由未注册: pattern = %q", pattern)
	}
}

// TestNewMuxQuestionGenerateTimeout 核心锚点（BR1）：注册 handler 挂 2h 任务级
// 超时（session_extract 同款 deadline 行为断言）。
func TestNewMuxQuestionGenerateTimeout(t *testing.T) {
	var gotDeadline time.Time
	var hasDeadline bool
	inner := asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		gotDeadline, hasDeadline = ctx.Deadline()
		return nil
	})
	mux := NewMux(nil, nil, nil, nil, inner)
	h, pattern := mux.Handler(asynq.NewTask(TypeQuestionGenerate, []byte(`{}`)))
	if pattern != TypeQuestionGenerate {
		t.Fatalf("pattern = %q, want %q", pattern, TypeQuestionGenerate)
	}
	before := time.Now()
	if err := h.ProcessTask(context.Background(), asynq.NewTask(TypeQuestionGenerate, []byte(`{}`))); err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}
	after := time.Now()
	if !hasDeadline {
		t.Fatal("注册的任务应携带任务级超时：handler ctx 应有 deadline")
	}
	lo, hi := before.Add(questionGenerateTimeout-time.Second), after.Add(questionGenerateTimeout+time.Second)
	if gotDeadline.Before(lo) || gotDeadline.After(hi) {
		t.Errorf("deadline = %v, want [%v, %v]", gotDeadline, lo, hi)
	}
}

// TestQuestionGeneratePayloadJSON 边界补充：payload json tag 与投递侧 schema 对齐。
func TestQuestionGeneratePayloadJSON(t *testing.T) {
	raw := `{"generation_id":"1785000000000003001"}`
	var p QuestionGeneratePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.GenerationID != "1785000000000003001" {
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
