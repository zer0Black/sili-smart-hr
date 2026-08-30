package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor/rules"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/repository"
)

// ConversationlogDetailFetcher 是详情拉取依赖（P2_TECH_002 客户端满足此接口）。
type ConversationlogDetailFetcher interface {
	GetSessionDetail(ctx context.Context, secret, sessionKey string) (*conversationlog.SessionDetail, error)
}

// SecretProvider 提供上游集成密钥明文（装配层从 IntegrationSecretRepository 解密构造）。
type SecretProvider func(ctx context.Context) (string, error)

// Extractor 是会话特征抽取组件（specs §2.2 输入定义）。
type Extractor struct {
	llm     llm.Client
	cl      ConversationlogDetailFetcher
	repo    repository.SessionFeatureRepository
	params  repository.SystemParamReader // 黑名单与脱敏正则热调读取
	secrets SecretProvider
}

// New 构造 Extractor，五个依赖集中注入（Trim 只用 params，其余供全流程接线）。
func New(llmClient llm.Client, cl ConversationlogDetailFetcher, repo repository.SessionFeatureRepository, sysParams repository.SystemParamReader, secrets SecretProvider) *Extractor {
	return &Extractor{
		llm:     llmClient,
		cl:      cl,
		repo:    repo,
		params:  sysParams,
		secrets: secrets,
	}
}

