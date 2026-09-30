package grading

// prompt_test.go 契约测试：双类型 prompt 组装（specs TST §5.2.2 步骤2、03 §4.4 2、
// P2_TST_002 03 §4.3 作答段接入）。维度段含编码/名称/锚点、题目段含情境/作答要求全文、
// 作答对话段逐题行拼接、输出 schema 约束在模板内。

import (
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/domain"
)

func testDims() []domain.Dimension {
	return []domain.Dimension{
		{Code: "MGT_DELEGATE", Name: "授权与分工", ModuleCode: domain.ModuleAIMgmt,
			DataSource: domain.SourceTest, Anchor: "90+ 充分授权；<40 事必躬亲",
			Weight: 60, IncludeOverview: true, Enabled: true},
		{Code: "MGT_REVIEW", Name: "把关与审查", ModuleCode: domain.ModuleAIMgmt,
			DataSource: domain.SourceTest, Anchor: "90+ 严格把关；<40 放任输出",
			Weight: 40, IncludeOverview: true, Enabled: true},
	}
}

func testQuestions() []domain.Question {
	return []domain.Question{
		{QuestionNo: "Q-AG-0001", Source: domain.QuestionSourceAI,
			Scenario: "情境一：团队接到紧急交付。", Requirement: "要求一：选出处置方式。"},
		{QuestionNo: "Q-AG-0002", Source: domain.QuestionSourceAI,
			Scenario: "情境二：AI 产出存在争议。", Requirement: "要求二：选出把关方式。"},
	}
}

// scaleQuestions 量表题（题项陈述/作答方式说明语义）。
func scaleQuestions() []domain.Question {
	return []domain.Question{
		{QuestionNo: "Q-Scale-0001", Source: domain.QuestionSourceScale, ScaleKey: domain.ScaleKeyRisoHudson,
			Scenario: "我倾向于看到事物积极的一面。", Requirement: "按认同程度作答。"},
		{QuestionNo: "Q-Scale-0002", Source: domain.QuestionSourceScale, ScaleKey: domain.ScaleKeyRisoHudson,
			Scenario: "我做事追求正确与秩序。", Requirement: "按认同程度作答。"},
	}
}

// testAnswers ai_mgmt 作答记录（seq 升序两行）。
func testAnswers() []domain.AssessmentTestAnswer {
	return []domain.AssessmentTestAnswer{
		{QuestionSeq: 1, Content: "C，因为紧急交付需先授权分工"},
		{QuestionSeq: 2, Content: "A"},
	}
}

// TestBuildAIMgmtPromptContainsContexts 锚点（BR8）：维度段（编码/名称/锚点）与
// 题目段（情境/作答要求全文）入 prompt，输出 schema 含 dimensions 四字段。
func TestBuildAIMgmtPromptContainsContexts(t *testing.T) {
	p := buildAIMgmtPrompt(testDims(), testQuestions(), testAnswers())
	checks := []struct{ name, sub string }{
		{"维度编码", "MGT_DELEGATE"},
		{"维度名称", "授权与分工"},
		{"锚点", "90+ 充分授权"},
		{"情境一", "情境一：团队接到紧急交付。"},
		{"要求一", "要求一：选出处置方式。"},
		{"情境二", "情境二：AI 产出存在争议。"},
		{"schema", `"dimensions"`},
		{"score字段", `"score"`},
		{"insufficient字段", `"insufficient"`},
		{"rationale字段", `"rationale"`},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("prompt 缺%s（%q）", c.name, c.sub)
		}
	}
}

// TestBuildAIMgmtPromptWithAnswers 核心断言（BR1）：作答对话段逐题行拼接，
// 行内题号取 QuestionSeq、内容为作答原文（全过程对话非仅最终选项）。
func TestBuildAIMgmtPromptWithAnswers(t *testing.T) {
	p := buildAIMgmtPrompt(testDims(), testQuestions(), testAnswers())
	checks := []struct{ name, sub string }{
		{"作答段标题", "【作答对话段】"},
		{"第1题作答", "第 1 题作答：C，因为紧急交付需先授权分工"},
		{"第2题作答", "第 2 题作答：A"},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("prompt 缺%s（%q）", c.name, c.sub)
		}
	}
	if strings.Contains(p, emptyAnswerSectionPlaceholder) {
		t.Error("有作答记录时不应保留空段占位")
	}
}

// TestBuildPromptEmptyAnswersFallback 核心断言：answers 空时保留占位文案兜底
// （理论不可达，提交门槛已保证全答，03 §4.3），且无作答行。
func TestBuildPromptEmptyAnswersFallback(t *testing.T) {
	for name, p := range map[string]string{
		"ai_mgmt":   buildAIMgmtPrompt(testDims(), testQuestions(), nil),
		"enneagram": buildEnneagramPrompt(scaleQuestions(), nil),
	} {
		if !strings.Contains(p, "【作答对话段】") {
			t.Errorf("%s prompt 缺作答对话段标题", name)
		}
		if !strings.Contains(p, emptyAnswerSectionPlaceholder) {
			t.Errorf("%s prompt 空作答应保留占位文案 %q", name, emptyAnswerSectionPlaceholder)
		}
		if strings.Contains(p, "题作答：") {
			t.Errorf("%s prompt 空作答应无作答行", name)
		}
	}
}

// TestBuildEnneagramPromptWithAnswers 核心断言：量表题作答（数字串）同样
// 逐题行入作答对话段。
func TestBuildEnneagramPromptWithAnswers(t *testing.T) {
	answers := []domain.AssessmentTestAnswer{
		{QuestionSeq: 1, Content: "4"},
		{QuestionSeq: 2, Content: "5"},
	}
	p := buildEnneagramPrompt(scaleQuestions(), answers)
	if !strings.Contains(p, "【作答对话段】") {
		t.Error("prompt 缺作答对话段标题")
	}
	if !strings.Contains(p, "第 1 题作答：4") || !strings.Contains(p, "第 2 题作答：5") {
		t.Errorf("prompt 缺量表题作答行: %s", p)
	}
}

// TestBuildEnneagramPromptContainsContexts 锚点（BR2）：量表题全文入 prompt、
// 判型输出 schema 四字段、维度口径不进 prompt。
func TestBuildEnneagramPromptContainsContexts(t *testing.T) {
	p := buildEnneagramPrompt(scaleQuestions(), testAnswers())
	checks := []struct{ name, sub string }{
		{"题项陈述", "我倾向于看到事物积极的一面。"},
		{"作答方式", "按认同程度作答。"},
		{"main_type", `"main_type"`},
		{"wing_type", `"wing_type"`},
		{"distribution", `"distribution"`},
		{"rationale", `"rationale"`},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("prompt 缺%s（%q）", c.name, c.sub)
		}
	}
	if strings.Contains(p, "MGT_") {
		t.Error("enneagram prompt 不应含型别维度口径")
	}
}

// TestPromptVersionBumped 核心断言：作答段模板变更后 PromptVersion 必须升 v2
// （落库标记，后续幂等重评按版本比对触发）。
func TestPromptVersionBumped(t *testing.T) {
	if PromptVersion != "v2" {
		t.Fatalf("PromptVersion = %q, want v2", PromptVersion)
	}
}
