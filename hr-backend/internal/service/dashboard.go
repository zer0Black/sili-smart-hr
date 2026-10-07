// dashboard 团队看板域业务层：A1 看板全量聚合与 A2 能力逐期趋势（specs P2_TMD_001 §4.1/§4.2/§5.2）。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// 计算常量（03 §4.4：随源码发版的确定性常量，非在线配置）。
const (
	trendWindowSize  = 8  // 趋势观察窗口期数
	avgMinPeople     = 3  // 维度均分最小有数据人数（< 3 置空）
	weaknessMaxCount = 2  // 共性短板每模块最低维数
	weaknessHardCap  = 3  // 并列短板至多项数
	lowScoreLine     = 60 // 低分占比分数线
)

// DashboardService 团队看板域只读聚合服务（specs §5.2，03 A1/A2）。
type DashboardService interface {
	// Overview 查询看板全量（03 A1）：区间列表 + 活跃度三态与环比 + 两模块维度均分与
	// 共性短板 + 九型快照 + 培训建议。
	Overview(ctx context.Context, periodStart, periodEnd string) (*DashboardOverviewDTO, error)
	// Trend 查询能力逐期趋势（03 A2）：type 映射单模块近 8 期序列。
	Trend(ctx context.Context, abilityType string) (*DashboardTrendDTO, error)
}

// DashboardOverviewDTO A1 响应 data（03 A1 响应字段）。
type DashboardOverviewDTO struct {
	Periods        []DashboardPeriodItem  `json:"periods"`
	SelectedPeriod *DashboardPeriodItem   `json:"selected_period"`
	DataUpdatedAt  *string                `json:"data_updated_at"`
	StaffTotal     int                    `json:"staff_total"`
	Activity       *DashboardActivityDTO  `json:"activity"`
	Modules        []DashboardModuleDTO   `json:"modules"`
	Enneagram      *DashboardEnneagramDTO `json:"enneagram"`
	Suggestion     DashboardSuggestionDTO `json:"suggestion"`
}

// DashboardPeriodItem 区间条目（含止日展示口径，03 §1.3）。
type DashboardPeriodItem struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	IsCurrent   bool   `json:"is_current"`
}

// DashboardActivityDTO 活跃度三态与环比（specs §4.1.4 规则2）。
type DashboardActivityDTO struct {
	Active  DashboardCountRatio `json:"active"`
	LowFreq DashboardCountRatio `json:"low_freq"`
	Unused  DashboardCountRatio `json:"unused"`
	Mom     *DashboardMomDTO    `json:"mom"`
}

// DashboardCountRatio 人数与占全员比例（一位小数）。
type DashboardCountRatio struct {
	Count int     `json:"count"`
	Ratio float64 `json:"ratio"`
}

// DashboardMomDTO 环比计数差（本期减上一落库区间）。
type DashboardMomDTO struct {
	ActiveChange int `json:"active_change"`
	UnusedChange int `json:"unused_change"`
}

// DashboardModuleDTO 模块雷达卡（恒两行，specs §4.1.2 C）。
type DashboardModuleDTO struct {
	Module     string             `json:"module"`
	Dimensions []DashboardDimItem `json:"dimensions"`
	OverallAvg *int               `json:"overall_avg"`
}

// DashboardDimItem 维度均分条目（specs §4.1.4 规则3/规则4）。
type DashboardDimItem struct {
	DimensionCode string   `json:"dimension_code"`
	DimensionName string   `json:"dimension_name"`
	AvgScore      *int     `json:"avg_score"`
	LowRatio      *float64 `json:"low_ratio"`
	IsWeakness    bool     `json:"is_weakness"`
}

// DashboardEnneagramDTO 九型构成快照（恒 9 项，specs §4.1.4 规则5）。
type DashboardEnneagramDTO struct {
	ScoredCount    int                      `json:"scored_count"`
	CoverageRatio  float64                  `json:"coverage_ratio"`
	Distribution   []DashboardEnneagramItem `json:"distribution"`
	DominantType   string                   `json:"dominant_type"`
	DominantRatio  float64                  `json:"dominant_ratio"`
	SecondaryType  string                   `json:"secondary_type"`
	SecondaryRatio float64                  `json:"secondary_ratio"`
}

