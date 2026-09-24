// question_batch_test 题库批次 service 层测试：fake repo 注入（沿用 question_test.go
// 的 qFakeRepo/qFakeDimRepo），不连真库。覆盖 03 §3.5/§3.7/§3.8/§3.9/§3.10 业务规则。
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

// qBatchFakeRepo 是 QuestionBatchRepository 的测试假实现：返回值挂载 + 调用探针。
type qBatchFakeRepo struct {
	pendingList []domain.QuestionBatch
	pendingErr  error

	batchByID   map[int64]*domain.QuestionBatch
	findByIDErr error

	questions    []domain.Question
	questionsErr error

	confirmCalled    bool
	confirmID        int64
	confirmGot       map[int64]string
	confirmAdmitted  int64
	confirmRejectedN int64
	confirmErr       error

	voidCalled bool
	voidID     int64
	voidErr    error

	resubmitCalled bool
	resubmitQ      *domain.Question
	resubmitGot    map[string]any
	resubmitBatch  *domain.QuestionBatch
	resubmitErr    error
}

func (r *qBatchFakeRepo) ListPending(_ context.Context) ([]domain.QuestionBatch, error) {
	return r.pendingList, r.pendingErr
}

func (r *qBatchFakeRepo) FindByID(_ context.Context, id int64) (*domain.QuestionBatch, error) {
	if r.findByIDErr != nil {
		return nil, r.findByIDErr
	}
	b, ok := r.batchByID[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return b, nil
}

func (r *qBatchFakeRepo) ListQuestionsByBatchID(_ context.Context, _ int64) ([]domain.Question, error) {
	return r.questions, r.questionsErr
}

func (r *qBatchFakeRepo) FindPendingResubmitBatch(_ context.Context) (*domain.QuestionBatch, error) {
	return nil, gorm.ErrRecordNotFound
}

func (r *qBatchFakeRepo) CreateBatchWithQuestions(_ context.Context, _ *gorm.DB, _ *domain.QuestionBatch, _ *[]domain.Question) error {
	return nil
}

func (r *qBatchFakeRepo) NextBatchNo(_ context.Context, _ string, _ time.Time) (string, error) {
	return "", nil
}

func (r *qBatchFakeRepo) ConfirmBatch(_ context.Context, id int64, rejected map[int64]string) (int64, int64, error) {
	r.confirmCalled = true
	r.confirmID = id
	r.confirmGot = rejected
	return r.confirmAdmitted, r.confirmRejectedN, r.confirmErr
}

func (r *qBatchFakeRepo) VoidBatch(_ context.Context, id int64) error {
	r.voidCalled = true
	r.voidID = id
	return r.voidErr
}

func (r *qBatchFakeRepo) ResubmitToBatch(_ context.Context, q *domain.Question, updates map[string]any) (*domain.QuestionBatch, error) {
	r.resubmitCalled = true
	r.resubmitQ = q
	r.resubmitGot = updates
	if r.resubmitErr != nil {
		return nil, r.resubmitErr
	}
	return r.resubmitBatch, nil
}

func newQBatchSvc(repo *qBatchFakeRepo, qRepo *qFakeRepo, dimRepo *qFakeDimRepo) service.QuestionBatchService {
	return service.NewQuestionBatchService(repo, qRepo, dimRepo)
}

// pendingBatch 一枚待审核生成批默认形状。
func pendingBatch(id int64, count int) *domain.QuestionBatch {
	return &domain.QuestionBatch{
		ID:            id,
		BatchNo:       "#G0925",
		Title:         "授权与分工 ×2",
		Source:        domain.QuestionSourceAI,
		BatchType:     domain.QuestionBatchTypeGenerate,
		Status:        domain.QuestionBatchStatusPending,
		QuestionCount: count,
		CreatedAt:     time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
	}
}

// batchQuestion 挂批待审核题默认形状。
func batchQuestion(id int64, batchID int64, questionNo string) domain.Question {
	return domain.Question{
		ID:           id,
		QuestionNo:   questionNo,
		Source:       domain.QuestionSourceAI,
		DimensionID:  201,
		Scenario:     "情境-" + questionNo,
		Requirement:  "要求-" + questionNo,
		FocusPoint:   "考察-" + questionNo,
		Status:       domain.QuestionStatusPending,
		RejectReason: "存量原因",
		BatchID:      batchID,
		Version:      1,
	}
}

// rejectedAIQuestion 一枚可重新提交态的已驳回 AI 题。
func rejectedAIQuestion() *domain.Question {
	q := aiQuestion()
	q.Status = domain.QuestionStatusRejected
	q.RejectReason = "旧原因"
	return q
}

func validResubmitInput() service.ResubmitInput {
	return service.ResubmitInput{
		ID:          101,
		DimensionID: 201,
		Scenario:    "修正后的情境",
		Requirement: "修正后的要求",
		FocusPoint:  "修正后的考察点",
		Version:     2,
	}
}

// ---------- ListPendingBatches ----------

// TestQuestionBatchListPendingAssembles 卡区列表组装：雪花 ID string 化与批次卡字段透传。
func TestQuestionBatchListPendingAssembles(t *testing.T) {
	g := pendingBatch(901, 12)
	s := pendingBatch(902, 144)
	s.BatchNo, s.Title = "#S0925", "Riso-Hudson 标准量表"
	s.Source, s.BatchType = domain.QuestionSourceScale, domain.QuestionBatchTypeImport
	s.CreatedAt = time.Date(2026, 9, 25, 11, 0, 0, 0, time.UTC)
	svc := newQBatchSvc(&qBatchFakeRepo{pendingList: []domain.QuestionBatch{*g, *s}}, &qFakeRepo{}, &qFakeDimRepo{})

	cards, err := svc.ListPendingBatches(context.Background())
	if err != nil {
		t.Fatalf("ListPendingBatches: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("want 2 cards, got %d", len(cards))
	}
	c0 := cards[0]
	if c0.ID != "901" || c0.BatchNo != "#G0925" || c0.Title != "授权与分工 ×2" ||
		c0.Source != "AI" || c0.BatchType != "GENERATE" || c0.QuestionCount != 12 {
		t.Fatalf("card0 mismatch: %+v", c0)
	}
	if !c0.CreatedAt.Equal(g.CreatedAt) {
		t.Fatalf("created_at mismatch: %v", c0.CreatedAt)
	}
	if cards[1].Source != "SCALE" || cards[1].BatchType != "IMPORT" {
		t.Fatalf("card1 mismatch: %+v", cards[1])
	}
}

// TestQuestionBatchListPendingEmpty 无待审核批返回非 nil 空切片（前端契约稳定）。
func TestQuestionBatchListPendingEmpty(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{}, &qFakeDimRepo{})
	cards, err := svc.ListPendingBatches(context.Background())
	if err != nil {
		t.Fatalf("ListPendingBatches: %v", err)
	}
	if cards == nil || len(cards) != 0 {
		t.Fatalf("want non-nil empty slice, got %v", cards)
	}
}

// TestQuestionBatchListPendingRepoError 仓储错误透传非业务错误。
func TestQuestionBatchListPendingRepoError(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{pendingErr: gorm.ErrInvalidDB}, &qFakeRepo{}, &qFakeDimRepo{})
	if _, err := svc.ListPendingBatches(context.Background()); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- GetBatchQuestions ----------

// TestGetBatchQuestionsFound 明细组装：批次卡 + 题目全文，question_no 升序，
// reject_reason 恒空串（03 §3.8），维度名活跃回填。
func TestGetBatchQuestionsFound(t *testing.T) {
	b := pendingBatch(901, 2)
	q2 := batchQuestion(102, 901, "Q-AG-0002")
	q1 := batchQuestion(101, 901, "Q-AG-0001")
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: b}, questions: []domain.Question{q1, q2}}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{dims: aiMgmtDims()})

	res, err := svc.GetBatchQuestions(context.Background(), 901)
	if err != nil {
		t.Fatalf("GetBatchQuestions: %v", err)
	}
	if res.Batch == nil || res.Batch.ID != "901" || res.Batch.BatchNo != "#G0925" || res.Batch.QuestionCount != 2 {
		t.Fatalf("batch card mismatch: %+v", res.Batch)
	}
	if len(res.Questions) != 2 {
		t.Fatalf("want 2 questions, got %d", len(res.Questions))
	}
	r0 := res.Questions[0]
	if r0.ID != "101" || r0.QuestionNo != "Q-AG-0001" || res.Questions[1].QuestionNo != "Q-AG-0002" {
		t.Fatalf("want question_no ASC, got %+v", res.Questions)
	}
	if r0.DimensionID != "201" || r0.DimensionName != "授权与分工" || r0.AnswerMode != "CHAT" {
		t.Fatalf("dimension/answer_mode mismatch: %+v", r0)
	}
	if r0.Scenario != "情境-Q-AG-0001" || r0.Requirement != "要求-Q-AG-0001" || r0.FocusPoint != "考察-Q-AG-0001" {
		t.Fatalf("text mismatch: %+v", r0)
	}
	if r0.RejectReason != "" {
		t.Fatalf("reject_reason want恒空串, got %q", r0.RejectReason)
	}
}

