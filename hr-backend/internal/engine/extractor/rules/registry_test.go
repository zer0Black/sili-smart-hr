package rules

import (
	"slices"
	"testing"

	"sili-smart-hr/backend/internal/engine/extractor/rules/baseline"
)

// fakeClient 模拟新客户端接入：四方法实现 + Register 一行即挂载（specs §2.5 高级用法）。
// contributing 指针控制贡献窗口，测试结束后复原为零贡献空壳（注册表只增不减，
// 窗口复原模式同 detect_test.go 的 detectFakeClient）。
type fakeClient struct {
	contributing *bool
}

func (f *fakeClient) Client() string { return "fake_for_test" }
func (f *fakeClient) UserPrefixes() []string {
	if f == nil || f.contributing == nil || !*f.contributing {
		return nil
	}
	return []string{"<fake-frame>"}
}
func (f *fakeClient) NonUserPrefixes() []string { return nil }
func (f *fakeClient) DetectFeatures() []DetectFeature {
	if f == nil || f.contributing == nil || !*f.contributing {
		return nil
	}
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "<fake-frame>", Weight: 1, Once: true},
	}
}

func clientNames(rs []ClientRules) []string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Client()
	}
	return names
}

// TestAllClientRulesRegistry 是注册表核心断言：
// init 注册 5 个客户端且顺序按文件名字典序；手动 Register fake 后长度 +1 且前面顺序不变。
// fake 以贡献窗口模式注册（defer 复原后零贡献），防污染同包后续用例。
func TestAllClientRulesRegistry(t *testing.T) {
	t.Run("init注册五个客户端且顺序按文件名字典序", func(t *testing.T) {
		got := AllClientRules()
		want := []string{ClientAutomation, ClientClaudeCode, ClientOMO, ClientOpenCode, ClientWorkBuddy}
		if len(got) != len(want) {
			t.Fatalf("注册数 = %d, 期望 %d（%v）", len(got), len(want), clientNames(got))
		}
		for i, w := range want {
			if got[i].Client() != w {
				t.Errorf("AllClientRules()[%d].Client() = %q, 期望 %q", i, got[i].Client(), w)
			}
		}
	})

	t.Run("手动Register追加fake且前面顺序不变", func(t *testing.T) {
		before := clientNames(AllClientRules())
		restore := registerFakeClient()
		defer restore()

		after := AllClientRules()
		if len(after) != len(before)+1 {
			t.Fatalf("注册 fake 后长度 = %d, 期望 %d+1", len(after), len(before))
		}
		if last := after[len(after)-1].Client(); last != "fake_for_test" {
			t.Errorf("新注册项落位末尾失败: got %q, 期望 fake_for_test", last)
		}
		for i, w := range before {
			if after[i].Client() != w {
				t.Errorf("既有顺序被扰动: after[%d] = %q, 期望 %q", i, after[i].Client(), w)
			}
		}
	})
}

// registerFakeClient 注册 fakeClient 并返回复原函数（贡献窗口关闭后对
// AllClientRules 枚举面零贡献，防污染同测试二进制内后续用例）。
func registerFakeClient() func() {
	on := true
	Register(&fakeClient{contributing: &on})
	return func() { on = false }
}

// probeClient 是写穿透探针：标识值与任何注册项不同，避免与既有注册值撞车。
type probeClient struct{}

func (probeClient) Client() string                  { return "probe_write_through" }
func (probeClient) UserPrefixes() []string          { return nil }
func (probeClient) NonUserPrefixes() []string       { return nil }
func (probeClient) DetectFeatures() []DetectFeature { return nil }

// TestAllClientRulesReturnsCopy 断言返回值是内部切片拷贝：外部改写不穿透注册表。
func TestAllClientRulesReturnsCopy(t *testing.T) {
	first := AllClientRules()
	if len(first) == 0 {
		restore := registerFakeClient() // 保证有可改写元素（注册表无反注册，仅追加）
		defer restore()
		first = AllClientRules()
	}
	original := first[0].Client()
	first[0] = probeClient{} // 外部写穿透探针：改写首元素

	again := AllClientRules()
	if again[0].Client() != original {
		t.Errorf("外部改写穿透注册表: again[0] = %q, 期望内部值 %q", again[0].Client(), original)
	}
}

