package conversationlog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// --- 测试基建 ---

// roundTripFunc 把函数适配为 http.RoundTripper，测试注入替换 c.httpClient.Transport。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// newRetryClient 构造注入 fake Transport 与记录式 sleepFn 的 Client。
// newReqFn 返回标准 GET 请求构造器；sleeps 记录退避序列。
func newRetryClient(t *testing.T, rt http.RoundTripper) (*Client, *[]time.Duration) {
	t.Helper()
	c := NewClient("http://upstream.test")
	c.httpClient.Transport = rt
	var sleeps []time.Duration
	c.sleepFn = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}
	return c, &sleeps
}

// newReqFn 返回指向固定 URL 的请求构造器，模拟调用方 reqFn 契约。
func newReqFn() func(context.Context) (*http.Request, error) {
	return func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.test/api/conversation-log/", nil)
	}
}

// newResp 构造带可关闭 Body 的响应。
func newResp(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(http.NoBody),
		Header:     make(http.Header),
	}
}

// --- 核心断言 ---

// TestDoWithRetryNetworkErrorRecovered 落地 BR1/BR4：传输错误可重试，
// 前两次 conn refused、第三次 200，最终拿到 200 且共 3 次请求。
func TestDoWithRetryNetworkErrorRecovered(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls <= 2 {
			return nil, errors.New("conn refused")
		}
		return newResp(http.StatusOK), nil
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("request calls: got %d want 3", calls)
	}
	if len(*sleeps) != 2 {
		t.Fatalf("backoff count: got %d want 2", len(*sleeps))
	}
}

// TestDoWithRetryExhausted 落地 BR1/BR3/BR4：恒返连接错误，重试耗尽归
// ErrNetwork，共 4 次执行，退避序列 [10s, 20s, 40s]。
func TestDoWithRetryExhausted(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("conn refused")
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err == nil {
		t.Fatal("expect non-nil error, got nil")
	}
	if resp != nil {
		t.Fatalf("expect nil resp on exhausted, got %+v", resp)
	}
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("errors.Is: got %v want ErrNetwork", err)
	}
	if calls != 4 {
		t.Fatalf("request calls: got %d want 4 (1+3 retries)", calls)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second}
	if len(*sleeps) != len(want) {
		t.Fatalf("backoff sequence: got %v want %v", *sleeps, want)
	}
	for i, d := range want {
		if (*sleeps)[i] != d {
			t.Fatalf("backoff[%d]: got %v want %v (full %v)", i, (*sleeps)[i], d, *sleeps)
		}
	}
}

// TestDoWithRetry5xxRetried 落地 BR1：5xx 属可重试，500、500、200 后成功。
func TestDoWithRetry5xxRetried(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch {
		case calls <= 2:
			return newResp(http.StatusInternalServerError), nil
		default:
			return newResp(http.StatusOK), nil
		}
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if calls != 3 {
		t.Fatalf("request calls: got %d want 3", calls)
	}
	if len(*sleeps) != 2 {
		t.Fatalf("backoff count: got %d want 2", len(*sleeps))
	}
}

// TestDoWithRetryDeterministicNoRetry 落地 BR1：非 5xx 响应（401）首轮直通，
// 请求计数 1 且零退避等待，分类交调用方。
func TestDoWithRetryDeterministicNoRetry(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return newResp(http.StatusUnauthorized), nil
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
	if len(*sleeps) != 0 {
		t.Fatalf("sleep calls: got %d want 0", len(*sleeps))
	}
}

// TestDoWithRetryCtxCancel 落地 BR2/BR3：sleepFn 返回 ctx 错误立即中止，
// 剩余重试不再发起，错误链含 context.Canceled。
func TestDoWithRetryCtxCancel(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("conn refused")
	})
	c := NewClient("http://upstream.test")
	c.httpClient.Transport = rt
	c.sleepFn = func(ctx context.Context, d time.Duration) error {
		return context.Canceled
	}

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err == nil {
		t.Fatal("expect non-nil error, got nil")
	}
	if resp != nil {
		t.Fatalf("expect nil resp, got %+v", resp)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is: got %v want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1 (no further retries after cancel)", calls)
	}
}

// --- 边界与异常分支 ---

