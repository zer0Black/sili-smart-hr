# 综合评估与聚合 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_TECH_005_TECH_综合评估与聚合 |
| 模块代号 | TECH（技术组件，engine/evaluator + scorer + activity 子域） |
| 文档版本 | v1.6 |
| 创建日期 | 2026-09-05 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计范围声明

本组件是后端进程内 Go 技术组件（specs §1.4），**无本系统对外 HTTP 接口**：不注册路由、不进受保护路由组。本文档承载 dev_plan 依赖的四类内部契约，形态与 T4（P2_TECH_003）03 文档同构：

| 契约类型 | 承载方式 | 消费方 |
|---------|---------|--------|
| Asynq 异步任务契约 | 任务类型常量 + payload schema + handler 行为约定 | worker 调度（T6 跑批复用） |
| 组件方法契约 | Evaluator / Activity / Scorer 公开方法签名与语义 | worker handler、测试、T6 编排、F6 定向分析、F7 阅卷后聚合刷新 |
| 仓储契约 | 三个新 Repository 接口 + DimensionRepository 扩展 | Wire 装配、聚合与幂等判定 |
| 配置读取契约 | DimensionReader 窄接口 + 参数键复用 | 维度口径与活跃度阈值读取、rationale 兜底脱敏 |

---

## 2. 模块信息

**代码落位：** `hr-backend/internal/engine/{evaluator,scorer,activity}/`（替换三处 doc.go 占位，specs §4.1）。人群签名识别 IdentifyPopulation 为包级纯函数，落 activity 包（消费主体）。

**依赖方向（specs §4.2-4.3）：**

```
worker/task（Asynq handler）
    └─ evaluator.Evaluator（能力1/2/6，EvaluatePerson 组合编排）
         ├─ llm.Client            （P2_TECH_001 底座，评估专用 client 装配，见 §2.1）
         ├─ llm.EnabledModelProvider（model_name 落库取值，评估时解析启用模型）
         ├─ SessionFeatureRepository（既有，ListByPersonAndRange 只读取数）
         ├─ DimensionSpecReader    （本 Feature 定义，见 §5.2）
         ├─ DimensionScoreRepository（本 Feature 新增）
         ├─ repository.SystemParamReader（既有，读脱敏正则键，rationale 兜底脱敏）
         ├─ *activity.Activity    （能力3/4，StatPersonByKey / StatPerson）
         └─ *scorer.Scorer        （能力5，Aggregate）

activity.Activity
    ├─ conversationlog.Client（P2_TECH_002，ListSessions 列表拉取 + 集成密钥 Bearer）
    ├─ SessionFeatureRepository（status 口径全集取数）
    ├─ ThresholdReader         （本 Feature 定义，见 §5.2）
    ├─ ActivityStatRepository （本 Feature 新增）
    └─ SecretProvider          （集成密钥解密注入，装配经 service.ResolveIntegrationSecret，见 §7）

scorer.Scorer
    ├─ DimensionScoreRepository（聚合读数，含 F7 写入的 active_test 行）
    └─ AggregateScoreRepository（本 Feature 新增）
```

### 2.1 Wire 装配

三包各自 `New`，evaluator 组合持有 activity 与 scorer（specs §2.5 的 `evaluator.New` 七参为概念形：能力3/5 的接口分别挂在 `*Activity` 与 `*Scorer` 上且需独立导出供 T6/F6/F7 单独调用，单构造函数无法承载，与 T4 specs 四参→实现五参先例同款偏差）。实现组合形为九参：除下列三包外，evaluator 还需 thresholds（Evaluate 内签名识别经 IdentifyPopulation 三参注入低频阈值，§4.1）与 sysParams（rationale 兜底脱敏，§4.3）：

```go
act := activity.New(cl, featureRepo, thresholdReader, actRepo, secrets)
sc := scorer.New(scoreRepo, aggRepo)
ev := evaluator.New(llmClient, modelProvider, featureRepo, dimSpecReader,
    thresholdReader, scoreRepo, sysParamReader, act, sc)
// secrets 为 activity.SecretProvider，装配层经 service.ResolveIntegrationSecret
// 从 IntegrationSecretRepository 解密构造（extractor NewExtractorProvider 同款，§7 收敛点）。
```

新增 provider 后 `go generate ./...` 再生 wire_gen.go。**评估专用 LLM client**（与 ExtractorLLMClient 同款命名类型规避 Wire 类型表冲突）：

