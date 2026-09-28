# 主动测试评估运营 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_TST_001_FEAT_主动测试评估运营 |
| 模块代号 | TST（主动测试域） |
| 文档版本 | v1.3 |
| 创建日期 | 2026-09-28 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、状态、权限），技术实现层面以规则文件为准（方法约定、表名、字段类型、响应结构）。冲突逐条在 §1.2-1.4 声明。

### 1.2 HTTP 方法归一化

specs 未指定 HTTP 方法。本设计按规则文件 §2.1 归一化：查询一律 GET，变更一律 POST 加动作路径。路由风格沿用既有 `/api/assessment/batches` 先例（ASM_001）：资源根路径 + 动词后缀（create/resend/cancel），查询子路径用名词（link）。本功能资源根为 `/api/assessment/test-tasks`，与批次（对话分析）在 assessment 域内并列，页面同属评测运营中心。

### 1.3 一次性令牌的鉴权边界（跨 Feature 契约）

specs §2.1 明确员工作答页与主平台 JWT 物理隔离，作答页（含令牌校验、作答数据、提交接口）归 F8。本功能只产出令牌（创建与重发时生成、落 `assessment_test_links` 表），不提供任何免 JWT 的 HTTP 面：链接弹窗走主平台 JWT 查询当前链接，令牌原文与哈希双列落链接行（见 04 文档 §3.2），F8 依其自身设计消费校验接口。本文档不定义 `/api/answer/*` 类接口。

### 1.4 轮询探针的承载方式

specs §4.1.3 未终态任务轮询刷新明确复用 F6 已建的页面级 10 秒轮询通道，本 feature 只把两类测试任务的未终态计数并入同一探针。探针接口（既有 `GET /api/assessment/batches/stats` 或等价轮询通道）的扩展属于 F6 通道的增量字段，本设计以专用查询接口 A2（`GET /api/assessment/test-tasks/poll-counts`）承载两类任务的计数，前端探针消费该接口判断是否需要刷新任务列表；两 tab 列表刷新仍走 A1 查询，频次由前端控制，后端不做推送。

### 1.5 作答提交回调的时序归属

specs §5.2 把作答提交落库与阅卷任务投递定义为 F8 提交接口的事务内动作（5.2.5 投递失败即整体回滚）。提交接口本身归 F8，但**事务内的三步写动作**（任务转已完成、链接置已使用、阅卷状态置阅卷中并投递 `assessment:test-grade` 任务）操作的是本功能的表与任务类型，其契约在本文档 §4 定义，F8 的提交实现按此调用本域 service 方法，保证状态机单点归属主动测试域。

### 1.6 CompleteTask 对 pending 态的防御性放行（技术层偏差）

specs §6.2 任务状态转换表无「待作答 → 已完成」路径（完成须经 进行中 → 已完成）。本设计的 CompleteTask 允许 pending/in_progress 均可入：F8 会话上报（StartSession）与提交（CompleteTask）非强制先后，会话上报失败或未发生时员工直接提交仍可闭环，避免作答事实因上报缺失被拒；条件更新守卫（completed/canceled 终态拦截）承载幂等。pending 直达 completed 属对 specs 转换表的防御性扩展，正常链路仍为 pending → in_progress → completed。

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

