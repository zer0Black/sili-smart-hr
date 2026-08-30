package rules

// omo 承载 OMO 类插件客户端专属规则（specs §2.4 能力8）。
// 前缀来源：基线 config.go injectPrefixesFactory 的「OMO 工具转写家族」、
// 「mcp__ 服务器工具转写」两组，及「插件续跑、system 侧事件通知与任务提醒」
// 组的 OMO 部分（[SYSTEM DIRECTIVE:、[SYSTEM NOTIFICATION、[agent-auto]；
// The TodoWrite tool 与任务提醒两条归 claudecode）。
func init() { Register(omoRules{}) }

type omoRules struct{}

func (omoRules) Client() string { return ClientOMO }

// UserPrefixes 返回 OMO 贡献的 user 侧注入前缀集。
// "TodoWrite " 等带尾空格工具转写形态与 claudecode 的 "The TodoWrite tool"
// 不同前缀，无冲突；Bash 打头有人类真输入歧义刻意不收录（基线裁定）。
func (omoRules) UserPrefixes() []string {
	return []string{
		// 来源：基线「OMO 工具转写家族」组（工具名加空格打头转写）
		"TodoWrite ",
		"Write ",
		"Read ",
		"Edit ",
		"PowerShell ",
		"Grep ",
		"Glob ",
		// 来源：基线「mcp__ 服务器工具转写」组
		"mcp__",
		// 来源：基线「插件续跑、system 侧事件通知与任务提醒」组的 OMO 部分
		"[SYSTEM DIRECTIVE:",
		"[SYSTEM NOTIFICATION",
		"[agent-auto]",
	}
}

// NonUserPrefixes 返回空集：OMO 无 assistant 侧收窄规则。
func (omoRules) NonUserPrefixes() []string {
	return nil
}

// DetectFeatures 返回签名特征（specs §2.4 能力7 第 1 条 omo 专属特征）。
// mcp__ 转写属 OMO 侧高频伴随形态但多客户端 mcp 工具共用，
// 作共享信号不入专属特征集（权重 1 层次留探测侧演进）。
func (omoRules) DetectFeatures() []DetectFeature {
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "[SYSTEM DIRECTIVE:", Weight: 3, Once: true},
		{Kind: FeatureKindMarker, Pattern: "OMO_INTERNAL_INITIATOR", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "TodoWrite ", Weight: 2, Once: true},
	}
}
