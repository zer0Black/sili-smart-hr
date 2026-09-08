# CLAUDE.md

本文件给在后端目录（hr-backend/）下工作的 Claude Code 提供操作规约。项目整体定位、跨前后端约定、本地启动见上一层 [../CLAUDE.md](../CLAUDE.md)，本文件只承载后端独有约定。Go module 名是 `sili-smart-hr/backend`，与目录名 hr-backend 解耦，import 路径一律写 `sili-smart-hr/backend/internal/...`，根 package 是 `app`（装配层），不是 `main`。当前处于 B 档可运行脚手架阶段，account 登录链路（domain → repository → service → handler → router → wire）是首个可运行样板，用户管理、维度、系统参数、大模型配置、集成密钥等域已按同构四层贯通，engine 的 extractor（会话特征抽取全链路）、activity（活跃度统计）、evaluator（跨会话综合评估）、scorer（多维聚合）四个子域已实现，其余 2 个子域（pipeline/fallback）仍是 doc.go 占位，integration 的 3 个客户端已全量实现，worker 已实现并注册健康任务、会话抽取任务与单人评估任务（engine:person-evaluate）。answer / questionbank / profile / dashboard / workspace 等业务域尚未开工。规格权威源是 [../context/03_architecture/architecture.md](../context/03_architecture/architecture.md)，第 4 章承载运行时约定，代码注释反复引用它。

## 常用命令

```bash
go run ./cmd/server                 # 启动，默认 SQLite + localhost:6379 Redis，监听 8080
go build ./cmd/server               # 编译
go test ./...                       # 全量测试
go test ./internal/service -run TestLoginSuccess   # 跑单个测试
go generate ./...                   # 再生 wire_gen.go，改了 wire.go 的 provider 集合后必跑
```

切 PostgreSQL：`SQL_DSN="postgres://sili:sili@localhost:5432/sili_smart_hr?sslmode=disable" go run ./cmd/server`。Redis 是 Asynq 硬依赖，后端启动前必须可 ping 通，否则进程直接退出。

## 目录结构与分层

`internal/` 既定四层加横切能力层。`cmd/server` 是单进程入口，同时拉起 Gin HTTP server、Asynq worker server 与 Asynq scheduler。`config` 用 Viper 加载 yaml 与环境变量。`domain` 是 GORM 模型纯数据结构，`model` 承载数据库连接、方言切换与迁移编排。`repository`、`service` 是数据访问与业务逻辑层，`api/{handler,middleware,router}` 是 HTTP 接口层，`pkg/{errcode,jwt,response}` 是内部通用工具。`engine/extractor` 已实现会话特征抽取（裁剪、抽取、落库、过滤、脱敏、ExtractByKey 编排入口），`engine/activity` 已实现活跃度统计（IdentifyPopulation/ParseDigests 纯函数 + Activity 组件 StatPersonByKey/StatPerson，取数前移 7 天缓冲加末轮归属，经 ActivityStatRepository upsert 幂等落库），`engine/evaluator` 已实现跨会话综合评估（EvaluatePerson 原子入口、Evaluate 幂等评估、AssembleProfileSet 档案组装），`engine/scorer` 已实现多维聚合（Aggregate 按 evidence_json 口径摘要聚合模块分与总览分），三者经 Wire 全链装配接入 worker；`engine/{fallback,pipeline}` 目前仍是 doc.go 占位，每个 doc.go 的 package 注释已写明该子域未来职责，是后续 feature 落地时的落点指引。`integration/{conversationlog,llm,userapi}` 已全量实现。`worker/{server,scheduler,task}` 已实现。架构文档提及的 `openapi/` 目录尚未建立。

业务域在 `service` 与 `handler` 内按文件划分（新域建 `service/dimension.go`、`handler/dimension.go`），新 domain 模型必须在 [model/migrate.go](internal/model/migrate.go) 的 `allModels()` 登记才参与 AutoMigrate。

## 入口与生命周期