// TestGetBatchQuestionsEmptyBatch 空批明细：questions 非 nil 空列表。
func TestGetBatchQuestionsEmptyBatch(t *testing.T) {
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 0)}}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	res, err := svc.GetBatchQuestions(context.Background(), 901)
	if err != nil {
		t.Fatalf("GetBatchQuestions: %v", err)
	}
	if res.Questions == nil || len(res.Questions) != 0 {
		t.Fatalf("want non-nil empty questions, got %v", res.Questions)
	}
}

// TestGetBatchQuestionsNotFound 批次不存在返 1702。
func TestGetBatchQuestionsNotFound(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{}, &qFakeDimRepo{})
	_, err := svc.GetBatchQuestions(context.Background(), 999)
	wantQCode(t, err, 1702)
}

// TestGetBatchQuestionsClosed 终态批（CLOSED/VOIDED）返 1706（03 §3.8、BR5）。
func TestGetBatchQuestionsClosed(t *testing.T) {
	closed := pendingBatch(901, 1)
	closed.Status = domain.QuestionBatchStatusClosed
	voided := pendingBatch(902, 1)
	voided.Status = domain.QuestionBatchStatusVoided
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: closed, 902: voided}}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	_, err1 := svc.GetBatchQuestions(context.Background(), 901)
	wantQCode(t, err1, 1706)
	_, err2 := svc.GetBatchQuestions(context.Background(), 902)
	wantQCode(t, err2, 1706)
}

