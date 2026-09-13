// AssessmentAlertRepository 承载告警信号的数据访问。
// 每批次至多一条、重复判定幂等覆盖见 specs P2_ASM_001 §5.2.4 规则4（BR1）。
package repository

import (
	"context"
	"errors"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// AssessmentAlertRepository 是告警信号的数据访问接口（双重接口范式，account 样板）。
type AssessmentAlertRepository interface {
	// UpsertByBatch 按 uk_alert_batch 唯一索引 upsert：无行 Create、有行覆盖
	// failed_count/total_count/failed_ratio/signaled_at（batch_no 快照不变）。
	UpsertByBatch(ctx context.Context, alert *domain.AssessmentAlert) error
}

type assessmentAlertRepository struct {
	db *gorm.DB
}

// NewAssessmentAlertRepository 返回 AssessmentAlertRepository 接口实现。
func NewAssessmentAlertRepository(db *gorm.DB) AssessmentAlertRepository {
	return &assessmentAlertRepository{db: db}
}

func (r *assessmentAlertRepository) UpsertByBatch(ctx context.Context, alert *domain.AssessmentAlert) error {
	var existing domain.AssessmentAlert
	err := r.db.WithContext(ctx).Where("batch_id = ?", alert.BatchID).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := r.db.WithContext(ctx).Create(alert).Error; err != nil {
			if !dberr.UniqueViolation(err) {
				return err
			}
			// 并发双写撞 uk_alert_batch：转覆盖更新收敛（唯一索引兜底）。
			return r.updateByBatchID(ctx, alert)
		}
		return nil
	case err != nil:
		return err
	default:
		return r.updateByBatchID(ctx, alert)
	}
}

// updateByBatchID 覆盖更新告警字段；updated_at 经 autoUpdateTime 由 GORM 刷新。
func (r *assessmentAlertRepository) updateByBatchID(ctx context.Context, alert *domain.AssessmentAlert) error {
	return r.db.WithContext(ctx).Model(&domain.AssessmentAlert{}).
		Where("batch_id = ?", alert.BatchID).
		Updates(map[string]any{
			"failed_count": alert.FailedCount,
			"total_count":  alert.TotalCount,
			"failed_ratio": alert.FailedRatio,
			"signaled_at":  alert.SignaledAt,
		}).Error
}
