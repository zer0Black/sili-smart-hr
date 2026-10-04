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
	// ListByToken 按人全量读各期活跃度行，period_start_at ASC，供 B1 区间并集与所选区间行。
	ListByToken(ctx context.Context, tokenName string) ([]domain.ActivityStat, error)
	// ListLatestByTokens 批量取每人最新周期行，供 A1 活跃度列。
	ListLatestByTokens(ctx context.Context, tokenNames []string) ([]domain.ActivityStat, error)
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

// ListByToken 按人全量各期活跃度行，period_start_at ASC（specs P2_PRF_001
// §5.2.2 步骤2 区间并集、步骤3 所选区间行）。
func (r *activityStatRepository) ListByToken(ctx context.Context, tokenName string) ([]domain.ActivityStat, error) {
	var list []domain.ActivityStat
	err := r.db.WithContext(ctx).
		Where("token_name = ?", tokenName).
		Order("period_start_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListLatestByTokens 批量 IN 单条 SQL 取每人最新周期行（specs P2_PRF_001
// §5.1.4 规则2：禁止逐人查询）：分组子查询 (token_name, MAX(period_start_at))
// 自连接回主表，一人一行。
func (r *activityStatRepository) ListLatestByTokens(ctx context.Context, tokenNames []string) ([]domain.ActivityStat, error) {
	if len(tokenNames) == 0 {
		return []domain.ActivityStat{}, nil
	}
	sub := r.db.WithContext(ctx).
		Model(&domain.ActivityStat{}).
		Select("token_name, MAX(period_start_at) AS period_start_at").
		Where("token_name IN ?", tokenNames).
		Group("token_name")
	var list []domain.ActivityStat
	err := r.db.WithContext(ctx).
		Where("(token_name, period_start_at) IN (?)", sub).
		Order("token_name ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
