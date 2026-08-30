package extractor

// 分类判定链测试（specs §2.4 能力1 判定优先级、§5.1 用例表）。
// 内部测试包：被测 classifyMsg/classifySequence 为包内私有契约签名。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/repository"
)

// mkMsg 构造 user/text 消息（最常见形态）。
func mkMsg(text string) conversationlog.Message {
	return conversationlog.Message{Role: "user", Kind: "text", Text: text}
}

// mkRoleMsg 构造指定角色 text 消息。
func mkRoleMsg(role, text string) conversationlog.Message {
	return conversationlog.Message{Role: role, Kind: "text", Text: text}
}

func TestClassifyInjectionBaseline(t *testing.T) {
	// 出厂前缀全集逐条有用例（以前缀数生成断言，遗漏时测试失败）。
	cases := map[string]string{
		"<command-name>":                         "<command-name>/clear</command-name>",
		"<local-command-caveat>":                 "<local-command-caveat>caveat</local-command-caveat>",
		"<local-command-stdout>":                 "<local-command-stdout>stdout</local-command-stdout>",
		"<ide_opened_file>":                      "<ide_opened_file><path>main.go</path></ide_opened_file>",
		"CRITICAL: Respond with TEXT ONLY":       "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools.",
		"Describe your most recent action":       "Describe your most recent action",
		"The user stepped away":                  "The user stepped away and is coming back",
		"Note:":                                  "Note: some file content here",
		"The following skills are available":     "The following skills are available:",
		"Contents of":                            "Contents of src/main.go",
		"Called the":                             "Called the Read tool",
		"[SYSTEM DIRECTIVE:":                     "[SYSTEM DIRECTIVE: continue]",
		"[SYSTEM NOTIFICATION":                   "[SYSTEM NOTIFICATION] background task",
		"The TodoWrite tool":                     "The TodoWrite tool hasn't been used recently",
		"The task tools haven't been used":       "The task tools haven't been used recently",
		"[agent-auto]":                           "[agent-auto] env guide",
		"Generate a title for this conversation": "Generate a title for this conversation",
		"<session>":                              "<session>info</session>",
		"User request (context):":                "User request (context): the raw input",
		"Analyze *only*":                         "Analyze *only* the tool result",
		"Summarize the tool call input":          "Summarize the tool call input below",
		"The following is the user's CLAUDE.md configuration": "The following is the user's CLAUDE.md configuration",
		`{"user":`:                          `{"user":"old message"}`,
		`{"Bash":`:                          `{"Bash":"ls -la"}`,
		"User: ":                            "User: 帮我查一下日志",
		"Err on the side of blocking":       "Err on the side of blocking",
		"Review the classification process": "Review the classification process",
		"<block>":                           "<block>reason</block>",
		"[Your previous response had no visible output": "[Your previous response had no visible output]",
		"<user_query>": "<user_query>continue</user_query>",
		"Your task is to create a detailed and highly structured summary": "Your task is to create a detailed and highly structured summary",
		"# System":                            "# System\n - All text you output outside of tool use",
		"# Environment":                       "# Environment\nYou have been invoked in the following environment:",
		"TodoWrite ":                          "TodoWrite 4 items",
		"Write ":                              "Write c:\\logs\\app.log",
		"Read ":                               "Read args=111",
		"Edit ":                               "Edit file=main.go",
		"PowerShell ":                         "PowerShell Get-ChildItem -Path .",
		"Grep ":                               "Grep pattern=foo",
		"Glob ":                               "Glob pattern=**/*.go",
		"mcp__":                               "mcp__codegraph__codegraph_search query=x",
		"<system-reminder":                    "<system-reminder>\ncurrentDate\n</system-reminder>", // 开式命中走内容分流，非规约内容归噪音
		"Base directory":                      "Base directory is /app",                             // 非 for this skill 形态走黑名单
		"This is the git status at the start": "This is the git status at the start",
		"# Using your tools":                  "# Using your tools",
		"# auto memory":                       "# auto memory",
		"# Context management":                "# Context management",
		"# Tone and style":                    "# Tone and style",
		"# Text output":                       "# Text output",
		"When you use a pronoun":              "When you use a pronoun",
		"You are an interactive agent":        "You are an interactive agent",
		"When you have enough information to act": "When you have enough information to act",
		"When referencing files":                  "When referencing files",
		"- [":                                     "- [DataX 目标表命名偏好](memory.md)",
		"<total_tokens>":                          "<total_tokens>15000000 tokens left</total_tokens>",
		"count":                                   "count",
		"<analysis>":                              "<analysis>compaction output</analysis>",
		"This session is being continued":         "This session is being continued from a previous conversation",
		"## 身份定义":                                 "## 身份定义\n\n你是一名资深全栈架构师与工程开发助手",
	}
	if len(cases) != len(InjectPrefixes()) {
		t.Fatalf("基线用例数 %d != 出厂前缀数 %d，须逐条补齐", len(cases), len(InjectPrefixes()))
	}
	for _, p := range InjectPrefixes() {
		body, ok := cases[p]
		if !ok {
			t.Errorf("出厂前缀 %q 缺少基线用例", p)
			continue
		}
		// 大小写敏感：原样命中丢弃。
		res := classifyMsg(mkMsg(body), nil, nil, nil)
		if res.Class != classDrop {
			t.Errorf("前缀 %q 原样消息 res.Class=%d, want classDrop", p, res.Class)
		}
		// 前导换行剥离后同样命中（实测前导换行形态）。
		res = classifyMsg(mkMsg("\n  "+body), nil, nil, nil)
		if res.Class != classDrop {
			t.Errorf("前缀 %q 前导换行变体 res.Class=%d, want classDrop", p, res.Class)
		}
	}
	// 大小写敏感反例：变体大小写不命中黑名单，走 role 兜底保留。
	res := classifyMsg(mkMsg("NOTE: 大写变形不命中"), nil, nil, nil)
	if res.Class != classKeep {
		t.Errorf("大小写变体 res.Class=%d, want classKeep（大小写敏感）", res.Class)
	}
}