本功能全部接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号共享全量测试任务（specs §2.1）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 不单独限流，沿用受保护路由组既有策略（业务接口以 JWT 鉴权与前端轮询节奏约束调用量）。列表与轮询计数接口被 10 秒级轮询消费，单账号稳态量级低于任何需要限流的门槛。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`；分页结构作 `data` 透传，四字段 `list`/`total`/`page`/`page_size`。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500、批次 16xx（ASM_001）、题库 17xx（QBN_001）。主动测试域分配 **18xx 段**（18xx 起空闲，代码事实核对无误），本功能新增码见 §5。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

承载雪花 ID 的字段 json 序列化一律带 `,string`，前端类型用 string：

- 任务主键 `id`：domain 模型与 service DTO 双层 `json:"id,string"`。
- 外键 `task_id`：值来自任务雪花主键，`json:"task_id,string"`。
- 请求转运的 `dimension_ids`（B1）：数组项为维度雪花 ID，DTO 层 `[]string` 收发。

分页 `total`、状态计数不在此列。

### 2.5 时间格式

任务列表「发起时间」「完成时间」与链接弹窗「生成时间」显示 `yyyy-MM-dd HH:mm`（specs §4.1.2/§4.3.2 与 §8.3 偏离记录第 3 条，与 F6 同页同口径）。响应 JSON 时间字段直出 `yyyy-MM-dd HH:mm` 本地时区字符串（F6 批次域 triggered_at 同口径，DTO 层 time.Local().Format 直出），前端原样渲染。

### 2.6 隐私与令牌安全

- 作答令牌原文仅在创建/重发响应与链接弹窗查询中返回（运营复制分发的载体）；链接行落原文列与 SHA-256 哈希列双列（specs §4.3.4 规则1 要求弹窗始终展示链接全文、历史链接也可见，哈希不可逆无法支撑，见 04 文档 §3.2），哈希列承载 F8 校验点查与唯一性兜底。防枚举语义收敛为令牌随机不可预测加一次性有效（specs §5.1.4 规则1 本义）。令牌生成用 crypto/rand 32 字节 base64url（43 字符）。
- 阅卷产出的理由与证据只落脱敏文本与题目 ID 清单（04 文档 §3.3 隐私边界），prompt 全文禁落任何列。
- 本域表不落对话原文；员工作答对话记录归 F8 落库，阅卷经进程内读取（§4.4）。

---

## 3. 接口列表

共 7 个新接口，按页面组织：评测运营中心页两 tab（A）、发起评测弹窗（B）、作答链接弹窗（C）。人员检索复用 P2_SYS_001 已建 `GET /api/staffs`，子能力维度复用 P2_DIM_001 已建 `GET /api/dimensions/tree`，均不新增。

需求追溯编号对应 specs 第 4 章页面功能（两 tab 同构合并引用）。

### A. 评测运营中心页 · AI 管理能力 / 九型人格 tab

#### A1. 查询测试任务列表

**接口路径：** `GET /api/assessment/test-tasks`

**需求追溯：** [需求：F7-4.1.2A] [需求：F7-4.1.2B] [需求：F7-4.1.3 任务记录查询/筛选重置] [需求：F7-4.1.4 规则1/2/3] [需求：F7-4.1.5]

**说明：** 单类型测试任务分页查询，按发起时间倒序。`test_type` 为必传查询参数（tab 即类型，前端两 tab 各自持有一份查询状态），后端单接口承载两类任务，替代建两个同构路由。列表行携带任务号、对象、双状态轴（任务状态、阅卷状态）与当前链接状态；逾期高亮由前端按 `status=expired` 渲染。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| test_type | string | 是 | - | 测试类型 [可选值：ai_mgmt/enneagram]，对应 AI 管理能力 / 九型人格 tab |
| status | string | 否 | 空（全部） | 任务状态 [可选值：pending/in_progress/completed/expired/canceled] |
| keyword | string | 否 | 空 | 按对象姓名或任务号模糊搜索（经 likeescape.EscapeLike 转义，规则文件 §1.11） |
| page | integer | 否 | 1 | 页码，从 1 开始 |
| page_size | integer | 否 | 10 | 每页数量 [可选值：10/20/50]（specs §8.3 偏离第 1 条）。实现为宽松超集：任意 1-100 整数均接受（>100 钳位 100） |

**请求示例：**

```
GET /api/assessment/test-tasks?test_type=ai_mgmt&status=pending&keyword=张&page=1&page_size=10
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1790000000000000001",
        "task_no": "T202609280001",
        "test_type": "ai_mgmt",
        "staff_name": "张敏",
        "status": "pending",
        "link_status": "valid",
        "grading_status": "waiting",
        "created_at": "2026-09-28 08:30",
        "completed_at": null
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
| id | string | 任务主键（雪花 ID，string 化） |
| task_no | string | 任务号，T（ai_mgmt）/ E（enneagram）前缀 + yyyyMMdd + 4 位当日序号，人工沟通引用锚点 |
| test_type | string | 测试类型 [可选值：ai_mgmt/enneagram]，回显查询条件 |
| staff_name | string | 被测评人姓名（发起时的人员快照，人名即标识，系统无工号） |
| status | string | 任务状态 [可选值：pending 待作答/in_progress 进行中/completed 已完成/expired 已逾期/canceled 已取消]（specs §6.1） |
| link_status | string | 当前链接状态 [可选值：valid 有效/used 已使用/invalid 已失效]，为该任务最新一条链接行的状态（重发产生新行后自动指向新行） |
| grading_status | string | 阅卷状态 [可选值：waiting 待阅卷/grading 阅卷中/scored 已评分（九型 tab 呈现为已判定）/degraded 已降级]（specs §6.1；九型 tab 文案由前端 i18n 映射，取值同源） |
| created_at | string | 发起时间（yyyy-MM-dd HH:mm 本地时区直出，前端原样渲染） |
| completed_at | string \| null | 完成时间（员工作答提交时刻，同格式直出），未完成为 null，前端渲染 — |

**错误码：** 1400（test_type 缺失或非法、status 非枚举值）、1500。

