// DashboardQueryRepository 承载团队看板域聚合只读查询与建议生成 tick 的
// 批次拾取扫描（specs P2_TMD_001 §5.2.2、§5.1.2 步1、03 §4.1/§4.3）。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// PeriodBound 落库区间双界（看板区间列表与多区间批量查询的窗口元素）。
type PeriodBound struct {
	StartAt time.Time
	EndAt   time.Time
}

// DashboardQueryRepository 是看板聚合只读查询接口。
type DashboardQueryRepository interface {
	// ListPeriods 取 activity_stats/dimension_scores/aggregate_scores 三表
	// distinct (period_start_at, period_end_at) 并集，新到旧（start 降序，
	// 同 start 按 end 降序）。
	ListPeriods(ctx context.Context) ([]PeriodBound, error)
	// ListActivityByPeriod 双界精确匹配全行（三态计数输入，不过滤）。
	ListActivityByPeriod(ctx context.Context, start, end int64) ([]domain.ActivityStat, error)
	// ListDimScoresByPeriod 双界精确匹配全公司维度行（含 insufficient/failed，
	// 过滤在 service 层，形态复用 ListByPeriodAllCompany）。
	ListDimScoresByPeriod(ctx context.Context, start, end int64) ([]domain.DimensionScore, error)
	// ListDimScoresByPeriods 窗口多区间批量取维度行（逐区间 OR 条件，
	// 三库兼容），行级过滤在 service 内存做。
	ListDimScoresByPeriods(ctx context.Context, bounds []PeriodBound) ([]domain.DimensionScore, error)
	// ListModuleAggScoresByPeriods 同窗口批量取模块聚合行（module <> overview）。
	ListModuleAggScoresByPeriods(ctx context.Context, bounds []PeriodBound) ([]domain.AggregateScore, error)
	// FindLatestFinishedAt assessment_batches 按 period 双界匹配行的 finished_at
	// 最新非空值（trigger_type 不限），无匹配返 (nil, nil)。
	FindLatestFinishedAt(ctx context.Context, start, end int64) (*time.Time, error)
	// FindEarliestPendingSuggestBatch tick 拾取扫描：周期批次已终态且满足拾取
	// 条件（specs §5.1.4 规则2 判定 a/b）的 triggered_at 最早一条，无命中返
	// (nil, nil)。生成中建议行所在 period 恒不命中（拾取锁，规则5）。
	FindEarliestPendingSuggestBatch(ctx context.Context, suggestionRows []domain.TeamTrainingSuggestion) (*domain.AssessmentBatch, error)
}

type dashboardQueryRepository struct {
	db *gorm.DB
}

// NewDashboardQueryRepository 返回 DashboardQueryRepository 接口实现。
func NewDashboardQueryRepository(db *gorm.DB) DashboardQueryRepository {
	return &dashboardQueryRepository{db: db}
}

// periodRow 三表 distinct 归并的中间载体。
type periodRow struct {
	PeriodStartAt time.Time
	PeriodEndAt   time.Time
}

// ListPeriods 三表各查 distinct 后内存归并（等值区间跨表去重），排序语义固定
// 新到旧。行数级为全员 × 期数聚合后的期数，量级极小。三模型均无 TableName
// 覆写且无软删字段，表名走 GORM 复数化，用 Model 解析避免硬编码漂移。
func (r *dashboardQueryRepository) ListPeriods(ctx context.Context) ([]PeriodBound, error) {
	models := []any{&domain.ActivityStat{}, &domain.DimensionScore{}, &domain.AggregateScore{}}
	seen := make(map[PeriodBound]struct{})
	var rows []periodRow
	for _, m := range models {
		if err := r.db.WithContext(ctx).
			Model(m).
			Select("DISTINCT period_start_at AS period_start_at, period_end_at AS period_end_at").
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			seen[PeriodBound{StartAt: row.PeriodStartAt, EndAt: row.PeriodEndAt}] = struct{}{}
		}
		rows = rows[:0]
	}
	bounds := make([]PeriodBound, 0, len(seen))
	for b := range seen {
		bounds = append(bounds, b)
	}
	sortPeriodsDesc(bounds)
	return bounds, nil
}

// sortPeriodsDesc 区间新到旧就地排序：start 降序，同 start 按 end 降序。
func sortPeriodsDesc(bounds []PeriodBound) {
	for i := 1; i < len(bounds); i++ {
		for j := i; j > 0; j-- {
			a, b := bounds[j-1], bounds[j]
			if b.StartAt.After(a.StartAt) ||
				(b.StartAt.Equal(a.StartAt) && b.EndAt.After(a.EndAt)) {
				bounds[j-1], bounds[j] = b, a
				continue
			}
			break
		}
	}
}

