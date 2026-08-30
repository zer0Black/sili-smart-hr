package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// anthropicEvent 一条 Anthropic SSE 事件（事件名 + data JSON）。
type anthropicEvent struct {
	event string
	data  string
}

// mockAnthropicSSE 起一个按序输出 events 的 httptest 服务端，Anthropic SSE 格式（event 行 + data 行）。
func mockAnthropicSSE(t *testing.T, events []anthropicEvent, capture func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, e := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.event, e.data)
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// anthropicMessageStart message_start 事件数据，input_tokens 实际随本事件下发。
func anthropicMessageStart(prompt, completion int) string {
	return fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":%d,"output_tokens":%d}}}`, prompt, completion)
}

// anthropicMessageDelta message_delta 事件数据，usage 为最终累计值（specs §5.2 场景2）。
func anthropicMessageDelta(prompt, completion int) string {
	return fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens_details":{}}}`, prompt, completion)
}

// anthropicTextDelta content_block_delta 事件数据（text_delta 增量）。
func anthropicTextDelta(text string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, strconv.Quote(text))
}

func testAnthropicMC(baseURL string) ModelConfig {
	return ModelConfig{
		Provider: "anthropic",
		ModelID:  "claude-sonnet-4-5",
		BaseURL:  baseURL,
		APIKey:   "sk-ant-test-key",
	}
}

// newAnthropicStream 建立指向 mock 的流，失败即 Fatal。
func newAnthropicStream(t *testing.T, baseURL string, req ChatRequest) Stream {
	t.Helper()
	a := newAnthropicAdapter(testAnthropicMC(baseURL), 0)
	s, err := a.StreamChat(context.Background(), testAnthropicMC(baseURL), req)
	if err != nil {
		t.Fatalf("StreamChat 错误: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func anthropicUserReq() ChatRequest {
	return ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
}

func TestAnthropicStreamAssemble(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("Hello ")},
		{event: "content_block_delta", data: anthropicTextDelta("world")},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	if got := drainAll(t, s); got != "Hello world" {
		t.Errorf("拼装内容 = %q, want %q", got, "Hello world")
	}
}

func TestAnthropicAuthHeader(t *testing.T) {
	var gotKey string
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("ok")},
	}, func(r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
	})
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	if gotKey != "sk-ant-test-key" {
		t.Errorf("x-api-key 头 = %q, want %q", gotKey, "sk-ant-test-key")
	}
}

func TestAnthropicError429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit reached"}}`)
	}))
	defer srv.Close()
	a := newAnthropicAdapter(testAnthropicMC(srv.URL), 0)
	_, err := a.StreamChat(context.Background(), testAnthropicMC(srv.URL), anthropicUserReq())
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if he.status != 429 {
		t.Errorf("status = %d, want 429", he.status)
	}
}

func TestAnthropicContextLength(t *testing.T) {
	// 真实 API 形状：type 为 invalid_request_error（ErrorType 枚举无 context_length_exceeded），
	// 超限只能靠 message 内容识别（官方文档与社区报错均为 "prompt is too long: N > M"）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 200936 tokens > 199999 maximum"}}`)
	}))
	defer srv.Close()
	a := newAnthropicAdapter(testAnthropicMC(srv.URL), 0)
	_, err := a.StreamChat(context.Background(), testAnthropicMC(srv.URL), anthropicUserReq())
	if err == nil {
		t.Fatal("应返回错误")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为 *httpError, 实际 %T: %v", err, err)
	}
	if he.status != 400 {
		t.Errorf("status = %d, want 400", he.status)
	}
	if !strings.Contains(he.message, "context_length_exceeded") {
		t.Errorf("message = %q, 应含 context_length_exceeded 前置标记", he.message)
	}
}

func TestAnthropicUsage(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("hi")},
		{event: "message_delta", data: anthropicMessageDelta(25, 40)},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	p, c := s.Usage()
	if p != 25 || c != 40 {
		t.Errorf("Usage() = (%d, %d), want (25, 40)", p, c)
	}
}

// 边界：官方基础示例的 message_delta.usage 仅含 output_tokens，input_tokens 随
// message_start 下发，只采 message_start 也能拿到完整用量（specs §5.2 场景2）。
func TestAnthropicUsageFromMessageStartOnly(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "message_start", data: anthropicMessageStart(25, 1)},
		{event: "content_block_delta", data: anthropicTextDelta("hi")},
		{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	p, c := s.Usage()
	if p != 25 {
		t.Errorf("promptTokens = %d, want 25（message_start 的 input_tokens 不应丢失）", p)
	}
	if c != 15 {
		t.Errorf("completionTokens = %d, want 15（message_delta 的累计 output_tokens 覆盖）", c)
	}
}

func TestAnthropicMaxTokensDefault(t *testing.T) {
	cases := []struct {
		name      string
		maxTokens int
		want      float64
	}{
		{name: "零值兜底4096", maxTokens: 0, want: 4096},
		{name: "显式2000透传", maxTokens: 2000, want: 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := mockAnthropicSSE(t, []anthropicEvent{
				{event: "content_block_delta", data: anthropicTextDelta("ok")},
			}, func(r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
			})
			req := anthropicUserReq()
			req.MaxTokens = tc.maxTokens
			s := newAnthropicStream(t, srv.URL, req)
			drainAll(t, s)
			if body["max_tokens"] != tc.want {
				t.Errorf("max_tokens = %v, want %v", body["max_tokens"], tc.want)
			}
		})
	}
}

