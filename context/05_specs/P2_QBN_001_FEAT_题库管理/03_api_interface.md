# 题库管理 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| 所属 Feature | P2_QBN_001_FEAT_题库管理 |
| 文档版本 | v1.0 |
| 创建日期 | 2026-09-23 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书.md](01_功能需求规格说明书.md)、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md) |

---

## 一、基础信息

**模块代号：** QBN（题库域）

**接口协议：** HTTP，简化 GET/POST 模式（GET 仅查询，POST 仅变更，POST 以路径区分动作）

**Base URL：** `/api`（无版本前缀，规则文件 §2.1）

**认证方式：** Bearer JWT，全部接口挂 `_authenticated` 受保护路由组（specs 2.3）

**模块前缀说明：** 遵循规则文件 §1.9，接口路径以业务实体为根（`/api/questions`、`/api/question-batches`、`/api/question-generations`、`/api/scales`），模块代号 QBN 仅用于错误码段（17xx）与文档分区，不进路径。

---

## 二、核心设计原则

| 方法 | 用途 | 请求体 | 适用场景 |
|------|------|--------|---------|
| GET | 查询数据 | 无 | 列表、批次卡、批次明细、详情、量表候选、生成进度 |
| POST | 变更数据 | 有 | 生成发起、量表引入、确认入库、作废、编辑、启停、删除、重新提交、取消生成 |

**雪花 ID 传输约定（规则文件 §1.2 强制）。** 凡承载雪花 ID 的字段（题目 ID、批次 ID、generation ID、维度 ID）JSON 序列化一律带 `,string`，前端类型定义为 string。下文响应示例中 ID 均以字符串字面量呈现。

**统一响应格式（规则文件 §2.2）：**

```json
{ "code": 0, "message": "ok", "data": { } }
```

`code = 0` 成功，非 0 为业务错误码。`data` 字段无 `omitempty`，成功无载荷时为 `null`。分页结构 `{list, total, page, page_size}` 作 data 透传（规则文件 §2.3）。

**specs 方法收敛。** specs 2.3 鉴权矩阵中的 PUT/DELETE 按规则文件 §2.1 收敛为 POST（技术实现层面规则文件优先），编辑、启停、删除均 POST 以路径区分动作，与 account、dimension 域先例一致。

**生成异步化的边界。** specs 4.3.3 写「同步 LLM 生成请求」，但 LLM 生成 5~30 题经底座并发限制（在飞上限 4）排队后耗时可达分钟级，而 HTTP server 的 WriteTimeout 硬编码 30s（架构文档 4.2，hr-backend CLAUDE.md）会掐断任何长连接。specs 4.3.4 规则 3 明确「进度回传机制（流式或轮询）由接口数据设计阶段确定」，据此落地为 Asynq 异步任务 + 前端轮询：交互语义不变（页面停留等待、离开放弃本批、失败整批作废），仅进度经 `GET /api/question-generations/{id}` 回传。

---

## 三、接口列表

需求追溯编号 F-001 ~ F-013 对应 specs 第 4 章页面功能点。

| 序号 | 方法 | 路径 | 用途 | 需求追溯 |
|------|------|------|------|---------|
| 1 | GET | /api/questions | 题库列表查询（分 tab） | F-001 题库查询 |
| 2 | GET | /api/questions/{id} | 题目详情（查看弹窗） | F-002 查看题目 |
| 3 | POST | /api/questions/update | 题目编辑 | F-003 编辑题目 |
| 4 | POST | /api/questions/toggle-status | 题目启停 | F-004 停用/启用题目 |
| 5 | POST | /api/questions/resubmit | 题目重新送审 | F-005 重新提交 |
| 6 | POST | /api/questions/delete | 题目删除 | F-006 删除题目 |
| 7 | GET | /api/question-batches | 待审核批次卡列表 | F-007 批次卡加载 |
| 8 | GET | /api/question-batches/{id}/questions | 批次题目明细 | F-008 审核视图加载 |
| 9 | POST | /api/question-batches/{id}/confirm | 批次确认入库 | F-009 确认入库 |
| 10 | POST | /api/question-batches/{id}/void | 批次作废 | F-010 作废批次 |
| 11 | GET | /api/scales | 量表候选列表（引入弹窗） | F-011 量表引入弹窗 |
| 12 | POST | /api/scales/import | 引入九型量表 | F-012 引入量表 |
| 13 | POST | /api/question-generations/create | 发起 AI 管理题生成 | F-013 开始生成 |
| 14 | GET | /api/question-generations/{id} | 生成进度轮询 | F-013 生成进行态/完成态/失败态 |
| 15 | POST | /api/question-generations/{id}/cancel | 取消生成（放弃本批） | F-013 生成进行态离开放弃（specs 4.3.3 返回题库、4.3.4 规则 1），定义见 §3.14 尾部 |

**辅助说明：**

