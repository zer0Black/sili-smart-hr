package extractor

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// mkProfileJSON 构造 LLM 输出形态的档案 JSON。
func mkProfileJSON(summary string, instrTexts []string, beh ProfileBehavior) string {
	segs := make([]string, len(instrTexts))
	for i, t := range instrTexts {
		segs[i] = fmt.Sprintf(`{"text":%q}`, t)
	}
	b := fmt.Sprintf(`{"instruction_specificity":%q,"interrupt_style":%q,"review_ratio":%q,"paste_scale":%q,"narrative_absent":%t}`,
		beh.InstructionSpecificity, beh.InterruptStyle, beh.ReviewRatio, beh.PasteScale, beh.NarrativeAbsent)
	return fmt.Sprintf(`{"summary":%q,"instruction":[%s],"behavior":%s}`, summary, strings.Join(segs, ","), b)
}

var validBeh = ProfileBehavior{
	InstructionSpecificity: "high",
	InterruptStyle:         "rare",
	ReviewRatio:            "low",
	PasteScale:             "moderate",
}

func TestParseProfileValid(t *testing.T) {
	summary := "会话围绕订单导出模块重构，涉及 GORM 查询优化。"
	raw := "```json\n" + mkProfileJSON(summary, []string{"帮我修复导出超时的分页查询"}, validBeh) + "\n```"
	gotSummary, instr, beh, err := parseProfile(raw, ProfileStats{UserMsgCount: 2})
	if err != nil {
		t.Fatalf("合法输入不应报错: %v", err)
	}
	if gotSummary != summary {
		t.Errorf("summary = %q, want %q", gotSummary, summary)
	}
	if len(instr) != 1 || instr[0].Text != "帮我修复导出超时的分页查询" {
		t.Errorf("instruction = %+v, want 单条原文本", instr)
	}
	if beh != validBeh {
		t.Errorf("behavior = %+v, want %+v", beh, validBeh)
	}
}

func TestParseProfileValidPlainWithoutFence(t *testing.T) {
	// 边界：前后空白 + 裸围栏（无 json 语言标注）也应剥离。
	raw := "\n  ```\n" + mkProfileJSON("纯 JSON 摘要。", []string{"一条指令"}, validBeh) + "\n```  \n "
	summary, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("裸围栏与空白包裹应剥离: %v", err)
	}
	if summary != "纯 JSON 摘要。" || len(instr) != 1 {
		t.Errorf("解析值不符: summary=%q instr=%+v", summary, instr)
	}
}

func TestParseProfileSingleLineFence(t *testing.T) {
	// 单行紧凑围栏：```json{...} 语言标注与内容同行无换行，剥语言段后可解析。
	raw := "```json" + mkProfileJSON("单行围栏摘要。", []string{"单行指令"}, validBeh)
	summary, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("单行紧凑围栏应剥语言标注后解析: %v", err)
	}
	if summary != "单行围栏摘要。" || len(instr) != 1 {
		t.Errorf("解析值不符: summary=%q instr=%+v", summary, instr)
	}
	// 语言标注与 JSON 间有空格的常见输出形态：剥标注段后跳过空白再解析。
	spaced := "```json " + mkProfileJSON("空格围栏摘要。", []string{"空格指令"}, validBeh)
	summary, instr, _, err = parseProfile(spaced, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("语言标注后带空格应剥标注与空白后解析: %v", err)
	}
	if summary != "空格围栏摘要。" || len(instr) != 1 || instr[0].Text != "空格指令" {
		t.Errorf("空格形态解析值不符: summary=%q instr=%+v", summary, instr)
	}
	// 裸内容以英文单词打头（非语言标注形态）：不剥词，按非法 JSON 判失败走重试。
	if _, _, _, err := parseProfile("```notjson at all", ProfileStats{UserMsgCount: 1}); !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("围栏内非 JSON 文本应判 ErrSchemaInvalid, got %v", err)
	}
}

