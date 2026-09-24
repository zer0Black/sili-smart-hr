// Package task 承载 Asynq 任务处理器。
package task

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

// TypeHealthCheck 是 no-op 健康任务的类型名。
const TypeHealthCheck = "health:check"

// 队列名常量单点定义（防互饿分流）：投递侧（pipeline 入队）与消费侧（worker
// server Queues 注册）共同引用，双侧改名只动此处。default 承载 tick 等分钟级
// 任务保最低延迟，batch 承载 batch-run 编排，extract 承载会话抽取洪峰。
const (
	QueueDefault = "default"
	QueueBatch   = "batch"
	QueueExtract = "extract"
)

// NewMux 构造 worker 任务路由：健康任务原地注册，抽取、评估、批次与生成任务经
// 参数注入（保持单一注册入口）。session-extract / person-evaluate / batch-tick /
// batch-run / questionbank:generate 均以 context.WithTimeout 挂任务级超时
// （asynq v0.26.0 ServeMux 无 options 注册 API），预算声明见各自任务文件的
// timeout 常量。
func NewMux(sessionExtract, personEvaluate, batchTick, batchRun, questionGenerate asynq.HandlerFunc) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeHealthCheck, HandleHealthCheck)
	mux.Handle(TypeSessionExtract, withTimeout(sessionExtract, sessionExtractTimeout))
	mux.Handle(TypePersonEvaluate, withTimeout(personEvaluate, personEvaluateTimeout))
	mux.Handle(TypeBatchTick, withTimeout(batchTick, batchTickTimeout))
	mux.Handle(TypeBatchRun, withTimeout(batchRun, batchRunTimeout))
	mux.Handle(TypeQuestionGenerate, withTimeout(questionGenerate, questionGenerateTimeout))
	return mux
}

// withTimeout 给 Handler 挂任务级 deadline：父 ctx 自带更早 deadline 时取更早者。
func withTimeout(h asynq.Handler, d time.Duration) asynq.Handler {
	return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return h.ProcessTask(ctx, t)
	})
}

// HandleHealthCheck 是 no-op 处理器：写一条 slog 证明 worker 与 scheduler 在跑。
func HandleHealthCheck(_ context.Context, t *asynq.Task) error {
	slog.Info("asynq health task tick", "type", t.Type(), "payload_size", len(t.Payload()))
	return nil
}
