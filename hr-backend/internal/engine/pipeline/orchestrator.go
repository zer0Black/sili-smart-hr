// orchestrator.go 批次编排器：批次创建与手动发起入口（specs P2_ASM_001 §5.2.2 步骤1、
// 03 §4.8）。TickTrigger 周期触发判定与 RunBatch 编排全流程归后续任务。
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// ExtractWaitTimeout 抽取落库等待上限；ExtractPollInterval 轮询间隔。
	// 上限推导：抽取任务走独立 extract 队列（与 batch-run 分离，互不占并发），
	// 单会话抽取典型 10-60s，取充裕余量后定 30 分钟，超时按已落库档案继续评估。
	ExtractWaitTimeout  = 30 * time.Minute
	ExtractPollInterval = 10 * time.Second
)

const (
	// batchNoSeqMax 批次号 3 位序号上限（04 §3.1：B+yyyyMMddHHmm+3位序号）。
	batchNoSeqMax = 999
	// staffPageSize 全员名单分页页大小（上游 page_size 上限 100）。
	staffPageSize = 100
	// staffListMaxPages 全员名单翻页页数上限（上游分页失效防无界循环，
	// fetchAllSessions 同范式）。
	staffListMaxPages = 100
)

// ErrStaffFetchFailed 全员名单拉取失败哨兵（service 层据此映射 1305，errors.Is 判定）。
var ErrStaffFetchFailed = errors.New("pipeline: 全员名单拉取失败")

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
// 依赖面。retryBase 退避基准可经测试覆盖（默认 PersonEvalRetryBase），生产不动。
// extractWaitTimeout/extractPollInterval 抽取落库等待参数同范式（默认上方常量）。
type Orchestrator struct {
	repo        repository.AssessmentBatchRepository
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

	extractWaitTimeout  time.Duration
	extractPollInterval time.Duration
}

// NewOrchestrator 组装批次编排器。
func NewOrchestrator(
	repo repository.AssessmentBatchRepository,
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

		extractWaitTimeout:  ExtractWaitTimeout,
		extractPollInterval: ExtractPollInterval,
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
// all 经 StaffFetcher 全量分页拉取），批次行与人员明细同事务落库（running 态 +
// target_names_json + total_count + pending 行），无孤儿批次（03 §1.5）。
// 时间列统一 UTC 落库，与统计查询的 UTC 口径对齐（SQLite 偏移串字典序可比）。
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
			BatchNo:         fmt.Sprintf("B%s%03d", now.Local().Format("200601021504"), seq),
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
		// persons 携带构建闭包进事务：批次行 Create 回调填雪花 ID 后再取 ID 组装，
		// 保证明细行关联与批次行同事务原子落库。
		err := o.repo.CreateWithPersons(ctx, batch, func(batchID int64) []domain.AssessmentBatchPerson {
			return buildPersons(batchID, names)
		})
		if err == nil {
			return batch, nil
		}
		if !dberr.UniqueViolation(err) {
			return nil, fmt.Errorf("pipeline: 批次落库: %w", err)
		}
		// 撞 uk_batch_no：同分钟内并发建批，序号 +1 重试（specs §5.2.5 交任务级重试前的收敛）。
		// 批次行与人明细同事务，重试不产生半批次。
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
		return nil, o.failWholeBatch(ctx, batch, fmt.Sprintf("batch-run 入队失败: %v", err))
	}
	return batch, nil
}

// failWholeBatch 整批失败收敛路径：FailWholeBatch 落终态 + 告警（占比 100.00
// 超阈必写，§5.2.4 规则4），返回可直接上抛的复合错误。内存副本同步终态字段，
// 告警读库前以内存值为准（与库内 FailWholeBatch 同口径：计数置满 N/N）。
func (o *Orchestrator) failWholeBatch(ctx context.Context, batch *domain.AssessmentBatch, reason string) error {
	if failErr := o.repo.FailWholeBatch(ctx, batch.ID, reason); failErr != nil {
		return fmt.Errorf("pipeline: %s；落 failed 终态失败: %w", reason, failErr)
	}
	batch.Status = domain.BatchStatusFailed
	batch.ErrorSummary = reason
	batch.EvaluatedCount = batch.TotalCount
	batch.FailedCount = batch.TotalCount
	if o.alerts != nil {
		_ = o.alerts.WriteAlert(ctx, batch)
	}
	return fmt.Errorf("pipeline: %s", reason)
}

