package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakes（specs §5.1：fake adapter / fake provider / fake TokenCounter 注入） ---

type fakeProvider struct {
	mc  ModelConfig
	err error
}

func (p *fakeProvider) GetEnabledModel(context.Context) (ModelConfig, error) {
	return p.mc, p.err
}

type fakeAdapter struct {
	calls atomic.Int32
	fn    func(call int) (Stream, error)
}

func (a *fakeAdapter) StreamChat(_ context.Context, _ ModelConfig, _ ChatRequest) (Stream, error) {
	n := int(a.calls.Add(1))
	if a.fn == nil {
		return &fakeStream{}, nil
	}
	return a.fn(n)
}

type fakeStream struct{}

func (*fakeStream) Recv() (StreamChunk, error) { return StreamChunk{}, io.EOF }
func (*fakeStream) Close() error               { return nil }
func (*fakeStream) Usage() (int, int)          { return 11, 22 }

type fakeTokenCounter struct {
	count int
	calls atomic.Int32
}

func (c *fakeTokenCounter) CountTokens(context.Context, string, []ChatMessage) (int, error) {
	c.calls.Add(1)
	return c.count, nil
}

var clientMC = ModelConfig{Provider: "deepseek", ModelID: "deepseek-chat", BaseURL: "http://127.0.0.1:9", APIKey: "sk-client-test"}

// 编译期断言：New 返回值满足 Client 接口（接口只暴露 StreamChat，改动接口编译即报错）。
var _ Client = New(Config{}, &fakeProvider{mc: clientMC})

// fastCfg 紧凑重试参数，避免真实 30s 退避拖慢测试。
func fastCfg() Config {
	return Config{MaxRetries: 2, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
}

func userReq() ChatRequest {
	return ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
}

// factoryProbe 适配器工厂探针：替换包级工厂变量计数构造次数并返回 fake adapter。
type factoryProbe struct {
	openai    atomic.Int32
	anthropic atomic.Int32
}

func installProbe(t *testing.T, fa *fakeAdapter) *factoryProbe {
	t.Helper()
	p := &factoryProbe{}
	oldO, oldA := newOpenaiAdapterFn, newAnthropicAdapterFn
	newOpenaiAdapterFn = func(ModelConfig, time.Duration) adapter {
		p.openai.Add(1)
		return fa
	}
	newAnthropicAdapterFn = func(ModelConfig, time.Duration) adapter {
		p.anthropic.Add(1)
		return fa
	}
	t.Cleanup(func() { newOpenaiAdapterFn, newAnthropicAdapterFn = oldO, oldA })
	return p
}

// --- 错误分类（specs §5.1 错误分类映射） ---

func TestClientClassify401(t *testing.T) {
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 401, message: "Invalid API key"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if n := fa.calls.Load(); n != 1 {
		t.Errorf("adapter 调用 %d 次, want 1（401 确定性错误不重试）", n)
	}
	var le *Error
	if !errors.As(err, &le) || le.StatusCode != 401 {
		t.Errorf("分类错误 StatusCode = %+v, want 401", le)
	}
}

func TestClientClassify429(t *testing.T) {
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 429, message: "rate limit"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if n := fa.calls.Load(); n != 3 {
		t.Errorf("adapter 调用 %d 次, want 3（MaxRetries=2 重试耗尽）", n)
	}
}

func TestClientClassifyContextLength(t *testing.T) {
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 400, message: "context_length_exceeded: prompt is too long"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrContextLengthExceeded) {
		t.Fatalf("err = %v, want ErrContextLengthExceeded", err)
	}
	if n := fa.calls.Load(); n != 1 {
		t.Errorf("adapter 调用 %d 次, want 1（400 确定性错误不重试）", n)
	}
}

func TestClientClassifyTimeout(t *testing.T) {
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{timeout: true, message: "transport timeout"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if n := fa.calls.Load(); n != 3 {
		t.Errorf("adapter 调用 %d 次, want 3（超时可重试，MaxRetries=2 耗尽）", n)
	}
}

func TestClientClassify5xx(t *testing.T) {
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 500, message: "internal error"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("err = %v, want ErrRetryExhausted", err)
	}
	if n := fa.calls.Load(); n != 3 {
		t.Errorf("adapter 调用 %d 次, want 3（MaxRetries=2 耗尽）", n)
	}
}

