package evaluator

// schema_test.go 契约测试：parseScoreOutput 宽容解析与 validateAndConverge
// 白名单收敛（specs §2.4 能力1 评分输出 schema 与校验规则）。

import (
	"errors"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/domain"
)

// scoreSpecs 三维度口径（收敛白名单基准）。
func scoreSpecs() []DimensionSpec {
	return []DimensionSpec{
		{Code: "AI_INSTRUCTION", Name: "指令能力", Module: "AI_USAGE", PromptText: "p1", AnchorText: "a1", Weight: 30, InOverview: true},
		{Code: "AI_VALUE", Name: "价值产出", Module: "AI_USAGE", PromptText: "p2", AnchorText: "a2", Weight: 40, InOverview: true},
		{Code: "AI_REVIEW", Name: "审查把关", Module: "AI_USAGE", PromptText: "p3", AnchorText: "a3", Weight: 30, InOverview: false},
	}
}

// dimJSON 构造单维度 LLM 输出行（score 为 nil 表示 insufficient）。
func dimJSON(code string, score *int, insufficient bool, rationale string) string {
	s := "null"
	if score != nil {
		s = itoa(*score)
	}
	return `{"code":` + quoteJSON(code) + `,"score":` + s +
		`,"insufficient":` + boolJSON(insufficient) + `,"rationale":` + quoteJSON(rationale) + `}`
}