// blankEntriesRemoved 过滤空串条目并返回新切片：前缀侧 HasPrefix(text,"")
// 恒真会全量误杀，脱敏侧空正则零宽匹配会乱形。
func blankEntriesRemoved(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// readParams 单次往返批量读两参数键，回退口径与单键读一致：DB 故障整体回退出厂
//（记 WARN 留观测），键缺失回退 nil（调用方按 nil 判回退），空集是运维显式语义。
func (e *Extractor) readParams() (prefixes, patterns []string) {
	loaded, err := e.params.ReadStringArrays(ParamKeyInjectPrefixes, ParamKeyRedactPatterns)
	if err != nil {
		slog.Warn("read extractor params failed, fallback to factory set", "err", err)
		return nil, nil
	}
	prefixes = loaded[ParamKeyInjectPrefixes]
	patterns = loaded[ParamKeyRedactPatterns]
	return prefixes, patterns
}

// injectPrefixes 过滤参数读出侧的追加集（空串条目剔除：HasPrefix(text,"")
// 恒真会全量误杀）。生效集组装（出厂并集 ∪ 追加集）单点收敛在
// classifySequence 侧的 appendEffectivePrefixes。
func (e *Extractor) injectPrefixes(prefixes []string) []string {
	return blankEntriesRemoved(prefixes)
}

// redactPatterns 读脱敏正则集：DB 故障回退出厂（安全机制不失效，故障记 WARN）。
// 过滤后空集按显式清空语义处理（nil 走 Redact 出厂回退，见 redact.go）。
func (e *Extractor) redactPatterns(patterns []string) []string {
	if patterns == nil {
		return nil // nil 语义为走 Redact 出厂分支
	}
	filtered := blankEntriesRemoved(patterns)
	if len(filtered) == 0 {
		slog.Warn("redact patterns all blank entries, fallback to factory set",
			"raw_count", len(patterns))
		return nil
	}
	return filtered
}

// preparedData 是判定与 LLM 段的共享中间结构：参数各读一次、分类算一次、
// 视图与统计各算一次，ShouldSkip、Extract 与 Trim 消费同一份，防两套判定漂移。
type preparedData struct {
	view           TrimmedView
	trimStats      TrimStats
	profile        ProfileStats
	redactPatterns []string // 输出侧脱敏复用，免二次点查
	// 规则4 结构化信号：对应条目存在即真，截断丢弃不翻转存在性。
	hasAIResponse bool
	hasToolLines  bool
	// 规则3 计数口径：截断前的保留类条数，预算截断是视图控制手段而非证据否定
	// （与 hasAI/hasTool 同一裁决），零输入会话不因截断丢几条而误落 empty_shell 终态。
	keptBeforeTruncation int
	// 探测产出（specs §2.4 能力7）：classifySequence 单趟内联计分的裁决值，
	// 与裁剪共用同一趟消息遍历（03 §4 单趟实现约束）。
	client string
}

// prepare 单轮执行裁剪与统计全链。规则0 由调用方先行：nil detail 进此函数会 panic。
// key 透传 buildView 落 trim 日志归因，Trim 独立调用传空串。
func (e *Extractor) prepare(key string, detail *conversationlog.SessionDetail) preparedData {
	rawPrefixes, rawPatterns := e.readParams()
	prefixes := e.injectPrefixes(rawPrefixes)
	patterns := e.redactPatterns(rawPatterns)
	// 裁剪与探测在 classifySequence 单趟产出（03 §4 单趟实现约束）。
	dc := classifySequence(detail.Messages, prefixes, patterns)
	view, trimStats, sig := e.buildView(key, dc.classified, patterns, dc.client)
	profile := computeStats(detail, dc.classified, view.CharCount, patterns)
	return preparedData{
		view:                 view,
		trimStats:            trimStats,
		profile:              profile,
		redactPatterns:       patterns,
		hasAIResponse:        sig.hasAI,
		hasToolLines:         sig.hasTool,
		keptBeforeTruncation: sig.keptBeforeTrunc,
		client:               dc.client,
	}
}

// prepareAndJudge 统一规则0 与规则1-4 的判定入口（Extract 与 ShouldSkip 共用，
// 防顺序约定分散两处漂移）：invalid 时零值返回，不执行 prepare（防 nil detail 下钻）。
func (e *Extractor) prepareAndJudge(key string, detail *conversationlog.SessionDetail, minUser, minKept int) (preparedData, bool, string) {
	if detailInvalid(detail) {
		return preparedData{}, true, SkipDetailInvalid
	}
	p := e.prepare(key, detail)
	skip, reason := e.shouldSkipPrepared(p, minUser, minKept)
	return p, skip, reason
}

// ExtractionResult 是抽取结果（specs §2.3）。降级是终态设计：failed 仍返回
// err=nil 与含 Stats 的 Profile，基础设施错误（落库失败）才走 error 通道。
type ExtractionResult struct {
	Skipped    bool
	SkipReason string
	Profile    *FeatureProfile
	Reused     bool
	Status     string // success / failed / skipped
}

// sessionRef 是会话归属引用（key + tokenName 参数对单点收拢），日志归因与
// 落库归属共用；persistCtx 内嵌承载三态落库场景。
type sessionRef struct {
	key       string
	tokenName string
}

func (s sessionRef) logAttrs() []any {
	return []any{"key", s.key, "token_name", s.tokenName}
}

// withClientAttrs 追加 client 日志归因字段（specs §6.1）：空串表示未经探测
//（detail_invalid 等路径），不追加防伪造归因。探测输出恒为七值非空串。
func withClientAttrs(attrs []any, client string) []any {
	if client == "" {
		return attrs
	}
	return append(attrs, "client", client)
}

// reuseTerminal 终态幂等预检：success/skipped 返回 Reused 结果（skipped 如实回传
// 既有行 error_code），failed 与无行返回 nil 继续抽取流程（specs §2.4 能力3）。
// skipped 复用同时置 Skipped=true，与首次 persistSkipped 语义一致（worker 以
// res.Skipped 记跳过日志，复用路径不置会让重放场景静默）。
func (e *Extractor) reuseTerminal(ctx context.Context, s sessionRef) (*ExtractionResult, error) {
	existing, err := e.repo.FindBySessionKey(ctx, s.key)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if res := terminalResult(existing); res != nil {
		slog.Info("extract reused", append(s.logAttrs(), "status", existing.Status)...)
		return res, nil
	}
	return nil, nil // failed 行继续流程，重抽翻转。
}

// terminalResult 按库内终态行构造复用结果：skipped 如实回传既有行 error_code 并置
// Skipped（与首次 persistSkipped 语义一致）；非终态返回 nil。
func terminalResult(existing *domain.SessionFeature) *ExtractionResult {
	switch existing.Status {
	case domain.FeatureStatusSuccess:
		return &ExtractionResult{Reused: true, Status: existing.Status}
	case domain.FeatureStatusSkipped:
		return &ExtractionResult{Reused: true, Skipped: true, SkipReason: existing.ErrorCode, Status: existing.Status}
	}
	return nil
}

// saveReusedResult 收敛 Save 竞态：reused=true 表示库内已有终态行且本次内容未写入
// （对端抢先翻转），本地三态判定作废，重查以库内实际行构造返回；重查失败或行非终态
// 的极端窗口返回 nil，调用方保守回退本地判定（此时 WARN 留痕，返回值与库内状态可能失真）。
func (e *Extractor) saveReusedResult(ctx context.Context, s sessionRef) *ExtractionResult {
	current, err := e.repo.FindBySessionKey(ctx, s.key)
	if err != nil || current == nil {
		slog.Warn("reuse convergence requery failed, fallback to local verdict",
			append(s.logAttrs(), "row_missing", current == nil, "err", err)...)
		return nil
	}
	res := terminalResult(current)
	if res != nil {
		slog.Info("extract reused after save", append(s.logAttrs(), "status", current.Status)...)
	}
	return res
}

// Extract 对单会话执行完整抽取（过滤判定 → 裁剪与统计 → LLM → 落库）。
// 幂等预检与三态落库的行时间列统一取 detail.Session（time.Unix 口径，04 §3.1）。
// 预检保留使单独调用仍正确，并发竞态由 repo.Save 唯一索引兜底。
func (e *Extractor) Extract(ctx context.Context, key string, detail *conversationlog.SessionDetail, tokenName string) (*ExtractionResult, error) {
	if res, err := e.reuseTerminal(ctx, sessionRef{key: key, tokenName: tokenName}); err != nil || res != nil {
		return res, err
	}
	return e.doExtract(ctx, key, detail, tokenName)
}

// doExtract 是无终态预检的抽取骨架，供 Extract 与 ExtractByKey 复用：
// 调用方已各自完成终态预检，此处只走判定 → LLM → 落库主链。
func (e *Extractor) doExtract(ctx context.Context, key string, detail *conversationlog.SessionDetail, tokenName string) (*ExtractionResult, error) {
	// 单轮 prepare 供跳过判定与 LLM 段共享，抽取中途改参数页不再产生两套过滤视图。
	ref := sessionRef{key: key, tokenName: tokenName}
	pc := persistCtx{sessionRef: ref, detail: detail}
	p, skip, reason := e.prepareAndJudge(key, detail, MinUserMessages, MinKeptMessages)
	pc.client = p.client
	if skip {
		return e.persistSkipped(ctx, pc, reason)
	}

	out, err := e.llmExtract(ctx, ref, p.view, p.profile, p.client)
	if err != nil {
		return nil, err // ctx 取消等基础设施错误：不落行，交任务重试
	}
	if out.errCode == "" {
		// 输出侧脱敏在 LLM 输出后落库前（specs §2.4 能力5）：Instruction 逐条 +
		// Summary 兜底（LLM 自由文本可能复述输入侧漏网的敏感串，主库只见脱敏档案）。
		out.summary = Redact(out.summary, p.redactPatterns)
		for i := range out.instruction {
			out.instruction[i].Text = Redact(out.instruction[i].Text, p.redactPatterns)
		}
	}

	profile := &FeatureProfile{Stats: p.profile, Summary: out.summary, Instruction: out.instruction, Behavior: out.behavior}
	if out.errCode != "" {
		return e.persistFailed(ctx, pc, profile, out.errCode)
	}
	return e.persistSuccess(ctx, pc, profile)
}

// ExtractByKey 按会话键执行全流程（拉详情 + 抽取）。终态预检先于拉详情：
// success/skipped 不重拉，failed 与无行才继续。业务空双通道（信封 success=false 与
// HTTP 404）不落行返回；残留 failed 行终态化为 skipped（session_not_found）；
// 确定性上游错误落 failed 终态行，其余走 error 通道交重试。
func (e *Extractor) ExtractByKey(ctx context.Context, sessionKey, tokenName string) (*ExtractionResult, error) {
	ref := sessionRef{key: sessionKey, tokenName: tokenName}
	if res, err := e.reuseTerminal(ctx, ref); err != nil || res != nil {
		return res, err
	}
	secret, err := e.secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("extractor: resolve integration secret: %w", err)
	}
	detail, err := e.cl.GetSessionDetail(ctx, secret, sessionKey)
	if err != nil {
		var ue *conversationlog.UpstreamError
		if (errors.Is(err, conversationlog.ErrUpstreamBusiness) &&
			errors.As(err, &ue) && ue.IsNotFound()) ||
			errors.Is(err, conversationlog.ErrNotFound) {
			slog.Info("session not found, treat as business empty", ref.logAttrs()...)
			return e.finalizeNotFound(ctx, ref)
		}
		// 确定性上游错误重试恒失败，落 failed 终态行防烧预算（密钥修复后补跑可重抽
		// 翻转），与底座「确定性错误不重试」约定对齐；其余（网络、超时、5xx）走
		// error 通道交任务重试。既有行元数据经 rowMetaOf 复用（覆写为 epoch 会让
		// 行对时间窗查询永久不可见，与 finalizeNotFound 同一裁决），client 同样
		// 复用既有行原值；首抽无行时落 unknown（有会话无探测输入是拉取失败态，
		// 与 detail_invalid 的空串区分，防 fetch 错误行混入 detail_invalid 观测桶）。
		if errors.Is(err, conversationlog.ErrUnauthorized) ||
			errors.Is(err, conversationlog.ErrBadRequest) ||
			errors.Is(err, conversationlog.ErrDecode) ||
			errors.Is(err, conversationlog.ErrNotConfigured) ||
			errors.Is(err, conversationlog.ErrUnexpectedStatus) {
			pc := persistCtx{sessionRef: ref, client: rules.ClientUnknown}
			if meta := e.rowMetaOf(ctx, sessionKey); meta != nil {
				pc.keepMeta = meta
				// legacy 失败行（client 列加列迁移兜底 ''）落回 '' 会混入
				// detail_invalid 观测桶，空串保持 unknown。
				if meta.client != "" {
					pc.client = meta.client
				}
			}
			return e.persistFailed(ctx, pc, emptyStatsProfile(), ErrDetailFetchCode)
		}
		return nil, fmt.Errorf("extractor: fetch session detail key=%s: %w", sessionKey, err)
	}
	return e.doExtract(ctx, sessionKey, detail, tokenName)
}

