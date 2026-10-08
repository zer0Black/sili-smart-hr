package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/engine/fallback"
)

// test_expire_tick_test.go 逾期 tick 任务 handler 契约测试
//（specs P2_TST_001 §5.3.2/§5.3.5、03 §4.3）。

// fakeTestExpireRunner 逾期扫描 fake：记录收到的 now 与调用数，err 可注入。
type fakeTestExpireRunner struct {
	calls int
	nows  []time.Time
	nos   []string
	err   error
}

var _ TestExpireRunner = (*fakeTestExpireRunner)(nil)

func (f *fakeTestExpireRunner) ExpirePending(ctx context.Context, now time.Time) ([]string, error) {
	f.calls++
	f.nows = append(f.nows, now)
	return f.nos, f.err
}

// fakeNodeRecorder 节点投递 fake：收集 entries 供断言（Record 恒 nil 安全）。
type fakeNodeRecorder struct {
	entries []fallback.PendingLog
}

func (f *fakeNodeRecorder) Record(entry fallback.PendingLog) {
	f.entries = append(f.entries, entry)
}

// TestExpireTickHandler 核心锚点：fake 返回 3 条推进时 handler 返 nil 且
// runner 被调一次；runner 返回 error 时 handler 透传同一 error（交 Asynq 重试）。
func TestExpireTickHandler(t *testing.T) {
	r := &fakeTestExpireRunner{nos: []string{"T1", "T2", "T3"}}
	h := NewTestExpireTickHandler(r, nil)
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
	hErr := NewTestExpireTickHandler(rErr, nil)
	got := hErr(context.Background(), asynq.NewTask(TypeTestExpireTick, nil))
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 透传 %v", got, wantErr)
	}
}

// TestExpireTickZeroAdvance 覆盖零命中路径：nos 空同样成功（specs §5.3.2 步骤1
// 零命中属成功），不记任何节点。
func TestExpireTickZeroAdvance(t *testing.T) {
	r := &fakeTestExpireRunner{}
	rec := &fakeNodeRecorder{}
	h := NewTestExpireTickHandler(r, rec)
	if err := h(context.Background(), asynq.NewTask(TypeTestExpireTick, nil)); err != nil {
		t.Fatalf("零命中应 nil: %v", err)
	}
	if r.calls != 1 {
		t.Fatalf("ExpirePending 调用 = %d, want 1", r.calls)
	}
	if len(rec.entries) != 0 {
		t.Fatalf("零命中不应记节点, got %d 条", len(rec.entries))
	}
}

// TestTestExpireTickWritesNodeLog 核心锚点（specs P4_LOG_001 §5.2 逾期自动取消
// 节点，任务号逐行口径）：runner 返回 2 个 TaskNo 时 entries 恰 2 条，各自
// Target/Summary 命中、module system_job、操作人「系统」、result success。
func TestTestExpireTickWritesNodeLog(t *testing.T) {
	r := &fakeTestExpireRunner{nos: []string{"T20261008S01", "T20261008S02"}}
	rec := &fakeNodeRecorder{}
	h := NewTestExpireTickHandler(r, rec)

	if err := h(context.Background(), asynq.NewTask(TypeTestExpireTick, nil)); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(rec.entries) != 2 {
		t.Fatalf("节点条数 = %d, want 2", len(rec.entries))
	}
	for i, want := range []string{"测试任务 T20261008S01", "测试任务 T20261008S02"} {
		e := rec.entries[i]
		if e.Target != want {
			t.Errorf("entries[%d].Target = %q, want %q", i, e.Target, want)
		}
		if e.Summary != "任务自动逾期取消" {
			t.Errorf("entries[%d].Summary = %q, want 任务自动逾期取消", i, e.Summary)
		}
		if e.Module != "system_job" || e.FallbackName != "系统" || e.Result != "success" {
			t.Errorf("entries[%d] 头字段异常: %+v", i, e)
		}
		if e.RequestPath != TypeTestExpireTick {
			t.Errorf("entries[%d].RequestPath = %q, want %q", i, e.RequestPath, TypeTestExpireTick)
		}
	}
}

// TestExpireTickTimeoutConst 核心锚点：任务级超时精确 60s（specs §5.3 与 03 §4.3）。
func TestExpireTickTimeoutConst(t *testing.T) {
	if testExpireTickTimeout != 60*time.Second {
		t.Errorf("testExpireTickTimeout = %v, want 60s", testExpireTickTimeout)
	}
}