---

#### A2. 查询测试任务轮询计数

**接口路径：** `GET /api/assessment/test-tasks/poll-counts`

**需求追溯：** [需求：F7-4.1.3 未终态任务轮询刷新]

**说明：** 页面级 10 秒轮询探针的两类任务计数数据源（specs §4.1.3：待作答/进行中/已完成但阅卷未终态的任务计数并入 F6 轮询通道）。前端探针发现任一计数 > 0 且页面停留时刷新当前 tab 列表；已逾期、已取消、阅卷已终态的任务不计数（无状态自动变化，不触发轮询）。接口恒返回两类计数，前端只消费当前 tab 的一类。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "ai_mgmt_active": 3,
    "enneagram_active": 1
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| ai_mgmt_active | integer | AI 管理能力类未终态任务数：`test_type=ai_mgmt` 且（status ∈ {pending, in_progress} 或（status=completed 且 grading_status ∈ {waiting, grading}）），已取消任务即使阅卷在途也不计（specs §4.1.3：其阅卷推进经手动刷新呈现） |
| enneagram_active | integer | 九型人格类同口径计数 |

**错误码：** 1500。

---

### B. 发起评测弹窗

#### B1. 发起主动测试

**接口路径：** `POST /api/assessment/test-tasks/create`

**需求追溯：** [需求：F7-4.2.2] [需求：F7-4.2.3 提交发起] [需求：F7-4.2.4 规则1/2/3] [需求：F7-5.1]

**说明：** 单人单类型定向发起（决策 12）：创建测试任务（生成任务号）、固化题目快照、生成一次性作答令牌与链接、写题目引用计数，单事务完成（specs §5.1.4 规则3）。成功后前端关闭弹窗、切换到对应 tab、重置查询条件回第一页刷新列表。同人多任务并存不校验不告警（specs §4.1.4 规则3）。

AI 管理能力取题：按 `dimension_ids` 圈定的子能力集合，各子能力取当前 `status=ACTIVE` 且 `source=AI` 的全部题目（specs §4.2.4 规则1：题库为代表性样本规模，全取即全做）；任一子能力启用题为 0 时整体报错不建任务（1803）。
九型取题：取最新引入量表（两套并存取 `question_batches` 中该量表最近一个 IMPORT 批次，无则视为未引入）的 `status=ACTIVE` 且 `source=SCALE` 题目全量；无启用状态量表题时报错（1804）。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| test_type | string | 是 | 测试类型 [可选值：ai_mgmt/enneagram] |
| staff_id | string | 是 | 被测评人标识（上游 user_id 字符串形态，来自 `GET /api/staffs`），落库快照供 F8 会话与画像对齐 |
| staff_name | string | 是 | 被测评人姓名（快照；与 dimension_scores.token_name 同口径的人员归属键） |
| dimension_ids | array\<string\> | 条件必填 | `test_type=ai_mgmt` 时必填且非空：子能力维度雪花 ID 数组（当前启用的 AI_MGMT 子能力子集）；`enneagram` 时忽略该字段 |

**请求示例：**

