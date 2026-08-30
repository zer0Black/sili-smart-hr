package extractor

// 统计块测试（specs §2.2 字段注释口径、§2.4 能力2/4、§5.1 用例表）。
// 内部测试包：computeStats 为包内私有契约签名。

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// runStats 分类 + 统计两步连跑，返回统计块（测试便捷入口）。
func runStats(t *testing.T, detail *conversationlog.SessionDetail) ProfileStats {
	t.Helper()
	return computeStats(detail, classifySequenceSeq(detail.Messages, nil, nil), 100, nil)
}

// mkToolMsg 构造 tool_use/tool_result 元数据行。
func mkToolMsg(text string) conversationlog.Message {
	return conversationlog.Message{Role: "tool", Kind: "tool_use", Text: text}
}

// mkDetailFull 构造带 Session 元数据与 Turns 的详情。
func mkDetailFull(turnCount int, msgs []conversationlog.Message, turns []conversationlog.TurnMeta) *conversationlog.SessionDetail {
	return &conversationlog.SessionDetail{
		Session:  conversationlog.SessionSummary{TurnCount: turnCount},
		Turns:    turns,
		Messages: msgs,
	}
}

// cmdHash 复算期望指令哈希：规范化（去首尾空白、压缩空白、剥路径）后 SHA-256 前 16 位。
// 复算本体收敛在包内 hash16 单点（fingerprint.go）。
func cmdHash(normalized string) string {
	return hash16(normalized)
}

// TestComputeStats 锚点一：已知会话的完整统计（3 真实用户消息、2 打断、5 工具、粘贴 2 条）。
func TestComputeStats(t *testing.T) {
	paste1 := "报错堆栈：\nat main.go:32 panic\ngoroutine 1 [running]:"
	paste2 := "配置文件内容：\n```yaml\nserver:\n  port: 8080\n```"
	msgs := []conversationlog.Message{
		mkMsg("帮我修复登录超时问题"), // 真实用户消息 1（单行手打）
		mkRoleMsg("assistant", "我先看一下日志。"),
		mkToolMsg("Read args=111"),             // Read ×1
		mkToolMsg("Edit args=2055"),            // Edit ×1
		mkMsg(paste1),                          // 真实用户消息 2 + 粘贴 1（多行含堆栈 trace）
		mkMsg("[Request interrupted by user]"), // 打断 1（独立标记）
		mkMsg("继续，改查 D:\\proj\\src 下的问题"),      // 真实用户消息 3（单行含路径，不计粘贴）
		mkToolMsg("Bash command=ls"),           // Bash ×1
		mkToolMsg("Read args=52"),              // Read ×2
		mkToolMsg("Edit args=88"),              // Edit ×2
		mkRoleMsg("assistant", "已修复，[Request interrupted by user for tool use] 等待确认。"), // 打断 2（嵌 assistant）
		mkMsg(paste2), // 真实用户消息 4?（粘贴 2）——注意此条为粘贴消息同时是真实用户消息
		mkRoleMsg("assistant", "收到配置。"),
	}
	// 第 2 条粘贴走 IDE 选中文本通道：不计真实用户消息但计粘贴（UserMsgCount 锚 3）。
	msgs[11] = mkMsg("<ide_selection>\n```yaml\nserver:\n  port: 8080\n```\n</ide_selection>")
	ideText := "```yaml\nserver:\n  port: 8080\n```"

	turns := []conversationlog.TurnMeta{
		{ID: 1, CreatedAt: 1000, TurnKind: "first"},
		{ID: 2, CreatedAt: 1060, TurnKind: "tool_round"},
		{ID: 3, CreatedAt: 1150, TurnKind: "normal"},
		{ID: 4, CreatedAt: 1300, TurnKind: "tool_round"},
	}
	detail := mkDetailFull(4, msgs, turns)
	st := runStats(t, detail)

	if st.TurnCount != 4 {
		t.Errorf("TurnCount=%d, want 4（列表口径）", st.TurnCount)
	}
	if st.UserMsgCount != 3 {
		t.Errorf("UserMsgCount=%d, want 3（手打1+粘贴1+路径1，IDE 选中文本不计）", st.UserMsgCount)
	}
	if st.InterruptCount != 2 {
		t.Errorf("InterruptCount=%d, want 2（独立1+嵌assistant1）", st.InterruptCount)
	}
	if st.DurationSec != 300 {
		t.Errorf("DurationSec=%d, want 300（末轮1300-首轮1000）", st.DurationSec)
	}
	wantTools := map[string]int{"Read": 2, "Edit": 2, "Bash": 1}
	if !reflect.DeepEqual(st.ToolCounts, wantTools) {
		t.Errorf("ToolCounts=%v, want %v", st.ToolCounts, wantTools)
	}
	wantKinds := map[string]int{"first": 1, "normal": 1, "tool_round": 2}
	if !reflect.DeepEqual(st.TurnKindCounts, wantKinds) {
		t.Errorf("TurnKindCounts=%v, want %v", st.TurnKindCounts, wantKinds)
	}
	if st.TrimmedChars != 100 {
		t.Errorf("TrimmedChars=%d, want 100（入参透传）", st.TrimmedChars)
	}
	if st.PasteCharCount != utf8.RuneCountInString(paste1)+utf8.RuneCountInString(ideText) {
		t.Errorf("PasteCharCount=%d, want %d（粘贴1原文%d + IDE选中文本%d）",
			st.PasteCharCount, utf8.RuneCountInString(paste1)+utf8.RuneCountInString(ideText), utf8.RuneCountInString(paste1), utf8.RuneCountInString(ideText))
	}
	if len(st.SpecFingerprints) != 0 {
		t.Errorf("SpecFingerprints=%v, want 空", st.SpecFingerprints)
	}
	// 指令哈希：3 条真实输入各一（规范化后互不相同）。
	if len(st.CmdReuseHashes) != 3 {
		t.Errorf("CmdReuseHashes=%v, want 3 条", st.CmdReuseHashes)
	}
}

