package task

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/engine/questiongen"
)

// TypeQuestionGenerate 题库生成任务类型，payload 携生成会话主键（specs 4.3.3）。
const TypeQuestionGenerate = "questionbank:generate"

// questionGenerateTimeout 任务级超时单点声明：满额预算 30 题×(LLM 120s×2 重试
// +退避)≈125min，留余量取 2h；超时 ctx 取消走 FAILED 整批作废（specs 4.3.4
// 规则 1）。调整 providers.go NewQuestionGenLLMClient 参数须同步该处。
const questionGenerateTimeout = 2 * time.Hour

// QuestionGeneratePayload 任务载荷：generation_id 为雪花 ID 十进制字符串。
type QuestionGeneratePayload struct {
	GenerationID string `json:"generation_id"`
}

// NewQuestionGenerateHandler 构造生成任务 handler（mux 注册由 NewMux 统一，投递归
// service 层）。坏格式/非正数 generation_id 属构造侧确定性错误，丢弃任务记 ERROR
// （batch_run 同款）；gen.Run 业务侧已终态化（FAILED/CANCELED）一律返回 nil 任务
// 不重试，仅 Run 上抛的基础设施错误透传交 Asynq 重试（MaxRetry(3) 的适用面是
// 进程崩溃等基础设施级失败，重投后 MarkRunning 见 ErrNotQueued 直接返回 nil）。
func NewQuestionGenerateHandler(gen *questiongen.Generator) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p QuestionGeneratePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			slog.Error("question generate payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		generationID, err := strconv.ParseInt(p.GenerationID, 10, 64)
		if err != nil || generationID <= 0 {
			slog.Error("question generate payload generation_id invalid, discard task",
				"generation_id", p.GenerationID, "err", err)
			return nil
		}
		return gen.Run(ctx, generationID)
	}
}
