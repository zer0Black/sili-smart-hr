package questiongen

// questiongen_test.go 契约测试：Generator.Run 逐题生成编排（specs 4.3.4 规则 1/2/3、
// 4.3.5 进度、04 §3.3 staging 零残留）。核心断言用真 :memory: SQLite 落三表验证。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// ---- 内存库基建（仿 repository/question_batch_test 反射版雪花回调）----

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_qgen_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignTestSnowflakeID(tx.Statement.Dest)
	})
	if err := db.AutoMigrate(&domain.Question{}, &domain.QuestionBatch{}, &domain.QuestionGeneration{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func assignTestSnowflakeID(dest any) {
	v := reflect.ValueOf(dest)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		setTestID(v)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				setTestID(elem)
			}
		}
	}
}

func setTestID(v reflect.Value) {
	f := v.FieldByName("ID")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 && f.Int() == 0 {
		f.SetInt(snowflake.NextID())
	}
}

// seedQueued 写一行 QUEUED 生成会话（两维轮转）。
func seedQueued(t *testing.T, db *gorm.DB, count int) domain.QuestionGeneration {
	t.Helper()
	g := domain.QuestionGeneration{
		DimensionIDs: fmt.Sprintf("[%d,%d]", dimA.ID, dimB.ID),
		Count:        count,
		Status:       domain.QuestionGenStatusQueued,
	}
	if err := db.Create(&g).Error; err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	return g
}

func loadGen(t *testing.T, db *gorm.DB, id int64) domain.QuestionGeneration {
	t.Helper()
	var g domain.QuestionGeneration
	if err := db.First(&g, id).Error; err != nil {
		t.Fatalf("load generation %d: %v", id, err)
	}
	return g
}

func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", model, err)
	}
	return n
}

// 出题维度口径。
var (
	dimA = DimensionSpec{ID: 101, Name: "授权与分工", Description: "考察授权判断"}
	dimB = DimensionSpec{ID: 102, Name: "团队协调", Description: "考察协调能力"}
)

// ---- fake 依赖 ----

// fakeLLM 可编程 LLM：fn 每次调用返回一段回复或错误（仿 evaluator fakeLLM）。
type fakeLLM struct {
	mu      sync.Mutex
	fn      func(call int) (string, error)
	calls   int
	prompts []string
}

func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	prompt := ""
	for _, m := range req.Messages {
		prompt += m.Content + "\n"
	}
	f.prompts = append(f.prompts, prompt)
	if f.fn == nil {
		return nil, errors.New("fake llm not programmed")
	}
	resp, err := f.fn(f.calls)
	if err != nil {
		return nil, err
	}
	return &fakeStream{content: resp}, nil
}

type fakeStream struct {
	done    bool
	content string
}

func (s *fakeStream) Recv() (llm.StreamChunk, error) {
	if s.done {
		return llm.StreamChunk{}, io.EOF
	}
	s.done = true
	return llm.StreamChunk{Content: s.content}, nil
}

func (s *fakeStream) Close() error      { return nil }
func (s *fakeStream) Usage() (int, int) { return 0, 0 }

var _ llm.Client = (*fakeLLM)(nil)

// fakeDimReader 内存维度口径 fake。
type fakeDimReader struct {
	dims map[int64]DimensionSpec
	err  error
}

