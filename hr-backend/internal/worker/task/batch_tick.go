package task

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
)

// TypeBatchTick 周期判定任务类型，每分钟触发（03 §4.1）。
// pipeline 包投递侧如需该常量须重复声明（禁循环 import），改动双侧同步。
const TypeBatchTick = "engine:batch-tick"

// batchTickTimeout 任务级超时 60s（03 §4.3）：判定与建批为 DB 读写，
// 名单为上游一次全量拉取，错误即上抛重试，无需更长预算。
const batchTickTimeout = 60 * time.Second

// BatchTickRunner tick 行为注入面（*pipeline.Orchestrator 鸭子满足）。
type BatchTickRunner interface {
	TickTrigger(ctx context.Context, now time.Time) error
}

// NewBatchTickHandler 构造周期判定任务 handler（mux 注册归 T5）。
// 未命中与同源阻塞两种无操作路径 err==nil 不重试；配置读取/名单拉取/
// 建批落库/入队四类错误 err 非 nil 交 Asynq 任务级重试（03 §4.3 技术修正）。
func NewBatchTickHandler(runner BatchTickRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		return runner.TickTrigger(ctx, time.Now())
	}
}
