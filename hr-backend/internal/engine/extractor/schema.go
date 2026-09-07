package extractor

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// 包内错误载体：ErrSchemaInvalid 供调用方重试一次；ErrLLMUpstream 是 failed 行
// error_code 的兜底错误码；ErrDetailFetch 是详情拉取确定性失败（鉴权/参数/信封
// 契约/部署配置缺失/兜底状态桶）落 failed 行时的 error_code。
var (
	ErrSchemaInvalid = errors.New("extractor: profile schema invalid")
	ErrLLMUpstream   = errors.New("extractor: llm upstream failed")
	ErrDetailFetch   = errors.New("extractor: session detail fetch failed")
)

// failed 行 error_code 落库标识符（04 §3.1 值域：ErrLLMUpstream/ErrSchemaInvalid/
// ErrDetailFetch）。Error() 全串带 extractor: 前缀，直接落库会让 error_code 出现
// 带前缀长句与裸标识符两种形态混存，按错误码聚合时分裂。
const (
	ErrLLMUpstreamCode   = "ErrLLMUpstream"
	ErrSchemaInvalidCode = "ErrSchemaInvalid"
	ErrDetailFetchCode   = "ErrDetailFetch"
)

// lengthLimit 是模板约定的摘要与单条指令片段字数上限（specs §2.2：字面值写在
// promptTemplate 文本内，本文件校验与截断引用本常量，单一来源防漂移）。
const lengthLimit = 200

// llmProfileJSON 是 LLM 输出的三块 JSON 形态（schema 见 promptTemplate）。
type llmProfileJSON struct {
	Summary     string           `json:"summary"`
	Instruction []InstructionSeg `json:"instruction"`
	Behavior    ProfileBehavior  `json:"behavior"`
}

// parseProfile 把 LLM 输出解析为三块并执行 schema 校验（specs §2.4 能力2）。
// 校验失败返回 ErrSchemaInvalid 语义错误，调用方据此重试一次；零输入会话
// （UserMsgCount==0）Instruction 非空判失败防编造；零叙述形态靠 LLM 遵守
// prompt 指示 + narrative_absent 字段透传，校验放行。
func parseProfile(raw string, stats ProfileStats) (string, []InstructionSeg, ProfileBehavior, error) {
	body := StripFences(strings.TrimSpace(raw))
	var p llmProfileJSON
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		// 无围栏输出 JSON 后跟解释文字的形态（stripFences 只覆盖围栏包裹）：
		// 截取首个含 summary 键的顶层平衡 JSON 对象重试一次，防白白消耗 schema
		// 重试并误落 failed。候选须含 summary 键：前导说明文字中的示例对象
		//（{"ok":true} 形态）自身平衡合法，无键约束会被劫持为截取结果。
		if trimmed := firstProfileObject(body); trimmed != "" {
			body = trimmed
			err = json.Unmarshal([]byte(body), &p)
		}
		if err != nil {
			return "", nil, ProfileBehavior{}, ErrSchemaInvalid
		}
	}
	summary := strings.TrimSpace(p.Summary)
	if summary == "" || utf8.RuneCountInString(summary) > lengthLimit*2 {
		return "", nil, ProfileBehavior{}, ErrSchemaInvalid
	}
	if !validBehavior(p.Behavior) {
		return "", nil, ProfileBehavior{}, ErrSchemaInvalid
	}
	if stats.UserMsgCount == 0 && len(p.Instruction) > 0 {
		return "", nil, ProfileBehavior{}, ErrSchemaInvalid
	}
	if p.Instruction == nil {
		p.Instruction = []InstructionSeg{} // null 归一空数组，落库序列化为 []
	}
	for i := range p.Instruction {
		text := strings.TrimSpace(p.Instruction[i].Text)
		if text == "" {
			return "", nil, ProfileBehavior{}, ErrSchemaInvalid
		}
		p.Instruction[i].Text = truncateInstruction(text)
	}
	return summary, p.Instruction, p.Behavior, nil
}

// validBehavior 校验行为特征四字段枚举值域（specs §2.2）：前三字段含 no_evidence，
// paste_scale 值域 heavy/moderate/light/none 无 no_evidence（规模信号以统计口径兜底）。
func validBehavior(b ProfileBehavior) bool {
	return slices.Contains([]string{"high", "medium", "low", "no_evidence"}, b.InstructionSpecificity) &&
		slices.Contains([]string{"frequent", "occasional", "rare", "no_evidence"}, b.InterruptStyle) &&
		slices.Contains([]string{"high", "medium", "low", "no_evidence"}, b.ReviewRatio) &&
		slices.Contains([]string{"heavy", "moderate", "light", "none"}, b.PasteScale)
}

// firstProfileObject 截取首个含 summary 键的顶层平衡 JSON 对象：按括号深度配对
// （字符串字面量内的括号不计数，含转义）逐候选扫描，候选反序列化后含 summary 键
// 才采纳。兜底 LLM 在 JSON 后跟解释文字的形态（json.Unmarshal 拒绝尾随文本）；
// 无键约束的首个平衡对象会被前导说明文字里的示例对象（{"ok":true} 形态）劫持。
func firstProfileObject(s string) string {
	from := 0
	for from < len(s) {
		cand := FirstBalancedJSONObject(s[from:])
		if cand == "" {
			return ""
		}
		var probe llmProfileJSON
		if json.Unmarshal([]byte(cand), &probe) == nil && probe.Summary != "" {
			return cand
		}
		// 候选不含 summary 键（示例对象或不完整候选），跳过其后继续扫。
		from += strings.Index(s[from:], cand) + len(cand)
	}
	return ""
}

