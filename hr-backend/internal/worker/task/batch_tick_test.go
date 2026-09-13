package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// batch_tick_test.go 周期判定任务 handler 契约测试（03 §4.1/§4.3）。

// fakeBatchTickRunner tick 判定 fake：记录收到的 now，err 可注入。
type fakeBatchTickRunner struct {
	calls int
	nows  []time.Time
	err   error
}

var _ BatchTickRunner = (*fakeBatchTickRunner)(nil)

func (f *fakeBatchTickRunner) TickTrigger(ctx context.Context, now time.Time) error {
	f.calls++
	f.nows = append(f.nows, now)
	return f.err
}

// TestBatchTickDelegates 核心锚点：handler 调 TickTrigger 且透传错误。
func TestBatchTickDelegates(t *testing.T) {
	r := &fakeBatchTickRunner{}
	h := NewBatchTickHandler(r)
	before := time.Now()
	if err := h(context.Background(), asynq.NewTask(TypeBatchTick, nil)); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	after := time.Now()
	if r.calls != 1 {
		t.Fatalf("TickTrigger 调用 = %d, want 1", r.calls)
	}
	if r.nows[0].Before(before) || r.nows[0].After(after) {
		t.Errorf("TickTrigger now = %v, want 介于 [%v, %v]", r.nows[0], before, after)
	}

	rErr := &fakeBatchTickRunner{err: errors.New("config read failed")}
	hErr := NewBatchTickHandler(rErr)
	if err := hErr(context.Background(), asynq.NewTask(TypeBatchTick, nil)); err == nil {
		t.Fatal("TickTrigger err 非 nil 应透传交 Asynq 重试")
	}
}

// TestBatchTickTimeoutConst 核心锚点：超时常量精确 60s（03 §4.3）。
func TestBatchTickTimeoutConst(t *testing.T) {
	if batchTickTimeout != 60*time.Second {
		t.Errorf("batchTickTimeout = %v, want 60s", batchTickTimeout)
	}
}
