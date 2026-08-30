package rules

// claudeCode 承载 Claude Code 客户端专属规则（specs §2.4 能力8）。
// 前缀来源：基线 config.go injectPrefixesFactory 的壳层与命令输出、探针与压缩摘要
// 请求、system-reminder 复合消息、辅助旁路请求、transcript 重放、权限裁决旁路各组；
// 归属按形态重分（03 §3 契约裁定 2）：
//   - The following skills are available 由基线「文件回显与技能清单」组重分至本客户端
//   - # Environment 基线「系统转储」组形态归 opencode，不入本文件
//   - SR 分流、command 壳、插话等通道归属以 classify.go 注释标注
func init() { Register(claudeCode{}) }

type claudeCode struct{}

func (claudeCode) Client() string { return ClientClaudeCode }

// UserPrefixes 返回 Claude Code 贡献的 user 侧注入前缀集。
func (claudeCode) UserPrefixes() []string {
	return []string{
		// 来源：基线「壳层与命令输出」组（command 壳四件套）
		"<command-name>",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<ide_opened_file>",
		// 来源：基线「system-reminder 复合消息」组（标签开式，命中后按内容语义分流）
		"<system-reminder",
		"Base directory",
		// 来源：基线「文件回显与技能清单」组（技能清单形态重分至本客户端）
		"The following skills are available",
		// 来源：基线「探针与压缩摘要请求」组
		"CRITICAL: Respond with TEXT ONLY",
		"Describe your most recent action",
		"The user stepped away",
		// 来源：基线「插件续跑、system 侧事件通知与任务提醒」组（TodoWrite 与任务提醒归本客户端）
		"The TodoWrite tool",
		"The task tools haven't been used",
		// 来源：基线「辅助旁路请求」组（标题生成 / next-speaker classifier / action summarizer）
		"Generate a title for this conversation",
		"<session>",
		"User request (context):",
		"Analyze *only*",
		"Summarize the tool call input",
		// 来源：基线「transcript 重放」组（头部说明与两类 JSON 载荷）
		"The following is the user's CLAUDE.md configuration",
		`{"user":`,
		`{"Bash":`,
		// 来源：基线「权限裁决旁路与 assistant 侧裁决产物」组
		"Err on the side of blocking",
		"Review the classification process",
		"<block>",
	}
}

// NonUserPrefixes 返回非 user 侧收窄前缀："<block>"（assistant 侧裁决产物）。
// 其余三条收窄前缀（"<analysis>"、ContinuationPrefix、"User: "）归通用层。
func (claudeCode) NonUserPrefixes() []string {
	return []string{"<block>"}
}

// DetectFeatures 返回签名特征（权重按 specs §2.4 能力7 第 1-2 条标定）。
func (claudeCode) DetectFeatures() []DetectFeature {
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "<system-reminder", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "<command-name>", Weight: 2, Once: true},
		{Kind: FeatureKindMarker, Pattern: "The user sent a new message while you were working:", Weight: 2, Once: true},
	}
}
