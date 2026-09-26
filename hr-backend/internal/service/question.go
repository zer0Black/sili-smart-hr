// question 题库域业务层：题目列表组装（维度名回填/摘要截断/作答方式派生）、
// 详情、编辑校验、启停与删除的业务规则（specs P2_QBN_001 §4.1.2/§4.1.3/§4.1.4）。
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// 文本字段长度边界（specs §4.1.2 E）。
const (
	questionScenarioMax    = 1000
	questionRequirementMax = 2000
	questionFocusMax       = 500
	questionSummaryRunes   = 50
)

// 作答方式派生值（03 §3.1，由 source 派生不落库）。
const (
	answerModeChat    = "CHAT"
	answerModeLikert5 = "LIKERT5"
)

// QuestionListItem 列表行 DTO（03 §3.1 响应字段）。
type QuestionListItem struct {
	ID            string    `json:"id"`
	QuestionNo    string    `json:"question_no"`
	Source        string    `json:"source"`
	DimensionID   string    `json:"dimension_id"`
	DimensionName string    `json:"dimension_name"`
	AnswerMode    string    `json:"answer_mode"`
	Status        string    `json:"status"`
	Summary       string    `json:"summary"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// QuestionDetail 详情 DTO（03 §3.2 响应字段全集）。batch_no 经 batchRepo 回填，
// 批次已被物理清理等查不到时降级空串（batch_id 有值透传不受影响）。
type QuestionDetail struct {
	ID             string    `json:"id"`
	QuestionNo     string    `json:"question_no"`
	Source         string    `json:"source"`
	DimensionID    string    `json:"dimension_id"`
	DimensionName  string    `json:"dimension_name"`
	AnswerMode     string    `json:"answer_mode"`
	Status         string    `json:"status"`
	Scenario       string    `json:"scenario"`
	Requirement    string    `json:"requirement"`
	FocusPoint     string    `json:"focus_point"`
	RejectReason   string    `json:"reject_reason"`
	BatchID        string    `json:"batch_id"`
	BatchNo        string    `json:"batch_no"`
	ReferenceCount int       `json:"reference_count"`
	Version        int       `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// QuestionListInput 列表查询入参。DimensionIDSet 区分「未传维度」与「传空」。
type QuestionListInput struct {
	Source         string
	DimensionID    int64
	DimensionIDSet bool
	Status         string
	Keyword        string
	Page           int
	PageSize       int
}

// QuestionListResult 分页结果，作 data 透传（{list,total,page,page_size} 同构）。
type QuestionListResult struct {
	List     []QuestionListItem `json:"list"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

// UpdateQuestionInput 编辑入参（03 §3.3 请求体）。
type UpdateQuestionInput struct {
	ID          int64
	DimensionID int64
	Scenario    string
	Requirement string
	FocusPoint  string
	Version     int
}

// QuestionMutationResult 编辑/启停共用响应（03 §3.3/§3.4）。Status 仅 Toggle 返回，
// omitempty 让 Update 响应无此字段；Delete 无结果。
type QuestionMutationResult struct {
	ID        string    `json:"id"`
	Status    string    `json:"status,omitempty"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// QuestionService 题库域业务接口。
type QuestionService interface {
	ListQuestions(ctx context.Context, in QuestionListInput) (*QuestionListResult, error)
	GetQuestion(ctx context.Context, id int64) (*QuestionDetail, error)
	UpdateQuestion(ctx context.Context, in UpdateQuestionInput) (*QuestionMutationResult, error)
	ToggleQuestionStatus(ctx context.Context, id int64, targetStatus string, version int) (*QuestionMutationResult, error)
	DeleteQuestion(ctx context.Context, id int64, version int) error
}

type questionService struct {
	repo      repository.QuestionRepository
	dimRepo   repository.DimensionRepository
	batchRepo repository.QuestionBatchRepository
}

// NewQuestionService 构造题库域 service。batchRepo 供详情回填 batch_no（03 §3.2）。
func NewQuestionService(repo repository.QuestionRepository, dimRepo repository.DimensionRepository, batchRepo repository.QuestionBatchRepository) QuestionService {
	return &questionService{repo: repo, dimRepo: dimRepo, batchRepo: batchRepo}
}

// answerModeOf 作答方式按 source 派生（03 §3.1）。
func answerModeOf(source string) string {
	if source == domain.QuestionSourceScale {
		return answerModeLikert5
	}
	return answerModeChat
}

// summarize 摘要按 rune 截前 50 字符，AI 与 SCALE 同规则（03 §3.1）。
func summarize(scenario string) string {
	if utf8.RuneCountInString(scenario) <= questionSummaryRunes {
		return scenario
	}
	return string([]rune(scenario)[:questionSummaryRunes])
}

// int64ToString 雪花 ID 序列化辅助，domain/DTO 双层 string 化约定（规则文件 §1.2）。
func int64ToString(v int64) string { return strconv.FormatInt(v, 10) }

// resolveQuestion 存在性查询收敛：NotFound 映射 1701，DB 错误 wrap 上报。
func (s *questionService) resolveQuestion(ctx context.Context, id int64) (*domain.Question, error) {
	q, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.QuestionNotFound)
		}
		return nil, fmt.Errorf("find question by id: %w", err)
	}
	return q, nil
}