- 生成页与题库页两处维度下拉同复用 `GET /api/dimensions/tree`（P2_DIM_001 已落地）：生成页多选卡片取 AI_MGMT 模块 enabled 子能力，题库页查询区按 tab 取对应模块节点（AI tab 取 AI_MGMT，SCALE tab 取 ENNEAGRAM 型别），无新接口。
- 审核视图的标记/取消标记/全部不标是纯前端状态（specs 4.2.3），确认入库时一次性提交，无逐题接口。
- 完成态「前往审核」携带批次 ID 前端路由跳转，无接口。

---

### 3.1 题库列表查询

**接口路径：** `GET /api/questions`

**需求追溯：** F-001 题库查询（specs 4.1.2 A/B）

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| source | string | 是 | - | 来源 tab：AI / SCALE（对应 AI 管理题库、九型人格量表 tab） |
| dimension_id | string | 否 | 空 | 维度筛选。空为全部维度；AI tab 传 AI_MGMT 子能力维度 ID，SCALE tab 传型别维度 ID |
| status | string | 否 | 空 | 状态筛选：ACTIVE / DISABLED / REJECTED，空为全部状态 |
| keyword | string | 否 | 空 | 按题目编号与情境描述/题项陈述全文模糊搜索 |
| page | integer | 否 | 1 | 页码 |
| page_size | integer | 否 | 20 | 每页数量，服务端钳制上限 100 |

**请求示例：**

```
GET /api/questions?source=AI&status=ACTIVE&keyword=授权&page=1&page_size=10
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1785000000000000101",
        "question_no": "Q-AG-0001",
        "source": "AI",
        "dimension_id": "1780000000000000100",
        "dimension_name": "授权与分工",
        "answer_mode": "CHAT",
        "status": "ACTIVE",
        "summary": "下属使用 AI 工具完成季度规划…",
        "updated_at": "2026-09-23T10:31:24Z"
      },
      {
        "id": "1785000000000000102",
        "question_no": "Q-Scale-0001",
        "source": "SCALE",
        "dimension_id": "1780000000000000201",
        "dimension_name": "调停型",
        "answer_mode": "LIKERT5",
        "status": "ACTIVE",
        "summary": "我常常能察觉到别人想要什么…",
        "updated_at": "2026-09-22T18:00:00Z"
      }
    ],
    "total": 32,
    "page": 1,
    "page_size": 10
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| list[].id | string | 题目 ID（雪花，string 化） |
| list[].question_no | string | 题目编号 |
| list[].source | string | 来源：AI / SCALE |
| list[].dimension_id | string | 所属维度 ID（雪花，string 化） |
| list[].dimension_name | string | 所属维度名。维度已停用/软删时回传存量名称，前端照常展示（specs 4.1.2 E） |
| list[].answer_mode | string | 作答方式派生：AI→CHAT（对话作答），SCALE→LIKERT5（Likert 5 级），由 source 派生不落库 |
| list[].status | string | ACTIVE / DISABLED / REJECTED |
| list[].summary | string | 题目摘要。AI 题为 scenario 前 50 字符服务端截取（specs 4.1.2 B），量表题为题项陈述截取；前端悬浮全文走详情接口 |
| list[].updated_at | string | 更新时间，默认排序字段（specs 4.1.5） |

**业务规则：**

- 恒排除 PENDING 态：待审核题目仅在批次审核视图可见（specs 4.3.4 规则 2）。
- 排序固定 `updated_at DESC, id DESC`，不提供排序参数（specs 4.1.5）。
- keyword 经 likeescape.EscapeLike（internal/pkg/likeescape）转义后对 question_no 与 scenario 做 `LIKE ? ESCAPE '\'` OR 匹配（规则文件 §1.11）。
- dimension_id / status 取值与 source tab 的域一致性由前端保证（AI tab 只出 AI_MGMT 维度、SCALE tab 只出 ENNEAGRAM 型别），服务端不强制校验跨 tab 组合。

---

### 3.2 题目详情

**接口路径：** `GET /api/questions/{id}`

**需求追溯：** F-002 查看题目（specs 4.1.2 D）

**路径参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| id | string | 题目 ID（雪花，string 化） |

**请求示例：**