// TestComputeStatsCurrentContext 覆盖 BR5：@CurrentContext 转储计入 UserMsgCount 且整条计粘贴。
func TestComputeStatsCurrentContext(t *testing.T) {
	ctx := "@CurrentContext{{{<startContext>\n{\"tree\":\"组件树\"}\n帮我检查这个组件树"
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(ctx)}, nil))
	if st.UserMsgCount != 1 {
		t.Errorf("UserMsgCount=%d, want 1（@CurrentContext 归保留类计入）", st.UserMsgCount)
	}
	if st.PasteCharCount != utf8.RuneCountInString(ctx) {
		t.Errorf("PasteCharCount=%d, want %d（转储整条计入）", st.PasteCharCount, utf8.RuneCountInString(ctx))
	}
	if len(st.CmdReuseHashes) != 1 {
		t.Errorf("CmdReuseHashes 应含转储末尾指令的 1 条哈希")
	}
}

// TestComputeStatsFingerprints 覆盖 BR（SpecFingerprints）：分类结果指纹清单透传。
func TestComputeStatsFingerprints(t *testing.T) {
	full, _ := mkClaudeMD()
	msgs := []conversationlog.Message{
		mkRoleMsg("system", full), // claudeMd 指纹
		mkMsg("Base directory for this skill: /skills/x\n\n# Skill X\n\n正文"), // skill_doc 指纹
		mkMsg("普通指令"),
	}
	st := runStats(t, mkDetailFull(1, msgs, nil))
	if len(st.SpecFingerprints) != 2 {
		t.Fatalf("SpecFingerprints 数=%d, want 2", len(st.SpecFingerprints))
	}
	if st.SpecFingerprints[0].Kind != FPClaudeMD || st.SpecFingerprints[1].Kind != FPSkillDoc {
		t.Errorf("指纹类型=%s/%s, want %s/%s",
			st.SpecFingerprints[0].Kind, st.SpecFingerprints[1].Kind, FPClaudeMD, FPSkillDoc)
	}
}

// TestComputeStatsContinuation 覆盖 BR6：续接标记消息（被黑名单丢弃）仍置 ContinuationHit。
func TestComputeStatsContinuation(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("This session is being continued from a previous conversation"),
		mkToolMsg("Read args=10"),
	}
	st := runStats(t, mkDetailFull(2, msgs, nil))
	if !st.ContinuationHit {
		t.Error("以 This session is being continued 开头的消息应置 ContinuationHit=true")
	}
	if st.UserMsgCount != 0 {
		t.Errorf("续接摘要按注入丢弃，UserMsgCount=%d, want 0", st.UserMsgCount)
	}
	if len(st.CmdReuseHashes) != 0 {
		t.Errorf("零真实输入会话 CmdReuseHashes 应为空数组，得 %v", st.CmdReuseHashes)
	}
	// 对照组：非前缀开头不误置。
	st = runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg("Another session is being continued here")}, nil))
	if st.ContinuationHit {
		t.Error("非续接前缀不应误置 ContinuationHit")
	}
}

