# 周期批量评估跑批编排 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_ASM_001_FEAT_周期批量评估跑批编排 |
| 模块代号 | ASM（评估运营域） |
| 文档版本 | v1.7（2026-09-13：§4.4 流程新增步骤6 抽取落库等待与「等待」参数段，原步骤6-8 顺延为 7-9，同步步骤引用；对应 specs v1.9） |
| 创建日期 | 2026-09-11 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、状态、权限），技术实现层面以规则文件为准（方法约定、表名、字段类型、响应结构）。冲突逐条在 §1.2-1.5 声明。

### 1.2 HTTP 方法归一化

specs 未指定 HTTP 方法。本设计按规则文件 §2.1 归一化：查询一律 GET，变更一律 POST 加动作路径（本功能仅一个变更接口 `/create`）。批次不提供取消、作废、删除操作（specs §6.2 说明），故无 delete/update 接口。

### 1.3 表名与路径前缀

规则文件 §1.9 沿用 GORM 默认 NamingPolicy 实体复数化，不强制模块前缀。表名用语义化复数（`assessment_batches`、`assessment_batch_persons`、`assessment_alerts`）；接口路径挂在 `/api/assessment/batches` 下，与既有 `/api/assessment-config`（配置域，P2_SYS_001）区分：前者是运营操作面，后者是配置面。

### 1.4 定时触发实现方式（对 specs 的技术实现修正）

specs §5.1.2 步骤1 描述为「scheduler 按评估周期配置注册周期任务，配置变更时重注册」。本设计改为**固定每分钟触发的 tick 任务 + handler 内判定命中**，理由：

1. 月周期的触发日为「每月最后一天」，标准 cron 表达式需 `L` 扩展语法，robfig/cron 默认解析器不支持，跨实现风险高。
2. 配置变更热生效与进程重启恢复两条要求，tick 设计天然满足（每次 tick 读取当前配置），无需维护重注册逻辑与内存态。
3. 行为契约不变：触发时点、周期窗口推算、配置变更下次生效、重启后按持久化配置恢复，四项均与 specs 一致。

判定命中后走同一批次创建入口，与手动发起共用编排链路。

### 1.5 全员名单的确定时机

按 specs §4.1.4 规则1（选「全员」时批次先落骨架、执行开头展开为发起时点的全员名单快照）与 §5.1.2 步骤2（触发时读取评估对象快照），全员模式下的人员集合分两步确定：**批次创建时**只做集成密钥探测（密钥缺失立刻报错，不落 0 人批次静默跑空）并落骨架记录（`target_names_json='[]'`、`total_count=0`、无人员明细行），建批同步路径不触上游翻页（60s tick 预算防全员翻页击穿）；**批次执行（batch-run）开头**经人员检索接口 `GET /api/staffs` 全量分页拉取全员名单，原子展开回填 `target_names_json`、`total_count` 并批量写入人员明细（`status=pending`，`'[]'` 守卫防重试双插）。展开仍属发起时点快照语义：展开时点紧随建批入队，批次执行中途的人员变动不影响已展开名单。

编排展开会话列表后的 `token_name` 分组只承担两项用途：供 `EvaluatePerson` 的 sessions 入参注入、供覆盖会话数与会话级失败比例的分母口径消费。名单不因分组结果增删：窗口内无会话的人员照常入批次，其 `sessions` 为空时由 T5 的零档案路径落 `skipped` 终态并计入成功侧，人数口径与 specs §5.2.5「批次内全部人员计入失败计数（列表显示 N/N）」一致。

全员名单拉取失败（上游人员接口不可达）时：specified 模式建批前的密钥探测失败与 all 模式执行开头展开失败分别处置——定时链路记 ERROR 上抛 Asynq 任务级重试（§4.3），手动建批返回 1305 不落批次记录（B1 错误码表），展开失败（批次已落库）整批落 failed 终态。`total_count` 在骨架期短暂为 0 属预期（展开通常秒级完成），前端进度分母在展开完成前按 0 处理。

### 1.6 评估对象的展开键

评估链路（T4 档案、T5 评估、活跃度统计）以 `token_name` 为人员归属键，specs §5.2.2 步骤2 亦按 `token_name` 内存分组。人员检索接口 `GET /api/staffs`（P2_SYS_001 已建）返回 `staff_id`（上游 user_id）与 `staff_name`（上游 username）。

本设计以 **`staff_name` 作为 `token_name`** 展开，即假定两字段同取人员人名空间。若实现阶段核实上游 username 与 token_name 不同源（T4 §3.2 记载会话列表接口的 username 字段恒为 `admin`、无区分度），须以 token_name 为准回改本契约的人员选择数据源，接口形态与批次表结构不受影响。

---

## 2. 通用约定

### 2.1 基础配置

| 配置项 | 值 | 来源 |
|-------|------|------|
| Base URL 前缀 | `/api` | 规则文件 §2.1 |
| 版本控制 | 不分段，无 /v1 | 规则文件 §2.1 |
| 方法约定 | GET 仅查询，POST 仅变更 | 规则文件 §2.1 |
| 认证方式 | Bearer JWT | 规则文件 §2.1 |
| 路由归属 | 受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))` | 规则文件 §2.6 |

本功能全部接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号共享全量批次记录与跑批态势（specs §2.1）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 本功能接口不单独限流，沿用受保护路由组的既有策略（全系统仅在公开接口与登录链路上限流，业务接口以 JWT 鉴权与前端轮询节奏约束调用量）。列表与统计卡两接口被前端 10 秒轮询，单账号稳态 12 次/分钟（两接口各 6 次），量级低于任何需要限流的门槛。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`；分页结构作 `data` 透传，四字段 `list`/`total`/`page`/`page_size`。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500。评估运营域分配 **16xx 段**（避让 14xx/15xx 通用码），本功能新增码见 §5。人员检索复用既有 1305。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