// rowMetaOf 查既有行并转接元数据载荷（无行或查询失败返回 nil，走零值落库）。
// 复用 turn_count 与时间列：覆写为 epoch 会让行对时间窗查询永久不可见。
func (e *Extractor) rowMetaOf(ctx context.Context, key string) *rowMeta {
	existing, err := e.repo.FindBySessionKey(ctx, key)
	if err != nil || existing == nil {
		return nil
	}
	return rowMetaOfRow(existing)
}

// finalizeNotFound 业务空收尾：上游已无该会话，既有 failed 非终态行（上游按留存
// 策略删除会话后的残留）终态化为 skipped，防每轮重拉与 fail_ratio 分子恒含已删除
// 会话的虚高；无行保持业务空语义（不落行）。终态化只翻状态列，turn_count 与时间列
// 复用既有行原值（无 detail 可回退，覆写会让行对时间窗查询永久不可见）。
func (e *Extractor) finalizeNotFound(ctx context.Context, ref sessionRef) (*ExtractionResult, error) {
	existing, err := e.repo.FindBySessionKey(ctx, ref.key)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status == domain.FeatureStatusFailed {
		meta := rowMetaOfRow(existing)
		client := meta.client
		if client == "" { // legacy 空串是加列迁移兜底值，归一到 unknown 防混入 detail_invalid 桶
			client = rules.ClientUnknown
		}
		pc := persistCtx{sessionRef: ref, keepMeta: meta, client: client}
		return e.persistSkipped(ctx, pc, SkipSessionNotFound)
	}
	return &ExtractionResult{Skipped: false, Status: ""}, nil
}

