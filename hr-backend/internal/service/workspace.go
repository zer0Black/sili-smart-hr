// workspace 工作台域业务层：W1 全量聚合编排（specs P2_WRK_001 §5.1，03 W1）。
// 任一数据源失败对应区块空标记降级，接口整体不失败（specs §5.1.4 规则2，03 §1.3 降级矩阵）。
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/repository"
)

// 工作台自有计算常量（03 §4.3）：与 dashboard.go 同值常量（trendWindowSize 等）
// 同源引用，此处只新增两线。
const (
	attentionLimitPerCategory = 5 // 关注人群每类配额（specs §4.1.4 规则3，不跨类补足）
	weakScoreLine             = 60 // 短板人群模块总分判定线（取整后 < 60，与 lowScoreLine 同值不同语义）
)

// WorkspaceService 工作台域只读聚合服务（specs §5.1，03 W1）。
type WorkspaceService interface {
	// Overview 查询工作台全量（03 W1）：四区块一次返回，任一数据源失败
	// 对应区块空标记降级，接口整体不失败（specs §5.1.4 规则2）。
	Overview(ctx context.Context) (*WorkspaceDTO, error)
}

// WorkspaceDTO W1 响应 data（03 W1 响应字段）。
type WorkspaceDTO struct {
	Batch         *WorkspaceBatchDTO      `json:"batch"`
	CurrentPeriod *WorkspacePeriodDTO     `json:"current_period"`
	Trend         *WorkspaceTrendDTO      `json:"trend"`
	Profile       *WorkspaceProfileDTO    `json:"profile"`
	Attention     []WorkspaceAttentionRow `json:"attention"`
}

// WorkspaceBatchDTO 态势卡（恒返回对象，内部字段按数据源独立降级，03 §1.3）。
type WorkspaceBatchDTO struct {
	Status        *string `json:"status"`
	NextTriggerAt *string `json:"next_trigger_at"`
	AlertCount    *int    `json:"alert_count"`
	OverdueCount  *int    `json:"overdue_count"`
	DataUpdatedAt *string `json:"data_updated_at"`
}

// WorkspacePeriodDTO 区间条目（含止日展示口径，03 §1.9）。
type WorkspacePeriodDTO struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

// WorkspaceTrendDTO 演进区（specs §4.1.2 B）。
type WorkspaceTrendDTO struct {
	Periods  []WorkspacePeriodDTO       `json:"periods"`
	Series   []WorkspaceTrendSeriesDTO  `json:"series"`   // 恒 AI_USAGE/AI_MGMT 两行
	Activity *WorkspaceTrendActivityDTO `json:"activity"` // 活跃率与未使用两卡
}

// WorkspaceTrendSeriesDTO 单模块综合分序列（03 §1.8 同 F10 A2 composite 口径）。
type WorkspaceTrendSeriesDTO struct {
	Module       string `json:"module"`
	Scores       []*int `json:"scores"`
	CurrentScore *int   `json:"current_score"`
	ChangeVsPrev *int   `json:"change_vs_prev"`
}

// WorkspaceTrendActivityDTO 活跃率与未使用两卡（03 §1.6 pp 口径）。
type WorkspaceTrendActivityDTO struct {
	ActiveRatio    float64  `json:"active_ratio"`
	ActiveChangePP *float64 `json:"active_change_pp"`
	UnusedCount    int      `json:"unused_count"`
	UnusedChange   *int     `json:"unused_change"`
}

// WorkspaceProfileDTO 画像速览区（03 §1.7 精简投影）。
type WorkspaceProfileDTO struct {
	Modules    []WorkspaceModuleDTO   `json:"modules"`    // 不含 low_ratio，由 buildDashboardModules 结果映射
	Enneagram  *WorkspaceEnneagramDTO `json:"enneagram"`
	Weaknesses []WorkspaceWeaknessDTO `json:"weaknesses"` // 两模块 is_weakness 合并清单
	Suggestion WorkspaceSuggestionDTO `json:"suggestion"` // 恒返回对象 status/summary
}

// WorkspaceModuleDTO 雷达卡条目（03 §1.7 裁剪）。
type WorkspaceModuleDTO struct {
	Module     string                `json:"module"`
	Dimensions []WorkspaceDimItemDTO `json:"dimensions"`
	OverallAvg *int                  `json:"overall_avg"`
}

