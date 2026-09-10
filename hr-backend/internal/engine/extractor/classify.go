package extractor

import (
	"strings"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// 上游消息角色与类型字面（conversationlog 契约），包内判定链统一引用。
const (
	roleUser       = "user"
	roleAssistant  = "assistant"
	roleSystem     = "system"
	roleTool       = "tool"
	kindText       = "text"
	kindToolUse    = "tool_use"
	kindToolResult = "tool_result"
)

// msgClass 是单条 text 消息的处置决策（specs §2.4 能力1 判定优先级）。
type msgClass int

const (
	classDrop        msgClass = iota // 框架噪音丢弃
	classKeep                        // 保留进视图（真实用户输入 / assistant 叙述）
	classEvent                       // 转事件标记行（打断等）
	classExtract                     // 提取通道（command-args / system 插话），携带提取后正文
	classFingerprint                 // 规约指纹（含 SR 包裹的 claudeMd/AGENTS.md/skill_doc）
	classTool                        // 工具元数据行（tool_use/tool_result），原文单行进 [TOOL] 行
)

// classifyResult 是 classifyMsg 的判定产物，字段随 Class 语义取值。
type classifyResult struct {
	Class          msgClass
	Payload        string // classExtract/classKeep 时为进视图正文（IDE 选中文本只含选中文本）
	Fp             *SpecFingerprint
	IsIDESelection bool // IDE 选中文本：只计 PasteCharCount、不计真实用户消息（specs 第三类第 5 条）
	// IDESelectionChars 带随附正文的选中规模保底（未命中粘贴合取时兜住计入）：消息本体
	// 转普通保留类（正文进视图），命中合取时 payload 已含选中块全额计一次，不叠加。
	IDESelectionChars int
	// InterruptCount 承载本消息打断标记的出现次数（判定链剥后口径，统计侧直接
	// 累加，免二次全文检索与口径分裂）；置位后正文归 drop 的消息计数照常透出。
	InterruptCount int
}

// classifiedMsg 是 classifySequence 的逐消息产物：内嵌 classifyResult（嵌入提升
// 使 cm.Class/cm.Payload 等既有引用不变），新增字段只改 classifyResult 一处加
// 单行拷贝，防双 struct 字段集漂移。
type classifiedMsg struct {
	Msg conversationlog.Message
	classifyResult
	ReplayExcluded bool   // transcript 区段内整块剥离（含区段内未命中前缀的漏出形态）
	redacted       string // payload 的 Redact 结果懒缓存：视图侧与统计哈希侧共享同一次全文扫描
	redactedDone   bool
}

// redactedPayload 取（或首算并缓存）payload 的脱敏结果，避免 buildView 与
// computeStats 对同 payload 重复全文正则扫描。须以 slice 元素指针调用，值副本缓存不回写。
func (cm *classifiedMsg) redactedPayload(patterns []string) string {
	if !cm.redactedDone {
		cm.redacted = Redact(cm.Payload, patterns)
		cm.redactedDone = true
	}
	return cm.redacted
}

// 判定链锚点字面量（specs §2.4 能力1 各形态）。
const (
	srTagOpen           = "<system-reminder"
	srTagClose          = "</system-reminder>"
	cmdNameOpen         = "<command-name>"
	cmdNameClose        = "</command-name>"
	cmdArgsOpen         = "<command-args>"
	cmdArgsClose        = "</command-args>"
	baseDirMarker       = "Base directory for this skill"
	interjectMarker     = "The user sent a new message while you were working:"
	interjectTailMarker = "This is how Claude Code surfaces messages" // 插话尾段解释起始（实测形态）
	interruptMarker     = "[Request interrupted by user"              // 不含右括号：覆盖 for tool use 变体
	currentCtxMarker    = "@CurrentContext{{{<startContext>"
	ideSelOpen          = "<ide_selection>"
	ideSelClose         = "</ide_selection>"
	userQueryOpen       = "<user_query>"
	userQueryClose      = "</user_query>"
	transcriptOpen      = "<transcript>"
	transcriptClose     = "</transcript>"
)

// classifyMsg 对单条 text 消息执行判定链：内容语义白名单 → 前缀黑名单 → 特殊形态 →
// role 兜底 → 保留类内嵌 SR 剥离。指纹判定先于噪音兜底，防复合消息内嵌 currentDate
// 段时规约指纹整类误杀（specs §2.4 能力1）；打断计数经命名返回值透出（不随正文丢弃）。
// userPrefixes/nonUserPrefixes 为会话级预计算的黑名单视图（classifySequence 传入，
// 免逐消息重算交集），nil 语义回退出厂。
func classifyMsg(m conversationlog.Message, userPrefixes, nonUserPrefixes, redactPatterns []string) (res classifyResult) {
	trimmed := strings.TrimLeft(m.Text, " \t\r\n")
	// 打断计数置位后随任意 return 路径透出（含 role 兜底归 drop 的消息），
	// defer 在函数出口统一回填。
	interruptHits := 0
	defer func() { res.InterruptCount = interruptHits }()

	// 优先级1a：command 提取通道，仅 user 角色且命令壳打头（<command-name>/
	// <command-message> 两种实测首标签形态），归属 claude_code。含非空 args
	// 内文走提取；无 args 标签或 args 为空的纯壳直接丢弃（无参命令无指令可提，
	// 黑名单只收 <command-name> 前缀，command-message 打头纯壳靠本通道前置兜住）。
	// 正文任意位置含标签字面（如用户讨论 slash 命令写法）不属壳打头，走正常
	// 保留，防提问本体被劫为提取载荷。
	if m.Role == roleUser && isCommandEcho(trimmed) {
		// 命令执行中被打断的形态：框架把打断标记拼进命令回显正文，通道前置于
		// 打断检索，此处补计数；args 内文同步剥标记，防标记字面进指令摘录
		//（与插话通道 strip 后提取同口径）。
		if args := extractTag(m.Text, cmdArgsOpen, cmdArgsClose); strings.TrimSpace(args) != "" {
			if n, _ := countInterrupts(args); n > 0 {
				interruptHits = n
				args = strings.TrimSpace(stripInterruptMarkers(args))
			}
			// 技能名前缀拼回载荷（<command-name> 内文，剥首斜杠）：slash 命令是
			// 用户主动调用技能的直接证据，剥离后评估侧只剩 args 会丢失调用事实。
			payload := args
			if name := strings.TrimSpace(extractTag(m.Text, cmdNameOpen, cmdNameClose)); name != "" {
				payload = "调用技能 " + strings.TrimPrefix(name, "/") + "：" + args
			}
			return classifyResult{Class: classExtract, Payload: payload}
		}
		if n, _ := countInterrupts(m.Text); n > 0 {
			interruptHits = n
		}
		return classifyResult{Class: classDrop}
	}

	// 优先级1b：SR 标签开式命中后按内容语义分流，规约特征产指纹、噪音归 drop
	//（特征白名单在 fingerprint.go 单一来源），通道归属 claude_code（specs §2.4
	// 能力1 第 3 条：归属只影响组织与标注，不影响判定次序）。SR 包裹的
	// <ide_selection> 选中文本仅 user 角色计：system 侧 SR 复合消息多为框架注入，
	// 误记 [USER] 行会污染零输入判定。
	if strings.HasPrefix(trimmed, srTagOpen) {
		// 通道前置打断预检：SR 头部注入与正文带打断的复合形态在 drop 前补计数，
		// 活打断不随框架壳丢弃（与 1a/1.5 同款预检，1b 此前缺位）。
		if n, _ := countInterrupts(m.Text); n > 0 {
			interruptHits = n
		}
		if fp := extractFingerprint(m.Text, redactPatterns); fp != nil {
			return classifyResult{Class: classFingerprint, Fp: fp}
		}
		if m.Role == roleUser {
			if sel, after := srWrappedSelection(m.Text); sel != "" || after != "" {
				return ideSelectionResult(sel, after)
			}
			// SR 闭壳后的 <user_query> 内文提取，归属 workbuddy（WorkBuddy 客户端
			// 实测形态：SR 框架块前置、真实指令在 <user_query> 标签内）：整条 drop
			// 会让该人群用户侧证据全量丢失，提取内文为真实用户输入。
			if q := extractTag(m.Text, userQueryOpen, userQueryClose); strings.TrimSpace(q) != "" {
				return classifyResult{Class: classExtract, Payload: strings.TrimSpace(q)}
			}
		}
		return classifyResult{Class: classDrop}
	}

	// 优先级1c：Base directory 独立命中通道（与前缀并列，不依赖 SR 包裹），
	// 仅 user 角色产 skill_doc 指纹，归属 claude_code。
	if m.Role == roleUser && strings.HasPrefix(trimmed, baseDirMarker) {
		return classifyResult{Class: classFingerprint, Fp: extractFingerprint(m.Text, redactPatterns)}
	}

	// 优先级1.5：Note: 文件回显载体的打断预检（先于黑名单）。实测形态：框架把
	// 当次打断通知与文件修改回显合并注入（Note: 打头、正文内嵌打断子串），直接
	// 走黑名单丢弃会让活打断漏计 InterruptCount。与「黑名单噪音内嵌标记属历史
	// 转储不计」的裁定区分：Note: 回显是当次事件载体。命中黑名单后仍丢弃，
	// 打断计数独立透出供 stats 计数。角色不限：user 侧 Note: 文件回显与 system
	// 侧同载体（防 user 侧同形态漏计的口径不对称）。
	if strings.HasPrefix(trimmed, "Note:") {
		if n, _ := countInterrupts(m.Text); n > 0 {
			interruptHits = n
		}
	}

	// 优先级2：system 插话转述提取，归属 claude_code（内嵌 <ide_opened_file>
	// 通知壳与尾段解释剥离），先于黑名单（specs §3.2 提取类优先于丢弃类）：运维
	// 增补前缀若撞插话 marker，黑名单先拦会让转述的真实指令整条丢失且不可回退
	// 感知。真实插话在用户正文后固定跟解释段、日期变更与文件回显（实测形态），
	// 按尾段锚点截断取首段；打断标记检索在剥离后的首段正文执行（specs 第三类
	// 第 3 条），计数不随降级丢弃。
	if m.Role == roleSystem && strings.HasPrefix(trimmed, interjectMarker) {
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, interjectMarker))
		body = stripTagBlocks(body, "<ide_opened_file>", "</ide_opened_file>")
		body = stripTagBlocks(body, srTagOpen, srTagClose)
		if i := strings.Index(body, interjectTailMarker); i >= 0 {
			body = body[:i]
		}
		body = strings.TrimSpace(body)
		if body == "" {
			return classifyResult{Class: classDrop}
		}
		if strings.Contains(body, interruptMarker) {
			interruptHits = strings.Count(body, interruptMarker)
			body = strings.TrimSpace(stripInterruptMarkers(body))
		}
		return classifyResult{Class: classExtract, Payload: body}
	}

	// 优先级3：前缀黑名单（大小写敏感）。effective 由 classifySequence 会话级
	// 预计算传入（出厂并集 ∪ 追加集，03 §5.1）；此处 nil 分支仅兜直接调用
	// classifyMsg 的既有测试面（等值回退出厂并集）。非 user 角色收窄为 assistant
	// 子集（模型生成物重放/裁决产物/载荷转述），OMO 转写与碎片家族前缀对
	// assistant 叙述的前缀碰撞属误杀面（specs 裁定，见 config.go 注释）。
	// 收窄对追加集同样生效：运维增补的 user 侧转储前缀（如 Note:）若全量应用到
	// assistant，会误杀同前缀真实叙述致会话不可逆落 empty_shell。
	effective := userPrefixes
	if userPrefixes == nil {
		effective = InjectPrefixes()
	}
	if m.Role != roleUser {
		effective = nonUserPrefixes
		if effective == nil {
			effective = assistantNarrowedPrefixes() // narrowedPrefixes 的 nil 回退同款
		}
	}
	for _, p := range effective {
		if strings.HasPrefix(trimmed, p) {
			return classifyResult{Class: classDrop}
		}
	}

	// 优先级4：打断标记处理。标记字面只承载事件语义（计数走 stats 侧子串检索，
	// 事件行由纯标记消息产出），正文不得随标记整条丢弃：剥离标记片段后的余文
	// 按原角色继续判定（assistant 叙述保留防误落 empty_shell、user 正文与粘贴
	// 保全），剥空的纯标记消息降级事件行。检索前先剥内嵌 SR 块，防用户正文尾部
	// appended 块内的框架标记字面误触剥离；Contains 预检在绝大多数消息上成立。
	if strings.Contains(m.Text, interruptMarker) {
		if n, stripped := countInterrupts(m.Text); n > 0 {
			interruptHits = n
			if body := stripInterruptMarkers(stripped); strings.TrimSpace(body) != "" {
				// 余文以剥后正文替代原文继续判定（优先级5 起复用 body 变量）。
				m.Text = body
				trimmed = strings.TrimLeft(body, " \t\r\n")
			} else {
				return classifyResult{Class: classEvent, InterruptCount: n}
			}
		}
	}

	// 优先级5：@CurrentContext 上下文转储整条保留（末尾常内嵌真实指令）。
	// 仅 user 角色计：system/assistant 侧以该前缀打头的转储不属用户输入，
	// 误保留会伪造 [AI] 叙述绕过零响应防线，且 UserMsgCount 口径与之分裂。
	if m.Role == roleUser && strings.HasPrefix(trimmed, currentCtxMarker) {
		return classifyResult{Class: classKeep, Payload: m.Text}
	}

	// 优先级6：role 兜底。system 通道几乎全为框架注入。
	if m.Role == roleSystem {
		return classifyResult{Class: classDrop}
	}
	// tool 角色的 text 变体（上游网关异常透传或形态演进）：归工具证据行而非
	// assistant 叙述，防伪 [AI] 行绕过零响应防线（Kind=tool_* 同归 classTool）。
	if m.Role == roleTool {
		return classifyResult{Class: classTool, Payload: m.Text}
	}

	// 优先级7：保留类 user/text 内嵌 SR 块剥离（未闭合剥到末尾，剥空降级丢弃）。
	// Contains 预检：无 SR 开标签时原文不含 SR 块，剥壳结果即原文，免全文扫描。
	body := m.Text
	if m.Role == roleUser {
		if strings.Contains(body, srTagOpen) {
			body = stripTagBlocks(body, srTagOpen, srTagClose)
		}
		// ide_selection 壳层丢弃，选中文本置布尔计粘贴；要求完整包裹形态
		//（开闭标签同时存在）：裸开标签打头的提问正文（如询问标签写法）被劫持
		// 会让单输入会话误落 empty_shell，未闭合残余也不虚增粘贴规模。随附正文
		// 经 ideSelectionResult 转普通保留类保留（specs：用户真实输入不截不丢）。
		if strings.HasPrefix(strings.TrimLeft(body, " \t\r\n"), ideSelOpen) && strings.Contains(body, ideSelClose) {
			return ideSelectionResult(strings.TrimSpace(extractTag(body, ideSelOpen, ideSelClose)), textAfterTag(body, ideSelClose))
		}
	}
	body = strings.TrimRight(body, "\r\n")
	if strings.TrimSpace(body) == "" {
		return classifyResult{Class: classDrop}
	}
	return classifyResult{Class: classKeep, Payload: body}
}

