package rules

// openCode 承载 OpenCode 客户端专属规则（specs §2.4 能力8）。
// 前缀来源：基线 config.go injectPrefixesFactory「系统转dump」组的 # Environment
// （环境头是 OpenCode 专属形态，基线分组内重分归属，03 §3 契约裁定 2；
// 同组 # System 归 automation，见 automation.go）。
// The user has asked you to 刻意不入前缀表：该前缀承载 opencode CLI 会话播种消息
// （用户创始指令），收录会让创始指令整条丢失（specs §2.4 能力7 第 1 条裁定保留）。
func init() { Register(openCode{}) }

type openCode struct{}

func (openCode) Client() string { return ClientOpenCode }

// UserPrefixes 返回 OpenCode 贡献的 user 侧注入前缀集（环境头专属形态）。
func (openCode) UserPrefixes() []string {
	return []string{
		// 来源：基线「系统转dump」组（环境头 OpenCode 专属，自基线系统转储组重分）
		"# Environment",
	}
}

// NonUserPrefixes 返回空集：OpenCode 无 assistant 侧收窄规则。
func (openCode) NonUserPrefixes() []string {
	return nil
}

// DetectFeatures 返回签名特征（权重按 specs §2.4 能力7 第 1-2 条标定；
// 播种形态仅作识别信号，不进前缀黑名单）。
func (openCode) DetectFeatures() []DetectFeature {
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "# Environment", Weight: 3, Once: true},
		{Kind: FeatureKindPrefix, Pattern: "The user has asked you to", Weight: 2, Once: true},
	}
}
