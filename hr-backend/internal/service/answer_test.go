// Package service_test 对 answer 作答域业务层做黑盒单元测试（specs P2_TST_002
// §5.1/§5.2/§5.3，03 A1/A2/A3）。fakeAnswerRepo / fakeAnswerTaskRepo /
// fakeAnswerSvcTask / fakeAnswerQuestionRepo 复用 fakeDimRepo 驱动，不依赖真实 DB。
// 覆盖：
//   - 令牌校验链：不存在/invalid/任务 canceled 三路径同文案 1901、A3 幂等特例、
//     空令牌 1901
//   - Context：快乐路径组装、断点恢复、完成态、StartSession 阻断 1500、坏快照 JSON
//   - Reply：九型合法/非法、ai_mgmt 长度边界、服务端权威题号、完成态兜底、
//     末题 finished、作答中被取消拦截、trim 落库
//   - Submit：完整性门槛 1903、幂等短路、1802 收敛 1901、正常提交
package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"

	"gorm.io/gorm"
)

// fakeAnswerRepo 是 repository.AssessmentTestAnswerRepository 的测试假实现。
type fakeAnswerRepo struct {
	link      *domain.AssessmentTestLink
	linkErr   error
	rows      []domain.AssessmentTestAnswer
	count     int64
	countErr  error
	listErr   error
	insertErr error

	insertCalled bool
	gotInsert    *domain.AssessmentTestAnswer
	gotTokenHash string
}

func (f *fakeAnswerRepo) Insert(_ context.Context, row *domain.AssessmentTestAnswer) error {
	f.insertCalled = true
	if f.insertErr != nil {
		return f.insertErr
	}
	row.ID = int64(7000 + len(f.rows) + 1) // 模拟 GORM 雪花回调
	f.rows = append(f.rows, *row)
	f.count++
	f.gotInsert = row
	return nil
}

func (f *fakeAnswerRepo) CountByTask(_ context.Context, _ int64) (int64, error) {
	return f.count, f.countErr
}

func (f *fakeAnswerRepo) ListByTask(_ context.Context, _ int64) ([]domain.AssessmentTestAnswer, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// 对齐真实仓储契约：question_seq 升序返回。
	sorted := make([]domain.AssessmentTestAnswer, len(f.rows))
	copy(sorted, f.rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].QuestionSeq < sorted[j].QuestionSeq })
	return sorted, nil
}

func (f *fakeAnswerRepo) FindLinkByTokenHash(_ context.Context, tokenHash string) (*domain.AssessmentTestLink, error) {
	f.gotTokenHash = tokenHash
	if f.linkErr != nil {
		return nil, f.linkErr
	}
	if f.link != nil && tokenHash != f.link.TokenHash {
		return nil, nil // 哈希不命中：无行（uk_token_hash 点查语义）
	}
	return f.link, nil
}

var _ repository.AssessmentTestAnswerRepository = (*fakeAnswerRepo)(nil)

// fakeAnswerTaskRepo 是 repository.AssessmentTestTaskRepository 的窄假实现：
// answer 域只消费 GetByID，其余方法零行为。byID 可在调用间被改写，模拟
// 并发推进后的任务行。
type fakeAnswerTaskRepo struct {
	byID   *domain.AssessmentTestTask
	byIDEr error
}