// TestComputeStatsEmpty 覆盖边界：空 map/slice 初始化（JSON 序列化 [] 与 {} 而非 null）。
func TestComputeStatsEmpty(t *testing.T) {
	st := runStats(t, mkDetailFull(0, nil, nil))
	if st.ToolCounts == nil || len(st.ToolCounts) != 0 {
		t.Errorf("ToolCounts 应初始化为空 map，得 %v", st.ToolCounts)
	}
	if st.TurnKindCounts == nil || len(st.TurnKindCounts) != 0 {
		t.Errorf("TurnKindCounts 应初始化为空 map，得 %v", st.TurnKindCounts)
	}
	if st.SpecFingerprints == nil || len(st.SpecFingerprints) != 0 {
		t.Errorf("SpecFingerprints 应初始化为空 slice，得 %v", st.SpecFingerprints)
	}
	if st.CmdReuseHashes == nil || len(st.CmdReuseHashes) != 0 {
		t.Errorf("CmdReuseHashes 应初始化为空 slice，得 %v", st.CmdReuseHashes)
	}
	if st.TurnCount != 0 || st.UserMsgCount != 0 || st.InterruptCount != 0 || st.DurationSec != 0 || st.PasteCharCount != 0 || st.ContinuationHit {
		t.Errorf("空会话统计应为零值，得 %+v", st)
	}
}

// TestComputeStatsEmptyTurns 覆盖边界：零轮与单轮的 DurationSec。
func TestComputeStatsEmptyTurns(t *testing.T) {
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg("问")}, nil))
	if st.DurationSec != 0 {
		t.Errorf("零 Turns 时 DurationSec=%d, want 0", st.DurationSec)
	}
	st = runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg("问")},
		[]conversationlog.TurnMeta{{ID: 1, CreatedAt: 500, TurnKind: "first"}}))
	if st.DurationSec != 0 {
		t.Errorf("单轮 DurationSec=%d, want 0（自差为零）", st.DurationSec)
	}
}

// TestPasteDetection 锚点二：粘贴判定五类 + 语音口语体排除（specs §5.1）。
func TestPasteDetection(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"单行手打指令不计", "帮我修复登录超时问题", false},
		{"单行含技术特征串不计（手打口径）", "看下 D:\\proj\\src\\main.go 的报错", false},
		{"多行粘贴报错计入", "报错堆栈：\nat main.go:32 panic\ngoroutine 1 [running]:", true},
		{"多行含代码围栏计入", "配置如下：\n```yaml\nserver:\n  port: 8080\n```", true},
		{"多行含路径串计入", "两个文件对比：\nE:\\a\\src\\main.go\nE:\\a\\src\\backup.go", true},
		{"多行含Unix路径计入", "日志在：\n/var/log/app/error.log\n/var/log/app/panic.log", true},
		{"多行口语体长文不计（语音转文本）", "那个事情的\n话大概是这样子的\n你先帮我看看\n我下午再过来找你", false},
	}
	for _, c := range cases {
		st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(c.text)}, nil))
		got := st.PasteCharCount > 0
		if got != c.want {
			t.Errorf("%s：PasteCharCount=%d（计粘贴=%v）, want %v（原文 %d 字符）",
				c.name, st.PasteCharCount, got, c.want, utf8.RuneCountInString(c.text))
		}
		if c.want && st.PasteCharCount != utf8.RuneCountInString(c.text) {
			t.Errorf("%s：PasteCharCount=%d, want 整条原文 %d（脱敏前口径）", c.name, st.PasteCharCount, utf8.RuneCountInString(c.text))
		}
	}
	// IDE 选中文本与 @CurrentContext 整条计入（无判定歧义，specs §2.2）。
	ide := "<ide_selection>\nfunc main() {}\n</ide_selection>"
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(ide)}, nil))
	if st.PasteCharCount != utf8.RuneCountInString("func main() {}") {
		t.Errorf("IDE 选中文本 PasteCharCount=%d, want 选中文本 %d", st.PasteCharCount, utf8.RuneCountInString("func main() {}"))
	}
	if st.UserMsgCount != 0 {
		t.Errorf("IDE 选中文本不计真实用户消息，UserMsgCount=%d, want 0", st.UserMsgCount)
	}
	ctx := "@CurrentContext{{{<startContext>\n场景树 JSON\n指令"
	st = runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(ctx)}, nil))
	if st.PasteCharCount != utf8.RuneCountInString(ctx) {
		t.Errorf("@CurrentContext PasteCharCount=%d, want 整条 %d", st.PasteCharCount, utf8.RuneCountInString(ctx))
	}
}