承载雪花 ID 的字段 json 序列化一律带 `,string`，前端类型用 string：

- 批次主键 `id`：domain 模型与 service DTO 双层 `json:"id,string"`（account 链路样板）。
- 批次关联外键 `batch_id`：值来自批次雪花主键，`json:"batch_id,string"`。

分页 `total`、计数类字段（总人数、失败人数、覆盖会话数）与比例字段不在此列。

### 2.5 时间与日期格式

通用规范第 9 条的时间显示为 `yyyy-MM-dd HH:mm:ss`，本功能两处偏离（specs §8.3 已记录）：

- 批次列表「触发时间」与计划卡「下次执行」用 `yyyy-MM-dd HH:mm`（跑批粒度到分钟，秒位无信息量）。
- 批次列表「评估时段」用日期区间 `yyyy-MM-dd`（评估时段的语义是整日边界）。

接口入参的 `period_start`/`period_end` 亦为 `yyyy-MM-dd`，与列表展示、前端日期范围选择器同口径，**两端均含止日**（可相等，表示评估单日）。Unix 秒转换在评估链路取数装配处完成：`period_start` 按当日 00:00:00、`period_end` 按次日 00:00:00 转换（左闭右开），与 T5 Period 的半开区间语义一致。DB 列 `period_start_at`/`period_end_at` 存两端当日零点，与接口同口径（见 [04_model_interface.md](04_model_interface.md) §3.1）。

日期范围选择器与接口传递的都是含止日的展示口径，「上周一至上周日」即 `period_start=周一`、`period_end=周日`，不做 ±1 天的手工换算。

### 2.6 隐私边界

批次记录与人员明细只落人员归属（`token_name`）与统计计数，不落对话原文、特征档案内容、评分理由与 LLM prompt。失败原因摘要在写入前截断至 255 字符（specs §4.3.2 只展示摘要不展示原始堆栈）。

---

## 3. 接口列表

共 6 个新接口，按页面组织：评测运营中心页（A）、发起评测弹窗（B）。人员检索复用 P2_SYS_001 已建的 `GET /api/staffs`（C）。

需求追溯编号对应 specs 第 4 章页面功能。

### A. 评测运营中心页

#### A1. 查询批次列表

**接口路径：** `GET /api/assessment/batches`

**需求追溯：** [需求：F6-4.1.2C] [需求：F6-4.1.2D] [需求：F6-4.1.3 批次记录查询/筛选重置] [需求：F6-4.1.4 规则2] [需求：F6-4.1.5]

**说明：** 批次记录分页查询，按触发时间倒序。列表行携带进度、覆盖会话数与失败人数，前端 10 秒轮询本接口刷新进行中批次（specs §4.1.3 后台自动流程）。

停滞批次（`stalled=true`）的状态仍为 `running`，与状态筛选的「进行中」一并返回，由前端按 `stalled` 标识区分呈现（specs §4.1.2C）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| trigger_type | string | 否 | 空（全部） | 触发方式 [可选值：scheduled/manual] |
| status | string | 否 | 空（全部） | 批次状态 [可选值：running/success/partial_failed/failed] |
| page | integer | 否 | 1 | 页码，从 1 开始 |
| page_size | integer | 否 | 10 | 每页数量 [可选值：10/20/50]（specs §8.3 偏离记录第 1 条）。实现为宽松超集：任意 1-100 整数均接受（>100 钳位 100），前端只发三档 |

**请求示例：**