// TestClientConstants 锚定七值标识单源（specs §2.3 值域）。
func TestClientConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ClientClaudeCode", ClientClaudeCode, "claude_code"},
		{"ClientOpenCode", ClientOpenCode, "opencode"},
		{"ClientWorkBuddy", ClientWorkBuddy, "workbuddy"},
		{"ClientOMO", ClientOMO, "omo"},
		{"ClientAutomation", ClientAutomation, "automation"},
		{"ClientMixed", ClientMixed, "mixed"},
		{"ClientUnknown", ClientUnknown, "unknown"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, 期望 %q", c.name, c.got, c.want)
		}
	}
}

// TestFeatureKindConstants 锚定特征 Kind 值域两字面量。
func TestFeatureKindConstants(t *testing.T) {
	if FeatureKindPrefix != "prefix" {
		t.Errorf("FeatureKindPrefix = %q, 期望 prefix", FeatureKindPrefix)
	}
	if FeatureKindMarker != "marker" {
		t.Errorf("FeatureKindMarker = %q, 期望 marker", FeatureKindMarker)
	}
}

// baselineUserPrefixes/baselineNonUserPrefixes 快照已收敛 rules/baseline 包单源
//（两包等集断言共用，防各自抄写 59 条清单漏改一份）。
var (
	baselineUserPrefixes    = baseline.UserPrefixes
	baselineNonUserPrefixes = baseline.NonUserPrefixes
)

// sortedCopy 排序拷贝（并集比对前统一口径）。
func sortedCopy(items []string) []string {
	out := slices.Clone(items)
	slices.Sort(out)
	return out
}

// factoryClientSet 出厂五客户端标识集：完备性并集只统计工厂客户端，
// 排除其他测试手动 Register 的 fake（防污染并集造成假失败）。
var factoryClientSet = map[string]struct{}{
	ClientClaudeCode: {},
	ClientOpenCode:   {},
	ClientWorkBuddy:  {},
	ClientOMO:        {},
	ClientAutomation: {},
}

