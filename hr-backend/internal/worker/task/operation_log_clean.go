package task

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// TypeOperationLogClean 操作日志清理任务类型（specs §5.5.1、03 §4.3）：
// 每日 03:00 cron 触发。
const TypeOperationLogClean = "operation-log:clean"

// operationLogCleanTimeout 任务级超时 300s（03 §4.3）：分批删除为纯 DB 写，
// 单批 1000 循环驱动，300s 预算覆盖大存量日清理。
const operationLogCleanTimeout = 300 * time.Second

// OperationLogCleanRunner 清理行为注入面（*service.OperationLogRecorder
// 鸭子满足，装配收敛在 app 包）。
type OperationLogCleanRunner interface {
	CleanExpired(ctx context.Context, now time.Time) (int64, error)
}

// NewOperationLogCleanHandler 构造清理 handler：零 payload（test-expire-tick
// 同款），调 CleanExpired(ctx, time.Now().UTC())，删除条数记 INFO
//（specs §5.5.2 步骤3），err 透传交 Asynq 重试（§5.5.4 规则2 幂等补偿）。
func NewOperationLogCleanHandler(runner OperationLogCleanRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		n, err := runner.CleanExpired(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Info("operation log clean finished", "deleted", n)
		}
		return nil
	}
}
