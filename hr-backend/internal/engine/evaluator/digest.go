package evaluator

import (
	"context"
	"fmt"

	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/repository"
)

// DimensionSpecReader 评分维度口径读取窄接口（03 §5.2），DimensionRepository
// 经装配层适配满足（ListEnabledFullByDataSource 组装为 []DimensionSpec）。
type DimensionSpecReader interface {
	// ListEnabledConversationSpecs 返回启用且 data_source=CONVERSATION 的维度
	// 全字段（含 prompt/anchor），Module 取 module_code 原值。
	ListEnabledConversationSpecs(ctx context.Context) ([]DimensionSpec, error)
}

// loadSpecs 读维度口径快照：读取失败 wrap ErrDimensionConfigRead；空集返回
// ErrNoDimensions（specs §2.3：配置前置问题，不落评分行，error 上抛交任务重试）。
func loadSpecs(ctx context.Context, r DimensionSpecReader) ([]DimensionSpec, error) {
	specs, err := r.ListEnabledConversationSpecs(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDimensionConfigRead, err)
	}
	if len(specs) == 0 {
		return nil, ErrNoDimensions
	}
	return specs, nil
}

// fetchDigests 档案取数归一（specs §2.4 能力1 流程段）：ListByPersonAndRange
// start 前移 24h 缓冲取回三态行（上游按 first_turn_at 过滤的既定契约，容纳
// first_turn 在前周期、last_turn 落本周期的跨边界行）→ ParseDigests → 内存过滤
// last_turn ∈ [Start, End) 归一到本周期，与 activity 侧同口径。读取失败 wrap
// ErrProfileRead。
func fetchDigests(ctx context.Context, repo repository.SessionFeatureRepository, tokenName string, period activity.Period) ([]activity.ProfileDigest, error) {
	rows, err := repo.ListByPersonAndRange(ctx, tokenName, period.Start-24*3600, period.End)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfileRead, err)
	}
	all := activity.ParseDigests(rows)
	filtered := make([]activity.ProfileDigest, 0, len(all))
	for _, d := range all {
		last := d.LastTurn.UTC().Unix()
		if last >= period.Start && last < period.End {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}
