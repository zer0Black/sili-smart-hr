// Package service_test 对 assessment_test_task 主动测试业务层做黑盒单元测试。
//
// fakeTSTRepo / fakeTSTQuestionRepo / fakeTSTBatchRepo 复用 fakeDimRepo / fakeUserapiClient /
// fakeBatchSecretRepo 驱动，不依赖真实 DB / 外部 HTTP。覆盖：
//   - Create 六步：枚举校验 1400、对象校验 1805（任务不创建）、AI 组卷 0 题 1803 携维度名、
//     九型未就绪 1804、快照序列化、令牌熵与 7 天有效期、事务收口与同人并存
//   - Resend：expired 回 pending / 新令牌新有效期、pending 换链接、状态守卫 1802/1801
//   - Cancel：成功 / completed 1802 / 查无 1801
//   - Link：正常组装 / 无链接行降级 / 查无 1801
//   - List 枚举校验与 DTO 组装、PollCounts 直通、ScaleStatus 三态
package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"

	"gorm.io/gorm"
)

// fakeTSTRepo 是 repository.AssessmentTestTaskRepository 的测试假实现，字段挂返回与探针。
// ReplaceLink / CancelTask 内联模拟真实条件更新语义（expired→pending、置 canceled）。
type fakeTSTRepo struct {
	listRows  []repository.TestTaskRow
	listTotal int64
	listErr   error
	byID      *domain.AssessmentTestTask
	byIDErr   error
	createErr error
	nextNo    string
	nextNoErr error
	current   *domain.AssessmentTestLink
	currentErr error
	replaceErr    error
	cancelAffected int64
	cancelErr     error
	activeAI       int64
	activeEnne     int64
	countErr       error

	listCalled   bool
	gotFilter    repository.TestTaskFilter
	createCalled bool
	createCalls  int
	createdTask  *domain.AssessmentTestTask
	createdLink  *domain.AssessmentTestLink
	gotNextPrefix string
	replaceTaskID int64
	replacedLink  *domain.AssessmentTestLink
	gotCancelID   int64
}

