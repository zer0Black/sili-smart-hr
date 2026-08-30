package extractor

import (
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// 短播报分档阈值：assistant 叙述内部 ≤ 此字符数为短播报，其余为长叙述
// （specs §3.1 截断顺序，口径按叙述正文计、不含行标签）。
const shortNarrativeChars = 200

// 视图行标签（specs §2.3 行格式约定）。
const (
	linePrefixUser  = "[USER] "
	linePrefixAI    = "[AI] "
	linePrefixTool  = "[TOOL] "
	linePrefixEvent = "[EVENT] "
	// eventInterrupt 是打断标记的事件行文案（specs §2.4 能力1 第三类第 3 条）。
	eventInterrupt = "[Request interrupted by user]"
)

// TrimmedView 是裁剪视图（specs §2.3）。行格式：[USER]/[AI]/[TOOL]/[EVENT] 标签
// 打头 + 单空格 + 内容；多行内容首行打标签后续行原样缩进跟随，不转义。
type TrimmedView struct {
	Lines     []string
	CharCount int // 保留行字符数（rune）合计
}

// TrimStats 是裁剪统计（specs §2.3）。
type TrimStats struct {
	TotalMessages   int
	KeptMessages    int
	InjectedDropped int // classDrop 条数（指纹是提取非丢弃，指纹数由 SpecFingerprints 推导）
	Truncated       bool
}

// viewEntry 是进入视图的单条消息（一条消息可展开为多行文本）。
type viewEntry struct {
	lines []string // 首行带标签，多行内容后续行原样跟随
	chars int      // 全部行字符数（rune）合计，含行间换行（与 prompt 逐行补 \n 同口径）
	tier  viewTier
	drop  bool
}

// viewTier 是预算截断档（specs §3.1）：0 短播报、1 工具摘要、2 长叙述；
// -1 用户/事件行不参与预算计数与丢弃。
type viewTier int

const (
	tierKeep  viewTier = -1
	tierShort viewTier = 0
	tierTool  viewTier = 1
	tierLong  viewTier = 2
)

// viewSignals 是规则4 的结构化信号载体：hasAI/hasTool 按条目存在性取值，
// keptBeforeTrunc 是规则3 截断前保留类计数（截断不翻转证据，裁决见 buildView）。
type viewSignals struct {
	hasAI           bool
	hasTool         bool
	keptBeforeTrunc int
}

// Trim 执行裁剪，纯函数不落库不调 LLM（specs §2.4 能力1）。直接复用 prepare，
// 裁剪链（前缀 → 脱敏 → 分类 → 视图）与抽取判定严格同一实现。
// 用户真实输入无条件全文进视图，预算截断只作用于 assistant 叙述与 [TOOL] 行，
// classExtract（command-args 与 system 插话提取）同归真实用户输入。
func (e *Extractor) Trim(detail *conversationlog.SessionDetail) (TrimmedView, TrimStats) {
	if detail == nil {
		return TrimmedView{}, TrimStats{}
	}
	p := e.prepare("", detail)
	return p.view, p.trimStats
}

// buildView 组装裁剪视图并产出结构化信号。截断不翻转证据：empty_shell 是终态
// 跳过误判不可逆，有真实 AI 工作的会话宁可放行由 Stats 兜底。key 与 client
// 仅用于 trim 日志归因（specs §6.1），Trim 独立调用路径分别为空/未知。
func (e *Extractor) buildView(key string, classified []classifiedMsg, redactPatterns []string, client string) (TrimmedView, TrimStats, viewSignals) {
	var stats TrimStats
	sig := viewSignals{}
	stats.TotalMessages = len(classified)

	entries := make([]viewEntry, 0, len(classified))
	for i := range classified {
		cm := &classified[i] // 取指针：脱敏结果懒缓存在 slice 元素上，供 computeStats 复用
		switch cm.Class {
		case classDrop:
			stats.InjectedDropped++ // 含 transcript 区段剥离（ReplayExcluded 同为 classDrop）
		case classFingerprint:
			// 指纹提取进统计块（SpecFingerprints），无视图行；提取非丢弃，
			// 不计入 InjectedDropped（指纹数可由 len(SpecFingerprints) 推导）。
		case classTool:
			// 工具元数据行：原文单行进视图，整体属工具摘要档（specs §3.3 视图文本只见占位符）。
			entries = append(entries, mkEntry(linePrefixTool+cm.redactedPayload(redactPatterns), tierTool))
			sig.hasTool = true
			stats.KeptMessages++
		case classEvent:
			entries = append(entries, mkEntry(linePrefixEvent+eventInterrupt, tierKeep))
			stats.KeptMessages++
		case classKeep, classExtract:
			if cm.isUserInput() {
				// IDE 选中文本壳层丢弃：只计 PasteCharCount 不进视图，进视图会伪造
				// [USER] 行触发零输入指示误判 Instruction 编造（specs 第三类第 5 条）。
				if cm.IsIDESelection {
					break
				}
				// 用户真实输入（含粘贴与提取通道）无条件全文进视图，进视图前过输入侧 Redact。
				payload := cm.redactedPayload(redactPatterns)
				entries = append(entries, mkEntry(linePrefixUser+payload, tierKeep))
				stats.KeptMessages++
			} else { // assistant 叙述：复述密钥/回显路径时同口径脱敏后进 [AI] 行（specs §3.3）。
				payload := cm.redactedPayload(redactPatterns)
				tier := tierLong
				if utf8.RuneCountInString(payload) <= shortNarrativeChars {
					tier = tierShort
				}
				entries = append(entries, mkEntry(linePrefixAI+payload, tier))
				sig.hasAI = true
				stats.KeptMessages++
			}
		}
	}

	// 规则3 计数锚定在截断前：KeptMessages 随后会被截断丢弃回减。
	sig.keptBeforeTrunc = stats.KeptMessages

	if e.truncate(entries) {
		stats.Truncated = true
		slog.Warn("trim budget truncation triggered",
			"total_messages", stats.TotalMessages, "kept_messages", stats.KeptMessages)
	}

	view := TrimmedView{Lines: make([]string, 0, len(entries))}
	for _, en := range entries {
		if en.drop {
			stats.KeptMessages--
			continue
		}
		view.Lines = append(view.Lines, en.lines...)
		view.CharCount += en.chars
	}
	// 裁剪统计 DEBUG 口径（specs §6.1：trim done key=... kept=12/165 chars=7200 client=...）。
	trimAttrs := withClientAttrs([]any{"key", key, "kept", stats.KeptMessages,
		"total", stats.TotalMessages, "chars", view.CharCount, "truncated", stats.Truncated}, client)
	slog.Debug("trim done", trimAttrs...)
	return view, stats, sig
}

// mkEntry 组装单条视图项：首行打标签，多行内容后续行原样缩进跟随（specs §2.3 行格式）。
// chars 含换行（每行一个，与 buildPrompt 逐行补 \n 同口径），防预算系统性低估。
func mkEntry(firstLine string, tier viewTier) viewEntry {
	lines := strings.Split(firstLine, "\n")
	chars := len(lines) // 行间换行按行数计
	for _, l := range lines {
		chars += utf8.RuneCountInString(l)
	}
	return viewEntry{lines: lines, chars: chars, tier: tier}
}

// truncate 按预算丢弃非用户行（specs §3.1）：预算只计非用户内容（用户输入无条件保全，
// 事件行不参与），档间顺序短播报 → 工具摘要 → 长叙述，档内从字符数小的行开始丢，
// 同字符数按出现顺序从后往前丢；触发任一丢弃返回 true。
func (e *Extractor) truncate(entries []viewEntry) bool {
	const budget = MaxSessionChars
	used := 0
	droppable := 0
	for i := range entries {
		if entries[i].tier >= 0 {
			used += entries[i].chars
			droppable++
		}
	}
	if used <= budget || droppable == 0 {
		return false
	}

	truncated := false
	for tier := tierShort; tier <= tierLong && used > budget; tier++ {
		idxs := make([]int, 0, droppable)
		for i := range entries {
			if entries[i].tier == tier && !entries[i].drop {
				idxs = append(idxs, i)
			}
		}
		// 档内字符数升序；同字符数时后出现者在前（等价从后往前丢）。
		sort.SliceStable(idxs, func(a, b int) bool {
			if entries[idxs[a]].chars != entries[idxs[b]].chars {
				return entries[idxs[a]].chars < entries[idxs[b]].chars
			}
			return idxs[a] > idxs[b]
		})
		for _, i := range idxs {
			if used <= budget {
				break
			}
			entries[i].drop = true
			used -= entries[i].chars
			truncated = true
		}
	}
	return truncated
}
