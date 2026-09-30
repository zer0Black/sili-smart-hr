// answer 域三接口业务层：令牌校验链 + 规则化剧本推进
// （specs P2_TST_002 §5.1/§5.2/§5.3，03 A1/A2/A3）。施测为规则化状态机无 LLM 调用
// （specs §4.1.4 规则1），服务端产出推进事实，剧本文案归前端 i18n（03 §1.4）。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// answerAction 枚举（03 A2）。
const (
	AnswerActionNext     = "next"
	AnswerActionFinished = "finished"
)

// answerContentMaxRunes ai_mgmt 回复长度上限，rune 计数（specs §4.1.2 B / §5.2.2 步骤2）。
const answerContentMaxRunes = 500

// AnswerQuestionItem A1 questions[]/A2 next_question 条目（03 A1 响应字段）。
type AnswerQuestionItem struct {
	Seq           int    `json:"seq"`
	DimensionName string `json:"dimension_name"`
	Scenario      string `json:"scenario"`
	Requirement   string `json:"requirement"`
}

// AnswerReplyItem A1 replies[] 条目。
type AnswerReplyItem struct {
	Seq     int    `json:"seq"`
	Content string `json:"content"`
}

// AnswerContextResult A1 响应 data。
type AnswerContextResult struct {
	TaskNo        string               `json:"task_no"`
	TestType      string               `json:"test_type"`
	QuestionTotal int                  `json:"question_total"`
	AnsweredCount int                  `json:"answered_count"`
	Finished      bool                 `json:"finished"`
	Questions     []AnswerQuestionItem `json:"questions"`
	Replies       []AnswerReplyItem    `json:"replies"`
}

// AnswerReplyResult A2 响应 data。
type AnswerReplyResult struct {
	QuestionSeq   int                 `json:"question_seq"`
	AnsweredCount int                 `json:"answered_count"`
	QuestionTotal int                 `json:"question_total"`
	Action        string              `json:"action"` // next / finished
	NextQuestion  *AnswerQuestionItem `json:"next_question"`
}

// AnswerSubmitResult A3 响应 data。
type AnswerSubmitResult struct {
	TaskNo string `json:"task_no"`
}

// AnswerService answer 域业务接口（03 §3 三接口）。
type AnswerService interface {
	Context(ctx context.Context, token string) (*AnswerContextResult, error)
	Reply(ctx context.Context, token, content string) (*AnswerReplyResult, error)
	Submit(ctx context.Context, token string) (*AnswerSubmitResult, error)
}

type answerService struct {
	answerRepo   repository.AssessmentTestAnswerRepository
	taskRepo     repository.AssessmentTestTaskRepository
	taskSvc      AssessmentTestTaskService
	questionRepo repository.QuestionRepository
	dimRepo      repository.DimensionRepository
	now          func() time.Time
}

// NewAnswerService 构造 answer 域 service。taskRepo 只读消费 GetByID 点查任务行，
// taskSvc 注入 F7 StartSession/CompleteTask 两契约（03 §4.2）。
func NewAnswerService(
	answerRepo repository.AssessmentTestAnswerRepository,
	taskRepo repository.AssessmentTestTaskRepository,
	taskSvc AssessmentTestTaskService,
	questionRepo repository.QuestionRepository,
	dimRepo repository.DimensionRepository,
	now func() time.Time,
) AnswerService {
	return &answerService{
		answerRepo:   answerRepo,
		taskRepo:     taskRepo,
		taskSvc:      taskSvc,
		questionRepo: questionRepo,
		dimRepo:      dimRepo,
		now:          now,
	}
}