func TestClassifyCommandArgs(t *testing.T) {
	// <command-name> 打头三标签混排含非空 args（主流形态）。
	main := "<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>检查支付模块的并发安全问题</command-args>"
	res := classifyMsg(mkMsg(main), nil, nil, nil)
	if res.Class != classExtract || res.Payload != "检查支付模块的并发安全问题" {
		t.Errorf("主流形态 res.Class=%d res.Payload=%q, want extract/args内文", res.Class, res.Payload)
	}
	// <command-message> 打头（少数形态）。
	minor := "<command-message>review</command-message>\n<command-name>/review</command-name>\n<command-args>检查并发</command-args>"
	res = classifyMsg(mkMsg(minor), nil, nil, nil)
	if res.Class != classExtract || res.Payload != "检查并发" {
		t.Errorf("少数形态 res.Class=%d res.Payload=%q, want extract/args内文", res.Class, res.Payload)
	}
	// ARGUMENTS 重复段丢弃：res.Payload 只含 args 标签内文。
	mixed := "<command-name>/review</command-name><command-message>review</command-message><command-args>真实指令</command-args>\nARGUMENTS 真实指令"
	res = classifyMsg(mkMsg(mixed), nil, nil, nil)
	if res.Class != classExtract || res.Payload != "真实指令" {
		t.Errorf("ARGUMENTS 混排 res.Payload=%q, want 只含 args 内文", res.Payload)
	}
	// 无 args 纯壳 /clear → drop。
	res = classifyMsg(mkMsg("<command-name>/clear</command-name>"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("纯壳 /clear res.Class=%d, want classDrop", res.Class)
	}
	// command-message 打头的无参纯壳 → drop（黑名单只收 <command-name> 前缀，
	// 此形态靠提取通道的前置丢弃兜住，走 role 兜底会虚增 UserMsgCount 把
	// empty_shell 会话误放行，specs §2.4 能力1 形态2）。
	res = classifyMsg(mkMsg("<command-message>clear</command-message>"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("command-message 纯壳 res.Class=%d, want classDrop", res.Class)
	}
	// command-message 打头带 name 标签无 args 的混排纯壳 → 同样 drop。
	res = classifyMsg(mkMsg("<command-message>clear</command-message>\n<command-name>/clear</command-name>"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("command-message 混排纯壳 res.Class=%d, want classDrop", res.Class)
	}
	// args 为空同样无指令可提。
	res = classifyMsg(mkMsg("<command-name>/model</command-name><command-args></command-args>"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("空 args res.Class=%d, want classDrop", res.Class)
	}
}

func TestClassifySRVariants(t *testing.T) {
	// 无属性开式 + claudeMd 规约特征 → 产指纹（真实网关形态：标题行独立、引导语前置）。
	claude := "<system-reminder>\nAs you answer the user's questions, you can use the following context:\n# claudeMd\nCodebase and user instructions are shown below.\n\n# MUST\n内容规约正文\n</system-reminder>"
	res := classifyMsg(mkMsg(claude), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPClaudeMD {
		t.Errorf("claudeMd 开式 res.Class=%d res.Fp=%v, want fingerprint/%s", res.Class, res.Fp, FPClaudeMD)
	}
	// 无属性开式 + currentDate → 噪音。
	cur := "<system-reminder>\n# currentDate\n\n2026-08-24\n</system-reminder>"
	res = classifyMsg(mkMsg(cur), nil, nil, nil)
	if res.Class != classDrop || res.Fp != nil {
		t.Errorf("currentDate res.Class=%d, want classDrop 无指纹", res.Class)
	}
	// data-role 属性开式 → 噪音。
	dr := "<system-reminder data-role=\"user-context\">\nmemory_and_skills_reminder 载荷\n</system-reminder>"
	res = classifyMsg(mkMsg(dr), nil, nil, nil)
	if res.Class != classDrop || res.Fp != nil {
		t.Errorf("data-role 变体 res.Class=%d, want classDrop 无指纹", res.Class)
	}
	// claudeMd 复合正文行中提及 AGENTS.md（实测形态：以 AGENTS.md 为准），kind 取判定序
	// 先命中的 claude_md；旧全文 Contains 会让其错标 agents_md。
	mixedKind := "<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n# MUST\n以 AGENTS.md 为准\n</system-reminder>"
	res = classifyMsg(mkMsg(mixedKind), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPClaudeMD {
		t.Errorf("复合提及 AGENTS.md res.Class=%d res.Fp=%v, want fingerprint/%s", res.Class, res.Fp, FPClaudeMD)
	}
	// 其余噪音语义变体。
	for _, body := range []string{
		"<system-reminder>\nNote: file content\n</system-reminder>",
		"<system-reminder>\nCalled the Read tool\n</system-reminder>",
		"<system-reminder>\nResult of calling the Read tool\n</system-reminder>",
		"<system-reminder>\nMCP Server Instructions\n</system-reminder>",
		"<system-reminder>\nThe following skills are available:\n</system-reminder>",
		"<system-reminder>\n<total_tokens>100</total_tokens>\n</system-reminder>",
	} {
		res = classifyMsg(mkMsg(body), nil, nil, nil)
		if res.Class != classDrop || res.Fp != nil {
			t.Errorf("SR 噪音语义 res.Class=%d res.Fp=%v, want classDrop", res.Class, res.Fp)
		}
	}
	// SR 包裹的 AGENTS.md 特征 → agents_md 指纹（独立标识行行首）。
	agents := "<system-reminder>\nAGENTS.md\n\n# 项目规约\n</system-reminder>"
	res = classifyMsg(mkMsg(agents), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPAgentsMD {
		t.Errorf("AGENTS.md res.Class=%d res.Fp=%v, want fingerprint/%s", res.Class, res.Fp, FPAgentsMD)
	}
	// SR 噪音正文行中提及 AGENTS.md（实测形态：SYSTEM NOTIFICATION 文件清单 - `AGENTS.md`
	// 与 CLAUDE.md 回显链接行）不产指纹。
	for _, noisy := range []string{
		"<system-reminder>\n[SYSTEM NOTIFICATION - NOT USER INPUT]\n\n## 3. 变更文件\n- `AGENTS.md`\n\n## 4. 正文\n</system-reminder>",
		"<system-reminder>\nContents of D:\\proj\\lafs\\CLAUDE.md:\n\n# CLAUDE.md\n\n详细规范参见同目录下的 [`AGENTS.md`](AGENTS.md)。\n</system-reminder>",
	} {
		res = classifyMsg(mkMsg(noisy), nil, nil, nil)
		if res.Class != classDrop || res.Fp != nil {
			t.Errorf("噪音提及 AGENTS.md res.Class=%d res.Fp=%v, want classDrop/nil", res.Class, res.Fp)
		}
	}
	// SR 包裹的 Base directory 技能文档 → skill_doc 指纹。
	skill := "<system-reminder>\nBase directory for this skill: /skills/x\n\n# Skill X\n</system-reminder>"
	res = classifyMsg(mkMsg(skill), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPSkillDoc {
		t.Errorf("SR 包裹技能文档 res.Class=%d res.Fp=%v, want fingerprint/%s", res.Class, res.Fp, FPSkillDoc)
	}
}

func TestClassifyBaseDirectoryStandalone(t *testing.T) {
	body := "Base directory for this skill: /skills/x\n\n# Skill X\n\n技能文档正文"
	// 无 SR 包裹、user 角色裸前缀（specs §6.4 问题1：漏判会让 9k-13k 规约全文进视图）。
	res := classifyMsg(mkMsg(body), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPSkillDoc {
		t.Fatalf("裸 Base directory res.Class=%d res.Fp=%v, want fingerprint/%s", res.Class, res.Fp, FPSkillDoc)
	}
	if res.Payload != "" {
		t.Errorf("全文零进入视图，res.Payload 应为空，实得 %q", res.Payload)
	}
	// 前导换行变体同样命中（独立命中通道，剥离前导空白后判定）。
	res = classifyMsg(mkMsg("\n"+body), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPSkillDoc {
		t.Errorf("前导换行变体 res.Class=%d, want fingerprint/skill_doc", res.Class)
	}
}

func TestClassifyRoleFallback(t *testing.T) {
	// user/text 未命中 → 保留。
	res := classifyMsg(mkRoleMsg("user", "帮我修复登录超时问题"), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "帮我修复登录超时问题" {
		t.Errorf("user/text res.Class=%d res.Payload=%q, want keep/原文", res.Class, res.Payload)
	}
	// assistant/text 未命中 → 保留。
	res = classifyMsg(mkRoleMsg("assistant", "已完成修复并跑过测试"), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "已完成修复并跑过测试" {
		t.Errorf("assistant/text res.Class=%d, want keep", res.Class)
	}
	// system/text 未命中 → 丢弃。
	res = classifyMsg(mkRoleMsg("system", "You are a title generator"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("system/text res.Class=%d, want classDrop", res.Class)
	}
	// 黑名单外形态点名：The task tools haven't been used 的 system 消息 → drop。
	res = classifyMsg(mkRoleMsg("system", "The task tools haven't been used recently"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("task tools system 消息 res.Class=%d, want classDrop", res.Class)
	}
	// 黑名单外 agent 类型清单经 system 兜底丢弃（specs §2.2）。
	res = classifyMsg(mkRoleMsg("system", "Available agent types:"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("agent 类型清单 system 消息 res.Class=%d, want classDrop", res.Class)
	}
}

func TestClassifySRMixedCurrentDate(t *testing.T) {
	// claudeMd 复合消息实测内嵌 currentDate 日期段（specs §2.2 Hash 注释）：
	// 指纹特征先判，防噪音先判把规约指纹整类误杀。
	mixed := "<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n# 规约\n\n# currentDate\n\n2026-08-24\n\n</system-reminder>"
	res := classifyMsg(mkMsg(mixed), nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPClaudeMD {
		t.Errorf("claudeMd+currentDate 混排 res.Class=%d res.Fp=%v, want fingerprint/claude_md", res.Class, res.Fp)
	}
}

func TestClassifyIdeSelection(t *testing.T) {
	// 壳层丢弃但选中文本计粘贴：置布尔标记、res.Payload 只含选中文本，由 stats 消费。
	msg := "<ide_selection>\nfunc main() {}\n</ide_selection>"
	res := classifyMsg(mkMsg(msg), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "func main() {}" || !res.IsIDESelection {
		t.Errorf("ide_selection res.Class=%d res.Payload=%q res.IsIDESelection=%v, want keep/选中文本/true", res.Class, res.Payload, res.IsIDESelection)
	}
	// 壳内为空 → 丢弃。
	res = classifyMsg(mkMsg("<ide_selection></ide_selection>"), nil, nil, nil)
	if res.Class != classDrop || res.IsIDESelection {
		t.Errorf("空 ide_selection res.Class=%d res.IsIDESelection=%v, want classDrop/false", res.Class, res.IsIDESelection)
	}
	// 构造反例：用户正文以历史标记字面量打头，应按真实用户消息保留且不置布尔。
	lit := "[IDE_SELECTION] foo"
	res = classifyMsg(mkMsg(lit), nil, nil, nil)
	if res.Class != classKeep || res.Payload != lit || res.IsIDESelection {
		t.Errorf("标记字面量开头正文 res.Class=%d res.Payload=%q res.IsIDESelection=%v, want keep/原文/false", res.Class, res.Payload, res.IsIDESelection)
	}
	// 裸开标签打头的提问正文（无闭标签）：按正文保留不劫持，防单输入会话误落 empty_shell。
	question := "<ide_selection>是什么意思，帮我解释这个标签"
	res = classifyMsg(mkMsg(question), nil, nil, nil)
	if res.Class != classKeep || res.Payload != question || res.IsIDESelection {
		t.Errorf("裸开标签提问 res.Class=%d res.Payload=%q res.IsIDESelection=%v, want keep/原文/false", res.Class, res.Payload, res.IsIDESelection)
	}
}

func TestClassifyCurrentContext(t *testing.T) {
	// @CurrentContext 转储整条保留（末尾常内嵌真实指令，specs §2.4 能力4）。
	msg := "@CurrentContext{{{<startContext>\n{\"tree\":...}\n帮我检查这个组件树"
	res := classifyMsg(mkMsg(msg), nil, nil, nil)
	if res.Class != classKeep || res.Payload != msg {
		t.Errorf("@CurrentContext res.Class=%d, want keep 整条保留", res.Class)
	}
	// system 侧同前缀不保留：误计会经 [AI] 行伪造叙述绕过零响应防线；
	// assistant 落优先级7 兜底保留为叙述属 role 兜底认可行为，不在此列。
	res = classifyMsg(mkRoleMsg("system", msg), nil, nil, nil)
	if res.Class == classKeep {
		t.Errorf("system 角色 @CurrentContext res.Class=keep, want 非 keep")
	}
}

func TestClassifyCustomPrefixes(t *testing.T) {
	// 追加语义（03 §5.3）：配置集只追加不替换，删除配置条目只收回运维自己追加的
	// 条目，代码前缀并集不受配置影响。传 ["Note:"]（运维配置仅剩 Note: 条目）时
	// User: 仍被出厂并集拦截，基线替换语义的 keep 预期在此翻转（03 §5.4）。
	scattered := []conversationlog.Message{mkMsg("User: 删除前缀后的回退形态")}
	got := classifySequenceSeq(scattered, []string{"Note:"}, nil)
	if got[0].Class != classDrop || got[0].ReplayExcluded {
		t.Errorf("出厂前缀 User: 追加语义下仍拦截 res.Class=%d, want classDrop", got[0].Class)
	}
	// 自定义前缀命中新增形态。
	got = classifySequenceSeq([]conversationlog.Message{mkMsg("Note: 自定义命中")}, []string{"Note:"}, nil)
	if got[0].Class != classDrop {
		t.Errorf("自定义前缀命中 res.Class=%d, want classDrop", got[0].Class)
	}
}

func TestClassifyNonTextKind(t *testing.T) {
	// tool_use/tool_result 在优先级0 直接归 classTool，不进判定链（防黑名单误杀工具证据）。
	for _, kind := range []string{"tool_use", "tool_result"} {
		m := conversationlog.Message{Role: "tool", Kind: kind, Text: "Read args=111"}
		got := classifySequenceSeq([]conversationlog.Message{m}, nil, nil)
		if got[0].Class != classTool || got[0].ReplayExcluded {
			t.Errorf("%s 行 res.Class=%d, want classTool 无条件保留", kind, got[0].Class)
		}
	}
	// 空 text 消息无内容可保留 → 丢弃。
	res := classifyMsg(mkMsg(""), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("空 text res.Class=%d, want classDrop", res.Class)
	}
}

// TestTrimBlankPrefixEntry 回归：参数页误配空串条目（HasPrefix(text,"") 恒真）不得
// 让全部 text 消息误杀落 empty_shell 终态；空串在读出侧过滤，其余条目仍生效。
func TestTrimBlankPrefixEntry(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.inject_prefixes": {"", "Note:"},
	}})
	view, stats := ext.Trim(mkDetail([]conversationlog.Message{
		mkMsg("Note: 噪音"),
		mkMsg("正常用户指令"),
	}))
	if len(view.Lines) != 1 || !strings.HasPrefix(view.Lines[0], "[USER] 正常用户指令") {
		t.Errorf("含空串条目的黑名单不得误杀正常消息，Lines=%v", view.Lines)
	}
	if stats.InjectedDropped != 1 {
		t.Errorf("InjectedDropped=%d, want 1（仅噪音命中）", stats.InjectedDropped)
	}
	// 过滤空串后只剩空集：追加语义下空集即无追加，生效集回落出厂并集
	//（基线替换语义的「空集放开全部过滤」在此翻转，03 §5.3 显式清空行）。
	ext = newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.inject_prefixes": {""},
	}})
	view, _ = ext.Trim(mkDetail([]conversationlog.Message{mkMsg("The following skills are available: x")}))
	if len(view.Lines) != 0 {
		t.Errorf("空集无追加：出厂前缀仍拦截，Lines=%v", view.Lines)
	}
}

func TestClassifyInlineSR(t *testing.T) {
	// 指令正文尾部 appended SR 块（14k 级以长文模拟）。
	dump := strings.Repeat("Skills changed. New skills: ", 500)
	msg := "帮我分析这段代码的性能瓶颈\n\n<system-reminder>\n" + dump + "\n</system-reminder>"
	res := classifyMsg(mkMsg(msg), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "帮我分析这段代码的性能瓶颈" {
		t.Errorf("内嵌 SR res.Class=%d res.Payload=%q, want keep/仅正文", res.Class, res.Payload)
	}
	// 未闭合块剥到消息末尾。
	msg2 := "指令正文\n\n<system-reminder>\n" + dump
	res = classifyMsg(mkMsg(msg2), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "指令正文" {
		t.Errorf("未闭合 SR res.Class=%d res.Payload=%q, want keep/剥到末尾", res.Class, res.Payload)
	}
	// 整条剥空 → 降级 drop。
	msg3 := "<system-reminder>\n" + dump + "\n</system-reminder>"
	res = classifyMsg(mkMsg(msg3), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("剥空消息 res.Class=%d, want classDrop", res.Class)
	}
}

func TestClassifyInterject(t *testing.T) {
	mkInterject := func(body string) conversationlog.Message {
		return mkRoleMsg("system", "The user sent a new message while you were working:\n"+body)
	}
	// 纯转述 → 提取。
	res := classifyMsg(mkInterject("先停下来，改查另一个问题"), nil, nil, nil)
	if res.Class != classExtract || res.Payload != "先停下来，改查另一个问题" {
		t.Errorf("插话 res.Class=%d res.Payload=%q, want extract/指令", res.Class, res.Payload)
	}
	// 混排 <ide_opened_file> 通知壳被剥离、指令保留。
	mixed := mkInterject("<ide_opened_file><path>main.go</path></ide_opened_file>\n继续用中文写注释")
	res = classifyMsg(mixed, nil, nil, nil)
	if res.Class != classExtract || res.Payload != "继续用中文写注释" {
		t.Errorf("混排插话 res.Payload=%q, want 只含指令", res.Payload)
	}
}

func TestClassifyInterrupt(t *testing.T) {
	// 嵌入正文的消息：剥标记后余文按原角色继续判定（user/assistant 归保留类，
	// system 走 role 兜底丢弃），指令与叙述证据不随打断丢失。
	for _, role := range []string{"user", "assistant"} {
		text := "正文叙述 [Request interrupted by user] 尾部"
		res := classifyMsg(mkRoleMsg(role, text), nil, nil, nil)
		if res.Class != classKeep || res.Payload != "正文叙述  尾部" {
			t.Errorf("%s 角色打断 res.Class=%d res.Payload=%q, want keep/剥后正文", role, res.Class, res.Payload)
		}
	}
	res := classifyMsg(mkRoleMsg("system", "通知 [Request interrupted by user] 标记"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("system 打断剥标记后走兜底 res.Class=%d, want classDrop", res.Class)
	}
	// 纯标记消息（含 for tool use 变体）剥空 → 事件行。
	res = classifyMsg(mkMsg("[Request interrupted by user]"), nil, nil, nil)
	if res.Class != classEvent {
		t.Errorf("纯标记 res.Class=%d, want classEvent", res.Class)
	}
	res = classifyMsg(mkMsg("[Request interrupted by user for tool use]"), nil, nil, nil)
	if res.Class != classEvent {
		t.Errorf("for tool use 变体 res.Class=%d, want classEvent", res.Class)
	}
	// 标记 + 粘贴正文：粘贴证据保全（多行含围栏仍计粘贴）。
	paste := "分析这段日志\n```\nerror: boom\n```\n[Request interrupted by user]"
	res = classifyMsg(mkRoleMsg("user", paste), nil, nil, nil)
	if res.Class != classKeep || !strings.Contains(res.Payload, "error: boom") {
		t.Errorf("粘贴打断 res.Class=%d res.Payload=%q, want keep/正文保全", res.Class, res.Payload)
	}
}

func TestClassifyTranscriptSection(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("The following is the user's CLAUDE.md configuration"), // 头部说明
		mkMsg("<transcript>\n"),            // 开标签（带尾换行变体）
		mkMsg(`{"user":"历史指令"}`),           // 区段内 JSON 载荷
		mkMsg(`{"Bash":"cd /app && ls"}`),  // 区段内 Bash 载荷
		mkMsg("变异载荷不命中任何前缀"),               // 区段内漏出形态
		mkMsg("User: 人类消息对照组"),             // 区段内 User: 形态
		mkMsg("</transcript>\n"),           // 闭标签（带尾换行变体）
		mkRoleMsg("assistant", "区段外的正常叙述"), // 区段外对照组
		mkMsg("真实用户指令"),                    // 区段外正常消息
	}
	got := classifySequenceSeq(msgs, nil, nil)
	wantExcluded := []bool{false, false, true, true, true, true, false, false, false}
	for i, w := range wantExcluded {
		if got[i].ReplayExcluded != w {
			t.Errorf("第 %d 条 ReplayExcluded=%v, want %v（%q）", i, got[i].ReplayExcluded, w, msgs[i].Text)
		}
	}
	// 区段外对照：黑名单无误杀，assistant 叙述与真实指令保留。
	if got[7].Class != classKeep || got[8].Class != classKeep {
		t.Errorf("区段外对照组 res.Class=%d/%d, want keep/keep", got[7].Class, got[8].Class)
	}
	// 散装 User: 消息（无区段包裹）按前缀独立命中丢弃。
	scattered := classifySequenceSeq([]conversationlog.Message{mkMsg("User: 散装形态")}, nil, nil)
	if scattered[0].Class != classDrop || scattered[0].ReplayExcluded {
		t.Errorf("散装 User: res.Class=%d excluded=%v, want classDrop/false", scattered[0].Class, scattered[0].ReplayExcluded)
	}
	// 区段未闭合：剥离至会话末尾。
	unclosed := classifySequenceSeq([]conversationlog.Message{
		mkMsg("<transcript>\n"),
		mkMsg(`{"user":"x"}`),
		mkMsg("未闭合尾部载荷"),
	}, nil, nil)
	for i := 1; i <= 2; i++ {
		if !unclosed[i].ReplayExcluded {
			t.Errorf("未闭合区段第 %d 条应剥离至末尾", i)
		}
	}
	// 标签行判定不限角色（specs 形态9 口径）：assistant 侧字面同形标签行
	// 同样驱动区段状态机，其后真实输入按区段内载荷剥离。
	across := classifySequenceSeq([]conversationlog.Message{
		mkRoleMsg("assistant", "<transcript>"),
		mkMsg("真实用户指令"),
		mkRoleMsg("assistant", "</transcript>"),
	}, nil, nil)
	if across[0].Class != classDrop || across[1].Class != classDrop || !across[1].ReplayExcluded {
		t.Errorf("任意角色标签行应开层剥离区段内载荷：class=%d/%d excluded=%v",
			across[0].Class, across[1].Class, across[1].ReplayExcluded)
	}
}

// fpHashHex 复算期望指纹哈希：SHA-256 前 16 位（specs §2.2 Hash 口径）。
// 哈希单点已收敛至包内 hash16（fingerprint.go），测试直接引用同源实现，防口径漂移。
func fpHashHex(body string) string {
	return hash16(body)
}

// fakeSysParams 是 SystemParamReader 的内存 fake：按 key 返回注入集，未命中回退空集
// （ReadStringArray 错误语义走 errFakeParam 分支单独覆盖）。
type fakeSysParams struct {
	values map[string][]string
	err    error
}

func (f *fakeSysParams) ReadStringArray(key string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.values[key]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeSysParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string][]string, len(keys))
	for _, key := range keys {
		if v, ok := f.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

// trimSysParams 把 fake 适配为 Extractor 构造入参形态。
func trimSysParams(f *fakeSysParams) repository.SystemParamReader { return f }

var errFakeParam = errors.New("fake param db failure")

// newTrimExtractor 构造只用于 Trim 的 Extractor：llm/cl/repo/secrets 均 nil（Trim 纯函数不触碰）。
func newTrimExtractor(p repository.SystemParamReader) *Extractor {
	return New(nil, nil, nil, p, nil)
}

// mkDetail 构造会话详情（Trim 只消费 Messages）。
func mkDetail(msgs []conversationlog.Message) *conversationlog.SessionDetail {
	return &conversationlog.SessionDetail{Messages: msgs}
}

// countPrefix 统计以指定标签打头的行数。
func countPrefix(lines []string, tag string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, tag) {
			n++
		}
	}
	return n
}

// findLine 返回首条以 tag 打头且含 sub 的行，无则空串。
func findLine(lines []string, tag, sub string) string {
	for _, l := range lines {
		if strings.HasPrefix(l, tag) && strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// TestTrimViewAssembly 覆盖锚点一：混合会话的行组装（BR1）与噪音零进入（BR2 用户行、BR5 脱敏时机）。
func TestTrimViewAssembly(t *testing.T) {
	paste := "报错堆栈：\nat main.go:32 panic\n路径 D:\\proj\\src\\main.go"
	msgs := []conversationlog.Message{
		mkRoleMsg("system", "<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n# 规约\n</system-reminder>"), // 指纹，不进视图
		mkMsg("The following skills are available: - skill-a"),                                                                            // 噪音
		mkRoleMsg("system", "Available agent types: - claude"),                                                                            // 噪音（system 兜底）
		mkRoleMsg("assistant", "已定位到问题在连接池耗尽，我先加监控。"),                                                                                     // AI 叙述
		mkMsg(paste), // 用户粘贴（含路径，脱敏后进视图）
		conversationlog.Message{Role: "tool", Kind: "tool_use", Text: "Read args=111"},      // 工具摘要
		mkMsg("[Request interrupted by user]"),                                              // 打断标记 → 事件行
		mkRoleMsg("system", "The user sent a new message while you were working:\n先改成只读模式"), // 插话提取 → USER 行
		mkMsg("<command-name>/review</command-name><command-args>重点看并发安全</command-args>"),   // command-args 提取 → USER 行
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))

	// 行字符合计（多行粘贴展开后逐行计数）：粘贴脱敏后原文 3 行合计 = 6+19+14 = 39。
	wantLines := []string{
		"[AI] 已定位到问题在连接池耗尽，我先加监控。",
		"[USER] 报错堆栈：",
		"at main.go:32 panic",
		"路径 [PATH]",
		"[TOOL] Read args=111",
		"[EVENT] [Request interrupted by user]",
		"[USER] 先改成只读模式",
		"[USER] 重点看并发安全",
	}
	if len(view.Lines) != len(wantLines) {
		t.Fatalf("总行数=%d, want %d（指纹与噪音零进入）\n实得 %q", len(view.Lines), len(wantLines), view.Lines)
	}
	for i, w := range wantLines {
		if view.Lines[i] != w {
			t.Errorf("Lines[%d]=%q, want %q", i, view.Lines[i], w)
		}
	}
	// 统计块：指纹 1 条、噪音 3 条（技能清单/agent 清单/打断消息本身非噪音但转事件不计 dropped）。
	if stats.TotalMessages != len(msgs) || stats.KeptMessages != 6 {
		t.Errorf("统计 Total=%d Kept=%d, want %d/6", stats.TotalMessages, stats.KeptMessages, len(msgs))
	}
	// InjectedDropped 只计 classDrop（2 条噪音：技能清单/agent 清单）；指纹是提取
	// 非丢弃，指纹数由 len(SpecFingerprints) 推导。
	if stats.InjectedDropped != 2 {
		t.Errorf("InjectedDropped=%d, want 2（噪音2）", stats.InjectedDropped)
	}
	if stats.Truncated {
		t.Error("未超预算 Truncated 应为 false")
	}
	// CharCount 含换行（每行一个，与 buildPrompt 逐行补 \n 同口径）。
	sum := 0
	for _, l := range view.Lines {
		sum += utf8.RuneCountInString(l)
	}
	sum += len(view.Lines)
	if view.CharCount != sum {
		t.Errorf("CharCount=%d, want 行字符数（rune）+行数（换行）合计 %d", view.CharCount, sum)
	}
}

// TestTrimEmptyMessages 覆盖边界：空 messages 返回空视图零统计。
func TestTrimEmptyMessages(t *testing.T) {
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(nil))
	if len(view.Lines) != 0 || view.CharCount != 0 {
		t.Errorf("空会话 Lines=%d CharCount=%d, want 0/0", len(view.Lines), view.CharCount)
	}
	if stats.TotalMessages != 0 || stats.KeptMessages != 0 || stats.InjectedDropped != 0 || stats.Truncated {
		t.Errorf("空会话统计=%+v, want 全零", stats)
	}
}

// TestTrimUserInputUnconditional 覆盖锚点二（BR2/BR3）：80KB 用户粘贴 + assistant 超预算，
// 用户行逐字节保留、assistant 行被丢、Truncated=true。
func TestTrimUserInputUnconditional(t *testing.T) {
	paste := strings.Repeat("A", 80*1024) // 80KB 用户粘贴，远超 MaxSessionChars
	long := strings.Repeat("B", MaxSessionChars+1000)
	msgs := []conversationlog.Message{
		mkMsg(paste),
		mkRoleMsg("assistant", long),
		mkRoleMsg("assistant", "短播报"),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Error("assistant 超预算应置 Truncated=true")
	}
	if got := countPrefix(view.Lines, "[USER]"); got != 1 {
		t.Fatalf("[USER] 行数=%d, want 1", got)
	}
	// 用户行逐字节保留：粘贴全文在视图内且可复原（多行粘贴此处为单行 80KB）。
	idx := strings.Index(strings.Join(view.Lines, "\n"), paste)
	if idx < 0 {
		t.Fatal("80KB 用户粘贴应完整出现在视图中")
	}
	if got := countPrefix(view.Lines, "[AI]"); got != 0 {
		t.Errorf("[AI] 行数=%d, want 0（assistant 行全部被截断丢弃）", got)
	}
	// 用户行不受截断影响：CharCount 含 80KB 用户行。
	if view.CharCount < utf8.RuneCountInString(paste) {
		t.Errorf("CharCount=%d 应含完整用户行 %d", view.CharCount, utf8.RuneCountInString(paste))
	}
}

// TestTrimAssistantMultiline 覆盖边界：assistant 多行叙述首行打标签后续行原样跟随（BR1）。
func TestTrimAssistantMultiline(t *testing.T) {
	narrative := "第一步分析日志。\n第二步定位根因。\n第三步修复验证。"
	view, _ := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail([]conversationlog.Message{
		mkRoleMsg("assistant", narrative),
	}))
	if len(view.Lines) != 3 {
		t.Fatalf("行数=%d, want 3", len(view.Lines))
	}
	if view.Lines[0] != "[AI] 第一步分析日志。" {
		t.Errorf("首行=%q, want [AI] 标签打头", view.Lines[0])
	}
	if view.Lines[1] != "第二步定位根因。" || view.Lines[2] != "第三步修复验证。" {
		t.Errorf("后续行应原样跟随：%v", view.Lines[1:])
	}
}

// TestTrimTruncationOrder 覆盖锚点三（BR3）：三档丢弃顺序断言。
// 布局（assistant 叙述 ≤200 字符为短播报、其余为长叙述，[TOOL] 行整体为工具摘要档）：
//
//	用户行 1 条、[EVENT] 1 条（不参与丢弃与预算计数）
//	短播报 2 条：150 / 10 字符；工具摘要 2 条：300 / 30 字符；长叙述 2 条：69000 / 5000 字符
//
// 非用户行字符合计 155+55+305+35+69005+5005 = 74560 > 72000，需丢 ≥ 2560。
// 预期（档间：短播报 → 工具摘要 → 长叙述；档内：字符数小的行先丢）：
//
//	短播报档清空（10 先丢、150 后丢）→ 工具档清空（30 先丢、300 后丢）
//	→ 长叙述档丢 5005 字符条后余 69005 ≤ 72000 停，69000 字符条保留（长叙述保留最多）。
func TestTrimTruncationOrder(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("查"),
		mkRoleMsg("assistant", strings.Repeat("S", 150)), // 短播报（先出现，较大）
		conversationlog.Message{Role: "tool", Kind: "tool_use", Text: strings.Repeat("T", 300)},
		mkRoleMsg("assistant", strings.Repeat("S", 10)),    // 短播报（后出现，较小）
		mkRoleMsg("assistant", strings.Repeat("L", 69000)), // 长叙述（较大）
		conversationlog.Message{Role: "tool", Kind: "tool_result", Text: strings.Repeat("T", 30)},
		mkMsg("[Request interrupted by user]"),
		mkRoleMsg("assistant", strings.Repeat("L", 5000)), // 长叙述（较小）
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Fatal("超预算应置 Truncated=true")
	}
	// 用户行与事件行不参与丢弃。
	if got := countPrefix(view.Lines, "[USER]"); got != 1 {
		t.Errorf("[USER] 行数=%d, want 1（用户行不参与丢弃）", got)
	}
	if got := countPrefix(view.Lines, "[EVENT]"); got != 1 {
		t.Errorf("[EVENT] 行数=%d, want 1（事件行不参与丢弃）", got)
	}
	// 短播报最先被丢、工具摘要次之：均整档清空；长叙述档内从小丢起，69000 条保留最多。
	// S*10/T*30/L*5000 是更长同类行的子串，统一用精确行集合校验。
	kept := map[string]bool{}
	for _, l := range view.Lines {
		kept[l] = true
	}
	for _, gone := range []string{
		"[AI] " + strings.Repeat("S", 150), "[AI] " + strings.Repeat("S", 10),
		"[TOOL] " + strings.Repeat("T", 300), "[TOOL] " + strings.Repeat("T", 30),
		"[AI] " + strings.Repeat("L", 5000),
	} {
		if kept[gone] {
			t.Errorf("应被丢弃的行仍保留（head %.20s）", gone)
		}
	}
	if !kept["[AI] "+strings.Repeat("L", 69000)] {
		t.Error("长叙述 69000 字符条应保留（长叙述最后截、保留最多）")
	}
}

// TestTrimTruncationStopInTier 覆盖边界：档未清空即达标即停（短播报档只丢小字符条）。
// 行字符：短播报 105+55、工具 207、长叙述 71655 → 非用户合计 72022 超 22。
// 预期丢 55 字符短播报后余 71967 ≤ 72000 即停，105 字符短播报、工具与长叙述保留。
func TestTrimTruncationStopInTier(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", strings.Repeat("S", 100)),
		mkRoleMsg("assistant", strings.Repeat("S", 50)),
		conversationlog.Message{Role: "tool", Kind: "tool_use", Text: strings.Repeat("T", 200)},
		mkRoleMsg("assistant", strings.Repeat("L", 71650)),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Fatal("超预算应置 Truncated=true")
	}
	// 保留集为 105/207/71655 三行（findLine 子串匹配会命中更长行，改用精确行匹配）。
	kept := map[string]bool{}
	for _, l := range view.Lines {
		kept[l] = true
	}
	if kept["[AI] "+strings.Repeat("S", 50)] {
		t.Error("档内 50 字符短播报应先被丢")
	}
	if !kept["[AI] "+strings.Repeat("S", 100)] {
		t.Error("达标即停：100 字符短播报应保留")
	}
	if !kept["[TOOL] "+strings.Repeat("T", 200)] || !kept["[AI] "+strings.Repeat("L", 71650)] {
		t.Error("达标即停：工具与长叙述应保留")
	}
	if len(view.Lines) != 3 {
		t.Errorf("达标即停应只丢一条，实得 %d 行", len(view.Lines))
	}
}

// TestTrimTruncationTiebreak 覆盖边界：档内同字符数按出现顺序从后往前丢。
// 两条 60 字符短播报（先 A 后 B）+ 71900 字符长叙述：合计超 35，
// 预期丢后出现的 B 即达标，A 保留。
func TestTrimTruncationTiebreak(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", strings.Repeat("A", 60)),
		mkRoleMsg("assistant", strings.Repeat("B", 60)),
		mkRoleMsg("assistant", strings.Repeat("L", 71900)),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Fatal("超预算应置 Truncated=true")
	}
	if findLine(view.Lines, "[AI]", strings.Repeat("B", 60)) != "" {
		t.Error("同字符数时后出现的 B 应先被丢")
	}
	if findLine(view.Lines, "[AI]", strings.Repeat("A", 60)) == "" {
		t.Error("同字符数时先出现的 A 应保留")
	}
}

// TestTrimShortNarrativePayloadTier 边界回归：短播报分档按叙述正文（res.Payload）计，
// 不含 [AI] 行标签（开发计划 02：分档阈值只划分 assistant 叙述内部）。
// res.Payload 恰 200 字符属短播报档；旧口径按整行计会把 196-200 区间错落长叙述档。
func TestTrimShortNarrativePayloadTier(t *testing.T) {
	// 布局：A=[AI]+200 字符短播报（tier 0，chars=206）、B=[AI]+71798 字符长叙述
	// （tier 2，chars=71804），合计 72010 > 72000，丢档内最小的 A 即达标。
	// 若 A 被错判长叙述档，同档从后丢会先丢 B，断言翻转。
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", strings.Repeat("S", 200)),
		mkRoleMsg("assistant", strings.Repeat("L", 71798)),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Fatal("超预算应触发截断")
	}
	kept := map[string]bool{}
	for _, l := range view.Lines {
		kept[l] = true
	}
	if kept["[AI] "+strings.Repeat("S", 200)] {
		t.Error("res.Payload 200 字符短播报是 tier 0 档内唯一条目，应先被丢")
	}
	if !kept["[AI] "+strings.Repeat("L", 71798)] {
		t.Error("长叙述档最后截，应保留（旧口径误丢此条即分档反转）")
	}
}

// TestTrimNoTruncationUnderBudget 覆盖边界：非用户合计恰等于预算边界不触发截断（BR3 边界值归属）。
// chars 口径含换行（每行一个）：[AI] 行 = 5+200+1 = 206，[TOOL] 行 = 7+71786+1，
// 合计恰等 MaxSessionChars=72000。
func TestTrimNoTruncationUnderBudget(t *testing.T) {
	assistant := strings.Repeat("S", 200)
	toolLen := MaxSessionChars - utf8.RuneCountInString("[AI] ") - 200 - 1 - utf8.RuneCountInString("[TOOL] ") - 1
	tool := strings.Repeat("T", toolLen)
	msgs := []conversationlog.Message{
		mkMsg("问"),
		mkRoleMsg("assistant", assistant),
		conversationlog.Message{Role: "tool", Kind: "tool_use", Text: tool},
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if stats.Truncated {
		t.Error("非用户合计恰等于 MaxSessionChars 不应触发截断")
	}
	wantChars := utf8.RuneCountInString("[USER] 问") + 1 + utf8.RuneCountInString("[AI] "+assistant) + 1 + utf8.RuneCountInString("[TOOL] "+tool) + 1
	if len(view.Lines) != 3 || view.CharCount != wantChars {
		t.Errorf("预算内应全保留：Lines=%d CharCount=%d want=%d", len(view.Lines), view.CharCount, wantChars)
	}
}

// TestTrimCustomParams 覆盖参数读取：注入前缀与脱敏正则热调（含读取失败回退出厂）。
func TestTrimCustomParams(t *testing.T) {
	// 自定义前缀集命中自定义形态。
	ext := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.inject_prefixes": {"自定义噪音"},
	}})
	view, stats := ext.Trim(mkDetail([]conversationlog.Message{
		mkMsg("自定义噪音内容"),
		mkMsg("正常输入"),
	}))
	if len(view.Lines) != 1 || !strings.HasPrefix(view.Lines[0], "[USER] 正常输入") {
		t.Errorf("自定义前缀应生效：Lines=%v", view.Lines)
	}
	if stats.InjectedDropped != 1 {
		t.Errorf("InjectedDropped=%d, want 1", stats.InjectedDropped)
	}

	// 自定义脱敏正则（非出厂字面量，兜底 [REDACTED]）。
	ext = newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.redact_patterns": {`内部代号-\d+`},
	}})
	view, _ = ext.Trim(mkDetail([]conversationlog.Message{mkMsg("看下内部代号-9527的日志")}))
	if !strings.Contains(view.Lines[0], "[REDACTED]") || strings.Contains(view.Lines[0], "9527") {
		t.Errorf("自定义脱敏正则应替换 [REDACTED]，得 %q", view.Lines[0])
	}

	// 参数读取 DB 故障：回退出厂前缀与出厂脱敏，安全机制不因故障失效。
	ext = newTrimExtractor(&fakeSysParams{err: errFakeParam})
	view, _ = ext.Trim(mkDetail([]conversationlog.Message{
		mkMsg("The following skills are available: x"),
		mkMsg("密钥 sk-abcdefghijklmnopqrst 在 D:\\a\\b.txt"),
	}))
	if len(view.Lines) != 1 {
		t.Fatalf("故障回退出厂黑名单应生效，Lines=%v", view.Lines)
	}
	if !strings.Contains(view.Lines[0], "[SECRET]") || !strings.Contains(view.Lines[0], "[PATH]") {
		t.Errorf("故障回退出厂脱敏应生效，得 %q", view.Lines[0])
	}
}

// TestTrimChineseRuneTier 中文口径回归：100 汉字叙述（UTF-8 占 300+ 字节）
// 按 rune 计 105 ≤ 200 判短播报档，与英文短播报同档参与档内排序；
// 若误按字节计（305 > 200）会错落长叙述档，导致截断顺序漂移。
// 布局：A=[AI]+100 汉字（105 rune）、B=[AI]+195 个 S（200 rune，tier 0）、
// L=[AI]+71750 个 L（tier 2），非用户合计 72060 超 60，丢档内最小的 A 即达标。
func TestTrimChineseRuneTier(t *testing.T) {
	chinese := strings.Repeat("好", 100) // 100 rune / 300 字节
	msgs := []conversationlog.Message{
		mkMsg("问"),
		mkRoleMsg("assistant", chinese),
		mkRoleMsg("assistant", strings.Repeat("S", 195)),
		mkRoleMsg("assistant", strings.Repeat("L", 71750)),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Fatal("超预算应置 Truncated=true")
	}
	if got := countPrefix(view.Lines, "[AI]"); got != 2 {
		t.Fatalf("[AI] 行数=%d, want 2（只丢档内最小的 100 汉字短播报）\n实得 %v", got, view.Lines)
	}
	kept := map[string]bool{}
	for _, l := range view.Lines {
		kept[l] = true
	}
	if kept["[AI] "+chinese] {
		t.Error("100 汉字短播报是 tier 0 档内最小条，应先被丢")
	}
	if !kept["[AI] "+strings.Repeat("S", 195)] {
		t.Error("英文短播报 200 rune 同档更大，达标即停应保留（字节口径会误丢此条）")
	}
	if !kept["[AI] "+strings.Repeat("L", 71750)] {
		t.Error("长叙述档最后截，应保留")
	}
}

// TestRedactPatternsMatchDefaults 守护出厂脱敏单源：seed 序列化值（json.Marshal
// RedactPatterns()）的 pattern 集与 redactDefaults 预编译正则的 String() 逐条相等，
// 防双源漂移（漂移会让 DB 注入分支与出厂回退分支匹配域分叉、短密钥漏脱敏）。
func TestRedactPatternsMatchDefaults(t *testing.T) {
	seeded, err := json.Marshal(RedactPatterns())
	if err != nil {
		t.Fatalf("marshal RedactPatterns(): %v", err)
	}
	var seededPatterns []string
	if err := json.Unmarshal(seeded, &seededPatterns); err != nil {
		t.Fatalf("unmarshal RedactPatterns(): %v", err)
	}
	if len(seededPatterns) != len(redactDefaults) {
		t.Fatalf("条数漂移：RedactPatterns()=%d redactDefaults=%d", len(seededPatterns), len(redactDefaults))
	}
	for i, rule := range redactDefaults {
		if seededPatterns[i] != rule.re.String() {
			t.Errorf("pattern[%d] 漂移：seed=%q redactDefaults=%q", i, seededPatterns[i], rule.re.String())
		}
	}
}

// TestTrimIdeSelectionPaste 覆盖边界：IDE 选中文本壳层丢弃，选中文本只计
// PasteCharCount 不进视图（specs 第三类第 5 条），纯选中会话视图无 [USER] 行。
func TestTrimIdeSelectionPaste(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("<ide_selection>\nfunc main() {}\n</ide_selection>"),
		mkRoleMsg("assistant", strings.Repeat("B", MaxSessionChars+10)),
	}
	view, stats := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	if !stats.Truncated {
		t.Error("超预算应 Truncated=true")
	}
	for _, l := range view.Lines {
		if strings.HasPrefix(l, "[USER] ") {
			t.Errorf("IDE 选中文本不进视图（壳层丢弃仅计粘贴），实得 %q", l)
		}
	}
}