func (f *fakeTSTRepo) ListByFilter(_ context.Context, tf repository.TestTaskFilter) ([]repository.TestTaskRow, int64, error) {
	f.listCalled = true
	f.gotFilter = tf
	return f.listRows, f.listTotal, f.listErr
}
func (f *fakeTSTRepo) GetByID(_ context.Context, _ int64) (*domain.AssessmentTestTask, error) {
	return f.byID, f.byIDErr
}
func (f *fakeTSTRepo) CreateWithLink(_ context.Context, task *domain.AssessmentTestTask, link *domain.AssessmentTestLink) error {
	f.createCalled = true
	f.createCalls++
	if f.createErr != nil {
		return f.createErr
	}
	if task.ID == 0 {
		task.ID = 9001 // 模拟 GORM 雪花 Create 回调
	}
	link.TaskID = task.ID
	f.createdTask = task
	f.createdLink = link
	return nil
}
func (f *fakeTSTRepo) NextTaskNo(_ context.Context, prefix string, now time.Time) (string, error) {
	f.gotNextPrefix = prefix
	if f.nextNoErr != nil {
		return "", f.nextNoErr
	}
	return f.nextNo, nil
}
func (f *fakeTSTRepo) CurrentLink(_ context.Context, _ int64) (*domain.AssessmentTestLink, error) {
	return f.current, f.currentErr
}
func (f *fakeTSTRepo) ReplaceLink(_ context.Context, taskID int64, newLink *domain.AssessmentTestLink) error {
	f.replaceTaskID = taskID
	f.replacedLink = newLink
	if f.replaceErr != nil {
		return f.replaceErr
	}
	// 模拟事务内条件更新：expired 才回 pending（specs §6.2），pending 重发无状态推进
	if f.byID != nil && f.byID.ID == taskID && f.byID.Status == domain.TestTaskStatusExpired {
		f.byID.Status = domain.TestTaskStatusPending
	}
	newLink.TaskID = taskID
	return nil
}
func (f *fakeTSTRepo) CancelTask(_ context.Context, taskID int64) (int64, error) {
	f.gotCancelID = taskID
	if f.cancelErr != nil {
		return 0, f.cancelErr
	}
	if f.cancelAffected > 0 && f.byID != nil && f.byID.ID == taskID {
		f.byID.Status = domain.TestTaskStatusCanceled
	}
	return f.cancelAffected, nil
}
func (f *fakeTSTRepo) MarkSessionStarted(_ context.Context, _ int64) error { return nil }
func (f *fakeTSTRepo) CountActiveByType(_ context.Context) (int64, int64, error) {
	return f.activeAI, f.activeEnne, f.countErr
}
func (f *fakeTSTRepo) ExpirePending(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

var _ repository.AssessmentTestTaskRepository = (*fakeTSTRepo)(nil)

// fakeTSTQuestionRepo 是 repository.QuestionRepository 的测试假实现，
// ListActiveByDimensionIDs / ListActiveByScaleKey 承载组卷取题行为。
type fakeTSTQuestionRepo struct {
	byDims    map[int64][]domain.Question
	byDimsErr error
	byScale   []domain.Question
	byScaleErr error

	gotDimIDs   []int64
	gotScaleKey string
}

func (f *fakeTSTQuestionRepo) ListPage(_ context.Context, _ string, _ *int64, _, _ string, _, _ int) ([]domain.Question, int64, error) {
	return nil, 0, nil
}
func (f *fakeTSTQuestionRepo) FindByID(_ context.Context, _ int64) (*domain.Question, error) {
	return nil, nil
}
func (f *fakeTSTQuestionRepo) UpdateWithVersion(_ context.Context, _ int64, _ int, _ map[string]any) (int64, error) {
	return 0, nil
}
func (f *fakeTSTQuestionRepo) SoftDeleteWithVersion(_ context.Context, _ int64, _ int) (int64, error) {
	return 0, nil
}
func (f *fakeTSTQuestionRepo) MaxQuestionSeq(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (f *fakeTSTQuestionRepo) ListActiveByDimensionIDs(_ context.Context, dimensionIDs []int64) ([]domain.Question, error) {
	f.gotDimIDs = dimensionIDs
	if f.byDimsErr != nil {
		return nil, f.byDimsErr
	}
	var out []domain.Question
	for _, id := range dimensionIDs {
		out = append(out, f.byDims[id]...)
	}
	return out, nil
}
func (f *fakeTSTQuestionRepo) ListActiveByScaleKey(_ context.Context, scaleKey string) ([]domain.Question, error) {
	f.gotScaleKey = scaleKey
	if f.byScaleErr != nil {
		return nil, f.byScaleErr
	}
	return f.byScale, nil
}
func (f *fakeTSTQuestionRepo) IncrementReferenceCounts(_ context.Context, _ *gorm.DB, _ []int64) error {
	return nil
}

var _ repository.QuestionRepository = (*fakeTSTQuestionRepo)(nil)

// fakeTSTBatchRepo 是 repository.QuestionBatchRepository 的测试假实现，
// 仅 FindLatestImportedBatch 有行为（最新引入量批判定）。
type fakeTSTBatchRepo struct {
	imported    *domain.QuestionBatch
	importedErr error
}

func (f *fakeTSTBatchRepo) ListPending(_ context.Context) ([]domain.QuestionBatch, error) { return nil, nil }
func (f *fakeTSTBatchRepo) FindByID(_ context.Context, _ int64) (*domain.QuestionBatch, error) {
	return nil, nil
}
func (f *fakeTSTBatchRepo) ListQuestionsByBatchID(_ context.Context, _ int64) ([]domain.Question, error) {
	return nil, nil
}
func (f *fakeTSTBatchRepo) FindPendingResubmitBatch(_ context.Context) (*domain.QuestionBatch, error) {
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeTSTBatchRepo) CreateBatchWithQuestions(_ context.Context, _ *gorm.DB, _ *domain.QuestionBatch, _ []domain.Question) error {
	return nil
}
func (f *fakeTSTBatchRepo) NextBatchNo(_ context.Context, _ string, _ time.Time) (string, error) {
	return "", nil
}
func (f *fakeTSTBatchRepo) ConfirmBatch(_ context.Context, _ int64, _ map[int64]string) (int64, int64, error) {
	return 0, 0, nil
}
func (f *fakeTSTBatchRepo) VoidBatch(_ context.Context, _ int64) error { return nil }
func (f *fakeTSTBatchRepo) ResubmitToBatch(_ context.Context, _ *domain.Question, _ map[string]any) (*domain.QuestionBatch, error) {
	return nil, nil
}
func (f *fakeTSTBatchRepo) FindLatestImportedBatch(_ context.Context) (*domain.QuestionBatch, error) {
	return f.imported, f.importedErr
}

var _ repository.QuestionBatchRepository = (*fakeTSTBatchRepo)(nil)

// tstFixedNow 固定本地时区时刻，创建时间与有效期断言以此为锚。
func tstFixedNow() time.Time { return time.Date(2026, 9, 28, 8, 30, 0, 0, time.Local) }

// newTSTSvc 组装被测 service；密文用 batchEncKey 加密，与构造注入的 encKey 同源。
func newTSTSvc(t *testing.T, taskRepo *fakeTSTRepo, qRepo *fakeTSTQuestionRepo, bRepo *fakeTSTBatchRepo, dimRepo *fakeDimRepo, ua *fakeUserapiClient, now func() time.Time) service.AssessmentTestTaskService {
	t.Helper()
	secretRepo := &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1, SecretCipher: encryptedSecret(t, "sec")}}
	return service.NewAssessmentTestTaskService(taskRepo, qRepo, bRepo, dimRepo, ua, secretRepo, batchEncKey, now)
}

// aiMgmtEnabledDims 模拟 ListEnabledFullByDataSource(TEST) 返回：5 个启用 AI_MGMT 子能力
// 加 1 个 ENNEAGRAM 维度（同为 TEST 来源，验证 ModuleCode 过滤）。
func aiMgmtEnabledDims() []domain.Dimension {
	return []domain.Dimension{
		{ID: 101, Code: "MGT_PLAN", Name: "任务规划", ModuleCode: domain.ModuleAIMgmt, DataSource: domain.SourceTest, Enabled: true},
		{ID: 102, Code: "MGT_DELEGATE", Name: "授权分工", ModuleCode: domain.ModuleAIMgmt, DataSource: domain.SourceTest, Enabled: true},
		{ID: 103, Code: "MGT_RISK", Name: "风险识别", ModuleCode: domain.ModuleAIMgmt, DataSource: domain.SourceTest, Enabled: true},
		{ID: 104, Code: "MGT_ALIGN", Name: "目标对齐", ModuleCode: domain.ModuleAIMgmt, DataSource: domain.SourceTest, Enabled: true},
		{ID: 105, Code: "MGT_JUDGE", Name: "决策判断", ModuleCode: domain.ModuleAIMgmt, DataSource: domain.SourceTest, Enabled: true},
		{ID: 90, Code: "ENNE_TYPE_1", Name: "完美型", ModuleCode: domain.ModuleEnneagram, DataSource: domain.SourceTest, Enabled: true},
	}
}

// aiMgmtQuestions 两维度各 2 题（specs §4.2.4 规则1 全取即全做）。
func aiMgmtQuestions() map[int64][]domain.Question {
	return map[int64][]domain.Question{
		101: {
			{ID: 3001, DimensionID: 101, QuestionNo: "Q-AG-0001"},
			{ID: 3002, DimensionID: 101, QuestionNo: "Q-AG-0002"},
		},
		102: {
			{ID: 3003, DimensionID: 102, QuestionNo: "Q-AG-0003"},
			{ID: 3004, DimensionID: 102, QuestionNo: "Q-AG-0004"},
		},
	}
}

