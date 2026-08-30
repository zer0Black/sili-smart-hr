// Package scheduler 封装 Asynq 定时调度的构造。
package scheduler

import (
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/worker/task"
)

// healthCheckCron 是 no-op 健康任务的触发周期，每分钟一次。
const healthCheckCron = "*/1 * * * *"

// NewScheduler 构造 Asynq scheduler 并注册 no-op 健康任务。
func NewScheduler(opt asynq.RedisConnOpt) *asynq.Scheduler {
	s := asynq.NewScheduler(opt, &asynq.SchedulerOpts{
		Location: time.Local,
	})
	s.Register(healthCheckCron, asynq.NewTask(task.TypeHealthCheck, nil))
	return s
}