// TestParseProfileTrailingFenceSameLine 尾部闭围栏与内容同行的三种形态：闭围栏
// 不独立成行时按字符串尾部 ``` 兜底剥除（剥后以 { 起头才认定包裹）。
func TestParseProfileTrailingFenceSameLine(t *testing.T) {
	body := mkProfileJSON("尾部同行摘要。", []string{"同行指令"}, validBeh)
	cases := []struct {
		name string
		raw  string
	}{
		{"单行带语言标注", "```json" + body + "```"},
		{"单行无标注", "```" + body + "```"},
		{"首行多行尾同行", "```json\n" + body + "```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			summary, instr, _, err := parseProfile(c.raw, ProfileStats{UserMsgCount: 1})
			if err != nil {
				t.Fatalf("%s 应剥尾部闭围栏后解析: %v", c.name, err)
			}
			if summary != "尾部同行摘要。" || len(instr) != 1 || instr[0].Text != "同行指令" {
				t.Errorf("%s 解析值不符: summary=%q instr=%+v", c.name, summary, instr)
			}
		})
	}
	// instruction 摘录内嵌 ``` 且整串恰以 ``` 结尾：JSON 主体的 } 之后还有闭围栏，
	// 不属包裹形态的误剥范畴；此处锁定无独立闭行时不剥（透传交 Unmarshal 裁决）。
	innerTail := "示例代码\n```go\nfmt.Println(x)\n```"
	t.Run("内嵌围栏位于instruction内不误剥", func(t *testing.T) {
		raw := "```json\n" + mkProfileJSON("内嵌围栏摘要。", []string{innerTail}, validBeh)
		summary, _, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
		if err != nil {
			t.Fatalf("内嵌围栏不应破坏解析: %v", err)
		}
		if summary != "内嵌围栏摘要。" {
			t.Errorf("summary = %q, want 原文", summary)
		}
	})
}

// TestParseProfileLeadingExampleObject 回归：前导说明文字内嵌平衡示例对象
//（{"ok":true} 形态）不得劫持平衡对象截取。无 summary 键约束的截取会采到示例
// 对象，Unmarshal 成功但 summary 空，误判 schema 失效消耗重试并可能误落 failed。
func TestParseProfileLeadingExampleObject(t *testing.T) {
	body := mkProfileJSON("真实档案摘要内容。", []string{"真实指令"}, validBeh)
	raw := "好的，示例形如 {\"ok\":true} 如下：\n" + body
	summary, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("前导示例对象不应劫持截取: %v", err)
	}
	if summary != "真实档案摘要内容。" || len(instr) != 1 {
		t.Errorf("解析值 = %q %+v, want 真实档案三块", summary, instr)
	}
}

func TestParseProfileInvalid(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		stats ProfileStats
	}{
		{"非法JSON", "{not json", ProfileStats{UserMsgCount: 1}},
		{"Summary空", mkProfileJSON("", []string{"指令"}, validBeh), ProfileStats{UserMsgCount: 1}},
		{"Summary空白串", mkProfileJSON("   ", []string{"指令"}, validBeh), ProfileStats{UserMsgCount: 1}},
		{"Summary超两倍500字", mkProfileJSON(strings.Repeat("业", 500), []string{"指令"}, validBeh), ProfileStats{UserMsgCount: 1}},
		{"Summary刚超两倍401字", mkProfileJSON(strings.Repeat("业", 401), []string{"指令"}, validBeh), ProfileStats{UserMsgCount: 1}},
		{"review_ratio枚举外值", mkProfileJSON("摘要", []string{"指令"}, ProfileBehavior{InstructionSpecificity: "high", InterruptStyle: "rare", ReviewRatio: "very_high", PasteScale: "moderate"}), ProfileStats{UserMsgCount: 1}},
		{"interrupt_style枚举外值", mkProfileJSON("摘要", []string{"指令"}, ProfileBehavior{InstructionSpecificity: "high", InterruptStyle: "never", ReviewRatio: "low", PasteScale: "moderate"}), ProfileStats{UserMsgCount: 1}},
		{"instruction_specificity枚举外值", mkProfileJSON("摘要", []string{"指令"}, ProfileBehavior{InstructionSpecificity: "extreme", InterruptStyle: "rare", ReviewRatio: "low", PasteScale: "moderate"}), ProfileStats{UserMsgCount: 1}},
		{"paste_scale值域无no_evidence", mkProfileJSON("摘要", []string{"指令"}, ProfileBehavior{InstructionSpecificity: "high", InterruptStyle: "rare", ReviewRatio: "low", PasteScale: "no_evidence"}), ProfileStats{UserMsgCount: 1}},
		{"零输入会话返回非空Instruction", mkProfileJSON("摘要", []string{"编造的指令"}, validBeh), ProfileStats{UserMsgCount: 0}},
		{"指令片段text空白", mkProfileJSON("摘要", []string{"   "}, validBeh), ProfileStats{UserMsgCount: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := parseProfile(c.raw, c.stats)
			if !errors.Is(err, ErrSchemaInvalid) {
				t.Fatalf("期望 ErrSchemaInvalid, got %v", err)
			}
		})
	}
}

