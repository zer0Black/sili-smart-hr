package extractor

// 短会话过滤测试（specs §2.4 能力4 判定规则唯一权威、§5.1 短会话过滤用例全集）。
// 内部测试包：BR2 空区间经 ShouldSkip 阈值参数注入覆盖。

import (
	"fmt"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// mkSkipDetail 构造带会话标识与轮次的详情（ShouldSkip 先校验 SessionKey 非空）。
func mkSkipDetail(sessionKey string, turnCount int, msgs []conversationlog.Message) *conversationlog.SessionDetail {
	return &conversationlog.SessionDetail{
		Session:  conversationlog.SessionSummary{SessionKey: sessionKey, TurnCount: turnCount},
		Messages: msgs,
	}
}

// mkTools 构造 n 条工具行（tool_use 元信息）。
func mkTools(n int) []conversationlog.Message {
	msgs := make([]conversationlog.Message, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, conversationlog.Message{Role: "tool", Kind: "tool_use", Text: fmt.Sprintf("Edit args=%d", i)})
	}
	return msgs
}

// mkShellMsgs 构造零输入空壳消息集：n 工具行加 2 条 assistant 叙述（保留类证据，
// 无 user 输入），empty_shell 路径与零叙述锚点共用。
func mkShellMsgs(n int) []conversationlog.Message {
	return append(mkTools(n),
		mkRoleMsg("assistant", "先分析日志。"),
		mkRoleMsg("assistant", "再定位根因。"),
	)
}

