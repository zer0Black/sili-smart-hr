package evaluator

// digest_test.go 契约测试：fetchDigests 取数窗口口径（specs §2.4 能力1 流程段）。

import (
	"context"
	"errors"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/repository"
)

// fakeFeatureRepo 复用 activity 测试同形态：可配置返回 + 调用参数探针。
type fakeFeatureRepo struct {
	rows    []domain.SessionFeature
	err     error
	calls   int
	lastArg struct {
		token string
		start int64
		end   int64
	}
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

var _ repository.SessionFeatureRepository = (*fakeFeatureRepo)(nil)

var weekBase = time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC).Unix()

func testPeriod() activity.Period {
	return activity.Period{Start: weekBase, End: weekBase + 7*24*3600}
}

// featRow 构造档案行（first/last 为 Unix 秒）。
func featRow(key, status string, first, last int64) domain.SessionFeature {
	return domain.SessionFeature{
		SessionKey:  key,
		TokenName:   "张三",
		Status:      status,
		FirstTurnAt: time.Unix(first, 0).UTC(),
		LastTurnAt:  time.Unix(last, 0).UTC(),
	}
}

// TestFetchDigestsWindowFilter 锚点：4 行档案（窗口内 2、缓冲窗内但 last_turn
// 在前周期 1、窗口后 1）→ 过滤后 2 行；且取数 start 前移 24h 缓冲。
func TestFetchDigestsWindowFilter(t *testing.T) {
	p := testPeriod()
	day := int64(24 * 3600)
	repo := &fakeFeatureRepo{rows: []domain.SessionFeature{
		// 跨边界行：first_turn 在前周期、last_turn 落本周期 → 计入。
		featRow("cross-1", domain.FeatureStatusSuccess, p.Start-2*3600, p.Start+3600),
		// 常规窗口内行 → 计入。
		featRow("in-1", domain.FeatureStatusSuccess, p.Start+day, p.Start+day+600),
		// 缓冲窗内取回但 last_turn 在前周期 → 排除。
		featRow("out-prev", domain.FeatureStatusSuccess, p.Start-3600, p.Start-1800),
		// last_turn 在窗口后 → 排除。
		featRow("out-after", domain.FeatureStatusSuccess, p.End+3600, p.End+7200),
	}}
	got, err := fetchDigests(context.Background(), repo, "张三", p)
	if err != nil {
		t.Fatalf("fetchDigests: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2（窗口内 2 行）", len(got))
	}
	keys := map[string]bool{}
	for _, d := range got {
		keys[d.SessionKey] = true
	}
	if !keys["cross-1"] || !keys["in-1"] {
		t.Errorf("过滤结果键集 = %v, want cross-1 与 in-1", keys)
	}
	// 取数窗口：start 前移 24h 缓冲、end 取周期 End。
	if repo.lastArg.start != p.Start-day {
		t.Errorf("fetch start = %d, want %d（前移 24h）", repo.lastArg.start, p.Start-day)
	}
	if repo.lastArg.end != p.End {
		t.Errorf("fetch end = %d, want %d", repo.lastArg.end, p.End)
	}
	if repo.lastArg.token != "张三" {
		t.Errorf("token = %q, want 张三", repo.lastArg.token)
	}
}

// TestFetchDigestsBoundaryEndExcluded 边界：last_turn == End（右开）排除、
// last_turn == Start（左闭）计入。
func TestFetchDigestsBoundaryEndExcluded(t *testing.T) {
	p := testPeriod()
	repo := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("at-start", domain.FeatureStatusSuccess, p.Start, p.Start),
		featRow("at-end", domain.FeatureStatusSuccess, p.End-3600, p.End),
	}}
	got, err := fetchDigests(context.Background(), repo, "张三", p)
	if err != nil {
		t.Fatalf("fetchDigests: %v", err)
	}
	if len(got) != 1 || got[0].SessionKey != "at-start" {
		t.Fatalf("got = %+v, want 仅 at-start（[Start,End) 闭开）", got)
	}
}

// TestFetchDigestsDigestFieldsParsed ParseDigests 复用：Stats 解析与 LastTurn 透传。
func TestFetchDigestsDigestFieldsParsed(t *testing.T) {
	p := testPeriod()
	repo := &fakeFeatureRepo{rows: []domain.SessionFeature{
		featRow("ok-1", domain.FeatureStatusSuccess, p.Start+100, p.Start+200),
	}}
	got, err := fetchDigests(context.Background(), repo, "张三", p)
	if err != nil {
		t.Fatalf("fetchDigests: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	d := got[0]
	if !d.HasBlocks {
		t.Errorf("success 行 HasBlocks 应为 true")
	}
	if !d.LastTurn.Equal(time.Unix(p.Start+200, 0).UTC()) {
		t.Errorf("LastTurn = %v, want %v", d.LastTurn, time.Unix(p.Start+200, 0).UTC())
	}
}

// TestFetchDigestsRepoError 锚点：仓储错误 wrap 为 ErrProfileRead。
func TestFetchDigestsRepoError(t *testing.T) {
	cause := errors.New("db down")
	repo := &fakeFeatureRepo{err: cause}
	_, err := fetchDigests(context.Background(), repo, "张三", testPeriod())
	if !errors.Is(err, ErrProfileRead) {
		t.Fatalf("err = %v, want ErrProfileRead", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("wrap 链应保留底层错误, got %v", err)
	}
}

// TestFetchDigestsEmptyRows 空档案返回空切片非 nil、无错误。
func TestFetchDigestsEmptyRows(t *testing.T) {
	repo := &fakeFeatureRepo{rows: nil}
	got, err := fetchDigests(context.Background(), repo, "张三", testPeriod())
	if err != nil {
		t.Fatalf("fetchDigests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}
