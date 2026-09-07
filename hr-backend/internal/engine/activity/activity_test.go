package activity

// Activity 组件测试（specs §5.1 逐条锚定 + 自行补充边界）。
// 黑盒测试包：Activity 经 New 构造，依赖全 fake 注入。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/repository"
)

// ---- fake 依赖 ----

// fakeListFetcher 按页返回预置会话，支持翻页与错误注入。
type fakeListFetcher struct {
	pages    [][]conversationlog.SessionSummary
	total    int64
	calls    int
	err      error
	lastReq  conversationlog.ListSessionsRequest
	lastSecs []string
}

func (f *fakeListFetcher) ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error) {
	f.calls++
	f.lastReq = req
	f.lastSecs = append(f.lastSecs, secret)
	if f.err != nil {
		return nil, 0, f.err
	}
	page := req.Page
	if page < 1 || page > len(f.pages) {
		return []conversationlog.SessionSummary{}, f.total, nil
	}
	return f.pages[page-1], f.total, nil
}

// fakeFeatureRepo 返回预置档案行。
type fakeFeatureRepo struct {
	rows    []domain.SessionFeature
	err     error
	lastArg struct {
		token string
		start int64
		end   int64
	}
	calls int
}

func (f *fakeFeatureRepo) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	return nil, nil
}

func (f *fakeFeatureRepo) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	return false, nil
}

func (f *fakeFeatureRepo) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	f.calls++
	f.lastArg.token = tokenName
	f.lastArg.start = start
	f.lastArg.end = end
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// 编译期断言 fake 满足仓储接口。
var _ repository.SessionFeatureRepository = (*fakeFeatureRepo)(nil)

// fakeThresholds 可变阈值（热重载测试用 setTh 覆写）。
type fakeThresholds struct {
	active  int
	lowFreq int
	err     error
}

func (f *fakeThresholds) ActivityThresholds(ctx context.Context) (int, int, error) {
	return f.active, f.lowFreq, f.err
}

// fakeStatRepo 内存承载 upsert 行，行数不增即幂等。
type fakeStatRepo struct {
	rows  []*domain.ActivityStat
	calls int
	err   error
}

func (f *fakeStatRepo) Upsert(ctx context.Context, rec *domain.ActivityStat) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	for i, r := range f.rows {
		if r.TokenName == rec.TokenName && r.PeriodStartAt.Equal(rec.PeriodStartAt) {
			f.rows[i] = rec
			return nil
		}
	}
	f.rows = append(f.rows, rec)
	return nil
}

var _ repository.ActivityStatRepository = (*fakeStatRepo)(nil)

// ---- 测试辅助 ----

var (
	secretOK = func(ctx context.Context) (string, error) { return "s3cret", nil }
	base     = time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC).Unix()
)

func weekPeriod() Period { return Period{Start: base, End: base + 7*24*3600} }

// featRow 构造一条档案行（Unix 秒入参）。
func featRow(key, status string, first, last int64, client string) domain.SessionFeature {
	return domain.SessionFeature{
		SessionKey:  key,
		TokenName:   "张三",
		Status:      status,
		Client:      client,
		FirstTurnAt: time.Unix(first, 0).UTC(),
		LastTurnAt:  time.Unix(last, 0).UTC(),
	}
}

// sess 构造一条列表会话。
func sess(key string, turns int) conversationlog.SessionSummary {
	return conversationlog.SessionSummary{SessionKey: key, TurnCount: turns, TokenName: "张三"}
}

// newAct 组装被测组件。
func newAct(cl *fakeListFetcher, feats *fakeFeatureRepo, th *fakeThresholds, repo *fakeStatRepo) *Activity {
	return New(cl, feats, th, repo, secretOK)
}

// ---- specs §5.1 验收锚点 ----

