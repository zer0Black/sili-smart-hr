package evaluator

// prompt_test.go 契约测试：buildPrompt 五段组装（specs §2.4 能力1 prompt 五段结构、
// §2.2 组装规则表 C01-C07 承接、§3.3 人名不进 LLM 上下文）。

import (
	"strings"
	"testing"
)

// promptSpecs 构造三维度口径（其一 PromptText 为空，锚定指令段披露）。
func promptSpecs() []DimensionSpec {
	return []DimensionSpec{
		{Code: "AI_INSTRUCTION", Name: "指令能力", Module: "AI_USAGE",
			PromptText: "评估用户指令的明确与细致程度", AnchorText: "90-100 指令具体可执行；0-9 无有效指令", Weight: 30, InOverview: true},
		{Code: "AI_VALUE", Name: "价值产出", Module: "AI_USAGE",
			PromptText: "评估任务价值信号", AnchorText: "80-89 高价值；10-19 低价值", Weight: 40, InOverview: true},
		{Code: "AI_REVIEW", Name: "审查把关", Module: "AI_USAGE",
			PromptText: "", AnchorText: "70-79 审查充分", Weight: 30, InOverview: false},
	}
}

// promptSet 构造 3/5 披露口径的档案集（5 个 success 仅 3 块可见）。
func promptSet() *ProfileSet {
	return &ProfileSet{
		SummaryBlock: "sessions_total: 5\nuser_msg_count: 30\nzero_input_sessions: 1\npaste_char_count: 1200\npaste_msg_sessions: 2\ncmd_reuse_groups: 3\ncontinuation_sessions: 1\nnarrative_absent_sessions: 2\nfailed_profiles: 1\n",
		SessionBlocks: []string{
			"session_key: s-1\nsummary: 调试服务启动问题\nbehavior: {\"paste_scale\":\"moderate\"}",
			"session_key: s-2\nsummary: 修复导出超时\nbehavior: {\"paste_scale\":\"light\"}",
			"session_key: s-3\nsummary: 构建配置调整\nbehavior: {\"paste_scale\":\"none\"}",
		},
		VisibleCount: 3,
		TotalSuccess: 5,
	}
}

// TestBuildPromptNoTokenName 锚点（BR8，specs §3.3）：prompt 全文不含人名。
// 入参本身无人名（剥离由调用侧保证），本函数义务是不引入任何人名。
func TestBuildPromptNoTokenName(t *testing.T) {
	p := buildPrompt(promptSpecs(), promptSet())
	if strings.Contains(p, "李雪涛") {
		t.Error("prompt 不应包含人名 李雪涛")
	}
	if strings.Contains(p, "张三") {
		t.Error("prompt 不应包含人名 张三")
	}
}

// TestBuildPromptFiveSegments 锚点（specs §2.4 能力1 五段结构）：维度段含各维度
// 名称与锚点原文、证据段含 3/5 截断披露行、统计段含汇总块原文、指令段含 0-100 分制。
func TestBuildPromptFiveSegments(t *testing.T) {
	specs := promptSpecs()
	set := promptSet()
	p := buildPrompt(specs, set)
	checks := []struct{ name, sub string }{
		{"系统段评分员角色", "评分"},
		{"系统段JSON约束", "JSON"},
		{"系统段dimensions数组", `"dimensions"`},
		{"维度段名称1", "指令能力"},
		{"维度段名称2", "价值产出"},
		{"维度段名称3", "审查把关"},
		{"维度段锚点1", "90-100 指令具体可执行"},
		{"维度段锚点2", "80-89 高价值"},
		{"维度段提示词1", "评估用户指令的明确与细致程度"},
		{"证据段披露行", "3/5 个会话档案"},
		{"证据段密度披露", "按证据密度选取"},
		{"证据块1", "session_key: s-1"},
		{"证据块2", "session_key: s-2"},
		{"统计段汇总块粘贴合计", "paste_char_count: 1200"},
		{"统计段汇总块同首指令组数", "cmd_reuse_groups: 3"},
		{"统计段汇总块failed数", "failed_profiles: 1"},
		{"统计段C01零输入承接", "zero_input_sessions"},
		{"统计段C02降权承接", "narrative_absent"},
		{"统计段C04重做解读承接", "重做"},
		{"统计段C05粘贴联合判读承接", "粘贴"},
		{"统计段C07防双重计数承接", "双重计数"},
		{"指令段分制", "0-100"},
		{"指令段insufficient指示", "insufficient"},
		{"指令段score置null", "null"},
		{"指令段理由字数", "200 字"},
		{"指令段证据不足禁低分", "证据不足"},
	}
	for _, c := range checks {
		if !strings.Contains(p, c.sub) {
			t.Errorf("%s: prompt 缺少子串 %q", c.name, c.sub)
		}
	}
}

// TestBuildPromptEmptyPromptTextDimension 维度段 PromptText 为空的维度照常入段
//（specs §2.2 维度段：来自维度配置原文），且指令段标注该维度证据不足时标 insufficient。
func TestBuildPromptEmptyPromptTextDimension(t *testing.T) {
	p := buildPrompt(promptSpecs(), promptSet())
	if !strings.Contains(p, "审查把关") {
		t.Error("PromptText 为空的维度仍应进维度段")
	}
}

// TestBuildPromptEmptyBlocks 证据段零块边界：披露行 0/0，不 panic。
func TestBuildPromptEmptyBlocks(t *testing.T) {
	set := &ProfileSet{SummaryBlock: "sessions_total: 0\n", SessionBlocks: []string{}}
	p := buildPrompt(promptSpecs(), set)
	if !strings.Contains(p, "0/0 个会话档案") {
		t.Errorf("空块披露行应为 0/0:\n%.200s", p)
	}
}

// TestBuildPromptNilSet set 为 nil 边界不 panic。
func TestBuildPromptNilSet(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil set panic: %v", r)
		}
	}()
	p := buildPrompt(promptSpecs(), nil)
	if !strings.Contains(p, "指令能力") {
		t.Error("nil set 维度段仍应组装")
	}
}

// TestBuildPromptEmptySpecs specs 为空边界：维度段空仍产出其余段，不 panic。
func TestBuildPromptEmptySpecs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("空 specs panic: %v", r)
		}
	}()
	p := buildPrompt(nil, promptSet())
	if !strings.Contains(p, "3/5 个会话档案") {
		t.Error("空 specs 证据段披露行仍应组装")
	}
}