// TestGetBatchQuestionsSoftDeletedDimensionName 维度软删回传存量名称（复用 SP1 双查回填）。
func TestGetBatchQuestionsSoftDeletedDimensionName(t *testing.T) {
	b := pendingBatch(901, 1)
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: b}, questions: []domain.Question{batchQuestion(101, 901, "Q-AG-0001")}}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{unscopedNames: map[int64]string{201: "授权与分工"}})
	res, err := svc.GetBatchQuestions(context.Background(), 901)
	if err != nil {
		t.Fatalf("GetBatchQuestions: %v", err)
	}
	if res.Questions[0].DimensionName != "授权与分工" {
		t.Fatalf("want存量名称, got %q", res.Questions[0].DimensionName)
	}
}

// TestGetBatchQuestionsRepoError 题目行仓储错误透传。
func TestGetBatchQuestionsRepoError(t *testing.T) {
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)}, questionsErr: gorm.ErrInvalidDB}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	if _, err := svc.GetBatchQuestions(context.Background(), 901); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- ConfirmBatch ----------

// TestConfirmSuccess 确认入库成功：rejected map 透传、结果计数与 CLOSED 状态组装。
func TestConfirmSuccess(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID:        map[int64]*domain.QuestionBatch{901: pendingBatch(901, 3)},
		questions:        []domain.Question{batchQuestion(101, 901, "Q-AG-0001"), batchQuestion(102, 901, "Q-AG-0002"), batchQuestion(103, 901, "Q-AG-0003")},
		confirmAdmitted:  2,
		confirmRejectedN: 1,
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})

	res, err := svc.ConfirmBatch(context.Background(), 901, []service.RejectItem{
		{QuestionID: 102, Reason: "场景迁移性差"},
	})
	if err != nil {
		t.Fatalf("ConfirmBatch: %v", err)
	}
	if res.BatchID != "901" || res.BatchStatus != "CLOSED" || res.AdmittedCount != 2 || res.RejectedCount != 1 {
		t.Fatalf("result mismatch: %+v", res)
	}
	if !repo.confirmCalled || repo.confirmID != 901 {
		t.Fatal("repo.ConfirmBatch not invoked as expected")
	}
	if len(repo.confirmGot) != 1 || repo.confirmGot[102] != "场景迁移性差" {
		t.Fatalf("rejected map mismatch: %v", repo.confirmGot)
	}
}

