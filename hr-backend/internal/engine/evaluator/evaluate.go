package evaluator

// evaluate.go Evaluate 主流程（specs §2.4 能力1）：口径读取 → 幂等预检 →
// 档案取数 → 签名识别 → 组装与 LLM → 校验收敛 → 脱敏与落库。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
)

// evalMaxTokens 单次评分调用输出上限（逐请求设置）。
const evalMaxTokens = 4000

// llmErrEvalUpstreamCode LLM 评估调用失败的 error_code 落库值（specs §2.3 错误码表）。
const llmErrEvalUpstreamCode = "ErrLLMEvalUpstream"

// schemaErrCode ErrSchemaInvalid 的 error_code 落库值。
const schemaErrCode = "ErrSchemaInvalid"

// evidenceJSON 评分行证据快照（specs §2.3 evidence_json 注释）：周期 success 档案
// session_key 清单 + 统计摘要快照 + 维度口径摘要，规则产出非 LLM 引用。
type evidenceJSON struct {
	SessionKeys   []string                 `json:"session_keys"`
	Summary       map[string]int           `json:"summary"`
	DimensionSpecs []evidenceSpecSnapshot  `json:"dimension_specs"`
	SignatureKind string                   `json:"signature_kind,omitempty"`
}

// evidenceSpecSnapshot 单维度口径摘要：weight 与 in_overview 是聚合唯一权重来源。
type evidenceSpecSnapshot struct {
	Code          string `json:"code"`
	Weight        int    `json:"weight"`
	InOverview    bool   `json:"in_overview"`
	ConfigMissing bool   `json:"config_missing,omitempty"`
}

// Evaluate 对单人周期执行综合评估（specs §2.4 能力1，不含活跃度与聚合）。
// sessions 可空：非空时直传签名识别；空时签名识别退化为仅档案侧判据，不做单人
// 列表拉取兜底。TokenName 组装前剥离（人名不进 LLM 上下文），落库时经 SaveAll 回填。
func (e *Evaluator) Evaluate(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*EvaluateResult, error) {
	specs, err := loadSpecs(ctx, e.specs)
	if err != nil {
		return nil, err
	}

	// 幂等预检（能力6 规则1/2）：全 success 复用跳过 LLM；存在 failed 先删后评。
	reuse, err := e.idempotencyCheck(ctx, tokenName, period, specs)
	if err != nil {
		return nil, err
	}
	if reuse {
		slog.Info("eval reused", "token_name", tokenName, "period_start", period.Start)
		return &EvaluateResult{Reused: true}, nil
	}

	// 活跃度阈值（签名判据入参）：读取失败 wrap ErrDimensionConfigRead。
	_, lowFreq, err := e.thresholds.ActivityThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDimensionConfigRead, err)
	}

	digests, err := fetchDigests(ctx, e.featureRepo, tokenName, period)
	if err != nil {
		return nil, err
	}

	sig := activity.IdentifyPopulation(sessions, digests, lowFreq)
	successCount := 0
	for _, d := range digests {
		if d.Status == domain.FeatureStatusSuccess {
			successCount++
		}
	}

	// 零有效档案与签名命中（bypass_orchestrator / auto_client）同走零 LLM 跳过路径，
	// 全维度落 insufficient 行（specs §2.3 ErrNoValidProfiles：业务态非错误）。
	if successCount < MinValidProfilesForEval ||
		(sig.Kind == activity.KindBypassOrchestrator || sig.Kind == activity.KindAutoClient) {
		slog.Info("eval skipped", "token_name", tokenName, "period_start", period.Start,
			"success_profiles", successCount, "signature_kind", sig.Kind)
		rows := e.skipRows(specs, digests, sig.Kind)
		if err := e.saveRows(ctx, tokenName, period, rows); err != nil {
			return nil, err
		}
		return &EvaluateResult{Scores: viewRows(rows), Skipped: true}, nil
	}

	set := buildProfileSet(digests)
	prompt := buildPrompt(specs, set)

	// 模型解析失败落空串不阻断（评分行 model_name 记空串，调用失败另走降级）。
	modelID := ""
	if mc, err := e.modelProvider.GetEnabledModel(ctx); err == nil {
		modelID = mc.ModelID
	}

	rows, err := e.llmEvaluate(ctx, tokenName, period, prompt, specs, digests, modelID)
	if err != nil {
		return nil, err // ctx 取消类基础设施错误：不落行交任务重试
	}
	if err := e.saveRows(ctx, tokenName, period, rows); err != nil {
		return nil, err
	}
	slog.Debug("eval completed", "token_name", tokenName, "period_start", period.Start,
		"dimensions", len(specs))
	return &EvaluateResult{Scores: viewRows(rows)}, nil
}