// DashboardEnneagramItem 单型别人数与占比。
type DashboardEnneagramItem struct {
	Type  string  `json:"type"`
	Count int     `json:"count"`
	Ratio float64 `json:"ratio"`
}

// DashboardSuggestionDTO 培训建议四态（specs §4.1.4 规则6，恒返回）。
type DashboardSuggestionDTO struct {
	Status      string                      `json:"status"`
	PeriodStart *string                     `json:"period_start"`
	PeriodEnd   *string                     `json:"period_end"`
	GeneratedAt *string                     `json:"generated_at"`
	Modules     []DashboardSuggestionModule `json:"modules"`
	Summary     string                      `json:"summary"`
}

// DashboardSuggestionModule 单模块建议条目集。
type DashboardSuggestionModule struct {
	Module      string                    `json:"module"`
	Suggestions []DashboardSuggestionItem `json:"suggestions"`
}

// DashboardSuggestionItem 单条培训方向建议（脱敏落库原文透传）。
type DashboardSuggestionItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DashboardTrendDTO A2 响应 data（03 A2 响应字段）。
type DashboardTrendDTO struct {
	Type          string                   `json:"type"`
	Module        string                   `json:"module"`
	Periods       []DashboardPeriodItem    `json:"periods"`
	CurrentPeriod *DashboardPeriodItem     `json:"current_period"`
	DataUpdatedAt *string                  `json:"data_updated_at"`
	Composite     *DashboardTrendComposite `json:"composite"`
	Dimensions    []DashboardTrendDim      `json:"dimensions"`
}

// DashboardTrendComposite 综合分摘要卡（specs §4.2.2 A）。
type DashboardTrendComposite struct {
	Score          int  `json:"score"`
	ChangeVsPrev   *int `json:"change_vs_prev"`
	DimensionCount int  `json:"dimension_count"`
}

// DashboardTrendDim 维度走势条目（specs §4.2.2 B/C）。
type DashboardTrendDim struct {
	DimensionCode string                `json:"dimension_code"`
	DimensionName string                `json:"dimension_name"`
	IsWeakness    bool                  `json:"is_weakness"`
	History       []DashboardTrendPoint `json:"history"`
	CurrentScore  *int                  `json:"current_score"`
	PrevScore     *int                  `json:"prev_score"`
	Change        *int                  `json:"change"`
}

// DashboardTrendPoint 单期走势点（置空期 Score=nil 断线）。
type DashboardTrendPoint struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Score       *int   `json:"score"`
}

type dashboardService struct {
	queries     repository.DashboardQueryRepository
	suggestions repository.TeamTrainingSuggestionRepository
	dims        repository.DimensionRepository
	results     repository.AssessmentTestResultRepository
	staffs      userapiClient
	secretRepo  repository.IntegrationSecretRepository
	encKey      []byte
}

// NewDashboardService 构造团队看板域 service，Wire 自动装配（encKey 绑定 NewLLMEncKey）。
func NewDashboardService(
	queries repository.DashboardQueryRepository,
	suggestions repository.TeamTrainingSuggestionRepository,
	dims repository.DimensionRepository,
	results repository.AssessmentTestResultRepository,
	staffs userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
) DashboardService {
	return &dashboardService{
		queries:     queries,
		suggestions: suggestions,
		dims:        dims,
		results:     results,
		staffs:      staffs,
		secretRepo:  secretRepo,
		encKey:      encKey,
	}
}