// TestTrimRedactAllKeptRows 回归锚点：保留类行统一过输入侧脱敏——assistant 叙述、
// IDE 选中文本与 @CurrentContext 转储含敏感串时视图只见占位符（specs §3.3
// 视图文本与 LLM 产出片段均只见占位符，非仅用户行）。
func TestTrimRedactAllKeptRows(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", "密钥 sk-Zz9SecretKey123 生效，服务在内网 10.1.2.3 上。"),
		mkMsg("<ide_selection>\nconst key = \"sk-IdeSelKey9999\";\n</ide_selection>"),
		mkMsg("@CurrentContext{{{<startContext>\n服务地址 192.168.1.100\n检查连通性"),
	}
	view, _ := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail(msgs))
	joined := strings.Join(view.Lines, "\n")
	for _, plain := range []string{"sk-Zz9SecretKey123", "sk-IdeSelKey9999", "192.168.1.100"} {
		if strings.Contains(joined, plain) {
			t.Errorf("视图含明文敏感串 %q\n实得 %q", plain, view.Lines)
		}
	}
	for _, ph := range []string{"[SECRET]", "[ADDR]"} {
		if !strings.Contains(joined, ph) {
			t.Errorf("视图缺少占位符 %s\n实得 %q", ph, view.Lines)
		}
	}
}