[cmd/server/main.go](cmd/server/main.go) 的启动序列有几处非显而易见的设计。slog 双段设置：先以 info 起步跑完 InitializeApp，再用配置的 log level 重设，初始化阶段日志恒为 info 级。生产密钥熔断条件是 `!IsSQLite() && IsDefaultJWTSecret()`，即只有非 SQLite 库且仍用源码公开默认密钥才拒绝启动，SQLite 开发模式放行。Redis 预检 5 秒超时 ping，失败即 `os.Exit(1)`。scheduler 用独立 goroutine 跑，失败经带缓冲的 `fatalErr` channel 通知主循环，没有这个机制定时任务会静默停止而 `/health` 仍报 ok，新增长寿命组件应仿照接入 fatalErr。优雅关闭顺序是 HTTP Shutdown → AsynqServer.Shutdown → Scheduler.Shutdown → AsynqClient.Close → DB.Close → Redis.Close，先停入口流量再停消费侧最后关存储。HTTP 超时（ReadHeader 10s、Read 30s、Write 30s、Idle 120s）是硬编码常量。

## Wire 依赖注入

三件套分工：[wire.go](wire.go)（build tag `wireinject`）定义 injector `InitializeApp`，函数体 `return nil, nil` 是占位，Wire 据此生成真实代码；[wire_gen.go](wire_gen.go)（build tag `!wireinject`）是生成产物，不可手改，`//go:generate` 指令挂在这里；[providers.go](providers.go)（package `app`）承载 `App` 聚合 struct 与 Wire 无法自动推断的手工 provider。

装配链经 Wire 编排：config → db → redis → asynq → repository → service → handler → router，main 拿到聚合的 `App` 驱动各组件生命周期。改了 provider 集合后跑 `go generate ./...` 再生 wire_gen.go（指令实际执行 `go run -mod=mod github.com/google/wire/cmd/wire`，wire 作为普通依赖靠 go run 拉起，无需单独安装）。

三处 Wire 陷阱都在 providers.go。`NewHTTPAddr` 用命名类型 `type HTTPAddr string` 而非裸 string，因为 injector 入参 `configPath` 也是 string，若返回裸 string 会与 configPath 在 Wire 类型绑定表里产生 multiple bindings 冲突，新增任何返回 string 的 provider 都要改用命名类型。`NewAsynqConnOpt` 与 `NewRedisClient` 不共享 redis 实例，Asynq 用 `asynq.RedisClientOpt` 自建 go-redis v9 连接，与项目自己的 `*redis.Client` 是两个独立连接池连同一 Redis，这是 Asynq SDK 限制，不要试图共享。

## 分层与接口约定

双重接口模式是测试可注入性的关键，account 链路是示范：`repository.AccountRepository` 接口定义在 [repository/account.go](internal/repository/account.go) 包内，`service.AccountService` 接口定义在 [service/account.go](internal/service/account.go) 包内，实现结构体小写不导出，只通过 `New*` 返回接口。service 测试用 fake 实现 repo 接口，编译期断言保证符合契约。

Service 层错误模型分两类：`*service.Error{Code, Msg}` 是业务错误，`NewError` 从 errcode 取文案；`fmt.Errorf("...: %w", err)` 是 wrap 底层的内部错误。

Handler 错误映射见 [handler/account.go](internal/api/handler/account.go) 的 `handleServiceError`：所有成功 HTTP 200；鉴权类（code 1003）HTTP 401；其余业务错误（含 InvalidCredentials 1001、BadRequest 1400）HTTP 200 带 code；非 `*service.Error` HTTP 500 带 code 1500。登录失败因此返回 HTTP 200、body code=1001，与前端约定一致（前端拦截器看 body code 而非 HTTP status）。新写 handler 必须用 `handleServiceError`，不要自行 c.JSON。Handler 经 `middleware.AccountIDFromContext(c)` 取 `account_id`（int64），不直接读 claims，中间件已注入 gin.Context。

## 数据模型与多库

[model/db.go](internal/model/db.go) 的 `chooseDB` 按三段前缀切库：DSN 空或 local 前缀走 glebarez/sqlite 纯 Go 驱动（路径硬编码 `data/sili-smart-hr.db`，会建目录）；postgres:// 或 postgresql:// 前缀走 PostgreSQL 并关 PreferSimpleProtocol 规避长连接 stale prepared plan；其余走 MySQL 并自动补 parseTime 以扫描 time.Time。方言在 InitDB 时一次性锁定为包级全局 `current`（`model.DatabaseType`），进程级不可变，测试里改 `current` 必须用 `t.Cleanup` 还原。

