// assessment_batch 批次域业务层：批次列表（A1）、跑批态势统计（A2）、跑批计划（A3）、
// 评估对象名单（A4）、失败明细（A5）、发起手动定向分析（B1）。口径细节见 specs
// P2_ASM_001 §4.1（时间归属区间与周期窗口分离、名单摘要、进度与停滞派生）。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// BatchListFilter 列表查询条件（trigger_type/status 空串=全部）。
type BatchListFilter struct {
	TriggerType    string
	Status         string
	Page, PageSize int
}

// BatchListDTO 列表行（03 A1 响应字段一一对应）。
type BatchListDTO struct {
	ID                  int64    `json:"id,string"`
	BatchNo             string   `json:"batch_no"`
	TriggerType         string   `json:"trigger_type"`
	TargetMode          string   `json:"target_mode"`
	TargetBrief         []string `json:"target_brief"`
	TargetNames         []string `json:"target_names"` // 全量名单快照（悬浮展示，03 A1 v1.5）
	PeriodStart         string   `json:"period_start"` // yyyy-MM-dd
	PeriodEnd           string   `json:"period_end"`   // yyyy-MM-dd（含端点，与 period_start 同口径）
	Status              string   `json:"status"`
	Stalled             bool     `json:"stalled"`
	EvaluatedCount      int      `json:"evaluated_count"`
	TotalCount          int      `json:"total_count"`
	ProgressPercent     int      `json:"progress_percent"` // 向下取整，total=0 时 0（后端兜底除零）
	CoveredSessionCount int      `json:"covered_session_count"`
	FailedCount         int      `json:"failed_count"`
	TriggeredAt         string   `json:"triggered_at"` // yyyy-MM-dd HH:mm
}

// BatchStatsDTO 统计卡（03 A2）。
type BatchStatsDTO struct {
	EvalCount            int64 `json:"eval_count"`
	EvaluatedPersonCount int64 `json:"evaluated_person_count"`
	RunningBatchCount    int64 `json:"running_batch_count"`
}

// BatchPlanDTO 计划卡（03 A3）。
type BatchPlanDTO struct {
	NextTriggerAt       string   `json:"next_trigger_at"` // yyyy-MM-dd HH:mm
	Period              string   `json:"period"`
	TargetMode          string   `json:"target_mode"`
	TargetBrief         []string `json:"target_brief"`
	TargetNames         []string `json:"target_names"` // specified 全量名单（悬浮展示，03 A3 v1.6）；all 为空数组
	TargetCount         int      `json:"target_count"` // all 模式上游不可达降级返 0（不报错）
	DimensionBaseCount  int      `json:"dimension_base_count"`
	DimensionUpperCount int      `json:"dimension_upper_count"`
}

// BatchTargetsDTO 评估对象名单（03 A4）。
type BatchTargetsDTO struct {
	BatchID     int64    `json:"batch_id,string"`
	TargetMode  string   `json:"target_mode"`
	Names       []string `json:"names"`
	Total       int      `json:"total"`
	PeriodStart string   `json:"period_start"`
	PeriodEnd   string   `json:"period_end"`
}

// BatchFailureItem 失败清单单行（03 A5）。
type BatchFailureItem struct {
	TokenName    string `json:"token_name"`
	ErrorSummary string `json:"error_summary"`
}

// BatchFailuresDTO 失败明细（03 A5）。
type BatchFailuresDTO struct {
	BatchID     int64              `json:"batch_id,string"`
	BatchNo     string             `json:"batch_no"`
	FailedCount int                `json:"failed_count"`
	TotalCount  int                `json:"total_count"`
	PeriodStart string             `json:"period_start"`
	PeriodEnd   string             `json:"period_end"`
	List        []BatchFailureItem `json:"list"`
}

