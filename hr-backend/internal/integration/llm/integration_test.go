package llm

// 端到端集成测试（specs §5.2 场景3/4/5，场景1/2 已在 openai/anthropic_client_test.go 覆盖）：
// 用 httptest 起 mock 服务端，经 New(cfg, provider) 全链路（provider → 预检 → 适配器 → gate → retry）
// 驱动真实 SDK 适配器；fake adapter 路径经工厂探针注入。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- 共用基建 ---

// inflightTracker 原子统计 mock 服务端同时在飞请求数与观测峰值（无 -race 环境的确定性计数）。
type inflightTracker struct {
	cur atomic.Int32
	max atomic.Int32
}

func (t *inflightTracker) enter() {
	c := t.cur.Add(1)
	for {
		m := t.max.Load()
		if c <= m || t.max.CompareAndSwap(m, c) {
			return
		}
	}
}

func (t *inflightTracker) exit() { t.cur.Add(-1) }

// drainSafe 读完流返回拼装内容，不在 goroutine 内 Fatal（错误回传主断言）。
func drainSafe(s Stream) (string, error) {
	var sb []byte
	for {
		chunk, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return string(sb), nil
		}
		if err != nil {
			_ = s.Close()
			return string(sb), err
		}
		sb = append(sb, chunk.Content...)
	}
}

// mockReqHit 记录 mock 收到的请求路径与鉴权头（Bearer 与 x-api-key 分别记录，
// anthropic SDK 会同时携带 x-api-key 与可能来自环境的 Authorization，不能拼接判等）。
type mockReqHit struct {
	mu      sync.Mutex
	paths   []string
	bearers map[string]int // Authorization Bearer 值 → 次数
	apiKeys map[string]int // x-api-key 值 → 次数
}

func newMockReqHit() *mockReqHit {
	return &mockReqHit{bearers: map[string]int{}, apiKeys: map[string]int{}}
}

func (h *mockReqHit) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, r.URL.Path)
	h.apiKeys[r.Header.Get("x-api-key")]++
	if b := r.Header.Get("Authorization"); b != "" {
		h.bearers[b]++
	}
}

func (h *mockReqHit) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.paths)
}

func (h *mockReqHit) bearerCount(v string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bearers[v]
}

func (h *mockReqHit) apiKeyCount(v string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.apiKeys[v]
}

func (h *mockReqHit) firstPath() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.paths) == 0 {
		return ""
	}
	return h.paths[0]
}

// mockOpenaiSSE 输出单块 content 后收 [DONE] 的 OpenAI 兼容 SSE mock。
func mockOpenaiSSE(content string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, content)
}

// --- 场景3：Retry-After 实测（specs §5.2 场景3 / [TS1]） ---

// TestIntegrationRetryAfter 双路径：
// a) 真实 SDK + httptest：mock 首次 429、二次 SSE → 指数退避重试后整体成功、拼装完整
//
//	（go-openai v1.42.0 不暴露 Retry-After 头，真实路径由指数退避兜底）；
//
// b) 工厂探针 + fake adapter：返回 &httpError{status:429, retryAfter:50ms} →
//
//	重试编排按 Retry-After 等待后成功，且不触发指数退避（InitialBackoff=600ms 作对照）。
func TestIntegrationRetryAfter(t *testing.T) {
	t.Run("real 429 then SSE backoff retry", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hits.Add(1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":{"message":"Rate limit reached","type":"requests"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			fmt.Fprintf(w, "data: %s\n\n", mockOpenaiSSE("recovered"))
			flusher.Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer srv.Close()

		c := New(fastCfg(), &fakeProvider{mc: testMC(srv.URL)}) // 真实工厂，真实 SDK 指 mock
		s, err := c.StreamChat(context.Background(), userReq())
		if err != nil {
			t.Fatalf("429 重试后 StreamChat 应成功, err = %v", err)
		}
		defer s.Close()
		if got := drainAll(t, s); got != "recovered" {
			t.Errorf("拼装内容 = %q, want %q", got, "recovered")
		}
		if n := hits.Load(); n != 2 {
			t.Errorf("mock 收到 %d 次请求, want 2（首次 429 + 重试成功）", n)
		}
	})

	t.Run("fake 429 Retry-After honored without exponential backoff", func(t *testing.T) {
		const retryAfter = 50 * time.Millisecond
		fa := &fakeAdapter{fn: func(call int) (Stream, error) {
			if call == 1 {
				return nil, &httpError{status: 429, message: "rate limit", retryAfter: retryAfter}
			}
			return &fakeStream{}, nil
		}}
		installProbe(t, fa)
		// InitialBackoff 远大于 retryAfter：若误走指数退避，耗时 >= 600ms 即被识破。
		cfg := Config{MaxRetries: 2, InitialBackoff: 600 * time.Millisecond, MaxBackoff: 1200 * time.Millisecond}
		c := New(cfg, &fakeProvider{mc: clientMC})

		start := time.Now()
		s, err := c.StreamChat(context.Background(), userReq())
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Retry-After 等待后 StreamChat 应成功, err = %v", err)
		}
		s.Close()
		if n := fa.calls.Load(); n != 2 {
			t.Errorf("adapter 调用 %d 次, want 2（429 一次 + 重试成功一次）", n)
		}
		if elapsed >= cfg.InitialBackoff {
			t.Errorf("耗时 %v >= InitialBackoff %v，误走了指数退避而非 Retry-After", elapsed, cfg.InitialBackoff)
		}
		if elapsed < retryAfter-10*time.Millisecond {
			t.Errorf("耗时 %v < Retry-After %v，未按服务商标注时长等待", elapsed, retryAfter)
		}
	})
}

