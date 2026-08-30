package extractor

import (
	"fmt"
	"strings"
)

// promptTemplate 是 LLM 段 prompt 模板（specs §2.4 能力2）：长度约束字面值直接写进
// 模板文本，不设独立命名常量；schema 校验侧引用同一字面值 200。
const promptTemplate = `你是会话特征抽取器。基于裁剪后的会话视图与统计参考，产出特征档案三块，只输出一个 JSON 对象，不要输出其他文本。

【产出要求】
1. summary 内容摘要：归纳会话的工作域与技术栈、任务价值信号，200 字以内。
2. instruction 用户指令片段：真实用户输入的代表性摘录（已脱敏），每条不超 200 字；上下文转储、日志粘贴正文等转储类长文本不进摘录。
3. behavior 行为特征：按证据判定以下信号，无证据时标 no_evidence，禁止编造。

【输出 JSON schema】
{"summary": string, "instruction": [{"text": string}], "behavior": {"instruction_specificity": "high" | "medium" | "low" | "no_evidence", "interrupt_style": "frequent" | "occasional" | "rare" | "no_evidence", "review_ratio": "high" | "medium" | "low" | "no_evidence", "paste_scale": "heavy" | "moderate" | "light" | "none", "narrative_absent": bool}}

【行为信号口径】
- instruction_specificity 指令具体度：依据用户指令的明确与细致程度。
- interrupt_style 打断频率：依据打断标记事件行与统计参考的打断次数。
- review_ratio 审查把关类话语占比：依据用户对 AI 产出的复核、纠正、追问类话语占比。
- paste_scale 粘贴日志/代码供分析的规模：依据统计参考的粘贴字符规模。

【零输入会话】裁剪视图无 [USER] 标签行时：instruction 输出空数组 []，禁止编造指令；instruction_specificity 标 no_evidence；summary 与其余信号照常依据工具摘要行与 AI 叙述产出。

【零叙述会话】裁剪视图无 [AI] 标签行时：summary 依据 [TOOL] 工具摘要行（工具名与参数规模）归纳工作域与技术栈；behavior 中 narrative_absent 置 true；review_ratio 与 instruction_specificity 等以叙述或指令为分母的信号一律标 no_evidence，禁止输出数值。`

// buildPrompt 组装裁剪视图喂 LLM 的 prompt（specs §2.4 能力2）：剥离 TokenName 后
// 组装（入参不含人名，人员归属由落库侧回填，防人名进 LLM 上下文）。
func buildPrompt(view TrimmedView, stats ProfileStats) string {
	var b strings.Builder
	// CharCount 是 rune 计数，中文正文 UTF-8 每 rune 3 字节，按 rune 计预分配会
	// 系统性低估触发扩容拷贝，乘 3 按字节量级预估。
	b.Grow(len(promptTemplate) + view.CharCount*3 + 256)
	b.WriteString(promptTemplate)
	b.WriteString("\n\n【统计参考】（规则确定性产出，供行为信号判定）\n")
	fmt.Fprintf(&b, "真实用户消息数：%d\n", stats.UserMsgCount)
	fmt.Fprintf(&b, "打断次数：%d\n", stats.InterruptCount)
	fmt.Fprintf(&b, "粘贴日志/代码字符规模：%d\n", stats.PasteCharCount)
	fmt.Fprintf(&b, "轮次数：%d\n", stats.TurnCount)
	b.WriteString("\n【裁剪视图】\n")
	for _, line := range view.Lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
