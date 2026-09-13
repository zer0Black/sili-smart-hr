// Package fallback 是失败重试与降级子域：固定次数重试（Retry）、失败人数占比
// 超阈判定（BelowAlertThreshold）与告警信号写入（AlertWriter），specs §5.3。
package fallback
