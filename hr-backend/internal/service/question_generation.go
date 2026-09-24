// question_generation 生成会话业务层：发起校验（题数/维度集合/模型已配置）、
// QUEUED 建行与任务投递编排、进度轮询组装与协作式取消（specs P2_QBN_001
// §4.3.2/§4.3.3/§4.3.4 规则 1/3、§4.3.5，03 §3.13/§3.14）。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// 题数边界（specs §4.3.2：整数 5~30）。
const (
	generationCountMin = 5
	generationCountMax = 30
)

// CreateGenerationDTO 发起响应（03 §3.13）。
type CreateGenerationDTO struct {
	GenerationID string `json:"generation_id"`
	Status       string `json:"status"`
}

// GenerationProgressDTO 进度轮询响应（03 §3.14）：batch_id/batch_no 仅 COMPLETED
// 携带，error_code 仅 FAILED/CANCELED 携带（omitempty 收敛）。
type GenerationProgressDTO struct {
	GenerationID         string `json:"generation_id"`
	Status               string `json:"status"`
	GeneratedCount       int    `json:"generated_count"`
	Count                int    `json:"count"`
	CurrentDimensionID   string `json:"current_dimension_id"`   // 未开始 "0"
	CurrentDimensionName string `json:"current_dimension_name"` // 回显维度名，未开始空串
	BatchID              string `json:"batch_id,omitempty"`
	BatchNo              string `json:"batch_no,omitempty"`
	ErrorCode            string `json:"error_code,omitempty"`
}

// GenerationEnqueuer 生成任务投递窄接口：service 层不 import asynq，
// 装配层用 AsynqClient 适配（providers.go，enqueue.go 同款）。
type GenerationEnqueuer interface {
	EnqueueGenerate(ctx context.Context, generationID int64) error
}

// QuestionGenerationService 生成会话域业务接口。
type QuestionGenerationService interface {
	CreateGeneration(ctx context.Context, dimensionIDs []int64, count int) (*CreateGenerationDTO, error)
	GetProgress(ctx context.Context, id int64) (*GenerationProgressDTO, error)
	CancelGeneration(ctx context.Context, id int64) error
}

type questionGenerationService struct {
	repo      repository.QuestionGenerationRepository
	dimRepo   repository.DimensionRepository
	batchRepo repository.QuestionBatchRepository
	enqueuer  GenerationEnqueuer
	provider  llm.EnabledModelProvider
}

// NewQuestionGenerationService 构造生成会话 service：provider 探测启用模型
// （发起前置），enqueuer 投递 questionbank:generate 任务。
func NewQuestionGenerationService(repo repository.QuestionGenerationRepository,
	dimRepo repository.DimensionRepository, batchRepo repository.QuestionBatchRepository,
	enqueuer GenerationEnqueuer, provider llm.EnabledModelProvider) QuestionGenerationService {
	return &questionGenerationService{
		repo: repo, dimRepo: dimRepo, batchRepo: batchRepo,
		enqueuer: enqueuer, provider: provider,
	}
}

// CreateGeneration 发起编排（03 §3.13）：count 5~30、维度集合限当前启用 AI_MGMT、
// 模型已排他启用三重前置；建 QUEUED 行（维度快照 JSON）后投递，投递失败删行
// 回 1500（不留 worker 永不领取的孤儿行）。
func (s *questionGenerationService) CreateGeneration(ctx context.Context, dimensionIDs []int64, count int) (*CreateGenerationDTO, error) {
	if count < generationCountMin || count > generationCountMax {
		return nil, NewError(errcode.BadRequest)
	}
	deduped, err := s.validateDimensions(ctx, dimensionIDs)
	if err != nil {
		return nil, err
	}
	if _, err := s.provider.GetEnabledModel(ctx); err != nil {
		if errors.Is(err, ErrLLMModelNotEnabled) {
			return nil, NewError(errcode.LLMNotConfigured)
		}
		return nil, fmt.Errorf("probe enabled llm model: %w", err)
	}
	snapshot, err := json.Marshal(deduped)
	if err != nil {
		return nil, fmt.Errorf("marshal dimension snapshot: %w", err)
	}
	row := &domain.QuestionGeneration{
		DimensionIDs: string(snapshot),
		Count:        count,
		Status:       domain.QuestionGenStatusQueued,
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, fmt.Errorf("create question generation: %w", err)
	}
	if err := s.enqueuer.EnqueueGenerate(ctx, row.ID); err != nil {
		// 投递失败回滚建行：QUEUED 孤儿行无消费者，留着会污染发起方轮询。
		if derr := s.repo.Delete(ctx, row.ID); derr != nil {
			return nil, fmt.Errorf("enqueue question generate (cleanup failed: %v): %w", derr, err)
		}
		return nil, NewError(errcode.Internal)
	}
	return &CreateGenerationDTO{
		GenerationID: int64ToString(row.ID),
		Status:       domain.QuestionGenStatusQueued,
	}, nil
}

