// question_test 题库 service 层测试：fake repo 注入（dimFakeRepo 先例），
// 不连真库。覆盖 03 §3.1/§3.2/§3.3/§3.4/§3.6 的业务规则与核心断言。
package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// qFakeRepo 是 repository.QuestionRepository 的测试假实现：返回值字段挂载 +
// 探针断言。写路径成功后 FindByID 优先返回 afterWrite 快照，模拟回读最新行。
type qFakeRepo struct {
	list      []domain.Question
	listTotal int64
	listErr   error

	findByID       *domain.Question
	findByIDErr    error
	findByIDCalls  int
	afterWrite     *domain.Question
	afterWriteFrom int // FindByID 第 N 次调用起返回 afterWrite（1 起）

	updateRows int64
	updateErr  error
	updatesGot map[string]any
	updateID   int64

	deleteRows int64
	deleteErr  error
	deleteID   int64
	deleteCall bool
}

var _ repository.QuestionRepository = (*qFakeRepo)(nil)
var _ repository.DimensionRepository = (*qFakeDimRepo)(nil)

func (r *qFakeRepo) ListPage(_ context.Context, _ string, _ int64, _ bool, _, _ string, _, _ int) ([]domain.Question, int64, error) {
	return r.list, r.listTotal, r.listErr
}

func (r *qFakeRepo) FindByID(_ context.Context, id int64) (*domain.Question, error) {
	r.findByIDCalls++
	if r.afterWrite != nil && r.findByIDCalls >= r.afterWriteFrom {
		return r.afterWrite, nil
	}
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	return r.findByID, nil
}

func (r *qFakeRepo) UpdateWithVersion(_ context.Context, id int64, _ int, updates map[string]any) (int64, error) {
	r.updateID = id
	r.updatesGot = updates
	return r.updateRows, r.updateErr
}

func (r *qFakeRepo) SoftDeleteWithVersion(_ context.Context, id int64, _ int) (int64, error) {
	r.deleteCall = true
	r.deleteID = id
	return r.deleteRows, r.deleteErr
}

func (r *qFakeRepo) ListByIDs(_ context.Context, _ []int64) ([]domain.Question, error) {
	return nil, nil
}

