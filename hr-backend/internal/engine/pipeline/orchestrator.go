// orchestrator.go 批次编排器：批次创建、周期触发判定（TickTrigger）与批次编排
// 全流程（RunBatch），specs P2_ASM_001 §5.1/§5.2。
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

// ErrSecretResolveFailed 集成密钥解析失败哨兵（all 模式名单链路同样映射 1305）。
var ErrSecretResolveFailed = errors.New("pipeline: 集成密钥解析失败")

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
// batchID 参与 TaskID 维度：同批次重放去重，跨批次补跑独立投递。
type SessionEnqueuer interface {
	EnqueueSessionExtract(ctx context.Context, batchID int64, sessionKey, tokenName string) error
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

// CreateBatch 创建批次（03 §4.8）：specified 去重后落全名单；all 只落骨架
//（target_names_json='[]'，名单由 RunBatch 异步展开），同步路径仅密钥探测，
// 防建批预算被全员翻页击穿。时间列统一 UTC 落库（SQLite 偏移串字典序可比）。
func (o *Orchestrator) CreateBatch(ctx context.Context, req CreateBatchRequest) (*domain.AssessmentBatch, error) {
	names, err := o.resolveNames(ctx, req)
	if err != nil {
		return nil, err
	}
	// specified 空名单拒批；all 骨架（names 为空切片）放行，RunBatch 异步展开。
	skeleton := req.TargetMode == domain.BatchTargetAll
	if len(names) == 0 && !skeleton {
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

// SubmitManualBatch 手动发起入口（03 §4.8）：建批后投递 batch-run。入队走
// WithoutCancel 派生子 ctx：批次已落库，入队不应随 HTTP 请求取消（用户提交后
// 关页会把整批误杀成 failed）。入队失败落 failed 终态 + 告警不留孤儿，
// 批次已终态故返回入队原始错误仅供提示。
func (o *Orchestrator) SubmitManualBatch(ctx context.Context, req CreateBatchRequest) (*domain.AssessmentBatch, error) {
	batch, err := o.CreateBatch(ctx, req)
	if err != nil {
		return nil, err
	}
	enqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := o.batchEnq.EnqueueBatchRun(enqCtx, batch.ID); err != nil {
		enqErr := fmt.Errorf("pipeline: batch-run 入队失败: %w", err)
		if failErr := o.failWholeBatch(batch, enqErr.Error()); failErr != nil {
			return nil, failErr
		}
		return nil, enqErr
	}
	return batch, nil
}

// failWholeBatch 整批失败收敛：落 failed 终态 + 告警。落库走 WithoutCancel 派生
// ctx（本路径常见成因就是调用方 ctx 已取消，复用会让兜底必败留孤儿 running）；
// 告警按落库返回的实况计数，total==0（0 人批次或已终态幂等重放）不写，防
// ratio=0 的无效告警行覆盖既有信号。落库成功返 nil，失败上抛重试。
func (o *Orchestrator) failWholeBatch(batch *domain.AssessmentBatch, reason string) error {
	failCtx := context.WithoutCancel(context.Background())
	failed, total, failErr := o.repo.FailWholeBatch(failCtx, batch.ID, reason)
	if failErr != nil {
		return fmt.Errorf("pipeline: %s；落 failed 终态失败: %w", reason, failErr)
	}
	batch.Status = domain.BatchStatusFailed
	batch.ErrorSummary = reason
	if total > 0 {
		batch.EvaluatedCount = int(total)
		batch.FailedCount = int(failed)
		if o.alerts != nil {
			_ = o.alerts.WriteAlert(failCtx, batch)
		}
	}
	return nil
}

// TickTrigger 周期触发判定（03 §4.3）：读配置 → TriggerHit → 已建批判定 →
// 读名单 → 同源阻塞判定 → 建批入队。错误上抛交 Asynq 任务级重试；入队失败
// 回滚本次建批后上抛，重试在宽限窗内重建，耗尽则跳过至下个周期。
func (o *Orchestrator) TickTrigger(ctx context.Context, now time.Time) error {
	cfg, err := o.configRepo.Get(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: 配置读取: %w", err)
	}
	if !TriggerHit(now, cfg.Period, cfg.TriggerTime) {
		return nil
	}

	// 同源批次查询一次复用两处判定（同一 tick 用同一快照）。
	prev, err := o.repo.FindLatestScheduled(ctx)
	if err != nil {
		return fmt.Errorf("pipeline: 同源批次查询: %w", err)
	}

	// 本周期已建批判定：不限状态是关键，批次快速落终态后限 running 的查询
	// 会漏掉它，宽限窗内剩余 tick 重复建批。
	periodStart, err := PrevTriggerAt(now, cfg.Period, cfg.TriggerTime)
	if err != nil {
		return fmt.Errorf("pipeline: 本期触发点推算: %w", err)
	}
	if prev != nil && !prev.TriggeredAt.Before(periodStart) {
		return nil
	}

	// 评估对象快照：specified 读配置关联名单，all 由 CreateBatch 落骨架、
	// RunBatch 异步展开。
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

	// 同源阻塞判定：存在 running 且非停滞的定时批次时记 ERROR 跳过返回 nil，
	// 不排队叠加；停滞批次不阻塞。
	if prev != nil && prev.Status == domain.BatchStatusRunning && !IsStalled(now, prev.TriggeredAt, cfg.Period, prev.Status) {
		slog.Error("batch tick skipped: previous scheduled batch still running",
			"batch_no", prev.BatchNo, "triggered_at", prev.TriggeredAt)
		return nil
	}

	// 含止日口径：窗口左闭右开，PeriodStart 为窗口 start 当日零点，PeriodEnd 为
	// 窗口 end 前一日零点，保证连续周期不重叠不遗漏。窗口锚定触发点（periodStart）
	// 而非消费时刻 now：深夜触发点的宽限窗跨零点时 now 已落入次周期，按 now 推算
	// 会评估错误的周期窗口（TriggerHit 自身按触发点所在日判定，两处须同锚）。
	start, end := CurrentPeriodWindow(periodStart, cfg.Period)
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
		// 入队失败回滚本次建批（删骨架批次行）后上抛交 Asynq 任务级重试
		//（specs §5.1.5：重试耗尽跳过至下个周期）。回滚是重试可达的前提：
		// 不删则已建批判定拦截重试、批次滞留 running 成孤儿。回滚失败一并上抛
		// 交重试（DeleteBatch 幂等，重放时批次已删则守卫无命中）。
		slog.Error("batch-run enqueue failed, rolling back batch",
			"batch_id", batch.ID, "batch_no", batch.BatchNo, "err", err)
		if delErr := o.repo.DeleteBatch(context.WithoutCancel(context.Background()), batch.ID); delErr != nil {
			return fmt.Errorf("pipeline: batch-run 投递失败: %w；建批回滚失败: %v", err, delErr)
		}
		return fmt.Errorf("pipeline: batch-run 投递失败（已回滚建批）: %w", err)
	}
	slog.Info("batch tick triggered", "batch_no", batch.BatchNo, "period", cfg.Period)
	return nil
}

// RunBatch 批次编排全流程（03 §4.4）：骨架展开 → 分组 → 回填会话数 → 投递抽取
// → 等待落库 → 逐人评估 → 终态推进。非 running 幂等返回。重试重放全链重跑：
// 列表重拉、抽取任务按批次维度 TaskID 去重、人员行 pending 守卫防双计，
// 各步骤幂等收敛（无跨步骤跳过，防首跑与重放结果分叉）。
func (o *Orchestrator) RunBatch(ctx context.Context, batchID int64) error {
	batch, err := o.repo.GetByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("pipeline: 批次读取: %w", err)
	}
	if batch == nil || batch.Status != domain.BatchStatusRunning {
		return nil // 幂等终态：重复消费不重复编排
	}

	// all 骨架异步展开：'[]' 守卫防重试双插；指定名单批次建批时已具名。
	if batch.TargetMode == domain.BatchTargetAll && batch.TargetNamesJSON == "[]" {
		names, err := o.fetchAllStaffNames(ctx)
		if err != nil {
			return o.failWholeBatch(batch, fmt.Sprintf("全员名单拉取失败: %v", err))
		}
		if len(names) == 0 {
			return o.failWholeBatch(batch, "全员名单为空")
		}
		if err := o.repo.ExpandTargets(ctx, batchID, names); err != nil {
			return fmt.Errorf("pipeline: 全员名单展开落库: %w", err)
		}
		batch, err = o.repo.GetByID(ctx, batchID)
		if err != nil {
			return fmt.Errorf("pipeline: 批次读回: %w", err)
		}
		if batch == nil || batch.Status != domain.BatchStatusRunning {
			return nil
		}
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

	// 名单快照：展开后统一从此处取（骨架批次刚展开、重试批次直接命中缓存值）。
	var names []string
	if err := json.Unmarshal([]byte(batch.TargetNamesJSON), &names); err != nil {
		return fmt.Errorf("pipeline: 名单快照反序列化: %w", err)
	}

	sessions, err := activity.FetchAllSessions(ctx, o.cl, secret, period)
	if err != nil {
		// 上游列表不可用：整批落 failed 终态；落库失败则上抛交任务级重试
		//（重试入口幂等守卫快速返回），不留无人管的 running 批次。
		slog.Error("batch run upstream list failed", "batch_id", batchID, "batch_no", batch.BatchNo, "err", err)
		return o.failWholeBatch(batch, fmt.Sprintf("会话列表拉取失败: %v", err))
	}
	sessions = activity.DedupSessions(sessions) // 跨页重复去重（§5.2.5）

	// specified 批次收窄到名单内：上游不支持按人过滤（中文 token_name 禁用，
	// T4 §3.2）故仍全量拉取，但投递、等待与会话总数只算名单内会话，防指定
	// 少数人却抽取全组织会话烧 LLM 配额、等待屏障被无关会话撑爆超时。
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}
	scoped := make([]conversationlog.SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		if _, ok := nameSet[s.TokenName]; ok {
			scoped = append(scoped, s)
		}
	}

	groups := make(map[string][]conversationlog.SessionSummary)
	for _, s := range scoped {
		groups[s.TokenName] = append(groups[s.TokenName], s)
	}

	// 回填会话数（03 §4.4 步骤4）：名单快照全员入明细，无会话者为 0。
	// 重放重跑幂等：total 与逐人计数按同一窗口重算覆盖。
	personSessions := make(map[string]int, len(groups))
	for name, ss := range groups {
		personSessions[name] = len(ss)
	}
	if err := o.repo.UpdateTotalSessions(ctx, batchID, len(scoped), personSessions); err != nil {
		return fmt.Errorf("pipeline: 回填会话数: %w", err)
	}

	// 逐会话投递抽取任务（一会话一任务）。TaskID 批次维度去重（batchID:session_key）：
	// 同批次重放时段已在途的同键任务不重复入队；跨批次补跑同会话是独立任务正常投递
	//（执行侧 FindBySessionKey 幂等预检兜底）。只把投递成功的 session_key 收进
	// 等待集合：投递失败者无档案行，等待它永不达齐。
	pendingKeys := make([]string, 0, len(scoped))
	for _, s := range scoped {
		if err := o.sessionEnq.EnqueueSessionExtract(ctx, batchID, s.SessionKey, s.TokenName); err != nil {
			// 投递失败该会话无档案行，不计入失败比例分子，记日志继续。
			slog.Error("session extract enqueue failed", "batch_id", batchID, "session_key", s.SessionKey, "err", err)
			continue
		}
		pendingKeys = append(pendingKeys, s.SessionKey)
	}

	// 等待抽取落库：档案未落库时评估会走 skipped，轮询到齐再评估；
	// 超时按已落库档案继续，不中断不上抛。
	if err := o.waitExtractReady(ctx, batch, pendingKeys); err != nil {
		return err
	}

	// 逐人评估：固定 4 worker 消费名字通道，协程数与并发度对齐。
	// 零会话者映射 nil → 空切片：EvaluatePerson 以 nil 为「未提供需内部拉取」
	// 信号，空切片表示已知零会话，免重复全量翻页。
	nameCh := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < BatchRunConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tokenName := range nameCh {
				sess := groups[tokenName]
				if sess == nil {
					sess = []conversationlog.SessionSummary{}
				}
				o.runOnePerson(ctx, batch, tokenName, period, sess)
			}
		}()
	}
	for _, name := range names {
		nameCh <- name
	}
	close(nameCh)
	wg.Wait()

	return o.finalizeBatch(ctx, batchID, names, period, pendingKeys)
}