// rowMetaOfRow 把既有档案行转元数据载荷（finalizeNotFound 已持行对象的专用形态）：
// client 与时间列同裁决，随载荷复用既有行原值。
func rowMetaOfRow(existing *domain.SessionFeature) *rowMeta {
	return &rowMeta{
		turnCount: existing.TurnCount, first: existing.FirstTurnAt, last: existing.LastTurnAt,
		client: existing.Client,
	}
}

// rowMeta 是无 detail 场景（残留行终态化）的行元数据来源：含 client 列复用值。
type rowMeta struct {
	turnCount int
	first     time.Time
	last      time.Time
	client    string
}

// persistCtx 是三态落库的共享上下文：行归属与元数据时间列来源（04 §3.1）。
type persistCtx struct {
	sessionRef
	detail *conversationlog.SessionDetail
	// keepMeta 非空时 meta 取既有行原值（无 detail 的残留行终态化场景）
	keepMeta *rowMeta
	// client 是 client 列落库值（specs §2.4 能力3）：正常三态行取探测产出
	//（detail_invalid 行空串）；无 detail 的终态化复用既有行原值（不重探测）；
	// 同时兼日志归因字段（specs §6.1），空串日志侧不追加防伪造。
	client string
}

// meta 取行元数据。keepMeta 优先（残留行终态化复用既有值）；nil detail 走
// epoch（零值 time.Time 序列化为 0000-00-00 会被 MySQL NO_ZERO_DATE 拒绝）；
// session 时间缺失时回退 turns 时间戳，防落 epoch 让行对时间窗查询永久不可见。
func (c persistCtx) meta() rowMeta {
	if c.keepMeta != nil {
		return *c.keepMeta
	}
	if c.detail == nil {
		return rowMeta{first: time.Unix(0, 0).UTC(), last: time.Unix(0, 0).UTC()}
	}
	d := c.detail
	first, last := sessionTurnTimes(d)
	if d.Session.FirstTurnTime == 0 || d.Session.LastTurnTime == 0 {
		if lo, hi, ok := turnTimeBounds(d.Turns); ok {
			if d.Session.FirstTurnTime == 0 {
				first = time.Unix(lo, 0).UTC()
			}
			if d.Session.LastTurnTime == 0 {
				last = time.Unix(hi, 0).UTC()
			}
		}
	}
	return rowMeta{turnCount: d.Session.TurnCount, first: first, last: last}
}

