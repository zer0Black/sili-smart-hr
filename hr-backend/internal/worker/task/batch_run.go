package task

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/engine/pipeline"
)

// TypeBatchRun 批次编排任务类型（payload 携批次主键，03 §4.1）。
const TypeBatchRun = "engine:batch-run"

// batchRunTimeout 任务级超时单点声明（03 §4.4 推导）：4 路并发百人量级单人
// 最坏 4200s 时约 29.2h 超出该值，超出部分由超时切断，批次停留 running 按停滞
// 处置。兜底值与日周期停滞边界对齐，非运行期预期值。
const batchRunTimeout = 24 * time.Hour

// BatchRunTaskPayload 任务载荷：batch_id 为雪花 ID 十进制字符串（03 §4.2）。
type BatchRunTaskPayload struct {
	BatchID string `json:"batch_id"`
}

// NewBatchRunHandler 构造批次编排任务 handler（mux 注册由 NewMux 统一，归 T5）。
// 坏格式/非正数 batch_id 属构造侧确定性错误，丢弃任务记 ERROR。
func NewBatchRunHandler(orch *pipeline.Orchestrator) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p BatchRunTaskPayload
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
		return orch.RunBatch(ctx, batchID)
	}
}
