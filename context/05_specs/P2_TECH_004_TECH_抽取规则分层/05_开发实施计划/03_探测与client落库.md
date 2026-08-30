# 探测与client落库 实现计划

> **目标：** 落地 DetectClient 客户端签名探测（计分并入 prepare 单趟、独立函数可测）、session_features 表新增 client 列（domain 加字段随 AutoMigrate 重建生效，删库重建验证）、三态落库行携带 client（复用回传、翻转覆盖、终态化复用、detail_invalid 空串四规则）、日志补 client 字段、探测准确性测试组。
> **架构：** detect.go 落 extractor 包根（依赖 conversationlog 与 rules，rules 包保持不 import extractor）；探测在 prepare 阶段与裁剪共享消息遍历（classifiedMsg 单趟产出），DetectClient 保持独立函数形态供测试直调；client 列对幂等状态机零参与（Save 判定条件不含 client）。
> **技术栈：** Go 1.25，GORM AutoMigrate（模型加字段随建表生效），无新依赖。

---

## 文件结构

### 新建文件
- `hr-backend/internal/engine/extractor/detect.go` — DetectClient 计分裁决纯函数 + 单趟计分器
- `hr-backend/internal/engine/extractor/detect_test.go` — 纯客户端样本、混合裁决、零特征、弱信号裁决测试

### 修改文件
- `hr-backend/internal/domain/session_feature.go` — SessionFeature 新增 Client 字段（Status 与 TurnCount 之间）
- `hr-backend/internal/repository/session_feature.go` — updateColumns 手写翻转列清单加 client 列（守护测试自动覆盖）
- `hr-backend/internal/engine/extractor/extract.go` — preparedData 加 client 字段；prepare 内探测产出；persist 三态接线（复用/翻转/终态化/detail_invalid）；日志补 client
- `hr-backend/internal/engine/extractor/trim.go` — prepare 链路透传（如探测在 classifySequence 侧单趟产出则此处只透传）
- `hr-backend/internal/engine/extractor/classify.go` — classifiedMsg 或 classifySequence 返回侧承载探测计分（实现者按单趟设计选址，DetectClient 独立函数形态不变）
- `hr-backend/internal/model/migrate_test.go` — client 列建表断言补齐
- `hr-backend/internal/engine/extractor/extract_test.go` — client 三态落库、复用不覆盖、翻转覆盖、detail_invalid 空串、fakeFeatureRepo Save 透传 client
- `hr-backend/CLAUDE.md` / 根 `CLAUDE.md` — 规约文档同步（extractor 分层结构与 client 列状态描述）

---

## 数据模型来源

| 来源 | 文件路径 | 状态 |
|------|----------|------|
| 04_model_interface.md | `/context/05_specs/P2_TECH_004_TECH_抽取规则分层/04_model_interface.md` | ✅ 存在 |

引用：04_model_interface.md → session_features 新增 client 列（§3.1）。模型层加字段 `Client string `gorm:"type:varchar(32);not null"``（落位 Status 与 TurnCount 之间），实际落库路径是 GORM AutoMigrate 加列随建表生效。按用户指令：新项目无历史库，**删库重建验证**（删 data/sili-smart-hr.db 后启动自动建表即含 client 列），不写存量加列迁移钩子，不走 ALTER。

---

## 任务清单

