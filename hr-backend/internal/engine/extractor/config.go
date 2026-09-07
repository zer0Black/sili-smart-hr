package extractor

import (
	"slices"

	"sili-smart-hr/backend/internal/engine/extractor/rules"
)

// 抽取行为常量（specs §2.2），包内定义随源码发版，不入库不入环境变量。
const (
	MinUserMessages  = 1                    // 真实用户消息数阈值：裁剪后 ≥ 此值放行；取 2 会误杀单指令长任务与续接推进会话
	MinKeptMessages  = 10                   // 零真实输入时的证据阈值：保留类消息合计 ≥ 此值放行，低于视为空壳跳过
	MaxSessionTokens = 24000                // 非用户内容 token 预算
	MaxSessionChars  = MaxSessionTokens * 3 // 字符（rune）执行口径（1 token ≈ 3 字符），截断判定按 rune 数累计
)

// ContinuationPrefix 是 continuation 续接摘要前缀，收敛 rules 包单源引用
// （compaction 续接跨客户端，归通用层）。黑名单条目与 stats 的 ContinuationHit
// 观测共用同一常量：双处字面量单边漂移会让黑名单漏拦（压缩转储进视图）
// 或观测恒假（specs §2.4 能力1 形态与第三类第 4 条同源）。
const ContinuationPrefix = rules.ContinuationPrefix

// unionPrefixes 以 map[string]struct{} 收敛多来源前缀并集（specs §2.4 能力1
// 注意事项：不同客户端可贡献相同前缀字面，去重避免重复匹配开销）。
func unionPrefixes(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range groups {
		for _, p := range group {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	return out
}

// InjectPrefixes 返回运行时出厂黑名单的拷贝：通用层前缀 ∪ 全部客户端
// UserPrefixes 并集（去重，03 §5.2 签名不变）。供注册表完备性断言、migrate
// seed 与装配层引用。命中后经内容语义分流，不直接丢弃（见 classify.go）。
func InjectPrefixes() []string {
	groups := [][]string{rules.GenericUserPrefixes()}
	for _, r := range rules.AllClientRules() {
		groups = append(groups, r.UserPrefixes())
	}
	return unionPrefixes(groups...) // unionPrefixes 本身产出新切片，无需二次拷贝
}

// appendEffectivePrefixes 组装运行时生效黑名单（03 §5.1）：出厂并集 ∪ 追加集
// 去重；paramAppend 为 nil 或空时即出厂并集，二者收敛同值（03 §5.3）。
func appendEffectivePrefixes(paramAppend []string) []string {
	userBase := InjectPrefixes()
	if len(paramAppend) == 0 {
		return userBase
	}
	return unionPrefixes(userBase, paramAppend)
}

// assistantNarrowedPrefixes 返回非 user 侧收窄出厂集：全部客户端
// NonUserPrefixes 并集 ∪ 通用层收窄集（去重）。基线四元素形态：模型生成物
// 重放、assistant 侧裁决产物、user 载荷转述前缀。其余形态仅出现于 user 侧
// 转储，对 assistant 叙述的前缀碰撞属误杀面（specs §2.4 能力1 判定优先级 2）。
func assistantNarrowedPrefixes() []string {
	groups := [][]string{rules.GenericNonUserPrefixes()}
	for _, r := range rules.AllClientRules() {
		groups = append(groups, r.NonUserPrefixes())
	}
	return unionPrefixes(groups...)
}

// narrowedPrefixes 返回非 user 侧收窄并集（03 §5.1：非 user 角色按收窄集
// 判定，基线语义不变）。收窄集 ⊆ 出厂并集，追加条目只作用 user 侧（追加的
// user 侧转储前缀若全量应用到 assistant 会误杀同前缀真实叙述致会话不可逆落
// empty_shell），无需与生效集求交。
func narrowedPrefixes() []string {
	return assistantNarrowedPrefixes()
}

// copyStrings 返回切片拷贝（slices.Clone 单点封装）。函数形态防外部写穿透包内
// 出厂集，seed 与读参回退路径各自持独立副本。
func copyStrings(items []string) []string {
	return slices.Clone(items)
}

// 出厂脱敏正则字面量单源：redactPatternsFactory（system_params seed 源）与
// redactDefaults（出厂回退预编译）及 stats.go 粘贴判定的路径正则共同引用，防多处手抄漂移。
const (
	// Windows 绝对路径（盘符开头）：前导捕获组回填防吞闭引号闭括号；路径体排除空白、
	// 中文句读与汉字（中文紧随非路径成分，吞入会让整句进占位符）。取舍：带空格目录
	// （C:\Program Files）在中段空格处截断，属宁漏拦不吞正文的已知盲区，与 Unix 同款。
	redactWinPathPattern = `(^|[^A-Za-z0-9])[A-Za-z]:\\[^\s"'(){}<>,;:\n。？！、，：；（）\p{Han}]+`
	// Unix 绝对路径：定界类（含汉字与中文句读）前导 + ASCII 路径体；`/{2,}` 消费
	// file:/// 前三斜杠之二；双斜杠与 URL 协议头不可分，双斜杠形态不拦属已知取舍。
	redactUnixPathPattern = `(^|[\p{Han}\s="'(),:;>。？！、，：；（）\[\]]|/{2,})/[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)*/?`
	// sk- 密钥：前导捕获组排除词字符与连字符（RE2 无 lookahead，替换串 ${1} 回填），
	// 拦 task-/risk- 等英文连字符词内嵌误杀（specs 有意不设真实长度门槛）。
	redactSKKeyPattern = `(^|[^A-Za-z0-9_-])sk-[A-Za-z0-9_-]{4,}`
	// 内网地址三段合一：首尾捕获组夹住地址、地址体独立成组（组2），防长数字串中段
	// 误切与版本号误判；八位组不校验范围（宽松口径偏安全侧）。相邻地址经
	// replaceGroupKeepDelims 从组末续扫逐个命中；version 10.0.0.1 类字面属已知误杀面
	//（地址漏拦的隐私代价 > 版本号偶发失真的信号代价）。
	redactIntranetPattern = `([^0-9A-Za-z.]|^)((?:10\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3})\.\d{1,3})($|[^0-9A-Za-z.])`
)

// redactPatternsFactory 是脱敏正则集出厂字面量（specs §2.4 能力5），覆盖 Windows/Unix
// 绝对路径、sk- 密钥、内网地址。经系统参数 extractor.redact_patterns 可配。
var redactPatternsFactory = []string{
	redactWinPathPattern,
	redactUnixPathPattern,
	redactSKKeyPattern,
	redactIntranetPattern,
}

// RedactPatterns 返回出厂脱敏正则集的拷贝（语义同 copyStrings 单源说明）。
func RedactPatterns() []string {
	return copyStrings(redactPatternsFactory)
}