// TestTrimPerformance 覆盖锚点四（BR4）：500 消息 / 120KB 会话 Trim 耗时 < 50ms。
func TestTrimPerformance(t *testing.T) {
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
	// 补足 120KB：追加长叙述（不推进消息总数）。
	for total := 0; total < 120*1024; {
		chunk := 2000
		msgs = append(msgs, mkRoleMsg("assistant", strings.Repeat("L", chunk)))
		total += chunk
	}
	total := 0
	for _, m := range msgs {
		total += len(m.Text)
	}
	if total < 120*1024 || len(msgs) < 500 {
		t.Fatalf("构造规模不足：msgs=%d chars=%d", len(msgs), total)
	}

	ext := newTrimExtractor(&fakeSysParams{})
	ext.Trim(mkDetail(msgs)) // 预热（正则编译与分配稳定后计时）
	start := time.Now()
	ext.Trim(mkDetail(msgs))
	cost := time.Since(start)
	if cost >= 50*time.Millisecond {
		t.Errorf("Trim 耗时 %v ≥ 50ms（specs §3.1）", cost)
	}
}

// mkClaudeMD 构造 claudeMd 复合消息（真实网关形态：引导语前置、标题行独立、
// currentDate 日期段在后），返回完整消息与剥离日期段后的规约正文。
func mkClaudeMD() (string, string) {
	intro := "As you answer the user's questions, you can use the following context:\n# claudeMd\nCodebase and user instructions are shown below."
	body := intro + "\n\n## 规约A\n\n规约正文段落。\n\n## 规约B\n\n另一段正文。"
	full := "<system-reminder>\n" + body + "\n\n# currentDate\n\n2026-08-24\n</system-reminder>"
	return full, body
}

