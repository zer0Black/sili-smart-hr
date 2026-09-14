package evaluator

// evaluate_person.go EvaluatePerson 原子入口（specs §2.4 能力6）：
// 活跃度统计 → 综合评估 → 聚合，三表各自落库不包事务，重跑按幂等收敛。

import (
	"context"

	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// EvaluatePerson 单人周期评估原子入口（specs §2.4 能力6）：活跃度 → 评估 → 聚合。
// sessions 语义按 nil/空切片区分：nil = 未提供（内部全量拉取，独立任务路径）；
// 空切片 = 已知零会话（批次编排路径，免重复翻页）。LLM 失败降级继续聚合，
// error 通道只透传基础设施错误。
func (e *Evaluator) EvaluatePerson(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*EvaluateResult, error) {
	var stat *activity.ActivityStat
	var digests []activity.ProfileDigest
	var err error
	if sessions == nil {
		stat, sessions, digests, err = e.act.StatPersonByKeyWithSessions(ctx, tokenName, period)
	} else {
		// StatPerson 透出档案集，两路都注入 Evaluate 免二次取数（快照同源，三表口径一致）。
		stat, digests, err = e.act.StatPerson(ctx, sessions, tokenName, period)
	}
	if err != nil {
		return nil, err
	}

	res, err := e.Evaluate(ctx, tokenName, period, sessions, digests)
	if err != nil {
		return nil, err
	}
	res.Activity = stat

	agg, err := e.sc.Aggregate(ctx, tokenName, period)
	if err != nil {
		return nil, err
	}
	res.Aggregate = agg
	return res, nil
}
