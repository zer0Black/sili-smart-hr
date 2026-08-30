package extractor

// 判定链修复回归（评审第四轮）：WorkBuddy user_query 提取、Note: 全角色打断
// 预检、1b 打断预检、命令壳 args 剥标记、插话先于黑名单、指纹单趟扫描优先级。

import (
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// TestClassifySRUserQueryExtraction WorkBuddy 客户端实测形态：SR 框架块前置、
// 真实指令在 SR 闭壳后的 <user_query> 标签内。整条 drop 会让该人群用户侧证据
// 全量丢失（kept_user=0 但工具行放行，画像失去指令证据），提取内文为真实输入。
func TestClassifySRUserQueryExtraction(t *testing.T) {
	msg := mkMsg("<system-reminder>\ncurrentDate 上下文块\n</system-reminder>\n<user_query>帮我把上一版方案再改一下</user_query>")
	res := classifyMsg(msg, nil, nil, nil)
	if res.Class != classExtract || res.Payload != "帮我把上一版方案再改一下" {
		t.Errorf("SR+user_query res.Class=%d res.Payload=%q, want extract/指令内文", res.Class, res.Payload)
	}
	// 全链路：UserMsgCount 计入（提取通道归真实用户输入）。
	p := newTrimExtractor(&fakeSysParams{}).prepare("", mkDetailFull(1, []conversationlog.Message{msg}, nil))
	if p.profile.UserMsgCount != 1 {
		t.Errorf("UserMsgCount=%d, want 1（user_query 内文是真实用户输入）", p.profile.UserMsgCount)
	}
	// user_query 内文为空（纯框架通知形态）仍归 drop。
	res = classifyMsg(mkMsg("<system-reminder>\nteammate 通知\n</system-reminder>\n<user_query></user_query>"), nil, nil, nil)
	if res.Class != classDrop {
		t.Errorf("空 user_query res.Class=%d, want classDrop", res.Class)
	}
}

// TestClassifyNoteUserInterrupt Note: 文件回显打断预检不限角色：user 侧同载体
// 与 system 侧同口径计数（旧实现只覆盖 system，user 侧被黑名单先拦漏计）。
func TestClassifyNoteUserInterrupt(t *testing.T) {
	for _, role := range []string{"user", "system"} {
		msg := mkRoleMsg(role, "Note: e:\\proj\\a.go was modified externally\n[Request interrupted by user]")
		got := classifySequenceSeq([]conversationlog.Message{msg}, nil, nil)
		if got[0].Class != classDrop {
			t.Errorf("%s Note: Class=%d, want classDrop（黑名单仍丢弃）", role, got[0].Class)
		}
		if got[0].InterruptCount != 1 {
			t.Errorf("%s Note: InterruptCount=%d, want 1（活打断计数不随丢弃丢失）", role, got[0].InterruptCount)
		}
	}
}

// TestClassifySRHeadInterruptPrecheck SR 头部注入 + 正文带打断的复合形态：
// 1b 通道 drop 前补计数（1a 与 1.5 有预检、1b 此前缺位）。
func TestClassifySRHeadInterruptPrecheck(t *testing.T) {
	msg := mkMsg("<system-reminder>\ncurrentDate\n</system-reminder>\n[Request interrupted by user for tool use]")
	got := classifySequenceSeq([]conversationlog.Message{msg}, nil, nil)
	if got[0].Class != classDrop {
		t.Errorf("SR 复合 Class=%d, want classDrop", got[0].Class)
	}
	if got[0].InterruptCount != 1 {
		t.Errorf("SR 复合 InterruptCount=%d, want 1", got[0].InterruptCount)
	}
}

// TestClassifyCommandArgsInterruptStripped 命令壳提取通道的 args 内文同步剥打断
// 标记：标记字面以 [USER] 行进视图会被 LLM 误读为指令内容（与插话通道同口径）。
func TestClassifyCommandArgsInterruptStripped(t *testing.T) {
	msg := "<command-name>/review</command-name>\n<command-args>[Request interrupted by user]</command-args>"
	res := classifyMsg(mkMsg(msg), nil, nil, nil)
	if res.Class != classExtract {
		t.Fatalf("res.Class=%d, want classExtract", res.Class)
	}
	if strings.Contains(res.Payload, "[Request interrupted") {
		t.Errorf("args 内文应剥标记, got %q", res.Payload)
	}
	if res.InterruptCount != 1 {
		t.Errorf("InterruptCount=%d, want 1（打断照常计数）", res.InterruptCount)
	}
}

// TestClassifyInterjectBeforeBlacklist 插话提取先于黑名单（specs §3.2 提取类
// 优先于丢弃类）：运维增补前缀撞插话 marker 时指令证据仍保全。
func TestClassifyInterjectBeforeBlacklist(t *testing.T) {
	cfg := append(InjectPrefixes(), "The user sent")
	msg := mkRoleMsg("system", "The user sent a new message while you were working:\n把方案改成B")
	res := classifyMsg(msg, cfg, nil, nil)
	if res.Class != classExtract || res.Payload != "把方案改成B" {
		t.Errorf("增补前缀撞 marker res.Class=%d res.Payload=%q, want extract/指令保全", res.Class, res.Payload)
	}
}

// TestFingerprintKindSingleScan 指纹类型判定单趟扫描的优先级回归：claudeMd 行
// 在 AGENTS.md 行之后仍优先（全文档维度优先级），不因早停或扫描顺序改判。
func TestFingerprintKindSingleScan(t *testing.T) {
	mixed := "<system-reminder>\nAGENTS.md\n\n# 规约\n\n# claudeMd\n后出现的特征行\n</system-reminder>"
	if got := fingerprintKind(mixed); got != FPClaudeMD {
		t.Errorf("后置 claudeMd 行 kind=%q, want %s（全文档优先级）", got, FPClaudeMD)
	}
	if got := fingerprintKind("AGENTS.md\n正文"); got != FPAgentsMD {
		t.Errorf("AGENTS.md kind=%q, want %s", got, FPAgentsMD)
	}
	if got := fingerprintKind("普通文本"); got != "" {
		t.Errorf("无特征 kind=%q, want 空串", got)
	}
}