// TestShouldSkip 表驱动覆盖 specs §5.1 短会话过滤用例全集（判定序见 §2.4 能力4）。
func TestShouldSkip(t *testing.T) {
	// 阈值终态锚定：边缘组（kept=9）与空区间用例随常量调整同步翻转。
	if MinUserMessages != 1 || MinKeptMessages != 10 {
		t.Fatalf("包内常量已调整为 MinUserMessages=%d MinKeptMessages=%d，本表用例须同步审视", MinUserMessages, MinKeptMessages)
	}
	ext := newTrimExtractor(&fakeSysParams{})

	// 零输入保留类 5 条（3 工具 + 2 叙述）。
	shell5 := mkShellMsgs(3)
	// 单指令长任务：1 条真实输入 + 30 条工具行（specs §2.4 放行示例一）。
	longTask := append([]conversationlog.Message{mkMsg("重构支付模块的错误处理并补齐测试")}, mkTools(30)...)
	// continuation 纯推进会话：续接摘要按注入丢弃后零输入，保留类 15 条（12 工具 + 3 叙述，specs §2.4 放行示例二）。
	continuation := append([]conversationlog.Message{
		mkMsg("This session is being continued from a previous conversation, please proceed."),
	}, append(mkTools(12),
		mkRoleMsg("assistant", "已完成第一步数据迁移。"),
		mkRoleMsg("assistant", "第二步索引重建完成。"),
		mkRoleMsg("assistant", "收尾验证通过。"),
	)...)
	// 仅探针的重度会话：20 条探针（statusline/stepped-away 形态，噪音零计入）+ 保留类 9 条。
	// 若探针被误计入，Kept=29 且有工具行会误放行，用例翻红。
	probeOnly := []conversationlog.Message{}
	probes := []string{
		"CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.",
		"Describe your most recent action",
		"The user stepped away and is coming back",
	}
	for i := 0; i < 20; i++ {
		probeOnly = append(probeOnly, mkMsg(probes[i%len(probes)]))
	}
	probeOnly = append(probeOnly, mkTools(6)...)
	probeOnly = append(probeOnly,
		mkRoleMsg("assistant", "残留叙述一。"),
		mkRoleMsg("assistant", "残留叙述二。"),
		mkRoleMsg("assistant", "残留叙述三。"),
	)
	// 工作型 tc=1：单请求内多轮交互（1 输入 + 8 工具 + 2 叙述）。
	tc1Work := append([]conversationlog.Message{mkMsg("排查接口超时")}, append(mkTools(8),
		mkRoleMsg("assistant", "定位到慢查询。"),
		mkRoleMsg("assistant", "已加索引验证。"),
	)...)
	// 边缘组 kept=MinKeptMessages-1=9 续接收尾：8 工具调用 + 1 条成果叙述，零真实输入（specs §2.4 边缘误杀实测形态）。
	edge9 := append([]conversationlog.Message{
		mkMsg("This session is being continued from a previous conversation, summary follows."),
	}, append(mkTools(8), mkRoleMsg("assistant", "续接收尾：全量回归通过，工单关闭。"))...)
	// 纯打断标记 12 条：事件行凑满阈值，被零响应统一复核拦截。
	interrupts := make([]conversationlog.Message, 12)
	for i := range interrupts {
		interrupts[i] = mkMsg("[Request interrupted by user]")
	}
	// 零响应：1 条真实输入粘贴长文档，无 AI 无工具。
	paste := mkMsg("需求文档全文：\n```\n" + strings.Repeat("功能条目。\n", 40) + "```\n请基于此文档出评估")
	// 会议整理旁路子请求（specs §3.2：system 提示 + user 模板指令，零响应落 empty_shell）。
	meeting := []conversationlog.Message{
		mkRoleMsg("system", "你是一个会议/沟通记录整理助手"),
		mkMsg("请把以下会议记录整理成结构化纪要：与会人、议题、结论、待办"),
	}
	// BR4 合取对照组：无 AI 有工具 / 有 AI 无工具，均放行（零响应判定合取语义）。
	noNarrative := append([]conversationlog.Message{mkMsg("跑一遍全量测试")}, mkTools(12)...)
	noTool := []conversationlog.Message{mkMsg("评审这个设计方案")}
	for i := 0; i < 12; i++ {
		noTool = append(noTool, mkRoleMsg("assistant", fmt.Sprintf("评审意见第 %d 条：建议补充边界用例。", i+1)))
	}

	cases := []struct {
		name       string
		detail     *conversationlog.SessionDetail
		wantSkip   bool
		wantReason string
	}{
		{"空壳会话_零输入保留类5条", mkSkipDetail("s-empty-shell", 3, shell5), true, SkipEmptyShell},
		{"空详情_messages为空", mkSkipDetail("s-no-msgs", 1, nil), true, SkipDetailInvalid},
		{"nil详情", nil, true, SkipDetailInvalid},
		{"缺会话标识_Session零值", &conversationlog.SessionDetail{Messages: []conversationlog.Message{mkMsg("有消息无标识")}}, true, SkipDetailInvalid},
		{"单指令长任务_1输入30工具", mkSkipDetail("s-long-task", 5, longTask), false, ""},
		{"continuation纯推进_零输入保留类15", mkSkipDetail("s-cont", 8, continuation), false, ""},
		{"仅探针_噪音不计入保留类9条不足", mkSkipDetail("s-probe", 20, probeOnly), true, SkipEmptyShell},
		{"turn_count1_单请求多交互放行", mkSkipDetail("s-tc1", 1, tc1Work), false, ""},
		{"turn_count99_空壳照跳_轮次不参与判定", mkSkipDetail("s-tc99", 99, shell5), true, SkipEmptyShell},
		{"边缘组_kept9续接收尾零输入", mkSkipDetail("s-edge9", 1, edge9), true, SkipEmptyShell},
		{"纯打断12条_事件行凑满阈值被零响应拦截", mkSkipDetail("s-int", 6, interrupts), true, SkipEmptyShell},
		{"零响应_1输入粘贴长文档无AI无工具", mkSkipDetail("s-paste", 1, []conversationlog.Message{paste}), true, SkipEmptyShell},
		{"会议整理子请求_system加user模板零响应", mkSkipDetail("s-meeting", 1, meeting), true, SkipEmptyShell},
		{"BR4对照_无AI有工具放行", mkSkipDetail("s-nonarr", 4, noNarrative), false, ""},
		{"BR4对照_无工具有AI放行", mkSkipDetail("s-notool", 4, noTool), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, reason := ext.ShouldSkip(tc.detail, MinUserMessages, MinKeptMessages)
			if skip != tc.wantSkip || reason != tc.wantReason {
				t.Errorf("ShouldSkip = (%v, %q), want (%v, %q)", skip, reason, tc.wantSkip, tc.wantReason)
			}
		})
	}
}

