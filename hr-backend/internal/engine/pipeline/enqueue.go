// enqueue.go Asynq 投递适配器：batch-run 与 session-extract 两类任务的窄接口实现。
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// AsynqEnqueuer Asynq 双任务投递适配器（满足 BatchEnqueuer 与 SessionEnqueuer）。
type AsynqEnqueuer struct {
	client *asynq.Client
}

// NewAsynqEnqueuer 组装投递适配器。
func NewAsynqEnqueuer(client *asynq.Client) *AsynqEnqueuer {
	return &AsynqEnqueuer{client: client}
}

// batchRunPayload batch-run 任务载荷（03 §4.2：雪花 ID 十进制字符串）。
type batchRunPayload struct {
	BatchID string `json:"batch_id"`
}

// EnqueueBatchRun 投递批次编排任务。
func (e *AsynqEnqueuer) EnqueueBatchRun(ctx context.Context, batchID int64) error {
	payload, err := json.Marshal(batchRunPayload{BatchID: fmt.Sprintf("%d", batchID)})
	if err != nil {
		return fmt.Errorf("pipeline: batch-run payload 序列化: %w", err)
	}
	if _, err := e.client.EnqueueContext(ctx, asynq.NewTask(typeBatchRun, payload)); err != nil {
		return fmt.Errorf("pipeline: batch-run 投递: %w", err)
	}
	return nil
}

// sessionExtractPayload 抽取任务载荷（03 §3.2 同构）。
type sessionExtractPayload struct {
	SessionKey string `json:"session_key"`
	TokenName  string `json:"token_name"`
}

// EnqueueSessionExtract 投递单会话抽取任务（一会话一任务，specs §5.2.2 步骤3）。
func (e *AsynqEnqueuer) EnqueueSessionExtract(ctx context.Context, sessionKey, tokenName string) error {
	payload, err := json.Marshal(sessionExtractPayload{SessionKey: sessionKey, TokenName: tokenName})
	if err != nil {
		return fmt.Errorf("pipeline: session-extract payload 序列化: %w", err)
	}
	if _, err := e.client.EnqueueContext(ctx, asynq.NewTask(typeSessionExtract, payload)); err != nil {
		return fmt.Errorf("pipeline: session-extract 投递: %w", err)
	}
	return nil
}