// AssessmentBatchService 是批次业务接口：查询侧（A1-A5）+ 创建侧（B1）。
type AssessmentBatchService interface {
	List(ctx context.Context, f BatchListFilter) ([]BatchListDTO, int64, error)
	Stats(ctx context.Context) (*BatchStatsDTO, error)
	Plan(ctx context.Context) (*BatchPlanDTO, error)
	Targets(ctx context.Context, batchID int64) (*BatchTargetsDTO, error)
	Failures(ctx context.Context, batchID int64) (*BatchFailuresDTO, error)
	Create(ctx context.Context, p CreateBatchPayload) (*CreateBatchResult, error)
}

// CreateBatchPayload B1 请求体（service 侧）。
type CreateBatchPayload struct {
	TargetMode  string     // all / specified
	Staffs      []StaffDTO // specified 模式必填非空，staff_name 必填
	PeriodStart string     // yyyy-MM-dd
	PeriodEnd   string     // yyyy-MM-dd
}

// CreateBatchResult B1 响应。
type CreateBatchResult struct {
	ID         int64  `json:"id,string"`
	BatchNo    string `json:"batch_no"`
	Status     string `json:"status"`
	TotalCount int    `json:"total_count"`
}

// ManualBatchSubmitter 手动发起窄接口（*pipeline.Orchestrator 鸭子满足，测试注入 fake）。
type ManualBatchSubmitter interface {
	SubmitManualBatch(ctx context.Context, req pipeline.CreateBatchRequest) (*domain.AssessmentBatch, error)
}

type assessmentBatchService struct {
	batchRepo  repository.AssessmentBatchRepository
	configRepo repository.AssessmentConfigRepository
	dimRepo    repository.DimensionRepository
	staffs     userapiClient
	secretRepo repository.IntegrationSecretRepository
	encKey     []byte
	now        func() time.Time
	submitter  ManualBatchSubmitter
}

// NewAssessmentBatchService 构造批次 service。now 注入便于测试锚定时间；
// submitter 为手动发起入口（B1），查询侧方法不消费。
func NewAssessmentBatchService(
	batchRepo repository.AssessmentBatchRepository,
	configRepo repository.AssessmentConfigRepository,
	dimRepo repository.DimensionRepository,
	staffs userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	now func() time.Time,
	submitter ManualBatchSubmitter,
) AssessmentBatchService {
	return &assessmentBatchService{
		batchRepo:  batchRepo,
		configRepo: configRepo,
		dimRepo:    dimRepo,
		staffs:     staffs,
		secretRepo: secretRepo,
		encKey:     encKey,
		now:        now,
		submitter:  submitter,
	}
}