func (r *dashboardQueryRepository) ListActivityByPeriod(ctx context.Context, start, end int64) ([]domain.ActivityStat, error) {
	var list []domain.ActivityStat
	err := r.db.WithContext(ctx).
		Where("period_start_at = ? AND period_end_at = ?",
			time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Order("token_name ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *dashboardQueryRepository) ListDimScoresByPeriod(ctx context.Context, start, end int64) ([]domain.DimensionScore, error) {
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

// ListDimScoresByPeriods 多区间取行用逐区间 OR（成对 IN 三库兼容性差：SQLite
// 支持行值 IN 但 MySQL 8 前不支持）。窗口至多 8 期，OR 条件数受窗口上限约束。
func (r *dashboardQueryRepository) ListDimScoresByPeriods(ctx context.Context, bounds []PeriodBound) ([]domain.DimensionScore, error) {
	if len(bounds) == 0 {
		return []domain.DimensionScore{}, nil
	}
	query := r.db.WithContext(ctx).
		Where(periodOrConds(bounds), periodOrArgs(bounds)...).
		Order("period_start_at ASC, token_name ASC, dimension_code ASC")
	var list []domain.DimensionScore
	if err := query.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *dashboardQueryRepository) ListModuleAggScoresByPeriods(ctx context.Context, bounds []PeriodBound) ([]domain.AggregateScore, error) {
	if len(bounds) == 0 {
		return []domain.AggregateScore{}, nil
	}
	query := r.db.WithContext(ctx).
		Where("module <> ? AND ("+periodOrConds(bounds)+")", append([]any{domain.ModuleOverview}, periodOrArgs(bounds)...)...).
		Order("period_start_at ASC, token_name ASC, module ASC")
	var list []domain.AggregateScore
	if err := query.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// periodOrConds 多区间 OR 条件占位串（每区间双界等值成对）。
func periodOrConds(bounds []PeriodBound) string {
	conds := "(period_start_at = ? AND period_end_at = ?)"
	for i := 1; i < len(bounds); i++ {
		conds += " OR (period_start_at = ? AND period_end_at = ?)"
	}
	return conds
}

// periodOrArgs 与 periodOrConds 成对的绑定参数（UTC 口径，SQLite 文本列
// 字典序可比前提）。
func periodOrArgs(bounds []PeriodBound) []any {
	args := make([]any, 0, len(bounds)*2)
	for _, b := range bounds {
		args = append(args, b.StartAt.UTC(), b.EndAt.UTC())
	}
	return args
}

// FindLatestFinishedAt 同区间多批次取 finished_at 最新非空行（running 行
// finished_at 为 NULL 天然排除），03 §1.8 口径。
func (r *dashboardQueryRepository) FindLatestFinishedAt(ctx context.Context, start, end int64) (*time.Time, error) {
	var row struct{ FinishedAt *time.Time }
	err := r.db.WithContext(ctx).
		Model(&domain.AssessmentBatch{}).
		Select("finished_at").
		Where("period_start_at = ? AND period_end_at = ? AND finished_at IS NOT NULL",
			time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Order("finished_at DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return row.FinishedAt, nil
}

// FindEarliestPendingSuggestBatch 扫描周期批次已终态者，逐 period 套拾取判定：
// 无建议行即命中；有行时须存在比建议行所属批次新的同 period success 重跑批次。
// 批次新旧与命中排序统一按 triggered_at（specs 03 §4.1 步1），终态顺序约束
// 保证 triggered_at 更晚者 finished_at 亦更晚。终态命中集合一次查询圈定，
// 逐 period 判定在内存做（建议行与批次行数级均为周频，量级极小）。
func (r *dashboardQueryRepository) FindEarliestPendingSuggestBatch(ctx context.Context, suggestionRows []domain.TeamTrainingSuggestion) (*domain.AssessmentBatch, error) {
	var batches []domain.AssessmentBatch
	err := r.db.WithContext(ctx).
		Where("trigger_type = ? AND status IN ?",
			domain.BatchTriggerScheduled,
			[]string{domain.BatchStatusSuccess, domain.BatchStatusPartialFailed, domain.BatchStatusFailed}).
		Order("triggered_at ASC").
		Find(&batches).Error
	if err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return nil, nil
	}

	for i := range batches {
		if pendingSuggestBatch(&batches[i], batches, suggestionRows) {
			return &batches[i], nil
		}
	}
	return nil, nil
}

// pendingSuggestBatch 判定批次 batch 是否满足拾取条件（specs §5.1.4 规则2）：
// a) 同 period 无建议行；b) 建议行存在且 batch 自身是比其所属批次新的 success
// 重跑批次（所属批次已产出建议不重复命中，partial_failed/failed 重跑不覆盖）。
// 建议行为 generating（拾取锁）时 a/b 均不成立。
func pendingSuggestBatch(batch *domain.AssessmentBatch, batches []domain.AssessmentBatch, suggestionRows []domain.TeamTrainingSuggestion) bool {
	var suggestion *domain.TeamTrainingSuggestion
	for i := range suggestionRows {
		if suggestionRows[i].PeriodStartAt.Equal(batch.PeriodStartAt) &&
			suggestionRows[i].PeriodEndAt.Equal(batch.PeriodEndAt) {
			suggestion = &suggestionRows[i]
			break
		}
	}
	if suggestion == nil {
		return true // 判定 a：该 period 无建议行
	}
	if suggestion.Status == domain.SuggestionStatusGenerating {
		return false // 生成中行即拾取锁，行存在期间扫描不命中
	}
	// 判定 b：建议行所属批次按 batch_no 冗余对位（04 §3.1），新旧按
	// triggered_at（03 §4.1 步1）；所属批次不在终态命中集合（如 running）
	// 时不判重跑，待其终态后由后续 tick 处置。
	ownerTriggeredAt := time.Time{}
	for i := range batches {
		b := &batches[i]
		if b.PeriodStartAt.Equal(batch.PeriodStartAt) && b.PeriodEndAt.Equal(batch.PeriodEndAt) &&
			b.BatchNo == suggestion.BatchNo {
			ownerTriggeredAt = b.TriggeredAt
			break
		}
	}
	if ownerTriggeredAt.IsZero() {
		return false
	}
	return batch.Status == domain.BatchStatusSuccess && batch.TriggeredAt.After(ownerTriggeredAt)
}
