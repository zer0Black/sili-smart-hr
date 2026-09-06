package evaluator

// assemble_test.go 契约测试：档案集分层组装与密度截取（specs §2.4 能力2、§5.1 组装用例锚点）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/extractor"
)

// digestOf 构造测试档案 digest（pj 为完整落库形态 ProfileJSON，可为空）。
func digestOf(key, status string, st extractor.ProfileStats, pj string) activity.ProfileDigest {
	return activity.ProfileDigest{
		SessionKey:  key,
		Status:      status,
		Stats:       st,
		HasBlocks:   status == domain.FeatureStatusSuccess,
		ProfileJSON: pj,
	}
}

// profileJSONOf 按 extractor 落库序列化形态（键名字段名原样）构造 ProfileJSON。
func profileJSONOf(t *testing.T, p extractor.FeatureProfile) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	return string(b)
}

// behaviorZeroJSONLen 零值 Behavior 的紧凑 JSON 长度（块字符计数口径的一部分）。
func behaviorZeroJSONLen(t *testing.T) int {
	t.Helper()
	b, err := json.Marshal(extractor.ProfileBehavior{})
	if err != nil {
		t.Fatalf("marshal behavior: %v", err)
	}
	return len(string(b))
}

// TestDensityRule 密度公式（BR1）：UserMsgCount×3 + 工具总量×1 + 指纹数×2 + PasteCharCount>0?2:0。
func TestDensityRule(t *testing.T) {
	base := extractor.ProfileStats{
		UserMsgCount:      2,
		PasteCharCount:    300,
		ToolCounts:        map[string]int{"Edit": 3, "Read": 2},
		SpecFingerprints:  []extractor.SpecFingerprint{{Kind: "claude_md"}, {Kind: "agents_md"}},
	}
	if got := density(digestOf("k", domain.FeatureStatusSuccess, base, "")); got != 2*3+5+2*2+2 {
		t.Errorf("density(粘贴>0) = %d, want %d", got, 2*3+5+2*2+2)
	}
	noPaste := base
	noPaste.PasteCharCount = 0
	if got := density(digestOf("k", domain.FeatureStatusSuccess, noPaste, "")); got != 2*3+5+2*2 {
		t.Errorf("density(粘贴=0) = %d, want %d", got, 2*3+5+2*2)
	}
}

// TestSummaryBlockAggregation 锚点：三态混合档案（含零输入会话、narrative_absent、
// failed、CmdReuse 同首命中）→ 汇总块各字段具体值。
func TestSummaryBlockAggregation(t *testing.T) {
	ok1Stats := extractor.ProfileStats{
		UserMsgCount:     5,
		InterruptCount:   2,
		PasteCharCount:   300,
		ToolCounts:       map[string]int{"Edit": 3, "Read": 1},
		TurnKindCounts:   map[string]int{"user": 5, "assistant": 4},
		SpecFingerprints: []extractor.SpecFingerprint{
			{Kind: "claude_md", Hash: "aaa", SectionList: []string{"A", "B"}},
			{Kind: "agents_md", Hash: "bbb", SectionList: []string{"C"}},
		},
		CmdReuseHashes:  []string{"h1", "h1", "h2"},
		ContinuationHit: true,
	}
	ok1 := digestOf("ok-1", domain.FeatureStatusSuccess, ok1Stats, profileJSONOf(t, extractor.FeatureProfile{
		Summary:  "调试服务启动问题",
		Behavior: extractor.ProfileBehavior{NarrativeAbsent: true, PasteScale: "none"},
	}))
	// 零输入 success 会话：UserMsgCount==0。
	ok2 := digestOf("ok-2", domain.FeatureStatusSuccess, extractor.ProfileStats{}, profileJSONOf(t, extractor.FeatureProfile{
		Summary: "空输入",
	}))
	// failed 行：仅 Stats 块（LLM 块天然缺席 narrative_absent 计数）。
	fail1 := digestOf("fail-1", domain.FeatureStatusFailed, extractor.ProfileStats{
		UserMsgCount:    3,
		PasteCharCount:  100,
		ContinuationHit: true,
	}, profileJSONOf(t, extractor.FeatureProfile{}))
	// skipped 行：空壳，Stats 零值。
	skip1 := digestOf("skip-1", domain.FeatureStatusSkipped, extractor.ProfileStats{}, "")

	ps := buildProfileSet([]activity.ProfileDigest{ok1, ok2, fail1, skip1})
	want := []string{
		"sessions_total: 4",
		"sessions_valid: 3",
		"sessions_skipped: 1",
		"user_msg_count: 8",
		"zero_input_sessions: 2", // ok-2 与 skip-1（UserMsgCount==0 行数，三态口径）
		"interrupt_count: 2",
		"paste_char_count: 400",
		"paste_msg_sessions: 2",
		"tool_top10: Edit=3, Read=1",
		"turn_kind_counts: user=5, assistant=4",
		"spec_fingerprint_kinds: agents_md=1, claude_md=1",
		"spec_unique_hashes: 2",
		"spec_section_titles: 3",
		"cmd_reuse_groups: 2", // h1/h2 去重（同首命中）
		"continuation_sessions: 2",
		"narrative_absent_sessions: 1",
		"failed_profiles: 1",
	}
	for _, w := range want {
		if !strings.Contains(ps.SummaryBlock, w) {
			t.Errorf("SummaryBlock 缺少 %q:\n%s", w, ps.SummaryBlock)
		}
	}
	if ps.TotalSuccess != 2 {
		t.Errorf("TotalSuccess = %d, want 2", ps.TotalSuccess)
	}
}