// Create 发起手动定向分析（03 B1）：枚举与字段校验 → 时段边界校验（specs §4.2.4 规则2）
// → 名单校验与去重 → 委托编排器建批入队，批次记录与名单快照同请求同步落库。
func (s *assessmentBatchService) Create(ctx context.Context, p CreateBatchPayload) (*CreateBatchResult, error) {
	if p.TargetMode != domain.BatchTargetAll && p.TargetMode != domain.BatchTargetSpecified {
		return nil, NewError(errcode.BadRequest)
	}
	if p.PeriodStart == "" || p.PeriodEnd == "" {
		return nil, NewError(errcode.BadRequest)
	}
	start, err := time.ParseInLocation("2006-01-02", p.PeriodStart, time.Local)
	if err != nil {
		return nil, NewError(errcode.BatchPeriodInvalid)
	}
	end, err := time.ParseInLocation("2006-01-02", p.PeriodEnd, time.Local)
	if err != nil {
		return nil, NewError(errcode.BatchPeriodInvalid)
	}
	today := time.Date(s.now().Year(), s.now().Month(), s.now().Day(), 0, 0, 0, 0, time.Local)
	// 只评已沉淀完的对话：终点含今天及以后非法；两端均含止日可相等（评估单日）。
	if end.Before(start) || !end.Before(today) {
		return nil, NewError(errcode.BatchPeriodInvalid)
	}
	var names []string
	if p.TargetMode == domain.BatchTargetSpecified {
		if len(p.Staffs) == 0 {
			return nil, NewError(errcode.BatchTargetInvalid)
		}
		names = make([]string, 0, len(p.Staffs))
		seen := make(map[string]struct{}, len(p.Staffs))
		for _, st := range p.Staffs {
			name := strings.TrimSpace(st.StaffName)
			if name == "" {
				return nil, NewError(errcode.BatchTargetInvalid)
			}
			// staff_name 去重键与 uk_batch_person 同键收敛（同名同人）。
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	batch, err := s.submitter.SubmitManualBatch(ctx, pipeline.CreateBatchRequest{
		TriggerType: domain.BatchTriggerManual,
		TargetMode:  p.TargetMode,
		TargetNames: names,
		PeriodStart: start,
		PeriodEnd:   end,
	})
	if err != nil {
		// all 模式名单链路失败（含密钥未配置，骨架批次建批前的探测）不落批次
		// 记录，映射 1305；其余错误统一 1500，前端 toast 留在弹窗。
		if errors.Is(err, pipeline.ErrStaffFetchFailed) || errors.Is(err, pipeline.ErrSecretResolveFailed) {
			return nil, NewError(errcode.StaffListUnavailable)
		}
		return nil, NewError(errcode.Internal)
	}
	return &CreateBatchResult{
		ID:         batch.ID,
		BatchNo:    batch.BatchNo,
		Status:     batch.Status,
		TotalCount: batch.TotalCount,
	}, nil
}

// List 列表查询：枚举校验 → repo 分页 → 单例配置单点读取 → 逐行组装 DTO
//（停滞派生 + 摘要 + 进度）。
func (s *assessmentBatchService) List(ctx context.Context, f BatchListFilter) ([]BatchListDTO, int64, error) {
	if !validBatchTriggerType(f.TriggerType) || !validBatchStatus(f.Status) {
		return nil, 0, NewError(errcode.BadRequest)
	}
	list, total, err := s.batchRepo.ListByFilter(ctx, repository.BatchFilter{
		TriggerType: f.TriggerType,
		Status:      f.Status,
		Page:        f.Page,
		PageSize:    f.PageSize,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list batches: %w", err)
	}
	// 单例配置读一次共享全页行（读失败按非停滞降级，WARN），口径与统计卡
	// running_batch_count 同函数（03 §4.5 两处口径一致）。
	cfg, cfgErr := s.configRepo.Get(ctx)
	if cfgErr != nil {
		slog.Warn("list batch stalled degrade: config unreadable", "err", cfgErr)
	}
	items := make([]BatchListDTO, 0, len(list))
	for i := range list {
		items = append(items, s.toListDTO(cfg, cfgErr == nil, &list[i]))
	}
	return items, total, nil
}

// toListDTO 组装列表行。configOK=false（配置读取失败）按非停滞降级。
func (s *assessmentBatchService) toListDTO(cfg *domain.AssessmentConfig, configOK bool, b *domain.AssessmentBatch) BatchListDTO {
	stalled := false
	if configOK {
		stalled = pipeline.IsStalled(s.now(), b.TriggeredAt, cfg.Period, b.Status)
	}
	names := parseTargetNames(b.TargetNamesJSON)
	dto := BatchListDTO{
		ID:                  b.ID,
		BatchNo:             b.BatchNo,
		TriggerType:         b.TriggerType,
		TargetMode:          b.TargetMode,
		TargetBrief:         briefNames(names, b.TargetMode),
		TargetNames:         names,
		PeriodStart:         b.PeriodStartAt.Local().Format("2006-01-02"),
		PeriodEnd:           b.PeriodEndAt.Local().Format("2006-01-02"),
		Status:              b.Status,
		Stalled:             stalled,
		EvaluatedCount:      b.EvaluatedCount,
		TotalCount:          b.TotalCount,
		ProgressPercent:     progressPercent(b.EvaluatedCount, b.TotalCount),
		CoveredSessionCount: b.CoveredSessionCount,
		FailedCount:         b.FailedCount,
		TriggeredAt:         b.TriggeredAt.Local().Format("2006-01-02 15:04"),
	}
	return dto
}

// Stats 统计卡：本期跑批间隔 = [本期触发点, 下次触发点)（specs §4.1.2A）。
// 两端由 PrevTriggerAt/NextTriggerAt 纯日历推算（与历史定时批次无关，周期配置
// 变更不产生超宽间隔），UTC 口径传入仓储（批次行 triggered_at 以 UTC 落库，
// SQLite 偏移串字典序可比）。
func (s *assessmentBatchService) Stats(ctx context.Context) (*BatchStatsDTO, error) {
	cfg, err := s.configRepo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get assessment config: %w", err)
	}
	now := s.now()
	end, err := pipeline.NextTriggerAt(now, cfg.Period, cfg.TriggerTime)
	if err != nil {
		return nil, fmt.Errorf("next trigger at: %w", err)
	}
	start, err := pipeline.PrevTriggerAt(now, cfg.Period, cfg.TriggerTime)
	if err != nil {
		return nil, fmt.Errorf("prev trigger at: %w", err)
	}
	evalCount, err := s.batchRepo.CountInRange(ctx, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("count batches in range: %w", err)
	}
	personCount, err := s.batchRepo.CountSuccessSideTriggeredBetween(ctx, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("count success side: %w", err)
	}
	// 进行中剔除停滞：与列表 Stalled 派生同一 IsStalled 函数（03 §4.5 两处口径
	// 一致），running 批次量级个位，逐行判定替代按周期长度回推的近似口径。
	triggeredAts, err := s.batchRepo.ListRunningTriggeredAt(ctx)
	if err != nil {
		return nil, fmt.Errorf("list running triggered_at: %w", err)
	}
	runningCount := int64(0)
	for _, ta := range triggeredAts {
		if !pipeline.IsStalled(now, ta, cfg.Period, domain.BatchStatusRunning) {
			runningCount++
		}
	}
	return &BatchStatsDTO{
		EvalCount:            evalCount,
		EvaluatedPersonCount: personCount,
		RunningBatchCount:    runningCount,
	}, nil
}

// Plan 计划卡：读配置单例推下次执行（03 A3），specified 名单 brief/names，
// all 模式 target_count 经人员检索接口取 total（上游不可达降级 0 不报错）。
func (s *assessmentBatchService) Plan(ctx context.Context) (*BatchPlanDTO, error) {
	cfg, err := s.configRepo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get assessment config: %w", err)
	}
	next, err := pipeline.NextTriggerAt(s.now(), cfg.Period, cfg.TriggerTime)
	if err != nil {
		return nil, fmt.Errorf("next trigger at: %w", err)
	}
	dto := &BatchPlanDTO{
		NextTriggerAt: next.Local().Format("2006-01-02 15:04"),
		Period:        cfg.Period,
		TargetMode:    cfg.TargetMode,
		TargetBrief:   []string{},
		TargetNames:   []string{},
	}
	if cfg.TargetMode == domain.BatchTargetSpecified {
		members, merr := s.configRepo.ListMembers(ctx, cfg.ID)
		if merr != nil {
			return nil, fmt.Errorf("list assessment config members: %w", merr)
		}
		names := make([]string, 0, len(members))
		for i := range members {
			names = append(names, members[i].StaffName)
		}
		dto.TargetNames = names
		dto.TargetBrief = briefNames(names, domain.BatchTargetSpecified)
		dto.TargetCount = len(members)
	} else {
		// all 模式全员计数：上游不可达（含密钥未配置）降级 0，不报错（03 A3）。
		if secret, serr := ResolveIntegrationSecret(ctx, s.secretRepo, s.encKey); serr == nil {
			if _, total, terr := s.staffs.ListStaffs(ctx, secret, "", 1, 1); terr == nil {
				dto.TargetCount = int(total)
			}
		}
	}
	groupCounts, derr := s.dimRepo.CountEnabledByGroupCode(ctx, domain.SourceConversation)
	if derr != nil {
		return nil, fmt.Errorf("count enabled dimensions: %w", derr)
	}
	dto.DimensionBaseCount = groupCounts[domain.GroupBase]
	dto.DimensionUpperCount = groupCounts[domain.GroupUpper]
	return dto, nil
}

// Targets 名单查询：返回创建时点完整名单快照与评估时段，供重新发起预填（specs §4.1.3）。
func (s *assessmentBatchService) Targets(ctx context.Context, batchID int64) (*BatchTargetsDTO, error) {
	b, err := s.batchRepo.GetByID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}
	if b == nil {
		return nil, NewError(errcode.BatchNotFound)
	}
	names := parseTargetNames(b.TargetNamesJSON)
	return &BatchTargetsDTO{
		BatchID:     b.ID,
		TargetMode:  b.TargetMode,
		Names:       names,
		Total:       len(names),
		PeriodStart: b.PeriodStartAt.Local().Format("2006-01-02"),
		PeriodEnd:   b.PeriodEndAt.Local().Format("2006-01-02"),
	}, nil
}