// --- 场景4：全局并发实测（specs §5.2 场景4 / [TS2]） ---

// TestIntegrationConcurrency openai 适配器指向恒定延迟 mock，MaxConcurrency=4 并发 6 个 StreamChat：
// mock 统计同时在飞恒 ≤ 4，其余排队，全部完成且内容正确。
func TestIntegrationConcurrency(t *testing.T) {
	const delay = 60 * time.Millisecond
	tr := &inflightTracker{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.enter()
		defer tr.exit()
		time.Sleep(delay) // 恒定延迟拉开在飞窗口
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", mockOpenaiSSE("ok"))
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	cfg := fastCfg()
	cfg.MaxConcurrency = 4
	cfg.QueueSize = 8
	c := New(cfg, &fakeProvider{mc: testMC(srv.URL)})

	const n = 6
	contents := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := c.StreamChat(context.Background(), userReq())
			if err != nil {
				errs[i] = err
				return
			}
			got, err := drainSafe(s)
			contents[i], errs[i] = got, err
			_ = s.Close() // 释放槽，放行排队者
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("第 %d 个调用失败: %v", i+1, errs[i])
			continue
		}
		if contents[i] != "ok" {
			t.Errorf("第 %d 个内容 = %q, want %q", i+1, contents[i], "ok")
		}
	}
	if inFlight := tr.max.Load(); inFlight > 4 {
		t.Errorf("mock 同时在飞峰值 = %d, want ≤ 4", inFlight)
	}
	if inFlight := tr.max.Load(); inFlight < 2 {
		t.Errorf("mock 同时在飞峰值 = %d，恒定延迟下应观测到并发重叠（≥2）", inFlight)
	}
}

// --- 场景5：排他模型切换（specs §5.2 场景5 / [TS3]） ---

// seqProvider 可编程序列 provider：按调用次序返回不同 ModelConfig。
type seqProvider struct {
	mu  sync.Mutex
	seq []ModelConfig
	idx int
}

func (p *seqProvider) GetEnabledModel(context.Context) (ModelConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mc := p.seq[p.idx]
	p.idx++
	return mc, nil
}

// TestIntegrationModelSwitch provider 第一次返回 deepseek、第二次返回 anthropic：
// 两次调用分别命中 openai mock 与 anthropic mock，按协议路径与鉴权头区分断言。
func TestIntegrationModelSwitch(t *testing.T) {
	oaiHit := newMockReqHit()
	oaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oaiHit.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", mockOpenaiSSE("from-openai"))
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer oaiSrv.Close()

	antHit := newMockReqHit()
	antSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		antHit.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicTextDelta("from-anthropic"))
		flusher.Flush()
	}))
	defer antSrv.Close()

	fp := &seqProvider{seq: []ModelConfig{
		{Provider: "deepseek", ModelID: "deepseek-chat", BaseURL: oaiSrv.URL, APIKey: "sk-switch-openai"},
		{Provider: "anthropic", ModelID: "claude-sonnet-4-5", BaseURL: antSrv.URL, APIKey: "sk-ant-switch"},
	}}
	c := New(fastCfg(), fp)

	// 第一次：deepseek → OpenAI 兼容协议
	s1, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("第一次 StreamChat 错误: %v", err)
	}
	if got := drainAll(t, s1); got != "from-openai" {
		t.Errorf("第一次拼装内容 = %q, want %q", got, "from-openai")
	}
	s1.Close()

	// 第二次：anthropic → Anthropic Messages 协议
	s2, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("第二次 StreamChat 错误: %v", err)
	}
	if got := drainAll(t, s2); got != "from-anthropic" {
		t.Errorf("第二次拼装内容 = %q, want %q", got, "from-anthropic")
	}
	s2.Close()

	// 按协议路径与鉴权头区分断言
	if n := oaiHit.count(); n != 1 {
		t.Errorf("openai mock 收到 %d 次, want 1", n)
	}
	if path := oaiHit.firstPath(); path != "/chat/completions" {
		t.Errorf("openai mock 路径 = %q, want /chat/completions", path)
	}
	if n := oaiHit.bearerCount("Bearer sk-switch-openai"); n != 1 {
		t.Errorf("openai mock Authorization Bearer 命中 %d 次, want 1", n)
	}
	if n := antHit.count(); n != 1 {
		t.Errorf("anthropic mock 收到 %d 次, want 1", n)
	}
	if path := antHit.firstPath(); path != "/v1/messages" {
		t.Errorf("anthropic mock 路径 = %q, want /v1/messages", path)
	}
	if n := antHit.apiKeyCount("sk-ant-switch"); n != 1 {
		t.Errorf("anthropic mock x-api-key 命中 %d 次, want 1", n)
	}
}

// --- 全链路流式收口（specs §5.2 场景1 的 client 层复验） ---

// TestIntegrationFullStream 经 New() 全链路指向含 usage 末块的 openai mock：
// 完整 StreamChat 拼装内容完整、Usage() 等于 mock 设定值。
func TestIntegrationFullStream(t *testing.T) {
	srv := mockSSE(t, []string{
		`{"choices":[{"delta":{"content":"Integ"}}]}`,
		`{"choices":[{"delta":{"content":"ration"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":77,"completion_tokens":88}}`,
	}, nil)
	c := New(fastCfg(), &fakeProvider{mc: testMC(srv.URL)})

	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	if got := drainAll(t, s); got != "Integration" {
		t.Errorf("拼装内容 = %q, want %q", got, "Integration")
	}
	if p, c := s.Usage(); p != 77 || c != 88 {
		t.Errorf("Usage() = (%d, %d), want (77, 88)", p, c)
	}
}