方言工具三件套：`QuoteIdent`（PG 双引号、SQLite/MySQL 反引号，包裹 group/key/order 等保留字列名）、`BoolLit`（PG true/false、SQLite/MySQL 1/0）、`EscapeLike`（转义 LIKE 通配符 `%`/`_`/`\`，配合 `ESCAPE '\'` 子句做字面匹配）。QuoteIdent/BoolLit 目前只被 db.go 内部和测试调用，业务域尚未使用，为未来手写含保留字或布尔字面量的原生 SQL 预留；EscapeLike 已被 account 域 `ListAccounts` 使用，凡接收用户输入的 LIKE 查询都必须用它（详见 [AGENTS_DATABASE_API_RULE.md](../AGENTS_DATABASE_API_RULE.md) 1.11）。业务代码优先用 GORM 链式 API（如 `Where("col = ?", true)`），只有手写含保留字、布尔字面量或 LIKE 通配符的 SQL 片段才用这些工具。

[model/migrate.go](internal/model/migrate.go) 的 `migrateDB` 三段式：A 段类型迁移钩子必须在 AutoMigrate 之前（先 Migrator 探测再按方言 ALTER，否则旧类型仍在），目前承载 session_features.client 列的存量库加列钩子（NOT NULL 无 default 的加列在有数据行的库上会被 SQLite/PG 拒绝，钩子以 `DEFAULT ''` 兜底）与 aggregate_scores 单精度 float 列改双精度钩子（早期 type:float 在 MySQL 落单精度 FLOAT，经 Migrator.ColumnTypes 探测列类型再按方言 ALTER，SQLite 类型亲和直接跳过）；B 段 AutoMigrate 建表加列加索引为主，GORM 不删列不改列类型；C 段数据回填钩子用幂等 UPDATE 放 AutoMigrate 之后，目前承载 inject_prefixes 替换语义存量行（出厂全集 JSON）一次性重置为空数组（精确匹配全集值，运维改过的行不触碰）。无版本号、每次启动重跑、靠幂等保证安全。跨方言改列钩子的统一形态：SQLite 因类型亲和直接跳过，PG/MySQL 先探测列当前类型再按方言 ALTER。自定义 type tag 必须用方言规范名（如 double precision），裸 double 在 PG 是非法类型名首启即拒。SQLite 不设连接池，只有 MySQL/PG 调 applyPool（SQLite 设连接池会破坏单写特性）。

字段约定从源头规避多库方言坑：模型 tag 用 GORM 通用类型（varchar/text/int/bigint），不写 jsonb/timestamptz/bigserial 等库特定类型；时间用 time.Time 配 autoCreateTime/autoUpdateTime，不写 DATETIME/TIMESTAMP 字面量；半结构化数据（特征档案、评分理由）用 TEXT 列存序列化字符串，不用 JSON 类型列；保留字列名用 QuoteIdent 包裹；外键约束不开，关联靠业务字段；布尔字段在模型层用 bool 但不加 default tag，避免 MySQL 与 PG 布尔默认值规范化差异让 AutoMigrate 每次启动判定需要 ALTER 形成抖动，新建账号由业务层显式置 true。[domain/account.go](internal/domain/account.go) 是多库兼容 tag 示范，`PasswordHash` 用 `json:"-"`，任何序列化路径都不泄露，新模型含敏感字段应仿此。

## 响应与错误

统一响应结构在 [pkg/response/response.go](internal/pkg/response/response.go)：`Response{Code int, Message string, Data any}`，`Page{List any, Total int64, Page, PageSize int}`，分页作 data 透传。一处实现偏差需留意：架构文档描述 `Response.Data` 为 `json:"data,omitempty"`，但实际 response.go 是 `json:"data"` 无 omitempty，因此 `OK()` 无 data 时 body 是 `{"code":0,"message":"ok","data":null}`，data 字段不会被省略。

错误码集中在 [pkg/errcode/errcode.go](internal/pkg/errcode/errcode.go)：Success=0、InvalidCredentials=1001、AccountDisabled=1002、Unauthorized=1003、BadRequest=1400、Internal=1500。1400/1500 用千位前缀区分类别（1xxx 业务错误）。`AccountDisabled`(1002) 定义了但登录路径刻意不返回（反枚举），注册它仅为未来管理后台启用禁用操作预留。`errcode.Message(code)` 未注册返回 "error"。`response.Fail(c, httpStatus, code)` 文案自动取 errcode.Message。把底层错误 Error() 动态透传进前端 Msg 时必须按错误类别白名单兜底：网络、ctx 取消、URL parse 类错误链携带完整上游 URL（specs 330 定位信息白名单外），命中即换通用文案，完整原因只落日志，样板见 [service/integration_secret.go](internal/service/integration_secret.go) 的 Test。