// validateDimensions 维度集合校验（specs §4.3.2：至少 1 项，限当前启用 AI_MGMT）：
// 去重（保首现顺序，快照稳定）后逐项须命中启用集合，任一未命中（停用/非
// AI_MGMT/不存在）即 1400。
func (s *questionGenerationService) validateDimensions(ctx context.Context, dimensionIDs []int64) ([]int64, error) {
	if len(dimensionIDs) == 0 {
		return nil, NewError(errcode.BadRequest)
	}
	enabled, err := (&questionService{dimRepo: s.dimRepo}).enabledAIMgmtDimensionIDs(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(dimensionIDs))
	deduped := make([]int64, 0, len(dimensionIDs))
	for _, id := range dimensionIDs {
		if !enabled[id] {
			return nil, NewError(errcode.BadRequest)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		deduped = append(deduped, id)
	}
	return deduped, nil
}

// GetProgress 进度组装（03 §3.14）：RUNNING 态当前维度名回显复用 SP1 双查回填
// （软删回传存量名）；COMPLETED 附批次号；未开始维度提示 "0"/空串。
func (s *questionGenerationService) GetProgress(ctx context.Context, id int64) (*GenerationProgressDTO, error) {
	g, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.GenerationNotFound)
		}
		return nil, fmt.Errorf("find question generation: %w", err)
	}
	dto := &GenerationProgressDTO{
		GenerationID:   int64ToString(g.ID),
		Status:         g.Status,
		GeneratedCount: g.GeneratedCount,
		Count:          g.Count,
	}
	if g.CurrentDimensionID != 0 {
		dto.CurrentDimensionID = int64ToString(g.CurrentDimensionID)
		if g.Status == domain.QuestionGenStatusRunning {
			name, nerr := (&questionService{dimRepo: s.dimRepo}).resolveDimensionName(ctx, g.CurrentDimensionID)
			if nerr != nil {
				return nil, nerr
			}
			dto.CurrentDimensionName = name
		}
	} else {
		dto.CurrentDimensionID = "0"
	}
	if g.Status == domain.QuestionGenStatusCompleted && g.BatchID != 0 {
		dto.BatchID = int64ToString(g.BatchID)
		b, berr := s.batchRepo.FindByID(ctx, g.BatchID)
		if berr != nil {
			if errors.Is(berr, gorm.ErrRecordNotFound) {
				return nil, NewError(errcode.Internal)
			}
			return nil, fmt.Errorf("find generation batch: %w", berr)
		}
		dto.BatchNo = b.BatchNo
	}
	if g.Status == domain.QuestionGenStatusFailed || g.Status == domain.QuestionGenStatusCanceled {
		dto.ErrorCode = g.ErrorCode
	}
	return dto, nil
}

// CancelGeneration 协作式取消（03 §3.14 尾部）：存在性收敛 1703，取消语义
// （QUEUED/RUNNING 置 CANCELED、终态幂等成功）由 repo.RequestCancel 承载。
func (s *questionGenerationService) CancelGeneration(ctx context.Context, id int64) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.GenerationNotFound)
		}
		return fmt.Errorf("find question generation: %w", err)
	}
	if err := s.repo.RequestCancel(ctx, id); err != nil {
		return fmt.Errorf("request cancel question generation: %w", err)
	}
	return nil
}