func (r *qFakeRepo) MaxQuestionSeq(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

// qFakeDimRepo 是 question service 用的 DimensionRepository 假实现：
// ListAll 返回可配置维度集（编辑校验过滤 AI_MGMT+enabled），unscopedNames 模拟
// 软删行存量名称二查回填。
type qFakeDimRepo struct {
	dims          []domain.Dimension
	err           error
	unscopedNames map[int64]string
	unscopedErr   error
}

func (r *qFakeDimRepo) ListAll(_ context.Context) ([]domain.Dimension, error) { return r.dims, r.err }
func (r *qFakeDimRepo) FindByID(_ context.Context, _ int64) (*domain.Dimension, error) {
	return nil, nil
}
func (r *qFakeDimRepo) FindByCodeExcludingDeleted(_ context.Context, _ string) (*domain.Dimension, error) {
	return nil, nil
}
func (r *qFakeDimRepo) Create(_ context.Context, _ *domain.Dimension) error { return nil }
func (r *qFakeDimRepo) UpdateWithVersion(_ context.Context, _ int64, _ int, _ map[string]any) (int64, error) {
	return 0, nil
}
func (r *qFakeDimRepo) SoftDeleteWithVersion(_ context.Context, _ int64, _ int) (int64, error) {
	return 0, nil
}
func (r *qFakeDimRepo) GetActivitySetting(_ context.Context) (*domain.DimensionSetting, error) {
	return nil, nil
}
func (r *qFakeDimRepo) UpdateActivitySetting(_ context.Context, _, _ int) error { return nil }
func (r *qFakeDimRepo) ListEnabledFullByDataSource(_ context.Context, _ string) ([]domain.Dimension, error) {
	return nil, nil
}
func (r *qFakeDimRepo) CountEnabledByGroupCode(_ context.Context, _ string) (map[string]int, error) {
	return nil, nil
}
func (r *qFakeDimRepo) ListNamesByIDsUnscoped(_ context.Context, _ []int64) (map[int64]string, error) {
	return r.unscopedNames, r.unscopedErr
}

func newQuestionSvc(repo *qFakeRepo, dimRepo *qFakeDimRepo) service.QuestionService {
	return service.NewQuestionService(repo, dimRepo, &qBatchFakeRepo{})
}

// aiQuestion 构造一枚可编辑态 AI 题默认形状。
func aiQuestion() *domain.Question {
	return &domain.Question{
		ID:          101,
		QuestionNo:  "Q-AG-0001",
		Source:      domain.QuestionSourceAI,
		DimensionID: 201,
		Scenario:    "情境描述",
		Requirement: "作答要求",
		FocusPoint:  "考察点",
		Status:      domain.QuestionStatusActive,
		BatchID:     301,
		Version:     2,
	}
}

// aiMgmtDims 两枚启用 AI_MGMT 维度（201 命中、202 备选）。
func aiMgmtDims() []domain.Dimension {
	return []domain.Dimension{
		{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: true},
		{ID: 202, Name: "风险与担责", ModuleCode: domain.ModuleAIMgmt, Enabled: true},
	}
}

func validUpdateInput() service.UpdateQuestionInput {
	return service.UpdateQuestionInput{
		ID:          101,
		DimensionID: 201,
		Scenario:    "新情境",
		Requirement: "新作答要求",
		FocusPoint:  "新考察点",
		Version:     2,
	}
}

// ---------- ListQuestions ----------

// TestListQuestionsAssembles 列表组装：dimension_name 回填、answer_mode 派生、
// 分页结构透传。
func TestListQuestionsAssembles(t *testing.T) {
	scale := domain.Question{
		ID: 102, QuestionNo: "Q-Scale-0001", Source: domain.QuestionSourceScale,
		DimensionID: 401, Scenario: "题项陈述", Status: domain.QuestionStatusActive, Version: 1,
	}
	repo := &qFakeRepo{list: []domain.Question{*aiQuestion(), scale}, listTotal: 32}
	dimRepo := &qFakeDimRepo{dims: []domain.Dimension{
		{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: true},
		{ID: 401, Name: "调停型", ModuleCode: domain.ModuleEnneagram, Enabled: true},
	}}
	svc := newQuestionSvc(repo, dimRepo)

	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{
		Source: domain.QuestionSourceAI, Page: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.Total != 32 || res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("paging mismatch: %+v", res)
	}
	if len(res.List) != 2 {
		t.Fatalf("want 2 rows, got %d", len(res.List))
	}
	row := res.List[0]
	if row.ID != "101" || row.DimensionID != "201" || row.DimensionName != "授权与分工" {
		t.Fatalf("row0 mismatch: %+v", row)
	}
	if row.AnswerMode != "CHAT" {
		t.Fatalf("AI answer_mode want CHAT, got %s", row.AnswerMode)
	}
	if res.List[1].AnswerMode != "LIKERT5" {
		t.Fatalf("SCALE answer_mode want LIKERT5, got %s", res.List[1].AnswerMode)
	}
}

// TestListQuestionsEmptyPage 空页：list 归一空切片非 nil（前端契约稳定）。
func TestListQuestionsEmptyPage(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{}, &qFakeDimRepo{})
	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{
		Source: domain.QuestionSourceAI, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.List == nil {
		t.Fatal("empty page list should be non-nil empty slice")
	}
}

// TestListQuestionsDisabledDimensionName 维度停用回传存量名称（活跃 map 未命中
// 由同表全量兜底回填）。
func TestListQuestionsDisabledDimensionName(t *testing.T) {
	repo := &qFakeRepo{list: []domain.Question{*aiQuestion()}, listTotal: 1}
	dimRepo := &qFakeDimRepo{dims: []domain.Dimension{
		{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: false},
	}}
	svc := newQuestionSvc(repo, dimRepo)
	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceAI})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.List[0].DimensionName != "授权与分工" {
		t.Fatalf("want存量名称, got %q", res.List[0].DimensionName)
	}
}

