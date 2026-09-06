// ActivityStatRepository 承载活跃度统计行的幂等写入（specs TECH_005 §2.4 能力3）。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// ActivityStatRepository 是活跃度统计的数据访问接口。
type ActivityStatRepository interface {
	// Upsert 按唯一索引 (token_name, period_start_at) upsert 全列覆盖：同人同周期
	// 重跑按唯一索引更新，历史周期行不动（specs 能力3）。键与列值均取自 rec。
	Upsert(ctx context.Context, rec *domain.ActivityStat) error
}

type activityStatRepository struct {
	db *gorm.DB
}

// NewActivityStatRepository 返回 ActivityStatRepository 接口实现。
func NewActivityStatRepository(db *gorm.DB) ActivityStatRepository {
	return &activityStatRepository{db: db}
}

// Upsert 单行 upsert，骨架同 session_feature 的 Save（前置查询 + UPDATE map 全列
// 覆盖 + Create 撞唯一索引转查后覆盖）。键取自 rec 本身（能力3 落库行由业务层组装）。
func (r *activityStatRepository) Upsert(ctx context.Context, rec *domain.ActivityStat) error {
	db := r.db.WithContext(ctx)
	existing, err := r.findByUniqueKey(db, rec)
	if err != nil {
		return err
	}
	if existing != nil {
		return r.updateByID(db, rec, existing.ID)
	}
	rec.CreatedAt, rec.UpdatedAt = utcNow(), utcNow()
	if err := db.Create(rec).Error; err != nil {
		if !dberr.UniqueViolation(err) {
			return err
		}
		existing, ferr := r.findByUniqueKey(db, rec)
		if ferr != nil {
			return ferr
		}
		if existing == nil {
			return err
		}
		return r.updateByID(db, rec, existing.ID)
	}
	return nil
}

// findByUniqueKey 按唯一索引两列点查，无行返回 (nil, nil)。
func (r *activityStatRepository) findByUniqueKey(db *gorm.DB, rec *domain.ActivityStat) (*domain.ActivityStat, error) {
	var existing domain.ActivityStat
	err := db.Where("token_name = ? AND period_start_at = ?",
		rec.TokenName, rec.PeriodStartAt).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// updateByID 全业务列覆盖（map 形态空串与零值须写入），保留原行 ID，updated_at 显式 UTC。
func (r *activityStatRepository) updateByID(db *gorm.DB, rec *domain.ActivityStat, id int64) error {
	return db.Model(&domain.ActivityStat{}).Where("id = ?", id).Updates(map[string]interface{}{
		"token_name":          rec.TokenName,
		"period_start_at":     rec.PeriodStartAt,
		"period_end_at":       rec.PeriodEndAt,
		"session_count":       rec.SessionCount,
		"valid_session_count": rec.ValidSessionCount,
		"skipped_count":       rec.SkippedCount,
		"total_turns":         rec.TotalTurns,
		"active_level":        rec.ActiveLevel,
		"population_note":     rec.PopulationNote,
		"client_dist_json":    rec.ClientDistJSON,
		"updated_at":          utcNow(),
	}).Error
}