func TestExtractFingerprint(t *testing.T) {
	full, body := mkClaudeMD()
	fp := extractFingerprint(full, nil)
	if fp == nil {
		t.Fatal("claudeMd 复合消息应产指纹，得 nil")
	}
	if fp.Kind != FPClaudeMD {
		t.Errorf("Kind=%q, want %q", fp.Kind, FPClaudeMD)
	}
	if fp.CharCount != utf8.RuneCountInString(body) {
		t.Errorf("CharCount=%d, want %d（剥离 currentDate 后正文字符数）", fp.CharCount, utf8.RuneCountInString(body))
	}
	if fp.Hash != fpHashHex(body) {
		t.Errorf("Hash=%q, want %q（SHA-256 前 16 位）", fp.Hash, fpHashHex(body))
	}
	wantSections := []string{"# claudeMd", "## 规约A", "## 规约B"}
	if len(fp.SectionList) != len(wantSections) {
		t.Fatalf("SectionList=%v, want %v", fp.SectionList, wantSections)
	}
	for i, w := range wantSections {
		if fp.SectionList[i] != w {
			t.Errorf("SectionList[%d]=%q, want %q", i, fp.SectionList[i], w)
		}
	}
}

func TestFingerprintSectionRedact(t *testing.T) {
	full := "<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n## 日志目录 D:\\logs\\app\n\n正文。\n</system-reminder>"
	fp := extractFingerprint(full, nil)
	if fp == nil {
		t.Fatal("应产指纹")
	}
	// 指纹场景传出厂 RedactPatterns() 常量，注入出厂字面量命中类型化映射，输出 [PATH]。
	want := []string{"# claudeMd", "## 日志目录 [PATH]"}
	if len(fp.SectionList) != len(want) {
		t.Fatalf("SectionList=%v, want %v", fp.SectionList, want)
	}
	for i, w := range want {
		if fp.SectionList[i] != w {
			t.Errorf("SectionList[%d]=%q, want %q（标题过 Redact 脱敏）", i, fp.SectionList[i], w)
		}
	}
	// 哈希与计数按脱敏前原文计算（脱敏只作用于章节标题）。
	if fp.Hash != fpHashHex("# claudeMd\nCodebase and user instructions are shown below.\n\n## 日志目录 D:\\logs\\app\n\n正文。") {
		t.Errorf("Hash 应按脱敏前原文计算")
	}
	// 接线一致性：classifyMsg 产出的指纹哈希与 extractFingerprint 同口径非空。
	if cres := classifyMsg(mkMsg(full), nil, nil, nil); cres.Fp == nil || cres.Fp.Hash != fp.Hash {
		t.Errorf("classifyMsg 接线指纹哈希不一致：%v vs %v", cres.Fp, fp)
	}
}

func TestFingerprintDateStability(t *testing.T) {
	_, body := mkClaudeMD()
	a := "<system-reminder>\n" + body + "\n\n# currentDate\n\n2026-08-23\n</system-reminder>"
	b := "<system-reminder>\n" + body + "\n\n# currentDate\n\n2026-09-01\n</system-reminder>"
	fa, fb := extractFingerprint(a, nil), extractFingerprint(b, nil)
	if fa == nil || fb == nil {
		t.Fatal("两条均应产指纹")
	}
	if fa.Hash != fb.Hash {
		t.Errorf("仅 currentDate 日期不同，Hash=%q/%q 应相等（日期剥离口径）", fa.Hash, fb.Hash)
	}
	if fa.CharCount != fb.CharCount {
		t.Errorf("仅日期不同，CharCount=%d/%d 应相等", fa.CharCount, fb.CharCount)
	}
	if fa.Hash != fpHashHex(body) {
		t.Errorf("Hash 应等于剥离后正文哈希，得 %q want %q", fa.Hash, fpHashHex(body))
	}
}

func TestFingerprintIteration(t *testing.T) {
	_, body := mkClaudeMD()
	a := "<system-reminder>\n" + body + "\n\n# currentDate\n\n2026-08-24\n</system-reminder>"
	changed := strings.Replace(body, "## 规约B", "## 规约B改", 1)
	b := "<system-reminder>\n" + changed + "\n\n# currentDate\n\n2026-08-24\n</system-reminder>"
	fa, fb := extractFingerprint(a, nil), extractFingerprint(b, nil)
	if fa == nil || fb == nil {
		t.Fatal("两条均应产指纹")
	}
	if fa.Hash == fb.Hash {
		t.Errorf("改一节标题后 Hash 应变化，两者同为 %q", fa.Hash)
	}
	if fb.Hash != fpHashHex(changed) {
		t.Errorf("迭代后 Hash=%q, want %q", fb.Hash, fpHashHex(changed))
	}
	// 章节差异可检出。
	hasB, hasBm := false, false
	for _, s := range fa.SectionList {
		if s == "## 规约B" {
			hasB = true
		}
	}
	for _, s := range fb.SectionList {
		if s == "## 规约B改" {
			hasBm = true
		}
	}
	if !hasB || !hasBm {
		t.Errorf("SectionList 差异应可检出：a 含旧标题=%v，b 含新标题=%v", hasB, hasBm)
	}
}

func TestExtractFingerprintNonSpec(t *testing.T) {
	cases := map[string]string{
		"技能清单":                    "<system-reminder>\nThe following skills are available:\n- skill-a\n</system-reminder>",
		"文件全文回显":                  "<system-reminder>\nResult of calling the Read tool:\n# 某业务文档\n\n13k 字符级正文\n</system-reminder>",
		"currentDate 独立块":         "<system-reminder>\n# currentDate\n\n2026-08-24\n</system-reminder>",
		"total_tokens 遥测":         "<system-reminder>\n<total_tokens>15000000 tokens left</total_tokens>\n</system-reminder>",
		"MCP Server Instructions": "<system-reminder>\nMCP Server Instructions\n\n指令内容\n</system-reminder>",
		"data-role 变体":            "<system-reminder data-role=\"user-context\">\nmemory_and_skills_reminder 载荷\n</system-reminder>",
	}
	for name, text := range cases {
		if fp := extractFingerprint(text, nil); fp != nil {
			t.Errorf("%s 应归噪音返回 nil，得 %+v", name, fp)
		}
	}
}