// Overview 编排（03 §4.3 A1 六步）：区间列表 → selected 定位 → 全员名单（1305）→
// 九型快照 → 活跃度三态与环比 → 维度聚合与短板判定 → 建议行与数据更新时间。
// 九型与建议恒取最新快照，不随所选区间变化（specs §4.1.4 规则5/规则6）。
func (s *dashboardService) Overview(ctx context.Context, periodStart, periodEnd string) (*DashboardOverviewDTO, error) {
	bounds, err := s.queries.ListPeriods(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dashboard periods: %w", err)
	}
	// 空态：三表无区间直接返回，跳过 userapi（03 §1.6）。
	if len(bounds) == 0 {
		return &DashboardOverviewDTO{
			Periods:    []DashboardPeriodItem{},
			Modules:    []DashboardModuleDTO{},
			Suggestion: emptySuggestionDTO(),
		}, nil
	}

	// 入参校验与 selected 定位：只传一端/日期非法 1400；双界 Unix 秒精确匹配（start 当日
	// 00:00 本地、end 次日 00:00 本地，口径同 profile），无匹配 2101。
	selIdx := 0
	if periodStart != "" || periodEnd != "" {
		if periodStart == "" || periodEnd == "" {
			return nil, NewError(errcode.BadRequest)
		}
		wantStart, err1 := time.ParseInLocation(layoutDate, periodStart, time.Local)
		wantEnd, err2 := time.ParseInLocation(layoutDate, periodEnd, time.Local)
		if err1 != nil || err2 != nil {
			return nil, NewError(errcode.BadRequest)
		}
		wantEnd = wantEnd.AddDate(0, 0, 1)
		selIdx = -1
		for i, b := range bounds {
			if b.StartAt.Unix() == wantStart.Unix() && b.EndAt.Unix() == wantEnd.Unix() {
				selIdx = i
				break
			}
		}
		if selIdx < 0 {
			return nil, NewError(errcode.DashboardPeriodInvalid)
		}
	}
	sel := bounds[selIdx]

	periods := make([]DashboardPeriodItem, 0, len(bounds))
	for i, b := range bounds {
		periods = append(periods, dashboardPeriodItem(b, i == 0))
	}

	// 全员名单计数（失败整体 1305，specs §5.2.4 规则2）。
	secret, err := resolveUserapiSecret(ctx, s.secretRepo, s.encKey)
	if err != nil {
		return nil, NewError(errcode.StaffListUnavailable)
	}
	names, err := userapi.FetchAllStaff(ctx, s.staffs, secret)
	if err != nil {
		return nil, NewError(errcode.StaffListUnavailable)
	}
	staffTotal := len(names)

	enneagram, err := s.buildEnneagram(ctx, names, staffTotal)
	if err != nil {
		return nil, err
	}
	activity, err := s.buildActivity(ctx, bounds, selIdx, staffTotal)
	if err != nil {
		return nil, err
	}

	// 维度聚合：维度全集取启用且 module ∈ 两模块，行按 source 分模块（specs §4.1.4 规则3）。
	dims, err := s.dims.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load dimensions: %w", err)
	}
	dimRows, err := s.queries.ListDimScoresByPeriod(ctx, sel.StartAt.Unix(), sel.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("list dimension scores: %w", err)
	}

	// 建议行（恒最新快照）与数据更新时间（批次终态，无批次取区间右开界，03 §1.8）。
	sugRow, err := s.suggestions.FindLatest(ctx)
	if err != nil {
		return nil, fmt.Errorf("find latest suggestion: %w", err)
	}
	fin, err := s.queries.FindLatestFinishedAt(ctx, sel.StartAt.Unix(), sel.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("find latest finished at: %w", err)
	}
	updatedAt := dashboardUpdatedAt(fin, sel.EndAt)

	return &DashboardOverviewDTO{
		Periods:        periods,
		SelectedPeriod: &DashboardPeriodItem{PeriodStart: sel.StartAt.Local().Format(layoutDate), PeriodEnd: sel.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate)},
		DataUpdatedAt:  updatedAt,
		StaffTotal:     staffTotal,
		Activity:       activity,
		Modules:        buildDashboardModules(dims, dimRows),
		Enneagram:      enneagram,
		Suggestion:     buildSuggestionDTO(sugRow),
	}, nil
}

