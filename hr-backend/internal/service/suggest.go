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

// suggestStuckThreshold 生成中行滞留判定阈值（specs §5.1.4 规则5/§5.1.5）：
// 覆盖 4 次执行×240s 加 30/60/120s 阶梯退避约 19.5 分钟合法在途；饱和排队等待
// 不在内，滞留续投可能与在途任务重复，幂等守卫收敛、代价至多一次额外 LLM 调用。
const suggestStuckThreshold = 20 * time.Minute

// suggestStuckReason 滞留行所属批次已删时的 MarkFailed 记因。
const suggestStuckReason = "建议生成滞留：所属批次已不存在"

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

// ProvideSuggestGenerator 把 *suggestgen.Generator 适配为 SuggestGenerator 窄接口，
// 供 wire 在 app 包注入（ProvideUserapiClient 同款形态）。
func ProvideSuggestGenerator(g *suggestgen.Generator) SuggestGenerator { return g }

// SuggestService 团队培训建议生成编排服务（specs §5.1）。
type SuggestService struct {
	suggestions repository.TeamTrainingSuggestionRepository
	queries     repository.DashboardQueryRepository
	batches     repository.AssessmentBatchRepository
	dims        repository.DimensionRepository
	gen         SuggestGenerator
	enq         SuggestEnqueuer
	secretRepo  repository.IntegrationSecretRepository
	encKey      []byte
	staffs      userapiClient
}

// NewSuggestService 构造建议生成 service：gen 为生成引擎（生产经
// ProvideSuggestGenerator 适配，测试可注入 fake）。
func NewSuggestService(suggestions repository.TeamTrainingSuggestionRepository,
	queries repository.DashboardQueryRepository,
	batches repository.AssessmentBatchRepository,
	dims repository.DimensionRepository,
	gen SuggestGenerator,
	enq SuggestEnqueuer,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	staffs userapiClient,
) *SuggestService {
	return &SuggestService{
		suggestions: suggestions,
		queries:     queries,
		batches:     batches,
		dims:        dims,
		gen:         gen,
		enq:         enq,
		secretRepo:  secretRepo,
		encKey:      encKey,
		staffs:      staffs,
	}
}

// TickScan tick 拾取编排（specs §5.1.2 步1/步2/步6，03 §4.1）：拾取扫描命中
// 则幂等建行/续作并投递 generate，无命中走滞留续投检查。投递失败行保持
// generating，重试 tick 被拾取锁拦下空转，滞留超阈值后由续投兜底（步5）。
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
		return s.retryStuckGenerating(ctx, rows, now)
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

// retryStuckGenerating 滞留续投（specs §5.1.4 规则5、§5.1.5 滞留恢复）：
// 拾取无命中时取超阈滞留行按 batch_no 重投 generate（幂等安全见 Generate
// 终态守卫）；批次已删落 failed。仅在扫描无命中分支执行，与拾取判定解耦。
func (s *SuggestService) retryStuckGenerating(ctx context.Context, rows []domain.TeamTrainingSuggestion, now time.Time) error {
	stuckAt := now.Add(-suggestStuckThreshold)
	var stuck *domain.TeamTrainingSuggestion
	// rows 由仓储按 period 新到旧排序，倒序遍历取滞留行中 period 最早者。
	for i := len(rows) - 1; i >= 0; i-- {
		r := &rows[i]
		if r.Status == domain.SuggestionStatusGenerating && r.UpdatedAt.Before(stuckAt) {
			stuck = r
			break
		}
	}
	if stuck == nil {
		return nil
	}
	batch, err := s.batches.GetByBatchNo(ctx, stuck.BatchNo)
	if err != nil {
		return fmt.Errorf("get batch by batch_no for stuck suggestion: %w", err)
	}
	if batch == nil {
		if err := s.suggestions.MarkFailed(ctx, stuck.ID, suggestStuckReason); err != nil {
			return fmt.Errorf("mark stuck suggestion failed: %w", err)
		}
		slog.Warn("suggest stuck row batch missing, mark failed", "batch_no", stuck.BatchNo)
		return nil
	}
	if err := s.enq.EnqueueSuggestGenerate(ctx, batch.ID); err != nil {
		return fmt.Errorf("enqueue suggest generate for stuck row: %w", err)
	}
	if err := s.suggestions.TouchGenerating(ctx, stuck.ID, now); err != nil {
		return fmt.Errorf("touch stuck suggestion row: %w", err)
	}
	slog.Warn("suggest stuck generating row re-enqueued", "batch_no", stuck.BatchNo,
		"period_start", stuck.PeriodStartAt.Format(layoutDate), "period_end", stuck.PeriodEndAt.Format(layoutDate))
	return nil
}

// Generate 生成任务编排（specs §5.1.2 步2-5，03 §4.2）：批次与建议行定位
//（竞态残留幂等返 nil）→ 数据异常判定（三素材表均无行落 failed 避免重扫）→
// 素材汇总（口径同看板）→ 引擎生成 → generated 终态落库。
// 常规 err 上抛交 Asynq 任务级重试，重试沿既有 generating 行续作（generate
// 任务的 status=generating 守卫收敛竞态）。
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
	out, modelName, err := s.gen.Generate(ctx, material)
	if err != nil {
		return fmt.Errorf("generate suggestion: %w", err)
	}
	modulesJSON, err := json.Marshal(out.Modules)
	if err != nil {
		return fmt.Errorf("marshal suggestion modules: %w", err)
	}
	if err := s.suggestions.MarkGenerated(ctx, row.ID, batch.BatchNo,
		string(modulesJSON), out.Summary, modelName, suggestgen.PromptVersion, time.Now().UTC()); err != nil {
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