```
GET /api/questions/1785000000000000101
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1785000000000000101",
    "question_no": "Q-AG-0002",
    "source": "AI",
    "dimension_id": "1780000000000000110",
    "dimension_name": "风险与担责",
    "answer_mode": "CHAT",
    "status": "REJECTED",
    "scenario": "AI 助手依据历史报价自动向客户承诺了一项超出你授权范围的折扣…",
    "requirement": "请选择最符合你处理方式的选项… A. … B. … C. … D. …",
    "focus_point": "风险定级准确、担责边界清晰、客户沟通与内部回溯双线处理。",
    "reject_reason": "AI 建议存在明显性别与生育偏见…",
    "batch_id": "1785000000000000001",
    "batch_no": "#G0921",
    "reference_count": 3,
    "version": 2,
    "created_at": "2026-09-21T10:31:24Z",
    "updated_at": "2026-09-23T09:12:00Z"
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 题目 ID |
| question_no | string | 题目编号 |
| source | string | AI / SCALE |
| dimension_id | string | 所属维度 ID |
| dimension_name | string | 所属维度名（存量回传，维度删除后仍可展示） |
| answer_mode | string | CHAT / LIKERT5（source 派生） |
| status | string | PENDING / ACTIVE / DISABLED / REJECTED |
| scenario | string | 情境描述（AI）/ 题项陈述（量表）全文 |
| requirement | string | 作答要求（AI，含选项全文）/ 作答方式说明（量表） |
| focus_point | string | 考察点（AI）/ 计分键（量表） |
| reject_reason | string | 驳回原因，仅 REJECTED 非空（specs 4.1.2 D） |
| batch_id | string | 当前所属批次 ID |
| batch_no | string | 当前所属批次号 |
| reference_count | integer | 被引用次数（specs 4.1.2 D 被引用次数） |
| version | integer | 乐观锁版本号，编辑/启停/删除/重新提交提交时回传 |
| created_at | string | 入库时间（specs 4.1.2 D） |
| updated_at | string | 更新时间 |

**业务规则：**

- 目标题目不存在或已软删除返回 1701 QuestionNotFound。
- PENDING 态题目可查（批次审核视图复用本接口取全文），列表 tab 已隔离。

---

### 3.3 题目编辑

**接口路径：** `POST /api/questions/update`

**需求追溯：** F-003 编辑题目（specs 4.1.2 E、4.1.3 编辑题目）

**请求参数：**

```json
{
  "id": "1785000000000000101",
  "dimension_id": "1780000000000000110",
  "scenario": "你的下属小林第一次用 AI 工具辅助撰写季度规划…",
  "requirement": "请选择最符合你处理方式的选项… A. … B. … C. … D. …",
  "focus_point": "是否识别出可衡量性缺失、是否给出对齐节奏",
  "version": 2
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| id | string | 是 | 雪花 ID | 目标题目 ID |
| dimension_id | string | 是 | 须为当前启用的 AI_MGMT 子能力维度（specs 4.1.2 E：改选限当前启用集合） | 所属维度。题目原维度已停用时前端保留原值展示，提交必须落在启用集合内 |
| scenario | string | 是 | 1~1000 字符 | 情境描述（specs 4.1.2 E、8.3 偏离） |
| requirement | string | 是 | 1~2000 字符 | 作答要求（specs 4.1.2 E、8.3 偏离） |
| focus_point | string | 是 | 1~500 字符 | 考察点（specs 4.1.2 E） |
| version | integer | 是 | 正整数 | 乐观锁版本号，取自详情响应 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1785000000000000101",
    "version": 3,
    "updated_at": "2026-09-23T11:00:00Z"
  }
}
```

**业务规则：**

- 仅 source=AI 且 status ∈ {ACTIVE, DISABLED} 可编辑（specs 4.1.3：已驳回题修正并入重新提交，量表题不可编辑）。违反返回 1707 QuestionNotEditable。
- dimension_id 须命中当前启用的 AI_MGMT 维度集合，未命中返回 1400 BadRequest（specs 4.1.2 E）。
- 乐观锁校验：version 不一致返回 1713 QuestionVersionConflict（specs 规则 9 并发冲突）。
- 目标不存在或已软删除返回 1701。
- 服务端忽略任何尝试修改 question_no/source/status/batch_id/reference_count 的多余字段。

---

### 3.4 题目启停

**接口路径：** `POST /api/questions/toggle-status`

**需求追溯：** F-004 停用/启用题目（specs 4.1.3 停用/启用题目）

**请求参数：**

```json
{
  "id": "1785000000000000101",
  "target_status": "DISABLED",
  "version": 3
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| id | string | 是 | 雪花 ID | 目标题目 ID |
| target_status | string | 是 | ACTIVE / DISABLED | 目标状态。与当前状态相同返回 1400 |
| version | integer | 是 | 正整数 | 乐观锁版本号 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1785000000000000101",
    "status": "DISABLED",
    "version": 4,
    "updated_at": "2026-09-23T11:05:00Z"
  }
}
```

**业务规则：**

- 仅 status ∈ {ACTIVE, DISABLED} 可启停互切（specs 6.3），REJECTED/PENDING 题无此操作。违反返回 1708 QuestionStatusInvalid。
- 停用语义：新测试不取用，历史引用保留（specs 6.1）；启用恢复可指派。
- 乐观锁校验同编辑接口，冲突返回 1713。

---

### 3.5 题目重新送审

**接口路径：** `POST /api/questions/resubmit`

**需求追溯：** F-005 重新提交（specs 4.1.3 重新送审、规则 6）

**请求参数：**

```json
{
  "id": "1785000000000000101",
  "dimension_id": "1780000000000000110",
  "scenario": "AI 助手依据历史报价自动向客户承诺了一项超出你授权范围的折扣…（修正后）",
  "requirement": "请选择最符合你处理方式的选项…（修正后）",
  "focus_point": "风险定级准确、担责边界清晰…（修正后）",
  "version": 2
}
```

**请求字段说明：** 同 3.3 编辑接口（编辑弹窗预载现有内容，修正后提交，specs 规则 6 修正与复审一体）。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1785000000000000101",
    "status": "PENDING",
    "batch_id": "1785000000000000009",
    "batch_no": "#R0923",
    "version": 3
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| status | string | 回到 PENDING（specs 6.3 已驳回→待审核） |
| batch_id / batch_no | string | 归入的重新送审批次（并入既有或新建，specs 规则 6） |

**业务规则：**

- 仅 source=AI 且 status=REJECTED 可重新提交（量表题驳回仅可删除，specs 规则 6）。违反返回 1707。
- 修正内容校验同编辑接口；dimension_id 限当前启用 AI_MGMT 维度集合。
- 事务：题目文本更新 + status 置 PENDING + reject_reason 保留（复审再驳回时覆盖）+ 归批（存在 source=AI、batch_type=RESUBMIT、status=PENDING 的批次则 batch_id 改挂并入，否则新建 RESUBMIT 批次，question_count+1 / +N）。
- Toast 提示去向「已提交复审，进入重新送审批次」由前端按成功响应渲染（specs 4.1.5）。
- 乐观锁校验，冲突返回 1713。

---

### 3.6 题目删除

**接口路径：** `POST /api/questions/delete`

**需求追溯：** F-006 删除题目（specs 4.1.3 删除题目、规则 4）

**请求参数：**

```json
{
  "id": "1785000000000000104",
  "version": 5
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 目标题目 ID |
| version | integer | 是 | 乐观锁版本号 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": null
}
```

**业务规则：**

- 待审核题目不可删（specs 4.1.3：待审核题目在批次中管理）。status=PENDING 返回 1708。
- 被引用保护（specs 规则 4）：reference_count > 0 返回 1705 QuestionReferenced，前端提示「该题已被测试引用，只可停用」。
- 逻辑删除置 deleted_at（specs 6.1 已删除终态）。
- 量表题删除与作废等同释放引入位（specs 规则 8），无额外限制。
- 乐观锁校验，冲突返回 1713。

---

### 3.7 待审核批次卡列表

**接口路径：** `GET /api/question-batches`

**需求追溯：** F-007 批次卡加载（specs 4.1.2 C、4.1.5 前端自动流程）

**查询参数：** 无（返回全部 status=PENDING 批次）

**请求示例：**

```
GET /api/question-batches
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1785000000000000001",
        "batch_no": "#G0921",
        "title": "授权与分工 ×2",
        "source": "AI",
        "batch_type": "GENERATE",
        "question_count": 12,
        "created_at": "2026-09-21T22:15:00Z"
      },
      {
        "id": "1785000000000000002",
        "batch_no": "#S0921",
        "title": "Riso-Hudson 标准量表",
        "source": "SCALE",
        "batch_type": "IMPORT",
        "question_count": 144,
        "created_at": "2026-09-21T22:30:00Z"
      }
    ],
    "total": 2
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| list[].id | string | 批次 ID（雪花，string 化），开始审核/确认入库/作废的目标 |
| list[].batch_no | string | 批次号，#G/#S/#R+MMdd（specs 4.1.2 C） |
| list[].title | string | 批次标题（specs 4.1.2 C） |
| list[].source | string | AI / SCALE，决定审核重点提示文案（specs 规则 1） |
| list[].batch_type | string | GENERATE / IMPORT / RESUBMIT |
| list[].question_count | integer | 批内题目总数 |
| list[].created_at | string | 生成时间（specs 4.1.2 C 生成时间） |

