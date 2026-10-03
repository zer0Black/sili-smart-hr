// profile 个人画像域业务层：A1 人员画像列表聚合（specs P2_PRF_001 §5.1、03 §4.3 A1 链路）。
//
// 业务规则：
//   - BR1 上游名单实时拉取，失败整体 1305，不降级不缓存（specs §4.1.4 规则1）
//   - BR2 缺失值：活跃度无行 unused、模块分无聚合行 nil、九型无判型 nil（specs §4.1.4 规则2）
//   - BR3 模块最新聚合周期内存在 insufficient/failed 维度行 → degraded 只标注不改分（specs §4.1.4 规则3）
//   - BR4 短板集合 = 两模块最新周期参与聚合维度合并后最低分，并列全选（specs §4.1.4 规则4、03 §1.9）
//   - BR5 各列分别取各自最新周期，无区间参数（specs §4.1.4 规则5）
//   - BR6 仅看未使用与活跃度叠加时以未使用为准（specs §4.1.2 A）
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// 分页口径（03 A1：任意 1-100 接受，>100 钳位 100；specs §4.1.5 默认 10）。
const (
	profileDefaultPageSize = 10
	profileMaxPageSize     = 100
)

// B1 计算常量（03 §4.2：实现常量非在线配置，随源码发版）。
const (
	TrendWindowSize       = 4  // 走势期数（specs §4.2.2 D）
	CompanyAvgMinPeople   = 3  // 公司均分最小有数据人数（specs §4.2.4 规则4）
	ConfidenceHighSignals = 10 // 会话信号 ≥10 → high（specs §4.2.4 规则5）
	ConfidenceLowSignals  = 2  // <2 → low（2-9 medium）
)

// ProfileService 个人画像域只读查询服务（specs §5.1/§5.2，03 A1/A2/B1）。
type ProfileService interface {
	// List 查询人员画像列表（03 A1）：上游全员名单 + 每模块最新周期画像左联 + 服务端筛选分页。
	List(ctx context.Context, f ProfileFilter) (*ProfileListResult, error)
	// Detail 查询画像详情聚合（03 B1）。
	Detail(ctx context.Context, staffName string, periodStart, periodEnd string) (*ProfileDetailDTO, error)
	// Export 按筛选条件全量导出 xlsx（03 A2），实现见 profile_export.go。
	Export(ctx context.Context, f ProfileFilter) ([]byte, string, error)
}

// ProfileFilter A1/A2 共用筛选条件（03 A1 查询参数）。
type ProfileFilter struct {
	Name          string // 姓名包含匹配，已去首尾空格
	ActivityLevel string // 空=全部；active/low_freq/unused，非法值 1400
	DimensionCode string // 短板维度筛选；须为启用且 module ∈ {AI_USAGE,AI_MGMT} 的 code，否则 1400
	UnusedOnly    bool   // true 时忽略 ActivityLevel 强制 unused（specs §4.1.2 A）
	Page          int    // 默认 1
	PageSize      int    // 默认 10；任意 1-100 接受，>100 钳位 100
}

