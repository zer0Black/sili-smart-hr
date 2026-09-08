package evaluator

import (
	"context"
)

// DimensionSpecReader 评分维度口径读取窄接口（03 §5.2），DimensionRepository
// 经装配层适配满足（ListEnabledFullByDataSource 组装为 []DimensionSpec）。
type DimensionSpecReader interface {
	// ListEnabledConversationSpecs 返回启用且 data_source=CONVERSATION 的维度
	// 全字段（含 prompt/anchor），Module 取 module_code 原值。
	ListEnabledConversationSpecs(ctx context.Context) ([]DimensionSpec, error)
}

// loadSpecs 读维度口径快照：适配层已 wrap ErrDimensionConfigRead；空集返回
// ErrNoDimensions（specs §2.3：配置前置问题，不落评分行，error 上抛交任务重试）。
func loadSpecs(ctx context.Context, r DimensionSpecReader) ([]DimensionSpec, error) {
	specs, err := r.ListEnabledConversationSpecs(ctx)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, ErrNoDimensions
	}
	return specs, nil
}