```json
{
  "test_type": "ai_mgmt",
  "staff_id": "9001",
  "staff_name": "张敏",
  "dimension_ids": ["1780000000000000100", "1780000000000000101"]
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1790000000000000001",
    "task_no": "T202609280001",
    "test_type": "ai_mgmt",
    "staff_name": "张敏",
    "status": "pending",
    "answer_url": "/answer/AbCdEf...43chars",
    "created_at": "2026-09-28 08:30"
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 任务主键（雪花 ID，string 化） |
| task_no | string | 任务号（T/E 前缀 + yyyyMMdd + 4 位当日序号） |
| test_type | string | 测试类型 |
| staff_name | string | 被测评人姓名 |
| status | string | 任务状态，创建后恒为 `pending` |
| answer_url | string | 一次性作答链接相对路径（`/answer/{token}`）。specs §4.2.3 明确发起成功不自动打开链接弹窗，运营稍后经行内「作答链接」查看分发；本字段随创建响应一并返回供前端留存上下文，页面不弹窗展示 |
| created_at | string | 发起时间 |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（test_type 非枚举值、staff 缺失、ai_mgmt 时 dimension_ids 空） |
| 1803 | 子能力无可用题目（AI 管理能力：勾选的某子能力启用题为 0；message 携维度名，前端按 specs §4.2.4 规则1 文案 toast「子能力 X 无可用题目，请先在题库补充」） |
| 1804 | 九型量表未就绪（未引入或全部停用；前端 toast「九型量表未就绪，请先在题库引入量表」） |
| 1805 | 发起对象人员无效（staff_id 经上游人员接口校验不存在或上游不可达；specs §5.1.5 第一行，任务不创建） |
| 1500 | 服务内部错误（事务落库失败整体回滚，前端 toast 后留在弹窗） |

---

#### B2. 九型量表就绪查询

**接口路径：** `GET /api/assessment/test-tasks/scale-status`

**需求追溯：** [需求：F7-4.2.2 B 使用量表] [需求：F7-4.2.5]

**说明：** 九型人格表单的只读「使用量表」字段数据源。返回题库当前可作为组卷来源的量表（已引入且存在启用状态题目），两套并存时取最新引入的一套（判定：`question_batches` 中 scale_key 同值 IMPORT 批次按 created_at 最新，与 B1 取题同源）。题库不就绪时返回 `ready=false`，前端表单仍可打开、提交时报 1804，避免弹窗打开即阻断。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "ready": true,
    "scale_key": "RISO_HUDSON",
    "scale_name": "Riso-Hudson 九型人格量表",
    "active_question_count": 18
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| ready | boolean | 是否就绪（存在已引入且含启用状态题目的量表） |
| scale_key | string | 量表标识 [可选值：RISO_HUDSON/ESSENCE]，未就绪为空串 |
| scale_name | string | 量表名（内置模板名，作只读展示），未就绪为空串 |
| active_question_count | integer | 该量表当前启用状态题目数（全量作答的题量预告），未就绪为 0 |

**错误码：** 1500。

---

### C. 任务行操作与作答链接弹窗

#### C1. 查询作答链接

**接口路径：** `GET /api/assessment/test-tasks/link`

**需求追溯：** [需求：F7-4.3.2] [需求：F7-4.3.4 规则1] [需求：F7-4.3.5]

**说明：** 作答链接弹窗数据源，返回该任务当前（最新一条）链接的全文、状态与生成时间，随行携带任务元信息（任务号、对象、评测类型）。链接全文含令牌原文，仅经此接口与 B1/C2 响应下发；弹窗打开期间不轮询，数据为打开时刻快照（specs §4.3.5）。

**查询参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| task_id | string | 是 | 任务主键（雪花 ID，string 形态） |

**请求示例：**

```
GET /api/assessment/test-tasks/link?task_id=1790000000000000001
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "task_id": "1790000000000000001",
    "task_no": "T202609280001",
    "test_type": "ai_mgmt",
    "staff_name": "张敏",
    "answer_url": "/answer/AbCdEf...43chars",
    "link_status": "valid",
    "generated_at": "2026-09-28 08:30",
    "expires_at": "2026-10-05 08:30"
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 任务主键（string 化） |
| task_no | string | 任务号 |
| test_type | string | 评测类型 [可选值：ai_mgmt/enneagram]（与发起表单评测类型同源，specs §4.3.2） |
| staff_name | string | 被测评人姓名 |
| answer_url | string | 作答链接相对路径（`/answer/{token}`，含一次性令牌原文） |
| link_status | string | 链接状态 [可选值：valid/used/invalid]；仅 valid 时前端复制按钮可点（specs §4.3.4 规则1） |
| generated_at | string | 当前链接生成时刻（重发后为新链接时刻，specs §4.1.4 规则1） |
| expires_at | string | 当前链接到期时刻（generated_at + 7 天有效期常量；已使用/已失效链接为历史到期时刻，前端不展示该列时不消费） |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1801 | 测试任务不存在 |
| 1400 | task_id 缺失或非法 |
| 1500 | 服务内部错误 |

---

#### C2. 重发作答链接

**接口路径：** `POST /api/assessment/test-tasks/resend`

**需求追溯：** [需求：F7-4.1.3 重发作答链接] [需求：F7-4.1.4 规则1/4] [需求：F7-5.3.4 规则2]

**说明：** 作废当前链接（旧链接行置 `invalid`）并生成新的一次性令牌与新链接行，新链接获得新的 7 天有效期。逾期任务重发后任务状态回到 `pending`（specs §4.1.3：已逾期是唯一稳态重发场景）；待作答任务重发仅换链接（作答提醒与链接丢失重生成同入口）；进行中、已完成、已取消任务拒绝重发（1802：进行中会话已绑定旧链接语义上仍在途，已完成链接已消耗，已取消为终态）。完成后响应直接携带新链接，前端以作答链接弹窗展示新链接供复制。注意：specs §4.1.3 重发按钮可见性仅覆盖已逾期，pending 任务的「链接丢失重生成」场景在 UI 无行内入口，与 8.1 术语表「作答提醒与链接丢失重生成共用重发」存在张力，前端实现时与产品澄清入口形态（本接口两种状态均受理）。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| task_id | string | 是 | 任务主键（string 形态） |

