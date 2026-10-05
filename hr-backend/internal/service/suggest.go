// suggest 建议生成编排业务层（specs P2_TMD_001 §5.1.2/§5.1.4/§5.1.5，03 §4.1/§4.2）：
// tick 拾取扫描（幂等建行 + 投递）与 generate 任务编排（素材汇总 → 引擎 → 终态落库）。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/suggestgen"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/repository"
)

// suggestAnomalyReason 批次数据异常落 failed 的固定记因（specs §5.1.5 末行）。
const suggestAnomalyReason = "批次数据异常：该评估区间无任何聚合素材行"

// suggestReasonMaxLen 失败原因列宽对齐（04 §3.1 error_summary varchar(255)）。
const suggestReasonMaxLen = 255

// SuggestEnqueuer 建议生成任务投递窄接口：service 层不 import asynq，
// 根层适配器（T7 AsynqSuggestEnqueuer）实现（GenerationEnqueuer 同款形态）。
type SuggestEnqueuer interface {
	EnqueueSuggestGenerate(ctx context.Context, batchID int64) error
}

// SuggestGenerator 建议生成引擎窄面：*suggestgen.Generator 鸭子满足，
// 抽象成接口便于测试注入 fake（llm.Client 窄面同思路）。
type SuggestGenerator interface {
	Generate(ctx context.Context, m suggestgen.Material) (suggestgen.Output, string, error)
}

// SuggestGeneratorInjector 生成器注入器：生产装配传 nil（沿用构造入参 gen），
// 测试传返回 fake 的闭包。
type SuggestGeneratorInjector func() SuggestGenerator

// SuggestService 团队培训建议生成编排服务（specs §5.1）。
type SuggestService struct {
	suggestions repository.TeamTrainingSuggestionRepository
	queries     repository.DashboardQueryRepository
	batches     repository.AssessmentBatchRepository
	dims        repository.DimensionRepository
	gen         SuggestGeneratorInjector
	enq         SuggestEnqueuer
	secretRepo  repository.IntegrationSecretRepository
	encKey      []byte
	staffs      userapiClient
}

// NewSuggestService 构造建议生成 service：gen 为生产引擎（T7 wire 装配），
// inject 仅测试用（nil 时沿用 gen）。
func NewSuggestService(suggestions repository.TeamTrainingSuggestionRepository,
	queries repository.DashboardQueryRepository,
	batches repository.AssessmentBatchRepository,
	dims repository.DimensionRepository,
	gen *suggestgen.Generator,
	enq SuggestEnqueuer,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	staffs userapiClient,
	inject SuggestGeneratorInjector,
) *SuggestService {
	if inject == nil {
		g := gen
		inject = func() SuggestGenerator { return g }
	}
	return &SuggestService{
		suggestions: suggestions,
		queries:     queries,
		batches:     batches,
		dims:        dims,
		gen:         inject,
		enq:         enq,
		secretRepo:  secretRepo,
		encKey:      encKey,
		staffs:      staffs,
	}
}

// TickScan tick 拾取编排（specs §5.1.2 步1/步2/步6，03 §4.1）：
// 建议行全量喂拾取扫描取最早待处理终态批次 → 无命中空转返 nil（生成中行即
// 拾取锁）→ 幂等建行/续作 → 投递 generate 任务。投递失败 err 透传交 tick
// 任务级重试，行保持 generating 由下个 tick 沿既有行续作。
func (s *SuggestService) TickScan(ctx context.Context, now time.Time) error {
	rows, err := s.suggestions.FindAll(ctx)
	if err != nil {
		return fmt.Errorf("find suggestion rows: %w", err)
	}
	batch, err := s.queries.FindEarliestPendingSuggestBatch(ctx, rows)
	if err != nil {
		return fmt.Errorf("scan pending suggest batch: %w", err)
	}
	if batch == nil {
		return nil
	}
	row := domain.TeamTrainingSuggestion{
		BatchNo:       batch.BatchNo,
		PeriodStartAt: batch.PeriodStartAt,
		PeriodEndAt:   batch.PeriodEndAt,
		Status:        domain.SuggestionStatusGenerating,
	}
	if _, err := s.suggestions.EnsureGenerating(ctx, row); err != nil {
		return fmt.Errorf("ensure generating suggestion row: %w", err)
	}
	slog.Info("suggest tick picked batch", "batch_no", batch.BatchNo)
	if err := s.enq.EnqueueSuggestGenerate(ctx, batch.ID); err != nil {
		return fmt.Errorf("enqueue suggest generate: %w", err)
	}
	return nil
}

