// Package questiongen 是 AI 出题生成器子域（specs P2_QBN_001 4.3 引擎侧）：
// LLM 逐题生成攒 staging，完成经 FinishCompleted 单事务建批落库，
// 失败/取消终态化零残留（04 §3.3）。
package questiongen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// 单题输出 token 上限：按最坏合法输出标定（1000+2000+500 字中文 ≈ 1.5 token/字
// + JSON 结构开销 ≈ 5500，留余量）。MaxTokens 取 0 时由底座默认兜底。
const questionMaxTokens = 6000

// DimensionSpec 出题维度口径（由 app 层适配 DimensionRepository 组装）。
type DimensionSpec struct {
	ID          int64
	Name        string
	Description string
}

// StagedQuestion staging 暂存的题目全文（编号未分配前形态，04 §3.3）。
type StagedQuestion struct {
	DimensionID int64  `json:"dimension_id"`
	Scenario    string `json:"scenario"`
	Requirement string `json:"requirement"`
	FocusPoint  string `json:"focus_point"`
}

// DimensionSpecReader 出题维度口径读取窄接口，DimensionRepository 经装配层
// 适配满足（按 dimension_ids 快照查维度名与说明，含软删行）。
type DimensionSpecReader interface {
	// ListSpecsByIDs 按 ID 集合返回维度口径（顺序与 ids 一致，未命中跳过）。
	ListSpecsByIDs(ctx context.Context, ids []int64) ([]DimensionSpec, error)
}

// Generator LLM 逐题生成器（worker handler 调 Run）。
type Generator struct {
	llm     llm.Client
	genRepo repository.QuestionGenerationRepository
	dims    DimensionSpecReader
}

// New 构造 Generator：llmClient 注入出题专用 LLM client（T4 的
// QuestionGenLLMClient）；dims 是维度读通道（按 dimension_ids 快照查维度名
//与说明，含软删行）。
func New(llmClient llm.Client, genRepo repository.QuestionGenerationRepository,
	dims DimensionSpecReader) *Generator {
	return &Generator{llm: llmClient, genRepo: genRepo, dims: dims}
}

// Run 执行一次生成会话（specs 4.3.4 规则 1/2/3，04 §3.3）：
// 领取 → 逐题 LLM 生成攒 staging → 完成单事务建批落库；LLM 失败/超时/取消
// 一律终态化返回 nil（任务不重试），仅存储等基础设施错误上抛交任务重试。
func (g *Generator) Run(ctx context.Context, generationID int64) error {
	if err := g.genRepo.MarkRunning(ctx, generationID); err != nil {
		if errors.Is(err, repository.ErrNotQueued) {
			return nil // 已取消/已领取/不存在：直接返回不重试
		}
		return err
	}
	gen, err := g.genRepo.FindByID(ctx, generationID)
	if err != nil {
		return fmt.Errorf("questiongen: load generation %d: %w", generationID, err)
	}

	dims, err := g.loadDims(ctx, gen.DimensionIDs)
	if err != nil {
		var ie *internalErr
		if errors.As(err, &ie) {
			// 数据异常（快照非法/维度全缺失）：重试无意义，终态化返回 nil。
			slog.Error("questiongen internal", "generation_id", generationID, "err", err)
			if ferr := g.genRepo.FinishTerminal(context.WithoutCancel(ctx), generationID,
				domain.QuestionGenStatusFailed, domain.QuestionGenErrorInternal); ferr != nil &&
				!errors.Is(ferr, repository.ErrAlreadyTerminal) {
				return fmt.Errorf("questiongen: finish internal %d: %w", generationID, ferr)
			}
			return nil
		}
		return err // 读通道基础设施错误：不终态化交任务重试
	}

	staging := make([]StagedQuestion, 0, gen.Count)
	for i := 0; i < gen.Count; i++ {
		// 协作式取消（04 §3.3）：每题前按主键读行检查 status。
		row, err := g.genRepo.FindByID(ctx, generationID)
		if err != nil {
			return fmt.Errorf("questiongen: cancel check %d: %w", generationID, err)
		}
		if row.Status == domain.QuestionGenStatusCanceled {
			return g.finishCanceled(ctx, generationID)
		}

		dim := dims[i%len(dims)]
		q, err := g.generateQuestion(ctx, dim)
		if err != nil {
			return g.finishLLMFailed(ctx, generationID, err)
		}
		q.DimensionID = dim.ID
		staging = append(staging, *q)

		// LLM 在飞期间外部取消（RequestCancel 已清行内 staging）：跳过进度
		// 回写直接终态化，防 SaveProgress 把 staging 写回（BR2 零残留）。
		if row, rerr := g.genRepo.FindByID(ctx, generationID); rerr != nil {
			return fmt.Errorf("questiongen: cancel recheck %d: %w", generationID, rerr)
		} else if row.Status == domain.QuestionGenStatusCanceled {
			return g.finishCanceled(ctx, generationID)
		}

		// 进度推进（specs 4.3.5）：current 指向下一题维度，末题为当前维度收尾。
		next := dim.ID
		if i+1 < gen.Count {
			next = dims[(i+1)%len(dims)].ID
		}
		stagingJSON, err := marshalStaging(staging)
		if err != nil {
			return err
		}
		if err := g.genRepo.SaveProgress(ctx, generationID, i+1, next, stagingJSON); err != nil {
			return fmt.Errorf("questiongen: save progress %d: %w", generationID, err)
		}
	}
	return g.finishCompleted(ctx, gen, dims, staging)
}

