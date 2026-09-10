package extractor

// 规则分层判定链测试（specs §2.4 能力1 第 1/2/4 条、§5.1 并集执行等价性、
// 03 §5.1 生效集公式与 §5.3 追加语义）：
//   - classifySequence 会话级生效集 = 出厂并集 ∪ 追加集（去重），nil 与 [] 收敛同值
//   - 非 user 侧收窄 = 出厂收窄并集（追加条目天然只作用 user 侧）
//   - 判定链锚点字面量与判定次序保持原位原值（等价性载体）

import (
	"slices"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/engine/extractor/rules"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// rulesContinuationPrefix 锚定 rules 包单源常量值（防 import 改名后测试失真）。
const rulesContinuationPrefix = rules.ContinuationPrefix

// unionEquivalenceSample 是代表性消息样本（每形态至少一条，specs §5.1 并集执行
// 等价性：基线表驱动用例的消息形态抽样）。want 锚定基线预期判定结果。
type unionEquivalenceSample struct {
	name string
	msg  conversationlog.Message
	// want 是出厂态（追加集为空）下的基线预期判定。
	wantClass   msgClass
	wantPayload string
	// wantInterrupt 是预期打断计数（Class/Payload/InterruptCount 三字段对照口径）。
	wantInterrupt int
}

// unionEquivalenceSamples 覆盖 specs §5.1 等价性验收列出的全部形态：
// SR 分流、command 壳+args、command 纯壳、Base directory、Note: 打断预检、
// 插话、打断标记、@CurrentContext、transcript 开闭区段、内嵌 SR 剥离、
// ide_selection、黑名单 OMO 转写、碎片 count、收窄 assistant 侧 <analysis>、
// 粘贴、口语体。
func unionEquivalenceSamples() []unionEquivalenceSample {
	return []unionEquivalenceSample{
		{
			name:          "SR 分流噪音（currentDate）",
			msg:           mkMsg("<system-reminder>\n# currentDate\n\n2026-08-24\n</system-reminder>"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "SR 分流指纹（claudeMd）",
			msg:           mkMsg("<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n# MUST\n规约正文\n</system-reminder>"),
			wantClass:     classFingerprint,
			wantInterrupt: 0,
		},
		{
			name:          "command 壳+args 提取",
			msg:           mkMsg("<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>检查支付模块的并发安全问题</command-args>"),
			wantClass:     classExtract,
			wantPayload:   "调用技能 review：检查支付模块的并发安全问题", // 含技能名前缀（调用事实进指令摘录）
			wantInterrupt: 0,
		},
		{
			name:          "command 纯壳丢弃",
			msg:           mkMsg("<command-name>/clear</command-name>"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "Base directory 独立命中指纹",
			msg:           mkMsg("Base directory for this skill: /skills/x\n\n# Skill X\n\n技能文档正文"),
			wantClass:     classFingerprint,
			wantInterrupt: 0,
		},
		{
			name:          "Note: 打断预检（计数透出正文丢弃）",
			msg:           mkRoleMsg("system", "Note: e:\\proj\\a.go was modified externally\n[Request interrupted by user]"),
			wantClass:     classDrop,
			wantInterrupt: 1,
		},
		{
			name:          "插话转述提取",
			msg:           mkRoleMsg("system", "The user sent a new message while you were working:\n先停下来，改查另一个问题"),
			wantClass:     classExtract,
			wantPayload:   "先停下来，改查另一个问题",
			wantInterrupt: 0,
		},
		{
			name:          "纯打断标记事件行",
			msg:           mkMsg("[Request interrupted by user]"),
			wantClass:     classEvent,
			wantInterrupt: 1,
		},
		{
			name:          "@CurrentContext 整条保留",
			msg:           mkMsg("@CurrentContext{{{<startContext>\n{\"tree\":...}\n帮我检查这个组件树"),
			wantClass:     classKeep,
			wantPayload:   "@CurrentContext{{{<startContext>\n{\"tree\":...}\n帮我检查这个组件树",
			wantInterrupt: 0,
		},
		{
			name:          "transcript 开标签行",
			msg:           mkMsg("<transcript>\n"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "transcript 闭标签行",
			msg:           mkMsg("</transcript>\n"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "内嵌 SR 剥离（保留类只余正文）",
			msg:           mkMsg("帮我分析这段代码的性能瓶颈\n\n<system-reminder>\nSkills changed. New skills: x\n</system-reminder>"),
			wantClass:     classKeep,
			wantPayload:   "帮我分析这段代码的性能瓶颈",
			wantInterrupt: 0,
		},
		{
			name:          "ide_selection 纯选中（计粘贴不计用户消息）",
			msg:           mkMsg("<ide_selection>\nfunc main() {}\n</ide_selection>"),
			wantClass:     classKeep,
			wantPayload:   "func main() {}",
			wantInterrupt: 0,
		},
		{
			name:          "黑名单 OMO 转写 TodoWrite 带尾空格",
			msg:           mkMsg("TodoWrite 4 items"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "碎片家族 count",
			msg:           mkMsg("count"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "收窄 assistant 侧 <analysis>",
			msg:           mkRoleMsg("assistant", "<analysis>\n回顾全量历史对话"),
			wantClass:     classDrop,
			wantInterrupt: 0,
		},
		{
			name:          "粘贴（多行含围栏保留）",
			msg:           mkMsg("分析这段日志\n```\nerror: boom\n```\n"),
			wantClass:     classKeep,
			wantPayload:   "分析这段日志\n```\nerror: boom\n```",
			wantInterrupt: 0,
		},
		{
			name:          "口语体单行保留",
			msg:           mkMsg("帮我修复登录超时问题"),
			wantClass:     classKeep,
			wantPayload:   "帮我修复登录超时问题",
			wantInterrupt: 0,
		},
	}
}

// TestUnionExecutionEquivalence 核心断言（specs §5.1 并集执行等价性 + 03 §5.3）：
// 全部代表性形态分别以 classifySequence(msgs, nil, nil) 与
// classifySequence(msgs, []string{}, nil) 断言两者逐消息 Class/Payload/
// InterruptCount 一致（追加语义 nil/[] 收敛），且与基线预期值一致。
func TestUnionExecutionEquivalence(t *testing.T) {
	samples := unionEquivalenceSamples()
	msgs := make([]conversationlog.Message, 0, len(samples))
	for _, s := range samples {
		msgs = append(msgs, s.msg)
	}
	nilRun := classifySequenceSeq(msgs, nil, nil)
	emptyRun := classifySequenceSeq(msgs, []string{}, nil)
	if len(nilRun) != len(msgs) || len(emptyRun) != len(msgs) {
		t.Fatalf("序列长度：nil=%d empty=%d want=%d", len(nilRun), len(emptyRun), len(msgs))
	}
	for i, s := range samples {
		a, b := nilRun[i], emptyRun[i]
		// 追加语义收敛：nil 与 [] 同值（03 §5.3：二者统一按空追加集处理）。
		if a.Class != b.Class || a.Payload != b.Payload || a.InterruptCount != b.InterruptCount {
			t.Errorf("[%s] nil 与 [] 判定分叉：class=%d/%d payload=%q/%q interrupt=%d/%d（追加语义须收敛）",
				s.name, a.Class, b.Class, a.Payload, b.Payload, a.InterruptCount, b.InterruptCount)
		}
		// 与基线预期一致。
		if a.Class != s.wantClass {
			t.Errorf("[%s] Class=%d, want %d", s.name, a.Class, s.wantClass)
		}
		if s.wantPayload != "" && a.Payload != s.wantPayload {
			t.Errorf("[%s] Payload=%q, want %q", s.name, a.Payload, s.wantPayload)
		}
		if a.InterruptCount != s.wantInterrupt {
			t.Errorf("[%s] InterruptCount=%d, want %d", s.name, a.InterruptCount, s.wantInterrupt)
		}
	}
}

// TestAppendSemanticsRuntimeEffectiveSet 验证追加语义生效集公式
// （03 §5.1：effective = 出厂并集 ∪ paramAppend，去重）：
// 追加集非空时出厂前缀仍在生效集内（替换语义下传入集会全量顶掉出厂前缀）。
func TestAppendSemanticsRuntimeEffectiveSet(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("TodoWrite 4 items"), // 出厂 OMO 转写前缀
		mkMsg("自定义噪音内容"),           // 追加前缀命中
		mkMsg("正常用户指令"),            // 均不命中，保留
	}
	got := classifySequenceSeq(msgs, []string{"自定义噪音"}, nil)
	if got[0].Class != classDrop {
		t.Errorf("出厂前缀 TodoWrite 在追加语义下仍生效：Class=%d, want classDrop", got[0].Class)
	}
	if got[1].Class != classDrop {
		t.Errorf("追加前缀命中：Class=%d, want classDrop", got[1].Class)
	}
	if got[2].Class != classKeep {
		t.Errorf("未命中消息保留：Class=%d, want classKeep", got[2].Class)
	}
}

// TestAppendSemanticsDedup 验证并集去重（03 §5.1：追加集内重复条目与出厂前缀
// 重复字面均单次匹配，不产生重复元素）。
func TestAppendSemanticsDedup(t *testing.T) {
	msgs := []conversationlog.Message{mkMsg("TodoWrite 4 items")}
	got := classifySequenceSeq(msgs, []string{"TodoWrite ", "TodoWrite ", "自定义噪音", "自定义噪音"}, nil)
	if got[0].Class != classDrop {
		t.Errorf("重复条目并集后仍应命中：Class=%d, want classDrop", got[0].Class)
	}
}

// TestAppendSemanticsNonUserNarrowedWithAppend 验证非 user 侧收窄交集语义
// （03 §5.1：narrowed = 收窄出厂并集 ∩ effective；追加条目通常不在收窄集，
// 天然只作用 user 侧）：追加集命中 assistant 叙述前缀时该叙述保留（追加前缀
// 不进收窄集），收窄子集内条目（如 User:）对 assistant 仍生效。
func TestAppendSemanticsNonUserNarrowedWithAppend(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", "自定义噪音 assistant 叙述撞追加前缀"),
		mkRoleMsg("assistant", "User: 之前说过要改配置"),
		mkMsg("自定义噪音 user 侧转储"),
	}
	got := classifySequenceSeq(msgs, []string{"自定义噪音"}, nil)
	if got[0].Class != classKeep {
		t.Errorf("追加前缀对 assistant 叙述不生效（只作用 user 侧）：Class=%d, want classKeep", got[0].Class)
	}
	if got[1].Class != classDrop {
		t.Errorf("收窄子集 User: 对 assistant 仍生效：Class=%d, want classDrop", got[1].Class)
	}
	if got[2].Class != classDrop {
		t.Errorf("追加前缀对 user 侧生效：Class=%d, want classDrop", got[2].Class)
	}
}

// TestAppendSemanticsBlankEntryPassthrough 空串条目过滤属读参层职责
// （extract.go blankEntriesRemoved），classifySequence 按契约原样并入生效集：
// 此处锁定直传空串的行为面（HasPrefix 恒真全量拦截），防调用方绕过读参层
// 时行为不确定。
func TestAppendSemanticsBlankEntryPassthrough(t *testing.T) {
	msgs := []conversationlog.Message{mkMsg("正常用户指令")}
	got := classifySequenceSeq(msgs, []string{""}, nil)
	if got[0].Class != classDrop {
		t.Errorf("空串直传按公式进生效集应全量拦截：Class=%d, want classDrop（过滤在读参层）", got[0].Class)
	}
}

// TestNarrowedPrefixesSemantics 断言收窄集语义（追加语义下收窄集恒为出厂
// 收窄并集，追加条目天然只作用 user 侧）：无参取值恒返四元素收窄并集；
// assistant 侧拦截只看收窄集，user 侧追加的转储前缀对 assistant 叙述不生效。
func TestNarrowedPrefixesSemantics(t *testing.T) {
	// 收窄并集四元素：基线 assistant 侧收窄形态全量。
	if got := narrowedPrefixes(); len(got) != 4 {
		t.Errorf("收窄集应恒为出厂收窄并集 4 条, got %d %v", len(got), got)
	}
	// 追加的 user 侧转储前缀不在收窄集：assistant 同字面叙述不被误杀。
	narrowed := narrowedPrefixes()
	for _, p := range []string{"自定义噪音", "TodoWrite "} {
		if slices.Contains(narrowed, p) {
			t.Errorf("user 侧追加/转储前缀 %q 不得进入收窄集（防误杀 assistant 叙述）", p)
		}
	}
}

// TestContinuationPrefixReferencesRulesSource 验证 ContinuationPrefix 收敛 rules
// 单源：extractor 包常量与 rules 包常量同值（stats.go ContinuationHit 引用点
// 零改动的前提；观测与拦截同源回归另见 stats_test.go 同名旧用例）。
func TestContinuationPrefixReferencesRulesSource(t *testing.T) {
	if ContinuationPrefix != rulesContinuationPrefix {
		t.Errorf("ContinuationPrefix 双源漂移：extractor=%q rules=%q", ContinuationPrefix, rulesContinuationPrefix)
	}
}

// TestPriorityChainPreserved 构造优先级冲突样本（command 壳 + 内嵌打断 + 黑名单前缀
// 叠加的消息），断言判定次序与基线一致（specs §5.1 判定优先级保持场景）。
func TestPriorityChainPreserved(t *testing.T) {
	// 冲突样本：user 消息 command 壳打头（1a 优先）、args 内文以出厂黑名单前缀
	// TodoWrite 打头、内嵌打断标记。三者叠加下的基线次序：提取通道先于黑名单
	//（黑名单先拦会让 TodoWrite 打头整条 drop）、打断计数随通道透出、
	// Payload 为剥标后 args 内文。
	conflict := mkMsg("<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>TodoWrite 4 items [Request interrupted by user]</command-args>")
	got := classifySequenceSeq([]conversationlog.Message{conflict}, nil, nil)
	if got[0].Class != classExtract {
		t.Fatalf("冲突样本 Class=%d, want classExtract（提取通道优先于黑名单）", got[0].Class)
	}
	if got[0].Payload != "调用技能 review：TodoWrite 4 items" {
		t.Errorf("冲突样本 Payload=%q, want 含技能名前缀的剥标 args %q", got[0].Payload, "调用技能 review：TodoWrite 4 items")
	}
	if got[0].InterruptCount != 1 {
		t.Errorf("冲突样本 InterruptCount=%d, want 1（打断计数透出）", got[0].InterruptCount)
	}
	// 收窄交集样本：assistant 侧按「生效集 ∩ 收窄出厂并集」判定（03 §5.1），
	// <analysis> 打头在收窄集内 drop；TodoWrite 打头仅 user 侧转储形态，不在
	// 收窄集，真实叙述保留进 [AI] 行。
	narrowed := classifySequenceSeq([]conversationlog.Message{
		mkRoleMsg("assistant", "<analysis>\n回顾全量历史对话"),
		mkRoleMsg("assistant", "TodoWrite 4 items 已转写完成"),
	}, nil, nil)
	if narrowed[0].Class != classDrop {
		t.Errorf("assistant <analysis> Class=%d, want classDrop（收窄子集内前缀仍拦截）", narrowed[0].Class)
	}
	if narrowed[1].Class != classKeep || narrowed[1].Payload != "TodoWrite 4 items 已转写完成" {
		t.Errorf("assistant TodoWrite Class=%d Payload=%q, want keep/原文（收窄交集不误杀叙述）",
			narrowed[1].Class, narrowed[1].Payload)
	}
}

// newClientFramePrefix 形态独特前缀字面（specs §2.5 高级用法示例的 <newclient-frame>）：
// 测试内 Register 只增不减（03 §3 契约裁定 1，无反注册），fake 贡献会进同包后续测试
// 的 effective 集。选用与出厂 59 条前缀及全部既有测试消息零碰撞的字面，规避对
// TestInjectPrefixesUnionAssembler（快照等集）与 TestClassifyInjectionBaseline
// （逐条用例数）等注册表敏感用例的干扰；贡献窗口另行在测试内即时复原（见
// newClientFake.contributing），窗口外 fake 只剩零贡献空壳。
const newClientFramePrefix = "<newclient-frame>"

// newClientFake 模拟新客户端接入：局部类型实现 ClientRules 四方法 + 一行 Register，
// 即 specs §2.5「新客户端接入的全部代码增量」。contributing 指针字段控制贡献窗口：
// true 时 UserPrefixes 返回专属前缀（测试断言期），false 时返回 nil（测试收尾复原后，
// 注册表内该项不再向 effective 集贡献任何条目）。指针接收者保证注册表持有的实例
// 与 defer 复原闭包共享同一状态（值副本复原不回写注册表）。
type newClientFake struct {
	contributing *bool
}

func (f *newClientFake) Client() string { return "newclient_fake" }
func (f *newClientFake) UserPrefixes() []string {
	if f == nil || f.contributing == nil || !*f.contributing {
		return nil
	}
	return []string{newClientFramePrefix}
}
func (f *newClientFake) NonUserPrefixes() []string             { return nil }
func (f *newClientFake) DetectFeatures() []rules.DetectFeature { return nil }

// registerNewClientFake 注册 fake 并返回复原函数：defer 调用后把注册表内该实例
// 切回零贡献态（注册表只增不减，无法摘除条目本身，只能收回其前缀贡献）。
func registerNewClientFake() func() {
	on := true
	rules.Register(&newClientFake{contributing: &on})
	return func() { on = false }
}

// TestNewClientZeroIntrusion 测试内注册 fake ClientRules（专属前缀
// "<newclient-frame>"），对含该前缀的会话跑 Trim 断言新前缀按并集生效（drop），
// 既有客户端前缀（Note:）行为不变（specs §5.1 新客户端接入零侵入、§2.4 能力8：
// 新客户端 = 新增规则 + 一行注册，判定链与既有客户端文件零改动）。
func TestNewClientZeroIntrusion(t *testing.T) {
	defer registerNewClientFake()()

	msgs := []conversationlog.Message{
		mkMsg("<newclient-frame> 新客户端框架注入载荷"), // fake 专属前缀
		mkMsg("Note: 既有客户端前缀的文件回显"),           // generic 层 Note:
		mkMsg("正常用户指令"),                       // 对照：不误杀
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))

	// 新前缀按并集生效：该消息被丢弃且计入 InjectedDropped。
	if got := countPrefix(view.Lines, "[USER] "); got != 1 {
		t.Fatalf("[USER] 行数=%d, want 1（新前缀消息与 Note: 噪音均丢弃）\n实得 %v", got, view.Lines)
	}
	if !strings.HasPrefix(view.Lines[0], "[USER] 正常用户指令") {
		t.Errorf("唯一保留行应为正常指令，实得 %q", view.Lines[0])
	}
	if stats.InjectedDropped != 2 {
		t.Errorf("InjectedDropped=%d, want 2（新客户端前缀 1 条 + Note: 1 条）", stats.InjectedDropped)
	}

	// 既有客户端前缀行为不变：Note: 打头消息判定与基线一致（drop，无打断时零透出）。
	got := classifySequenceSeq([]conversationlog.Message{mkMsg("Note: 既有前缀独立复核")}, nil, nil)
	if got[0].Class != classDrop || got[0].InterruptCount != 0 {
		t.Errorf("Note: 消息 Class=%d InterruptCount=%d, want classDrop/0（既有规则行为不变）",
			got[0].Class, got[0].InterruptCount)
	}
}

// dedupFakeClient 构造与 generic 层贡献相同前缀字面（"Note:"）的 fake 客户端：
// 注册后出厂并集的集合内容与长度均不变（并集去重吸收同字面贡献），对同包后续
// 测试零污染，无需贡献窗口复原。
type dedupFakeClient struct{}

func (dedupFakeClient) Client() string                        { return "dedup_fake" }
func (dedupFakeClient) UserPrefixes() []string                { return []string{"Note:"} }
func (dedupFakeClient) NonUserPrefixes() []string             { return nil }
func (dedupFakeClient) DetectFeatures() []rules.DetectFeature { return nil }

// TestPrefixDedupJudgement 构造 generic 与 fake 客户端贡献相同前缀字面（"Note:"），
// 断言并集去重后判定结果与单客户端贡献一致：effective 集中 "Note:" 仅出现一次，
// "Note:" 打头消息一次 HasPrefix 命中即 drop，无重复开销面（specs §5.1 前缀去重）。
func TestPrefixDedupJudgement(t *testing.T) {
	rules.Register(dedupFakeClient{})

	effective := appendEffectivePrefixes(nil)
	if n := strings.Count("\x00"+strings.Join(effective, "\x00")+"\x00", "\x00Note:\x00"); n != 1 {
		t.Fatalf("effective 集中 Note: 出现 %d 次, want 1（双客户端同字面贡献须去重）", n)
	}

	// 判定结果与单客户端贡献一致：drop 且计数口径不变。
	msgs := []conversationlog.Message{mkMsg("Note: 双客户端同字面前缀的文件回显")}
	got := classifySequenceSeq(msgs, nil, nil)
	if got[0].Class != classDrop {
		t.Errorf("Note: 打头消息 Class=%d, want classDrop（一次命中即 drop）", got[0].Class)
	}

	// 基线一致：去重后判定与基线预期逐字段锚定（Class=drop、Payload 空、零透出），
	// 与 generic 层单独贡献 Note: 时的判定结果一致。
	if got[0].Class != classDrop || got[0].Payload != "" || got[0].InterruptCount != 0 {
		t.Errorf("去重后判定与基线分叉：class=%d payload=%q interrupt=%d, want drop/空/0",
			got[0].Class, got[0].Payload, got[0].InterruptCount)
	}

	// Trim 面等价断言：同前缀消息被丢弃，计数与单贡献时一致（1 条）。
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(append(msgs, mkMsg("正常用户指令"))))
	if stats.InjectedDropped != 1 || len(view.Lines) != 1 {
		t.Errorf("Trim 面应只丢 1 条噪音：dropped=%d lines=%v", stats.InjectedDropped, view.Lines)
	}
	// 补充边界：fake 与 generic 各贡献既有字面时并集长度不膨胀（零重复开销面的直接证据）。
	if len(effective) != len(InjectPrefixes()) {
		t.Errorf("并集长度=%d, want %d（同字面贡献不产生重复元素）", len(effective), len(InjectPrefixes()))
	}
}