// TestListQuestionsSoftDeletedDimensionName 题目挂已软删维度：活跃 map 未命中后
// Unscoped 二查回填存量名称（specs §4.1.2 E 维度已软删时回传存量名称）。
func TestListQuestionsSoftDeletedDimensionName(t *testing.T) {
	repo := &qFakeRepo{list: []domain.Question{*aiQuestion()}, listTotal: 1}
	dimRepo := &qFakeDimRepo{unscopedNames: map[int64]string{201: "授权与分工"}}
	svc := newQuestionSvc(repo, dimRepo)
	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceAI})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.List[0].DimensionName != "授权与分工" {
		t.Fatalf("want存量名称, got %q", res.List[0].DimensionName)
	}
}

// TestListQuestionsUnknownDimensionName 双查均无的维度名回空串。
func TestListQuestionsUnknownDimensionName(t *testing.T) {
	repo := &qFakeRepo{list: []domain.Question{*aiQuestion()}, listTotal: 1}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceAI})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.List[0].DimensionName != "" {
		t.Fatalf("want empty name, got %q", res.List[0].DimensionName)
	}
}

// TestListQuestionsRepoError 仓储错误透传非业务错误。
func TestListQuestionsRepoError(t *testing.T) {
	repo := &qFakeRepo{listErr: gorm.ErrInvalidDB}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	if _, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceAI}); err == nil {
		t.Fatal("want error, got nil")
	}
}

// TestListQuestionsScaleTab 量表 tab：answer_mode=LIKERT5、摘要同 50 rune 规则。
func TestListQuestionsScaleTab(t *testing.T) {
	scale := domain.Question{
		ID: 102, QuestionNo: "Q-Scale-0001", Source: domain.QuestionSourceScale,
		DimensionID: 401, Scenario: strings.Repeat("陈", 80), Status: domain.QuestionStatusActive,
	}
	repo := &qFakeRepo{list: []domain.Question{scale}, listTotal: 1}
	dimRepo := &qFakeDimRepo{dims: []domain.Dimension{
		{ID: 401, Name: "调停型", ModuleCode: domain.ModuleEnneagram, Enabled: true},
	}}
	svc := newQuestionSvc(repo, dimRepo)
	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceScale})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	if res.List[0].AnswerMode != "LIKERT5" {
		t.Fatalf("want LIKERT5, got %s", res.List[0].AnswerMode)
	}
	if got := len([]rune(res.List[0].Summary)); got != 50 {
		t.Fatalf("SCALE summary want 50 runes, got %d", got)
	}
}

// ---------- GetQuestion ----------

// TestGetQuestionFound 详情字段全集（03 §3.2 json tag 权威），batch_no 经批次仓储回填。
func TestGetQuestionFound(t *testing.T) {
	q := aiQuestion()
	q.RejectReason = "存在偏见"
	q.ReferenceCount = 3
	q.CreatedAt = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	q.UpdatedAt = time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	batch := pendingBatch(301, 1)
	svc := service.NewQuestionService(&qFakeRepo{findByID: q}, &qFakeDimRepo{dims: aiMgmtDims()}, &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{301: batch}})

	d, err := svc.GetQuestion(context.Background(), 101)
	if err != nil {
		t.Fatalf("GetQuestion: %v", err)
	}
	if d.ID != "101" || d.QuestionNo != "Q-AG-0001" || d.Source != "AI" {
		t.Fatalf("meta mismatch: %+v", d)
	}
	if d.DimensionID != "201" || d.DimensionName != "授权与分工" || d.AnswerMode != "CHAT" {
		t.Fatalf("dimension mismatch: %+v", d)
	}
	if d.Scenario != "情境描述" || d.Requirement != "作答要求" || d.FocusPoint != "考察点" {
		t.Fatalf("text mismatch: %+v", d)
	}
	if d.RejectReason != "存在偏见" || d.ReferenceCount != 3 || d.Version != 2 {
		t.Fatalf("misc mismatch: %+v", d)
	}
	if d.BatchID != "301" || d.BatchNo != "#G0925" {
		t.Fatalf("batch mismatch: %+v", d)
	}
}