// FirstBalancedJSONObject 截取首个顶层平衡 JSON 对象：从首个 { 起按括号深度配对
// （字符串字面量内的括号不计数，含转义），无平衡对象返回空串。
// 导出供 evaluator 复用（宽容解析同源维护）。
func FirstBalancedJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// StripFences 剥 markdown 代码围栏包裹（```json ... ``` 或裸 ``` 围栏）。
// 只剥一层完整包裹，无围栏时原样返回。闭围栏按首个独立行判定（防 instruction
// 摘录内嵌 ``` 被误截断）；独立行缺失时兜底剥尾部紧跟的 ```，剥后以 { 起头
// 才认定包裹形态（单行紧凑/尾同行），防误剥内容尾部本就有的 ```。
// 导出供 evaluator 复用（两子域对同一 LLM 输出形态的容忍度同源维护）。
func StripFences(s string) string {
	const tick = "```"
	if !strings.HasPrefix(s, tick) {
		return s
	}
	rest := s[len(tick):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		// 首行是语言标注才丢弃（```json␊）；首行已含 { 时它是内容行而非标注
		//（```json{␊ 跨行变体、```json{...}```␊ 尾随文本变体），丢弃会切掉 JSON 首段。
		if !strings.Contains(rest[:i], "{") {
			rest = rest[i+1:]
		} else {
			rest = trimLanguagePrefix(rest[:i]) + rest[i:]
		}
	} else {
		// 单行紧凑围栏（```json{...} 语言标注与内容同行）：语言标注段（ASCII 字母）
		// 剥除后再吃掉紧随空白，须余 { 才成立，防误剥以英文单词打头的裸内容，
		// 兼容 ```json␣{ 语言标注后带空格的常见输出形态。
		rest = trimLanguagePrefix(rest)
	}
	if j := indexClosingFence(rest); j >= 0 {
		rest = rest[:j]
	} else {
		// 无独立闭行时兜底剥尾部 ```：优先字符串尾部紧跟（```json{...}``` 单行
		// 紧凑）；否则允许其后跟尾随文本（```json{...}```␊以上形态），但尾随段
		// 须无 JSON 结构字符（{}[]），防 instruction 摘录内嵌 ``` 被误当闭围栏
		// 截断 JSON 主体。剥后以 { 起头才认定包裹形态。
		if k := strings.LastIndex(rest, tick); k > 0 && strings.HasPrefix(rest, "{") &&
			!strings.ContainsAny(rest[k+len(tick):], "{}[]") {
			rest = rest[:k]
		}
	}
	return strings.TrimSpace(rest)
}

// trimLanguagePrefix 剥围栏开头的语言标注段（ASCII 字母加可选空白），标注后须
// 紧跟 { 才认定是语言标注（否则原样返回，防误剥以英文单词打头的裸内容）。
func trimLanguagePrefix(rest string) string {
	i := 0
	for i < len(rest) && rest[i] >= 'a' && rest[i] <= 'z' {
		i++
	}
	if i == 0 {
		return rest // 无语言标注（裸 ``` 围栏）
	}
	after := strings.TrimLeft(rest[i:], " \t")
	if !strings.HasPrefix(after, "{") {
		return rest
	}
	return after
}

// indexClosingFence 找首个独立成行的闭围栏位置，无则 -1。
func indexClosingFence(s string) int {
	return indexLine(s, func(line string) bool {
		return strings.TrimSpace(line) == "```"
	})
}

// truncateInstruction 指令片段兜底截断（specs §2.4 能力2 两档机制第二档）：
// 单条超模板约定上限时在句读边界（中文句读、英文句读、换行）截断并加省略标记，
// 禁止词中硬切；无句读时退空白词边界，连续无界串按上限硬切。上限内原样返回。
func truncateInstruction(text string) string {
	if utf8.RuneCountInString(text) <= lengthLimit {
		return text
	}
	runes := []rune(text)
	cut := -1
	// 候选截断点从 limit-1 起步：isSentenceBreak(runes[i]) 命中时 cut=i+1，i 取
	// limit-1 才可能得到恰达上限的截断，i=limit 时 cut 超限，故排除。
	for i := lengthLimit - 1; i >= 0; i-- {
		if isSentenceBreak(runes[i]) {
			cut = i + 1
			break
		}
	}
	if cut < 0 {
		// 连续无界串（无句读无空白）无边界可落，按上限硬切兜底：规格禁词中硬切的
		// 前提是存在可落边界，此形态截断即截词属规格外极端场景的最后防线。
		cut = lengthLimit
	}
	return string(runes[:cut]) + "..."
}

// isSentenceBreak 判定句读边界：中文句读、英文句读标点或换行。
func isSentenceBreak(r rune) bool {
	switch r {
	case '，', '。', '；', '：', '！', '？', '、', '\n', '\r',
		',', ';', ':', '!', '?', '.', ' ', '\t':
		return true
	}
	return false
}
