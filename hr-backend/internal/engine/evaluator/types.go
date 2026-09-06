package evaluator

import (
	"errors"

	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// 评估行为常量（specs §2.2 常量表），包内定义，调整走代码变更发版。
const (
	MinValidProfilesForEval = 1
	MaxProfileSetTokens     = 30000
	MaxProfileSetChars      = MaxProfileSetTokens * 3 // 字符执行口径：1 token ≈ 3 字符，与 T4 同折算
	MaxRationaleChars       = 200                     // 单维度评分理由字数约束，落库校验容差 2 倍
	ScoreMin                = 0                       // 绝对分下界（0-100 整数分制）
	ScoreMax                = 100                     // 绝对分上界
	PromptVersion           = "v1"                    // prompt 模板版本，模板变更时 +1 同步本常量
)

// DimensionSpec 评分维度口径快照（specs §2.2）：取数时快照落评分行 evidence_json，
// 维度配置变更后已产出历史不重算（聚合唯一权重来源）。
type DimensionSpec struct {
	Code       string // 维度编码（唯一键，如 AI_INSTRUCTION）
	Name       string // 维度名称
	Module     string // 所属模块（聚合分组键，值域同 DIM module_code）
	PromptText string // 评分提示词（仅对话分析维度非空）
	AnchorText string // 评分锚点（0-100 分值区间对应能力表现）
	Weight     int    // 模块内聚合权重百分比（0-100）
	InOverview bool   // 是否参与聚合（模块分与总览分门槛）
}

// DimensionScore 评分行落库组装视图（specs §2.3），对应 dimension_score 表业务列。
type DimensionScore struct {
	DimensionCode string
	Module        string
	Score         int    // 0-100 整数，insufficient 与 failed 行落 0
	Rationale     string // 评分理由（落库前过 Redact 兜底脱敏）
	Insufficient  bool   // 证据不足标记，true 时聚合剔除
	EvidenceJSON  string // 证据与口径快照 JSON（session_key 清单+统计摘要+维度口径摘要）
	Source        string // conversation（本组件）/ active_test（F7 阅卷）
	Status        string // success / failed
}

// EvaluateResult 单人周期评估结果（specs §2.3）。
type EvaluateResult struct {
	Scores  []DimensionScore // 维度评分行（含 insufficient 标记行）
	Skipped bool             // 跳过 LLM 直接落全维度 insufficient（零有效档案或签名命中）
	Reused  bool             // 命中既有 success 评分行复用未调 LLM（幂等）
}

// 哨兵错误（specs §2.3 错误码表）：均 error 上抛交 Asynq 任务级重试，不落评分行。
var (
	// ErrNoDimensions 无启用的对话分析维度（配置缺失或全部停用）。
	ErrNoDimensions = errors.New("evaluator: no enabled conversation dimensions")
	// ErrDimensionConfigRead 维度配置读取失败（wrap 底层错误）。
	ErrDimensionConfigRead = errors.New("evaluator: dimension config read failed")
	// ErrProfileRead 档案表读取失败（wrap 底层错误）。
	ErrProfileRead = errors.New("evaluator: profile read failed")
)

// Evaluator 跨会话综合评估组件（specs §2.4 能力1）。
// AssembleProfileSet（T6）与 Evaluate（T8）挂本类型，此处前置定义结构体与构造
// 保证各任务独立编译可验收。
type Evaluator struct {
	llm           llm.Client
	modelProvider llm.EnabledModelProvider
	featureRepo   repository.SessionFeatureRepository
	specs         DimensionSpecReader
	thresholds    activity.ThresholdReader
	scoreRepo     repository.DimensionScoreRepository
	sysParams     repository.SystemParamReader
}

// New 构造 Evaluator，七个依赖集中注入。
func New(llmClient llm.Client, modelProvider llm.EnabledModelProvider,
	featureRepo repository.SessionFeatureRepository, specs DimensionSpecReader,
	thresholds activity.ThresholdReader, scoreRepo repository.DimensionScoreRepository,
	sysParams repository.SystemParamReader) *Evaluator {
	return &Evaluator{
		llm:           llmClient,
		modelProvider: modelProvider,
		featureRepo:   featureRepo,
		specs:         specs,
		thresholds:    thresholds,
		scoreRepo:     scoreRepo,
		sysParams:     sysParams,
	}
}