// TestGetQuestionBatchNoMissing 批次查不到（已物理清理）降级空串，batch_id 照常透传。
func TestGetQuestionBatchNoMissing(t *testing.T) {
	q := aiQuestion()
	svc := newQuestionSvc(&qFakeRepo{findByID: q}, &qFakeDimRepo{})
	d, err := svc.GetQuestion(context.Background(), 101)
	if err != nil {
		t.Fatalf("GetQuestion: %v", err)
	}
	if d.BatchID != "301" || d.BatchNo != "" {
		t.Fatalf("want batch_id 301 with empty batch_no, got %+v", d)
	}
}

// TestGetQuestionNotFound 不存在（含软删）返 1701。
func TestGetQuestionNotFound(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{})
	_, err := svc.GetQuestion(context.Background(), 999)
	wantQCode(t, err, 1701)
}

// TestGetQuestionRepoError 仓储错误透传。
func TestGetQuestionRepoError(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrInvalidDB}, &qFakeDimRepo{})
	if _, err := svc.GetQuestion(context.Background(), 1); err == nil {
		t.Fatal("want error, got nil")
	}
}

// TestGetQuestionScaleAnswerMode SCALE 详情 answer_mode=LIKERT5。
func TestGetQuestionScaleAnswerMode(t *testing.T) {
	q := aiQuestion()
	q.Source = domain.QuestionSourceScale
	q.ID = 102
	svc := newQuestionSvc(&qFakeRepo{findByID: q}, &qFakeDimRepo{})
	d, err := svc.GetQuestion(context.Background(), 102)
	if err != nil {
		t.Fatalf("GetQuestion: %v", err)
	}
	if d.AnswerMode != "LIKERT5" {
		t.Fatalf("want LIKERT5, got %s", d.AnswerMode)
	}
}

// ---------- UpdateQuestion ----------

// TestUpdateQuestionSuccess 编辑成功：写路径字段集正确（03 §3.3 不可变字段忽略）、
// version+1、响应无 status 字段。
func TestUpdateQuestionSuccess(t *testing.T) {
	after := aiQuestion()
	after.Version = 3
	after.Scenario = "新情境"
	after.UpdatedAt = time.Date(2026, 9, 23, 11, 0, 0, 0, time.UTC)
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 1, afterWrite: after, afterWriteFrom: 2}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})

	res, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	if err != nil {
		t.Fatalf("UpdateQuestion: %v", err)
	}
	if res.ID != "101" || res.Version != 3 || res.Status != "" {
		t.Fatalf("result mismatch: %+v", res)
	}
	for _, col := range []string{"scenario", "requirement", "focus_point", "dimension_id"} {
		if _, ok := repo.updatesGot[col]; !ok {
			t.Fatalf("updates missing column %s: %v", col, repo.updatesGot)
		}
	}
	for _, col := range []string{"question_no", "source", "status", "batch_id", "reference_count"} {
		if _, ok := repo.updatesGot[col]; ok {
			t.Fatalf("updates must not touch column %s: %v", col, repo.updatesGot)
		}
	}
}

// TestUpdateQuestionRejectsScale 量表题编辑返 1707（BR2，03 §3.3）。
func TestUpdateQuestionRejectsScale(t *testing.T) {
	q := aiQuestion()
	q.Source = domain.QuestionSourceScale
	repo := &qFakeRepo{findByID: q}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1707)
}

