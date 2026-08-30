package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Client 底座公开接口，只暴露 StreamChat（specs §5.1 接口契约）。
type Client interface {
	StreamChat(ctx context.Context, req ChatRequest) (Stream, error)
}

// adapter 是协议适配器抽象，按服务商归属协议实现流式对话补全（specs §2.4 能力5）。
type adapter interface {
	StreamChat(ctx context.Context, mc ModelConfig, req ChatRequest) (Stream, error)
}

// 适配器工厂变量：client 经此构造适配器，测试替换为探针断言路由与缓存。
var (
	newOpenaiAdapterFn    = func(cfg ModelConfig, timeout time.Duration) adapter { return newOpenaiAdapter(cfg, timeout) }
	newAnthropicAdapterFn = func(cfg ModelConfig, timeout time.Duration) adapter { return newAnthropicAdapter(cfg, timeout) }
)

type client struct {
	cfg      Config
	provider EnabledModelProvider
	gate     *gate
	mu       sync.Mutex
	adapters map[string]adapter // 缓存 key = provider|baseURL|modelID|apiKey
}

// 编译期断言：client 未导出，同包内断言其满足 Client 接口（改动接口编译即报错）。
var _ Client = (*client)(nil)

// New 构造底座客户端，Config 缺省值兜底，TokenCounter 未注入时用默认实现。
func New(cfg Config, provider EnabledModelProvider) Client {
	cfg = applyDefaults(cfg)
	return &client{
		cfg:      cfg,
		provider: provider,
		gate:     newGate(cfg.MaxConcurrency, cfg.QueueSize),
		adapters: make(map[string]adapter),
	}
}

// StreamChat 统一入口主流程（specs §2.4 能力1/2/4/5）：
// 解析启用模型 → token 预检 → 选适配器（缓存）→ gate 排队 → 重试发起 → 错误分类。
func (c *client) StreamChat(ctx context.Context, req ChatRequest) (Stream, error) {
	start := time.Now()

	// 1. 启用模型解析：每次调用重新解析，模型切换即时生效（§2.4 能力5 注意事项）
	mc, err := c.provider.GetEnabledModel(ctx)
	if err != nil {
		return nil, c.failLog(mc, start, wrapProviderUnavailable(err))
	}

	// 2. token 预检：超限不进 gate、不调适配器（§2.4 能力2）
	if err := checkTokenBudget(ctx, c.cfg.TokenBudget, c.cfg.TokenCounter, mc.ModelID, req.Messages); err != nil {
		return nil, c.failLog(mc, start, err)
	}

	// 3. 适配器选择与缓存：模型或密钥变更即时重建，相同配置复用（§5.1）
	adp, err := c.adapterFor(mc)
	if err != nil {
		return nil, c.failLog(mc, start, err)
	}

	// 4. 全局并发 gate：队列满返回 ErrQueueFull（§2.4 能力4）
	queueStart := time.Now()
	if err := c.gate.Acquire(ctx); err != nil {
		if errors.Is(err, ErrQueueFull) {
			slog.Error("llm call rejected", "code", ErrQueueFull.Code, "queue_depth", c.gate.QueueDepth(), "model", mc.ModelID)
			return nil, err
		}
		return nil, c.failLog(mc, start, err) // ctx 取消透传
	}
	queueWait := time.Since(queueStart)

	// 5. 重试发起：占槽直至成功或失败，仅覆盖流建立阶段（§2.4 能力1/3）
	var stream Stream
	retries := 0
	err = retryWithBackoff(ctx, c.cfg, func() error {
		s, e := adp.StreamChat(ctx, mc, req)
		if e != nil {
			// 与 retryWithBackoff 的重试判定同源（isRetryable + attempt < MaxRetries）：
			// 只在真实将重试时记 WARN 并计数，耗尽的最后一次失败与确定性错误交由 doneLog ERROR 承载。
			if he := asHTTPError(e); he != nil && isRetryable(he, c.cfg.RetryOnStatus) && retries < c.cfg.MaxRetries {
				c.warnRetry(mc, e, retries, queueWait)
				retries++
			}
			return e
		}
		stream = s
		return nil
	})
	if err != nil {
		c.gate.Release()
		classified := classifyError(err, c.cfg.RetryOnStatus)
		c.doneLog(mc, classified, start, queueWait, retries, 0, 0)
		return nil, classified
	}

	// 6. 成功：返回 gatedStream，Close 时释放槽并记完成 INFO（此时 Usage 已收集真值）
	gs := &gatedStream{
		Stream: stream,
		once:   sync.Once{},
		classify: func(err error) error {
			return classifyError(err, c.cfg.RetryOnStatus)
		},
		release: func() {
			c.gate.Release()
			prompt, completion := stream.Usage()
			c.doneLog(mc, nil, start, queueWait, retries, prompt, completion)
		},
	}
	slog.Debug("llm stream established", "model", mc.ModelID, "retries", retries)
	return gs, nil
}