// validateToken 令牌校验链（03 §4.1，三接口共享）：哈希点查 → 链接态 → 到期判定
// → 任务态。失败一律 1901 同文案（specs §4.2.4 规则1 防枚举）；Submit 传
// allowCompletedIdempotent 放行「used + 任务 completed」幂等特例（03 §1.5）。
func (s *answerService) validateToken(ctx context.Context, token string, allowCompletedIdempotent bool) (*domain.AssessmentTestTask, error) {
	if token == "" {
		return nil, NewError(errcode.AnswerTokenInvalid)
	}
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	link, err := s.answerRepo.FindLinkByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("find link by token hash: %w", err)
	}
	if link == nil || link.Status == domain.LinkStatusInvalid {
		slog.Warn("answer token rejected", "token_hash_prefix", tokenHash[:8], "link_status", linkStatusOrEmpty(link))
		return nil, NewError(errcode.AnswerTokenInvalid)
	}
	// 到期即时判定收口 tick 空窗：每分钟 ExpirePending 只扫 pending 任务，
	// 先于此被作答救活的过期链接若不在此拦下，任务转 in_progress 后永不过期
	//（ExpirePending 同口径 expires_at < now，specs §5.1.4）。
	if link.ExpiresAt.Before(s.now()) {
		slog.Warn("answer token expired", "token_hash_prefix", tokenHash[:8])
		return nil, NewError(errcode.AnswerTokenInvalid)
	}
	task, err := s.taskRepo.GetByID(ctx, link.TaskID)
	if err != nil {
		slog.Error("answer validate get task failed", "task_no", "", "err", err)
		return nil, fmt.Errorf("get test task: %w", err)
	}
	if task == nil { // 链接孤儿，理论不可达
		slog.Warn("answer token rejected", "token_hash_prefix", tokenHash[:8], "link_status", link.Status)
		return nil, NewError(errcode.AnswerTokenInvalid)
	}
	switch task.Status {
	case domain.TestTaskStatusPending, domain.TestTaskStatusInProgress:
		return task, nil
	case domain.TestTaskStatusCompleted:
		if allowCompletedIdempotent && link.Status == domain.LinkStatusUsed {
			return task, nil // A3 幂等重复提交放行（03 §1.5）
		}
	}
	slog.Warn("answer token rejected", "token_hash_prefix", tokenHash[:8], "link_status", link.Status, "task_no", task.TaskNo)
	return nil, NewError(errcode.AnswerTokenInvalid)
}

// linkStatusOrEmpty link 为 nil（不存在）时空串占位。
func linkStatusOrEmpty(link *domain.AssessmentTestLink) string {
	if link == nil {
		return ""
	}
	return link.Status
}

// Context A1（specs §5.1.2）：校验链 → StartSession（失败阻断作答）→ 快照现读组装。
func (s *answerService) Context(ctx context.Context, token string) (*AnswerContextResult, error) {
	task, err := s.validateToken(ctx, token, false)
	if err != nil {
		return nil, err
	}
	if err := s.taskSvc.StartSession(ctx, task.ID); err != nil {
		slog.Error("answer start session failed", "task_no", task.TaskNo, "err", err)
		return nil, fmt.Errorf("start session: %w", err)
	}
	questionIDs, err := parseAnswerQuestionIDs(task.QuestionIDsJSON)
	if err != nil {
		slog.Error("answer parse question ids failed", "task_no", task.TaskNo, "err", err)
		return nil, err
	}
	questions, err := s.loadQuestionItems(ctx, task, questionIDs)
	if err != nil {
		slog.Error("answer load questions failed", "task_no", task.TaskNo, "err", err)
		return nil, err
	}
	rows, err := s.answerRepo.ListByTask(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("list answers: %w", err)
	}
	// ListByTask 契约自带 question_seq 升序，rows 单次读取即计数与列表双来源。
	res := &AnswerContextResult{
		TaskNo:        task.TaskNo,
		TestType:      task.TestType,
		QuestionTotal: len(questionIDs),
		AnsweredCount: len(rows),
		Finished:      len(rows) >= len(questionIDs), // ≥ 口径（03 §1.7）
		Questions:     questions,
		Replies:       make([]AnswerReplyItem, 0, len(rows)),
	}
	for _, r := range rows {
		res.Replies = append(res.Replies, AnswerReplyItem{Seq: r.QuestionSeq, Content: r.Content})
	}
	return res, nil
}