func TestAnthropicEOF(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("x")},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("流结束后 Recv 应返回 io.EOF, 实际 %v", err)
	}
}

// --- 边界与异常补充 ---

// 边界：system 消息转 System 块、user/assistant 进 Messages、model 与 temperature 透传（specs §2.4 能力1）。
func TestAnthropicRequestShape(t *testing.T) {
	var body map[string]any
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("ok")},
	}, func(r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	})
	temp := 0.7
	s := newAnthropicStream(t, srv.URL, ChatRequest{
		Messages: []ChatMessage{
			{Role: "system", Content: "你是人才测评专家"},
			{Role: "user", Content: "u"},
			{Role: "assistant", Content: "a"},
		},
		Temperature: &temp,
		MaxTokens:   2000,
	})
	drainAll(t, s)
	if body["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v, want claude-sonnet-4-5", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	sys, _ := body["system"].([]any)
	if len(sys) != 1 {
		t.Fatalf("system 长度 = %d, want 1", len(sys))
	}
	first, _ := sys[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "你是人才测评专家" {
		t.Errorf("system 块 = %v, want {type:text text:你是人才测评专家}", first)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages 长度 = %d, want 2（system 不进 messages）", len(msgs))
	}
	u, _ := msgs[0].(map[string]any)
	if u["role"] != "user" {
		t.Errorf("首条 role = %v, want user", u["role"])
	}
	a, _ := msgs[1].(map[string]any)
	if a["role"] != "assistant" {
		t.Errorf("次条 role = %v, want assistant", a["role"])
	}
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}
}

// 边界：Temperature 为 nil 时不设温度（specs §2.2，服务商默认）。
func TestAnthropicTemperatureOmitted(t *testing.T) {
	var body map[string]any
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("ok")},
	}, func(r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	})
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	if _, ok := body["temperature"]; ok {
		t.Errorf("temperature 不应设置, got %v", body["temperature"])
	}
}

// 边界：流中未出现 message_delta 时 Usage 为 (0, 0)（specs §2.3「未获取为 0」）。
func TestAnthropicUsageZero(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("hi")},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	drainAll(t, s)
	p, c := s.Usage()
	if p != 0 || c != 0 {
		t.Errorf("Usage() = (%d, %d), want (0, 0)", p, c)
	}
}

// 边界：服务端不可达属传输层错误，标记 timeout 供 retry 分类重试。
func TestAnthropicTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // 立即关闭制造连接拒绝
	a := newAnthropicAdapter(testAnthropicMC(srv.URL), 0)
	_, err := a.StreamChat(context.Background(), testAnthropicMC(srv.URL), anthropicUserReq())
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
func TestAnthropicContextCanceled(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("x")},
	}, nil)
	a := newAnthropicAdapter(testAnthropicMC(srv.URL), 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.StreamChat(ctx, testAnthropicMC(srv.URL), anthropicUserReq())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// 边界：Recv 阶段 ctx 超时透传 ctx 错误而非误分类 ErrTimeout（SDK 把取消吞成传输层
// 错误，靠 ctx 判定归属，specs v1.3「ctx 取消优先透传」）。
func TestAnthropicRecvCtxDeadline(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", anthropicTextDelta("x"))
		flusher.Flush()
		<-block // 发完首块挂住，制造 Recv 阻塞窗口
	}))
	defer srv.Close()
	defer close(block)

	a := newAnthropicAdapter(testAnthropicMC(srv.URL), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s, err := a.StreamChat(ctx, testAnthropicMC(srv.URL), anthropicUserReq())
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

// 边界：流中途 SSE error 事件（richErrorDecoder 转 apierror）转 httpError。
func TestAnthropicStreamMidError(t *testing.T) {
	srv := mockAnthropicSSE(t, []anthropicEvent{
		{event: "content_block_delta", data: anthropicTextDelta("partial")},
		{event: "error", data: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`},
	}, nil)
	s := newAnthropicStream(t, srv.URL, anthropicUserReq())
	for {
		_, err := s.Recv()
		if err == nil {
			continue
		}
		var he *httpError
		if !errors.As(err, &he) {
			t.Fatalf("流中错误应为 *httpError, 实际 %T: %v", err, err)
		}
		if !strings.Contains(he.message, "Overloaded") {
			t.Errorf("message = %q, 应含 Overloaded", he.message)
		}
		break
	}
}

// 边界：BaseURL 为空时省略 WithBaseURL 走服务商默认（specs §3.2），不发真实请求仅断言构造。
func TestAnthropicDefaultBaseURL(t *testing.T) {
	a := newAnthropicAdapter(ModelConfig{Provider: "anthropic", ModelID: "claude-sonnet-4-5", APIKey: "sk-ant-x"}, 0)
	if len(a.client.Options) == 0 { // WithAPIKey 至少追加一项，证明客户端构造成功
		t.Fatal("client Options 不应为空")
	}
}

// 编译期断言：anthropicStream 实现底座 Stream。
var _ Stream = (*anthropicStream)(nil)
