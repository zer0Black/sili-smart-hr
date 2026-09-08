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

// evalMaxTokens 单次评分调用输出上限：按最坏合法输出标定——20 维度 × 容差上限
// 400 字理由（中文 1 字 ≈ 1.5 token）+ JSON 键与结构开销 ≈ 700 token/维 = 14000。
const evalMaxTokens = 14000

// llmErrEvalUpstreamCode LLM 评估调用失败的 error_code 落库值（specs §2.3 错误码表）。
const llmErrEvalUpstreamCode = "ErrLLMEvalUpstream"

// schemaErrCode ErrSchemaInvalid 的 error_code 落库值。
const schemaErrCode = "ErrSchemaInvalid"

// evidenceJSON 评分行证据快照（specs §2.3 evidence_json 注释）：周期 success 档案
// session_key 清单 + 统计摘要快照 + 维度口径摘要，规则产出非 LLM 引用。
type evidenceJSON struct {
	SessionKeys    []string               `json:"session_keys"`
	Summary        map[string]int         `json:"summary"`
	DimensionSpecs []evidenceSpecSnapshot `json:"dimension_specs"`
	SignatureKind  string                 `json:"signature_kind,omitempty"`
}

// evidenceSpecSnapshot 单维度口径摘要：weight 与 in_overview 是聚合唯一权重来源。
type evidenceSpecSnapshot struct {
	Code          string `json:"code"`
	Weight        int    `json:"weight"`
	InOverview    bool   `json:"in_overview"`
	ConfigMissing bool   `json:"config_missing,omitempty"`
}

// Evaluate 对单人周期执行综合评估（specs §2.4 能力1，不含活跃度与聚合）。
// sessions 非空直传签名识别（session_key 去重同 activity 口径），空时退化仅档案判据；
// digests 空（nil）时内部取数；TokenName 组装前剥离，落库时经 SaveAll 回填。
func (e *Evaluator) Evaluate(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary, digests []activity.ProfileDigest) (*EvaluateResult, error) {
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

	// 活跃度阈值（签名判据入参）：适配器双消费方共用返回裸错误，
	// 哨兵由本包在此 wrap（activity 侧同款各自 wrap）。
	_, lowFreq, err := e.thresholds.ActivityThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDimensionConfigRead, err)
	}

	if digests == nil {
		digests, err = activity.FetchWindowDigests(ctx, e.featureRepo, tokenName, period)
		if err != nil {
			return nil, err
		}
	}

	// 签名识别入参去重（跨页重复兜底，与 activity 侧同一实现）。
	sig := activity.IdentifyPopulation(activity.DedupSessions(sessions), digests, lowFreq)
	successCount := 0
	for _, d := range digests {
		if d.Status == domain.FeatureStatusSuccess {
			successCount++
		}
	}

	// 证据快照单点计算：签名 kind 是唯一的路径相关变量，落 LLM 维度行时置空
	//（specs：签名处置经独立通道承载，score 行 evidence 记 normal 口径）。
	ev := buildEvidence(specs, digests)

	// 零有效档案与签名命中（bypass_orchestrator / auto_client）同走零 LLM 跳过路径，
	// 全维度落 insufficient 行（specs §2.3 ErrNoValidProfiles：业务态非错误）。
	if successCount < MinValidProfilesForEval ||
		(sig.Kind == activity.KindBypassOrchestrator || sig.Kind == activity.KindAutoClient) {
		slog.Info("eval skipped", "token_name", tokenName, "period_start", period.Start,
			"success_profiles", successCount, "signature_kind", sig.Kind)
		rows := skipInsufficientRows(specs, ev, sig.Kind, func(DimensionSpec) bool { return true })
		if err := e.saveRows(ctx, tokenName, period, rows); err != nil {
			return nil, err
		}
		return &EvaluateResult{Scores: rows, Skipped: true}, nil
	}

	// 空提示词维度不进 LLM 上下文（specs §3.2 维度配置缺失：不阻断其余维度）：
	// 其 insufficient 行由 skipInsufficientRows 确定性生成。
	scored := scoredPromptSpecs(specs)
	missingPrompt := func(s DimensionSpec) bool { return s.PromptText == "" }
	if len(scored) == 0 {
		// 全维度空提示词：零维度 prompt 无评分意义，直接短路落全量 insufficient 行
		//（DB 直改或存量脏数据绕过 service 校验的防御，免烧空 LLM 调用）。
		rows := skipInsufficientRows(specs, ev, "", missingPrompt)
		if err := e.saveRows(ctx, tokenName, period, rows); err != nil {
			return nil, err
		}
		return &EvaluateResult{Scores: rows}, nil
	}

	set := buildProfileSet(digests)
	slog.Debug("profile set assembled", "token_name", tokenName, "period_start", period.Start,
		"visible", set.VisibleCount, "total_success", set.TotalSuccess,
		"summary_chars", len(set.SummaryBlock))
	if set.VisibleCount < set.TotalSuccess {
		slog.Warn("profile set truncated", "token_name", tokenName, "period_start", period.Start,
			"visible", set.VisibleCount, "total_success", set.TotalSuccess)
	}

	// 模型解析失败落空串不阻断（评分行 model_name 记空串，调用失败另走降级）。
	// 与 StreamChat 内部是两次独立读取，落库值可能偏离实际调用模型，记 WARN 供观测。
	modelID := ""
	if mc, err := e.modelProvider.GetEnabledModel(ctx); err == nil {
		modelID = mc.ModelID
	} else {
		slog.Warn("eval model resolve failed, model_name will be empty", "err", err)
	}

	rows, err := e.llmEvaluate(ctx, tokenName, period, set, scored, ev, modelID)
	if err != nil {
		return nil, err // ctx 取消类基础设施错误：不落行交任务重试
	}
	rows = append(rows, skipInsufficientRows(specs, ev, "", missingPrompt)...)
	if err := e.saveRows(ctx, tokenName, period, rows); err != nil {
		return nil, err
	}
	slog.Debug("eval completed", "token_name", tokenName, "period_start", period.Start,
		"dimensions", len(specs))
	return &EvaluateResult{Scores: rows}, nil
}