func TestExtractFingerprintKinds(t *testing.T) {
	agents := "<system-reminder>\nAGENTS.md\n\n# 项目规约\n\n正文\n</system-reminder>"
	fp := extractFingerprint(agents, nil)
	if fp == nil || fp.Kind != FPAgentsMD {
		t.Errorf("AGENTS.md 指纹 Kind=%v, want %s", fp, FPAgentsMD)
	}
	// Base directory 技能文档（user 角色独立通道同一入口）。
	skill := "Base directory for this skill: /skills/x\n\n# Skill X\n\n技能正文"
	fp = extractFingerprint(skill, nil)
	if fp == nil || fp.Kind != FPSkillDoc {
		t.Errorf("Base directory 指纹 Kind=%v, want %s", fp, FPSkillDoc)
	}
	// 空串与无特征正文归 nil。
	if extractFingerprint("", nil) != nil {
		t.Error("空串应返回 nil")
	}
	if extractFingerprint("普通业务文本无规约特征", nil) != nil {
		t.Error("无特征文本应返回 nil")
	}
	// 深层标题（h3-h6）同样进 SectionList。
	deep := "<system-reminder>\nAGENTS.md\n\n### 子节\n\n正文\n</system-reminder>"
	fp = extractFingerprint(deep, nil)
	if fp == nil || len(fp.SectionList) != 1 || fp.SectionList[0] != "### 子节" {
		t.Errorf("深层标题 SectionList=%v", fp.SectionList)
	}
}

// TestRedactCompileCache 守护注入分支缓存：同一 pattern（含编译失败态）命中后返回
// 同一缓存条目，坏 pattern 不逐次重编译。
func TestRedactCompileCache(t *testing.T) {
	good := `(?i)token=\S+`
	e1 := compileRedactPattern(good)
	e2 := compileRedactPattern(good)
	if e1.re != e2.re || e1.err != nil {
		t.Errorf("合法 pattern 二次取应命中缓存：e1=%v e2=%v", e1.re == e2.re, e2.err)
	}
	bad := "["
	b1 := compileRedactPattern(bad)
	b2 := compileRedactPattern(bad)
	if b1.err == nil || b1.err != b2.err {
		t.Errorf("非法 pattern 失败态应缓存复用：b1.err=%v b2.err=%v", b1.err, b2.err)
	}
}

// TestClassifyInterjectTailNoise 真实插话尾部噪音回归：插话在用户正文后固定跟
// 解释段（This is how Claude Code surfaces...）、日期变更、SR 包裹回显与
// total_tokens 尾段，提取载荷只含用户正文，尾部噪音不得随指令进视图。
func TestClassifyInterjectTailNoise(t *testing.T) {
	msg := mkRoleMsg("system", "The user sent a new message while you were working:\n你能明白我说的吗\n\n"+
		"This is how Claude Code surfaces messages the user sends mid-turn. Use it to spot the user's original language...\n\n"+
		"The date has changed. Today is 2026-08-25.\n\n"+
		"<system-reminder>\nContents of D:\\proj\\CLAUDE.md:\n大段文件回显\n</system-reminder>\n\n"+
		"<total_tokens>14775529 tokens left</total_tokens>")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classExtract || res.Payload != "你能明白我说的吗" {
		t.Errorf("插话尾段噪音 res.Class=%d res.Payload=%q, want extract/仅用户正文", res.Class, res.Payload)
	}
}

// TestClassifyInterruptInlineSR 打断判定顺序回归（specs 第三类第 6 条：先剥离内嵌块
// 再计打断）：用户正文尾部 appended SR 块内的框架标记字面不得把整条降级为事件行。
func TestClassifyInterruptInlineSR(t *testing.T) {
	msg := mkMsg("停一下，我改主意了，先查另一个问题\n\n<system-reminder>\nThe user doesn't want to proceed. [Request interrupted by user for tool use]\n</system-reminder>")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classKeep || res.Payload != "停一下，我改主意了，先查另一个问题" {
		t.Errorf("内嵌 SR 含打断标记 res.Class=%d res.Payload=%q, want keep/仅正文", res.Class, res.Payload)
	}
	// 正文自身含标记：剥标记后正文保留（用户真打断了，正文仍是指令证据）。
	res = classifyMsg(mkMsg("正文 [Request interrupted by user] 尾部"), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "正文  尾部" {
		t.Errorf("正文打断 res.Class=%d res.Payload=%q, want keep/剥后正文", res.Class, res.Payload)
	}
}

// TestStatsInterruptCountInlineSR 打断计数与判定链同口径：保留类消息内嵌 SR 块中的
// 标记字面不计入 InterruptCount。
func TestStatsInterruptCountInlineSR(t *testing.T) {
	detail := mkDetail([]conversationlog.Message{
		mkMsg("指令正文\n\n<system-reminder>\n[Request interrupted by user for tool use]\n</system-reminder>"),
	})
	p := newTrimExtractor(&fakeSysParams{}).prepare("", detail)
	if p.profile.InterruptCount != 0 {
		t.Errorf("内嵌 SR 块内标记不应计数, InterruptCount=%d", p.profile.InterruptCount)
	}
	detail2 := mkDetail([]conversationlog.Message{
		mkMsg("正文 [Request interrupted by user] 尾部"),
	})
	p2 := newTrimExtractor(&fakeSysParams{}).prepare("", detail2)
	if p2.profile.InterruptCount != 1 {
		t.Errorf("正文标记应计数, InterruptCount=%d", p2.profile.InterruptCount)
	}
}

// TestTrimBlankRedactEntryFallback 回归：参数页误配脱敏集为全空串条目（[""]）
// 时回退出厂脱敏，明文敏感串不出域。旧实现以非空集走注入分支且逐条跳过，出厂
// 规则整体失效，与「停用脱敏须改参数值而非清空」的 fail-safe 语义矛盾。
func TestTrimBlankRedactEntryFallback(t *testing.T) {
	ext := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.redact_patterns": {""},
	}})
	view, _ := ext.Trim(mkDetail([]conversationlog.Message{
		mkMsg("密钥 sk-AbCd1234EfGh 在 D:\\a\\b.txt 与 10.1.2.3"),
	}))
	if strings.Contains(strings.Join(view.Lines, "\n"), "sk-AbCd1234EfGh") {
		t.Errorf("全空串脱敏集须回退出厂，明文密钥进视图: %v", view.Lines)
	}
	// 空串混入正常条目：空串跳过，其余条目生效（对称 injectPrefixes 语义）。
	ext = newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.redact_patterns": {"", `内部代号-\d+`},
	}})
	view, _ = ext.Trim(mkDetail([]conversationlog.Message{mkMsg("看下内部代号-9527的日志")}))
	if !strings.Contains(view.Lines[0], "[REDACTED]") {
		t.Errorf("混入空串不得让其余正则失效, got %q", view.Lines[0])
	}
}

// TestFingerprintSpecBodyHeadingAnchor 回归：currentDate 切分行首锚定，规约正文
// 自身含 ## currentDate 类标题时不再从标题中段截断（旧子串定位截断后尾部残留
// 孤立 #，哈希与章节清单系统性失真）。
func TestFingerprintSpecBodyHeadingAnchor(t *testing.T) {
	body := "AGENTS.md\n\n## 前言\n正文A。\n\n## currentDate 机制说明\n日期段落描述。"
	full := "<system-reminder>\n" + body + "\n</system-reminder>"
	fp := extractFingerprint(full, nil)
	if fp == nil || fp.Kind != FPAgentsMD {
		t.Fatalf("应产 agents_md 指纹, got %+v", fp)
	}
	// 正文完整保留：哈希覆盖全部章节，SectionList 含正文内的 currentDate 机制章节标题。
	if fp.Hash != fpHashHex(body) {
		t.Errorf("Hash 应按完整正文计算（行首锚定不截断内文标题）")
	}
	found := false
	for _, s := range fp.SectionList {
		if s == "## currentDate 机制说明" {
			found = true
		}
	}
	if !found {
		t.Errorf("SectionList 应含正文内 currentDate 章节标题, got %v", fp.SectionList)
	}
	// 真正的 currentDate 日期段（标题行行首）仍照常剥离。
	withDate := "<system-reminder>\n" + body + "\n\n# currentDate\n\n2026-08-26\n</system-reminder>"
	fp2 := extractFingerprint(withDate, nil)
	if fp2 == nil || fp2.Hash != fpHashHex(body) {
		t.Errorf("行首 currentDate 日期段应照常剥离, got %+v", fp2)
	}
}
func TestClassifyCommandArgsDiscussion(t *testing.T) {
	discuss := "怎么在命令里带参数？我看文档写的是 <command-args>这里是参数</command-args> 这样用对吗"
	res := classifyMsg(mkMsg(discuss), nil, nil, nil)
	if res.Class != classKeep || res.Payload != discuss {
		t.Errorf("讨论标签语法 res.Class=%d res.Payload=%q, want keep/原文", res.Class, res.Payload)
	}
	// 命令壳打头的真实回显仍走提取。
	echo := "<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>重点看并发</command-args>"
	res = classifyMsg(mkMsg(echo), nil, nil, nil)
	if res.Class != classExtract || res.Payload != "重点看并发" {
		t.Errorf("命令回显 res.Class=%d res.Payload=%q, want extract/args内文", res.Class, res.Payload)
	}
	// 实测无参命令回显形态（User: 打头三标签混排）：Contains 命中但壳非打头，
	// 落黑名单 User: 前缀丢弃，结果与提取通道的空 args drop 殊途同归。
	echoNoArgs := "User: <command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>"
	res = classifyMsg(mkMsg(echoNoArgs), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("无参命令回显 res.Class=%d, want classDrop", res.Class)
	}
}

// TestClassifySRWrappedIdeSelection SR 包裹的 ide_selection 选中文本按粘贴信号保留
// （Claude Code 真实形态常经 SR 壳注入），不得整条 drop 丢失 PasteCharCount。
func TestClassifySRWrappedIdeSelection(t *testing.T) {
	msg := mkMsg("<system-reminder>\n<ide_selection>\nfunc a() {}\n</ide_selection>\n</system-reminder>")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classKeep || res.Payload != "func a() {}" || !res.IsIDESelection {
		t.Errorf("SR 包裹选中 res.Class=%d res.Payload=%q res.IsIDESelection=%v, want keep/选中文本/true", res.Class, res.Payload, res.IsIDESelection)
	}
	// SR 包裹但无选中块仍走指纹/噪音分流（此处为噪音 → drop）。
	noisy := "<system-reminder>\n# currentDate\n\n2026-08-24\n</system-reminder>"
	res = classifyMsg(mkMsg(noisy), nil, nil, nil)
	if res.Class != classDrop || res.IsIDESelection {
		t.Errorf("无选中块 res.Class=%d res.IsIDESelection=%v, want classDrop/false", res.Class, res.IsIDESelection)
	}
	// 统计消费：SR 包裹选中文本计入 PasteCharCount、不计 UserMsgCount。
	detail := mkDetail([]conversationlog.Message{mkMsg("<system-reminder>\n<ide_selection>\nfunc a() {}\n</ide_selection>\n</system-reminder>")})
	p := newTrimExtractor(&fakeSysParams{}).prepare("", detail)
	if p.profile.PasteCharCount != len([]rune("func a() {}")) {
		t.Errorf("SR 包裹选中 PasteCharCount=%d, want %d", p.profile.PasteCharCount, len([]rune("func a() {}")))
	}
	if p.profile.UserMsgCount != 0 {
		t.Errorf("SR 包裹选中 UserMsgCount=%d, want 0", p.profile.UserMsgCount)
	}
}