// idempotencyCheck 幂等预检（specs §2.3 Evaluate 行 + 能力6 规则1/2）：
// ListByPersonPeriodExact 双界精确读同人同周期全 source 行（含 F7 active_test），
// 按 source=conversation 过滤后判定：全 success 且 dimension_code 集合覆盖当前
// 启用维度集合 → true；存在 failed → DeleteConversationFailed 后继续（返回 false）。
func (e *Evaluator) idempotencyCheck(ctx context.Context, tokenName string, period activity.Period, specs []DimensionSpec) (bool, error) {
	existing, err := e.scoreRepo.ListByPersonPeriodExact(ctx, tokenName, period.Start, period.End)
	if err != nil {
		return false, fmt.Errorf("evaluator: ErrScoreRead: %w", err)
	}
	hasFailed := false
	codes := make(map[string]bool, len(existing))
	for _, r := range existing {
		if r.Source != domain.ScoreSourceConversation {
			continue // active_test 行不参与判定（能力6 规则1）
		}
		if r.Status == domain.ScoreStatusFailed {
			hasFailed = true
		} else if r.Status == domain.ScoreStatusSuccess {
			codes[r.DimensionCode] = true
		}
	}
	if hasFailed {
		if _, err := e.scoreRepo.DeleteConversationFailed(ctx, tokenName, period.Start, period.End); err != nil {
			return false, wrapEvalStoreWrite(tokenName, err)
		}
		return false, nil
	}
	full := len(codes) > 0
	for _, s := range specs {
		if !codes[s.Code] {
			full = false
			break
		}
	}
	return full, nil
}

// skipRows 零 LLM 跳过路径的全维度 insufficient 行（specs §2.4 能力1 与能力4 处置列）：
// Score=0、缺省文案、Source=conversation、Status=success，EvidenceJSON 记签名 kind。
func (e *Evaluator) skipRows(specs []DimensionSpec, digests []activity.ProfileDigest, sigKind string) []domain.DimensionScore {
	ev := buildEvidence(specs, digests, sigKind)
	rows := make([]domain.DimensionScore, 0, len(specs))
	for _, spec := range specs {
		rows = append(rows, domain.DimensionScore{
			DimensionCode: spec.Code,
			Module:        spec.Module,
			Rationale:     insufficientDefaultRationale,
			Insufficient:  true,
			EvidenceJSON:  ev,
			Source:        domain.ScoreSourceConversation,
			PromptVersion: PromptVersion,
			Status:        domain.ScoreStatusSuccess,
		})
	}
	return rows
}