// Trend 编排（03 §4.3 A2 五步）：type 兜底映射 → 前 8 期窗口反转旧到新 → 逐期逐维度
// 均分（同 Overview 口径）→ 综合分与取整分差 → 本期短板标识。数据固定最新期次，
// 无区间切换参数（specs §4.2.4 规则1）。
func (s *dashboardService) Trend(ctx context.Context, abilityType string) (*DashboardTrendDTO, error) {
	typ, module, source := "use", domain.ModuleAIUsage, domain.ScoreSourceConversation
	if abilityType == "manage" {
		typ, module, source = "manage", domain.ModuleAIMgmt, domain.ScoreSourceActiveTest
	}

	bounds, err := s.queries.ListPeriods(ctx)
	if err != nil {
		return nil, fmt.Errorf("list dashboard periods: %w", err)
	}
	window := bounds
	if len(window) > trendWindowSize {
		window = window[:trendWindowSize]
	}
	// 反转为旧到新。
	for i, j := 0, len(window)-1; i < j; i, j = i+1, j-1 {
		window[i], window[j] = window[j], window[i]
	}

	periods := make([]DashboardPeriodItem, 0, len(window))
	for i, b := range window {
		periods = append(periods, dashboardPeriodItem(b, i == len(window)-1))
	}
	dto := &DashboardTrendDTO{
		Type:       typ,
		Module:     module,
		Periods:    periods,
		Dimensions: []DashboardTrendDim{},
	}
	if len(window) == 0 {
		return dto, nil
	}
	current := window[len(window)-1]
	dto.CurrentPeriod = &DashboardPeriodItem{
		PeriodStart: current.StartAt.Local().Format(layoutDate),
		PeriodEnd:   current.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
	}

	rows, err := s.queries.ListDimScoresByPeriods(ctx, window)
	if err != nil {
		return nil, fmt.Errorf("list dimension scores by periods: %w", err)
	}
	moduleRows := make([]domain.DimensionScore, 0, len(rows))
	for _, r := range rows {
		if r.Source == source {
			moduleRows = append(moduleRows, r)
		}
	}
	// 空态：该模块窗口内无任何落库行，composite/data_updated_at/dimensions 维持空（03 A2）。
	if len(moduleRows) == 0 {
		return dto, nil
	}

	// 逐期逐维度均分（< 3 人置空，specs §4.2.4 规则2）。
	perPeriod := make([]map[string]dimAvg, len(window))
	for i, b := range window {
		perPeriod[i] = aggregateDimScores(rowsOfBound(moduleRows, b), source)
	}
	curIdx := len(window) - 1

	// 逐期综合分：参与维度未取整均分的算术平均取整，全置空该期 nil。
	compScores := make([]*int, len(window))
	for i, aggs := range perPeriod {
		if len(aggs) == 0 {
			continue
		}
		sum := 0.0
		for _, a := range aggs {
			sum += a.avg
		}
		v := int(math.Round(sum / float64(len(aggs))))
		compScores[i] = &v
	}
	if compScores[curIdx] != nil {
		comp := &DashboardTrendComposite{Score: *compScores[curIdx], DimensionCount: len(perPeriod[curIdx])}
		if curIdx >= 1 && compScores[curIdx-1] != nil {
			d := *compScores[curIdx] - *compScores[curIdx-1]
			comp.ChangeVsPrev = &d
		}
		dto.Composite = comp
	}

	fin, err := s.queries.FindLatestFinishedAt(ctx, current.StartAt.Unix(), current.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("find latest finished at: %w", err)
	}
	dto.DataUpdatedAt = dashboardUpdatedAt(fin, current.EndAt)

	// 维度全集走势：短板标识按本期判定结果，历史期次不回溯（specs §4.2.4 规则3）。
	dims, err := s.dims.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load dimensions: %w", err)
	}
	weak := weaknessSet(perPeriod[curIdx])
	for _, d := range dims {
		if !d.Enabled || d.ModuleCode != module {
			continue
		}
		td := DashboardTrendDim{
			DimensionCode: d.Code,
			DimensionName: d.Name,
			History:       make([]DashboardTrendPoint, 0, len(window)),
		}
		_, td.IsWeakness = weak[d.Code]
		for i, b := range window {
			pt := DashboardTrendPoint{
				PeriodStart: b.StartAt.Local().Format(layoutDate),
				PeriodEnd:   b.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
			}
			if a, ok := perPeriod[i][d.Code]; ok {
				v := int(math.Round(a.avg))
				pt.Score = &v
			}
			td.History = append(td.History, pt)
		}
		if n := len(td.History); n > 0 && td.History[n-1].Score != nil {
			v := *td.History[n-1].Score
			td.CurrentScore = &v
		}
		if n := len(td.History); n > 1 && td.History[n-2].Score != nil {
			v := *td.History[n-2].Score
			td.PrevScore = &v
		}
		if td.CurrentScore != nil && td.PrevScore != nil {
			v := *td.CurrentScore - *td.PrevScore
			td.Change = &v
		}
		dto.Dimensions = append(dto.Dimensions, td)
	}
	return dto, nil
}