// TestConfirmAllAdmitted rejected 缺省：全通过等价（specs 规则 2），空 map 落库。
func TestConfirmAllAdmitted(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID:       map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)},
		questions:       []domain.Question{batchQuestion(101, 901, "Q-AG-0001")},
		confirmAdmitted: 1,
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})

	res, err := svc.ConfirmBatch(context.Background(), 901, nil)
	if err != nil {
		t.Fatalf("ConfirmBatch: %v", err)
	}
	if res.AdmittedCount != 1 || res.RejectedCount != 0 {
		t.Fatalf("result mismatch: %+v", res)
	}
	if len(repo.confirmGot) != 0 {
		t.Fatalf("want empty rejected map, got %v", repo.confirmGot)
	}
}

// TestConfirmRejectsForeignQuestion 核心断言：rejected 混入非本批 ID 返 1400 且
// 事务未执行（repo.ConfirmBatch 未被调用，题目状态不变）。
func TestConfirmRejectsForeignQuestion(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 2)},
		questions: []domain.Question{batchQuestion(101, 901, "Q-AG-0001"), batchQuestion(102, 901, "Q-AG-0002")},
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})

	_, err := svc.ConfirmBatch(context.Background(), 901, []service.RejectItem{
		{QuestionID: 101, Reason: "本批题"},
		{QuestionID: 999, Reason: "越权题"},
	})
	wantQCode(t, err, 1400)
	if repo.confirmCalled {
		t.Fatal("repo.ConfirmBatch must not run on foreign question_id")
	}
}

// TestConfirmReasonLength 核心断言：reason 501 rune 返 1400（BR2）；恰 500 通过、空串 1400。
func TestConfirmReasonLength(t *testing.T) {
	build := func(id int64, reason string) []service.RejectItem {
		return []service.RejectItem{{QuestionID: id, Reason: reason}}
	}
	over := &qBatchFakeRepo{
		batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)},
		questions: []domain.Question{batchQuestion(101, 901, "Q-AG-0001")},
	}
	svc := newQBatchSvc(over, &qFakeRepo{}, &qFakeDimRepo{})
	_, err := svc.ConfirmBatch(context.Background(), 901, build(101, strings.Repeat("驳", 501)))
	wantQCode(t, err, 1400)
	if over.confirmCalled {
		t.Fatal("repo.ConfirmBatch must not run on invalid reason")
	}

	empty := &qBatchFakeRepo{batchByID: over.batchByID, questions: over.questions}
	svc2 := newQBatchSvc(empty, &qFakeRepo{}, &qFakeDimRepo{})
	_, err = svc2.ConfirmBatch(context.Background(), 901, build(101, ""))
	wantQCode(t, err, 1400)

	ok := &qBatchFakeRepo{batchByID: over.batchByID, questions: over.questions, confirmAdmitted: 0, confirmRejectedN: 1}
	svc3 := newQBatchSvc(ok, &qFakeRepo{}, &qFakeDimRepo{})
	if _, err := svc3.ConfirmBatch(context.Background(), 901, build(101, strings.Repeat("驳", 500))); err != nil {
		t.Fatalf("exact-500 reason want pass, got %v", err)
	}
}

// TestConfirmCountMismatch 批内题目数与批次 question_count 不一致（题目被并发删）映射 1713。
func TestConfirmCountMismatch(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 5)},
		questions: []domain.Question{batchQuestion(101, 901, "Q-AG-0001"), batchQuestion(102, 901, "Q-AG-0002"), batchQuestion(103, 901, "Q-AG-0003")},
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})

	_, err := svc.ConfirmBatch(context.Background(), 901, []service.RejectItem{{QuestionID: 101, Reason: "有效标记"}})
	wantQCode(t, err, 1713)
	if repo.confirmCalled {
		t.Fatal("repo.ConfirmBatch must not run on count mismatch")
	}
}

// TestConfirmBatchNotFound 批次不存在返 1702。
func TestConfirmBatchNotFound(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{}, &qFakeDimRepo{})
	_, err := svc.ConfirmBatch(context.Background(), 999, nil)
	wantQCode(t, err, 1702)
}

// TestConfirmBatchNotPending repo 哨兵 ErrBatchNotPending 映射 1706（BR5）。
func TestConfirmBatchNotPending(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID:  map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)},
		questions:  []domain.Question{batchQuestion(101, 901, "Q-AG-0001")},
		confirmErr: repository.ErrBatchNotPending,
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	_, err := svc.ConfirmBatch(context.Background(), 901, nil)
	wantQCode(t, err, 1706)
}