// TestDensityOrderAndTruncation 锚点：200 个 success 档案（单档案 600 字符三块）
// → 截取 150 个，VisibleCount=150、TotalSuccess=200，首块为密度最高档案。
func TestDensityOrderAndTruncation(t *testing.T) {
	behLen := behaviorZeroJSONLen(t)
	summaryLen := 600 - behLen // 三块合计 600 字符（无指令段）
	digests := make([]activity.ProfileDigest, 0, 200)
	for i := 1; i <= 200; i++ {
		st := extractor.ProfileStats{UserMsgCount: i} // density=3i 互异
		pj := profileJSONOf(t, extractor.FeatureProfile{Summary: strings.Repeat("x", summaryLen)})
		digests = append(digests, digestOf(fmt.Sprintf("s-%03d", i), domain.FeatureStatusSuccess, st, pj))
	}
	ps := buildProfileSet(digests)
	if len(ps.SessionBlocks) != 150 {
		t.Fatalf("SessionBlocks 条数 = %d, want 150（90000/600）", len(ps.SessionBlocks))
	}
	if ps.VisibleCount != 150 || ps.TotalSuccess != 200 {
		t.Errorf("Visible=%d Total=%d, want 150/200", ps.VisibleCount, ps.TotalSuccess)
	}
	if !strings.Contains(ps.SessionBlocks[0], "session_key: s-200") {
		t.Errorf("首块应为密度最高档案 s-200, got:\n%s", ps.SessionBlocks[0])
	}
	if !strings.Contains(ps.SessionBlocks[149], "session_key: s-051") {
		t.Errorf("末块应为 s-051, got:\n%s", ps.SessionBlocks[149])
	}
}

// TestDensityTieStableOrder 锚点：两档案同密度不同 key → key 字典序在前者先出。
func TestDensityTieStableOrder(t *testing.T) {
	mk := func(key string) activity.ProfileDigest {
		return digestOf(key, domain.FeatureStatusSuccess,
			extractor.ProfileStats{UserMsgCount: 1}, profileJSONOf(t, extractor.FeatureProfile{Summary: "同密度"}))
	}
	ps := buildProfileSet([]activity.ProfileDigest{mk("key-b"), mk("key-a")})
	if len(ps.SessionBlocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(ps.SessionBlocks))
	}
	if !strings.Contains(ps.SessionBlocks[0], "session_key: key-a") || !strings.Contains(ps.SessionBlocks[1], "session_key: key-b") {
		t.Errorf("同密度应按 key 字典序稳定排序:\n%s\n%s", ps.SessionBlocks[0], ps.SessionBlocks[1])
	}
}

// TestBudgetNotTriggered 锚点：10 个档案合计远低预算 → 无截取。
func TestBudgetNotTriggered(t *testing.T) {
	digests := make([]activity.ProfileDigest, 0, 10)
	for i := 0; i < 10; i++ {
		digests = append(digests, digestOf(fmt.Sprintf("k-%d", i), domain.FeatureStatusSuccess,
			extractor.ProfileStats{UserMsgCount: 1}, profileJSONOf(t, extractor.FeatureProfile{Summary: "短档案"})))
	}
	ps := buildProfileSet(digests)
	if ps.VisibleCount != 10 || ps.TotalSuccess != 10 || len(ps.SessionBlocks) != 10 {
		t.Errorf("Visible=%d Total=%d blocks=%d, want 全量 10", ps.VisibleCount, ps.TotalSuccess, len(ps.SessionBlocks))
	}
}

// TestSkippedFailedExcluded 锚点：混合三态输入 → 第二层只含 success 行（BR5）。
func TestSkippedFailedExcluded(t *testing.T) {
	digests := []activity.ProfileDigest{
		digestOf("ok-1", domain.FeatureStatusSuccess, extractor.ProfileStats{UserMsgCount: 1},
			profileJSONOf(t, extractor.FeatureProfile{Summary: "可见"})),
		digestOf("fail-1", domain.FeatureStatusFailed, extractor.ProfileStats{UserMsgCount: 1},
			profileJSONOf(t, extractor.FeatureProfile{Summary: "不可见"})),
		digestOf("skip-1", domain.FeatureStatusSkipped, extractor.ProfileStats{}, ""),
	}
	ps := buildProfileSet(digests)
	if len(ps.SessionBlocks) != 1 {
		t.Fatalf("blocks = %d, want 1（仅 success）", len(ps.SessionBlocks))
	}
	if !strings.Contains(ps.SessionBlocks[0], "session_key: ok-1") {
		t.Errorf("唯一块应为 ok-1:\n%s", ps.SessionBlocks[0])
	}
	if strings.Contains(ps.SessionBlocks[0], "不可见") {
		t.Errorf("failed 行三块文本不应进第二层")
	}
	// failed/skipped 只进汇总块统计。
	if !strings.Contains(ps.SummaryBlock, "failed_profiles: 1") || !strings.Contains(ps.SummaryBlock, "sessions_skipped: 1") {
		t.Errorf("汇总块应统计 failed/skipped:\n%s", ps.SummaryBlock)
	}
}

