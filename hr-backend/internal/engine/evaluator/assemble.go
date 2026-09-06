package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/extractor"
)

// ProfileSet 档案集分层组装产物（specs §2.3，仅内存临时持有，禁止落日志与缓存）。
type ProfileSet struct {
	SummaryBlock  string   // 第一层周期统计汇总块（规则聚合三态 Stats，全量必进不占预算）
	SessionBlocks []string // 第二层逐会话块（success 按密度降序、预算内截取）
	VisibleCount  int      // 进视图档案数（截断披露分子）
	TotalSuccess  int      // 周期 success 档案总数（截断披露分母）
}

// AssembleProfileSet 分层组装档案集（specs §2.4 能力2）：fetchDigests 取数归一后
// 纯内存组装，纯读不调 LLM 不落库。
func (e *Evaluator) AssembleProfileSet(ctx context.Context, tokenName string, period activity.Period) (*ProfileSet, error) {
	digests, err := fetchDigests(ctx, e.featureRepo, tokenName, period)
	if err != nil {
		return nil, err
	}
	return buildProfileSet(digests), nil
}

// parseSuccessProfile 二次解析 success 行 ProfileJSON（键名与 extractor 落库序列化
// 对齐）。失败或空串按零值返回 false，不报错不 panic（failed 行天然缺 LLM 块）。
func parseSuccessProfile(d activity.ProfileDigest) (extractor.FeatureProfile, bool) {
	if d.ProfileJSON == "" {
		return extractor.FeatureProfile{}, false
	}
	var p extractor.FeatureProfile
	if json.Unmarshal([]byte(d.ProfileJSON), &p) != nil {
		return extractor.FeatureProfile{}, false
	}
	return p, true
}

// density 证据密度（specs §2.4 能力2 确定性规则）：
// UserMsgCount×3 + 工具调用总量×1 + SpecFingerprints 数×2 + PasteCharCount>0?2:0。
func density(d activity.ProfileDigest) int {
	tools := 0
	for _, n := range d.Stats.ToolCounts {
		tools += n
	}
	n := d.Stats.UserMsgCount*3 + tools + len(d.Stats.SpecFingerprints)*2
	if d.Stats.PasteCharCount > 0 {
		n += 2
	}
	return n
}

// buildProfileSet 纯函数核心（测试锚点）：digests 输入直接组装两层结构。
// 第二层按密度降序（同密度按 SessionKey 字典序），累计三块文本字符数
// （session_key 标签与分隔符不计）超 MaxProfileSetChars 时截取预算内前 N 个。
func buildProfileSet(digests []activity.ProfileDigest) *ProfileSet {
	ps := &ProfileSet{SessionBlocks: []string{}}
	ps.SummaryBlock = buildSummaryBlock(digests)

	type cand struct {
		d    activity.ProfileDigest
		text string
		size int // 三块文本字符数（runes），预算计数口径
		dens int // 预计算密度，排序比较器复用
	}
	cands := make([]cand, 0, len(digests))
	for _, d := range digests {
		if d.Status != domain.FeatureStatusSuccess {
			continue // skipped 不进任何层、failed 只进汇总块统计（BR5）
		}
		ps.TotalSuccess++
		p, _ := parseSuccessProfile(d) // 坏 JSON 零值兜底
		behavior, _ := json.Marshal(p.Behavior)
		var b strings.Builder
		size := utf8.RuneCountInString(p.Summary)
		fmt.Fprintf(&b, "session_key: %s\nsummary: %s\n", d.SessionKey, p.Summary)
		for _, seg := range p.Instruction {
			size += utf8.RuneCountInString(seg.Text)
			fmt.Fprintf(&b, "instruction: %s\n", seg.Text)
		}
		size += utf8.RuneCountInString(string(behavior))
		b.WriteString("behavior: ")
		b.Write(behavior)
		cands = append(cands, cand{d: d, text: b.String(), size: size, dens: density(d)})
	}
	slices.SortStableFunc(cands, func(a, b cand) int {
		if a.dens != b.dens {
			return b.dens - a.dens // 密度降序
		}
		return strings.Compare(a.d.SessionKey, b.d.SessionKey) // 同密度 key 字典序
	})
	used := 0
	for _, c := range cands {
		if used+c.size > MaxProfileSetChars {
			break // 截取排序后前缀内预算容纳的 N 个，超出部分整块丢弃
		}
		used += c.size
		ps.SessionBlocks = append(ps.SessionBlocks, c.text)
	}
	ps.VisibleCount = len(ps.SessionBlocks)
	return ps
}

