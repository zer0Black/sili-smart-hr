// orchestrator.go 批次编排器：批次创建与手动发起入口（specs P2_ASM_001 §5.2.2 步骤1、
// 03 §4.8）。TickTrigger 周期触发判定与 RunBatch 编排全流程归后续任务。
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/evaluator"
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/dberr"
	"sili-smart-hr/backend/internal/repository"
)

// 编排常量（03 §4.7）。
const (
	PersonEvalMaxRetries = 3                // 单人评估重试次数（含首次共 4 次尝试）
	PersonEvalRetryBase  = 30 * time.Second // 退避基准，倍增
	BatchRunConcurrency  = 4                // 逐人评估并发上限（对齐评估专用 client gate）
)

const (
	// batchNoSeqMax 批次号 3 位序号上限（04 §3.1：B+yyyyMMddHHmm+3位序号）。
	batchNoSeqMax = 999
	// staffPageSize 全员名单分页页大小（上游 page_size 上限 100）。
	staffPageSize = 100
	// personInsertBatch 人员明细批量落库批尺寸。
	personInsertBatch = 100
	// 任务类型常量（03 §4.1）：worker/task 定义 handler 侧，此处投递侧重复
	// 声明规避循环 import（task → pipeline → task）；改动须双侧同步。
	typeBatchRun       = "engine:batch-run"
	typeSessionExtract = "engine:session-extract"
)

// StaffFetcher 全员名单拉取窄接口（*userapi.Client 鸭子满足）。
type StaffFetcher interface {
	ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) ([]userapi.Staff, int64, error)
}

// SessionListFetcher 会话列表拉取窄接口（*conversationlog.Client 鸭子满足，
// 与 activity.SessionListFetcher 同签名，窄接口消费侧各持定义）。
type SessionListFetcher interface {
	ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error)
}

// SecretResolver 集成密钥解析（service.ResolveIntegrationSecret 收敛点注入）。
type SecretResolver func(ctx context.Context) (string, error)

// BatchEnqueuer batch-run 任务投递窄接口（Asynq 适配器满足，测试注入 fake）。
type BatchEnqueuer interface {
	EnqueueBatchRun(ctx context.Context, batchID int64) error
}

// SessionEnqueuer 会话抽取任务投递窄接口（Asynq 适配器与 fake 满足）。
type SessionEnqueuer interface {
	EnqueueSessionExtract(ctx context.Context, sessionKey, tokenName string) error
}

// PersonEvaluator 单人评估窄接口（*evaluator.Evaluator 鸭子满足）。
type PersonEvaluator interface {
	EvaluatePerson(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*evaluator.EvaluateResult, error)
}

// Orchestrator 批次编排器：批次从创建到终态的状态机持有者（specs §5.2.1）。
// cl/featureRepo/configRepo/evaluator/sessionEnq 承载 RunBatch 与 TickTrigger 的
// 依赖面，本任务只消费 repo/alertRepo/staffs/secrets/batchEnq/alerts。
// retryBase 退避基准可经测试覆盖（默认 PersonEvalRetryBase），生产不动。
type Orchestrator struct {
	repo        repository.AssessmentBatchRepository
	alertRepo   repository.AssessmentAlertRepository
	featureRepo repository.SessionFeatureRepository
	configRepo  repository.AssessmentConfigRepository
	cl          SessionListFetcher
	staffs      StaffFetcher
	secrets     SecretResolver
	evaluator   PersonEvaluator
	alerts      *fallback.AlertWriter
	batchEnq    BatchEnqueuer
	sessionEnq  SessionEnqueuer
	retryBase   time.Duration
}

