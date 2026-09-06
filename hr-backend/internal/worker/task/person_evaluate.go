package task

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/evaluator"
)

// TypePersonEvaluate 单人评估任务类型（03 §3.1）。
const TypePersonEvaluate = "engine:person-evaluate"

// personEvaluateTimeout 任务级超时 1050s（03 §3.3 推导：列表拉取最坏 190s +
// LLM 段最坏 840s + 落库冗余 ≈1035s，1050s 含 15s 冗余）。
const personEvaluateTimeout = 1050 * time.Second

// PersonEvaluateTaskPayload 任务载荷（03 §3.2）。
type PersonEvaluateTaskPayload struct {
	TokenName   string `json:"token_name"`
	PeriodStart int64  `json:"period_start"`
	PeriodEnd   int64  `json:"period_end"`
}

// NewPersonEvaluateHandler 构造评估任务 handler（mux 注册由 NewMux 统一）。
// err==nil 全部不重试（含 Skipped/Reused/failed 降级终态）；err 非 nil 交 Asynq 重试。
func NewPersonEvaluateHandler(ev *evaluator.Evaluator) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p PersonEvaluateTaskPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil || p.TokenName == "" || p.PeriodEnd <= p.PeriodStart {
			// 构造侧确定性错误，重试恒失败，丢弃任务记 ERROR（session_extract 同款）。
			// 日志只记长度与错误码，payload 原文不落（03 §3.3 日志规范）。
			slog.Error("person evaluate payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		_, err := ev.EvaluatePerson(ctx, p.TokenName, activity.Period{Start: p.PeriodStart, End: p.PeriodEnd}, nil)
		return err
	}
}