// TestShouldSkipMinUserMessagesBranch 覆盖 BR2：0 < UserMsgCount < minUser 跳过记
// min_user_messages。当前常量 MinUserMessages=1 该区间为空，经 ShouldSkip 参数注入
// minUser=2 驱动分支；主入口断言同一会话按常量放行（阈值调整时此断言同步翻转）。
func TestShouldSkipMinUserMessagesBranch(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{})
	detail := mkSkipDetail("s-br2", 2, append([]conversationlog.Message{mkMsg("唯一一条真实输入")}, mkTools(5)...))
	skip, reason := ext.ShouldSkip(detail, 2, MinKeptMessages)
	if !skip || reason != SkipMinUserMessages {
		t.Errorf("minUser=2 时 1 条输入 = (%v, %q), want (true, %q)", skip, reason, SkipMinUserMessages)
	}
	skip, reason = ext.ShouldSkip(detail, MinUserMessages, MinKeptMessages)
	if skip || reason != "" {
		t.Errorf("当前常量 MinUserMessages=%d 下应放行，得 (%v, %q)", MinUserMessages, skip, reason)
	}
}

// TestShouldSkipRule4MultilineLiteral 翻转面回归：多行用户消息续行恰为 "[AI] "
// 字面量打头时，规则4 不得据渲染前缀误判存在 AI 叙述，空壳照判 empty_shell。
func TestShouldSkipRule4MultilineLiteral(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{})
	// 1 条真实输入（多行，第二行伪装 [AI] 续行）+ 零 AI 零工具。
	msgs := []conversationlog.Message{
		mkMsg("我贴一段历史记录：\n[AI] 上次会话伪造的叙述行"),
	}
	skip, reason := ext.ShouldSkip(mkSkipDetail("s-ai-literal", 1, msgs), MinUserMessages, MinKeptMessages)
	if !skip || reason != SkipEmptyShell {
		t.Errorf("续行 [AI] 字面量不构成 AI 工作证据，得 (%v, %q), want (true, %q)", skip, reason, SkipEmptyShell)
	}
}

// TestShouldSkipRule4TruncationKeepsAI 翻转面回归：超预算会话 AI 行全被截断丢弃、
// 视图仅剩用户行时，结构化信号仍认定存在 AI 叙述，放行进 LLM 而非误判终态空壳。
// empty_shell 是 skipped 终态（复用不重判），误判不可逆；截断只是视图预算控制，
// 证据存在性在分类阶段确立。
func TestShouldSkipRule4TruncationKeepsAI(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{})
	msgs := []conversationlog.Message{
		mkMsg("排查线上性能问题"),
		mkRoleMsg("assistant", strings.Repeat("分析", MaxSessionChars/2+500)),
		mkRoleMsg("assistant", strings.Repeat("结论", MaxSessionChars/2+500)),
	}
	// 先锚定截断确实发生且 AI 行全部被丢，测试方有翻转力。
	view, trimStats := ext.Trim(mkDetail(msgs))
	if !trimStats.Truncated {
		t.Fatal("前置失败：构造的会话未触发预算截断")
	}
	for _, l := range view.Lines {
		if strings.HasPrefix(l, linePrefixAI) {
			t.Fatalf("前置失败：视图仍含 AI 行 %q", l)
		}
	}
	skip, reason := ext.ShouldSkip(mkSkipDetail("s-ai-trunc", 2, msgs), MinUserMessages, MinKeptMessages)
	if skip || reason != "" {
		t.Errorf("截断丢弃 AI 行后应按结构化证据放行，得 (%v, %q)", skip, reason)
	}
}

