// Package grading 是 AI 阅卷引擎子域（specs P2_TST_001 §5.2，03 §4.4）：
// 双类型分流阅卷——ai_mgmt 逐子能力评分落 dimension_scores（source=active_test）
// 并自调聚合刷新，enneagram 判型落 assessment_test_results（参考性口径不进聚合）。
package grading

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
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// gradingMaxTokens 单次阅卷输出上限：两形态最坏合法输出（9 维分数 + 理由 或
// 判型四字段）≈ 3000 token，留余量。MaxTokens 取 0 时由底座默认兜底。
const gradingMaxTokens = 6000

// TaskRepo 任务读写窄面。
type TaskRepo interface {
	GetByID(ctx context.Context, id int64) (*domain.AssessmentTestTask, error)
	MarkGradingTerminal(ctx context.Context, taskID int64, gradingStatus string) error
}

// QuestionRepo 题目快照现读窄面（Unscoped 含软删行）。
type QuestionRepo interface {
	ListByIDsUnscoped(ctx context.Context, ids []int64) ([]domain.Question, error)
}

// ResultRepo 判型结果落库窄面。
type ResultRepo interface {
	UpsertByTaskID(ctx context.Context, r *domain.AssessmentTestResult) error
}

// DimRepo 维度口径读取窄面（Unscoped 含停用与软删行：快照口径直读）。
type DimRepo interface {
	ListFullByCodesUnscoped(ctx context.Context, codes []string) ([]domain.Dimension, error)
}

// ConfigRepo 周期配置读取窄面。
type ConfigRepo interface {
	Get(ctx context.Context) (*domain.AssessmentConfig, error)
}

// AggRepo 聚合刷新窄面（*scorer.Scorer 满足）。
type AggRepo interface {
	Aggregate(ctx context.Context, tokenName string, period activity.Period) (*scorer.AggregateResult, error)
}

// Grader AI 阅卷引擎：Run 由 assessment:test-grade worker 驱动。
type Grader struct {
	llm           llm.Client
	modelProvider llm.EnabledModelProvider
	taskRepo      TaskRepo
	questionRepo  QuestionRepo
	resultRepo    ResultRepo
	dimRepo       DimRepo
	scoreRepo     repository.DimensionScoreRepository
	agg           AggRepo
	configRepo    ConfigRepo
	sysParams     repository.SystemParamReader
}

// New 构造 Grader，十个依赖集中注入（llmClient 为 240s 阅卷专用 client，T4 装配）。
func New(llmClient llm.Client, modelProvider llm.EnabledModelProvider,
	taskRepo TaskRepo, questionRepo QuestionRepo, resultRepo ResultRepo,
	dimRepo DimRepo, scoreRepo repository.DimensionScoreRepository, aggRepo AggRepo,
	configRepo ConfigRepo, sysParams repository.SystemParamReader) *Grader {
	return &Grader{
		llm:           llmClient,
		modelProvider: modelProvider,
		taskRepo:      taskRepo,
		questionRepo:  questionRepo,
		resultRepo:    resultRepo,
		dimRepo:       dimRepo,
		scoreRepo:     scoreRepo,
		agg:           aggRepo,
		configRepo:    configRepo,
		sysParams:     sysParams,
	}
}