func TestParseProfileZeroInput(t *testing.T) {
	// specs §2.4 能力2 零输入：Instruction 空数组放行，指令类信号 no_evidence。
	beh := ProfileBehavior{InstructionSpecificity: "no_evidence", InterruptStyle: "rare", ReviewRatio: "no_evidence", PasteScale: "none"}
	raw := mkProfileJSON("续接推进的工具型会话，围绕数据管道修复。", []string{}, beh)
	summary, instr, gotBeh, err := parseProfile(raw, ProfileStats{UserMsgCount: 0})
	if err != nil {
		t.Fatalf("零输入空 Instruction 应放行: %v", err)
	}
	if len(instr) != 0 {
		t.Errorf("instruction = %+v, want 空数组", instr)
	}
	if summary == "" || gotBeh != beh {
		t.Errorf("summary/behavior 透传不符: %q %+v", summary, gotBeh)
	}
}

func TestParseProfileZeroNarrative(t *testing.T) {
	// specs §2.4 能力2 零叙述：narrative_absent 形态放行，叙述/指令分母信号 no_evidence。
	beh := ProfileBehavior{InstructionSpecificity: "no_evidence", InterruptStyle: "occasional", ReviewRatio: "no_evidence", PasteScale: "light", NarrativeAbsent: true}
	raw := mkProfileJSON("依据工具行归纳：会话为前端构建配置修复。", []string{"修复构建报错"}, beh)
	_, _, gotBeh, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("narrative_absent 形态应放行: %v", err)
	}
	if !gotBeh.NarrativeAbsent {
		t.Errorf("narrative_absent 未透传: %+v", gotBeh)
	}
}

func TestParseProfileSummaryTwoTimesBoundary(t *testing.T) {
	// 恰 400 字（2 倍上限）不判失败，超才失败（specs: 超约定上限 2 倍判失败）。
	raw := mkProfileJSON(strings.Repeat("业", 400), []string{"指令"}, validBeh)
	summary, _, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("恰 2 倍长度应放行: %v", err)
	}
	if utf8.RuneCountInString(summary) != 400 {
		t.Errorf("summary 长度 = %d, want 400", utf8.RuneCountInString(summary))
	}
}

func TestParseProfileInstructionNullNormalized(t *testing.T) {
	// instruction: null 解码为 nil，成功路径归一空 slice 保证落库序列化为 []。
	raw := `{"summary":"摘要","instruction":null,"behavior":{"instruction_specificity":"high","interrupt_style":"rare","review_ratio":"low","paste_scale":"none","narrative_absent":false}}`
	_, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("null instruction 应放行: %v", err)
	}
	if instr == nil {
		t.Fatal("instruction 应归一为非 nil 空 slice")
	}
}

