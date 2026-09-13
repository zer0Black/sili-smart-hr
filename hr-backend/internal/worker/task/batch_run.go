package task

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/hibiken/asynq"
)

// TypeBatchRun 批次编排任务类型，payload 携批次主键（03 §4.1）。
// pipeline 包投递侧重复声明同值常量（禁循环 import），改动须双侧同步。
const TypeBatchRun = "engine:batch-run"

// batchRunTimeout 任务级超时单点声明（03 §4.4 推导）：4 路并发百人量级单人
// 最坏 4200s 时约 29.2h 超出该值，超出部分由超时切断，批次停留 running 按停滞
// 处置。兜底值与日周期停滞边界对齐，非运行期预期值。
const batchRunTimeout = 24 * time.Hour

// BatchRunPayload 任务载荷：batch_id 为雪花 ID 十进制字符串（03 §4.2）。
type BatchRunPayload struct {
	BatchID string `json:"batch_id"`
}

// BatchRunRunner 批次编排注入面（*pipeline.Orchestrator 鸭子满足）。
type BatchRunRunner interface {
	RunBatch(ctx context.Context, batchID int64) error
}

// NewBatchRunHandler 构造批次编排任务 handler（mux 注册由 NewMux 统一，归 T5）。
// 坏格式/非正数 batch_id 属构造侧确定性错误，丢弃任务记 ERROR；
// err==nil 含整批失败终态不重试，err 非 nil 透传交 Asynq 重试（03 §4.4）。
func NewBatchRunHandler(runner BatchRunRunner) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p BatchRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			slog.Error("batch run payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		batchID, err := strconv.ParseInt(p.BatchID, 10, 64)
		if err != nil || batchID <= 0 {
			slog.Error("batch run payload batch_id invalid, discard task",
				"batch_id", p.BatchID, "err", err)
			return nil
		}
		return runner.RunBatch(ctx, batchID)
	}
}
