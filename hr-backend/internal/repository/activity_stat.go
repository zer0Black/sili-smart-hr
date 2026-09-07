// ActivityStatRepository 承载活跃度统计行的幂等写入（specs TECH_005 §2.4 能力3）。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// Upsert 单行 upsert（clause.OnConflict）：唯一索引 (token_name, period_start_at)
// 撞键时 UPDATE 全业务列（列名形态空串与零值照写），键与列值均取自 rec。
func (r *activityStatRepository) Upsert(ctx context.Context, rec *domain.ActivityStat) error {
	rec.UpdatedAt = utcNow()
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "token_name"}, {Name: "period_start_at"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"period_end_at", "session_count", "valid_session_count", "skipped_count",
				"total_turns", "active_level", "population_note", "client_dist_json",
				"updated_at",
			}),
		}).
		Create(rec).Error
}
