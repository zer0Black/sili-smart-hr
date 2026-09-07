// Package activity 是使用活跃度统计子域：纯规则统计，不调 LLM。
package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/repository"
)

// 哨兵错误（specs §2.3 错误码表）：可 errors.Is 命中，供 pipeline/监控按码分类。
var (
	// ErrSessionListFetch 会话列表拉取失败（上游重试耗尽透传记因）。
	ErrSessionListFetch = errors.New("activity: session list fetch failed")
	// ErrProfileRead 档案表读取失败。
	ErrProfileRead = errors.New("activity: profile read failed")
	// ErrDimensionConfigRead 活跃度阈值读取失败（适配层 wrap 后透传）。
	ErrDimensionConfigRead = errors.New("activity: dimension config read failed")
	// ErrStoreWrite 活跃度行落库失败。
	ErrStoreWrite = errors.New("activity: store write failed")
)

// listPageSize 列表翻页页大小（上游上限 100，specs §2.4 能力3 注意事项）。
const listPageSize = 100

// Period 评估区间（specs §2.2），闭开区间 [Start, End)，Unix 秒。
// 落 activity 包（消费主体），evaluator import 复用。
type Period struct {
	Start int64 // 含
	End   int64 // 不含
}

// ActivityStat 统计产出（specs §2.3 字段一一对应，落库列映射在 repository 侧）。
type ActivityStat struct {
	SessionCount      int
	ValidSessionCount int
	SkippedCount      int
	TotalTurns        int
	ActiveLevel       string
	PopulationNote    string
	ClientDist        map[string]int
}

// ThresholdReader 活跃度阈值读取窄接口，DimensionRepository 经装配层适配满足
// （GetActivitySetting 返回 *domain.DimensionSetting）。
type ThresholdReader interface {
	ActivityThresholds(ctx context.Context) (active, lowFreq int, err error)
}

// SessionListFetcher 列表拉取窄接口（*conversationlog.Client 鸭子满足）。
type SessionListFetcher interface {
	ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error)
}

// SecretProvider 同 extractor 形态（装配层解密注入）。
type SecretProvider func(ctx context.Context) (string, error)

// Activity 是使用活跃度统计组件（specs §2.4 能力3）。
type Activity struct {
	cl          SessionListFetcher
	featureRepo repository.SessionFeatureRepository
	thresholds  ThresholdReader
	repo        repository.ActivityStatRepository
	secrets     SecretProvider
}

// New 构造 Activity，五个依赖集中注入。
func New(cl SessionListFetcher, featureRepo repository.SessionFeatureRepository,
	thresholds ThresholdReader, repo repository.ActivityStatRepository,
	secrets SecretProvider) *Activity {
	return &Activity{
		cl:          cl,
		featureRepo: featureRepo,
		thresholds:  thresholds,
		repo:        repo,
		secrets:     secrets,
	}
}

// StatPersonByKey 拉列表并统计（specs §2.4 能力3）：全量串行翻页 → token_name
// 内存分组 → DedupSessions → statPersonCore。生产评估链走 WithSessions 透出形态
//（EvaluatePerson 消费），本形态保留给 specs 接口表的定向分析单人场景。
func (a *Activity) StatPersonByKey(ctx context.Context, tokenName string, period Period) (*ActivityStat, error) {
	stat, _, _, err := a.StatPersonByKeyWithSessions(ctx, tokenName, period)
	return stat, err
}

// StatPersonByKeyWithSessions StatPersonByKey 的列表与档案透出形态（specs §2.4
// 能力6 组合入口消费）：额外返回拉取到的窗口内列表与档案集（已归一过滤），
// 供 EvaluatePerson 注入 Evaluate 免二次拉取与二次取数。
func (a *Activity) StatPersonByKeyWithSessions(ctx context.Context, tokenName string, period Period) (*ActivityStat, []conversationlog.SessionSummary, []ProfileDigest, error) {
	secret, err := a.secrets(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("activity: resolve integration secret: %w", err)
	}
	all, err := a.fetchAllSessions(ctx, secret, period)
	if err != nil {
		return nil, nil, nil, err
	}
	mine := make([]conversationlog.SessionSummary, 0, len(all))
	for _, s := range all {
		if s.TokenName != tokenName {
			continue
		}
		mine = append(mine, s)
	}
	mine = DedupSessions(mine) // 跨页重复去重后透出（能力4 两处口径一致）
	digests, err := a.fetchProfiles(ctx, tokenName, period)
	if err != nil {
		return nil, nil, nil, err
	}
	stat, err := a.statPersonCore(ctx, tokenName, mine, digests, period)
	if err != nil {
		return nil, nil, nil, err
	}
	return stat, mine, digests, nil
}

// fetchAllSessions 串行翻页拉全量列表（并发翻页会返回重复页，specs §2.4 能力3
// 注意事项）：total 驱动翻页，累计条数覆盖 total 即止，防上游 total 抖动死循环。
func (a *Activity) fetchAllSessions(ctx context.Context, secret string, period Period) ([]conversationlog.SessionSummary, error) {
	var all []conversationlog.SessionSummary
	page := 1
	for {
		items, total, err := a.cl.ListSessions(ctx, secret, conversationlog.ListSessionsRequest{
			StartTime: period.Start,
			EndTime:   period.End,
			Page:      page,
			PageSize:  listPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSessionListFetch, err)
		}
		all = append(all, items...)
		// 三重终止：累计覆盖 total、空页、短页（len(items) < 页大小即上游无更多数据，
		// 防 total 低报时提前截断静默丢会话）。
		if int64(len(all)) >= total || len(items) < listPageSize {
			return all, nil
		}
		page++
	}
}