// Generate 生成任务编排（specs §5.1.2 步2-5，03 §4.2）：批次与建议行定位
//（竞态残留幂等返 nil）→ 数据异常判定（三素材表均无行落 failed 避免重扫）→
// 素材汇总（口径同看板）→ 引擎生成 → generated 终态落库。
// 常规 err 上抛交 Asynq 任务级重试，沿既有 generating 行续作（specs §5.1.4 规则3）。
func (s *SuggestService) Generate(ctx context.Context, batchID int64) error {
	batch, err := s.batches.GetByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("get assessment batch: %w", err)
	}
	if batch == nil {
		return nil
	}
	row, err := s.suggestions.GetByPeriod(ctx, batch.PeriodStartAt.Unix(), batch.PeriodEndAt.Unix())
	if err != nil {
		return fmt.Errorf("get suggestion row: %w", err)
	}
	if row == nil || row.Status != domain.SuggestionStatusGenerating {
		return nil
	}

	bounds := []repository.PeriodBound{{StartAt: batch.PeriodStartAt, EndAt: batch.PeriodEndAt}}
	dimRows, err := s.queries.ListDimScoresByPeriod(ctx, batch.PeriodStartAt.Unix(), batch.PeriodEndAt.Unix())
	if err != nil {
		return fmt.Errorf("list dimension scores: %w", err)
	}
	aggRows, err := s.queries.ListModuleAggScoresByPeriods(ctx, bounds)
	if err != nil {
		return fmt.Errorf("list module agg scores: %w", err)
	}
	actRows, err := s.queries.ListActivityByPeriod(ctx, batch.PeriodStartAt.Unix(), batch.PeriodEndAt.Unix())
	if err != nil {
		return fmt.Errorf("list activity stats: %w", err)
	}
	// 批次数据异常（specs §5.1.5 末行）：三表均无行才命中（dimension_scores 与
	// aggregate_scores 均无任何行，且仅有的 activity 亦无行）；仅 activity_stats
	// 有行的正常周期（如启用首周全员未使用）不属异常，照常生成。
	if len(dimRows) == 0 && len(aggRows) == 0 && len(actRows) == 0 {
		if err := s.suggestions.MarkFailed(ctx, row.ID, suggestAnomalyReason); err != nil {
			return fmt.Errorf("mark suggestion failed: %w", err)
		}
		slog.Warn("suggest batch data anomaly, mark failed", "batch_no", batch.BatchNo)
		return nil
	}

	material, err := s.buildMaterial(ctx, batch, dimRows, aggRows, actRows)
	if err != nil {
		return err
	}
	out, modelName, err := s.gen().Generate(ctx, material)
	if err != nil {
		return fmt.Errorf("generate suggestion: %w", err)
	}
	modulesJSON, err := json.Marshal(out.Modules)
	if err != nil {
		return fmt.Errorf("marshal suggestion modules: %w", err)
	}
	if err := s.suggestions.MarkGenerated(ctx, row.ID, batch.BatchNo,
		string(modulesJSON), out.Summary, modelName, time.Now().UTC()); err != nil {
		return fmt.Errorf("mark suggestion generated: %w", err)
	}
	slog.Info("team suggestion generated", "batch_no", batch.BatchNo, "model_name", modelName)
	return nil
}

// MarkFailedIfExhausted 重试耗尽落库钩子（specs §5.1.4 规则3、03 §4.2 末段）：
// 定位该 period 建议行，generating 行落 failed 记因；终态行或批次缺失幂等返回。
func (s *SuggestService) MarkFailedIfExhausted(ctx context.Context, batchID int64, reason string) error {
	batch, err := s.batches.GetByID(ctx, batchID)
	if err != nil {
		return fmt.Errorf("get assessment batch: %w", err)
	}
	if batch == nil {
		return nil
	}
	row, err := s.suggestions.GetByPeriod(ctx, batch.PeriodStartAt.Unix(), batch.PeriodEndAt.Unix())
	if err != nil {
		return fmt.Errorf("get suggestion row: %w", err)
	}
	if row == nil || row.Status != domain.SuggestionStatusGenerating {
		return nil
	}
	if err := s.suggestions.MarkFailed(ctx, row.ID, truncateSuggestReason(reason)); err != nil {
		return fmt.Errorf("mark suggestion failed: %w", err)
	}
	slog.Warn("suggest retries exhausted, mark failed", "batch_no", batch.BatchNo)
	return nil
}

