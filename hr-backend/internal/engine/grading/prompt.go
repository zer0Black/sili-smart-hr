package grading

// prompt.go 阅卷 prompt 构造（specs TST §5.2.2 步骤2，03 §4.4 组装规则）：
// ai_mgmt 评分员与 enneagram 判型师两套模板，纯函数组装。

import (
	"fmt"
	"strings"

	"sili-smart-hr/backend/internal/domain"
)

// PromptVersion 阅卷 prompt 模板版本（04 §3.3 prompt_version 列落库值），
// 模板变更时 +1 同步本常量。v2：作答对话段接入真实记录拼接（P2_TST_002 03 §4.3）。
const PromptVersion = "v2"

// aiMgmtSystemTemplate ai_mgmt 系统段：评分员角色 + JSON schema 约束。
const aiMgmtSystemTemplate = `你是 AI 管理能力测评的评分员。基于下方维度口径、题目全文与员工作答对话，对每个子能力维度给出 0-100 整数分与评分理由。只输出一个 JSON 对象，不要输出其他文本。

【输出 JSON schema】
{"dimensions": [{"code": string, "score": 0-100 整数或 null, "insufficient": bool, "rationale": string}]}

【输出纪律】
1. score 为 0-100 整数；作答证据不足时 score 置 null 且 insufficient 置 true，禁止把证据不足判为低分。
2. rationale 评分理由 200 字以内，只依据作答对话陈述，禁止编造。
3. dimensions 数组覆盖下方维度段的全部维度，code 与维度编码一致。`

// aiMgmtDimensionTemplate 维度段条目：编码 + 名称 + 锚点（评分口径，specs §5.2.3）。
const aiMgmtDimensionTemplate = `维度编码：%s
维度名称：%s
评分锚点：%s`

// aiMgmtQuestionTemplate 题目段条目：情境 + 作答要求全文（快照 ID 现读）。
const aiMgmtQuestionTemplate = `【题目 %s】
情境：%s
作答要求：%s`

// emptyAnswerSectionPlaceholder 作答对话段空态兜底（理论不可达：提交门槛已
// 保证全答，specs §5.2.3、P2_TST_002 03 §4.3）。
const emptyAnswerSectionPlaceholder = "（暂无作答对话记录）"

// answerLineTemplate 作答段条目：题号取行内 QuestionSeq（03 §4.3 逐题一行）。
const answerLineTemplate = "第 %d 题作答：%s"

// aiMgmtInstructionTail 输出要求复述段。
const aiMgmtInstructionTail = `【输出要求】
- 每维度输出 0-100 整数分；证据不足标 insufficient 且 score 置 null，score 非 null 时须为 0-100 整数。
- rationale 理由 200 字以内。`

// buildAIMgmtPrompt 组装 ai_mgmt 阅卷 prompt：系统段 + 维度段 + 题目段 +
// 作答对话段（全过程逐题行）+ 输出要求段。
func buildAIMgmtPrompt(dims []domain.Dimension, questions []domain.Question, answers []domain.AssessmentTestAnswer) string {
	return buildGradingPrompt(
		aiMgmtSystemTemplate,
		"【维度段】（评分口径，逐维度评分）\n",
		func(b *strings.Builder) {
			for _, d := range dims {
				fmt.Fprintf(b, aiMgmtDimensionTemplate+"\n\n", d.Code, d.Name, d.Anchor)
			}
		},
		"【题目段】（任务题目快照全文）\n",
		questions,
		func(b *strings.Builder, q domain.Question) {
			fmt.Fprintf(b, aiMgmtQuestionTemplate+"\n\n", q.QuestionNo, q.Scenario, q.Requirement)
		},
		answers,
		aiMgmtInstructionTail,
	)
}

// enneagramSystemTemplate enneagram 系统段：判型师角色 + 判型输出 schema 约束
// （specs §5.2.2 步骤3：主型、翼型、9 型倾向分布与判定依据）。
const enneagramSystemTemplate = `你是九型人格测评的判型师。基于下方量表题目全文与员工作答对话，判定其九型主型、翼型与 9 型倾向分布。只输出一个 JSON 对象，不要输出其他文本。

【输出 JSON schema】
{"main_type": 1-9 整数, "wing_type": 1-9 整数或 0（无显著翼型）, "distribution": {"1"-"9": 百分比}, "rationale": string}

【输出纪律】
1. main_type 为主型 1-9；wing_type 为与主型相邻的翼型，无显著翼型置 0。
2. distribution 覆盖 "1" 到 "9" 全部九键，各项为 0-100 百分比一位小数，九项总和约 100。
3. rationale 判定依据 200 字以内，只依据作答倾向陈述，禁止编造。`

// enneagramQuestionTemplate 量表题条目：题项陈述 + 作答方式说明
// （量表题自含计分键，型别维度口径不进 prompt，03 §4.4 2c）。
const enneagramQuestionTemplate = `【题目 %s】
题项陈述：%s
作答方式：%s`

// enneagramInstructionTail 判型输出要求复述段。
const enneagramInstructionTail = `【输出要求】
- main_type/wing_type 为 1-9 整数，无显著翼型 wing_type 置 0。
- distribution 九键齐全，各项 0-100 一位小数，总和约 100。
- rationale 判定依据 200 字以内。`

// buildEnneagramPrompt 组装 enneagram 阅卷 prompt：判型师角色 + 量表题全文 +
// 作答对话段（全过程逐题行）+ 输出要求段。
func buildEnneagramPrompt(questions []domain.Question, answers []domain.AssessmentTestAnswer) string {
	return buildGradingPrompt(
		enneagramSystemTemplate,
		"", // 无维度段：量表题自含计分键，型别维度口径不进 prompt（03 §4.4 2c）
		nil,
		"【量表题目段】（任务题目快照全文）\n",
		questions,
		func(b *strings.Builder, q domain.Question) {
			fmt.Fprintf(b, enneagramQuestionTemplate+"\n\n", q.QuestionNo, q.Scenario, q.Requirement)
		},
		answers,
		enneagramInstructionTail,
	)
}

// buildGradingPrompt 两类型 prompt 共享组装骨架：系统段 +（可选口径段）+ 题目段 +
// 作答对话段 + 输出要求段。段文案差异由调用方模板承载，骨架单点防两处漂移。
func buildGradingPrompt(
	system string,
	sectionHeader string,
	section func(*strings.Builder),
	questionHeader string,
	questions []domain.Question,
	questionItem func(*strings.Builder, domain.Question),
	answers []domain.AssessmentTestAnswer,
	tail string,
) string {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(system)
	if section != nil {
		b.WriteString("\n\n")
		b.WriteString(sectionHeader)
		section(&b)
		b.WriteString("\n")
	} else {
		b.WriteString("\n\n")
	}
	b.WriteString(questionHeader)
	for _, q := range questions {
		questionItem(&b, q)
	}
	b.WriteString("\n【作答对话段】（员工作答对话全过程）\n")
	// 作答原文不脱敏进段（P2_TST_002 03 §4.3：脱敏约束落库 rationale，prompt
	// 进程内一次性消费保留阅卷证据）；空记录占位兜底。
	if len(answers) == 0 {
		b.WriteString(emptyAnswerSectionPlaceholder)
	} else {
		for _, a := range answers {
			fmt.Fprintf(&b, answerLineTemplate+"\n", a.QuestionSeq, a.Content)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(tail)
	return b.String()
}