// TestWindowAttributionAndDedup 锚点：跨边界档案行（first_turn 前周期、last_turn 本周期）
// 过滤后计入；缓冲窗内 last_turn 在前周期的行排除；重复 session_key 列表去重。
func TestWindowAttributionAndDedup(t *testing.T) {
	p := weekPeriod()
	day := int64(24 * 3600)
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		// first_turn 在前周期（Start-2h）、last_turn 落本周期第 1 天：计入。
		featRow("cross-1", domain.FeatureStatusSuccess, p.Start-7200, p.Start+3600, "claude_code"),
		// first_turn 在缓冲窗内（Start-3600）、last_turn 也在前周期：排除。
		featRow("out-1", domain.FeatureStatusSuccess, p.Start-3600, p.Start-1800, "claude_code"),
		// 常规行：计入。
		featRow("in-1", domain.FeatureStatusSuccess, p.Start+day, p.Start+day+600, "opencode"),
	}}
	sessions := []conversationlog.SessionSummary{
		sess("a", 3), sess("b", 4), sess("a", 3), // 重复键 a 去重
	}
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), sessions, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.ValidSessionCount != 2 {
		t.Errorf("ValidSessionCount = %d, want 2（跨边界计入、前周期行排除）", stat.ValidSessionCount)
	}
	if stat.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2（重复键去重）", stat.SessionCount)
	}
	if stat.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", stat.SkippedCount)
	}
	if stat.ClientDist["claude_code"] != 1 || stat.ClientDist["opencode"] != 1 {
		t.Errorf("ClientDist = %v, want claude_code:1 opencode:1", stat.ClientDist)
	}
	// 取数缓冲窗：start 前移 24h。
	if feats.lastArg.start != p.Start-day {
		t.Errorf("fetch start = %d, want %d（前移 24h 缓冲）", feats.lastArg.start, p.Start-day)
	}
}

// TestActivityLevelThresholds 锚点：ValidSessionCount 12/7/3（阈值 10/5）→ active/low_freq/unused。
func TestActivityLevelThresholds(t *testing.T) {
	p := weekPeriod()
	for _, tc := range []struct {
		valid int
		want  string
	}{
		{12, domain.ActiveLevelActive},
		{7, domain.ActiveLevelLowFreq},
		{3, domain.ActiveLevelUnused},
	} {
		rows := make([]domain.SessionFeature, 0, tc.valid)
		for i := 0; i < tc.valid; i++ {
			rows = append(rows, featRow(fmt.Sprintf("k-%d", i), domain.FeatureStatusSuccess,
				p.Start+int64(i)*3600, p.Start+int64(i)*3600+600, "claude_code"))
		}
		act := newAct(nil, &fakeFeatureRepo{rows: rows},
			&fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
		stat, err := act.StatPerson(context.Background(), nil, "张三", p)
		if err != nil {
			t.Fatalf("valid=%d: %v", tc.valid, err)
		}
		if stat.ActiveLevel != tc.want {
			t.Errorf("valid=%d ActiveLevel = %q, want %q", tc.valid, stat.ActiveLevel, tc.want)
		}
	}
}

// TestValidSessionCount 锚点：success 10 + failed 2 + skipped 30 →
// ValidSessionCount=12、SessionCount=42、SkippedCount=30。
func TestValidSessionCount(t *testing.T) {
	p := weekPeriod()
	rows := make([]domain.SessionFeature, 0, 42)
	add := func(n int, status string) {
		for i := 0; i < n; i++ {
			rows = append(rows, featRow(fmt.Sprintf("%s-%d", status, i), status,
				p.Start+int64(len(rows))*60, p.Start+int64(len(rows))*60+30, "claude_code"))
		}
	}
	add(10, domain.FeatureStatusSuccess)
	add(2, domain.FeatureStatusFailed)
	add(30, domain.FeatureStatusSkipped)
	sessions := make([]conversationlog.SessionSummary, 0, 42)
	for i := range rows {
		sessions = append(sessions, sess(rows[i].SessionKey, 2))
	}
	act := newAct(nil, &fakeFeatureRepo{rows: rows},
		&fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), sessions, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.ValidSessionCount != 12 || stat.SessionCount != 42 || stat.SkippedCount != 30 {
		t.Errorf("valid=%d session=%d skipped=%d, want 12/42/30",
			stat.ValidSessionCount, stat.SessionCount, stat.SkippedCount)
	}
}

// TestActivityIdempotent 锚点：同人同周期二跑，fake repo 行数不增、字段更新。
func TestActivityIdempotent(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("k-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200, "claude_code"),
	}}
	repo := &fakeStatRepo{}
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, repo)
	if _, err := act.StatPerson(context.Background(), sess1(), "张三", p); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// 二跑前改档案：新行让 ValidSessionCount 变化，断言字段更新。
	feats.rows = append(feats.rows,
		featRow("k-2", domain.FeatureStatusSuccess, p.Start+300, p.Start+400, "opencode"))
	if _, err := act.StatPerson(context.Background(), sess1(), "张三", p); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("repo rows = %d, want 1（唯一索引幂等）", len(repo.rows))
	}
	if repo.rows[0].ValidSessionCount != 2 {
		t.Errorf("second run ValidSessionCount = %d, want 2（覆盖更新）", repo.rows[0].ValidSessionCount)
	}
	if repo.calls != 2 {
		t.Errorf("upsert calls = %d, want 2", repo.calls)
	}
}