// turnTimeBounds 取 turns 时间戳 min/max（上游对顺序无契约，透传原序）。
func turnTimeBounds(turns []conversationlog.TurnMeta) (lo, hi int64, ok bool) {
	for _, t := range turns {
		if t.CreatedAt == 0 {
			continue
		}
		if !ok {
			lo, hi, ok = t.CreatedAt, t.CreatedAt, true
			continue
		}
		if t.CreatedAt < lo {
			lo = t.CreatedAt
		}
		if t.CreatedAt > hi {
			hi = t.CreatedAt
		}
	}
	return lo, hi, ok
}

// persistRow 收敛三态落库共用骨架：补行元数据列 + client 列 + Save + 错误包装 +
// reused 竞态收敛。返回 res 非 nil 表示对端终态胜出（调用方直接透传并跳过本地日志）。
func (e *Extractor) persistRow(ctx context.Context, c persistCtx, row *domain.SessionFeature) (*ExtractionResult, error) {
	m := c.meta()
	row.TurnCount, row.FirstTurnAt, row.LastTurnAt = m.turnCount, m.first, m.last
	row.Client = c.client
	reused, err := e.repo.Save(ctx, row)
	if err != nil {
		return nil, wrapStoreWrite(c.key, err)
	}
	if reused {
		if res := e.saveReusedResult(ctx, c.sessionRef); res != nil {
			return res, nil
		}
	}
	return nil, nil
}

