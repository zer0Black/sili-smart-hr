// assessment_test_task 主动测试域业务层：任务列表（A1）、轮询计数（A2）、发起（B1）、
// 量表就绪（B2）、链接查询/重发/取消（C1/C2/C3）。口径细节见 specs P2_TST_001
// §4.1/§4.2/§5.1 与 03 §3。
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/dberr"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/questionbank/scaledata"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// linkTTL 链接有效期常量：自生成时刻起算 7 天，重发重新起算（specs §4.1.4 规则4，
// 在线配置不在本期范围）。
const linkTTL = 7 * 24 * time.Hour

// 任务号前缀（specs §8.1 术语表）：T=AI 管理能力，E=九型人格。
const (
	taskNoPrefixAIMgmt    = "T"
	taskNoPrefixEnneagram = "E"
)

// answerURLPrefix 作答链接前缀（specs §5.1.2 步骤3），令牌原文拼入路径。
const answerURLPrefix = "/answer/"

// tokenBytes 令牌随机字节数：base64url 编码后恒 43 字符（specs §5.1.4 规则1）。
const tokenBytes = 32

// taskNoRetryLimit 撞 uk_task_no 并发重号时重取序号的重试上限（04 §3.1 索引说明）。
const taskNoRetryLimit = 3

// ListTestTaskFilter 任务列表查询条件（03 A1）：test_type 必填二值，status/keyword 空跳过。
type ListTestTaskFilter struct {
	TestType string
	Status   string
	Keyword  string
	Page     int
	PageSize int
}