// buildMaterial 素材汇总（specs §5.1.2 步3、§5.1.3，口径同看板 A1）：
// 活跃度三态计数（userapi 全员为分母，无统计行并入未使用）、两模块维度均分与
// 低分占比（aggregateDimScores 同款）、模块聚合分（module_score 平均，上游加权
// 平均口径）、共性短板清单（weaknessSet 同款）与维度口径快照（评分锚点摘要）。
func (s *SuggestService) buildMaterial(ctx context.Context, batch *domain.AssessmentBatch,
	dimRows []domain.DimensionScore, aggRows []domain.AggregateScore, actRows []domain.ActivityStat) (suggestgen.Material, error) {
	material := suggestgen.Material{
		PeriodStart: batch.PeriodStartAt.Local().Format(layoutDate),
		PeriodEnd:   batch.PeriodEndAt.AddDate(0, 0, -1).Local().Format(layoutDate),
	}

	secret, err := resolveUserapiSecret(ctx, s.secretRepo, s.encKey)
	if err != nil {
		return material, fmt.Errorf("resolve userapi secret: %w", err)
	}
	names, err := userapi.FetchAllStaff(ctx, s.staffs, secret)
	if err != nil {
		return material, fmt.Errorf("fetch staff list: %w", err)
	}
	material.Activity.StaffTotal = len(names)

	seen := map[string]struct{}{}
	for _, r := range actRows {
		switch r.ActiveLevel {
		case domain.ActiveLevelActive:
			material.Activity.Active++
		case domain.ActiveLevelLowFreq:
			material.Activity.LowFreq++
		case domain.ActiveLevelUnused:
			material.Activity.Unused++
		}
		seen[r.TokenName] = struct{}{}
	}
	// 无统计行人员并入未使用（specs §4.1.4 规则2 看板同口径）。
	if noRow := material.Activity.StaffTotal - len(seen); noRow > 0 {
		material.Activity.Unused += noRow
	}

	dims, err := s.dims.ListAll(ctx)
	if err != nil {
		return material, fmt.Errorf("load dimensions: %w", err)
	}

	// 模块聚合分：module_score 算术平均（specs §5.1.2 步3，module<>overview 已由仓储过滤）。
	aggSum, aggCnt := map[string]float64{}, map[string]int{}
	for _, r := range aggRows {
		if r.ModuleScore == nil {
			continue
		}
		aggSum[r.Module] += *r.ModuleScore
		aggCnt[r.Module]++
	}

	sourceOf := map[string]string{
		domain.ModuleAIUsage: domain.ScoreSourceConversation,
		domain.ModuleAIMgmt:  domain.ScoreSourceActiveTest,
	}
	for _, module := range []string{domain.ModuleAIUsage, domain.ModuleAIMgmt} {
		aggs := aggregateDimScores(dimRows, sourceOf[module])
		weak := weaknessSet(aggs)
		mod := suggestgen.ModuleMaterial{Module: module, DimAverages: []suggestgen.DimAvg{}}
		if cnt := aggCnt[module]; cnt > 0 {
			v := aggSum[module] / float64(cnt)
			mod.AggScore = &v
		}
		for _, d := range dims {
			if !d.Enabled || d.ModuleCode != module {
				continue
			}
			material.DimSpecs = append(material.DimSpecs,
				suggestgen.DimSpec{Code: d.Code, Name: d.Name, Anchor: d.Anchor})
			item := suggestgen.DimAvg{Code: d.Code, Name: d.Name}
			if a, ok := aggs[d.Code]; ok {
				avg := math.Round(a.avg*10) / 10
				low := round1Percent(a.lowCount, a.count)
				item.Avg, item.LowRatio = &avg, &low
				if _, isWeak := weak[d.Code]; isWeak {
					material.WeakDimensions = append(material.WeakDimensions, suggestgen.WeakDimMaterial{
						Module: module, Code: d.Code, Name: d.Name, Avg: avg,
					})
				}
			}
			mod.DimAverages = append(mod.DimAverages, item)
		}
		material.Modules = append(material.Modules, mod)
	}
	return material, nil
}

// truncateSuggestReason 失败原因按字符截断至列宽（多字节防腰斩）。
func truncateSuggestReason(s string) string {
	if utf8.RuneCountInString(s) <= suggestReasonMaxLen {
		return s
	}
	return string([]rune(s)[:suggestReasonMaxLen])
}