func (f *fakeAnswerTaskRepo) ListByFilter(_ context.Context, _ repository.TestTaskFilter) ([]repository.TestTaskRow, int64, error) {
	return nil, 0, nil
}
func (f *fakeAnswerTaskRepo) GetByID(_ context.Context, _ int64) (*domain.AssessmentTestTask, error) {
	return f.byID, f.byIDEr
}
func (f *fakeAnswerTaskRepo) CreateWithLink(_ context.Context, _ *domain.AssessmentTestTask, _ *domain.AssessmentTestLink) error {
	return nil
}
func (f *fakeAnswerTaskRepo) NextTaskNo(_ context.Context, _ string, _ time.Time) (string, error) {
	return "", nil
}
func (f *fakeAnswerTaskRepo) CurrentLink(_ context.Context, _ int64) (*domain.AssessmentTestLink, error) {
	return nil, nil
}
func (f *fakeAnswerTaskRepo) ReplaceLink(_ context.Context, _ int64, _ *domain.AssessmentTestLink) error {
	return nil
}
func (f *fakeAnswerTaskRepo) CancelTask(_ context.Context, _ int64) (int64, error) { return 0, nil }
func (f *fakeAnswerTaskRepo) MarkSessionStarted(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (f *fakeAnswerTaskRepo) CountActiveByType(_ context.Context) (int64, int64, error) {
	return 0, 0, nil
}
func (f *fakeAnswerTaskRepo) ExpirePending(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeAnswerTaskRepo) CompleteTask(_ context.Context, _ int64, _ func(*gorm.DB) error, _ time.Time) error {
	return nil
}
func (f *fakeAnswerTaskRepo) MarkGradingTerminal(_ context.Context, _ int64, _ string) error {
	return nil
}

var _ repository.AssessmentTestTaskRepository = (*fakeAnswerTaskRepo)(nil)

// fakeAnswerSvcTask 是 service.AssessmentTestTaskService 的窄假实现：
// answer 域只消费 StartSession/CompleteTask。onComplete 在 CompleteTask 失败
//（终态守卫 1802）时回调，供竞态用例同步改写任务行模拟并发推进。
type fakeAnswerSvcTask struct {
	startErr       error
	startCalls     int
	completeErr    error
	completeCalls  int
	gotCompleteID  int64
	completeStatus string // CompleteTask 成功后模拟落库状态
	onGuardReject  func()
}

func (f *fakeAnswerSvcTask) List(_ context.Context, _ service.ListTestTaskFilter) ([]service.TestTaskListDTO, int64, error) {
	return nil, 0, nil
}
func (f *fakeAnswerSvcTask) PollCounts(_ context.Context) (*service.TestTaskPollCountsDTO, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) Create(_ context.Context, _ service.CreateTestTaskPayload) (*service.CreateTestTaskResult, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) ScaleStatus(_ context.Context) (*service.TestScaleStatusDTO, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) Link(_ context.Context, _ int64) (*service.TestTaskLinkDTO, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) Resend(_ context.Context, _ int64) (*service.TestTaskLinkDTO, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) Cancel(_ context.Context, _ int64) (*service.TestTaskCancelDTO, error) {
	return nil, nil
}
func (f *fakeAnswerSvcTask) CompleteTask(_ context.Context, taskID int64) error {
	f.completeCalls++
	f.gotCompleteID = taskID
	if f.completeErr != nil {
		if f.onGuardReject != nil {
			f.onGuardReject()
		}
		return f.completeErr
	}
	f.completeStatus = domain.TestTaskStatusCompleted
	return nil
}
func (f *fakeAnswerSvcTask) StartSession(_ context.Context, taskID int64) error {
	f.startCalls++
	return f.startErr
}

var _ service.AssessmentTestTaskService = (*fakeAnswerSvcTask)(nil)

// fakeAnswerQuestionRepo 是 repository.QuestionRepository 的窄假实现：
// answer 域只消费 ListByIDsUnscoped。
type fakeAnswerQuestionRepo struct {
	byIDs   map[int64]domain.Question
	byIDsEr error

	gotIDs []int64
}

func (f *fakeAnswerQuestionRepo) ListPage(_ context.Context, _ string, _ *int64, _, _ string, _, _ int) ([]domain.Question, int64, error) {
	return nil, 0, nil
}
func (f *fakeAnswerQuestionRepo) FindByID(_ context.Context, _ int64) (*domain.Question, error) {
	return nil, nil
}
func (f *fakeAnswerQuestionRepo) UpdateWithVersion(_ context.Context, _ int64, _ int, _ map[string]any) (int64, error) {
	return 0, nil
}
func (f *fakeAnswerQuestionRepo) SoftDeleteWithVersion(_ context.Context, _ int64, _ int) (int64, error) {
	return 0, nil
}
func (f *fakeAnswerQuestionRepo) MaxQuestionSeq(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (f *fakeAnswerQuestionRepo) ListActiveByDimensionIDs(_ context.Context, _ []int64) ([]domain.Question, error) {
	return nil, nil
}
func (f *fakeAnswerQuestionRepo) ListActiveByScaleKey(_ context.Context, _ string) ([]domain.Question, error) {
	return nil, nil
}
func (f *fakeAnswerQuestionRepo) ListByIDsUnscoped(_ context.Context, ids []int64) ([]domain.Question, error) {
	f.gotIDs = ids
	if f.byIDsEr != nil {
		return nil, f.byIDsEr
	}
	out := make([]domain.Question, 0, len(ids))
	for _, id := range ids { // 模拟快照集合权威：未命中 ID 跳过
		if q, ok := f.byIDs[id]; ok {
			out = append(out, q)
		}
	}
	return out, nil
}
func (f *fakeAnswerQuestionRepo) IncrementReferenceCounts(_ context.Context, _ *gorm.DB, _ []int64) error {
	return nil
}

var _ repository.QuestionRepository = (*fakeAnswerQuestionRepo)(nil)

// answerFixture 组装被测 service 与四假实现，链接到任务的 valid 令牌固定 tokAnswer。
type answerFixture struct {
	svc      service.AnswerService
	ansRepo  *fakeAnswerRepo
	taskRepo *fakeAnswerTaskRepo
	taskSvc  *fakeAnswerSvcTask
	qRepo    *fakeAnswerQuestionRepo
	dimRepo  *fakeDimRepo
}

const answerTokenPlain = "AnsFixtureTokenPlain0000000000000000000000"

func answerTokenHash() string {
	sum := sha256.Sum256([]byte(answerTokenPlain))
	return hex.EncodeToString(sum[:])
}

// answerAIMgmtTask 五题 ai_mgmt 任务：两维度 2+3 题，快照 [601 602 603 604 605]。
func answerAIMgmtTask() *domain.AssessmentTestTask {
	ids, _ := json.Marshal([]int64{601, 602, 603, 604, 605})
	return &domain.AssessmentTestTask{
		ID:                 8001,
		TaskNo:             "T202609290001",
		TestType:           domain.TestTypeAIMgmt,
		StaffID:            "u1",
		StaffName:          "张敏",
		Status:             domain.TestTaskStatusPending,
		GradingStatus:      domain.GradingStatusWaiting,
		QuestionIDsJSON:    string(ids),
		ScaleKey:           "",
		DimensionCodesJSON: `["MGT_ALIGN","MGT_DELEGATE"]`,
	}
}

// answerEnneagramTask 三题九型任务，快照 [701 702 703]。
func answerEnneagramTask() *domain.AssessmentTestTask {
	ids, _ := json.Marshal([]int64{701, 702, 703})
	return &domain.AssessmentTestTask{
		ID:              8002,
		TaskNo:          "E202609290001",
		TestType:        domain.TestTypeEnneagram,
		StaffID:         "u1",
		StaffName:       "张敏",
		Status:          domain.TestTaskStatusInProgress,
		GradingStatus:   domain.GradingStatusWaiting,
		QuestionIDsJSON: string(ids),
		ScaleKey:        domain.ScaleKeyRisoHudson,
	}
}

// answerAIQuestions 五道 AI 题全文，601/602 属 MGT_ALIGN（目标设定与对齐）。
func answerAIQuestions() map[int64]domain.Question {
	return map[int64]domain.Question{
		601: {ID: 601, DimensionID: 301, QuestionNo: "Q-AG-0001", Scenario: "情境一", Requirement: "要求一"},
		602: {ID: 602, DimensionID: 301, QuestionNo: "Q-AG-0002", Scenario: "情境二", Requirement: "要求二"},
		603: {ID: 603, DimensionID: 302, QuestionNo: "Q-AG-0003", Scenario: "情境三", Requirement: "要求三"},
		604: {ID: 604, DimensionID: 302, QuestionNo: "Q-AG-0004", Scenario: "情境四", Requirement: "要求四"},
		605: {ID: 605, DimensionID: 302, QuestionNo: "Q-AG-0005", Scenario: "情境五", Requirement: "要求五"},
	}
}

// answerScaleQuestions 三道量表题。
func answerScaleQuestions() map[int64]domain.Question {
	return map[int64]domain.Question{
		701: {ID: 701, DimensionID: 401, QuestionNo: "Q-Scale-0001", Scenario: "题项一", Requirement: "1-5 级说明"},
		702: {ID: 702, DimensionID: 401, QuestionNo: "Q-Scale-0002", Scenario: "题项二", Requirement: "1-5 级说明"},
		703: {ID: 703, DimensionID: 401, QuestionNo: "Q-Scale-0003", Scenario: "题项三", Requirement: "1-5 级说明"},
	}
}

// answerDims 两维度：code→name 映射供 dimension_name 组装。
func answerDims() []domain.Dimension {
	return []domain.Dimension{
		{ID: 301, Code: "MGT_ALIGN", Name: "目标设定与对齐"},
		{ID: 302, Code: "MGT_DELEGATE", Name: "授权分工"},
	}
}

// newAnswerFixture 公共装配：valid 链接 + pending ai_mgmt 任务 + 五题现读 + 两维度。
func newAnswerFixture(t *testing.T) *answerFixture {
	t.Helper()
	return newAnswerFixtureTask(t, answerAIMgmtTask())
}

// newAnswerFixtureTask 携指定任务装配。链接固定未到期（过期判定用例自行改写）。
func newAnswerFixtureTask(t *testing.T, task *domain.AssessmentTestTask) *answerFixture {
	t.Helper()
	link := &domain.AssessmentTestLink{ID: 90, TaskID: task.ID, TokenHash: answerTokenHash(), Status: domain.LinkStatusValid, ExpiresAt: time.Now().Add(time.Hour)}
	ansRepo := &fakeAnswerRepo{link: link}
	taskRepo := &fakeAnswerTaskRepo{byID: task}
	taskSvc := &fakeAnswerSvcTask{}
	qRepo := &fakeAnswerQuestionRepo{byIDs: answerAIQuestions()}
	dimRepo := &fakeDimRepo{dims: answerDims()}
	if task.TestType == domain.TestTypeEnneagram {
		qRepo.byIDs = answerScaleQuestions()
	}
	svc := service.NewAnswerService(ansRepo, taskRepo, taskSvc, qRepo, dimRepo, func() time.Time { return time.Now() })
	return &answerFixture{svc: svc, ansRepo: ansRepo, taskRepo: taskRepo, taskSvc: taskSvc, qRepo: qRepo, dimRepo: dimRepo}
}

// wantAnswerErr 断言 err 为 *service.Error 且 code/msg 等于预期。
func wantAnswerErr(t *testing.T, err error, want int) {
	t.Helper()
	serr, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("err 类型 %T, want *service.Error（err=%v）", err, err)
	}
	if serr.Code != want {
		t.Errorf("code = %d, want %d", serr.Code, want)
	}
}

// wantNotServiceErr 断言 err 为非 *service.Error 的包裹错误（前端收 1500）。
func wantNotServiceErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want 非 *service.Error 包裹错误")
	}
	if _, ok := err.(*service.Error); ok {
		t.Fatalf("err 类型 %T, want 非 *service.Error（err=%v）", err, err)
	}
}

