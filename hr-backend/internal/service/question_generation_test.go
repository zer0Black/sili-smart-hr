// question_generation_test 生成会话 service 层测试：fake repo/enqueuer/provider
// 注入，不连真库。覆盖 03 §3.13/§3.14 发起校验、投递回滚、进度组装与取消幂等。
package service_test

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// qGenFakeRepo 是 QuestionGenerationRepository 的假实现。
type qGenFakeRepo struct {
	created   []*domain.QuestionGeneration
	createErr error

	findByID    *domain.QuestionGeneration
	findByIDErr error

	deleted   []int64
	deleteErr error

	cancelCalled bool
	cancelID     int64
	cancelErr    error
}

func (r *qGenFakeRepo) Create(_ context.Context, g *domain.QuestionGeneration) error {
	if g.ID == 0 {
		g.ID = 3001 // 模拟 GORM 雪花回调赋主键
	}
	r.created = append(r.created, g)
	return r.createErr
}

func (r *qGenFakeRepo) FindByID(_ context.Context, _ int64) (*domain.QuestionGeneration, error) {
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	return r.findByID, nil
}

func (r *qGenFakeRepo) MarkRunning(_ context.Context, _ int64) error { return nil }

func (r *qGenFakeRepo) SaveProgress(_ context.Context, _ int64, _ int, _ int64, _ string) error {
	return nil
}

func (r *qGenFakeRepo) FinishCompleted(_ context.Context, _ int64, _ *domain.QuestionBatch, _ *[]domain.Question) error {
	return nil
}

func (r *qGenFakeRepo) FinishTerminal(_ context.Context, _ int64, _, _ string) error { return nil }

func (r *qGenFakeRepo) RequestCancel(_ context.Context, id int64) error {
	r.cancelCalled = true
	r.cancelID = id
	return r.cancelErr
}

func (r *qGenFakeRepo) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return r.deleteErr
}

// fakeGenEnqueuer 是 GenerationEnqueuer 的假实现。
type fakeGenEnqueuer struct {
	called bool
	lastID int64
	err    error
}

func (e *fakeGenEnqueuer) EnqueueGenerate(_ context.Context, id int64) error {
	e.called = true
	e.lastID = id
	return e.err
}

// fakeEnabledModelProvider 是 llm.EnabledModelProvider 的假实现。
type fakeEnabledModelProvider struct {
	cfg llm.ModelConfig
	err error
}

func (p *fakeEnabledModelProvider) GetEnabledModel(_ context.Context) (llm.ModelConfig, error) {
	return p.cfg, p.err
}

func newQGenSvc(repo *qGenFakeRepo, dimRepo *qFakeDimRepo, enq *fakeGenEnqueuer, provider *fakeEnabledModelProvider) service.QuestionGenerationService {
	return service.NewQuestionGenerationService(repo, dimRepo, repository.QuestionBatchRepository(nil), enq, provider)
}

func okProvider() *fakeEnabledModelProvider {
	return &fakeEnabledModelProvider{cfg: llm.ModelConfig{Provider: "deepseek", ModelID: "deepseek-chat"}}
}

func validGenInput() ([]int64, int) { return []int64{201, 202}, 12 }

// ---------- CreateGeneration：校验 ----------

// TestCreateGenerationValidatesCount 核心断言：count=4/31 越界与非整数（0/负数）返 1400，
// 恰 5 与 30 通过。
func TestCreateGenerationValidatesCount(t *testing.T) {
	for _, bad := range []int{4, 31, 0, -5} {
		repo := &qGenFakeRepo{}
		svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())
		_, err := svc.CreateGeneration(context.Background(), []int64{201}, bad)
		wantQCode(t, err, 1400)
		if len(repo.created) != 0 {
			t.Fatalf("count=%d 不应建行", bad)
		}
	}
	for _, ok := range []int{5, 30} {
		repo := &qGenFakeRepo{}
		enq := &fakeGenEnqueuer{}
		svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, enq, okProvider())
		res, err := svc.CreateGeneration(context.Background(), []int64{201}, ok)
		if err != nil {
			t.Fatalf("count=%d 应通过: %v", ok, err)
		}
		if res.Status != domain.QuestionGenStatusQueued {
			t.Fatalf("count=%d status want QUEUED, got %s", ok, res.Status)
		}
		if len(repo.created) != 1 || repo.created[0].Count != ok {
			t.Fatalf("count=%d 建行不符: %+v", ok, repo.created)
		}
		if !enq.called {
			t.Fatalf("count=%d 应投递任务", ok)
		}
	}
}

