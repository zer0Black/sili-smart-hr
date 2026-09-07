package evaluator

// evaluate_person.go EvaluatePerson 原子入口（specs §2.4 能力6）：
// 活跃度统计 → 综合评估 → 聚合，三表各自落库不包事务，重跑按幂等收敛。

import (
	"context"

	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// EvaluatePerson 单人周期评估原子入口（specs §2.4 能力6）：活跃度 → 评估 → 聚合。
// sessions 可空：空时内部 StatPersonByKey 拉取，拉到的窗口内列表与档案集注入
// Evaluate（签名识别列表口径 + 免二次取数）；非空时直传 StatPerson 免二次拉取。
// LLM 失败降级路径（Evaluate 内已落 failed 行、err=nil）继续聚合；error 通道
// 只透传基础设施错误（配置/落库/列表拉取），业务态不占。
func (e *Evaluator) EvaluatePerson(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*EvaluateResult, error) {
	var stat *activity.ActivityStat
	var digests []activity.ProfileDigest
	var err error
	if len(sessions) == 0 {
		stat, sessions, digests, err = e.act.statPersonByKey(ctx, tokenName, period)
	} else {
		stat, err = e.act.statPerson(ctx, sessions, tokenName, period)
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

// statPersonByKey 适配透传：消费 StatPersonByKeyWithSessions 的列表与档案透出形态。
func (a *activityStatAdapter) statPersonByKey(ctx context.Context, tokenName string, period activity.Period) (*activity.ActivityStat, []conversationlog.SessionSummary, []activity.ProfileDigest, error) {
	return a.StatPersonByKeyWithSessions(ctx, tokenName, period)
}

// statPerson 适配透传。
func (a *activityStatAdapter) statPerson(ctx context.Context, sessions []conversationlog.SessionSummary, tokenName string, period activity.Period) (*activity.ActivityStat, error) {
	return a.StatPerson(ctx, sessions, tokenName, period)
}