// TestUpdateQuestionRejectsRejected 已驳回题不可走编辑（重新送审路径）。
func TestUpdateQuestionRejectsRejected(t *testing.T) {
	q := aiQuestion()
	q.Status = domain.QuestionStatusRejected
	repo := &qFakeRepo{findByID: q}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1707)
}

// TestUpdateQuestionRejectsPending 待审核题在批次中管理，编辑拒 1707。
func TestUpdateQuestionRejectsPending(t *testing.T) {
	q := aiQuestion()
	q.Status = domain.QuestionStatusPending
	repo := &qFakeRepo{findByID: q}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1707)
}

// TestUpdateQuestionDimensionNotEnabled dimension_id 传停用/非 AI_MGMT 维度返 1400
// （BR4，限当前启用集合）。
func TestUpdateQuestionDimensionNotEnabled(t *testing.T) {
	cases := []struct {
		name string
		dims []domain.Dimension
		in   int64
	}{
		{"disabled AI_MGMT", []domain.Dimension{
			{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: false}}, 201},
		{"non AI_MGMT module", []domain.Dimension{
			{ID: 203, Name: "底层能力", ModuleCode: domain.ModuleAIUsage, Enabled: true}}, 203},
		{"unknown dimension", nil, 999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &qFakeRepo{findByID: aiQuestion()}
			svc := newQuestionSvc(repo, &qFakeDimRepo{dims: tc.dims})
			in := validUpdateInput()
			in.DimensionID = tc.in
			_, err := svc.UpdateQuestion(context.Background(), in)
			wantQCode(t, err, 1400)
		})
	}
}

// TestUpdateQuestionLengthBounds 三文本长度边界（BR3，03 §3.3）：恰上限通过、
// 越上限 1 rune 返 1400、空串返 1400。
func TestUpdateQuestionLengthBounds(t *testing.T) {
	build := func(scenario, requirement, focus int) service.UpdateQuestionInput {
		in := validUpdateInput()
		in.Scenario = strings.Repeat("境", scenario)
		in.Requirement = strings.Repeat("求", requirement)
		in.FocusPoint = strings.Repeat("点", focus)
		return in
	}
	after := aiQuestion()
	after.Version = 3
	ok := func(t *testing.T, in service.UpdateQuestionInput) {
		t.Helper()
		repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 1, afterWrite: after, afterWriteFrom: 2}
		svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
		if _, err := svc.UpdateQuestion(context.Background(), in); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	}
	bad := func(t *testing.T, in service.UpdateQuestionInput) {
		t.Helper()
		repo := &qFakeRepo{findByID: aiQuestion()}
		svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
		_, err := svc.UpdateQuestion(context.Background(), in)
		wantQCode(t, err, 1400)
	}

	ok(t, build(1000, 2000, 500)) // 恰上限
	bad(t, build(1001, 2000, 500))
	bad(t, build(1000, 2001, 500))
	bad(t, build(1000, 2000, 501))
	bad(t, build(0, 2000, 500))
	bad(t, build(1000, 0, 500))
	bad(t, build(1000, 2000, 0))
}

// TestUpdateQuestionNotFound 目标不存在返 1701。
func TestUpdateQuestionNotFound(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1701)
}

// TestUpdateQuestionVersionConflict 乐观锁冲突（BR5）：题仍在但 RowsAffected=0 返 1713。
func TestUpdateQuestionVersionConflict(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 0}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1713)
}

// TestUpdateQuestionVersionConflictButDeleted RowsAffected=0 且题已消失（并发删除）返 1701。
func TestUpdateQuestionVersionConflictButDeleted(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 0}
	repo.findByIDCalls = 1 // 令冲突复核的第二次 FindByID 直接走 findByIDErr
	repo.findByIDErr = gorm.ErrRecordNotFound
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.UpdateQuestion(context.Background(), validUpdateInput())
	wantQCode(t, err, 1701)
}