// dimJSONInt 整数 score 形态的 dimJSON 简写。
func dimJSONInt(code string, score int, insufficient bool, rationale string) string {
	return dimJSON(code, &score, insufficient, rationale)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// scorePtr 构造 *scoreNumber（scoreDimension.Score 字段的测试简写）。
func scorePtr(n int) *scoreNumber {
	return &scoreNumber{n: n}
}

// TestParseScoreOutputPlain 纯 JSON 直接解析成功。
func TestParseScoreOutputPlain(t *testing.T) {
	raw := `{"dimensions":[` + dimJSONInt("AI_INSTRUCTION", 72, false, "指令明确") + `]}`
	out, err := parseScoreOutput(raw)
	if err != nil {
		t.Fatalf("合法 JSON 解析失败: %v", err)
	}
	if len(out.Dimensions) != 1 {
		t.Fatalf("维度数 = %d, want 1", len(out.Dimensions))
	}
	d := out.Dimensions[0]
	if d.Code != "AI_INSTRUCTION" || d.Score == nil || d.Score.n != 72 || d.Insufficient || d.Rationale != "指令明确" {
		t.Errorf("维度字段不符: %+v", d)
	}
}

// TestParseScoreOutputFenced 锚点：markdown 围栏包裹的合法 JSON → 解析成功且维度数正确。
func TestParseScoreOutputFenced(t *testing.T) {
	raw := "```json\n" + `{"dimensions":[` +
		dimJSONInt("AI_INSTRUCTION", 60, false, "理由一") + `,` +
		dimJSON("AI_VALUE", nil, true, "证据不足") + `]}` + "\n```"
	out, err := parseScoreOutput(raw)
	if err != nil {
		t.Fatalf("围栏包裹应剥壳解析成功: %v", err)
	}
	if len(out.Dimensions) != 2 {
		t.Fatalf("维度数 = %d, want 2", len(out.Dimensions))
	}
	if out.Dimensions[1].Score != nil {
		t.Errorf("null score 应解码为 nil, got %v", *out.Dimensions[1].Score)
	}
}

// TestParseScoreOutputTrailing 锚点：JSON 后跟解释文字 → 截取平衡对象解析成功。
func TestParseScoreOutputTrailing(t *testing.T) {
	raw := `{"dimensions":[` + dimJSONInt("AI_VALUE", 80, false, "高价值信号") + `]}` +
		"\n以上是各维度评分结果，供参考。"
	out, err := parseScoreOutput(raw)
	if err != nil {
		t.Fatalf("尾随解释文字应截取平衡对象解析成功: %v", err)
	}
	if len(out.Dimensions) != 1 || out.Dimensions[0].Code != "AI_VALUE" {
		t.Errorf("截取解析结果不符: %+v", out.Dimensions)
	}
}

// TestParseScoreOutputLeadingExample 前导说明文字内嵌平衡示例对象（{"ok":true} 形态）
// 不得劫持截取：候选须含 dimensions 键。
func TestParseScoreOutputLeadingExample(t *testing.T) {
	raw := "好的，示例形如 {\"ok\":true} 如下：\n" +
		`{"dimensions":[` + dimJSONInt("AI_REVIEW", 50, false, "审查充分") + `]}`
	out, err := parseScoreOutput(raw)
	if err != nil {
		t.Fatalf("前导示例对象不应劫持截取: %v", err)
	}
	if len(out.Dimensions) != 1 || out.Dimensions[0].Code != "AI_REVIEW" {
		t.Errorf("截取解析结果不符: %+v", out.Dimensions)
	}
}

// TestParseScoreOutputGarbage 锚点：纯文本无 JSON → ErrSchemaInvalid。
func TestParseScoreOutputGarbage(t *testing.T) {
	if _, err := parseScoreOutput("抱歉，我无法完成这个任务。"); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("纯文本应判 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseScoreOutputEmptyObject 空 JSON 对象（无 dimensions 键）判 ErrSchemaInvalid。
func TestParseScoreOutputEmptyObject(t *testing.T) {
	if _, err := parseScoreOutput("{}"); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("无 dimensions 键应判 ErrSchemaInvalid, got %v", err)
	}
}

// TestValidateConvergeBaseline 全部命中且合法：透传逐 spec 定位。
func TestValidateConvergeBaseline(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_VALUE", Score: scorePtr(88), Rationale: "价值理由"},
		{Code: "AI_INSTRUCTION", Score: scorePtr(72), Rationale: "指令理由"},
		{Code: "AI_REVIEW", Score: scorePtr(60), Rationale: "审查理由"},
	}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("合法输出不应报错: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("行数 = %d, want 3", len(got))
	}
	if got[0].DimensionCode != "AI_INSTRUCTION" || got[0].Score != 72 || got[0].Status != "success" || got[0].Insufficient {
		t.Errorf("行[0] 不符（应按 spec 定位）: %+v", got[0])
	}
	if got[2].DimensionCode != "AI_REVIEW" || got[2].Score != 60 {
		t.Errorf("行[2] 不符: %+v", got[2])
	}
}

// TestValidateConvergeUnknownCode 锚点（BR4）：输出含未知 code + 缺 1 维 →
// 未知丢弃、缺失维补 insufficient 行、总行数 == len(specs)。
func TestValidateConvergeUnknownCode(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_INSTRUCTION", Score: scorePtr(72), Rationale: "指令理由"},
		{Code: "AI_HALLUCINATED", Score: scorePtr(72), Rationale: "幻觉维度"},
	}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("未知 code 应丢弃不报错: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("总行数 = %d, want 3（== len(specs)）", len(got))
	}
	codes := map[string]domain.DimensionScore{}
	for _, row := range got {
		codes[row.DimensionCode] = row
	}
	if _, ok := codes["AI_HALLUCINATED"]; ok {
		t.Error("未知 code 应被丢弃")
	}
	miss := codes["AI_VALUE"]
	if !miss.Insufficient || miss.Score != 0 || miss.Status != "success" {
		t.Errorf("缺失维应补 insufficient 行: %+v", miss)
	}
	if miss.Rationale == "" {
		t.Error("缺失维 rationale 应为缺省文案")
	}
	miss2 := codes["AI_REVIEW"]
	if !miss2.Insufficient || miss2.Score != 0 {
		t.Errorf("缺失维（AI_REVIEW）同样补 insufficient 行: %+v", miss2)
	}
}

// TestValidateConvergeScoreRange 锚点（BR5）：-5 与 105 判 ErrSchemaInvalid；
// 0 与 100 边界通过。
func TestValidateConvergeScoreRange(t *testing.T) {
	for _, bad := range []int{-5, 105} {
		bad := bad
		out := &scoreOutput{Dimensions: []scoreDimension{
			{Code: "AI_INSTRUCTION", Score: scorePtr(bad), Rationale: "越界"},
		}}
		if _, err := validateAndConverge(out, scoreSpecs()); !errors.Is(err, ErrSchemaInvalid) {
			t.Errorf("score=%d 应判 ErrSchemaInvalid, got %v", bad, err)
		}
	}
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_INSTRUCTION", Score: scorePtr(0), Rationale: "下界"},
		{Code: "AI_VALUE", Score: scorePtr(100), Rationale: "上界"},
	}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("边界 0/100 应通过: %v", err)
	}
	if got[0].Score != 0 || got[0].Insufficient || got[1].Score != 100 {
		t.Errorf("边界值行不符: %+v %+v", got[0], got[1])
	}
}