// dimAvg 维度聚合结果（avg 未取整原始值，参与短板判定与参考线计算，03 §1.4）。
type dimAvg struct {
	avg      float64
	count    int
	lowCount int
}

// aggregateDimScores 按 code 聚合参与行：剔除 status != success 或 insufficient 行，
// 参与人数 < avgMinPeople 的维度不出现在结果（置空语义，specs §4.1.4 规则3）。
func aggregateDimScores(rows []domain.DimensionScore, source string) map[string]dimAvg {
	type acc struct {
		sum, count, low int
	}
	byCode := map[string]*acc{}
	for _, r := range rows {
		if r.Source != source {
			continue
		}
		if r.Status != domain.ScoreStatusSuccess || r.Insufficient {
			continue
		}
		a := byCode[r.DimensionCode]
		if a == nil {
			a = &acc{}
			byCode[r.DimensionCode] = a
		}
		a.sum += r.Score
		a.count++
		if r.Score < lowScoreLine {
			a.low++
		}
	}
	out := make(map[string]dimAvg, len(byCode))
	for code, a := range byCode {
		if a.count < avgMinPeople {
			continue
		}
		out[code] = dimAvg{avg: float64(a.sum) / float64(a.count), count: a.count, lowCount: a.low}
	}
	return out
}

// weaknessSet 共性短板判定（specs §4.1.4 规则4）：有均分维度按未取整均分升序
// （并列 code 升序）取最低 weaknessMaxCount 维；与第 2 名并列的补入至 weaknessHardCap。
func weaknessSet(aggs map[string]dimAvg) map[string]struct{} {
	set := map[string]struct{}{}
	if len(aggs) == 0 {
		return set
	}
	codes := make([]string, 0, len(aggs))
	for c := range aggs {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		ai, aj := aggs[codes[i]].avg, aggs[codes[j]].avg
		if math.Abs(ai-aj) > 1e-9 {
			return ai < aj
		}
		return codes[i] < codes[j]
	})
	n := weaknessMaxCount
	if n > len(codes) {
		n = len(codes)
	}
	for _, c := range codes[:n] {
		set[c] = struct{}{}
	}
	for i := weaknessMaxCount; i < len(codes) && i < weaknessHardCap; i++ {
		if math.Abs(aggs[codes[i]].avg-aggs[codes[weaknessMaxCount-1]].avg) <= 1e-9 {
			set[codes[i]] = struct{}{}
		}
	}
	return set
}

// buildDashboardModules 组装两模块雷达卡（恒两行）：AI_USAGE 取 conversation 行、
// AI_MGMT 取 active_test 行；OverallAvg 为各维度未取整均分的算术平均取整，
// 任一维度置空时 nil（specs §4.1.4 规则3）。
func buildDashboardModules(dims []domain.Dimension, rows []domain.DimensionScore) []DashboardModuleDTO {
	aggsByModule := map[string]map[string]dimAvg{
		domain.ModuleAIUsage: aggregateDimScores(rows, domain.ScoreSourceConversation),
		domain.ModuleAIMgmt:  aggregateDimScores(rows, domain.ScoreSourceActiveTest),
	}
	out := make([]DashboardModuleDTO, 0, 2)
	for _, module := range []string{domain.ModuleAIUsage, domain.ModuleAIMgmt} {
		aggs := aggsByModule[module]
		weak := weaknessSet(aggs)
		item := DashboardModuleDTO{Module: module, Dimensions: []DashboardDimItem{}}
		sum, cnt, complete := 0.0, 0, true
		for _, d := range dims {
			if !d.Enabled || d.ModuleCode != module {
				continue
			}
			cnt++
			dim := DashboardDimItem{DimensionCode: d.Code, DimensionName: d.Name}
			if a, ok := aggs[d.Code]; ok {
				v := int(math.Round(a.avg))
				dim.AvgScore = &v
				lr := round1Percent(a.lowCount, a.count)
				dim.LowRatio = &lr
				_, dim.IsWeakness = weak[d.Code]
				sum += a.avg
			} else {
				complete = false
			}
			item.Dimensions = append(item.Dimensions, dim)
		}
		if cnt > 0 && complete {
			v := int(math.Round(sum / float64(cnt)))
			item.OverallAvg = &v
		}
		out = append(out, item)
	}
	return out
}