// TestBuildProfileSetEmpty 空输入边界：空集不 panic、计零、blocks 非 nil。
func TestBuildProfileSetEmpty(t *testing.T) {
	ps := buildProfileSet(nil)
	if ps == nil {
		t.Fatal("buildProfileSet(nil) 返回 nil")
	}
	if ps.VisibleCount != 0 || ps.TotalSuccess != 0 || len(ps.SessionBlocks) != 0 {
		t.Errorf("空集计数值非零: %+v", ps)
	}
	if ps.SessionBlocks == nil {
		t.Error("SessionBlocks 应为空切片非 nil")
	}
	if !strings.Contains(ps.SummaryBlock, "sessions_total: 0") {
		t.Errorf("空集汇总块应落零值:\n%s", ps.SummaryBlock)
	}
}

// TestBuildProfileSetBadJSONNotPanic success 行坏 JSON：二次解析失败按零值处理不 panic。
func TestBuildProfileSetBadJSONNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("坏 JSON panic: %v", r)
		}
	}()
	d := digestOf("bad-1", domain.FeatureStatusSuccess, extractor.ProfileStats{UserMsgCount: 2}, "{not-json")
	ps := buildProfileSet([]activity.ProfileDigest{d})
	if ps.TotalSuccess != 1 || ps.VisibleCount != 1 {
		t.Errorf("坏 JSON 行仍计 success: Visible=%d Total=%d", ps.VisibleCount, ps.TotalSuccess)
	}
	if strings.Contains(ps.SummaryBlock, "narrative_absent_sessions: 1") {
		t.Errorf("坏 JSON 行 absent 不应计数:\n%s", ps.SummaryBlock)
	}
}

// TestAssembleProfileSetEndToEnd 组件级：fetchDigests 取数 + buildProfileSet 组装。
func TestAssembleProfileSetEndToEnd(t *testing.T) {
	p := testPeriod()
	row := featRow("ok-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200)
	row.Client = "claude_code"
	row.ProfileJSON = profileJSONOf(t, extractor.FeatureProfile{Summary: "端到端"})
	repo := &fakeFeatureRepo{rows: []domain.SessionFeature{
		row,
		featRow("skip-1", domain.FeatureStatusSkipped, p.Start+300, p.Start+400),
	}}
	ev := New(nil, nil, repo, nil, nil, nil, nil)
	set, err := ev.AssembleProfileSet(context.Background(), "张三", p)
	if err != nil {
		t.Fatalf("AssembleProfileSet: %v", err)
	}
	if set.TotalSuccess != 1 || set.VisibleCount != 1 {
		t.Errorf("Visible=%d Total=%d, want 1/1", set.VisibleCount, set.TotalSuccess)
	}
	if !strings.Contains(set.SessionBlocks[0], "session_key: ok-1") {
		t.Errorf("块应为 ok-1:\n%s", set.SessionBlocks[0])
	}
}

// TestAssembleProfileSetRepoError 仓储错误 wrap ErrProfileRead 上抛。
func TestAssembleProfileSetRepoError(t *testing.T) {
	repo := &fakeFeatureRepo{err: errors.New("db down")}
	ev := New(nil, nil, repo, nil, nil, nil, nil)
	_, err := ev.AssembleProfileSet(context.Background(), "张三", testPeriod())
	if !errors.Is(err, ErrProfileRead) {
		t.Fatalf("err = %v, want ErrProfileRead", err)
	}
}

// TestAssembleProfileSetPerf 锚点：150 档案含 100k+ 字符 → buildProfileSet < 200ms。
func TestAssembleProfileSetPerf(t *testing.T) {
	summary := strings.Repeat("y", 700) // 150×700+Behavior ≈ 105k+ 字符
	digests := make([]activity.ProfileDigest, 0, 150)
	for i := 0; i < 150; i++ {
		st := extractor.ProfileStats{
			UserMsgCount:   i + 1,
			ToolCounts:     map[string]int{"Edit": i, "Read": i / 2},
			TurnKindCounts: map[string]int{"user": i + 1, "assistant": i},
		}
		digests = append(digests, digestOf(fmt.Sprintf("p-%03d", i), domain.FeatureStatusSuccess, st,
			profileJSONOf(t, extractor.FeatureProfile{Summary: summary})))
	}
	start := time.Now()
	ps := buildProfileSet(digests)
	elapsed := time.Since(start)
	if elapsed >= 200*time.Millisecond {
		t.Errorf("buildProfileSet 耗时 %v, want < 200ms", elapsed)
	}
	if ps.VisibleCount == 0 {
		t.Error("性能用例应产出可见块")
	}
}