## 横切能力层

engine 层的 extractor 子域已实现（会话特征抽取，specs P2_TECH_003 基线 + P2_TECH_004 规则分层）：逐会话读入原始对话，裁剪框架注入噪音（判定链为通用层 + rules 包客户端规则注册表并集执行，前缀黑名单经 system_params 追加语义热调，新客户端接入 = 新增 rules/<client>.go + Register 一行）后喂 LLM 压缩成四块特征档案（Stats 规则产出 + Summary/Instruction/Behavior LLM 产出，脱敏后落 session_features 表）。DetectClient 探测（tool_use/tool_result 元信息行与 transcript 重放区段跳过计分，两口径与分类侧剥离一致）在 classifySequence 单趟消息遍历内联产出主客户端标识，三态行均携带 client 列：正常路径取探测产出（七值），detail_invalid 行空串，无 detail 的终态化复用既有行值（legacy 空串归一 unknown，防混入 detail_invalid 桶）、首抽拉取失败落 unknown（与 detail_invalid 空串口径区分）。ExtractByKey 封装拉详情加抽取全流程供 worker 逐会话调度；activity 子域已实现（specs P2_TECH_005）：IdentifyPopulation 人群签名识别纯函数与 Activity 组件 StatPersonByKey/StatPerson 活跃度统计（纯规则不调 LLM，档案取数 start 前移 7 天缓冲再按 LastTurn 末轮归属归一到本周期，计数与签名判据共用同一集合，activity_stats 经仓储 Upsert 幂等落库）；evaluator 子域已实现（specs P2_TECH_005）：EvaluatePerson 单人周期原子入口编排活跃度→评估→聚合（三表各自落库不包事务，重跑幂等收敛），Evaluate 主流程含幂等预检（全 success 且 prompt_version 与当前常量一致才复用跳过 LLM、failed 与 skip_no_llm 标记行先删后评，零 LLM 跳过与空提示词补行路径均带自愈标记不进复用，模板升级发版按版本比对触发重评）、零档案与签名命中走 Skipped 全维度 insufficient 行、LLM 失败降级落 failed 行（err=nil 终态不重试）、schema 校验白名单收敛与 rationale 落库前 Redact 兜底脱敏，AssembleProfileSet 档案集分层组装供复用；scorer 子域已实现：Aggregate 读同人同周期全部 source 评分行（conversation 与 active_test 并列）按 evidence_json 口径摘要聚合模块分与总览分，insufficient/failed 剔除后剩余权重归一，全剔除 nil 显式落空，幂等 upsert。评估链装配在 [providers.go](providers.go)：EvaluatorLLMClient 评估专用 client（180s Timeout / TokenBudget 51000 / 独立并发 gate，与全局及 extractor 通道互不挤占），ActivityThresholdReader 与 DimensionSpecReaderAdapter 把 DimensionRepository 适配为 activity.ThresholdReader / evaluator.DimensionSpecReader 窄接口（读取失败带上下文上抛无默认值回退，哨兵由消费方包各自 wrap），NewEvaluatorProvider 九参组装三组件。pipeline、fallback 2 个子域仍为 doc.go 占位。隐私架构要点：对话原文只在 extractor 阶段临时进内存喂 LLM，抽取后丢弃，主库只存脱敏特征档案与评分理由，PG 不存对话原文。

