package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// anthropicMaxTokensFallback Anthropic 协议 max_tokens 必填，req 零值时兜底（specs §2.2）。
const anthropicMaxTokensFallback = 4096

// anthropicAdapter 用 anthropic-sdk-go 实现 Anthropic Messages 协议（specs §3.2 协议兼容性）。
type anthropicAdapter struct {
	client anthropic.Client
}

// 编译期断言：anthropicAdapter 实现适配器抽象（接口统一定义于 client.go）。
var _ adapter = (*anthropicAdapter)(nil)

// newAnthropicAdapter 构造 Anthropic 适配器。
// BaseURL 为空时省略 WithBaseURL 走服务商默认（specs §3.2：地址留空走服务商默认）；
// timeout 经 WithRequestTimeout 承载，<=0 时省略该选项走 SDK 默认；
// WithMaxRetries(0) 关闭 SDK 内置重试（默认 2，重试集含 409 与全部 5xx），
// 重试统一由底座 retryWithBackoff 单层编排，避免叠加放大请求次数与退避时长。
func newAnthropicAdapter(cfg ModelConfig, timeout time.Duration) *anthropicAdapter {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(0),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if timeout > 0 {
		opts = append(opts, option.WithRequestTimeout(timeout))
	}
	return &anthropicAdapter{client: anthropic.NewClient(opts...)}
}

// StreamChat 发起流式对话补全，返回底座 Stream；SDK 错误统一转为 *httpError 供 client 分类。
// SDK v1.47.0 流式方法为 client.Messages.NewStreaming，返回 ssestream.Stream（事件 variant
// 为 ContentBlockDeltaEvent/MessageDeltaEvent，与计划的 stream.MessageStream 描述等价）。
func (a *anthropicAdapter) StreamChat(ctx context.Context, mc ModelConfig, req ChatRequest) (Stream, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(mc.ModelID),
		MaxTokens: int64(req.MaxTokens),
	}
	if params.MaxTokens == 0 { // Anthropic 协议 max_tokens 必填，0 时兜底（OpenAI 路径 0 透传走服务商默认）
		params.MaxTokens = anthropicMaxTokensFallback
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system": // system 消息转 System 文本块，不进 Messages（specs §2.4 能力1）
			params.System = append(params.System, anthropic.TextBlockParam{Text: m.Content})
		case "assistant":
			params.Messages = append(params.Messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)},
			})
		default: // user 及未知角色按 user 归位，保持消息序列不丢
			params.Messages = append(params.Messages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)},
			})
		}
	}
	stream := a.client.Messages.NewStreaming(ctx, params)
	if err := stream.Err(); err != nil {
		if ctx.Err() != nil { // ctx 先于请求结束，透传 ctx 错误（排队与建立均受 ctx 约束）
			return nil, ctx.Err()
		}
		return nil, translateAnthropicError(err)
	}
	return &anthropicStream{ctx: ctx, stream: stream}, nil
}

// anthropicStream 包装 anthropic-sdk-go 消息流，实现底座 Stream。
type anthropicStream struct {
	ctx             context.Context // 建流时的 ctx，Recv 阶段判定取消归属
	stream          *ssestream.Stream[anthropic.MessageStreamEventUnion]
	promptTokens    int
	completionTokns int
}

// 编译期断言：anthropicStream 实现底座 Stream。
var _ Stream = (*anthropicStream)(nil)

// Recv 读下一块增量；text_delta 取 Text 作为本块增量，message_start 与 message_delta
// 累计 Usage（input_tokens 实际随 message_start 下发，message_delta.usage 官方示例仅含
// output_tokens，两处都采，累计语义下覆盖为同一真值）；流正常结束返回 io.EOF。
// ctx 已取消/超时先于 SDK 错误透传（SDK 把取消吞成传输层错误，靠 ctx 判定归属，specs v1.3）。
func (s *anthropicStream) Recv() (StreamChunk, error) {
	for s.stream.Next() {
		switch variant := s.stream.Current().AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			if variant.Delta.Type == "text_delta" {
				return StreamChunk{Content: variant.Delta.Text}, nil
			}
		case anthropic.MessageStartEvent:
			s.promptTokens = int(variant.Message.Usage.InputTokens)
			s.completionTokns = int(variant.Message.Usage.OutputTokens)
		case anthropic.MessageDeltaEvent:
			if variant.Usage.InputTokens > 0 { // 官方基础示例 delta 仅含 output_tokens，0 表未下发不覆盖
				s.promptTokens = int(variant.Usage.InputTokens)
			}
			s.completionTokns = int(variant.Usage.OutputTokens)
		}
	}
	if err := s.stream.Err(); err != nil {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return StreamChunk{}, ctxErr
		}
		return StreamChunk{}, translateAnthropicError(err)
	}
	return StreamChunk{}, io.EOF
}

func (s *anthropicStream) Close() error { return s.stream.Close() }

// Usage 返回从 message_start/message_delta 累计的 token 用量，服务商未返回时为 (0, 0)（specs §2.3「未获取为 0」）。
func (s *anthropicStream) Usage() (prompt, completion int) {
	return s.promptTokens, s.completionTokns
}

// anthropicContextLengthMarkers Anthropic 超限错误的消息片段。API 真实返回
// invalid_request_error（ErrorType 枚举无 context_length_exceeded），超限只能按 message 内容识别。
var anthropicContextLengthMarkers = []string{"prompt is too long", "context_length_exceeded"}

// translateAnthropicError 把 anthropic-sdk-go 错误转为 *httpError：
// *anthropic.Error → status + RawJSON（SDK v1.47.0 的 Error 无嵌套 Error.Message 字段，
// 原始错误体经 RawJSON 透出，超限时前置标记 context_length_exceeded 供 client 分类）；
// 其余（连接拒绝、DNS、超时等传输层）→ timeout 标记。
// SDK 无 Retry-After 解析，retryAfter 恒 0，由 retry 指数退避兜底。
func translateAnthropicError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		msg := apiErr.RawJSON()
		for _, marker := range anthropicContextLengthMarkers {
			if strings.Contains(msg, marker) {
				msg = "context_length_exceeded: " + msg
				break
			}
		}
		return &httpError{status: apiErr.StatusCode, message: msg}
	}
	return &httpError{timeout: true, message: err.Error()}
}
