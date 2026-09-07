package extractor

// DetectClient 计分裁决测试（specs §2.4 能力7、§5.1 探测准确性、03 §4 契约表）。
// 纯客户端样本、mixed/unknown/弱信号裁决、Once 语义、新客户端零侵入与性能预算。

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/engine/extractor/rules"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// detectPureSample 是纯客户端样本（specs §5.1「DetectClient 纯客户端样本」：每客户端
// 典型会话消息与期望标识）。
type detectPureSample struct {
	name string
	msgs []conversationlog.Message
	want string
}

// detectPureSamples 五客户端典型会话构造，消息字面避开其他客户端特征防交叉命中。
func detectPureSamples() []detectPureSample {
	return []detectPureSample{
		{
			name: "workbuddy：user_query 包裹 + team-context marker",
			msgs: []conversationlog.Message{
				mkMsg("<user_query>\n帮我安排本周迭代评审\n</user_query>"),
				mkMsg("团队上下文注入 data-role=\"team-context\" 的载荷"),
			},
			want: rules.ClientWorkBuddy,
		},
		{
			name: "omo：SYSTEM DIRECTIVE + TodoWrite 转写 + 尾标记",
			msgs: []conversationlog.Message{
				mkMsg("[SYSTEM DIRECTIVE: 请立即执行自动化流程"),
				mkMsg("TodoWrite 修复登录页样式问题"),
				mkMsg("会话尾标记 OMO_INTERNAL_INITIATOR"),
			},
			want: rules.ClientOMO,
		},
		{
			name: "automation：中文身份定义 + 工具碎片",
			msgs: []conversationlog.Message{
				mkMsg("## 身份定义\n你是一名自动化流程助手"),
				mkMsg("# Using your tools\n工具使用规范如下"),
			},
			want: rules.ClientAutomation,
		},
		{
			name: "claude_code：SR 开式 + command 壳",
			msgs: []conversationlog.Message{
				mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
				mkMsg("<command-name>/review</command-name>"),
			},
			want: rules.ClientClaudeCode,
		},
		{
			name: "opencode：Environment 头 + 播种形态",
			msgs: []conversationlog.Message{
				mkMsg("# Environment\n项目环境详细信息"),
				mkMsg("The user has asked you to 检查接口超时问题"),
			},
			want: rules.ClientOpenCode,
		},
	}
}

// TestDetectClientPureSamples 核心断言：五客户端典型会话各返回对应标识。
func TestDetectClientPureSamples(t *testing.T) {
	for _, c := range detectPureSamples() {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectClient(mkDetail(c.msgs)); got != c.want {
				t.Errorf("DetectClient() = %q, 期望 %q", got, c.want)
			}
		})
	}
}

// TestDetectClientMixed 核心断言：混合裁决与零特征会话（specs §5.1 混合裁决行）。
func TestDetectClientMixed(t *testing.T) {
	t.Run("WorkBuddy 包 Claude Code：SR 分流 + user_query 并存", func(t *testing.T) {
		// SR 开式给 claude_code 计 3 分，user_query 给 workbuddy 计 3 分，
		// 次高分 > 0 且分差 0 < 2，mixed 是正确输出而非失败（BR1）。
		detail := mkDetail([]conversationlog.Message{
			mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
			mkMsg("<user_query>\n真实用户指令\n</user_query>"),
			mkMsg("普通助手回复，不命中任何特征"),
		})
		if got := DetectClient(detail); got != rules.ClientMixed {
			t.Errorf("DetectClient() = %q, 期望 %q（两客户端均 3 分并列）", got, rules.ClientMixed)
		}
	})

	t.Run("零特征会话返回 unknown 且无 error", func(t *testing.T) {
		detail := mkDetail([]conversationlog.Message{
			mkMsg("今天状态怎么样"),
			mkMsg("帮我把这封邮件改得委婉一点"),
		})
		if got := DetectClient(detail); got != rules.ClientUnknown {
			t.Errorf("DetectClient() = %q, 期望 %q（全零分属正常态，BR3）", got, rules.ClientUnknown)
		}
	})
}

// TestDetectClientWeakSignal 核心断言：单客户端弱信号即定归属（BR2）。
// 仅 # System 共享形态（Weight 1）命中，其余客户端零分：次高点为零分时
// 即便最高分 1、分差 1 < 2 也不判 mixed。
func TestDetectClientWeakSignal(t *testing.T) {
	detail := mkDetail([]conversationlog.Message{
		mkMsg("# System\n系统级提示词转储"),
		mkMsg("普通对话内容"),
	})
	if got := DetectClient(detail); got != rules.ClientAutomation {
		t.Errorf("DetectClient() = %q, 期望 %q（次高点零分不判 mixed）", got, rules.ClientAutomation)
	}
}

