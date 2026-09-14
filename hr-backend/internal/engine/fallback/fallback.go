package fallback

import (
	"context"
	"math"
	"time"
)

// BatchAlertThreshold 失败人数占比告警阈值，百分比口径 0-100（03 §4.7）。
const BatchAlertThreshold = 10.00

// retryShiftCap 移位安全上限：baseDelay 左移超出 int64 位宽会归零，
// 退避退化为零间隔紧密重试（worker/server.retryDelay 同款钳位）。
const retryShiftCap = 28

// Retry 通用重试器：固定次数、基准倍增退避；ctx 由调用方传入并原样透传给 fn，
// 需要逐次独立预算时由 fn 内部自行派生子 ctx。
// maxAttempts 含首次（如 4 = 首次 + 3 重试）。父 ctx 取消即终止返回 ctx.Err()。
// 全部尝试失败返回最后一次错误。attempt 1 失败后不等待（首次立即执行）。
func Retry(ctx context.Context, fn func(ctx context.Context) error, maxAttempts int, baseDelay time.Duration) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// 退避曲线 baseDelay << (attempt-2)：第 2 次等 1×base，第 3 次等 2×base。
			shift := attempt - 2
			if shift > retryShiftCap {
				shift = retryShiftCap
			}
			timer := time.NewTimer(baseDelay << shift)
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

// FailedRatioPercent 失败占比百分比纯函数（两位小数舍入），包内唯一口径，
// 供告警判定与告警落库消费。totalCount<=0 返 0（无零除路径）。
func FailedRatioPercent(failedCount, totalCount int) float64 {
	if totalCount <= 0 {
		return 0
	}
	return round2(float64(failedCount) / float64(totalCount) * 100)
}

// round2 百分比保留两位小数。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// BelowAlertThreshold 告警判定纯函数：失败占比 ≤ 10.00 返回 true（未超阈）。
// totalCount=0 返回 true（防御式，无零除路径）。
func BelowAlertThreshold(failedCount, totalCount int) bool {
	if totalCount <= 0 {
		return true
	}
	return FailedRatioPercent(failedCount, totalCount) <= BatchAlertThreshold
}
