// Package scheduler 封装 Asynq 定时调度的构造。
package scheduler

import (
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/worker/task"
)

// healthCheckCron 是 no-op 健康任务与批次 tick 任务的触发周期，每分钟一次。
const healthCheckCron = "*/1 * * * *"

// NewScheduler 构造 Asynq scheduler 并注册 no-op 健康任务、批次 tick 任务与
// 主动测试逾期 tick 任务（同频独立注册，specs §5.3.1）。tick 每分钟触发后经
// TickTrigger 读配置判定（specs §5.1.2 步骤1），重启后按持久化配置天然重新生效。
func NewScheduler(opt asynq.RedisConnOpt) *asynq.Scheduler {
	s := asynq.NewScheduler(opt, &asynq.SchedulerOpts{
		Location: time.Local,
	})
	s.Register(healthCheckCron, asynq.NewTask(task.TypeHealthCheck, nil))
	s.Register(healthCheckCron, asynq.NewTask(task.TypeBatchTick, nil))
	s.Register(healthCheckCron, asynq.NewTask(task.TypeTestExpireTick, nil))
	return s
}