- [x] **T1: 数据库任务 — session_features 新增 client 列（删库重建）**

  **文件：**
  - 引用：`/context/05_specs/P2_TECH_004_TECH_抽取规则分层/04_model_interface.md` → session_features ADD COLUMN client
  - 修改：`hr-backend/internal/domain/session_feature.go`（模型加字段）

  **步骤1（写入模型）：** domain.SessionFeature 在 Status 与 TurnCount 之间加：
  ```go
  Client string `gorm:"type:varchar(32);not null"` // 主客户端标识（探测七值），detail_invalid 行为空串
  ```
  模型加字段即完成 DDL 变更载体（AutoMigrate 建表/加列由 allModels 既有链路承载，session_features 已登记无需改 migrate.go 的 allModels）。

  **步骤2（执行变更）：** 删除开发库 `hr-backend/data/sili-smart-hr.db`（按用户指令重新建表，不考虑老数据），`go run ./cmd/server` 启动自动建表（需 Redis 在跑；也可用 `go test ./internal/model/` 内存库验证）。无独立迁移文件，无 Flyway/Liquibase。

  **步骤3（验证变更结果）：** `sqlite3 hr-backend/data/sili-smart-hr.db ".schema session_features"` 确认建表语句含 `client varchar(32) NOT NULL` 且列序在 status 之后；或跑 migrate_test 的建表断言（T4 补齐后）。三库由 GORM dialector 翻译，SQLite 验证即代表模型 tag 正确，MySQL/PG DDL 见 04 §3.1 参考。

  **步骤4（提交）：** 由控制器统一提交。AutoMigrate 无 checksum 问题，后续对同表新变更仍走模型加字段或幂等钩子。

  **验收锚点：**
  - 验证命令：`cd hr-backend && rm -f data/sili-smart-hr.db && go test ./internal/model/ -run TestMigrateCreatesSessionFeatures -v`
  - 预期输出：PASS（既有建表断言 + T4 补的 client 列断言；本任务先确认模型编译与既有断言不破，client 列专断言在 T4 落地）

  **specs 依据：**
  - 章节范围：§2.3 输出定义（client 列注释：varchar(32) 七值、三态行均写入、detail_invalid 空串例外）、§1.4 技术栈表 GORM 行（模型加字段随建表生效，不做存量迁移）、04 §3.1（字段说明表与业务规则六条）
  - 业务规则索引：
    - [BR1] §2.3：client 列 varchar(32)，值域七值，三态行均写入，detail_invalid 行落空串
    - [BR2] §1.4：GORM 模型直接加字段随建表生效，新项目无历史库不做存量迁移
    - [BR3] 04 §3.1：探测对幂等判定零参与，Save 状态机判定条件不含 client 列

  **依赖：** 无

  **提交信息：** feat(domain): session_features 新增 client 列（模型加字段随建表生效）

- [x] **T2: repository 翻转列加 client**

  **文件：**
  - 修改：`hr-backend/internal/repository/session_feature.go:136-147`（updateColumns）
  - 测试：`hr-backend/internal/repository/session_feature_internal_test.go`（守护测试自动覆盖，无需改动）

  **契约：**
  - 签名（updateColumns 改后形态）：
  ```go
  func updateColumns(rec *domain.SessionFeature) map[string]interface{} {
      return map[string]interface{}{
          "token_name":    rec.TokenName,
          "status":        rec.Status,
          "client":        rec.Client,
          "turn_count":    rec.TurnCount,
          "first_turn_at": rec.FirstTurnAt,
          "last_turn_at":  rec.LastTurnAt,
          "profile_json":  rec.ProfileJSON,
          "error_code":    rec.ErrorCode,
          "updated_at":    utcNow(),
      }
  }
  ```
  - 关键逻辑：只加 "client" 一行；守护测试 TestUpdateColumnsMatchesDomainModel 反射枚举模型业务列，T1 加字段后守护测试先红（模型多 client 列而 map 缺），本任务补行后转绿（TDD 红→绿路径天然成立）；Save 状态机（isTerminalStatus/applyExisting）零改动（client 不参与幂等判定）
  - 依赖：T1

  **验收锚点：**
  - 核心断言：`TestUpdateColumnsMatchesDomainModel`——T1 后先跑确认 FAIL（缺 client 列报警），补行后 PASS；翻转路径断言由 extract_test 的 fakeRepo 同步透传后端到端覆盖
  - 验证命令：`cd hr-backend && go test ./internal/repository/ -run TestUpdateColumnsMatchesDomainModel -v`
  - 预期输出：`--- PASS: TestUpdateColumnsMatchesDomainModel`

  **specs 依据：**
  - 章节范围：04 §3.1 业务规则（failed 行翻转 client 随新探测结果覆盖、翻转 UPDATE 并发防御不变）、03 §6（仓储契约：Save 透传该列，幂等状态机与 client 交互规则见 04）
  - 业务规则索引：
    - [BR1] 04 §3.1：failed 行翻转时 client 随新探测结果覆盖（纯函数覆盖值恒等）
    - [BR2] 04 §3.1：翻转 UPDATE 并发防御（WHERE id AND status 双条件）不涉及 client 列，沿用
    - [BR3] 04 §7.2：探测对幂等判定零参与显式声明，防实现期误把 client 纳入判定条件

  **依赖：** T1

  **提交信息：** feat(repository): updateColumns 翻转列清单加 client

