package service_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/service"
)

// fakeLLMStream 是 llm.Stream 的假实现：chunks 依次返回，之后 EOF；Close 记录调用。
type fakeLLMStream struct {
	chunks      []llm.StreamChunk
	idx         int
	closeCalled bool
}

func (s *fakeLLMStream) Recv() (llm.StreamChunk, error) {
	if s.idx < len(s.chunks) {
		c := s.chunks[s.idx]
		s.idx++
		return c, nil
	}
	return llm.StreamChunk{}, io.EOF
}

func (s *fakeLLMStream) Close() error      { s.closeCalled = true; return nil }
func (s *fakeLLMStream) Usage() (int, int) { return 3, 2 }

// fakeLLMClient 是 llm.Client 的假实现：记录入参请求，返回预设流或错误。
type fakeLLMClient struct {
	req     llm.ChatRequest
	reqSeen bool
	stream  *fakeLLMStream
	err     error
}

func (c *fakeLLMClient) StreamChat(_ context.Context, req llm.ChatRequest) (llm.Stream, error) {
	c.req, c.reqSeen = req, true
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

// fakeEnabledProvider 是 llm.EnabledModelProvider 的假实现。
type fakeEnabledProvider struct {
	mc  llm.ModelConfig
	err error
}

func (p *fakeEnabledProvider) GetEnabledModel(context.Context) (llm.ModelConfig, error) {
	return p.mc, p.err
}

var (
	_ llm.Client               = (*fakeLLMClient)(nil)
	_ llm.EnabledModelProvider = (*fakeEnabledProvider)(nil)
)

// fakePinger 记录收到的明文密钥并返回预设结果。
type fakePinger struct {
	gotSecret string
	err       error
}

func (p *fakePinger) Ping(_ context.Context, secret string) error {
	p.gotSecret = secret
	return p.err
}

// newProbe 构造真实探测：探测内部不再前置 provider 查询，模型解析收敛在 llm.Client。
// 需要 fakeEnabledProvider 驱动真实模型解析的用例经 realClientFor 包装注入。
func newProbe(c llm.Client, secret *domain.IntegrationSecret, pinger service.ConversationlogPinger) service.DependencyProbe {
	return service.NewRealDependencyProbe(c, &fakeSecretRepo{getSecret: secret}, crypto.DeriveKey("test-key"), pinger)
}

// realClientFor 包装 llm.New 真实客户端，探测经完整链路（模型解析→token 预检→适配器路由）。
func realClientFor(t *testing.T, p llm.EnabledModelProvider) llm.Client {
	t.Helper()
	return llm.New(llm.Config{Timeout: 2 * time.Second, MaxRetries: 0}, p)
}

// TestProbeLLM_NotConfiguredModel：无启用模型 → not_configured_model。
// 错误经真实 StreamChat 的 wrapProviderUnavailable 分类，验证 cause 链穿透仍可识别哨兵。
func TestProbeLLM_NotConfiguredModel(t *testing.T) {
	client := realClientFor(t, &fakeEnabledProvider{err: service.ErrLLMModelNotEnabled})
	probe := newProbe(client, &domain.IntegrationSecret{}, &fakePinger{})

	if got := probe.ProbeLLM(context.Background()); got != service.StatusNotConfiguredModel {
		t.Fatalf("want not_configured_model, got %q", got)
	}
}

// TestProbeLLM_ResolveError：桥接非哨兵错误（如解密失败）→ unreachable。
func TestProbeLLM_ResolveError(t *testing.T) {
	client := realClientFor(t, &fakeEnabledProvider{err: errors.New("decrypt failed")})
	probe := newProbe(client, &domain.IntegrationSecret{}, &fakePinger{})

	if got := probe.ProbeLLM(context.Background()); got != service.StatusUnreachable {
		t.Fatalf("want unreachable, got %q", got)
	}
}

// TestProbeLLM_Reachable：流建立且读完到 EOF → reachable，且流被 Close（释放并发槽）；
// 断言探测请求是最小请求（单条 user 消息、MaxTokens=8）。
func TestProbeLLM_Reachable(t *testing.T) {
	stream := &fakeLLMStream{chunks: []llm.StreamChunk{{Content: "pong"}}}
	client := &fakeLLMClient{stream: stream}
	probe := newProbe(client, &domain.IntegrationSecret{}, &fakePinger{})

	if got := probe.ProbeLLM(context.Background()); got != service.StatusReachable {
		t.Fatalf("want reachable, got %q", got)
	}
	if !client.reqSeen {
		t.Fatal("llm client not called")
	}
	if len(client.req.Messages) != 1 || client.req.Messages[0].Role != "user" {
		t.Fatalf("probe request messages mismatch: %+v", client.req.Messages)
	}
	if client.req.MaxTokens != 8 {
		t.Fatalf("probe MaxTokens want 8, got %d", client.req.MaxTokens)
	}
	if !stream.closeCalled {
		t.Fatal("stream must be closed to release concurrency slot")
	}
}

// TestProbeLLM_StreamError：流建立失败（如 ErrAuth）→ unreachable。
func TestProbeLLM_StreamError(t *testing.T) {
	authErr := *llm.ErrAuth
	client := &fakeLLMClient{err: &authErr}
	probe := newProbe(client, &domain.IntegrationSecret{}, &fakePinger{})

	if got := probe.ProbeLLM(context.Background()); got != service.StatusUnreachable {
		t.Fatalf("want unreachable, got %q", got)
	}
}

// errRecvStream 在 Recv 时返回领域错误，覆盖读取中断路径。
type errRecvStream struct{ closed bool }

func (s *errRecvStream) Recv() (llm.StreamChunk, error) {
	e := *llm.ErrAuth
	return llm.StreamChunk{}, &e
}
func (s *errRecvStream) Close() error      { s.closed = true; return nil }
func (s *errRecvStream) Usage() (int, int) { return 0, 0 }

// streamOverridingClient 强制返回预设流（含读出错的流）。
type streamOverridingClient struct{ stream llm.Stream }

func (c *streamOverridingClient) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.Stream, error) {
	return c.stream, nil
}

