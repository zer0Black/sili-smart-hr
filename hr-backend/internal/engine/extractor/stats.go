package extractor

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// ProfileStats 是统计数字块（specs §2.2），规则确定性计算。
type ProfileStats struct {
	TurnCount        int
	UserMsgCount     int
	InterruptCount   int
	DurationSec      int
	TrimmedChars     int
	ToolCounts       map[string]int
	TurnKindCounts   map[string]int
	ContinuationHit  bool
	PasteCharCount   int
	SpecFingerprints []SpecFingerprint
	CmdReuseHashes   []string
}

// 续接标记前缀（config.go 单源常量）与粘贴近似判定的特征正则。路径判据用独立
// 编译期正则（与出厂脱敏正则同源字面量）匹配原文：判据锚在脱敏产物的 [PATH]
// 占位符会把隐私配置（redact_patterns 增删改）与行为统计（PasteCharCount）耦合，
// 运维改写正则后输出 [REDACTED] 会让路径特征静默漏计。
var (
	traceRe         = regexp.MustCompile(`(?i)(goroutine \d+|\[running\]|panic:|Traceback \(most recent call last\))`)
	winPathRe       = regexp.MustCompile(redactWinPathPattern)
	unixPathRe      = regexp.MustCompile(redactUnixPathPattern)
	spaceCollapseRe = regexp.MustCompile(`\s+`)
)

// emptyStatsProfile 是无 detail 场景（ErrDetailFetch 落行）的降级档案，与
// computeStats 空输入路径同源归一（newProfileStats 单点）：序列化输出 {} 与 []
// 而非 null，T5 读 failed 行统计块免判空。
func emptyStatsProfile() *FeatureProfile {
	return &FeatureProfile{Stats: newProfileStats(0)}
}

// newProfileStats 构造归一空值的统计骨架（map/slice 非零值形态），computeStats
// 与 emptyStatsProfile 共用，防两处手工初始化字段集漂移（新增 map/slice 字段
// 漏改一处会让部分 failed 行序列化 null 破坏免判空契约）。
func newProfileStats(trimmedChars int) ProfileStats {
	return ProfileStats{
		TrimmedChars:     trimmedChars,
		ToolCounts:       map[string]int{},
		TurnKindCounts:   map[string]int{},
		SpecFingerprints: []SpecFingerprint{},
		CmdReuseHashes:   []string{},
	}
}

// computeStats 与裁剪同源计算统计块：入参为原始消息序列、分类结果与视图字符数。
// 空 map/slice 初始化为空值，保证 JSON 序列化 [] 与 {} 而非 null。
func computeStats(detail *conversationlog.SessionDetail, classified []classifiedMsg, trimmedChars int, redactPatterns []string) ProfileStats {
	st := newProfileStats(trimmedChars)
	if detail == nil {
		return st
	}
	st.TurnCount = detail.Session.TurnCount
	for _, t := range detail.Turns {
		st.TurnKindCounts[t.TurnKind]++
	}
	// 时长取 turns 时间戳 max-min（turnTimeBounds 单点，跳零值与乱序口径与行时间列一致）：
	// 零值脏时间戳混入会产出 epoch 量级时长污染统计参考。
	if lo, hi, ok := turnTimeBounds(detail.Turns); ok {
		st.DurationSec = int(hi - lo)
	}

	for i := range classified {
		cm := &classified[i] // 取指针：哈希侧复用 buildView 已算的脱敏缓存，免二次全文扫描
		if cm.Class == classFingerprint && cm.Fp != nil {
			st.SpecFingerprints = append(st.SpecFingerprints, *cm.Fp)
		}
		if cm.Class == classTool {
			// tool_use 行按工具名计数（Text 首个空格前 token）；tool_result 元数据
			// 不含工具名，跳过防伪工具名 tool_result 计入。
			if cm.Msg.Kind == kindToolUse {
				st.ToolCounts[toolNameOf(cm.Msg.Text)]++
			}
			continue
		}
		if cm.ReplayExcluded {
			continue // transcript 重放是历史会话转储，其打断/续接痕迹不属本会话信号
		}
		// 续接摘要消息自身按黑名单整块丢弃，观测信号须在 class 过滤前置位，否则恒为假。
		if !st.ContinuationHit && strings.HasPrefix(strings.TrimLeft(cm.Msg.Text, " \t\r\n"), ContinuationPrefix) {
			st.ContinuationHit = true
		}
		// 打断计数由判定链置位（剥 SR 块与通道剥离后的实际口径），先于 class 过滤
		// 累加：剥标记后归 drop 的消息（system 兜底）是本会话真实打断事件，计数
		// 不随正文丢弃。
		st.InterruptCount += cm.InterruptCount
		if cm.Class != classKeep && cm.Class != classExtract && cm.Class != classEvent {
			continue // 黑名单噪音内嵌的标记与判定链结论矛盾，不计入本会话信号
		}
		if cm.Class == classEvent {
			continue // 事件行到此为止：不计真实用户消息
		}
		// IDE 选中文本（IsIDESelection 布尔）：不计真实用户消息，选中文本整条计粘贴。
		payload := cm.Payload
		isUserInput := !cm.IsIDESelection && cm.isUserInput()
		if isUserInput {
			st.UserMsgCount++
			redacted := cm.redactedPayload(redactPatterns)
			st.CmdReuseHashes = append(st.CmdReuseHashes, cmdReuseHash(redacted))
		}
		if cm.IsIDESelection {
			st.PasteCharCount += utf8.RuneCountInString(payload) // IDE 选中文本按布尔整条计入，无判定歧义
		} else if isUserInput && isPasteText(payload) {
			// 带随附正文：payload 已含选中块（ideSelectionResult 拼接），命中合取
			// 全额计入一次，选中规模保底仅在未命中时兜住（specs 整条单次口径，防双计）。
			st.PasteCharCount += utf8.RuneCountInString(payload)
		} else if cm.IDESelectionChars > 0 {
			st.PasteCharCount += cm.IDESelectionChars
		}
	}
	return st
}