// TestUpdateQuestionRepoError 读路径仓储错误透传。
func TestUpdateQuestionRepoError(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrInvalidDB}, &qFakeDimRepo{dims: aiMgmtDims()})
	if _, err := svc.UpdateQuestion(context.Background(), validUpdateInput()); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- ToggleQuestionStatus ----------

// TestToggleQuestionSuccess 启停成功：仅写 status、version+1、响应含新 status。
func TestToggleQuestionSuccess(t *testing.T) {
	after := aiQuestion()
	after.Status = domain.QuestionStatusDisabled
	after.Version = 3
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 1, afterWrite: after, afterWriteFrom: 2}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	res, err := svc.ToggleQuestionStatus(context.Background(), 101, domain.QuestionStatusDisabled, 2)
	if err != nil {
		t.Fatalf("ToggleQuestionStatus: %v", err)
	}
	if res.ID != "101" || res.Status != "DISABLED" || res.Version != 3 {
		t.Fatalf("result mismatch: %+v", res)
	}
	if repo.updatesGot["status"] != domain.QuestionStatusDisabled {
		t.Fatalf("updates.status mismatch: %v", repo.updatesGot)
	}
}

// TestToggleQuestionSameTarget target 与当前状态相同返 1400（03 §3.4）。
func TestToggleQuestionSameTarget(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion()}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	_, err := svc.ToggleQuestionStatus(context.Background(), 101, domain.QuestionStatusActive, 2)
	wantQCode(t, err, 1400)
}

// TestToggleQuestionInvalidTarget target 非 ACTIVE/DISABLED 返 1400。
func TestToggleQuestionInvalidTarget(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion()}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	for _, target := range []string{domain.QuestionStatusPending, domain.QuestionStatusRejected, "BOGUS", ""} {
		_, err := svc.ToggleQuestionStatus(context.Background(), 101, target, 2)
		wantQCode(t, err, 1400)
	}
}

// TestToggleQuestionInvalidCurrent 前置状态非法（PENDING/REJECTED）返 1708（03 §3.4）。
func TestToggleQuestionInvalidCurrent(t *testing.T) {
	for _, st := range []string{domain.QuestionStatusPending, domain.QuestionStatusRejected} {
		q := aiQuestion()
		q.Status = st
		repo := &qFakeRepo{findByID: q}
		svc := newQuestionSvc(repo, &qFakeDimRepo{})
		_, err := svc.ToggleQuestionStatus(context.Background(), 101, domain.QuestionStatusDisabled, 2)
		wantQCode(t, err, 1708)
	}
}

// TestToggleQuestionNotFound 目标不存在返 1701。
func TestToggleQuestionNotFound(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{})
	_, err := svc.ToggleQuestionStatus(context.Background(), 999, domain.QuestionStatusDisabled, 2)
	wantQCode(t, err, 1701)
}

// TestToggleQuestionVersionConflict 乐观锁冲突返 1713。
func TestToggleQuestionVersionConflict(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 0}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	_, err := svc.ToggleQuestionStatus(context.Background(), 101, domain.QuestionStatusDisabled, 2)
	wantQCode(t, err, 1713)
}

// TestToggleQuestionConflictButDeleted 冲突且题消失返 1701。
func TestToggleQuestionConflictButDeleted(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), updateRows: 0}
	repo.findByIDCalls = 1
	repo.findByIDErr = gorm.ErrRecordNotFound
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	_, err := svc.ToggleQuestionStatus(context.Background(), 101, domain.QuestionStatusDisabled, 2)
	wantQCode(t, err, 1701)
}

// ---------- DeleteQuestion ----------

