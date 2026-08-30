package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// retryCfg 构造紧凑时间参数的测试配置，避免真实 30s 退避。
func retryCfg(maxRetries int, initial, max time.Duration, statuses []int) Config {
	return Config{
		MaxRetries:     maxRetries,
		InitialBackoff: initial,
		MaxBackoff:     max,
		RetryOnStatus:  statuses,
	}
}

func TestRetry429Recover(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, []int{429, 500, 502, 503, 504})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		if calls <= 3 {
			return &httpError{status: 429}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 4 {
		t.Fatalf("want 4 calls, got %d", calls)
	}
}

func TestRetry429Exhausted(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, []int{429, 500, 502, 503, 504})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 429}
	})
	he, ok := err.(*httpError)
	if !ok {
		t.Fatalf("want *httpError, got %T(%v)", err, err)
	}
	if he.status != 429 {
		t.Fatalf("want status 429, got %d", he.status)
	}
	if calls != 4 {
		t.Fatalf("want 4 calls, got %d", calls)
	}
}

func TestRetryNoRetry401(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, []int{429, 500, 502, 503, 504})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 401}
	})
	he, ok := err.(*httpError)
	if !ok {
		t.Fatalf("want *httpError, got %T(%v)", err, err)
	}
	if he.status != 401 {
		t.Fatalf("want status 401, got %d", he.status)
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

func TestRetry5xxExhausted(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, []int{429, 500, 502, 503, 504})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 500}
	})
	he, ok := err.(*httpError)
	if !ok {
		t.Fatalf("want *httpError, got %T(%v)", err, err)
	}
	if he.status != 500 {
		t.Fatalf("want status 500, got %d", he.status)
	}
	if calls != 4 {
		t.Fatalf("want 4 calls, got %d", calls)
	}
}

func TestRetryRetryAfter(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, time.Second, []int{429, 500, 502, 503, 504})
	start := time.Now()
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		if calls == 1 {
			return &httpError{status: 429, retryAfter: 50 * time.Millisecond}
		}
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 calls, got %d", calls)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("want elapsed >= 50ms, got %v", elapsed)
	}
}

// TestRetryRetryAfterCappedByMaxBackoff 回归：429 Retry-After 超过 MaxBackoff 时
// 封顶到 MaxBackoff，服务商标注的超长等待不击穿调用方的任务级预算。
func TestRetryRetryAfterCappedByMaxBackoff(t *testing.T) {
	cfg := retryCfg(0, time.Millisecond, 30*time.Millisecond, []int{429, 500, 502, 503, 504})
	he := &httpError{status: 429, retryAfter: 5 * time.Minute}
	if got := backoffDuration(he, cfg, 0); got != 30*time.Millisecond {
		t.Fatalf("backoffDuration = %v, want MaxBackoff 30ms（封顶）", got)
	}
	// 标注值在上限内时照用。
	he.retryAfter = 20 * time.Millisecond
	if got := backoffDuration(he, cfg, 0); got != 20*time.Millisecond {
		t.Fatalf("backoffDuration = %v, want 20ms（上限内照用）", got)
	}
}

// TestRetryRetryAfterMaxRetryAfterKnob 三形态：未配 MaxRetryAfter 回退 MaxBackoff；
// 显式配更小值时按小值封顶；显式配更大值时标注值在放宽数值内照用。
func TestRetryRetryAfterMaxRetryAfterKnob(t *testing.T) {
	statuses := []int{429, 500, 502, 503, 504}
	base := retryCfg(0, time.Millisecond, 30*time.Second, statuses)

	// 形态一：MaxRetryAfter 零值回退 MaxBackoff（兼容默认装配）。
	if got := backoffDuration(&httpError{status: 429, retryAfter: 5 * time.Minute}, base, 0); got != 30*time.Second {
		t.Fatalf("零值 MaxRetryAfter 应回退 MaxBackoff: %v", got)
	}

	// 形态二：显式配 MaxRetryAfter 更小，标注超小值封顶到小值。
	small := base
	small.MaxRetryAfter = 10 * time.Second
	if got := backoffDuration(&httpError{status: 429, retryAfter: 25 * time.Second}, small, 0); got != 10*time.Second {
		t.Fatalf("MaxRetryAfter=10s 应封顶 25s 标注: %v", got)
	}
	// 配小值后上限内的标注仍照用。
	if got := backoffDuration(&httpError{status: 429, retryAfter: 8 * time.Second}, small, 0); got != 8*time.Second {
		t.Fatalf("8s 标注在 10s 上限内应照用: %v", got)
	}

	// 形态三：显式配 MaxRetryAfter 更大，超过 MaxBackoff 的标注在新上限内照用。
	large := base
	large.MaxRetryAfter = 2 * time.Minute
	if got := backoffDuration(&httpError{status: 429, retryAfter: 90 * time.Second}, large, 0); got != 90*time.Second {
		t.Fatalf("MaxRetryAfter=2min 时 90s 标注应照用: %v", got)
	}
	if got := backoffDuration(&httpError{status: 429, retryAfter: 5 * time.Minute}, large, 0); got != 2*time.Minute {
		t.Fatalf("MaxRetryAfter=2min 应封顶 5min 标注: %v", got)
	}
}

