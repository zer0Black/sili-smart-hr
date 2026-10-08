package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// operation_log_clean_test.go 操作日志清理任务 handler 契约测试
//（specs P4_LOG_001 §5.5、03 §4.3）。

// fakeOpLogCleanRunner 清理 fake：记录收到的 now 与调用数，返回值可注入。
type fakeOpLogCleanRunner struct {
	calls int
	nows  []time.Time
	n     int64
	err   error
}

var _ OperationLogCleanRunner = (*fakeOpLogCleanRunner)(nil)

func (f *fakeOpLogCleanRunner) CleanExpired(ctx context.Context, now time.Time) (int64, error) {
	f.calls++
	f.nows = append(f.nows, now)
	return f.n, f.err
}

// TestOperationLogCleanHandler 核心锚点：runner 返回 sentinel err 时 handler
// 透传同一 err（交 Asynq 调度重试，specs §5.5.5）；成功路径返 nil。
func TestOperationLogCleanHandler(t *testing.T) {
	wantErr := errors.New("db down")
	r := &fakeOpLogCleanRunner{err: wantErr}
	h := NewOperationLogCleanHandler(r)
	got := h(context.Background(), asynq.NewTask(TypeOperationLogClean, nil))
	if !errors.Is(got, wantErr) {
		t.Fatalf("err = %v, want 透传 %v", got, wantErr)
	}
	if r.calls != 1 {
		t.Fatalf("CleanExpired 调用 = %d, want 1", r.calls)
	}

	rOK := &fakeOpLogCleanRunner{n: 2000}
	hOK := NewOperationLogCleanHandler(rOK)
	if err := hOK(context.Background(), asynq.NewTask(TypeOperationLogClean, nil)); err != nil {
		t.Fatalf("清理 2000 条应 nil: %v", err)
	}
	if rOK.calls != 1 {
		t.Fatalf("CleanExpired 调用 = %d, want 1", rOK.calls)
	}
}

// TestOperationLogCleanZeroDeletion 覆盖零删除路径：n=0 同样成功（无过期数据
// 属正常空转）。
func TestOperationLogCleanZeroDeletion(t *testing.T) {
	r := &fakeOpLogCleanRunner{n: 0}
	h := NewOperationLogCleanHandler(r)
	if err := h(context.Background(), asynq.NewTask(TypeOperationLogClean, nil)); err != nil {
		t.Fatalf("零删除应 nil: %v", err)
	}
}

// TestOperationLogCleanTimeoutConst 核心锚点：任务级超时精确 300s（03 §4.3）。
func TestOperationLogCleanTimeoutConst(t *testing.T) {
	if operationLogCleanTimeout != 300*time.Second {
		t.Errorf("operationLogCleanTimeout = %v, want 300s", operationLogCleanTimeout)
	}
}