// listDimensionNames 双查取维度 ID→名称映射：ListAll 活跃行（含停用）优先，活跃
// map 未命中的 ID 再 Unscoped 查软删行名称回填（specs §4.1.2 E 维度已停用/软删时
// 回传存量名称）。仅补充真实需要的缺口，避免全量 Unscoped 拉长文本列。
func (s *questionService) listDimensionNames(ctx context.Context, ids ...int64) (map[int64]string, error) {
	dims, err := s.dimRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dimensions: %w", err)
	}
	names := make(map[int64]string, len(dims))
	for i := range dims {
		names[dims[i].ID] = dims[i].Name
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := names[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return names, nil
	}
	softDeleted, err := s.dimRepo.ListNamesByIDsUnscoped(ctx, missing)
	if err != nil {
		return nil, fmt.Errorf("list soft-deleted dimension names: %w", err)
	}
	for id, name := range softDeleted {
		names[id] = name
	}
	return names, nil
}

// resolveDimensionName 单 ID 维度名解析，与 listDimensionNames 同口径（详情复用）。
func (s *questionService) resolveDimensionName(ctx context.Context, dimensionID int64) (string, error) {
	names, err := s.listDimensionNames(ctx, dimensionID)
	if err != nil {
		return "", err
	}
	return names[dimensionID], nil
}

// ListQuestions 列表组装：repo 分页取行 → 维度名双查回填 → 派生字段（03 §3.1）。
func (s *questionService) ListQuestions(ctx context.Context, in QuestionListInput) (*QuestionListResult, error) {
	rows, total, err := s.repo.ListPage(ctx, in.Source, in.DimensionID, in.DimensionIDSet, in.Status, in.Keyword, in.Page, in.PageSize)
	if err != nil {
		return nil, fmt.Errorf("list questions: %w", err)
	}
	dimIDs := make([]int64, len(rows))
	for i := range rows {
		dimIDs[i] = rows[i].DimensionID
	}
	names, err := s.listDimensionNames(ctx, dimIDs...)
	if err != nil {
		return nil, err
	}
	items := make([]QuestionListItem, 0, len(rows))
	for i := range rows {
		q := &rows[i]
		items = append(items, QuestionListItem{
			ID:            int64ToString(q.ID),
			QuestionNo:    q.QuestionNo,
			Source:        q.Source,
			DimensionID:   int64ToString(q.DimensionID),
			DimensionName: names[q.DimensionID],
			AnswerMode:    answerModeOf(q.Source),
			Status:        q.Status,
			Summary:       summarize(q.Scenario),
			UpdatedAt:     q.UpdatedAt,
		})
	}
	page, pageSize := in.Page, in.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return &QuestionListResult{List: items, Total: total, Page: page, PageSize: pageSize}, nil
}