// TestClassifySRSelectionEdge 回归：选中块未闭合不取到末尾（防 SR 尾壳与框架注入
// 整段计入 PasteCharCount）；SR 复合消息混排选中文本与规约载荷时指纹优先不丢；
// system 角色 SR 选中文本不进选中通道（防误记 [USER] 行污染零输入判定）。
func TestClassifySRSelectionEdge(t *testing.T) {
	// 未闭合：上游截断丢失 </ide_selection>，按噪音 drop，粘贴规模宁漏计不虚增。
	unclosed := mkMsg("<system-reminder>\n<ide_selection>\n选中但未闭合\n</system-reminder>")
	res := classifyMsg(unclosed, nil, nil, nil)
	if res.Class != classDrop || res.IsIDESelection {
		t.Errorf("未闭合选中 res.Class=%d res.IsIDESelection=%v, want classDrop/false", res.Class, res.IsIDESelection)
	}
	// 混排规约载荷与选中块：指纹优先，选中文本通道让位。
	mixed := mkMsg("<system-reminder>\n<ide_selection>\n选中片段\n</ide_selection>\n# claudeMd\nCodebase and user instructions are shown below.\n</system-reminder>")
	res = classifyMsg(mixed, nil, nil, nil)
	if res.Class != classFingerprint || res.Fp == nil || res.Fp.Kind != FPClaudeMD {
		t.Errorf("混排指纹 res.Class=%d res.Fp=%v, want fingerprint/claude_md", res.Class, res.Fp)
	}
	// system 角色 SR 选中：走噪音 drop，不产 [USER] 行。
	sysSel := mkRoleMsg("system", "<system-reminder>\n<ide_selection>\nsys 选中\n</ide_selection>\n</system-reminder>")
	res = classifyMsg(sysSel, nil, nil, nil)
	if res.Class != classDrop || res.IsIDESelection {
		t.Errorf("system 侧 SR 选中 res.Class=%d res.IsIDESelection=%v, want classDrop/false", res.Class, res.IsIDESelection)
	}
}

// TestClassifyIdeSelectionTrailingBody 回归：ide_selection 闭标签后的随附正文是
// 真实用户输入，须保留进 res.Payload（specs：用户真实输入无条件全文进视图），丢弃会
// 让「选中后提问」形态的指令证据丢失、单输入会话误落 empty_shell。带随附正文时
// 消息转普通保留类（res.IsIDESelection=false，选中文本作正文前缀），粘贴判定回落通用规则。
func TestClassifyIdeSelectionTrailingBody(t *testing.T) {
	// 裸包裹 + 随附提问。
	msg := mkMsg("<ide_selection>\nfunc main() {}\n</ide_selection>\n\n请解释这段代码")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classKeep || res.IsIDESelection {
		t.Fatalf("裸包裹随附正文 res.Class=%d res.IsIDESelection=%v, want keep/false（转普通保留类）", res.Class, res.IsIDESelection)
	}
	if !strings.Contains(res.Payload, "func main() {}") || !strings.Contains(res.Payload, "请解释这段代码") {
		t.Errorf("res.Payload 应同时含选中文本与随附正文, got %q", res.Payload)
	}
	// SR 包裹 + 随附提问（正文在 SR 壳外）：正文拼回，SR 壳不残留。
	srMsg := mkMsg("<system-reminder>\n<ide_selection>\nfunc a() {}\n</ide_selection>\n</system-reminder>\n这段代码有性能问题吗")
	res = classifyMsg(srMsg, nil, nil, nil)
	if res.Class != classKeep || res.IsIDESelection {
		t.Fatalf("SR 包裹随附正文 res.Class=%d res.IsIDESelection=%v, want keep/false", res.Class, res.IsIDESelection)
	}
	if !strings.Contains(res.Payload, "func a() {}") || !strings.Contains(res.Payload, "这段代码有性能问题吗") {
		t.Errorf("res.Payload 应含选中文本与随附正文, got %q", res.Payload)
	}
	if strings.Contains(res.Payload, "system-reminder") {
		t.Errorf("res.Payload 不应残留 SR 壳, got %q", res.Payload)
	}
	// 纯选中（无随附正文）保持原行为：res.IsIDESelection=true 只计粘贴不计用户消息。
	pure := mkMsg("<ide_selection>\nfunc b() {}\n</ide_selection>")
	res = classifyMsg(pure, nil, nil, nil)
	if !res.IsIDESelection || res.Payload != "func b() {}" {
		t.Errorf("纯选中 res.IsIDESelection=%v res.Payload=%q, want true/选中文本", res.IsIDESelection, res.Payload)
	}
	// 随附正文进视图产 [USER] 行（指令证据可见）。
	view, _ := newTrimExtractor(&fakeSysParams{}).Trim(mkDetail([]conversationlog.Message{msg}))
	if got := countPrefix(view.Lines, "[USER] "); got != 1 {
		t.Errorf("随附正文应进视图产 [USER] 行, got %d 行 %v", got, view.Lines)
	}
}

// TestClassifyInterjectBeforeInterrupt 回归：插话提取先于打断检索，插话消息内嵌
// 打断标记时转述指令不丢（先打断会把指令整条降级事件行，UserMsgCount 归零）。
// 标记字面随 res.Payload 保留属全角色子串检索的规约内近似，此处只锁指令保全。
func TestClassifyInterjectBeforeInterrupt(t *testing.T) {
	msg := mkRoleMsg("system", "The user sent a new message while you were working:\n把方案改成B\n[Request interrupted by user for tool use]")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classExtract || !strings.Contains(res.Payload, "把方案改成B") {
		t.Errorf("内嵌打断的插话 res.Class=%d res.Payload=%q, want extract/含转述正文", res.Class, res.Payload)
	}
	// 普通消息（非插话前缀）的打断检索不受顺序影响：剥标记后正文保留。
	res = classifyMsg(mkMsg("正文 [Request interrupted by user] 尾部"), nil, nil, nil)
	if res.Class != classKeep || res.Payload != "正文  尾部" {
		t.Errorf("普通打断 res.Class=%d res.Payload=%q, want keep/剥后正文", res.Class, res.Payload)
	}
}

// TestClassifyToolRoleText 回归：tool 角色的 text 变体（上游异常透传）归工具
// 证据行，不走 assistant 叙述兜底产伪 [AI] 行绕过零响应防线。
func TestClassifyToolRoleText(t *testing.T) {
	m := conversationlog.Message{Role: "tool", Kind: "text", Text: "工具输出正文形态"}
	res := classifyMsg(m, nil, nil, nil)
	if res.Class != classTool {
		t.Errorf("tool 角色 text res.Class=%d, want classTool", res.Class)
	}
}

// TestClassifyBlacklistRoleScope 回归：黑名单对非 user 角色收窄为 assistant 子集
// （模型生成物重放/裁决产物/载荷转述）。OMO 转写与碎片家族前缀对 assistant 真实
// 叙述的前缀碰撞属误杀（实测形态 Read clearTaskRouteLayers.），唯一叙述被误杀的
// 零工具会话会误落 empty_shell 终态不可逆；user 侧全集不受子集化影响。
func TestClassifyBlacklistRoleScope(t *testing.T) {
	// assistant 真实叙述：前缀与 OMO/碎片家族碰撞，应保留。
	for _, text := range []string{
		"Read clearTaskRouteLayers.",
		"Write the migration as follows:",
		"- [ ] 修复登录页",
		"count 个原子操作已完成。",
	} {
		res := classifyMsg(mkRoleMsg("assistant", text), nil, nil, nil)
		if res.Class != classKeep {
			t.Errorf("assistant 叙述 %q res.Class=%d, want keep（前缀碰撞不误杀）", text, res.Class)
		}
	}
	// assistant 侧模型生成物重放与裁决产物：子集内前缀仍生效。
	for _, text := range []string{
		"<analysis>\n回顾全量历史对话",
		"This session is being continued from a previous",
		"<block>no</block>",
		"User: 之前说过要改配置",
	} {
		res := classifyMsg(mkRoleMsg("assistant", text), nil, nil, nil)
		if res.Class != classDrop {
			t.Errorf("assistant 重放/裁决形态 %q res.Class=%d, want drop", text, res.Class)
		}
	}
	// user 侧全集不变：OMO 转写与碎片前缀照常拦截。
	for _, text := range []string{
		"TodoWrite 4 items",
		"Read args=111",
		"- [DataX 目标表命名偏好](memory)",
	} {
		res := classifyMsg(mkMsg(text), nil, nil, nil)
		if res.Class != classDrop {
			t.Errorf("user 转储形态 %q res.Class=%d, want drop", text, res.Class)
		}
	}
	// 配置集收窄同样生效（回归：配置路径曾全量应用致收窄失效）：运维增补的
	// user 侧转储前缀只拦 user，assistant 同前缀真实叙述保留；出厂子集条目
	//（User: ）对 assistant 仍生效，user 侧配置全集不受影响。
	cfg := append(InjectPrefixes(), "OMO 转写新形态: ")
	res := classifyMsg(mkRoleMsg("assistant", "OMO 转写新形态: 这是运维为 user 转储增补的前缀"), cfg, nil, nil)
	if res.Class != classKeep {
		t.Errorf("assistant 增补前缀叙述 res.Class=%d, want keep（配置集收窄生效）", res.Class)
	}
	res = classifyMsg(mkRoleMsg("assistant", "User: 之前说过要改配置"), cfg, nil, nil)
	if res.Class != classDrop {
		t.Errorf("assistant User: 重放 res.Class=%d, want drop（收窄子集仍生效）", res.Class)
	}
	res = classifyMsg(mkMsg("OMO 转写新形态: user 侧新转储形态"), cfg, nil, nil)
	if res.Class != classDrop {
		t.Errorf("user 增补前缀转储 res.Class=%d, want drop（user 侧配置全集）", res.Class)
	}
}

// TestSRWrappedSelectionDoubleClose 回归：闭标签后随附正文带多层 SR 尾壳（框架
// 嵌套注入实测形态）须循环剥净，残留壳会让标签原文混进 [USER] 视图行并计入
// PasteCharCount（虚增粘贴规模）。
func TestSRWrappedSelectionDoubleClose(t *testing.T) {
	sel := "const layered = drawLayers(map)"
	body := "帮我把这段渲染逻辑抽成独立函数"
	msg := "<system-reminder>\n<ide_selection>\n" + sel + "\n</ide_selection>\n" +
		body + "\n</system-reminder>\n</system-reminder>"
	res := classifyMsg(mkMsg(msg), nil, nil, nil)
	if res.Class != classKeep {
		t.Fatalf("带随附正文 res.Class=%d, want keep", res.Class)
	}
	if strings.Contains(res.Payload, "</system-reminder>") {
		t.Errorf("payload 残留 SR 闭壳: %q", res.Payload)
	}
	if !strings.Contains(res.Payload, sel) || !strings.Contains(res.Payload, body) {
		t.Errorf("payload 应含选中与正文: %q", res.Payload)
	}
}

// TestClassifyCommandArgsRoleLimit 回归：提取通道仅 user 角色触发。assistant/system
// 正文含标签字面（如解释 slash 命令写法）不得被劫为用户输入，应走各自角色保留/兜底路径。
func TestClassifyCommandArgsRoleLimit(t *testing.T) {
	explain := "slash 命令的包裹形态是 <command-args>参数</command-args> 标签内填参数。"
	// assistant 解释叙述按角色保留进 [AI] 行。
	res := classifyMsg(mkRoleMsg("assistant", explain), nil, nil, nil)
	if res.Class != classKeep || res.Payload != explain {
		t.Errorf("assistant 含标签字面 res.Class=%d res.Payload=%q, want keep/原文", res.Class, res.Payload)
	}
	// system 含标签字面走 system 兜底丢弃。
	res = classifyMsg(mkRoleMsg("system", explain), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("system 含标签字面 res.Class=%d, want classDrop", res.Class)
	}
	// user 角色同形态（非命令壳打头）按真实讨论保留（形态前置回归对照，
	// 见 TestClassifyCommandArgsDiscussion）。
	res = classifyMsg(mkMsg(explain), nil, nil, nil)
	if res.Class != classKeep || res.Payload != explain {
		t.Errorf("user 角色讨论形态 res.Class=%d res.Payload=%q, want keep/原文", res.Class, res.Payload)
	}
}