// NewOrchestrator 组装批次编排器。
func NewOrchestrator(
	repo repository.AssessmentBatchRepository,
	alertRepo repository.AssessmentAlertRepository,
	featureRepo repository.SessionFeatureRepository,
	configRepo repository.AssessmentConfigRepository,
	cl SessionListFetcher,
	staffs StaffFetcher,
	secrets SecretResolver,
	evaluator PersonEvaluator,
	alerts *fallback.AlertWriter,
	batchEnq BatchEnqueuer,
	sessionEnq SessionEnqueuer,
) *Orchestrator {
	return &Orchestrator{
		repo:        repo,
		alertRepo:   alertRepo,
		featureRepo: featureRepo,
		configRepo:  configRepo,
		cl:          cl,
		staffs:      staffs,
		secrets:     secrets,
		evaluator:   evaluator,
		alerts:      alerts,
		batchEnq:    batchEnq,
		sessionEnq:  sessionEnq,
		retryBase:   PersonEvalRetryBase,
	}
}

// CreateBatchRequest 建批请求（03 §4.8）。
type CreateBatchRequest struct {
	TriggerType string    // scheduled / manual
	TargetMode  string    // all / specified
	TargetNames []string  // specified 模式的人名切片（staff_name 去重后传入）；all 模式为空
	PeriodStart time.Time // 含起日当日零点
	PeriodEnd   time.Time // 含止日当日零点
}

// CreateBatch 创建批次（03 §4.8）：生成批次号、解析名单快照（specified 去重；
// all 经 StaffFetcher 全量分页拉取），落 running 态 + target_names_json +
// total_count + 人员明细 pending 行。名单拉取失败上抛，不落孤儿批次（03 §1.5）。
func (o *Orchestrator) CreateBatch(ctx context.Context, req CreateBatchRequest) (*domain.AssessmentBatch, error) {
	names, err := o.resolveNames(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("pipeline: 名单为空，不落 0 人批次")
	}
	namesJSON, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 名单序列化: %w", err)
	}

	now := time.Now().UTC()
	for seq := 1; seq <= batchNoSeqMax; seq++ {
		batch := &domain.AssessmentBatch{
			BatchNo:         fmt.Sprintf("B%s%03d", now.Format("200601021504"), seq),
			TriggerType:     req.TriggerType,
			TargetMode:      req.TargetMode,
			TargetNamesJSON: string(namesJSON),
			TotalCount:      len(names),
			Status:          domain.BatchStatusRunning,
			ErrorSummary:    "",
			PeriodStartAt:   req.PeriodStart,
			PeriodEndAt:     req.PeriodEnd,
			TriggeredAt:     now,
		}
		err := o.repo.Create(ctx, batch)
		if err == nil {
			if err := o.repo.CreatePersons(ctx, buildPersons(batch.ID, names)); err != nil {
				return nil, fmt.Errorf("pipeline: 人员明细落库: %w", err)
			}
			return batch, nil
		}
		if !dberr.UniqueViolation(err) {
			return nil, fmt.Errorf("pipeline: 批次落库: %w", err)
		}
		// 撞 uk_batch_no：同分钟内并发建批，序号 +1 重试（specs §5.2.5 交任务级重试前的收敛）。
	}
	return nil, fmt.Errorf("pipeline: 批次号序号耗尽（%d 次撞唯一键）", batchNoSeqMax)
}