**请求示例：**

```json
{ "task_id": "1790000000000000001" }
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "task_id": "1790000000000000001",
    "task_no": "T202609280001",
    "test_type": "ai_mgmt",
    "staff_name": "张敏",
    "answer_url": "/answer/XyZ123...43chars",
    "link_status": "valid",
    "generated_at": "2026-09-30 09:00",
    "expires_at": "2026-10-07 09:00"
  }
}
```

**响应字段：** 同 C1（重发后任务状态变化经前端刷新列表呈现，响应不重复返回任务状态；`link_status` 恒为 `valid`）。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1801 | 测试任务不存在 |
| 1802 | 任务状态不允许重发（status ∈ {in_progress, completed, canceled}） |
| 1400 | task_id 缺失或非法 |
| 1500 | 服务内部错误 |

---

#### C3. 取消测试任务

**接口路径：** `POST /api/assessment/test-tasks/cancel`

**需求追溯：** [需求：F7-4.1.3 取消任务] [需求：F7-4.1.4 规则2]

**说明：** 作废当前作答链接并取消任务（status → `canceled`，终态）。仅待作答、进行中、已逾期任务可取消；已完成不可取消（作答已提交、阅卷链路已启动，specs §4.1.3）。进行中且阅卷在途的取消只作废链接与阻断后续轮询，在途阅卷按 5.2 兜底自然收敛、结果照常落库（specs §4.1.3 与 §5.2.4 规则4：阅卷状态列如实推进）。前端二次确认文案含任务号与对象姓名（消费 C1/A1 行数据组装，无接口依赖）。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| task_id | string | 是 | 任务主键（string 形态） |

**请求示例：**

