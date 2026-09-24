package questiongen

// prompt_test.go 契约测试：buildQuestionPrompt 组装（specs 4.3.1 管理情境 /
// 4.3.2 维度混合出题 / 4.1.2 E 三段文本与长度约束）与 parseQuestionOutput
// 宽容解析（StripFences + schema 校验 1000/2000/500 rune）。

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func testDim() DimensionSpec {
	return DimensionSpec{ID: 101, Name: "授权与分工", Description: "考察管理者向下属授权、划分职责的判断与执行"}
}

// TestBuildQuestionPromptContainsDimension 锚点（BR3）：prompt 含维度名与说明原文。
func TestBuildQuestionPromptContainsDimension(t *testing.T) {
	p := buildQuestionPrompt(testDim())
	if !strings.Contains(p, "授权与分工") {
		t.Error("prompt 缺维度名")
	}
	if !strings.Contains(p, "考察管理者向下属授权") {
		t.Error("prompt 缺维度说明原文")
	}
}

// TestBuildQuestionPromptContract 锚点：prompt 声明三字段 JSON schema、
// A/B/C/D 四选项行内书写与三段长度约束（模板纪律与校验同值防漂移）。
func TestBuildQuestionPromptContract(t *testing.T) {
	p := buildQuestionPrompt(testDim())
	checks := []struct{ name, sub string }{
		{"JSON对象", `"scenario"`},
		{"字段2", `"requirement"`},
		{"字段3", `"focus_point"`},
		{"四选项", "A/B/C/D"},
		{"情境约束", "1000"},
		{"要求约束", "2000"},
		{"考察点约束", "500"},
		{"管理情境", "管理"},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("prompt 缺%s（%q）", c.name, c.sub)
		}
	}
}

// goodJSON 合法单题输出（三段各短文本）。
func goodJSON() string {
	return `{"scenario":"你负责的团队接到紧急交付任务，两名下属对方案分歧明显。","requirement":"请从下列处置方式中选出最贴近你做法的一项，并在后续对话中说明理由。\nA. 亲自裁定方案\nB. 授权下属试验后汇报\nC. 上报上级决策\nD. 暂缓等待更多信息","focus_point":"授权判断与责任分配"}`
}

// fencedJSON 围栏包裹形态。
func fencedJSON() string {
	return "```json\n" + goodJSON() + "\n```"
}

// TestParseQuestionOutputValid 锚点：裸 JSON 与围栏包裹均解析成功，字段回填。
func TestParseQuestionOutputValid(t *testing.T) {
	for name, raw := range map[string]string{"裸JSON": goodJSON(), "围栏": fencedJSON()} {
		q, err := parseQuestionOutput(raw)
		if err != nil {
			t.Fatalf("%s 解析失败: %v", name, err)
		}
		if q.Scenario == "" || q.Requirement == "" || q.FocusPoint == "" {
			t.Errorf("%s 三段文本应非空: %+v", name, q)
		}
		if !strings.Contains(q.Requirement, "A.") || !strings.Contains(q.Requirement, "D.") {
			t.Errorf("%s requirement 应含 A/D 选项原文", name)
		}
	}
}

// TestParseQuestionOutputRejectsOverlong 核心断言（BR4）：scenario 1001 rune
// 判 ErrSchemaInvalid；requirement 2001、focus_point 501 同判。
func TestParseQuestionOutputRejectsOverlong(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("长", n) }
	cases := []struct {
		name string
		raw  string
	}{
		{"scenario_1001", `{"scenario":"` + pad(1001) + `","requirement":"要求","focus_point":"考察"}`},
		{"requirement_2001", `{"scenario":"情境","requirement":"` + pad(2001) + `","focus_point":"考察"}`},
		{"focus_point_501", `{"scenario":"情境","requirement":"要求","focus_point":"` + pad(501) + `"}`},
	}
	for _, c := range cases {
		if _, err := parseQuestionOutput(c.raw); !errors.Is(err, ErrSchemaInvalid) {
			t.Errorf("%s 应判 ErrSchemaInvalid, got %v", c.name, err)
		}
	}
}

// TestParseQuestionOutputBoundaryAccepted 边界值恰好在上限内应放行（BR4 上界含）。
func TestParseQuestionOutputBoundaryAccepted(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("长", n) }
	raw := `{"scenario":"` + pad(1000) + `","requirement":"` + pad(2000) + `","focus_point":"` + pad(500) + `"}`
	q, err := parseQuestionOutput(raw)
	if err != nil {
		t.Fatalf("边界值应放行: %v", err)
	}
	if utf8.RuneCountInString(q.Scenario) != 1000 {
		t.Errorf("scenario rune 数应 1000, got %d", utf8.RuneCountInString(q.Scenario))
	}
}

// TestParseQuestionOutputRejectsEmpty 与坏结构：空字段、非对象 JSON、
// 纯文本均判 ErrSchemaInvalid。
func TestParseQuestionOutputRejectsEmpty(t *testing.T) {
	cases := map[string]string{
		"空scenario": `{"scenario":"","requirement":"要求","focus_point":"考察"}`,
		"缺字段":       `{"scenario":"情境"}`,
		"数组形态":      `["scenario"]`,
		"纯文本":       "这不是 JSON",
		"空串":        "",
	}
	for name, raw := range cases {
		if _, err := parseQuestionOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
			t.Errorf("%s 应判 ErrSchemaInvalid, got %v", name, err)
		}
	}
}

// TestBuildBatchTitle 顿号连接与 255 rune 截断。
func TestBuildBatchTitle(t *testing.T) {
	two := []DimensionSpec{{Name: "授权与分工"}, {Name: "团队协调"}}
	if got := buildBatchTitle(two); got != "授权与分工、团队协调" {
		t.Errorf("title = %q, want 顿号连接", got)
	}
	var many []DimensionSpec
	for i := range 120 {
		many = append(many, DimensionSpec{Name: strings.Repeat("维", 8) + string(rune('A'+i%26))})
	}
	got := buildBatchTitle(many)
	if utf8.RuneCountInString(got) > 255 {
		t.Errorf("title 应 ≤255 rune, got %d", utf8.RuneCountInString(got))
	}
}