func sess1() []conversationlog.SessionSummary {
	return []conversationlog.SessionSummary{sess("k-1", 5)}
}

// TestThresholdHotReload 锚点：阈值 (10,5) → (3,1)，ValidSessionCount=7 分级翻转。
func TestThresholdHotReload(t *testing.T) {
	p := weekPeriod()
	rows := make([]domain.SessionFeature, 0, 7)
	for i := 0; i < 7; i++ {
		rows = append(rows, featRow(fmt.Sprintf("k-%d", i), domain.FeatureStatusSuccess,
			p.Start+int64(i)*3600, p.Start+int64(i)*3600+600, "claude_code"))
	}
	th := &fakeThresholds{active: 10, lowFreq: 5}
	act := newAct(nil, &fakeFeatureRepo{rows: rows}, th, &fakeStatRepo{})
	stat1, err := act.StatPerson(context.Background(), nil, "张三", p)
	if err != nil || stat1.ActiveLevel != domain.ActiveLevelLowFreq {
		t.Fatalf("first run level=%q err=%v, want low_freq", stat1.ActiveLevel, err)
	}
	th.active, th.lowFreq = 3, 1
	stat2, err := act.StatPerson(context.Background(), nil, "张三", p)
	if err != nil || stat2.ActiveLevel != domain.ActiveLevelActive {
		t.Fatalf("second run level=%q err=%v, want active（阈值热更）", stat2.ActiveLevel, err)
	}
}

// TestAutoClientForcedUnused 锚点：档案趋零 + 列表量 20 + client=claude_code →
// ActiveLevel=unused、PopulationNote=evidence_note.auto_client。
func TestAutoClientForcedUnused(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("k-1", domain.FeatureStatusSkipped, p.Start+100, p.Start+200, "claude_code"),
	}}
	sessions := mkSessionsAt(20, p)
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), sessions, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.ActiveLevel != domain.ActiveLevelUnused {
		t.Errorf("ActiveLevel = %q, want unused（auto_client 强制）", stat.ActiveLevel)
	}
	if stat.PopulationNote != NoteAutoClient {
		t.Errorf("PopulationNote = %q, want %q", stat.PopulationNote, NoteAutoClient)
	}
}

// mkSessionsAt 构造 n 条落窗口内的列表会话（键唯一）。
func mkSessionsAt(n int, p Period) []conversationlog.SessionSummary {
	ss := make([]conversationlog.SessionSummary, 0, n)
	for i := 0; i < n; i++ {
		ss = append(ss, conversationlog.SessionSummary{
			SessionKey: fmt.Sprintf("s-%03d", i),
			TurnCount:  2,
			TokenName:  "张三",
		})
	}
	return ss
}

// TestStatPersonByKeyFetchFailure 锚点：列表返回错误 → err 非 nil、fake 落库零调用。
func TestStatPersonByKeyFetchFailure(t *testing.T) {
	p := weekPeriod()
	cl := &fakeListFetcher{err: errors.New("network down")}
	repo := &fakeStatRepo{}
	act := newAct(cl, &fakeFeatureRepo{}, &fakeThresholds{active: 10, lowFreq: 5}, repo)
	stat, err := act.StatPersonByKey(context.Background(), "张三", p)
	if err == nil {
		t.Fatalf("err = nil, want 非 nil（ErrSessionListFetch 语义）")
	}
	if stat != nil {
		t.Errorf("stat = %+v, want nil", stat)
	}
	if repo.calls != 0 {
		t.Errorf("upsert calls = %d, want 0（不落任何行）", repo.calls)
	}
}