```
GET /api/assessment/batches?trigger_type=scheduled&status=running&page=1&page_size=10
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1780000000000000009",
        "batch_no": "B202609132300001",
        "trigger_type": "scheduled",
        "target_mode": "specified",
        "target_brief": ["张敏", "李芳"],
        "target_names": ["张敏", "李芳", "王强", "赵磊", "周杰"],
        "period_start": "2026-09-07",
        "period_end": "2026-09-13",
        "status": "running",
        "stalled": false,
        "evaluated_count": 42,
        "total_count": 128,
        "progress_percent": 32,
        "covered_session_count": 517,
        "failed_count": 3,
        "triggered_at": "2026-09-13 23:00"
      }
    ],
    "total": 1,
    "page": 1,
    "page_size": 10
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 批次主键（雪花 ID，string 化） |
| batch_no | string | 批次号，人工沟通的引用锚点 |
| trigger_type | string | 触发方式 [可选值：scheduled/manual] |
| target_mode | string | 评估对象模式 [可选值：all/specified] |
| target_brief | array | 评估对象摘要：`specified` 模式取名单前 2 个人名，`all` 模式为空数组（前端渲染「全员」）。超过 2 人时前端拼「前两人名 等 N 人」，N 取 `total_count`（specs §4.1.5） |
| target_names | array | 评估对象完整名单快照（`token_name` 人名全量数组，`all` 模式同返快照），供列表悬浮展示全部名单（specs §4.1.5 通用规范悬浮全名单） |
| period_start | string | 评估时段起点（含），`yyyy-MM-dd` |
| period_end | string | 评估时段终点（**含**），`yyyy-MM-dd`；与 `period_start` 同口径，可相等表示单日评估 |
| status | string | 批次状态 [可选值：running/success/partial_failed/failed] |
| stalled | boolean | 停滞标识：`running` 且距触发超过一个周期长度（判定口径见 §4.5） |
| evaluated_count | integer | 已到达终态的单人评估数（进度分子） |
| total_count | integer | 批次总人数（进度分母），创建时由名单快照确定（§1.5），无 0 值窗口 |
| progress_percent | integer | 进度百分比，整数，向下取整；`total_count=0` 时为 0（后端计算，规避前端除零） |
| covered_session_count | integer | 覆盖会话数：到达成功侧终态各人窗口内会话数之和（specs §5.2.4 规则3） |
| failed_count | integer | 失败人数，非零时前端以警示色强调 |
| triggered_at | string | 触发时间，`yyyy-MM-dd HH:mm` |

**错误码：** 1400（筛选值非法）、1500。

---

#### A2. 查询跑批态势统计

**接口路径：** `GET /api/assessment/batches/stats`

**需求追溯：** [需求：F6-4.1.2A] [需求：F6-4.1.3 刷新统计]

**说明：** 态势统计卡数据源，随列表轮询一并刷新。三项指标均以「本期跑批间隔」（上一次定时批次触发时点至下一次触发时点之间）为时间归属区间，与周期窗口（评估数据区间）口径分离（specs §4.1.2A 与 §8.1）。

`running_batch_count` 剔除停滞批次，与列表 `status=running` 的行数差额即已停滞批次（specs §4.1.2A）。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "eval_count": 2,
    "evaluated_person_count": 96,
    "running_batch_count": 1
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| eval_count | integer | 本期评测次数：触发时间落于本期跑批间隔内的批次数（定时加手动），补跑历史时段的手动批次计入发起当期 |
| evaluated_person_count | integer | 已完成评估人次：本期跑批间隔内全部批次中到达成功侧终态（success/reused/degraded/skipped）的单人评估计数，不去重，含部分失败批次中的成功者 |
| running_batch_count | integer | 进行中批次数：`running` 且非停滞的批次数 |

**错误码：** 1500。

---

#### A3. 查询跑批计划

**接口路径：** `GET /api/assessment/batches/plan`

**需求追溯：** [需求：F6-4.1.2B] [需求：F6-4.1.3 计划卡跳转]

**说明：** 跑批计划卡数据源，按当前评估周期配置（`assessment_configs` 单例）推算下次触发时点与评估对象，并返回当前启用的对话分析维度分组计数。

周期窗口与触发日推算口径源自 P2_SYS_001 §4.1.4 规则2（周期长度联动触发日：日→每日、周→每周日、月→每月最后一天），本功能只读取不改写。评估维度只读展示，口径在维度配置页维护。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "next_trigger_at": "2026-09-13 23:00",
    "period": "weekly",
    "target_mode": "specified",
    "target_brief": ["张敏", "李芳"],
    "target_names": ["张敏", "李芳", "王强", "赵磊"],
    "target_count": 12,
    "dimension_base_count": 4,
    "dimension_upper_count": 4
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| next_trigger_at | string | 下次执行时点，`yyyy-MM-dd HH:mm`；由 `period` + `trigger_time` 推算，已过当次则顺延一个周期 |
| period | string | 周期长度 [可选值：daily/weekly/monthly] |
| target_mode | string | 评估对象模式 [可选值：all/specified] |
| target_brief | array | 指定人员名单前 2 个人名，`all` 模式为空数组 |
| target_names | array | 评估对象完整名单（specified 为当前配置名单全量、`all` 模式为空数组由前端渲染「全员」），供计划卡悬浮展示全部名单（specs §4.1.5 通用规范悬浮全名单，与 A1 的 `target_names` 同承载） |
| target_count | integer | 评估对象人数。`specified` 模式为名单条数；`all` 模式为经人员检索接口取到的全员人数，上游不可达时返回 0（该情形下前端只渲染「全员」不展示人数，不影响计划卡其余字段） |
| dimension_base_count | integer | 当前启用的对话分析维度中 `group_code=BASE` 的计数（specs §4.1.2B 的「底层 N 维」） |
| dimension_upper_count | integer | 当前启用的对话分析维度中 `group_code=UPPER` 的计数（同上的「上层 N 维」） |

**错误码：** 1500。

---

#### A4. 查询批次评估对象名单

**接口路径：** `GET /api/assessment/batches/targets`

**需求追溯：** [需求：F6-4.1.3 重新发起] [需求：F6-4.1.5]

**说明：** 返回批次的完整评估对象名单快照与评估时段，供停滞批次行「重新发起」把两者一并带入发起评测弹窗（specs §4.1.3 明确「完整名单，非列表摘要」且要求「评估对象与评估时段」同为预填值）。名单为批次创建时点的人员快照，批次执行中途的人员变动不影响已落库的名单。

**查询参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| batch_id | string | 是 | 批次主键（雪花 ID，string 形态） |

**请求示例：**

```
GET /api/assessment/batches/targets?batch_id=1780000000000000009
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch_id": "1780000000000000009",
    "target_mode": "specified",
    "names": ["张敏", "李芳", "王强"],
    "total": 3,
    "period_start": "2026-09-07",
    "period_end": "2026-09-13"
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| batch_id | string | 批次主键（雪花 ID，string 化） |
| target_mode | string | 评估对象模式 [可选值：all/specified] |
| names | array | 评估对象完整名单（`token_name`，即人名）；`all` 模式返回该批次的完整人员快照。补跑预填场景下 B1 提交仅需 `staff_name`（见 B1 staffs 参数说明），人名即完整提交信息 |
| total | integer | 名单条数 |
| period_start | string | 评估时段起点（含），`yyyy-MM-dd`，供补跑预填 |
| period_end | string | 评估时段终点（含），`yyyy-MM-dd`；与 `period_start` 同口径，可相等表示单日评估 |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1601 | 批次不存在 |
| 1400 | batch_id 缺失或非法 |
| 1500 | 服务内部错误 |

---

#### A5. 查询批次失败明细

**接口路径：** `GET /api/assessment/batches/failures`

**需求追溯：** [需求：F6-4.3.2] [需求：F6-4.3.4 规则1] [需求：F6-4.3.5]