// adapterFor 按服务商归属协议取适配器，命中缓存直接复用（specs §2.4 能力5 / §5.1 适配器缓存）。
// 未命中即当前配置已与缓存条目不同（密钥轮换、换模型、跨服务商切换），排他启用唯一模型
// 语义下旧条目不会再被命中，全清重建：缓存恒 ≤1 条，旧密钥（明文驻留缓存 key）随之清出。
// 在飞调用已持有旧 adapter 引用，替换 map 不影响其执行完毕。
func (c *client) adapterFor(mc ModelConfig) (adapter, error) {
	key := adapterKey(mc)
	c.mu.Lock()
	defer c.mu.Unlock()
	if a, ok := c.adapters[key]; ok {
		return a, nil
	}
	a := c.newAdapter(mc)
	if a == nil {
		return nil, wrapProviderUnavailable(fmt.Errorf("unsupported provider: %s", mc.Provider))
	}
	c.adapters = map[string]adapter{key: a}
	return a, nil
}

// adapterKey 缓存键：provider|baseURL|modelID|apiKey，任一变更即视为不同配置。
func adapterKey(mc ModelConfig) string {
	return mc.Provider + "|" + mc.BaseURL + "|" + mc.ModelID + "|" + mc.APIKey
}

// newAdapter 归属映射：deepseek/openai/zhipu 走 OpenAI 兼容，anthropic 走 Anthropic（specs §2.4 能力5）。
func (c *client) newAdapter(mc ModelConfig) adapter {
	switch mc.Provider {
	case "deepseek", "openai", "zhipu":
		return newOpenaiAdapterFn(mc, c.cfg.Timeout)
	case "anthropic":
		return newAnthropicAdapterFn(mc, c.cfg.Timeout)
	default:
		return nil
	}
}

// gatedStream 包装底层 Stream，Close 时经 sync.Once 释放并发槽；
// Recv 阶段的错误同样收敛为领域错误（specs §2.3 全量承诺），ctx 取消优先透传。
type gatedStream struct {
	Stream
	once     sync.Once
	classify func(err error) error
	release  func()
}

// Recv 读下一块增量；io.EOF 原样透传表流正常结束；ctx 已取消直接返回 ctx 错误
// （底层适配器可能把取消吞成传输超时）；其余非 EOF 错误经 classify 收敛为 *Error，
// 调用方可 errors.Is 分类处置（specs §2.3）。
func (s *gatedStream) Recv() (StreamChunk, error) {
	chunk, err := s.Stream.Recv()
	if err == nil || errors.Is(err, io.EOF) {
		return chunk, err
	}
	if ctxErr := ctxErrOf(err); ctxErr != nil {
		return chunk, ctxErr
	}
	return chunk, s.classify(err)
}

// Close 关闭底层流并释放并发槽，幂等。
func (s *gatedStream) Close() error {
	err := s.Stream.Close()
	s.once.Do(s.release)
	return err
}

// defaultTimeout 单次调用默认超时。OpenAI 路径承载于 http.Client.Timeout、Anthropic 路径
// 承载于 WithRequestTimeout，两者都覆盖从建连到流式 body 读毕的全程，长回复生成常超 2 分钟，
// 参考 anthropic-sdk-go 对流式 Messages 的建议取 10 分钟量级。
const defaultTimeout = 10 * time.Minute

// applyDefaults 填充 Config 零值默认（specs §2.2 参数列表）。
func applyDefaults(cfg Config) Config {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 30 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 120 * time.Second
	}
	if len(cfg.RetryOnStatus) == 0 {
		cfg.RetryOnStatus = defaultRetryOnStatus
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	// QueueSize 0/负值统一归默认 1024：0 不表达「禁用排队」，排他启用唯一模型下
	// 禁排队语义无场景，显式配置请给正整数；gate 层 queueSize=0 的立即拒绝仅包内防御归一。
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.TokenCounter == nil {
		cfg.TokenCounter = NewTikTokenCounter()
	}
	return cfg
}

