// DimensionScoreRepository 承载维度评分行的幂等读写（specs TECH_005 §2.4 能力1）。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	// 空串与 false 须写入）。
	SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error
	// ListByToken 按人全量读各期评分行（含全部 status/source），period_start_at ASC，
	// 供 B1 区间并集与 trend 组装。
	ListByToken(ctx context.Context, tokenName string) ([]domain.DimensionScore, error)
	// ListLatestByTokens 批量取每人每模块各自最新聚合周期的维度行：先按 (token_name, module)
	// 取 max(period_start_at) 对应周期，再取该 (人, 模块, 周期) 全部维度行。供 A1 降权计数
	// 与短板集合。
	ListLatestByTokens(ctx context.Context, tokenNames []string) ([]domain.DimensionScore, error)
	// ListByPeriodAllCompany 按双界精确匹配取全公司维度行（含 insufficient/failed 行，
	// 过滤在 service 层做），供 B1 公司均分。
	ListByPeriodAllCompany(ctx context.Context, start, end int64) ([]domain.DimensionScore, error)
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

// DeleteConversationFailed 先删后评的删除侧：source=conversation 且 status=failed，
// 或 error_code 带 skip_no_llm 标记的 success 跳过行（重跑自愈通道），双界精确
// 匹配圈定本周期（active_test 行与历史周期行不动，specs 能力6 规则2）。
func (r *dimensionScoreRepository) DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("token_name = ? AND period_start_at = ? AND period_end_at = ? AND source = ? AND (status = ? OR error_code = ?)",
			tokenName, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC(),
			domain.ScoreSourceConversation, domain.ScoreStatusFailed, domain.ErrorCodeSkipNoLLM).
		Delete(&domain.DimensionScore{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// SaveAll 批量 upsert（clause.OnConflict，SQLite/MySQL/PG 三方言支持）：
// 唯一索引 (token_name, period_start_at, dimension_code) 撞键时 UPDATE 全业务列
// （列名形态不被零值跳过，空串与 false 照写）。token_name 与双界周期以入参为
// 权威回填行（喂 LLM 前剥离、落库回填，specs 能力1）。
func (r *dimensionScoreRepository) SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error {
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
				{Name: "token_name"}, {Name: "period_start_at"}, {Name: "dimension_code"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"period_end_at", "module", "score", "rationale", "insufficient",
				"evidence_json", "source", "model_name", "prompt_version",
				"status", "error_code", "updated_at",
			}),
		}).
		Create(&rows).Error
}

// ListByToken 按人全量各期行，period_start_at ASC；同人内同期同码多行时
// dimension_code 次序稳定（specs P2_PRF_001 §5.2.2 步骤4）。
func (r *dimensionScoreRepository) ListByToken(ctx context.Context, tokenName string) ([]domain.DimensionScore, error) {
	var list []domain.DimensionScore
	err := r.db.WithContext(ctx).
		Where("token_name = ?", tokenName).
		Order("period_start_at ASC, dimension_code ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListLatestByTokens 批量 IN 单条 SQL 取每人每模块最新周期行（specs P2_PRF_001
// §5.1.4 规则2：禁止逐人循环查询）。分组子查询 (token_name, module, MAX(period_start_at))
// 自连接回主表：两模块最新周期可各自错位（§4.1.4 规则5），每 (人, 模块, 周期) 的
// 全部维度行整期返回。
func (r *dimensionScoreRepository) ListLatestByTokens(ctx context.Context, tokenNames []string) ([]domain.DimensionScore, error) {
	if len(tokenNames) == 0 {
		return []domain.DimensionScore{}, nil
	}
	sub := r.db.WithContext(ctx).
		Model(&domain.DimensionScore{}).
		Select("token_name, module, MAX(period_start_at) AS period_start_at").
		Where("token_name IN ?", tokenNames).
		Group("token_name, module")
	var list []domain.DimensionScore
	err := r.db.WithContext(ctx).
		Where("(token_name, module, period_start_at) IN (?)", sub).
		Order("token_name ASC, module ASC, dimension_code ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListByPeriodAllCompany 双界精确匹配全公司行（= 与 =），insufficient 与 failed
// 行一并返回，status 过滤归 service 层（specs P2_PRF_001 §5.2.2 步骤5）。
func (r *dimensionScoreRepository) ListByPeriodAllCompany(ctx context.Context, start, end int64) ([]domain.DimensionScore, error) {
	var list []domain.DimensionScore
	err := r.db.WithContext(ctx).
		Where("period_start_at = ? AND period_end_at = ?",
			time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Order("token_name ASC, dimension_code ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