// TestCreateGenerationDimensionEmpty dimension_ids 空集合返 1400（至少 1 项）。
func TestCreateGenerationDimensionEmpty(t *testing.T) {
	repo := &qGenFakeRepo{}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())
	_, err := svc.CreateGeneration(context.Background(), nil, 12)
	wantQCode(t, err, 1400)
	if len(repo.created) != 0 {
		t.Fatal("空维度集合不应建行")
	}
}

// TestCreateGenerationDimensionNotEnabled 核心断言：含停用维度/非 AI_MGMT 维度/
// 不存在维度返 1400 且不建行。
func TestCreateGenerationDimensionNotEnabled(t *testing.T) {
	cases := []struct {
		name    string
		dimRepo *qFakeDimRepo
		ids     []int64
	}{
		{"disabled", &qFakeDimRepo{dims: []domain.Dimension{
			{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: false},
		}}, []int64{201, 202}},
		{"other module", &qFakeDimRepo{dims: []domain.Dimension{
			{ID: 205, Name: "外层维度", ModuleCode: "EVAL", Enabled: true},
		}}, []int64{205}},
		{"not found", &qFakeDimRepo{dims: aiMgmtDims()}, []int64{201, 999}},
	}
	for _, tc := range cases {
		repo := &qGenFakeRepo{}
		svc := newQGenSvc(repo, tc.dimRepo, &fakeGenEnqueuer{}, okProvider())
		_, err := svc.CreateGeneration(context.Background(), tc.ids, 12)
		wantQCode(t, err, 1400)
		if len(repo.created) != 0 {
			t.Fatalf("case %s 不应建行", tc.name)
		}
	}
}

// TestCreateGenerationDeduplicatesDimensions 重复 ID 去重：快照只留一份且投递正常。
func TestCreateGenerationDeduplicatesDimensions(t *testing.T) {
	repo := &qGenFakeRepo{}
	enq := &fakeGenEnqueuer{}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, enq, okProvider())
	if _, err := svc.CreateGeneration(context.Background(), []int64{201, 201, 202}, 10); err != nil {
		t.Fatalf("去重后应通过: %v", err)
	}
	if len(repo.created) != 1 || repo.created[0].DimensionIDs != "[201,202]" {
		t.Fatalf("快照应去重: %+v", repo.created)
	}
}

// TestCreateGenerationLLMNotConfigured 核心断言：provider 返回 ErrLLMModelNotEnabled
// 返 1709 且不建行。
func TestCreateGenerationLLMNotConfigured(t *testing.T) {
	repo := &qGenFakeRepo{}
	enq := &fakeGenEnqueuer{}
	provider := &fakeEnabledModelProvider{err: service.ErrLLMModelNotEnabled}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, enq, provider)

	_, err := svc.CreateGeneration(context.Background(), []int64{201}, 12)
	wantQCode(t, err, 1709)
	if len(repo.created) != 0 {
		t.Fatal("LLM 未配置不应建行")
	}
	if enq.called {
		t.Fatal("LLM 未配置不应投递")
	}
}

// TestCreateGenerationProviderError provider 非 1709 类错误透传 1500 语义（非业务错误）。
func TestCreateGenerationProviderError(t *testing.T) {
	provider := &fakeEnabledModelProvider{err: errors.New("db down")}
	svc := newQGenSvc(&qGenFakeRepo{}, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, provider)
	if _, err := svc.CreateGeneration(context.Background(), []int64{201}, 12); err == nil {
		t.Fatal("want error, got nil")
	}
}