```go
type EvaluatorLLMClient llm.Client

// 装配参数：Timeout 180s / MaxRetries 1 / InitialBackoff 5s / MaxBackoff 10s /
// MaxRetryAfter 60s（与 extractor 专用 client 同构，超时推导见 §3.3）。
// Timeout 覆盖建连到流式 body 读毕全程（底座 http.Client.Timeout 语义），
// 评估输出 MaxTokens=4000 的多维度 JSON，120s 验收线（specs §3.1）与 180s
// p99 观测线（specs §6.2）都要求单次调用预算至少 180s。
// TokenBudget = MaxProfileSetTokens + 17000 = 47000：证据段 90000 字符（30000 token）
// + 维度段按 specs §3.1 20 维度上限标定（20 × prompt≤2000 + anchor≤500 ≈ 50k 字符
// ≈ 16.7k token 取整）+ 系统段、统计块与指令段余量；TokenCounter 用 CharDiv3Counter
// （与 T4 同折算）。评分请求 MaxTokens=4000（20 维度 × 200 字理由 + JSON 结构余量，
// ChatRequest 逐请求设置）。
```

三个组件实例（act/sc/ev）均注册进 providers.go，worker/task 的 NewMux 经参数注入注册 person-evaluate handler（单一注册入口范式不变）。

---

## 3. Asynq 异步任务契约

### 3.1 任务类型

```go
// worker/task 内注册，任务粒度一人一任务（specs §2.1 能力6）。
const TypePersonEvaluate = "engine:person-evaluate"
```

**需求追溯：** specs §4.2 引入方式、§2.4 能力6。

### 3.2 任务 payload

```json
{
  "token_name": "李雪涛",
  "period_start": 1756560000,
  "period_end": 1757164800
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| token_name | string | 是 | 人员归属（上游调用令牌名）；空值 handler 记 ERROR 后返回 nil 丢弃任务（构造侧确定性错误，T4 同款） |
| period_start | integer | 是 | 评估区间起点，Unix 秒（含） |
| period_end | integer | 是 | 评估区间终点，Unix 秒（不含）；period_end ≤ period_start 视为坏 payload 同款丢弃 |

payload 由 T6 跑批编排构造入队（本 Feature 只定义 schema 与消费行为；周期批量场景 T6 应一次拉全量列表内存分组后经 EvaluatePerson 的 sessions 入参注入，避免逐人重复拉取，specs §2.5 完整示例）。

### 3.3 handler 行为约定

```
输入:  payload{token_name, period_start, period_end}
流程:  ev.EvaluatePerson(ctx, tokenName, Period{Start, End}, nil)
       （sessions 空 → 内部 StatPersonByKey 拉列表；T6 已分组时应改走进程内直调并传列表）
返回:  err == nil → 任务成功（含 Skipped=true 零档案跳过、Reused=true 幂等复用、
       评分行 failed 三种终态，评分/聚合/活跃度行均已按规格落库，不重试）
       err != nil → 交 Asynq 任务级重试（ErrNoDimensions / ErrDimensionConfigRead /
       ErrProfileRead / ErrScoreRead / ErrSessionListFetch / ErrStoreWrite，
       specs §2.3 错误码表）
超时:  任务级超时 1050s，单点声明在 worker/task 的 personEvaluateTimeout：
       列表拉取最坏 190s（30s × 4 次尝试 + 10/20/40s 退避，P2_TECH_002 契约）
       + LLM 段最坏 840s（评估专用 client Timeout 180s：429 带 Retry-After
       封顶 60s 下单次 callOnce 180+60+180=420s，schema 校验失败重试共 2 次
       完整调用）+ 档案取数组装亚秒级 + 三表落库冗余 ≈ 1035s 最坏，1050s
       含 15s 冗余。满足 specs §2.2 对 EvaluatePerson 的 ctx 预算 ≥ 900s
       下限（该下限按 LLM 840s + 取数组装余量计，未含 StatPersonByKey 的
       列表拉取段；任务级 1050s 全覆盖）。前提：EvaluatorLLMClient 的
       cfg.Timeout 装配为 180s 量级（§2.1），否则 LLM 慢路径（120s 验收
       线至 180s 观测线区间）会先触 deadline 产生伪失败
