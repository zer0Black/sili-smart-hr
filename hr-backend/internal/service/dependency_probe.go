package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// llmProbeTimeout 单次大模型探测超时上限 10 秒（specs 03 §3.4 只读约束：
// 避免健康测试触发模型侧 429；底座重试退避 30s 起步，ctx 会先杀掉等待，总耗时收敛于此）。
const llmProbeTimeout = 10 * time.Second

// realDependencyProbe 是 config 落地后的真实外部依赖探测（specs 03 §3.4 处理流程 4/5 步），
// 替换 NoopDependencyProbe 接入 wire。探测只读，不修改任何配置与数据（specs §5.4.4 规则1）。
type realDependencyProbe struct {
	provider   llm.EnabledModelProvider
	llmClient  llm.Client
	secretRepo repository.IntegrationSecretRepository
	encKey     []byte
	convLog    ConversationlogPinger
}

var _ DependencyProbe = (*realDependencyProbe)(nil)

// NewRealDependencyProbe 注入启用模型桥接、LLM 底座客户端、集成密钥仓库、AES 密钥与会话日志探活。
func NewRealDependencyProbe(
	provider llm.EnabledModelProvider,
	llmClient llm.Client,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	convLog ConversationlogPinger,
) DependencyProbe {
	return &realDependencyProbe{
		provider:   provider,
		llmClient:  llmClient,
		secretRepo: secretRepo,
		encKey:     encKey,
		convLog:    convLog,
	}
}

// ProbeLLM 读启用模型发一次最小流式请求（specs 03 §3.4 流程 4）：
// 未启用 → not_configured_model；建立/读取失败 → unreachable；成功 → reachable。
// 走生产 llm.Client 完整链路（模型解析、并发闸、适配器路由），探测即真实调用的抽样。
func (p *realDependencyProbe) ProbeLLM(ctx context.Context) string {
	mc, err := p.provider.GetEnabledModel(ctx)
	if err != nil {
		if errors.Is(err, ErrLLMModelNotEnabled) {
			return StatusNotConfiguredModel
		}
		slog.Error("llm probe resolve enabled model failed", "err", err)
		return StatusUnreachable
	}

	probeCtx, cancel := context.WithTimeout(ctx, llmProbeTimeout)
	defer cancel()

	stream, err := p.llmClient.StreamChat(probeCtx, llm.ChatRequest{
		Messages:  []llm.ChatMessage{{Role: "user", Content: "ping"}},
		MaxTokens: 8,
	})
	if err != nil {
		slog.Warn("llm probe unreachable", "code", llmErrCode(err), "model", mc.ModelID)
		return StatusUnreachable
	}
	defer stream.Close()

	for {
		_, rerr := stream.Recv()
		if rerr == nil {
			continue
		}
		if errors.Is(rerr, io.EOF) {
			return StatusReachable
		}
		slog.Warn("llm probe stream failed", "code", llmErrCode(rerr), "model", mc.ModelID)
		return StatusUnreachable
	}
}

// ProbeIntegration 读集成密钥发一次鉴权探测（specs 03 §3.4 流程 5）：
// 未配置 → not_configured_key；解密失败或 Ping 失败 → unreachable；成功 → reachable。
// 密钥解析走 ResolveIntegrationSecret 收敛点，未配置经哨兵 ErrIntegrationSecretNotConfigured 判定。
func (p *realDependencyProbe) ProbeIntegration(ctx context.Context) string {
	plaintext, err := ResolveIntegrationSecret(ctx, p.secretRepo, p.encKey)
	if err != nil {
		if errors.Is(err, ErrIntegrationSecretNotConfigured) {
			return StatusNotConfiguredKey
		}
		slog.Error("integration probe resolve secret failed", "err", err)
		return StatusUnreachable
	}
	if perr := p.convLog.Ping(ctx, plaintext); perr != nil {
		slog.Warn("integration probe unreachable", "err", perr)
		return StatusUnreachable
	}
	return StatusReachable
}

// llmErrCode 提取底座领域错误 Code 供日志定位（ErrAuth/ErrRateLimited 等），
// 不携带错误消息原文以防密钥意外泄露，模型名与 code 已足够诊断。
func llmErrCode(err error) string {
	var le *llm.Error
	if errors.As(err, &le) {
		return le.Code
	}
	return "unknown"
}