**业务规则：**

- 仅返回 PENDING 态，CLOSED/VOIDED 不出现在卡区（specs 规则 7、4.1.3 作废批次）。
- 排序 created_at DESC。无待审核批次时返回空 list，前端隐藏批次卡区（specs 4.1.5）。
- 不分页（同时待审核批次为个位数量级）。

---

### 3.8 批次题目明细

**接口路径：** `GET /api/question-batches/{id}/questions`

**需求追溯：** F-008 审核视图加载（specs 4.2.2 A）

**路径参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| id | string | 批次 ID（雪花，string 化） |

**请求示例：**

```
GET /api/question-batches/1785000000000000001/questions
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch": {
      "id": "1785000000000000001",
      "batch_no": "#G0921",
      "title": "授权与分工 ×2",
      "source": "AI",
      "batch_type": "GENERATE",
      "question_count": 12,
      "created_at": "2026-09-21T22:15:00Z"
    },
    "questions": [
      {
        "id": "1785000000000000101",
        "question_no": "Q-AG-0101",
        "dimension_id": "1780000000000000100",
        "dimension_name": "授权与分工",
        "answer_mode": "CHAT",
        "scenario": "AI 给出了一份 12 人团队两周任务分解草案…",
        "requirement": "请选择最符合你处理方式的选项… A. … B. … C. … D. …",
        "focus_point": "识别高风险决策点、给出人机分工原则、保留必要审批节点。",
        "reject_reason": ""
      }
    ]
  }
}
```