// TestCreateGenerationSuccess 核心路径：QUEUED 行字段（维度快照 JSON、count）与响应组装。
func TestCreateGenerationSuccess(t *testing.T) {
	repo := &qGenFakeRepo{}
	enq := &fakeGenEnqueuer{}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, enq, okProvider())
	ids, count := validGenInput()

	res, err := svc.CreateGeneration(context.Background(), ids, count)
	if err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}
	row := repo.created[0]
	if row.Status != domain.QuestionGenStatusQueued || row.Count != count || row.DimensionIDs != "[201,202]" {
		t.Fatalf("行字段不符: %+v", row)
	}
	if row.GeneratedCount != 0 || row.CurrentDimensionID != 0 || row.BatchID != 0 || row.ErrorCode != "" {
		t.Fatalf("初始行不应携带进度/批次/错误: %+v", row)
	}
	if res.GenerationID != "3001" || res.Status != domain.QuestionGenStatusQueued {
		t.Fatalf("响应不符: %+v", res)
	}
	if !enq.called || enq.lastID != row.ID {
		t.Fatalf("投递不符: called=%v id=%d want %d", enq.called, enq.lastID, row.ID)
	}
}

// TestCreateGenerationEnqueueFailCleansUp 核心断言：投递失败时行已删、返回 1500。
func TestCreateGenerationEnqueueFailCleansUp(t *testing.T) {
	repo := &qGenFakeRepo{}
	enq := &fakeGenEnqueuer{err: errors.New("redis down")}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, enq, okProvider())

	_, err := svc.CreateGeneration(context.Background(), []int64{201}, 12)
	wantQCode(t, err, 1500)
	if len(repo.deleted) != 1 || repo.deleted[0] != repo.created[0].ID {
		t.Fatalf("投递失败应删行: deleted=%v", repo.deleted)
	}
}

// TestCreateGenerationCreateRepoError 建行仓储错误透传非业务错误。
func TestCreateGenerationCreateRepoError(t *testing.T) {
	svc := newQGenSvc(&qGenFakeRepo{createErr: gorm.ErrInvalidDB}, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())
	if _, err := svc.CreateGeneration(context.Background(), []int64{201}, 12); err == nil {
		t.Fatal("want error, got nil")
	}
}

// TestCreateGenerationDimRepoError 维度读通道错误透传非业务错误。
func TestCreateGenerationDimRepoError(t *testing.T) {
	svc := newQGenSvc(&qGenFakeRepo{}, &qFakeDimRepo{err: gorm.ErrInvalidDB}, &fakeGenEnqueuer{}, okProvider())
	if _, err := svc.CreateGeneration(context.Background(), []int64{201}, 12); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- GetProgress ----------

func runningGeneration() *domain.QuestionGeneration {
	return &domain.QuestionGeneration{
		ID:                 3001,
		DimensionIDs:       "[201,202]",
		Count:              12,
		Status:             domain.QuestionGenStatusRunning,
		GeneratedCount:     6,
		CurrentDimensionID: 201,
		Staging:            "[{}]",
	}
}

// TestGetProgressRunning 核心断言（BR4）：RUNNING 态回显当前维度名，无批次与错误字段。
func TestGetProgressRunning(t *testing.T) {
	repo := &qGenFakeRepo{findByID: runningGeneration()}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())

	res, err := svc.GetProgress(context.Background(), 3001)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if res.GenerationID != "3001" || res.Status != "RUNNING" || res.GeneratedCount != 6 || res.Count != 12 {
		t.Fatalf("基础字段不符: %+v", res)
	}
	if res.CurrentDimensionID != "201" || res.CurrentDimensionName != "授权与分工" {
		t.Fatalf("当前维度回显不符: %+v", res)
	}
	if res.BatchID != "" || res.BatchNo != "" || res.ErrorCode != "" {
		t.Fatalf("RUNNING 不应带批次/错误字段: %+v", res)
	}
}

// TestGetProgressRunningSoftDeletedDimension 软删维度回传存量名（复用双查回填）。
func TestGetProgressRunningSoftDeletedDimension(t *testing.T) {
	g := runningGeneration()
	repo := &qGenFakeRepo{findByID: g}
	svc := newQGenSvc(repo, &qFakeDimRepo{unscopedNames: map[int64]string{201: "授权与分工"}}, &fakeGenEnqueuer{}, okProvider())

	res, err := svc.GetProgress(context.Background(), 3001)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if res.CurrentDimensionName != "授权与分工" {
		t.Fatalf("软删维度应回传存量名, got %q", res.CurrentDimensionName)
	}
}