var _ llm.Client = (*streamOverridingClient)(nil)

// TestProbeLLM_RecvDomainError：流读取中途收到 ErrAuth → unreachable，流仍被 Close。
func TestProbeLLM_RecvDomainError(t *testing.T) {
	es := &errRecvStream{}
	probe := newProbe(&streamOverridingClient{stream: es}, &domain.IntegrationSecret{}, &fakePinger{})

	if got := probe.ProbeLLM(context.Background()); got != service.StatusUnreachable {
		t.Fatalf("want unreachable, got %q", got)
	}
	if !es.closed {
		t.Fatal("stream must be closed even on recv error")
	}
}

// TestProbeIntegration_NotConfiguredKey：密钥未配置（cipher 空）→ not_configured_key。
func TestProbeIntegration_NotConfiguredKey(t *testing.T) {
	probe := newProbe(&fakeLLMClient{},
		&domain.IntegrationSecret{SecretCipher: ""}, &fakePinger{})

	if got := probe.ProbeIntegration(context.Background()); got != service.StatusNotConfiguredKey {
		t.Fatalf("want not_configured_key, got %q", got)
	}
}

// TestProbeIntegration_Reachable：解密成功且 Ping 通过 → reachable，Ping 收到明文密钥。
func TestProbeIntegration_Reachable(t *testing.T) {
	key := crypto.DeriveKey("test-key")
	cipher, err := crypto.Encrypt(key, "secret-plain")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pinger := &fakePinger{}
	probe := newProbe(&fakeLLMClient{},
		&domain.IntegrationSecret{SecretCipher: cipher}, pinger)

	if got := probe.ProbeIntegration(context.Background()); got != service.StatusReachable {
		t.Fatalf("want reachable, got %q", got)
	}
	if pinger.gotSecret != "secret-plain" {
		t.Fatalf("pinger secret mismatch: %q", pinger.gotSecret)
	}
}

// TestProbeIntegration_Unreachable：Ping 失败 → unreachable。
func TestProbeIntegration_Unreachable(t *testing.T) {
	key := crypto.DeriveKey("test-key")
	cipher, _ := crypto.Encrypt(key, "secret-plain")
	probe := newProbe(&fakeLLMClient{},
		&domain.IntegrationSecret{SecretCipher: cipher}, &fakePinger{err: errors.New("timeout")})

	if got := probe.ProbeIntegration(context.Background()); got != service.StatusUnreachable {
		t.Fatalf("want unreachable, got %q", got)
	}
}

// TestProbeIntegration_DecryptFailed：密文损坏 → unreachable（而非 not_configured_key）。
func TestProbeIntegration_DecryptFailed(t *testing.T) {
	probe := newProbe(&fakeLLMClient{},
		&domain.IntegrationSecret{SecretCipher: "broken"}, &fakePinger{})

	if got := probe.ProbeIntegration(context.Background()); got != service.StatusUnreachable {
		t.Fatalf("want unreachable, got %q", got)
	}
}
