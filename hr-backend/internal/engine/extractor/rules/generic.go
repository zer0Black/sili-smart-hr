package rules

// ContinuationPrefix 是 continuation 续接摘要前缀（compaction 续接跨客户端，归通用层）。
// 双源说明：基线在 extractor 包 config.go 定义同名常量，rules 包防环不 import extractor，
// 此处重新声明；02 子计划 T2 收敛为 extractor 引用 rules.ContinuationPrefix 单源。
const ContinuationPrefix = "This session is being continued"

// GenericUserPrefixes 通用层贡献的 user 侧前缀（跨客户端共享形态，specs §2.2 通用规则层）。
func GenericUserPrefixes() []string {
	return []string{
		// 来源：基线 config.go「文件回显与技能清单」组（文件回显跨客户端）
		"Note:",
		"Contents of",
		"Called the",
		// 来源：基线「transcript 重放」组（user 载荷转述跨客户端）
		"User: ",
		// 来源：基线「提示词碎片转 dump 家族」组（清单行跨客户端转储形态）
		"- [",
		// 来源：基线「compaction 压缩输出与 continuation 摘要」组（模型生成物重放跨客户端）
		"<analysis>",
		ContinuationPrefix,
	}
}

// GenericNonUserPrefixes 通用层贡献的非 user 侧收窄前缀："<analysis>"、
// ContinuationPrefix、"User: "（模型生成物重放与 transcript 载荷跨客户端，
// 归通用层）；"<block>" 归 claudecode。收窄并集 == 基线
// assistantPrefixesFactory 四元素（与各客户端 NonUserPrefixes 拆分裁定一致）。
func GenericNonUserPrefixes() []string {
	return []string{
		"<analysis>",       // 模型生成物重放（compaction 压缩输出）
		ContinuationPrefix, // 模型生成物重放（continuation 续接摘要）
		"User: ",           // transcript 载荷 user 侧转述
	}
}
