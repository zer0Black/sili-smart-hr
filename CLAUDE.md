# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目定位

sili-smart-hr 是综合人才测评平台：把兄弟系统 sili-smart-api（LLM 网关）沉淀的 AI 对话日志周期性转化为能力评分，叠加主动测试形成人才画像，用于能力盘点与培训提升。

当前处于 B 档可运行脚手架阶段，account 登录链路（POST /api/login、GET /api/me）是首个样板，用户管理、维度配置、系统参数、大模型配置、集成密钥等链路已前后端贯通。engine 在后端的 extractor（会话特征抽取）与 activity（使用活跃度统计）子域已实现，其余 4 个子域仍为占位，answer / questionbank / profile / dashboard / workspace 等业务域前后端均未开工。新增业务域时，account 全链路（后端 domain → repository → service → handler → router → wire，前端 feature → route）是参考样板。

规格依据在 [context/](context/) 目录，[context/03_architecture/architecture.md](context/03_architecture/architecture.md) 是模块划分与依赖关系的权威来源，第 4 章承载运行时约定。

## 文档分层（披露式加载）

单仓双目录，前后端各有独立 CLAUDE.md 承载该端命令、架构与陷阱。本根文件只承载跨端整体规约、本地启动与全局约束，进入子目录工作前先读对应子文档：

- 后端（Go）见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)：module 名 `sili-smart-hr/backend`、Wire 装配、四层分层、多库切换与迁移、errcode、worker、account 样板、测试约定。
- 前端（React）见 [hr-frontend/CLAUDE.md](hr-frontend/CLAUDE.md)：路由与鉴权布局、httpClient 解包契约、Zustand persist、TanStack Query 封装、i18n、Tailwind 4 token、account feature 样板。
- 前端设计系统见 [hr-frontend/DESIGN.md](hr-frontend/DESIGN.md)，实现权威在 [hr-frontend/src/styles/globals.css](hr-frontend/src/styles/globals.css)，冲突以 globals.css 为准。

进入 hr-backend/ 或 hr-frontend/ 子目录时会自动加载该目录 CLAUDE.md，与本文档叠加生效。

## 技术栈

后端 Go 1.25：Gin、GORM（按 SQL_DSN 切 SQLite/MySQL/PostgreSQL）、Asynq（Redis 异步任务与定时调度）、Wire（编译时依赖注入）、Viper、JWT（golang-jwt/v5）、slog。

前端 React 19 + TypeScript：Rsbuild 构建、TanStack Router（文件路由）与 TanStack Query、Zustand、axios、shadcn/ui（new-york）、Tailwind 4、React Hook Form + Zod、Recharts、TanStack Table、i18next 双语。

文档与实现的一处偏差：架构文档把 @base-ui/react 列为在用，但 package.json 尚未引入，属计划项；Recharts、TanStack Table、dayjs 同为预留依赖当前未用。

## 本地启动

需要 Redis、后端、前端三件事。数据库默认 SQLite 免起库，无需单独装 PostgreSQL。Redis 是 Asynq 硬依赖，必须最先起且后端能 ping 通，否则启动即退出。

起 Redis（与 PostgreSQL，后者可选）：

```bash
docker compose up -d
```

只要 Redis：`docker run -d --name sili-redis -p 6379:6379 redis:7-alpine`。

起后端：hr-backend/ 下 `go run ./cmd/server`，默认监听 8080，数据库落 data/sili-smart-hr.db，首启自动建表，不预置任何账号。切 PostgreSQL（设 SQL_DSN）、`go generate ./...` 再生 wire_gen.go 等见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)。

起前端：hr-frontend/ 下 `pnpm install && pnpm dev`，跑在 3000，/api 与 /health 经 rsbuild proxy 转发后端同源免跨域。type-check、shadcn 引入等见 [hr-frontend/CLAUDE.md](hr-frontend/CLAUDE.md)。

浏览器开 http://localhost:3000，新库首访无账号，__root beforeLoad 探针据 system_initializations 记录存在性把未初始化态重定向到 /setup 向导，在向导页创建首个账号后跳 /login 闭合鉴权链路。

环境坑：Rsbuild dev 在部分 Windows 绑到 ::1，localhost 不通时改 127.0.0.1；系统代理（常见 7897）会劫持 go mod 与 curl，卡住时关代理或配 GOPROXY 与 docker 镜像加速；TaskStop 停后台进程可能留孤儿，需 taskkill 按 PID 清理。

## 架构要点（宏观）

单进程内嵌。cmd/server 在一个二进制里同时拉起 Gin HTTP server、Asynq worker server 与 scheduler，捕获信号优雅关闭（先 HTTP，再 asynq，最后 DB 与 Redis）。

装配链由 Wire 编排：config → db → redis → asynq → repository → service → handler → router，main 拿聚合的 App 结构驱动生命周期。改 provider 集合后 `go generate ./...` 再生 wire_gen.go。细节见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)。

后端四层：API（handler/middleware/router）→ Service → Repository → Domain，engine、integration、worker 为横切能力层。Service 用接口定义依赖（如 AccountRepository、AccountService）便于注入 fake。integration 的 3 个客户端（conversationlog/userapi/llm）已全量实现，engine 的 extractor 子域已实现（会话特征抽取全链路与 Asynq 任务注册，判定链为通用层 + 客户端规则注册表并集，session_features 行携带探测的主客户端 client 列），activity 子域已实现（人群签名识别 IdentifyPopulation 纯函数 + StatPersonByKey/StatPerson 活跃度统计，纯规则不调 LLM，24h 缓冲加末轮归属的取数口径，activity_stats 经仓储 Upsert 幂等落库），evaluator 子域已实现（跨会话综合评估：EvaluatePerson 原子入口编排活跃度→评估→聚合三表落库，Evaluate 幂等判定与 LLM 降级终态，档案集分层组装与五段 prompt），scorer 子域已实现（读同人同周期全部 source 评分行按 evidence_json 口径摘要聚合模块分与总览分，幂等 upsert），三者经评估专用 LLM client（180s Timeout 独立并发 gate）与 DimensionRepository 双适配器完成 Wire 全链装配；其余 2 个子域（pipeline/fallback）仍为 doc.go 占位，worker 已实现跑批通道并注册健康任务、会话抽取任务与单人评估任务（engine:person-evaluate，任务级超时 1050s）。

