// Package server 封装 Asynq worker server 的构造。
package server

import (
	"time"

	"github.com/hibiken/asynq"
)

// 抽取任务重试策略：LLM 持续慢/持续失败的会话按默认 MaxRetry=25 加四次方
// 指数退避会驻留队列数天，观测窗口持续虚高，此处收紧上限并封顶单次退避。
// MaxRetry 经 config.Asynq 任务级字段尚未承载（入队侧 pipeline 未实现），
// 当前以 RetryDelayFunc 收敛退避面，任务级 MaxRetry 留待 pipeline 入队时配置。
const (
	retryDelayBase = 30 * time.Second
	retryDelayCap  = 10 * time.Minute
	// retryDelayMaxShift 是移位安全上限：30s≈2^35 纳秒，左移 28 位内不溢出
	// int64；超出点先在此钳位，反正对应时长早已超封顶值。
	retryDelayMaxShift = 28
)

// retryDelay 计算第 n 次重试的退避时长。asynq 以自增前的 msg.Retried 调用
// （首次失败 n=0），先钳位再封指数后移位：负移位会 panic 击穿 worker
// goroutine（recover 只包 handler 层），高次移位溢出为负会绕过封顶判断。
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
// 编排）> extract（会话抽取洪峰）。batch-run 的等待屏障靠 extract 持续被消费推进，
// 同池混跑时洪峰会饿死对方，分流后各队列消费互不挤占。
func NewServer(opt asynq.RedisConnOpt, concurrency int) *asynq.Server {
	if concurrency <= 0 {
		concurrency = 10
	}
	return asynq.NewServer(opt, asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			"default": 6,
			"batch":   2,
			"extract": 2,
		},
		RetryDelayFunc: func(n int, _ error, _ *asynq.Task) time.Duration {
			return retryDelay(n)
		},
	})
}
