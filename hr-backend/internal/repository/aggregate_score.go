// AggregateScoreRepository 承载聚合结果行的幂等写入（specs TECH_005 §2.4 能力5）。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AggregateScoreRepository 是聚合结果的数据访问接口。
type AggregateScoreRepository interface {
	// UpsertAll 按唯一索引 (token_name, period_start_at, module) upsert 全列覆盖：
	// token_name 与双界周期以入参为权威回填；module_score/overview_score 为 *float64，
	// nil 显式落 NULL（全剔除覆盖旧值，specs 能力5 规则6），历史周期行不动。
	UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error
}

type aggregateScoreRepository struct {
	db *gorm.DB
}

// NewAggregateScoreRepository 返回 AggregateScoreRepository 接口实现。
func NewAggregateScoreRepository(db *gorm.DB) AggregateScoreRepository {
	return &aggregateScoreRepository{db: db}
}

// UpsertAll 批量 upsert（clause.OnConflict，骨架同 dimension_score 的 SaveAll）：
// 唯一索引撞键时 UPDATE 全业务列；module_score/overview_score 的 nil 经列名形态
// 显式落 NULL（全剔除覆盖旧值，specs 能力5 规则6），历史周期行不动。
func (r *aggregateScoreRepository) UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error {
	if len(rows) == 0 {
		return nil
	}
	startAt, endAt := time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
	for i := range rows {
		rows[i].TokenName = tokenName
		rows[i].PeriodStartAt = startAt
		rows[i].PeriodEndAt = endAt
		rows[i].UpdatedAt = utcNow()
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "token_name"}, {Name: "period_start_at"}, {Name: "module"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"period_end_at", "module_score", "overview_score",
				"included_json", "excluded_json", "updated_at",
			}),
		}).
		Create(&rows).Error
}
