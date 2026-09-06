package activity

// 人群签名识别测试（specs §5.1 签名矩阵用例表逐条锚定）。
// 黑盒测试包：IdentifyPopulation 与 ParseDigests 均为导出纯函数。

import (
	"reflect"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// mkSessions 构造 n 条列表会话，tc1Count 条 TurnCount=1，其余 TurnCount=2。
func mkSessions(n, tc1Count int) []conversationlog.SessionSummary {
	if n == 0 {
		return nil
	}
	ss := make([]conversationlog.SessionSummary, 0, n)
	for i := 0; i < n; i++ {
		tc := 2
		if i < tc1Count {
			tc = 1
		}
		ss = append(ss, conversationlog.SessionSummary{
			SessionKey: "sk-" + time.Now().Format("150405") + "-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			TurnCount:  tc,
		})
	}
	return ss
}

// mkDigests 构造档案摘要切片：client 按序循环取值，status 按序循环取值。
func mkDigests(n int, clients []string, statuses []string) []ProfileDigest {
	if n == 0 {
		return nil
	}
	ds := make([]ProfileDigest, 0, n)
	for i := 0; i < n; i++ {
		ds = append(ds, ProfileDigest{
			SessionKey: "dk-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Status:     statuses[i%len(statuses)],
			Client:     clients[i%len(clients)],
		})
	}
	return ds
}

// allSkippedWorkbuddy 等便捷构造集合。
func allSkipped(n int, client string) []ProfileDigest {
	return mkDigests(n, []string{client}, []string{domain.FeatureStatusSkipped})
}

func TestAutoClientSignature(t *testing.T) {
	sig := IdentifyPopulation(mkSessions(20, 0), allSkipped(20, "claude_code"), 5)
	if sig.Kind != "auto_client" || sig.Note != NoteAutoClient {
		t.Fatalf("got %+v, want {auto_client %s}", sig, NoteAutoClient)
	}
}

func TestBypassSignature(t *testing.T) {
	sig := IdentifyPopulation(mkSessions(20, 0), allSkipped(20, "workbuddy"), 5)
	if sig.Kind != "bypass_orchestrator" || sig.Note != NoteBypass {
		t.Fatalf("got %+v, want {bypass_orchestrator %s}", sig, NoteBypass)
	}
}

func TestWorkTC1NormalPath(t *testing.T) {
	// tc=1 会话 12/20，档案 success 10/20 行（占比恰 50% ≥ 20% 分线）。
	profiles := mkDigests(20, []string{"claude_code"}, []string{
		domain.FeatureStatusSuccess, domain.FeatureStatusSkipped,
	})
	sig := IdentifyPopulation(mkSessions(20, 12), profiles, 5)
	if sig.Kind != "work_tc1" || sig.Note != NoteWorkTC1 {
		t.Fatalf("got %+v, want {work_tc1 %s}", sig, NoteWorkTC1)
	}
}

func TestTC1BelowLineFallsNormal(t *testing.T) {
	// tc=1 占 6/20（30% < 50%），success 占比 15/20 达线：命中即止落 normal。
	profiles := mkDigests(20, []string{"claude_code"}, []string{
		domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSkipped,
	})
	sig := IdentifyPopulation(mkSessions(20, 6), profiles, 5)
	if sig.Kind != "normal" || sig.Note != "" {
		t.Fatalf("got %+v, want {normal \"\"}", sig)
	}
}

func TestFailedDominanceExcluded(t *testing.T) {
	// failed 16/20 行（80% ≥ 50%）：前置排除落 normal。
	profiles := mkDigests(20, []string{"claude_code"}, []string{
		domain.FeatureStatusFailed, domain.FeatureStatusFailed, domain.FeatureStatusFailed, domain.FeatureStatusFailed,
	})
	sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5)
	if sig.Kind != "normal" || sig.Note != "" {
		t.Fatalf("got %+v, want {normal \"\"}", sig)
	}
}