// WorkspaceDimItemDTO 维度条目（仅 code/name/avg_score/is_weakness）。
type WorkspaceDimItemDTO struct {
	DimensionCode string `json:"dimension_code"`
	DimensionName string `json:"dimension_name"`
	AvgScore      *int   `json:"avg_score"`
	IsWeakness    bool   `json:"is_weakness"`
}

// WorkspaceEnneagramDTO 九型速览（恒 9 项，无覆盖率字段，03 §1.7）。
type WorkspaceEnneagramDTO struct {
	Distribution  []DashboardEnneagramItem `json:"distribution"`
	DominantType  string                   `json:"dominant_type"`
	DominantRatio float64                  `json:"dominant_ratio"`
}

// WorkspaceWeaknessDTO 共性短板标签条目（specs §4.1.4 规则4 同源判定）。
type WorkspaceWeaknessDTO struct {
	Module        string  `json:"module"`
	DimensionCode string  `json:"dimension_code"`
	DimensionName string  `json:"dimension_name"`
	LowRatio      float64 `json:"low_ratio"`
}

// WorkspaceSuggestionDTO 研判摘要（裁剪只留 status/summary，03 §1.7）。
type WorkspaceSuggestionDTO struct {
	Status  string `json:"status"`  // generated/generating/failed/none
	Summary string `json:"summary"` // 非 generated 空串
}

// WorkspaceAttentionRow 关注表行（specs §4.1.2 D、03 §1.5）。
type WorkspaceAttentionRow struct {
	StaffName       string                   `json:"staff_name"`
	Category        string                   `json:"category"` // weak/unused
	ActivityLevel   string                   `json:"activity_level"`
	AIUsageScore    *int                     `json:"ai_usage_score"`
	AIMGMTScore     *int                     `json:"ai_mgmt_score"`
	WeakModules     []WorkspaceWeakModuleDTO `json:"weak_modules"`     // 短板人群载体，未使用空数组
	DaysSinceActive *int                     `json:"days_since_active"` // 未使用人群载体，短板 null
}

// WorkspaceWeakModuleDTO 短板行关注原因条目（specs §4.1.2 D）。
type WorkspaceWeakModuleDTO struct {
	Module   string   `json:"module"`
	Score    int      `json:"score"`
	WeakDims []string `json:"weak_dims"`
}

type workspaceService struct {
	queries     repository.WorkspaceQueryRepository
	dashboard   repository.DashboardQueryRepository
	suggestions repository.TeamTrainingSuggestionRepository
	dims        repository.DimensionRepository
	configRepo  repository.AssessmentConfigRepository
	staffs      userapiClient
	secretRepo  repository.IntegrationSecretRepository
	encKey      []byte
	now         func() time.Time
}

// NewWorkspaceService 构造工作台域 service（encKey 绑定 NewLLMEncKey，now 生产传 time.Now）。
func NewWorkspaceService(
	queries repository.WorkspaceQueryRepository,
	dashboard repository.DashboardQueryRepository,
	suggestions repository.TeamTrainingSuggestionRepository,
	dims repository.DimensionRepository,
	configRepo repository.AssessmentConfigRepository,
	staffs userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	now func() time.Time,
) WorkspaceService {
	return &workspaceService{
		queries:     queries,
		dashboard:   dashboard,
		suggestions: suggestions,
		dims:        dims,
		configRepo:  configRepo,
		staffs:      staffs,
		secretRepo:  secretRepo,
		encKey:      encKey,
		now:         now,
	}
}