并发:  由 config.Asynq.Concurrency 承载，评估通道 LLM 在飞 ≤ 4 由评估专用
       client 的独立 gate 承载（与全局/extractor 通道互不共享，§2.1 T4 先例；
       跑批期三通道并行时跨通道总在飞为各通道 gate 上限之和），组件自身不加锁
日志:  只输出 token_name、period、计数、维度 code 与错误码，禁止输出档案内容、
       prompt 文本、rationale 原文（specs §6.1 隐私口径）
```

任务注册落位 `worker/task`（HandleFunc 范式与既有 session-extract 一致），真实跑批 cron 注册归 T6。

---

## 4. 组件方法契约

### 4.1 方法清单

| 方法 | 所属 | 签名（概念形，ctx 略） | 语义 | 需求追溯 |
|------|------|----------------------|------|---------|
| EvaluatePerson | Evaluator | (tokenName, period, sessions) → (*EvaluateResult, error) | 单人周期原子入口：活跃度统计 → 综合评估 → 聚合，三表落库；sessions 空时内部 StatPersonByKey 拉取 | specs §2.4 能力6 |
| Evaluate | Evaluator | (tokenName, period, sessions) → (*EvaluateResult, error) | 仅综合评估：读档案、签名识别、组装、调 LLM、评分行落库；幂等判定内聚（全 success 复用、failed 先删后评） | specs §2.4 能力1 |
| AssembleProfileSet | Evaluator | (tokenName, period) → (*ProfileSet, error) | 档案集分层组装，纯读不调 LLM 不落库（内部读档案表，需 ctx） | specs §2.4 能力2 |
| StatPersonByKey | Activity | (tokenName, period) → (*ActivityStat, error) | 活跃度统计，内部经 P2_TECH_002 拉列表 + 档案表取数，落库 | specs §2.4 能力3 |
| StatPerson | Activity | (sessions, tokenName, period) → (*ActivityStat, error) | 活跃度统计，列表由调用方传入（档案侧数据仍内部读取），落库 | specs §2.4 能力3 |
| IdentifyPopulation | activity 包级 | (sessions, profiles, lowFreqThreshold) → PopulationSignature | 人群签名识别纯函数。**实现形三参**：低频下限阈值入参注入（判据「列表会话量 ≥ 低频下限」需要阈值，specs §2.4 能力4 两参为概念签名，调用方从 ThresholdReader 读出后传入，测试可锚定边界），T4 ShouldSkip 三参先例 | specs §2.4 能力4 |
| Aggregate | Scorer | (tokenName, period) → (*AggregateResult, error) | 读同人同周期全部 source 评分行重算聚合行，幂等 upsert | specs §2.4 能力5 |

返回结构 `EvaluateResult{Activity, Scores, Aggregate, Skipped, Reused}`、`ActivityStat`、`AggregateResult`、`ProfileSet`、`PopulationSignature`、`Period`、`DimensionSpec`、`ProfileDigest` 的字段语义以 specs §2.2-2.3 为准，类型落位各自包内，本文档不重复定义。

### 4.2 错误返回语义（error 通道与业务态字段的边界）

error 与 Result 字段是两条互斥通道，handler 据此决定是否重试（specs §2.4 能力6 注意事项：error 通道只留基础设施类错误）：

| 场景 | error | Result | 落库行为 | handler 处置 |
|------|-------|--------|---------|-------------|
| 正常评分 | nil | Scores 齐，Skipped/Reused=false | 全维度评分行 + 活跃度行 + 聚合行 | 成功，不重试 |
| 零有效档案跳过 | nil | Skipped=true, err=nil | 全维度 insufficient 评分行（status=success）+ 活跃度行 + 聚合行（全剔除 nil 显式落空） | 成功，不重试 |
| 幂等复用 | nil | Reused=true | 无新评分行；活跃度与聚合照常重算落库 | 成功，不重试 |
| LLM 失败降级 | nil | Scores 为 failed 占位行 | failed 评分行（全维度占位，error_code 记因）+ 活跃度行 + 聚合行（剔除 failed 维度） | 成功（降级是终态），不重试 |
| 无启用对话分析维度 | ErrNoDimensions | 无效 | 不落评分行 | 上抛交 Asynq 重试（配置可能在线修复，重试有意义；耗尽由 T6 观测 ERROR） |
| 维度配置/阈值读取失败 | ErrDimensionConfigRead | 无效 | 不落评分行 | 上抛重试 |
| 档案表读取失败 | ErrProfileRead | 无效 | 不落行 | 上抛重试 |
| 评分行读取失败 | ErrScoreRead | 无效 | 不落行 | 上抛重试 |
| 列表拉取重试耗尽（sessions 空路径） | ErrSessionListFetch | 无效 | 不落任何行 | 上抛重试（调用方已传 sessions 的路径不触发） |
| 落库失败 | ErrStoreWrite | 无效 | 不落行 | 上抛重试；重试耗尽该人本周期无评分行，属 eval_fail_ratio 已知漏计项 |

ErrNoValidProfiles 仅作文档语义标识，运行时走 Skipped=true 通道，error 与 error_code 列均不承载（specs §2.3）。

### 4.3 关键行为约定（实现锚点）

- **档案取数口径**：ListByPersonAndRange 传参 start 前移 24h（容纳跨边界档案行），取回后统一按 `last_turn_time ∈ [Start, End)` 内存二次过滤；Evaluate（评分证据）、StatPerson/StatPersonByKey（status 口径计数）、IdentifyPopulation（签名判据）三处共用同一过滤后集合（specs §2.4 能力3/4）。
- **Evaluate 幂等判定**：ListExact 读同人同周期 source=conversation 评分行（period 双界精确匹配），无 failed 且 success 行的 dimension_code 集合覆盖当前启用维度集合 → Reused=true 跳过 LLM；存在 failed → 先删同人同周期 source=conversation 的 failed 行再走完整调用（active_test 行不受影响）。
- **评分 schema 校验**：code 白名单收敛（未知丢弃、缺失补 insufficient 行）、score 非 null 时 0-100 整数、rationale 超 MaxRationaleChars×2 判失败；宽容解析同 T4（剥 markdown 围栏、截首个含 dimensions 键的顶层平衡 JSON）；失败重试一次，仍失败落 failed 行。
- **rationale 兜底脱敏**：落库前过 extractor.Redact(text, patterns)，patterns 经 SystemParamReader 读 extractor.redact_patterns 键（nil 回退出厂集，Redact 内建回退，复用 T4 参数键与函数）。
- **model_name 落库**：调 LLM 前经 llm.EnabledModelProvider.GetEnabledModel 解析当前启用模型 ModelID；解析失败或未调 LLM 的行（零档案跳过路径）落空串。
- **prompt_version**：evaluator 包内常量 `PromptVersion = "v1"`，模板变更时 +1 并同步该常量。
- **部分维度缺提示词**：DIM 规则 6 下对话分析维度提示词为空时该维度按 insufficient 处理（不阻断其余维度），rationale 落配置缺省文案，evidence_json 口径摘要记 config_missing 标记（specs §3.2 维度配置缺失行）。
- **Evaluate 独立调用 sessions 空的退化**：sessions 空时不做单人列表拉取兜底（中文 token_name 上游过滤不可用），签名识别退化为仅档案侧判据（status 分布与 client 列），note 落 auto_client 表型并列呈现（specs §2.4 能力1 注意事项）；组合路径由 EvaluatePerson 注入 sessions 无此退化。
- **组合入口不包事务**：三表落库各自独立，中途失败重跑按幂等规则收敛（specs §2.4 能力6）。
- **prompt 五段结构与 TokenName 剥离**：组装规则与 C01-C07 承接映射按 specs §2.2 执行，prompt 全文不含 token_name 人名。

---

## 5. 仓储与配置读取契约

### 5.1 三个新 Repository

落位 `internal/repository/{dimension_score,aggregate_score,activity_stat}.go`，接口定义包内（双重接口范式），表结构见 [04_model_interface.md](04_model_interface.md) §3。

```go
// DimensionScoreRepository 评分行数据访问。
type DimensionScoreRepository interface {
    // ListByPersonPeriodExact 双界精确匹配同人同周期全部评分行（period_start 与
    // period_end 均精确等于传入 period 的 Unix 秒转换值；Evaluate 幂等判定与
    // Aggregate 聚合读数同口径，含 F7 写入的 active_test 行）。
    ListByPersonPeriodExact(ctx context.Context, tokenName string, start, end int64) ([]domain.DimensionScore, error)
    // DeleteConversationFailed 删同人同周期 source=conversation 且 status=failed
    // 的行（Evaluate failed 翻转前置；active_test 行不受影响）。
    DeleteConversationFailed(ctx context.Context, tokenName string, start, end int64) (int64, error)
    // SaveAll 按唯一索引 (token_name, period_start_at, dimension_code) upsert
    // 全列覆盖落库（insufficient 标记行与 failed 占位行同路径）。
    SaveAll(ctx context.Context, tokenName string, start, end int64, rows []domain.DimensionScore) error
}

