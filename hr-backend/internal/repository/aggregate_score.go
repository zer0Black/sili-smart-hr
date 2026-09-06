// AggregateScoreRepository 承载聚合结果行的幂等写入（specs TECH_005 §2.4 能力5）。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
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

// UpsertAll 逐行 upsert，骨架同 dimension_score 的 SaveAll。
func (r *aggregateScoreRepository) UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error {
	db := r.db.WithContext(ctx)
	startAt, endAt := time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
	for i := range rows {
		rows[i].TokenName = tokenName
		rows[i].PeriodStartAt = startAt
		rows[i].PeriodEndAt = endAt
		if err := r.upsertOne(db, &rows[i]); err != nil {
			return err
		}
	}
	return nil
}

// upsertOne 单行 upsert：前置查询 + UPDATE map 全列覆盖（nil 指针显式落 NULL，
// map 形态不被零值跳过）+ Create 撞唯一索引转查后覆盖。
func (r *aggregateScoreRepository) upsertOne(db *gorm.DB, row *domain.AggregateScore) error {
	existing, err := r.findByUniqueKey(db, row)
	if err != nil {
		return err
	}
	if existing != nil {
		return r.updateByID(db, row, existing.ID)
	}
	row.CreatedAt, row.UpdatedAt = utcNow(), utcNow()
	if err := db.Create(row).Error; err != nil {
		if !dberr.UniqueViolation(err) {
			return err
		}
		existing, ferr := r.findByUniqueKey(db, row)
		if ferr != nil {
			return ferr
		}
		if existing == nil {
			return err
		}
		return r.updateByID(db, row, existing.ID)
	}
	return nil
}

// findByUniqueKey 按唯一索引三列点查，无行返回 (nil, nil)。
func (r *aggregateScoreRepository) findByUniqueKey(db *gorm.DB, row *domain.AggregateScore) (*domain.AggregateScore, error) {
	var existing domain.AggregateScore
	err := db.Where("token_name = ? AND period_start_at = ? AND module = ?",
		row.TokenName, row.PeriodStartAt, row.Module).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// updateByID 全业务列覆盖：module_score/overview_score 的 nil 经 map 显式写 NULL
//（全剔除覆盖旧值，specs 能力5 规则6），保留原行 ID，updated_at 显式 UTC。
func (r *aggregateScoreRepository) updateByID(db *gorm.DB, row *domain.AggregateScore, id int64) error {
	return db.Model(&domain.AggregateScore{}).Where("id = ?", id).Updates(map[string]interface{}{
		"token_name":     row.TokenName,
		"period_start_at": row.PeriodStartAt,
		"period_end_at":   row.PeriodEndAt,
		"module":          row.Module,
		"module_score":    row.ModuleScore,
		"overview_score":  row.OverviewScore,
		"included_json":   row.IncludedJSON,
		"excluded_json":   row.ExcludedJSON,
		"updated_at":      utcNow(),
	}).Error
}