// enneagramDistribution 判型计数转恒 9 项分布（dashboard 与 workspace 共用口径）：
// 按型别 1-9 升序、占比一位小数（分母由调用方决定）。
func enneagramDistribution(counts map[string]int, denom int) []DashboardEnneagramItem {
	distribution := make([]DashboardEnneagramItem, 0, 9)
	for i := 1; i <= 9; i++ {
		typ := strconv.Itoa(i)
		c := counts[typ]
		distribution = append(distribution, DashboardEnneagramItem{Type: typ, Count: c, Ratio: round1Percent(c, denom)})
	}
	return distribution
}

// buildEnneagram 九型构成快照（specs §4.1.4 规则5）：全员最新 scored 判型集合统计，
// 恒 9 项按型别升序，主导/次主导取占比最高两型（并列按型别序号升序取先）；
// 名单空或无判型行时 nil。
func (s *dashboardService) buildEnneagram(ctx context.Context, names []string, staffTotal int) (*DashboardEnneagramDTO, error) {
	if len(names) == 0 {
		return nil, nil
	}
	byStaff, err := s.results.ListLatestScoredByStaffNames(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list latest enneagram results: %w", err)
	}
	counts := map[string]int{}
	scored := 0
	for _, name := range names {
		res, ok := byStaff[name]
		if !ok || res.MainType == "" {
			continue
		}
		counts[res.MainType]++
		scored++
	}
	if scored == 0 {
		return nil, nil
	}
	distribution := enneagramDistribution(counts, scored)
	order := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := distribution[order[i]], distribution[order[j]]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Type < b.Type
	})
	dom, sec := distribution[order[0]], distribution[order[1]]
	var coverage float64
	if staffTotal > 0 {
		coverage = round1Percent(scored, staffTotal)
	}
	return &DashboardEnneagramDTO{
		ScoredCount:    scored,
		CoverageRatio:  coverage,
		Distribution:   distribution,
		DominantType:   dom.Type,
		DominantRatio:  dom.Ratio,
		SecondaryType:  sec.Type,
		SecondaryRatio: sec.Ratio,
	}, nil
}

// countActivityLevels 活跃度行三态计数（dashboard 与 workspace 共用口径）：
// 返回 active/low/unused 行数与去重人数（无统计行并入未使用由调用方按名单合计）。
func countActivityLevels(rows []domain.ActivityStat) (active, low, unused, involved int) {
	seen := map[string]struct{}{}
	for _, r := range rows {
		switch r.ActiveLevel {
		case domain.ActiveLevelActive:
			active++
		case domain.ActiveLevelLowFreq:
			low++
		case domain.ActiveLevelUnused:
			unused++
		}
		seen[r.TokenName] = struct{}{}
	}
	return active, low, unused, len(seen)
}

// buildActivity 活跃度三态与环比（specs §4.1.4 规则2）：无统计行人员并入未使用；
// 环比取区间列表下一项（更旧一期）同口径计数差，所选为最早区间时 mom=nil。
func (s *dashboardService) buildActivity(ctx context.Context, bounds []repository.PeriodBound, selIdx, staffTotal int) (*DashboardActivityDTO, error) {
	sel := bounds[selIdx]
	rows, err := s.queries.ListActivityByPeriod(ctx, sel.StartAt.Unix(), sel.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("list activity stats: %w", err)
	}
	active, low, unusedRows, involved := countActivityLevels(rows)
	unusedNoRow := staffTotal - involved
	if unusedNoRow < 0 {
		unusedNoRow = 0
	}
	unused := unusedRows + unusedNoRow

	dto := &DashboardActivityDTO{
		Active:  dashboardCountRatio(active, staffTotal),
		LowFreq: dashboardCountRatio(low, staffTotal),
		Unused:  dashboardCountRatio(unused, staffTotal),
	}
	if selIdx+1 >= len(bounds) {
		return dto, nil
	}
	prev := bounds[selIdx+1]
	prevRows, err := s.queries.ListActivityByPeriod(ctx, prev.StartAt.Unix(), prev.EndAt.Unix())
	if err != nil {
		return nil, fmt.Errorf("list prev activity stats: %w", err)
	}
	pActive, _, pUnusedRows, pInvolved := countActivityLevels(prevRows)
	pUnusedNoRow := staffTotal - pInvolved
	if pUnusedNoRow < 0 {
		pUnusedNoRow = 0
	}
	dto.Mom = &DashboardMomDTO{
		ActiveChange: active - pActive,
		UnusedChange: unused - (pUnusedRows + pUnusedNoRow),
	}
	return dto, nil
}