// AggregateScoreRepository 聚合行数据访问。
type AggregateScoreRepository interface {
    // UpsertAll 按唯一索引 (token_name, period_start_at, module) upsert 全列覆盖
    // （幂等重算；全剔除时 module_score/overview_score 写 NULL，旧值不保留，
    // specs §2.4 能力5 规则6）。
    UpsertAll(ctx context.Context, tokenName string, start, end int64, rows []domain.AggregateScore) error
}

// ActivityStatRepository 活跃度行数据访问。
type ActivityStatRepository interface {
    // Upsert 按唯一索引 (token_name, period_start_at) upsert 全列覆盖
    // （同人同周期重算覆盖，历史周期行不动）。
    Upsert(ctx context.Context, rec *domain.ActivityStat) error
}
```

upsert 语义细节（并发防御、UTC 时间口径）见 04 文档各表业务规则段；start/end 入参 Unix 秒，仓储内 `time.Unix(n,0).UTC()` 转换（T4 ListByPersonAndRange 同款）。

### 5.2 维度口径与阈值读取（DimensionRepository 扩展）

消费侧窄接口定义在消费包（Go 隐式实现，T4 ConversationlogDetailFetcher 先例），仓储侧新增一个方法支撑：

```go
// evaluator 包内：评分维度口径读取（ specs DimensionSpec 的组装来源）。
type DimensionSpecReader interface {
    // ListEnabledConversationSpecs 返回启用且 data_source=CONVERSATION 的维度
    // 全字段（含 prompt/anchor，区别于 DimensionRepository.ListAll 的 brief 投影），
    // 装配为 []DimensionSpec（Module 取 module_code 原值）。
    ListEnabledConversationSpecs(ctx context.Context) ([]DimensionSpec, error)
}