// --- token 预检（specs §2.4 能力2 / §5.1 token 预检） ---

func TestClientTokenBudgetShortCircuit(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	cfg := fastCfg()
	cfg.TokenBudget = 32000
	cfg.TokenCounter = &fakeTokenCounter{count: 40000}
	c := New(cfg, &fakeProvider{mc: clientMC})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrContextLengthExceeded) {
		t.Fatalf("err = %v, want ErrContextLengthExceeded", err)
	}
	if n := fa.calls.Load(); n != 0 {
		t.Errorf("adapter 调用 %d 次, want 0（预检超限不调适配器）", n)
	}
}

// 边界：预算内正常放行到适配器；TokenBudget<=0 跳过预检（specs §2.2 默认 0 不预检）。
func TestClientTokenBudgetPass(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	cfg := fastCfg()
	cfg.TokenBudget = 32000
	cfg.TokenCounter = &fakeTokenCounter{count: 100}
	c := New(cfg, &fakeProvider{mc: clientMC})
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	defer s.Close()
	if n := fa.calls.Load(); n != 1 {
		t.Errorf("adapter 调用 %d 次, want 1", n)
	}

	fa2 := &fakeAdapter{}
	installProbe(t, fa2)
	c2 := New(fastCfg(), &fakeProvider{mc: clientMC}) // TokenBudget 0，无计数器
	if _, err := c2.StreamChat(context.Background(), userReq()); err != nil {
		t.Fatalf("TokenBudget=0 应跳过预检, err = %v", err)
	}
}

// --- 适配器缓存与路由（specs §5.1 适配器缓存 / §2.4 能力5） ---

func TestClientAdapterCache(t *testing.T) {
	fa := &fakeAdapter{}
	p := installProbe(t, fa)
	fp := &fakeProvider{mc: clientMC}
	c := New(fastCfg(), fp)
	for i := 0; i < 2; i++ {
		s, err := c.StreamChat(context.Background(), userReq())
		if err != nil {
			t.Fatalf("第 %d 次 StreamChat 错误: %v", i+1, err)
		}
		s.Close()
	}
	if n := p.openai.Load(); n != 1 {
		t.Errorf("openai 构造 %d 次, want 1（相同 ModelConfig 复用）", n)
	}
	fp.mc.APIKey = "sk-rotated" // 密钥轮换即时重建
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("换 Key 后 StreamChat 错误: %v", err)
	}
	s.Close()
	if n := p.openai.Load(); n != 2 {
		t.Errorf("openai 构造 %d 次, want 2（APIKey 变更重建）", n)
	}
	if n := p.anthropic.Load(); n != 0 {
		t.Errorf("anthropic 构造 %d 次, want 0", n)
	}
}

func TestClientProviderRouting(t *testing.T) {
	fa := &fakeAdapter{}
	p := installProbe(t, fa)
	fp := &fakeProvider{}
	c := New(fastCfg(), fp)
	for _, provider := range []string{"deepseek", "openai", "zhipu"} {
		fp.mc = ModelConfig{Provider: provider, ModelID: "m-" + provider, APIKey: "k-" + provider}
		s, err := c.StreamChat(context.Background(), userReq())
		if err != nil {
			t.Fatalf("%s: StreamChat 错误: %v", provider, err)
		}
		s.Close()
	}
	if n := p.openai.Load(); n != 3 {
		t.Errorf("openai 构造 %d 次, want 3（deepseek/openai/zhipu 收敛 OpenAI 协议）", n)
	}
	fp.mc = ModelConfig{Provider: "anthropic", ModelID: "claude-sonnet-4-5", APIKey: "k-a"}
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("anthropic: StreamChat 错误: %v", err)
	}
	s.Close()
	if n := p.anthropic.Load(); n != 1 {
		t.Errorf("anthropic 构造 %d 次, want 1", n)
	}
	if n := p.openai.Load(); n != 3 {
		t.Errorf("anthropic 轮 openai 构造 %d 次, want 保持 3", n)
	}
}