// extractTag 取首个 open..close 标签内文，未闭合取到文本末尾。
func extractTag(text, open, close string) string {
	i := strings.Index(text, open)
	if i < 0 {
		return ""
	}
	rest := text[i+len(open):]
	if j := strings.Index(rest, close); j >= 0 {
		return rest[:j]
	}
	return rest
}

// isUserInput 是真实用户输入谓词（视图行档与统计口径共享单点）：user 角色保留类，
// 或任意角色的提取通道产物（command-args 与 system 插话转述）。IDE 选中文本
// （IsIDESelection）不在此判定内，由调用方按布尔单独排除（specs §2.4 能力4）。
func (cm *classifiedMsg) isUserInput() bool {
	return cm.Msg.Role == roleUser || cm.Class == classExtract
}

// countInterrupts 剥内嵌 SR 块后在余文检索打断标记（命令壳/Note: 预检/打断主判定
// 三处共用口径），返回出现次数与剥后余文。SR 剥离防正文尾部 appended 块内的框架
// 标记字面误触计数。
func countInterrupts(text string) (n int, stripped string) {
	stripped = stripTagBlocks(text, srTagOpen, srTagClose)
	return strings.Count(stripped, interruptMarker), stripped
}

// stripInterruptMarkers 剥离打断标记片段（含变体后缀与闭合右括号）：marker 刻意
// 不含右括号（覆盖 for tool use 变体），吞掉标记后 closeWindow 字节窗口内的首个
// ]；窗口外无 ] 属未闭合异常形态，标记本身剥除、余文原样保留，防误吞后续无关正文。
func stripInterruptMarkers(text string) string {
	const closeWindow = 32
	var b strings.Builder
	b.Grow(len(text)) // 剥离结果接近原长，预分配免递增扩容拷贝
	rest := text
	for {
		i := strings.Index(rest, interruptMarker)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		after := rest[i+len(interruptMarker):]
		rest = after
		if j := strings.IndexByte(after[:min(len(after), closeWindow)], ']'); j >= 0 {
			rest = after[j+1:]
		}
	}
}