// buildSummaryBlock 汇总块（specs §2.4 能力2 第一层）：对全部三态行 Stats 规则聚合，
// 紧凑 key: value 数字块，无条件全量进 prompt 不参与预算判定。narrative_absent 取
// success 行 Behavior 块二次解析的 absent 标记计数（failed 行天然缺席）。
func buildSummaryBlock(digests []activity.ProfileDigest) string {
	var total, valid, skipped, failed int
	var userMsg, zeroInput, interrupts int
	var pasteChars, pasteMsgs, continuations, absent int
	tools := map[string]int{}
	kinds := map[string]int{}
	turns := map[string]int{}
	specHashes := map[string]bool{}
	sections := map[string]bool{}
	cmdHashes := map[string]bool{}

	for _, d := range digests {
		total++
		switch d.Status {
		case domain.FeatureStatusSuccess:
			valid++
			if p, ok := parseSuccessProfile(d); ok && p.Behavior.NarrativeAbsent {
				absent++
			}
		case domain.FeatureStatusFailed:
			valid++
			failed++
		case domain.FeatureStatusSkipped:
			skipped++
		}
		st := d.Stats
		userMsg += st.UserMsgCount
		if st.UserMsgCount == 0 {
			zeroInput++
		}
		interrupts += st.InterruptCount
		pasteChars += st.PasteCharCount
		if st.PasteCharCount > 0 {
			pasteMsgs++
		}
		if st.ContinuationHit {
			continuations++
		}
		for k, v := range st.ToolCounts {
			tools[k] += v
		}
		for k, v := range st.TurnKindCounts {
			turns[k] += v
		}
		for _, fp := range st.SpecFingerprints {
			kinds[fp.Kind]++
			specHashes[fp.Hash] = true
			for _, s := range fp.SectionList {
				sections[s] = true
			}
		}
		for _, h := range st.CmdReuseHashes {
			cmdHashes[h] = true
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "sessions_total: %d\n", total)
	fmt.Fprintf(&b, "sessions_valid: %d\n", valid)
	fmt.Fprintf(&b, "sessions_skipped: %d\n", skipped)
	fmt.Fprintf(&b, "user_msg_count: %d\n", userMsg)
	fmt.Fprintf(&b, "zero_input_sessions: %d\n", zeroInput)
	fmt.Fprintf(&b, "interrupt_count: %d\n", interrupts)
	fmt.Fprintf(&b, "paste_char_count: %d\n", pasteChars)
	fmt.Fprintf(&b, "paste_msg_sessions: %d\n", pasteMsgs)
	fmt.Fprintf(&b, "tool_top10: %s\n", formatCounts(tools, 10))
	fmt.Fprintf(&b, "turn_kind_counts: %s\n", formatCounts(turns, 0))
	fmt.Fprintf(&b, "spec_fingerprint_kinds: %s\n", formatCounts(kinds, 0))
	fmt.Fprintf(&b, "spec_unique_hashes: %d\n", len(specHashes))
	fmt.Fprintf(&b, "spec_section_titles: %d\n", len(sections))
	fmt.Fprintf(&b, "cmd_reuse_groups: %d\n", len(cmdHashes))
	fmt.Fprintf(&b, "continuation_sessions: %d\n", continuations)
	fmt.Fprintf(&b, "narrative_absent_sessions: %d\n", absent)
	fmt.Fprintf(&b, "failed_profiles: %d\n", failed)
	return b.String()
}

// formatCounts 计数映射渲染为 "name=count" 逗号串：计数降序、同数按名排序，
// topN>0 时截前 N（ToolCounts top 10），0 为全量。
func formatCounts(m map[string]int, topN int) string {
	type kv struct {
		k string
		v int
	}
	entries := make([]kv, 0, len(m))
	for k, v := range m {
		entries = append(entries, kv{k, v})
	}
	slices.SortFunc(entries, func(a, b kv) int {
		if a.v != b.v {
			return b.v - a.v
		}
		return strings.Compare(a.k, b.k)
	})
	if topN > 0 && len(entries) > topN {
		entries = entries[:topN]
	}
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.k + "=" + strconv.Itoa(e.v)
	}
	return strings.Join(parts, ", ")
}
