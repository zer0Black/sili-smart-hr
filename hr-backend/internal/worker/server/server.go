// Package server 封装 Asynq worker server 的构造。
package server

import (
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/worker/task"
)

// 抽取任务重试策略：默认 MaxRetry=25 加四次方指数退避会让持续失败的会话驻留
// 队列数天、观测窗口持续虚高，此处封顶单次退避（入队侧已配 MaxRetry=4）。
const (
	retryDelayBase = 30 * time.Second
	retryDelayCap  = 10 * time.Minute
	// retryDelayMaxShift 移位安全上限：30s≈2^35 纳秒，左移 28 位内不溢出 int64。
	retryDelayMaxShift = 28
)

// retryDelay 第 n 次重试的退避时长。asynq 以自增前的 msg.Retried 调用（首次
// n=0），钳位后再移位：负移位 panic 击穿 worker goroutine（recover 只包 handler
// 层），高次溢出为负会绕过封顶判断。
func retryDelay(n int) time.Duration {
	shift := n - 1
	if shift < 0 {
		shift = 0
	}
	if shift > retryDelayMaxShift {
		shift = retryDelayMaxShift
	}
	d := retryDelayBase << shift
	if d > retryDelayCap {
		return retryDelayCap
	}
	return d
}

// NewServer 构造 Asynq worker server，并发度来自配置。
// 队列按严格优先级分流（防互饿）：default（tick 等分钟级任务）> batch（batch-run
// 编排）> extract（会话抽取洪峰）。StrictPriority 必须显式置 true：asynq 默认
// 是按权重的加权轮询，洪峰期 extract 仍占消费份额，与分流意图不符。
func NewServer(opt asynq.RedisConnOpt, concurrency int) *asynq.Server {
	if concurrency <= 0 {
		concurrency = 10
	}
	return asynq.NewServer(opt, asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			task.QueueDefault: 6,
			task.QueueBatch:   2,
			task.QueueExtract: 2,
		},
		StrictPriority: true,
		RetryDelayFunc: func(n int, _ error, _ *asynq.Task) time.Duration {
			return retryDelay(n)
		},
	})
}
