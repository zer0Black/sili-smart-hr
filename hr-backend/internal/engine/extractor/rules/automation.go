package rules

// automation 承载自动化客户端专属规则（specs §2.4 能力8）。
// 前缀来源：基线 config.go injectPrefixesFactory「提示词碎片转储家族」组、
// 「系统转储」组的 # System 与「中文系统提示词转储」的 ## 身份定义。
func init() { Register(automationRules{}) }

type automationRules struct{}

func (automationRules) Client() string { return ClientAutomation }

// UserPrefixes 返回 automation 贡献的 user 侧注入前缀集。
// 碎片家族是自动化客户端的实测形态（整段系统提示词按小节碎片注入）。
func (automationRules) UserPrefixes() []string {
	return []string{
		// 来源：基线「提示词碎片转储家族」组
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
		"<total_tokens>",
		"count",
		// 来源：基线「系统转储」组的 # System 与中文系统提示词转储
		"# System",
		"## 身份定义",
	}
}

// NonUserPrefixes 返回空集：automation 无 assistant 侧收窄规则。
func (automationRules) NonUserPrefixes() []string {
	return nil
}

// DetectFeatures 返回签名特征（specs §2.4 能力7 第 1 条 automation 专属特征）。
// 分钟级会话节奏是 automation 实测形态但刻意不进逐会话探测，
// 仅前缀形态特征参与计分；# System 属共享转储形态，权重 1。
func (automationRules) DetectFeatures() []DetectFeature {
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "# Using your tools", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "## 身份定义", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "# System", Weight: 1, Once: true},
	}
}
