// question_batch 题库批次域业务层：待审核卡区、批次明细组装、确认入库与作废的
// 校验编排、驳回题重新提交归批（specs P2_QBN_001 §4.1.3/§4.1.4/§4.2，03 §3.5/§3.7-§3.10）。
// 行级题目维护仍归 question.go，两者共用其私有校验与名称回填。
package service

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// 驳回原因长度边界（specs §4.1.4 规则 3，通用规范 3 上限 500）。
const rejectReasonMax = 500

// BatchCardDTO 批次卡（03 §3.7 字段）。
type BatchCardDTO struct {
	ID            string    `json:"id"`
	BatchNo       string    `json:"batch_no"`
	Title         string    `json:"title"`
	Source        string    `json:"source"`
	BatchType     string    `json:"batch_type"`
	QuestionCount int       `json:"question_count"`
	CreatedAt     time.Time `json:"created_at"`
}

// ReviewQuestionDTO 审核视图逐题全文（03 §3.8）。reject_reason 恒空串预留：
// 标记在前端进行（specs §4.2.4 规则 1），仅对齐确认入库结构。
type ReviewQuestionDTO struct {
	ID            string `json:"id"`
	QuestionNo    string `json:"question_no"`
	DimensionID   string `json:"dimension_id"`
	DimensionName string `json:"dimension_name"`
	AnswerMode    string `json:"answer_mode"`
	Scenario      string `json:"scenario"`
	Requirement   string `json:"requirement"`
	FocusPoint    string `json:"focus_point"`
	RejectReason  string `json:"reject_reason"`
}

// BatchQuestionsDTO 批次明细（03 §3.8）：批次卡 + 全量题目（question_no 升序，不分页）。
type BatchQuestionsDTO struct {
	Batch     *BatchCardDTO       `json:"batch"`
	Questions []ReviewQuestionDTO `json:"questions"`
}

// RejectItem 确认入库请求的单条驳回标记（03 §3.9）。
type RejectItem struct {
	QuestionID int64
	Reason     string
}

// ConfirmResult 确认入库响应（03 §3.9）。
type ConfirmResult struct {
	BatchID       string `json:"batch_id"`
	BatchStatus   string `json:"batch_status"`
	AdmittedCount int64  `json:"admitted_count"`
	RejectedCount int64  `json:"rejected_count"`
}

// ResubmitInput 重新提交入参（03 §3.5，字段同编辑接口）。
type ResubmitInput struct {
	ID           int64
	DimensionID  int64
	Scenario     string
	Requirement  string
	FocusPoint   string
	Version      int
}

// ResubmitResult 重新提交响应（03 §3.5）：归入的重新送审批次与新版本号。
type ResubmitResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	BatchID string `json:"batch_id"`
	BatchNo string `json:"batch_no"`
	Version int    `json:"version"`
}

// QuestionBatchService 题库批次域业务接口。
type QuestionBatchService interface {
	ListPendingBatches(ctx context.Context) ([]BatchCardDTO, error)
	GetBatchQuestions(ctx context.Context, batchID int64) (*BatchQuestionsDTO, error)
	ConfirmBatch(ctx context.Context, batchID int64, rejected []RejectItem) (*ConfirmResult, error)
	VoidBatch(ctx context.Context, batchID int64) error
	ResubmitQuestion(ctx context.Context, in ResubmitInput) (*ResubmitResult, error)
}

type questionBatchService struct {
	repo    repository.QuestionBatchRepository
	qs      *questionService
	dimRepo repository.DimensionRepository
}

// NewQuestionBatchService 构造批次域 service。复用 questionService 的私有校验与
// 名称回填（同包直调，题目行读取走 qRepo 同一实例）。
func NewQuestionBatchService(repo repository.QuestionBatchRepository, qRepo repository.QuestionRepository, dimRepo repository.DimensionRepository) QuestionBatchService {
	return &questionBatchService{
		repo:    repo,
		qs:      &questionService{repo: qRepo, dimRepo: dimRepo},
		dimRepo: dimRepo,
	}
}