// TestClassifyTranscriptNestedReplay 回归：嵌套重放的载荷拆分出整条标签行时，
// 按层级配对计数剥离，内层闭标签不提前结束外层区段，外层后续载荷不漏出。
func TestClassifyTranscriptNestedReplay(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("<transcript>\n"),           // 外层开标签
		mkMsg(`{"user":"外层载荷1"}`),         // 外层载荷
		mkMsg("<transcript>\n"),           // 嵌套重放的内层开标签（历史会话自身含重放）
		mkMsg(`{"Bash":"cd /app"}`),       // 内层载荷
		mkMsg("</transcript>\n"),          // 内层闭标签：配对减层，外层区段继续
		mkMsg("Bash grep -n secret /app"), // 外层后续载荷：Bash 打头刻意不收录黑名单，靠层级计数兜住
		mkMsg("</transcript>\n"),          // 外层闭标签
		mkMsg("真实用户指令"),                   // 区段外对照
	}
	wantExcluded := []bool{false, true, false, true, false, true, false, false}
	got := classifySequenceSeq(msgs, nil, nil)
	for i, w := range wantExcluded {
		if got[i].ReplayExcluded != w {
			t.Errorf("第 %d 条 ReplayExcluded=%v, want %v（%q）", i, got[i].ReplayExcluded, w, msgs[i].Text)
		}
	}
	if got[7].Class != classKeep {
		t.Errorf("区段外对照 res.Class=%d, want keep", got[7].Class)
	}
	// 孤立闭标签（无配对开标签）不减穿为负，其后消息不误入剥离。
	stray := classifySequenceSeq([]conversationlog.Message{
		mkMsg("</transcript>\n"),
		mkMsg("孤立闭标签后的真实指令"),
	}, nil, nil)
	if stray[0].ReplayExcluded || stray[1].ReplayExcluded || stray[1].Class != classKeep {
		t.Errorf("孤立闭标签后误剥离：%+v", stray)
	}
}

// TestFingerprintSectionCustomRedact 回归：指纹章节标题脱敏走热调正则集。
// 运维经 extractor.redact_patterns 增补正则后，命中新正则的标题不得原样进
// SpecFingerprints 落库（specs §2.2 SectionList 已脱敏、正则集经系统参数可配）。
func TestFingerprintSectionCustomRedact(t *testing.T) {
	full := "<system-reminder>\n# claudeMd\nCodebase and user instructions are shown below.\n\n## 内部代号-9527 部署节\n\n正文。\n</system-reminder>"
	fp := extractFingerprint(full, []string{`内部代号-\d+`})
	if fp == nil {
		t.Fatal("应产指纹")
	}
	want := []string{"# claudeMd", "## [REDACTED] 部署节"}
	if len(fp.SectionList) != len(want) {
		t.Fatalf("SectionList=%v, want %v", fp.SectionList, want)
	}
	for i, w := range want {
		if fp.SectionList[i] != w {
			t.Errorf("SectionList[%d]=%q, want %q", i, fp.SectionList[i], w)
		}
	}
	// nil 回退出厂集：自定义代号原样保留（出厂集不含该正则），验证两集合互不串扰。
	fpDefault := extractFingerprint(full, nil)
	if fpDefault == nil || fpDefault.SectionList[1] != "## 内部代号-9527 部署节" {
		t.Errorf("nil 回退出厂集应保留原标题，得 %v", fpDefault)
	}
}

// ---- 评审第二轮修复的新行为锁定 ----

// TestClassifyUnknownKind 未知 Kind（上游形态演进，如 thinking 块）进判定链按
// role 兜底处置，防思考全文以 [TOOL] 行进视图污染工具统计与零响应判定
// （specs 判定优先级0 只豁免 tool_use/tool_result 两值）。
func TestClassifyUnknownKind(t *testing.T) {
	msgs := []conversationlog.Message{
		{Role: "assistant", Kind: "thinking", Text: "思考全文：先分析问题再动手"},
		{Role: "tool", Kind: "tool_use", Text: "Edit args=2055"},
	}
	got := classifySequenceSeq(msgs, nil, nil)
	// thinking 块按 assistant 走保留类叙述（不伪造工具行）。
	if got[0].Class != classKeep {
		t.Errorf("thinking 块 Class=%d, want classKeep（按 role 兜底走叙述）", got[0].Class)
	}
	if got[1].Class != classTool {
		t.Errorf("tool_use 行 Class=%d, want classTool", got[1].Class)
	}
	// 工具统计不含思考文本（非 kindToolUse 不进 ToolCounts 由 stats 侧守护，
	// 这里锁定分类层不再把未知 Kind 归工具行）。
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1, msgs, nil))
	if _, ok := p.profile.ToolCounts["思考全文：先分析问题再动手"]; ok {
		t.Errorf("ToolCounts 含思考全文 key: %v", p.profile.ToolCounts)
	}
}

// TestClassifyNoteInterruptProbe Note: 文件回显载体合并打断通知（实测形态）：
// 消息按黑名单丢弃但打断标志透出，InterruptCount 不漏计活打断。
func TestClassifyNoteInterruptProbe(t *testing.T) {
	msg := mkRoleMsg("system", "Note: e:\\proj\\stats.go was modified externally\n<ide_selection>x</ide_selection>\n[Request interrupted by user]")
	got := classifySequenceSeq([]conversationlog.Message{msg}, nil, nil)
	if got[0].Class != classDrop {
		t.Errorf("Note: 载体 Class=%d, want classDrop（黑名单仍丢弃）", got[0].Class)
	}
	if got[0].InterruptCount != 1 {
		t.Error("Note: 载体内嵌打断应透出计数（活打断不随丢弃丢失）")
	}
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1, []conversationlog.Message{msg}, nil))
	if p.profile.InterruptCount != 1 {
		t.Errorf("InterruptCount = %d, want 1（Note: 合并注入的当次打断）", p.profile.InterruptCount)
	}
}

// TestStripInterruptMarkers 打断标记剥离边界：变体后缀闭合括号吞除、
// 未闭合形态窗口外无 ] 保留余文、多次标记逐个剥离。
func TestStripInterruptMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"正文 [Request interrupted by user] 尾部", "正文  尾部"},
		{"叙述 [Request interrupted by user for tool use] 继续", "叙述  继续"},
		{"前 [Request interrupted by user", "前 "}, // 未闭合：标记剥除，余文保留
		{"一 [Request interrupted by user] 二 [Request interrupted by user] 三", "一  二  三"},
		{"[Request interrupted by user]", ""},
	}
	for _, c := range cases {
		if got := stripInterruptMarkers(c.in); got != c.want {
			t.Errorf("stripInterruptMarkers(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestInterruptAssistantNarrativeKept 频繁打断的纯问答会话不误落 empty_shell：
// assistant 叙述嵌打断标记时剥标记保留叙述，hasAI 信号保全（评审 P0-2 侧面一）。
func TestInterruptAssistantNarrativeKept(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("assistant", "经过分析问题出在并发访问，建议方案是……"),
		mkRoleMsg("assistant", "方案二如下……"),
	}
	msgs = append(msgs,
		mkRoleMsg("assistant", "首次尝试 [Request interrupted by user]"),
		mkRoleMsg("assistant", "二次尝试 [Request interrupted by user for tool use]"),
	)
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(2, msgs, nil))
	if !p.hasAIResponse {
		t.Error("打断标记嵌 assistant 叙述时 hasAI 应保全（防误落 empty_shell 终态）")
	}
	if p.profile.InterruptCount != 2 {
		t.Errorf("InterruptCount = %d, want 2", p.profile.InterruptCount)
	}
	// 视图保留叙述行。
	view, _ := newTrimExtractor(&fakeSysParams{}).Trim(mkDetailFull(2, msgs, nil))
	if got := countPrefix(view.Lines, "[AI] "); got != 4 {
		t.Errorf("[AI] 行数 = %d, want 4（叙述与剥后余文都保留）, lines=%v", got, view.Lines)
	}
}

// TestInterruptUserPasteKept 用户正文/粘贴内嵌标记字面（分析会话日志类任务）：
// 指令与粘贴证据保全（评审 P0-2 侧面二）。
func TestInterruptUserPasteKept(t *testing.T) {
	paste := "帮我分析这段会话日志为什么中断\n```\nUser: 跑一下构建\n[Request interrupted by user for tool use]\nAssistant: done\n```"
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1, []conversationlog.Message{mkMsg(paste)}, nil))
	if p.profile.UserMsgCount != 1 {
		t.Errorf("UserMsgCount = %d, want 1（正文保全计入）", p.profile.UserMsgCount)
	}
	if p.profile.PasteCharCount == 0 {
		t.Error("粘贴证据应保全（多行含围栏计粘贴）")
	}
	if p.profile.InterruptCount != 1 {
		t.Errorf("InterruptCount = %d, want 1", p.profile.InterruptCount)
	}
}

// TestRedactWinPathSpaces 定界回填与吞文本防护的 Windows 路径脱敏形态（评审 S3）。
// 路径体排除空白后，带空格目录（Program Files）在中段空格处截断属已知取舍：
// 空白入路径体会把「C:\logs done」的后随正文整段吞进占位符、相邻第二路径截残
// 漏脱敏，宁漏拦不吞正文。
func TestRedactWinPathSpaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{`打开"C:\x\y"看`, `打开"[PATH]"看`},
		{`(见C:\logs\err.log)结束`, `(见[PATH])结束`},
		{`正常句子没有路径`, `正常句子没有路径`},
		{`文件在 D:\a\b 看一下`, `文件在 [PATH] 看一下`},                           // 路径体到汉字终止（尾随空格不进占位符）
		{`安装目录 C:\Program Files\Git\bin`, `安装目录 [PATH] Files\Git\bin`}, // 空格目录截断（申报取舍）
		// 吞文本防护回归：ASCII 尾巴不被吞、相邻第二路径完整命中。
		{`see C:\logs done and fix it`, `see [PATH] done and fix it`},
		{`diff C:\a C:\b`, `diff [PATH] [PATH]`},
	}
	for _, c := range cases {
		if got := Redact(c.in, nil); got != c.want {
			t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSeedMessageKept opencode 会话播种消息（The user has asked you to ...）保留：
// 该前缀已移出黑名单（承载用户创始指令），标题旁路的历史转述形态由阈值与零响应
// 防线兜底（评审裁定）。
func TestSeedMessageKept(t *testing.T) {
	res := classifyMsg(mkMsg("The user has asked you to teach them something"), nil, nil, nil)
	if res.Class != classKeep {
		t.Errorf("播种消息 res.Class=%d, want classKeep（创始指令保留）", res.Class)
	}
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1,
		[]conversationlog.Message{mkMsg("The user has asked you to teach them MBA math")}, nil))
	if p.profile.UserMsgCount != 1 {
		t.Errorf("UserMsgCount = %d, want 1（播种指令计入）", p.profile.UserMsgCount)
	}
}

// TestIDESelectionWithBodyPaste 带随附正文的 IDE 选中文本：正文进视图计用户消息，
// 选中规模保底计入 PasteCharCount（无技术特征串不漏计，评审裁定）。
func TestIDESelectionWithBodyPaste(t *testing.T) {
	msg := mkMsg("<system-reminder>\n<ide_selection>\nSELECT id, name FROM users WHERE age > 18\n</ide_selection>\n帮我把这段查询优化一下\n</system-reminder>")
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1, []conversationlog.Message{msg}, nil))
	if p.profile.UserMsgCount != 1 {
		t.Errorf("UserMsgCount = %d, want 1（随附正文进视图计入）", p.profile.UserMsgCount)
	}
	sel := "SELECT id, name FROM users WHERE age > 18"
	if p.profile.PasteCharCount < utf8.RuneCountInString(sel) {
		t.Errorf("PasteCharCount = %d, want ≥ 选中规模 %d（保底计入）", p.profile.PasteCharCount, utf8.RuneCountInString(sel))
	}
}