// TestShouldSkipRule3TruncationKeepsCount 翻转面回归：零输入会话的保留类证据充足
// （≥ MinKeptMessages）时，预算截断丢几条后不得让规则3 计数跌破阈值误落 empty_shell
// 终态（与规则4 截断不翻转存在性同一裁决，empty_shell 终态不可逆）。
func TestShouldSkipRule3TruncationKeepsCount(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{})
	// 零输入：14 条长工具行（每条 8000 字符，行含标签与换行共 8008）。合计远超
	// 预算触发截断，档内同尺寸从后丢，截断后仅保留 floor(72000/8008)=8 条 < 10：
	// 旧口径按截断后计数跌破阈值会误落 empty_shell 终态，新口径按截断前 14 放行。
	tools := make([]conversationlog.Message, 14)
	for i := range tools {
		tools[i] = conversationlog.Message{Role: "tool", Kind: "tool_use", Text: strings.Repeat("T", 8000)}
	}
	// 锚定截断确实发生且截断后计数跌破阈值。
	_, trimStats := ext.Trim(mkDetail(tools))
	if !trimStats.Truncated {
		t.Fatal("前置失败：构造的会话未触发预算截断")
	}
	if trimStats.KeptMessages >= MinKeptMessages {
		t.Fatalf("前置失败：截断后 Kept=%d 未跌破阈值，无翻转力", trimStats.KeptMessages)
	}
	skip, reason := ext.ShouldSkip(mkSkipDetail("s-rule3-trunc", 8, tools), MinUserMessages, MinKeptMessages)
	if skip || reason != "" {
		t.Errorf("截断丢条目后规则3 应按截断前计数放行，得 (%v, %q)", skip, reason)
	}
}

// TestShouldSkipIdeSelectionOnly 回归锚点：IDE 选中文本壳层丢弃只计粘贴，纯选中
// 会话零输入零响应落 empty_shell（specs 第三类第 5 条），不会伪造 [USER] 行
// 触发零输入指示误判 Instruction 编造。
func TestShouldSkipIdeSelectionOnly(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{})
	msgs := append([]conversationlog.Message{
		mkMsg("<ide_selection>\nfunc main() {}\n</ide_selection>"),
	}, mkTools(12)...)
	view, _ := ext.Trim(mkDetail(msgs))
	for _, l := range view.Lines {
		if strings.HasPrefix(l, "[USER] ") {
			t.Fatalf("IDE 选中文本不应进视图伪造 [USER] 行，实得 %q", l)
		}
	}
	// 零输入但工具证据充足（12 ≥ 10）：规则3 放行，规则4 有工具证据同放行。
	if skip, reason := ext.ShouldSkip(mkSkipDetail("s-ide-only", 1, msgs), MinUserMessages, MinKeptMessages); skip {
		t.Errorf("纯选中 + 工具推进会话应放行，得 (%v, %q)", skip, reason)
	}
	// 纯选中零响应（无 AI 无工具）：落 empty_shell 终态。
	pure := []conversationlog.Message{mkMsg("<ide_selection>\nconst a = 1;\n</ide_selection>")}
	if skip, reason := ext.ShouldSkip(mkSkipDetail("s-ide-pure", 1, pure), MinUserMessages, MinKeptMessages); !skip || reason != SkipEmptyShell {
		t.Errorf("纯选中零响应应落 empty_shell，得 (%v, %q)", skip, reason)
	}
}

// 出厂黑名单外的新形态走 role 兜底保留计入 UserMsgCount 放行；参数页增补前缀后该消息
// 变噪音，零输入且保留类不足阈值落 empty_shell。
func TestShouldSkipCustomParams(t *testing.T) {
	msgs := append([]conversationlog.Message{mkMsg("ZZNEWINJECT 新形态噪音载荷")},
		mkRoleMsg("assistant", "叙述一。"),
		mkRoleMsg("assistant", "叙述二。"),
		mkRoleMsg("assistant", "叙述三。"),
		mkRoleMsg("assistant", "叙述四。"),
		mkRoleMsg("assistant", "叙述五。"),
		mkRoleMsg("assistant", "叙述六。"),
		mkRoleMsg("assistant", "叙述七。"),
		mkRoleMsg("assistant", "叙述八。"),
	)
	detail := mkSkipDetail("s-cust", 2, msgs)

	extDefault := newTrimExtractor(&fakeSysParams{})
	if skip, reason := extDefault.ShouldSkip(detail, MinUserMessages, MinKeptMessages); skip || reason != "" {
		t.Errorf("出厂黑名单下新形态计入输入应放行，得 (%v, %q)", skip, reason)
	}
	extCustom := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.inject_prefixes": {"ZZNEWINJECT"},
	}})
	if skip, reason := extCustom.ShouldSkip(detail, MinUserMessages, MinKeptMessages); !skip || reason != SkipEmptyShell {
		t.Errorf("增补前缀后零输入保留类不足应跳过，得 (%v, %q), want (true, %q)", skip, reason, SkipEmptyShell)
	}
}