// TickTrigger 周期触发判定（03 §4.3，BatchTickRunner 消费面）：
// 读配置 → TriggerHit 未命中返 nil → 本周期已建批判定 → 读名单 → 同源阻塞判定
// → 推窗口 → CreateBatch + 入队。四类错误（配置读取/名单拉取/建批落库/入队）
// 上抛交 Asynq 任务级重试；入队失败落终态兜底，不留孤儿 running 批次。
func (o *Orchestrator) TickTrigger(ctx context.Context, now time.Time) error {
	cfg, err := o.configRepo.Get(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: 配置读取: %w", err)
	}
	if !TriggerHit(now, cfg.Period, cfg.TriggerTime) {
		return nil
	}

	// 本周期已建批判定（宽限窗防重复建批）：最近定时批次的 triggered_at 落在
	// 本期触发点之后即已建过，直接返回。PrevTriggerAt 给出本期触发点。
	periodStart, err := PrevTriggerAt(now, cfg.Period, cfg.TriggerTime)
	if err != nil {
		return fmt.Errorf("pipeline: 本期触发点推算: %w", err)
	}
	if prev, err := o.repo.FindLatestRunningScheduled(ctx); err != nil {
		return fmt.Errorf("pipeline: 同源批次查询: %w", err)
	} else if prev != nil && !prev.TriggeredAt.Before(periodStart) {
		return nil
	}

	// 评估对象快照（specs §5.1.2 步骤2）：specified 读配置关联名单，all 由
	// CreateBatch 经 StaffFetcher 展开。
	var names []string
	if cfg.TargetMode == domain.BatchTargetSpecified {
		members, err := o.configRepo.ListMembers(ctx, cfg.ID)
		if err != nil {
			return fmt.Errorf("pipeline: 指定名单读取: %w", err)
		}
		for _, m := range members {
			names = append(names, m.StaffName)
		}
	}

	// 同源阻塞判定（specs §5.1.4 规则2）：存在 running 且非停滞的定时批次时
	// 记 ERROR 跳过返回 nil，不排队叠加；停滞批次不阻塞。
	if prev, err := o.repo.FindLatestRunningScheduled(ctx); err != nil {
		return fmt.Errorf("pipeline: 同源批次查询: %w", err)
	} else if prev != nil && !IsStalled(now, prev.TriggeredAt, cfg.Period, prev.Status) {
		slog.Error("batch tick skipped: previous scheduled batch still running",
			"batch_no", prev.BatchNo, "triggered_at", prev.TriggeredAt)
		return nil
	}

	// 含止日口径（specs §5.1.4 规则1）：窗口左闭右开，PeriodStart 为窗口 start
	// 当日零点，PeriodEnd 为窗口 end 前一日零点，保证连续周期不重叠不遗漏。
	start, end := CurrentPeriodWindow(now, cfg.Period)
	batch, err := o.CreateBatch(ctx, CreateBatchRequest{
		TriggerType: domain.BatchTriggerScheduled,
		TargetMode:  cfg.TargetMode,
		TargetNames: names,
		PeriodStart: time.Unix(start, 0),
		PeriodEnd:   time.Unix(end, 0).AddDate(0, 0, -1),
	})
	if err != nil {
		return err
	}
	if err := o.batchEnq.EnqueueBatchRun(ctx, batch.ID); err != nil {
		// 与 SubmitManualBatch 同款兜底：批次已落库，入队失败落 failed 终态
		// 并写告警，返回错误交任务级重试（重试路径经已建批判定收敛）。
		return o.failWholeBatch(ctx, batch, fmt.Sprintf("batch-run 投递失败: %v", err))
	}
	slog.Info("batch tick triggered", "batch_no", batch.BatchNo, "period", cfg.Period)
	return nil
}

// 会话列表拉取翻页常量（页大小与页数上限复用 activity 包口径）。
const (
	sessionListPageSize = 100
	sessionListMaxPages = 100
)

