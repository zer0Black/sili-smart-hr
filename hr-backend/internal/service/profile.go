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
	"sort"
	"strings"

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

// ProfileDetailDTO B1 详情响应载荷（字段集在详情任务落地，此处仅保接口编译完整）。
type ProfileDetailDTO struct{}

type profileService struct {
	dimScores  repository.DimensionScoreRepository       // T1 三方法
	aggScores  repository.AggregateScoreRepository       // T2 两方法
	activity   repository.ActivityStatRepository         // T2 两方法
	results    repository.AssessmentTestResultRepository // T2 一方法
	dimensions repository.DimensionRepository            // ListAll 基准集合（已有）
	userapi    userapiClient                             // service 包内既有鸭子接口（*userapi.Client 适配）
	secretRepo repository.IntegrationSecretRepository    // resolveSecret
	encKey     string
}

// NewProfileService 构造个人画像域 service，Wire 自动装配。
func NewProfileService(
	dimScores repository.DimensionScoreRepository,
	aggScores repository.AggregateScoreRepository,
	activity repository.ActivityStatRepository,
	results repository.AssessmentTestResultRepository,
	dimensions repository.DimensionRepository,
	userapi userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey string,
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

// List 编排：校验 → 密钥 → 全量名单 → 批量 IN 四表 → 内存组装/筛选/排序/分页（03 §4.3）。
func (s *profileService) List(ctx context.Context, f ProfileFilter) (*ProfileListResult, error) {
	if f.ActivityLevel != "" && !validProfileActivityLevel(f.ActivityLevel) {
		return nil, NewError(errcode.BadRequest)
	}
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
		return &ProfileListResult{List: []ProfileListItem{}, Total: 0, Page: page, PageSize: pageSize}, nil
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

	// 姓名升序（specs §8.3 偏离记录）+ 内存分页。
	sort.SliceStable(items, func(i, j int) bool { return items[i].StaffName < items[j].StaffName })
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

// Detail 查询画像详情聚合（03 B1），实现待详情任务落地。
func (s *profileService) Detail(ctx context.Context, staffName string, periodStart, periodEnd string) (*ProfileDetailDTO, error) {
	panic("not implemented")
}

// Export 按筛选条件全量导出 xlsx（03 A2），实现见 profile_export.go。
func (s *profileService) Export(ctx context.Context, f ProfileFilter) ([]byte, string, error) {
	panic("not implemented")
}

// resolveSecret 解密 userapi Bearer 密钥明文，失败由调用方统一映射 1305。
func (s *profileService) resolveSecret(ctx context.Context) (string, error) {
	return resolveUserapiSecret(ctx, s.secretRepo, []byte(s.encKey))
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