// classifyError 把 httpError/超时等收敛为底座领域错误（specs §2.3 / §5.1 错误分类映射）。
// statuses 为 client 生效的重试状态码集，Retryable 按它判定，与 retryWithBackoff 实际行为同源。
// sentinel 是共享指针，必须复制值再填字段，禁止原地修改污染全局状态。
func classifyError(err error, statuses []int) error {
	if err == nil {
		return nil
	}
	if le := asLLMError(err); le != nil {
		return le // 已是领域错误（预检超限等），透传
	}
	if ctxErr := ctxErrOf(err); ctxErr != nil {
		return ctxErr // ctx 取消/超时透传，由调用方按取消语义处理
	}
	he := asHTTPError(err)
	if he == nil {
		return copyError(ErrUnknown, err, statuses)
	}
	switch {
	case he.status == 401:
		return copyError(ErrAuth, he, statuses)
	case he.status == 429:
		return copyError(ErrRateLimited, he, statuses)
	case he.status == 400 && strings.Contains(he.message, "context_length_exceeded"):
		return copyError(ErrContextLengthExceeded, he, statuses)
	case he.timeout:
		return copyError(ErrTimeout, he, statuses)
	case he.status >= 500:
		return copyError(ErrRetryExhausted, he, statuses)
	default:
		return copyError(ErrUnknown, he, statuses)
	}
}

// asHTTPError 提取内部协议错误，支持包装层。
func asHTTPError(err error) *httpError {
	var he *httpError
	if errors.As(err, &he) {
		return he
	}
	return nil
}

// asLLMError 提取底座领域错误，支持包装层。
func asLLMError(err error) *Error {
	var le *Error
	if errors.As(err, &le) {
		return le
	}
	return nil
}

// ctxErrOf 判定错误链是否携带 ctx 取消/超时。
func ctxErrOf(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

// copyError 复制 sentinel 值填充原始状态与消息，返回独立实例；
// statuses 为生效重试状态码集，Retryable 与 retryWithBackoff 的重试判定同源。
func copyError(sentinel *Error, src error, statuses []int) *Error {
	e := *sentinel
	if he := asHTTPError(src); he != nil {
		e.StatusCode = he.status
		e.Msg = he.message
		e.Retryable = isRetryable(he, statuses)
	} else {
		e.Msg = src.Error()
	}
	return &e
}

// wrapProviderUnavailable 包装 ErrProviderUnavailable，保留原始原因。
func wrapProviderUnavailable(cause error) error {
	e := *ErrProviderUnavailable
	e.Msg = cause.Error()
	return &e
}

// warnRetry 重试进行中 WARN（specs §6.1）：status、retry、backoff、queue_depth。
// 调用方已保证仅在真实将重试时进入（isRetryable 且未耗尽），401 等确定性错误的
// 失败语义由 doneLog ERROR code=ErrXxx 承载（§6.1 把密钥失效归 ERROR 场景），
// 避免假 retrying WARN 污染 llm_retry_total 指标。
func (c *client) warnRetry(mc ModelConfig, err error, retry int, queueWait time.Duration) {
	he := asHTTPError(err)
	if he == nil {
		return
	}
	slog.Warn("llm call retrying", "status", he.status, "retry", retry, "backoff", backoffDuration(he, c.cfg, retry),
		"queue_depth", c.gate.QueueDepth(), "model", mc.ModelID)
}

// doneLog 每次调用完成 INFO（specs §6.1）：model、prompt_tokens、completion_tokens、
// duration、queue_wait、retries、ok；失败路径升级 ERROR 记录 code。
// 日志只记录模型名、token 用量、耗时、排队等待、重试次数、结果，禁止密钥与对话原文（§3.3）。
func (c *client) doneLog(mc ModelConfig, err error, start time.Time, queueWait time.Duration, retries, prompt, completion int) {
	if err != nil {
		slog.Error("llm call failed", "code", errCode(err), "model", mc.ModelID,
			"prompt_tokens", prompt, "completion_tokens", completion,
			"duration", time.Since(start), "queue_wait", queueWait, "retries", retries)
		return
	}
	slog.Info("llm call completed", "model", mc.ModelID,
		"prompt_tokens", prompt, "completion_tokens", completion,
		"duration", time.Since(start), "queue_wait", queueWait, "retries", retries, "ok", true)
}

// errCode 提取失败日志的 code 字段：领域错误取 Code，ctx 取消标注 canceled，
// 其余收敛为 ErrUnknown（specs §6.1 code=ErrXxx 格式）。
func errCode(err error) string {
	if le := asLLMError(err); le != nil {
		return le.Code
	}
	if ctxErrOf(err) != nil {
		return "context_canceled"
	}
	return ErrUnknown.Code
}

// failLog 前置失败（解析/预检/路由阶段，未进 gate）日志：错误码 + 耗时。
func (c *client) failLog(mc ModelConfig, start time.Time, err error) error {
	c.doneLog(mc, err, start, 0, 0, 0, 0)
	return err
}