// withClientAttrs 探测产出点局部追加 client 字段（specs §6.1）：空串表示未经
// 探测（复用与拉取失败路径），不追加防伪造归因。
func (c persistCtx) withClientAttrs(attrs []any) []any {
	return withClientAttrs(attrs, c.client)
}

// persistSkipped 落 skipped 元数据行：profile_json 空串、不计算 Stats、error_code 记跳过原因。
func (e *Extractor) persistSkipped(ctx context.Context, c persistCtx, reason string) (*ExtractionResult, error) {
	res, err := e.persistRow(ctx, c, &domain.SessionFeature{
		SessionKey: c.key,
		TokenName:  c.tokenName,
		Status:     domain.FeatureStatusSkipped,
		ErrorCode:  reason,
	})
	if err != nil || res != nil {
		return res, err
	}
	slog.Info("session skipped", c.withClientAttrs(append(c.logAttrs(), "reason", reason))...)
	return &ExtractionResult{Skipped: true, SkipReason: reason, Status: domain.FeatureStatusSkipped}, nil
}

// persistFailed 落 failed 降级行：profile_json 仅 Stats 块、error_code 记错误码，err=nil。
func (e *Extractor) persistFailed(ctx context.Context, c persistCtx, profile *FeatureProfile, errCode string) (*ExtractionResult, error) {
	pj, err := json.Marshal(statsOnlyJSON{Stats: profile.Stats})
	if err != nil {
		return nil, wrapStoreWrite(c.key, err)
	}
	res, err := e.persistRow(ctx, c, &domain.SessionFeature{
		SessionKey:  c.key,
		TokenName:   c.tokenName,
		Status:      domain.FeatureStatusFailed,
		ProfileJSON: string(pj),
		ErrorCode:   errCode,
	})
	if err != nil || res != nil {
		return res, err
	}
	slog.Error("extract failed", c.withClientAttrs(append(c.logAttrs(), "code", errCode))...)
	return &ExtractionResult{Profile: profile, Status: domain.FeatureStatusFailed}, nil
}

// persistSuccess 落 success 行：profile_json 全四块序列化、error_code 空串。
func (e *Extractor) persistSuccess(ctx context.Context, c persistCtx, profile *FeatureProfile) (*ExtractionResult, error) {
	pj, err := json.Marshal(profile)
	if err != nil {
		return nil, wrapStoreWrite(c.key, err)
	}
	res, err := e.persistRow(ctx, c, &domain.SessionFeature{
		SessionKey:  c.key,
		TokenName:   c.tokenName,
		Status:      domain.FeatureStatusSuccess,
		ProfileJSON: string(pj),
	})
	if err != nil || res != nil {
		return res, err
	}
	// 单会话抽取成功与裁剪统计同为 DEBUG 口径（specs §6.1 日志级别表）。
	slog.Debug("extract completed", c.withClientAttrs(append(c.logAttrs(), "status", domain.FeatureStatusSuccess))...)
	return &ExtractionResult{Profile: profile, Status: domain.FeatureStatusSuccess}, nil
}