func (f *fakeDimReader) ListSpecsByIDs(ctx context.Context, ids []int64) ([]DimensionSpec, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]DimensionSpec, 0, len(ids))
	for _, id := range ids {
		if d, ok := f.dims[id]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// newFixture 标准四依赖注入（真 repo + fake LLM + fake 维度）。
type genFixture struct {
	db      *gorm.DB
	genRepo repository.QuestionGenerationRepository
	llm     *fakeLLM
	dims    *fakeDimReader
	gen     *Generator
}

func newFixture(t *testing.T) *genFixture {
	t.Helper()
	db := newTestDB(t)
	f := &genFixture{
		db:      db,
		genRepo: repository.NewQuestionGenerationRepository(db),
		llm:     &fakeLLM{},
		dims:    &fakeDimReader{dims: map[int64]DimensionSpec{dimA.ID: dimA, dimB.ID: dimB}},
	}
	f.gen = New(f.llm, f.genRepo, f.dims)
	return f
}

// validReply 第 n 题合法输出。
func validReply(n int) string {
	return fmt.Sprintf(`{"scenario":"情境-%d：团队接到紧急交付任务。","requirement":"要求-%d：请选出最贴近你做法的一项。\nA. 亲自裁定\nB. 授权试验\nC. 上报决策\nD. 暂缓等待","focus_point":"考察-%d"}`, n, n, n)
}

// ---- 核心断言 ----

// TestRunHappyPath 核心断言（BR1/BR3/BR5）：count=3 两维轮转，终态 COMPLETED、
// batch_id 落值、3 行 PENDING 题目 Q-AG 编号连续、批次 question_count=3、
// staging 清空、SaveProgress 调 3 次且 generated_count 递增。
func TestRunHappyPath(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 3)
	f.llm.fn = func(call int) (string, error) { return validReply(call), nil }

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// LLM 恰 3 次调用，prompt 按快照顺序轮转两维（A,B,A）。
	if f.llm.calls != 3 {
		t.Fatalf("LLM 调用 %d 次, want 3", f.llm.calls)
	}
	for i, want := range []string{dimA.Name, dimB.Name, dimA.Name} {
		if !strings.Contains(f.llm.prompts[i], want) {
			t.Errorf("第 %d 题 prompt 应含维度 %s", i+1, want)
		}
	}

	// generation 终态与进度。
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusCompleted {
		t.Fatalf("status want COMPLETED, got %s", row.Status)
	}
	if row.BatchID == 0 {
		t.Fatal("batch_id 应落值")
	}
	if row.GeneratedCount != 3 {
		t.Fatalf("generated_count want 3, got %d", row.GeneratedCount)
	}
	if row.Staging != "" {
		t.Fatalf("staging 应清空, got %q", row.Staging)
	}

	// 批次行：PENDING、GENERATE、question_count=3、title 维度组合、维度快照。
	var batch domain.QuestionBatch
	if err := f.db.First(&batch, row.BatchID).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	if batch.Status != domain.QuestionBatchStatusPending ||
		batch.BatchType != domain.QuestionBatchTypeGenerate ||
		batch.Source != domain.QuestionSourceAI || batch.QuestionCount != 3 {
		t.Fatalf("batch row mismatch: %+v", batch)
	}
	if batch.Title != "授权与分工、团队协调" {
		t.Errorf("title = %q, want 维度名顿号连接", batch.Title)
	}
	if batch.DimensionIDs != fmt.Sprintf("[%d,%d]", dimA.ID, dimB.ID) {
		t.Errorf("batch dimension_ids = %q", batch.DimensionIDs)
	}

	// 题目行：3 行 PENDING、Q-AG 连续编号、维度轮转、文本落库。
	var qs []domain.Question
	if err := f.db.Where("batch_id = ?", row.BatchID).Order("question_no").Find(&qs).Error; err != nil {
		t.Fatalf("load questions: %v", err)
	}
	if len(qs) != 3 {
		t.Fatalf("want 3 question rows, got %d", len(qs))
	}
	for i, q := range qs {
		if q.Status != domain.QuestionStatusPending {
			t.Errorf("题目 %s want PENDING, got %s", q.QuestionNo, q.Status)
		}
		wantNo := fmt.Sprintf("Q-AG-%04d", i+1)
		if q.QuestionNo != wantNo {
			t.Errorf("编号 want %s, got %s", wantNo, q.QuestionNo)
		}
		if q.Version != 1 || q.Source != domain.QuestionSourceAI {
			t.Errorf("题目 %s version/source = %d/%s", q.QuestionNo, q.Version, q.Source)
		}
		if q.Scenario == "" || q.Requirement == "" || q.FocusPoint == "" {
			t.Errorf("题目 %s 三段文本应非空", q.QuestionNo)
		}
	}
	if qs[0].DimensionID != dimA.ID || qs[1].DimensionID != dimB.ID || qs[2].DimensionID != dimA.ID {
		t.Errorf("维度轮转错位: %d/%d/%d", qs[0].DimensionID, qs[1].DimensionID, qs[2].DimensionID)
	}
}