// SubmitManualBatch 手动发起入口（03 §4.8）：CreateBatch 后投递 batch-run；
// 入队失败把该批次落 failed 终态（占比 100.00 超阈必写告警，§5.2.4 规则4），不留孤儿进行中批次。
func (o *Orchestrator) SubmitManualBatch(ctx context.Context, req CreateBatchRequest) (*domain.AssessmentBatch, error) {
	batch, err := o.CreateBatch(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := o.batchEnq.EnqueueBatchRun(ctx, batch.ID); err != nil {
		reason := fmt.Sprintf("batch-run 入队失败: %v", err)
		if failErr := o.repo.FailWholeBatch(ctx, batch.ID, reason); failErr != nil {
			return nil, fmt.Errorf("pipeline: %s；落 failed 终态失败: %w", reason, failErr)
		}
		batch.Status = domain.BatchStatusFailed
		batch.ErrorSummary = reason
		batch.EvaluatedCount = batch.TotalCount
		batch.FailedCount = batch.TotalCount
		if o.alerts != nil {
			_ = o.alerts.WriteAlert(ctx, batch)
		}
		return nil, fmt.Errorf("pipeline: %w", err)
	}
	return batch, nil
}

// TickTrigger 周期触发判定（03 §4.3，BatchTickRunner 消费面）：
// 读配置 → TriggerHit 未命中返 nil → 读名单 → 同源阻塞判定 → 推窗口 → CreateBatch + 入队。
// 四类错误（配置读取/名单拉取/建批落库/入队）上抛交 Asynq 任务级重试。
// 完整逻辑归 T4 实现。
func (o *Orchestrator) TickTrigger(ctx context.Context, now time.Time) error {
	return nil
}

// 会话列表拉取翻页常量（页大小与页数上限复用 activity 包口径）。
const (
	sessionListPageSize = 100
	sessionListMaxPages = 100
)

// RunBatch 批次编排全流程（03 §4.4）：非 running 批次幂等返回 nil。
// 展开分组 → 回填会话数 → 投递抽取 → 逐人评估（T3）→ 推进终态（T3）→ 告警（T3）。
func (o *Orchestrator) RunBatch(ctx context.Context, batchID int64) error {
	batch, err := o.repo.GetByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("pipeline: 批次读取: %w", err)
	}
	if batch == nil || batch.Status != domain.BatchStatusRunning {
		return nil // 幂等终态：重复消费不重复编排
	}

	secret, err := o.secrets(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: 集成密钥解析: %w", err)
	}
	// Period 半开区间（03 §2.5）：PeriodStart 当日零点为 Start，PeriodEnd 加一天零点为 End。
	period := activity.Period{
		Start: batch.PeriodStartAt.Unix(),
		End:   batch.PeriodEndAt.AddDate(0, 0, 1).Unix(),
	}
	sessions, err := o.fetchAllSessions(ctx, secret, period)
	if err != nil {
		// 上游列表不可用：整批落 failed 终态（全员计入失败计数，§5.2.5 异常表第一行）。
		reason := fmt.Sprintf("会话列表拉取失败: %v", err)
		slog.Error("batch run upstream list failed", "batch_id", batchID, "batch_no", batch.BatchNo, "err", err)
		if failErr := o.repo.FailWholeBatch(ctx, batchID, reason); failErr != nil {
			return fmt.Errorf("pipeline: %s；落 failed 终态失败: %w", reason, failErr)
		}
		// 终态判定写告警（§5.2.4 规则4）：占比 100.00 超阈必写。
		batch.Status = domain.BatchStatusFailed
		batch.ErrorSummary = reason
		batch.EvaluatedCount = batch.TotalCount
		batch.FailedCount = batch.TotalCount
		if o.alerts != nil {
			_ = o.alerts.WriteAlert(ctx, batch)
		}
		return nil
	}

	sessions = activity.DedupSessions(sessions) // 跨页重复去重（§5.2.5）
	groups := make(map[string][]conversationlog.SessionSummary)
	for _, s := range sessions {
		groups[s.TokenName] = append(groups[s.TokenName], s)
	}

	// 回填会话数（03 §4.4 步骤4）：名单快照全员入明细，无会话者为 0。
	personSessions := make(map[string]int, len(groups))
	for name, ss := range groups {
		personSessions[name] = len(ss)
	}
	if err := o.repo.UpdateTotalSessions(ctx, batchID, len(sessions), personSessions); err != nil {
		return fmt.Errorf("pipeline: 回填会话数: %w", err)
	}

	// 逐会话投递抽取任务（一会话一任务，specs §5.2.2 步骤3）。
	for _, s := range sessions {
		if err := o.sessionEnq.EnqueueSessionExtract(ctx, s.SessionKey, s.TokenName); err != nil {
			// 投递失败该会话无档案行，不计入失败比例分子（已知披露，§5.2.5），记日志继续。
			slog.Error("session extract enqueue failed", "batch_id", batchID, "session_key", s.SessionKey, "err", err)
		}
	}

	// 逐人评估（specs §5.2.2 步骤4-7）：名单快照全员入编排，不在分组内者为零会话
	//（skipped 终态归 T5 评估侧）。逐人并发上限 4（channel 信号量，03 §4.4）。
	var names []string
	if err := json.Unmarshal([]byte(batch.TargetNamesJSON), &names); err != nil {
		return fmt.Errorf("pipeline: 名单快照反序列化: %w", err)
	}
	sem := make(chan struct{}, BatchRunConcurrency)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(tokenName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			o.runOnePerson(ctx, batch, tokenName, period, groups[tokenName], len(groups[tokenName]))
		}(name)
	}
	wg.Wait()

	return o.finalizeBatch(ctx, batchID, names, period)
}

