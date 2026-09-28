package grading

// prompt_test.go 契约测试：双类型 prompt 组装（specs TST §5.2.2 步骤2、03 §4.4 2）。
// 维度段含编码/名称/锚点、题目段含情境/作答要求全文、输出 schema 约束在模板内。

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

// TestBuildAIMgmtPromptContainsContexts 锚点（BR8）：维度段（编码/名称/锚点）与
// 题目段（情境/作答要求全文）入 prompt，输出 schema 含 dimensions 四字段。
func TestBuildAIMgmtPromptContainsContexts(t *testing.T) {
	p := buildAIMgmtPrompt(testDims(), testQuestions())
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

// TestBuildAIMgmtPromptEmptyAnswerSection 补充：作答对话段当前无来源时空段占位。
func TestBuildAIMgmtPromptEmptyAnswerSection(t *testing.T) {
	p := buildAIMgmtPrompt(testDims(), testQuestions())
	if !strings.Contains(p, "【作答对话段】") {
		t.Error("prompt 缺作答对话段标题")
	}
	if strings.Contains(p, "员工：") {
		t.Error("无作答来源时对话段应空段占位")
	}
}

// TestBuildEnneagramPromptContainsContexts 锚点（BR2）：量表题全文入 prompt、
// 判型输出 schema 四字段、维度口径不进 prompt。
func TestBuildEnneagramPromptContainsContexts(t *testing.T) {
	p := buildEnneagramPrompt(scaleQuestions())
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
