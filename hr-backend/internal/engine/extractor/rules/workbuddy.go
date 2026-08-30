package rules

// workBuddy 承载 WorkBuddy 客户端专属规则（specs §2.4 能力8）。
// 前缀来源：基线 config.go injectPrefixesFactory「空回复通知与 WorkBuddy
// 续接模板」组整组归属本客户端（空回复通知、user_query 复合形态壳、
// 续接摘要指令模板）。
func init() { Register(workBuddy{}) }

type workBuddy struct{}

func (workBuddy) Client() string { return ClientWorkBuddy }

// UserPrefixes 返回 WorkBuddy 贡献的 user 侧注入前缀集。
func (workBuddy) UserPrefixes() []string {
	return []string{
		// 来源：基线「空回复通知与 WorkBuddy 续接模板」组
		"[Your previous response had no visible output",
		"<user_query>",
		"Your task is to create a detailed and highly structured summary",
	}
}

// NonUserPrefixes 返回空集：WorkBuddy 无 assistant 侧收窄规则。
func (workBuddy) NonUserPrefixes() []string {
	return nil
}

// DetectFeatures 返回签名特征（三个专属强特征直接映射，权重按
// specs §2.4 能力7 第 1-2 条标定：user_query/team-context 3、user-context 2）。
// SR 闭壳后 user_query 内容提取通道归本客户端，通道实现留 classify.go。
func (workBuddy) DetectFeatures() []DetectFeature {
	return []DetectFeature{
		{Kind: FeatureKindPrefix, Pattern: "<user_query>", Weight: 3, Once: true},
		{Kind: FeatureKindMarker, Pattern: `data-role="team-context"`, Weight: 3, Once: true},
		{Kind: FeatureKindMarker, Pattern: `data-role="user-context"`, Weight: 2, Once: true},
	}
}