// RunBatch 批次编排全流程（03 §4.4）：非 running 批次幂等返回 nil。
// 展开分组 → 回填会话数 → 投递抽取 → 等待抽取落库（投递成功集合计数到齐，
// 超时按已落库档案继续）→ 逐人评估 → 推进终态 → 告警。
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
		slog.Error("batch run upstream list failed", "batch_id", batchID, "batch_no", batch.BatchNo, "err", err)
		_ = o.failWholeBatch(ctx, batch, fmt.Sprintf("会话列表拉取失败: %v", err))
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
	// 只把投递成功的 session_key 收进等待集合：投递失败者无档案行，等待它永不达齐。
	pendingKeys := make([]string, 0, len(sessions))
	for _, s := range sessions {
		if err := o.sessionEnq.EnqueueSessionExtract(ctx, s.SessionKey, s.TokenName); err != nil {
			// 投递失败该会话无档案行，不计入失败比例分子（已知披露，§5.2.5），记日志继续。
			slog.Error("session extract enqueue failed", "batch_id", batchID, "session_key", s.SessionKey, "err", err)
			continue
		}
		pendingKeys = append(pendingKeys, s.SessionKey)
	}

	// 等待抽取落库（specs §5.2.2 步骤3 与步骤4 之间）：档案未落库时评估会走 skipped，
	// 轮询计数到齐再评估；超时按已落库档案继续，不中断不上抛。
	if err := o.waitExtractReady(ctx, batch, pendingKeys); err != nil {
		return err
	}

	// 逐人评估（specs §5.2.2 步骤4-7）：名单快照全员入编排，不在分组内者为零会话
	//（skipped 终态归 T5 评估侧）。固定 4 worker 消费名字通道（03 §4.4 并发上限），
	// 协程数与并发度对齐，长名单不囤积阻塞协程。
	var names []string
	if err := json.Unmarshal([]byte(batch.TargetNamesJSON), &names); err != nil {
		return fmt.Errorf("pipeline: 名单快照反序列化: %w", err)
	}
	nameCh := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < BatchRunConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tokenName := range nameCh {
				o.runOnePerson(ctx, batch, tokenName, period, groups[tokenName])
			}
		}()
	}
	for _, name := range names {
		nameCh <- name
	}
	close(nameCh)
	wg.Wait()

	return o.finalizeBatch(ctx, batchID, names, period)
}

// waitExtractReady 抽取落库等待屏障：轮询统计等待集合中已有档案行数（任意状态，
// failed 终态行同样视为已落库），到齐返回 nil 进入评估；超时记 WARN 返回 nil 按
// 已落库档案继续评估；ctx 取消返回 ctx.Err() 上抛。空集合直接跳过。计数查询本身
// 失败记 WARN 后继续轮询（瞬时故障拖长等待，仍受超时上限约束）。
func (o *Orchestrator) waitExtractReady(ctx context.Context, batch *domain.AssessmentBatch, sessionKeys []string) error {
	if len(sessionKeys) == 0 {
		return nil
	}
	want := int64(len(sessionKeys))
	deadline := time.Now().Add(o.extractWaitTimeout)
	for {
		done, err := o.featureRepo.CountExistingBySessionKeys(ctx, sessionKeys)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("extract wait count failed, keep polling",
				"batch_id", batch.ID, "batch_no", batch.BatchNo, "err", err)
		} else if done >= want {
			return nil
		}
		if !time.Now().Before(deadline) {
			slog.Warn("extract wait timeout, continue with landed profiles",
				"batch_id", batch.ID, "batch_no", batch.BatchNo, "expected", want, "done", done)
			return nil
		}
		timer := time.NewTimer(o.extractPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// runOnePerson 单人评估与终态推进（specs §5.2.4 规则1：失败只影响自己）：
// 评估成功按结果映射终态，重试耗尽落 failed；终态推进回写失败记 ERROR 日志后继续。
func (o *Orchestrator) runOnePerson(ctx context.Context, batch *domain.AssessmentBatch, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) {
	res, err := o.evaluatePersonWithRetry(ctx, tokenName, period, sessions)
	status := domain.PersonStatusFailed
	errorSummary := ""
	if err == nil {
		status = personTerminal(res)
	} else {
		errorSummary = err.Error()
		slog.Error("person evaluate retry exhausted", "batch_no", batch.BatchNo, "token_name", tokenName, "err", err)
	}
	if advErr := o.repo.AdvancePersonTerminal(ctx, batch.ID, tokenName, status, errorSummary, len(sessions)); advErr != nil {
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
// 会话级失败比例分子为窗口内全部 failed 档案（与 fetchAllSessions 全量分母同基，
// specs §5.2.2 步骤8），分母为 0 置 0.00 不除零；占比超阈写告警信号。
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
		failed, err := o.featureRepo.CountFailedInRange(ctx, period.Start, period.End)
		if err != nil {
			return fmt.Errorf("pipeline: 失败会话计数: %w", err)
		}
		sessionFailRatio = fallback.FailedRatioPercent(int(failed), batch.TotalSessionCount)
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
// 页数上限防上游分页失效（恒满页且 total 虚高时）无界翻页。
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
	for page := 1; page <= staffListMaxPages; page++ {
		items, total, err := o.staffs.ListStaffs(ctx, secret, "", page, staffPageSize)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrStaffFetchFailed, err)
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
	return nil, fmt.Errorf("%w: 翻页超 %d 页上限（上游分页疑似失效）", ErrStaffFetchFailed, staffListMaxPages)
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
// 主键由雪花回调填零值，批次行落库后 ID 已就绪。
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
