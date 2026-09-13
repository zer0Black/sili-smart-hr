package fallback

import (
	"context"
	"math"
	"time"
)

// BatchAlertThreshold 失败人数占比告警阈值，百分比口径 0-100（03 §4.7）。
const BatchAlertThreshold = 10.00

// Retry 通用重试器：固定次数、基准倍增退避、每次尝试派生子 ctx。
// maxAttempts 含首次（如 4 = 首次 + 3 重试）。父 ctx 取消即终止返回 ctx.Err()。
// 全部尝试失败返回最后一次错误。attempt 1 失败后不等待（首次立即执行）。
func Retry(ctx context.Context, fn func(ctx context.Context) error, maxAttempts int, baseDelay time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// 退避曲线 baseDelay << (attempt-2)：第 2 次等 1×base，第 3 次等 2×base。
			timer := time.NewTimer(baseDelay << (attempt - 2))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := fn(ctx); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// BelowAlertThreshold 告警判定纯函数：失败占比 ≤ 10.00 返回 true（未超阈）。
// totalCount=0 返回 true（无零除路径，防御式）。百分比口径保留两位小数比较。
func BelowAlertThreshold(failedCount, totalCount int) bool {
	if totalCount <= 0 {
		return true
	}
	ratio := math.Round(float64(failedCount)/float64(totalCount)*10000) / 100
	return ratio <= BatchAlertThreshold
}