多库切换在 model/db.go 的 chooseDB，按 SQL_DSN 前缀选 dialector，glebarez/sqlite 纯 Go 实现支撑 CGO_ENABLED=0 静态编译。迁移由 migrateDB 编排，AutoMigrate 为主，类型变更与数据回填补手写幂等迁移。方言工具（QuoteIdent/BoolLit）与三段式迁移见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)。

统一响应 `{code, message, data}`，分页 `{list, total, page, page_size}` 作 data 透传；错误码集中 [hr-backend/internal/pkg/errcode/errcode.go](hr-backend/internal/pkg/errcode/errcode.go)，code=0 成功。handler 把 `*service.Error` 映射 HTTP 状态：鉴权失败 401，其余业务错误 200 带 code。前端 httpClient 据此解包，消费方直接拿 data，code 非 0 抛 ApiError。

## 关键约束

登录反枚举。账号不存在、密码错误、账号禁用三条路径都返回 InvalidCredentials（1001），并各执行一次等效 bcrypt 比对收敛时序侧信道（顺序敏感：先 bcrypt 再判 Enabled）。errcode 定义了 AccountDisabled（1002）但登录路径刻意不返回。前端登录页只识别凭证错误与通用错误两类。

开发 JWT 密钥公开可 forge。config 默认密钥已随源码公开，生产（非 SQLite）启动若未由 JWT_SECRET 覆盖，main.go 直接拒绝启动。系统无预置账号，所有库（含 SQLite）首启均空 accounts 与 system_initializations，首个账号经 /setup 向导创建并写入初始化记录锁定状态。

系统无工号。所有人都没有员工编号，任何人员相关界面（后端模型、前端列表与搜索）不得出现工号列或 empNo 字段。

AI 测评对话式施测。主动测试不把选项结构化写进任务，员工作答与 AI 逐题对话；作答页（routes/answer/$token）用一次性令牌鉴权，与主平台 JWT 物理隔离，刻意不挂 _authenticated 布局。大模型配置页排他启用唯一模型，全系统 AI 调用都走当前启用模型，无独立默认。

数据库字段约定（从源头规避多库方言坑）。时间用 time.Time 配 autoCreateTime/autoUpdateTime，不写 DATETIME/TIMESTAMP 字面量；半结构化数据（特征档案、评分理由）用 TEXT 列存序列化字符串，不用 JSON 类型列；外键约束不开，关联靠业务字段；布尔字段模型层用 bool 但不加 default:true/false tag，避免 MySQL 与 PG 默认值差异让 AutoMigrate 每次启动判定需 ALTER 形成抖动（account.Enabled 为示范，新建账号由业务层显式置 true）；保留字列名（group/key/order 等）用 model.QuoteIdent 包裹。表结构变更在 migrateDB 加幂等钩子：改列类型先 Migrator 探测再按方言 ALTER，数据回填用幂等 UPDATE 放 AutoMigrate 之后。细节见 [hr-backend/CLAUDE.md](hr-backend/CLAUDE.md)。

雪花 ID 与前端精度坑。主键用雪花 ID（应用层生成，int64），GORM 全局 Create 回调为任何带 `ID int64` 且为 0 的模型透明赋值，新模型零配置即用。雪花值超 2^53 过 JS 安全整数上限，凡承载雪花 ID 的字段（主键 ID、外键如 account_id、service DTO 转运字段）json tag 一律带 `,string`，domain 与 DTO 是两条独立序列化路径须双层覆盖，前端类型用 string；漏打会让 JSON.parse 低位归零、ID 指错对象。account 链路（domain.Account 与 service.AccountDTO 双层 string 化）是样板，细则见 [AGENTS_DATABASE_API_RULE.md](AGENTS_DATABASE_API_RULE.md) 1.2。

前端鉴权与文案约定。鉴权态用 Zustand persist 落 localStorage（key `sili-smart-hr-auth`），`_authenticated` 布局在 beforeLoad 本地校验 token 过期；401 或 HTTP 200 + code 1003 都触发登出（清 token、清 query 缓存、跳 /login）。可见文案走 i18next，命名空间 common（默认）/ auth / account，zh.json 与 en.json 同步维护。前端不硬编码后端地址，dev 走 rsbuild proxy，生产走 Nginx 同域反代。细节见 [hr-frontend/CLAUDE.md](hr-frontend/CLAUDE.md)。

注释密度。函数 doc 注释不超过 4 行，只写非显然的约定与坑位；禁止复述代码行为、罗列错误分类矩阵或把 specs 依据整段搬进注释，同一函数 specs 引用最多 1 处。字段级行内注释一行一个语义是合理密度，膨胀的 doc 注释按此精简。

规约文档随实现同步。模块从占位变实现、能力清单、路由或错误码集合变化时，同一变更集必须同步更新对应 CLAUDE.md（根文件与前后端子文件）的状态描述；已被代码证伪的陈述（如仍写着占位、路由清单缺新接口）会误导后续会话的改动决策。
