package extractor

import (
	"strings"

	"sili-smart-hr/backend/internal/engine/extractor/rules"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// clientDetectPlan 是单客户端的探测预编译载荷：DetectFeatures 快照在消息循环外
// 取一次，防循环内每条消息重复触发规则实现的切片分配。
type clientDetectPlan struct {
	client   string
	features []rules.DetectFeature
}

// detectPlans 汇总全部已注册客户端的探测载荷（消息遍历前置）。
func detectPlans() []clientDetectPlan {
	clientRules := rules.AllClientRules()
	plans := make([]clientDetectPlan, len(clientRules))
	for i, r := range clientRules {
		plans[i] = clientDetectPlan{client: r.Client(), features: r.DetectFeatures()}
	}
	return plans
}

// detectFeatureKey 是 Once 特征命中标记键：客户端 + Kind + 字面（全会话计一次；
// 含 Kind 防同字面注册 prefix 与 marker 双特征时跨 Kind 短路）。
type detectFeatureKey struct {
	client  string
	kind    string
	pattern string
}

// detectScorer 是探测计分的单趟状态机：score 逐消息累计，verdict 收口裁决。
// classifySequence 单趟内联复用（03 §4 实现约束），DetectClient 直调路径独立持有。
type detectScorer struct {
	scores  map[string]int
	onceHit map[detectFeatureKey]bool
}

func newDetectScorer() *detectScorer {
	return &detectScorer{
		scores:  make(map[string]int),
		onceHit: make(map[detectFeatureKey]bool),
	}
}

// score 对单条消息跑全部客户端特征计分。tool_use/tool_result 是网关元信息行
//（Text 形如 "TodoWrite args=..."），与判定链 classTool 同口径跳过，
// 防工具转写字面误命中 omo 等前缀特征虚增计分。
func (s *detectScorer) score(plans []clientDetectPlan, m conversationlog.Message) {
	if m.Kind == kindToolUse || m.Kind == kindToolResult {
		return
	}
	trimmed := strings.TrimLeft(m.Text, " \t\r\n")
	for _, p := range plans {
		for _, f := range p.features {
			key := detectFeatureKey{client: p.client, kind: f.Kind, pattern: f.Pattern}
			if f.Once && s.onceHit[key] {
				continue
			}
			var hit bool
			switch f.Kind {
			case rules.FeatureKindPrefix:
				hit = strings.HasPrefix(trimmed, f.Pattern)
			case rules.FeatureKindMarker:
				hit = strings.Contains(m.Text, f.Pattern)
			}
			if !hit {
				continue
			}
			s.scores[p.client] += f.Weight
			if f.Once {
				s.onceHit[key] = true
			}
		}
	}
}

// verdict 裁决：最高分定归属，次高分 > 0 且分差 < 2 判 mixed，全零判 unknown
//（specs §2.4 能力7 第 2 条）。
func (s *detectScorer) verdict(plans []clientDetectPlan) string {
	topClient := ""
	var top, second int
	for _, p := range plans {
		sc := s.scores[p.client]
		if sc > top {
			second = top
			top = sc
			topClient = p.client
		} else if sc > second {
			second = sc
		}
	}
	switch {
	case top == 0:
		return rules.ClientUnknown
	case second > 0 && top-second < 2:
		return rules.ClientMixed
	default:
		return topClient
	}
}

// DetectClient 客户端签名探测（specs §2.4 能力7）：最高分定归属，次高分 > 0
// 且分差 < 2 判 mixed，全零判 unknown。tool_use/tool_result 与 transcript 重放
// 区段同 classifySequence 口径跳过计分。纯函数独立形态供测试直调（03 §4）。
func DetectClient(detail *conversationlog.SessionDetail) string {
	if detail == nil {
		return rules.ClientUnknown
	}
	plans := detectPlans()
	scorer := newDetectScorer()
	replayDepth := 0
	for _, m := range detail.Messages {
		if m.Kind == kindToolUse || m.Kind == kindToolResult {
			continue
		}
		if stripped := strings.TrimSpace(m.Text); stripped == transcriptOpen || stripped == transcriptClose {
			if stripped == transcriptOpen {
				replayDepth++
			} else if replayDepth > 0 {
				replayDepth--
			}
			continue
		}
		if replayDepth > 0 {
			continue
		}
		scorer.score(plans, m)
	}
	return scorer.verdict(plans)
}