// ---------- Context ----------

// TestContextHappyPath pending 任务 valid 链接：元信息/题目全集/dimension_name/
// StartSession 触发（specs §5.1.2）。
func TestContextHappyPath(t *testing.T) {
	fx := newAnswerFixture(t)

	res, err := fx.svc.Context(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.TaskNo != "T202609290001" || res.TestType != domain.TestTypeAIMgmt {
		t.Errorf("TaskNo/TestType = %s/%s, want T202609290001/ai_mgmt", res.TaskNo, res.TestType)
	}
	if res.QuestionTotal != 5 || res.AnsweredCount != 0 || res.Finished {
		t.Errorf("total/answered/finished = %d/%d/%v, want 5/0/false", res.QuestionTotal, res.AnsweredCount, res.Finished)
	}
	if fx.taskSvc.startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", fx.taskSvc.startCalls)
	}
	if fx.ansRepo.insertCalled {
		t.Error("校验与组装不得落库作答记录")
	}
	if len(res.Questions) != 5 {
		t.Fatalf("len(Questions) = %d, want 5", len(res.Questions))
	}
	if res.Questions[0].DimensionName != "目标设定与对齐" {
		t.Errorf("Questions[0].DimensionName = %q, want 目标设定与对齐", res.Questions[0].DimensionName)
	}
	if res.Questions[0].Seq != 1 || res.Questions[0].Scenario != "情境一" || res.Questions[0].Requirement != "要求一" {
		t.Errorf("Questions[0] = %+v, want seq=1 情境一/要求一", res.Questions[0])
	}
	if len(res.Replies) != 0 {
		t.Errorf("len(Replies) = %d, want 0", len(res.Replies))
	}
	if fx.taskSvc.startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", fx.taskSvc.startCalls)
	}
	if fx.ansRepo.insertCalled {
		t.Error("校验与组装不得落库作答记录")
	}
}