integration 层 3 个客户端已全量实现：conversationlog 是 sili-smart-api 会话日志的只读消费客户端（Bearer 鉴权，conversation_turns 增量存储需详情接口拼回，ClickHouse 中 id 恒为 0 业务标识用 request_id，按 session_key 组织），提供 ListSessions（username/token_name 上游精确过滤、时间窗过滤、分页钳制、UserID 内存过滤）、GetSessionDetail（turns/messages 全量归一化）与 Ping 探活，统一承载分档超时（探活 5s / 列表详情 30s）、列表与详情的指数退避重试（10s→20s→40s）与 sentinel 错误分类（ErrUpstreamBusiness 载体 *UpstreamError 支持 IsNotFound 识别）；llm 是平台大模型调用底座（排他启用唯一模型，全系统 AI 调用都走当前启用模型），统一请求/响应契约与错误码、全局并发限制与 FIFO 排队、token 预算预检、指数退避重试、OpenAI 兼容与 Anthropic 双协议适配器及统一调用入口；userapi 对接 sili-smart-api 用户信息接口（username 模糊查询、分页），响应经 LimitReader 限 10MB，只取 user_id 与 username 投影为 Staff。改这些客户端时以包内既有实现与测试为基准（信封指针校验、body drain、ctx 取消口径、哨兵语义均有活样板）。

worker 层已实现：[worker/server](internal/worker/server) 构造 Asynq server（并发度 `<=0` 兜底 10，RetryDelayFunc 经 retryDelay 函数以 30s 基准倍增封顶 10min 且对 asynq 首次重试 n=0 钳位防负移位 panic——asynq 以自增前的 msg.Retried 调用该函数，panic 落点在 recover 保护之外会击穿进程），[worker/scheduler](internal/worker/scheduler) 注册每分钟 cron 触发 HealthCheck（Location 用 time.Local），[worker/task](internal/worker/task) 的 NewMux 统一注册三类任务：HealthCheck 是 no-op 仅写 slog 证明跑批通道在跑，engine:session-extract（[session_extract.go](internal/worker/task/session_extract.go)）是单会话特征抽取任务（任务级超时 600s 单点声明在 sessionExtractTimeout，550s 最坏全链加 50s 落库冗余；payload 携 session_key/token_name，坏格式与空值 payload 均直接返回 nil 丢弃任务记 ERROR（构造侧确定性错误，重试恒失败）；err==nil 含 Skipped 与 failed 终态均不重试，仅 error 通道交 Asynq 重试），engine:person-evaluate（[person_evaluate.go](internal/worker/task/person_evaluate.go)）是单人周期评估任务（任务级超时 1050s 单点声明在 personEvaluateTimeout，LLM 段最坏 840s、列表翻页与落库共享剩余 210s，每页最坏 190s 多页线性放大由 listMaxPages 页数上限兜底；payload 携 token_name/period_start/period_end，坏格式、空 token_name、period_start ≤ 0、period_end ≤ period_start 均返回 nil 丢弃记 ERROR；handler 调 EvaluatePerson 透传 err，err==nil 含 Skipped/Reused/failed 降级终态均不重试）。新任务类型在 worker/task 加 `const TypeXxx` 加 handler，再经 NewMux 参数注入注册（NewMux 是单一注册入口，双 handler 形参在 providers.go 经命名类型 + NewMuxAdapter 适配规避 Wire 同型参数限制），新定时任务在 scheduler 的 NewScheduler 内 Register，worker 并发度由 config.Asynq.Concurrency 控制。架构文档提到跑批默认周日 23 点，真实跑批 cron 归 pipeline 子域尚未注册。

## 配置

[config/config.go](internal/config/config.go) 用 Viper 读 yaml 加 BindEnv 绑定环境变量。已绑定环境变量的有 SQL_DSN、JWT_SECRET、SQL_MAX_IDLE_CONNS、SQL_MAX_OPEN_CONNS、SQL_MAX_LIFETIME、REDIS_ADDR、SERVER_PORT。注意 redis.password、redis.db、asynq.concurrency、cors.allowed_origins、jwt.ttl、log.level 只在 yaml 未绑环境变量，改这些必须动 [configs/config.yaml](configs/config.yaml)。`applyDefaults` 让空配置也能跑（port 8080、redis localhost:6379、jwt 默认密钥、ttl 24h、concurrency 10、level info、dsn "local"）。

DSN 方言探测：`IsSQLite` 是 DN空或 local 前缀，`IsPostgres` 是 postgres:// 或 postgresql:// 前缀，其余视作 MySQL，config_test.go 的 TestDSNDialectDetection 是理解前缀判定边界的权威用例（覆盖 local-dev 这类后缀仍算 SQLite）。`IsDefaultJWTSecret` 与硬编码常量 `sili-smart-hr-dev-jwt-secret-7f3a9e2b1c4d6e8a` 比对，该常量同时出现在 config.go 和 configs/config.yaml 双重公开，这就是生产强制 JWT_SECRET 覆盖的根因。

