# 会话特征抽取 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_TECH_003_TECH_会话特征抽取 |
| 模块代号 | TECH（技术组件，engine/extractor 子域） |
| 文档版本 | v1.5 |
| 创建日期 | 2026-08-23 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计范围声明

本组件是后端进程内 Go 技术组件（specs §1.4），**无本系统对外 HTTP 接口**：Gin 列为技术栈仅因复用进程内 HTTP server 生命周期，组件自身不注册任何路由。本文档承载的是 dev_plan 依赖的三类内部契约：

| 契约类型 | 承载方式 | 消费方 |
|---------|---------|--------|
| Asynq 异步任务契约 | 任务类型常量 + payload schema + handler 行为约定 | worker 调度（T6 跑批复用） |
| 组件方法契约 | Extractor 公开方法签名与语义 | worker handler、测试、T6 编排 |
| 仓储与参数读取契约 | Repository 接口 + 参数键约定 | Wire 装配、T5 取数 |

与前置 TECH feature 的差异（它们按「无 HTTP 无表」跳过 interface 阶段）：本 Feature 有新增数据库表（session_features、system_params，见 [04_model_interface.md](04_model_interface.md)），跳过会让 dev_plan 缺少表结构与仓储契约依据，故 interface 阶段正常执行，本文档即为设计产出。

---

## 2. 模块信息

**代码落位：** `hr-backend/internal/engine/extractor/`（替换 doc.go 占位，specs §4.1）

**依赖方向（specs §4.2-4.3）：**

```
worker/task（Asynq handler）
    └─ extractor.Extractor
         ├─ llm.Client            （P2_TECH_001 底座，唯一 LLM 通道）
         ├─ conversationlog.Client（P2_TECH_002 集成，唯一详情拉取通道）
         ├─ SessionFeatureRepository（本 Feature 新增）
         ├─ SystemParamReader     （本 Feature 新增，读 system_params 表）
         └─ SecretProvider        （func(ctx) (string, error)，装配层从
                                    IntegrationSecretRepository 取密文解密构造，
                                    供 ExtractByKey 拉详情的 Bearer 鉴权）
```

**Wire 装配：** `extractor.New(llmClient, clClient, sessionFeatureRepo, sysParamReader, secretProvider)` 挂入既有 repository → service → handler → router 装配链（新增 provider 后 `go generate ./...` 再生 wire_gen.go）。装配的 llmClient 须以 60s 量级 Timeout 构造（覆盖 P2_TECH_001 默认 10min，见 §3.3 超时推导的前提）。

> SecretProvider 注入点说明：ExtractByKey 内部经 P2_TECH_002 拉详情（specs §2.4 能力6），GetSessionDetail 需要 Bearer 集成密钥，四注入点装配无法支撑该路径，故契约定为五注入点；specs §2.5/§4.2 示例为四参概念形，以本节为准（dev_plan 子计划 03 T12 同款五参装配）。

---

## 3. Asynq 异步任务契约

### 3.1 任务类型

```go
// worker/task 内注册，任务粒度一会话一任务（specs §2.4 能力6）。
const TypeSessionExtract = "engine:session-extract"
```

**需求追溯：** specs §2.1 能力6（批量抽取编排入口）、§2.5 使用示例。

### 3.2 任务 payload

```json
{
  "session_key": "3f7c2a90-1e5b-4c8a-9d2f-6a8b7c5d4e3f",
  "token_name": "张三"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| session_key | string | 是 | 会话标识，须来自 P2_TECH_002 列表接口返回值；空值 handler 记 ERROR 后返回 nil 丢弃任务（构造侧确定性错误，重试恒失败） |
| token_name | string | 是 | 人员归属（上游调用令牌名），落库回填用；空值同上 |

payload 由 T6 跑批编排构造入队（本 Feature 只定义 schema 与消费行为，入队方不在本期）。

### 3.3 handler 行为约定

```
输入:  payload{session_key, token_name}
流程:  ext.ExtractByKey(ctx, sessionKey, tokenName)
返回:  err == nil            → 任务成功（含 Skipped=true 与 Status=failed 的会话级终态，
                              二者均已落行或按规格免落，不重试）
       err != nil            → 交 Asynq 任务级重试（ErrStoreWrite 落库失败等基础设施错误；
                              specs §2.3 错误码表：重试耗尽本周期该会话无档案行，
                              属 extract_fail_ratio 分子的已知漏计项）