- [x] **T3: DetectClient 计分裁决函数**

  **文件：**
  - 创建：`hr-backend/internal/engine/extractor/detect.go`
  - 测试：`hr-backend/internal/engine/extractor/detect_test.go`（由实现者按 TDD 指导创建）

  **契约：**
  - 签名：
  ```go
  package extractor

  import "sili-smart-hr/backend/internal/engine/extractor/rules"

  // DetectClient 客户端签名探测（specs §2.4 能力7）。纯函数：对 Detail.Messages
  // 逐条跑全部已注册客户端的 DetectFeatures 计分，得分最高者为会话主客户端；
  // 次高分 > 0 且最高分与次高分分差 < 2 时返回 mixed（严格并列分差为 0 含于该条件）；
  // 次高点为零分时单客户端信号即定归属（不判 mixed）；全部零分返回 unknown。
  // 纯规则不调 LLM，永不返回 error。
  func DetectClient(detail *conversationlog.SessionDetail) string
  ```
  - 关键逻辑：
    1. 计分模型：每客户端独立累计 score map[string]int；DetectFeature.Kind == rules.FeatureKindPrefix 时取消息 TrimLeft 后前缀匹配（Once=true 全会话计一次后跳过该特征后续匹配，Once=false 逐消息累计）；Kind == rules.FeatureKindMarker 时 strings.Contains 子串存在性（Once 语义同前缀类）
    2. 裁决：找最高分 top 与次高分 second（不同 client）；second > 0 && top-second < 2 → rules.ClientMixed；仅一个客户端得分（second==0 且 top>0）→ 该客户端标识；全零 → rules.ClientUnknown
    3. 性能：单趟遍历消息 × 每客户端特征表（5 客户端 × 少量特征，500 消息远低于 10ms 预算）；实现上 detect 的单趟遍历与 classifySequence 合并的接线在 T5，本函数保持独立可直调形态（specs §2.4 能力7 注意事项）
    4. nil detail 防御：返回 rules.ClientUnknown（探测在 prepare 前有 detailInvalid 短路，此处防御性兜底）
  - 依赖：`rules.AllClientRules()`、`rules.ClientMixed`、`rules.ClientUnknown`、conversationlog.Message

  **验收锚点：**
  - 核心断言：
    - `TestDetectClientPureSamples`——五客户端各构造典型会话（workbuddy：`<user_query>` 包裹消息 + data-role marker；omo：连续 "TodoWrite "/"mcp__" 前缀 + [SYSTEM DIRECTIVE:；automation："## 身份定义"/"# Using your tools" 碎片；claude_code：`<system-reminder` 开式 + command 壳；opencode："# Environment" 头 + "The user has asked you to"），断言 `DetectClient(detail)` 分别返回 "workbuddy"/"omo"/"automation"/"claude_code"/"opencode"
    - `TestDetectClientMixed`——WorkBuddy 包 Claude Code 会话（SR 分流 + user_query 并存，两客户端均得高分且分差 < 2）断言返回 "mixed"；零特征会话（普通中文对话无任何签名）断言返回 "unknown" 且无 error
    - `TestDetectClientWeakSignal`——单客户端弱信号（仅一条共享形态命中 Weight 1、其余客户端零分）断言返回该客户端（次高点零分不判 mixed）
    - `TestDetectClientNewClientZeroIntrusion`——测试内注册 fake ClientRules（专属前缀 "<newclient-frame>"，Weight 3 Once），对含该前缀的会话断言 `DetectClient` 返回 fake 标识；既有五客户端典型样本（同 PureSamples 构造）重跑断言结果不变（承接 02 T5 交接的探测面断言，闭合 specs §5.1 新客户端接入零侵入场景的 DetectClient 半边；fake 前缀字面形态独特，注册污染不干扰其余用例）
  - 验证命令：`cd hr-backend && go test ./internal/engine/extractor/ -run TestDetectClient -v`
  - 预期输出：四个测试 PASS（PureSamples / Mixed / WeakSignal / NewClientZeroIntrusion）

  **specs 依据：**
  - 章节范围：§2.3 输出定义（DetectClient 签名与七值）、§2.4 能力7 第1-3条（特征来源、计分与裁决、识别不承载判定正确性）、§5.1（新客户端接入零侵入场景的探测面断言）、03 §4（契约表：输入输出失败态性能预算正确性边界）
  - 业务规则索引：
    - [BR1] §2.4 能力7 第2条：次高分 > 0 且最高分与次高分分差 < 2 返回 mixed（严格并列分差 0 含于该条件）
    - [BR2] §2.4 能力7 第2条：次高点为零分时单客户端信号即定归属，即便最高分为 1、分差 1 < 2 也不判 mixed
    - [BR3] §2.4 能力7 第2条：全部零分返回 unknown，属正常态不记错误日志
    - [BR4] §2.4 能力7 第2条：专属强特征权重 2-3、共享形态权重 1
    - [BR5] §2.4 能力7 第3条：识别失败不影响裁剪判定，并集执行不依赖识别正确性
    - [BR6] 03 §4：纯函数不调 LLM，永不返回 error；≤ 10ms/会话预算
    - [BR7] §5.1：测试内注册 fake ClientRules，探测识别新客户端，既有客户端规则行为无变化

  **依赖：** 子计划 01（DetectFeatures 已在客户端规则文件）

  **提交信息：** feat(extractor): DetectClient 客户端签名探测计分裁决

- [x] **T4: prepare 单趟接线与日志 client 字段**

  **文件：**
  - 修改：`hr-backend/internal/engine/extractor/extract.go`
  - 修改：`hr-backend/internal/engine/extractor/classify.go`（或 trim.go，实现者按单趟设计选址）
  - 测试：`hr-backend/internal/engine/extractor/extract_test.go`

  **契约：**
  - 签名（preparedData 扩展）：
  ```go
  type preparedData struct {
      view           TrimmedView
      trimStats      TrimStats
      profile        ProfileStats
      redactPatterns []string
      client         string // DetectClient 产出（探测并入本趟遍历）
      hasAIResponse  bool
      hasToolLines   bool
      keptBeforeTruncation int
  }
  ```
  - 关键逻辑：
    1. prepare 内调 detectScore（若 T3 的单趟合并设计：classifySequence 返回侧带出计分 map，prepare 裁决；若独立趟：prepare 直接调 DetectClient。两形态均满足「避免二次全量扫描」意图，实现者按单趟优先选址，探测耗时并入裁剪预算观测）
    2. 日志补 client：trim done（DEBUG）追加 `"client", p.client`；session skipped（INFO）追加 client；extract completed（DEBUG）追加。extract failed（ERROR）按 specs §6.1 示例带 code 已有，client 可选追加（失败路径 client 已产出，追加保持归因一致）
    3. logAttrs 扩展或独立追加：sessionRef.logAttrs 保持 key/token_name 两字段（复用路径无 detail 无 client），探测产出点局部追加，避免复用路径伪造 client
  - 依赖：T3

  **验收锚点：**
  - 核心断言：`TestPrepareCarriesClient`——含 `<user_query>` 形态的 detail 走 `ext.prepare`（或 ShouldSkip 间接），断言 preparedData.client == "workbuddy"；纯中文普通会话 client == "unknown"（内部测试包直访私有字段）
  - 验证命令：`cd hr-backend && go test ./internal/engine/extractor/ -run TestPrepareCarriesClient -v`
  - 预期输出：PASS

  **specs 依据：**
  - 章节范围：§2.4 能力7 注意事项（探测并入 classifySequence 单趟、独立函数形态保证可测性）、§3.1 性能表（探测耗时 ≤ 10ms 并入裁剪遍历单趟）、§6.1 日志规范（DEBUG/INFO 场景示例含 client 字段）
  - 业务规则索引：
    - [BR1] §2.4 能力7 注意事项：计分并入裁剪遍历单趟产出，避免二次全量扫描
    - [BR2] §6.1：日志字段追加 client（枚举值非消息内容，隐私口径不变）
    - [BR3] §3.1：探测耗时 ≤ 10ms/会话（500 消息内），与裁剪同量级

  **依赖：** T3

  **提交信息：** feat(extractor): prepare 单趟产出 client，日志补 client 字段

- [x] **T5: 三态落库接线（四规则）与端到端测试**

  **文件：**
  - 修改：`hr-backend/internal/engine/extractor/extract.go`（persist 三态 + persistCtx）
  - 测试：`hr-backend/internal/engine/extractor/extract_test.go`
  - 修改：`hr-backend/internal/model/migrate_test.go`（client 列建表断言）
  - 修改：`hr-backend/CLAUDE.md`、根 `CLAUDE.md`（规约文档同步）

  **契约：**
  - 签名（persistCtx 扩展与接线形态）：
  ```go
  type persistCtx struct {
      sessionRef
      detail *conversationlog.SessionDetail
      client string // 探测产出；detail_invalid 路径空串；终态化复用既有行值
      keepMeta *rowMeta
  }

  // persistRow 内：row.Client = c.client（三态统一）
  ```
  - 关键逻辑（四规则，specs §2.4 能力3 与 04 §3.1 业务规则逐条）：
    1. success/failed/skipped 正常三态行：doExtract 内 prepareAndJudge 返回后 pc.client = p.client，persistRow 统一写入
    2. detail_invalid 跳过路径：prepareAndJudge 在 detailInvalid 短路返回时 p 为零值，pc.client 落空串 ""（判定在 prepare 之前，无消息可探测）
    3. 残留 failed 行终态化（finalizeNotFound / 确定性上游错误 persistFailed 无 detail 场景）：pc.client 取既有行 existing.Client 复用（与 rowMeta 同一裁决：上游已无会话不重探测）；rowMetaOfRow 扩展携带 client 或 persistCtx 直接带既有行值，实现者选一（注意 ExtractByKey 拉详情失败路径 pc.detail == nil 且探测无输入，client 必须复用既有行）
    4. reuseTerminal 复用路径：库内行直接构造结果，client 回传既有行值天然成立（ExtractionResult 不加 Client 字段——specs §2.3 五方法返回结构无增减，client 只落库不进返回值）
    5. fakeFeatureRepo.Save 的翻转分支同步透传 r.Client = rec.Client（extract_test 内 fake 与真实现同构约定）
    6. migrate_test 补断言：session_features 建表含 client 列（Migrator HasColumn 或全列断言清单加 client）；规约文档同步：hr-backend/CLAUDE.md extractor 段落与根 CLAUDE.md 架构要点段落补分层结构与 client 列描述（规约文档随实现同步约束）
  - 依赖：T1, T2, T4

  **验收锚点：**
  - 核心断言：
    - `TestExtractClientColumnThreeStates`——(a) fake 详情（workbuddy 形态）跑 Extract 成功路径断言落库行 Client == "workbuddy"；(b) 预置既有 success 行（Client: "omo"）重跑断言复用且行 Client 保持 "omo" 不覆盖；(c) 预置 failed 行（Client: "unknown"）重跑翻转断言行 Client 更新为新探测值；(d) detail_invalid（空 messages）路径断言行 Client == ""
    - `TestFinalizeNotFoundKeepsClient`——预置残留 failed 行（Client: "automation"），ExtractByKey 命中 not found 终态化 skipped，断言行 Client 保持 "automation"
  - 验证命令：`cd hr-backend && go test ./internal/engine/extractor/ ./internal/model/ ./internal/repository/ -v`
  - 预期输出：三包全部测试 PASS（含 client 列建表断言与守护测试）

  **specs 依据：**
  - 章节范围：§2.4 能力3（client 列写入与四条幂等交互规则）、§2.3 输出定义（ExtractionResult 无增减、client 不进返回值）、04 §3.1 业务规则（六条）、§5.1（client 列落库测试场景）、§5.2（client 列建表场景）、根 CLAUDE.md 规约文档随实现同步约束
  - 业务规则索引：
    - [BR1] §2.4 能力3：三态行均写入 client（探测结果随行落库）
    - [BR2] §2.4 能力3：success/skipped 复用行不重探测，client 回传既有行值不覆盖
    - [BR3] §2.4 能力3：failed 行翻转时 client 随新探测结果覆盖（纯函数覆盖恒等）
    - [BR4] §2.4 能力3：残留 failed 行终态化时 client 随元数据复用既有行原值
    - [BR5] §2.4 能力3：detail_invalid 跳过行未经探测 client 落空串（七值之外显式例外）
    - [BR6] §5.2：全新库建表即含 client 列，新抽取三态行均带值

  **依赖：** T1, T2, T4

  **提交信息：** feat(extractor): 三态落库接线 client 列四规则，建表断言与规约文档同步

- [x] **T6: 集成测试补齐（client 建表与追加语义端到端）与探测单趟观测**

  **文件：**
  - 测试：`hr-backend/internal/engine/extractor/extract_test.go`（追加语义端到端）
  - 测试：`hr-backend/internal/engine/extractor/detect_test.go`（探测单趟观测）
  - 修改：`hr-backend/internal/model/migrate_test.go`（client 列建表断言，若 T5 已落则本任务核验）

  **契约：**
  - 签名（新增测试）：
    ```go
    // TestAppendPrefixIntegration 分层下参数页追加端到端（specs §5.2 场景2）：
    // fake sysParams 注入追加前缀 "ZZZ-append-prefix"，对含该前缀的多客户端会话
    // （claude_code 形态 + omo 形态各一条命中消息）跑 Extract；对照组预置删除
    // 出厂前缀配置形态（追加语义下配置删除不收回代码前缀）。
    func TestAppendPrefixIntegration(t *testing.T)

    // TestDetectSinglePassBudget 500 消息会话跑 prepare（裁剪+探测单趟），
    // 观测口径断言耗时预算内（非硬失败，超限 t.Log 留观测）。
    func TestDetectSinglePassBudget(t *testing.T)
    ```
  - 关键逻辑：TestAppendPrefixIntegration 断言追加前缀对 claude_code 形态与 omo 形态会话均生效（命中消息 drop 或进 InjectedDropped），对照组确认显式配置不含出厂前缀时出厂前缀仍拦截（代码并集不受配置影响）；TestDetectSinglePassBudget 构造 500 消息（复用基线 TestTrimPerformance 的构造手法），计时 prepare 全链，断言耗时显著高于纯裁剪基线的差值在探测预算量级（≤ 10ms 观测线，超限 t.Log 不 t.Fatal）
  - 依赖：T3, T4, T5；子计划 02 T3

  **验收锚点：**
  - 核心断言：`TestAppendPrefixIntegration`——追加集 {\"ZZZ-append-prefix\"} 下两类客户端会话的命中消息均被 drop；`TestDetectSinglePassBudget`——500 消息 prepare 完成且探测增量耗时 < 10ms（t.Log 观测口径）
  - 验证命令：`cd hr-backend && go test ./internal/engine/extractor/ -run "TestAppendPrefixIntegration|TestDetectSinglePassBudget" -v`
  - 预期输出：两个测试 PASS
  - 覆盖率验收（specs §5.4）：`cd hr-backend && go test ./internal/engine/extractor/... ./internal/model/ ./internal/repository/ -cover`，单元覆盖率 ≥ 80%、核心能力（判定链等价性、注册表、探测、client 落库相关文件）≥ 95%，未达标补用例后再收尾

  **specs 依据：**
  - 章节范围：§5.2（client 列建表、参数页全局追加在分层下生效两场景及对照组裁定）、§5.1（探测并入单趟观测口径）、§3.1（探测耗时 ≤ 10ms 预算行）
  - 业务规则索引：
    - [BR1] §5.2：新前缀对全部客户端会话生效（通用层追加语义），判定与基线参数联动行为一致
    - [BR2] §5.2：对照组确认配置删除不收回代码前缀（与基线替换语义的已知行为差异，specs 裁定）
    - [BR3] §5.1：500 消息会话跑 prepare，裁剪+探测合计不显著高于基线裁剪单独耗时
    - [BR4] §5.2：全新库启动建表，session_features 建表即含 client 列，新抽取三态行均带值

  **依赖：** T3, T4, T5；子计划 02 T3

  **提交信息：** test(extractor): 追加语义端到端与探测单趟观测集成测试
