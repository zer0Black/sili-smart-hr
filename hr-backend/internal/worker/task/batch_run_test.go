package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

// batch_run_test.go 批次编排任务 handler 契约测试（03 §4.1/§4.2/§4.4）。
// BatchRunRunner 接口注入 fake 探针（任务契约的 Duck 类型面）。

// fakeBatchRunRunner 批次编排 fake：记录收到的批次 ID，err 可注入。
type fakeBatchRunRunner struct {
	calls int
	ids   []int64
	err   error
}

var _ BatchRunRunner = (*fakeBatchRunRunner)(nil)

func (f *fakeBatchRunRunner) RunBatch(ctx context.Context, batchID int64) error {
	f.calls++
	f.ids = append(f.ids, batchID)
	return f.err
}

func runBatchRunTask(h asynq.HandlerFunc, payload string) error {
	return h(context.Background(), asynq.NewTask(TypeBatchRun, []byte(payload)))
}

// TestBatchRunPayloadInvalid 核心锚点：空 payload / 非数字 / 0 / 负数四种，
// 返回 nil 丢弃且 RunBatch 未被调用（03 §4.2，构造侧确定性错误）。
func TestBatchRunPayloadInvalid(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"空payload", `{}`},
		{"空batch_id", `{"batch_id":""}`},
		{"非数字", `{"batch_id":"abc"}`},
		{"零", `{"batch_id":"0"}`},
		{"负数", `{"batch_id":"-9"}`},
		{"非法JSON", `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeBatchRunRunner{}
			h := NewBatchRunHandler(r)
			if err := runBatchRunTask(h, tc.payload); err != nil {
				t.Errorf("确定性坏 payload 应丢弃返回 nil, got %v", err)
			}
			if r.calls != 0 {
				t.Errorf("RunBatch 不应被调用，实际 %d 次", r.calls)
			}
		})
	}
}

// TestBatchRunDispatches 核心锚点：payload {"batch_id":"123"} → RunBatch 收到 int64(123)。
func TestBatchRunDispatches(t *testing.T) {
	r := &fakeBatchRunRunner{}
	h := NewBatchRunHandler(r)
	if err := runBatchRunTask(h, `{"batch_id":"123"}`); err != nil {
		t.Fatalf("正常调用应 nil: %v", err)
	}
	if r.calls != 1 || r.ids[0] != 123 {
		t.Errorf("RunBatch 收到 %v (calls=%d), want [123]", r.ids, r.calls)
	}
}

// TestBatchRunErrorPropagation RunBatch err 非 nil 透传交 Asynq 重试；
// err==nil 含整批失败终态不重试（03 §4.4 返回契约）。
func TestBatchRunErrorPropagation(t *testing.T) {
	r := &fakeBatchRunRunner{err: errors.New("db down")}
	h := NewBatchRunHandler(r)
	if err := runBatchRunTask(h, `{"batch_id":"123"}`); err == nil {
		t.Fatal("err 非 nil 应透传交 Asynq 重试")
	}
}

// TestBatchRunTimeoutConst 核心锚点：超时常量精确 24h（03 §4.4 推导兜底值）。
func TestBatchRunTimeoutConst(t *testing.T) {
	if batchRunTimeout != 24*time.Hour {
		t.Errorf("batchRunTimeout = %v, want 24h", batchRunTimeout)
	}
}
