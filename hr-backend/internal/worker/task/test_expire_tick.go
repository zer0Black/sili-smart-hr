package task

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/fallback"
)

// TypeTestExpireTick 逾期判定任务类型，每分钟触发（specs §5.3.1、03 §4.1）。
const TypeTestExpireTick = "assessment:test-expire-tick"

// testExpireTickTimeout 任务级超时 60s（specs §5.3 与 03 §4.3）：扫描与推进为
// 纯 DB 读写，与 batch-tick 同量级。
const testExpireTickTimeout = 60 * time.Second

// TestExpireRunner tick 行为注入面（*repository.AssessmentTestTaskRepository
// 鸭子满足，语义为调用仓储 ExpirePending，返回推进任务的 TaskNo 清单）。
type TestExpireRunner interface {
	ExpirePending(ctx context.Context, now time.Time) ([]string, error)
}

// NewTestExpireTickHandler 构造逾期 tick handler：无 payload，调
// ExpirePending(ctx, time.Now().UTC())（比较口径与 expires_at 落库 UTC 一致），
// 推进条数记 INFO，err 透传交 Asynq 任务级重试（specs §5.3.5：下个 tick 自然补偿）。
// opLogger 逐任务记逾期取消节点（specs P4_LOG_001 §5.2 任务号口径，nil 安全）。
func NewTestExpireTickHandler(runner TestExpireRunner, opLogger fallback.PendingLogRecorder) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		nos, err := runner.ExpirePending(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(nos) > 0 {
			slog.Info("test expire tick advanced", "count", len(nos))
		}
		if opLogger == nil {
			return nil
		}
		for _, no := range nos {
			opLogger.Record(fallback.PendingLog{
				FallbackName: domain.OpOperatorSystem,
				Module:       domain.OpModuleSystemJob,
				Target:       fmt.Sprintf("测试任务 %s", no),
				Summary:      "任务自动逾期取消",
				Result:       domain.OpResultSuccess,
				RequestPath:  TypeTestExpireTick,
			})
		}
		return nil
	}
}