// TestDetectClientNilAndEmpty 边界补充：nil detail 防御兜底与空消息会话。
func TestDetectClientNilAndEmpty(t *testing.T) {
	if got := DetectClient(nil); got != rules.ClientUnknown {
		t.Errorf("DetectClient(nil) = %q, 期望 %q（防御性兜底）", got, rules.ClientUnknown)
	}
	if got := DetectClient(&conversationlog.SessionDetail{}); got != rules.ClientUnknown {
		t.Errorf("DetectClient(空会话) = %q, 期望 %q（全部零分）", got, rules.ClientUnknown)
	}
}

// TestDetectClientOnceSemantics 边界补充：Once 特征全会话只计一次。
// 两条 command 壳（Once 计一次得 2 分）对照 # System（automation 1 分）：
// 正确实现分差 1 判 mixed；Once 失效按条累计（4 分）则分差 3 判 claude_code。
func TestDetectClientOnceSemantics(t *testing.T) {
	detail := mkDetail([]conversationlog.Message{
		mkMsg("<command-name>/review</command-name>"),
		mkMsg("<command-name>/clear</command-name>"),
		mkMsg("# System\n系统级提示词转储"),
	})
	if got := DetectClient(detail); got != rules.ClientMixed {
		t.Errorf("DetectClient() = %q, 期望 %q（Once 计一次时 2 分对 1 分分差 1）", got, rules.ClientMixed)
	}
}

// detectFakeClient 新客户端探测 fake（specs §5.1 新客户端接入零侵入、BR7）：
// contributing 指针控制贡献窗口，defer 复原后注册表内该项零贡献
// （注册只增不减，窗口复原模式同 rules_layering_test.go 的 newClientFake）。
type detectFakeClient struct {
	contributing *bool
}

func (f *detectFakeClient) Client() string { return "detect_fake" }

func (f *detectFakeClient) UserPrefixes() []string {
	if f == nil || f.contributing == nil || !*f.contributing {
		return nil
	}
	return []string{"<newclient-frame>"}
}

func (f *detectFakeClient) NonUserPrefixes() []string { return nil }

func (f *detectFakeClient) DetectFeatures() []rules.DetectFeature {
	if f == nil || f.contributing == nil || !*f.contributing {
		return nil
	}
	return []rules.DetectFeature{
		{Kind: rules.FeatureKindPrefix, Pattern: "<newclient-frame>", Weight: 3, Once: true},
		{Kind: rules.FeatureKindMarker, Pattern: "<detect-fake-multi>", Weight: 1, Once: false},
	}
}

// registerDetectFake 注册探测 fake 并返回复原函数（窗口外零贡献）。
func registerDetectFake() func() {
	on := true
	rules.Register(&detectFakeClient{contributing: &on})
	return func() { on = false }
}

// TestDetectClientNewClientZeroIntrusion 核心断言：测试内注册 fake ClientRules
// （专属前缀 "<newclient-frame>" Weight 3 Once），探测识别新客户端，
// 既有五客户端典型样本结果不变（BR7）。
func TestDetectClientNewClientZeroIntrusion(t *testing.T) {
	defer registerDetectFake()()

	t.Run("专属前缀会话识别新客户端", func(t *testing.T) {
		detail := mkDetail([]conversationlog.Message{
			mkMsg("<newclient-frame> 新客户端框架注入"),
			mkMsg("普通用户指令"),
		})
		if got := DetectClient(detail); got != "detect_fake" {
			t.Errorf("DetectClient() = %q, 期望 detect_fake（注册即探测，零侵入）", got)
		}
	})

	t.Run("Once=false 特征逐消息累计", func(t *testing.T) {
		// 3 条 marker 各计 1 分（fake 3 分）对照 automation 1 分：分差 2 不判 mixed。
		// 若误按 Once 只计一次（fake 1 分），与 automation 并列判 mixed，断言即失败。
		detail := mkDetail([]conversationlog.Message{
			mkMsg("信号一 <detect-fake-multi>"),
			mkMsg("信号二 <detect-fake-multi>"),
			mkMsg("信号三 <detect-fake-multi>"),
			mkMsg("# System\n系统级提示词转储"),
		})
		if got := DetectClient(detail); got != "detect_fake" {
			t.Errorf("DetectClient() = %q, 期望 detect_fake（累计 3 分对 1 分分差 2）", got)
		}
	})

	t.Run("既有五客户端典型样本结果不变", func(t *testing.T) {
		for _, c := range detectPureSamples() {
			if got := DetectClient(mkDetail(c.msgs)); got != c.want {
				t.Errorf("%s: DetectClient() = %q, 期望 %q（fake 注册零侵入）", c.name, got, c.want)
			}
		}
	})
}