超时:  handler ctx 预算 ≥ 440s（specs §2.3 已修订：详情拉取 120s（30s × 4 次尝试）
       + 拉取退避 70s（10s+20s+40s）+ LLM 段 250s（底座装配 MaxRetries=1，schema 校验
       失败后的一次组件级重试共 2 次完整调用，每次最坏 60s 超时 + 5s 底座退避 + 60s
       重试超时 = 125s）= 440s；此前 310s 口径按底座零重试计偏小，会在底座退避重试
       叠 schema 重试的慢路径提前触达 deadline 产生伪失败），由底座承载，
       Asynq 任务侧不再叠加更短超时。成立前提：底座 client 的 cfg.Timeout 须
       装配为 60s 量级（P2_TECH_001 默认 10min，本组件装配时显式覆盖；
       dev_plan 须落该配置项），否则 LLM 慢路径单次 600s 级会先触 deadline
       产生伪失败
并发:  由 config.Asynq.Concurrency 承载，组件自身不加锁（specs §2.4 能力6）
日志:  只输出 session_key、token_name、计数与错误码，禁止输出消息文本与
       裁剪视图内容（specs §6.1 隐私口径）
```

任务注册落位 `worker/task`（`HandleFunc` 范式与既有 HealthCheck 一致），真实跑批 cron 注册归 T6。

---

## 4. 组件方法契约

### 4.1 方法清单

| 方法 | 签名（概念形） | 语义 | 需求追溯 |
|------|--------------|------|---------|
| Extract | (key, detail, tokenName) → (*ExtractionResult, error) | 单会话全流程：过滤判定 → 裁剪与统计 → LLM → 落库（详情由调用方传入） | specs §2.3/§2.4 能力2 |
| ExtractByKey | (sessionKey, tokenName) → (*ExtractionResult, error) | worker 主路径：内部经 P2_TECH_002 拉详情后走 Extract 全流程 | specs §2.4 能力6 |
| Trim | (detail) → (TrimmedView, TrimStats) | 只裁剪，不调 LLM 不落库，供调试与测试 | specs §2.4 能力1 |
| ShouldSkip | (detail, minUser, minKept) → (bool, SkipReason) | 短会话过滤纯规则判定（三参实现形：minUser/minKept 阈值入参供测试锚定空区间分支，主链调用传常量 MinUserMessages/MinKeptMessages，specs §2.4 能力4 的单参为概念签名） | specs §2.4 能力4 |
| Redact | (text, patterns) → string | 正则脱敏纯函数（包级函数；双参实现形：patterns 为注入正则集，空集或 nil 回退出厂 RedactPatterns，specs §2.4 能力5 的单参为概念签名） | specs §2.4 能力5 |

概念签名省略 ctx；`Extract/ExtractByKey` 的 ctx 超时语义见 §3.3。返回结构 `ExtractionResult{Skipped, SkipReason, Profile, Reused, Status}` 字段语义以 specs §2.3 为准（Status 值域 success/failed/skipped；Reused=true 覆盖 success 复用与 skipped 终态复用两种形态，Status 如实回传既有行状态）。

### 4.2 错误返回语义（与会话级终态的边界）

组件错误（error 返回）与会话级终态（err=nil + Result 字段）是两条互斥通道，handler 据此决定是否重试：

| 场景 | error | Result | 落库行为 | handler 处置 |
|------|-------|--------|---------|-------------|
| 抽取成功 | nil | Skipped=false, Status=success | 四块档案行 | 成功，不重试 |
| 命中成功即跳过 | nil | Reused=true, Status=success | 复用既有 success 行 | 成功，不重试 |
| 既有 skipped 行复用 | nil | Reused=true, Status=skipped, SkipReason=既有行 error_code | 终态复用（不重拉不重判） | 成功，不重试 |
| LLM 失败降级 | nil | Skipped=false, Status=failed | 统计块 + error_code 行 | 成功（降级是终态设计），不重试 |
| 短会话过滤 | nil | Skipped=true, SkipReason=... | skipped 元数据行（终态） | 成功，不重试 |
| 上游业务空（ErrNotFound 双通道） | nil | Skipped=false, Reused=false, Status=空串, 无 Profile | 不落行，INFO 日志（既有 failed 残留行终态化为 skipped，reason=session_not_found，元数据列复用既有行原值）；空串不属三态值域，消费方按字段组合识别业务空 | 成功，不重试 |
| 详情拉取确定性失败（鉴权/参数/信封契约） | nil | Skipped=false, Status=failed | failed 元数据行（error_code=ErrDetailFetch，Stats 零值；密钥修复后补跑重抽翻转） | 成功，不重试（重试恒失败烧预算） |
| 详情缺 session/messages 空 | nil | Skipped=true, SkipReason=detail_invalid | skipped 元数据行（终态） | 成功，不重试 |
| 落库失败（ErrStoreWrite） | 非 nil | 无效 | 不落行 | 返回 err 交 Asynq 重试 |

错误码内部载体（specs §2.3 错误码表）：ErrLLMUpstream、ErrSchemaInvalid、ErrDetailFetch、ErrContextLengthExceeded、ErrNotFound、ErrDetailInvalid、ErrEmptyShell、ErrStoreWrite。注意 ErrEmptyShell/ErrDetailInvalid 是落库 status/skip_reason 值的书面载体，运行时以 Result 字段表达，error 通道不返回。

---

## 5. 仓储契约

### 5.1 SessionFeatureRepository

落位 `internal/repository/session_feature.go`，接口定义包内（account 域双重接口范式），实现注入 fake 供测试。

```go
// Save 幂等落库：已存在 success 行返回 reused=true 不覆盖；failed 允许原地翻转 success；
// skipped 行为终态（调用方按 specs §2.4 能力3 状态机决定是否调用，仓储对 skipped 行
// 的再次写入返回 reused=true）。唯一索引 uk_session_key 兜底并发双写（冲突转重查收敛）；
// failed 行翻转的 UPDATE 带 id AND status 双条件，RowsAffected=0 重查按库内实际行收敛
//（防并发覆写已终态行，见 04 文档 §3.1 翻转 UPDATE 的并发防御）。
Save(rec *domain.SessionFeature) (reused bool, err error)