// TestDoWithRetryFirstAttemptSuccess 首次即成功：零退避零额外请求。
func TestDoWithRetryFirstAttemptSuccess(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return newResp(http.StatusOK), nil
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if calls != 1 || len(*sleeps) != 0 {
		t.Fatalf("calls=%d sleeps=%d, want 1/0", calls, len(*sleeps))
	}
}

// TestDoWithRetry4xxVariantsNoRetry 落地 BR1 分类矩阵：400/403/404 等非 5xx
// 均直通不重试，响应原样交调用方分类。429 例外（瞬时限流走重试，独立用例覆盖）。
func TestDoWithRetry4xxVariantsNoRetry(t *testing.T) {
	for _, status := range []int{400, 403, 404} {
		var calls int
		rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return newResp(status), nil
		})
		c, sleeps := newRetryClient(t, rt)

		resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
		if err != nil {
			t.Fatalf("status %d: unexpected error %v", status, err)
		}
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("status %d: got %d", status, resp.StatusCode)
		}
		if calls != 1 || len(*sleeps) != 0 {
			t.Fatalf("status %d: calls=%d sleeps=%d, want 1/0", status, calls, len(*sleeps))
		}
	}
}

// TestDoWithRetry429Exhausted 429 限流属瞬时错误走重试路径（与 5xx 同款退避），
// 耗尽后归 ErrRateLimited；首轮 429 次轮恢复的形态退避一轮后透传成功。
func TestDoWithRetry429Exhausted(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return newResp(http.StatusTooManyRequests), nil
	})
	c, sleeps := newRetryClient(t, rt)

	_, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err == nil || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("429 恒返应耗尽归 ErrRateLimited, got %v", err)
	}
	if calls != 4 || len(*sleeps) != 3 {
		t.Fatalf("calls=%d sleeps=%d, want 4/3（1+3 重试与 10s/20s/40s 退避）", calls, len(*sleeps))
	}

	var statuses []int
	rt2 := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		statuses = append(statuses, http.StatusTooManyRequests)
		if len(statuses) == 1 {
			return newResp(http.StatusTooManyRequests), nil
		}
		return newResp(200), nil
	})
	c2, sleeps2 := newRetryClient(t, rt2)
	resp, err := c2.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("429 后恢复应透传成功: %v", err)
	}
	resp.Body.Close()
	if len(*sleeps2) != 1 {
		t.Fatalf("sleeps=%d, want 1（一轮退避后成功）", len(*sleeps2))
	}
}

// TestDoWithRetry5xxExhausted 落地 BR1/BR4：5xx 恒返时重试耗尽归 ErrNetwork，
// 4 次执行 + [10s,20s,40s] 退避，与传输错误耗尽同路径。
func TestDoWithRetry5xxExhausted(t *testing.T) {
	var calls int
	var lastStatus int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		lastStatus = http.StatusServiceUnavailable
		return newResp(http.StatusServiceUnavailable), nil
	})
	c, sleeps := newRetryClient(t, rt)

	_, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("errors.Is: got %v want ErrNetwork", err)
	}
	if calls != 4 {
		t.Fatalf("request calls: got %d want 4", calls)
	}
	if len(*sleeps) != 3 || (*sleeps)[0] != 10*time.Second || (*sleeps)[1] != 20*time.Second || (*sleeps)[2] != 40*time.Second {
		t.Fatalf("backoff sequence: got %v want [10s 20s 40s]", *sleeps)
	}
	_ = lastStatus
}

// TestDoWithRetryCtxDoneBeforeRetry 落地 BR2：父 ctx 已取消时循环顶部
// 直通返回 ctx.Err()，不发起任何请求。
func TestDoWithRetryCtxDoneBeforeRetry(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return newResp(http.StatusOK), nil
	})
	c, _ := newRetryClient(t, rt)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.doWithRetry(ctx, listTimeout, newReqFn())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is: got %v want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("request calls: got %d want 0", calls)
	}
}

// TestDoWithRetryParentCancelDuringAttempt 落地 BR2：尝试中的父 ctx 取消
// 归为不可重试，立即返回 context.Canceled，请求计数 1。
func TestDoWithRetryParentCancelDuringAttempt(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, context.Canceled
	})
	c, sleeps := newRetryClient(t, rt)

	_, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is: got %v want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
	if len(*sleeps) != 0 {
		t.Fatalf("sleep calls: got %d want 0", len(*sleeps))
	}
}