// TestRunProgressPersistence 进度推进（BR5）：count=3 时逐题落
// generated_count=1/2/3 与 current_dimension_id 指向下一题维度，终态前可观测。
// 第 3 题 LLM 调用开始时（第 2 题 SaveProgress 已写）外部读行快照。
func TestRunProgressPersistence(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 3)
	var mu sync.Mutex
	progress := []domain.QuestionGeneration{}
	f.llm.fn = func(call int) (string, error) {
		if call == 3 {
			// 第 2 题完成后、第 3 题开题前读行快照（此刻 SaveProgress 已写 2 次）。
			mu.Lock()
			progress = append(progress, loadGen(t, f.db, g.ID))
			mu.Unlock()
		}
		return validReply(call), nil
	}

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(progress) != 1 {
		t.Fatalf("快照数 %d, want 1", len(progress))
	}
	snap := progress[0]
	if snap.GeneratedCount != 2 {
		t.Errorf("第 2 题后 generated_count want 2, got %d", snap.GeneratedCount)
	}
	// 第 3 题维度 = dims[2%2] = dimA，第 2 题完成后 current 应指向它。
	if snap.CurrentDimensionID != dimA.ID {
		t.Errorf("current_dimension_id want %d（下一题维度）, got %d", dimA.ID, snap.CurrentDimensionID)
	}
	if snap.Status != domain.QuestionGenStatusRunning {
		t.Errorf("进行中 status want RUNNING, got %s", snap.Status)
	}
	// staging 已承载 2 题。
	if !strings.Contains(snap.Staging, `"dimension_id":101`) || !strings.Contains(snap.Staging, `"dimension_id":102`) {
		t.Errorf("staging 应含两题 JSON: %q", snap.Staging)
	}
}

// TestRunLLMFailZeroResidue 核心断言（BR2）：fake LLM 恒错 → 终态 FAILED +
// error_code=LLM_FAILED、questions 0 行、无批次、staging 空、返回 nil。
func TestRunLLMFailZeroResidue(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 3)
	f.llm.fn = func(call int) (string, error) {
		return "", &llm.Error{Code: "ErrRetryExhausted", Msg: "upstream down"}
	}

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run 应返回 nil（业务失败终态化）, got %v", err)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed {
		t.Fatalf("status want FAILED, got %s", row.Status)
	}
	if row.ErrorCode != domain.QuestionGenErrorLLMFailed {
		t.Fatalf("error_code want LLM_FAILED, got %s", row.ErrorCode)
	}
	if row.Staging != "" {
		t.Fatalf("staging 应清空, got %q", row.Staging)
	}
	if row.BatchID != 0 {
		t.Fatalf("batch_id 应为 0, got %d", row.BatchID)
	}
	if n := countRows(t, f.db, &domain.Question{}); n != 0 {
		t.Fatalf("questions 表应 0 行, got %d", n)
	}
	if n := countRows(t, f.db, &domain.QuestionBatch{}); n != 0 {
		t.Fatalf("question_batches 表应 0 行, got %d", n)
	}
}

// TestRunLLMTimeoutClassification 超时归类：ErrTimeout 形错误落 LLM_TIMEOUT。
func TestRunLLMTimeoutClassification(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 2)
	f.llm.fn = func(call int) (string, error) {
		return "", &llm.Error{Code: "ErrTimeout", Msg: "transport timeout"}
	}
	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run 应返回 nil, got %v", err)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorLLMTimeout {
		t.Fatalf("want FAILED + LLM_TIMEOUT, got %s/%s", row.Status, row.ErrorCode)
	}
}