// llmOutcome 是 LLM 段的产出：errCode 空串表示成功携带三块；err 非 nil 表示
// 基础设施错误（ctx 取消、瞬时错误）须交任务重试；errCode 非空是 failed 终态。
type llmOutcome struct {
	summary     string
	instruction []InstructionSeg
	behavior    ProfileBehavior
	errCode     string
}

// llmExtract 执行 LLM 段：buildPrompt → StreamChat → parseProfile，ErrSchemaInvalid
// 且 ctx 未取消时重试第二次（specs §2.4 能力2）。error 通道只留 ctx 取消类基础
// 设施错误，其余按 error_code 降级落 failed 终态。
func (e *Extractor) llmExtract(ctx context.Context, ref sessionRef, view TrimmedView, stats ProfileStats, client string) (llmOutcome, error) {
	prompt := buildPrompt(view, stats)
	callOnce := func() (llmOutcome, error) {
		stream, err := e.llm.StreamChat(ctx, llm.ChatRequest{Messages: []llm.ChatMessage{
			{Role: "user", Content: prompt},
		}})
		if err != nil {
			return llmOutcome{}, err
		}
		defer stream.Close()
		var b strings.Builder
		b.Grow(1024) // 摘要级输出千字符量级，免逐 chunk 扩容拷贝
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break // 流正常结束
			}
			if err != nil {
				return llmOutcome{}, err
			}
			b.WriteString(chunk.Content)
		}
		summary, instruction, behavior, err := parseProfile(b.String(), stats)
		if err != nil {
			return llmOutcome{}, err
		}
		return llmOutcome{summary: summary, instruction: instruction, behavior: behavior}, nil
	}

	out, err := callOnce()
	if err != nil && errors.Is(err, ErrSchemaInvalid) && ctx.Err() == nil {
		slog.Warn("profile schema invalid, retrying", ref.logAttrs()...)
		out, err = callOnce()
	}

	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return llmOutcome{}, fmt.Errorf("extractor: llm segment canceled: %w", ctx.Err())
	}
	// 瞬时错误（底座重试耗尽的超时/排队满/限流）同样按 ErrLLMUpstream 降级落
	// failed 行，补跑时重抽翻转；error 通道只留给 ctx 取消类基础设施错误。
	errCode := ErrLLMUpstreamCode
	if errors.Is(err, llm.ErrContextLengthExceeded) {
		errCode = llm.ErrContextLengthExceeded.Code // 超限与上游故障在 error_code 可区分
	} else if errors.Is(err, ErrSchemaInvalid) {
		errCode = ErrSchemaInvalidCode
	}
	failedAttrs := withClientAttrs(append(ref.logAttrs(), "code", errCode), client)
	slog.Error("extract failed", failedAttrs...)
	return llmOutcome{errCode: errCode}, nil
}

// sessionTurnTimes 取行时间列来源：FirstTurnTime/LastTurnTime 经 time.Unix 转换（04 §3.1）。
// 统一 UTC 口径与 ListByPersonAndRange 查询端点对齐（SQLite 文本字典序比较）。
func sessionTurnTimes(detail *conversationlog.SessionDetail) (time.Time, time.Time) {
	return time.Unix(detail.Session.FirstTurnTime, 0).UTC(), time.Unix(detail.Session.LastTurnTime, 0).UTC()
}

// statsOnlyJSON 是 failed 行的落库形态：仅 Stats 块（specs §2.3 档案表注释与
// 04 §3.1 failed 行降级口径），LLM 三块不落。
type statsOnlyJSON struct {
	Stats ProfileStats `json:"Stats"`
}

// wrapStoreWrite 包装落库错误（ErrStoreWrite 语义）：不落行，任务返回 err 交 Asynq 重试。
func wrapStoreWrite(key string, err error) error {
	slog.Error("extract store write failed", "key", key, "code", "ErrStoreWrite")
	return fmt.Errorf("extractor: ErrStoreWrite: session_key=%s: %w", key, err)
}