// TestGetProgressQueuedNotStarted QUEUED 未开始：current_dimension_id "0"、名称空串。
func TestGetProgressQueuedNotStarted(t *testing.T) {
	g := &domain.QuestionGeneration{ID: 3001, DimensionIDs: "[201]", Count: 10, Status: domain.QuestionGenStatusQueued}
	repo := &qGenFakeRepo{findByID: g}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())

	res, err := svc.GetProgress(context.Background(), 3001)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if res.CurrentDimensionID != "0" || res.CurrentDimensionName != "" {
		t.Fatalf("未开始维度提示不符: %+v", res)
	}
}

// TestGetProgressCompleted 核心断言（BR5）：COMPLETED 返回 batch_id/batch_no 且无 error_code。
func TestGetProgressCompleted(t *testing.T) {
	g := runningGeneration()
	g.Status = domain.QuestionGenStatusCompleted
	g.GeneratedCount = 12
	g.BatchID = 901
	batchRepo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{
		901: {ID: 901, BatchNo: "#G0925"},
	}}
	svc := service.NewQuestionGenerationService(&qGenFakeRepo{findByID: g}, &qFakeDimRepo{dims: aiMgmtDims()}, batchRepo, &fakeGenEnqueuer{}, okProvider())

	res, err := svc.GetProgress(context.Background(), 3001)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if res.Status != "COMPLETED" || res.BatchID != "901" || res.BatchNo != "#G0925" {
		t.Fatalf("完成态批次字段不符: %+v", res)
	}
	if res.ErrorCode != "" {
		t.Fatalf("完成态不应带 error_code: %+v", res)
	}
}

// TestGetProgressFailed 失败态：error_code 透传，无批次字段。
func TestGetProgressFailed(t *testing.T) {
	g := runningGeneration()
	g.Status = domain.QuestionGenStatusFailed
	g.ErrorCode = domain.QuestionGenErrorLLMTimeout
	repo := &qGenFakeRepo{findByID: g}
	svc := newQGenSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()}, &fakeGenEnqueuer{}, okProvider())

	res, err := svc.GetProgress(context.Background(), 3001)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if res.ErrorCode != "LLM_TIMEOUT" || res.BatchID != "" {
		t.Fatalf("失败态字段不符: %+v", res)
	}
}

// TestGetProgressNotFound generation 不存在返 1703。
func TestGetProgressNotFound(t *testing.T) {
	svc := newQGenSvc(&qGenFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{}, &fakeGenEnqueuer{}, okProvider())
	_, err := svc.GetProgress(context.Background(), 999)
	wantQCode(t, err, 1703)
}

// TestGetProgressRepoError 仓储错误透传非业务错误。
func TestGetProgressRepoError(t *testing.T) {
	svc := newQGenSvc(&qGenFakeRepo{findByIDErr: gorm.ErrInvalidDB}, &qFakeDimRepo{}, &fakeGenEnqueuer{}, okProvider())
	if _, err := svc.GetProgress(context.Background(), 3001); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- CancelGeneration ----------

// TestCancelGenerationSuccess 取消成功：委托 repo.RequestCancel。
func TestCancelGenerationSuccess(t *testing.T) {
	repo := &qGenFakeRepo{findByID: runningGeneration()}
	svc := newQGenSvc(repo, &qFakeDimRepo{}, &fakeGenEnqueuer{}, okProvider())
	if err := svc.CancelGeneration(context.Background(), 3001); err != nil {
		t.Fatalf("CancelGeneration: %v", err)
	}
	if !repo.cancelCalled || repo.cancelID != 3001 {
		t.Fatal("repo.RequestCancel 未按预期调用")
	}
}

// TestCancelGenerationNotFound 取消目标不存在返 1703。
func TestCancelGenerationNotFound(t *testing.T) {
	svc := newQGenSvc(&qGenFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{}, &fakeGenEnqueuer{}, okProvider())
	err := svc.CancelGeneration(context.Background(), 999)
	wantQCode(t, err, 1703)
}

// TestCancelGenerationRepoError 取消仓储错误透传。
func TestCancelGenerationRepoError(t *testing.T) {
	repo := &qGenFakeRepo{findByID: runningGeneration(), cancelErr: repository.ErrAlreadyTerminal}
	svc := newQGenSvc(repo, &qFakeDimRepo{}, &fakeGenEnqueuer{}, okProvider())
	if err := svc.CancelGeneration(context.Background(), 3001); !errors.Is(err, repository.ErrAlreadyTerminal) {
		t.Fatalf("want ErrAlreadyTerminal 透传, got %v", err)
	}
}