// scoredPromptSpecs 有评分提示词的维度（进 LLM 上下文）。
func scoredPromptSpecs(specs []DimensionSpec) []DimensionSpec {
	out := make([]DimensionSpec, 0, len(specs))
	for _, s := range specs {
		if s.PromptText != "" {
			out = append(out, s)
		}
	}
	return out
}

// skipInsufficientRows 零 LLM 路径的确定性 insufficient 行单点：predicate 圈定
// 落行维度（全量 skip 恒 true，空提示词按 PromptText==""），ErrorCode 落
// skip_no_llm 标记走先删后评自愈（skip 行不进复用判定，补全提示词后同款自愈）。
func skipInsufficientRows(specs []DimensionSpec, ev, sigKind string, predicate func(DimensionSpec) bool) []domain.DimensionScore {
	rows := make([]domain.DimensionScore, 0, len(specs))
	for _, spec := range specs {
		if !predicate(spec) {
			continue
		}
		rows = append(rows, domain.DimensionScore{
			DimensionCode: spec.Code,
			Module:        spec.Module,
			Rationale:     insufficientDefaultRationale,
			Insufficient:  true,
			EvidenceJSON:  evidenceWithSignature(ev, sigKind),
			Source:        domain.ScoreSourceConversation,
			PromptVersion: PromptVersion,
			Status:        domain.ScoreStatusSuccess,
			ErrorCode:     domain.ErrorCodeSkipNoLLM,
		})
	}
	return rows
}

// evidenceWithSignature 把签名 kind 写入证据快照：ev 由调用方按 normal 口径
// 单点构造，skip 路径覆写 signature_kind 字段（无需二次序列化整个快照的
// 重新计算，JSON 尾部字段替换即可；sigKind 为空时原样返回）。
func evidenceWithSignature(ev, sigKind string) string {
	if sigKind == "" || sigKind == activity.KindNormal {
		return ev
	}
	return strings.TrimSuffix(ev, "}") + `,"signature_kind":"` + sigKind + `"}`
}