// TestStatPersonListCountOnly 锚点：5 会话（1 重复键）+ 档案 3 success →
// SessionCount=4、TotalTurns=ΣTurnCount。
func TestStatPersonListCountOnly(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("f-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200, "claude_code"),
		featRow("f-2", domain.FeatureStatusSuccess, p.Start+300, p.Start+400, "opencode"),
		featRow("f-3", domain.FeatureStatusSuccess, p.Start+500, p.Start+600, "claude_code"),
	}}
	sessions := []conversationlog.SessionSummary{
		sess("a", 3), sess("b", 5), sess("c", 7), sess("d", 11), sess("b", 5),
	}
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), sessions, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.SessionCount != 4 {
		t.Errorf("SessionCount = %d, want 4（重复键去重）", stat.SessionCount)
	}
	if stat.TotalTurns != 3+5+7+11 {
		t.Errorf("TotalTurns = %d, want %d", stat.TotalTurns, 3+5+7+11)
	}
}

// ---- specs §5.3 性能锚点 ----

// TestActivityStatPerf：150 档案 + 分组列表跑 StatPerson 断言耗时 < 500ms。
func TestActivityStatPerf(t *testing.T) {
	p := weekPeriod()
	rows := make([]domain.SessionFeature, 0, 150)
	sessions := make([]conversationlog.SessionSummary, 0, 150)
	for i := 0; i < 150; i++ {
		ts := p.Start + int64(i)*600
		rows = append(rows, featRow(fmt.Sprintf("k-%04d", i), domain.FeatureStatusSuccess,
			ts, ts+300, "claude_code"))
		sessions = append(sessions, sess(fmt.Sprintf("k-%04d", i), 4))
	}
	act := newAct(nil, &fakeFeatureRepo{rows: rows},
		&fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	start := time.Now()
	if _, err := act.StatPerson(context.Background(), sessions, "张三", p); err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if d := time.Since(start); d >= 500*time.Millisecond {
		t.Errorf("StatPerson 150 档案耗时 %v, want < 500ms", d)
	}
}

// ---- 自行补充边界与异常 ----

// TestStatPersonByKeyPagination：多页串行翻页、内存分组、去重。page1 为满页
// （100 条，上游按 PageSize 填页契约），短页即尾页的终止判据下须由 page2 收尾。
func TestStatPersonByKeyPagination(t *testing.T) {
	p := weekPeriod()
	page1 := make([]conversationlog.SessionSummary, 0, 100)
	page1 = append(page1,
		conversationlog.SessionSummary{SessionKey: "p1-a", TurnCount: 2, TokenName: "张三"},
		conversationlog.SessionSummary{SessionKey: "p1-b", TurnCount: 3, TokenName: "李四"},
	)
	for i := len(page1); i < 100; i++ {
		page1 = append(page1, conversationlog.SessionSummary{
			SessionKey: fmt.Sprintf("fill-%03d", i), TurnCount: 1, TokenName: "填充人",
		})
	}
	page2 := []conversationlog.SessionSummary{
		{SessionKey: "p2-a", TurnCount: 4, TokenName: "张三"},
		{SessionKey: "p1-a", TurnCount: 2, TokenName: "张三"}, // 跨页重复键
	}
	cl := &fakeListFetcher{
		pages: [][]conversationlog.SessionSummary{page1, page2},
		total: 102, // 102 条记录两页（100 + 2），total 驱动串行翻页
	}
	feats := &fakeFeatureRepo{}
	act := newAct(cl, feats, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPersonByKey(context.Background(), "张三", p)
	if err != nil {
		t.Fatalf("StatPersonByKey: %v", err)
	}
	// 张三名下：p1-a、p2-a（p1-b 归李四，重复 p1-a 去重）。
	if stat.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2（分组 + 去重）", stat.SessionCount)
	}
	if stat.TotalTurns != 2+4 {
		t.Errorf("TotalTurns = %d, want 6", stat.TotalTurns)
	}
	if cl.calls != 2 {
		t.Errorf("list calls = %d, want 2（满页翻到下一页）", cl.calls)
	}
	if cl.lastReq.PageSize != 100 || cl.lastReq.Username != "" {
		t.Errorf("lastReq = %+v, want PageSize=100 Username 空（全量拉取）", cl.lastReq)
	}
	// 时间窗参数：列表拉取按 [Start, End) 传窗口。
	if cl.lastReq.StartTime != p.Start || cl.lastReq.EndTime != p.End {
		t.Errorf("list time window = [%d,%d), want [%d,%d)", cl.lastReq.StartTime, cl.lastReq.EndTime, p.Start, p.End)
	}
}

// TestStatPersonByKeySecretError：密钥解析失败 → error 上抛不落库。
func TestStatPersonByKeySecretError(t *testing.T) {
	p := weekPeriod()
	cl := &fakeListFetcher{}
	repo := &fakeStatRepo{}
	act := New(cl, &fakeFeatureRepo{}, &fakeThresholds{active: 10, lowFreq: 5}, repo,
		func(ctx context.Context) (string, error) { return "", errors.New("secret missing") })
	if _, err := act.StatPersonByKey(context.Background(), "张三", p); err == nil {
		t.Fatal("err = nil, want 非 nil")
	}
	if repo.calls != 0 {
		t.Errorf("upsert calls = %d, want 0", repo.calls)
	}
}

// TestStatPersonThresholdReadError：阈值读取失败 → error 上抛不落库。
func TestStatPersonThresholdReadError(t *testing.T) {
	p := weekPeriod()
	repo := &fakeStatRepo{}
	act := newAct(nil, &fakeFeatureRepo{}, &fakeThresholds{err: errors.New("db down")}, repo)
	if _, err := act.StatPerson(context.Background(), nil, "张三", p); err == nil {
		t.Fatal("err = nil, want 非 nil（ErrDimensionConfigRead 语义）")
	}
	if repo.calls != 0 {
		t.Errorf("upsert calls = %d, want 0", repo.calls)
	}
}

// TestStatPersonProfileReadError：档案读取失败 → error 上抛不落库。
func TestStatPersonProfileReadError(t *testing.T) {
	p := weekPeriod()
	repo := &fakeStatRepo{}
	act := newAct(nil, &fakeFeatureRepo{err: errors.New("db down")},
		&fakeThresholds{active: 10, lowFreq: 5}, repo)
	if _, err := act.StatPerson(context.Background(), nil, "张三", p); err == nil {
		t.Fatal("err = nil, want 非 nil（ErrProfileRead 语义）")
	}
	if repo.calls != 0 {
		t.Errorf("upsert calls = %d, want 0", repo.calls)
	}
}

// TestStatPersonStoreWriteError：落库失败 → error 上抛。
func TestStatPersonStoreWriteError(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("k-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200, "claude_code"),
	}}
	repo := &fakeStatRepo{err: errors.New("write fail")}
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, repo)
	if _, err := act.StatPerson(context.Background(), sess1(), "张三", p); err == nil {
		t.Fatal("err = nil, want 非 nil（ErrStoreWrite 语义）")
	}
}