// waitExtractReady 抽取落库等待屏障：轮询查已落库键做差集收缩（查询代价随
// 完成度递减），集合空进评估；超时按已落库档案继续返回 nil；ctx 取消上抛。
// failed 终态行同样视为已落库；查询失败记 WARN 继续轮询，受超时上限约束。
func (o *Orchestrator) waitExtractReady(ctx context.Context, batch *domain.AssessmentBatch, sessionKeys []string) error {
	pending := sessionKeys
	deadline := time.Now().Add(o.extractWaitTimeout)
	for len(pending) > 0 {
		landed, err := o.featureRepo.ListExistingBySessionKeys(ctx, pending)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("extract wait query failed, keep polling",
				"batch_id", batch.ID, "batch_no", batch.BatchNo, "err", err)
		} else {
			landedSet := make(map[string]struct{}, len(landed))
			for _, k := range landed {
				landedSet[k] = struct{}{}
			}
			next := make([]string, 0, len(pending))
			for _, k := range pending {
				if _, ok := landedSet[k]; !ok {
					next = append(next, k)
				}
			}
			pending = next
			if len(pending) == 0 {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			slog.Warn("extract wait timeout, continue with landed profiles",
				"batch_id", batch.ID, "batch_no", batch.BatchNo, "remaining", len(pending))
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
	return nil
}

// runOnePerson 单人评估与终态推进（specs §5.2.4 规则1）：失败只影响自己。
// 评估成功按结果映射终态，重试耗尽落 failed；回写失败记日志后继续不阻塞。
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

// finalizeBatch 全员终态后落批次终态（specs §5.2.2 步骤6-8）：evaluated_count <
// total_count（存在回写失败者）不落终态留 running 停滞处置；否则按失败占比落
// 终态（≤10.00 success、<100 partial_failed、=100 failed）。会话级失败比例分子
// 为本批次投递会话中 failed 档案数（与名单内会话数分母同基），分母 0 置 0.00；
// 占比超阈写告警。
func (o *Orchestrator) finalizeBatch(ctx context.Context, batchID int64, names []string, period activity.Period, pendingKeys []string) error {
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
		failed, err := o.featureRepo.CountFailedByKeys(ctx, pendingKeys, period.Start, period.End)
		if err != nil {
			return fmt.Errorf("pipeline: 失败会话计数: %w", err)
		}
		sessionFailRatio = fallback.FailedRatioPercent(int(failed), batch.TotalSessionCount)
	}
	if err := o.repo.FinalizeBatch(ctx, batchID, status, sessionFailRatio); err != nil {
		return fmt.Errorf("pipeline: 批次终态落库: %w", err)
	}
	if !fallback.BelowAlertThreshold(batch.FailedCount, batch.TotalCount) && o.alerts != nil {
		_ = o.alerts.WriteAlert(ctx, batch) // 写入失败仅记日志不重试
	}
	return nil
}

// resolveNames 解析名单快照：specified 按 staff_name 去重（同名同人收敛，与
// uk_batch_person 同键）；all 只做密钥探测后返回空骨架（防密钥缺失时静默跑空）。
func (o *Orchestrator) resolveNames(ctx context.Context, req CreateBatchRequest) ([]string, error) {
	if req.TargetMode == domain.BatchTargetSpecified {
		return dedupeNames(req.TargetNames), nil
	}
	// all 骨架：只做密钥探测（配置缺失时立刻报错，不落 0 人批次静默跑空），
	// 名单展开移至 RunBatch 开头异步执行，建批同步路径不触上游翻页。
	if _, err := o.secrets(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSecretResolveFailed, err)
	}
	return []string{}, nil
}

// fetchAllStaffNames 全员名单全量分页拉取（RunBatch 异步展开消费）：短页/空页
// 与收齐 total 双终止，页数上限防上游分页失效无界翻页。
func (o *Orchestrator) fetchAllStaffNames(ctx context.Context) ([]string, error) {
	secret, err := o.secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSecretResolveFailed, err)
	}
	names := make([]string, 0)
	seen := make(map[string]struct{})
	fetched := 0 // 原始行数累计（未去重），与上游 total 同基
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
		fetched += len(items)
		// 终止：短页/空页是唯一可信信号（FetchAllSessions 同范式）；total>0
		// 时按原始行数收齐亦可终止——上游跨页重复行会让去重后 len(names)
		// 永远追不上 total，误用会打到页数上限整批失败。
		if len(items) < staffPageSize || len(items) == 0 {
			return names, nil
		}
		if total > 0 && int64(fetched) >= total {
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
