package task

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/engine/extractor"
)

// TypeSessionExtract 是单会话抽取任务类型（一会话一任务，03 §3.1）。
const TypeSessionExtract = "engine:session-extract"

// sessionExtractTimeout 任务级超时单点声明（providers.go NewExtractorLLMClient
// 注释口径对齐此处）：LLM 最坏 360s（429 Retry-After 封顶 60s 计入的双调用）
// + 详情拉取最坏 190s = 550s，另加 50s 覆盖裁剪脱敏与落库——零冗余会让最坏
// 会话在落库阶段被 deadline 取消进入恒失败重试。
const sessionExtractTimeout = 600 * time.Second

// ExtractTaskPayload 任务载荷（03_api_interface.md §3.2）。
type ExtractTaskPayload struct {
	SessionKey string `json:"session_key"`
	TokenName  string `json:"token_name"`
}

// NewSessionExtractHandler 构造抽取任务 handler（mux 注册由 NewMux 统一）。
// err==nil 全部不重试（含 Skipped 与 failed 终态）；err 非 nil 交 Asynq 重试（03 §3.3）。
// Skipped 日志由组件侧 persistSkipped/reuseTerminal 单点承载，此处不重复记。
func NewSessionExtractHandler(ext *extractor.Extractor) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p ExtractTaskPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			// 坏格式 payload 属构造侧确定性错误，重试恒失败，丢弃任务记 ERROR。
			// 日志白名单只记长度与错误，payload 原文不落（03 §3.3 禁输出规约）。
			slog.Error("session extract payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		if p.SessionKey == "" || p.TokenName == "" {
			slog.Error("session extract payload empty, discard task",
				"session_key", p.SessionKey, "token_name", p.TokenName)
			return nil
		}
		_, err := ext.ExtractByKey(ctx, p.SessionKey, p.TokenName)
		return err
	}
}