// scaleQuestions 九型量表启用题（specs §4.2.4 规则2 固定量表全量）。
func scaleQuestions() []domain.Question {
	return []domain.Question{
		{ID: 5001, ScaleKey: domain.ScaleKeyRisoHudson, QuestionNo: "Q-Scale-0001"},
		{ID: 5002, ScaleKey: domain.ScaleKeyRisoHudson, QuestionNo: "Q-Scale-0002"},
		{ID: 5003, ScaleKey: domain.ScaleKeyRisoHudson, QuestionNo: "Q-Scale-0003"},
	}
}

// aiMgmtHappyEnv 快乐路径公共装配，多个用例共享。
func aiMgmtHappyEnv(t *testing.T) (*fakeTSTRepo, *fakeTSTQuestionRepo, *fakeTSTBatchRepo, *fakeDimRepo, *fakeUserapiClient, service.AssessmentTestTaskService) {
	t.Helper()
	taskRepo := &fakeTSTRepo{nextNo: "T202609280001"}
	qRepo := &fakeTSTQuestionRepo{byDims: aiMgmtQuestions()}
	bRepo := &fakeTSTBatchRepo{}
	dimRepo := &fakeDimRepo{dims: aiMgmtEnabledDims()}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "u1", StaffName: "张敏"}}}
	svc := newTSTSvc(t, taskRepo, qRepo, bRepo, dimRepo, ua, tstFixedNow)
	return taskRepo, qRepo, bRepo, dimRepo, ua, svc
}

// wantServiceErr 断言 err 为 *service.Error 且 code 等于 want。
func wantServiceErr(t *testing.T, err error, want int) {
	t.Helper()
	serr, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("err 类型 %T, want *service.Error（err=%v）", err, err)
	}
	if serr.Code != want {
		t.Errorf("code = %d, want %d", serr.Code, want)
	}
}

// TestCreateAIMgmtHappyPath 核心断言：task_no 前缀 T、status=pending、answer_url 形态、
// 快照 4 题、链接 7 天有效期、CreateWithLink 事务收口（引用计数 +1 在该事务内，T3 已实现）。
func TestCreateAIMgmtHappyPath(t *testing.T) {
	taskRepo, _, _, dimRepo, ua, svc := aiMgmtHappyEnv(t)

	res, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
		TestType:     domain.TestTypeAIMgmt,
		StaffID:      "u1",
		StaffName:    "张敏",
		DimensionIDs: []string{"101", "102"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(res.TaskNo, "T") {
		t.Errorf("TaskNo = %q, want T 前缀", res.TaskNo)
	}
	if res.TaskNo != "T202609280001" {
		t.Errorf("TaskNo = %q, want 透传 NextTaskNo 返回值", res.TaskNo)
	}
	if res.Status != domain.TestTaskStatusPending {
		t.Errorf("Status = %q, want pending", res.Status)
	}
	if res.TestType != domain.TestTypeAIMgmt || res.StaffName != "张敏" || res.ID != 9001 {
		t.Errorf("res = %+v", res)
	}
	if res.CreatedAt != "2026-09-28 08:30" {
		t.Errorf("CreatedAt = %q, want 2026-09-28 08:30", res.CreatedAt)
	}
	urlRe := regexp.MustCompile(`^/answer/[A-Za-z0-9_-]{43}$`)
	if !urlRe.MatchString(res.AnswerURL) {
		t.Errorf("AnswerURL = %q, want /answer/{43 字符 base64url 令牌}", res.AnswerURL)
	}
	if !taskRepo.createCalled {
		t.Fatal("CreateWithLink 未被调用（任务+链接+引用计数应单事务收口）")
	}
	if taskRepo.gotNextPrefix != "T" {
		t.Errorf("NextTaskNo prefix = %q, want T", taskRepo.gotNextPrefix)
	}
	if dimRepo.gotDataSource != domain.SourceTest {
		t.Errorf("ListEnabledFullByDataSource 入参 = %q, want TEST", dimRepo.gotDataSource)
	}
	if !ua.called || ua.lastKW != "张敏" || ua.lastSecret != "sec" {
		t.Errorf("staff 校验 ListStaffs 调用不符: called=%v kw=%q secret=%q", ua.called, ua.lastKW, ua.lastSecret)
	}

	task := taskRepo.createdTask
	if task.Status != domain.TestTaskStatusPending || task.GradingStatus != domain.GradingStatusWaiting {
		t.Errorf("task 双状态轴 = %q/%q, want pending/waiting", task.Status, task.GradingStatus)
	}
	if task.StaffID != "u1" || task.StaffName != "张敏" || task.TestType != domain.TestTypeAIMgmt {
		t.Errorf("task 对象快照 = %+v", task)
	}
	if task.ScaleKey != "" {
		t.Errorf("ai_mgmt 任务 ScaleKey = %q, want 空串", task.ScaleKey)
	}
	var ids []int64
	if err := json.Unmarshal([]byte(task.QuestionIDsJSON), &ids); err != nil {
		t.Fatalf("question_ids_json 非法: %v", err)
	}
	if len(ids) != 4 {
		t.Fatalf("快照题目数 = %d, want 4（两维度各 2 题全取）", len(ids))
	}
	want := map[int64]bool{3001: true, 3002: true, 3003: true, 3004: true}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("快照题目 %d 不在预期集合", id)
		}
	}
	var codes []string
	if err := json.Unmarshal([]byte(task.DimensionCodesJSON), &codes); err != nil {
		t.Fatalf("dimension_codes_json 非法: %v", err)
	}
	if len(codes) != 2 || codes[0] != "MGT_PLAN" || codes[1] != "MGT_DELEGATE" {
		t.Errorf("dimension_codes_json = %v, want [MGT_PLAN MGT_DELEGATE]", codes)
	}

	link := taskRepo.createdLink
	if !link.GeneratedAt.Equal(tstFixedNow()) {
		t.Errorf("GeneratedAt = %v, want now", link.GeneratedAt)
	}
	if !link.ExpiresAt.Equal(tstFixedNow().Add(7 * 24 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want now+7 天（BR7 有效期常量）", link.ExpiresAt)
	}
	if link.Status != domain.LinkStatusValid || link.TaskID != 9001 {
		t.Errorf("link = %+v, want valid 且绑定任务", link)
	}
	hashRe := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !hashRe.MatchString(link.TokenHash) {
		t.Errorf("TokenHash = %q, want 64 位 hex", link.TokenHash)
	}
	if len(link.TokenPlain) != 43 {
		t.Errorf("TokenPlain 长度 = %d, want 43", len(link.TokenPlain))
	}
}