// Failures 失败明细：打开时刻只读快照（specs §4.3.4 规则1），FailedCount 取批次行值；
// ErrorSummary 取人员行自身值（FailWholeBatch 已把批次级原因写入每行，不再重复覆盖）。
func (s *assessmentBatchService) Failures(ctx context.Context, batchID int64) (*BatchFailuresDTO, error) {
	b, err := s.batchRepo.GetByID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}
	if b == nil {
		return nil, NewError(errcode.BatchNotFound)
	}
	persons, err := s.batchRepo.ListFailedByBatch(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("list failed persons: %w", err)
	}
	list := make([]BatchFailureItem, 0, len(persons))
	for i := range persons {
		list = append(list, BatchFailureItem{
			TokenName:    persons[i].TokenName,
			ErrorSummary: persons[i].ErrorSummary,
		})
	}
	return &BatchFailuresDTO{
		BatchID:     b.ID,
		BatchNo:     b.BatchNo,
		FailedCount: b.FailedCount,
		TotalCount:  b.TotalCount,
		PeriodStart: b.PeriodStartAt.Local().Format("2006-01-02"),
		PeriodEnd:   b.PeriodEndAt.Local().Format("2006-01-02"),
		List:        list,
	}, nil
}

// parseTargetNames 解析名单快照 JSON，空或非法均降级空数组（前端稳定序列化契约）。
func parseTargetNames(raw string) []string {
	names := []string{}
	if raw == "" {
		return names
	}
	if err := json.Unmarshal([]byte(raw), &names); err != nil || names == nil {
		slog.Warn("parse target_names_json degraded", "err", err)
		return []string{}
	}
	return names
}

// briefNames 名单摘要：specified 取前 2 人名，all 模式空数组（前端渲染「全员」，03 A1/A3）。
func briefNames(names []string, targetMode string) []string {
	if targetMode != domain.BatchTargetSpecified {
		return []string{}
	}
	if len(names) > 2 {
		return names[:2]
	}
	return names
}

// progressPercent 进度百分比向下取整，total=0 时 0（后端兜底除零，03 A1）。
func progressPercent(evaluated, total int) int {
	if total <= 0 {
		return 0
	}
	return evaluated * 100 / total
}

// validBatchTriggerType / validBatchStatus 枚举校验，空串视为全部放行（03 A1 请求参数）。
func validBatchTriggerType(v string) bool {
	switch v {
	case "", domain.BatchTriggerScheduled, domain.BatchTriggerManual:
		return true
	}
	return false
}

func validBatchStatus(v string) bool {
	switch v {
	case "", domain.BatchStatusRunning, domain.BatchStatusSuccess, domain.BatchStatusPartialFailed, domain.BatchStatusFailed:
		return true
	}
	return false
}