// --- 启用模型解析（specs §2.4 能力5 注意事项） ---

func TestClientNoProvider(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{err: errors.New("no enabled model")})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if n := fa.calls.Load(); n != 0 {
		t.Errorf("adapter 调用 %d 次, want 0", n)
	}
}

func TestClientUnknownProvider(t *testing.T) {
	fa := &fakeAdapter{}
	p := installProbe(t, fa)
	mc := clientMC
	mc.Provider = "unknown"
	c := New(fastCfg(), &fakeProvider{mc: mc})
	_, err := c.StreamChat(context.Background(), userReq())
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if n := fa.calls.Load(); n != 0 {
		t.Errorf("adapter 调用 %d 次, want 0", n)
	}
	if p.openai.Load() != 0 || p.anthropic.Load() != 0 {
		t.Error("未知 Provider 不应构造任何适配器")
	}
}

// --- 并发 gate（specs §2.4 能力4） ---

type streamResult struct {
	stream Stream
	err    error
}

func TestClientQueueFull(t *testing.T) {
	started := make(chan struct{})
	release1 := make(chan struct{})
	fa := &fakeAdapter{fn: func(call int) (Stream, error) {
		if call == 1 {
			close(started)
			<-release1 // 第一个调用占住唯一在飞槽
		}
		return &fakeStream{}, nil
	}}
	installProbe(t, fa)
	cfg := fastCfg()
	cfg.MaxConcurrency = 1
	cfg.QueueSize = 1
	c := New(cfg, &fakeProvider{mc: clientMC})

	ch1 := make(chan streamResult, 1)
	go func() {
		s, err := c.StreamChat(context.Background(), userReq())
		ch1 <- streamResult{stream: s, err: err}
	}()
	<-started // g1 已占槽

	ch2 := make(chan streamResult, 1)
	go func() {
		s, err := c.StreamChat(context.Background(), userReq())
		ch2 <- streamResult{stream: s, err: err}
	}()
	// 确定性等待 g2 入队（queue depth 达 1）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.(*client).gate.QueueDepth() == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if d := c.(*client).gate.QueueDepth(); d != 1 {
		t.Fatalf("队列长度 = %d, want 1", d)
	}

	// 槽满 + 队列满，第 3 个调用立即拒绝
	if _, err := c.StreamChat(context.Background(), userReq()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}

	close(release1) // 放行 g1：g1 返回流（主流程已占槽成功），其 Close 释放槽后 g2 才能出队
	r1 := <-ch1
	if r1.err != nil {
		t.Fatalf("g1 应成功, err = %v", r1.err)
	}
	_ = r1.stream.Close() // 释放槽，g2 出队执行

	r2 := <-ch2
	if r2.err != nil {
		t.Fatalf("g2 应成功, err = %v", r2.err)
	}
	_ = r2.stream.Close()
}

func TestClientReleaseOnClose(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	cfg := fastCfg()
	cfg.MaxConcurrency = 1
	cfg.QueueSize = 8
	c := New(cfg, &fakeProvider{mc: clientMC})

	s1, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("首次 StreamChat 错误: %v", err)
	}
	// 槽被占：MaxConcurrency=1 下排队调用等不到槽，短超时 ctx 返回 DeadlineExceeded
	ctxShort, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	if _, err := c.StreamChat(ctxShort, userReq()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("占用期 err = %v, want context.DeadlineExceeded", err)
	}
	cancel()

	if err := s1.Close(); err != nil {
		t.Fatalf("Close 错误: %v", err)
	}
	// Close 已释放槽：新调用立即成功
	s3, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Close 后 StreamChat err = %v, want nil（槽位应已释放）", err)
	}
	s3.Close()
}