// TestCreateAIMgmtDuplicateAllowed：同人同类型二次发起均成功，无重复校验无告警（specs §4.1.4 规则3）。
func TestCreateAIMgmtDuplicateAllowed(t *testing.T) {
	taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)
	p := service.CreateTestTaskPayload{
		TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
		DimensionIDs: []string{"101", "102"},
	}
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Fatalf("第一次 Create: %v", err)
	}
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Fatalf("同人二次 Create 应放行（specs §4.1.4 规则3）: %v", err)
	}
	if taskRepo.createCalls != 2 {
		t.Errorf("createCalls = %d, want 2（两次各自建任务）", taskRepo.createCalls)
	}
}

// TestCreateTSTValidation 表驱动覆盖枚举校验（specs §4.2.2/03 B1 1400 行），
// userapi 与 CreateWithLink 均不得被触达。
func TestCreateTSTValidation(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*service.CreateTestTaskPayload)
	}{
		{"test_type 非枚举", func(p *service.CreateTestTaskPayload) { p.TestType = "foo" }},
		{"test_type 空", func(p *service.CreateTestTaskPayload) { p.TestType = "" }},
		{"staff_id 空", func(p *service.CreateTestTaskPayload) { p.StaffID = "" }},
		{"staff_name 空", func(p *service.CreateTestTaskPayload) { p.StaffName = "" }},
		{"staff_name 空白", func(p *service.CreateTestTaskPayload) { p.StaffName = "  " }},
		{"ai_mgmt dimension_ids 空", func(p *service.CreateTestTaskPayload) { p.DimensionIDs = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskRepo, _, _, _, ua, svc := aiMgmtHappyEnv(t)
			p := service.CreateTestTaskPayload{
				TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
				DimensionIDs: []string{"101"},
			}
			tc.mod(&p)

			_, err := svc.Create(context.Background(), p)
			wantServiceErr(t, err, errcode.BadRequest)
			if ua.called {
				t.Error("枚举校验失败不应触达 userapi")
			}
			if taskRepo.createCalled {
				t.Error("校验失败不应创建任务")
			}
		})
	}
}

// TestCreateDimensionNotEnabled：dimension_ids 含停用/非 AI_MGMT/不存在/非数字项返 1400
//（勾选范围限当前启用 AI_MGMT 子能力，specs §4.2.2 A）。
func TestCreateDimensionNotEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"不存在", []string{"999"}},
		{"非 AI_MGMT 模块", []string{"90"}},
		{"非数字", []string{"abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)
			_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
				TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
				DimensionIDs: tc.ids,
			})
			wantServiceErr(t, err, errcode.BadRequest)
			if taskRepo.createCalled {
				t.Error("维度集合非法不应创建任务")
			}
		})
	}
}

// TestCreateDimensionEmpty：勾选维度中 104 无启用题返 1803 且 Msg 携维度名
//（specs §4.2.4 规则1「子能力 X 无可用题目，请先在题库补充」）。
func TestCreateDimensionEmpty(t *testing.T) {
	taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)

	_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
		TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
		DimensionIDs: []string{"101", "104"},
	})
	wantServiceErr(t, err, errcode.TestDimensionQuestionsEmpty)
	serr, _ := err.(*service.Error)
	if !strings.Contains(serr.Msg, "目标对齐") {
		t.Errorf("Msg = %q, want 携维度名「目标对齐」", serr.Msg)
	}
	if !strings.Contains(serr.Msg, "无可用题目") {
		t.Errorf("Msg = %q, want 含 specs §4.2.4 规则1 文案", serr.Msg)
	}
	if taskRepo.createCalled {
		t.Error("0 题维度不应创建任务")
	}
}

// TestCreateStaffInvalid：上游返回空列表或 staff_id+staff_name 双匹配未命中返 1805，
// 任务不创建（specs §5.1.5 第一行）。
func TestCreateStaffInvalid(t *testing.T) {
	t.Run("上游空列表", func(t *testing.T) {
		taskRepo, _, _, _, ua, svc := aiMgmtHappyEnv(t)
		ua.staffs = nil
		_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
			TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
			DimensionIDs: []string{"101"},
		})
		wantServiceErr(t, err, errcode.TestStaffInvalid)
		if taskRepo.createCalled {
			t.Error("人员无效不应创建任务")
		}
	})
	t.Run("姓名不匹配", func(t *testing.T) {
		taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)
		_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
			TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "李芳",
			DimensionIDs: []string{"101"},
		})
		wantServiceErr(t, err, errcode.TestStaffInvalid)
		if taskRepo.createCalled {
			t.Error("人员无效不应创建任务")
		}
	})
	t.Run("上游不可达", func(t *testing.T) {
		taskRepo, _, _, _, ua, svc := aiMgmtHappyEnv(t)
		ua.err = errors.New("upstream down")
		_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
			TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
			DimensionIDs: []string{"101"},
		})
		wantServiceErr(t, err, errcode.TestStaffInvalid)
		if taskRepo.createCalled {
			t.Error("上游不可达不应创建任务")
		}
	})
}

