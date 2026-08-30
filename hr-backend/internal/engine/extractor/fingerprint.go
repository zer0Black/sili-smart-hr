package extractor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// 规约特征标记（specs §2.4 能力1 内容判定归向第 1 条），kind 白名单的语义锚点。
// 匹配收紧到行首级：实测真实 claudeMd 载荷标题行独立（"# claudeMd\nCodebase..."），
// 而 SYSTEM NOTIFICATION 文件清单、CLAUDE.md 回显的正文行中提及 AGENTS.md 会被
// 全文 Contains 误产指纹污染 SpecFingerprints。
const (
	claudeMDMarker = "# claudeMd" // claudeMd 复合消息标题行行首
	agentsMDMarker = "AGENTS.md"  // AGENTS.md 规约标识独立行行首
)

// currentDateHeading 是 claudeMd 复合消息内嵌的日期段标题行（specs §2.2 Hash 注释）。
const currentDateHeading = "# currentDate"

// hash16 取 SHA-256 前 16 位 hex：指纹哈希与指令复用哈希的统一口径单点
// （两消费方口径漂移会让迭代证据与复用证据互不可比）。
func hash16(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// extractFingerprint 从 SR 包裹消息或 Base directory 技能文档提取指纹（specs §2.4
// 能力1 第二类）。kind 按内容语义白名单判定，白名单外返回 nil 归噪音；正文剥 SR 壳
// 与 currentDate 日期段后计字符数与 SHA-256 前 16 位（日期每日漂移，不剥会让迭代
// 证据永久为真）；章节标题逐条过 Redact 脱敏入列，哈希与计数按脱敏前原文计算。
func extractFingerprint(text string, redactPatterns []string) *SpecFingerprint {
	kind := fingerprintKind(text)
	if kind == "" {
		return nil
	}
	body := specBody(text)
	fp := &SpecFingerprint{
		Kind:        kind,
		CharCount:   utf8.RuneCountInString(body),
		Hash:        hash16(body),
		SectionList: []string{}, // 预置空切片：无标题规约序列化为 [] 而非 null，下游免判空
	}
	for _, line := range strings.Split(body, "\n") {
		title, ok := sectionTitle(line)
		if !ok {
			continue
		}
		fp.SectionList = append(fp.SectionList, Redact(title, redactPatterns))
	}
	return fp
}

// fingerprintKind 按内容语义白名单判定指纹类型，优先级 claudeMd → AGENTS.md →
// Base directory（全文档维度：claudeMd 特征行在后仍优先），皆无返回空串归噪音。
// 三标记单趟行扫描：SR 噪音消息普遍较长且三标记皆不命中，逐标记全文扫描会让
// 扫描量放大三倍；命中最高优先级标记即早停。
func fingerprintKind(text string) string {
	seenAgents, seenSkill := false, false
	found := ""
	indexLine(text, func(line string) bool {
		line = strings.TrimLeft(line, " \t\r")
		switch {
		case strings.HasPrefix(line, claudeMDMarker):
			found = FPClaudeMD
			return true // 判定序首位，命中即收敛
		case strings.HasPrefix(line, agentsMDMarker):
			seenAgents = true
		case strings.HasPrefix(line, baseDirMarker):
			seenSkill = true
		}
		return false
	})
	switch {
	case found != "":
		return found
	case seenAgents:
		return FPAgentsMD
	case seenSkill:
		return FPSkillDoc
	}
	return ""
}

// specBody 提取规约正文：剥 SR 开闭壳，按 currentDate 标题行切分取前段，前后空白裁齐。
// 切分锚定行首（与 fingerprintKind 同口径）：子串定位会让规约正文自身含 ## currentDate
// 类标题时从标题中段截断，正文尾部残留孤立 #，哈希与章节清单系统性失真。
func specBody(text string) string {
	body := strings.TrimSpace(text)
	if strings.HasPrefix(body, srTagOpen) {
		if i := strings.Index(body, ">"); i >= 0 {
			body = body[i+1:]
		}
		body = strings.TrimSuffix(body, srTagClose)
	}
	if j := indexLinePrefix(body, currentDateHeading); j >= 0 {
		body = body[:j]
	}
	return strings.TrimSpace(body)
}

// indexLinePrefix 找首个以 prefix 打头的行首偏移（允许行前空白），无则 -1。
func indexLinePrefix(text, prefix string) int {
	return indexLine(text, func(line string) bool {
		return strings.HasPrefix(strings.TrimLeft(line, " \t\r"), prefix)
	})
}

// sectionTitle 判定 markdown 标题行（1-6 个 # 加空格打头），返回 trim 后标题。
func sectionTitle(line string) (string, bool) {
	t := strings.TrimSpace(line)
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(t) && t[n] == ' ' {
		return t, true
	}
	return "", false
}