// TestTaskListDTO 列表行（03 A1 响应字段一一对应）。CreatedAt/CompletedAt 直出
// yyyy-MM-dd HH:mm 本地时区字符串，未完成 CompletedAt 为 nil 前端渲染 —。
type TestTaskListDTO struct {
	ID            int64   `json:"id,string"`
	TaskNo        string  `json:"task_no"`
	TestType      string  `json:"test_type"`
	StaffName     string  `json:"staff_name"`
	Status        string  `json:"status"`
	LinkStatus    string  `json:"link_status"`
	GradingStatus string  `json:"grading_status"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   *string `json:"completed_at"`
}

// TestTaskPollCountsDTO 轮询探针计数（03 A2）：两类任务未终态计数。
type TestTaskPollCountsDTO struct {
	AIMgmtActive    int64 `json:"ai_mgmt_active"`
	EnneagramActive int64 `json:"enneagram_active"`
}

// CreateTestTaskPayload B1 请求体（service 侧）。DimensionIDs 为维度雪花 ID 字符串数组，
// 仅 ai_mgmt 消费（03 §2.4 string 化约定）。
type CreateTestTaskPayload struct {
	TestType     string
	StaffID      string
	StaffName    string
	DimensionIDs []string
}

// CreateTestTaskResult B1 响应。AnswerURL 含令牌原文，供前端留存上下文（03 B1）。
type CreateTestTaskResult struct {
	ID        int64  `json:"id,string"`
	TaskNo    string `json:"task_no"`
	TestType  string `json:"test_type"`
	StaffName string `json:"staff_name"`
	Status    string `json:"status"`
	AnswerURL string `json:"answer_url"`
	CreatedAt string `json:"created_at"`
}

// TestScaleStatusDTO 九型量表就绪查询（03 B2）：未就绪 Ready=false 空串零值。
type TestScaleStatusDTO struct {
	Ready               bool   `json:"ready"`
	ScaleKey            string `json:"scale_key"`
	ScaleName           string `json:"scale_name"`
	ActiveQuestionCount int    `json:"active_question_count"`
}

// TestTaskLinkDTO 链接弹窗数据（03 C1/C2 共用，重发后 link_status 恒 valid）。
type TestTaskLinkDTO struct {
	TaskID      int64  `json:"task_id,string"`
	TaskNo      string `json:"task_no"`
	TestType    string `json:"test_type"`
	StaffName   string `json:"staff_name"`
	AnswerURL   string `json:"answer_url"`
	LinkStatus  string `json:"link_status"`
	GeneratedAt string `json:"generated_at"`
	ExpiresAt   string `json:"expires_at"`
}

// TestTaskCancelDTO 取消响应（03 C3）。
type TestTaskCancelDTO struct {
	TaskID int64  `json:"task_id,string"`
	Status string `json:"status"`
}

// TestGradeEnqueuer 阅卷任务投递窄接口（03 §4.6 契约表）：service 不 import asynq，
// tx 形参经本层闭包抹平为 repo 层 enqueue，装配层用 Asynq client 适配（T4）。
type TestGradeEnqueuer interface {
	EnqueueTestGrade(ctx context.Context, tx *gorm.DB, taskID int64) error
}

// AssessmentTestTaskService 是主动测试域业务接口。CompleteTask/StartSession 为
// F8 提交接口与会话上报的事务内动作，无 HTTP 面（03 §1.3/§1.5）。
type AssessmentTestTaskService interface {
	List(ctx context.Context, f ListTestTaskFilter) ([]TestTaskListDTO, int64, error)
	PollCounts(ctx context.Context) (*TestTaskPollCountsDTO, error)
	Create(ctx context.Context, p CreateTestTaskPayload) (*CreateTestTaskResult, error)
	ScaleStatus(ctx context.Context) (*TestScaleStatusDTO, error)
	Link(ctx context.Context, taskID int64) (*TestTaskLinkDTO, error)
	Resend(ctx context.Context, taskID int64) (*TestTaskLinkDTO, error)
	Cancel(ctx context.Context, taskID int64) (*TestTaskCancelDTO, error)
	CompleteTask(ctx context.Context, taskID int64) error
	StartSession(ctx context.Context, taskID int64) error
}

type assessmentTestTaskService struct {
	taskRepo      repository.AssessmentTestTaskRepository
	questionRepo  repository.QuestionRepository
	batchRepo     repository.QuestionBatchRepository
	dimRepo       repository.DimensionRepository
	staffs        userapiClient
	secretRepo    repository.IntegrationSecretRepository
	encKey        []byte
	gradeEnqueuer TestGradeEnqueuer
	now           func() time.Time
}

// NewAssessmentTestTaskService 构造主动测试 service。now 注入便于测试锚定
// 令牌有效期与时间直出口径。
func NewAssessmentTestTaskService(
	taskRepo repository.AssessmentTestTaskRepository,
	questionRepo repository.QuestionRepository,
	batchRepo repository.QuestionBatchRepository,
	dimRepo repository.DimensionRepository,
	staffs userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	gradeEnqueuer TestGradeEnqueuer,
	now func() time.Time,
) AssessmentTestTaskService {
	return &assessmentTestTaskService{
		taskRepo:      taskRepo,
		questionRepo:  questionRepo,
		batchRepo:     batchRepo,
		dimRepo:       dimRepo,
		staffs:        staffs,
		secretRepo:    secretRepo,
		encKey:        encKey,
		gradeEnqueuer: gradeEnqueuer,
		now:           now,
	}
}

// List 枚举校验（test_type 必填二值、status 可空五值，违者 1400）→ repo 分页 → DTO 组装。
func (s *assessmentTestTaskService) List(ctx context.Context, f ListTestTaskFilter) ([]TestTaskListDTO, int64, error) {
	if !validTestType(f.TestType) || !validTestTaskStatus(f.Status) {
		return nil, 0, NewError(errcode.BadRequest)
	}
	rows, total, err := s.taskRepo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: f.TestType,
		Status:   f.Status,
		Keyword:  f.Keyword,
		Page:     f.Page,
		PageSize: f.PageSize,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list test tasks: %w", err)
	}
	items := make([]TestTaskListDTO, 0, len(rows))
	for i := range rows {
		items = append(items, toTestTaskListDTO(&rows[i]))
	}
	return items, total, nil
}

// toTestTaskListDTO 组装列表行：无链接行时 LinkStatus 取零值空串由前端兜底。
func toTestTaskListDTO(row *repository.TestTaskRow) TestTaskListDTO {
	dto := TestTaskListDTO{
		ID:            row.Task.ID,
		TaskNo:        row.Task.TaskNo,
		TestType:      row.Task.TestType,
		StaffName:     row.Task.StaffName,
		Status:        row.Task.Status,
		LinkStatus:    row.LinkStatus,
		GradingStatus: row.Task.GradingStatus,
		CreatedAt:     row.Task.CreatedAt.Local().Format(layoutDateTime),
	}
	if row.Task.CompletedAt != nil {
		v := row.Task.CompletedAt.Local().Format(layoutDateTime)
		dto.CompletedAt = &v
	}
	return dto
}

// PollCounts 轮询探针两类未终态计数直通（03 A2，repo 层承载口径）。
func (s *assessmentTestTaskService) PollCounts(ctx context.Context) (*TestTaskPollCountsDTO, error) {
	aiMgmt, enneagram, err := s.taskRepo.CountActiveByType(ctx)
	if err != nil {
		return nil, fmt.Errorf("count active test tasks: %w", err)
	}
	return &TestTaskPollCountsDTO{AIMgmtActive: aiMgmt, EnneagramActive: enneagram}, nil
}

// Create 发起六步（specs §5.1.2）：①枚举校验 → ②对象校验（上游比对，1805）
// → ③组卷（1803/1804）→ ④快照序列化 → ⑤令牌生成 → ⑥取号+单事务落库。
// 同人多任务并存，无重复校验无告警（specs §4.1.4 规则3）。
func (s *assessmentTestTaskService) Create(ctx context.Context, p CreateTestTaskPayload) (*CreateTestTaskResult, error) {
	// ① 枚举校验：test_type 二值、staff 非空（空白视为缺失）、ai_mgmt 时
	// dimension_ids 非空（specs §4.2.2）。
	if !validTestType(p.TestType) {
		return nil, NewError(errcode.BadRequest)
	}
	if p.StaffID == "" || strings.TrimSpace(p.StaffName) == "" {
		return nil, NewError(errcode.BadRequest)
	}
	if p.TestType == domain.TestTypeAIMgmt && len(p.DimensionIDs) == 0 {
		return nil, NewError(errcode.BadRequest)
	}

	// ② 对象校验：解密密钥后经 userapi.ListStaffs 拉取比对 staff_id+staff_name，
	// 上游失败或未命中一律 1805 且任务不创建（specs §5.1.5 第一行）。
	if err := s.validateStaff(ctx, p.StaffID, p.StaffName); err != nil {
		slog.Error("test task create staff invalid",
			"staff_id", p.StaffID, "staff_name", p.StaffName, "err", err)
		return nil, err
	}

	// ③+④ 组卷与快照序列化。
	var (
		questionIDs    []int64
		dimensionCodes []string
		scaleKey       string
	)
	switch p.TestType {
	case domain.TestTypeAIMgmt:
		sel, err := s.selectAIMgmtDimensions(ctx, p.DimensionIDs)
		if err != nil {
			return nil, err
		}
		questions, qerr := s.questionRepo.ListActiveByDimensionIDs(ctx, sel.IDs)
		if qerr != nil {
			return nil, fmt.Errorf("list active questions: %w", qerr)
		}
		// 每子能力启用题全取，0 题报错携维度名（specs §4.2.4 规则1）。
		byDim := make(map[int64]int, len(sel.IDs))
		for i := range questions {
			byDim[questions[i].DimensionID]++
		}
		for _, id := range sel.IDs {
			if byDim[id] == 0 {
				slog.Warn("test task create dimension questions empty",
					"dimension", sel.NameByID[id])
				// message 为固定英文模板携维度名数据（03 B1 错误码表约定），
				// 前端按前后缀剥离取维度名后走 i18n 模板拼接。
				return nil, NewErrorWithMsg(errcode.TestDimensionQuestionsEmpty,
					fmt.Sprintf("dimension %s has no active questions", sel.NameByID[id]))
			}
		}
		for i := range questions {
			questionIDs = append(questionIDs, questions[i].ID)
		}
		dimensionCodes = sel.Codes
	default: // enneagram
		var questions []domain.Question
		var serr error
		scaleKey, questions, serr = s.resolveScale(ctx)
		if serr != nil {
			return nil, serr
		}
		if len(questions) == 0 {
			slog.Warn("test task create scale not ready")
			return nil, NewError(errcode.TestScaleNotReady)
		}
		for i := range questions {
			questionIDs = append(questionIDs, questions[i].ID)
		}
	}
	idsJSON, err := marshalQuestionIDs(questionIDs)
	if err != nil {
		return nil, err
	}
	codesJSON := "[]"
	if dimensionCodes != nil {
		if codesJSON, err = marshalStrings(dimensionCodes); err != nil {
			return nil, err
		}
	}

	// ⑤ 令牌生成：crypto/rand 32 字节 base64url=43 字符，hash=SHA-256 hex（specs §5.1.4 规则1）。
	// 落库时间统一 UTC 口径（04 §3.1：SQLite 文本字典序可比）。
	now := s.now().UTC()
	plain, hash, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	// ⑥ 取号 + 单事务落库（任务行+链接行+引用计数 +1，T3 CreateWithLink 收口）。
	// 撞 uk_task_no（同前缀同日期并发取号）时重取序号重试（04 §3.1 索引说明）。
	prefix := taskNoPrefixAIMgmt
	if p.TestType == domain.TestTypeEnneagram {
		prefix = taskNoPrefixEnneagram
	}
	var task *domain.AssessmentTestTask
	link := &domain.AssessmentTestLink{
		TokenPlain:  plain,
		TokenHash:   hash,
		Status:      domain.LinkStatusValid,
		GeneratedAt: now,
		ExpiresAt:   now.Add(linkTTL),
	}
	for range taskNoRetryLimit {
		taskNo, err := s.taskRepo.NextTaskNo(ctx, prefix, now)
		if err != nil {
			return nil, fmt.Errorf("next task no: %w", err)
		}
		cand := &domain.AssessmentTestTask{
			TaskNo:             taskNo,
			TestType:           p.TestType,
			StaffID:            p.StaffID,
			StaffName:          p.StaffName,
			Status:             domain.TestTaskStatusPending,
			GradingStatus:      domain.GradingStatusWaiting,
			QuestionIDsJSON:    idsJSON,
			ScaleKey:           scaleKey,
			DimensionCodesJSON: codesJSON,
		}
		if err := s.taskRepo.CreateWithLink(ctx, cand, link); err != nil {
			if dberr.UniqueViolation(err) {
				continue // 并发同号：重取下一序号重试
			}
			return nil, fmt.Errorf("create test task with link: %w", err)
		}
		task = cand
		break
	}
	if task == nil {
		return nil, fmt.Errorf("create test task: task no conflict after %d retries", taskNoRetryLimit)
	}
	// 文本详情形态埋点（specs §4.1.4 规则3）：对象/类型/任务号。
	injectDetail(ctx, domain.OpModuleAssessment,
		fmt.Sprintf("测试任务 %s", task.TaskNo),
		fmt.Sprintf("发起%s测试", testTypeName(p.TestType)),
		fmt.Sprintf("对象 %s，类型 %s，任务号 %s", p.StaffName, testTypeName(p.TestType), task.TaskNo))
	return &CreateTestTaskResult{
		ID:        task.ID,
		TaskNo:    task.TaskNo,
		TestType:  p.TestType,
		StaffName: p.StaffName,
		Status:    task.Status,
		AnswerURL: answerURLPrefix + plain,
		CreatedAt: now.Local().Format(layoutDateTime),
	}, nil
}

// dimSelection 维度圈定结果：按维度表 code ASC 序的 ID、编码序列与名称索引。
type dimSelection struct {
	IDs      []int64
	Codes    []string
	NameByID map[int64]string
}

// selectAIMgmtDimensions 圈定勾选维度：取 SourceTest 启用集过滤 ModuleCode=AI_MGMT，
// 与 dimension_ids 求交集校验（越界即 1400）。
func (s *assessmentTestTaskService) selectAIMgmtDimensions(ctx context.Context, dimensionIDs []string) (*dimSelection, error) {
	dims, err := s.dimRepo.ListEnabledFullByDataSource(ctx, domain.SourceTest)
	if err != nil {
		return nil, fmt.Errorf("list enabled dimensions: %w", err)
	}
	enabled := make(map[int64]domain.Dimension, len(dims))
	for i := range dims {
		if dims[i].ModuleCode == domain.ModuleAIMgmt {
			enabled[dims[i].ID] = dims[i]
		}
	}
	// 勾选集合去重并按维度表序（dims 本身 code ASC）输出，防重复 ID 重复取题。
	seen := make(map[int64]bool, len(dimensionIDs))
	for _, raw := range dimensionIDs {
		id, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			return nil, NewError(errcode.BadRequest)
		}
		if _, ok := enabled[id]; !ok {
			return nil, NewError(errcode.BadRequest)
		}
		seen[id] = true
	}
	if len(seen) == 0 {
		return nil, NewError(errcode.BadRequest)
	}
	sel := &dimSelection{
		IDs:      make([]int64, 0, len(seen)),
		Codes:    make([]string, 0, len(seen)),
		NameByID: make(map[int64]string, len(seen)),
	}
	for i := range dims { // dims 为 code ASC 序，快照编码与 ID 同序
		d := dims[i]
		if d.ModuleCode == domain.ModuleAIMgmt && seen[d.ID] {
			sel.IDs = append(sel.IDs, d.ID)
			sel.Codes = append(sel.Codes, d.Code)
			sel.NameByID[d.ID] = d.Name
		}
	}
	return sel, nil
}

// validateStaff 对象校验：经 userapi.FindStaff（keyword=staff_name 过滤后翻页
// 比对 staff_id+staff_name 双匹配，翻页口径单点在 userapi.WalkStaffPages），
// 未配置密钥、上游失败、未命中、翻页超上限统一 1805（specs §5.1.5）。
func (s *assessmentTestTaskService) validateStaff(ctx context.Context, staffID, staffName string) error {
	secret, err := ResolveIntegrationSecret(ctx, s.secretRepo, s.encKey)
	if err != nil {
		return NewError(errcode.TestStaffInvalid)
	}
	found, err := userapi.FindStaff(ctx, s.staffs, secret, staffID, staffName)
	if err != nil || !found {
		return NewError(errcode.TestStaffInvalid)
	}
	return nil
}

// resolveScale 组卷量表解析（01 §4.2.4 规则2）：自最新引入批次向下迭代，取首个
// 存在启用题的量表；全部停用或未引入返回空 scaleKey + 空 questions，调用方据此
// 映射 1804（两套并存取最新就绪的一套，与 03 B2 主句同源）。
func (s *assessmentTestTaskService) resolveScale(ctx context.Context) (string, []domain.Question, error) {
	batches, err := s.batchRepo.ListImportedByNewest(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("list imported batches: %w", err)
	}
	for _, b := range batches {
		questions, qerr := s.questionRepo.ListActiveByScaleKey(ctx, b.ScaleKey)
		if qerr != nil {
			return "", nil, fmt.Errorf("list active scale questions: %w", qerr)
		}
		if len(questions) > 0 {
			return b.ScaleKey, questions, nil
		}
	}
	return "", nil, nil
}

// ScaleStatus 量表就绪查询：resolveScale 同口径判定，未就绪 Ready=false 空串
// 零值，弹窗打开不阻断（03 B2）。
func (s *assessmentTestTaskService) ScaleStatus(ctx context.Context) (*TestScaleStatusDTO, error) {
	notReady := &TestScaleStatusDTO{}
	scaleKey, questions, err := s.resolveScale(ctx)
	if err != nil {
		return nil, err
	}
	if scaleKey == "" {
		return notReady, nil
	}
	name := ""
	if tpl, ok := scaledata.FindByKey(scaleKey); ok {
		name = tpl.Name
	}
	return &TestScaleStatusDTO{
		Ready:               true,
		ScaleKey:            scaleKey,
		ScaleName:           name,
		ActiveQuestionCount: len(questions),
	}, nil
}

// Link 作答链接查询：GetByID（查无 1801）+ CurrentLink 组装，无链接行时
// answer_url 空串、link_status=invalid（specs §4.3.4 规则1 降级）。
func (s *assessmentTestTaskService) Link(ctx context.Context, taskID int64) (*TestTaskLinkDTO, error) {
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	link, err := s.taskRepo.CurrentLink(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("current link: %w", err)
	}
	return toTestTaskLinkDTO(task, link), nil
}

// Resend 重发：读任务校验（查无 1801、status ∈ {pending, expired}，其余 1802），
// ReplaceLink 生成新令牌新有效期（expired 任务由 repo 事务内回 pending，specs §6.2）。
func (s *assessmentTestTaskService) Resend(ctx context.Context, taskID int64) (*TestTaskLinkDTO, error) {
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != domain.TestTaskStatusPending && task.Status != domain.TestTaskStatusExpired {
		return nil, NewError(errcode.TestTaskStatusInvalid)
	}
	now := s.now().UTC()
	plain, hash, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	newLink := &domain.AssessmentTestLink{
		TokenPlain:  plain,
		TokenHash:   hash,
		Status:      domain.LinkStatusValid,
		GeneratedAt: now,
		ExpiresAt:   now.Add(linkTTL),
	}
	if err := s.taskRepo.ReplaceLink(ctx, taskID, newLink); err != nil {
		// 锁内复核拒绝（并发推进到 completed/canceled 等）：映射 1802（specs §4.1.4）
		if errors.Is(err, repository.ErrTaskNotSubmittable) {
			return nil, NewError(errcode.TestTaskStatusInvalid)
		}
		return nil, fmt.Errorf("replace link: %w", err)
	}
	dto := toTestTaskLinkDTO(task, newLink)
	dto.LinkStatus = domain.LinkStatusValid // 重发响应恒 valid（03 C2）
	injectDetail(ctx, domain.OpModuleAssessment,
		fmt.Sprintf("测试任务 %s", task.TaskNo),
		"重发测试任务",
		fmt.Sprintf("任务号 %s，新链接生效", task.TaskNo))
	return dto, nil
}

// Cancel 取消任务：repo CancelTask 条件更新，affected=0 按当前状态区分
// （查无 1801、completed/canceled 1802，specs §4.1.4 规则2）。
func (s *assessmentTestTaskService) Cancel(ctx context.Context, taskID int64) (*TestTaskCancelDTO, error) {
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	affected, err := s.taskRepo.CancelTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("cancel test task: %w", err)
	}
	if affected == 0 {
		return nil, NewError(errcode.TestTaskStatusInvalid)
	}
	injectDetail(ctx, domain.OpModuleAssessment,
		fmt.Sprintf("测试任务 %s", task.TaskNo),
		"取消测试任务",
		fmt.Sprintf("任务号 %s，状态推进 cancelled", task.TaskNo))
	return &TestTaskCancelDTO{TaskID: task.ID, Status: domain.TestTaskStatusCanceled}, nil
}

// CompleteTask 员工作答提交的事务内推进（specs §5.2.2 步骤1、03 §4.6）：组装
// enqueue 闭包抹平 tx 形参后交 repo 单事务收口；repo 哨兵 ErrTaskNotSubmittable
// 映射 1802，completed 幂等路径 repo 已返回 nil。
func (s *assessmentTestTaskService) CompleteTask(ctx context.Context, taskID int64) error {
	enqueue := func(tx *gorm.DB) error {
		return s.gradeEnqueuer.EnqueueTestGrade(ctx, tx, taskID)
	}
	if err := s.taskRepo.CompleteTask(ctx, taskID, enqueue, s.now().UTC()); err != nil {
		if errors.Is(err, repository.ErrTaskNotSubmittable) {
			return NewError(errcode.TestTaskStatusInvalid)
		}
		return fmt.Errorf("complete test task: %w", err)
	}
	return nil
}

// StartSession F8 会话上报：pending→in_progress 条件更新直通（specs §6.2），
// 任务不存在 1801；affected=0 时复查任务状态：in_progress 为幂等成功，
// 其余（expired/canceled/completed）为校验后竞态，映射 1802 交调用方收敛。
func (s *assessmentTestTaskService) StartSession(ctx context.Context, taskID int64) error {
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		return err
	}
	affected, err := s.taskRepo.MarkSessionStarted(ctx, taskID)
	if err != nil {
		return fmt.Errorf("mark session started: %w", err)
	}
	if affected == 0 && task.Status != domain.TestTaskStatusInProgress {
		return NewError(errcode.TestTaskStatusInvalid)
	}
	return nil
}

// loadTask 点查任务，nil 行映射 1801。
func (s *assessmentTestTaskService) loadTask(ctx context.Context, taskID int64) (*domain.AssessmentTestTask, error) {
	task, err := s.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get test task: %w", err)
	}
	if task == nil {
		return nil, NewError(errcode.TestTaskNotFound)
	}
	return task, nil
}

// toTestTaskLinkDTO 组装链接弹窗 DTO：link 为 nil（无链接行）时降级
// answer_url 空串、link_status=invalid，时间字段空串。
func toTestTaskLinkDTO(task *domain.AssessmentTestTask, link *domain.AssessmentTestLink) *TestTaskLinkDTO {
	dto := &TestTaskLinkDTO{
		TaskID:     task.ID,
		TaskNo:     task.TaskNo,
		TestType:   task.TestType,
		StaffName:  task.StaffName,
		LinkStatus: domain.LinkStatusInvalid,
	}
	if link == nil {
		return dto
	}
	dto.AnswerURL = answerURLPrefix + link.TokenPlain
	dto.LinkStatus = link.Status
	dto.GeneratedAt = link.GeneratedAt.Local().Format(layoutDateTime)
	dto.ExpiresAt = link.ExpiresAt.Local().Format(layoutDateTime)
	return dto
}

// generateToken 生成一次性令牌：crypto/rand 32 字节 base64url（43 字符）+ SHA-256 hex。
func generateToken() (plain, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(sum[:]), nil
}

// marshalQuestionIDs 快照题目 ID 数组序列化。
func marshalQuestionIDs(ids []int64) (string, error) {
	b, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("marshal question ids: %w", err)
	}
	return string(b), nil
}

// marshalStrings 字符串数组序列化。
func marshalStrings(v []string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal strings: %w", err)
	}
	return string(b), nil
}

// validTestType test_type 枚举校验（空串非法：03 A1 test_type 必填）。
func validTestType(v string) bool {
	return v == domain.TestTypeAIMgmt || v == domain.TestTypeEnneagram
}

// testTypeName 测试类型中文名（埋点文本用，措辞对齐 i18n assessment.type*）。
func testTypeName(t string) string {
	if t == domain.TestTypeEnneagram {
		return "九型人格"
	}
	return "AI 管理能力"
}

// validTestTaskStatus status 枚举校验，空串视为全部放行（03 A1）。
func validTestTaskStatus(v string) bool {
	switch v {
	case "", domain.TestTaskStatusPending, domain.TestTaskStatusInProgress,
		domain.TestTaskStatusCompleted, domain.TestTaskStatusExpired, domain.TestTaskStatusCanceled:
		return true
	}
	return false
}