// TestIDESelectionWithBodySingleCount 选中块带随附正文形态的粘贴单次口径：
// 命中粘贴合取时 payload（选中+正文拼接）全额计一次，选中规模保底不叠加
//（双计会让重度 IDE 人群 PasteCharCount 系统性虚增近 2 倍，specs 第三类第 5 条）。
func TestIDESelectionWithBodySingleCount(t *testing.T) {
	sel := "E:\\proj\\src\\main.go:32 panic\nat main.go:33"
	after := "这段代码运行报错了，帮我看看"
	msg := "<system-reminder>\n<ide_selection>\n" + sel + "\n</ide_selection>\n" + after + "\n</system-reminder>"
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(msg)}, nil))

	full := utf8.RuneCountInString(sel + "\n" + after)
	if st.PasteCharCount != full {
		t.Errorf("PasteCharCount=%d, want 拼接全额单次 %d（双计口径会是 %d）",
			st.PasteCharCount, full, full+utf8.RuneCountInString(sel))
	}
	if st.UserMsgCount != 1 {
		t.Errorf("随附正文计真实用户消息，UserMsgCount=%d, want 1", st.UserMsgCount)
	}

	// 未命中合取（选中无技术特征、正文单行口语体）时选中规模保底计入。
	sel2 := "一段无技术特征的选中叙述文本"
	msg2 := "<system-reminder>\n<ide_selection>\n" + sel2 + "\n</ide_selection>\n看看这个\n</system-reminder>"
	st2 := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(msg2)}, nil))
	if st2.PasteCharCount != utf8.RuneCountInString(sel2) {
		t.Errorf("未命中保底 PasteCharCount=%d, want 选中规模 %d", st2.PasteCharCount, utf8.RuneCountInString(sel2))
	}
}

// TestPasteChineseRuneCount 中文口径回归：多行含围栏的中文粘贴按 rune 计数，
// 不按 UTF-8 字节数放大（1 汉字 = 3 字节）。
func TestPasteChineseRuneCount(t *testing.T) {
	paste := "中文粘贴：\n```\n连接池耗尽，请求堆积在队列\n```"
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(paste)}, nil))
	if st.PasteCharCount != utf8.RuneCountInString(paste) {
		t.Errorf("PasteCharCount=%d, want rune 数 %d（非字节数 %d）",
			st.PasteCharCount, utf8.RuneCountInString(paste), len(paste))
	}
	// IDE 中文选中文本同样按 rune 计。
	ide := "<ide_selection>\n// 初始化数据库连接池\n</ide_selection>"
	st = runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(ide)}, nil))
	if st.PasteCharCount != utf8.RuneCountInString("// 初始化数据库连接池") {
		t.Errorf("IDE 中文选中文本 PasteCharCount=%d, want rune 数 %d",
			st.PasteCharCount, utf8.RuneCountInString("// 初始化数据库连接池"))
	}
}

// TestPastePathIndependentFromRedact 回归：粘贴路径判据匹配原文（独立正则），
// 与脱敏配置解耦。运维改写 redact_patterns（输出 [REDACTED] 而非 [PATH]）或仅保留
// 密钥正则后，多行含路径消息仍计入 PasteCharCount（specs §2.2 路径串判据不随配置失效）。
func TestPastePathIndependentFromRedact(t *testing.T) {
	pathPaste := "两个文件对比：\nE:\\a\\src\\main.go\n/var/log/app/error.log"
	// 仅密钥正则的脱敏集：路径原文不替换任何占位符。
	patterns := []string{`sk-[A-Za-z0-9_-]{4,}`}
	ext := newTrimExtractor(&fakeSysParams{values: map[string][]string{
		"extractor.redact_patterns": patterns,
	}})
	p := ext.prepare("", mkDetail([]conversationlog.Message{mkMsg(pathPaste)}))
	if p.profile.PasteCharCount != utf8.RuneCountInString(pathPaste) {
		t.Errorf("改写脱敏集后路径粘贴漏计, PasteCharCount=%d want %d（判据须与脱敏配置解耦）",
			p.profile.PasteCharCount, utf8.RuneCountInString(pathPaste))
	}
	// 单行含路径仍不计（合取口径不回归）。
	p = ext.prepare("", mkDetail([]conversationlog.Message{mkMsg("看下 D:\\proj\\src\\main.go 的报错")}))
	if p.profile.PasteCharCount != 0 {
		t.Errorf("单行路径不计粘贴, got %d", p.profile.PasteCharCount)
	}
}