// buildSuggestionDTO 建议行四态映射（specs §4.1.4 规则6）：generated 展开内容，
// 其余态内容字段空值；modules_json 解析失败按空数组容错（展示层只读）。
func buildSuggestionDTO(row *domain.TeamTrainingSuggestion) DashboardSuggestionDTO {
	if row == nil {
		return emptySuggestionDTO()
	}
	switch row.Status {
	case domain.SuggestionStatusGenerated:
		dto := DashboardSuggestionDTO{Status: "generated", Modules: []DashboardSuggestionModule{}}
		ps := row.PeriodStartAt.Local().Format(layoutDate)
		pe := row.PeriodEndAt.Local().AddDate(0, 0, -1).Format(layoutDate)
		dto.PeriodStart, dto.PeriodEnd = &ps, &pe
		if row.GeneratedAt != nil {
			ga := row.GeneratedAt.Local().Format(layoutDateTime)
			dto.GeneratedAt = &ga
		}
		if row.ModulesJSON != "" {
			var mods []DashboardSuggestionModule
			if json.Unmarshal([]byte(row.ModulesJSON), &mods) == nil {
				for i := range mods {
					if mods[i].Suggestions == nil {
						mods[i].Suggestions = []DashboardSuggestionItem{}
					}
				}
				if mods != nil {
					dto.Modules = mods
				}
			}
		}
		dto.Summary = row.Summary
		return dto
	case domain.SuggestionStatusGenerating:
		return DashboardSuggestionDTO{Status: "generating", Modules: []DashboardSuggestionModule{}}
	case domain.SuggestionStatusFailed:
		return DashboardSuggestionDTO{Status: "failed", Modules: []DashboardSuggestionModule{}}
	}
	return emptySuggestionDTO()
}

// emptySuggestionDTO none 态（无建议行）。
func emptySuggestionDTO() DashboardSuggestionDTO {
	return DashboardSuggestionDTO{Status: "none", Modules: []DashboardSuggestionModule{}}
}

// dashboardPeriodItem 区间条目（end 为右开界，展示为止日前一日）。
func dashboardPeriodItem(b repository.PeriodBound, isCurrent bool) DashboardPeriodItem {
	return DashboardPeriodItem{
		PeriodStart: b.StartAt.Local().Format(layoutDate),
		PeriodEnd:   b.EndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
		IsCurrent:   isCurrent,
	}
}

// dashboardUpdatedAt 数据更新时间（03 §1.8）：批次终态时间，无批次取区间右开界。
func dashboardUpdatedAt(fin *time.Time, fallbackEnd time.Time) *string {
	at := fallbackEnd
	if fin != nil {
		at = *fin
	}
	v := at.Local().Format(layoutDateTime)
	return &v
}

// dashboardCountRatio 人数与占全员比例一位小数（分母 0 时占比 0）。
func dashboardCountRatio(count, total int) DashboardCountRatio {
	var ratio float64
	if total > 0 {
		ratio = round1Percent(count, total)
	}
	return DashboardCountRatio{Count: count, Ratio: ratio}
}

// round1Percent part/total 百分比一位小数。
func round1Percent(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*1000) / 10
}

// rowsOfBound 取双界匹配行（Unix 秒精确比较）。
func rowsOfBound(rows []domain.DimensionScore, b repository.PeriodBound) []domain.DimensionScore {
	out := make([]domain.DimensionScore, 0, len(rows))
	for _, r := range rows {
		if r.PeriodStartAt.Unix() == b.StartAt.Unix() && r.PeriodEndAt.Unix() == b.EndAt.Unix() {
			out = append(out, r)
		}
	}
	return out
}