// batchCardDTO 批次行 → 卡片 DTO 组装。
func batchCardDTO(b *domain.QuestionBatch) *BatchCardDTO {
	return &BatchCardDTO{
		ID:            int64ToString(b.ID),
		BatchNo:       b.BatchNo,
		Title:         b.Title,
		Source:        b.Source,
		BatchType:     b.BatchType,
		QuestionCount: b.QuestionCount,
		CreatedAt:     b.CreatedAt,
	}
}

// resolveBatch 批次存在性收敛：NotFound 映射 1702，DB 错误 wrap 上报。
func (s *questionBatchService) resolveBatch(ctx context.Context, id int64) (*domain.QuestionBatch, error) {
	b, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.QuestionBatchNotFound)
		}
		return nil, fmt.Errorf("find question batch by id: %w", err)
	}
	return b, nil
}

// ListPendingBatches 卡区列表（03 §3.7）：仅 PENDING、created_at DESC 由 repo 承载。
func (s *questionBatchService) ListPendingBatches(ctx context.Context) ([]BatchCardDTO, error) {
	list, err := s.repo.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending batches: %w", err)
	}
	cards := make([]BatchCardDTO, 0, len(list))
	for i := range list {
		cards = append(cards, *batchCardDTO(&list[i]))
	}
	return cards, nil
}

// GetBatchQuestions 批次明细（03 §3.8）：非 PENDING 返 1706（前端刷新卡区与列表）；
// 维度名回显复用 SP1 双查回填；reject_reason 恒置空串。
func (s *questionBatchService) GetBatchQuestions(ctx context.Context, batchID int64) (*BatchQuestionsDTO, error) {
	b, err := s.resolveBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.QuestionBatchStatusPending {
		return nil, NewError(errcode.QuestionBatchClosed)
	}
	rows, err := s.repo.ListQuestionsByBatchID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("list batch questions: %w", err)
	}
	dimIDs := make([]int64, len(rows))
	for i := range rows {
		dimIDs[i] = rows[i].DimensionID
	}
	names, err := s.qs.listDimensionNames(ctx, dimIDs...)
	if err != nil {
		return nil, err
	}
	questions := make([]ReviewQuestionDTO, 0, len(rows))
	for i := range rows {
		q := &rows[i]
		questions = append(questions, ReviewQuestionDTO{
			ID:            int64ToString(q.ID),
			QuestionNo:    q.QuestionNo,
			DimensionID:   int64ToString(q.DimensionID),
			DimensionName: names[q.DimensionID],
			AnswerMode:    answerModeOf(q.Source),
			Scenario:      q.Scenario,
			Requirement:   q.Requirement,
			FocusPoint:    q.FocusPoint,
			RejectReason:  "", // 恒空串（03 §3.8），标记在前端
		})
	}
	return &BatchQuestionsDTO{Batch: batchCardDTO(b), Questions: questions}, nil
}