func TestJudgementOrder(t *testing.T) {
	// success 占比 25% ≥ 20% 且 tc=1 会话占比 15/20 ≥ 50%：先命中 work_tc1，client=workbuddy 不落旁路。
	profiles := mkDigests(20, []string{"workbuddy"}, []string{
		domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess,
		domain.FeatureStatusSuccess, domain.FeatureStatusSkipped, domain.FeatureStatusSkipped, domain.FeatureStatusSkipped,
	})
	sig := IdentifyPopulation(mkSessions(20, 15), profiles, 5)
	if sig.Kind != "work_tc1" || sig.Note != NoteWorkTC1 {
		t.Fatalf("got %+v, want {work_tc1 %s}", sig, NoteWorkTC1)
	}
}

func TestMixedClientNoBypass(t *testing.T) {
	// 全 skipped 但 client 混 workbuddy/claude_code：存在正常客户端行不判旁路，落 auto_client。
	profiles := mkDigests(20, []string{"workbuddy", "claude_code"}, []string{domain.FeatureStatusSkipped})
	sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5)
	if sig.Kind != "auto_client" || sig.Note != NoteAutoClient {
		t.Fatalf("got %+v, want {auto_client %s}", sig, NoteAutoClient)
	}
}

func TestEmptyClientBypassFamily(t *testing.T) {
	// client 全空串（detail_invalid 形态）视同旁路族：与 workbuddy 同判。
	sig := IdentifyPopulation(mkSessions(20, 0), allSkipped(20, ""), 5)
	if sig.Kind != "bypass_orchestrator" || sig.Note != NoteBypass {
		t.Fatalf("got %+v, want {bypass_orchestrator %s}", sig, NoteBypass)
	}
}

func TestEmptyProfilesBoundary(t *testing.T) {
	if sig := IdentifyPopulation(mkSessions(6, 0), nil, 5); sig.Kind != "auto_client" || sig.Note != NoteAutoClient {
		t.Fatalf("0 profiles + 6 sessions: got %+v, want auto_client", sig)
	}
	if sig := IdentifyPopulation(nil, nil, 5); sig.Kind != "normal" || sig.Note != "" {
		t.Fatalf("0 profiles + 0 sessions: got %+v, want normal", sig)
	}
}

func TestEmptySessionsDegraded(t *testing.T) {
	// sessions=nil + 档案趋零（全 skipped、client=workbuddy）：列表量门槛跳过，档案侧判据照判 bypass 表型。
	if sig := IdentifyPopulation(nil, allSkipped(20, "workbuddy"), 5); sig.Kind != "bypass_orchestrator" || sig.Note != NoteBypass {
		t.Fatalf("nil sessions + workbuddy: got %+v, want bypass_orchestrator", sig)
	}
	// sessions=nil + client=claude_code：auto_client 表型。
	if sig := IdentifyPopulation(nil, allSkipped(20, "claude_code"), 5); sig.Kind != "auto_client" || sig.Note != NoteAutoClient {
		t.Fatalf("nil sessions + claude_code: got %+v, want auto_client", sig)
	}
}

func TestLowFreqThresholdGate(t *testing.T) {
	// 列表量低于低频下限：门槛判据不满足，趋零形态落 normal。
	profiles := allSkipped(20, "workbuddy")
	if sig := IdentifyPopulation(mkSessions(4, 0), profiles, 5); sig.Kind != "normal" {
		t.Fatalf("4 sessions below threshold: got %+v, want normal", sig)
	}
	// 列表量恰等于下限（≥ 判据）：照判。
	if sig := IdentifyPopulation(mkSessions(5, 0), profiles, 5); sig.Kind != "bypass_orchestrator" {
		t.Fatalf("5 sessions at threshold: got %+v, want bypass_orchestrator", sig)
	}
}