// TestRunSchemaRetryThenFail schema 校验失败重试一次（evaluator 同构）：
// 首答超长二答仍超长 → LLM 共 2 次调用后 FAILED。
func TestRunSchemaRetryThenFail(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 1)
	bad := fmt.Sprintf(`{"scenario":"%s","requirement":"要求","focus_point":"考察"}`, repeatRune(1001))
	f.llm.fn = func(call int) (string, error) { return bad, nil }

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 2 {
		t.Fatalf("schema 失败应重试一次共 2 次调用, got %d", f.llm.calls)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed {
		t.Fatalf("status want FAILED, got %s", row.Status)
	}
}

// TestRunSchemaRetryRecovers 首答坏 JSON 二答合法 → 整批成功且 LLM 调用 2 次。
func TestRunSchemaRetryRecovers(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 1)
	f.llm.fn = func(call int) (string, error) {
		if call == 1 {
			return "这不是 JSON", nil
		}
		return validReply(call), nil
	}
	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 2 {
		t.Fatalf("want 2 次调用, got %d", f.llm.calls)
	}
	if row := loadGen(t, f.db, g.ID); row.Status != domain.QuestionGenStatusCompleted {
		t.Fatalf("status want COMPLETED, got %s", row.Status)
	}
}

// cancelBefore 在第 n 题 LLM 调用开始前经 repo.RequestCancel 取消（真实取消
// 路径同款语义：置 CANCELED + error_code + 清 staging），第 n 题取消检查命中。
func cancelBefore(t *testing.T, f *genFixture, id int64, beforeCall int) {
	f.llm.fn = func(call int) (string, error) {
		if call == beforeCall {
			if err := f.genRepo.RequestCancel(context.Background(), id); err != nil {
				t.Fatalf("外部取消: %v", err)
			}
		}
		return validReply(call), nil
	}
}

// TestRunCooperativeCancel 核心断言（BR2）：第 2 题 LLM 在飞期间外部取消 →
// 第 2 题成果不回写、第 3 题不再发起，终态 CANCELED、DB 0 行、staging 清空。
func TestRunCooperativeCancel(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 3)
	cancelBefore(t, f, g.ID, 2)

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 2 {
		t.Fatalf("取消后第 3 题不应再调 LLM, got %d 次", f.llm.calls)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusCanceled {
		t.Fatalf("status want CANCELED, got %s", row.Status)
	}
	if row.ErrorCode != domain.QuestionGenErrorCanceled {
		t.Fatalf("error_code want CANCELED, got %s", row.ErrorCode)
	}
	if row.GeneratedCount != 1 {
		t.Fatalf("取消后进度不应回写第 2 题, generated_count want 1, got %d", row.GeneratedCount)
	}
	if row.Staging != "" {
		t.Fatalf("取消后 staging 应保持清空, got %q", row.Staging)
	}
	if n := countRows(t, f.db, &domain.Question{}); n != 0 {
		t.Fatalf("questions 表应 0 行, got %d", n)
	}
	if n := countRows(t, f.db, &domain.QuestionBatch{}); n != 0 {
		t.Fatalf("question_batches 表应 0 行, got %d", n)
	}
}

// TestRunNotQueuedNoop 已取消/已领取的行 MarkRunning 见 ErrNotQueued：
// Run 直接返回 nil 且不触 LLM。
func TestRunNotQueuedNoop(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 2)
	f.db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("status", domain.QuestionGenStatusCanceled)

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 0 {
		t.Fatalf("不应触 LLM, got %d 次", f.llm.calls)
	}
	if row := loadGen(t, f.db, g.ID); row.Status != domain.QuestionGenStatusCanceled {
		t.Fatalf("status 应保持 CANCELED, got %s", row.Status)
	}
}