// TestDoWithRetryTimeoutRetriable 落地 BR2 区分语义：单次尝试的子 ctx
// 超时（父 ctx 未取消）属可重试传输错误，第二次成功即恢复。
func TestDoWithRetryTimeoutRetriable(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, context.DeadlineExceeded
		}
		return newResp(http.StatusOK), nil
	})
	c, sleeps := newRetryClient(t, rt)

	resp, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if calls != 2 || len(*sleeps) != 1 {
		t.Fatalf("calls=%d sleeps=%d, want 2/1", calls, len(*sleeps))
	}
}

// TestDoWithRetryReqFnError 落地 reqFn 契约：请求构造失败属配置类确定性失败，
// 首轮直通返回 ErrNotConfigured 包装，不重试不退避，不伪装网络故障。
func TestDoWithRetryReqFnError(t *testing.T) {
	boom := errors.New("bad url")
	c, sleeps := newRetryClient(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatal("transport must not be reached when reqFn fails")
		return nil, nil
	}))

	_, err := c.doWithRetry(context.Background(), listTimeout, func(ctx context.Context) (*http.Request, error) {
		return nil, boom
	})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("errors.Is: got %v want boom cause preserved", err)
	}
	if len(*sleeps) != 0 {
		t.Fatalf("sleep calls: got %d want 0", len(*sleeps))
	}
}

// TestDoWithRetryReqFnUsesDerivedCtx 落地 BR2/签名偏差依据：reqFn 收到的
// ctx 是父 ctx 派生的子 ctx（带完整超时预算），且每次尝试是新派生的。
func TestDoWithRetryReqFnUsesDerivedCtx(t *testing.T) {
	parent := context.Background()
	var gotCtx context.Context
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return newResp(http.StatusOK), nil
	})
	c, _ := newRetryClient(t, rt)

	_, err := c.doWithRetry(parent, listTimeout, func(ctx context.Context) (*http.Request, error) {
		gotCtx = ctx
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.test/", nil)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCtx == parent {
		t.Fatal("reqFn must receive derived child ctx, got parent")
	}
	if _, ok := gotCtx.Deadline(); !ok {
		t.Fatal("derived ctx must carry deadline (full per-attempt budget)")
	}
}

// TestDoWithRetryRetryRespBodyClosed 落地防连接泄漏：5xx 重试场景上一轮
// resp.Body 必须先 Close 再退避。
func TestDoWithRetryRetryRespBodyClosed(t *testing.T) {
	var calls, closed int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       &countingCloser{onClose: func() { closed++ }},
			Header:     make(http.Header),
		}, nil
	})
	c, _ := newRetryClient(t, rt)

	_, err := c.doWithRetry(context.Background(), listTimeout, newReqFn())
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("errors.Is: got %v want ErrNetwork", err)
	}
	if calls != 4 {
		t.Fatalf("request calls: got %d want 4", calls)
	}
	if closed != 4 {
		t.Fatalf("closed bodies: got %d want 4 (every 5xx body closed before backoff)", closed)
	}
}

// countingCloser 计数 Close 调用。
type countingCloser struct{ onClose func() }

func (c *countingCloser) Read(p []byte) (int, error) { return 0, io.EOF }
func (c *countingCloser) Close() error               { c.onClose(); return nil }

// TestSleepContextInterrupted 落地 sleepContext 契约：ctx 先结束返回 ctx.Err()。
func TestSleepContextInterrupted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := sleepContext(ctx, 5*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is: got %v want context.DeadlineExceeded", err)
	}
}

// TestSleepContextElapsed 等待自然到期返回 nil。
func TestSleepContextElapsed(t *testing.T) {
	if err := sleepContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSleepContextZeroDuration 零时长立即返回，正常 ctx 下为 nil。
func TestSleepContextZeroDuration(t *testing.T) {
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSleepContextDoneCtx ctx 已取消时零时长也返回 ctx.Err()。
func TestSleepContextDoneCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is: got %v want context.Canceled", err)
	}
}