// llmEvaluate LLM 段：StreamChat 流式读全 → parseScoreOutput → validateAndConverge；
// ErrSchemaInvalid 且 ctx 未取消重试一次（extractor llmExtract 同构）；重试耗尽与
// LLM 调用失败落全维度 failed 行（error_code 记因，err=nil）；ctx 取消上抛基础设施
// error 不落行。
func (e *Evaluator) llmEvaluate(ctx context.Context, tokenName string, period activity.Period,
	prompt string, specs []DimensionSpec, digests []activity.ProfileDigest, modelID string) ([]domain.DimensionScore, error) {
	callOnce := func() ([]domain.DimensionScore, error) {
		stream, err := e.llm.StreamChat(ctx, llm.ChatRequest{
			Messages:  []llm.ChatMessage{{Role: "user", Content: prompt}},
			MaxTokens: evalMaxTokens,
		})
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		var b strings.Builder
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			b.WriteString(chunk.Content)
		}
		out, err := parseScoreOutput(b.String())
		if err != nil {
			return nil, err
		}
		view, err := validateAndConverge(out, specs)
		if err != nil {
			return nil, err
		}
		// view 是包内视图，落库需 domain 行（ModelName/PromptVersion/ErrorCode 列
		// 仅 domain 形态承载），此处即转。
		rows := make([]domain.DimensionScore, 0, len(view))
		for _, r := range view {
			rows = append(rows, domain.DimensionScore{
				DimensionCode: r.DimensionCode,
				Module:        r.Module,
				Score:         r.Score,
				Rationale:     r.Rationale,
				Insufficient:  r.Insufficient,
				Source:        r.Source,
				Status:        r.Status,
			})
		}
		return rows, nil
	}

	rows, err := callOnce()
	if err != nil && errors.Is(err, ErrSchemaInvalid) && ctx.Err() == nil {
		slog.Warn("eval schema invalid, retrying", "token_name", tokenName, "period_start", period.Start)
		rows, err = callOnce()
	}
	if err == nil {
		convergeMissingPrompt(specs, rows)
		e.redactRows(ctx, rows)
		ev := buildEvidence(specs, digests, "")
		applyRowMeta(rows, ev, modelID)
		return rows, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("evaluator: llm segment canceled: %w", ctx.Err())
	}
	// 业务降级：落全维度 failed 行（error_code 记因），当期聚合照常推进（BR9）。
	errCode := llmErrEvalUpstreamCode
	if errors.Is(err, llm.ErrContextLengthExceeded) {
		errCode = llm.ErrContextLengthExceeded.Code
	} else if errors.Is(err, ErrSchemaInvalid) {
		errCode = schemaErrCode
	}
	slog.Error("eval failed", "token_name", tokenName, "period_start", period.Start, "code", errCode)
	ev := buildEvidence(specs, digests, "")
	failed := make([]domain.DimensionScore, 0, len(specs))
	for _, spec := range specs {
		failed = append(failed, domain.DimensionScore{
			DimensionCode: spec.Code,
			Module:        spec.Module,
			EvidenceJSON:  ev,
			Source:        domain.ScoreSourceConversation,
			ModelName:     modelID,
			PromptVersion: PromptVersion,
			Status:        domain.ScoreStatusFailed,
			ErrorCode:     errCode,
		})
	}
	return failed, nil
}

// convergeMissingPrompt 确定性收敛（specs §3.2 维度配置缺失行）：PromptText 为空的
// 维度在校验收敛后代码直接强制置 Insufficient=true、Score=0、Rationale=缺省文案。
// prompt 指令段标注仅披露层，LLM 对该维返回的分值一律覆盖。
func convergeMissingPrompt(specs []DimensionSpec, rows []domain.DimensionScore) {
	missing := make(map[string]bool, len(specs))
	for _, s := range specs {
		if s.PromptText == "" {
			missing[s.Code] = true
		}
	}
	if len(missing) == 0 {
		return
	}
	for i := range rows {
		if missing[rows[i].DimensionCode] {
			rows[i].Insufficient = true
			rows[i].Score = 0
			rows[i].Rationale = insufficientDefaultRationale
		}
	}
}

// redactRows 落库前 Redact 兜底（specs §3.3 第二道防线）：读正则（nil 回退出厂集）
// 逐行过 Rationale。
func (e *Evaluator) redactRows(ctx context.Context, rows []domain.DimensionScore) {
	patterns := e.redactPatterns(ctx)
	for i := range rows {
		rows[i].Rationale = extractor.Redact(rows[i].Rationale, patterns)
	}
}

