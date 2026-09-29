package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/domain"
)

// TypeTestGrade AI 阅卷任务类型，payload 携任务主键（specs §5.2.1，03 §4.1）。
const TypeTestGrade = "assessment:test-grade"

// testGradeTimeout 任务级超时 300s（specs §5.2.4 规则3 初值：单任务阅卷为一次
// LLM 调用量级；阅卷专用 client Timeout 240s 小于本值保重试边界自洽，调整
// providers.go NewGradingLLMClient 参数须同步该处）。
const testGradeTimeout = 300 * time.Second

// TestGradePayload 任务载荷（03 §4.2）：task_id 为雪花 ID 十进制字符串。
type TestGradePayload struct {
	TaskID string `json:"task_id"`
}

// TestGrader 阅卷执行窄面（*grading.Grader 满足；本包不可 import grading：
// grading→pipeline→task 依赖链，装配收敛在 app 包）。
type TestGrader interface {
	Run(ctx context.Context, taskID int64) error
}

// TerminalDegrader 降级处置窄面：读任务行定 test_type（终态推进收敛进
// TestGradeResultRepo.DegradeTask 单事务，本接口只承担读）。
type TerminalDegrader interface {
	GetByID(ctx context.Context, id int64) (*domain.AssessmentTestTask, error)
}

// TestGradeResultRepo 判型降级窄面：DegradeTask 单事务落降级行并推任务行
// degraded（enneagram），ai_mgmt 仅推任务行（04 §3.3 降级行形态）。
type TestGradeResultRepo interface {
	DegradeTask(ctx context.Context, taskID int64, enneagram bool) error
}

// retryBudget 读 asynq 重试元数据（v0.26.0 签名返 (n, ok)），ok=false 按 m=0
// 处理。包级变量供测试注入（asynq 无公开 API 构造带 metadata 的 ctx）。
var retryBudget = func(ctx context.Context) (retried, maxRetry int) {
	n, ok := asynq.GetRetryCount(ctx)
	if !ok {
		n = 0
	}
	m, ok := asynq.GetMaxRetry(ctx)
	if !ok {
		m = 0
	}
	return n, m
}

// NewTestGradeHandler 构造阅卷任务 handler（mux 注册由 NewMux 统一）。坏
// payload 丢弃记 ERROR（question_generate 同款）；grader.Run 的 err 透传交
// Asynq 任务级重试，重试预算耗尽时吞错走降级（specs §5.2.5）。
func NewTestGradeHandler(grader TestGrader, degrader TerminalDegrader, results TestGradeResultRepo) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p TestGradePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			slog.Error("test grade payload invalid, discard task",
				"payload_bytes", len(t.Payload()), "err", err)
			return nil
		}
		taskID, err := strconv.ParseInt(p.TaskID, 10, 64)
		if err != nil || taskID <= 0 {
			slog.Error("test grade payload task_id invalid, discard task",
				"task_id", p.TaskID, "err", err)
			return nil
		}

		if err := grader.Run(ctx, taskID); err != nil {
			// ERROR 记任务号与错误摘要（specs §5.2.5 异常表）；任务行读取失败
			// 时退记 task_id，不因日志增强反向阻断重试。
			taskNo := ""
			if t2, gerr := degrader.GetByID(ctx, taskID); gerr == nil && t2 != nil {
				taskNo = t2.TaskNo
			}
			slog.Error("test grade run failed, will retry",
				"task_id", taskID, "task_no", taskNo, "err", err)
			retried, maxRetry := retryBudget(ctx)
			if retried < maxRetry {
				return err
			}
			return degrade(ctx, taskID, degrader, results)
		}
		return nil
	}
}

// degrade 重试耗尽降级（specs §5.2.5：WARN 记任务号，不产出告警信号）：
// DegradeTask 单事务承载「enneagram 落降级行 + 任务行推 degraded」两步，
// 防半降级中间态；失败上抛保留下次执行/补偿再投递收敛。
func degrade(ctx context.Context, taskID int64, degrader TerminalDegrader, results TestGradeResultRepo) error {
	task, err := degrader.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("test grade degrade: load task %d: %w", taskID, err)
	}
	slog.Warn("test grade retries exhausted, degrade to terminal",
		"task_id", taskID, "task_no", task.TaskNo)

	if err := results.DegradeTask(ctx, taskID, task.TestType == domain.TestTypeEnneagram); err != nil {
		return fmt.Errorf("test grade degrade: degrade task %d: %w", taskID, err)
	}
	return nil
}