func TestRetryCtxCancel(t *testing.T) {
	calls := 0
	cfg := retryCfg(10, time.Second, 2*time.Second, []int{429, 500, 502, 503, 504})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := retryWithBackoff(ctx, cfg, func() error {
		calls++
		return &httpError{status: 429}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRetryNotRetryableTimeoutValue(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, []int{429, 500, 502, 503, 504})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{timeout: true}
	})
	he, ok := err.(*httpError)
	if !ok {
		t.Fatalf("want *httpError, got %T(%v)", err, err)
	}
	if !he.timeout {
		t.Fatal("want timeout == true")
	}
	if calls != 4 {
		t.Fatalf("want 4 calls, got %d", calls)
	}
}

// --- 边界与异常分支 ---

// 非 *httpError 错误透传，不重试：重试判定只针对内部协议错误。
func TestRetryPassThroughNonHTTPError(t *testing.T) {
	calls := 0
	sentinel := errors.New("boom")
	cfg := retryCfg(3, time.Millisecond, 10*time.Millisecond, nil)
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

// RetryOnStatus 为空时回落默认 [429,500,502,503,504]（specs §2.2）。
func TestRetryDefaultStatuses(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		calls := 0
		cfg := retryCfg(2, time.Millisecond, 5*time.Millisecond, nil)
		err := retryWithBackoff(context.Background(), cfg, func() error {
			calls++
			return &httpError{status: status}
		})
		he, ok := err.(*httpError)
		if !ok || he.status != status {
			t.Fatalf("status %d: want *httpError{status:%d}, got %T(%v)", status, status, err, err)
		}
		if calls != 3 {
			t.Fatalf("status %d: want 3 calls, got %d", status, calls)
		}
	}
	// 不在默认集内的 401 仍不重试。
	calls := 0
	cfg := retryCfg(2, time.Millisecond, 5*time.Millisecond, nil)
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 401}
	})
	if calls != 1 {
		t.Fatalf("want 1 call for 401, got %d", calls)
	}
	if he, ok := err.(*httpError); !ok || he.status != 401 {
		t.Fatalf("want *httpError{status:401}, got %T(%v)", err, err)
	}
}

// 退避倍增且封顶 MaxBackoff：InitialBackoff=1ms, MaxBackoff=2ms，
// 三次等待依次 1ms、2ms、2ms，总耗时 >= 5ms（≥ 下界防 flaky）。
func TestRetryBackoffCap(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Millisecond, 2*time.Millisecond, []int{429})
	start := time.Now()
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 429}
	})
	elapsed := time.Since(start)
	if he, ok := err.(*httpError); !ok || he.status != 429 {
		t.Fatalf("want *httpError{status:429}, got %T(%v)", err, err)
	}
	if calls != 4 {
		t.Fatalf("want 4 calls, got %d", calls)
	}
	if elapsed < 5*time.Millisecond {
		t.Fatalf("want elapsed >= 5ms (1+2+2), got %v", elapsed)
	}
}

// 429 无 Retry-After 头（retryAfter=0）走指数退避而非 0 等待。
func TestRetry429NoRetryAfterUsesBackoff(t *testing.T) {
	calls := 0
	cfg := retryCfg(1, 20*time.Millisecond, time.Second, []int{429})
	start := time.Now()
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		if calls == 1 {
			return &httpError{status: 429, retryAfter: 0}
		}
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("want elapsed >= 20ms backoff, got %v", elapsed)
	}
}

// MaxRetries=0：仅初试，失败即返，不进入重试循环。
func TestRetryZeroMaxRetries(t *testing.T) {
	calls := 0
	cfg := retryCfg(0, time.Millisecond, 10*time.Millisecond, []int{429})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return &httpError{status: 429}
	})
	if he, ok := err.(*httpError); !ok || he.status != 429 {
		t.Fatalf("want *httpError{status:429}, got %T(%v)", err, err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

// ctx 已在等待期间取消时返回 ctx.Err()（DeadlineExceeded 变体）。
func TestRetryCtxDeadline(t *testing.T) {
	cfg := retryCfg(5, time.Second, 2*time.Second, []int{429})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := retryWithBackoff(ctx, cfg, func() error {
		return &httpError{status: 429}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
}

// fn 首次即成功：零重试零等待。
func TestRetryImmediateSuccess(t *testing.T) {
	calls := 0
	cfg := retryCfg(3, time.Second, 2*time.Second, []int{429})
	err := retryWithBackoff(context.Background(), cfg, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}