// TestStripFencesClosingOnOwnLine 回归：instruction 摘录内嵌 ``` 时闭围栏仍按
// 独立行定位，内层围栏不被误当作闭合边界截断 JSON（LastIndex 实现会切坏）。
func TestStripFencesClosingOnOwnLine(t *testing.T) {
	inner := "示例 ```go\nfmt.Println(x)\n``` 代码"
	raw := "```json\n" + mkProfileJSON("带内嵌围栏的摘要。", []string{inner}, validBeh)
	res, _, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("内嵌围栏不应破坏解析: %v", err)
	}
	if res != "带内嵌围栏的摘要。" {
		t.Errorf("summary = %q, want 原文", res)
	}
	// 模型漏写闭围栏：整段透传，JSON 完整时仍解析成功（无假截断），残缺时由
	// Unmarshal 判失败走重试。
	noClose := "```json\n" + mkProfileJSON("无闭围栏。", []string{"指令"}, validBeh)
	res2, _, _, err := parseProfile(noClose, ProfileStats{UserMsgCount: 1})
	if err != nil {
		t.Fatalf("漏闭围栏但 JSON 完整应解析成功: %v", err)
	}
	if res2 != "无闭围栏。" {
		t.Errorf("summary = %q, want 原文", res2)
	}
}

// TestStripFencesLanguageLineVariants 回归：首行已含 {（内容行形态）时不得当语言行
// 整行丢弃。变体：```json{␊ JSON 对象从首行孤立 { 开始续行；```json{...}```␊
// 单行紧凑后跟尾随文本；```{␊ 裸花括号起头。旧实现无条件丢弃首行，JSON 首段丢失。
func TestStripFencesLanguageLineVariants(t *testing.T) {
	// 对象主体：首字符 { 单独在语言行，其余行是对象剩余部分（真实跨行形态）。
	tail := `"summary": "跨行变体摘要。", "instruction": [{"text": "跨行指令"}], "behavior": {"instruction_specificity": "high", "interrupt_style": "rare", "review_ratio": "low", "paste_scale": "none", "narrative_absent": false}}`
	cases := []struct {
		name string
		raw  string
	}{
		{"标注后紧跟花括号即换行", "```json{\n" + tail + "\n```"},
		{"裸花括号起头跨行", "```{\n" + tail + "\n```"},
		{"单行紧凑加尾随文本", "```json{" + tail + "```\n以上是结果。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			summary, instr, _, err := parseProfile(c.raw, ProfileStats{UserMsgCount: 1})
			if err != nil {
				t.Fatalf("%s 应剥壳后正常解析: %v", c.name, err)
			}
			if summary != "跨行变体摘要。" || len(instr) != 1 || instr[0].Text != "跨行指令" {
				t.Errorf("%s 解析值不符: summary=%q instr=%+v", c.name, summary, instr)
			}
		})
	}
	// 既有行为保持：首行确为纯语言标注（无 {）时照常丢弃。
	normal := "```json\n" + mkProfileJSON("常规摘要。", []string{"指令"}, validBeh) + "\n```"
	if _, _, _, err := parseProfile(normal, ProfileStats{UserMsgCount: 1}); err != nil {
		t.Fatalf("纯语言行形态应照常丢弃语言行: %v", err)
	}
}

func TestBuildPrompt(t *testing.T) {
	view := TrimmedView{Lines: []string{
		"[USER] 帮我修复登录超时的问题",
		"[AI] 已定位到连接池配置。",
		"[TOOL] Edit args=2055",
	}, CharCount: 50}
	p := buildPrompt(view, ProfileStats{UserMsgCount: 1, PasteCharCount: 1200})
	checks := []struct{ name, sub string }{
		{"零输入指示", "无 [USER] 标签行"},
		{"零输入空数组指示", "空数组 []"},
		{"零叙述指示", "无 [AI] 标签行"},
		{"零叙述叙述缺失标记", "narrative_absent 置 true"},
		{"摘要长度约束字面值", "200 字以内"},
		{"片段长度约束字面值", "不超 200 字"},
		{"schema指令字段", `"instruction"`},
		{"schema行为字段", `"narrative_absent"`},
		{"视图用户行纯文本进入", "[USER] 帮我修复登录超时的问题"},
		{"视图AI行纯文本进入", "[AI] 已定位到连接池配置。"},
		{"视图工具行纯文本进入", "[TOOL] Edit args=2055"},
		{"统计参考粘贴字符数", "1200"},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("%s: prompt 缺少子串 %q", c.name, c.sub)
		}
	}
}

