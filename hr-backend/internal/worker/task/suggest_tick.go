package task

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
)

// TypeSuggestTick 建议生成 tick 任务类型（03 §4.1）：每分钟 cron 触发，
// 只做扫描拾取、幂等建行（生成中行即拾取锁）与投递生成任务。
const TypeSuggestTick = "dashboard:suggest-tick"

// suggestTickTimeout tick 任务级超时 60s（03 §4.1）：扫描建行投递均为轻量
// DB 读写，与 batchTickTimeout 同量级。
const suggestTickTimeout = 60 * time.Second

// SuggestTickRunner tick 行为注入面（*service.SuggestService 鸭子满足，
// 装配收敛在 app 包）。
type SuggestTickRunner interface {
	TickScan(ctx context.Context, now time.Time) error
}

// NewSuggestTickHandler 构造建议 tick 任务 handler（mux 注册归 health.go NewMux）。
// 零 payload 极简（batch_tick 同款）；无命中空转 err==nil，扫描/建行/投递
// 失败 err 非 nil 透传交 Asynq 任务级重试，行保持 generating 由下个 tick
// 沿既有行续作（03 §4.1）。
func NewSuggestTickHandler(runner SuggestTickRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		return runner.TickScan(ctx, time.Now())
	}
}
