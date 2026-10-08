# 操作日志 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P4_LOG_001_FEAT_操作日志 |
| 模块代号 | LOG（操作日志域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-07 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、权限、口径），技术实现层面以规则文件为准（方法约定、响应结构、错误码段位、雪花 ID string 化）。冲突与技术层补充逐条在 §1.2-1.11 声明。

### 1.2 HTTP 方法归一化：本域全 GET

specs 未指定 HTTP 方法。本域 HTTP 面只有两个查询接口（列表、导出），全部 GET（规则文件 §2.1 GET 仅查询）。导出虽产出文件，语义是按当前筛选条件的读取物化（specs §4.1.4 规则 4），无服务端状态变更，归 GET，与 F9 画像导出先例同款。记录通道（specs §5.1）是中间件内部机制、批次节点（§5.2）是 worker 侧调用、清理（§5.5）是 scheduler 定时任务，三者均无 HTTP 面，契约见 §4。

### 1.3 记录通道的挂载形态（技术层裁决）

specs §5.1.2 描述中间件「挂载于 JWT 之后，登录接口单独挂载」。结合代码现状（router.go 全局中间件仅 Recovery → Logger → CORS，受保护组 `r.Group("/api", middleware.JWT(jwtMgr))`，登录走公开路由）裁定：

