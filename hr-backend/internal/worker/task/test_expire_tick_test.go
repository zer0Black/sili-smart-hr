package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// test_expire_tick_test.go 逾期 tick 任务 handler 契约测试
//（specs P2_TST_001 §5.3.2/§5.3.5、03 §4.3）。

// fakeTestExpireRunner 逾期扫描 fake：记录收到的 now 与调用数，err 可注入。
type fakeTestExpireRunner struct {
	calls int
	nows  []time.Time
	n     int64
	err   error
}

var _ TestExpireRunner = (*fakeTestExpireRunner)(nil)

func (f *fakeTestExpireRunner) ExpirePending(ctx context.Context, now time.Time) (int64, error) {
	f.calls++
	f.nows = append(f.nows, now)
	return f.n, f.err
}

// TestExpireTickHandler 核心锚点：fake 返回 3 条推进时 handler 返 nil 且
// runner 被调一次；runner 返回 error 时 handler 透传同一 error（交 Asynq 重试）。
func TestExpireTickHandler(t *testing.T) {
	r := &fakeTestExpireRunner{n: 3}
	h := NewTestExpireTickHandler(r)
	before := time.Now()
	if err := h(context.Background(), asynq.NewTask(TypeTestExpireTick, nil)); err != nil {
		t.Fatalf("推进 3 条应 nil: %v", err)
	}
	after := time.Now()
	if r.calls != 1 {
		t.Fatalf("ExpirePending 调用 = %d, want 1", r.calls)
	}
	if r.nows[0].Before(before) || r.nows[0].After(after) {
		t.Errorf("ExpirePending now = %v, want 介于 [%v, %v]", r.nows[0], before, after)
	}

	wantErr := errors.New("db down")
	rErr := &fakeTestExpireRunner{err: wantErr}
	hErr := NewTestExpireTickHandler(rErr)
	got := hErr(context.Background(), asynq.NewTask(TypeTestExpireTick, nil))
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 透传 %v", got, wantErr)
	}
}

// TestExpireTickZeroAdvance 覆盖零命中路径：n=0 同样成功（specs §5.3.2 步骤1 零命中属成功）。
func TestExpireTickZeroAdvance(t *testing.T) {
	r := &fakeTestExpireRunner{n: 0}
	h := NewTestExpireTickHandler(r)
	if err := h(context.Background(), asynq.NewTask(TypeTestExpireTick, nil)); err != nil {
		t.Fatalf("零命中应 nil: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("ExpirePending 调用 = %d, want 1", r.calls)
	}
}

// TestExpireTickTimeoutConst 核心锚点：任务级超时精确 60s（specs §5.3 与 03 §4.3）。
func TestExpireTickTimeoutConst(t *testing.T) {
	if testExpireTickTimeout != 60*time.Second {
		t.Errorf("testExpireTickTimeout = %v, want 60s", testExpireTickTimeout)
	}
}