// TestDetectClientPerformanceBudget 边界补充：500 消息探测预算 ≤ 10ms
// （03 §4 性能预算；实测微秒级，超限走观测口径留记录不判失败，与
// TestDetectSinglePassBudget 同口径，防慢速 CI 容器产生与缺陷无关的 flaky）。
func TestDetectClientPerformanceBudget(t *testing.T) {
	msgs := make([]conversationlog.Message, 0, 500)
	for i := 0; i < 250; i++ {
		msgs = append(msgs,
			mkMsg(fmt.Sprintf("<user_query>\n第 %d 条用户指令\n</user_query>", i)),
			mkMsg(fmt.Sprintf("助手正常回复内容 %d", i)),
		)
	}
	start := time.Now()
	got := DetectClient(mkDetail(msgs))
	elapsed := time.Since(start)
	if got != rules.ClientWorkBuddy {
		t.Fatalf("DetectClient() = %q, 期望 %q", got, rules.ClientWorkBuddy)
	}
	if elapsed > detectSinglePassBudgetLine {
		t.Logf("500 消息探测耗时 %v 超观测线 %v（specs §3.1 预算行，观测口径不判失败）",
			elapsed, detectSinglePassBudgetLine)
	}
}

// mkSinglePassBudgetMsgs 构造 500 消息会话（复用 TestTrimPerformance 的构造手法：
// user 指令 / assistant 叙述 / 工具行 / 噪音 / system 兜底轮转，另混入探测特征
// 保证探测遍历非全零短路路径）。
func mkSinglePassBudgetMsgs() []conversationlog.Message {
	msgs := make([]conversationlog.Message, 0, 500)
	msgs = append(msgs, mkMsg(strings.Repeat("U", 1024)))
	for i := 0; len(msgs) < 499; i++ {
		switch i % 5 {
		case 0:
			msgs = append(msgs, mkRoleMsg("assistant", fmt.Sprintf("第%d轮已推进：%s", i, strings.Repeat("a", 200))))
		case 1:
			msgs = append(msgs, conversationlog.Message{Role: "tool", Kind: "tool_use", Text: fmt.Sprintf("Edit args=%d", i)})
		case 2:
			msgs = append(msgs, mkMsg("The following skills are available: 噪音填充"))
		case 3:
			msgs = append(msgs, mkMsg(fmt.Sprintf("用户指令 %d", i)))
		case 4:
			msgs = append(msgs, mkRoleMsg("system", "You are a title generator"))
		}
	}
	// 探测特征消息：claude_code SR 开式（Once 计一次），探测侧有真实计分路径。
	msgs = append(msgs, mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"))
	return msgs
}

// detectSinglePassBudgetLine 探测增量预算观测线（specs §3.1 探测耗时 ≤ 10ms）。
const detectSinglePassBudgetLine = 10 * time.Millisecond

// TestDetectSinglePassBudget 核心锚点（specs §5.1 探测并入单趟、§3.1 探测预算行，
// BR3）：500 消息会话跑 prepare 全链（裁剪 + 探测单趟），先预热再计时，以
// DetectClient 直调耗时为探测基线，断言 prepare 全链耗时与基线的差值在探测
// 预算量级（≤ 10ms 观测线，非硬失败：超限 t.Log 留观测不 t.Fatal）。
func TestDetectSinglePassBudget(t *testing.T) {
	msgs := mkSinglePassBudgetMsgs()
	if len(msgs) != 500 {
		t.Fatalf("构造规模：msgs=%d, want 500", len(msgs))
	}
	detail := mkDetail(msgs)
	ext := newTrimExtractor(&fakeSysParams{})

	// 探测基线：DetectClient 直调耗时（多轮取最小值压噪声）。
	detectBaseline := time.Duration(1 << 62)
	for i := 0; i < 5; i++ {
		start := time.Now()
		if got := DetectClient(detail); got == "" {
			t.Fatalf("DetectClient 返回空串（防御路径异常）")
		}
		if e := time.Since(start); e < detectBaseline {
			detectBaseline = e
		}
	}

	// prepare 全链（裁剪 + 探测）：预热后计时（正则编译与分配稳定），多轮取最小值。
	prepareCost := time.Duration(1 << 62)
	ext.prepare("", detail)
	for i := 0; i < 5; i++ {
		start := time.Now()
		ext.prepare("", detail)
		if e := time.Since(start); e < prepareCost {
			prepareCost = e
		}
	}

	// 纯裁剪基线：同会话跑裁剪链而探测归零的对照耗时。探测已并入 classifySequence
	// 单趟，用「prepare 全链 - DetectClient 直调」近似探测增量，
	// 与 specs §5.1「裁剪 + 探测合计不显著高于基线裁剪单独耗时」同口径：
	// 差值（探测增量）应在探测预算量级内。
	increment := prepareCost - detectBaseline
	t.Logf("单趟观测：prepare 全链 %v，DetectClient 直调 %v，探测增量近似 %v（预算线 %v）",
		prepareCost, detectBaseline, increment, detectSinglePassBudgetLine)
	if increment > detectSinglePassBudgetLine {
		// 观测口径非硬失败（specs §5.1 预期结果列）：超限留观测记录。
		t.Logf("探测增量 %v 超观测线 %v（specs §3.1 预算行，观测口径不判失败）",
			increment, detectSinglePassBudgetLine)
	}
	if prepareCost >= 50*time.Millisecond {
		t.Errorf("prepare 全链耗时 %v ≥ 50ms 裁剪预算线（specs §3.1）", prepareCost)
	}
}

// TestDetectSkipsToolMetaLines 守护 tool_use/tool_result 行不参与计分：
// 网关元信息 Text 形如 "TodoWrite args=..." 会误命中 omo 前缀特征，
// 仅 SR 特征（3 分）的 claude_code 会话混入工具行时不得误判 mixed。
func TestDetectSkipsToolMetaLines(t *testing.T) {
	detail := mkDetail([]conversationlog.Message{
		mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
		conversationlog.Message{Role: "tool", Kind: "tool_use", Text: "TodoWrite args=[{...}]"},
		conversationlog.Message{Role: "tool", Kind: "tool_result", Text: "OMO_INTERNAL_INITIATOR 转储"},
	})
	if got := DetectClient(detail); got != rules.ClientClaudeCode {
		t.Errorf("DetectClient() = %q, 期望 %q（工具元信息行不得计分）", got, rules.ClientClaudeCode)
	}
}

// TestDetectSkipsReplayExcluded 守护重放区段不计分：claude_code 会话内嵌 opencode
// 历史重放时，区段内外客户端签名不得参与裁决（分类侧整体剥离的内容对探测同样
// 不可信），否则边缘会话被推向 mixed 且 client 列不可逆污染。
func TestDetectSkipsReplayExcluded(t *testing.T) {
	detail := mkDetail([]conversationlog.Message{
		mkMsg("<system-reminder>\n# currentDate\n\n2026-08-28\n</system-reminder>"),
		mkMsg("<transcript>"),
		mkMsg("# Environment\nopencode 历史会话重放载荷"),
		mkMsg("The user has asked you to 历史指令转述"),
		mkMsg("</transcript>"),
		mkMsg("当前会话真实用户指令"),
	})
	if got := DetectClient(detail); got != rules.ClientClaudeCode {
		t.Errorf("DetectClient() = %q, 期望 %q（重放区段内外客户端特征不得计分）", got, rules.ClientClaudeCode)
	}
	// classifySequence 单趟内联路径与独立入口裁决必须同值。
	if got := classifySequence(detail.Messages, nil, nil).client; got != rules.ClientClaudeCode {
		t.Errorf("classifySequence().client = %q, 期望 %q（单趟口径与 DetectClient 一致）", got, rules.ClientClaudeCode)
	}
}

// TestDetectOnceKeyIncludesKind 守护 Once 键含 Kind 维度：同字面注册 prefix 与
// marker 双 Once 特征时互不短路，得分为两特征权重之和（缺 Kind 维度的键会让
// 先命中特征把另一 Kind 永久短路，得分依赖声明顺序）。
func TestDetectOnceKeyIncludesKind(t *testing.T) {
	// 双 Kind 同字面特征集：prefix 与 marker 各 2 分，一条消息同时满足两形态。
	features := []rules.DetectFeature{
		{Kind: rules.FeatureKindPrefix, Pattern: "<dual-frame>", Weight: 2, Once: true},
		{Kind: rules.FeatureKindMarker, Pattern: "<dual-frame>", Weight: 2, Once: true},
	}
	plans := []clientDetectPlan{{client: "dual_fake", features: features}}
	scorer := newDetectScorer()
	scorer.score(plans, mkMsg("<dual-frame> 同时命中前缀与子串"))
	if got := scorer.scores["dual_fake"]; got != 4 {
		t.Errorf("双 Kind 同字面合计得分 = %d, want 4（prefix 2 + marker 2，Once 不得跨 Kind 短路）", got)
	}
}
