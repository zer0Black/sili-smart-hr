package model

import (
	"fmt"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// groupBase/groupUpper 作为 GroupCode 指针取值源（AI_USAGE 专属两层分组）。
var (
	groupBase  = domain.GroupBase
	groupUpper = domain.GroupUpper
)

// seedDefaultDimensions 首启 seed 默认对话分析维度（AI 使用能力 8 维，口径源
// UI 原型 dimension-config-list.html 默认数据，2026-09-10 真实会话实测后重写
// 提示词证据锚定）：仅全新库写入，dimensions 表存在任何行（含软删行）即跳过，
// 运营维护过的库不触碰。并发双实例同窗空库时整批撞 uk_dimension_code，
// UniqueViolation 视为对端胜出。
func seedDefaultDimensions(db *gorm.DB) error {
	var n int64
	if err := db.Unscoped().Model(&domain.Dimension{}).Count(&n).Error; err != nil {
		return fmt.Errorf("count dimensions: %w", err)
	}
	if n > 0 {
		return nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		dims := defaultDimensions()
		return tx.Create(&dims).Error
	})
	if dberr.UniqueViolation(err) {
		return nil // 并发双 seed 对端胜出，行已落库
	}
	if err != nil {
		return fmt.Errorf("seed dimensions: %w", err)
	}
	return nil
}

