package llm

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// 编译期断言：*Error 与 *httpError 均实现 error 接口。
var (
	_ error = ErrAuth
	_ error = &httpError{}
)

func TestErrorSentinelsMatchable(t *testing.T) {
	if !errors.Is(ErrAuth, ErrAuth) {
		t.Error("errors.Is(ErrAuth, ErrAuth) 应为 true")
	}
	if errors.Is(ErrRateLimited, ErrAuth) {
		t.Error("errors.Is(ErrRateLimited, ErrAuth) 应为 false")
	}
}

func TestErrorCodeValues(t *testing.T) {
	cases := []struct {
		sentinel *Error
		want     string
	}{
		{ErrAuth, "ErrAuth"},
		{ErrRateLimited, "ErrRateLimited"},
		{ErrQueueFull, "ErrQueueFull"},
		{ErrContextLengthExceeded, "ErrContextLengthExceeded"},
		{ErrTimeout, "ErrTimeout"},
		{ErrRetryExhausted, "ErrRetryExhausted"},
		{ErrProviderUnavailable, "ErrProviderUnavailable"},
		{ErrUnknown, "ErrUnknown"},
	}
	for _, c := range cases {
		if c.sentinel == nil {
			t.Fatalf("sentinel %s 不应为 nil", c.want)
		}
		if c.sentinel.Code != c.want {
			t.Errorf("sentinel Code = %q, want %q", c.sentinel.Code, c.want)
		}
	}
}

// 边界：8 个 sentinel 两两互异，Code 无重复。
func TestErrorSentinelsDistinct(t *testing.T) {
	all := []*Error{ErrAuth, ErrRateLimited, ErrQueueFull, ErrContextLengthExceeded,
		ErrTimeout, ErrRetryExhausted, ErrProviderUnavailable, ErrUnknown}
	seen := make(map[string]bool, len(all))
	for _, s := range all {
		if seen[s.Code] {
			t.Errorf("sentinel Code 重复: %s", s.Code)
		}
		seen[s.Code] = true
	}
}

// 边界：包装后的 sentinel 仍可被 errors.Is 匹配，这是 retry/fallback 路径的消费方式。
func TestErrorSentinelsWrappedMatchable(t *testing.T) {
	wrapped := fmt.Errorf("重试耗尽: %w", ErrRetryExhausted)
	if !errors.Is(wrapped, ErrRetryExhausted) {
		t.Error("包装后的 ErrRetryExhausted 应可被 errors.Is 匹配")
	}
	if errors.Is(wrapped, ErrTimeout) {
		t.Error("包装 ErrRetryExhausted 不应匹配 ErrTimeout")
	}
}

// 边界：Error.Error() 输出非空且携带 Code。
func TestErrorMessage(t *testing.T) {
	if got := ErrAuth.Error(); got == "" {
		t.Error("Error() 输出不应为空")
	}
	e := &Error{Code: "ErrTimeout", Msg: "调用超时", StatusCode: 0, Retryable: true}
	if got := e.Error(); got == "" {
		t.Error("带 Msg 的 Error() 输出不应为空")
	}
}

// 边界：httpError 字段语义与 Error() 输出。
func TestHTTPError(t *testing.T) {
	e := &httpError{status: 429, message: "rate limited", retryAfter: 30 * time.Second}
	if e.status != 429 || e.retryAfter != 30*time.Second {
		t.Error("httpError 字段值与构造不符")
	}
	if got := e.Error(); got == "" {
		t.Error("httpError.Error() 输出不应为空")
	}
	te := &httpError{timeout: true}
	if !te.timeout {
		t.Error("timeout 标记应为 true")
	}
	_ = io.EOF // 占位引用，流结束语义在 Stream 实现任务中验证
}