// textAfterTag 取首个 close 标签之后的余文（剥首尾空白），ide_selection 随附
// 正文提取用；无 close 标签返回空串。
func textAfterTag(text, close string) string {
	j := strings.Index(text, close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(text[j+len(close):])
}

// isCommandEcho 判定是否命令回显壳打头（<command-name>/<command-message>，实测两种
// 打头形态），是命令提取通道的形态前置，防用户正文讨论 <command-args> 标签被劫持。
func isCommandEcho(trimmed string) bool {
	return strings.HasPrefix(trimmed, "<command-name>") || strings.HasPrefix(trimmed, "<command-message>")
}

// indexLine 逐行扫描返回首个命中 match 的行首字节偏移，无命中返回 -1。
// indexLinePrefix（行前缀锚定）与 indexClosingFence（独立行全等）共用本骨架。
func indexLine(text string, match func(line string) bool) int {
	from := 0
	for from <= len(text) {
		lineEnd := strings.IndexByte(text[from:], '\n')
		var line string
		if lineEnd < 0 {
			line = text[from:]
		} else {
			line = text[from : from+lineEnd]
		}
		if match(line) {
			return from
		}
		if lineEnd < 0 {
			return -1
		}
		from += lineEnd + 1
	}
	return -1
}

// srWrappedSelection 提取 SR 包裹消息内的 <ide_selection> 选中文本与闭标签后随附
// 正文（SR 尾壳剥离）。未闭合不取到末尾：把 SR 尾壳与框架注入整段计入粘贴规模，
// 宁漏计不虚增；随附正文属真实用户输入，有则消息转普通保留类。
func srWrappedSelection(text string) (sel, after string) {
	i := strings.Index(text, ideSelOpen)
	if i < 0 {
		return "", ""
	}
	rest := text[i+len(ideSelOpen):]
	j := strings.Index(rest, ideSelClose)
	if j < 0 {
		return "", ""
	}
	sel = strings.TrimSpace(rest[:j])
	// SR 尾壳剥两端：正文在壳内（正文\n</system-reminder>）与壳外（</system-reminder>\n正文）
	// 两种实测形态，残留壳会让标签原文混进粘贴与视图；循环剥净多层闭壳（框架嵌套
	// 注入时可见双闭形态，单次 TrimPrefix+TrimSuffix 会残留一层）。
	after = strings.TrimSpace(rest[j+len(ideSelClose):])
	for {
		stripped := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), srTagClose))
		stripped = strings.TrimSpace(strings.TrimSuffix(stripped, srTagClose))
		if stripped == after {
			break
		}
		after = stripped
	}
	return sel, after
}