// gatedStream 语义：Close 幂等，release 经 sync.Once 恰好执行一次。
func TestGatedStreamReleaseOnce(t *testing.T) {
	var n atomic.Int32
	gs := &gatedStream{Stream: &fakeStream{}, release: func() { n.Add(1) }}
	if err := gs.Close(); err != nil {
		t.Fatalf("Close 错误: %v", err)
	}
	if err := gs.Close(); err != nil {
		t.Fatalf("二次 Close 错误: %v", err)
	}
	if got := n.Load(); got != 1 {
		t.Errorf("release 执行 %d 次, want 1", got)
	}
}

// gatedStream.Rev 收敛：流中非 EOF 错误按生效集分类为领域错误，io.EOF 原样透传。
func TestGatedStreamRecvClassify(t *testing.T) {
	gs := &gatedStream{
		Stream:   &fakeStream{},
		classify: func(err error) error { return classifyError(err, defaultRetryOnStatus) },
	}
	if _, err := gs.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF 应原样透传, got %v", err)
	}

	gs2 := &gatedStream{
		Stream:   &errorStream{err: &httpError{status: 429, message: "rate limit"}},
		classify: func(err error) error { return classifyError(err, defaultRetryOnStatus) },
	}
	_, err := gs2.Recv()
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("流中 429 应收敛为 ErrRateLimited, got %v", err)
	}
	var le *Error
	if !errors.As(err, &le) || le.StatusCode != 429 {
		t.Errorf("收敛体应携带 StatusCode=429")
	}
}

// errorStream 恒返回同一错误的流，供 Recv 收敛测试。
type errorStream struct{ err error }

func (s *errorStream) Recv() (StreamChunk, error) { return StreamChunk{}, s.err }
func (s *errorStream) Close() error               { return nil }
func (s *errorStream) Usage() (int, int)          { return 0, 0 }

// 适配器缓存淘汰：同 provider|baseURL|modelID 换 key 后旧条目被清理，map 不无限增长。
func TestClientAdapterCacheEvictsOldKey(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	fp := &fakeProvider{mc: clientMC}
	c := New(fastCfg(), fp)
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	fp.mc.APIKey = "sk-second"
	s, err = c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	cl := c.(*client)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if len(cl.adapters) != 1 {
		t.Errorf("换 key 后缓存 %d 条, want 1（旧 key 条目应被清理）", len(cl.adapters))
	}
}

// 适配器缓存淘汰（排他语义）：换模型、跨服务商切换后旧条目同样被清理，缓存恒 ≤1 条，
// 已撤销的旧密钥不以明文驻留缓存 key。
func TestClientAdapterCacheEvictsOnModelSwitch(t *testing.T) {
	fa := &fakeAdapter{}
	installProbe(t, fa)
	fp := &fakeProvider{}
	c := New(fastCfg(), fp)

	// 同 provider 换 ModelID/BaseURL
	fp.mc = clientMC
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	next := clientMC
	next.ModelID = "deepseek-reasoner"
	next.BaseURL = "http://127.0.0.1:10"
	next.APIKey = "sk-rotated"
	fp.mc = next
	s, err = c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 跨 provider 切换
	next = clientMC
	next.Provider = "anthropic"
	next.ModelID = "claude-sonnet-4-5"
	next.APIKey = "sk-ant-rotated"
	fp.mc = next
	s, err = c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	cl := c.(*client)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if len(cl.adapters) != 1 {
		t.Errorf("模型切换后缓存 %d 条, want 1（旧条目应被清理）", len(cl.adapters))
	}
}

// --- 日志脱敏（specs §3.3 / §6.1） ---

