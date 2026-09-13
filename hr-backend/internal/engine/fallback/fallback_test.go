package fallback_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/repository"
)

var errSentinel = errors.New("sentinel failure")

func TestRetryExhaustsAndReturnsLastErr(t *testing.T) {
	calls := 0
	err := fallback.Retry(context.Background(), func(ctx context.Context) error {
		calls++
		return errSentinel
	}, 3, time.Millisecond)

	if !errors.Is(err, errSentinel) {
		t.Fatalf("期望返回 errSentinel，得到 %v", err)
	}
	if calls != 3 {
		t.Fatalf("期望 fn 被调 3 次，实际 %d 次", calls)
	}
}

func TestRetryContextCancelStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := fallback.Retry(ctx, func(ctx context.Context) error {
		calls++
		cancel() // 第 1 次尝试失败后取消父 ctx
		return errSentinel
	}, 3, 10*time.Second)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("期望返回 context.Canceled，得到 %v", err)
	}
	if calls != 1 {
		t.Fatalf("期望 fn 仅被调 1 次，实际 %d 次", calls)
	}
}

func TestRetrySucceedsOnSecondAttempt(t *testing.T) {
	calls := 0
	err := fallback.Retry(context.Background(), func(ctx context.Context) error {
		calls++
		if calls == 1 {
			return errSentinel
		}
		return nil
	}, 3, time.Millisecond)

	if err != nil {
		t.Fatalf("期望 nil，得到 %v", err)
	}
	if calls != 2 {
		t.Fatalf("期望 fn 被调 2 次，实际 %d 次", calls)
	}
}

func TestRetryFirstAttemptImmediate(t *testing.T) {
	start := time.Now()
	calls := 0
	_ = fallback.Retry(context.Background(), func(ctx context.Context) error {
		calls++
		if calls == 1 {
			if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
				t.Fatalf("首次尝试应在 baseDelay 等待前立即执行，实际延迟 %v", elapsed)
			}
			return errSentinel
		}
		return nil
	}, 2, 500*time.Millisecond)
}

func TestRetryBackoffSequenceDoubles(t *testing.T) {
	var times []time.Time
	_ = fallback.Retry(context.Background(), func(ctx context.Context) error {
		times = append(times, time.Now())
		return errSentinel
	}, 3, 20*time.Millisecond)

	if len(times) != 3 {
		t.Fatalf("期望 3 次尝试，实际 %d 次", len(times))
	}
	d1 := times[1].Sub(times[0])
	d2 := times[2].Sub(times[1])
	if d1 < 15*time.Millisecond || d1 > 40*time.Millisecond {
		t.Fatalf("第 1 次退避应约 20ms，实际 %v", d1)
	}
	if d2 < 35*time.Millisecond || d2 > 80*time.Millisecond {
		t.Fatalf("第 2 次退避应约 40ms（倍增），实际 %v", d2)
	}
}

func TestBelowAlertThreshold(t *testing.T) {
	cases := []struct {
		failed, total int
		want          bool
	}{
		{1, 10, true},   // 10.00 未超阈（≤ 判定）
		{2, 10, false},  // 20.00 超阈
		{1, 1000, true}, // 0.10
		{0, 5, true},    // 0.00
		{5, 0, true},    // 防御零除
		{101, 1000, false}, // 10.10 超阈
		{1005, 10000, false}, // 10.05 超阈（两位小数精度边界）
	}
	for _, c := range cases {
		if got := fallback.BelowAlertThreshold(c.failed, c.total); got != c.want {
			t.Errorf("BelowAlertThreshold(%d, %d) = %v，期望 %v", c.failed, c.total, got, c.want)
		}
	}
}

// fakeAlertRepo 捕获 UpsertByBatch 入参供断言。
type fakeAlertRepo struct {
	mu      sync.Mutex
	captured *domain.AssessmentAlert
	err     error
}

var _ repository.AssessmentAlertRepository = (*fakeAlertRepo)(nil)

func (f *fakeAlertRepo) UpsertByBatch(ctx context.Context, alert *domain.AssessmentAlert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = alert
	return f.err
}

func TestWriteAlertPersisted(t *testing.T) {
	repo := &fakeAlertRepo{}
	w := fallback.NewAlertWriter(repo)

	batch := &domain.AssessmentBatch{
		ID:          123,
		BatchNo:     "B20260913-001",
		FailedCount: 2,
		TotalCount:  8,
	}
	before := time.Now().UTC()
	if err := w.WriteAlert(context.Background(), batch); err != nil {
		t.Fatalf("期望 nil，得到 %v", err)
	}
	after := time.Now().UTC()

	got := repo.captured
	if got == nil {
		t.Fatal("仓储未被调用")
	}
	if got.FailedRatio != 25.00 {
		t.Errorf("FailedRatio = %v，期望 25.00", got.FailedRatio)
	}
	if got.BatchNo != "B20260913-001" {
		t.Errorf("BatchNo = %q，期望透传批次号", got.BatchNo)
	}
	if got.BatchID != 123 {
		t.Errorf("BatchID = %d，期望 123", got.BatchID)
	}
	if got.FailedCount != 2 || got.TotalCount != 8 {
		t.Errorf("FailedCount/TotalCount = %d/%d，期望 2/8", got.FailedCount, got.TotalCount)
	}
	if got.SignaledAt.Before(before) || got.SignaledAt.After(after) {
		t.Errorf("SignaledAt = %v，期望在调用时刻窗口内（UTC now）", got.SignaledAt)
	}
}

func TestWriteAlertRepoErrorSwallowed(t *testing.T) {
	repo := &fakeAlertRepo{err: errors.New("db down")}
	w := fallback.NewAlertWriter(repo)

	batch := &domain.AssessmentBatch{BatchNo: "B1", FailedCount: 5, TotalCount: 10}
	if err := w.WriteAlert(context.Background(), batch); err != nil {
		t.Fatalf("写入失败应仅记日志返回 nil，得到 %v", err)
	}
}