// ideSelectionResult 组装 ide_selection 命中后的分类结果：纯选中（无随附正文）
// 计粘贴不计用户消息（specs 第三类第 5 条）；带随附正文时消息整体转普通保留类，
// payload 拼接选中块与正文，命中粘贴合取全额计一次，IDESelectionChars 仅未命中时
// 保底（防选中规模双计）；after 内后续 ide_selection 块剥壳并入正文。
func ideSelectionResult(sel, after string) classifyResult {
	after = mergeIDESelections(after)
	if after == "" {
		if sel == "" {
			return classifyResult{Class: classDrop}
		}
		return classifyResult{Class: classKeep, Payload: sel, IsIDESelection: true}
	}
	payload := after
	if sel != "" {
		payload = sel + "\n" + after
	}
	return classifyResult{Class: classKeep, Payload: payload, IDESelectionChars: utf8.RuneCountInString(sel)}
}

// mergeIDESelections 把正文内后续 ide_selection 块的标签壳剥掉、选中文本并入正文
// （首个块由调用方提取，此处只处理残余块），无块时原样返回。
func mergeIDESelections(text string) string {
	for {
		i := strings.Index(text, ideSelOpen)
		if i < 0 {
			return text
		}
		rest := text[i+len(ideSelOpen):]
		j := strings.Index(rest, ideSelClose)
		if j < 0 {
			return text // 未闭合残余不劫持正文，标签原样保留宁漏剥不吞文
		}
		text = text[:i] + strings.TrimSpace(rest[:j]) + " " + rest[j+len(ideSelClose):]
	}
}