// GetQuestion 详情组装（03 §3.2），PENDING 态可查（批次审核视图复用）。
func (s *questionService) GetQuestion(ctx context.Context, id int64) (*QuestionDetail, error) {
	q, err := s.resolveQuestion(ctx, id)
	if err != nil {
		return nil, err
	}
	name, err := s.resolveDimensionName(ctx, q.DimensionID)
	if err != nil {
		return nil, err
	}
	batchNo := ""
	if q.BatchID != 0 {
		batch, err := s.batchRepo.FindByID(ctx, q.BatchID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("find question batch by id: %w", err)
			}
		} else {
			batchNo = batch.BatchNo
		}
	}
	return &QuestionDetail{
		ID:             int64ToString(q.ID),
		QuestionNo:     q.QuestionNo,
		Source:         q.Source,
		DimensionID:    int64ToString(q.DimensionID),
		DimensionName:  name,
		AnswerMode:     answerModeOf(q.Source),
		Status:         q.Status,
		Scenario:       q.Scenario,
		Requirement:    q.Requirement,
		FocusPoint:     q.FocusPoint,
		RejectReason:   q.RejectReason,
		BatchID:        int64ToString(q.BatchID),
		BatchNo:        batchNo,
		ReferenceCount: q.ReferenceCount,
		Version:        q.Version,
		CreatedAt:      q.CreatedAt,
		UpdatedAt:      q.UpdatedAt,
	}, nil
}

// validateQuestionText 编辑文本长度校验（specs §4.1.2 E），越界 1400。
func validateQuestionText(scenario, requirement, focusPoint string) error {
	n := utf8.RuneCountInString(scenario)
	if n < 1 || n > questionScenarioMax {
		return NewError(errcode.BadRequest)
	}
	n = utf8.RuneCountInString(requirement)
	if n < 1 || n > questionRequirementMax {
		return NewError(errcode.BadRequest)
	}
	n = utf8.RuneCountInString(focusPoint)
	if n < 1 || n > questionFocusMax {
		return NewError(errcode.BadRequest)
	}
	return nil
}

// enabledAIMgmtDimensionIDs 当前启用的 AI_MGMT 维度集合（specs §4.1.2 E 改选限
// 当前启用集合）：ListAll 后过滤 module_code=AI_MGMT 且 enabled。
func (s *questionService) enabledAIMgmtDimensionIDs(ctx context.Context) (map[int64]bool, error) {
	dims, err := s.dimRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dimensions: %w", err)
	}
	ids := make(map[int64]bool, len(dims))
	for i := range dims {
		if dims[i].ModuleCode == domain.ModuleAIMgmt && dims[i].Enabled {
			ids[dims[i].ID] = true
		}
	}
	return ids, nil
}

// conflictOrMissing 乐观锁 RowsAffected=0 的二义收敛（specs 规则 9）：题仍在返
// 1713，已不存在（并发删除/软删）返 1701。
func (s *questionService) conflictOrMissing(ctx context.Context, id int64) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.QuestionNotFound)
		}
		return fmt.Errorf("reload question: %w", err)
	}
	return NewError(errcode.QuestionVersionConflict)
}

// mutationResult 写路径成功后的响应组装（回读最新行携带新 version 与 updated_at）。
func (s *questionService) mutationResult(ctx context.Context, id int64, status string) (*QuestionMutationResult, error) {
	q, err := s.repo.FindByID(ctx, id)
	if err != nil {
		// 刚写成功即被并发删除的极端情况，按版本冲突提示刷新；DB 错误上报 1500。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.QuestionVersionConflict)
		}
		return nil, fmt.Errorf("reload question: %w", err)
	}
	return &QuestionMutationResult{
		ID:        int64ToString(q.ID),
		Status:    status,
		Version:   q.Version,
		UpdatedAt: q.UpdatedAt,
	}, nil
}