// runOnePerson 单人评估与终态推进（specs §5.2.4 规则1：失败只影响自己）：
// 评估成功按结果映射终态，重试耗尽落 failed；终态推进回写失败记 ERROR 日志后继续。
func (o *Orchestrator) runOnePerson(ctx context.Context, batch *domain.AssessmentBatch, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary, sessionCount int) {
	res, err := o.evaluatePersonWithRetry(ctx, tokenName, period, sessions)
	status := domain.PersonStatusFailed
	errorSummary := ""
	if err == nil {
		status = personTerminal(res)
	} else {
		errorSummary = err.Error()
		slog.Error("person evaluate retry exhausted", "batch_no", batch.BatchNo, "token_name", tokenName, "err", err)
	}
	if advErr := o.repo.AdvancePersonTerminal(ctx, batch.ID, tokenName, status, errorSummary, sessionCount); advErr != nil {
		// 回写失败（specs §5.2.5）：批次留 running 按停滞处置，不阻塞其余人员。
		slog.Error("advance person terminal failed", "batch_no", batch.BatchNo, "token_name", tokenName, "err", advErr)
	}
}

// evaluatePersonWithRetry 单人评估带编排器侧重试（specs §5.3.4 规则1）：3 次重试
// 共 4 次尝试（Retry 的 maxAttempts 含首次），每次尝试派生 1050s 预算子 ctx，
// 退避等待不计入预算。
func (o *Orchestrator) evaluatePersonWithRetry(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*evaluator.EvaluateResult, error) {
	var res *evaluator.EvaluateResult
	err := fallback.Retry(ctx, func(attemptCtx context.Context) error {
		attemptCtx, cancel := context.WithTimeout(attemptCtx, personEvalBudget)
		defer cancel()
		r, err := o.evaluator.EvaluatePerson(attemptCtx, tokenName, period, sessions)
		if err != nil {
			return err
		}
		res = r
		return nil
	}, PersonEvalMaxRetries+1, o.retryBase)
	return res, err
}

// personEvalBudget 单次评估尝试预算（specs §5.2.2 步骤4，对齐 person-evaluate 任务超时）。
const personEvalBudget = 1050 * time.Second

// personTerminal 单人终态判定（03 §4.6 表）：Reused/Skipped 标志优先，
// 存在 failed 评分行判 degraded，否则 success。
func personTerminal(res *evaluator.EvaluateResult) string {
	switch {
	case res.Reused:
		return domain.PersonStatusReused
	case res.Skipped:
		return domain.PersonStatusSkipped
	}
	for _, s := range res.Scores {
		if s.Status == domain.ScoreStatusFailed {
			return domain.PersonStatusDegraded
		}
	}
	return domain.PersonStatusSuccess
}

