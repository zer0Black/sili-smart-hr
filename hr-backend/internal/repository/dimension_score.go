// DimensionScoreRepository 承载维度评分行的幂等读写（specs TECH_005 §2.4 能力1）。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// DimensionScoreRepository 是维度评分记录的数据访问接口。
type DimensionScoreRepository interface {
	// ListByPersonPeriodExact 按人加双界精确匹配读评分行（period_start_at 与
	// period_end_at 均等于传入值），供 Evaluate 幂等判定与 Aggregate 聚合读数。
	// start/end 为 Unix 秒，仓储内转 UTC。返回全部 status 行，dimension_code ASC。
	ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error)
	// DeleteConversationFailed 删同人同周期双界精确匹配下 source=conversation 且
	// status=failed 的行（先删后评，active_test 行不动），返回删除行数。
	DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error)
	// SaveAll 按唯一索引 (token_name, period_start_at, dimension_code) upsert 全列覆盖：
	// token_name 与双界周期以入参为权威回填，存在同键行则 UPDATE 全业务列（map 形态，
	// 空串与 false 须写入），并发双写撞唯一索引转查行后覆盖。
	SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error
}

type dimensionScoreRepository struct {
	db *gorm.DB
}

// NewDimensionScoreRepository 返回 DimensionScoreRepository 接口实现。
func NewDimensionScoreRepository(db *gorm.DB) DimensionScoreRepository {
	return &dimensionScoreRepository{db: db}
}

// ListByPersonPeriodExact 双界精确匹配（= 与 =）：定向分析窄窗口行与周期批量行按
// period_start 隔离、同 start 不同 end 的行也须隔离（specs 能力5 聚合域口径）。
// 端点统一 UTC 口径（ListByPersonAndRange 同款，SQLite 文本列偏移串字典序可比前提）。
func (r *dimensionScoreRepository) ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error) {
	var list []domain.DimensionScore
	err := r.db.WithContext(ctx).
		Where("token_name = ? AND period_start_at = ? AND period_end_at = ?",
			tokenName, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Order("dimension_code ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteConversationFailed 先删后评的删除侧：仅 source=conversation 且 status=failed，
// 双界精确匹配圈定本周期（active_test 行与历史周期行不动，specs 能力6 规则2）。
func (r *dimensionScoreRepository) DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("token_name = ? AND period_start_at = ? AND period_end_at = ? AND source = ? AND status = ?",
			tokenName, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC(),
			domain.ScoreSourceConversation, domain.ScoreStatusFailed).
		Delete(&domain.DimensionScore{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// SaveAll 逐行 upsert：前置查询按唯一键命中则 UPDATE 全业务列，否则 Create。
// token_name 与双界周期以入参为权威回填行（喂 LLM 前剥离、落库时回填，specs 能力1）。
func (r *dimensionScoreRepository) SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error {
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

// upsertOne 单行 upsert，session_feature 的 Save 同款骨架：前置查询 + UPDATE map 全列
// 覆盖 + Create 撞唯一索引转查后覆盖。map 形态防 struct Updates 跳过空串与 false 零值。
func (r *dimensionScoreRepository) upsertOne(db *gorm.DB, row *domain.DimensionScore) error {
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
		// 并发双写后写者：撞唯一索引转查行后按库内行覆盖收敛。
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
func (r *dimensionScoreRepository) findByUniqueKey(db *gorm.DB, row *domain.DimensionScore) (*domain.DimensionScore, error) {
	var existing domain.DimensionScore
	err := db.Where("token_name = ? AND period_start_at = ? AND dimension_code = ?",
		row.TokenName, row.PeriodStartAt, row.DimensionCode).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

// updateByID 全业务列覆盖（map 形态空串与 false 须写入，updated_at 显式 UTC 与
// session_feature.updateColumns 同口径），保留原行 ID。
func (r *dimensionScoreRepository) updateByID(db *gorm.DB, row *domain.DimensionScore, id int64) error {
	return db.Model(&domain.DimensionScore{}).Where("id = ?", id).Updates(map[string]interface{}{
		"token_name":      row.TokenName,
		"period_start_at": row.PeriodStartAt,
		"period_end_at":   row.PeriodEndAt,
		"dimension_code":  row.DimensionCode,
		"module":          row.Module,
		"score":           row.Score,
		"rationale":       row.Rationale,
		"insufficient":    row.Insufficient,
		"evidence_json":   row.EvidenceJSON,
		"source":          row.Source,
		"model_name":      row.ModelName,
		"prompt_version":  row.PromptVersion,
		"status":          row.Status,
		"error_code":      row.ErrorCode,
		"updated_at":      utcNow(),
	}).Error
}