// redactPatterns 读脱敏正则集：DB 故障回退出厂（安全机制不失效，记 WARN）。
func (e *Evaluator) redactPatterns(ctx context.Context) []string {
	loaded, err := e.sysParams.ReadStringArrays(extractor.ParamKeyRedactPatterns)
	if err != nil {
		slog.Warn("read redact patterns failed, fallback to factory set", "err", err)
		return nil
	}
	return loaded[extractor.ParamKeyRedactPatterns]
}

// applyRowMeta 回填行内公共列：evidence_json、model_name、prompt_version
//（token_name 与周期由 SaveAll 权威回填）。
func applyRowMeta(rows []domain.DimensionScore, ev, modelID string) {
	for i := range rows {
		rows[i].EvidenceJSON = ev
		rows[i].ModelName = modelID
		rows[i].PromptVersion = PromptVersion
	}
}

// buildEvidence 构造证据快照（specs §2.3 evidence_json 注释）：session_key 清单
// 取周期 success 档案（与 prompt 证据段同集）、统计摘要快照取 SummaryBlock 关键
// 计数结构化、维度口径摘要含 config_missing 标记。
func buildEvidence(specs []DimensionSpec, digests []activity.ProfileDigest, sigKind string) string {
	keys := make([]string, 0, len(digests))
	summary := make(map[string]int)
	for _, d := range digests {
		summary["sessions_total"]++
		switch d.Status {
		case domain.FeatureStatusSuccess:
			keys = append(keys, d.SessionKey)
			summary["sessions_valid"]++
		case domain.FeatureStatusFailed:
			summary["sessions_valid"]++
			summary["failed_profiles"]++
		case domain.FeatureStatusSkipped:
			summary["sessions_skipped"]++
		}
		summary["user_msg_count"] += d.Stats.UserMsgCount
		summary["interrupt_count"] += d.Stats.InterruptCount
		summary["paste_char_count"] += d.Stats.PasteCharCount
	}
	specSnaps := make([]evidenceSpecSnapshot, 0, len(specs))
	for _, s := range specs {
		specSnaps = append(specSnaps, evidenceSpecSnapshot{
			Code:          s.Code,
			Weight:        s.Weight,
			InOverview:    s.InOverview,
			ConfigMissing: s.PromptText == "",
		})
	}
	raw, err := json.Marshal(evidenceJSON{
		SessionKeys:    keys,
		Summary:        summary,
		DimensionSpecs: specSnaps,
		SignatureKind:  sigKind,
	})
	if err != nil {
		// 纯值 marshal 恒成功，兜底空对象防 evidence 列落空串。
		return "{}"
	}
	return string(raw)
}

// saveRows 落库（specs §2.3 ErrStoreWrite）：SaveAll 失败 wrap 上抛交任务重试。
func (e *Evaluator) saveRows(ctx context.Context, tokenName string, period activity.Period, rows []domain.DimensionScore) error {
	if err := e.scoreRepo.SaveAll(ctx, tokenName, period.Start, period.End, rows); err != nil {
		return wrapEvalStoreWrite(tokenName, err)
	}
	return nil
}

// wrapEvalStoreWrite 包装落库错误（ErrStoreWrite 语义，specs §6.1 ERROR code）。
func wrapEvalStoreWrite(tokenName string, err error) error {
	slog.Error("eval store write failed", "token_name", tokenName, "code", "ErrStoreWrite")
	return fmt.Errorf("%w: %w", ErrStoreWrite, err)
}

// viewRows domain 行转 EvaluateResult.Scores 视图（types.go DimensionScore）。
func viewRows(rows []domain.DimensionScore) []DimensionScore {
	out := make([]DimensionScore, 0, len(rows))
	for _, r := range rows {
		out = append(out, DimensionScore{
			DimensionCode: r.DimensionCode,
			Module:        r.Module,
			Score:         r.Score,
			Rationale:     r.Rationale,
			Insufficient:  r.Insufficient,
			EvidenceJSON:  r.EvidenceJSON,
			Source:        r.Source,
			Status:        r.Status,
		})
	}
	return out
}