func TestSuccessRatioEdgeAt20Percent(t *testing.T) {
	// success 恰 20%（4/20）达线走工作型路径：tc=1 未达线落 normal。
	profiles := mkDigests(20, []string{"claude_code"}, []string{
		domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess,
		domain.FeatureStatusSkipped, domain.FeatureStatusSkipped,
	})
	if sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5); sig.Kind != "normal" {
		t.Fatalf("success 4/20 at line: got %+v, want normal", sig)
	}
	// success 3/20（15% < 20%）趋零：client=workbuddy 落 bypass。
	// statuses 显式列 20 项（3 success 17 skipped），循环取值下分布保持 3/20。
	belowLine := make([]string, 20)
	for i := range belowLine {
		belowLine[i] = domain.FeatureStatusSkipped
	}
	belowLine[0], belowLine[1], belowLine[2] = domain.FeatureStatusSuccess, domain.FeatureStatusSuccess, domain.FeatureStatusSuccess
	profiles = mkDigests(20, []string{"workbuddy"}, belowLine)
	if sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5); sig.Kind != "bypass_orchestrator" {
		t.Fatalf("success 3/20 below line: got %+v, want bypass_orchestrator", sig)
	}
}

func TestFailedRatioEdgeAt50Percent(t *testing.T) {
	// failed 恰 50%（10/20）触发前置排除。
	profiles := mkDigests(20, []string{"workbuddy"}, []string{
		domain.FeatureStatusFailed, domain.FeatureStatusSkipped,
	})
	if sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5); sig.Kind != "normal" || sig.Note != "" {
		t.Fatalf("failed 10/20 at line: got %+v, want normal", sig)
	}
	// failed 9/20（45% < 50%）不排除：success 0 趋零 + workbuddy 落 bypass。
	profiles = mkDigests(20, []string{"workbuddy"}, []string{
		domain.FeatureStatusFailed, domain.FeatureStatusSkipped, domain.FeatureStatusSkipped,
	})
	if sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5); sig.Kind != "bypass_orchestrator" {
		t.Fatalf("failed 9/20 below line: got %+v, want bypass_orchestrator", sig)
	}
}

func TestTC1RatioEdgeAt50Percent(t *testing.T) {
	// tc=1 恰 10/20（50%）达线 + success 达线：落 work_tc1。
	profiles := mkDigests(20, []string{"claude_code"}, []string{
		domain.FeatureStatusSuccess, domain.FeatureStatusSkipped,
	})
	if sig := IdentifyPopulation(mkSessions(20, 10), profiles, 5); sig.Kind != "work_tc1" {
		t.Fatalf("tc1 10/20 at line: got %+v, want work_tc1", sig)
	}
}

func TestThresholdOverskipNeverFires(t *testing.T) {
	// threshold 判据是 auto_client 子集，按序命中即止首期恒落 auto_client。
	profiles := allSkipped(20, "claude_code")
	if sig := IdentifyPopulation(mkSessions(20, 0), profiles, 5); sig.Kind != "auto_client" || sig.Note != NoteAutoClient {
		t.Fatalf("got %+v, want auto_client (threshold_overskip not fired)", sig)
	}
	if NoteThreshold == "" {
		t.Fatal("NoteThreshold constant must be retained")
	}
}

func TestUnknownClientInBypassFamily(t *testing.T) {
	// 探测退化 unknown 视同旁路族（specs §2.4 能力4 注意事项）。
	if sig := IdentifyPopulation(mkSessions(20, 0), allSkipped(20, "unknown"), 5); sig.Kind != "bypass_orchestrator" {
		t.Fatalf("unknown client: got %+v, want bypass_orchestrator", sig)
	}
	if sig := IdentifyPopulation(mkSessions(20, 0), allSkipped(20, "omo"), 5); sig.Kind != "bypass_orchestrator" {
		t.Fatalf("omo client: got %+v, want bypass_orchestrator", sig)
	}
}