// Overview 编排（03 §4.1 六步）：态势卡 → 演进区 → 画像区 → 关注人群 → 名单 → 组装。
// 各数据源独立 try-degrade，任一失败记 ERROR 后对应区块空标记，接口整体不失败。
func (s *workspaceService) Overview(ctx context.Context) (*WorkspaceDTO, error) {
	dto := &WorkspaceDTO{Batch: &WorkspaceBatchDTO{}}

	// 名单（03 §4.1 步5 前置拉取）：失败仅 trend.activity 与 attention 降级。
	names, namesOK := s.fetchStaffNames(ctx)

	bounds, periodsErr := s.dashboard.ListPeriods(ctx)
	if periodsErr != nil {
		slog.Error("workspace degrade: list periods", "err", periodsErr)
		bounds = nil
	}

	batch := s.buildBatch(ctx, bounds)
	dto.Batch = batch

	if len(bounds) == 0 {
		// 无落库区间：下游三区块空态（specs §4.1.4 规则4/规则1）；批次表空时 status 已 nil。
		return dto, nil
	}
	cur := bounds[0]
	dto.CurrentPeriod = &WorkspacePeriodDTO{
		PeriodStart: cur.StartAt.Local().Format(layoutDate),
		PeriodEnd:   cur.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
	}

	var trend *WorkspaceTrendDTO
	var profile *WorkspaceProfileDTO
	var attention []WorkspaceAttentionRow

	// 维度配置：trend.series 与 profile 共用；失败两区块同步降级（03 §1.3）。
	dims, dimsErr := s.dims.ListAll(ctx)
	if dimsErr != nil {
		slog.Error("workspace degrade: load dimensions", "err", dimsErr)
	} else {
		window := workspaceTrendWindow(bounds)
		trend = s.buildTrend(ctx, window, names, namesOK)
		profile = s.buildProfile(ctx, cur, dims)
		if namesOK {
			attention = s.buildAttention(ctx, cur, bounds, dims, names)
		}
	}
	dto.Trend, dto.Profile, dto.Attention = trend, profile, attention
	return dto, nil
}

// fetchStaffNames 全量名单去重（profile.assembleRows 同款）。失败返回 namesOK=false
// 由调用方决定 activity/attention 降级范围。
func (s *workspaceService) fetchStaffNames(ctx context.Context) ([]string, bool) {
	secret, err := resolveUserapiSecret(ctx, s.secretRepo, s.encKey)
	if err != nil {
		slog.Error("workspace degrade: resolve userapi secret", "err", err)
		return nil, false
	}
	names, err := userapi.FetchAllStaff(ctx, s.staffs, secret)
	if err != nil {
		slog.Error("workspace degrade: fetch staff names", "err", err)
		return nil, false
	}
	return names, true
}

