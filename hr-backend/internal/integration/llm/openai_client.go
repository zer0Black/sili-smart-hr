package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// openaiAdapter 用 go-openai 实现 OpenAI 兼容协议（deepseek/openai/zhipu/三方代理，specs §3.2 协议兼容性）。
type openaiAdapter struct {
	client *openai.Client
	// httpClient 与 client 内 config.HTTPClient 同一实例，持有引用供超时设置。
	httpClient *http.Client
}

// 编译期断言：openaiAdapter 实现适配器抽象（接口统一定义于 client.go）。
var _ adapter = (*openaiAdapter)(nil)

// newOpenaiAdapter 构造 OpenAI 兼容适配器。
// BaseURL 非空时覆盖默认地址（specs §3.2：地址带 /v1 根路径，SDK 拼 /chat/completions）；
// timeout 承载在 httpClient.Timeout，<=0 表示不限。
func newOpenaiAdapter(cfg ModelConfig, timeout time.Duration) *openaiAdapter {
	oc := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		oc.BaseURL = cfg.BaseURL
	}
	hc := &http.Client{Timeout: timeout}
	oc.HTTPClient = hc
	return &openaiAdapter{client: openai.NewClientWithConfig(oc), httpClient: hc}
}

// StreamChat 发起流式对话补全，返回底座 Stream；SDK 错误统一转为 *httpError 供 client 分类。
func (a *openaiAdapter) StreamChat(ctx context.Context, mc ModelConfig, req ChatRequest) (Stream, error) {
	oaiReq := openai.ChatCompletionRequest{
		Model:         mc.ModelID,
		Messages:      make([]openai.ChatCompletionMessage, 0, len(req.Messages)),
		Stream:        true,
		StreamOptions: &openai.StreamOptions{IncludeUsage: true}, // 流末块携带 Usage（specs §2.3）
		MaxTokens:     req.MaxTokens,
	}
	if req.Temperature != nil {
		oaiReq.Temperature = float32(*req.Temperature)
	}
	for _, m := range req.Messages {
		oaiReq.Messages = append(oaiReq.Messages, openai.ChatCompletionMessage{Role: m.Role, Content: m.Content})
	}
	stream, err := a.client.CreateChatCompletionStream(ctx, oaiReq)
	if err != nil {
		if ctx.Err() != nil { // ctx 先于请求结束，透传 ctx 错误（排队与建立均受 ctx 约束）
			return nil, ctx.Err()
		}
		return nil, translateOpenaiError(err)
	}
	return &openaiStream{ctx: ctx, stream: stream}, nil
}

// openaiStream 包装 go-openai 流，实现底座 Stream。
type openaiStream struct {
	ctx             context.Context // 建流时的 ctx，Recv 阶段判定取消归属
	stream          *openai.ChatCompletionStream
	promptTokens    int
	completionTokns int
}

// 编译期断言：openaiStream 实现底座 Stream。
var _ Stream = (*openaiStream)(nil)

// Recv 读下一块增量；io.EOF 原样返回；携带 Usage 的末块（Choices 为空）收集用量后返回空内容。
// ctx 已取消/超时先于 SDK 错误透传（SDK 把取消吞成传输层错误，靠 ctx 判定归属，specs v1.3）。
func (s *openaiStream) Recv() (StreamChunk, error) {
	resp, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return StreamChunk{}, io.EOF
		}
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return StreamChunk{}, ctxErr
		}
		return StreamChunk{}, translateOpenaiError(err)
	}
	if resp.Usage != nil {
		s.promptTokens = resp.Usage.PromptTokens
		s.completionTokns = resp.Usage.CompletionTokens
	}
	var content string
	if len(resp.Choices) > 0 { // Usage 末块 Choices 为空，判空防越界
		content = resp.Choices[0].Delta.Content
	}
	return StreamChunk{Content: content}, nil
}

func (s *openaiStream) Close() error { return s.stream.Close() }

// Usage 返回流中收集的 token 用量，服务商未返回时为 (0, 0)（specs §2.3「未获取为 0」）。
func (s *openaiStream) Usage() (prompt, completion int) {
	return s.promptTokens, s.completionTokns
}

// translateOpenaiError 把 go-openai 错误转为 *httpError：
// APIError → status + message（context_length_exceeded 前置标记供 client 分类）；
// RequestError → 响应到达但错误体不可解析，保留 status；
// 其余（连接拒绝、DNS、超时等传输层）→ timeout 标记。
// go-openai v1.42.0 的 APIError 无 Retry-After 字段，retryAfter 恒 0，由 retry 指数退避兜底。
func translateOpenaiError(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		msg := apiErr.Message
		if isOpenaiContextLengthCode(apiErr.Code) {
			msg = "context_length_exceeded: " + msg
		}
		return &httpError{status: apiErr.HTTPStatusCode, message: msg}
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return &httpError{status: reqErr.HTTPStatusCode, message: reqErr.Error()}
	}
	return &httpError{timeout: true, message: err.Error()}
}

// isOpenaiContextLengthCode 判定服务商错误码是否为上下文超限（code 类型不定，字符串时比较）。
func isOpenaiContextLengthCode(code any) bool {
	s, ok := code.(string)
	return ok && s == "context_length_exceeded"
}