// Run 执行一次阅卷（specs §5.2.2 步骤2-6，03 §4.4）：终态守卫 → 上下文组装 →
// 单次 LLM 调用 → 双类型落库分流 → 推进 scored。解析校验失败视同调用失败上抛
// 交 Asynq 任务级重试（specs §5.2.5），重试耗尽降级归 T4 钩子处置。
func (g *Grader) Run(ctx context.Context, taskID int64) error {
	task, err := g.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("grading: load task %d: %w", taskID, err)
	}
	if task == nil {
		return fmt.Errorf("grading: task %d not found", taskID)
	}
	// 终态幂等守卫（specs §5.2.4 规则1）：waiting/grading 均继续，scored/degraded 直接返回。
	if task.GradingStatus == domain.GradingStatusScored || task.GradingStatus == domain.GradingStatusDegraded {
		return nil
	}

	questions, err := g.loadQuestions(ctx, task)
	if err != nil {
		return err
	}

	// 模型解析失败落空串不阻断（与 evaluator 同口径），调用失败另走重试。
	modelID := ""
	if mc, merr := g.modelProvider.GetEnabledModel(ctx); merr == nil {
		modelID = mc.ModelID
	} else {
		slog.Warn("grading model resolve failed, model_name will be empty", "err", merr)
	}

	switch task.TestType {
	case domain.TestTypeAIMgmt:
		return g.runAIMgmt(ctx, task, questions, modelID)
	case domain.TestTypeEnneagram:
		return g.runEnneagram(ctx, task, questions, modelID)
	default:
		return fmt.Errorf("grading: unknown test_type %q of task %d", task.TestType, taskID)
	}
}

// loadQuestions 展开题目快照现读全文（specs §5.1.4 规则2：集合以快照为准，
// 文本经 Unscoped 现读，03 §4.6 注记）。
func (g *Grader) loadQuestions(ctx context.Context, task *domain.AssessmentTestTask) ([]domain.Question, error) {
	var ids []int64
	if err := json.Unmarshal([]byte(task.QuestionIDsJSON), &ids); err != nil {
		return nil, fmt.Errorf("grading: parse question_ids_json of task %d: %w", task.ID, err)
	}
	questions, err := g.questionRepo.ListByIDsUnscoped(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("grading: load questions of task %d: %w", task.ID, err)
	}
	return questions, nil
}

// runAIMgmt ai_mgmt 阅卷（specs §5.2.2 步骤3a/4a/5）：维度口径取任务快照全字段
// 直读（含停用维度）→ LLM 评分 → 逐子能力落 active_test 行 → 自调聚合 → 推进 scored。
func (g *Grader) runAIMgmt(ctx context.Context, task *domain.AssessmentTestTask, questions []domain.Question, modelID string) error {
	dims, err := g.taskDimensions(ctx, task)
	if err != nil {
		return err
	}

	raw, err := g.callLLM(ctx, buildAIMgmtPrompt(dims, questions))
	if err != nil {
		return err
	}
	out, err := parseScoreOutput(raw)
	if err != nil {
		return err
	}

	rows, err := buildScoreRows(out, dims, raw)
	if err != nil {
		return err
	}

	// period 按阅卷时读取的当前周期配置以任务创建时刻推算窗口（03 §4.4 4a）。
	period, err := g.taskPeriod(ctx, task)
	if err != nil {
		return err
	}

	// 落库前 Redact 兜底（specs §3.3 第二道防线，evaluator 同款）。
	patterns := g.redactPatterns(ctx)
	evidence := buildEvidence(dims)
	for i := range rows {
		rows[i].Rationale = extractor.Redact(rows[i].Rationale, patterns)
		rows[i].EvidenceJSON = evidence
		rows[i].ModelName = modelID
		rows[i].PromptVersion = PromptVersion
	}

	if err := g.scoreRepo.SaveAll(ctx, task.StaffName, period.Start, period.End, rows); err != nil {
		return fmt.Errorf("grading: save scores of task %d: %w", task.ID, err)
	}

	// 汇入画像：聚合失败仅记 ERROR 不阻断终态（specs §5.2.5 画像汇入失败行）。
	if _, aerr := g.agg.Aggregate(ctx, task.StaffName, period); aerr != nil {
		slog.Error("grading aggregate failed, left to next task or batch run",
			"task_no", task.TaskNo, "staff_name", task.StaffName, "err", aerr)
	}

	if err := g.taskRepo.MarkGradingTerminal(ctx, task.ID, domain.GradingStatusScored); err != nil {
		return fmt.Errorf("grading: mark scored of task %d: %w", task.ID, err)
	}
	slog.Info("grading ai_mgmt completed", "task_no", task.TaskNo, "dimensions", len(rows))
	return nil
}