// Reply A2（specs §5.2.2）：校验链 → 服务端推算题号 → 格式校验 → 组装下一题 →
// 落库 → 推进指令。组装先于落库：组装失败零落库，客户端重发仍落原题号，
// 保证「按已落库记录数推算当前题」的推进单调（specs §5.2.4 规则1）。
func (s *answerService) Reply(ctx context.Context, token, content string) (*AnswerReplyResult, error) {
	task, err := s.validateToken(ctx, token, false)
	if err != nil {
		return nil, err
	}
	questionIDs, err := parseAnswerQuestionIDs(task.QuestionIDsJSON)
	if err != nil {
		slog.Error("answer parse question ids failed", "task_no", task.TaskNo, "err", err)
		return nil, err
	}
	questionTotal := len(questionIDs)
	answered, err := s.answerRepo.CountByTask(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("count answers: %w", err)
	}
	seq := int(answered) + 1
	if seq > questionTotal { // 完成态兜底：不落库不推进（03 A2 1902）
		return nil, NewError(errcode.AnswerReplyInvalid)
	}
	trimmed := strings.TrimSpace(content)
	if !validAnswerContent(task.TestType, trimmed) {
		return nil, NewError(errcode.AnswerReplyInvalid)
	}
	res := &AnswerReplyResult{
		QuestionSeq:   seq,
		AnsweredCount: int(answered) + 1,
		QuestionTotal: questionTotal,
		Action:        AnswerActionFinished,
		NextQuestion:  nil,
	}
	if res.AnsweredCount < questionTotal {
		next, err := s.buildQuestionItem(ctx, task, questionIDs[res.AnsweredCount], res.AnsweredCount+1)
		if err != nil {
			slog.Error("answer load next question failed", "task_no", task.TaskNo, "err", err)
			return nil, err
		}
		res.NextQuestion = next
	}
	row := &domain.AssessmentTestAnswer{TaskID: task.ID, QuestionSeq: seq, Content: trimmed}
	if err := s.answerRepo.Insert(ctx, row); err != nil {
		slog.Error("answer insert failed", "task_no", task.TaskNo, "err", err)
		return nil, fmt.Errorf("insert answer: %w", err)
	}
	if res.AnsweredCount < questionTotal {
		res.Action = AnswerActionNext
	}
	return res, nil
}

// Submit A3（specs §5.3.2）：校验链（幂等特例）→ 完整性门槛 → F7 CompleteTask。
func (s *answerService) Submit(ctx context.Context, token string) (*AnswerSubmitResult, error) {
	task, err := s.validateToken(ctx, token, true)
	if err != nil {
		return nil, err
	}
	if task.Status == domain.TestTaskStatusCompleted { // 幂等短路：不重复进事务（03 §1.5）
		return &AnswerSubmitResult{TaskNo: task.TaskNo}, nil
	}
	questionIDs, err := parseAnswerQuestionIDs(task.QuestionIDsJSON)
	if err != nil {
		slog.Error("answer parse question ids failed", "task_no", task.TaskNo, "err", err)
		return nil, err
	}
	count, err := s.answerRepo.CountByTask(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("count answers: %w", err)
	}
	if int(count) < len(questionIDs) { // < 题数拒绝（specs §5.3.4 规则1，≥ 完成权威）
		return nil, NewError(errcode.AnswerIncomplete)
	}
	if err := s.taskSvc.CompleteTask(ctx, task.ID); err != nil {
		if serr, ok := err.(*Error); ok && serr.Code == errcode.TestTaskStatusInvalid {
			// F7 终态守卫 1802 收敛为令牌不可用（specs §5.3.4 规则2，03 §1.5）
			return nil, NewError(errcode.AnswerTokenInvalid)
		}
		slog.Error("answer complete task failed", "task_no", task.TaskNo, "err", err)
		return nil, fmt.Errorf("complete task: %w", err)
	}
	return &AnswerSubmitResult{TaskNo: task.TaskNo}, nil
}

