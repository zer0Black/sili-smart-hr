package task

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"
)

// TestNewMuxRegistersAllFour 核心锚点：NewMux 五参注册全部六类任务
//（health 原地 + session-extract / person-evaluate / batch-tick / batch-run /
// questionbank:generate）。
func TestNewMuxRegistersAllFour(t *testing.T) {
	noop := asynq.HandlerFunc(func(context.Context, *asynq.Task) error { return nil })
	mux := NewMux(noop, noop, noop, noop, noop)
	for _, typ := range []string{TypeHealthCheck, TypeSessionExtract, TypePersonEvaluate, TypeBatchTick, TypeBatchRun, TypeQuestionGenerate} {
		if h, _ := mux.Handler(asynq.NewTask(typ, nil)); h == nil {
			t.Errorf("任务类型 %q 未注册", typ)
		}
	}
}