**字段说明：**

- batch 结构同 3.7 卡片字段。
- questions[] 含逐题审核所需全文：question_no、dimension_id（string）/dimension_name、answer_mode、scenario、requirement、focus_point（specs 4.2.2 A 审核卡片字段）。reject_reason 恒空串（标记在前端进行，specs 4.2.4 规则 1），预留字段对齐确认入库结构。

**业务规则：**

- 批次不存在返回 1702 QuestionBatchNotFound。
- 批次非 PENDING 态返回 1706 QuestionBatchClosed（前端刷新批次卡区与列表，specs 4.2.4 规则 1）。
- 全量返回不分页（批次最多 144 题，specs 4.2.4 规则 1 单请求无分页）。
- 题目按 question_no 升序（specs 4.2.5）。

---

### 3.9 批次确认入库

**接口路径：** `POST /api/question-batches/{id}/confirm`

**需求追溯：** F-009 确认入库（specs 4.2.3 确认入库、规则 2/3/7）

**请求参数：**

```json
{
  "rejected": [
    {
      "question_id": "1785000000000000102",
      "reason": "情境仅绑定互联网广告销售，场景迁移性差"
    }
  ]
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| rejected | array | 否 | 每项 question_id 须属本批次，reason 必填 1~500 字符 | 被标记驳回的题目与原因。空数组或缺省为全部默认通过（specs 规则 2 未标记默认通过） |
| rejected[].question_id | string | 是 | 雪花 ID | 被驳回题目 ID |
| rejected[].reason | string | 是 | 1~500 字符（specs 规则 3） | 驳回原因 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch_id": "1785000000000000001",
    "batch_status": "CLOSED",
    "admitted_count": 11,
    "rejected_count": 1
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| admitted_count | integer | 入库题数（未标记题，置 ACTIVE），Toast「入库 N 题」（specs 4.2.5） |
| rejected_count | integer | 驳回题数（置 REJECTED） |

**业务规则：**

- 单请求整批提交（specs 4.2.4 规则 1），rejected 之外的批内题目全部置 ACTIVE。
- rejected[].question_id 不属本批次返回 1400；题目数与批次 question_count 对不上（题目被并发操作）按 1713 冲突处理。
- 批次状态须为 PENDING：CLOSED/VOIDED 返回 1706（specs 4.2.4 规则 1、规则 7）。
- 事务：批次置 CLOSED + closed_at、未标记题目批量置 ACTIVE、标记题目批量置 REJECTED 并写 reject_reason。
- 成功后批次关闭，同批不能再改标记（specs 规则 7）。

---

### 3.10 批次作废

**接口路径：** `POST /api/question-batches/{id}/void`

**需求追溯：** F-010 作废批次（specs 4.1.3 作废批次）

**请求参数：** 路径参数 id（批次 ID），无请求体（二次确认由前端删除确认弹窗承载，specs 通用规范 14）。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": null
}
```

**业务规则：**

- 批次状态须为 PENDING，否则返回 1706。
- 作废语义按 batch_type 分流（specs 4.1.3、6.2）：GENERATE/IMPORT 批次批内题目批量软删（不进入题库，释放量表引入位）；RESUBMIT 批次批内题目回退 REJECTED 并保留原驳回原因（题库既有题目不作废）。
- 批次置 VOIDED + voided_at，从待审核卡区消失。
- 事务整批提交，与确认入库互斥（两者都要求 PENDING 前置，先到者胜出，后者收 1706）。

---

### 3.11 量表候选列表

**接口路径：** `GET /api/scales`

**需求追溯：** F-011 量表引入弹窗（specs 4.1.2 F 第一步）

**查询参数：** 无

**请求示例：**

