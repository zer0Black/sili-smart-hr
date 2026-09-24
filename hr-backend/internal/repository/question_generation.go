// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"errors"
	"time"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// ErrNotQueued 生成会话非 QUEUED（已领取/已终态/不存在）：MarkRunning 领取 CAS 拒绝。
var ErrNotQueued = errors.New("question generation not queued")

// ErrNotRunning 生成会话非 RUNNING（已终态/被取消/不存在）：FinishCompleted 前置拒绝。
var ErrNotRunning = errors.New("question generation not running")

// ErrAlreadyTerminal 生成会话已终态且不可逆：FinishTerminal 幂等重入拒绝。
var ErrAlreadyTerminal = errors.New("question generation already terminal")

// QuestionGenerationRepository 是 LLM 生成会话（过程表）的数据访问接口。
// 状态迁移全带前置状态 WHERE（CAS），staging 整串覆盖，完成事务三表收口在
// FinishCompleted 单方法内（specs 04 §3.3）。
type QuestionGenerationRepository interface {
	Create(ctx context.Context, g *domain.QuestionGeneration) error
	FindByID(ctx context.Context, id int64) (*domain.QuestionGeneration, error)
	// MarkRunning worker 领取：仅 status=QUEUED 时置 RUNNING，RowsAffected=0
	// 返回 ErrNotQueued（已被取消等）。
	MarkRunning(ctx context.Context, id int64) error
	// SaveProgress 更新进度与暂存：generated_count/current_dimension_id/staging
	// 原子覆盖。读-改-写在 engine 单写者内串行，无并发冲突面。
	SaveProgress(ctx context.Context, id int64, generatedCount int, currentDimensionID int64, staging string) error
	// FinishCompleted 完成事务（specs 04 §3.3）：同事务内建 GENERATE 批次 +
	// staging 展开插 questions（编号连续分配收在事务内，Generator 不预填
	// question_no、status=PENDING、batch_id 回填）+ generation 置 COMPLETED +
	// batch_id 回填 + staging 清空。失败整体回滚上抛，engine 决定是否 FinishTerminal。
	FinishCompleted(ctx context.Context, id int64, batch *domain.QuestionBatch, questions *[]domain.Question) error
	// FinishTerminal 失败/取消终态：置 status（FAILED/CANCELED）+ error_code +
	// staging 清空。仅 RUNNING/QUEUED 可置，已终态返回 ErrAlreadyTerminal。
	FinishTerminal(ctx context.Context, id int64, status, errorCode string) error
	// RequestCancel 协作式取消：仅 QUEUED/RUNNING 置 CANCELED（error_code=CANCELED），
	// 已终态幂等成功（RowsAffected=0 视为已终态，返回 nil）。
	RequestCancel(ctx context.Context, id int64) error
}

type questionGenerationRepository struct {
	db *gorm.DB
}

// NewQuestionGenerationRepository 返回 QuestionGenerationRepository 接口实现。
func NewQuestionGenerationRepository(db *gorm.DB) QuestionGenerationRepository {
	return &questionGenerationRepository{db: db}
}

func (r *questionGenerationRepository) Create(ctx context.Context, g *domain.QuestionGeneration) error {
	return r.db.WithContext(ctx).Create(g).Error
}

func (r *questionGenerationRepository) FindByID(ctx context.Context, id int64) (*domain.QuestionGeneration, error) {
	var g domain.QuestionGeneration
	if err := r.db.WithContext(ctx).First(&g, id).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// MarkRunning CAS 领取（specs 04 §3.3 状态流转）：WHERE 恒带 status=QUEUED，
// RowsAffected=0 即已被领取/取消/不存在，返回 ErrNotQueued。map Updates 不触发
// autoUpdateTime，显式写 updated_at（与 SP2 先例一致）。
func (r *questionGenerationRepository) MarkRunning(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Model(&domain.QuestionGeneration{}).
		Where("id = ? AND status = ?", id, domain.QuestionGenStatusQueued).
		Updates(map[string]any{
			"status":     domain.QuestionGenStatusRunning,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotQueued
	}
	return nil
}

// SaveProgress staging 整串覆盖：engine 单写者内读-改-写串行，无合并语义，
// 旧暂存被直接替换。
func (r *questionGenerationRepository) SaveProgress(ctx context.Context, id int64, generatedCount int, currentDimensionID int64, staging string) error {
	return r.db.WithContext(ctx).Model(&domain.QuestionGeneration{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"generated_count":      generatedCount,
			"current_dimension_id": currentDimensionID,
			"staging":              staging,
			"updated_at":           time.Now(),
		}).Error
}

// FinishCompleted 单事务收口（specs 04 §3.3 完成事务）：NextBatchNo("G") 建批
// （BatchNo 空 时由本方法生成）→ CreateBatchWithQuestions(外层 tx) 展开 staging
// 落 questions（Q-AG 编号连续分配、PENDING、挂批）→ generation 置 COMPLETED +
// batch_id 回填 + staging 清空。WHERE 恒带 status=RUNNING，RowsAffected=0 返回
// ErrNotRunning 整体回滚（questions 零残留）。
func (r *questionGenerationRepository) FinishCompleted(ctx context.Context, id int64, batch *domain.QuestionBatch, questions *[]domain.Question) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		batchRepo := NewQuestionBatchRepository(tx)
		if batch.BatchNo == "" {
			no, err := batchRepo.NextBatchNo(ctx, "G", time.Now())
			if err != nil {
				return err
			}
			batch.BatchNo = no
		}
		if err := batchRepo.CreateBatchWithQuestions(ctx, tx, batch, questions); err != nil {
			return err
		}
		res := tx.Model(&domain.QuestionGeneration{}).
			Where("id = ? AND status = ?", id, domain.QuestionGenStatusRunning).
			Updates(map[string]any{
				"status":     domain.QuestionGenStatusCompleted,
				"batch_id":   batch.ID,
				"staging":    "",
				"updated_at": time.Now(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotRunning
		}
		return nil
	})
}

// FinishTerminal 终态清空暂存（specs 04 §3.3 失败零残留）：仅 RUNNING/QUEUED 可置，
// 已终态 RowsAffected=0 返回 ErrAlreadyTerminal（调用方忽略）。
func (r *questionGenerationRepository) FinishTerminal(ctx context.Context, id int64, status, errorCode string) error {
	res := r.db.WithContext(ctx).Model(&domain.QuestionGeneration{}).
		Where("id = ? AND status IN ?", id, []string{
			domain.QuestionGenStatusRunning, domain.QuestionGenStatusQueued,
		}).
		Updates(map[string]any{
			"status":     status,
			"error_code": errorCode,
			"staging":    "",
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAlreadyTerminal
	}
	return nil
}

// RequestCancel 协作式取消：cancel 只置状态不强杀任务，staging 同步清空；
// 已终态（COMPLETED/FAILED/CANCELED/不存在）幂等成功返回 nil。
func (r *questionGenerationRepository) RequestCancel(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Model(&domain.QuestionGeneration{}).
		Where("id = ? AND status IN ?", id, []string{
			domain.QuestionGenStatusQueued, domain.QuestionGenStatusRunning,
		}).
		Updates(map[string]any{
			"status":     domain.QuestionGenStatusCanceled,
			"error_code": domain.QuestionGenErrorCanceled,
			"staging":    "",
			"updated_at": time.Now(),
		})
	return res.Error
}