// ListByPersonAndRange 按人加时间窗取档案（T5 evaluator 取数口径）。
// start/end 为 Unix 秒，仓储内转 time.Time 走 idx_token_first_turn 组合索引；
// 返回全部三态行（success/failed/skipped，skipped 行 profile_json 为空串），
// T5 按 status 分布做人群签名识别（specs §2.4 能力3、§7.2 C08-C12）。
ListByPersonAndRange(ctx, tokenName string, start, end int64) ([]domain.SessionFeature, error)
```

表结构、状态机与降级口径见 [04_model_interface.md](04_model_interface.md) §3.1。

### 5.2 SystemParamReader

落位 `internal/repository/system_param.go`（或复用 SYS 域既有文件），为共享 SYS 能力的读取接口：

```go
// ReadStringArray 按键读字符串数组参数。行不存在或值反序列化失败时返回包内
// 出厂默认（由调用方以 extractor 包常量注入），DB 非唯一权威（04 文档 §3.2 业务规则）。
ReadStringArray(key string) ([]string, error)

// ReadStringArrays 批量按键读（单次 DB 往返），逐键回退与损坏口径同上；
// 键缺失时该键在返回 map 中缺席（调用方回退），error 仅在真实 DB 故障时非 nil。
ReadStringArrays(keys ...string) (map[string][]string, error)
```

本 Feature 消费两个参数键，值格式均为 JSON 字符串数组：

| 参数键 | 用途 | 出厂默认 | 需求追溯 |
|--------|------|---------|---------|
| extractor.inject_prefixes | 注入识别前缀黑名单 | specs §2.2 InjectPrefixes 注释收录的形态全集（前缀字面） | specs §2.4 能力1、§6.4 问题1 |
| extractor.redact_patterns | 脱敏正则集（路径/密钥/内网地址） | specs §2.2 RedactPatterns | specs §2.4 能力5 |

读取时机为每会话任务一次（两键批量单次往返），参数页改动在新一轮抽取生效（specs §5.2 参数联动用例）。

---

## 6. 与系统参数域的边界

system_params 表结构与本节读取契约随本 Feature 落地（specs §4.3 标注该 SYS 能力待补齐，无它则两参数键无处承载）。参数页管理 HTTP 接口（列表/编辑/乐观锁冲突错误码）与界面归属系统参数域后续变更 Feature，本 Feature 无 HTTP 面，不消费管理接口。管理接口落地时沿用规则文件 §2 的统一响应、错误码与 Bearer JWT 约定，不改变本节读取契约。

---

## 7. HTTP 接口与安全声明

- **无对外 HTTP 接口**：本组件不注册路由、不进受保护路由组，规则文件 §2 的响应格式、分页参数、HTTP 错误码映射对本组件无适用对象；后续 T5/T6 的档案查询与跑批管理接口归属各自 Feature 的 interface 设计。
- **认证**：任务链路无 HTTP 认证面；对上游调用经 P2_TECH_001/002 既有 Bearer 集成密钥通道。
- **传输与存储安全（specs §3.3 全量承接）**：对话原文与裁剪视图仅内存临时持有，禁日志禁缓存禁落库；prompt 组装前剥离 TokenName（人名不进 LLM 上下文）；用户真实输入进视图前过 RedactPatterns 输入侧脱敏（敏感串不进 LLM 上下文）；档案落库前过输出侧脱敏，主库只见 [PATH]/[SECRET]/[ADDR] 占位符。
- **日志规范**：DEBUG 裁剪统计/INFO 跳过与复用/WARN schema 重试与截断/ERROR LLM 与落库失败，字段仅 session_key、token_name、计数、错误码（specs §6.1）。
- **监控指标**（T6 聚合、F11 呈现，本 Feature 只定义口径）：extract_fail_ratio（分子 status=failed 计数、分母周期会话总量列表口径，≥10% 告警）、skip_ratio（观测）、inject_residual_ratio（连续两周期抬升提示黑名单失效）、extract_duration（p99>120s 观测线）。

---

## 8. SSOT 合规与一致性

- [x] 组件接口集合与 specs §2.3 输出定义一一对应（Extract/ExtractByKey/Trim/ShouldSkip/Redact）。实现形差异已在 §4.1 逐条声明：ShouldSkip 三参（阈值入参供测试锚定）、Redact 双参（注入正则集）、InjectPrefixes/RedactPatterns 函数形态返回拷贝（specs §2.2 为包级导出变量；私有 factory 切片加拷贝函数防外部写穿透，seed 与读参回退共用同源）；概念签名省略的 ctx 在仓储契约（§5.1）与本节 ListByPersonAndRange 已补齐。
- [x] 能力覆盖：六个能力（裁剪/抽取/落库/过滤/脱敏/编排入口）在任务契约与方法契约中均有承载。
- [x] 错误码值域与会话级终态边界对齐 specs §2.3 错误码表（六错误码 + Skipped 字段通道）。
- [x] 隐私与安全约束（specs §3.3）在 §7 全量承接。
- [x] 无 HTTP 接口与 specs §1.4（进程内组件）一致，文档已声明设计范围避免误读为缺漏。
- [x] 仓储契约与 [04_model_interface.md](04_model_interface.md) 表结构一一对应（状态机、索引路径、Unix 秒转换口径）。

---

## 9. 不涉及的设计

- 跑批周期调度、翻页去重、逐人聚合、失败重试编排（T6 pipeline Feature）。
- T5 消费侧的档案查询 HTTP 接口（evaluator 进程内直连仓储，无接口面；画像/看板展示接口归属各业务域）。
- 参数页管理接口与界面（系统参数域后续变更 Feature，见 §6）。

---

**文档版本：** v1.5
**最后更新：** 2026-08-27
**作者：** lixuetao