// TestStatPersonEmptyAll：空列表 + 空档案 → unused、normal 空串 note、零计数。
func TestStatPersonEmptyAll(t *testing.T) {
	p := weekPeriod()
	act := newAct(nil, &fakeFeatureRepo{}, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), nil, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.ActiveLevel != domain.ActiveLevelUnused || stat.PopulationNote != "" {
		t.Errorf("level=%q note=%q, want unused/空串", stat.ActiveLevel, stat.PopulationNote)
	}
	if stat.SessionCount != 0 || stat.ValidSessionCount != 0 || stat.TotalTurns != 0 {
		t.Errorf("counts = %+v, want 全零", stat)
	}
}

// TestStatPersonPersistFields：落库行字段一一对应（周期转换、ClientDistJSON、note）。
func TestStatPersonPersistFields(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("k-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200, "claude_code"),
		featRow("k-2", domain.FeatureStatusSuccess, p.Start+300, p.Start+400, "opencode"),
		featRow("k-3", domain.FeatureStatusSkipped, p.Start+500, p.Start+600, "claude_code"),
	}}
	repo := &fakeStatRepo{}
	act := newAct(nil, feats, &fakeThresholds{active: 2, lowFreq: 1}, repo)
	if _, err := act.StatPerson(context.Background(), sess1(), "张三", p); err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(repo.rows))
	}
	r := repo.rows[0]
	if !r.PeriodStartAt.Equal(time.Unix(p.Start, 0).UTC()) || !r.PeriodEndAt.Equal(time.Unix(p.End, 0).UTC()) {
		t.Errorf("period = [%v,%v), want Unix 秒转换口径", r.PeriodStartAt, r.PeriodEndAt)
	}
	if r.ValidSessionCount != 2 || r.SkippedCount != 1 || r.SessionCount != 1 || r.TotalTurns != 5 {
		t.Errorf("counts = %d/%d/%d/%d, want 2/1/1/5", r.ValidSessionCount, r.SkippedCount, r.SessionCount, r.TotalTurns)
	}
	if r.ActiveLevel != domain.ActiveLevelActive {
		t.Errorf("ActiveLevel = %q, want active", r.ActiveLevel)
	}
	var dist map[string]int
	if err := json.Unmarshal([]byte(r.ClientDistJSON), &dist); err != nil {
		t.Fatalf("ClientDistJSON 非法 JSON: %v", err)
	}
	if dist["claude_code"] != 2 || dist["opencode"] != 1 {
		t.Errorf("ClientDistJSON = %v, want claude_code:2 opencode:1", dist)
	}
}