// TestConfirmBatchRepoError 仓储错误透传非业务错误。
func TestConfirmBatchRepoError(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID:  map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)},
		questions:  []domain.Question{batchQuestion(101, 901, "Q-AG-0001")},
		confirmErr: gorm.ErrInvalidDB,
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	if _, err := svc.ConfirmBatch(context.Background(), 901, nil); err == nil {
		t.Fatal("want error, got nil")
	}
}

// ---------- VoidBatch ----------

// TestVoidBatchSuccess 作废成功：委托 repo 且无结果载荷。
func TestVoidBatchSuccess(t *testing.T) {
	repo := &qBatchFakeRepo{batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 2)}}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	if err := svc.VoidBatch(context.Background(), 901); err != nil {
		t.Fatalf("VoidBatch: %v", err)
	}
	if !repo.voidCalled || repo.voidID != 901 {
		t.Fatal("repo.VoidBatch not invoked as expected")
	}
}

// TestVoidBatchNotFound 批次不存在返 1702。
func TestVoidBatchNotFound(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{}, &qFakeDimRepo{})
	err := svc.VoidBatch(context.Background(), 999)
	wantQCode(t, err, 1702)
}

// TestVoidBatchNotPending repo 哨兵 ErrBatchNotPending 映射 1706（BR5）。
func TestVoidBatchNotPending(t *testing.T) {
	repo := &qBatchFakeRepo{
		batchByID: map[int64]*domain.QuestionBatch{901: pendingBatch(901, 1)},
		voidErr:   repository.ErrBatchNotPending,
	}
	svc := newQBatchSvc(repo, &qFakeRepo{}, &qFakeDimRepo{})
	err := svc.VoidBatch(context.Background(), 901)
	wantQCode(t, err, 1706)
}

// ---------- ResubmitQuestion ----------

// TestResubmitSuccessKeepsReason 核心断言：成功后题 status=PENDING、reject_reason
// 保留原值（经 FindByID 读回断言）；updates 列集不含 reject_reason（repo 层保留语义）。
func TestResubmitSuccessKeepsReason(t *testing.T) {
	after := rejectedAIQuestion()
	after.Status = domain.QuestionStatusPending
	after.Version = 3
	after.Scenario = "修正后的情境"
	after.Requirement = "修正后的要求"
	after.FocusPoint = "修正后的考察点"
	after.BatchID = 801
	qRepo := &qFakeRepo{findByID: rejectedAIQuestion(), afterWrite: after, afterWriteFrom: 2}
	batch := &domain.QuestionBatch{ID: 801, BatchNo: "#R0925", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeResubmit, Status: domain.QuestionBatchStatusPending, QuestionCount: 1}
	bRepo := &qBatchFakeRepo{resubmitBatch: batch}
	dims := &qFakeDimRepo{dims: aiMgmtDims()}
	svc := newQBatchSvc(bRepo, qRepo, dims)

	res, err := svc.ResubmitQuestion(context.Background(), validResubmitInput())
	if err != nil {
		t.Fatalf("ResubmitQuestion: %v", err)
	}
	if res.ID != "101" || res.Status != "PENDING" || res.BatchID != "801" || res.BatchNo != "#R0925" || res.Version != 3 {
		t.Fatalf("result mismatch: %+v", res)
	}
	if !bRepo.resubmitCalled {
		t.Fatal("repo.ResubmitToBatch not invoked")
	}
	if bRepo.resubmitQ.Version != 2 {
		t.Fatalf("optimistic lock version want request value 2, got %d", bRepo.resubmitQ.Version)
	}
	for _, col := range []string{"scenario", "requirement", "focus_point", "dimension_id"} {
		if _, ok := bRepo.resubmitGot[col]; !ok {
			t.Fatalf("updates missing column %s: %v", col, bRepo.resubmitGot)
		}
	}
	for _, col := range []string{"reject_reason", "status", "batch_id", "version"} {
		if _, ok := bRepo.resubmitGot[col]; ok {
			t.Fatalf("updates must not touch column %s: %v", col, bRepo.resubmitGot)
		}
	}
	// 经 FindByID 读回：status=PENDING 且驳回原因保留原值。
	d, err := newQuestionSvc(qRepo, dims).GetQuestion(context.Background(), 101)
	if err != nil {
		t.Fatalf("read back via FindByID: %v", err)
	}
	if d.Status != domain.QuestionStatusPending {
		t.Fatalf("read-back status want PENDING, got %s", d.Status)
	}
	if d.RejectReason != "旧原因" {
		t.Fatalf("reject_reason want preserved '旧原因', got %q", d.RejectReason)
	}
}

