// enqueue.go Asynq 投递适配器：batch-run 与 session-extract 两类任务的窄接口实现。
// 任务类型常量与 payload 单点定义在 worker/task（消费侧），本包单向 import task
// 引用同一符号（task 不 import pipeline，无循环）。
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/worker/task"
)

// 队列划分（防互饿）：常量收敛在 task.Queue*（消费侧 server.go 同引），
// tick 类分钟级任务走 default 队列保最低延迟。

// AsynqEnqueuer Asynq 双任务投递适配器（满足 BatchEnqueuer 与 SessionEnqueuer）。
type AsynqEnqueuer struct {
	client *asynq.Client
}

// NewAsynqEnqueuer 组装投递适配器。
func NewAsynqEnqueuer(client *asynq.Client) *AsynqEnqueuer {
	return &AsynqEnqueuer{client: client}
}

// EnqueueBatchRun 投递批次编排任务（batch 队列）。
// MaxRetry 收紧为 6：24h 预算长任务默认 25 次重试会在故障期反复重放整批编排。
func (e *AsynqEnqueuer) EnqueueBatchRun(ctx context.Context, batchID int64) error {
	payload, err := json.Marshal(task.BatchRunPayload{BatchID: fmt.Sprintf("%d", batchID)})
	if err != nil {
		return fmt.Errorf("pipeline: batch-run payload 序列化: %w", err)
	}
	if _, err := e.client.EnqueueContext(ctx,
		asynq.NewTask(task.TypeBatchRun, payload),
		asynq.MaxRetry(6),
		asynq.Queue(task.QueueBatch)); err != nil {
		return fmt.Errorf("pipeline: batch-run 投递: %w", err)
	}
	return nil
}

// EnqueueSessionExtract 投递单会话抽取任务（extract 队列，一会话一任务，
// specs §5.2.2 步骤3）。MaxRetry=4 对齐会话级重试语义（30s 基准倍增封顶 10min）。
func (e *AsynqEnqueuer) EnqueueSessionExtract(ctx context.Context, sessionKey, tokenName string) error {
	payload, err := json.Marshal(task.ExtractTaskPayload{SessionKey: sessionKey, TokenName: tokenName})
	if err != nil {
		return fmt.Errorf("pipeline: session-extract payload 序列化: %w", err)
	}
	if _, err := e.client.EnqueueContext(ctx,
		asynq.NewTask(task.TypeSessionExtract, payload),
		asynq.MaxRetry(4),
		asynq.Queue(task.QueueExtract),
		// TaskID 用 session_key 去重：重放撞 ErrTaskIDConflict 视作已在途。
		asynq.TaskID(sessionKey)); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) {
			return nil
		}
		return fmt.Errorf("pipeline: session-extract 投递: %w", err)
	}
	return nil
}