// defaultDimensions 返回全新构造的默认 8 维切片，避免雪花回调改写包级状态。
// 均为 AI_USAGE 模块、CONVERSATION 数据来源、参与总览、启用态，权重合计 100。
// 提示词判读规则锚定评分员实际可见的证据形态（逐会话块的 summary/instruction/
// behavior 与统计段字段），禁用日志不可见的锚点档（团队复用、落地去向等）。
func defaultDimensions() []domain.Dimension {
	return []domain.Dimension{
		{
			Code: "AI_TASK_FIT", Name: "任务适配判断力",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupBase,
			DataSource: domain.SourceConversation, Weight: 15,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 交办任务与 AI 能力匹配，出错时质疑并纠偏；60~89 任务基本匹配但偶有误判；40~59 常把琐碎或超界任务交给 AI；<40 盲从 AI 输出或任务与目标脱节。",
			Prompt:      "评估「任务适配判断力」：把对的任务交给 AI 并保持把关。判读规则：1) instruction 中的指令是否指向明确的工作产出（对照 summary 的工作域），琐碎问询占比高则降档；2) behavior.review_ratio 为 high/medium 说明有复核把关，low 且 zero_input_sessions 占比高则降档；3) summary 提到的产出须与指令量级匹配，指令密集但产出空泛属重复劳动形态，降档；4) 禁止因证据少直接给低分，证据不足标 insufficient。",
			Description: "判断哪些任务适合交给 AI、哪些应保留人工的能力，是高效协作的前提。信号来自对话中任务拆解与边界讨论。",
		},
		{
			Code: "AI_PROMPT", Name: "指令与 prompt 设计",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupBase,
			DataSource: domain.SourceConversation, Weight: 12,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 指令含背景、约束、验收口径，可复用；60~89 意图清楚但缺约束；40~59 表达模糊致 AI 反复确认或返工；<40 只有只言片语，产出全靠 AI 猜测。",
			Prompt:      "评估「指令与 prompt 设计」：用户指令的清晰度与结构化程度。判读规则：1) 依据逐会话块 instruction 段的真实指令文本判档：含目标、背景、约束、输出要求中三项以上为 high，两项 medium，仅一句指令或「继续」类为 low；2) behavior.instruction_specificity 是抽取侧同口径信号，作交叉参考；3) paste_char_count 大说明供给了上下文，但 paste_msg_sessions 占比高且 user_msg_count 低属转储依赖，不加分；4) 指令稀薄时对照统计段 zero_input_sessions，证据不足标 insufficient。",
			Description: "构造清晰、结构化、可复用指令的能力。从对话中 prompt 草稿与迭代过程提取信号。",
		},
		{
			Code: "AI_DIALOG", Name: "对话驾驭",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupBase,
			DataSource: domain.SourceConversation, Weight: 10,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 主动纠偏、追问收敛，会话有明确产出收尾；60~89 能跟随推进但纠偏被动；40~59 跑偏后不自知，会话常无果；<40 放任输出，中断频繁无方向。",
			Prompt:      "评估「对话驾驭」：多轮对话中维持方向、主动纠偏、收敛目标的能力。判读规则：1) instruction 中的追问、纠正、改向类指令（「不对」「换个思路」「这里有问题」类）是主动驾驭的直接证据；2) behavior.review_ratio 高说明持续把关；3) interrupt_count 高且会话无产出收尾属失控形态，降档；4) continuation_sessions 高说明依赖跨会话续接推进，本身中性，续接后能收敛不降档；5) 依据可见档案判读，证据不足标 insufficient。",
			Description: "多轮对话中维持上下文、主动纠偏、追问收敛的能力。",
		},
		{
			Code: "AI_TOOL_USE", Name: "Skill 调用",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupBase,
			DataSource: domain.SourceConversation, Weight: 13,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 会话中主动且恰当调用既有 skill/工作流，时机与问题匹配；60~89 有调用但时机把握一般；40~59 极少调用或调用与问题错配；<40 从不调用可用工具。",
			Prompt:      "评估「Skill 调用」：调用既有 skill/工具的判断与时机。判读规则：1) 仅依据 instruction 或 summary 中出现的显式 skill/工具调用证据（如「使用技能 /xxx」「调用 xxx」或 summary 记录的工具编排行为）评分；2) 统计段 tool_top10 反映 AI 侧工具调用量，属 AI 自主行为，仅作任务复杂度参考，不直接计分；3) 无任何调用证据时标 insufficient，禁止按 tool_top10 数量推断用户能力，禁止给无证据的猜测分。",
			Description: "在合适时机调用合适 Skill 的判断与执行能力。",
		},
		{
			Code: "AI_HARNESS", Name: "Harness 编排驾驭",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupUpper,
			DataSource: domain.SourceConversation, Weight: 15,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 主动设计多步骤编排并要求验证闭环；60~89 能分步推进并验收中间产物；40~59 单步指令为主，无过程把控；<40 一句话甩任务，全程无介入。",
			Prompt:      "评估「Harness 编排驾驭」：组织多步骤任务、划分阶段、验收中间产物的能力。判读规则：1) 编排证据须来自 instruction：分步指令、阶段划分、验收要求（「先做 A 再做 B」「检查结果」「跑测试」）是用户编排的直接证据；2) summary 中记录的流程化推进（计划驱动、分阶段执行）需分辨是用户指令驱动还是 AI 自主推进，AI 自主编排不计入用户能力；3) behavior.review_ratio 与编排验收信号互相印证；4) 纯单步指令会话不足以支撑本维度，标 insufficient。",
			Description: "编排多步骤、多角色、多工具复杂工作流的能力，体现对 Agent 协作体系的整体掌控。",
		},
		{
			Code: "AI_SKILL_WRITE", Name: "Skill 编写能力",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupUpper,
			DataSource: domain.SourceConversation, Weight: 12,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 主动创建结构完整的可复用 skill/规则/模板并多轮迭代；60~89 有创建但结构或边界粗糙；40~59 仅零散修改既有配置；<40 无任何创建行为。",
			Prompt:      "评估「Skill 编写能力」：把工作经验封装为可复用 skill/规则/模板的创建与迭代。判读规则：1) 创建证据须来自 instruction 或 summary 中明确记录的写 skill、建规则、写模板、配置工作流行为；统计段 spec_fingerprint_kinds 中 skill_doc 计数是独立佐证（存在 skill 规约注入说明环境里有 skill 在用）；2) spec_fingerprint_kinds 仅有 claude_md/agents_md 而无创建行为记录时，说明只是使用他人配置的环境，不加分；3) 本维度证据在日志中天然稀疏，无创建证据一律标 insufficient，禁止由其他维度分数顺延推断。",
			Description: "编写可复用 Skill 的质量与工程化水平，是协作能力沉淀的关键。",
		},
		{
			Code: "AI_VALUE", Name: "价值产出质量",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupUpper,
			DataSource: domain.SourceConversation, Weight: 13,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 会话承载任务业务价值明确，产出具体可用；60~89 任务有价值，产出需少量修正；40~59 试探性会话占比高，产出空泛；<40 纯闲聊或重复劳动。",
			Prompt:      "评估「价值产出质量」：会话承载任务的价值与产出质量（日志看不到产出最终去向，只评过程信号）。判读规则：1) 任务价值性：summary 的工作域与任务描述是否指向明确业务产出，对照低价值形态（纯试探、闲聊、格式转换类琐碎任务）；2) 产出质量：summary 中记录的产出是否具体、结构化（有代码/文档/方案实体），对照空泛套话；3) 净有效产出比：有价值任务会话数占 sessions_valid 的比例，narrative_absent_sessions 占比高时降档参考；4) 单一维度跨会话综合判读，少数高价值会话拉不动大量低价值会话的均值。",
			Description: "AI 协作最终产出的业务价值与可用度，是能力模型最贴近业务结果的维度。",
		},
		{
			Code: "AI_KNOWLEDGE", Name: "知识沉淀与复用",
			ModuleCode: domain.ModuleAIUsage, GroupCode: &groupUpper,
			DataSource: domain.SourceConversation, Weight: 10,
			IncludeOverview: true, Enabled: true, Version: 1,
			Anchor: "90+ 主动沉淀方法为文档/模板并跨会话复用；60~89 偶尔整理沉淀；40~59 沉淀零散无体系；<40 从不沉淀。",
			Prompt:      "评估「知识沉淀与复用」：把协作经验固化为可复用资产并跨会话迁移。判读规则：1) 沉淀证据：instruction 或 summary 中记录的写文档、整理模板、固化流程、更新规范类行为；2) 复用证据：不同会话的 instruction 中出现结构一致的模板化指令，或 summary 提到沿用既定方法；3) spec_unique_hashes 多属不同项目各自的规约文件，规约存在本身不构成沉淀证据，仅当 summary 明确记录规约文件由本会话产出或更新时才可计入；4) cmd_reuse_groups 是同首指令重做信号，一律按重做解读不作复用证据；5) 本维度证据天然稀疏，无沉淀与复用证据标 insufficient。",
			Description: "把协作经验沉淀为可复用知识资产的能力，体现长期主义与组织贡献。",
		},
	}
}