// StatPerson 对调用方传入的列表统计（sessions 须已按窗口过滤；档案侧数据由
// 内部经 ListByPersonAndRange 读取）。落库经 repo.Upsert 幂等覆盖。
// 透出取回的档案集，供 EvaluatePerson 注入 Evaluate 免二次取数（与
// StatPersonByKeyWithSessions 对称）。
func (a *Activity) StatPerson(ctx context.Context, sessions []conversationlog.SessionSummary, tokenName string, period Period) (*ActivityStat, []ProfileDigest, error) {
	profiles, err := a.fetchProfiles(ctx, tokenName, period)
	if err != nil {
		return nil, nil, err
	}
	stat, err := a.statPersonCore(ctx, tokenName, sessions, profiles, period)
	if err != nil {
		return nil, nil, err
	}
	return stat, profiles, nil
}

// DedupSessions 按 session_key 去重（跨页重复兜底），保序保留首见行。
// 计数、签名识别与 Evaluate 侧签名入参三处共用的单一权威实现。
func DedupSessions(sessions []conversationlog.SessionSummary) []conversationlog.SessionSummary {
	if len(sessions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(sessions))
	out := make([]conversationlog.SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		if _, dup := seen[s.SessionKey]; dup {
			continue
		}
		seen[s.SessionKey] = struct{}{}
		out = append(out, s)
	}
	return out
}

// statPersonCore 统计共核（specs §2.4 能力3 步骤2-5）：列表去重计数 + 档案
// status/client 分布 + 分级判定 + 签名识别 + 落库。profiles 由调用侧取数
// （两条路径共用 fetchProfiles 单点，口径恒同源）。
func (a *Activity) statPersonCore(ctx context.Context, tokenName string, sessions []conversationlog.SessionSummary, profiles []ProfileDigest, period Period) (*ActivityStat, error) {
	active, lowFreq, err := a.thresholds.ActivityThresholds(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDimensionConfigRead, err)
	}

	// 列表口径：session_key 去重兜底（跨页重复），TotalTurns 含 skipped 会话。
	dedup := DedupSessions(sessions)
	totalTurns := 0
	for _, s := range dedup {
		totalTurns += s.TurnCount
	}

	// 档案口径：status 分布与 client 分布同源同一过滤后集合。
	valid, skipped := 0, 0
	clientDist := make(map[string]int, len(profiles))
	for _, p := range profiles {
		switch p.Status {
		case domain.FeatureStatusSuccess, domain.FeatureStatusFailed:
			valid++
		case domain.FeatureStatusSkipped:
			skipped++
		}
		clientDist[p.Client]++
	}

	level := domain.ActiveLevelUnused
	switch {
	case valid >= active:
		level = domain.ActiveLevelActive
	case valid >= lowFreq:
		level = domain.ActiveLevelLowFreq
	}

	// 人群签名（能力4）共用同一过滤后集合，auto_client 命中强制 unused（C08）。
	sig := IdentifyPopulation(dedup, profiles, lowFreq)
	if sig.Kind == KindAutoClient {
		level = domain.ActiveLevelUnused
	}

	distJSON, err := json.Marshal(clientDist)
	if err != nil {
		return nil, fmt.Errorf("activity: marshal client dist: %w", err)
	}
	rec := &domain.ActivityStat{
		TokenName:         tokenName,
		PeriodStartAt:     time.Unix(period.Start, 0).UTC(),
		PeriodEndAt:       time.Unix(period.End, 0).UTC(),
		SessionCount:      len(dedup),
		ValidSessionCount: valid,
		SkippedCount:      skipped,
		TotalTurns:        totalTurns,
		ActiveLevel:       level,
		PopulationNote:    sig.Note,
		ClientDistJSON:    string(distJSON),
	}
	if err := a.repo.Upsert(ctx, rec); err != nil {
		slog.Error("activity stat store failed", "token_name", tokenName, "code", "ErrStoreWrite")
		return nil, fmt.Errorf("%w: %w", ErrStoreWrite, err)
	}
	if sig.Kind != KindNormal {
		slog.Info("population signature", "token_name", tokenName, "kind", sig.Kind)
	}
	return &ActivityStat{
		SessionCount:      rec.SessionCount,
		ValidSessionCount: rec.ValidSessionCount,
		SkippedCount:      rec.SkippedCount,
		TotalTurns:        rec.TotalTurns,
		ActiveLevel:       rec.ActiveLevel,
		PopulationNote:    rec.PopulationNote,
		ClientDist:        clientDist,
	}, nil
}

// fetchProfiles 档案取数单点（specs §2.4 能力3 步骤3），evaluator 经
// FetchWindowDigests 共用同款取数口径。
func (a *Activity) fetchProfiles(ctx context.Context, tokenName string, period Period) ([]ProfileDigest, error) {
	return FetchWindowDigests(ctx, a.featureRepo, tokenName, period)
}

// FetchWindowDigests 档案取数归一单点（specs §2.4 能力3 步骤3）：ListByPersonAndRange
// start 前移 24h 缓冲取回三态行（容纳跨边界行）→ ParseDigests → 内存过滤
// LastTurn ∈ [Start, End) 归一到本周期。计数、签名判据与评分证据三侧共用本函数。
func FetchWindowDigests(ctx context.Context, repo repository.SessionFeatureRepository, tokenName string, period Period) ([]ProfileDigest, error) {
	rows, err := repo.ListByPersonAndRange(ctx, tokenName, period.Start-24*3600, period.End)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfileRead, err)
	}
	all := ParseDigests(rows)
	filtered := make([]ProfileDigest, 0, len(all))
	for _, d := range all {
		last := d.LastTurn.UTC().Unix()
		if last >= period.Start && last < period.End {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}