// toolNameLenLimit 工具名长度上限：tool_use 元数据行首 token 超此长度视为上游
// 异常透传（正常工具名恒短于 64），截断防原文长串成为 ToolCounts 的 map key 落库。
const toolNameLenLimit = 64

// toolNameOf 取工具行元信息首个空格前 token 作工具名（如 Edit args=2055 → Edit）。
// 超 toolNameLenLimit 截断：上游异常透传的长文本（无空格连续串）不整段进 key；
// 按 rune 边界回退截断（多字节串硬切产无效 UTF-8 键会让计数跨会话不可比）。
func toolNameOf(text string) string {
	if i := strings.IndexByte(text, ' '); i >= 0 {
		text = text[:i]
	}
	if len(text) > toolNameLenLimit {
		cut := toolNameLenLimit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}

// isPasteText 粘贴近似判定（合取）：user 侧消息多行（含换行）且含代码围栏/堆栈
// trace/路径特征之一；@CurrentContext 转储按前缀整条计入；单行文本（含技术特征串）
// 与多行口语体长文不计。路径判据匹配原文（独立正则），与脱敏配置解耦。
func isPasteText(payload string) bool {
	if strings.HasPrefix(strings.TrimLeft(payload, " \t\r\n"), currentCtxMarker) {
		return true
	}
	if !strings.ContainsAny(payload, "\r\n") {
		return false
	}
	return strings.ContainsAny(payload, "\r\n") && (strings.Contains(payload, "```") ||
		traceRe.MatchString(payload) || winPathRe.MatchString(payload) || unixPathRe.MatchString(payload))
}

// cmdReuseHash 指令规范化哈希（specs §2.2 CmdReuseHashes 注释）：入参为已脱敏文本，
// 剥路径占位符（[PATH] 与 [REDACTED] 两种）后压缩空白取 SHA-256 前 16 位
// （hash16 单点，口径与规约指纹统一）。
func cmdReuseHash(redacted string) string {
	red := stripPathPlaceholders(redacted)
	norm := strings.TrimSpace(spaceCollapseRe.ReplaceAllString(red, " "))
	return hash16(norm)
}

// stripPlaceholders 剥离路径类占位符：占位符留下的空隙随后被空白压缩归一。
func stripPathPlaceholders(text string) string {
	text = strings.ReplaceAll(text, redactPlaceholderPath, "")
	return strings.ReplaceAll(text, redactPlaceholderCustom, "")
}