```
GET /api/scales
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "scale_key": "RISO_HUDSON",
        "name": "Riso-Hudson 标准量表",
        "question_count": 144,
        "estimated_minutes": 25,
        "description": "主流九型量表，覆盖 9 型别全维度题项",
        "imported": true
      },
      {
        "scale_key": "ESSENCE",
        "name": "Essence 精简量表",
        "question_count": 108,
        "estimated_minutes": 18,
        "description": "标准量表的缩短版本，作答负担更轻",
        "imported": false
      }
    ]
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| scale_key | string | 量表标识：RISO_HUDSON / ESSENCE |
| name | string | 量表名 |
| question_count | integer | 题目数量（specs 4.1.2 F） |
| estimated_minutes | integer | 预估作答时长（分钟） |
| description | string | 卡片描述 |
| imported | boolean | 已引入标记：存在该量表未逻辑删除的题目（含待审核/启用/已停用/已驳回，specs 规则 8）。前端据此置灰并标注「已引入」 |

**业务规则：**

- 候选清单来自内置模板（见 04 文档 §4.1），接口实时计算 imported（查 questions 表 scale_key + 软删行可见性），引入/作废/删除后状态即时变化。
- 第二步确认页的只读汇总（计分方式、批次说明、将生成的批次号）由前端据本响应组合渲染，批次号按 #S+当日预览展示。

---

### 3.12 引入九型量表

**接口路径：** `POST /api/scales/import`

**需求追溯：** F-012 引入量表（specs 4.1.3 引入九型量表、规则 8）

**请求参数：**

```json
{
  "scale_key": "RISO_HUDSON"
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| scale_key | string | 是 | RISO_HUDSON / ESSENCE | 引入的量表标识 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch_id": "1785000000000000002",
    "batch_no": "#S0923",
    "question_count": 144
  }
}
```

**业务规则：**

- 事务（specs 4.1.3 引入九型量表）：ensure ENNEAGRAM 9 型别维度（不存在则建，04 文档 §4.2）→ 按内置模板批量创建题目（连续分配 Q-Scale 编号，status=PENDING，source=SCALE）→ 建 IMPORT 批次（batch_no 按 #S+MMdd 同日序号规则）。
- 唯一性（specs 规则 8）：该量表已引入（存在未逻辑删除的量表题）返回 1704 ScaleAlreadyImported，前端提示「该量表已引入」并刷新弹窗置灰状态。并发重复引入由事务内行锁串行化拦截：引入事务先对 ensure 的 9 个 ENNEAGRAM 型别维度行 SELECT ... FOR UPDATE（两套量表共享这 9 行，任何并发引入必然触碰同一组行），锁内再判已引入（锁定读读最新已提交数据），后到事务见先到事务已提交的题目行并收 1704；questions 表无 UNIQUE 约束可拦此场景（编号不同、存在性判定跨多行），行锁是唯一串行化锚点。
- 两套量表允许同时在库各自独立成批（specs 规则 8 并存）。
- 成功即形成待审核批次，「进入审核」前端携 batch_id 跳审核视图，「稍后审核」留列表态。

---

### 3.13 发起 AI 管理题生成

**接口路径：** `POST /api/question-generations/create`

**需求追溯：** F-013 开始生成（specs 4.3.3 开始生成）

**请求参数：**

```json
{
  "dimension_ids": ["1780000000000000100", "1780000000000000110"],
  "count": 12
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| dimension_ids | string[] | 是 | 至少 1 项；每项须为当前启用的 AI_MGMT 子能力维度；去重后 ≤ 当前启用数 | 子能力维度多选（specs 4.3.2） |
| count | integer | 是 | 5~30 整数 | 本批生成题数（specs 4.3.2） |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "generation_id": "1785000000000003001",
    "status": "QUEUED"
  }
}
```

**业务规则：**

- 校验通过即建 generation 行（QUEUED）并投递 Asynq 任务（default 队列），接口即时返回 QUEUED，worker 领取置 RUNNING 后逐题生成（specs 4.3.4 规则 3 授权的进度轮询机制），轮询首拍即可见 QUEUED→RUNNING 迁移。
- 维度集合含非启用/非 AI_MGMT 维度返回 1400。
- 大模型未配置（当前无排他启用模型）返回 1709 LLMNotConfigured（specs 7.1 依赖大模型配置页已排他启用一个模型）。
- LLM 生成经调用底座全局并发限制（在飞上限 4，FIFO 排队），多运营并发发起不放大上游压力。
- 接口即时返回，生成结果经 3.14 轮询获取；HTTP 侧维持长连接不可行（WriteTimeout 30s 硬编码），见 §二。

---

### 3.14 生成进度轮询

**接口路径：** `GET /api/question-generations/{id}`

**需求追溯：** F-013 生成进行态/完成态/失败态（specs 4.3.4 规则 1/2/3、4.3.5）

**路径参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| id | string | generation ID（雪花，string 化），取自 3.13 响应 |

**请求示例：**

```
GET /api/question-generations/1785000000000003001
```

**响应示例（进行中）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "generation_id": "1785000000000003001",
    "status": "RUNNING",
    "generated_count": 6,
    "count": 12,
    "current_dimension_id": "1780000000000000100",
    "current_dimension_name": "授权与分工"
  }
}
```

