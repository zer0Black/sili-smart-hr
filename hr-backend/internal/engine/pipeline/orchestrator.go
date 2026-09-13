// orchestrator.go 批次编排器：批次创建与手动发起入口（specs P2_ASM_001 §5.2.2 步骤1、
// 03 §4.8）。TickTrigger 周期触发判定与 RunBatch 编排全流程归后续任务。
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// StaffFetcher 全员名单拉取窄接口（*userapi.Client 鸭子满足）。
type StaffFetcher interface {
	ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) ([]userapi.Staff, int64, error)
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
type Orchestrator struct {
	repo        repository.AssessmentBatchRepository
	alertRepo   repository.AssessmentAlertRepository
	featureRepo repository.SessionFeatureRepository
	configRepo  repository.AssessmentConfigRepository
	cl          *conversationlog.Client
	staffs      StaffFetcher
	secrets     SecretResolver
	evaluator   PersonEvaluator
	alerts      *fallback.AlertWriter
	batchEnq    BatchEnqueuer
	sessionEnq  SessionEnqueuer
}

// NewOrchestrator 组装批次编排器。
func NewOrchestrator(
	repo repository.AssessmentBatchRepository,
	alertRepo repository.AssessmentAlertRepository,
	featureRepo repository.SessionFeatureRepository,
	configRepo repository.AssessmentConfigRepository,
	cl *conversationlog.Client,
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
