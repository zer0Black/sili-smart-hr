package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestOpenaiAdapter 构造指向 mock 服务端的适配器。
func newTestOpenaiAdapter(t *testing.T, cfg ModelConfig) *openaiAdapter {
	t.Helper()
	return newOpenaiAdapter(cfg, 0)
}

// mockSSE 返回一个依次输出 chunks SSE 块再收 [DONE] 的 httptest 服务端。
func mockSSE(t *testing.T, chunks []string, capture func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// drainAll 读完流并返回拼装内容。
func drainAll(t *testing.T, s Stream) string {
	t.Helper()
	var sb strings.Builder
	for {
		chunk, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv 意外错误: %v", err)
		}
		sb.WriteString(chunk.Content)
	}
	return sb.String()
}

func testMC(baseURL string) ModelConfig {
	return ModelConfig{
		Provider: "deepseek",
		ModelID:  "deepseek-chat",
		BaseURL:  baseURL,
		APIKey:   "sk-test-key",
	}
}

func TestOpenaiStreamAssemble(t *testing.T) {
	srv := mockSSE(t, []string{
		`{"choices":[{"delta":{"content":"Hello "}}]}`,
		`{"choices":[{"delta":{"content":"world"}}]}`,
	}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	if got := drainAll(t, s); got != "Hello world" {
		t.Errorf("拼装内容 = %q, want %q", got, "Hello world")
	}
}

func TestOpenaiAuthHeader(t *testing.T) {
	var gotAuth string
	srv := mockSSE(t, []string{`{"choices":[{"delta":{"content":"ok"}}]}`}, func(r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	})
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("Authorization 头 = %q, want %q", gotAuth, "Bearer sk-test-key")
	}
}

func TestOpenaiError401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	_, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if he.status != 401 {
		t.Errorf("status = %d, want 401", he.status)
	}
}

func TestOpenaiContextLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"This model's maximum context length is 4096 tokens","code":"context_length_exceeded"}}`)
	}))
	defer srv.Close()
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	_, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if !strings.Contains(he.message, "context_length_exceeded") {
		t.Errorf("message = %q, 应含 context_length_exceeded", he.message)
	}
}

func TestOpenaiUsageCollected(t *testing.T) {
	srv := mockSSE(t, []string{
		`{"choices":[{"delta":{"content":"hi"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}`,
	}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	p, c := s.Usage()
	if p != 12 || c != 34 {
		t.Errorf("Usage() = (%d, %d), want (12, 34)", p, c)
	}
}

func TestOpenaiUsageZero(t *testing.T) {
	srv := mockSSE(t, []string{
		`{"choices":[{"delta":{"content":"hi"}}]}`,
	}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	p, c := s.Usage()
	if p != 0 || c != 0 {
		t.Errorf("Usage() = (%d, %d), want (0, 0)", p, c)
	}
}

func TestOpenaiEOF(t *testing.T) {
	srv := mockSSE(t, []string{`{"choices":[{"delta":{"content":"x"}}]}`}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("流结束后 Recv 应返回 io.EOF, 实际 %v", err)
	}
}

// --- 边界与异常补充 ---

// 边界：请求体应携带 stream:true、stream_options.include_usage:true、model 与 messages（specs §2.4 能力1、§2.3）。
func TestOpenaiRequestBody(t *testing.T) {
	var body map[string]any
	srv := mockSSE(t, []string{`{"choices":[{"delta":{"content":"ok"}}]}`}, func(r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	})
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	temp := 0.7
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{
		Messages:    []ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}},
		Temperature: &temp,
		MaxTokens:   2000,
	})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	if body["model"] != "deepseek-chat" {
		t.Errorf("model = %v, want deepseek-chat", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	so, _ := body["stream_options"].(map[string]any)
	if so == nil || so["include_usage"] != true {
		t.Errorf("stream_options.include_usage = %v, want true", body["stream_options"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages 长度 = %d, want 2", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "s" {
		t.Errorf("首条消息 = %v, want {role:system content:s}", first)
	}
	if body["max_tokens"] != float64(2000) {
		t.Errorf("max_tokens = %v, want 2000", body["max_tokens"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}
}

// 边界：Temperature 为 nil 时不设温度（服务商默认），MaxTokens 0 透传（specs §2.2）。
func TestOpenaiRequestOptionalFields(t *testing.T) {
	var body map[string]any
	srv := mockSSE(t, []string{`{"choices":[{"delta":{"content":"ok"}}]}`}, func(r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	})
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "u"}},
	})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	drainAll(t, s)
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature 不应设置, got %v", body["temperature"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("max_tokens 不应设置, got %v", body["max_tokens"])
	}
}

// 边界：429 错误转 httpError 且 retryAfter 为 0（SDK 未暴露 Retry-After 时由指数退避兜底）。
func TestOpenaiError429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"Rate limit reached","type":"requests"}}`)
	}))
	defer srv.Close()
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	_, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if he.status != 429 {
		t.Errorf("status = %d, want 429", he.status)
	}
	if he.retryAfter != 0 {
		t.Errorf("retryAfter = %v, want 0", he.retryAfter)
	}
}