// TestRunCtxCanceled ctx 取消落 FAILED（spec：超时 ctx 取消走 FAILED），
// 不区分超时归类（非 *llm.Error 路径归 LLM_FAILED）。
func TestRunCtxCanceled(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 2)
	ctx, cancel := context.WithCancel(context.Background())
	f.llm.fn = func(call int) (string, error) {
		cancel()
		return "", context.Canceled
	}
	if err := f.gen.Run(ctx, g.ID); err != nil {
		t.Fatalf("Run 应返回 nil, got %v", err)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed {
		t.Fatalf("status want FAILED, got %s", row.Status)
	}
	if n := countRows(t, f.db, &domain.Question{}); n != 0 {
		t.Fatalf("questions 表应 0 行, got %d", n)
	}
}

// TestRunMissingGeneration 行不存在：MarkRunning 见 ErrNotQueued 语义，返回 nil。
func TestRunMissingGeneration(t *testing.T) {
	f := newFixture(t)
	if err := f.gen.Run(context.Background(), 999999); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.llm.calls != 0 {
		t.Fatalf("不应触 LLM, got %d 次", f.llm.calls)
	}
}

// TestRunInternalSnapshotBad 数据异常终态化：快照非法或维度全缺失时置
// FAILED + INTERNAL 返回 nil（重试无意义）；维度读通道故障上抛交任务重试。
func TestRunInternalSnapshotBad(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 2)
	f.db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("dimension_ids", "not-json")

	if err := f.gen.Run(context.Background(), g.ID); err != nil {
		t.Fatalf("快照非法应终态化返回 nil, got %v", err)
	}
	row := loadGen(t, f.db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorInternal {
		t.Fatalf("want FAILED + INTERNAL, got %s/%s", row.Status, row.ErrorCode)
	}

	// 维度全缺失同走 INTERNAL。
	g2 := seedQueued(t, f.db, 1)
	f.db.Model(&domain.QuestionGeneration{}).Where("id = ?", g2.ID).
		Update("dimension_ids", "[999]")
	if err := f.gen.Run(context.Background(), g2.ID); err != nil {
		t.Fatalf("维度全缺失应终态化返回 nil, got %v", err)
	}
	if row := loadGen(t, f.db, g2.ID); row.Status != domain.QuestionGenStatusFailed ||
		row.ErrorCode != domain.QuestionGenErrorInternal {
		t.Fatalf("want FAILED + INTERNAL, got %s/%s", row.Status, row.ErrorCode)
	}

	// 读通道故障属基础设施错误：上抛不终态化。
	g3 := seedQueued(t, f.db, 1)
	f.dims.err = errors.New("dimension store down")
	if err := f.gen.Run(context.Background(), g3.ID); err == nil {
		t.Fatal("读通道故障应上抛 error")
	}
	if row := loadGen(t, f.db, g3.ID); row.Status != domain.QuestionGenStatusRunning {
		t.Fatalf("故障行应保持 RUNNING 待重试, got %s", row.Status)
	}
}

// TestRunStoreWriteErrorPropagates SaveProgress 失败属基础设施错误：
// error 上抛交 Asynq 重试，不终态化。
func TestRunStoreWriteErrorPropagates(t *testing.T) {
	f := newFixture(t)
	g := seedQueued(t, f.db, 1)
	f.llm.fn = func(call int) (string, error) { return validReply(call), nil }
	// 关闭底层 sql.DB 模拟存储故障：真 repo 的 Updates 会失败。
	sqlDB, _ := f.db.DB()
	_ = sqlDB.Close()

	err := f.gen.Run(context.Background(), g.ID)
	if err == nil {
		t.Fatal("存储故障应上抛 error 交任务重试")
	}
}

func repeatRune(n int) string {
	s := make([]rune, n)
	for i := range s {
		s[i] = '长'
	}
	return string(s)
}