## JWT 与 account 样板

[pkg/jwt/jwt.go](internal/pkg/jwt/jwt.go) 的 Manager 用 HS256 对称签名，claims 是 `{AccountID int64, Username string, jwt.RegisteredClaims}`，强制 issuer 为 `sili-smart-hr` 且 Parse 时校验，防同密钥被其他服务 token 跨服务重放；Parse 强制校验签名方法是 HMAC 防 alg=none 攻击；Generate 空 secret 直接报错。JWT 中间件从 Authorization: Bearer 取 token，解析失败返 401 加 code 1003，成功注入 account_id 与 username 到 gin.Context。

account 登录链路（domain → repository → service → handler → router → wire）是新增业务域的参考样板。反枚举设计最需谨慎：三条路径（账号不存在、密码错误、账号禁用）统一返回 InvalidCredentials(1001)，账号不存在时用启动时预生成的 dummyHash 执行一次等效 bcrypt 比对收敛时序侧信道。顺序敏感：先 bcrypt 比对密码再判 Enabled，这样禁用账号也经过一次 bcrypt 且禁用也返回 InvalidCredentials，调整这段顺序会破坏时序收敛。`last_login_at` 更新失败只 warn 不阻断登录。路由组织上，全局中间件 Recovery → Logger → CORS（recovery 最外层兜底 panic），公开路由 GET /health、POST /api/login、GET/POST /api/setup/* 与 GET /api/system/status 不挂 JWT，受保护组 `r.Group("/api", middleware.JWT(jwtMgr))` 含 GET /api/me 与 POST /api/system/health-check，新业务接口归入这个 auth 组。系统不再预置任何账号，SQLite 与 PG/MySQL 首启 accounts 与 system_initializations 均空，首个账号经 /api/setup/initialize 创建并写入初始化记录。

## 测试约定

包命名分两种：多数用黑盒（`package config_test`、`package jwt_test`、`package service_test`，只能访问导出符号），model 用白盒 `package model` 因为要访问私有项 current、ensureParseTime、migrateDB。Fake 模式见 [service/account_test.go](internal/service/account_test.go)：`fakeRepo` 实现 `repository.AccountRepository` 接口，用字段挂可配置返回值加 lastLoginCalled 探针，配合编译期接口断言 `var _ repository.AccountRepository = (*fakeRepo)(nil)`，接口加方法时 fake 编译即报错防漂移。fake 模拟错误文案时必须与真实 Error() 输出格式对齐（如 classifiedError 只输出 cause 不带哨兵前缀），断言锚定真实格式才能守护文案契约。bcrypt 测试用 `bcrypt.MinCost` 加速，生产用 `bcrypt.DefaultCost`（setup.go Initialize 与 account service 的密码哈希都用 DefaultCost），生产别为快降 cost。model 测试改全局 `current` 后用 t.Cleanup 还原，迁移测试用 :memory: SQLite 免文件清理。

## 陷阱

编译上 [Dockerfile](Dockerfile) 用 `CGO_ENABLED=0` 依赖 glebarez/sqlite 纯 Go 驱动，绝不要引入 mattn/go-sqlite3（CGO 依赖）会破坏静态编译，配合 `-trimpath -ldflags="-s -w"` 减体积。AutoMigrate 布尔字段不加 default tag 规避 MySQL/PG 规范化差异抖动，且 GORM 不删列不改列类型，这类变更在 migrateDB 加幂等手写迁移钩子。Redis 无免起方案，本地开发必须先起 Redis（docker compose 或单独容器），启动 ping 不通即 exit。Wire 上 HTTPAddr 命名类型避免 string 冲突，Asynq 与项目 Redis 客户端不共享连接实例，wire.go 的 `return nil, nil` 是占位别手改 wire_gen.go。安全上默认 JWT secret 双重公开，生产强制覆盖；系统无预置账号，所有库首启均空，首个账号经向导创建；登录反枚举顺序敏感；response.Data 实际无 omitempty，成功响应会带 `data:null`。生命周期上 scheduler 失败经 fatalErr channel 防 /health 假绿，新长寿命组件仿此；测试改全局 model.current 必须 t.Cleanup 还原。
