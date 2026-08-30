package llm

import (
	"context"
	"time"
)

// defaultRetryOnStatus specs §2.2 默认重试状态码集。
var defaultRetryOnStatus = []int{429, 500, 502, 503, 504}

// retryWithBackoff 对限流与瞬时错误按指数退避重试（specs §2.4 能力3）。
// fn 返回 nil 即成功；返回 *httpError 时按 cfg 判定是否重试。
// 等待时长：429 且带 Retry-After 优先，否则 InitialBackoff * 2^attempt 封顶 MaxBackoff。
// 重试耗尽返回最后一次 *httpError，由 client 层分类为对应 sentinel。
func retryWithBackoff(ctx context.Context, cfg Config, fn func() error) error {
	statuses := cfg.RetryOnStatus
	if len(statuses) == 0 {
		statuses = defaultRetryOnStatus
	}

	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		he, ok := err.(*httpError)
		if !ok || !isRetryable(he, statuses) {
			return err
		}
		if attempt >= cfg.MaxRetries {
			return err
		}
		if waitErr := sleepContext(ctx, backoffDuration(he, cfg, attempt)); waitErr != nil {
			return waitErr
		}
	}
}

// isRetryable 重试判定：传输超时可重试；status 在重试状态码集内可重试；
// 401、400 等确定性错误不可重试（specs §2.4 能力3 注意事项）。
func isRetryable(he *httpError, statuses []int) bool {
	if he.timeout {
		return true
	}
	for _, s := range statuses {
		if he.status == s {
			return true
		}
	}
	return false
}

// backoffDuration 计算第 attempt 次重试前等待时长：
// 429 且 Retry-After > 0 优先用服务商标注时长，封顶 MaxRetryAfter（<=0 回退
// MaxBackoff 保持兼容；标注值过长会击穿调用方的任务级 ctx 预算，封顶后退化为
// 常规上限等待）；其余按指数退避封顶 MaxBackoff。
func backoffDuration(he *httpError, cfg Config, attempt int) time.Duration {
	if he.status == 429 && he.retryAfter > 0 {
		limit := cfg.MaxRetryAfter
		if limit <= 0 {
			limit = cfg.MaxBackoff
		}
		if he.retryAfter > limit {
			return limit
		}
		return he.retryAfter
	}
	d := cfg.InitialBackoff << attempt
	if d <= 0 || d > cfg.MaxBackoff { // 溢出或超上限封顶
		return cfg.MaxBackoff
	}
	return d
}

// sleepContext 可中断等待，ctx 先结束返回 ctx.Err()。
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