// TestDeleteQuestionSuccess 删除成功（SCALE 与 DISABLED 亦无额外限制）。
func TestDeleteQuestionSuccess(t *testing.T) {
	for _, q := range []*domain.Question{aiQuestion()} {
		repo := &qFakeRepo{findByID: q, deleteRows: 1}
		svc := newQuestionSvc(repo, &qFakeDimRepo{})
		if err := svc.DeleteQuestion(context.Background(), q.ID, q.Version); err != nil {
			t.Fatalf("DeleteQuestion: %v", err)
		}
		if !repo.deleteCall || repo.deleteID != q.ID {
			t.Fatal("SoftDeleteWithVersion not invoked as expected")
		}
	}
	scale := aiQuestion()
	scale.Source = domain.QuestionSourceScale
	repo := &qFakeRepo{findByID: scale, deleteRows: 1}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	if err := svc.DeleteQuestion(context.Background(), scale.ID, scale.Version); err != nil {
		t.Fatalf("DeleteQuestion scale: %v", err)
	}
}

// TestDeleteQuestionPending PENDING 题删除返 1708（BR6）。
func TestDeleteQuestionPending(t *testing.T) {
	q := aiQuestion()
	q.Status = domain.QuestionStatusPending
	repo := &qFakeRepo{findByID: q}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	err := svc.DeleteQuestion(context.Background(), 101, 2)
	wantQCode(t, err, 1708)
}

// TestDeleteQuestionReferenced 被引用题删除返 1705（BR1）。
func TestDeleteQuestionReferenced(t *testing.T) {
	q := aiQuestion()
	q.ReferenceCount = 3
	repo := &qFakeRepo{findByID: q}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	err := svc.DeleteQuestion(context.Background(), 101, 2)
	wantQCode(t, err, 1705)
}

// TestDeleteQuestionNotFound 目标不存在返 1701。
func TestDeleteQuestionNotFound(t *testing.T) {
	svc := newQuestionSvc(&qFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{})
	err := svc.DeleteQuestion(context.Background(), 999, 1)
	wantQCode(t, err, 1701)
}

// TestDeleteQuestionVersionConflict 乐观锁冲突返 1713。
func TestDeleteQuestionVersionConflict(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), deleteRows: 0}
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	err := svc.DeleteQuestion(context.Background(), 101, 2)
	wantQCode(t, err, 1713)
}

// TestDeleteQuestionConflictButDeleted 冲突且题消失返 1701。
func TestDeleteQuestionConflictButDeleted(t *testing.T) {
	repo := &qFakeRepo{findByID: aiQuestion(), deleteRows: 0}
	repo.findByIDCalls = 1
	repo.findByIDErr = gorm.ErrRecordNotFound
	svc := newQuestionSvc(repo, &qFakeDimRepo{})
	err := svc.DeleteQuestion(context.Background(), 101, 2)
	wantQCode(t, err, 1701)
}

// ---------- Summary 截断 ----------

// TestListSummaryTruncates50Runes 核心断言：scenario 120 个中文字符的 AI 题
// summary 恰为前 50 rune（rune 截取，字节切片会截出乱码）。
func TestListSummaryTruncates50Runes(t *testing.T) {
	q := aiQuestion()
	q.Scenario = strings.Repeat("题", 120)
	repo := &qFakeRepo{list: []domain.Question{*q}, listTotal: 1}
	svc := newQuestionSvc(repo, &qFakeDimRepo{dims: aiMgmtDims()})

	res, err := svc.ListQuestions(context.Background(), service.QuestionListInput{Source: domain.QuestionSourceAI})
	if err != nil {
		t.Fatalf("ListQuestions: %v", err)
	}
	summary := res.List[0].Summary
	if got := len([]rune(summary)); got != 50 {
		t.Fatalf("summary want 50 runes, got %d", got)
	}
	if summary != strings.Repeat("题", 50) {
		t.Fatalf("summary content mismatch: %q", summary)
	}
}

// wantQCode 断言 err 为携带指定业务码的 *service.Error。
func wantQCode(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want code %d, got nil", code)
	}
	se, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("want *service.Error, got %T: %v", err, err)
	}
	if se.Code != code {
		t.Fatalf("want code %d, got %v", code, err)
	}
}