type captureHandler struct {
	mu  sync.Mutex
	buf strings.Builder
	n   atomic.Int32
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n.Add(1)
	h.buf.WriteString(r.Message + " ")
	r.Attrs(func(a slog.Attr) bool {
		h.buf.WriteString(a.Key + "=" + fmt.Sprint(a.Value.Any()) + " ")
		return true
	})
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestClientLogNoSecret(t *testing.T) {
	h := &captureHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	const secret = "sk-leak-check-9f8e"
	mc := clientMC
	mc.APIKey = secret
	fa := &fakeAdapter{fn: func(call int) (Stream, error) {
		if call >= 2 {
			return nil, &httpError{status: 401, message: "Invalid API key"}
		}
		return &fakeStream{}, nil
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: mc})

	s, err := c.StreamChat(context.Background(), userReq()) // 成功路径，Close 触发完成 INFO
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	s.Close()
	if _, err := c.StreamChat(context.Background(), userReq()); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err) // 失败路径覆盖 ERROR 日志
	}

	if h.n.Load() == 0 {
		t.Fatal("未捕获任何日志，断言空转")
	}
	h.mu.Lock()
	out := h.buf.String()
	h.mu.Unlock()
	if strings.Contains(out, secret) {
		t.Errorf("日志泄露 API Key: %s", out)
	}
	if !strings.Contains(out, "model=deepseek-chat") {
		t.Errorf("完成日志应携带 model 字段: %s", out)
	}
}

// --- applyDefaults（specs §2.2 参数列表） ---

func TestApplyDefaults(t *testing.T) {
	cfg := applyDefaults(Config{})
	if cfg.Timeout != 10*time.Minute {
		t.Errorf("Timeout = %v, want 10min（流式全程超时，参考 SDK 流式建议量级）", cfg.Timeout)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialBackoff != 30*time.Second {
		t.Errorf("InitialBackoff = %v, want 30s", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 120*time.Second {
		t.Errorf("MaxBackoff = %v, want 120s", cfg.MaxBackoff)
	}
	if want := []int{429, 500, 502, 503, 504}; !reflect.DeepEqual(cfg.RetryOnStatus, want) {
		t.Errorf("RetryOnStatus = %v, want %v", cfg.RetryOnStatus, want)
	}
	if cfg.MaxConcurrency != 4 {
		t.Errorf("MaxConcurrency = %d, want 4", cfg.MaxConcurrency)
	}
	if cfg.QueueSize != 1024 {
		t.Errorf("QueueSize = %d, want 1024", cfg.QueueSize)
	}
	if cfg.TokenCounter == nil {
		t.Error("TokenCounter 应兜底默认实现")
	}
}

// 边界：显式非零值不被默认覆盖。
func TestApplyDefaultsKeepsExplicit(t *testing.T) {
	tc := &fakeTokenCounter{}
	in := Config{
		Timeout:        5 * time.Second,
		MaxRetries:     1,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     3 * time.Second,
		RetryOnStatus:  []int{500},
		MaxConcurrency: 2,
		QueueSize:      7,
		TokenCounter:   tc,
	}
	out := applyDefaults(in)
	if out.Timeout != in.Timeout || out.MaxRetries != in.MaxRetries ||
		out.InitialBackoff != in.InitialBackoff || out.MaxBackoff != in.MaxBackoff ||
		out.MaxConcurrency != in.MaxConcurrency || out.QueueSize != in.QueueSize {
		t.Errorf("显式值被覆盖: %+v", out)
	}
	if !reflect.DeepEqual(out.RetryOnStatus, []int{500}) {
		t.Errorf("RetryOnStatus = %v, want [500]", out.RetryOnStatus)
	}
	if out.TokenCounter != tc {
		t.Error("显式 TokenCounter 应保持同一实例")
	}
}

// --- classifyError 直接单测与 sentinel 防污染回归 ---

func TestClassifyErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want *Error
	}{
		{"403 其余 4xx 归未知", &httpError{status: 403, message: "forbidden"}, ErrUnknown},
		{"非 httpError 普通错误归未知", errors.New("boom"), ErrUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := classifyError(tc.in, defaultRetryOnStatus); !errors.Is(err, tc.want) {
				t.Fatalf("classifyError(%v) = %v, want %s", tc.in, err, tc.want.Code)
			}
		})
	}
	if got := classifyError(context.Canceled, defaultRetryOnStatus); !errors.Is(got, context.Canceled) {
		t.Errorf("ctx.Canceled 应透传, got %v", got)
	}
	if got := classifyError(context.DeadlineExceeded, defaultRetryOnStatus); !errors.Is(got, context.DeadlineExceeded) {
		t.Errorf("ctx.DeadlineExceeded 应透传, got %v", got)
	}
	if got := classifyError(ErrContextLengthExceeded, defaultRetryOnStatus); !errors.Is(got, ErrContextLengthExceeded) {
		t.Errorf("上游 *Error 应透传, got %v", got)
	}
	if got := classifyError(nil, defaultRetryOnStatus); got != nil {
		t.Errorf("nil 输入应返回 nil, got %v", got)
	}
}

