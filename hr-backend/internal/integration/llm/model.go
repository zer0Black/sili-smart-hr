package llm

import (
	"context"
	"fmt"
	"time"
)

// ChatRequest 一次对话补全的统一请求，模型不在此指定，恒取当前排他启用模型。
type ChatRequest struct {
	Messages    []ChatMessage
	Temperature *float64
	MaxTokens   int
}

type ChatMessage struct {
	Role    string // system / user / assistant
	Content string
}

// Config 底座横切行为配置，字段与默认值见 specs §2.2 参数列表。
// MaxRetryAfter 为后增旋钮不在 specs 列表：429 Retry-After 封顶，<=0 回退 MaxBackoff。
type Config struct {
	Timeout        time.Duration
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxRetryAfter  time.Duration
	RetryOnStatus  []int
	MaxConcurrency int // 全局在飞硬上限，默认 4，不可禁用
	QueueSize      int
	TokenBudget    int
	TokenCounter   TokenCounter
}

// ModelConfig 底座消费的启用模型参数，由配置域实现 provider 时填充。
type ModelConfig struct {
	Provider string // deepseek / openai / zhipu / anthropic
	ModelID  string
	BaseURL  string
	APIKey   string
}

// EnabledModelProvider 由配置域（P2_SYS_001）实现并注入，底座不依赖配置域实现。
type EnabledModelProvider interface {
	GetEnabledModel(ctx context.Context) (ModelConfig, error)
}

// Stream 流式读取接口，调用方拼装全部 Recv 块即得完整回复。
type Stream interface {
	Recv() (StreamChunk, error) // 读下一块增量，正常结束返回 io.EOF
	Close() error
	Usage() (prompt, completion int) // 流结束后返回最终 token 用量，未获取为 0
}

type StreamChunk struct {
	Content string
}

// Error 即 specs 的 LLMError：统一领域错误，携带稳定 Code 供调用方分类处置。
type Error struct {
	Code       string // 稳定错误码，见下方 sentinel 的 Code 值
	Msg        string
	StatusCode int  // 原始 HTTP 状态码，0 表示非 HTTP 错误
	Retryable  bool // 是否已重试耗尽，供调用方判断是否走 fallback
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Msg != "" {
		return fmt.Sprintf("llm: %s: %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("llm: %s", e.Code)
}

// Is 按 Code 匹配同类错误：classifyError 返回 sentinel 的复制体（防污染共享指针），
// errors.Is 靠此方法识别复制体与 sentinel 同类。Code 全局唯一（见测试断言）。
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	t, ok := target.(*Error)
	return ok && e.Code == t.Code
}

// specs §2.3 错误码定义，8 个 sentinel，Code 与表格错误码列一致（§6.2 日志 code= 值印证）。
var (
	ErrAuth                  = &Error{Code: "ErrAuth"}
	ErrRateLimited           = &Error{Code: "ErrRateLimited"}
	ErrQueueFull             = &Error{Code: "ErrQueueFull"}
	ErrContextLengthExceeded = &Error{Code: "ErrContextLengthExceeded"}
	ErrTimeout               = &Error{Code: "ErrTimeout"}
	ErrRetryExhausted        = &Error{Code: "ErrRetryExhausted"}
	ErrProviderUnavailable   = &Error{Code: "ErrProviderUnavailable"}
	ErrUnknown               = &Error{Code: "ErrUnknown"}
)

// httpError 是 adapter 抛给 retry/client 的内部协议错误，adapter 把 SDK 错误转换为该类型。
type httpError struct {
	status     int
	message    string
	retryAfter time.Duration // 429 Retry-After 头时长，0 表示无
	timeout    bool          // 传输层超时标记
}

func (e *httpError) Error() string {
	if e.timeout {
		return fmt.Sprintf("llm http: transport timeout: %s", e.message)
	}
	return fmt.Sprintf("llm http: status %d: %s", e.status, e.message)
}
