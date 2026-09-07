package evaluator

import (
	"fmt"
	"strings"
)

// systemTemplate 系统段（specs §2.4 能力1 五段之首）：评分员角色 + JSON schema
// 约束 + dimensions 数组格式示例。MaxRationaleChars 字面值写在模板文本内
// （specs §2.2 注释：随模板版本化），校验侧引用 types.go 同值常量。
const systemTemplate = `你是人才能力评估的评分员。基于下方维度口径、证据与统计参考，对每个维度给出 0-100 整数分与评分理由。只输出一个 JSON 对象，不要输出其他文本。

【输出 JSON schema】
{"dimensions": [{"code": string, "score": 0-100 整数或 null, "insufficient": bool, "rationale": string}]}

【输出纪律】
1. score 为 0-100 整数；证据不足时 score 置 null 且 insufficient 置 true，禁止把证据不足判为低分。
2. rationale 评分理由 200 字以内，只依据证据与统计参考陈述，禁止编造。
3. dimensions 数组覆盖下方维度段的全部维度，code 与维度编码一致。`

// dimensionTemplate 维度段条目模板：名称 + 编码 + 提示词 + 锚点（维度配置原文）。
const dimensionTemplate = `维度编码：%s
维度名称：%s
评分提示词：%s
评分锚点：%s`

// evidenceHeaderTemplate 证据段头部截断披露行（specs §2.4 能力2）。
const evidenceHeaderTemplate = `以下为 %d/%d 个会话档案（按证据密度选取），仅此部分细粒度证据可见：`

// statsGuidance 统计段承接指令（specs §2.2 组装规则表 C01-C07）：与汇总块标签
// 语义呼应的判读纪律文本。
const statsGuidance = `【统计参考判读纪律】
- zero_input_sessions 为零输入会话数（C01）：该部分会话指令证据稀薄，指令类维度证据稀薄时标 insufficient，禁止把证据不足判为低分。
- narrative_absent_sessions 为零叙述会话数（C02）：价值产出与审查把关维度按占比作证据降权参考。
- tool_top10 工具证据仅达工具名与字符数级别（C03），工作域归纳精度有限。
- cmd_reuse_groups 为同首指令命中组数（C04）：CmdReuseHashes 同首指令命中一律按重做/重试解读，不作知识沉淀复用证据计分，不设间隔条件。
- 粘贴量按 paste_char_count、paste_msg_sessions、user_msg_count 三量联合判读（C05）：单一事件主导的粘贴量不读作持续高约束供给习惯。
- failed_profiles 为抽取失败档案数（C06）：该部分会话仅统计块可用，证据缺失。
- continuation_sessions 为续接会话数（C07）：续接推进会话的叙述混有上会话回顾，指令与价值判读须联合识别，防双重计数。`

// instructionTail 指令段（specs §2.4 能力1）：输出硬约束复述。空提示词维度
// 不进维度段（specs §3.2 维度配置缺失：代码侧确定性 insufficient，不占 LLM 上下文）。
const instructionTail = `【输出要求】
- 每维度输出 0-100 整数分；证据不足标 insufficient 且 score 置 null，score 非 null 时须为 0-100 整数，禁止把证据不足判为低分。
- rationale 理由 200 字以内。`

// buildPrompt 五段组装（specs §2.4 能力1）：系统段 + 维度段 + 证据段 + 统计段 +
// 指令段。specs 只含有提示词维度；入参与产出都不含 token_name 人名（组装前剥离）；
// 人群签名不进 prompt（C08-C12 承接在能力4 规则侧）。
func buildPrompt(specs []DimensionSpec, set *ProfileSet) string {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(systemTemplate)

	b.WriteString("\n\n【维度段】（维度口径，逐维度评分）\n")
	for _, s := range specs {
		fmt.Fprintf(&b, dimensionTemplate+"\n\n", s.Code, s.Name, s.PromptText, s.AnchorText)
	}

	b.WriteString("\n【证据段】\n")
	if set != nil {
		fmt.Fprintf(&b, evidenceHeaderTemplate+"\n", set.VisibleCount, set.TotalSuccess)
		for _, blk := range set.SessionBlocks {
			b.WriteString(blk)
			b.WriteString("\n---\n")
		}
	}

	b.WriteString("\n【统计段】周期统计汇总（规则确定性产出，全量统计，不受证据段截取影响）\n")
	if set != nil {
		b.WriteString(set.SummaryBlock)
	}
	b.WriteString("\n")
	b.WriteString(statsGuidance)

	b.WriteString("\n\n")
	b.WriteString(instructionTail)
	return b.String()
}
