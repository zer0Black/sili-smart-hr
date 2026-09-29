package task

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// TypeTestExpireTick 逾期判定任务类型，每分钟触发（specs §5.3.1、03 §4.1）。
const TypeTestExpireTick = "assessment:test-expire-tick"

// testExpireTickTimeout 任务级超时 60s（specs §5.3 与 03 §4.3）：扫描与推进为
// 纯 DB 读写，与 batch-tick 同量级。
const testExpireTickTimeout = 60 * time.Second

// TestExpireRunner tick 行为注入面（*repository.AssessmentTestTaskRepository
// 鸭子满足，语义为调用仓储 ExpirePending）。
type TestExpireRunner interface {
	ExpirePending(ctx context.Context, now time.Time) (int64, error)
}

// NewTestExpireTickHandler 构造逾期 tick handler：无 payload，调
// ExpirePending(ctx, time.Now().UTC())（比较口径与 expires_at 落库 UTC 一致），
// 推进条数记 INFO，err 透传交 Asynq 任务级重试（specs §5.3.5：下个 tick 自然补偿）。
func NewTestExpireTickHandler(runner TestExpireRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		n, err := runner.ExpirePending(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Info("test expire tick advanced", "count", n)
		}
		return nil
	}
}
