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
	// ListByToken 按人全量读各期聚合行（含 overview 行），period_start_at ASC，供 B1 区间并集与较上期。
	ListByToken(ctx context.Context, tokenName string) ([]domain.AggregateScore, error)
	// ListLatestModuleRowsByTokens 批量取每人每模块最新聚合行（不含 overview 行），
	// 两模块最新周期可各自错位，供 A1 两模块分数列。
	ListLatestModuleRowsByTokens(ctx context.Context, tokenNames []string) ([]domain.AggregateScore, error)
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

// ListByToken 按人全量各期聚合行（含 overview 行），period_start_at ASC、同期内
// module 次序稳定（specs P2_PRF_001 §5.2.2 步骤2 区间并集、步骤4 较上期）。
func (r *aggregateScoreRepository) ListByToken(ctx context.Context, tokenName string) ([]domain.AggregateScore, error) {
	var list []domain.AggregateScore
	err := r.db.WithContext(ctx).
		Where("token_name = ?", tokenName).
		Order("period_start_at ASC, module ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListLatestModuleRowsByTokens 批量 IN 单条 SQL 取每人每模块最新聚合行（specs
// P2_PRF_001 §5.1.4 规则2：禁止逐人查询）：分组子查询 (token_name, module,
// MAX(period_start_at)) 自连接回主表，两模块最新周期可各自错位（§4.1.4 规则5）；
// 排除 overview 哨兵行（module_score 恒 NULL，列表分数列不消费）。
func (r *aggregateScoreRepository) ListLatestModuleRowsByTokens(ctx context.Context, tokenNames []string) ([]domain.AggregateScore, error) {
	if len(tokenNames) == 0 {
		return []domain.AggregateScore{}, nil
	}
	sub := r.db.WithContext(ctx).
		Model(&domain.AggregateScore{}).
		Select("token_name, module, MAX(period_start_at) AS period_start_at").
		Where("token_name IN ? AND module <> ?", tokenNames, domain.ModuleOverview).
		Group("token_name, module")
	var list []domain.AggregateScore
	err := r.db.WithContext(ctx).
		Where("(token_name, module, period_start_at) IN (?)", sub).
		Order("token_name ASC, module ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