// warnRetry 仅可重试错误记（specs §6.1）：401 等确定性失败不打 retrying WARN（避免
// 污染 llm_retry_total 指标与误导性 backoff 展示），429 重试路径保持 WARN 条数 = 重试次数。
func TestClientWarnRetryOnlyRetryable(t *testing.T) {
	h := &captureHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })

	// 401：确定性错误，零重试，不应出现 retrying WARN
	fa := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 401, message: "Invalid API key"}
	}}
	installProbe(t, fa)
	c := New(fastCfg(), &fakeProvider{mc: clientMC})
	if _, err := c.StreamChat(context.Background(), userReq()); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}

	// 429：MaxRetries=2 重试耗尽，retrying WARN 应恰 2 条
	fa2 := &fakeAdapter{fn: func(int) (Stream, error) {
		return nil, &httpError{status: 429, message: "rate limit"}
	}}
	installProbe(t, fa2)
	c2 := New(fastCfg(), &fakeProvider{mc: clientMC})
	if _, err := c2.StreamChat(context.Background(), userReq()); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}

	h.mu.Lock()
	out := h.buf.String()
	h.mu.Unlock()
	if n := strings.Count(out, "llm call retrying"); n != 2 {
		t.Errorf("retrying WARN = %d 条, want 2（429 两次重试；401 不记）: %s", n, out)
	}
}

// 终审遗留回归：分类必须复制 sentinel 值，禁止原地修改共享指针（并发数据竞态）。
func TestClassifyErrorSentinelNotMutated(t *testing.T) {
	_ = classifyError(&httpError{status: 401, message: "x"}, defaultRetryOnStatus)
	_ = classifyError(&httpError{status: 429, message: "x"}, defaultRetryOnStatus)
	_ = classifyError(&httpError{status: 400, message: "context_length_exceeded"}, defaultRetryOnStatus)
	_ = classifyError(&httpError{timeout: true}, defaultRetryOnStatus)
	_ = classifyError(&httpError{status: 502, message: "x"}, defaultRetryOnStatus)
	_ = classifyError(&httpError{status: 403, message: "x"}, defaultRetryOnStatus)
	for _, s := range []*Error{ErrAuth, ErrRateLimited, ErrContextLengthExceeded, ErrTimeout, ErrRetryExhausted, ErrUnknown} {
		if s.StatusCode != 0 || s.Msg != "" || s.Retryable {
			t.Errorf("sentinel %s 被污染: %+v", s.Code, s)
		}
	}
	// 复制体仍可被 errors.Is 识别（按 Code 匹配）
	if err := classifyError(&httpError{status: 401}, defaultRetryOnStatus); !errors.Is(err, ErrAuth) {
		t.Errorf("复制体应匹配 sentinel, got %v", err)
	}
}

// Retryable 与实际重试行为同源：自定义 RetryOnStatus 不含 503 时，
// 503 一次未重试即失败，Retryable 必须为 false（不误导调用方走 fallback）。
func TestClassifyErrorRetryableFollowsEffectiveStatuses(t *testing.T) {
	err := classifyError(&httpError{status: 503, message: "x"}, []int{500})
	var le *Error
	if !errors.As(err, &le) {
		t.Fatalf("应为 *Error, got %T", err)
	}
	if le.Retryable {
		t.Error("生效集 [500] 不含 503，Retryable 应为 false")
	}
	err = classifyError(&httpError{status: 503, message: "x"}, defaultRetryOnStatus)
	if !errors.As(err, &le) || !le.Retryable {
		t.Error("默认集含 503，Retryable 应为 true")
	}
}