- **受保护组**：记录中间件挂在 auth 组内、JWT 之后（`r.Group("/api", middleware.JWT(jwtMgr), middleware.OperationLog(recorder))` 形态，或组内逐路由前置），对全部受保护 POST 接口生效；GET 接口由中间件方法过滤跳过。
- **登录接口**：POST /api/login 在公开路由组单独挂记录中间件（无 JWT 上下文，操作人取请求 username 原值，specs §5.1.3）。
- **setup 与 answer 接口不记录**：/api/setup/initialize 是首账号向导（系统无账号前的引导行为，记录无操作人语义）；/api/answer/* 是员工作答令牌域（非平台账号操作，specs §2.1 权限矩阵只覆盖平台账号）。specs §4.1.4 规则 2 的口径是「平台账号全部写接口」，作答提交是员工行为而非平台账号行为，不属记录范围。此边界为 specs 未明说处的补充裁定。
- **写方法口径**：当前受保护组全部写接口均为 POST（router.go 无 PUT/DELETE），specs §8.1 术语表把写接口定义为「受保护路由组下的 POST 请求」，中间件按 POST 方法过滤即可，与现状一致；后续新增 PUT/DELETE 时中间件同步扩展（方法集合收敛在中间件常量）。

### 1.4 操作人姓名的取值口径

JWT claims 只有 account_id 与 username，无姓名（jwt.go 注入键仅此两个）。规格要求操作人显示姓名（specs §4.1.2 B），且账号删除后日志仍可读（specs §5.3.3 不回查账号表）。裁定：**记录中间件在落库时按 account_id 现查 accounts 表取 name 冗余落行**（软删行 Unscoped 取，删号后历史行已落姓名不受影响）；查不到（理论上仅并发删号窗口）回退 username。系统任务行操作人固定常量「系统」（specs §5.2.2）。现查发生在异步落库路径，不阻塞业务响应（specs §5.1.2 步骤 5）。

### 1.5 业务结果的判定口径

specs §5.1.3 规定以响应 code 是否为 0 判定 success/fail。实现裁决：记录中间件在业务 handler 执行后读取响应 body 的 code 字段（统一响应结构 JSON 解析，仅解析 code 一个字段）。业务错误摘要取 message 字段；参数校验失败（binding 失败直接 c.JSON 400 形态或 code=1400）同记 fail 并摘错误信息（specs §4.1.4 规则 2）。HTTP 层异常（panic 被 Recovery 接住、HTTP 500）由中间件在响应后按状态码非 200 记 fail，摘要记「服务内部错误」。

### 1.6 登录失败的反枚举口径

登录失败四路径（账号不存在、RSA 解密失败、密码错误、账号禁用）统一摘要「凭证校验未通过」（specs §4.1.4 规则 2 明文），操作人记请求 username 原值，不区分失败原因，与账号域反枚举设计一致。登录成功正常记 success。

### 1.7 变更对比的 JSON 结构

specs §4.2.2 定义 changes 结构为字段级 before/after。裁定统一结构：

```json
[{"field": "权重", "before": "30", "after": "50"}]
```

- field 为业务字段中文名（埋点侧组装时已翻译，前端直接渲染三列表格）。
- before/after 为字符串化值（数字、布尔统一 fmt 转字符串）；新增/删除类操作不走 changes（specs §4.1.4 规则 3 约束说明：无变更前/后语义，统一文本摘要形态，detail 承载）；仅更新/启停类操作产生 changes。
- 序列化落 operation_logs.changes_json（TEXT，规则文件 §1.5 半结构化数据约定），查询接口原样透传数组，无 changes 数据（空串）时返回 null，前端据此互斥渲染（specs §4.2.5）。

### 1.8 时间格式与筛选边界

- 列表与详情的操作时间显示 `yyyy-MM-dd HH:mm:ss`（specs §4.1.2 B，通用规范 9 条秒级精度），DTO 层 time.Local().Format 直出。
- 时间范围参数 `start_date` / `end_date` 均为 `yyyy-MM-dd`：start_date 换算为当日 00:00:00 本地时区、end_date 换算为当日 23:59:59（specs §4.1.2 A 自然日边界收拢），查询条件为 `created_at >= start AND created_at <= end`（双闭区间，与 F6 批次域左闭右开口径不同——此处 specs 明文写死 23:59:59 收拢，从 specs）。
- 只传一端按非法参数 1400（起止成对约束，specs §5.3.2 步骤 2 校验时间范围起止顺序）。

### 1.9 详情弹窗零请求与行数据内嵌

specs §4.2.2 明文弹窗数据已随列表加载零二次请求。裁定：A1 列表行 DTO 内嵌详情所需全量字段（summary、changes、detail），行级 id 供导出与前端 key 使用。无独立详情接口。

### 1.10 导出文件名与截断备注

- 文件名 `操作日志_YYYYMMDD_HHmmss.xlsx`（specs §4.1.4 规则 4，时间为导出时刻，与 F9 的 YYYYMMDD 单日期格式不同，从 specs）。Content-Disposition 双轨：ASCII 兜底 `filename="operation-logs.xlsx"` + RFC 5987 `filename*=UTF-8''<中文名>`（F9 先例）。
- 超 10000 行截断并在**文件首行**备注实际导出行数（specs 明文「文件首行」，即第 1 行为备注行、第 2 行表头、数据从第 3 行起；未超限时无备注行，第 1 行直接表头）。

### 1.11 评估运营与题库域的 detail 埋点增强

specs §7.2 对评估运营与题库域写接口的口径是「路径级兜底记录」，但 specs §4.1.4 规则 3 要求这两类操作输出文本详情段落，纯兜底形态无 detail 数据，两处口径存在缝隙。裁定：这两个域的写接口按 §4.1 埋点清单做 detail 埋点增强（Service 注入文本详情），规格的兜底口径仍作为埋点缺失时的降级形态（specs §5.1.2 步骤 3 的兜底语义不变）。

---

## 2. 通用约定

### 2.1 基础配置

| 配置项 | 值 | 来源 |
|-------|------|------|
| Base URL 前缀 | `/api` | 规则文件 §2.1 |
| 版本控制 | 不分段，无 /v1 | 规则文件 §2.1 |
| 方法约定 | GET 仅查询（本域全 GET） | 规则文件 §2.1 + 本文 §1.2 |
| 认证方式 | Bearer JWT | 规则文件 §2.1 |
| 路由归属 | 受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))` | 规则文件 §2.6 |

本域两个接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号可见全部操作日志含他人记录（specs §2.1：操作日志全员可见）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 不单独限流，沿用受保护路由组既有策略。查询为页面加载与手动触发，无轮询（specs §4.1.3 前端自动流程仅页面加载一次）；导出按钮带 loading 防重（specs §4.1.3 + 通用规范 23 条）。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`；分页结构作 `data` 透传，四字段 `list` / `total` / `page` / `page_size`。唯一例外是 A2 导出的二进制流（§1.10、F9 §1.6 先例）。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500、批次 16xx、题库 17xx、主动测试 18xx、员工作答 19xx、个人画像 20xx、团队看板 21xx。操作日志域分配 **22xx 段**（代码事实核对：21xx 为当前最大段，22xx 起空闲），本期无新增码（§5）。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

日志主键 `id` 为雪花 ID：domain 模型与 service DTO 双层 `json:"id,string"`（规则文件 §1.2，account 链路样板）。本表无外键转运字段（操作人姓名冗余为字符串，account_id 仅落库不回显 HTTP）。

### 2.5 时间与日期格式

- 操作时间（`created_at`）：`yyyy-MM-dd HH:mm:ss` 本地时区直出（specs §4.1.2 B）。
- 筛选时间范围（`start_date` / `end_date`）：`yyyy-MM-dd`，双闭区间收拢（§1.8）。

### 2.6 隐私与安全边界

- 密码（含密文）、API Key、集成密钥明文、评分理由原文不入日志任何字段（specs §5.1.4 规则 4）；密钥类操作的对象描述用脱敏掩码（llm_configs.api_key_masked 同款掩码快照）。
- 登录失败摘要统一「凭证校验未通过」，不落失败原因分类（§1.6）。
- 查询与导出均为只读行为，本身不记操作日志（specs §4.1.3 / §5.3.4 规则 1 / §5.4.4 规则 1）。
- 请求体不整体落日志：埋点注入的摘要与对比结构由 Service 显式组装，路径级兜底只落「接口路径 + 动作」与请求概述（方法与路径，不含 body），杜绝敏感字段经兜底路径泄露。

---

## 3. 接口列表

共 2 个新接口，按页面组织：操作日志页（A1 列表、A2 导出）。详情弹窗零请求（§1.9），无接口。无复用接口。

需求追溯编号对应 specs 第 4/5 章功能（F12 为 PRD 5.2.2 模块编号）。

### A. 操作日志页

#### A1. 查询操作日志列表

**接口路径：** `GET /api/operation-logs`

**需求追溯：** [需求：F12-4.1.2A] [需求：F12-4.1.2B] [需求：F12-4.1.3 条件查询/条件重置/详情查看/页面数据自动加载] [需求：F12-4.1.4 规则1/2/3/5] [需求：F12-4.1.5] [需求：F12-5.3]

**说明：** 分页查询操作日志，四维筛选（操作人模糊、操作类型精确、操作结果精确、时间范围），按操作时间倒序（specs §4.1.4 规则 5，操作时间为唯一排序键，同秒内次序由雪花 ID 落库序隐性承载）。行数据内嵌详情弹窗所需全量字段（§1.9）。操作人模糊匹配经 likeescape.EscapeLike 转义 + `ESCAPE '\'` 子句（specs §5.3.4 规则 2，规则文件 §1.11），「系统」任务行同样可检。空条件即全量倒序。分页宽松超集：任意 1-100 整数均接受（>100 钳位 100），前端选择器提供 10/20/30（specs §8.3 沿用通用规范 1 条）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| operator | string | 否 | 空 | 操作人姓名模糊匹配（含「系统」）；前端去首尾空格后提交（通用规范 4 条） |
| module | string | 否 | 空（全部类型） | 操作类型精确匹配 [可选值：login/account/dimension/system_params/llm_config/question_bank/assessment/system_job]（specs §4.1.4 规则 1 八类枚举），非法值 1400 |
| result | string | 否 | 空（全部） | 操作结果精确匹配 [可选值：success/fail]，非法值 1400 |
| start_date | string | 条件必填 | 空 | 时间范围起始日 `yyyy-MM-dd`（含，换算当日 00:00:00 本地时区，§1.8）；与 end_date 成对出现 |
| end_date | string | 条件必填 | 空 | 时间范围止日 `yyyy-MM-dd`（含，换算当日 23:59:59 本地时区）；只传一端、格式非法或 start > end 均 1400 |
| page | integer | 否 | 1 | 页码，从 1 开始 |
| page_size | integer | 否 | 10 | 每页数量 [可选值：10/20/30]（specs §8.3）。实现为宽松超集：任意 1-100 整数均接受（>100 钳位 100） |

**请求示例：**

```
GET /api/operation-logs?operator=张&module=dimension&result=success&start_date=2026-09-01&end_date=2026-09-30&page=1&page_size=10
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1790000000000001001",
        "created_at": "2026-10-07 14:23:05",
        "operator": "李学涛",
        "module": "dimension",
        "target": "维度「任务适配判断力」聚合权重",
        "summary": "调整维度聚合权重 30 → 50",
        "result": "success",
        "changes": [
          { "field": "聚合权重", "before": "30", "after": "50" }
        ],
        "detail": ""
      },
      {
        "id": "1790000000000001002",
        "created_at": "2026-10-07 14:20:11",
        "operator": "系统",
        "module": "system_job",
        "target": "批次 B20260928S01（2026-09-22 ~ 2026-09-28）",
        "summary": "区间执行完成：成功 96 人，失败 4 人",
        "result": "success",
        "changes": null,
        "detail": "周期批次区间执行完成，终态 partial_failed。成功侧 96 人（success 90、reused 4、degraded 2），失败 4 人，会话级失败比例 3.25%。"
      },
      {
        "id": "1790000000000001003",
        "created_at": "2026-10-07 14:15:42",
        "operator": "admin",
        "module": "login",
        "target": "登录",
        "summary": "凭证校验未通过",
        "result": "fail",
        "changes": null,
        "detail": "登录失败：凭证校验未通过"
      }
    ],
    "total": 3,
    "page": 1,
    "page_size": 10
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 日志主键（雪花 ID，string 化，§2.4）；前端行 key 与导出定位用 |
| created_at | string | 操作时间（yyyy-MM-dd HH:mm:ss 本地时区直出，specs §4.1.2 B） |
| operator | string | 操作执行者姓名或「系统」（落库时冗余，账号删除后日志仍可读，specs §5.3.3）；登录失败行为请求 username 原值（§1.6） |
| module | string | 操作类型 [可选值：login/account/dimension/system_params/llm_config/question_bank/assessment/system_job]（specs §4.1.4 规则 1 八类）；标签配色与中文名映射由前端 i18n 承载（specs §4.1.5） |
| target | string | 操作对象描述：埋点路径为业务对象描述（如「维度「任务适配判断力」聚合权重」），兜底路径为「POST /api/dimensions/update」形态的路径级描述（specs §4.1.2 B、§8.1 路径级语义） |
| summary | string | 一句话操作摘要；失败记录为业务错误摘要（specs §4.1.4 规则 2）；过长截断悬浮由前端渲染（通用规范 11 条） |
| result | string | 操作结果 [可选值：success/fail]，落库时一次性判定无转换（specs 第 6 章） |
| changes | array \| null | 字段级变更对比（§1.7 结构：field/before/after 三键，field 为业务字段中文名）；无对比数据为 null，前端整区不渲染（specs §4.2.5 互斥渲染） |
| changes[].field | string | 变更字段中文名 |
| changes[].before | string | 变更前值（字符串化） |
| changes[].after | string | 变更后值（字符串化） |
| detail | string | 文本详情段落（登录、评估运营、题库管理、系统任务类操作，specs §4.1.4 规则 3）；有关键域埋点 changes 的行为空串 |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（module/result 非枚举值、时间范围只传一端、日期格式非法、start > end） |
| 1500 | 服务内部错误 |

---

#### A2. 导出操作日志

**接口路径：** `GET /api/operation-logs/export`

**需求追溯：** [需求：F12-4.1.3 日志导出] [需求：F12-4.1.4 规则4] [需求：F12-5.4]

**说明：** 按当前筛选条件导出 xlsx，查询链路与 A1 完全复用（透传筛选、跳过分页、排序同操作时间倒序，specs §5.4.2 步骤 1）。六列与列表显示字段一致（操作时间、操作人、操作类型中文名、操作对象、详情摘要、结果中文名），不含行内操作列与 changes/detail 明细（specs §4.1.4 规则 4）。单次导出上限 10000 行，超限截断并在文件首行备注实际导出行数（§1.10）。成功返回二进制流旁路统一响应结构，失败返回统一 JSON 错误结构（前端按 Content-Type 判别，F9 先例）。导出为只读行为不记日志（specs §5.4.4 规则 1）。

**查询参数：** 同 A1（operator / module / result / start_date / end_date），无分页参数。

**请求示例：**

```
GET /api/operation-logs/export?module=dimension&start_date=2026-09-01&end_date=2026-09-30
```

**成功响应：**

- `Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`
- `Content-Disposition: attachment; filename="operation-logs.xlsx"; filename*=UTF-8''%E6%93%8D%E4%BD%9C%E6%97%A5%E5%BF%97_20261007_142305.xlsx`
- Body 为 xlsx 二进制流。表头六列：操作时间 / 操作人 / 操作类型 / 操作对象 / 详情摘要 / 结果。操作类型导出八类中文名（后端导出侧维护枚举中文名常量，与前端 i18n 措辞评审时核对同步，F9 enneagramTypeNames 同模式）；结果导出「成功/失败」。行序与 A1 同为操作时间倒序；命中行数 > 10000 时只导前 10000 行，第 1 行为「实际导出 N 行（命中 M 行，超出截断）」备注行、第 2 行起表头；未超限时第 1 行直接表头。

**失败响应：** 统一 JSON 错误结构（HTTP 200 带 code），错误码同 A1（1400 / 1500）。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（同 A1） |
| 1500 | 服务内部错误（含 xlsx 生成失败，specs §5.4.5） |

---

## 4. 非页面功能契约

### 4.1 记录通道（中间件兜底 + 关键域埋点）

specs §5.1 的完整契约，无 HTTP 面。实现形态：

**中间件（兜底）：**

1. 挂载位：受保护组 JWT 之后 + 登录接口单独挂载（§1.3）。auth 组内仅 POST 方法进入记录路径，GET 与其他方法直接放行。
2. 透传业务 handler 执行，响应完成后组装：从 JWT 上下文取 account_id（登录接口取请求 username）、从响应 body 解析 code（§1.5）、从 gin.Context 约定键读埋点数据。
3. 埋点数据缺失时降级路径级语义：module 按路径前缀推断业务域（/api/accounts → account、/api/dimensions → dimension、/api/assessment-config 与 /api/llm-configs 与 /api/integration-secret → 按接口归 system_params / llm_config、/api/questions 等 → question_bank、/api/assessment → assessment），无法推断的归 system_params；target 为「POST {path}」，summary 为「{业务域中文}接口调用：{success|fail}」。
4. 异步落库：带缓冲通道（容量 1024），后台单 goroutine 消费批量落库；通道满丢弃最旧记录并 WARN（specs §5.1.5 异常表第三行）；落库失败丢弃该条记 ERROR（specs §5.1.5 第一行）。进程优雅关闭时 flush 剩余记录（沿用 main.go 生命周期编排，新增组件仿 fatalErr 接入点）。操作人姓名现查（§1.4）。

**gin.Context 埋点约定键（Service 埋点注入面）：**

| 键 | 类型 | 语义 |
|----|------|------|
| oplog.module | string | 操作类型（八类枚举值） |
| oplog.target | string | 操作对象业务描述 |
| oplog.summary | string | 操作摘要（成功路径） |
| oplog.detail | string | 文本详情（评估运营/题库/登录/系统任务类） |
| oplog.changes | []ChangeItem | 字段级变更对比（§1.7 结构，关键域） |

埋点在 Service 写操作成功路径注入；失败路径中间件以响应 message 作 summary，不注入（specs §5.1.2 步骤 3）。数据仅在单请求生命周期内有效（specs §5.1.4 规则 3）。

**关键域埋点清单（specs §4.1.4 规则 3 + §7.2；新增/删除类走 detail 文本形态，仅更新/启停类产生 changes）：**

| 域 | module | 埋点接口 | 变更对比 |
|----|--------|---------|---------|
| 维度与权重 | dimension | dimensions/create、update、delete、activity-rule/save | update 与 activity-rule/save 是（写前读旧值写后取新值，字段级）；create/delete 否（detail 文本） |
| 系统参数 | system_params | assessment-config/save | 是 |
| 大模型配置 | llm_config | llm-configs/create、update、delete、enable | update/enable 是（api_key 类字段以掩码呈现，永不落明文）；create/delete 否（detail 文本） |
| 集成密钥 | llm_config | integration-secret/update、test | update 是（密钥值以掩码呈现）；test 验证无变更语义，仅注入对象与摘要（specs §7.2 更新与验证均要求注入） |
| 账号台账 | account | accounts/create、update、delete、toggle-enabled、reset-password | update/toggle-enabled 是（密码类字段不进对比表）；create/delete/reset-password 否（detail 文本，密码值永不落日志） |
| 评估运营 | assessment | batches/create、test-tasks/create、resend、cancel 等 | 否（文本详情） |
| 题库管理 | question_bank | questions/update、toggle-status、delete、resubmit、question-batches/:id/confirm、void、scales/import、question-generations/create、cancel 等 | 否（文本详情） |

未列表的受保护 POST 接口（如 system/health-check 测试探测）走中间件路径级兜底，保证零漏记（specs §1.2 业务目标 3）。

**需求追溯：** specs §5.1 全节。

### 4.2 系统任务批次级节点记录（worker 侧）

specs §5.2 的五类节点，操作人固定「系统」，module=system_job。写入方为 worker 任务处理器在节点完成处调用日志服务（同步落库或旁路，实现期定；写入失败跳过不阻断主任务，specs §5.2.5）：

| 节点 | 写入时机（上游 Feature） | 操作对象 | 摘要示例 |
|------|----------------------|---------|---------|
| 批次创建完成 | batch-tick / batches/create 建批后（P2_ASM_001） | 批次号 + 区间 | 周期批次创建完成，对象全员 |
| 批次区间执行终态 | batch-run 终态推进处（P2_ASM_001） | 批次号 + 区间 | 区间执行完成：成功 96 人，失败 4 人 |
| 失败超阈告警写入 | fallback AlertWriter（P2_ASM_001） | 批次号 + 区间 | 失败人数占比 12.50% 超阈值 10%，告警已写入 |
| 测试任务自动逾期取消 | test-expire-tick 推进处（P2_TST_001） | 任务号 | 任务自动逾期取消 |
| 建议生成完成或失败 | suggest-generate 终态处（P2_TMD_001） | 批次号 + 区间 | 培训建议生成完成 / 生成失败：{原因} |

结果按节点成败落 success/fail；告警写入属失败类节点（specs §5.2.3 说明列）。过程细节（tick 判定、逐人评估、单会话抽取）不记录（specs §5.2.4 规则 1）。

**需求追溯：** specs §5.2 全节。

### 4.3 定期清理任务（worker 侧）

specs §5.5 契约，按 test_expire_tick 范式落地：

- 任务类型 `operation-log:clean`，default 队列，零 payload，任务级超时 300s。
- cron 每日一次低峰时点（`0 3 * * *`，scheduler.go 新增独立 cron 常量）。
- 行为：计算清理边界（now 减 180 天），按主键分批删除 created_at 早于边界的记录（单批 1000 行循环删至无行，避免长事务锁表，specs §5.5.2 步骤 2）；删除条数记 INFO；err 透传交 Asynq 调度重试，重复删除幂等无副作用（specs §5.5.4 规则 2）。
- 保留窗口 180 天为 service 包内常量（specs §5.5.1：固定常量不进系统参数页）。

**需求追溯：** specs §5.5 全节。

### 4.4 计算常量

| 常量 | 初值 | 说明 |
|------|------|------|
| retentionDays | 180 | 日志保留窗口（specs §5.5.1） |
| cleanBatchSize | 1000 | 清理单批删除行数（specs §5.5.4 规则 1 分批，单批大小实现期定的落点） |
| exportMaxRows | 10000 | 单次导出行数上限（specs §4.1.4 规则 4） |
| recordBufferSize | 1024 | 异步落库通道容量（超限丢最旧保业务，specs §5.1.5 第三行；QueueSize 与 LLM 底座同量级经验值） |

均随源码发版。

### 4.5 路由注册

auth 组末尾追加（Gin 静态路由优先，`/operation-logs` 与 `/operation-logs/export` 不冲突，profiles 先例）：

```
auth.GET("/operation-logs", operationLogHandler.List)
auth.GET("/operation-logs/export", operationLogHandler.Export)
```

---

## 5. 错误码

**本期零新增码**：22xx 段预留给操作日志域后续需求，当前空闲。

本域两接口的失败面只有参数校验与内部错误（查询无资源定位语义，无 not found 类场景：空结果返回空列表而非错误；导出生成失败属内部错误范畴，F9 画像导出同款走 1500），参数错误复用 1400、内部错误复用 1500 已全覆盖：

| 错误码 | 常量 | 含义 | 使用场景 |
|--------|------|------|---------|
| 1400 | BadRequest（既有） | 请求参数错误 | module/result 非枚举值、时间范围只传一端、日期格式非法、start > end |
| 1500 | Internal（既有） | 服务内部错误 | 查询失败（specs §5.3.5）、导出生成失败（specs §5.4.5，前端 toast 提示） |

---

## 6. 与相邻域的边界

- **对 P1_ACC_001（account）**：登录接口挂独立记录点（§1.6 反枚举口径）；账号台账五个写接口为关键域埋点（变更对比，密码除外）；操作人姓名按 account_id 现查 accounts 表冗余落行（§1.4）。
- **对 P2_DIM_001（dimension）**：维度增删改与活跃度阈值保存为埋点域（Service 注入对象与变更对比）；埋点实现落在 dimension service 内，本 feature 提供注入键与组装契约（§4.1）。
- **对 P2_SYS_001（config）**：评估周期保存（system_params）、大模型清单增删改与排他启用（llm_config）、集成密钥更新（llm_config，specs §4.1.4 规则 1 决策 14）为埋点域；密钥值以掩码进对比结构。
- **对 P2_QBN_001（questionbank）**：题目维护、批次审核、量表引入、AI 生成（question-generations 域）写接口为 detail 埋点增强（§1.11），module 归 question_bank（specs §4.1.4 规则 1 AI 生成归题库类），埋点缺失时回落路径级兜底。
- **对 P2_ASM_001 / P2_TST_001 / P2_TMD_001**：手动批次与测试任务写接口路径级兜底；批次级节点五类由 worker 侧写入（§4.2）；本域不调用其服务、不改其表。
- **对消费侧**：A1/A2 只读 operation_logs 表；清理任务只删本表数据。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1 列表页（条件查询、重置、导出、详情、自动加载）→ A1/A2 + 前端交互（重置为前端清参重查，无接口依赖）；4.2 详情弹窗 → 零请求（§1.9，行数据内嵌）；5.1 记录通道 → §4.1；5.2 批次节点 → §4.2；5.3 查询接口 → A1；5.4 导出接口 → A2；5.5 清理任务 → §4.3。
- [x] 字段定义与 specs 一致：4.1.2 查询/显示字段、4.2.2 弹窗字段在请求/响应结构一一对应；两处适配已声明（时间双闭区间 §1.8、行内嵌详情 §1.9）。
- [x] 业务规则在接口层落地：八类枚举校验（A1 module 参数）、四维筛选（operator 模糊转义）、导出上限与首行备注（§1.10）、排序倒序（A1）、记录范围三类口径（§4.1/§4.2）、导出不记日志（A2 说明）。
- [x] 状态定义与 specs 第 6 章一致：result 两态落库一次性判定无转换，无状态机接口面。
- [x] 权限规则一致：两接口挂 JWT 无角色差异（specs §2.2），系统任务行与人工行同可见。
- [x] 技术层偏差已声明：全 GET（§1.2）、挂载形态（§1.3）、操作人现查（§1.4）、结果判定（§1.5）、登录反枚举（§1.6）、changes 结构（§1.7）、时间口径（§1.8）、零请求详情（§1.9）、导出文件名（§1.10）、题库/评估 detail 埋点增强（§1.11）。
- [x] 接口与数据模型一致性：A1/A2 响应字段均为 operation_logs 列投影或落库期组装值（04 §3.1），无两文档不一致字段。

---

## 8. 不涉及的设计

- 独立详情查询接口：specs §4.2.2 弹窗零请求，行数据内嵌（§1.9）。
- 日志的修改、撤回、人工删除入口：specs 第 6 章追加型审计数据，仅定期清理物理删除。
- 任何既有业务表的 DDL 变更：埋点是 Service 层代码行为，零表结构变化（04 §7）。
- 来源 IP 与操作终端的采集：specs §4.2.1 易用性决策明确去除。
- 日志写入的 HTTP 触发接口：记录通道是中间件与 worker 内部机制，无外部写入面。
- 操作日志页的轮询：specs §4.1.3 前端自动流程仅页面加载一次。

---

**文档版本：** v1.0
**最后更新：** 2026-10-07
**作者：** lixuetao