// idempotencyCheck 幂等预检（specs 能力6 规则1/2）：全 success（无 skip_no_llm
// 标记）且启用维度集合覆盖 → 复用；存在 failed/skip 标记/版本落后行 → 先删后评。
// 版本比对只圈当前启用维度行：停用维度残留行不触发重评，按其 evidence 快照
// 口径留任聚合（specs 能力5 规则1），否则每次跑批都因残留旧版本行判定需重评。
func (e *Evaluator) idempotencyCheck(ctx context.Context, tokenName string, period activity.Period, specs []DimensionSpec) (bool, error) {
	existing, err := e.scoreRepo.ListByPersonPeriodExact(ctx, tokenName, period.Start, period.End)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrScoreRead, err)
	}
	enabled := make(map[string]bool, len(specs))
	for _, s := range specs {
		enabled[s.Code] = true
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
			if r.ErrorCode == domain.ErrorCodeSkipNoLLM {
				hasFailed = true // skip 行不可复用：证据面可能已变（抽取后落行），走先删后评
				continue
			}
			if !enabled[r.DimensionCode] {
				continue // 停用维度残留行不进复用集合，也不触发重评
			}
			if r.PromptVersion != PromptVersion {
				hasFailed = true // 模板升级发版：旧版本行经先删后评重落地（非提前返回，
				continue         // 让本周期 failed/skip 残留行一并进入删除清理）
			}
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

// llmEvaluate LLM 段（specs §2.4 能力1）：ErrSchemaInvalid 且 ctx 未取消重试一次
//（extractor llmExtract 同构）；重试耗尽与调用失败落 scored 维度 failed 行
//（error_code 记因，err=nil 终态）；ctx 取消返回基础设施 error 不落行。
// ev 证据快照由调用方单点传入，与 skip 补行同源同口径。
func (e *Evaluator) llmEvaluate(ctx context.Context, tokenName string, period activity.Period,
	set *ProfileSet, scored []DimensionSpec, ev, modelID string) ([]domain.DimensionScore, error) {
	// prompt 由 (scored, set) 决定且重试间不可变，前置到重试单元外算一次。
	prompt := buildPrompt(scored, set)
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
		return validateAndConverge(out, scored)
	}

	rows, err := callOnce()
	if err != nil && errors.Is(err, ErrSchemaInvalid) && ctx.Err() == nil {
		slog.Warn("eval schema invalid, retrying", "token_name", tokenName, "period_start", period.Start)
		rows, err = callOnce()
	}
	if err == nil {
		e.redactRows(ctx, rows)
		applyRowMeta(rows, ev, modelID)
		return rows, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("evaluator: llm segment canceled: %w", ctx.Err())
	}
	// 业务降级：落 scored 维度 failed 行（error_code 记因），当期聚合照常推进（BR9）。
	errCode := llmErrEvalUpstreamCode
	if errors.Is(err, llm.ErrContextLengthExceeded) {
		errCode = llm.ErrContextLengthExceeded.Code
	} else if errors.Is(err, ErrSchemaInvalid) {
		errCode = schemaErrCode
	}
	slog.Error("eval failed", "token_name", tokenName, "period_start", period.Start, "code", errCode)
	failed := make([]domain.DimensionScore, 0, len(scored))
	for _, spec := range scored {
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
// （token_name 与周期由 SaveAll 权威回填）。
func applyRowMeta(rows []domain.DimensionScore, ev, modelID string) {
	for i := range rows {
		rows[i].EvidenceJSON = ev
		rows[i].ModelName = modelID
		rows[i].PromptVersion = PromptVersion
	}
}

// buildEvidence 构造证据快照（specs §2.3 evidence_json 注释）：恒以 normal
// 口径构造，skip 路径的签名标注经 evidenceWithSignature 尾部覆写，避免同周期
// 不同维度行由两处独立计算分叉。
func buildEvidence(specs []DimensionSpec, digests []activity.ProfileDigest) string {
	keys := make([]string, 0, len(digests))
	for _, d := range digests {
		if d.Status == domain.FeatureStatusSuccess {
			keys = append(keys, d.SessionKey)
		}
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
		Summary:        aggregateWindow(digests).evidenceSummary(),
		DimensionSpecs: specSnaps,
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