// finalizeBatch 全员终态后落批次终态（specs §5.2.2 步骤6-7）：读回批次，
// evaluated_count < total_count（存在回写失败者）不落终态留 running 停滞处置；
// 否则按失败人数占比落终态（≤10.00 success、<100 partial_failed、=100 failed），
// 会话级失败比例分母为 0 置 0.00 不除零；占比超阈写告警信号。
func (o *Orchestrator) finalizeBatch(ctx context.Context, batchID int64, names []string, period activity.Period) error {
	batch, err := o.repo.GetByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("pipeline: 批次读回: %w", err)
	}
	if batch == nil || batch.Status != domain.BatchStatusRunning {
		return nil
	}
	if batch.EvaluatedCount < batch.TotalCount {
		slog.Error("batch advance incomplete, left running for stalled handling",
			"batch_no", batch.BatchNo, "evaluated", batch.EvaluatedCount, "total", batch.TotalCount)
		return nil
	}
	status := domain.BatchStatusPartialFailed
	switch {
	case fallback.BelowAlertThreshold(batch.FailedCount, batch.TotalCount):
		status = domain.BatchStatusSuccess
	case batch.FailedCount == batch.TotalCount:
		status = domain.BatchStatusFailed
	}

	sessionFailRatio := 0.00
	if batch.TotalSessionCount > 0 {
		failed, err := o.featureRepo.CountFailedByTokenNames(ctx, names, period.Start, period.End)
		if err != nil {
			return fmt.Errorf("pipeline: 失败会话计数: %w", err)
		}
		sessionFailRatio = math.Round(float64(failed)/float64(batch.TotalSessionCount)*10000) / 100
	}
	if err := o.repo.FinalizeBatch(ctx, batchID, status, sessionFailRatio); err != nil {
		return fmt.Errorf("pipeline: 批次终态落库: %w", err)
	}
	if !fallback.BelowAlertThreshold(batch.FailedCount, batch.TotalCount) && o.alerts != nil {
		_ = o.alerts.WriteAlert(ctx, batch) // 写入失败仅记日志不重试（specs §5.3.5）
	}
	return nil
}

// fetchAllSessions 全量串行翻页拉取会话列表（禁止按人过滤，T4 §3.2：中文
// token_name 上游过滤不可用）。短页/空页终止，页数上限防上游分页失效无界推进。
func (o *Orchestrator) fetchAllSessions(ctx context.Context, secret string, period activity.Period) ([]conversationlog.SessionSummary, error) {
	var all []conversationlog.SessionSummary
	for page := 1; page <= sessionListMaxPages; page++ {
		items, _, err := o.cl.ListSessions(ctx, secret, conversationlog.ListSessionsRequest{
			StartTime: period.Start,
			EndTime:   period.End,
			Page:      page,
			PageSize:  sessionListPageSize,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < sessionListPageSize {
			return all, nil
		}
	}
	return nil, fmt.Errorf("会话列表翻页超 %d 页上限（上游分页疑似失效）", sessionListMaxPages)
}

// resolveNames 解析名单快照：specified 按 staff_name 去重（同名同人收敛，与
// uk_batch_person 同键）；all 经 StaffFetcher 分页拉取（短页终止，§1.6 staff_name 即展开键）。
func (o *Orchestrator) resolveNames(ctx context.Context, req CreateBatchRequest) ([]string, error) {
	if req.TargetMode == domain.BatchTargetSpecified {
		return dedupeNames(req.TargetNames), nil
	}
	secret, err := o.secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 集成密钥解析: %w", err)
	}
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for page := 1; ; page++ {
		items, total, err := o.staffs.ListStaffs(ctx, secret, "", page, staffPageSize)
		if err != nil {
			return nil, fmt.Errorf("pipeline: 全员名单拉取: %w", err)
		}
		for _, s := range items {
			if _, ok := seen[s.StaffName]; !ok {
				seen[s.StaffName] = struct{}{}
				names = append(names, s.StaffName)
			}
		}
		// 终止：已收齐上游宣告的总数（total>0 时以收齐为准）；
		// total 未知（0）退回短页/空页终止。
		if total > 0 {
			if int64(len(names)) >= total || len(items) == 0 {
				return names, nil
			}
			continue
		}
		if len(items) < staffPageSize {
			return names, nil
		}
	}
}

// dedupeNames 按值去重并保持首次出现序。
func dedupeNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

// buildPersons 展开人员明细 pending 行（specs §5.2.2 步骤1）。
func buildPersons(batchID int64, names []string) []domain.AssessmentBatchPerson {
	persons := make([]domain.AssessmentBatchPerson, 0, len(names))
	for _, n := range names {
		persons = append(persons, domain.AssessmentBatchPerson{
			BatchID:      batchID,
			TokenName:    n,
			Status:       domain.PersonStatusPending,
			SessionCount: 0,
			ErrorSummary: "",
		})
	}
	return persons
}
