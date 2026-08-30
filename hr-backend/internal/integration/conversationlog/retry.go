package conversationlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// doWithRetry 包装单次 HTTP 执行：传输错误（父 ctx 未取消）、5xx 与 429 限流可重试，
// 退避 initialBackoff << attempt（10s→20s→40s），最多 4 次执行，最坏约 190s，
// 调用方需自带覆盖此预算的 deadline。确定性失败首轮直通。
// 每次尝试独立派生超时子 ctx；非可重试响应的 body 由调用方 Close（连带 cancel 子
// ctx），重试轮次先 drain+Close 再退避防连接泄漏。
func (c *Client) doWithRetry(ctx context.Context, timeout time.Duration, reqFn func(context.Context) (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, wrapErr(err, fmt.Errorf("父 ctx 已取消，中止剩余重试 (attempt=%d)", attempt))
		}

		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := reqFn(reqCtx)
		if err != nil {
			cancel()
			return nil, wrapErr(ErrNotConfigured, fmt.Errorf("构造请求失败: %w", err))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			cancel()
			// 父 ctx 取消直通，其余可重试。
			if perr := ctx.Err(); perr != nil || errors.Is(err, context.Canceled) {
				if perr == nil {
					perr = context.Canceled
				}
				return nil, wrapErr(perr, fmt.Errorf("请求被取消: %w", err))
			}
			if attempt >= maxRetries {
				c.logFailed(req, 0, attempt)
				return nil, wrapErr(ErrNetwork, fmt.Errorf("网络不可达或超时（重试 %d 次耗尽）: %w", maxRetries, err))
			}
			backoff := initialBackoff << attempt
			c.logRetrying(req, attempt+1, backoff)
			if sleepErr := c.sleep(ctx, backoff); sleepErr != nil {
				return nil, wrapErr(sleepErr, fmt.Errorf("退避等待中止 (attempt=%d)", attempt))
			}
			continue
		}

		if retryableStatus(resp.StatusCode) {
			code := resp.StatusCode
			_ = drainAndClose(resp.Body) // 先收掉本轮 body 再退避，防连接泄漏
			cancel()
			if attempt >= maxRetries {
				c.logFailed(req, code, attempt)
				if code == http.StatusTooManyRequests {
					return nil, wrapErr(ErrRateLimited, fmt.Errorf("上游限流（重试 %d 次耗尽）: HTTP %d", maxRetries, code))
				}
				return nil, wrapErr(ErrNetwork, fmt.Errorf("上游 5xx（重试 %d 次耗尽）: HTTP %d", maxRetries, code))
			}
			backoff := initialBackoff << attempt
			c.logRetrying(req, attempt+1, backoff)
			if sleepErr := c.sleep(ctx, backoff); sleepErr != nil {
				return nil, wrapErr(sleepErr, fmt.Errorf("退避等待中止 (attempt=%d)", attempt))
			}
			continue
		}

		// 其余响应直接返回，body 由调用方 Close（连带 cancel）。
		resp.Body = cancelBody{ReadCloser: resp.Body, cancel: cancel}
		return resp, nil
	}
}

// retryableStatus 判定响应状态码是否走重试路径：5xx 与 429 限流（瞬时错误）。
func retryableStatus(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests
}

// sleep 取等待注入点，sleepFn 为 nil 时用 sleepContext 兜底。
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.sleepFn != nil {
		return c.sleepFn(ctx, d)
	}
	return sleepContext(ctx, d)
}

// cancelBody 把子 ctx 的 cancel 绑定到 body Close。
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// drainAndClose 读完（限额）并关闭 body，保证连接可复用。
func drainAndClose(body io.ReadCloser) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	return body.Close()
}

// sessionKeyFromPath 从请求 path 提取末段作 session_key。
func sessionKeyFromPath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// logRetrying 输出 WARN 级重试日志。
func (c *Client) logRetrying(req *http.Request, attempt int, backoff time.Duration) {
	slog.Warn("conversationlog request retrying",
		"method", req.Method,
		"path", req.URL.Path,
		"attempt", attempt,
		"backoff", backoff.String(),
	)
}

// logFailed 输出 ERROR 级失败日志，status 0 表示传输错误。
func (c *Client) logFailed(req *http.Request, status, attempt int) {
	slog.Error("conversationlog request failed",
		"method", req.Method,
		"path", req.URL.Path,
		"status", status,
		"session_key", sessionKeyFromPath(req.URL.Path),
		"attempt", attempt,
		"retries", maxRetries,
		"code", ErrNetwork.Error(),
	)
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
