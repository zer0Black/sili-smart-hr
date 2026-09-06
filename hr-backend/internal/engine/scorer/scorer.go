package scorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/repository"
)

// ErrScoreRead 既有评分行读取失败（wrap 底层错误，specs §2.3 错误码表）。
var ErrScoreRead = errors.New("scorer: score read failed")

// ErrStoreWrite 聚合行落库失败（wrap 底层错误）：不落行，error 上抛交任务重试。
var ErrStoreWrite = errors.New("scorer: store write failed")

// Scorer 多维打分与聚合组件（specs §2.4 能力5）。
type Scorer struct {
	scoreRepo repository.DimensionScoreRepository
	aggRepo   repository.AggregateScoreRepository
}

// New 构造 Scorer。
func New(scoreRepo repository.DimensionScoreRepository, aggRepo repository.AggregateScoreRepository) *Scorer {
	return &Scorer{scoreRepo: scoreRepo, aggRepo: aggRepo}
}

// IncludedWeight included_json 序列化单元（specs §2.3）。
type IncludedWeight struct {
	Code   string `json:"code"`
	Weight int    `json:"weight"`
}

// AggregateResult 聚合结果（specs §2.3）。
type AggregateResult struct {
	ModuleScores map[string]*float64
	Overview     *float64
	Included     map[string][]IncludedWeight
	Excluded     map[string][]string
}

// evidenceDoc evidence_json 解析侧结构：仅消费维度口径摘要（键名与 evaluator
// buildEvidence 对齐，不共享类型防循环依赖）。
type evidenceDoc struct {
	DimensionSpecs []specSnapshot `json:"dimension_specs"`
}

// specSnapshot 单维度口径摘要条目：weight 与 in_overview 是聚合唯一权重来源。
type specSnapshot struct {
	Code       string `json:"code"`
	Weight     int    `json:"weight"`
	InOverview bool   `json:"in_overview"`
}

// moduleAgg 模块内聚合累计器。
type moduleAgg struct {
	scoreSum  float64 // Σ(score×weight)
	weightSum int
	included  []IncludedWeight
	excluded  []string
}

// Aggregate 重算单人周期聚合行（读全部 source 评分行），幂等（specs §2.4 能力5）。
// 权重与 InOverview 一律取评分行 evidence_json 口径摘要，不从 dimension 域现读。
func (s *Scorer) Aggregate(ctx context.Context, tokenName string, period activity.Period) (*AggregateResult, error) {
	rows, err := s.scoreRepo.ListByPersonPeriodExact(ctx, tokenName, period.Start, period.End)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrScoreRead, err)
	}

	groups := make(map[string]*moduleAgg)
	for i := range rows {
		r := &rows[i]
		g := groups[r.Module]
		if g == nil {
			g = &moduleAgg{included: make([]IncludedWeight, 0), excluded: make([]string, 0)}
			groups[r.Module] = g
		}
		if !classifyRow(r, g) {
			g.excluded = append(g.excluded, r.DimensionCode)
		}
	}

	result := &AggregateResult{
		ModuleScores: make(map[string]*float64, len(groups)),
		Included:     make(map[string][]IncludedWeight, len(groups)),
		Excluded:     make(map[string][]string, len(groups)),
	}
	var totalScore float64
	var totalWeight int
	modules := make([]string, 0, len(groups))
	for m := range groups {
		modules = append(modules, m)
	}
	sort.Strings(modules)

	storeRows := make([]domain.AggregateScore, 0, len(modules)+1)
	for _, m := range modules {
		g := groups[m]
		result.Included[m] = g.included
		result.Excluded[m] = g.excluded
		ms := moduleScore(g)
		result.ModuleScores[m] = ms // 全剔除模块键存在值 nil（与落库行同口径）
		storeRows = append(storeRows, domain.AggregateScore{
			Module:       m,
			ModuleScore:  ms,
			IncludedJSON: marshalList(g.included),
			ExcludedJSON: marshalList(g.excluded),
		})
		if g.weightSum > 0 {
			totalScore += g.scoreSum
			totalWeight += g.weightSum
		}
	}
	if totalWeight > 0 {
		v := roundHalfUp(totalScore / float64(totalWeight))
		result.Overview = &v
	}
	storeRows = append(storeRows, domain.AggregateScore{
		Module:        domain.ModuleOverview,
		OverviewScore: result.Overview,
		IncludedJSON:  marshalIncludedMap(result.Included),
		ExcludedJSON:  marshalExcludedMap(result.Excluded),
	})

	if err := s.aggRepo.UpsertAll(ctx, tokenName, period.Start, period.End, storeRows); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStoreWrite, err)
	}
	return result, nil
}

// classifyRow 行分类：failed、insufficient、evidence 解析失败或口径摘要缺条目的
// 行返回 false（剔除进 Excluded）；in_overview=false 参考维返回 true 但不累计
// （不进聚合也不进 Excluded）；正常参与维返回 true 并累计入模块聚合器。
func classifyRow(r *domain.DimensionScore, g *moduleAgg) bool {
	if r.Status != domain.ScoreStatusSuccess || r.Insufficient {
		return false // failed 与 insufficient 一律剔除（specs 能力5 规则1）
	}
	var doc evidenceDoc
	if err := json.Unmarshal([]byte(r.EvidenceJSON), &doc); err != nil {
		return false // 坏行防御：解析失败按剔除处理，不阻断聚合
	}
	for _, sp := range doc.DimensionSpecs {
		if sp.Code != r.DimensionCode {
			continue
		}
		if !sp.InOverview {
			return true // 参考性维度：只落评分行，不进聚合（specs 能力5 注意事项）
		}
		g.scoreSum += float64(r.Score) * float64(sp.Weight)
		g.weightSum += sp.Weight
		g.included = append(g.included, IncludedWeight{Code: sp.Code, Weight: sp.Weight})
		return true
	}
	return false // 口径摘要缺本维度条目：无权重不可参与，按剔除处理
}

// moduleScore 模块分 = Σ(score×weight)/Σweight，roundHalfUp 一位小数；无参与维 nil。
func moduleScore(g *moduleAgg) *float64 {
	if g.weightSum == 0 {
		return nil
	}
	v := roundHalfUp(g.scoreSum / float64(g.weightSum))
	return &v
}

// marshalList 序列化列表快照，空集落 "[]" 防 NOT NULL 列写 "null"。
func marshalList[T any](list []T) string {
	if list == nil {
		return "[]"
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// marshalIncludedMap 序列化模块→参与清单快照，空 map 落 "{}"。
func marshalIncludedMap(m map[string][]IncludedWeight) string {
	raw, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// marshalExcludedMap 序列化模块→剔除清单快照，空 map 落 "{}"。
func marshalExcludedMap(m map[string][]string) string {
	raw, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// roundHalfUp 一位小数舍入（specs §2.4 能力5 注意事项）：分数域非负，
// math.Round 的 half away from zero 在此与 half up 等价。
func roundHalfUp(v float64) float64 {
	return math.Round(v*10) / 10
}
