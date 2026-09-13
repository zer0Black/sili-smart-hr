// AssessmentBatchRepository 承载批次域三表的数据访问：批次记录、人员明细与计数推进。
// 批次状态机与终态映射口径见 specs P2_ASM_001 §5.2.4/§6.2 与 04 §3.1/§3.2。
package repository

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// errorSummaryMaxLen 失败原因摘要截断长度，对齐列宽 varchar(255)（04 §3.1/§3.2）。
const errorSummaryMaxLen = 255

// BatchFilter 是批次列表的筛选与分页条件，TriggerType/Status 空串跳过条件。
type BatchFilter struct {
	TriggerType    string
	Status         string
	Page, PageSize int
}

// AssessmentBatchRepository 是批次域的数据访问接口（双重接口范式，account 样板）。
type AssessmentBatchRepository interface {
	Create(ctx context.Context, batch *domain.AssessmentBatch) error
	// CreatePersons 批量落人员明细行（建批时名单快照，初值 pending/0/""）。
	CreatePersons(ctx context.Context, persons []domain.AssessmentBatchPerson) error
	// GetByID 按主键点查，无行返 (nil, nil)。
	GetByID(ctx context.Context, id int64) (*domain.AssessmentBatch, error)
	// FindLatestRunningScheduled 取最近触发的 running 定时批次（tick 同源阻塞判定），
	// 无行返 (nil, nil)。
	FindLatestRunningScheduled(ctx context.Context) (*domain.AssessmentBatch, error)
	// ListByFilter 按 triggered_at DESC 倒序分页，返回 (list, total)。
	ListByFilter(ctx context.Context, f BatchFilter) ([]domain.AssessmentBatch, int64, error)
	// UpdateTotalSessions 单事务回填批次会话总数并逐人更新 session_count
	// （名单内无会话者为 0，人员行创建时已落）。
	UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error
	// AdvancePersonTerminal 单事务推进单人终态并原子自增批次计数（只增不减）。
	// 人员行 UPDATE 带 status='pending' 幂等守卫：affected==0（重入已终态）跳过计数自增。
	// 终态判定由调用方在事务外读回批次行，经 FinalizeBatch 守卫兜底。
	AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName string, personStatus, errorSummary string, sessionCount int) error
	// FinalizeBatch 落批次终态（WHERE status='running' 守卫，终态不可逆）：
	// affected==0 视为已终态幂等返回 nil。
	FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error
	// FailWholeBatch 批次级异常整批失败：人员行全落 failed、计数置满 N/N、批次落 failed。
	FailWholeBatch(ctx context.Context, batchID int64, reason string) error
	// CountInRange 统计 triggered_at ∈ [start, end) 的批次数（本期评测次数）。
	CountInRange(ctx context.Context, start, end time.Time) (int64, error)
	// CountRunningNonStalled 统计 running 且 triggered_at >= stalledBefore 的批次数。
	CountRunningNonStalled(ctx context.Context, stalledBefore time.Time) (int64, error)
	// CountSuccessSideInRanges 在批次 ID 列表范围内统计成功侧终态人员行数；
	// 空列表直接返回 0 不发 SQL。
	CountSuccessSideInRanges(ctx context.Context, batchIDs []int64) (int64, error)
}

type assessmentBatchRepository struct {
	db *gorm.DB
}

// NewAssessmentBatchRepository 返回 AssessmentBatchRepository 接口实现。
func NewAssessmentBatchRepository(db *gorm.DB) AssessmentBatchRepository {
	return &assessmentBatchRepository{db: db}
}

func (r *assessmentBatchRepository) Create(ctx context.Context, batch *domain.AssessmentBatch) error {
	return r.db.WithContext(ctx).Create(batch).Error
}

// CreatePersons 空切片直接返回 nil 不发 SQL。
func (r *assessmentBatchRepository) CreatePersons(ctx context.Context, persons []domain.AssessmentBatchPerson) error {
	if len(persons) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(persons, 100).Error
}

