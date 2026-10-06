// Package scheduler 封装 Asynq 定时调度的构造。
package scheduler

import (
	"time"

	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/worker/task"
)

// tickCron 是四类分钟级任务的触发周期，每分钟一次：no-op 健康任务、批次
// tick、主动测试逾期 tick 与建议生成 tick（同频共用，语义等价于各域独立
// cron 常量，单点收敛避免同值散布）。
const healthCheckCron = "*/1 * * * *"

// NewScheduler 构造 Asynq scheduler 并注册 no-op 健康任务、批次 tick 任务、
// 主动测试逾期 tick 任务与建议生成 tick 任务（同频独立注册，specs §5.3.1、
// 03 §4.1）。tick 每分钟触发后经 TickTrigger 读配置判定（specs §5.1.2 步骤1），
// 重启后按持久化配置天然重新生效；suggest tick 经 TickScan 扫描拾取
//（specs §5.1.2 步1/步6）。
func NewScheduler(opt asynq.RedisConnOpt) *asynq.Scheduler {
	s := asynq.NewScheduler(opt, &asynq.SchedulerOpts{
		Location: time.Local,
	})
	s.Register(healthCheckCron, asynq.NewTask(task.TypeHealthCheck, nil))
	s.Register(healthCheckCron, asynq.NewTask(task.TypeBatchTick, nil))
	s.Register(healthCheckCron, asynq.NewTask(task.TypeTestExpireTick, nil))
	s.Register(healthCheckCron, asynq.NewTask(task.TypeSuggestTick, nil))
	return s
}