**响应示例（完成）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "generation_id": "1785000000000003001",
    "status": "COMPLETED",
    "generated_count": 12,
    "count": 12,
    "current_dimension_id": "1780000000000000100",
    "current_dimension_name": "",
    "batch_id": "1785000000000000001",
    "batch_no": "#G0921"
  }
}
```

**响应示例（失败）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "generation_id": "1785000000000003001",
    "status": "FAILED",
    "generated_count": 6,
    "count": 12,
    "error_code": "LLM_TIMEOUT"
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| status | string | QUEUED / RUNNING / COMPLETED / FAILED / CANCELED |
| generated_count | integer | 已生成题数（进度「已生成 n/N」，specs 4.3.5） |
| count | integer | 目标题数 |
| current_dimension_id / current_dimension_name | string | 各状态恒返回（无 omitempty）。RUNNING 为当前正在构造的维度（进度提示，specs 4.3.5）；终态保留末次维度 ID、name 恒空串（维度名仅 RUNNING 态回填）；未开始 current_dimension_id 为 "0"、current_dimension_name 为空串 |
| batch_id / batch_no | string | 仅 COMPLETED 返回，完成态展示批次号、「前往审核」跳转目标（specs 4.3.3） |
| error_code | string | 仅 FAILED/CANCELED 返回：LLM_FAILED / LLM_TIMEOUT / CANCELED / INTERNAL，前端映射固定失败文案（specs 4.3.4 规则 3），不透传底层错误串 |

**业务规则：**

- 轮询建议间隔 2s，前端持续轮询直至终态（specs 4.3.4 规则 1 同步等待语义）。
- COMPLETED 即批次已自动进入待审核队列（specs 4.3.4 规则 2），题目 status=PENDING；失败/取消零残留（specs 4.3.4 规则 3）。
- generation 不存在返回 1703 GenerationNotFound。

**取消生成（页内操作，归 3.13 的发起方补充）：**

生成进行态离开页面需二次确认并放弃本批（specs 4.3.3 返回题库、4.3.4 规则 1）。取消动作落库不设独立接口路径，由前端在二次确认后调用：

**接口路径：** `POST /api/question-generations/{id}/cancel`

**请求参数：** 路径参数 id，无请求体。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": null
}
```

**业务规则：**

- 仅 QUEUED/RUNNING 可取消，终态调用幂等成功（已终态直接返回 ok，不报错）。
- 协作式取消：置 status=CANCELED，worker 每题生成前检查到即停止并清空 staging，整批放弃零残留（specs 4.3.4 规则 1 放弃本批）。

---

## 四、错误码规范

错误码集中定义于 [hr-backend/internal/pkg/errcode/errcode.go](../../../hr-backend/internal/pkg/errcode/errcode.go)，规则文件 §2.4 规定千位 1 标识业务错误。account 10xx、system 11xx（避让后实际 1101-1102）、dimension 12xx、config 13xx、assessment batch 16xx 已占用，题库域使用 **17xx 段**（百位 7 区分域，接续既有段位序列）。

### 4.1 questionbank 域错误码

| 错误码 | 常量 | 含义 | 触发场景 |
|--------|------|------|---------|
| 1701 | QuestionNotFound | 题目不存在 | 详情/编辑/启停/重新提交/删除目标题目不存在或已软删除 |
| 1702 | QuestionBatchNotFound | 批次不存在 | 批次明细/确认入库/作废目标批次不存在 |
| 1703 | GenerationNotFound | 生成会话不存在 | 进度轮询/取消的 generation 不存在 |
| 1704 | ScaleAlreadyImported | 量表已引入 | 引入已存在未逻辑删除题目的量表（specs 规则 8） |
| 1705 | QuestionReferenced | 题目已被引用 | 删除 reference_count > 0 的题目（specs 规则 4） |
| 1706 | QuestionBatchClosed | 批次已关闭或已作废 | 确认入库/作废/明细遇非 PENDING 批次（specs 4.2.4 规则 1、规则 7） |
| 1707 | QuestionNotEditable | 题目不可编辑 | 编辑/重新提交作用于量表题或状态不符的题目（specs 规则 5/6） |
| 1708 | QuestionStatusInvalid | 题目状态非法 | 状态转换前置校验失败（如删除待审核题、启停已驳回题，specs 6.3） |
| 1709 | LLMNotConfigured | 大模型未配置 | 发起生成时无排他启用模型（specs 7.1） |
| 1713 | QuestionVersionConflict | 题目已变更 | 乐观锁版本不一致，并发冲突（specs 规则 9） |

> 段内预留 1710-1712 供域内扩展（如生成参数校验细分）。

### 4.2 通用错误码（复用）

| 错误码 | 常量 | 含义 |
|--------|------|------|
| 0 | Success | 成功 |
| 1400 | BadRequest | 请求参数格式错误（含字段超长、维度集合非法、rejected[].question_id 不属本批次、target_status 与当前状态相同） |
| 1500 | Internal | 服务内部错误 |

### 4.3 错误响应格式

遵循规则文件 §2.2 统一响应结构，HTTP 状态码映射遵循 §2.4：

```json
{
  "code": 1705,
  "message": "question referenced",
  "data": null
}
```

message 取 errcode 注册的英文默认文案，可见文案由前端按 code 经 i18n 渲染（§4.4），不消费后端 message。

**HTTP 状态码映射：**

- 所有成功：HTTP 200，code=0。
- `code = 1003`（Unauthorized，token 失效）：HTTP 401，前端触发登出。
- 其余业务错误（含 17xx、1400）：HTTP 200 带 code，前端据 code 渲染。
- 非 `*service.Error` 异常：HTTP 500 带 1500。

### 4.4 异常反馈策略（specs 4.1.4 规则 9）