// 边界：5xx 非 JSON 错误体转 httpError 且 status 正确。
func TestOpenaiError500NonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal server error")
	}))
	defer srv.Close()
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	_, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if he.status != 500 {
		t.Errorf("status = %d, want 500", he.status)
	}
}

// 边界：服务端不可达属传输层错误，标记 timeout 供 retry 分类重试。
func TestOpenaiTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 立即关闭制造连接拒绝
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	_, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if !he.timeout {
		t.Errorf("传输层错误应标记 timeout=true, got %+v", he)
	}
}

// 边界：ctx 取消时返回 ctx 错误（排队与流建立受 ctx 约束，specs §2.4 能力4）。
func TestOpenaiContextCanceled(t *testing.T) {
	srv := mockSSE(t, []string{`{"choices":[{"delta":{"content":"x"}}]}`}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.StreamChat(ctx, testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// 边界：Recv 阶段 ctx 超时透传 ctx 错误而非误分类 ErrTimeout（SDK 把取消吞成传输层
// 错误，靠 ctx 判定归属，specs v1.3「ctx 取消优先透传」）。
func TestOpenaiRecvCtxDeadline(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"x"}}]}`)
		flusher.Flush()
		<-block // 发完首块挂住，制造 Recv 阻塞窗口
	}))
	defer srv.Close()
	defer close(block)

	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s, err := a.StreamChat(ctx, testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("建立流: %v", err)
	}
	defer s.Close()
	if _, err := s.Recv(); err != nil {
		t.Fatalf("首块: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_, err = s.Recv()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Recv err = %v, want context.DeadlineExceeded（不得误分类 ErrTimeout）", err)
	}
}

// 边界：流中途 APIError（SSE 内嵌 error）转 httpError。
func TestOpenaiStreamMidError(t *testing.T) {
	srv := mockSSE(t, []string{
		`{"choices":[{"delta":{"content":"partial"}}]}`,
		`{"error":{"message":"insufficient quota","code":"insufficient_quota"}}`,
	}, nil)
	a := newTestOpenaiAdapter(t, testMC(srv.URL))
	s, err := a.StreamChat(context.Background(), testMC(srv.URL), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	defer s.Close()
	for {
		_, err := s.Recv()
		if err == nil {
			continue
		}
		var he *httpError
		if !errors.As(err, &he) {
			t.Fatalf("流中错误应为 *httpError, 实际 %T: %v", err, err)
		}
		if !strings.Contains(he.message, "insufficient quota") {
			t.Errorf("message = %q, 应含 insufficient quota", he.message)
		}
		break
	}
}

// 边界：BaseURL 为空时走 SDK 默认地址（https://api.openai.com/v1），不发真实请求仅断言构造。
func TestOpenaiDefaultBaseURL(t *testing.T) {
	a := newOpenaiAdapter(ModelConfig{Provider: "openai", ModelID: "gpt-4o-mini", APIKey: "sk-x"}, 0)
	if a.client == nil {
		t.Fatal("client 不应为 nil")
	}
}

// 边界：HTTP 超时设置生效（Timeout 通过 HTTPClient.Timeout 承载）。
func TestOpenaiTimeoutConfig(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)
	cfg := testMC(srv.URL)
	a := newOpenaiAdapter(cfg, 0)
	a.httpClient.Timeout = 100 * time.Millisecond
	_, err := a.StreamChat(context.Background(), cfg, ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if !he.timeout {
		t.Errorf("超时应标记 timeout=true, got %+v", he)
	}
}

// 编译期断言：openaiStream 实现底座 Stream。
var _ Stream = (*openaiStream)(nil)