// TestContinuationPrefixSingleSource 回归：ContinuationHit 观测前缀与黑名单条目
// 同源（config.go ContinuationPrefix 单常量），黑名单命中丢弃的同时观测置位。
func TestContinuationPrefixSingleSource(t *testing.T) {
	joined := "\x00" + strings.Join(InjectPrefixes(), "\x00") + "\x00"
	if !strings.Contains(joined, "\x00"+ContinuationPrefix+"\x00") {
		t.Fatalf("出厂黑名单缺少 ContinuationPrefix 条目，观测与拦截口径将分裂")
	}
	st := runStats(t, mkDetailFull(1, []conversationlog.Message{
		mkMsg(ContinuationPrefix + " from a previous conversation"),
	}, nil))
	if !st.ContinuationHit || st.UserMsgCount != 0 {
		t.Errorf("前缀消息应拦截丢弃且观测置位: hit=%v user=%d", st.ContinuationHit, st.UserMsgCount)
	}
}
func TestCmdReuseHash(t *testing.T) {
	a := "分析 D:\\proj\\src\\main.go 的性能"
	b := "分析  /var/log/app.go  的性能" // 空白差异 + 路径差异
	stA := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(a)}, nil))
	stB := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(b)}, nil))
	if len(stA.CmdReuseHashes) != 1 || len(stB.CmdReuseHashes) != 1 {
		t.Fatalf("两会话各应产 1 条哈希，得 %v / %v", stA.CmdReuseHashes, stB.CmdReuseHashes)
	}
	if stA.CmdReuseHashes[0] != stB.CmdReuseHashes[0] {
		t.Errorf("同义异形指令哈希应相等（规范化口径），得 %q / %q", stA.CmdReuseHashes[0], stB.CmdReuseHashes[0])
	}
	// 期望值复算：脱敏剥占位符后压缩空白（"分析  的性能"→"分析 的性能"）再哈希。
	want := cmdHash("分析 的性能")
	if stA.CmdReuseHashes[0] != want {
		t.Errorf("哈希=%q, want 复算 %q（规范化文本 %q）", stA.CmdReuseHashes[0], want, "分析 的性能")
	}
	// 差异指令哈希不同。
	c := "分析另一个模块的性能"
	stC := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(c)}, nil))
	if stC.CmdReuseHashes[0] == stA.CmdReuseHashes[0] {
		t.Errorf("不同指令哈希应不同")
	}
	// 多空白压缩为单空格后相等（空白维度独立验证）。
	d := "分析   D:\\proj\\src\\main.go   的性能"
	stD := runStats(t, mkDetailFull(1, []conversationlog.Message{mkMsg(d)}, nil))
	if stD.CmdReuseHashes[0] != stA.CmdReuseHashes[0] {
		t.Errorf("仅空白差异的指令哈希应相等，得 %q / %q", stA.CmdReuseHashes[0], stD.CmdReuseHashes[0])
	}
}

// TestInterruptAllRoles 锚点四：打断标记嵌三角色各一次 → InterruptCount==3；
// 嵌入正文剥标记后按原角色保留（user 正文计 UserMsgCount，指令证据不随打断丢失），
// 独立纯标记消息降级事件行不计 UserMsgCount。
func TestInterruptAllRoles(t *testing.T) {
	msgs := []conversationlog.Message{
		mkRoleMsg("user", "开头 [Request interrupted by user] 结尾"),
		mkRoleMsg("assistant", "叙述中嵌 [Request interrupted by user for tool use] 变体"),
		mkRoleMsg("system", "通知内嵌 [Request interrupted by user] 标记"),
	}
	st := runStats(t, mkDetailFull(2, msgs, nil))
	if st.InterruptCount != 3 {
		t.Errorf("InterruptCount=%d, want 3（三角色嵌入各计一）", st.InterruptCount)
	}
	if st.UserMsgCount != 1 {
		t.Errorf("嵌入正文剥标记后保留计 UserMsgCount，得 %d, want 1", st.UserMsgCount)
	}

	// 补充：独立打断消息 + 嵌入混合（specs §5.1 打断标记计数用例）。
	mixed := append(append([]conversationlog.Message{}, msgs...),
		mkMsg("[Request interrupted by user]"), // 独立打断消息（纯标记剥空 → 事件行）
		mkMsg("真实指令"),
	)
	st = runStats(t, mkDetailFull(2, mixed, nil))
	if st.InterruptCount != 4 {
		t.Errorf("混合 InterruptCount=%d, want 4（三角色嵌入3+独立1）", st.InterruptCount)
	}
	if st.UserMsgCount != 2 {
		t.Errorf("混合 UserMsgCount=%d, want 2（嵌入正文1+真实指令1，独立打断不计）", st.UserMsgCount)
	}
	if len(st.CmdReuseHashes) != 2 {
		t.Errorf("CmdReuseHashes 应含嵌入正文与真实指令各 1 条，得 %v", st.CmdReuseHashes)
	}
	// 同一条消息内出现两次标记计二。
	twice := mkMsg("第一次 [Request interrupted by user] 后又 [Request interrupted by user] 一次")
	st2 := runStats(t, mkDetailFull(1, []conversationlog.Message{twice}, nil))
	if st2.InterruptCount != 2 {
		t.Errorf("同消息两次标记 InterruptCount=%d, want 2", st2.InterruptCount)
	}
}