**说明：** 失败明细弹窗数据源，返回该批次失败人员的姓名、失败原因摘要与评估时段。清单为打开时刻的只读快照，条数与批次行「失败人数」计数一致，顺序按单人评估终态落库先后排列（specs §4.3.4 规则1）。评估时段随响应一并返回，供弹窗底部「按失败对象重新发起」把「本批次评估时段与全部失败人员」作为预填值带入发起评测弹窗（specs §4.3.3），无需调用方另行持有列表行数据。

批次级异常（上游会话列表不可用）时，批次内全部人员落失败终态，`error_summary` 为批次级失败原因（specs §4.3.2 与 §5.2.5）。

**查询参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| batch_id | string | 是 | 批次主键（雪花 ID，string 形态） |

**请求示例：**

```
GET /api/assessment/batches/failures?batch_id=1780000000000000009
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch_id": "1780000000000000009",
    "batch_no": "B202609132300001",
    "failed_count": 2,
    "total_count": 128,
    "period_start": "2026-09-07",
    "period_end": "2026-09-13",
    "list": [
      { "token_name": "张敏", "error_summary": "评估执行失败：LLM 上游不可用（重试 3 次耗尽）" },
      { "token_name": "王强", "error_summary": "评估执行失败：LLM 上游不可用（重试 3 次耗尽）" }
    ]
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| batch_id | string | 批次主键（雪花 ID，string 化） |
| batch_no | string | 批次号 |
| failed_count | integer | 失败人数，与 `list` 条数一致 |
| total_count | integer | 批次总人数 |
| period_start | string | 评估时段起点（含），`yyyy-MM-dd`，供补跑预填 |
| period_end | string | 评估时段终点（含），`yyyy-MM-dd`；与 `period_start` 同口径，可相等表示单日评估 |
| list | array | 失败人员清单，每项含 `token_name`（人名，也是补跑带入的人员标识）与 `error_summary`（失败原因摘要，超 255 字符已截断，前端按通用规范截断显示、悬浮展示全部） |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1601 | 批次不存在 |
| 1400 | batch_id 缺失或非法 |
| 1500 | 服务内部错误 |

---

### B. 发起评测弹窗

#### B1. 发起手动定向分析

**接口路径：** `POST /api/assessment/batches/create`

**需求追溯：** [需求：F6-4.2.2] [需求：F6-4.2.4 规则1/2] [需求：F6-4.1.3 提交发起] [需求：F6-5.3.2 步骤5 补跑]

**说明：** 创建一条手动批次并异步入队执行，是手动定向分析与失败补跑的共用入口（specs §5.3.2 步骤5 明确无独立补跑页面）。提交成功后前端关闭弹窗、重置筛选条件并回到第一页。

评测类型在首期恒为「对话分析」（specs §4.2.2：另两态置灰禁用），故请求不携带类型字段，接口语义即发起对话分析评估；F7 开放另两态时经其 Feature 的变更设计扩展该字段。

批次记录与名单快照在接口内同步创建并返回批次号，编排展开会话列表异步进行（§1.5）。`specified` 模式的名单取请求携带的人员，`all` 模式经人员检索接口取发起时点的全员名单，两类名单均一次性落库，批次执行中途入职或离职的人员变动不影响已展开的批次（specs §4.1.4 规则1）。

同时段允许多个批次并存，不弹重叠警告（specs §4.1.4 规则3）；同人同区间的重复评估由评估引擎的幂等机制收敛，不产生重复计分。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| target_mode | string | 是 | 评估对象模式 [可选值：all/specified]；`all` 与 `specified` 互斥，`all` 时忽略 `staffs` |
| staffs | array | 条件必填 | `target_mode=specified` 时必填且非空；每项含 `staff_id`（string，可选，上游人员唯一标识，仅日志定位用途，不参与去重与落库）与 `staff_name`（string，必填，人员展开键与批次名单去重键，见 §1.6），数据来自 `GET /api/staffs`；补跑预填场景（A4/A5 带入）仅需 `staff_name`，`staff_id` 可省略 |
| period_start | string | 是 | 评估时段起点（含），`yyyy-MM-dd` |
| period_end | string | 是 | 评估时段终点（含），`yyyy-MM-dd`；不得早于 `period_start`（允许相等，表示评估单日），且不得为今天及以后日期（specs §4.2.4 规则2） |

**请求示例：**

```json
{
  "target_mode": "specified",
  "staffs": [
    { "staff_id": "usr_9001", "staff_name": "张敏" },
    { "staff_id": "usr_9002", "staff_name": "李芳" }
  ],
  "period_start": "2026-09-01",
  "period_end": "2026-09-07"
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000010",
    "batch_no": "B202609111430012",
    "status": "running",
    "total_count": 2
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 批次主键（雪花 ID，string 化） |
| batch_no | string | 批次号 |
| status | string | 批次状态，创建后恒为 `running` |
| total_count | integer | 批次总人数；`specified` 模式为 `staffs` 按 `staff_name` 去重后的条数（§4.8），`all` 模式为全员名单条数（两类均在创建时确定，§1.5） |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1602 | 评估时段非法（终点早于起点、终点为今天及以后、日期格式不符） |
| 1603 | 评估对象非法（`specified` 模式人员为空、`staffs` 项缺 `staff_name`；`staff_id` 为可选字段，缺失不拦） |
| 1305 | 全员名单拉取失败（`target_mode=all` 时上游人员接口不可达，不落批次记录） |
| 1400 | 参数格式错误（`target_mode` 非枚举值、字段缺失） |
| 1500 | 服务内部错误（批次记录落库失败，前端 toast 提示后留在弹窗）。批次记录已落库但 `batch-run` 入队失败时，同请求内先将该批次落 `failed` 终态并在 `error_summary` 记入队错误，再返回 1500，不留孤儿进行中批次（见 §4.8 `SubmitManualBatch`） |

---

### C. 复用接口

#### C1. 查询人员列表（复用 P2_SYS_001 已建）

**接口路径：** `GET /api/staffs`

**需求追溯：** [需求：F6-4.2.2 评估对象] [需求：F6-4.2.3 人员搜索]

**说明：** 发起评测弹窗的评估对象数据源，定义见 [P2_SYS_001 03 文档 §3.A3](../P2_SYS_001_FEAT_系统参数与大模型配置/03_api_interface.md)。本功能不新增接口，直接复用。

弹窗打开即加载首屏选项（含前端固定首项「全员」），键入时异步触发服务端模糊搜索（`keyword` 参数，`page_size` 建议 20）。上游不可达时返回 1305，按 specs §4.2.3「人员搜索」条目，弹窗内下拉展示错误态与重试入口，不阻断弹窗其余操作——**与 P2_SYS_001 系统参数页的降级行为不同**（该页降级为仅全员模式），差异源于两处对人员数据不可用的业务容忍度不同，本功能在发起入口保留重试通道。

---

## 4. 非页面功能契约

specs 第 5 章的三项非页面功能（周期跑批调度、跑批编排、失败兜底）无 HTTP 面，其契约以 Asynq 任务与 Go 组件方法承载。

### 4.1 任务类型

```go
// worker/task 内定义并注册。
const TypeBatchTick = "engine:batch-tick" // 周期判定，每分钟触发（scheduler 注册）
const TypeBatchRun  = "engine:batch-run"  // 批次编排，payload 携批次主键
```

**需求追溯：** specs §5.1.1（触发方式：Asynq scheduler）、§5.2.1（事件驱动）。

`TypeBatchTick` 在 [worker/scheduler](../../../hr-backend/internal/worker/scheduler) 内以每分钟 cron 注册（Location 用 `time.Local`，与既有 HealthCheck 同款）；`TypeBatchRun` 由 tick handler 与发起评测接口两处投递。

### 4.2 任务 payload

**batch-tick：** 无 payload（判定依据为触发时刻与当前配置，均从库读取）。

**batch-run：**

```json
{
  "batch_id": "1780000000000000009"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| batch_id | string | 是 | 批次主键（雪花 ID 的十进制字符串）；缺失、非数字或 ≤ 0 时 handler 记 ERROR 后返回 nil 丢弃任务（构造侧确定性错误，与 T4/T5 既有任务同款处置） |

### 4.3 batch-tick handler 行为约定

```
输入:  无
流程:  1. 读 assessment_configs 单例（周期长度、触发时点）
       2. 按触发时刻判定是否命中：当前 HH:mm 等于触发时点，且今日为触发日
          （daily→每日、weekly→周日、monthly→当月最后一天）
       3. 未命中直接返回 nil
       4. 命中 → 读评估对象配置；specified 模式读指定人员名单，all 模式经人员
          检索接口取全员名单快照（§1.5）
       5. 判定同源批次阻塞：存在 running 且非停滞的定时批次时，记 ERROR 跳过本次触发
       6. 推算本周期窗口（日→当日、周→本周一至本周日、月→本月 1 日至月末）
       7. 经 pipeline.Orchestrator.CreateBatch 创建定时批次并入队 batch-run
返回:  err == nil → 成功（含未命中与同源阻塞两种无操作路径，不重试）
       err != nil → 交 Asynq 任务级重试（配置读取失败、全员名单拉取失败、
       建批落库失败、入队失败四类）
超时:  任务级超时 60s（判定与建批为 DB 读写，名单为上游一次全量拉取；上述四类
       错误之一即上抛重试）
并发:  由 config.Asynq.Concurrency 承载
日志:  命中与跳过记 INFO/ERROR，字段仅触发时刻、批次号、跳过原因；各类失败记错误摘要
```

**对 specs §5.1.5 的技术实现修正（失败处置）：** specs 三行异常均记为「跳过本次触发」+ ERROR 日志。本设计仅在无操作路径（未命中、同源批次阻塞）上返回 nil 不重试，与 specs 一致；在配置读取失败、全员名单拉取失败、建批落库失败、入队失败四类可恢复错误上改交 Asynq 任务级重试，理由是瞬时故障重试即可建批，比整周期空转更贴合业务意图。行为边界：同一分钟内的重试若再次命中判定即建批，等效于本次触发成功；跨分钟的重试判定不命中，等效于跳过本次触发。重复建批由步骤5 的同源批次阻塞判定拦截（首次重试建批成功后，后续重试读到 `running` 的定时批次即跳过），批次号唯一索引为并发兜底。

**需求追溯：** specs §5.1.2 步骤1-3、§5.1.4 规则2/3、§5.1.5 三行异常处理。

**每分钟 tick 的幂等保证：** 同一分钟内只会有一个 worker 消费该任务（Asynq 队列语义），不产生重复触发。跨分钟的重复由「同源批次阻塞」判定拦截（specs §5.1.4 规则2）：上一次定时批次仍在 running 时跳过本次，故即使出现时钟回拨或双触发，最多只多出一条被阻塞的 ERROR 日志。

### 4.4 batch-run handler 行为约定

```
输入:  payload{batch_id}
流程:  1. 读批次记录，非 running 直接返回 nil（幂等终态，重复消费不重复编排）
       2. 经 conversationlog.Client.ListSessions 按评估时段全量串行翻页拉取会话列表
          （并发多页返回重复首页，见 T4 §3.2；禁止依赖上游按人过滤参数）
       3. 按 session_key 去重，按 token_name 内存分组得到各人会话集
       4. 回填批次 total_session_count（去重后会话总数），按创建时的名单快照
          落批次人员明细的 session_count（各人窗口内会话数，无会话者为 0）；
          人员明细的 pending 行已在创建时写入，本步骤只回填会话数
       5. 逐会话投递 engine:session-extract（一会话一任务）
       6. 等待抽取落库：按已投递会话的 session_key 集合轮询 session_features
          落库进度（含 failed 终态行），达到投递数或超过等待上限（30 分钟、
          轮询间隔 10 秒）即进入下一步；投递失败会话不参与等待；超时按已落库
          档案继续评估，不中断批次（specs §5.2.2 步骤4 与 §5.2.5）
       7. 逐人并发（上限 4 路）直调 evaluator.EvaluatePerson，
           以步骤3 分组结果中该人窗口内的会话列表作 sessions 入参
       8. 每人返回即回写该人终态并原子推进批次计数（见 §4.6）
       9. 全部终态后落批次终态、算会话级失败比例、按阈值写告警信号
返回:  err == nil → 成功（含批次已终态的幂等跳过、上游不可用导致的整批失败终态，
       两者均为终态不重试）
       err != nil → 交 Asynq 任务级重试
等待:  抽取落库等待上限 30 分钟、轮询间隔 10 秒（pipeline 包 ExtractWaitTimeout
       与 ExtractPollInterval）；上限按 worker 池并发度 10（batch-run 自占 1 个
       slot，余 9 路跑抽取）与单会话抽取典型 10-60s 留充裕余量取定；超时按
       已落库档案继续评估，不中断批次。
超时:  任务级超时 24h（86400s），单点声明在 worker/task 的 batchRunTimeout。
       推导：4 路并发下，全员百人量级按单人单次尝试预算 1050s（T5 §3.3）计，
       100/4 × 1050s ≈ 7.3h；单人最坏含首次与 3 次重试（4 × 1050s = 4200s）时
       为 100/4 × 4200s ≈ 29.2h，超出该值。超出部分由任务级超时切断，被切断
       的批次停留 running 并按停滞批次处置（specs §5.1.4 规则2 与 §5.2.5），
       不阻塞下个周期触发。正常路径（单人 30-90s）下全员百人约 15-40 分钟。
       24h 与日周期的停滞判定边界对齐，是兜底值而非运行期预期值。
并发:  编排器自身的逐人并发上限 4（specs §5.2.2 步骤5：不超过评估专用 client 的
       LLM 在飞 gate）。逐人评估占用评估通道 gate，与 T5 任务通道共享同一上限。
日志:  只输出批次号、计数、人员归属与错误码，禁止输出档案内容与 prompt 文本
```

**需求追溯：** specs §5.2.2 步骤1-8、§5.2.5 异常处理表 8 行。

**编排不改写评估侧数据：** 步骤5 的抽取任务投递、步骤6 的等待与步骤7 的单人评估均为既有 T4/T5 契约的调用，本功能不新增评估侧写入路径；`session_features` 的落库仍归 T4。

### 4.5 停滞判定口径

批次 `status=running` 且 `triggered_at` 距当前时刻超过「一个周期长度」即判停滞（specs §5.1.4 规则2）。周期长度取当前 `assessment_configs` 单例的周期长度，按日历推算：

| 周期长度 | 停滞边界 |
|---------|---------|
| daily | `triggered_at` + 1 天 |
| weekly | `triggered_at` + 7 天 |
| monthly | `triggered_at` + 1 个日历月（`AddDate(0, 1, 0)`） |

停滞是**查询期派生标识，不落库、不改写批次状态**（specs §6.2 说明明确其停留进行中不改写转换规则）。列表接口的 `stalled` 字段与统计卡的 `running_batch_count` 均由同一判定函数产出，保证两处口径一致。

### 4.6 批次推进与终态

单人评估返回即回写，回写内容与口径：

| 单人终态 | 判定 | 计数影响 |
|---------|------|---------|
| success | 评估返回且无 failed 评分行、`Skipped=false`、`Reused=false` | `evaluated_count+1`，`covered_session_count+该人会话数` |
| reused | `EvaluateResult.Reused=true`（幂等复用未调 LLM） | 同上（成功侧） |
| degraded | 返回且存在 failed 评分行（LLM 段失败降级） | 同上（降级保留统计，specs §5.2.4 规则3） |
| skipped | `EvaluateResult.Skipped=true`（零有效档案） | 同上（成功侧） |
| failed | 编排器侧重试耗尽（§4.7） | `evaluated_count+1`，`failed_count+1`，不计覆盖会话数 |

计数推进用 SQL 原子自增（`UPDATE ... SET evaluated_count = evaluated_count + 1 ...`），避免并发读改写竞态；`evaluated_count = total_count` 时落批次终态。终态判定读到的 `failed_count` 决定状态：

| 条件 | 批次状态 |
|------|---------|
| 失败人数占比 ≤ `10.00` | success |
| `10.00` < 失败人数占比 < `100.00` | partial_failed |
| 失败人数占比 = `100.00` | failed |
| 上游会话列表不可用 | failed（整批，全员计入失败计数） |

上表的占比与 §4.7 的 `batchAlertThreshold` 同口径：均按**百分比口径**计算并保留两位小数，取值范围 0 至 100（即 `failed_count / total_count × 100` 后保留两位小数，如 `12.50` 表示 12.5%），与告警阈值的比较按该精度判定（specs §5.2.2 步骤8）。阈值取 `10.00`，等价于比率式 `failed_count / total_count > 0.10`。失败人数占比超阈（`> 10.00`，含 `100.00`）写一条告警信号，每批次至多一条（`assessment_alerts.batch_id` 唯一索引兜底，重复判定幂等覆盖）。

会话级失败比例（`assessment_batches.session_fail_ratio`）同用该百分比口径，但不参与阈值判定，仅随批次记录落库供 F11 工作台呈现。

占比分母 `total_count` 在批次创建时由名单快照确定（§1.5），名单非空故分母恒 ≥ 1，无零除路径。整批不可用（上游会话列表拉取失败）时全员已在创建时落入人员明细，逐行落 `failed` 终态即实现 specs §5.2.5 的「全部人员计入失败计数（N/N）」。

### 4.7 重试与兜底

会话抽取任务的重试沿用 Asynq 任务级默认口径（30s 基准倍增退避，[worker/server](../../../hr-backend/internal/worker/server) 既有 `RetryDelayFunc`）。单人评估由编排器侧施加同一退避曲线，次数为本功能常量：

| 常量 | 初值 | 说明 |
|------|------|------|
| `personEvalMaxRetries` | 3 | 单人评估**重试次数**，含首次共 4 次尝试；每次尝试派生子 ctx 并各自拥有完整预算（1050s），退避等待不计入预算（specs §5.3.4 规则1） |
| `personEvalRetryBaseDelay` | 30s | 退避基准，倍增 |
| `batchAlertThreshold` | 10.00 | 失败人数占比告警阈值，百分比口径（0 至 100），与占比值同精度比较（§4.6） |
| `batchRunConcurrency` | 4 | 编排器逐人评估并发上限 |

三者为包内常量随源码发版，在线配置不在本期范围（specs §5.3.4 规则1）。告警信号写入失败仅记日志不重试（specs §5.3.5）；补跑不自动触发，由运营人员经发起评测手动执行（specs §5.3.4 规则3）。

### 4.8 组件方法契约

**pipeline（`engine/pipeline`，替换 doc.go 占位）：**

| 方法 | 签名（概念形，ctx 略） | 语义 | 需求追溯 |
|------|----------------------|------|---------|
| CreateBatch | (req CreateBatchRequest) → (*domain.AssessmentBatch, error) | 创建批次记录（定时或手动），生成批次号，解析名单快照（`specified` 取请求名单，`all` 经人员检索接口取全员名单），落 running 态、`target_names_json`、`total_count` 与批次人员明细 `pending` 行；两种来源共用 | specs §5.2.2 步骤1 |
| RunBatch | (batchID int64) → error | 批次编排全流程：展开分组 → 回填会话总数与各人会话数 → 投递抽取 → 等待抽取落库 → 逐人评估 → 推进终态 → 写告警；非 running 批次幂等返回 | specs §5.2.2 步骤2-8 |
| SubmitManualBatch | (req CreateBatchRequest) → (*domain.AssessmentBatch, error) | 手动发起入口：调 CreateBatch 后投递 batch-run 任务；入队失败时把该批次落 `failed` 终态并记 `error_summary` 后返回错误，不留孤儿进行中批次 | specs §4.2.3 提交发起 |

`CreateBatchRequest` 字段：`TriggerType`（scheduled/manual）、`TargetMode`（all/specified）、`TargetNames`（`specified` 名单的人名切片，按 `staff_name` 去重后作为批次名单与人员明细的写入源，与 `uk_batch_person` 的 (batch_id, token_name) 唯一键同键，同名同人收敛；`all` 模式为空由 CreateBatch 内部解析）、`PeriodStart`/`PeriodEnd`（`time.Time`，取含止日当日的零点，装配为 T5 入参时 `PeriodEnd` 加一天转半开区间）。`staff_id` 不进请求结构，仅 handler 侧记日志。

**fallback（`engine/fallback`，替换 doc.go 占位）：**

| 方法 | 签名（概念形，ctx 略） | 语义 | 需求追溯 |
|------|----------------------|------|---------|
| Retry | (fn func(ctx) error, maxAttempts int, baseDelay time.Duration) → error | 通用重试器：固定次数、基准倍增退避、每次尝试派生子 ctx（父 ctx 取消即终止） | specs §5.3.2 步骤3、§5.3.4 规则1 |
| BelowAlertThreshold | (failedCount, totalCount int) → bool | 告警判定纯函数：占比是否超阈（`totalCount=0` 返 false），精度口径与写入一致 | specs §5.2.4 规则4 |
| WriteAlert | (batch, failedCount, totalCount) → error | 写告警信号记录，`batch_id` 唯一索引幂等覆盖；失败仅记日志不重试 | specs §5.3.2 步骤4 |

**与既有域的调用面（只读消费，无写入）：**

| 依赖 | 用途 |
|------|------|
| `conversationlog.Client.ListSessions` | 步骤2 全量串行翻页拉取会话列表（复用既有契约，Bearer 集成密钥经 `service.ResolveIntegrationSecret` 装配） |
| `userapi` 人员检索（P2_SYS_001 `GET /api/staffs` 的数据源） | `all` 模式的批次创建取全员名单快照（§1.5），与发起弹窗的人员搜索同源 |
| `evaluator.Evaluator.EvaluatePerson` | 步骤6 单人评估（sessions 入参注入，进程内直调；ctx 预算单次 1050s 量级，满足 T5 §3.3 的 ≥900s 下限） |
| `llm` 评估专用 client 的 gate | 步骤6 并发上限对齐（4 路） |
| `session_features` 表（读） | 步骤8 会话级失败比例的分子（`status=failed` 计数，承接 T4 §6.2 分子口径） |

---

## 5. 错误码

新增 16xx 段共 3 个码，人员检索复用既有 1305。

| 错误码 | 常量 | 含义 | 使用场景 |
|--------|------|------|---------|
| 1601 | BatchNotFound | 批次不存在 | 失败明细、评估对象名单接口的 batch_id 查无记录 |
| 1602 | BatchPeriodInvalid | 评估时段非法 | 发起评测：终点早于起点、终点为今天及以后、日期格式不符 |
| 1603 | BatchTargetInvalid | 评估对象非法 | 发起评测：`specified` 模式人员为空、`staffs` 项缺 `staff_name`（`staff_id` 为可选字段，缺失不拦） |
| 1305 | StaffListUnavailable | 人员列表暂不可用 | 复用 P2_SYS_001 定义。本功能两处消费：发起评测弹窗的评估对象下拉（C1），以及 `target_mode=all` 时创建批次的全员名单拉取（B1，失败即不落批次记录） |

---

## 6. 与相邻域的边界

- **对 P2_SYS_001（config）**：只读消费 `assessment_configs`/`assessment_config_members`（周期长度、触发时点、评估对象）与 `GET /api/staffs`；周期窗口与触发日推算口径承接其 §4.1.4 规则2，本功能只读取不改写。配置变更在下次触发读取时生效（specs §4.1.4 规则4）。
- **对 P2_DIM_001（dimension）**：只读消费维度分组计数（计划卡展示）；评分口径经 T5 引擎间接消费。
- **对 T4（extractor）**：只投递 `engine:session-extract` 任务与只读 `session_features`（失败比例分子），档案表契约与重试语义归 T4。
- **对 T5（evaluator/scorer/activity）**：进程内直调 `EvaluatePerson` 已分组路径，单人终态的细粒度语义（Skipped/Reused/degraded）归 T5 定义，本功能只做批次层聚合；ctx 预算与并发上限按其 §3.3 声明施加。
- **对 F9/F10/F11（未建）**：本功能产出 `assessment_alerts` 告警信号供 F11 工作台消费；「查看结果」跳转个人画像列表页归 F9。
- **对 F7（未建）**：AI 管理能力与九型人格 tab 在同一页面容器叠加，本设计不预留其接口。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1 评测运营中心页（列表、统计卡、计划卡、筛选、发起入口、失败明细入口、重新发起、查看结果跳转、刷新统计）→ A1-A5；4.2 发起评测弹窗（对象、时段、提交、取消、人员搜索）→ B1 与复用的 C1；4.3 失败明细弹窗 → A5。
- [x] 字段定义与 specs 一致：4.1.2A/B/C/D、4.2.2、4.3.2 的全部字段在响应结构中一一对应；三处按 spec 语义适配（4.2.2「评测类型」首期恒为对话分析不携带，4.1.2D「停滞」以派生布尔 `stalled` 承载而非状态值，4.1.5 悬浮全名单经 A1/A3 的 `target_names` 全量字段承载），「周期长度」沿用上游字段名（specs v1.8）。
- [x] 业务规则在接口与任务契约中落地：全员互斥（B1 的 `target_mode` 单值）、全员名单在创建时快照（§1.5）、时段边界（1602 校验）、同周期批次并存（B1 无重叠校验）、进度与覆盖会话数口径（A1 字段 + §4.6）、失败人数占比与告警（§4.6-4.7）。
- [x] 状态定义与 specs 第 6 章一致：批次四态与转换条件落 §4.6，终态不可逆由 `RunBatch` 的幂等返回保证。
- [x] 权限规则一致：全部接口挂 JWT，无角色差异（specs §2.2），无字段级权限。
- [x] 非页面功能全覆盖：5.1 周期跑批调度 → §4.3；5.2 跑批编排 → §4.4/4.6；5.3 失败兜底 → §4.7。
- [x] 技术层偏差已声明：tick 替代动态 cron 重注册（§1.4）、batch-tick 失败处置改为上抛重试（§4.3）、评估对象展开键假定（§1.6）。
- [x] 接口与数据库一致性：A1-A5 响应字段与 [04_model_interface.md](04_model_interface.md) 三表列一一对应，`batch_id` 的 string 化双层覆盖（§2.4）。

---

## 8. 不涉及的设计

- 批次取消、作废、删除接口（specs §6.2 明确不提供）。
- 独立补跑页面与补跑接口（specs §5.3.2 步骤5 明确复用发起评测入口）。
- 告警信号的消费与呈现接口（F11 工作台）。
- 个人画像列表页的跳转目标接口（F9）。
- 周期配置与维度配置的维护接口（P2_SYS_001 / P2_DIM_001 已建）。
- 失败会话清单页与单会话重试接口（specs §4.3.4 规则1 明确清单只读；T4 §6.2 已知漏计项在本期不补）。
- 告警阈值与重试次数的在线配置（specs §5.3.4 规则1 明确不在本期范围，为包内常量）。

---

**文档版本：** v1.6
**最后更新：** 2026-09-13
**作者：** lixuetao

**v1.6 变更（监理扫描·模式三修复）：** A3 响应追加 `target_names` 全量名单字段（计划卡悬浮展示全部名单的承载，此前仅 target_brief 前 2 人名，对齐 A1 v1.5 同款处理）。

**v1.5 变更（监理扫描·模式三修复）：** A1 响应追加 `target_names` 全量名单字段（specs §4.1.5 悬浮展示全部名单的承载，此前仅 target_brief 前 2 人名）。

**v1.4 变更（监理扫描·模式二修复）：** B1 响应 total_count 说明补 specified 模式按 staff_name 去重语义（对齐 §4.8 TargetNames 去重口径）。

**v1.3 变更（监理扫描·模式二修复）：** staffs[].staff_id 改为可选字段仅日志定位，批次名单去重键收敛为 staff_name（与 uk_batch_person 唯一键同键，同名同人收敛，§3.B1/§4.8）；A4 names 字段补补跑预填仅需人名的提交口径；progress_percent 补向下取整规则（A1）；§2.1 轮询量级修正为 12 次/分钟；§7 合规清单接口编号修正为 A1-A5；错误码表 1603 同步 staff_id 可选。