// parseAnswerQuestionIDs 解析任务快照题目 ID 数组：repository.ParseQuestionIDs
// 单点实现，service 侧薄封装。
func parseAnswerQuestionIDs(s string) ([]int64, error) {
	return repository.ParseQuestionIDs(s)
}

// validAnswerContent 格式校验（specs §5.2.2 步骤2）：非空、ai_mgmt ≤500 rune、
// enneagram 1-5 整数（仅单字符数字，"3.5"/"三"/"05" 均非法）。
func validAnswerContent(testType, content string) bool {
	if content == "" {
		return false
	}
	switch testType {
	case domain.TestTypeAIMgmt:
		return utf8.RuneCountInString(content) <= answerContentMaxRunes
	case domain.TestTypeEnneagram:
		if len(content) != 1 { // "05"/" 3"/"３" 均非法：仅单字符数字
			return false
		}
		n, err := strconv.Atoi(content)
		return err == nil && n >= 1 && n <= 5
	default:
		return false
	}
}

// loadQuestionItems 题目全集组装（specs §5.1.3 快照现读口径）：ListByIDsUnscoped
// 现读全文，未命中 ID 跳过；ai_mgmt 维度名经快照 codes 取 {ID→Name} 映射，
// enneagram 恒空串。seq 按快照数组下标 1-based，与现读返回顺序解耦。
func (s *answerService) loadQuestionItems(ctx context.Context, task *domain.AssessmentTestTask, questionIDs []int64) ([]AnswerQuestionItem, error) {
	dimNames := map[int64]string{}
	if task.TestType == domain.TestTypeAIMgmt {
		names, err := s.dimensionNamesByCodes(ctx, task.DimensionCodesJSON)
		if err != nil {
			return nil, err
		}
		dimNames = names
	}
	questions, err := s.questionRepo.ListByIDsUnscoped(ctx, questionIDs)
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	byID := make(map[int64]domain.Question, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	items := make([]AnswerQuestionItem, 0, len(questionIDs))
	for i, id := range questionIDs {
		q, ok := byID[id]
		if !ok {
			continue // 快照集合权威，缺行跳过（物理删除异常态）
		}
		items = append(items, AnswerQuestionItem{
			Seq:           i + 1,
			DimensionName: dimNames[q.DimensionID],
			Scenario:      q.Scenario,
			Requirement:   q.Requirement,
		})
	}
	return items, nil
}

// buildQuestionItem 组装单条题目条目：快照指定 ID 现读，未命中（理论不可达）报错。
func (s *answerService) buildQuestionItem(ctx context.Context, task *domain.AssessmentTestTask, questionID int64, seq int) (*AnswerQuestionItem, error) {
	items, err := s.loadQuestionItems(ctx, task, []int64{questionID})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("next question %d missing", questionID)
	}
	item := items[0]
	item.Seq = seq
	return &item, nil
}

// dimensionNamesByCodes 快照维度 codes 现读维度行，返回 {维度ID→名称}。
func (s *answerService) dimensionNamesByCodes(ctx context.Context, codesJSON string) (map[int64]string, error) {
	var codes []string
	if codesJSON != "" {
		if err := json.Unmarshal([]byte(codesJSON), &codes); err != nil {
			return nil, fmt.Errorf("parse dimension_codes_json: %w", err)
		}
	}
	if len(codes) == 0 {
		return map[int64]string{}, nil
	}
	dims, err := s.dimRepo.ListFullByCodesUnscoped(ctx, codes)
	if err != nil {
		return nil, fmt.Errorf("list dimensions: %w", err)
	}
	names := make(map[int64]string, len(dims))
	for _, d := range dims {
		names[d.ID] = d.Name
	}
	return names, nil
}