// ProfileListResult A1 响应 data（分页四字段）。
type ProfileListResult struct {
	List     []ProfileListItem `json:"list"`
	Total    int64             `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

// ProfileListItem A1 行（03 A1 响应字段，全 string/number/bool 无雪花 ID）。
type ProfileListItem struct {
	StaffName         string   `json:"staff_name"`
	ActivityLevel     string   `json:"activity_level"`    // 无行按 unused（specs §4.1.4 规则2）
	AIUsageScore      *float64 `json:"ai_usage_score"`    // 无聚合行 nil
	AIUsageDegraded   bool     `json:"ai_usage_degraded"` // specs §4.1.4 规则3
	AIMGMTScore       *float64 `json:"ai_mgmt_score"`
	AIMGMTDegraded    bool     `json:"ai_mgmt_degraded"`
	EnneagramMainType *string  `json:"enneagram_main_type"` // "1"-"9" 或 nil
}

// ProfileDetailDTO B1 响应 data（03 B1 响应字段全量）。
type ProfileDetailDTO struct {
	StaffName      string                `json:"staff_name"`
	Periods        []ProfilePeriod       `json:"periods"`
	SelectedPeriod *ProfilePeriodRange   `json:"selected_period"`
	ActivityLevel  string                `json:"activity_level"`
	Enneagram      *ProfileEnneagram     `json:"enneagram"`
	Modules        []ProfileModuleCard   `json:"modules"`
	Dimensions     []ProfileDimensionRow `json:"dimensions"`
}

// ProfilePeriod 区间列表条目（03 B1 periods[]）。
type ProfilePeriod struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	IsCurrent   bool   `json:"is_current"`
}

// ProfilePeriodRange 实际生效区间（03 B1 selected_period）。
type ProfilePeriodRange struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

// ProfileEnneagram 九型判型（最新 scored 行，不随区间变化，03 B1 enneagram）。
type ProfileEnneagram struct {
	MainType      string             `json:"main_type"`
	WingType      string             `json:"wing_type"`
	Distribution  map[string]float64 `json:"distribution"`
	Rationale     string             `json:"rationale"`
}

// ProfileModuleCard 模块评分卡条目（03 B1 modules[]，恒两行）。
type ProfileModuleCard struct {
	Module           string   `json:"module"`
	Score            *float64 `json:"score"`
	ChangeVsPrev     *int     `json:"change_vs_prev"`
	EvaluatedAt      *string  `json:"evaluated_at"`
	DataStatus       string   `json:"data_status"`
	InsufficientCount int     `json:"insufficient_count"`
	FailedCount       int     `json:"failed_count"`
	MissingCount      int     `json:"missing_count"`
}

// ProfileDimensionRow 维度明细条目（03 B1 dimensions[]）。
type ProfileDimensionRow struct {
	Module        string               `json:"module"`
	DimensionCode string               `json:"dimension_code"`
	DimensionName string               `json:"dimension_name"`
	GroupCode     *string              `json:"group_code"`
	Score         *int                 `json:"score"`
	Status        string               `json:"status"`
	Rationale     string               `json:"rationale"`
	Evidences     []ProfileEvidence    `json:"evidences"`
	Trend         []ProfileTrendPoint  `json:"trend"`
	CompanyAvg    *float64             `json:"company_avg"`
}

// ProfileEvidence 证据来源条目（03 B1 evidences[]）。
type ProfileEvidence struct {
	Source       string         `json:"source"`
	Time         string         `json:"time"`
	Confidence   string         `json:"confidence"`
	SessionCount int            `json:"session_count"`
	Summary      map[string]any `json:"summary"`
}

// ProfileTrendPoint 走势条目（03 B1 trend[]）。
type ProfileTrendPoint struct {
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Score       int    `json:"score"`
}

type profileService struct {
	dimScores  repository.DimensionScoreRepository       // T1 三方法
	aggScores  repository.AggregateScoreRepository       // T2 两方法
	activity   repository.ActivityStatRepository         // T2 两方法
	results    repository.AssessmentTestResultRepository // T2 一方法
	dimensions repository.DimensionRepository            // ListAll 基准集合（已有）
	userapi    userapiClient                             // service 包内既有鸭子接口（*userapi.Client 适配）
	secretRepo repository.IntegrationSecretRepository    // resolveSecret
	encKey     []byte
}

// NewProfileService 构造个人画像域 service，Wire 自动装配（encKey 形参为 []byte，
// 绑定 NewLLMEncKey 的 []byte provider，与 assessment_config 样板同位）。
func NewProfileService(
	dimScores repository.DimensionScoreRepository,
	aggScores repository.AggregateScoreRepository,
	activity repository.ActivityStatRepository,
	results repository.AssessmentTestResultRepository,
	dimensions repository.DimensionRepository,
	userapi userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
) ProfileService {
	return &profileService{
		dimScores:  dimScores,
		aggScores:  aggScores,
		activity:   activity,
		results:    results,
		dimensions: dimensions,
		userapi:    userapi,
		secretRepo: secretRepo,
		encKey:     encKey,
	}
}

// profileModKey (人, 模块) 复合索引键。
type profileModKey struct {
	staff  string
	module string
}

// List 编排：校验分页 → 共享组装链路 → 内存分页（03 §4.3）。
func (s *profileService) List(ctx context.Context, f ProfileFilter) (*ProfileListResult, error) {
	page, pageSize := f.Page, f.PageSize
	if pageSize <= 0 {
		pageSize = profileDefaultPageSize
	}
	if pageSize > profileMaxPageSize {
		pageSize = profileMaxPageSize
	}
	if page <= 0 {
		page = 1
	}

	items, err := s.assembleRows(ctx, f)
	if err != nil {
		return nil, err
	}

	// 姓名升序（specs §8.3 偏离记录）+ 内存分页。
	offset := (page - 1) * pageSize
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	return &ProfileListResult{
		List:     items[offset:end],
		Total:    int64(len(items)),
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// assembleRows A1/A2 共享查询链路（specs §5.1.4 规则2）：
// 校验 → 密钥 → 全量名单 → 批量 IN 四表 → 内存组装/筛选/排序，不分页。
func (s *profileService) assembleRows(ctx context.Context, f ProfileFilter) ([]ProfileListItem, error) {
	if f.ActivityLevel != "" && !validProfileActivityLevel(f.ActivityLevel) {
		return nil, NewError(errcode.BadRequest)
	}

	// 短板筛选 code 合法性：启用且 module ∈ {AI_USAGE, AI_MGMT}（03 A1）。
	if f.DimensionCode != "" {
		dims, err := s.dimensions.ListAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("load dimensions: %w", err)
		}
		if _, ok := profileFilterableDimensionCodes(dims)[f.DimensionCode]; !ok {
			return nil, NewError(errcode.BadRequest)
		}
	}

	secret, err := s.resolveSecret(ctx)
	if err != nil {
		return nil, NewError(errcode.StaffListUnavailable)
	}

	// 全量拉名单：keyword 透传上游模糊过滤，按 staff_name 去重保首见。
	kw := strings.TrimSpace(f.Name)
	seen := make(map[string]struct{})
	names := make([]string, 0)
	if werr := userapi.WalkStaffPages(ctx, s.userapi, secret, kw, func(rows []userapi.Staff) error {
		for i := range rows {
			if _, dup := seen[rows[i].StaffName]; dup {
				continue
			}
			seen[rows[i].StaffName] = struct{}{}
			names = append(names, rows[i].StaffName)
		}
		return nil
	}); werr != nil {
		return nil, NewError(errcode.StaffListUnavailable)
	}
	// 上游过滤结果的内存包含校验兜底（03 A1：字面比较，无转义场景）。
	if kw != "" {
		kept := names[:0]
		for _, n := range names {
			if strings.Contains(n, kw) {
				kept = append(kept, n)
			}
		}
		names = kept
	}
	if len(names) == 0 {
		return []ProfileListItem{}, nil
	}

	// 批量 IN 四表（specs §5.1.4 规则2：禁止逐人查询）。
	aggRows, err := s.aggScores.ListLatestModuleRowsByTokens(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list latest aggregate rows: %w", err)
	}
	dimRows, err := s.dimScores.ListLatestByTokens(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list latest dimension rows: %w", err)
	}
	actRows, err := s.activity.ListLatestByTokens(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list latest activity rows: %w", err)
	}
	enneagrams, err := s.results.ListLatestScoredByStaffNames(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list latest enneagram results: %w", err)
	}

	aggBy := make(map[profileModKey]domain.AggregateScore, len(aggRows))
	for _, r := range aggRows {
		aggBy[profileModKey{r.TokenName, r.Module}] = r
	}
	dimsBy := make(map[profileModKey][]domain.DimensionScore, len(dimRows))
	for _, r := range dimRows {
		k := profileModKey{r.TokenName, r.Module}
		dimsBy[k] = append(dimsBy[k], r)
	}
	actBy := make(map[string]domain.ActivityStat, len(actRows))
	for _, r := range actRows {
		actBy[r.TokenName] = r
	}
	shortboard := buildShortboardSets(aggBy, dimsBy)

	items := make([]ProfileListItem, 0, len(names))
	for _, staff := range names {
		item := ProfileListItem{
			StaffName:     staff,
			ActivityLevel: domain.ActiveLevelUnused, // 无行按 unused（BR2）
		}
		if act, ok := actBy[staff]; ok {
			item.ActivityLevel = act.ActiveLevel
		}
		item.AIUsageScore, item.AIUsageDegraded = profileModuleColumn(aggBy, dimsBy, staff, domain.ModuleAIUsage)
		item.AIMGMTScore, item.AIMGMTDegraded = profileModuleColumn(aggBy, dimsBy, staff, domain.ModuleAIMgmt)
		if res, ok := enneagrams[staff]; ok && res.MainType != "" {
			mt := res.MainType
			item.EnneagramMainType = &mt
		}
		// 筛选：UnusedOnly 优先于 ActivityLevel（BR6）；短板筛选无评分行不命中（BR4）。
		if f.UnusedOnly {
			if item.ActivityLevel != domain.ActiveLevelUnused {
				continue
			}
		} else if f.ActivityLevel != "" && item.ActivityLevel != f.ActivityLevel {
			continue
		}
		if f.DimensionCode != "" {
			set, ok := shortboard[staff]
			if !ok {
				continue
			}
			if _, hit := set[f.DimensionCode]; !hit {
				continue
			}
		}
		items = append(items, item)
	}

	// 姓名升序（specs §8.3 偏离记录）。
	sort.SliceStable(items, func(i, j int) bool { return items[i].StaffName < items[j].StaffName })
	return items, nil
}

// Detail 编排（03 §4.3 B1 链路）：校验区间参数 → 三表 ListByToken 归并区间并集 →
// 定位 selected → 维度配置基准 → 各数据块组装 → userapi 解析姓名。
func (s *profileService) Detail(ctx context.Context, staffName string, periodStart, periodEnd string) (*ProfileDetailDTO, error) {
	if strings.TrimSpace(staffName) == "" {
		return nil, NewError(errcode.BadRequest)
	}
	var wantStart, wantEnd time.Time
	hasPeriod := periodStart != "" || periodEnd != ""
	if hasPeriod {
		if periodStart == "" || periodEnd == "" {
			return nil, NewError(errcode.BadRequest)
		}
		var err error
		// 03 §1.4 换算：start 当日 00:00、end 次日 00:00，与落库双界 Unix 秒精确匹配。
		if wantStart, err = time.ParseInLocation(layoutDate, periodStart, time.Local); err != nil {
			return nil, NewError(errcode.BadRequest)
		}
		if wantEnd, err = time.ParseInLocation(layoutDate, periodEnd, time.Local); err != nil {
			return nil, NewError(errcode.BadRequest)
		}
		wantEnd = wantEnd.AddDate(0, 0, 1)
	}

	dims, err := s.dimensions.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load dimensions: %w", err)
	}

	actRows, err := s.activity.ListByToken(ctx, staffName)
	if err != nil {
		return nil, fmt.Errorf("list activity rows: %w", err)
	}
	dimRows, err := s.dimScores.ListByToken(ctx, staffName)
	if err != nil {
		return nil, fmt.Errorf("list dimension rows: %w", err)
	}
	aggRows, err := s.aggScores.ListByToken(ctx, staffName)
	if err != nil {
		return nil, fmt.Errorf("list aggregate rows: %w", err)
	}

	// 区间并集（start 秒 + end 秒）去重新到旧（BR1）。
	bounds := collectPeriodBounds(actRows, dimRows, aggRows)
	periods := make([]ProfilePeriod, 0, len(bounds))
	for i, b := range bounds {
		periods = append(periods, ProfilePeriod{
			PeriodStart: b.start.In(time.Local).Format(layoutDate),
			PeriodEnd:   b.end.AddDate(0, 0, -1).In(time.Local).Format(layoutDate),
			IsCurrent:   i == 0,
		})
	}

	// 定位 selected：未传取首项；传入须双界精确匹配，否则 2001（specs §5.2.4 规则1）。
	var selStart, selEnd time.Time
	if !hasPeriod {
		if len(bounds) == 0 {
			return s.assembleEmptyDetail(ctx, staffName, dims)
		}
		selStart, selEnd = bounds[0].start, bounds[0].end
	} else {
		idx := -1
		for i, b := range bounds {
			if b.start.Unix() == wantStart.Unix() && b.end.Unix() == wantEnd.Unix() {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, NewError(errcode.ProfilePeriodInvalid)
		}
		selStart, selEnd = bounds[idx].start, bounds[idx].end
	}

	// 公司均分（BR3）：全公司所选区间维度行按 code 分组，剔 insufficient/failed 求均值。
	companyRows, err := s.dimScores.ListByPeriodAllCompany(ctx, selStart.Unix(), selEnd.Unix())
	if err != nil {
		return nil, fmt.Errorf("list company dimension rows: %w", err)
	}
	companyAvg := computeCompanyAverages(companyRows)

	// 九型：最新 scored 判型行，单人版复用 T2 方法（BR 隐私：仅透传脱敏落库内容）。
	enneagram, err := s.loadEnneagram(ctx, staffName)
	if err != nil {
		return nil, err
	}

	dto := &ProfileDetailDTO{
		StaffName:      staffName,
		Periods:        periods,
		SelectedPeriod: &ProfilePeriodRange{PeriodStart: selStart.In(time.Local).Format(layoutDate), PeriodEnd: selEnd.AddDate(0, 0, -1).In(time.Local).Format(layoutDate)},
		ActivityLevel:  domain.ActiveLevelUnused,
		Enneagram:      enneagram,
		Modules:        s.buildModuleCards(aggRows, dimRows, dims, bounds, selStart, selEnd),
		Dimensions:     s.buildDimensionRows(dimRows, dims, selStart, selEnd, companyAvg),
	}
	for _, r := range actRows {
		if r.PeriodStartAt.Unix() == selStart.Unix() && r.PeriodEndAt.Unix() == selEnd.Unix() {
			dto.ActivityLevel = r.ActiveLevel
			break
		}
	}

	// 姓名解析：上游失败 1305（BR8）；查无匹配回退 token_name 入参（03 §1.7）。
	name, err := s.resolveStaffName(ctx, staffName)
	if err != nil {
		return nil, err
	}
	dto.StaffName = name
	return dto, nil
}

// profilePeriodBound 区间双界（start 含 / end 不含，与落库行同构）。
type profilePeriodBound struct {
	start time.Time
	end   time.Time
}

// collectPeriodBounds 三表落库周期按 (start 秒, end 秒) 去重归并，新到旧排序。
// 排序键取 start 优先，start 相同（同起点不同长度的定向窗口）按 end 降序。
func collectPeriodBounds(actRows []domain.ActivityStat, dimRows []domain.DimensionScore, aggRows []domain.AggregateScore) []profilePeriodBound {
	seen := make(map[[2]int64]struct{})
	list := make([]profilePeriodBound, 0)
	add := func(st, en time.Time) {
		k := [2]int64{st.Unix(), en.Unix()}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		list = append(list, profilePeriodBound{start: st, end: en})
	}
	for _, r := range actRows {
		add(r.PeriodStartAt, r.PeriodEndAt)
	}
	for _, r := range dimRows {
		add(r.PeriodStartAt, r.PeriodEndAt)
	}
	for _, r := range aggRows {
		add(r.PeriodStartAt, r.PeriodEndAt)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].start.Unix() != list[j].start.Unix() {
			return list[i].start.Unix() > list[j].start.Unix()
		}
		return list[i].end.Unix() > list[j].end.Unix()
	})
	return list
}

// assembleEmptyDetail 空画像语义（specs §5.2.4 规则1）：四表无落库周期行时照常
// 解析姓名返回，periods 空、selected nil、各块按缺失口径、dimensions 按启用维度全 missing。
func (s *profileService) assembleEmptyDetail(ctx context.Context, staffName string, dims []domain.Dimension) (*ProfileDetailDTO, error) {
	enneagram, err := s.loadEnneagram(ctx, staffName)
	if err != nil {
		return nil, err
	}
	name, err := s.resolveStaffName(ctx, staffName)
	if err != nil {
		return nil, err
	}
	return &ProfileDetailDTO{
		StaffName:     name,
		Periods:       []ProfilePeriod{},
		ActivityLevel: domain.ActiveLevelUnused,
		Enneagram:     enneagram,
		Modules:       s.buildModuleCards(nil, nil, dims, nil, time.Time{}, time.Time{}),
		Dimensions:    s.buildDimensionRows(nil, dims, time.Time{}, time.Time{}, nil),
	}, nil
}

// resolveStaffName userapi 单页解析姓名（03 §1.7）：上游 err 整体 1305；
// username == staffName 即命中；查无匹配回退 token_name 入参原值。
func (s *profileService) resolveStaffName(ctx context.Context, staffName string) (string, error) {
	secret, err := s.resolveSecret(ctx)
	if err != nil {
		return "", NewError(errcode.StaffListUnavailable)
	}
	rows, _, uerr := s.userapi.ListStaffs(ctx, secret, staffName, 1, userapi.StaffPageSize)
	if uerr != nil {
		return "", NewError(errcode.StaffListUnavailable)
	}
	for _, r := range rows {
		if r.StaffName == staffName {
			return r.StaffName, nil
		}
	}
	return staffName, nil
}

// loadEnneagram 取最新 scored 判型行转 DTO；降级占位行（main_type 空串）视同无判型
//（03 §1.8），无判型 nil。
func (s *profileService) loadEnneagram(ctx context.Context, staffName string) (*ProfileEnneagram, error) {
	byStaff, err := s.results.ListLatestScoredByStaffNames(ctx, []string{staffName})
	if err != nil {
		return nil, fmt.Errorf("list latest enneagram result: %w", err)
	}
	res, ok := byStaff[staffName]
	if !ok || res.MainType == "" {
		return nil, nil
	}
	e := &ProfileEnneagram{
		MainType:     res.MainType,
		WingType:     res.WingType,
		Distribution: map[string]float64{},
		Rationale:    res.Rationale,
	}
	if res.DistributionJSON != "" {
		_ = json.Unmarshal([]byte(res.DistributionJSON), &e.Distribution)
	}
	return e, nil
}

// profileEvidenceJSON EvidenceJSON 解析目标（evaluator.evidenceJSON 同构，解耦定义）。
type profileEvidenceJSON struct {
	SessionKeys []string       `json:"session_keys"`
	Summary     map[string]int `json:"summary"`
}

// buildModuleCards 组装两模块评分卡（恒两行，03 B1 modules）：
// score 原始浮点直出；change_vs_prev = round(本期)-round(区间列表下一项聚合行)（BR2）；
// evaluated_at = PeriodEndAt Local 减一日（BR9）；data_status 判定优先级见 specs §4.2.4 规则8。
func (s *profileService) buildModuleCards(aggRows []domain.AggregateScore, dimRows []domain.DimensionScore,
	dims []domain.Dimension, bounds []profilePeriodBound, selStart, selEnd time.Time) []ProfileModuleCard {
	enabledByModule := map[string]int{}
	for _, d := range dims {
		if !d.Enabled {
			continue
		}
		if d.ModuleCode != domain.ModuleAIUsage && d.ModuleCode != domain.ModuleAIMgmt {
			continue
		}
		enabledByModule[d.ModuleCode]++
	}

	cards := make([]ProfileModuleCard, 0, 2)
	for _, module := range []string{domain.ModuleAIUsage, domain.ModuleAIMgmt} {
		card := ProfileModuleCard{Module: module, DataStatus: "pending"}
		var cur *domain.AggregateScore
		for i := range aggRows {
			if aggRows[i].Module == module &&
				aggRows[i].PeriodStartAt.Unix() == selStart.Unix() && aggRows[i].PeriodEndAt.Unix() == selEnd.Unix() {
				cur = &aggRows[i]
				break
			}
		}
		if cur != nil {
			card.Score = cur.ModuleScore
			ev := cur.PeriodEndAt.In(time.Local).AddDate(0, 0, -1).Format(layoutDate)
			card.EvaluatedAt = &ev
			card.DataStatus = "complete"
		}

		// 所选区间该模块维度行三计数（missing 基准 = 当前启用维度数 - 有评分行维度数）。
		scoredCodes := map[string]struct{}{}
		for _, r := range dimRows {
			if r.Module != module || r.PeriodStartAt.Unix() != selStart.Unix() || r.PeriodEndAt.Unix() != selEnd.Unix() {
				continue
			}
			switch {
			case r.Status == domain.ScoreStatusFailed:
				card.FailedCount++
			case r.Insufficient:
				card.InsufficientCount++
			}
			scoredCodes[r.DimensionCode] = struct{}{}
		}
		card.MissingCount = enabledByModule[module] - len(scoredCodes)
		if card.MissingCount < 0 {
			card.MissingCount = 0
		}

		// data_status 优先级：pending > missing > degraded > complete（BR6）。
		if cur != nil {
			switch {
			case card.FailedCount > 0 || card.MissingCount > 0:
				card.DataStatus = "missing"
			case card.InsufficientCount > 0:
				card.DataStatus = "degraded"
			}
		}

		// change_vs_prev：区间列表下一项（更旧一期）的该模块聚合行，取整分差（BR2）。
		if cur != nil {
			if prev := prevPeriodAggRow(aggRows, bounds, selStart, module); prev != nil && prev.ModuleScore != nil && cur.ModuleScore != nil {
				delta := int(math.Round(*cur.ModuleScore)) - int(math.Round(*prev.ModuleScore))
				card.ChangeVsPrev = &delta
			}
		}
		cards = append(cards, card)
	}
	return cards
}

// prevPeriodAggRow 在区间列表中找 selected 的下一项（更旧一期）对应聚合行。
func prevPeriodAggRow(aggRows []domain.AggregateScore, bounds []profilePeriodBound, selStart time.Time, module string) *domain.AggregateScore {
	for i, b := range bounds {
		if b.start.Unix() != selStart.Unix() {
			continue
		}
		if i+1 >= len(bounds) {
			return nil
		}
		nb := bounds[i+1]
		for j := range aggRows {
			if aggRows[j].Module == module &&
				aggRows[j].PeriodStartAt.Unix() == nb.start.Unix() && aggRows[j].PeriodEndAt.Unix() == nb.end.Unix() {
				return &aggRows[j]
			}
		}
		return nil
	}
	return nil
}

// buildDimensionRows 维度明细（03 B1 dimensions）：当前启用且 module ∈ 两模块维度全集，
// score 行命中同人同双界且 status=success 直出（insufficient 照常出分标 insufficient）；
// failed 行与无行全 missing 组装。evidences/trend/company_avg 逐维度填充。
func (s *profileService) buildDimensionRows(dimRows []domain.DimensionScore, dims []domain.Dimension,
	selStart, selEnd time.Time, companyAvg map[string]float64) []ProfileDimensionRow {
	// 同维度各期 success 行按期旧到新（trend 与本期命中共用）。
	type periodScore struct {
		start, end time.Time
		score      int
	}
	history := map[string][]periodScore{}
	for _, r := range dimRows {
		if r.Status != domain.ScoreStatusSuccess {
			continue
		}
		history[r.DimensionCode] = append(history[r.DimensionCode], periodScore{r.PeriodStartAt, r.PeriodEndAt, r.Score})
	}
	for code := range history {
		sort.SliceStable(history[code], func(i, j int) bool {
			return history[code][i].start.Unix() < history[code][j].start.Unix()
		})
	}

	rows := make([]ProfileDimensionRow, 0, len(dims))
	for _, d := range dims {
		if !d.Enabled || (d.ModuleCode != domain.ModuleAIUsage && d.ModuleCode != domain.ModuleAIMgmt) {
			continue
		}
		row := ProfileDimensionRow{
			Module:        d.ModuleCode,
			DimensionCode: d.Code,
			DimensionName: d.Name,
			GroupCode:     d.GroupCode,
			Status:        "missing",
			Evidences:     []ProfileEvidence{},
			Trend:         []ProfileTrendPoint{},
		}
		var hit *domain.DimensionScore
		for i := range dimRows {
			r := &dimRows[i]
			if r.DimensionCode != d.Code || r.PeriodStartAt.Unix() != selStart.Unix() || r.PeriodEndAt.Unix() != selEnd.Unix() {
				continue
			}
			if r.Status == domain.ScoreStatusSuccess {
				hit = r
				break
			}
		}
		if hit != nil {
			sc := hit.Score
			row.Score = &sc
			if hit.Insufficient {
				row.Status = "insufficient"
			} else {
				row.Status = "normal"
			}
			row.Rationale = hit.Rationale
			row.Evidences = []ProfileEvidence{buildProfileEvidence(*hit)}
		}
		// trend：最近 TrendWindowSize 期 success 序列（含本期，failed/无行期次不入）。
		for _, p := range tailHistory(history[d.Code], TrendWindowSize) {
			row.Trend = append(row.Trend, ProfileTrendPoint{
				PeriodStart: p.start.In(time.Local).Format(layoutDate),
				PeriodEnd:   p.end.AddDate(0, 0, -1).In(time.Local).Format(layoutDate),
				Score:       p.score,
			})
		}
		if avg, ok := companyAvg[d.Code]; ok {
			a := avg
			row.CompanyAvg = &a
		}
		rows = append(rows, row)
	}
	return rows
}

// tailHistory 取尾部 n 期（旧到新序保持）。
func tailHistory[T any](list []T, n int) []T {
	if len(list) <= n {
		return list
	}
	return list[len(list)-n:]
}

// buildProfileEvidence 单维度行证据条目（03 B1 evidences[]）：time 取行 UpdatedAt
// 本地时区直出；conversation 按 EvidenceJSON.session_keys 数量映射置信度（BR4），
// active_test 恒 high；解析失败降级空 summary + low，不报错。
func buildProfileEvidence(r domain.DimensionScore) ProfileEvidence {
	ev := ProfileEvidence{
		Source:     r.Source,
		Time:       r.UpdatedAt.Local().Format(layoutDateTime),
		Confidence: "low",
		Summary:    map[string]any{},
	}
	if r.Source == domain.ScoreSourceActiveTest {
		ev.Confidence = "high"
		return ev
	}
	var parsed profileEvidenceJSON
	if err := json.Unmarshal([]byte(r.EvidenceJSON), &parsed); err != nil {
		return ev
	}
	ev.SessionCount = len(parsed.SessionKeys)
	switch {
	case ev.SessionCount >= ConfidenceHighSignals:
		ev.Confidence = "high"
	case ev.SessionCount >= ConfidenceLowSignals:
		ev.Confidence = "medium"
	}
	if parsed.Summary != nil {
		for k, v := range parsed.Summary {
			ev.Summary[k] = v
		}
	}
	return ev
}

// computeCompanyAverages 全公司所选区间维度行按 code 分组：剔 insufficient 与 failed
// 后求算术平均；有数据人数 < CompanyAvgMinPeople 的维度置空（BR3）。
func computeCompanyAverages(rows []domain.DimensionScore) map[string]float64 {
	type acc struct {
		sum   float64
		count int
	}
	byCode := map[string]*acc{}
	for _, r := range rows {
		if r.Status != domain.ScoreStatusSuccess || r.Insufficient {
			continue
		}
		a := byCode[r.DimensionCode]
		if a == nil {
			a = &acc{}
			byCode[r.DimensionCode] = a
		}
		a.sum += float64(r.Score)
		a.count++
	}
	out := make(map[string]float64, len(byCode))
	for code, a := range byCode {
		if a.count < CompanyAvgMinPeople {
			continue
		}
		out[code] = a.sum / float64(a.count)
	}
	return out
}

// resolveSecret 解密 userapi Bearer 密钥明文，失败由调用方统一映射 1305。
func (s *profileService) resolveSecret(ctx context.Context) (string, error) {
	return resolveUserapiSecret(ctx, s.secretRepo, s.encKey)
}

// resolveUserapiSecret 集成密钥解密共享私有实现（assessment_config 与 profile 共用）。
func resolveUserapiSecret(ctx context.Context, repo repository.IntegrationSecretRepository, encKey []byte) (string, error) {
	secret, err := repo.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("get integration secret: %w", err)
	}
	plaintext, derr := decryptSecretCipher(encKey, secret.SecretCipher)
	if derr != nil {
		return "", fmt.Errorf("resolve integration secret: %s", derr.Msg)
	}
	return plaintext, nil
}

// validProfileActivityLevel 活跃度筛选枚举校验。
func validProfileActivityLevel(l string) bool {
	switch l {
	case domain.ActiveLevelActive, domain.ActiveLevelLowFreq, domain.ActiveLevelUnused:
		return true
	}
	return false
}

// profileFilterableDimensionCodes 短板筛选合法选项集：启用且 module ∈ {AI_USAGE, AI_MGMT}。
func profileFilterableDimensionCodes(dims []domain.Dimension) map[string]struct{} {
	set := make(map[string]struct{}, len(dims))
	for _, d := range dims {
		if !d.Enabled {
			continue
		}
		if d.ModuleCode != domain.ModuleAIUsage && d.ModuleCode != domain.ModuleAIMgmt {
			continue
		}
		set[d.Code] = struct{}{}
	}
	return set
}

// profileModuleColumn 取单人单模块分数列与降权标记：无聚合行 nil/false（BR2）；
// 有聚合行时在同模块同周期维度行中查 insufficient 或 failed（BR3，只标注不改分）。
// 全剔除模块 ModuleScore 为 nil，透传 nil 由前端按待评估渲染。
func profileModuleColumn(aggBy map[profileModKey]domain.AggregateScore,
	dimsBy map[profileModKey][]domain.DimensionScore, staff, module string) (*float64, bool) {
	agg, ok := aggBy[profileModKey{staff, module}]
	if !ok {
		return nil, false
	}
	degraded := false
	for _, d := range dimsBy[profileModKey{staff, module}] {
		if d.PeriodStartAt.Equal(agg.PeriodStartAt) &&
			(d.Insufficient || d.Status == domain.ScoreStatusFailed) {
			degraded = true
			break
		}
	}
	return agg.ModuleScore, degraded
}

// includedWeight included_json 序列化单元（scorer.IncludedWeight 同构，解耦定义防跨包依赖）。
type includedWeight struct {
	Code   string `json:"code"`
	Weight int    `json:"weight"`
}

// parseIncludedCodes 解析聚合行 included_json 的参与聚合 code 集合（03 §1.9 判据）；
// 解析失败按空集（防御：正常聚合链路恒合法，模块行恒数组形态）。
func parseIncludedCodes(includedJSON string) map[string]struct{} {
	set := make(map[string]struct{})
	var ws []includedWeight
	if err := json.Unmarshal([]byte(includedJSON), &ws); err != nil {
		return set
	}
	for _, w := range ws {
		set[w.Code] = struct{}{}
	}
	return set
}

// buildShortboardSets 计算每人短板集合（BR4）：两模块各自最新聚合周期 included_json
// 的 code 并集为参与聚合维度，分数取同周期维度行 Score（仅 success 行计分），
// 合并后取最低分并列全选；无任何有效分维度的人无短板集合。
func buildShortboardSets(aggBy map[profileModKey]domain.AggregateScore,
	dimsBy map[profileModKey][]domain.DimensionScore) map[string]map[string]struct{} {
	type candidate struct {
		code  string
		score int
	}
	candBy := make(map[string][]candidate)
	for k, agg := range aggBy {
		codes := parseIncludedCodes(agg.IncludedJSON)
		if len(codes) == 0 {
			continue
		}
		for _, d := range dimsBy[k] {
			if _, ok := codes[d.DimensionCode]; !ok {
				continue
			}
			if !d.PeriodStartAt.Equal(agg.PeriodStartAt) {
				continue
			}
			if d.Status != domain.ScoreStatusSuccess {
				continue
			}
			candBy[k.staff] = append(candBy[k.staff], candidate{code: d.DimensionCode, score: d.Score})
		}
	}
	sets := make(map[string]map[string]struct{}, len(candBy))
	for staff, cands := range candBy {
		if len(cands) == 0 {
			continue
		}
		minScore := cands[0].score
		for _, c := range cands[1:] {
			if c.score < minScore {
				minScore = c.score
			}
		}
		set := make(map[string]struct{}, 1)
		for _, c := range cands {
			if c.score == minScore {
				set[c.code] = struct{}{}
			}
		}
		sets[staff] = set
	}
	return sets
}