// unionEffectiveUserPrefixes 组装运行时生效 user 侧前缀并集：
// 通用层前缀 ∪ 各已注册工厂客户端 UserPrefixes（specs §5.1 生效集公式，追加集为空的出厂态）。
func unionEffectiveUserPrefixes(rs []ClientRules) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(items []string) {
		for _, p := range items {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	add(GenericUserPrefixes())
	for _, r := range rs {
		if _, ok := factoryClientSet[r.Client()]; ok {
			add(r.UserPrefixes())
		}
	}
	return out
}

// unionNonUserPrefixes 组装通用层与各已注册工厂客户端 NonUserPrefixes 并集
// （generic 持四条收窄前缀中的三条，须入并集，否则与基线等集关系永不成立）。
func unionNonUserPrefixes(rs []ClientRules) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	add := func(items []string) {
		for _, p := range items {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	add(GenericNonUserPrefixes())
	for _, r := range rs {
		if _, ok := factoryClientSet[r.Client()]; ok {
			add(r.NonUserPrefixes())
		}
	}
	return out
}

// TestRegistryCompleteness 是注册表完备性核心断言（specs §5.1 两视图一致）：
// 通用层前缀 ∪ 各客户端 UserPrefixes 的并集 == 基线出厂 user 侧快照集，
// NonUserPrefixes 并集 == 基线 assistant 收窄快照集，排序后逐元素相等。
// 快照集硬编码全集作最终裁判：并集漂移（误删/改拼写/多出条目）即失败。
func TestRegistryCompleteness(t *testing.T) {
	t.Run("user前缀并集等于基线出厂集", func(t *testing.T) {
		got := sortedCopy(unionEffectiveUserPrefixes(AllClientRules()))
		want := sortedCopy(baselineUserPrefixes)
		if len(got) != len(want) || !slices.Equal(got, want) {
			missing, extra := baseline.Diff(want, got)
			t.Errorf("user 前缀并集与基线快照不等：缺 %d 条 %v；多 %d 条 %v",
				len(missing), missing, len(extra), extra)
		}
	})

	t.Run("NonUser前缀并集等于基线assistant收窄集", func(t *testing.T) {
		got := sortedCopy(unionNonUserPrefixes(AllClientRules()))
		want := sortedCopy(baselineNonUserPrefixes)
		if len(got) != len(want) || !slices.Equal(got, want) {
			missing, extra := baseline.Diff(want, got)
			t.Errorf("NonUser 前缀并集与基线快照不等：缺 %d 条 %v；多 %d 条 %v",
				len(missing), missing, len(extra), extra)
		}
	})

	// 快照按字面硬编码（baseline 包不依赖本包），锚定其 continuation 字面
	// 与 rules 常量相等，防双源漂移。
	if !slices.Contains(baselineUserPrefixes, ContinuationPrefix) {
		t.Errorf("baseline 快照缺少 ContinuationPrefix 字面 %q", ContinuationPrefix)
	}
}

// TestGenericPrefixes 锚定通用层前缀清单（specs §2.2 通用规则层：跨客户端共享形态
// 一处定义；generic 7 条 user 侧 + 3 条 NonUser 侧拆分）。
func TestGenericPrefixes(t *testing.T) {
	wantUser := []string{
		"Note:",
		"Contents of",
		"Called the",
		"User: ",
		"- [",
		"<analysis>",
		"This session is being continued",
	}
	if got := sortedCopy(GenericUserPrefixes()); !slices.Equal(got, sortedCopy(wantUser)) {
		t.Errorf("GenericUserPrefixes() = %v, 期望 %v", got, wantUser)
	}

	wantNonUser := []string{
		"<analysis>",
		"This session is being continued",
		"User: ",
	}
	if got := sortedCopy(GenericNonUserPrefixes()); !slices.Equal(got, sortedCopy(wantNonUser)) {
		t.Errorf("GenericNonUserPrefixes() = %v, 期望 %v", got, wantNonUser)
	}

	if ContinuationPrefix != "This session is being continued" {
		t.Errorf("ContinuationPrefix = %q, 期望 This session is being continued", ContinuationPrefix)
	}
}

// TestClaudeCodePrefixes 锚定 claude_code 前缀归属（基线分组重分：# Environment 归
// opencode、The following skills are available 归 claudecode，03 §3 契约裁定 2）。
func TestClaudeCodePrefixes(t *testing.T) {
	wantUser := []string{
		"<command-name>",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<ide_opened_file>",
		"<system-reminder",
		"Base directory",
		"The following skills are available",
		"CRITICAL: Respond with TEXT ONLY",
		"Describe your most recent action",
		"The user stepped away",
		"The TodoWrite tool",
		"The task tools haven't been used",
		"Generate a title for this conversation",
		"<session>",
		"User request (context):",
		"Analyze *only*",
		"Summarize the tool call input",
		"The following is the user's CLAUDE.md configuration",
		`{"user":`,
		`{"Bash":`,
		"Err on the side of blocking",
		"Review the classification process",
		"<block>",
	}
	for _, r := range AllClientRules() {
		if r.Client() != ClientClaudeCode {
			continue
		}
		if got := sortedCopy(r.UserPrefixes()); !slices.Equal(got, sortedCopy(wantUser)) {
			t.Errorf("claude_code UserPrefixes() = %v, 期望 %v", got, wantUser)
		}
		wantNonUser := []string{"<block>"}
		if got := sortedCopy(r.NonUserPrefixes()); !slices.Equal(got, wantNonUser) {
			t.Errorf("claude_code NonUserPrefixes() = %v, 期望 %v", got, wantNonUser)
		}
		return
	}
	t.Fatalf("claude_code 未注册")
}

// TestOpenCodePrefixes 锚定 opencode 前缀归属（环境头是 OpenCode 专属形态；
// The user has asked you to 刻意不入前缀表，仅作探测特征，BR3）。
func TestOpenCodePrefixes(t *testing.T) {
	for _, r := range AllClientRules() {
		if r.Client() != ClientOpenCode {
			continue
		}
		wantUser := []string{"# Environment"}
		if got := r.UserPrefixes(); !slices.Equal(got, wantUser) {
			t.Errorf("opencode UserPrefixes() = %v, 期望 %v", got, wantUser)
		}
		if got := r.NonUserPrefixes(); len(got) != 0 {
			t.Errorf("opencode NonUserPrefixes() = %v, 期望空", got)
		}
		for _, p := range r.UserPrefixes() {
			if p == "The user has asked you to" {
				t.Errorf("opencode 前缀表收录了黑名单刻意排除的播种前缀（BR3）")
			}
		}
		return
	}
	t.Fatalf("opencode 未注册")
}

// TestWorkBuddyRules 锚定 workbuddy 前缀归属与探测特征：
// UserPrefixes 即基线「空回复通知与 WorkBuddy 续接模板」组三条整组归属，
// NonUserPrefixes 空集；特征为三个专属强特征（specs §2.4 能力7 第 1 条，BR1/BR2）。
func TestWorkBuddyRules(t *testing.T) {
	var target ClientRules
	for _, r := range AllClientRules() {
		if r.Client() == ClientWorkBuddy {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatalf("workbuddy 未注册")
	}

	if got := target.Client(); got != "workbuddy" {
		t.Errorf("workbuddy Client() = %q, 期望 workbuddy", got)
	}

	wantUser := []string{
		"[Your previous response had no visible output",
		"<user_query>",
		"Your task is to create a detailed and highly structured summary",
	}
	if got := sortedCopy(target.UserPrefixes()); !slices.Equal(got, sortedCopy(wantUser)) {
		t.Errorf("workbuddy UserPrefixes() = %v, 期望 %v", got, wantUser)
	}
	if got := target.NonUserPrefixes(); len(got) != 0 {
		t.Errorf("workbuddy NonUserPrefixes() = %v, 期望空", got)
	}

	features := target.DetectFeatures()
	if len(features) < 3 {
		t.Fatalf("workbuddy DetectFeatures() 仅 %d 条, 期望至少 3 条（%+v）", len(features), features)
	}
	for _, f := range features {
		if f.Weight < 1 {
			t.Errorf("特征 %q Weight = %d, 期望 ≥ 1（BR2）", f.Pattern, f.Weight)
		}
	}
}

// TestOMORules 锚定 omo 前缀归属与探测特征：
// UserPrefixes 承接基线「OMO 工具转写家族」「mcp__ 服务器工具转写」两组及
// 「插件续跑、system 侧事件通知」组的 OMO 部分（BR1/BR2），
// NonUserPrefixes 空集；特征为三条 omo 专属特征（specs §2.4 能力7 第 1 条）。
func TestOMORules(t *testing.T) {
	var target ClientRules
	for _, r := range AllClientRules() {
		if r.Client() == ClientOMO {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatalf("omo 未注册")
	}

	if got := target.Client(); got != "omo" {
		t.Errorf("omo Client() = %q, 期望 omo", got)
	}

	wantUser := []string{
		"[SYSTEM DIRECTIVE:",
		"[SYSTEM NOTIFICATION",
		"[agent-auto]",
		"TodoWrite ",
		"Write ",
		"Read ",
		"Edit ",
		"PowerShell ",
		"Grep ",
		"Glob ",
		"mcp__",
	}
	if got := sortedCopy(target.UserPrefixes()); !slices.Equal(got, sortedCopy(wantUser)) {
		t.Errorf("omo UserPrefixes() = %v, 期望 %v", got, wantUser)
	}
	// 核心断言显式复核：尾空格形态与 mcp__ 归属（防集合比对掩盖形态细节）
	for _, must := range []string{"mcp__", "TodoWrite "} {
		if !slices.Contains(target.UserPrefixes(), must) {
			t.Errorf("omo UserPrefixes() 缺 %q", must)
		}
	}
	// BR2：Bash 打头有人类真输入歧义，工具转写家族刻意不收录
	if slices.Contains(target.UserPrefixes(), "Bash ") {
		t.Errorf("omo 前缀表收录了 Bash 打头歧义形态（BR2）")
	}
	if got := target.NonUserPrefixes(); len(got) != 0 {
		t.Errorf("omo NonUserPrefixes() = %v, 期望空", got)
	}

	// BR1：SYSTEM DIRECTIVE、OMO_INTERNAL_INITIATOR 尾标记、工具名加空格打头转写
	// 是 omo 专属特征；mcp__ 转写多客户端共用属共享信号，不入专属特征集
	wantFeatures := []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "[SYSTEM DIRECTIVE:", Weight: 3, Once: true},
		{Kind: FeatureKindMarker, Pattern: "OMO_INTERNAL_INITIATOR", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "TodoWrite ", Weight: 2, Once: true},
	}
	if got := target.DetectFeatures(); !slices.Equal(got, wantFeatures) {
		t.Errorf("omo DetectFeatures() = %+v, 期望 %+v", got, wantFeatures)
	}
	for _, f := range target.DetectFeatures() {
		if f.Weight < 1 {
			t.Errorf("特征 %q Weight = %d, 期望 ≥ 1", f.Pattern, f.Weight)
		}
		if f.Pattern == "mcp__" {
			t.Errorf("mcp__ 共享形态误入 omo 专属特征集（BR1）")
		}
	}
}

// TestAutomationRules 锚定 automation 前缀归属与探测特征：
// UserPrefixes 承接基线「提示词碎片转储家族」组、「系统转储」组的 # System 与
// 中文变体 ## 身份定义（BR1：碎片家族前缀是专属形态特征，节奏不进逐会话探测），
// NonUserPrefixes 空集；特征为碎片家族专属形态 3、共享转储形态 1。
func TestAutomationRules(t *testing.T) {
	var target ClientRules
	for _, r := range AllClientRules() {
		if r.Client() == ClientAutomation {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatalf("automation 未注册")
	}

	if got := target.Client(); got != "automation" {
		t.Errorf("automation Client() = %q, 期望 automation", got)
	}

	wantUser := []string{
		"# System",
		"# Using your tools",
		"This is the git status at the start",
		"# auto memory",
		"# Context management",
		"# Tone and style",
		"# Text output",
		"When you use a pronoun",
		"You are an interactive agent",
		"When you have enough information to act",
		"When referencing files",
		"<total_tokens>",
		"count",
		"## 身份定义",
	}
	if got := sortedCopy(target.UserPrefixes()); !slices.Equal(got, sortedCopy(wantUser)) {
		t.Errorf("automation UserPrefixes() = %v, 期望 %v", got, wantUser)
	}
	// 核心断言显式复核：中文变体与 count 探针归属（防集合比对掩盖形态细节）
	for _, must := range []string{"## 身份定义", "count"} {
		if !slices.Contains(target.UserPrefixes(), must) {
			t.Errorf("automation UserPrefixes() 缺 %q", must)
		}
	}
	if got := target.NonUserPrefixes(); len(got) != 0 {
		t.Errorf("automation NonUserPrefixes() = %v, 期望空", got)
	}

	// BR1：碎片家族专属形态 3、# System 共享转储形态 1；
	// 分钟级节奏是 automation 实测形态但刻意不进逐会话探测，特征集只含前缀形态
	wantFeatures := []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "# Using your tools", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "## 身份定义", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "# System", Weight: 1, Once: true},
	}
	if got := target.DetectFeatures(); !slices.Equal(got, wantFeatures) {
		t.Errorf("automation DetectFeatures() = %+v, 期望 %+v", got, wantFeatures)
	}
	for _, f := range target.DetectFeatures() {
		if f.Weight < 1 {
			t.Errorf("特征 %q Weight = %d, 期望 ≥ 1", f.Pattern, f.Weight)
		}
		if f.Kind != FeatureKindPrefix {
			t.Errorf("特征 %q Kind = %q, 期望 %q（节奏不进逐会话探测，BR1）", f.Pattern, f.Kind, FeatureKindPrefix)
		}
	}
}

// TestDetectFeaturesAnchors 锚定 claude_code/opencode 探测特征初版
// （权重按 specs §2.4 能力7 标定：专属强特征 2-3，Kind 引用 T1 常量）。
func TestDetectFeaturesAnchors(t *testing.T) {
	wantByClient := map[string][]DetectFeature{
		ClientClaudeCode: {
			{Kind: FeatureKindPrefix, Pattern: "<system-reminder", Weight: 3, Once: true},
			{Kind: FeatureKindPrefix, Pattern: "<command-name>", Weight: 2, Once: true},
			{Kind: FeatureKindMarker, Pattern: "The user sent a new message while you were working:", Weight: 2, Once: true},
		},
		ClientOpenCode: {
			{Kind: FeatureKindPrefix, Pattern: "# Environment", Weight: 3, Once: true},
			{Kind: FeatureKindPrefix, Pattern: "The user has asked you to", Weight: 2, Once: true},
		},
	}
	found := map[string]bool{}
	for _, r := range AllClientRules() {
		want, ok := wantByClient[r.Client()]
		if !ok {
			continue
		}
		found[r.Client()] = true
		got := r.DetectFeatures()
		if !slices.Equal(got, want) {
			t.Errorf("%s DetectFeatures() = %+v, 期望 %+v", r.Client(), got, want)
		}
	}
	for c := range wantByClient {
		if !found[c] {
			t.Errorf("%s 未注册，探测特征断言未执行", c)
		}
	}
}