// TestValidateConvergeRationaleLimit 锚点（BR6）：500 字判 ErrSchemaInvalid，
// 399 字通过（含缺失补行场景的默认文案对照）。
func TestValidateConvergeRationaleLimit(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_INSTRUCTION", Score: scorePtr(50), Rationale: strings.Repeat("理", 500)},
	}}
	if _, err := validateAndConverge(out, scoreSpecs()); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("500 字 rationale 应判 ErrSchemaInvalid, got %v", err)
	}
	ok := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_INSTRUCTION", Score: scorePtr(50), Rationale: strings.Repeat("理", 399)},
	}}
	got, err := validateAndConverge(ok, scoreSpecs())
	if err != nil {
		t.Fatalf("399 字 rationale 应通过: %v", err)
	}
	if got[0].Rationale != strings.Repeat("理", 399) {
		t.Errorf("399 字 rationale 应原样保留")
	}
}

// TestValidateConvergeInsufficientNullScore 锚点（BR7）：insufficient=true 且
// score=72 → 收敛行 Score=0、Insufficient=true、Status=success、rationale 保留。
func TestValidateConvergeInsufficientNullScore(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_INSTRUCTION", Score: scorePtr(72), Insufficient: true, Rationale: "证据不足说明"},
	}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("insufficient=true 且 score 非 null 应收敛不报错: %v", err)
	}
	row := got[0]
	if row.Score != 0 || !row.Insufficient || row.Status != "success" {
		t.Errorf("收敛行应为 Score=0/Insufficient=true/Status=success: %+v", row)
	}
	if row.Rationale != "证据不足说明" {
		t.Errorf("rationale 应保留 LLM 输出: %q", row.Rationale)
	}
}

// TestValidateConvergeEmptyDimensions LLM 输出 dimensions 空数组：全部维度补
// insufficient 行（缺失补行通道的全量形态）。
func TestValidateConvergeEmptyDimensions(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("空 dimensions 应全量补行不报错: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("行数 = %d, want 3", len(got))
	}
	for _, row := range got {
		if !row.Insufficient || row.Score != 0 || row.Status != "success" {
			t.Errorf("全量补行应均 insufficient: %+v", row)
		}
	}
}

// TestValidateConvergeFieldFill 收敛行 Module/Source 字段按 spec 与常量填充。
func TestValidateConvergeFieldFill(t *testing.T) {
	out := &scoreOutput{Dimensions: []scoreDimension{
		{Code: "AI_REVIEW", Score: scorePtr(50), Rationale: "审查理由"},
	}}
	got, err := validateAndConverge(out, scoreSpecs())
	if err != nil {
		t.Fatalf("收敛失败: %v", err)
	}
	row := got[2]
	if row.Module != "AI_USAGE" {
		t.Errorf("Module = %q, want AI_USAGE", row.Module)
	}
	if row.Source != "conversation" {
		t.Errorf("Source = %q, want conversation", row.Source)
	}
}

// TestParseScoreOutputFloatForm 补充：模型输出 78.0 浮点形态（OpenAI 兼容通道
// 无严格 integer schema 约束时的常见形态）应收敛为 78 而非解码失败。
func TestParseScoreOutputFloatForm(t *testing.T) {
	raw := `{"dimensions":[{"code":"AI_INSTRUCTION","score":78.0,"insufficient":false,"rationale":"指令清晰"}]}`
	out, err := parseScoreOutput(raw)
	if err != nil {
		t.Fatalf("浮点整数形态应解析成功: %v", err)
	}
	d := out.Dimensions[0]
	if d.Score == nil || d.Score.n != 78 {
		t.Errorf("score = %+v, want 78", d.Score)
	}
}

// TestParseScoreOutputFractionalRejected 补充：非整数小数（78.5）解码失败走
// ErrSchemaInvalid 重试通道。
func TestParseScoreOutputFractionalRejected(t *testing.T) {
	raw := `{"dimensions":[{"code":"AI_INSTRUCTION","score":78.5,"insufficient":false,"rationale":"指令清晰"}]}`
	if _, err := parseScoreOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("非整数 score 应判 ErrSchemaInvalid, got %v", err)
	}
}