// runEnneagram enneagram 阅卷（specs §5.2.2 步骤3b/4b）：量表题全文入 prompt、
// 型别维度口径不进 prompt（量表题自含计分键）→ 判型四字段落结果行 → 推进 scored。
func (g *Grader) runEnneagram(ctx context.Context, task *domain.AssessmentTestTask, questions []domain.Question, modelID string) error {
	raw, err := g.callLLM(ctx, buildEnneagramPrompt(questions))
	if err != nil {
		return err
	}
	out, err := parseEnneagramOutput(raw)
	if err != nil {
		return err
	}

	distJSON, err := json.Marshal(out.Distribution)
	if err != nil {
		return fmt.Errorf("grading: marshal distribution of task %d: %w", task.ID, err)
	}
	result := &domain.AssessmentTestResult{
		TaskID:           task.ID,
		MainType:         out.MainType,
		WingType:         out.WingType,
		DistributionJSON: string(distJSON),
		Rationale:        extractor.Redact(out.Rationale, g.redactPatterns(ctx)),
		ModelName:        modelID,
		PromptVersion:    PromptVersion,
		GradingStatus:    domain.GradingStatusScored,
	}
	if err := g.resultRepo.UpsertByTaskID(ctx, result); err != nil {
		return fmt.Errorf("grading: upsert result of task %d: %w", task.ID, err)
	}
	if err := g.taskRepo.MarkGradingTerminal(ctx, task.ID, domain.GradingStatusScored); err != nil {
		return fmt.Errorf("grading: mark scored of task %d: %w", task.ID, err)
	}
	slog.Info("grading enneagram completed", "task_no", task.TaskNo, "main_type", out.MainType)
	return nil
}

// taskDimensions AI_MGMT 子能力口径：任务 dimension_codes_json 快照直读全字段
// （含停用维度，specs §5.1.4 规则2 快照不可变、04 §3.1 阅卷按此圈定）。快照解析
// 出 0 个维度属构造侧确定性错误，上抛交降级（防 0 行评分假 scored）。
func (g *Grader) taskDimensions(ctx context.Context, task *domain.AssessmentTestTask) ([]domain.Dimension, error) {
	var codes []string
	if err := json.Unmarshal([]byte(task.DimensionCodesJSON), &codes); err != nil {
		return nil, fmt.Errorf("grading: parse dimension_codes_json of task %d: %w", task.ID, err)
	}
	all, err := g.dimRepo.ListFullByCodesUnscoped(ctx, codes)
	if err != nil {
		return nil, fmt.Errorf("grading: load dimensions of task %d: %w", task.ID, err)
	}
	dims := make([]domain.Dimension, 0, len(all))
	for _, d := range all {
		if d.ModuleCode == domain.ModuleAIMgmt {
			dims = append(dims, d)
		}
	}
	if len(dims) == 0 {
		return nil, fmt.Errorf("grading: no AI_MGMT dimensions resolved from snapshot of task %d", task.ID)
	}
	return dims, nil
}

// taskPeriod 周期窗口：当前周期配置 + 任务创建时刻（03 §4.4 4a，口径同
// pipeline.CurrentPeriodWindow，同人同窗口与跑批聚合行双界精确对齐）。
func (g *Grader) taskPeriod(ctx context.Context, task *domain.AssessmentTestTask) (activity.Period, error) {
	cfg, err := g.configRepo.Get(ctx)
	if err != nil {
		return activity.Period{}, fmt.Errorf("grading: load period config: %w", err)
	}
	start, end := pipeline.CurrentPeriodWindow(task.CreatedAt, cfg.Period)
	return activity.Period{Start: start, End: end}, nil
}