func (r *assessmentBatchRepository) GetByID(ctx context.Context, id int64) (*domain.AssessmentBatch, error) {
	var b domain.AssessmentBatch
	err := r.db.WithContext(ctx).First(&b, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *assessmentBatchRepository) FindLatestRunningScheduled(ctx context.Context) (*domain.AssessmentBatch, error) {
	var b domain.AssessmentBatch
	err := r.db.WithContext(ctx).
		Where("status = ? AND trigger_type = ?", domain.BatchStatusRunning, domain.BatchTriggerScheduled).
		Order("triggered_at DESC").
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListByFilter page 应 >= 1（由调用方保证）。
func (r *assessmentBatchRepository) ListByFilter(ctx context.Context, f BatchFilter) ([]domain.AssessmentBatch, int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{})
	if f.TriggerType != "" {
		query = query.Where("trigger_type = ?", f.TriggerType)
	}
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []domain.AssessmentBatch
	if err := query.
		Order("triggered_at DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *assessmentBatchRepository) UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.AssessmentBatch{}).
			Where("id = ?", batchID).
			Update("total_session_count", totalSessions).Error; err != nil {
			return err
		}
		for name, count := range personSessions {
			if err := tx.Model(&domain.AssessmentBatchPerson{}).
				Where("batch_id = ? AND token_name = ?", batchID, name).
				Update("session_count", count).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// AdvancePersonTerminal 终态映射（04 §3.2）：成功侧四态累计会话数，failed 只计失败数。
func (r *assessmentBatchRepository) AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName string, personStatus, errorSummary string, sessionCount int) error {
	if personStatus == domain.PersonStatusFailed {
		errorSummary = truncateRunes(errorSummary, errorSummaryMaxLen)
	} else {
		errorSummary = ""
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ? AND token_name = ? AND status = ?", batchID, tokenName, domain.PersonStatusPending).
			Updates(map[string]any{
				"status":        personStatus,
				"error_summary": errorSummary,
				"finished_at":   utcNow(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 重入（batch-run 重试）：该人已终态，跳过计数自增防双计。
			return nil
		}
		updates := map[string]any{"evaluated_count": gorm.Expr("evaluated_count + ?", 1)}
		if personStatus == domain.PersonStatusFailed {
			updates["failed_count"] = gorm.Expr("failed_count + ?", 1)
		} else {
			updates["covered_session_count"] = gorm.Expr("covered_session_count + ?", sessionCount)
		}
		return tx.Model(&domain.AssessmentBatch{}).
			Where("id = ?", batchID).
			Updates(updates).Error
	})
}

func (r *assessmentBatchRepository) FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error {
	return r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("id = ? AND status = ?", batchID, domain.BatchStatusRunning).
		Updates(map[string]any{
			"status":             status,
			"session_fail_ratio": sessionFailRatio,
			"finished_at":        utcNow(),
		}).Error
}

func (r *assessmentBatchRepository) FailWholeBatch(ctx context.Context, batchID int64, reason string) error {
	reason = truncateRunes(reason, errorSummaryMaxLen)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := utcNow()
		if err := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ?", batchID).
			Updates(map[string]any{
				"status":        domain.PersonStatusFailed,
				"error_summary": reason,
				"finished_at":   now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.AssessmentBatch{}).
			Where("id = ?", batchID).
			Updates(map[string]any{
				"status":          domain.BatchStatusFailed,
				"error_summary":   reason,
				"evaluated_count": gorm.Expr("total_count"),
				"failed_count":    gorm.Expr("total_count"),
				"finished_at":     now,
			}).Error
	})
}

func (r *assessmentBatchRepository) CountInRange(ctx context.Context, start, end time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("triggered_at >= ? AND triggered_at < ?", start, end).
		Count(&n).Error
	return n, err
}

func (r *assessmentBatchRepository) CountRunningNonStalled(ctx context.Context, stalledBefore time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("status = ? AND triggered_at >= ?", domain.BatchStatusRunning, stalledBefore).
		Count(&n).Error
	return n, err
}

func (r *assessmentBatchRepository) CountSuccessSideInRanges(ctx context.Context, batchIDs []int64) (int64, error) {
	if len(batchIDs) == 0 {
		return 0, nil
	}
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatchPerson{}).
		Where("batch_id IN ? AND status IN ?", batchIDs,
			[]string{domain.PersonStatusSuccess, domain.PersonStatusReused, domain.PersonStatusDegraded, domain.PersonStatusSkipped}).
		Count(&n).Error
	return n, err
}

// truncateRunes 按字符截断至多 max 个 rune，避免多字节字符被腰斩。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