func TestInstructionTruncateBoundary(t *testing.T) {
	t.Run("500字片段句读边界截断", func(t *testing.T) {
		// 31 rune/句（30 汉字+句号）×16 + 4 字尾 = 500 字。
		big := strings.Repeat(strings.Repeat("字", 30)+"。", 16) + "尾部四字"
		if utf8.RuneCountInString(big) != 500 {
			t.Fatalf("构造长度 = %d, want 500", utf8.RuneCountInString(big))
		}
		raw := mkProfileJSON("正常摘要。", []string{big}, validBeh)
		_, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
		if err != nil {
			t.Fatalf("超长片段走兜底截断不判失败: %v", err)
		}
		got := instr[0].Text
		if !strings.HasSuffix(got, "。...") {
			t.Errorf("截断结果应带句读与省略标记, got %q", tail(got, 10))
		}
		cut := strings.TrimSuffix(got, "...")
		if !strings.HasPrefix(big, cut) {
			t.Error("截断结果应是原文前缀")
		}
		if !strings.HasSuffix(cut, "。") {
			t.Errorf("截断应落在句读边界, got %q", tail(cut, 5))
		}
		if utf8.RuneCountInString(got) > 203 {
			t.Errorf("截断后长度 = %d, 超 200+3", utf8.RuneCountInString(got))
		}
	})
	t.Run("250字区间同样句读边界截断", func(t *testing.T) {
		// 21 rune/句 ×11 + 19 字尾 = 250 字（200-400 区间）。
		mid := strings.Repeat(strings.Repeat("词", 20)+"。", 11) + strings.Repeat("尾", 19)
		if utf8.RuneCountInString(mid) != 250 {
			t.Fatalf("构造长度 = %d, want 250", utf8.RuneCountInString(mid))
		}
		raw := mkProfileJSON("正常摘要。", []string{mid}, validBeh)
		_, instr, _, err := parseProfile(raw, ProfileStats{UserMsgCount: 1})
		if err != nil {
			t.Fatalf("250 字区间同样兜底不判失败: %v", err)
		}
		got := instr[0].Text
		if !strings.HasSuffix(got, "。...") {
			t.Errorf("截断结果应带句读与省略标记, got %q", tail(got, 10))
		}
		if !strings.HasPrefix(mid, strings.TrimSuffix(got, "...")) {
			t.Error("截断结果应是原文前缀")
		}
	})
}

func TestTruncateInstructionFallback(t *testing.T) {
	t.Run("恰200字不截断", func(t *testing.T) {
		exact := strings.Repeat("b", 200)
		if got := truncateInstruction(exact); got != exact {
			t.Errorf("恰 200 字不应截断, got %q", tail(got, 10))
		}
	})
	t.Run("无句读退空白词边界", func(t *testing.T) {
		en := strings.Repeat("word ", 50) // 250 runes, 无句读标点
		got := truncateInstruction(en)
		if !strings.HasSuffix(got, "...") {
			t.Errorf("应带省略标记, got %q", tail(got, 10))
		}
		cut := strings.TrimSuffix(got, "...")
		if !strings.HasSuffix(cut, " ") {
			t.Errorf("无句读时应切在空白词边界, got %q", tail(cut, 5))
		}
		if !strings.HasPrefix(en, cut) {
			t.Error("截断结果应是原文前缀")
		}
	})
	t.Run("连续无界串按上限硬切", func(t *testing.T) {
		hard := strings.Repeat("a", 250)
		want := strings.Repeat("a", 200) + "..."
		if got := truncateInstruction(hard); got != want {
			t.Errorf("无任何边界时按上限硬切, got 长度 %d", utf8.RuneCountInString(got))
		}
	})
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