func TestParseDigests(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	goodJSON := `{"Stats":{"TurnCount":3,"UserMsgCount":5,"InterruptCount":1,"DurationSec":120,"TrimmedChars":900,"ToolCounts":{"Edit":2},"TurnKindCounts":{"first":1,"normal":2},"ContinuationHit":false,"PasteCharCount":40,"SpecFingerprints":[{"Kind":"claude_md","CharCount":100,"Hash":"abc","SectionList":["# T"]}],"CmdReuseHashes":["h1","h2"]},"Summary":"s","Instruction":[{"text":"i"}],"Behavior":{"instruction_specificity":"high","interrupt_style":"rare","review_ratio":"low","paste_scale":"light"}}`
	rows := []domain.SessionFeature{
		{SessionKey: "k1", Status: domain.FeatureStatusSuccess, Client: "claude_code", ProfileJSON: goodJSON, LastTurnAt: now},
		{SessionKey: "k2", Status: domain.FeatureStatusSkipped, Client: "workbuddy", ProfileJSON: "", LastTurnAt: now},
		{SessionKey: "k3", Status: domain.FeatureStatusSuccess, Client: "opencode", ProfileJSON: "{bad json", LastTurnAt: now},
		{SessionKey: "k4", Status: domain.FeatureStatusFailed, Client: "claude_code", ProfileJSON: `{"Stats":{"TurnCount":2,"UserMsgCount":4}}`, LastTurnAt: now},
	}
	ds := ParseDigests(rows)
	if len(ds) != 4 {
		t.Fatalf("len=%d, want 4", len(ds))
	}

	// success 行：Stats 解析正确、HasBlocks=true。
	d1 := ds[0]
	if d1.SessionKey != "k1" || d1.Status != domain.FeatureStatusSuccess || d1.Client != "claude_code" {
		t.Fatalf("row0 meta mismatch: %+v", d1)
	}
	if !d1.HasBlocks {
		t.Fatal("success row HasBlocks must be true")
	}
	if d1.ProfileJSON != goodJSON {
		t.Fatal("ProfileJSON must be carried through")
	}
	if !d1.LastTurn.Equal(now) {
		t.Fatalf("LastTurn=%v, want %v", d1.LastTurn, now)
	}
	st := d1.Stats
	if st.TurnCount != 3 || st.UserMsgCount != 5 || st.InterruptCount != 1 || st.DurationSec != 120 ||
		st.TrimmedChars != 900 || st.PasteCharCount != 40 {
		t.Fatalf("stats scalar mismatch: %+v", st)
	}
	if st.ToolCounts["Edit"] != 2 || st.TurnKindCounts["normal"] != 2 {
		t.Fatalf("stats map mismatch: %+v", st)
	}
	if len(st.SpecFingerprints) != 1 || st.SpecFingerprints[0].Kind != extractor.FPClaudeMD || st.SpecFingerprints[0].Hash != "abc" {
		t.Fatalf("fingerprint mismatch: %+v", st.SpecFingerprints)
	}
	if len(st.CmdReuseHashes) != 2 || st.CmdReuseHashes[0] != "h1" {
		t.Fatalf("cmd reuse hashes mismatch: %+v", st.CmdReuseHashes)
	}

	// skipped 行：HasBlocks=false、Stats 零值。
	d2 := ds[1]
	if d2.HasBlocks {
		t.Fatal("skipped row HasBlocks must be false")
	}
	if !reflect.DeepEqual(d2.Stats, extractor.ProfileStats{}) {
		t.Fatalf("skipped row stats must be zero value, got %+v", d2.Stats)
	}

	// 坏 JSON success 行：Stats 零值不 panic。
	d3 := ds[2]
	if d3.HasBlocks != true {
		t.Fatal("success row HasBlocks stays true even with bad JSON")
	}
	if !reflect.DeepEqual(d3.Stats, extractor.ProfileStats{}) {
		t.Fatalf("bad JSON row stats must be zero value, got %+v", d3.Stats)
	}

	// failed 行：仅 Stats 块（specs §2.2），HasBlocks=false、Stats 解析正确。
	d4 := ds[3]
	if d4.HasBlocks {
		t.Fatal("failed row HasBlocks must be false")
	}
	if d4.Stats.TurnCount != 2 || d4.Stats.UserMsgCount != 4 {
		t.Fatalf("failed row stats mismatch: %+v", d4.Stats)
	}
}

func TestParseDigestsEmpty(t *testing.T) {
	if got := ParseDigests(nil); len(got) != 0 {
		t.Fatalf("ParseDigests(nil)=%v, want empty", got)
	}
}