// TestCreateEnneagramScaleNotReady：无 IMPORT 批次或量表启用题为空返 1804，任务不创建
//（specs §4.2.4 规则2）。
func TestCreateEnneagramScaleNotReady(t *testing.T) {
	t.Run("未引入", func(t *testing.T) {
		taskRepo, _, bRepo, _, _, svc := aiMgmtHappyEnv(t)
		bRepo.importedErr = gorm.ErrRecordNotFound
		_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
			TestType: domain.TestTypeEnneagram, StaffID: "u1", StaffName: "张敏",
		})
		wantServiceErr(t, err, errcode.TestScaleNotReady)
		if taskRepo.createCalled {
			t.Error("量表未就绪不应创建任务")
		}
	})
	t.Run("全部停用", func(t *testing.T) {
		taskRepo, _, bRepo, _, _, svc := aiMgmtHappyEnv(t)
		bRepo.imported = &domain.QuestionBatch{ScaleKey: domain.ScaleKeyRisoHudson}
		_, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
			TestType: domain.TestTypeEnneagram, StaffID: "u1", StaffName: "张敏",
		})
		wantServiceErr(t, err, errcode.TestScaleNotReady)
		if taskRepo.createCalled {
			t.Error("量表无启用题不应创建任务")
		}
	})
}

// TestCreateEnneagramHappyPath：最新引入量表启用题全量入快照，任务号 E 前缀，
// scale_key 留痕、dimension_codes_json 为空数组。
func TestCreateEnneagramHappyPath(t *testing.T) {
	taskRepo := &fakeTSTRepo{nextNo: "E202609280001"}
	qRepo := &fakeTSTQuestionRepo{byScale: scaleQuestions()}
	bRepo := &fakeTSTBatchRepo{imported: &domain.QuestionBatch{ScaleKey: domain.ScaleKeyRisoHudson}}
	dimRepo := &fakeDimRepo{dims: aiMgmtEnabledDims()}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "u1", StaffName: "张敏"}}}
	svc := newTSTSvc(t, taskRepo, qRepo, bRepo, dimRepo, ua, tstFixedNow)

	res, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
		TestType: domain.TestTypeEnneagram, StaffID: "u1", StaffName: "张敏",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(res.TaskNo, "E") {
		t.Errorf("TaskNo = %q, want E 前缀", res.TaskNo)
	}
	if taskRepo.gotNextPrefix != "E" {
		t.Errorf("NextTaskNo prefix = %q, want E", taskRepo.gotNextPrefix)
	}
	if qRepo.gotScaleKey != domain.ScaleKeyRisoHudson {
		t.Errorf("ListActiveByScaleKey 入参 = %q, want 批次 scale_key", qRepo.gotScaleKey)
	}
	task := taskRepo.createdTask
	if task.ScaleKey != domain.ScaleKeyRisoHudson {
		t.Errorf("ScaleKey = %q, want RISO_HUDSON 留痕", task.ScaleKey)
	}
	if task.DimensionCodesJSON != "[]" {
		t.Errorf("DimensionCodesJSON = %q, want []（enneagram 空数组）", task.DimensionCodesJSON)
	}
	var ids []int64
	if err := json.Unmarshal([]byte(task.QuestionIDsJSON), &ids); err != nil {
		t.Fatalf("question_ids_json 非法: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("快照题目数 = %d, want 3（量表全量）", len(ids))
	}
	if res.Status != domain.TestTaskStatusPending {
		t.Errorf("Status = %q, want pending", res.Status)
	}
}

// TestTokenEntropy：连续两次生成的令牌互不相等、恒 43 字符 base64url，
// token_hash 为 64 位 hex 且等于原文 SHA-256（specs §5.1.4 规则1 随机不可预测）。
func TestTokenEntropy(t *testing.T) {
	taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)
	p := service.CreateTestTaskPayload{
		TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
		DimensionIDs: []string{"101"},
	}
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Fatalf("第一次 Create: %v", err)
	}
	first := *taskRepo.createdLink
	if _, err := svc.Create(context.Background(), p); err != nil {
		t.Fatalf("第二次 Create: %v", err)
	}
	second := *taskRepo.createdLink

	tokenRe := regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	hashRe := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !tokenRe.MatchString(first.TokenPlain) || !tokenRe.MatchString(second.TokenPlain) {
		t.Errorf("令牌形态非法: %q / %q", first.TokenPlain, second.TokenPlain)
	}
	if first.TokenPlain == second.TokenPlain {
		t.Error("连续两次生成的令牌相等，熵不足")
	}
	if !hashRe.MatchString(first.TokenHash) || !hashRe.MatchString(second.TokenHash) {
		t.Errorf("哈希形态非法: %q / %q", first.TokenHash, second.TokenHash)
	}
	if first.TokenHash == second.TokenHash {
		t.Error("两次令牌哈希相等")
	}
	// 哈希正确性：sha256(plain) 的 hex 与落库值一致
	sum := sha256.Sum256([]byte(second.TokenPlain))
	if hex.EncodeToString(sum[:]) != second.TokenHash {
		t.Error("token_hash 与 sha256(token_plain) 不符")
	}
}