// loadDims 解析 dimension_ids 快照并查维度口径（specs 4.3.2 混合出题）。
// 快照为空或维度全缺失属数据异常：置 FAILED + INTERNAL 返回 nil，
// 读通道故障属基础设施错误上抛交任务重试。
func (g *Generator) loadDims(ctx context.Context, dimensionIDs string) ([]DimensionSpec, error) {
	var ids []int64
	if err := json.Unmarshal([]byte(dimensionIDs), &ids); err != nil || len(ids) == 0 {
		return nil, &internalErr{fmt.Errorf("dimension_ids 快照非法: %q", dimensionIDs)}
	}
	dims, err := g.dims.ListSpecsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("questiongen: load dimension specs: %w", err)
	}
	if len(dims) == 0 {
		return nil, &internalErr{fmt.Errorf("dimension_ids 快照 %d 项全未命中维度表", len(ids))}
	}
	return dims, nil
}

// internalErr 数据异常哨兵形态：Run 统一落 FAILED + INTERNAL 终态。
type internalErr struct{ cause error }

func (e *internalErr) Error() string { return e.cause.Error() }
func (e *internalErr) Unwrap() error { return e.cause }

// generateQuestion 单题生成：StreamChat 流式收集 → parseQuestionOutput 宽容
// 解析校验，schema 失败重试一次（evaluator llmEvaluate 同构）。
func (g *Generator) generateQuestion(ctx context.Context, dim DimensionSpec) (*StagedQuestion, error) {
	callOnce := func() (*StagedQuestion, error) {
		stream, err := g.llm.StreamChat(ctx, llm.ChatRequest{
			Messages:  []llm.ChatMessage{{Role: "user", Content: buildQuestionPrompt(dim)}},
			MaxTokens: questionMaxTokens,
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
		return parseQuestionOutput(b.String())
	}
	q, err := callOnce()
	if err != nil && errors.Is(err, ErrSchemaInvalid) && ctx.Err() == nil {
		slog.Warn("questiongen schema invalid, retrying", "dimension", dim.Name)
		q, err = callOnce()
	}
	return q, err
}

// finishCompleted 完成收口（specs 4.3.4 规则 2 / 04 §3.3）：组装批次与题目行
// 后单事务 FinishCompleted（建批+插题+Q-AG 编号分配+置 COMPLETED+清 staging）。
// ErrNotRunning 视为外部取消竞态，幂等返回 nil；其余错误上抛交任务重试。
func (g *Generator) finishCompleted(ctx context.Context, gen *domain.QuestionGeneration,
	dims []DimensionSpec, staging []StagedQuestion) error {
	batch := domain.QuestionBatch{
		Title:         buildBatchTitle(dims),
		Source:        domain.QuestionSourceAI,
		BatchType:     domain.QuestionBatchTypeGenerate,
		Status:        domain.QuestionBatchStatusPending,
		QuestionCount: len(staging),
		DimensionIDs:  gen.DimensionIDs,
	}
	questions := make([]domain.Question, len(staging))
	for i, s := range staging {
		questions[i] = domain.Question{
			// question_no 不预填，由 FinishCompleted 在事务内分配 Q-AG 连续编号。
			Source:      domain.QuestionSourceAI,
			DimensionID: s.DimensionID,
			Scenario:    s.Scenario,
			Requirement: s.Requirement,
			FocusPoint:  s.FocusPoint,
			Status:      domain.QuestionStatusPending,
			Version:     1,
		}
	}
	if err := g.genRepo.FinishCompleted(ctx, gen.ID, &batch, &questions); err != nil {
		if errors.Is(err, repository.ErrNotRunning) {
			return nil // 完成前被取消：终态由取消方承载
		}
		return fmt.Errorf("questiongen: finish completed %d: %w", gen.ID, err)
	}
	slog.Info("questiongen completed", "generation_id", gen.ID,
		"batch_no", batch.BatchNo, "count", len(questions))
	return nil
}

// finishLLMFailed LLM 失败终态（specs 4.3.4 规则 3 整批作废）：超时形态
// （llm.ErrTimeout 同 Code 或 ctx 超时）落 LLM_TIMEOUT，其余 LLM_FAILED。
func (g *Generator) finishLLMFailed(ctx context.Context, generationID int64, cause error) error {
	errCode := domain.QuestionGenErrorLLMFailed
	if isTimeoutKind(cause) {
		errCode = domain.QuestionGenErrorLLMTimeout
	}
	slog.Error("questiongen failed", "generation_id", generationID, "code", errCode, "err", cause)
	if err := g.genRepo.FinishTerminal(context.WithoutCancel(ctx), generationID,
		domain.QuestionGenStatusFailed, errCode); err != nil && !errors.Is(err, repository.ErrAlreadyTerminal) {
		return fmt.Errorf("questiongen: finish terminal %d: %w", generationID, err)
	}
	return nil
}

// finishCanceled 取消命中终态（04 §3.3 协作式取消）：外部 RequestCancel 已置
// CANCELED 清 staging，此处幂等兜底（已终态 ErrAlreadyTerminal 忽略）。
func (g *Generator) finishCanceled(ctx context.Context, generationID int64) error {
	if err := g.genRepo.FinishTerminal(context.WithoutCancel(ctx), generationID,
		domain.QuestionGenStatusCanceled, domain.QuestionGenErrorCanceled); err != nil &&
		!errors.Is(err, repository.ErrAlreadyTerminal) {
		return fmt.Errorf("questiongen: finish canceled %d: %w", generationID, err)
	}
	return nil
}

// isTimeoutKind LLM 失败分类：*llm.Error 按 Code 判 ErrTimeout 同类，
// ctx 超时（DeadlineExceeded）同归超时。
func isTimeoutKind(err error) bool {
	var le *llm.Error
	if errors.As(err, &le) {
		return errors.Is(le, llm.ErrTimeout)
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// marshalStaging 序列化暂存区（纯值 marshal 恒成功，兜底空数组）。
func marshalStaging(staging []StagedQuestion) (string, error) {
	raw, err := json.Marshal(staging)
	if err != nil {
		return "", fmt.Errorf("questiongen: marshal staging: %w", err)
	}
	return string(raw), nil
}
