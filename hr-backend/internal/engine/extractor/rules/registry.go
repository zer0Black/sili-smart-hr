// Package rules 承载会话裁剪的客户端规则分层：每个客户端一个规则文件
// 经 init 调用 Register 挂载，判定链与探测执行期经 AllClientRules 只读消费
// （specs §2.4 能力8）。
package rules

import "sync"

// 客户端标识常量（七值单源，specs §2.3 值域；mixed/unknown 是探测裁决值非规则归属）
const (
	ClientClaudeCode = "claude_code"
	ClientOpenCode   = "opencode"
	ClientWorkBuddy  = "workbuddy"
	ClientOMO        = "omo"
	ClientAutomation = "automation"
	ClientMixed      = "mixed"
	ClientUnknown    = "unknown"
)

// DetectFeature.Kind 的值域（规则文件与探测侧统一引用，禁止裸字面量）
const (
	FeatureKindPrefix = "prefix"
	FeatureKindMarker = "marker"
)

// ClientRules 是客户端规则注册契约：每个客户端一个实现文件，registry 集中注册。
type ClientRules interface {
	// Client 返回客户端标识（七值常量之一的稳定字符串）。
	Client() string
	// UserPrefixes 返回该客户端贡献的 user 侧注入前缀集。
	UserPrefixes() []string
	// NonUserPrefixes 返回非 user 侧收窄前缀集（空集表示无 assistant 侧规则）。
	NonUserPrefixes() []string
	// DetectFeatures 返回客户端签名特征（探测计分用，specs §2.4 能力7）。
	DetectFeatures() []DetectFeature
}

// DetectFeature 是客户端签名的一条特征锚点。
type DetectFeature struct {
	Kind    string // 特征类型：FeatureKindPrefix（消息前缀）/ FeatureKindMarker（子串存在性）
	Pattern string // 字面量
	Weight  int    // 命中计分权重 ≥1；专属强特征 2-3、共享形态 1（specs §2.4 能力7）
	Once    bool   // true 全会话命中一次即计（前缀类）；false 逐消息累计
}

// allRules 是注册表本体：init 期写入、执行期只读（03 §3 契约裁定 1：无反注册
// 与运行时变更，规则集随二进制固定）。锁只防测试运行期 Register 与读并发
//（无锁切片在 -race 下是数据竞态）。
var (
	mu       sync.RWMutex
	allRules []ClientRules
)

// Register 在规则文件的 init 中调用完成挂载；新客户端接入 = 新增规则文件 + 此一行。
func Register(r ClientRules) {
	mu.Lock()
	defer mu.Unlock()
	allRules = append(allRules, r)
}

// AllClientRules 返回全部已注册规则（注册顺序按文件名稳定）；返回拷贝防外部写穿透。
func AllClientRules() []ClientRules {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]ClientRules, len(allRules))
	copy(out, allRules)
	return out
}