// UpdateQuestion 编辑（03 §3.3）：仅 AI 题 ACTIVE/DISABLED 可编辑，维度限启用
// AI_MGMT 集合，文本长度校验后乐观锁更新。question_no/source/status/batch_id/
// reference_count 等不可变字段不进 updates。
func (s *questionService) UpdateQuestion(ctx context.Context, in UpdateQuestionInput) (*QuestionMutationResult, error) {
	cur, err := s.resolveQuestion(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	// specs §4.1.3 编辑题目：仅启用/已停用 AI 题可编辑，量表题与驳回/待审核题走 1707。
	if cur.Source != domain.QuestionSourceAI ||
		(cur.Status != domain.QuestionStatusActive && cur.Status != domain.QuestionStatusDisabled) {
		return nil, NewError(errcode.QuestionNotEditable)
	}
	if err := validateQuestionText(in.Scenario, in.Requirement, in.FocusPoint); err != nil {
		return nil, err
	}
	enabled, err := s.enabledAIMgmtDimensionIDs(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled[in.DimensionID] {
		return nil, NewError(errcode.BadRequest)
	}

	rows, err := s.repo.UpdateWithVersion(ctx, in.ID, in.Version, map[string]any{
		"dimension_id": in.DimensionID,
		"scenario":     in.Scenario,
		"requirement":  in.Requirement,
		"focus_point":  in.FocusPoint,
	})
	if err != nil {
		return nil, fmt.Errorf("update question: %w", err)
	}
	if rows == 0 {
		return nil, s.conflictOrMissing(ctx, in.ID)
	}
	// 编辑响应无 status 字段（03 §3.3），传空串触发 omitempty。
	return s.mutationResult(ctx, in.ID, "")
}

// ToggleQuestionStatus 启停（03 §3.4）：仅 ACTIVE/DISABLED 互切，target 与当前
// 相同或非法值返 1400，前置状态非法返 1708。
func (s *questionService) ToggleQuestionStatus(ctx context.Context, id int64, targetStatus string, version int) (*QuestionMutationResult, error) {
	if targetStatus != domain.QuestionStatusActive && targetStatus != domain.QuestionStatusDisabled {
		return nil, NewError(errcode.BadRequest)
	}
	cur, err := s.resolveQuestion(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != domain.QuestionStatusActive && cur.Status != domain.QuestionStatusDisabled {
		return nil, NewError(errcode.QuestionStatusInvalid)
	}
	if targetStatus == cur.Status {
		return nil, NewError(errcode.BadRequest)
	}
	rows, err := s.repo.UpdateWithVersion(ctx, id, version, map[string]any{
		"status": targetStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("toggle question status: %w", err)
	}
	if rows == 0 {
		return nil, s.conflictOrMissing(ctx, id)
	}
	return s.mutationResult(ctx, id, targetStatus)
}

// DeleteQuestion 删除（03 §3.6）：PENDING 在批次中管理返 1708，被引用题返
// 1705（specs 规则 4），量表题删除无额外限制，乐观锁软删。
func (s *questionService) DeleteQuestion(ctx context.Context, id int64, version int) error {
	cur, err := s.resolveQuestion(ctx, id)
	if err != nil {
		return err
	}
	if cur.Status == domain.QuestionStatusPending {
		return NewError(errcode.QuestionStatusInvalid)
	}
	if cur.ReferenceCount > 0 {
		return NewError(errcode.QuestionReferenced)
	}
	rows, err := s.repo.SoftDeleteWithVersion(ctx, id, version)
	if err != nil {
		return fmt.Errorf("soft delete question: %w", err)
	}
	if rows == 0 {
		return s.conflictOrMissing(ctx, id)
	}
	return nil
}