| 异常类型 | 错误码 | 前端反馈 |
|---------|--------|---------|
| 表单校验失败 | 1400 | 字段内联报错，保留已填输入，不关闭弹窗 |
| 并发冲突（题目已变更） | 1713 | toast「数据已变更，请刷新后重试」，中止提交，刷新列表 |
| 并发冲突（批次已关闭/作废） | 1706 | toast并刷新批次卡区与列表（specs 4.2.4 规则 1） |
| 量表重复引入 | 1704 | toast「该量表已引入」，刷新引入弹窗置灰状态 |
| 被引用保护 | 1705 | toast「该题已被测试引用，只可停用」 |
| 生成失败 | FAILED + error_code | 失败态展示固定文案与「重新生成」「返回题库」（specs 4.3.4 规则 3） |
| 网络或服务异常 | 1500 / 网络错误 | toast「操作失败，请稍后重试」，允许重试 |

---

## 五、安全说明

### 5.1 认证与授权

- 全部接口挂受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))`（规则文件 §2.6），JWT 中间件校验签名与过期，注入 account_id，失败返 401 code 1003。
- 系统已移除角色概念（specs 2.1），题库为系统级共享数据，所有已登录平台账号可见可操作，接口层不做行级授权。

### 5.2 输入验证

- 类型与长度校验：handler 层对 scenario（1~1000）、requirement（1~2000）、focus_point（1~500）、驳回原因（1~500）、count（5~30）逐项校验，超长或越界返 1400。
- 状态前置校验：编辑/启停/重新提交/删除/确认入库/作废均校验前置状态，非法转换返 1707/1708/1706，防止 API 直调绕过前端按钮约束（specs 6.3）。
- SQL 注入防护：全程 GORM 链式 API 加占位符绑定；keyword 经 likeescape.EscapeLike 转义加 `ESCAPE '\'` 子句（规则文件 §1.11）。

### 5.3 传输安全

- 无密码、无敏感个人数据，RSA 加密通道不适用（规则文件 §2.5）。
- 生产强制 HTTPS 由 Nginx 反代承载（架构文档 4.4）。

### 5.4 限流

- 运营体量小（平台账号规模有限），读写接口不设接口级限流，JWT 鉴权已拦截未认证访问。
- 例外：`GET /api/question-generations/{id}` 轮询 2s 一次，单账号请求量天然受生成会话数约束（同时进行的生成为个位数），不设限流。

---

## 六、SSOT 合规说明

| specs 需求点 | 接口落地 | 状态 |
|-------------|---------|------|
| 题库查询（tab/维度/状态/关键词，specs 2.3、4.1.3） | GET /api/questions，source 必选 tab 参数 | 覆盖 |
| 待审核批次卡（4.1.2 C、4.1.5） | GET /api/question-batches 仅 PENDING | 覆盖 |
| 批次题目明细（4.2.2 A） | GET /api/question-batches/{id}/questions 全量 | 覆盖 |
| 批次确认入库（4.2.3、规则 2/3/7） | POST /confirm，rejected 数组一次性提交 | 覆盖 |
| 批次作废（4.1.3、6.2） | POST /void，按 batch_type 分流语义 | 覆盖 |
| 生成 AI 管理题（4.3、specs 2.3） | POST /api/question-generations/create + 进度轮询 | 覆盖 |
| 引入九型量表（4.1.3、规则 8） | GET /api/scales（含 imported）+ POST /api/scales/import | 覆盖 |
| 题目详情（4.1.2 D） | GET /api/questions/{id}，含被引用次数、驳回原因 | 覆盖 |
| 题目编辑（4.1.2 E） | POST /api/questions/update，仅 AI 题 ACTIVE/DISABLED | 覆盖 |
| 题目启停（4.1.3） | POST /api/questions/toggle-status | 覆盖 |
| 重新送审（4.1.3、规则 6） | POST /api/questions/resubmit，修正+归批一体 | 覆盖 |
| 题目删除（4.1.3、规则 4） | POST /api/questions/delete，被引用拒删 | 覆盖 |
| 生成进度/完成/失败态（4.3.4、4.3.5） | GET /api/question-generations/{id} 轮询 | 覆盖 |
| 生成放弃（4.3.4 规则 1） | POST /api/question-generations/{id}/cancel | 覆盖 |
| 雪花 ID string 化（specs 2.3 末段） | 全部 ID 字段 `,string` | 覆盖 |
| 并发冲突反馈（规则 9） | version 乐观锁 + 1713/1706/1704 | 覆盖 |

**specs 与规则文件冲突处理：**

- specs 2.3 的 PUT/DELETE 方法按规则文件 §2.1 收敛为 GET/POST（技术实现层面规则文件 > specs）。
- specs 4.3.3「同步 LLM 生成请求」按 specs 4.3.4 规则 3 的授权（进度机制由接口设计阶段确定）落地为异步任务 + 轮询，页面交互语义（停留等待、离开放弃、失败整批作废）不变。

---

## 七、变更记录

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|---------|------|
| v1.0 | 2026-09-23 | 初始版本，15 个接口（含 cancel），错误码 17xx 段 | lixuetao |