```json
{ "task_id": "1790000000000000001" }
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "task_id": "1790000000000000001",
    "status": "canceled"
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| task_id | string | 任务主键（string 化） |
| status | string | 取消后的任务状态，恒为 `canceled` |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1801 | 测试任务不存在 |
| 1802 | 任务状态不允许取消（status=completed，或并发重复取消时已为 canceled） |
| 1400 | task_id 缺失或非法 |
| 1500 | 服务内部错误 |

---

### D. 复用接口（不新增）

| 接口 | 来源 | 消费场景 |
|------|------|---------|
| `GET /api/staffs` | P2_SYS_001 | 发起弹窗测评对象下拉（弹窗打开加载首屏，键入异步模糊搜索）；上游不可达返 1305，弹窗内错误态与重试入口（specs §4.2.3 人员搜索，与 F6 同款降级） |
| `GET /api/dimensions/tree` | P2_DIM_001 | AI 管理能力表单子能力复选框组（取 AI_MGMT 模块 enabled 子能力，随维度配置启停动态渲染） |

---

## 4. 非页面功能契约

specs 第 5 章的三项非页面功能无新增 HTTP 面，契约以 Asynq 任务、service 方法与常量承载。

### 4.1 任务类型

```go
// worker/task 内定义并注册。
const TypeTestGrade = "assessment:test-grade" // AI 阅卷，payload 携任务主键
const TypeTestExpireTick = "assessment:test-expire-tick" // 逾期判定，每分钟触发（scheduler 注册）
```

**需求追溯：** specs §5.2.1（事件驱动）、§5.3.1（定时任务）。

队列归属（specs §5.2.4 规则3）：`TypeTestGrade` 走 **default 队列**（与 batch/extract 分流，复用 task 包队列常量单点 `QueueDefault`）；`TypeTestExpireTick` 同走 default 队列（tick 类分钟级任务，与 batch-tick 同款）。scheduler 注册沿用既有每分钟 cron 范式：`scheduler.go` 内 `s.Register(healthCheckCron, asynq.NewTask(task.TypeTestExpireTick, nil))` 一行（specs §5.3.1「每分钟 tick 与 F6 同通道，实现期归一」的落地形态：独立同频任务，与 batch-tick 并列注册，不混入其 handler）。

### 4.2 任务 payload

**assessment:test-grade：**

```json
{
  "task_id": "1790000000000000001"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| task_id | string | 是 | 任务主键（雪花 ID 十进制字符串）；缺失、非数字或 ≤ 0 时 handler 记 ERROR 后返回 nil 丢弃任务（构造侧确定性错误，与既有任务同款处置） |

**assessment:test-expire-tick：** 无 payload（判定依据为链接到期时间与当前时刻）。

### 4.3 assessment:test-expire-tick handler 行为约定

```
输入:  无
流程:  1. 扫描 status=pending 且当前有效链接 expires_at < now 的任务
          （进行中任务不参与命中：F8 会话上报后 status 已为 in_progress，
          5.3.4 规则1 的顺延由状态机天然承载，无需额外会话判定）
       2. 逐任务推进：status → expired，当前有效链接行 status → invalid
          （条件更新 WHERE status='pending' 守卫，幂等：已推进/并发双触发
          affected=0 直接跳过，specs §5.3.4 规则3 单任务至多一次逾期推进）
       3. 批次化单 SQL 或分批执行（任务量级：百人级运营场景下未作答任务
          常态个位数十位，无分批压力）
返回:  err == nil → 成功（含零命中）
       err != nil → 交 Asynq 任务级重试（DB 读失败、推进失败）
超时:  任务级超时 60s（与 batch-tick 同量级，纯 DB 读写）
日志:  推进条数记 INFO；失败记错误摘要（任务号粒度）
```

**需求追溯：** specs §5.3.2 步骤1-3、§5.3.4 规则1/3、§5.3.5。

逾期判定延迟一个 tick 无业务损失（specs §5.3.5），tick 失败交任务级重试自然补偿。

### 4.4 assessment:test-grade handler 行为约定（AI 阅卷）

```
输入:  payload{task_id}
流程:  1. 读任务行；grading_status ∈ {scored, degraded} 时直接返回 nil（终态
          幂等守卫，重复投递/重试不重复阅卷）。waiting/grading 均继续执行：
          CompleteTask 在投递事务内已置 grading（§4.6），若守卫拦非 waiting
          态阅卷将永不执行；waiting 为作答未提交形态（正常无投递路径），
          防御性放行，评分落库由唯一索引 upsert 幂等收敛（specs §5.2.4
          规则1）
       2. 组装阅卷上下文（进程内读取，不落任何中间表）：
          a. 题目快照：question_ids 展开读 questions 表（快照含题目 ID，
             文本以题库行现读为准——题目软删后 Unscoped 读取，编辑后读
             当前文本；specs §5.2.3「与题库实时状态隔离」指取题集合以
             快照为准，文本读取口径见 §4.6 实现注记）
          b. 作答对话全过程：F8 落库的作答记录（answer 域，未建前本任务
             无触发路径——作答提交归 F8，链路闭合依赖 F8 落地）
          c. 评分口径：dimensions 表读 AI_MGMT 子能力（ai_mgmt）的
             weight/anchor/include_overview（specs §5.2.2 步骤2 评分口径
             仅 AI 管理子能力）；enneagram 不取型别维度口径，量表题自含
             计分键
       3. 调用阅卷 LLM（走独立装配的专用 client，见 §4.5）：
          - ai_mgmt：逐子能力产出分数（0-100 整数）与理由
          - enneagram：产出主型、翼型、9 型倾向分布（百分比）与判定依据
       4. 结果落库（单事务）：
          a. ai_mgmt：逐子能力写 dimension_scores 行（source=active_test，
             token_name=staff_name，period 按阅卷执行时读取的当前评估周期
             配置（assessment_configs.period）以任务创建时刻推算窗口，口径
             同 pipeline.CurrentPeriodWindow：同人同窗口与跑批聚合行的
             period 双界精确匹配天然对齐（TECH_005 能力5 聚合域），周期
             配置变更后历史行不重算（与 DIM 规则1 生效时机一致）；
             evidence_json 同构携带口径摘要——weight/in_overview 快照，
             TECH_005 §6 跨 Feature 契约）；随后自调 scorer.Aggregate
             刷新该人该周期聚合行（specs §5.2.2 步骤5 汇入画像；聚合刷新
             失败仅记 ERROR 不阻断终态推进，由下个任务或下期跑批自然收敛，
             specs §5.2.5 画像汇入失败行）
          b. enneagram：写 assessment_test_results 判型结果行（主型、翼型、
             分布 JSON、依据；参考性口径不进 dimension_scores 聚合，
             specs §5.2.4 规则2 与 P2_DIM_001 既定约束）
       5. grading_status → scored；更新 finished 侧统计（无批次计数，
          单任务粒度）
返回:  err == nil → 成功
       err != nil → 交 Asynq 任务级重试（30s 基准倍增退避，worker server
       既有 RetryDelayFunc）
超时:  任务级超时 300s（specs §5.2.4 规则3 初值；单任务阅卷为一次 LLM
       调用量级）；重试耗尽 → grading_status → degraded，保留作答数据
       与统计上下文，不产出告警信号（specs §5.2.5：单人任务粒度无批次
       聚合告警语义），WARN 日志记任务号
幂等:  同一任务至多一份有效评分（specs §5.2.4 规则1）：dimension_scores
       行按唯一索引 upsert 收敛；assessment_test_results 按
       uk_result_task 唯一索引 upsert；重试重复触发收敛到同一份结果
日志:  记任务号与错误摘要，禁止输出 prompt 与作答对话原文
```

**需求追溯：** specs §5.2.2 步骤1-6、§5.2.4 规则1/2/4、§5.2.5 异常表。

**取消任务的在途阅卷：** 任务被取消（status=canceled）但阅卷已在途时照常执行到终态（specs §5.2.4 规则4）：作答已提交的取消任务评分照常落库、画像汇入照常、grading_status 如实推进 scored/degraded；作答未提交的取消任务停留 waiting（无 grade 任务投递路径）。

### 4.5 阅卷专用 LLM client 与常量

| 常量 | 初值 | 说明 |
|------|------|------|
| 阅卷 LLM client Timeout | 240s | 独立装配的专用 client（与评估 180s、出题 120s 的专用 client 模式同构，specs §5.2.4 规则3），小于任务级超时 300s 保证重试边界自洽；并发 gate 独立（在飞上限 4，与全局/评估/出题通道隔离） |
| linkTTL | 7 天 | 链接有效期（specs §4.1.4 规则4 初值），自链接生成时刻起算，包内常量 |
| testGradeMaxRetry | Asynq 默认 25（投递侧不收紧） | 阅卷为轻任务（单 LLM 调用），沿用默认重试预算；耗尽落 degraded 终态 |

三者随源码发版，在线配置不在本期范围（specs §4.1.4 规则4、§5.2.4 规则3）。

### 4.6 作答提交回调契约（对 F8 的服务契约）

specs §5.2.2 步骤1 的作答提交落库与阅卷投递归 F8 提交接口，本域提供 service 方法承载事务内三步（表与任务类型归属本域，状态机单点）：

| 方法 | 签名（概念形，ctx 略） | 语义 | 需求追溯 |
|------|----------------------|------|---------|
| CompleteTask | (taskID int64) → error | 员工作答提交成功的事务内推进：任务 status → completed（in_progress/pending 均可入，pending 放行为 §1.6 声明的防御性扩展，条件更新守卫）、当前有效链接 → used、grading_status → grading 并投递 `assessment:test-grade` 任务；三步与 F8 的作答记录落库同事务，投递失败整体回滚（specs §5.2.5 第二行：不存在提交成功但阅卷未投递的中间态） | specs §5.2.2 步骤1 |
| StartSession | (taskID int64) → error | F8 会话上报：员工进入作答页建立会话时任务 pending → in_progress（条件更新守卫，幂等）；F8 未建前无调用路径 | specs §6.2 待作答→进行中 |

F8 侧的令牌校验、作答页数据组装、提交接收接口归其自身 interface 设计，本域不预定义。

**实现注记（题目文本读取口径）：** 任务快照固化的是题目 ID 列表（specs §5.1.4 规则2），题目文本经 ID 现读 questions 表（Unscoped 含软删行）。题库编辑会改变文本内容，但快照 ID 集合不变，作答与阅卷的取题集合以快照为准；文本现读是当前实现口径，若后续要求文本级冻结，扩展为快照携带题目全文（04 文档 §3.1 预留说明）。

---

## 5. 错误码

新增 18xx 段共 5 个码；人员检索复用既有 1305，子能力维度读取复用 1201。

| 错误码 | 常量 | 含义 | 使用场景 |
|--------|------|------|---------|
| 1801 | TestTaskNotFound | 测试任务不存在 | C1/C2/C3 的 task_id 查无记录 |
| 1802 | TestTaskStatusInvalid | 测试任务状态不允许该操作 | 取消（completed/canceled）、重发（in_progress/completed/canceled）的前置状态校验失败 |
| 1803 | TestDimensionQuestionsEmpty | 子能力无可用题目 | B1 发起 ai_mgmt：勾选的某子能力启用题数为 0（specs §4.2.4 规则1） |
| 1804 | TestScaleNotReady | 九型量表未就绪 | B1 发起 enneagram：未引入或全部停用（specs §4.2.4 规则2） |
| 1805 | TestStaffInvalid | 发起对象人员无效 | B1 提交时 staff_id 经上游校验不存在或上游不可达（specs §5.1.5 第一行） |

---

## 6. 与相邻域的边界

- **对 P2_QBN_001（题库）**：组卷只读消费（按 source/status/scale_key/dimension_id 取启用题）；写路径仅一处——任务创建事务内对快照题目 `reference_count` +1（specs §5.1.2 步骤4，QBN §7.2 被依赖契约）。取消/逾期/降级不回退计数（计数语义为累计指派）。
- **对 P2_DIM_001（维度）**：只读消费 AI_MGMT 子能力启用集合（发起表单复选框、阅卷评分口径）与 ENNEAGRAM 参考性标记；不写维度表。
- **对 P2_TECH_005（评分/聚合）**：ai_mgmt 阅卷写 `dimension_scores` 行（source=active_test，evidence_json 同构携带口径摘要）后自调 Aggregate 刷新聚合；enneagram 判型结果不进聚合（参考性口径）。TECH_005 不感知阅卷时机。
- **对 P2_ASM_001（F6 批次）**：复用页面骨架、发起弹窗骨架、10 秒轮询通道与队列常量；两域数据互不交叉（批次是对话分析、任务表是主动测试，物理隔离 specs §1.2）。
- **对 F8（员工作答，未建）**：本域产出一次性令牌与作答 URL 形态（`/answer/{token}`）、提供 CompleteTask/StartSession 服务契约（§4.6）；令牌校验与作答页接口归 F8。F8 未落地前，进行中/已完成/阅卷链路无触发路径，状态机停留在待作答与逾期两态，口径自洽（specs §5.3.4 规则1）。
- **对 F9/F11（未建）**：评分行与判型结果供 F9 画像消费；任务逾期态势（status=expired 计数）供 F11 工作台消费，本功能只产出数据。
- **对 P2_SYS_001（配置）**：复用 `GET /api/staffs` 人员检索与集成密钥解析；阅卷 LLM 走当前排他启用模型（llm.EnabledModelProvider 既有通道）。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1 两 tab（查询、筛选重置、发起入口、作答链接、重发、取消、结果跳转、轮询刷新）→ A1/A2/B1/C1/C2/C3；4.2 发起弹窗（两表单、提交、取消、人员搜索、量表只读展示）→ B1/B2 与复用 D；4.3 链接弹窗（查看、复制、关闭）→ C1（复制为前端剪贴板操作，无接口）。
- [x] 字段定义与 specs 一致：4.1.2 查询/显示字段、4.2.2 表单字段、4.3.2 弹窗字段在请求/响应结构一一对应；三处适配已声明（任务号前缀 T/E 落 task_no、九型「判型状态」为前端 i18n 映射取值同源、链接弹窗评测类型字段 test_type 同源）。
- [x] 业务规则在接口与任务契约中落地：任务与链接一对一演进（C2 重发语义）、链接失效与任务状态联动（§4.3/§4.6）、同人多任务并存（B1 无重复校验）、逾期判定口径（§4.3 tick + 7 天常量）、阅卷幂等（§4.4 唯一索引 upsert）、物理隔离不重复计分（§4.4 ai_mgmt 走 active_test 源、enneagram 独立结果表）。
- [x] 状态定义与 specs 第 6 章一致：任务五态、链接三态、阅卷四态的转换条件落 §4.3/§4.4/§4.6 与 04 文档各表业务规则段；取消任务阅卷照常推进（§4.4）。
- [x] 权限规则一致：全部接口挂 JWT，无角色差异（specs §2.2），员工侧无 HTTP 面（§1.3）。
- [x] 非页面功能全覆盖：5.1 任务创建与链接生成 → B1 事务契约；5.2 AI 阅卷 → §4.4；5.3 逾期判定 → §4.3。
- [x] 技术层偏差已声明：轮询探针承载（§1.4）、作答回调契约归属（§1.5）、CompleteTask pending 防御性放行（§1.6）、题目文本现读口径（§4.6 注记）。
- [x] 接口与数据库一致性：A1/B1/C1/C2/C3 响应字段与 [04_model_interface.md](04_model_interface.md) 三表列一一对应，`id`/`task_id`/`dimension_ids` 的 string 化双层覆盖（§2.4）。

---

## 8. 不涉及的设计

- 员工作答页全部接口（令牌校验、作答数据、提交接收）：归 F8（specs §1.3、§1.5）。
- 阅卷结果的画像呈现与结果详情接口：归 F9（本功能只写数）。
- 工作台逾期态势的聚合与呈现接口：归 F11。
- 批量发起（多人/全员对象输入）：specs §4.2.4 规则3 明确不提供。
- 链接有效期的在线配置：specs §4.1.4 规则4 明确不在本期范围（包内常量）。
- 推送通道（IM/邮件系统对接）：specs §7.1 明确首期人工渠道。
- 发起弹窗评测类型卡片第三态（对话分析）：F6 已建，本功能不触碰其接口。

---

**文档版本：** v1.3（v1.3 监理模式三修复：§2.5 与 A1/B1/C1/C2 时间字段改直出 yyyy-MM-dd HH:mm 本地时区口径对齐代码事实；§4.4 2c enneagram 分支改为不消费维度口径）
**最后更新：** 2026-09-28
**作者：** lixuetao