// tstExpiredTask 构造 expired 任务行与旧链接（Resend 用例共享）。
func tstExpiredTask() (*domain.AssessmentTestTask, *domain.AssessmentTestLink) {
	task := &domain.AssessmentTestTask{
		ID: 7, TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt,
		StaffID: "u1", StaffName: "张敏", Status: domain.TestTaskStatusExpired,
		GradingStatus: domain.GradingStatusWaiting, QuestionIDsJSON: "[3001]",
	}
	oldLink := &domain.AssessmentTestLink{
		TaskID: 7, TokenPlain: "old-token-00000000000000000000000000000000000000",
		TokenHash: strings.Repeat("0", 64), Status: domain.LinkStatusInvalid,
		GeneratedAt: tstFixedNow().AddDate(0, 0, -8),
		ExpiresAt:   tstFixedNow().AddDate(0, 0, -1),
	}
	return task, oldLink
}

// TestResendExpiredBackToPending 核心断言：expired 任务重发后状态回 pending、
// 新令牌不等于旧令牌、expires_at=新时刻+7 天（specs §4.1.3/§4.1.4 规则1）。
func TestResendExpiredBackToPending(t *testing.T) {
	task, oldLink := tstExpiredTask()
	taskRepo := &fakeTSTRepo{byID: task, nextNo: "T202609280001"}
	// 推进 now 至重发时刻（新有效期自重发起算）
	resendAt := tstFixedNow().Add(48 * time.Hour)
	svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, func() time.Time { return resendAt })

	dto, err := svc.Resend(context.Background(), 7)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if task.Status != domain.TestTaskStatusPending {
		t.Errorf("任务状态 = %q, want expired 重发后回 pending", task.Status)
	}
	if taskRepo.replacedLink == nil || taskRepo.replacedLink.TokenPlain == oldLink.TokenPlain {
		t.Error("新令牌应重新生成，不得复用旧令牌")
	}
	if !taskRepo.replacedLink.ExpiresAt.Equal(resendAt.Add(7 * 24 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want 重发时刻+7 天", taskRepo.replacedLink.ExpiresAt)
	}
	if !taskRepo.replacedLink.GeneratedAt.Equal(resendAt) {
		t.Errorf("GeneratedAt = %v, want 重发时刻", taskRepo.replacedLink.GeneratedAt)
	}
	if taskRepo.replacedLink.Status != domain.LinkStatusValid {
		t.Errorf("新链接状态 = %q, want valid", taskRepo.replacedLink.Status)
	}
	// 返回 DTO 与新链接一致（link_status 恒 valid，03 C2）
	if dto.TaskID != 7 || dto.TaskNo != "T202609280001" || dto.TestType != domain.TestTypeAIMgmt || dto.StaffName != "张敏" {
		t.Errorf("dto 元信息 = %+v", dto)
	}
	if dto.LinkStatus != domain.LinkStatusValid {
		t.Errorf("LinkStatus = %q, want valid", dto.LinkStatus)
	}
	if !strings.HasPrefix(dto.AnswerURL, "/answer/") || strings.Contains(dto.AnswerURL, oldLink.TokenPlain) {
		t.Errorf("AnswerURL = %q, want 新令牌链接", dto.AnswerURL)
	}
	if dto.GeneratedAt != resendAt.Local().Format("2006-01-02 15:04") {
		t.Errorf("GeneratedAt = %q", dto.GeneratedAt)
	}
	if dto.ExpiresAt != resendAt.Add(7*24*time.Hour).Local().Format("2006-01-02 15:04") {
		t.Errorf("ExpiresAt = %q", dto.ExpiresAt)
	}
}

// TestResendPendingKeepsStatus：pending 任务重发仅换链接，任务状态保持 pending。
func TestResendPendingKeepsStatus(t *testing.T) {
	task := &domain.AssessmentTestTask{
		ID: 7, TaskNo: "T202609280001", TestType: domain.TestTypeEnneagram,
		StaffID: "u1", StaffName: "张敏", Status: domain.TestTaskStatusPending,
		GradingStatus: domain.GradingStatusWaiting, QuestionIDsJSON: "[5001]", ScaleKey: domain.ScaleKeyRisoHudson,
	}
	taskRepo := &fakeTSTRepo{byID: task}
	svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

	dto, err := svc.Resend(context.Background(), 7)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if task.Status != domain.TestTaskStatusPending {
		t.Errorf("任务状态 = %q, want pending 不变", task.Status)
	}
	if dto.LinkStatus != domain.LinkStatusValid {
		t.Errorf("LinkStatus = %q, want valid", dto.LinkStatus)
	}
}

// TestResendStatusGuard 核心断言：completed 任务返 1802、不存在任务返 1801，
// 其余非法态（in_progress/canceled）同样 1802。
func TestResendStatusGuard(t *testing.T) {
	for _, st := range []string{
		domain.TestTaskStatusInProgress, domain.TestTaskStatusCompleted, domain.TestTaskStatusCanceled,
	} {
		task := &domain.AssessmentTestTask{ID: 7, Status: st}
		taskRepo := &fakeTSTRepo{byID: task}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, err := svc.Resend(context.Background(), 7)
		wantServiceErr(t, err, errcode.TestTaskStatusInvalid)
		if taskRepo.replacedLink != nil {
			t.Errorf("status=%s 不应触发 ReplaceLink", st)
		}
	}

	missing := &fakeTSTRepo{}
	svc := newTSTSvc(t, missing, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
	_, err := svc.Resend(context.Background(), 999)
	wantServiceErr(t, err, errcode.TestTaskNotFound)
}

// TestCancel：pending 可取消返 canceled；completed/canceled 返 1802；查无返 1801（specs §4.1.4 规则2）。
func TestCancel(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		task := &domain.AssessmentTestTask{ID: 7, Status: domain.TestTaskStatusPending}
		taskRepo := &fakeTSTRepo{byID: task, cancelAffected: 1}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.Cancel(context.Background(), 7)
		if err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if dto.TaskID != 7 || dto.Status != domain.TestTaskStatusCanceled {
			t.Errorf("dto = %+v, want task_id=7 status=canceled", dto)
		}
		if taskRepo.gotCancelID != 7 {
			t.Errorf("CancelTask taskID = %d, want 7", taskRepo.gotCancelID)
		}
	})
	t.Run("completed 状态拒绝", func(t *testing.T) {
		task := &domain.AssessmentTestTask{ID: 7, Status: domain.TestTaskStatusCompleted}
		taskRepo := &fakeTSTRepo{byID: task, cancelAffected: 0}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, err := svc.Cancel(context.Background(), 7)
		wantServiceErr(t, err, errcode.TestTaskStatusInvalid)
	})
	t.Run("并发重复取消（已 canceled）", func(t *testing.T) {
		task := &domain.AssessmentTestTask{ID: 7, Status: domain.TestTaskStatusCanceled}
		taskRepo := &fakeTSTRepo{byID: task, cancelAffected: 0}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, err := svc.Cancel(context.Background(), 7)
		wantServiceErr(t, err, errcode.TestTaskStatusInvalid)
	})
	t.Run("查无", func(t *testing.T) {
		taskRepo := &fakeTSTRepo{}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, err := svc.Cancel(context.Background(), 999)
		wantServiceErr(t, err, errcode.TestTaskNotFound)
	})
}