// ConfirmBatch 确认入库编排（03 §3.9，specs §4.2.3/§4.1.4 规则 2/3/7）：终态前置
// 在读快照上先判 1706（作废后的生成批题目已软删，计数口径失真须先挡），rejected
// 归属与原因校验、批内计数一致性校验全部通过后才进 repo 事务；repo 哨兵再兜底
// 读后并发关闭。
func (s *questionBatchService) ConfirmBatch(ctx context.Context, batchID int64, rejected []RejectItem) (*ConfirmResult, error) {
	b, err := s.resolveBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if b.Status != domain.QuestionBatchStatusPending {
		return nil, NewError(errcode.QuestionBatchClosed)
	}
	rows, err := s.repo.ListQuestionsByBatchID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("list batch questions: %w", err)
	}
	batchIDs := make(map[int64]bool, len(rows))
	for i := range rows {
		batchIDs[rows[i].ID] = true
	}
	rejectedMap := make(map[int64]string, len(rejected))
	for _, item := range rejected {
		if n := utf8.RuneCountInString(item.Reason); n < 1 || n > rejectReasonMax {
			return nil, NewError(errcode.BadRequest)
		}
		if !batchIDs[item.QuestionID] {
			return nil, NewError(errcode.BadRequest)
		}
		rejectedMap[item.QuestionID] = item.Reason
	}
	// 计数一致性：批内题目被并发删除后与 question_count 对不上，按冲突处理。
	if int64(len(rows)) != int64(b.QuestionCount) {
		return nil, NewError(errcode.QuestionVersionConflict)
	}
	admitted, rejectedCount, err := s.repo.ConfirmBatch(ctx, batchID, rejectedMap)
	if err != nil {
		if errors.Is(err, repository.ErrBatchNotPending) {
			return nil, NewError(errcode.QuestionBatchClosed)
		}
		return nil, fmt.Errorf("confirm batch: %w", err)
	}
	return &ConfirmResult{
		BatchID:       int64ToString(batchID),
		BatchStatus:   domain.QuestionBatchStatusClosed,
		AdmittedCount: admitted,
		RejectedCount: rejectedCount,
	}, nil
}

// VoidBatch 作废编排（03 §3.10）：批次语义分流收在 repo 事务，service 只收敛
// 存在性与哨兵映射。
func (s *questionBatchService) VoidBatch(ctx context.Context, batchID int64) error {
	if _, err := s.resolveBatch(ctx, batchID); err != nil {
		return err
	}
	if err := s.repo.VoidBatch(ctx, batchID); err != nil {
		if errors.Is(err, repository.ErrBatchNotPending) {
			return NewError(errcode.QuestionBatchClosed)
		}
		return fmt.Errorf("void batch: %w", err)
	}
	return nil
}

// ResubmitQuestion 重新提交归批（03 §3.5，specs §4.1.4 规则 6、§6.3 已驳回→待审核）：
// 仅 AI 题 REJECTED 前置；文本与维度校验复用 SP1 编辑同一私有函数；updates 列集
// 不含 reject_reason（repo 层保留原值）；成功后经 FindByID 回读组装响应。
func (s *questionBatchService) ResubmitQuestion(ctx context.Context, in ResubmitInput) (*ResubmitResult, error) {
	cur, err := s.qs.resolveQuestion(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if cur.Source != domain.QuestionSourceAI || cur.Status != domain.QuestionStatusRejected {
		return nil, NewError(errcode.QuestionNotEditable)
	}
	if err := validateQuestionText(in.Scenario, in.Requirement, in.FocusPoint); err != nil {
		return nil, err
	}
	enabled, err := s.qs.enabledAIMgmtDimensionIDs(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled[in.DimensionID] {
		return nil, NewError(errcode.BadRequest)
	}
	// 请求携带的乐观锁令牌覆盖快照版本，repo WHERE version 守卫消费。
	cur.Version = in.Version
	batch, err := s.repo.ResubmitToBatch(ctx, cur, map[string]any{
		"dimension_id": in.DimensionID,
		"scenario":     in.Scenario,
		"requirement":  in.Requirement,
		"focus_point":  in.FocusPoint,
	})
	if err != nil {
		if errors.Is(err, repository.ErrVersionConflict) {
			return nil, NewError(errcode.QuestionVersionConflict)
		}
		return nil, fmt.Errorf("resubmit question: %w", err)
	}
	// 回读最新行取新版本与状态；刚成功即被并发删除的极端情况按冲突提示刷新。
	q, err := s.qs.repo.FindByID(ctx, in.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.QuestionVersionConflict)
		}
		return nil, fmt.Errorf("reload question: %w", err)
	}
	return &ResubmitResult{
		ID:      int64ToString(q.ID),
		Status:  q.Status,
		BatchID: int64ToString(q.BatchID),
		BatchNo: batch.BatchNo,
		Version: q.Version,
	}, nil
}