// callLLM 单次 LLM 调用并流式收集全文（重试由 client 内建 MaxRetries 与
// Asynq 任务级重试承载，specs §5.2.4 规则3）。
func (g *Grader) callLLM(ctx context.Context, prompt string) (string, error) {
	stream, err := g.llm.StreamChat(ctx, llm.ChatRequest{
		Messages:  []llm.ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens: gradingMaxTokens,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var b strings.Builder
	for {
		chunk, rerr := stream.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return "", rerr
		}
		b.WriteString(chunk.Content)
	}
	return b.String(), nil
}

// redactPatterns 读脱敏正则集：DB 故障回退出厂（安全机制不失效，记 WARN）。
func (g *Grader) redactPatterns(ctx context.Context) []string {
	loaded, err := g.sysParams.ReadStringArrays(extractor.ParamKeyRedactPatterns)
	if err != nil {
		slog.Warn("read redact patterns failed, fallback to factory set", "err", err)
		return nil
	}
	return loaded[extractor.ParamKeyRedactPatterns]
}

// gradingSpecSnapshot evidence 口径快照条目（与 evaluator 同构，聚合唯一权重来源）。
type gradingSpecSnapshot struct {
	Code       string `json:"code"`
	Weight     int    `json:"weight"`
	InOverview bool   `json:"in_overview"`
}

// gradingEvidence evidence_json 结构（04 §3.3：dimension_specs 口径摘要）。
type gradingEvidence struct {
	DimensionSpecs []gradingSpecSnapshot `json:"dimension_specs"`
}

// buildEvidence 组装口径快照（specs §5.2.3 评分口径：weight/in_overview）。
func buildEvidence(dims []domain.Dimension) string {
	specs := make([]gradingSpecSnapshot, 0, len(dims))
	for _, d := range dims {
		specs = append(specs, gradingSpecSnapshot{Code: d.Code, Weight: d.Weight, InOverview: d.IncludeOverview})
	}
	raw, err := json.Marshal(gradingEvidence{DimensionSpecs: specs})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// buildScoreRows 校验收敛评分行（specs §5.2.2 步骤3a）：code 白名单 = 任务口径
// 集合，多出丢弃、缺失补 insufficient 行；score 非 null 时须 0-100 整数。
func buildScoreRows(out *scoreOutput, dims []domain.Dimension, raw string) ([]domain.DimensionScore, error) {
	byCode := make(map[string]scoreDimension, len(out.Dimensions))
	for _, d := range out.Dimensions {
		if _, ok := byCode[d.Code]; !ok {
			byCode[d.Code] = d // 同 code 重复取首个
		}
	}
	rows := make([]domain.DimensionScore, 0, len(dims))
	for _, dim := range dims {
		d, ok := byCode[dim.Code]
		if !ok {
			rows = append(rows, insufficientRow(dim))
			continue
		}
		if d.Score != nil && (d.Score.N < 0 || d.Score.N > 100) {
			return nil, schemaErrWithExcerpt(raw)
		}
		row := domain.DimensionScore{
			DimensionCode: dim.Code,
			Module:        dim.ModuleCode,
			Rationale:     d.Rationale,
			Insufficient:  d.Insufficient,
			Source:        domain.ScoreSourceActiveTest,
			Status:        domain.ScoreStatusSuccess,
		}
		if d.Insufficient || d.Score == nil {
			row.Score = 0
			row.Insufficient = true
		} else {
			row.Score = d.Score.N
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// insufficientRow 缺失维度补行（specs §2.4 校验规则同构：score 0 + 标记 true）。
func insufficientRow(dim domain.Dimension) domain.DimensionScore {
	return domain.DimensionScore{
		DimensionCode: dim.Code,
		Module:        dim.ModuleCode,
		Score:         0,
		Rationale:     insufficientDefaultRationale,
		Insufficient:  true,
		Source:        domain.ScoreSourceActiveTest,
		Status:        domain.ScoreStatusSuccess,
	}
}

// insufficientDefaultRationale 缺失维度补行缺省理由。
const insufficientDefaultRationale = "作答证据不足：该子能力维度在作答对话中证据不足以支撑评分。"