// TestLink：正常组装 DTO；无链接行降级 answer_url 空串 link_status=invalid；查无 1801。
func TestLink(t *testing.T) {
	t.Run("正常", func(t *testing.T) {
		task := &domain.AssessmentTestTask{
			ID: 7, TaskNo: "E202609280001", TestType: domain.TestTypeEnneagram,
			StaffName: "张敏", Status: domain.TestTaskStatusPending,
		}
		link := &domain.AssessmentTestLink{
			TaskID: 7, TokenPlain: "tok-00000000000000000000000000000000000000000",
			Status: domain.LinkStatusValid, GeneratedAt: tstFixedNow(),
			ExpiresAt: tstFixedNow().Add(7 * 24 * time.Hour),
		}
		taskRepo := &fakeTSTRepo{byID: task, current: link}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.Link(context.Background(), 7)
		if err != nil {
			t.Fatalf("Link: %v", err)
		}
		if dto.TaskID != 7 || dto.TaskNo != "E202609280001" || dto.TestType != domain.TestTypeEnneagram || dto.StaffName != "张敏" {
			t.Errorf("dto 元信息 = %+v", dto)
		}
		if dto.AnswerURL != "/answer/"+link.TokenPlain {
			t.Errorf("AnswerURL = %q, want 含令牌原文", dto.AnswerURL)
		}
		if dto.LinkStatus != domain.LinkStatusValid {
			t.Errorf("LinkStatus = %q, want valid", dto.LinkStatus)
		}
		if dto.GeneratedAt != "2026-09-28 08:30" || dto.ExpiresAt != "2026-10-05 08:30" {
			t.Errorf("时间 = %q/%q", dto.GeneratedAt, dto.ExpiresAt)
		}
	})
	t.Run("无链接行降级", func(t *testing.T) {
		task := &domain.AssessmentTestTask{ID: 7, TaskNo: "T1", TestType: domain.TestTypeAIMgmt, StaffName: "张敏"}
		taskRepo := &fakeTSTRepo{byID: task}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.Link(context.Background(), 7)
		if err != nil {
			t.Fatalf("Link: %v", err)
		}
		if dto.AnswerURL != "" || dto.LinkStatus != domain.LinkStatusInvalid {
			t.Errorf("无链接行降级 = %q/%q, want 空串/invalid", dto.AnswerURL, dto.LinkStatus)
		}
	})
	t.Run("查无", func(t *testing.T) {
		svc := newTSTSvc(t, &fakeTSTRepo{}, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, err := svc.Link(context.Background(), 999)
		wantServiceErr(t, err, errcode.TestTaskNotFound)
	})
}

// TestList：枚举校验（test_type 必填二值、status 可空五值）与 DTO 组装
//（created_at/completed_at 直出 yyyy-MM-dd HH:mm，未完成 completed_at 为 nil）。
func TestList(t *testing.T) {
	t.Run("组装", func(t *testing.T) {
		completedAt := tstFixedNow().Add(2 * time.Hour)
		taskRepo := &fakeTSTRepo{
			listRows: []repository.TestTaskRow{
				{Task: domain.AssessmentTestTask{
					ID: 1, TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt, StaffName: "张敏",
					Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
					CreatedAt: tstFixedNow(),
				}, LinkStatus: domain.LinkStatusValid},
				{Task: domain.AssessmentTestTask{
					ID: 2, TaskNo: "T202609280002", TestType: domain.TestTypeAIMgmt, StaffName: "李芳",
					Status: domain.TestTaskStatusCompleted, GradingStatus: domain.GradingStatusGrading,
					CompletedAt: &completedAt, CreatedAt: tstFixedNow(),
				}, LinkStatus: domain.LinkStatusUsed},
			},
			listTotal: 2,
		}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		list, total, err := svc.List(context.Background(), service.ListTestTaskFilter{
			TestType: domain.TestTypeAIMgmt, Status: "", Keyword: "张", Page: 1, PageSize: 10,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 || len(list) != 2 {
			t.Fatalf("total=%d len=%d, want 2/2", total, len(list))
		}
		got := taskRepo.gotFilter
		if got.TestType != domain.TestTypeAIMgmt || got.Status != "" || got.Keyword != "张" || got.Page != 1 || got.PageSize != 10 {
			t.Errorf("repo 收到 filter = %+v", got)
		}
		if list[0].ID != 1 || list[0].TaskNo != "T202609280001" || list[0].TestType != domain.TestTypeAIMgmt ||
			list[0].StaffName != "张敏" || list[0].Status != domain.TestTaskStatusPending ||
			list[0].LinkStatus != domain.LinkStatusValid || list[0].GradingStatus != domain.GradingStatusWaiting {
			t.Errorf("list[0] = %+v", list[0])
		}
		if list[0].CreatedAt != "2026-09-28 08:30" {
			t.Errorf("CreatedAt = %q", list[0].CreatedAt)
		}
		if list[0].CompletedAt != nil {
			t.Errorf("未完成 CompletedAt = %v, want nil", *list[0].CompletedAt)
		}
		if list[1].CompletedAt == nil || *list[1].CompletedAt != "2026-09-28 10:30" {
			t.Errorf("已完成 CompletedAt = %v, want 2026-09-28 10:30", list[1].CompletedAt)
		}
	})
	t.Run("枚举校验", func(t *testing.T) {
		svc := newTSTSvc(t, &fakeTSTRepo{}, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		for _, f := range []service.ListTestTaskFilter{
			{TestType: "", Page: 1, PageSize: 10},
			{TestType: "foo", Page: 1, PageSize: 10},
			{TestType: domain.TestTypeAIMgmt, Status: "foo", Page: 1, PageSize: 10},
		} {
			_, _, err := svc.List(context.Background(), f)
			wantServiceErr(t, err, errcode.BadRequest)
		}
	})
	t.Run("仓储错误包装上抛", func(t *testing.T) {
		taskRepo := &fakeTSTRepo{listErr: errors.New("db down")}
		svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)
		_, _, err := svc.List(context.Background(), service.ListTestTaskFilter{TestType: domain.TestTypeAIMgmt})
		var serr *service.Error
		if err == nil || errors.As(err, &serr) {
			t.Errorf("err = %v, want 非 service.Error 的包装错误", err)
		}
	})
}

// TestPollCounts：CountActiveByType 直通（03 A2 两类计数）。
func TestPollCounts(t *testing.T) {
	taskRepo := &fakeTSTRepo{activeAI: 3, activeEnne: 1}
	svc := newTSTSvc(t, taskRepo, &fakeTSTQuestionRepo{}, &fakeTSTBatchRepo{}, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

	dto, err := svc.PollCounts(context.Background())
	if err != nil {
		t.Fatalf("PollCounts: %v", err)
	}
	if dto.AIMgmtActive != 3 || dto.EnneagramActive != 1 {
		t.Errorf("dto = %+v, want {3 1}", dto)
	}
}

// TestScaleStatus：就绪返量表信息与启用题数；未引入/全部停用返 Ready=false 空串零值，
// 弹窗打开不阻断（03 B2）。
func TestScaleStatus(t *testing.T) {
	t.Run("就绪", func(t *testing.T) {
		qRepo := &fakeTSTQuestionRepo{byScale: scaleQuestions()}
		bRepo := &fakeTSTBatchRepo{imported: &domain.QuestionBatch{ScaleKey: domain.ScaleKeyRisoHudson}}
		svc := newTSTSvc(t, &fakeTSTRepo{}, qRepo, bRepo, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.ScaleStatus(context.Background())
		if err != nil {
			t.Fatalf("ScaleStatus: %v", err)
		}
		if !dto.Ready || dto.ScaleKey != domain.ScaleKeyRisoHudson || dto.ActiveQuestionCount != 3 {
			t.Errorf("dto = %+v, want ready RISO_HUDSON/3 题", dto)
		}
		if dto.ScaleName == "" {
			t.Error("ScaleName 应为内置模板名")
		}
	})
	t.Run("未引入", func(t *testing.T) {
		bRepo := &fakeTSTBatchRepo{importedErr: gorm.ErrRecordNotFound}
		svc := newTSTSvc(t, &fakeTSTRepo{}, &fakeTSTQuestionRepo{}, bRepo, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.ScaleStatus(context.Background())
		if err != nil {
			t.Fatalf("ScaleStatus 未就绪不应报错: %v", err)
		}
		if dto.Ready || dto.ScaleKey != "" || dto.ScaleName != "" || dto.ActiveQuestionCount != 0 {
			t.Errorf("dto = %+v, want false 空串零值", dto)
		}
	})
	t.Run("全部停用", func(t *testing.T) {
		bRepo := &fakeTSTBatchRepo{imported: &domain.QuestionBatch{ScaleKey: domain.ScaleKeyEssence}}
		svc := newTSTSvc(t, &fakeTSTRepo{}, &fakeTSTQuestionRepo{}, bRepo, &fakeDimRepo{}, &fakeUserapiClient{}, tstFixedNow)

		dto, err := svc.ScaleStatus(context.Background())
		if err != nil {
			t.Fatalf("ScaleStatus: %v", err)
		}
		if dto.Ready || dto.ScaleKey != "" || dto.ActiveQuestionCount != 0 {
			t.Errorf("dto = %+v, want false 空串零值", dto)
		}
	})
}

// TestCreateDimensionIDNormalizes：dimension_ids 顺序打乱时快照编码仍按维度表 code ASC 序落
//（维度表 ListEnabledFullByDataSource 排序为锚）。
func TestCreateDimensionIDNormalizes(t *testing.T) {
	taskRepo, _, _, _, _, svc := aiMgmtHappyEnv(t)

	if _, err := svc.Create(context.Background(), service.CreateTestTaskPayload{
		TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张敏",
		DimensionIDs: []string{"102", "101"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var codes []string
	if err := json.Unmarshal([]byte(taskRepo.createdTask.DimensionCodesJSON), &codes); err != nil {
		t.Fatalf("dimension_codes_json 非法: %v", err)
	}
	if len(codes) != 2 || codes[0] != "MGT_PLAN" || codes[1] != "MGT_DELEGATE" {
		t.Errorf("codes = %v, want 按维度表序 [MGT_PLAN MGT_DELEGATE]", codes)
	}
}
