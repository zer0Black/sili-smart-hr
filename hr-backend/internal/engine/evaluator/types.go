package evaluator

import (
	"context"
	"errors"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/conversationlog"
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
	PromptVersion           = "v3"                    // prompt 模板版本（v3：统计段补 C08 头部样本保守折算；v2：空提示词维度分流），模板变更时 +1 同步本常量
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

// EvaluateResult 单人周期评估组合结果（specs §2.3）：窄形态（Scores/Skipped/
// Reused，Evaluate 产出）扩展 Activity 与 Aggregate 两组合字段（EvaluatePerson 产出）。
type EvaluateResult struct {
	Activity  *activity.ActivityStat  // 活跃度统计行（EvaluatePerson 路径产出）
	Scores    []domain.DimensionScore // 维度评分行（含 insufficient 标记行）
	Aggregate *scorer.AggregateResult // 聚合结果（EvaluatePerson 路径产出）
	Skipped   bool                    // 跳过 LLM 直接落全维度 insufficient（零有效档案或签名命中）
	Reused    bool                    // 命中既有 success 评分行复用未调 LLM（幂等）
}

// 哨兵错误（specs §2.3 错误码表）：均 error 上抛交 Asynq 任务级重试，不落评分行。
var (
	// ErrNoDimensions 无启用的对话分析维度（配置缺失或全部停用）。
	ErrNoDimensions = errors.New("evaluator: no enabled conversation dimensions")
	// ErrDimensionConfigRead 维度配置读取失败（适配层 wrap 后透传）。
	ErrDimensionConfigRead = errors.New("evaluator: dimension config read failed")
	// ErrScoreRead 既有评分行读取失败（幂等判定路径）。
	ErrScoreRead = errors.New("evaluator: score read failed")
	// ErrStoreWrite 评分行落库失败（wrap 底层错误）：不落行，error 上抛交任务重试。
	ErrStoreWrite = errors.New("evaluator: store write failed")
)

// Evaluator 跨会话综合评估组件（specs §2.4 能力1）。
// AssembleProfileSet（T6）与 Evaluate（T8）挂本类型；EvaluatePerson（T10）经
// act/sc 组合依赖编排活跃度与聚合（03 §2.1 组合形）。
type Evaluator struct {
	llm           llm.Client
	modelProvider llm.EnabledModelProvider
	featureRepo   repository.SessionFeatureRepository
	specs         DimensionSpecReader
	thresholds    activity.ThresholdReader
	scoreRepo     repository.DimensionScoreRepository
	sysParams     repository.SystemParamReader
	act           ActivityStatComponent
	sc            ScoreAggregator
}

// New 构造 Evaluator，九个依赖集中注入（七参基础上追加 act/sc 组合件，03 §2.1）。
// sc 传 *scorer.Scorer；act 传 *activity.Activity（方法集结构化满足窄面）。
func New(llmClient llm.Client, modelProvider llm.EnabledModelProvider,
	featureRepo repository.SessionFeatureRepository, specs DimensionSpecReader,
	thresholds activity.ThresholdReader, scoreRepo repository.DimensionScoreRepository,
	sysParams repository.SystemParamReader, act ActivityStatComponent,
	sc ScoreAggregator) *Evaluator {
	return &Evaluator{
		llm:           llmClient,
		modelProvider: modelProvider,
		featureRepo:   featureRepo,
		specs:         specs,
		thresholds:    thresholds,
		scoreRepo:     scoreRepo,
		sysParams:     sysParams,
		act:           act,
		sc:            sc,
	}
}

// ActivityStatComponent EvaluatePerson 组合依赖窄面（合理实现形偏差：以接口
// 窄化替代具体类型字段利测试探针注入）：ByKey 形态额外携带拉取到的窗口内列表
// 与档案集（已归一过滤），供 EvaluatePerson 注入 Evaluate 免二次拉取与二次取数
//（specs §2.2 sessions 参数说明）；方法名与 *activity.Activity 导出方法一致，
// 结构化满足无需适配层。
type ActivityStatComponent interface {
	StatPersonByKeyWithSessions(ctx context.Context, tokenName string, period activity.Period) (*activity.ActivityStat, []conversationlog.SessionSummary, []activity.ProfileDigest, error)
	StatPerson(ctx context.Context, sessions []conversationlog.SessionSummary, tokenName string, period activity.Period) (*activity.ActivityStat, []activity.ProfileDigest, error)
}

// ScoreAggregator EvaluatePerson 组合依赖窄面（*scorer.Scorer 满足）。
type ScoreAggregator interface {
	Aggregate(ctx context.Context, tokenName string, period activity.Period) (*scorer.AggregateResult, error)
}