// TestContextResume 预置 2 条回复：AnsweredCount=2、Replies 按 seq 升序、
// StartSession 重复调用幂等无副作用（specs §4.1.3 会话上报/对话恢复）。
func TestContextResume(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusInProgress
	fx.ansRepo.rows = []domain.AssessmentTestAnswer{
		{ID: 7102, TaskID: 8001, QuestionSeq: 2, Content: "第二题回复"},
		{ID: 7101, TaskID: 8001, QuestionSeq: 1, Content: "第一题回复"}, // 乱序预置验证投影排序
	}
	fx.ansRepo.count = 2

	res, err := fx.svc.Context(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.AnsweredCount != 2 || res.Finished {
		t.Errorf("answered/finished = %d/%v, want 2/false", res.AnsweredCount, res.Finished)
	}
	if len(res.Replies) != 2 {
		t.Fatalf("len(Replies) = %d, want 2", len(res.Replies))
	}
	if res.Replies[0].Seq != 1 || res.Replies[0].Content != "第一题回复" {
		t.Errorf("Replies[0] = %+v, want seq=1 第一题回复", res.Replies[0])
	}
	if res.Replies[1].Seq != 2 || res.Replies[1].Content != "第二题回复" {
		t.Errorf("Replies[1] = %+v, want seq=2 第二题回复", res.Replies[1])
	}
	if fx.taskSvc.startErr != nil { // 桩幂等返回 nil
		t.Errorf("StartSession 桩错误 = %v, want nil", fx.taskSvc.startErr)
	}
}

// TestContextFinished 回复数=题数：Finished=true 进完成待提交（03 §1.7 ≥ 口径）。
func TestContextFinished(t *testing.T) {
	fx := newAnswerFixture(t)
	for i := 1; i <= 5; i++ {
		fx.ansRepo.rows = append(fx.ansRepo.rows, domain.AssessmentTestAnswer{TaskID: 8001, QuestionSeq: i, Content: fmt.Sprintf("第%d题回复", i)})
	}
	fx.ansRepo.count = 5

	res, err := fx.svc.Context(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if !res.Finished {
		t.Error("Finished = false, want true（answered_count ≥ question_total）")
	}
	if res.AnsweredCount != 5 {
		t.Errorf("AnsweredCount = %d, want 5", res.AnsweredCount)
	}
}

// TestContextTokenInvalid 三路径同文案 1901：令牌不存在/链接 invalid/任务 canceled
//（specs §4.2.4 规则1 防枚举，Error() 全等断言）。
func TestContextTokenInvalid(t *testing.T) {
	fx := newAnswerFixture(t)
	_, errNoLink := fx.svc.Context(context.Background(), "NoSuchToken000000000000000000000000000")
	wantAnswerErr(t, errNoLink, errcode.AnswerTokenInvalid)

	fx = newAnswerFixture(t)
	fx.ansRepo.link.Status = domain.LinkStatusInvalid
	_, errInvalidLink := fx.svc.Context(context.Background(), answerTokenPlain)
	wantAnswerErr(t, errInvalidLink, errcode.AnswerTokenInvalid)

	fx = newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusCanceled
	_, errCanceled := fx.svc.Context(context.Background(), answerTokenPlain)
	wantAnswerErr(t, errCanceled, errcode.AnswerTokenInvalid)

	if errNoLink.Error() != errInvalidLink.Error() || errNoLink.Error() != errCanceled.Error() {
		t.Errorf("三路径文案不一致防枚举破绽：%q / %q / %q", errNoLink.Error(), errInvalidLink.Error(), errCanceled.Error())
	}
}

// TestContextTokenEmpty 空令牌 1901（03 A1 1400 由 handler 收敛，service 层统一 1901）。
func TestContextTokenEmpty(t *testing.T) {
	fx := newAnswerFixture(t)
	_, err := fx.svc.Context(context.Background(), "")
	wantAnswerErr(t, err, errcode.AnswerTokenInvalid)
}

// TestContextStartSessionBlocked StartSession 失败：返回非业务错误包裹
//（specs §5.1.5 阻断作答防逾期误判）。
func TestContextStartSessionBlocked(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskSvc.startErr = errors.New("start session down")

	_, err := fx.svc.Context(context.Background(), answerTokenPlain)
	wantNotServiceErr(t, err)
}

// TestContextBadSnapshotJSON 坏 question_ids_json：内部错误包裹（快照残缺上游缺陷）。
func TestContextBadSnapshotJSON(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.QuestionIDsJSON = "{bad"
	_, err := fx.svc.Context(context.Background(), answerTokenPlain)
	wantNotServiceErr(t, err)
}

// TestContextQuestionReadFail 现读失败：内部错误（specs §5.1.5 第三行）。
func TestContextQuestionReadFail(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.qRepo.byIDsEr = errors.New("db down")
	_, err := fx.svc.Context(context.Background(), answerTokenPlain)
	wantNotServiceErr(t, err)
}

// TestContextSnapshotMissingQuestion 快照内 ID 未命中题库：按题目现读失败处理，
// 返回内部错误 1500（specs §5.1.5 异常表，前端加载失败与重试）。
func TestContextSnapshotMissingQuestion(t *testing.T) {
	fx := newAnswerFixture(t)
	delete(fx.qRepo.byIDs, 603)

	_, err := fx.svc.Context(context.Background(), answerTokenPlain)
	wantNotServiceErr(t, err)
	if fx.ansRepo.insertCalled {
		t.Error("题目现读失败不得落库")
	}
}

// TestContextTokenExpiredInProgress 链接过期但任务已 in_progress：豁免放行，
// 断点续答可用（specs §4.1.3 进行中任务到期顺延、链接保持可用）。
func TestContextTokenExpiredInProgress(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusInProgress
	fx.ansRepo.link.ExpiresAt = time.Now().Add(-time.Minute)

	res, err := fx.svc.Context(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.QuestionTotal != 5 {
		t.Errorf("QuestionTotal = %d, want 5", res.QuestionTotal)
	}
}

// TestReplyTokenExpiredInProgress 回复侧同口径：in_progress 任务过期链接放行落库。
func TestReplyTokenExpiredInProgress(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusInProgress
	fx.ansRepo.link.ExpiresAt = time.Now().Add(-time.Minute)

	if _, err := fx.svc.Reply(context.Background(), answerTokenPlain, "过期后续答"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !fx.ansRepo.insertCalled {
		t.Error("in_progress 过期链接回复应落库")
	}
}

// TestContextEnneagram 九型任务：dimension_name 恒空串（03 A1 响应字段）。
func TestContextEnneagram(t *testing.T) {
	fx := newAnswerFixtureTask(t, answerEnneagramTask())

	res, err := fx.svc.Context(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if res.TestType != domain.TestTypeEnneagram || res.QuestionTotal != 3 {
		t.Errorf("test_type/total = %s/%d, want enneagram/3", res.TestType, res.QuestionTotal)
	}
	for i, q := range res.Questions {
		if q.DimensionName != "" {
			t.Errorf("Questions[%d].DimensionName = %q, want 空串", i, q.DimensionName)
		}
	}
}

// ---------- Reply ----------

// TestReplyEnneagramValid content="3"：落库 seq=1、action=next 携下一题
//（specs §5.2.2）。
func TestReplyEnneagramValid(t *testing.T) {
	fx := newAnswerFixtureTask(t, answerEnneagramTask())

	res, err := fx.svc.Reply(context.Background(), answerTokenPlain, "3")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if !fx.ansRepo.insertCalled {
		t.Fatal("合法回复未落库")
	}
	if fx.ansRepo.gotInsert.QuestionSeq != 1 || fx.ansRepo.gotInsert.Content != "3" {
		t.Errorf("落库行 = seq:%d content:%q, want seq:1 content:3", fx.ansRepo.gotInsert.QuestionSeq, fx.ansRepo.gotInsert.Content)
	}
	if fx.ansRepo.gotInsert.TaskID != 8002 {
		t.Errorf("落库 TaskID = %d, want 8002", fx.ansRepo.gotInsert.TaskID)
	}
	if res.Action != service.AnswerActionNext || res.QuestionSeq != 1 || res.AnsweredCount != 1 {
		t.Errorf("action/seq/count = %s/%d/%d, want next/1/1", res.Action, res.QuestionSeq, res.AnsweredCount)
	}
	if res.NextQuestion == nil || res.NextQuestion.Seq != 2 {
		t.Errorf("NextQuestion = %+v, want seq=2", res.NextQuestion)
	}
}

// TestReplyEnneagramInvalid "6"/"abc"/"3.5" 三输入：1902 不落库不推进
//（specs §5.2.4 规则2）。
func TestReplyEnneagramInvalid(t *testing.T) {
	for _, content := range []string{"6", "abc", "3.5"} {
		fx := newAnswerFixtureTask(t, answerEnneagramTask())
		_, err := fx.svc.Reply(context.Background(), answerTokenPlain, content)
		wantAnswerErr(t, err, errcode.AnswerReplyInvalid)
		if fx.ansRepo.insertCalled {
			t.Errorf("content=%q 非法回复不得落库", content)
		}
	}
}

// TestReplyAIMgmtLength ai_mgmt 501 rune 拒绝、500 rune 放行（specs §5.2.2 步骤2）。
func TestReplyAIMgmtLength(t *testing.T) {
	fx := newAnswerFixture(t)
	_, err := fx.svc.Reply(context.Background(), answerTokenPlain, strings.Repeat("测", 501))
	wantAnswerErr(t, err, errcode.AnswerReplyInvalid)
	if fx.ansRepo.insertCalled {
		t.Error("501 rune 回复不得落库")
	}

	fx = newAnswerFixture(t)
	if _, err := fx.svc.Reply(context.Background(), answerTokenPlain, strings.Repeat("测", 500)); err != nil {
		t.Fatalf("500 rune 回复应放行: %v", err)
	}
	if !fx.ansRepo.insertCalled {
		t.Error("500 rune 合法回复应落库")
	}
}

// TestReplyBlank 空串与纯空白：1902 不落库（TrimSpace 后非空校验）。
func TestReplyBlank(t *testing.T) {
	for _, content := range []string{"", "   ", "\t\n"} {
		fx := newAnswerFixture(t)
		_, err := fx.svc.Reply(context.Background(), answerTokenPlain, content)
		wantAnswerErr(t, err, errcode.AnswerReplyInvalid)
		if fx.ansRepo.insertCalled {
			t.Errorf("content=%q 空白回复不得落库", content)
		}
	}
}

// TestReplyTrimStored content 存 TrimSpace 后值（specs §5.2.2 步骤2 去首尾空格）。
func TestReplyTrimStored(t *testing.T) {
	fx := newAnswerFixture(t)
	if _, err := fx.svc.Reply(context.Background(), answerTokenPlain, "  C，因为…  "); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fx.ansRepo.gotInsert.Content != "C，因为…" {
		t.Errorf("落库 content = %q, want TrimSpace 后值", fx.ansRepo.gotInsert.Content)
	}
}

// TestReplyServerAuthoritative 客户端传 clientSeq=9：服务端推算 seq=1 落库，
// 响应回传实际题号（specs §5.2.4 规则1）。
func TestReplyServerAuthoritative(t *testing.T) {
	fx := newAnswerFixture(t)

	res, err := fx.svc.Reply(context.Background(), answerTokenPlain, "选 C，理由如下")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fx.ansRepo.gotInsert.QuestionSeq != 1 {
		t.Errorf("落库 Seq = %d, want 1（忽略客户端题号）", fx.ansRepo.gotInsert.QuestionSeq)
	}
	if res.QuestionSeq != 1 {
		t.Errorf("响应 QuestionSeq = %d, want 1（回传实际题号）", res.QuestionSeq)
	}
}

// TestReplyFinishedGuard 已答满再发回复：1902 完成态兜底不落库（03 A2 1902 说明）。
func TestReplyFinishedGuard(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 5

	_, err := fx.svc.Reply(context.Background(), answerTokenPlain, "多余回复")
	wantAnswerErr(t, err, errcode.AnswerReplyInvalid)
	if fx.ansRepo.insertCalled {
		t.Error("完成态兜底不得落库")
	}
}

// TestReplyLastFinished 最后一题合法回复：action=finished、NextQuestion=nil
//（specs §5.2.2 步骤4）。
func TestReplyLastFinished(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 4

	res, err := fx.svc.Reply(context.Background(), answerTokenPlain, "最后一题回复")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if res.Action != service.AnswerActionFinished {
		t.Errorf("Action = %s, want finished", res.Action)
	}
	if res.NextQuestion != nil {
		t.Errorf("NextQuestion = %+v, want nil", res.NextQuestion)
	}
	if res.AnsweredCount != 5 || res.QuestionTotal != 5 {
		t.Errorf("count/total = %d/%d, want 5/5", res.AnsweredCount, res.QuestionTotal)
	}
	if res.QuestionSeq != 5 {
		t.Errorf("QuestionSeq = %d, want 5", res.QuestionSeq)
	}
}

// TestContextTokenExpired 链接 valid、已过 ExpiresAt 且任务 pending：1901 即时
// 拦截（收口每分钟 tick 的空窗，防过期链接被 StartSession 救活后永不过期）。
func TestContextTokenExpired(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.link.ExpiresAt = time.Now().Add(-time.Minute)

	_, err := fx.svc.Context(context.Background(), answerTokenPlain)
	wantAnswerErr(t, err, errcode.AnswerTokenInvalid)
	if fx.taskSvc.startCalls != 0 {
		t.Error("过期链接不得触发 StartSession")
	}
}

// TestReplyBuildFailNoInsert 组装下一题失败（题目现读 DB 抖动）：零落库返回内部
// 错误，客户端重发仍落原题号（推进单调，specs §5.2.4 规则1）。
func TestReplyBuildFailNoInsert(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.qRepo.byIDsEr = errors.New("db down")

	_, err := fx.svc.Reply(context.Background(), answerTokenPlain, "回复")
	wantNotServiceErr(t, err)
	if fx.ansRepo.insertCalled {
		t.Error("组装失败不得落库（先组装后落库，重发仍落原题号）")
	}
}

// TestReplyTaskCanceled 作答中任务被取消：1901 拦截（specs §4.1.4 规则5）。
func TestReplyTaskCanceled(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusCanceled

	_, err := fx.svc.Reply(context.Background(), answerTokenPlain, "回复")
	wantAnswerErr(t, err, errcode.AnswerTokenInvalid)
	if fx.ansRepo.insertCalled {
		t.Error("取消任务回复不得落库")
	}
}

// TestReplyInsertFail 落库失败：内部错误包裹上抛（specs §5.2.5 第二行）。
func TestReplyInsertFail(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.insertErr = errors.New("insert down")

	_, err := fx.svc.Reply(context.Background(), answerTokenPlain, "回复")
	wantNotServiceErr(t, err)
}

// ---------- Submit ----------

// TestSubmitHappyPath 完整作答：CompleteTask 触发、返回任务号（specs §5.3.2）。
func TestSubmitHappyPath(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 5

	res, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if fx.taskSvc.completeCalls != 1 || fx.taskSvc.gotCompleteID != 8001 {
		t.Errorf("completeCalls/gotCompleteID = %d/%d, want 1/8001", fx.taskSvc.completeCalls, fx.taskSvc.gotCompleteID)
	}
	if res.TaskNo != "T202609290001" {
		t.Errorf("TaskNo = %q, want T202609290001", res.TaskNo)
	}
}

// TestSubmitIncomplete 回复数 < 题数：1903 且不触 CompleteTask（specs §5.3.4 规则1）。
func TestSubmitIncomplete(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 4

	_, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	wantAnswerErr(t, err, errcode.AnswerIncomplete)
	if fx.taskSvc.completeCalls != 0 {
		t.Errorf("completeCalls = %d, want 0（完整性拦截不进事务）", fx.taskSvc.completeCalls)
	}
}

// TestSubmitIdempotent used+completed：短路返回任务号不进事务（03 §1.5 幂等特例）。
func TestSubmitIdempotent(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.link.Status = domain.LinkStatusUsed
	fx.taskRepo.byID.Status = domain.TestTaskStatusCompleted
	now := time.Now()
	fx.taskRepo.byID.CompletedAt = &now

	res, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.TaskNo != "T202609290001" {
		t.Errorf("TaskNo = %q, want T202609290001", res.TaskNo)
	}
	if fx.taskSvc.completeCalls != 0 {
		t.Errorf("completeCalls = %d, want 0（幂等短路不进事务）", fx.taskSvc.completeCalls)
	}
}

// TestSubmitGuardRejected F7 终态守卫 1802 且任务真非 completed（如 canceled）：
// 统一收敛 1901（specs §5.3.4 规则2）。
func TestSubmitGuardRejected(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 5
	fx.taskSvc.completeErr = service.NewError(errcode.TestTaskStatusInvalid)

	_, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	wantAnswerErr(t, err, errcode.AnswerTokenInvalid)
}

// TestSubmitConcurrentRace 并发提交竞态：校验链读到 pending 而 CompleteTask 落地
// 时任务已被并发推 completed，重读任务态后仍返回成功（specs §5.3.4 规则2
// 已 completed 返回成功结果，员工视角页面转成功态）。
func TestSubmitConcurrentRace(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 5
	// 模拟竞态：校验链读到 pending，CompleteTask 因终态守卫返回 1802 的同时
	// 另一请求已把任务行推 completed。
	now := time.Now()
	fx.taskSvc.completeErr = service.NewError(errcode.TestTaskStatusInvalid)
	fx.taskSvc.onGuardReject = func() {
		fx.taskRepo.byID.Status = domain.TestTaskStatusCompleted
		fx.taskRepo.byID.CompletedAt = &now
	}

	res, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Submit 竞态: %v", err)
	}
	if res.TaskNo != "T202609290001" {
		t.Errorf("TaskNo = %q, want T202609290001", res.TaskNo)
	}
}

// TestSubmitCompleteFail 其余事务失败：内部错误包裹上抛（specs §5.3.5 第三行）。
func TestSubmitCompleteFail(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.count = 5
	fx.taskSvc.completeErr = fmt.Errorf("wrap: %w", errors.New("tx rollback"))

	_, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	wantNotServiceErr(t, err)
}

// TestSubmitTokenInvalid A1/A2 无幂等特例：completed 任务的链接 used 后 A2 仍 1901。
func TestSubmitTokenInvalid(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.ansRepo.link.Status = domain.LinkStatusUsed
	fx.taskRepo.byID.Status = domain.TestTaskStatusCompleted

	_, errA1 := fx.svc.Context(context.Background(), answerTokenPlain)
	wantAnswerErr(t, errA1, errcode.AnswerTokenInvalid)
	_, errA2 := fx.svc.Reply(context.Background(), answerTokenPlain, "回复")
	wantAnswerErr(t, errA2, errcode.AnswerTokenInvalid)
}

// TestSubmitExpiredInProgressSubmit in_progress 任务过期链接提交：豁免到期判定，
// 完整性通过即正常完成（specs §4.1.3 顺延至提交）。
func TestSubmitExpiredInProgressSubmit(t *testing.T) {
	fx := newAnswerFixture(t)
	fx.taskRepo.byID.Status = domain.TestTaskStatusInProgress
	fx.ansRepo.link.ExpiresAt = time.Now().Add(-time.Minute)
	fx.ansRepo.count = 5

	res, err := fx.svc.Submit(context.Background(), answerTokenPlain)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.TaskNo != "T202609290001" {
		t.Errorf("TaskNo = %q, want T202609290001", res.TaskNo)
	}
	if fx.taskSvc.completeCalls != 1 {
		t.Errorf("completeCalls = %d, want 1", fx.taskSvc.completeCalls)
	}
}