// buildBatch 态势卡（03 §4.1 步1，各源独立降级）：next_trigger_at 推算、批次状态定位
//（§1.4 两级回退）、alert 存在性、逾期计数、数据更新时间。
func (s *workspaceService) buildBatch(ctx context.Context, bounds []repository.PeriodBound) *WorkspaceBatchDTO {
	b := &WorkspaceBatchDTO{}

	// a. 下次跑批时点（口径同 F6 Plan）。
	if cfg, err := s.configRepo.Get(ctx); err != nil {
		slog.Error("workspace degrade: get assessment config", "err", err)
	} else if next, nerr := pipeline.NextTriggerAt(s.now(), cfg.Period, cfg.TriggerTime); nerr != nil {
		slog.Error("workspace degrade: next trigger at", "err", nerr)
	} else {
		v := next.Local().Format(layoutDateTime)
		b.NextTriggerAt = &v
	}

	// b. 批次表 triggered_at 最新行（空态判定与无区间回退源）。
	latest, lerr := s.queries.ListLatestBatch(ctx)
	if lerr != nil {
		slog.Error("workspace degrade: list latest batch", "err", lerr)
	}

	if len(bounds) > 0 {
		cur := bounds[0]
		rows, rerr := s.queries.ListByPeriodBounds(ctx, cur.StartAt.Unix(), cur.EndAt.Unix())
		if rerr != nil {
			slog.Error("workspace degrade: list batches by period", "err", rerr)
		} else if st := latestFinishedStatus(rows); st != nil {
			b.Status = st
		} else if st := latestTriggeredStatus(rows); st != nil {
			b.Status = st
		}
		// alert_count：区间批次 ID 集整体存在性（specs §4.1.4 规则2）。
		ids := make([]int64, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		exists, aerr := s.queries.ExistsAlertByBatchIDs(ctx, ids)
		switch {
		case aerr != nil:
			slog.Error("workspace degrade: exists alert", "err", aerr)
		case exists:
			v := 1
			b.AlertCount = &v
		default:
			v := 0
			b.AlertCount = &v
		}
		// data_updated_at：批次终态优先，无批次回退区间右开界（03 W1 字段说明）。
		if fin, ferr := s.dashboard.FindLatestFinishedAt(ctx, cur.StartAt.Unix(), cur.EndAt.Unix()); ferr != nil {
			slog.Error("workspace degrade: find latest finished at", "err", ferr)
		} else {
			b.DataUpdatedAt = dashboardUpdatedAt(fin, cur.EndAt)
		}
	} else {
		// 三表无区间：回退行承接 status，alert 恒 0，data_updated_at 取回退行 finished_at（03 §1.4）。
		if latest != nil {
			st := latest.Status
			b.Status = &st
		}
		v := 0
		b.AlertCount = &v
		if latest != nil && latest.FinishedAt != nil {
			b.DataUpdatedAt = dashboardUpdatedAt(latest.FinishedAt, time.Time{})
		}
	}

	// d. 逾期任务计数。
	if n, err := s.queries.CountExpiredTasks(ctx); err != nil {
		slog.Error("workspace degrade: count expired tasks", "err", err)
	} else {
		v := int(n)
		b.OverdueCount = &v
	}
	return b
}

// latestFinishedStatus 区间有终态批次时取 finished_at 最新非空行状态（03 §1.4 步2）。
func latestFinishedStatus(rows []domain.AssessmentBatch) *string {
	var best *domain.AssessmentBatch
	for i := range rows {
		r := &rows[i]
		if !isFinalBatchStatus(r.Status) || r.FinishedAt == nil {
			continue
		}
		if best == nil || r.FinishedAt.After(*best.FinishedAt) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	st := best.Status
	return &st
}

// latestTriggeredStatus 区间无终态批次时取 triggered_at 最新行状态（03 §1.4 步3）。
func latestTriggeredStatus(rows []domain.AssessmentBatch) *string {
	var best *domain.AssessmentBatch
	for i := range rows {
		r := &rows[i]
		if best == nil || r.TriggeredAt.After(best.TriggeredAt) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	st := best.Status
	return &st
}

func isFinalBatchStatus(status string) bool {
	switch status {
	case domain.BatchStatusSuccess, domain.BatchStatusPartialFailed, domain.BatchStatusFailed:
		return true
	}
	return false
}

// workspaceTrendWindow 前 8 期窗口反转旧到新（同 dashboard.Trend）。
// 复制出新切片：bounds 后续步骤仍按新到旧消费（关注人群取最旧区间），不可原地反转。
func workspaceTrendWindow(bounds []repository.PeriodBound) []repository.PeriodBound {
	window := bounds
	if len(window) > trendWindowSize {
		window = window[:trendWindowSize]
	}
	out := make([]repository.PeriodBound, len(window))
	copy(out, window)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// buildTrend 演进区（03 §4.1 步2）：series 逐期综合分 + activity 三态与环比。
// 维度窗口行查询失败返回 nil（trend 整区块降级，03 §1.3 dimension_scores 行）。
func (s *workspaceService) buildTrend(ctx context.Context, window []repository.PeriodBound, names []string, namesOK bool) *WorkspaceTrendDTO {
	trend := &WorkspaceTrendDTO{Periods: make([]WorkspacePeriodDTO, 0, len(window))}
	for _, b := range window {
		trend.Periods = append(trend.Periods, workspacePeriodItem(b))
	}

	rows, err := s.dashboard.ListDimScoresByPeriods(ctx, window)
	if err != nil {
		slog.Error("workspace degrade: list dim scores by periods", "err", err)
		return nil
	}
	trend.Series = make([]WorkspaceTrendSeriesDTO, 0, 2)
	for _, m := range []struct{ module, source string }{
		{domain.ModuleAIUsage, domain.ScoreSourceConversation},
		{domain.ModuleAIMgmt, domain.ScoreSourceActiveTest},
	} {
		trend.Series = append(trend.Series, workspaceSeries(window, rows, m.module, m.source))
	}

	if !namesOK {
		return trend
	}
	activity, err := s.buildTrendActivity(ctx, window, len(names))
	if err != nil {
		slog.Error("workspace degrade: build trend activity", "err", err)
		return trend
	}
	trend.Activity = activity
	return trend
}

// workspaceSeries 单模块逐期综合分（03 §1.8）：参与维度未取整均分算术平均取整，
// < 3 人维度置空剔除，全置空该期 nil；模块全窗口无行 scores 全 nil。
func workspaceSeries(window []repository.PeriodBound, rows []domain.DimensionScore, module, source string) WorkspaceTrendSeriesDTO {
	series := WorkspaceTrendSeriesDTO{Module: module, Scores: make([]*int, len(window))}
	hasRow := false
	for _, r := range rows {
		if r.Source == source {
			hasRow = true
			break
		}
	}
	if hasRow {
		for i, b := range window {
			aggs := aggregateDimScores(rowsOfBound(rows, b), source)
			if len(aggs) == 0 {
				continue
			}
			sum := 0.0
			for _, a := range aggs {
				sum += a.avg
			}
			v := int(math.Round(sum / float64(len(aggs))))
			series.Scores[i] = &v
		}
	}
	if n := len(window); n > 0 {
		series.CurrentScore = series.Scores[n-1]
		if n >= 2 && series.Scores[n-1] != nil && series.Scores[n-2] != nil {
			d := *series.Scores[n-1] - *series.Scores[n-2]
			series.ChangeVsPrev = &d
		}
	}
	return series
}

// buildTrendActivity 活跃率与未使用两卡（03 §1.6）：active_ratio 一位小数、pp 环比
// 两期占比均 round1 后相减再 round1、unused 计数差 int。
func (s *workspaceService) buildTrendActivity(ctx context.Context, window []repository.PeriodBound, staffTotal int) (*WorkspaceTrendActivityDTO, error) {
	cur := window[len(window)-1]
	curRows, err := s.dashboard.ListActivityByPeriod(ctx, cur.StartAt.Unix(), cur.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("list activity stats: %w", err)
	}
	active, unused := countWorkspaceActivity(curRows, staffTotal)
	dto := &WorkspaceTrendActivityDTO{
		ActiveRatio: round1Percent(active, staffTotal),
		UnusedCount: unused,
	}
	if len(window) < 2 {
		return dto, nil
	}
	prev := window[len(window)-2]
	prevRows, err := s.dashboard.ListActivityByPeriod(ctx, prev.StartAt.Unix(), prev.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("list prev activity stats: %w", err)
	}
	pActive, pUnused := countWorkspaceActivity(prevRows, staffTotal)
	// pp 差：两期占比均一位小数精度参与计算，差值再取一位小数（03 §1.6）。
	pp := math.Round((round1Percent(active, staffTotal)-round1Percent(pActive, staffTotal))*10) / 10
	dto.ActiveChangePP = &pp
	du := unused - pUnused
	dto.UnusedChange = &du
	return dto, nil
}

// countWorkspaceActivity 三态计数（buildActivity 同口径）：active 计 active 行，
// unused = unused 行 + 名单无统计行；无统计行计数为负时钳 0。
func countWorkspaceActivity(rows []domain.ActivityStat, staffTotal int) (active, unused int) {
	seen := map[string]struct{}{}
	for _, r := range rows {
		switch r.ActiveLevel {
		case domain.ActiveLevelActive:
			active++
		case domain.ActiveLevelUnused:
			unused++
		}
		seen[r.TokenName] = struct{}{}
	}
	noRow := staffTotal - len(seen)
	if noRow > 0 {
		unused += noRow
	}
	return active, unused
}

// buildProfile 画像速览区（03 §4.1 步3）：雷达 + 共性短板 + 九型 + 研判。
func (s *workspaceService) buildProfile(ctx context.Context, cur repository.PeriodBound, dims []domain.Dimension) *WorkspaceProfileDTO {
	rows, err := s.dashboard.ListDimScoresByPeriod(ctx, cur.StartAt.Unix(), cur.EndAt.Unix())
	if err != nil {
		slog.Error("workspace degrade: list dim scores by period", "err", err)
		return nil
	}
	profile := &WorkspaceProfileDTO{
		Modules:    make([]WorkspaceModuleDTO, 0, 2),
		Weaknesses: []WorkspaceWeaknessDTO{},
		Suggestion: WorkspaceSuggestionDTO{Status: "none"},
	}
	// 雷达与短板清单：buildDashboardModules 同口径组装，weaknessSet 同源补 low_ratio。
	for _, m := range []struct{ module, source string }{
		{domain.ModuleAIUsage, domain.ScoreSourceConversation},
		{domain.ModuleAIMgmt, domain.ScoreSourceActiveTest},
	} {
		aggs := aggregateDimScores(rows, m.source)
		weak := weaknessSet(aggs)
		item := WorkspaceModuleDTO{Module: m.module, Dimensions: []WorkspaceDimItemDTO{}}
		for _, d := range dims {
			if !d.Enabled || d.ModuleCode != m.module {
				continue
			}
			dim := WorkspaceDimItemDTO{DimensionCode: d.Code, DimensionName: d.Name}
			if a, ok := aggs[d.Code]; ok {
				v := int(math.Round(a.avg))
				dim.AvgScore = &v
				_, dim.IsWeakness = weak[d.Code]
				if _, hit := weak[d.Code]; hit {
					profile.Weaknesses = append(profile.Weaknesses, WorkspaceWeaknessDTO{
						Module: m.module, DimensionCode: d.Code, DimensionName: d.Name,
						LowRatio: round1Percent(a.lowCount, a.count),
					})
				}
			}
			item.Dimensions = append(item.Dimensions, dim)
		}
		profile.Modules = append(profile.Modules, item)
	}
	// overall_avg：各模块各维度未取整均分的算术平均取整，任一置空 nil（同 buildDashboardModules）。
	fillWorkspaceOverallAvg(profile.Modules, aggsByModuleOf(rows))

	// 九型：ListAllLatestScored 免名单取数（03 §4.2），分布分母为 scored 本身。
	scored, err := s.queries.ListAllLatestScored(ctx)
	if err != nil {
		slog.Error("workspace degrade: list all latest scored", "err", err)
	} else if enn := workspaceEnneagram(scored); enn != nil {
		profile.Enneagram = enn
	}

	// 研判：FindLatest 同 F10，失败降级 none/空串（03 §1.3）。
	if sug, err := s.suggestions.FindLatest(ctx); err != nil {
		slog.Error("workspace degrade: find latest suggestion", "err", err)
	} else {
		full := buildSuggestionDTO(sug)
		profile.Suggestion = WorkspaceSuggestionDTO{Status: full.Status, Summary: full.Summary}
	}
	return profile
}

// aggsByModuleOf 按模块分流的聚合结果（雷达 overall_avg 计算）。
func aggsByModuleOf(rows []domain.DimensionScore) map[string]map[string]dimAvg {
	return map[string]map[string]dimAvg{
		domain.ModuleAIUsage: aggregateDimScores(rows, domain.ScoreSourceConversation),
		domain.ModuleAIMgmt:  aggregateDimScores(rows, domain.ScoreSourceActiveTest),
	}
}

// fillWorkspaceOverallAvg 补各模块 overall_avg（同 buildDashboardModules 参考线口径）。
func fillWorkspaceOverallAvg(modules []WorkspaceModuleDTO, aggsByModule map[string]map[string]dimAvg) {
	for i := range modules {
		m := &modules[i]
		aggs := aggsByModule[m.Module]
		sum, cnt, complete := 0.0, 0, true
		for _, d := range m.Dimensions {
			cnt++
			if a, ok := aggs[d.DimensionCode]; ok {
				sum += a.avg
			} else {
				complete = false
			}
		}
		if cnt > 0 && complete {
			v := int(math.Round(sum / float64(cnt)))
			m.OverallAvg = &v
		}
	}
}

// workspaceEnneagram 九型速览（口径同 dashboard.buildEnneagram：恒 9 项、
// 并列按型别升序；分布占比分母为 scored_count 本身，无覆盖率字段）。
func workspaceEnneagram(byStaff map[string]domain.AssessmentTestResult) *WorkspaceEnneagramDTO {
	counts := map[string]int{}
	scored := 0
	for _, res := range byStaff {
		if res.MainType == "" {
			continue
		}
		counts[res.MainType]++
		scored++
	}
	if scored == 0 {
		return nil
	}
	distribution := make([]DashboardEnneagramItem, 0, 9)
	for i := 1; i <= 9; i++ {
		typ := strconv.Itoa(i)
		c := counts[typ]
		distribution = append(distribution, DashboardEnneagramItem{Type: typ, Count: c, Ratio: round1Percent(c, scored)})
	}
	dom := distribution[0]
	for _, item := range distribution[1:] {
		if item.Count > dom.Count {
			dom = item
		}
	}
	return &WorkspaceEnneagramDTO{Distribution: distribution, DominantType: dom.Type, DominantRatio: dom.Ratio}
}

// buildAttention 关注人群（03 §4.1 步4、03 §1.5）：短板前 5 升序在前 + 未使用
// 前 5 天数降序在后。任一数据源失败返回 nil（attention 区块空标记，03 §1.3）。
func (s *workspaceService) buildAttention(ctx context.Context, cur repository.PeriodBound,
	bounds []repository.PeriodBound, dims []domain.Dimension, names []string) []WorkspaceAttentionRow {
	aggRows, err := s.dashboard.ListModuleAggScoresByPeriods(ctx, []repository.PeriodBound{cur})
	if err != nil {
		slog.Error("workspace degrade: list module agg scores", "err", err)
		return nil
	}
	// 本期维度行（weak_dims 载体，与聚合行同期）。
	dimRows, err := s.dashboard.ListDimScoresByPeriod(ctx, cur.StartAt.Unix(), cur.EndAt.Unix())
	if err != nil {
		slog.Error("workspace degrade: list dim scores for attention", "err", err)
		return nil
	}
	actRows, err := s.dashboard.ListActivityByPeriod(ctx, cur.StartAt.Unix(), cur.EndAt.Unix())
	if err != nil {
		slog.Error("workspace degrade: list activity for attention", "err", err)
		return nil
	}
	allAct, err := s.queries.ListAllActivity(ctx)
	if err != nil {
		slog.Error("workspace degrade: list all activity", "err", err)
		return nil
	}

	rows := make([]WorkspaceAttentionRow, 0, attentionLimitPerCategory*2)
	rows = append(rows, buildWeakCandidates(aggRows, dimRows, actRows)...)
	rows = append(rows, buildUnusedCandidates(names, actRows, allAct, cur, bounds[len(bounds)-1])...)
	return rows
}

// buildWeakCandidates 短板人群：module_score 取整 < 60 入选，按取整最低总分
//（仅低于 60 的模块参与）升序前 5；weak_dims 按模块复用 buildShortboardSets
// 同口径（该人该模块短板集合，03 §1.5）。
func buildWeakCandidates(aggRows []domain.AggregateScore, dimRows []domain.DimensionScore,
	actRows []domain.ActivityStat) []WorkspaceAttentionRow {
	type cand struct {
		staff  string
		min    int
		scores map[string]int // module → 取整总分
	}
	levelBy := map[string]string{}
	for _, r := range actRows {
		levelBy[r.TokenName] = r.ActiveLevel
	}
	// 个人短板集合：buildShortboardSets 需要 (人,模块) 两键索引。
	aggBy := make(map[profileModKey]domain.AggregateScore, len(aggRows))
	for _, r := range aggRows {
		aggBy[profileModKey{r.TokenName, r.Module}] = r
	}
	dimsBy := make(map[profileModKey][]domain.DimensionScore)
	for _, r := range dimRows {
		k := profileModKey{r.TokenName, r.Module}
		dimsBy[k] = append(dimsBy[k], r)
	}
	// 个人短板集合按模块拆分：weak_dims 作用域是该人该模块（03 §1.5），
	// 逐模块过滤 (人,模块) 子集复用 buildShortboardSets 同口径。
	shortByModule := map[string]map[string]map[string]struct{}{}
	for _, m := range []string{domain.ModuleAIUsage, domain.ModuleAIMgmt} {
		aggOne := make(map[profileModKey]domain.AggregateScore)
		for k, v := range aggBy {
			if k.module == m {
				aggOne[k] = v
			}
		}
		dimsOne := make(map[profileModKey][]domain.DimensionScore)
		for k, v := range dimsBy {
			if k.module == m {
				dimsOne[k] = v
			}
		}
		shortByModule[m] = buildShortboardSets(aggOne, dimsOne)
	}

	cands := map[string]*cand{}
	for _, r := range aggRows {
		if r.ModuleScore == nil {
			continue
		}
		sc := int(math.Round(*r.ModuleScore))
		if sc >= weakScoreLine {
			continue
		}
		c := cands[r.TokenName]
		if c == nil {
			c = &cand{staff: r.TokenName, min: sc, scores: map[string]int{}}
			cands[r.TokenName] = c
		}
		if sc < c.min {
			c.min = sc
		}
		c.scores[r.Module] = sc
	}
	list := make([]*cand, 0, len(cands))
	for _, c := range cands {
		list = append(list, c)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].min != list[j].min {
			return list[i].min < list[j].min
		}
		return list[i].staff < list[j].staff
	})
	if len(list) > attentionLimitPerCategory {
		list = list[:attentionLimitPerCategory]
	}
	out := make([]WorkspaceAttentionRow, 0, len(list))
	for _, c := range list {
		row := WorkspaceAttentionRow{
			StaffName:     c.staff,
			Category:      "weak",
			ActivityLevel: levelBy[c.staff],
			WeakModules:   []WorkspaceWeakModuleDTO{},
		}
		if row.ActivityLevel == "" {
			row.ActivityLevel = domain.ActiveLevelUnused
		}
		for _, m := range []string{domain.ModuleAIUsage, domain.ModuleAIMgmt} {
			sc, ok := c.scores[m]
			if !ok {
				continue
			}
			dims := []string{}
			if set, hit := shortByModule[m][c.staff]; hit {
				for code := range set {
					dims = append(dims, code)
				}
			}
			sort.Strings(dims)
			row.WeakModules = append(row.WeakModules, WorkspaceWeakModuleDTO{Module: m, Score: sc, WeakDims: dims})
		}
		if u, ok := c.scores[domain.ModuleAIUsage]; ok {
			row.AIUsageScore = &u
		}
		if g, ok := c.scores[domain.ModuleAIMgmt]; ok {
			row.AIMGMTScore = &g
		}
		out = append(out, row)
	}
	return out
}

// buildUnusedCandidates 未使用人群：本期 unused 行 + 名单无统计行合成；
// 天数 = 最新落库区间 period_end_at 与该人最近非未使用行 period_end_at 两端
// 本地零点 Unix 秒差 / 86400 取整，无任何统计行者自并集最旧区间起点起算；降序前 5。
func buildUnusedCandidates(names []string, curAct []domain.ActivityStat, allAct []domain.ActivityStat,
	cur, oldest repository.PeriodBound) []WorkspaceAttentionRow {
	curLevelBy := map[string]string{}
	for _, r := range curAct {
		curLevelBy[r.TokenName] = r.ActiveLevel
	}
	// 每人最近非未使用行 period_end_at（跨期聚合）。
	lastActive := map[string]time.Time{}
	for _, r := range allAct {
		if r.ActiveLevel == domain.ActiveLevelUnused {
			continue
		}
		if prev, ok := lastActive[r.TokenName]; ok && !r.PeriodEndAt.After(prev) {
			continue
		}
		lastActive[r.TokenName] = r.PeriodEndAt
	}
	endRef := localMidnight(cur.EndAt)
	oldestRef := localMidnight(oldest.StartAt)

	type cand struct {
		staff string
		days  int
	}
	cands := []cand{}
	for _, name := range names {
		if lvl := curLevelBy[name]; lvl != "" && lvl != domain.ActiveLevelUnused {
			continue
		}
		ref := oldestRef
		if at, ok := lastActive[name]; ok {
			ref = localMidnight(at)
		}
		cands = append(cands, cand{staff: name, days: int(endRef.Sub(ref).Hours() / 24)})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].days != cands[j].days {
			return cands[i].days > cands[j].days
		}
		return cands[i].staff < cands[j].staff
	})
	if len(cands) > attentionLimitPerCategory {
		cands = cands[:attentionLimitPerCategory]
	}
	out := make([]WorkspaceAttentionRow, 0, len(cands))
	for _, c := range cands {
		out = append(out, WorkspaceAttentionRow{
			StaffName:       c.staff,
			Category:        "unused",
			ActivityLevel:   domain.ActiveLevelUnused,
			WeakModules:     []WorkspaceWeakModuleDTO{},
			DaysSinceActive: &c.days,
		})
	}
	return out
}

// localMidnight 时刻的本地日零点。
func localMidnight(at time.Time) time.Time {
	l := at.Local()
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.Local)
}

// workspacePeriodItem 区间条目（end 右开界，展示止日前一日，03 §1.9 含止日口径）。
func workspacePeriodItem(b repository.PeriodBound) WorkspacePeriodDTO {
	return WorkspacePeriodDTO{
		PeriodStart: b.StartAt.Local().Format(layoutDate),
		PeriodEnd:   b.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
	}
}