// TestResubmitScaleRejected 核心断言：SCALE 驳回题重新提交返 1707（BR3，量表题无送审路径）。
func TestResubmitScaleRejected(t *testing.T) {
	q := rejectedAIQuestion()
	q.Source = domain.QuestionSourceScale
	qRepo := &qFakeRepo{findByID: q}
	svc := newQBatchSvc(&qBatchFakeRepo{}, qRepo, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.ResubmitQuestion(context.Background(), validResubmitInput())
	wantQCode(t, err, 1707)
}

// TestResubmitNotRejected 核心断言：非 REJECTED 前置状态（ACTIVE/DISABLED/PENDING）
// 重新提交返 1707（BR4，已驳回→待审核仅经重新提交）。
func TestResubmitNotRejected(t *testing.T) {
	for _, st := range []string{domain.QuestionStatusActive, domain.QuestionStatusDisabled, domain.QuestionStatusPending} {
		q := aiQuestion()
		q.Status = st
		svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{findByID: q}, &qFakeDimRepo{dims: aiMgmtDims()})
		_, err := svc.ResubmitQuestion(context.Background(), validResubmitInput())
		wantQCode(t, err, 1707)
	}
}

// TestResubmitTextInvalid 文本长度校验复用 SP1：越上限与空串返 1400。
func TestResubmitTextInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *service.ResubmitInput)
	}{
		{"scenario over", func(in *service.ResubmitInput) { in.Scenario = strings.Repeat("境", 1001) }},
		{"requirement empty", func(in *service.ResubmitInput) { in.Requirement = "" }},
		{"focus over", func(in *service.ResubmitInput) { in.FocusPoint = strings.Repeat("点", 501) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{findByID: rejectedAIQuestion()}, &qFakeDimRepo{dims: aiMgmtDims()})
			in := validResubmitInput()
			tc.mutate(&in)
			_, err := svc.ResubmitQuestion(context.Background(), in)
			wantQCode(t, err, 1400)
		})
	}
}

// TestResubmitDimensionNotEnabled dimension_id 不在启用 AI_MGMT 集合返 1400。
func TestResubmitDimensionNotEnabled(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{findByID: rejectedAIQuestion()},
		&qFakeDimRepo{dims: []domain.Dimension{{ID: 201, Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt, Enabled: false}}})
	in := validResubmitInput()
	in.DimensionID = 201
	_, err := svc.ResubmitQuestion(context.Background(), in)
	wantQCode(t, err, 1400)
}

// TestResubmitVersionConflict 乐观锁失败（repo 哨兵）映射 1713。
func TestResubmitVersionConflict(t *testing.T) {
	bRepo := &qBatchFakeRepo{resubmitErr: repository.ErrVersionConflict}
	svc := newQBatchSvc(bRepo, &qFakeRepo{findByID: rejectedAIQuestion()}, &qFakeDimRepo{dims: aiMgmtDims()})
	_, err := svc.ResubmitQuestion(context.Background(), validResubmitInput())
	wantQCode(t, err, 1713)
}

// TestResubmitQuestionNotFound 目标题不存在返 1701。
func TestResubmitQuestionNotFound(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{}, &qFakeRepo{findByIDErr: gorm.ErrRecordNotFound}, &qFakeDimRepo{})
	_, err := svc.ResubmitQuestion(context.Background(), validResubmitInput())
	wantQCode(t, err, 1701)
}

// TestResubmitRepoError 仓储错误透传非业务错误。
func TestResubmitRepoError(t *testing.T) {
	svc := newQBatchSvc(&qBatchFakeRepo{resubmitErr: gorm.ErrInvalidDB},
		&qFakeRepo{findByID: rejectedAIQuestion()}, &qFakeDimRepo{dims: aiMgmtDims()})
	if _, err := svc.ResubmitQuestion(context.Background(), validResubmitInput()); err == nil {
		t.Fatal("want error, got nil")
	}
}
