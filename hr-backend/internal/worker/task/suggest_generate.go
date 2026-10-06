package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
)

// TypeSuggestGenerate 建议生成任务类型（03 §4.2），payload 携批次主键。
const TypeSuggestGenerate = "dashboard:suggest-generate"

// suggestGenerateTimeout 任务级超时 240s（specs §5.1.4 规则5 初值，对齐阅卷
// 链路量级覆盖 LLM 长响应）；建议生成专用 client Timeout 180s 小于本值保
// 重试边界自洽，调整 providers.go NewSuggestGenLLMClient 参数须同步该处。
const suggestGenerateTimeout = 240 * time.Second

// SuggestMaxRetry 生成任务 Asynq 重试基准 3 次（specs §5.1.4 规则3），投递侧
// AsynqSuggestEnqueuer 的 MaxRetry 选项同引本常量保单点。
const SuggestMaxRetry = 3

// SuggestGeneratePayload 任务载荷（03 §4.2）：batch_id 为雪花 ID 十进制字符串。
type SuggestGeneratePayload struct {
	BatchID string `json:"batch_id"`
}

// SuggestGenerateRunner 生成行为注入面（*service.SuggestService 鸭子满足，
// 装配收敛在 app 包）。
type SuggestGenerateRunner interface {
	Generate(ctx context.Context, batchID int64) error
	MarkFailedIfExhausted(ctx context.Context, batchID int64, reason string) error
}

// NewSuggestGenerateHandler 构造建议生成任务 handler（mux 注册归 health.go
// NewMux）。坏格式/非正数 batch_id 丢弃记 ERROR 防毒丸；常规失败透传交 Asynq
// 重试；重试耗尽落 failed 终结（specs §5.1.4 规则3，03 §4.2 末段）。
func NewSuggestGenerateHandler(runner SuggestGenerateRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p SuggestGeneratePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			slog.Error("suggest generate payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		batchID, err := strconv.ParseInt(p.BatchID, 10, 64)
		if err != nil || batchID <= 0 {
			slog.Error("suggest generate payload batch_id invalid, discard task",
				"batch_id", p.BatchID, "err", err)
			return nil
		}

		if err := runner.Generate(ctx, batchID); err != nil {
			retried, _ := retryBudget(ctx)
			if retried < SuggestMaxRetry {
				slog.Error("suggest generate failed, will retry",
					"batch_id", batchID, "err", err)
				return err
			}
			slog.Warn("suggest generate retries exhausted, mark failed",
				"batch_id", batchID)
			if merr := runner.MarkFailedIfExhausted(ctx, batchID, err.Error()); merr != nil {
				return fmt.Errorf("suggest generate mark failed %d: %w", batchID, merr)
			}
		}
		return nil
	}
}