// TestToolCountsExtraction 覆盖工具名提取口径：tool_use 行 Text 首个空格前 token；
// tool_result 元数据不含工具名，跳过不进 ToolCounts（specs §2.2 按工具名计数，
// 防伪工具名 tool_result 让每次调用计两次）。
func TestToolCountsExtraction(t *testing.T) {
	msgs := []conversationlog.Message{
		mkToolMsg("Edit args=2055"),
		conversationlog.Message{Role: "tool", Kind: "tool_result", Text: "tool_result result=223"},
		mkToolMsg("Bash"),
	}
	st := runStats(t, mkDetailFull(1, msgs, nil))
	want := map[string]int{"Edit": 1, "Bash": 1}
	if !reflect.DeepEqual(st.ToolCounts, want) {
		t.Errorf("ToolCounts=%v, want %v（tool_result 不计入）", st.ToolCounts, want)
	}
}

// TestStatsReplayExcludedNotCounted 回归：transcript 重放区段剥离的历史内容不计入
// 本会话行为信号——区段内载荷携带打断标记时 InterruptCount 不被污染（specs §2.2
// InterruptCount 本会话口径、§2.4 能力1 形态9 重放非本人当次输入）。
func TestStatsReplayExcludedNotCounted(t *testing.T) {
	msgs := []conversationlog.Message{
		mkMsg("<transcript>\n"),
		mkMsg("User: 历史会话中嵌 [Request interrupted by user] 的重放载荷"),
		mkMsg("</transcript>\n"),
		mkRoleMsg("assistant", "叙述中嵌 [Request interrupted by user] 标记"),
		mkMsg("真实指令"),
	}
	st := runStats(t, mkDetailFull(2, msgs, nil))
	if st.InterruptCount != 1 {
		t.Errorf("InterruptCount=%d, want 1（仅区段外本会话标记，重放痕迹不计）", st.InterruptCount)
	}
	if st.UserMsgCount != 1 {
		t.Errorf("UserMsgCount=%d, want 1（重放载荷不计真实用户消息）", st.UserMsgCount)
	}
	// 对照：无区段包裹时同形态标记照常计数（全角色子串检索不受影响）。
	scattered := runStats(t, mkDetailFull(1, []conversationlog.Message{
		mkMsg("User: 散装载荷嵌 [Request interrupted by user] 标记"),
	}, nil))
	if scattered.InterruptCount != 0 {
		t.Errorf("散装 User: 载荷被黑名单丢弃，其内嵌标记不是本会话信号，InterruptCount=%d want 0", scattered.InterruptCount)
	}
}

// TestFeatureProfileTypes 类型编译锚点：四块结构与 JSON tag（落库 TEXT 序列化路径）。
func TestFeatureProfileTypes(t *testing.T) {
	p := FeatureProfile{
		Stats: ProfileStats{
			ToolCounts:       map[string]int{},
			TurnKindCounts:   map[string]int{},
			SpecFingerprints: []SpecFingerprint{},
			CmdReuseHashes:   []string{},
		},
		Summary:     "摘要",
		Instruction: []InstructionSeg{{Text: "片段"}},
		Behavior: ProfileBehavior{
			InstructionSpecificity: "high",
			InterruptStyle:         "rare",
			ReviewRatio:            "low",
			PasteScale:             "light",
			NarrativeAbsent:        false,
		},
	}
	if p.Instruction[0].Text != "片段" || p.Behavior.PasteScale != "light" {
		t.Errorf("四块结构字段装配异常：%+v", p)
	}
}