// activity 包内：活跃度阈值读取。
type ThresholdReader interface {
    // ActivityThresholds 返回（活跃下限， 低频下限），复用 dimension_settings 单行表。
    ActivityThresholds(ctx context.Context) (active, lowFreq int, err error)
}
```

仓储支撑：`DimensionRepository` 新增 `ListEnabledFullByDataSource(ctx, dataSource string) ([]domain.Dimension, error)`（完整字段含 prompt/anchor，WHERE enabled AND data_source=? AND deleted_at IS NULL，code ASC）；`GetActivitySetting` 既有方法直接满足 ThresholdReader（装配层适配）。现有 dimension fake 补齐新方法一行（编译期接口断言防漂移）。

### 5.3 参数键复用（无新增键）

| 参数键 | 用途 | 消费点 |
|--------|------|--------|
| extractor.redact_patterns | rationale 落库前兜底脱敏的正则集 | evaluator 经 SystemParamReader 读取，回退口径同 extractor（nil 走 Redact 出厂分支） |

键归属 extractor 前缀，跨包只读复用（system_params 是共享 SYS 能力，键即接口）。

---

## 6. 与相邻域的边界

- **对 T4（extractor/session_features）**：只读消费（ListByPersonAndRange + client 列），Redact 函数复用，无写路径；档案表契约变更归 T4 变更 Feature。
- **对 T6（pipeline）**：跑批编排、周期调度、全量列表拉取分组、失败比例聚合归 T6；本组件提供 EvaluatePerson 处理器与 Evaluate/StatPerson 直调接口。
- **对 F6（评估运营）**：手动定向分析经 EvaluatePerson 传窄 period；画像快照消费三表数据归 F6，本组件产出到落库为止。
- **对 F7（主动测试阅卷）**：F7 写入 source=active_test 评分行（evidence_json 须同构携带口径摘要，含 weight 与 in_overview）后自调 Aggregate 刷新；本组件不感知阅卷时机。
- **对 DIM（维度配置）**：只读（ListEnabledFullByDataSource + GetActivitySetting）；评分口径快照落评分行 evidence_json，配置变更下次跑批生效、历史不重算。

---

## 7. HTTP 接口与安全声明

- **无对外 HTTP 接口**：规则文件 §2 的响应格式、分页参数、HTTP 错误码映射对本组件无适用对象；评分/聚合/活跃度的查询展示接口归属 F6/F9/F10/F11 各自 Feature 的 interface 设计（快照生成 F6、画像展示 F9、看板 F10、指标呈现 F11）。
- **认证**：任务链路无 HTTP 认证面；上游调用经 P2_TECH_001/002 既有通道（LLM 底座自持 API Key，conversationlog 经 SecretProvider 注入 Bearer 集成密钥，装配层复用 service.ResolveIntegrationSecret 收敛点）。
- **传输与存储安全（specs §3.3 全量承接）**：档案集与 prompt 仅内存临时持有，禁日志禁缓存禁落库；TokenName 组装前剥离（人名不进 LLM 上下文），落库时回填 token_name 列；rationale 落库前过 Redact 第二道防线；三表只存 token_name 归属字段，无工号无 user_id 冗余。
- **日志规范**：DEBUG 组装统计与单次评估成功 / INFO 签名命中、零档案跳过、评分复用 / WARN schema 重试与截取触发 / ERROR LLM 失败、落库失败、配置读取失败；字段仅 token_name、period、维度 code、计数、错误码。
- **监控指标**（T6 聚合、F11 呈现，本 Feature 只定义口径，specs §6.2）：eval_fail_ratio（failed 评分行人次/评估人次，≥10% 告警出厂默认）、eval_schema_fail_ratio（≥20% 观测）、eval_visible_ratio（人均 <50% 连续两周期观测）、population_signature_ratio（按 population_note 分桶，normal 空串桶不计）、unused_ratio、eval_duration（p99>180s 观测）、aggregate_duration（p99>1s 观测）。

---

## 8. SSOT 合规与一致性

- [x] 组件接口集合与 specs §2.3 输出定义一一对应（EvaluatePerson/Evaluate/AssembleProfileSet/StatPersonByKey/StatPerson/IdentifyPopulation/Aggregate）。实现形差异逐条声明：IdentifyPopulation 三参（阈值注入）、evaluator.New 九参组合形（§2.1，含 thresholds/sysParams/act/sc）、EvaluatorLLMClient 专用装配（§2.1）、ProfileDigest.LastTurn 实现形扩展（specs §2.2 两参概念形外的归一过滤判据字段，来源 domain 行 LastTurnAt，§4.3 档案取数口径消费）。
- [x] 六个能力（综合评估/分层组装/活跃度统计/签名识别/聚合/原子入口）在方法契约与行为约定中均有承载。
- [x] 错误码值域与 error/业务态双通道边界对齐 specs §2.3 错误码表（八错误码，ErrNoValidProfiles 走 Skipped 通道）。
- [x] 隐私与安全约束（specs §3.3）在 §7 全量承接（TokenName 剥离、Redact 兜底、内存持有、日志最小化）。
- [x] 无 HTTP 接口与 specs §1.4（进程内组件）一致。
- [x] 仓储契约与 [04_model_interface.md](04_model_interface.md) 表结构一一对应（唯一索引 upsert、双界精确匹配、Unix 秒转换口径）。
- [x] C01-C13 承接位置与 specs §7.2 一致（C01-C07 进 prompt 组装、C08-C12 规则侧签名、C13 观测指标）。
- [x] 超时与并发口径与 specs 一致：EvaluatorLLMClient Timeout 180s 支撑 specs §3.1 的 120s 验收线与 §6.2 的 180s 观测线，任务级 1050s 覆盖 specs §2.2 的 ctx ≥ 900s 预算；评估通道独立 gate 的在飞上限口径与 specs §3.1 一致（v1.2 修复）。

---

## 9. 不涉及的设计

- 跑批周期调度、全量列表拉取分组、逐人任务分发、失败比例聚合告警（T6 pipeline Feature）。
- 画像快照生成、画像/看板/工作台展示接口（F6/F9/F10/F11）。
- F7 阅卷写入 active_test 评分行的链路（F7 Feature；本组件只约定 evidence_json 口径摘要契约与 Aggregate 刷新接口）。
- 定向分析的管理 HTTP 接口与运营提示（F6；能力6 规则3 的「避开批量窗口起点」提示归 F6 界面）。
- dimension_score 等三表的字典表体系（规则文件 §1.8 显式排除）。

---

**文档版本：** v1.6
**最后更新：** 2026-09-06
**作者：** lixuetao
