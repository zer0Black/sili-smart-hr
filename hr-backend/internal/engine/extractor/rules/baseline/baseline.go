// Package baseline 承载出厂前缀的基线快照锚定集与集合比对工具，仅供
// rules 与 extractor 两包的完备性测试消费：单源硬编码，防两包各自抄写
// 59 条清单漏改一份导致等集断言互相矛盾。快照按字面硬编码（含
// ContinuationPrefix 的字面值），消费方测试锚定其与 rules 常量相等。
package baseline

import "slices"

// UserPrefixes 是基线 extractor.injectPrefixesFactory 的快照锚定集
// （specs §5.1 两视图一致：语义视图硬编码进测试作最终裁判，59 条零重复）。
var UserPrefixes = []string{
	// 壳层与命令输出
	"<command-name>",
	"<local-command-caveat>",
	"<local-command-stdout>",
	"<ide_opened_file>",
	// 探针与压缩摘要请求
	"CRITICAL: Respond with TEXT ONLY",
	"Describe your most recent action",
	"The user stepped away",
	// 文件回显与技能清单
	"Note:",
	"The following skills are available",
	"Contents of",
	"Called the",
	// system-reminder 复合消息与技能文档指纹入口
	"<system-reminder",
	"Base directory",
	// 插件续跑、system 侧事件通知与任务提醒
	"[SYSTEM DIRECTIVE:",
	"[SYSTEM NOTIFICATION",
	"The TodoWrite tool",
	"The task tools haven't been used",
	"[agent-auto]",
	// 辅助旁路请求
	"Generate a title for this conversation",
	"<session>",
	"User request (context):",
	"Analyze *only*",
	"Summarize the tool call input",
	// transcript 重放
	"The following is the user's CLAUDE.md configuration",
	`{"user":`,
	`{"Bash":`,
	"User: ",
	// 权限裁决旁路与 assistant 侧裁决产物
	"Err on the side of blocking",
	"Review the classification process",
	"<block>",
	// 空回复通知与 WorkBuddy 续接模板
	"[Your previous response had no visible output",
	"<user_query>",
	"Your task is to create a detailed and highly structured summary",
	// 系统转储
	"# System",
	"# Environment",
	// OMO 工具转写家族
	"TodoWrite ",
	"Write ",
	"Read ",
	"Edit ",
	"PowerShell ",
	"Grep ",
	"Glob ",
	// mcp__ 服务器工具转写
	"mcp__",
	// 提示词碎片转储家族
	"This is the git status at the start",
	"# Using your tools",
	"# auto memory",
	"# Context management",
	"# Tone and style",
	"# Text output",
	"When you use a pronoun",
	"You are an interactive agent",
	"When you have enough information to act",
	"When referencing files",
	"- [",
	"<total_tokens>",
	"count",
	// 中文系统提示词转储
	"## 身份定义",
	// compaction 压缩输出与 continuation 摘要
	"<analysis>",
	"This session is being continued",
}

// NonUserPrefixes 是基线 extractor.assistantPrefixesFactory 的四元素快照
// （specs §5.1：NonUserPrefixes 并集 == 基线收窄集）。
var NonUserPrefixes = []string{
	"<analysis>",
	"This session is being continued",
	"<block>",
	"User: ",
}

// SortedDedup 返回排序去重拷贝（比对前统一口径）。
func SortedDedup(items []string) []string {
	out := slices.Clone(items)
	slices.Sort(out)
	return slices.Compact(out)
}

// Diff 返回缺失（want 有 got 无）与多出（got 有 want 无）两差集，供失败定位。
func Diff(want, got []string) (missing, extra []string) {
	wantSet := make(map[string]struct{}, len(want))
	for _, p := range want {
		wantSet[p] = struct{}{}
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, p := range got {
		gotSet[p] = struct{}{}
	}
	for _, p := range want {
		if _, ok := gotSet[p]; !ok {
			missing = append(missing, p)
		}
	}
	for _, p := range got {
		if _, ok := wantSet[p]; !ok {
			extra = append(extra, p)
		}
	}
	return missing, extra
}