// TestStatPersonByKeyUsesSecret：密钥注入传递至列表客户端。
func TestStatPersonByKeyUsesSecret(t *testing.T) {
	p := weekPeriod()
	cl := &fakeListFetcher{pages: [][]conversationlog.SessionSummary{{}}, total: 0}
	act := newAct(cl, &fakeFeatureRepo{}, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	if _, err := act.StatPersonByKey(context.Background(), "张三", p); err != nil {
		t.Fatalf("StatPersonByKey: %v", err)
	}
	if len(cl.lastSecs) == 0 || cl.lastSecs[0] != "s3cret" {
		t.Errorf("secret 传递 = %v, want [s3cret]", cl.lastSecs)
	}
}

// TestStatPersonBoundaryLastTurn：last_turn 恰在 End（不含）与恰在 Start（含）。
func TestStatPersonBoundaryLastTurn(t *testing.T) {
	p := weekPeriod()
	feats := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("at-start", domain.FeatureStatusSuccess, p.Start-3600, p.Start, "claude_code"),
		featRow("at-end", domain.FeatureStatusSuccess, p.Start+100, p.End, "claude_code"),
	}}
	act := newAct(nil, feats, &fakeThresholds{active: 10, lowFreq: 5}, &fakeStatRepo{})
	stat, err := act.StatPerson(context.Background(), nil, "张三", p)
	if err != nil {
		t.Fatalf("StatPerson: %v", err)
	}
	if stat.ValidSessionCount != 1 {
		t.Errorf("ValidSessionCount = %d, want 1（Start 含 End 不含）", stat.ValidSessionCount)
	}
}

// TestStatPersonByKeyNoMatch：列表中无该 token 名 → 零计数行照常落库。
func TestStatPersonByKeyNoMatch(t *testing.T) {
	p := weekPeriod()
	cl := &fakeListFetcher{
		pages: [][]conversationlog.SessionSummary{{
			{SessionKey: "x-1", TurnCount: 2, TokenName: "李四"},
		}},
		total: 1,
	}
	repo := &fakeStatRepo{}
	act := newAct(cl, &fakeFeatureRepo{}, &fakeThresholds{active: 10, lowFreq: 5}, repo)
	stat, err := act.StatPersonByKey(context.Background(), "张三", p)
	if err != nil {
		t.Fatalf("StatPersonByKey: %v", err)
	}
	if stat.SessionCount != 0 || stat.TotalTurns != 0 {
		t.Errorf("counts = %d/%d, want 0/0（内存分组无命中）", stat.SessionCount, stat.TotalTurns)
	}
	if len(repo.rows) != 1 {
		t.Errorf("rows = %d, want 1（零计数行照常落库）", len(repo.rows))
	}
}