// stripTagBlocks 剥离指定标签块，未闭合剥到文本末尾。
func stripTagBlocks(text, open, close string) string {
	var b strings.Builder
	b.Grow(len(text)) // 剥离结果接近原长，预分配免 2x 递增扩容拷贝
	rest := text
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		rest = rest[i:]
		if j := strings.Index(rest, close); j >= 0 {
			rest = rest[j+len(close):]
		} else {
			return b.String()
		}
	}
}

// detectClassifyResult 是 classifySequence 的产物：分类切片加单趟内联探测的
// client 裁决值（探测并入裁剪遍历单趟产出，03 §4 实现约束）。
type detectClassifyResult struct {
	classified []classifiedMsg
	client     string
}

// classifySequence 逐消息驱动判定链并维护 transcript 区段剥离状态机（03 §5.1）：
// 黑名单视图会话级预计算一次，探测计分并入本趟消息遍历单趟产出，DetectClient
// 保持独立函数形态供测试直调。tool_use/tool_result 无条件归 classTool，
// 其余 Kind 按 role 兜底进判定链。
func classifySequence(msgs []conversationlog.Message, prefixes, redactPatterns []string) detectClassifyResult {
	out := make([]classifiedMsg, len(msgs))
	userPrefixes := appendEffectivePrefixes(prefixes)
	nonUserPrefixes := narrowedPrefixes()
	detectPlans := detectPlans()
	detect := newDetectScorer()
	replayDepth := 0
	for i, m := range msgs {
		cm := classifiedMsg{Msg: m}
		if m.Kind == kindToolUse || m.Kind == kindToolResult {
			cm.Class = classTool // 工具元数据行无条件保留，不进判定链
			cm.Payload = m.Text
			out[i] = cm
			continue
		}
		if stripped := strings.TrimSpace(m.Text); stripped == transcriptOpen || stripped == transcriptClose {
			if stripped == transcriptOpen {
				replayDepth++ // 开标签入层，嵌套重放内层标签同样计数
			} else if replayDepth > 0 {
				replayDepth-- // 闭标签配对减层，嵌套载荷的闭标签不提前结束外层
			}
			cm.Class = classDrop
			out[i] = cm
			continue
		}
		// 区段内 text 消息整体剥离（历史会话转述穿透判定链会伪叙述污染零叙述判定
		// 与截断预算）；tool_use/tool_result 行按 specs 无条件保留（上方通道）。
		if replayDepth > 0 {
			cm.ReplayExcluded = true
			cm.Class = classDrop
			out[i] = cm
			continue
		}
		// 探测计分在剥离判定之后：重放载荷对分类侧不可信，对探测同样不可信
		//（内嵌外客户端签名会虚增计分污染 mixed 裁决与 client 列）。
		detect.score(detectPlans, m)
		r := classifyMsg(m, userPrefixes, nonUserPrefixes, redactPatterns)
		cm.classifyResult = r // 内嵌整体赋值，字段集由 struct 定义单点收敛
		out[i] = cm
	}
	return detectClassifyResult{classified: out, client: detect.verdict(detectPlans)}
}

// classifySequenceSeq 是 classifySequence 的切片视图便捷形态：内部测试面大量
// 只消费分类切片，这里解包返回（探测 client 由 classifySequence 主链承载）。
func classifySequenceSeq(msgs []conversationlog.Message, prefixes, redactPatterns []string) []classifiedMsg {
	return classifySequence(msgs, prefixes, redactPatterns).classified
}
