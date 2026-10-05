// TeamTrainingSuggestionRepository 承载建议行状态机的建行与终态写
//（specs P2_TMD_001 §5.1.2 步2、§5.1.4 规则2/5）。
package repository

import (
	"context"
	"errors"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// TeamTrainingSuggestionRepository 是建议行的数据访问接口。
type TeamTrainingSuggestionRepository interface {
	// EnsureGenerating tick 幂等建行/重置续作：period 无行 INSERT 生成中行返 true；
	// 命中终态行（generated/failed）重置为 generating 续作（batch_no 更新、内容列
	// 置空、generated_at 置 NULL）返 true；generating 行返 false（沿用拾取锁）。
	EnsureGenerating(ctx context.Context, row domain.TeamTrainingSuggestion) (bool, error)
	// FindLatest 按 period 起止新到旧取第一条（specs §4.1.4 规则6：排序键为
	// period 而非生成时间），无行返 (nil, nil)。
	FindLatest(ctx context.Context) (*domain.TeamTrainingSuggestion, error)
	// FindAll 全量建议行，tick 拾取判定的建议行输入（行数级为同期数）。
	FindAll(ctx context.Context) ([]domain.TeamTrainingSuggestion, error)
	// GetByPeriod 按 period 双界 Unix 秒取行，无行返 (nil, nil)。
	GetByPeriod(ctx context.Context, periodStart, periodEnd int64) (*domain.TeamTrainingSuggestion, error)
	// MarkGenerated 落 generated 终态（WHERE status='generating' 守卫，
	// affected=0 幂等返回 nil）；prompt_version 落 suggestgen.PromptVersion
	//（evaluator 同模式，04 §3.1）。
	MarkGenerated(ctx context.Context, id int64, batchNo string, modulesJSON string, summary string, modelName string, promptVersion string, generatedAt time.Time) error
	// MarkFailed 落 failed 终态与原因（同款 generating 守卫，affected=0 幂等返回 nil）。
	MarkFailed(ctx context.Context, id int64, errSummary string) error
}

type teamTrainingSuggestionRepository struct {
	db *gorm.DB
}

// NewTeamTrainingSuggestionRepository 返回 TeamTrainingSuggestionRepository 接口实现。
func NewTeamTrainingSuggestionRepository(db *gorm.DB) TeamTrainingSuggestionRepository {
	return &teamTrainingSuggestionRepository{db: db}
}

func (r *teamTrainingSuggestionRepository) EnsureGenerating(ctx context.Context, row domain.TeamTrainingSuggestion) (bool, error) {
	existing, err := r.findByPeriodAt(ctx, row.PeriodStartAt, row.PeriodEndAt)
	if err != nil {
		return false, err
	}
	if existing == nil {
		if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
			// 并发双写撞 uk_suggestion_period：重读行按既有行分支收敛
			//（UpsertByBatch 同范式，唯一索引兜底）。
			if !dberr.UniqueViolation(err) {
				return false, err
			}
			existing, err = r.findByPeriodAt(ctx, row.PeriodStartAt, row.PeriodEndAt)
			if err != nil {
				return false, err
			}
			if existing == nil {
				return false, nil // 撞键行已被并发删除：下个 tick 重建
			}
		} else {
			return true, nil
		}
	}
	if existing.Status == domain.SuggestionStatusGenerating {
		return false, nil // 生成中行即拾取锁，沿用该行续作
	}
	// 终态行重置为 generating 续作：status IN 终态集守卫拦下并发竞态，
	// affected=0 视为已被并发重置，幂等返 false。
	res := r.db.WithContext(ctx).Model(&domain.TeamTrainingSuggestion{}).
		Where("period_start_at = ? AND period_end_at = ? AND status IN ?",
			row.PeriodStartAt, row.PeriodEndAt,
			[]string{domain.SuggestionStatusGenerated, domain.SuggestionStatusFailed}).
		Updates(map[string]any{
			"status":         domain.SuggestionStatusGenerating,
			"batch_no":       row.BatchNo,
			"modules_json":   "",
			"summary":        "",
			"model_name":     "",
			"prompt_version": "",
			"error_summary":  "",
			"generated_at":   nil,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// findByPeriodAt 按 period 双界点查（time.Time 入参，秒级 Unix 转换由调用方
// 完成），无行返 (nil, nil)。
func (r *teamTrainingSuggestionRepository) findByPeriodAt(ctx context.Context, start, end time.Time) (*domain.TeamTrainingSuggestion, error) {
	var row domain.TeamTrainingSuggestion
	err := r.db.WithContext(ctx).
		Where("period_start_at = ? AND period_end_at = ?", start, end).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// FindLatest period 起止新到旧第一条（补生成场景下旧区间行生成时间晚于
// 新区间行，仍按 period 取，specs §4.1.4 规则6）。
func (r *teamTrainingSuggestionRepository) FindLatest(ctx context.Context) (*domain.TeamTrainingSuggestion, error) {
	var row domain.TeamTrainingSuggestion
	err := r.db.WithContext(ctx).
		Order("period_start_at DESC, period_end_at DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *teamTrainingSuggestionRepository) FindAll(ctx context.Context) ([]domain.TeamTrainingSuggestion, error) {
	var list []domain.TeamTrainingSuggestion
	err := r.db.WithContext(ctx).
		Order("period_start_at DESC, period_end_at DESC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *teamTrainingSuggestionRepository) GetByPeriod(ctx context.Context, periodStart, periodEnd int64) (*domain.TeamTrainingSuggestion, error) {
	return r.findByPeriodAt(ctx, time.Unix(periodStart, 0).UTC(), time.Unix(periodEnd, 0).UTC())
}

func (r *teamTrainingSuggestionRepository) MarkGenerated(ctx context.Context, id int64, batchNo string, modulesJSON string, summary string, modelName string, promptVersion string, generatedAt time.Time) error {
	res := r.db.WithContext(ctx).Model(&domain.TeamTrainingSuggestion{}).
		Where("id = ? AND status = ?", id, domain.SuggestionStatusGenerating).
		Updates(map[string]any{
			"status":         domain.SuggestionStatusGenerated,
			"batch_no":       batchNo,
			"modules_json":   modulesJSON,
			"summary":        summary,
			"model_name":     modelName,
			"prompt_version": promptVersion,
			"error_summary":  "",
			"generated_at":   generatedAt,
		})
	return res.Error
}

func (r *teamTrainingSuggestionRepository) MarkFailed(ctx context.Context, id int64, errSummary string) error {
	res := r.db.WithContext(ctx).Model(&domain.TeamTrainingSuggestion{}).
		Where("id = ? AND status = ?", id, domain.SuggestionStatusGenerating).
		Updates(map[string]any{
			"status":        domain.SuggestionStatusFailed,
			"error_summary": errSummary,
		})
	return res.Error
}
