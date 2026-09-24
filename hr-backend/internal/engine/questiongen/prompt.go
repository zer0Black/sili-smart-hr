package questiongen

// prompt.go 出题 prompt 构造与输出解析（specs 4.3.1/4.3.2/4.1.2 E）：
// 纯函数，解析剥围栏 + schema 校验（evaluator 同构范式）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/engine/extractor"
)

// ErrSchemaInvalid 出题输出未通过 schema 校验：重试一次，仍失败判业务失败。
var ErrSchemaInvalid = errors.New("questiongen: question schema invalid")

// 三段文本长度上限（specs 4.1.2 E）：模板文本内写同值字面量，此处校验引用，
// 单一来源防漂移。maxBatchTitleRunes 对齐 question_batches.title varchar(255)。
const (
	maxScenarioRunes    = 1000
	maxRequirementRunes = 2000
	maxFocusPointRunes  = 500
	maxBatchTitleRunes  = 255
)

// questionPromptTemplate 出题指令模板（specs 4.3.1：中文管理情境 + 对话式
// 作答无标准答案；4.3.2：按维度分别构造情境）。长度约束字面值与校验常量同值。
const questionPromptTemplate = `你是人才测评的命题专家。请依据下方子能力维度，命制一道 AI 管理能力测评题。只输出一个 JSON 对象，不要输出其他文本。

【子能力维度】
维度名称：%s
维度说明：%s

【命题方向】
1. scenario 为中文管理情境：管理者与下属协作、AI 工具引入与使用、管理决策冲突等真实场景，200 字左右。
2. requirement 为作答要求，并在其中行内书写 A/B/C/D 四个选项（形如 A.…… B.…… C.…… D.……），400 字左右。
3. focus_point 为本题考察点一句话，80 字左右。
4. 情境紧扣维度名称与维度说明；四个选项均为合理管理处置但成熟度不同，无标准答案（员工经对话作答，考察管理判断而非知识记忆）。

【输出 JSON schema】
{"scenario": "...", "requirement": "...", "focus_point": "..."}

【输出纪律】
- scenario 不超过 1000 字，requirement 不超过 2000 字，focus_point 不超过 500 字。`

// buildQuestionPrompt 组装单题出题 prompt（specs 4.3.2：LLM 按维度分别构造情境）。
func buildQuestionPrompt(dim DimensionSpec) string {
	desc := dim.Description
	if desc == "" {
		desc = "（无维度说明，按维度名称命题）"
	}
	return fmt.Sprintf(questionPromptTemplate, dim.Name, desc)
}

// parseQuestionOutput 宽容解析（StripFences + 首个平衡对象兜底）与 schema
// 校验（specs 4.1.2 E：三段 1~1000/1~2000/1~500 rune），失败返回 ErrSchemaInvalid。
func parseQuestionOutput(raw string) (*StagedQuestion, error) {
	body := extractor.StripFences(strings.TrimSpace(raw))
	var out StagedQuestion
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		if trimmed := firstQuestionObject(body); trimmed != "" {
			body = trimmed
			err = json.Unmarshal([]byte(body), &out)
		}
		if err != nil {
			return nil, ErrSchemaInvalid
		}
	}
	out.Scenario = strings.TrimSpace(out.Scenario)
	out.Requirement = strings.TrimSpace(out.Requirement)
	out.FocusPoint = strings.TrimSpace(out.FocusPoint)
	if out.Scenario == "" || utf8.RuneCountInString(out.Scenario) > maxScenarioRunes {
		return nil, ErrSchemaInvalid
	}
	if out.Requirement == "" || utf8.RuneCountInString(out.Requirement) > maxRequirementRunes {
		return nil, ErrSchemaInvalid
	}
	if out.FocusPoint == "" || utf8.RuneCountInString(out.FocusPoint) > maxFocusPointRunes {
		return nil, ErrSchemaInvalid
	}
	return &out, nil
}

// firstQuestionObject 截取首个含 scenario 键的顶层平衡 JSON 对象（防前导
// 说明文字中的示例对象劫持），无则返回空串。
func firstQuestionObject(s string) string {
	from := 0
	for from < len(s) {
		cand := extractor.FirstBalancedJSONObject(s[from:])
		if cand == "" {
			return ""
		}
		var probe StagedQuestion
		if json.Unmarshal([]byte(cand), &probe) == nil && probe.Scenario != "" {
			return cand
		}
		from += strings.Index(s[from:], cand) + len(cand)
	}
	return ""
}

// buildBatchTitle 批次标题（specs 4.1.2 C：生成批次为维度组合描述）：
// 维度名顿号连接，超 255 rune 按 rune 边界截断。
func buildBatchTitle(specs []DimensionSpec) string {
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	title := strings.Join(names, "、")
	if utf8.RuneCountInString(title) > maxBatchTitleRunes {
		title = string([]rune(title)[:maxBatchTitleRunes])
	}
	return title
}
