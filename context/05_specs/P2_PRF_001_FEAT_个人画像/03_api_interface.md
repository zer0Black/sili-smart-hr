# 个人画像 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_PRF_001_FEAT_个人画像 |
| 模块代号 | PRF（个人画像域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-01 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、权限、状态），技术实现层面以规则文件为准（方法约定、响应结构、错误码段位）。冲突与技术层补充逐条在 §1.2-1.9 声明。

### 1.2 HTTP 方法归一化：本域全 GET

specs 未指定 HTTP 方法。本域是纯只读消费域（specs §1.1：不产生新评分、不调 LLM、不改上游数据），全部接口为查询：列表、导出、详情均 GET（规则文件 §2.1 GET 仅查询）。导出虽产出文件，语义是按当前筛选条件的读取物化，无服务端状态变更，归 GET。本域无任何 POST 接口。

### 1.3 人员归属键：staff_name 即 token_name

画像四表（activity_stats / dimension_scores / aggregate_scores / assessment_test_results 经 tasks）的人员归属键均为 `token_name`，且 P2_ASM_001 §1.6 已确立 `staff_name`（上游 username）作为 `token_name` 的展开契约（批次名单、阅卷落库同源）。本域沿用该契约：

- 接口的人员标识统一为 `staff_name` 字符串，四表联接与详情取数直接以之为键。
- `staff_id`（上游 user_id）不参与画像数据联接（四表无此列），列表响应不透传（显示字段仅姓名，specs §4.1.2 B；系统无工号约束）。未来对外预留接口（§4.1）若需 user_id 语义，届时按 5.3 契约扩展。

### 1.4 评估区间参数：yyyy-MM-dd 双端含止日

specs §8.1 定义评估区间为左闭右开 [start, end)，页面起止日期显示为 period_start 至 period_end 前一日。接口参数沿用 P2_ASM_001 §2.5 既定口径：`period_start` / `period_end` 均为 `yyyy-MM-dd` 且**两端均含止日**（即 period_end 是该区间最后一个自然日）。后端换算：period_start 按 `time.ParseInLocation(layoutDate, ..., time.Local)` 解析的当日 00:00:00 本地时区、period_end 按次日 00:00:00 本地时区，与落库行 `period_start_at` / `period_end_at` 双界精确匹配（与 ASM 批次接口同款换算：批次入参同为 time.Local 解析，落库行周期窗口来自 CurrentPeriodWindow 的 time.Local 零点；domain 注释中的 `.UTC()` 仅为 Unix 秒的时区显示转换，秒值不变）。响应中的区间（`periods` 列表、`selected_period`、`trend` 条目）同为含止日展示口径，前端原样渲染，无 ±1 天手工换算。

### 1.5 分数精度与等级映射的前后端分工

specs §4.2.4 规则3 定义模块总分为浮点、展示与等级判定前取整、较上期变化按取整后分差。分工裁定：

- 后端返回模块分**原始浮点值**（aggregate_scores.module_score 原值，保真）与**较上期变化 `change_vs_prev`**（按取整口径计算的 int 分差；任一期缺失为 null）。变化值由接口计算是 specs §5.2.2 步骤4 的明文（取区间列表下一项的聚合行计算），且需后端定位上一区间，前端无此上下文。
- 等级映射（≥85 优秀 / 70-84 良好 / 60-69 中等 / <60 待提升）与展示取整由前端常量承载（specs §4.2.4 规则2/规则3：前端规则拼装、阈值常量调整无需后端发版）。
- 维度分为整数（dimension_scores.score），直出。

### 1.6 导出接口的二进制流旁路

统一响应结构 `{code, message, data}` 是 JSON 解包契约（规则文件 §2.2），xlsx 文件流无法承载。导出接口成功时直接返回二进制流（`Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` + `Content-Disposition` 附件头），前端以 blob 请求消费；失败时返回统一 JSON 错误结构（HTTP 200 带 code，与既有错误映射一致），前端按响应 Content-Type 判别分支。文件名 `人员画像名单_YYYYMMDD.xlsx`（RFC 5987 `filename*` 编码承载中文名），日期取服务器当日。

### 1.7 姓名解析失败的回退口径（specs 未覆盖边界补充）

specs §5.2.4 规则2 定义上游不可达时接口整体失败（1305），未定义「上游可用但名单查无此人」（离职、改名）的处置。裁定：上游查询成功但无精确匹配行时，`staff_name` 回退落库 `token_name` 原值照常组装画像（数据仍在，画像可看），不视为错误、不加标记字段（前端无消费场景）。仅上游调用本身失败才整体 1305。

### 1.8 九型降级占位行的判型口径（specs 未覆盖边界补充）

阅卷重试耗尽的 enneagram 任务落降级占位行（`grading_status=degraded`，main_type 为空串，P2_TST_001 §5.2.5 降级路径）。specs §4.2.2 E 只定义「无判型行」的未参与态。裁定：判型查询过滤 `grading_status='scored'` 且 `main_type` 非空，取最新一行；降级占位行视为无判型（列表显示 -、详情整区未参与提示）。降级后重新发起测试产出新任务新判型行，最新 scored 行自然跳过降级行，口径自洽。

### 1.9 列表页每模块各自最新周期的取数口径

specs §4.1.2 B 各分数列分别表述为「aggregate_scores 最新周期 AI_USAGE 模块行」「AI_MGMT 模块行」，且 §4.1.4 规则5 明确列表不按统一区间取数。定向批次与周期批次窗口可能错位（specs §4.2.4 规则1），两模块的聚合行最新周期可能不同。裁定：列表页（A1/A2）按**每模块各自最新聚合周期**取数——模块分与降权标记取该模块最新聚合行、降权计数查同周期维度行；短板集合为两模块各自最新周期参与聚合维度（status=success 且 insufficient=false 且 include_overview=true，排除 ENNEAGRAM）合并后的最低分集合。「参与聚合」以聚合实际判据为准：include_overview=false 的维度只落评分行不进聚合（scorer classifyRow 口径，DIM「参与总览分」开关），不计入短板集合；参与聚合判据可直接取聚合行 included_json 的 code 集合（in_overview 已由聚合链路过滤）。详情页（B1）按所选区间统一取数，无此错位问题；B1 响应 dimensions[] 未回传 include_overview，前端核心结论短板类拼装（specs §4.2.4 规则2）判定参与聚合维度时读 dimensions/tree 的 include_overview 字段，与本节同口径。

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

本域全部接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号可见全员画像（specs §2.1：无创建人隔离）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 不单独限流，沿用受保护路由组既有策略。画像列表页无轮询通道（specs 第 4 章无轮询功能点），触发仅为页面加载与手动查询；导出按钮带 loading 防重（specs §4.1.3，通用规范第 23 条）。单账号稳态调用量远低于需限流门槛。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`；分页结构作 `data` 透传，四字段 `list` / `total` / `page` / `page_size`。唯一例外是 A2 导出的二进制流（§1.6）。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500、批次 16xx、题库 17xx、主动测试 18xx、员工作答 19xx。个人画像域分配 **20xx 段**（代码事实核对：19xx 为当前最大段，20xx 起空闲），本功能新增码见 §5。上游人员名单失败复用既有 1305。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

本域接口**不回显任何雪花 ID**：人员标识是 `staff_name` 字符串、维度标识是 `dimension_code` 字符串，响应无实体主键与外键转运字段。规则文件 §1.2 的双层 string 化约束在本域无落点（上游四表 domain 模型本就无 json tag，F9 经 DTO 下发时按需投影，见 04 文档 §4）。

### 2.5 时间与日期格式

- 评估区间（`periods` / `selected_period` / `trend` 条目 / 请求参数）：`yyyy-MM-dd` 双端含止日（§1.4）。
- 评分卡评估时间（`evaluated_at`）：`yyyy-MM-dd`（聚合行周期终点前一日，specs §4.2.2 B）。
- 证据时间（`evidences[].time`）：`yyyy-MM-dd HH:mm` 本地时区直出（与 F6/F7 列表时间同口径，DTO 层 Format 直出，前端原样渲染）。

### 2.6 隐私边界

- 详情接口任何字段不携带对话原文：维度解读取落库 `rationale`（上游已 Redact 脱敏），证据条目只透出统计摘要与会话信号数（evidence_json 本身无对话文本，specs §4.2.4 规则7 隐私红线由数据结构天然承载）。
- 本域零 LLM 调用、零上游表写入（specs §1.1 只读消费域）。
- 上游名单失败不降级、不缓存（specs §4.1.4 规则1：不允许降级为仅本系统有数据者）。

---

## 3. 接口列表

共 3 个新接口，按页面组织：人员画像列表页（A）、个人画像详情页（B）。维度选项复用 P2_DIM_001 已建 `GET /api/dimensions/tree`（C），不新增。

需求追溯编号对应 specs 第 4/5 章功能（F9 为 PRD 5.2.2 的模块编号）。

### A. 人员画像列表页

#### A1. 查询人员画像列表

**接口路径：** `GET /api/profiles`

**需求追溯：** [需求：F9-4.1.2A] [需求：F9-4.1.2B] [需求：F9-4.1.3 人员筛选查询/查看画像/页面加载自动查询] [需求：F9-4.1.4 规则1/2/3/4/5] [需求：F9-4.1.5] [需求：F9-5.1]

**说明：** 分页返回人员画像概要行：上游全员名单（userapi 客户端 WalkStaffPages 翻页聚合，specs §5.1.2 步骤2）+ 每模块最新周期画像数据左联 + 筛选与分页在服务端完成（specs §5.1.2 步骤3-5）。未参与任何评估的人入列表显示待评估态。姓名筛选优先透传上游 keyword（上游支持模糊过滤），拉回后仍做内存包含校验兜底。上游名单拉取失败时接口整体失败返回 1305，不返回半截数据（specs §5.1.4 规则1）。排序按姓名升序（UTF-8 字符串序，specs §8.3 偏离记录第 1 条）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| name | string | 否 | 空 | 姓名模糊匹配；前端去首尾空格后提交（specs §4.1.2 A）。名单为上游拉回后内存过滤：keyword 透传上游模糊过滤，拉回后按字面包含校验兜底（无 SQL LIKE 场景，不涉及转义） |
| activity_level | string | 否 | 空（全部） | 活跃度筛选 [可选值：active/low_freq/unused]，按本期（每人最新落库周期）活跃分级 |
| dimension_code | string | 否 | 空（全部） | 短板维度筛选：仅「该维度 ∈ 本期短板集合」的人命中（specs §4.1.4 规则4）。取值须为当前启用且 module ∈ {AI_USAGE, AI_MGMT} 的维度 code，否则 1400 |
| unused_only | boolean | 否 | false | 仅看未使用；true 时忽略 activity_level 强制按 unused 筛选（specs §4.1.2 A：与活跃度筛选叠加时以未使用为准） |
| page | integer | 否 | 1 | 页码，从 1 开始 |
| page_size | integer | 否 | 10 | 每页数量 [可选值：10/20/30]（specs §4.1.5）。实现为宽松超集：任意 1-100 整数均接受（>100 钳位 100） |

**请求示例：**

```
GET /api/profiles?name=张&activity_level=active&dimension_code=AI_UPPER_PLAN&unused_only=false&page=1&page_size=10
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "staff_name": "张敏",
        "activity_level": "active",
        "ai_usage_score": 82.35,
        "ai_usage_degraded": true,
        "ai_mgmt_score": 76.0,
        "ai_mgmt_degraded": false,
        "enneagram_main_type": "3"
      },
      {
        "staff_name": "张伟",
        "activity_level": "unused",
        "ai_usage_score": null,
        "ai_usage_degraded": false,
        "ai_mgmt_score": null,
        "ai_mgmt_degraded": false,
        "enneagram_main_type": null
      }
    ],
    "total": 2,
    "page": 1,
    "page_size": 10
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| staff_name | string | 姓名（上游人名，人员标识与 token_name 同源，§1.3） |
| activity_level | string | 活跃度 [可选值：active/low_freq/unused]；无 activity_stats 行时按 unused（specs §4.1.4 规则2） |
| ai_usage_score | number \| null | 本期 AI 使用能力模块分（该模块最新聚合行原始浮点值，§1.9）；无聚合行为 null，前端显示「待评估」（specs §4.1.4 规则2，不渲染为 0 分） |
| ai_usage_degraded | boolean | 降权标记：该模块最新聚合周期内存在 insufficient=true 或 status=failed 的维度行（specs §4.1.4 规则3）；警示只作标注不改分值，无聚合行时恒 false |
| ai_mgmt_score | number \| null | 本期 AI 管理能力模块分，同上口径；无主动测试评分行为 null（待评估） |
| ai_mgmt_degraded | boolean | 同 ai_usage_degraded 口径 |
| enneagram_main_type | string \| null | 九型主型数字串 [可选值："1"-"9"]，最新 scored 判型行（§1.8 口径）；型名映射由前端 i18n 承载；无判型行为 null，前端显示 - |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（activity_level 非枚举值、dimension_code 不在启用维度集合、unused_only 非布尔） |
| 1305 | 上游人员名单拉取失败（specs §5.1.4 规则1，接口整体失败不返回半截数据） |
| 1500 | 服务内部错误 |

---

#### A2. 导出人员画像名单

**接口路径：** `GET /api/profiles/export`

**需求追溯：** [需求：F9-4.1.3 导出名单]

**说明：** 按当前筛选条件导出 xlsx，字段与列表显示字段一致（姓名、活跃度、AI 使用能力、AI 管理能力、九型主型），**导出全部命中行（分页不截断）**（specs §4.1.3）。查询链路与 A1 完全复用（透传筛选条件、跳过分页，specs §5.1.4 规则2）。数值缺失导出空串；不含降权标记与展示态修饰（specs §4.1.3）。成功返回二进制流（§1.6），失败返回统一 JSON 错误结构（前端按 Content-Type 判别，列表处于错误态时导出按钮禁用由前端保障）。

**查询参数：** 同 A1（name / activity_level / dimension_code / unused_only），无分页参数。

**请求示例：**

```
GET /api/profiles/export?activity_level=active
```

**成功响应：**

- `Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`
- `Content-Disposition: attachment; filename="profiles.xlsx"; filename*=UTF-8''%E4%BA%BA%E5%91%98%E7%94%BB%E5%83%8F%E5%90%8D%E5%8D%95_20261001.xlsx`
- Body 为 xlsx 二进制流；表头五行：姓名 / 活跃度 / AI 使用能力 / AI 管理能力 / 九型主型；行序与排序口径同 A1（姓名升序）；九型主型列导出型名数字串对应的中文名（后端导出侧维护九型中文名常量，与前端 i18n 枚举值对齐，两处措辞在评审时核对同步）

**失败响应：** 统一 JSON 错误结构（HTTP 200 带 code），错误码同 A1（1305 / 1400 / 1500）。

---

### B. 个人画像详情页

#### B1. 查询画像详情聚合

**接口路径：** `GET /api/profiles/detail`

**需求追溯：** [需求：F9-4.2.2A] [需求：F9-4.2.2B] [需求：F9-4.2.2C] [需求：F9-4.2.2D] [需求：F9-4.2.2E] [需求：F9-4.2.3 返回列表/评估区间切换/维度明细展开/能力tab切换] [需求：F9-4.2.4 规则1/3/4/5/6/7/8] [需求：F9-4.2.5] [需求：F9-5.2]

**说明：** 一次请求返回单人指定评估区间的完整画像：区间列表、概览（活跃度、九型）、两模块评分卡（含较上期）、维度明细（分数、状态、解读、证据、近 4 期走势、公司均分对照）。区间为空时默认该人最新已落库区间（specs §5.2.2 步骤1）。区间列表为三表（activity_stats / dimension_scores / aggregate_scores）落库周期并集新到旧（specs §4.2.4 规则1）。姓名由服务端调 userapi 解析（keyword 精确匹配），上游失败整体 1305、查无此人回退 token_name（§1.7）。人员查无任何落库周期行时返回空画像语义（periods 为空数组、各数据块按缺失口径组装），不视为错误（specs §5.2.4 规则1）。核心结论区由前端基于本响应规则拼装，接口不产出结论文本（specs §4.2.4 规则2）。

**查询参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| staff_name | string | 是 | 人员标识（token_name 同源，§1.3）；缺失或空串 1400 |
| period_start | string | 条件必填 | 所选评估区间起点 `yyyy-MM-dd`（含）；与 period_end 成对出现，要么都传要么都不传（不传=最新区间） |
| period_end | string | 条件必填 | 所选评估区间止日 `yyyy-MM-dd`（含，§1.4）；只传一端或格式非法 1400 |

**请求示例：**

```
GET /api/profiles/detail?staff_name=张敏&period_start=2026-09-22&period_end=2026-09-28
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "staff_name": "张敏",
    "periods": [
      { "period_start": "2026-09-22", "period_end": "2026-09-28", "is_current": true },
      { "period_start": "2026-09-15", "period_end": "2026-09-21", "is_current": false }
    ],
    "selected_period": { "period_start": "2026-09-22", "period_end": "2026-09-28" },
    "activity_level": "active",
    "enneagram": {
      "main_type": "3",
      "wing_type": "2",
      "distribution": { "1": 8.2, "2": 14.5, "3": 32.1, "4": 6.0, "5": 5.5, "6": 9.8, "7": 7.4, "8": 8.9, "9": 7.6 },
      "rationale": "……（脱敏判定依据）"
    },
    "modules": [
      {
        "module": "AI_USAGE",
        "score": 82.35,
        "change_vs_prev": 3,
        "evaluated_at": "2026-09-28",
        "data_status": "degraded",
        "insufficient_count": 1,
        "failed_count": 0,
        "missing_count": 0
      },
      {
        "module": "AI_MGMT",
        "score": 76.0,
        "change_vs_prev": null,
        "evaluated_at": "2026-09-28",
        "data_status": "complete",
        "insufficient_count": 0,
        "failed_count": 0,
        "missing_count": 0
      }
    ],
    "dimensions": [
      {
        "module": "AI_USAGE",
        "dimension_code": "AI_BASE_CLARITY",
        "dimension_name": "指令清晰度",
        "group_code": "BASE",
        "score": 85,
        "status": "normal",
        "rationale": "……（脱敏评分理由）",
        "evidences": [
          {
            "source": "conversation",
            "time": "2026-09-28 23:15",
            "confidence": "high",
            "session_count": 12,
            "summary": { "total_turns": 156, "instruction_segments": 23 }
          }
        ],
        "trend": [
          { "period_start": "2026-09-08", "period_end": "2026-09-14", "score": 79 },
          { "period_start": "2026-09-15", "period_end": "2026-09-21", "score": 82 },
          { "period_start": "2026-09-22", "period_end": "2026-09-28", "score": 85 }
        ],
        "company_avg": 76.5
      },
      {
        "module": "AI_MGMT",
        "dimension_code": "MGT_DELEGATION",
        "dimension_name": "授权分工",
        "group_code": null,
        "score": null,
        "status": "missing",
        "rationale": "",
        "evidences": [],
        "trend": [],
        "company_avg": null
      }
    ]
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| staff_name | string | 姓名（上游解析的权威人名，§1.7 回退口径） |
| periods | array | 该人评估区间列表（三表落库周期并集，新到旧，specs §4.2.4 规则1），条目字段见下；查无任何落库周期为空数组（空画像语义，前端进空态） |
| periods[].period_start / period_end | string | 区间起止（yyyy-MM-dd 含止日，§1.4） |
| periods[].is_current | boolean | 是否该人最新已落库区间（本期标注，specs §4.2.2 A） |
| selected_period | object \| null | 实际生效区间（请求区间或默认最新区间）；空画像为 null |
| activity_level | string | 所选区间活跃度 [可选值：active/low_freq/unused]；无 activity_stats 行按 unused（specs §4.1.4 规则2 同口径延伸） |
| enneagram | object \| null | 九型判型（最新 scored 行，不随区间变化，specs §4.2.2 A/E）；无判型行为 null，前端整区显示未参与提示（specs §4.2.4 规则6） |
| enneagram.main_type | string | 主型数字串 "1"-"9"，型名映射由前端 i18n 承载 |
| enneagram.wing_type | string | 翼型数字串，无显著翼型为空串 |
| enneagram.distribution | object | 9 型倾向分布（distribution_json 原样透传，键 "1"-"9" 值百分比，主型柱高亮由前端按 main_type 渲染） |
| enneagram.rationale | string | 判型依据（脱敏落库原文） |
| modules | array | 两模块评分卡，恒含 AI_USAGE 与 AI_MGMT 两行（specs §4.2.2 B），条目字段见下 |
| modules[].module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT] |
| modules[].score | number \| null | 模块分原始浮点值（aggregate_scores.module_score 原值，§1.5）；无聚合行为 null（待评估，specs §4.2.4 规则8 判定优先级最高档） |
| modules[].change_vs_prev | integer \| null | 较上期变化：本期与区间列表下一项聚合行按取整后分差计算（specs §4.2.4 规则3）；任一期缺失为 null（前端不显示，specs §4.2.2 B） |
| modules[].evaluated_at | string \| null | 评估时间：聚合行周期终点前一日（yyyy-MM-dd，specs §4.2.2 B）；无聚合行为 null |
| modules[].data_status | string | 数据状态 [可选值：complete 数据完整/degraded 部分维度已降权/missing 部分维度缺失/pending 待评估]，判定优先级 pending > missing > degraded > complete（specs §4.2.4 规则8：降权与缺失并存按缺失） |
| modules[].insufficient_count | integer | 该模块所选区间 insufficient=true 维度行数（发现区风险类拼装数据源，specs §4.2.4 规则2） |
| modules[].failed_count | integer | 该模块所选区间 status=failed 维度行数 |
| modules[].missing_count | integer | 该模块当前启用维度中无评分行的维度数（基准集合为当前启用维度配置） |
| dimensions | array | 维度明细全集（当前启用且 module ∈ {AI_USAGE, AI_MGMT} 的维度，按模块与分组呈现属前端职责，specs §4.2.2 D），条目字段见下；空画像时仍按维度配置组装全 missing 行 |
| dimensions[].module | string | 所属模块 [可选值：AI_USAGE/AI_MGMT] |
| dimensions[].dimension_code | string | 维度编码（维度配置 code） |
| dimensions[].dimension_name | string | 维度显示名（维度配置现读） |
| dimensions[].group_code | string \| null | 分组 [可选值：BASE/UPPER]，仅 AI_USAGE 非 null（specs §4.2.2 D 分组呈现） |
| dimensions[].score | integer \| null | 维度分；insufficient 行照常返回分数（前端带降权标记）；failed 行与无行维度为 null（不渲染落库 0 分占位，specs §4.2.2 D） |
| dimensions[].status | string | 维度状态 [可选值：normal 正常/insufficient 已降权/missing 数据缺失]（specs 第 6 章状态映射） |
| dimensions[].rationale | string | 维度解读（脱敏落库原文，specs §4.2.4 规则7）；缺失态为空串 |
| dimensions[].evidences | array | 证据来源列表（specs §4.2.2 D），行缺失为空数组；evidence_json 本身无对话文本，「脱敏样本」由统计摘要与会话信号数承载 |
| evidences[].source | string | 来源类型 [可选值：conversation 对话分析/active_test 主动测试]（specs §4.2.2 D） |
| evidences[].time | string | 该维度评分行落库时刻（yyyy-MM-dd HH:mm，取行 updated_at） |
| evidences[].confidence | string | 置信度 [可选值：high/medium/low]，规则映射非 LLM 输出（specs §4.2.4 规则5）：conversation 按 session_keys 数量（≥10 high、2-9 medium、<2 low）；active_test 行存在即阅卷成功判 high（degraded 任务无评分行，low 分支当前链路无触发路径，映射防御性保留） |
| evidences[].session_count | integer | 会话信号次数（conversation 行 = len(session_keys)；active_test 为 0） |
| evidences[].summary | object | 统计摘要（evidence_json.summary 原样透传，键值因评估口径版本而异，前端按存在键渲染）；active_test 行为空 object |
| dimensions[].trend | array | 近 4 期走势（同人该维度各期评分行按 period 旧到新，specs §4.2.2 D：展开时才渲染、序列随本响应返回），条目含 period_start / period_end / score；某期该维度 failed 或无行为该期不入序列（走势仅呈现有分数的期次）；维度无任何历史行为空数组 |
| dimensions[].company_avg | number \| null | 公司均分对照：所选区间全公司该维度参与聚合维度行（剔除 insufficient 与 failed）的算术平均，实时聚合不落库（specs §4.2.4 规则4）；公司侧有数据人数 < 3 为 null（前端隐藏对照线） |

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（staff_name 缺失、区间只传一端、日期格式非法） |
| 2001 | 所选区间不在该人区间列表内（specs §5.2.4 规则1，前端提示并回落最新区间） |
| 1305 | 姓名上游解析失败（specs §5.2.4 规则2，整页错误态不用画像数据降级渲染） |
| 1500 | 服务内部错误 |

---

### C. 复用接口（不新增）

| 接口 | 来源 | 消费场景 |
|------|------|---------|
| `GET /api/dimensions/tree` | P2_DIM_001 | 列表页短板维度下拉选项（启用且 module ∈ {AI_USAGE, AI_MGMT} 的维度并集，specs §4.1.2 A 动态读维度配置）与详情页维度名称/分组/tab 计数（specs §4.2.2 D、§4.2.3 能力 tab 切换） |

人员名单不经独立检索接口：列表人群即全员（A1 内部全量拉取左联），无「先查人再查画像」两段式。

---

## 4. 非页面功能契约

### 4.1 画像数据接口（预留，首期不实现）

specs §5.3 的对外输出通道按契约要点转记，本期不实现、不注册路由、不占用开发任务（specs §5.3.2 第 4 条）：

1. 能力形态：按人员标识查询单人画像（活跃度、两模块总分与各维度分、维度解读与证据摘要、九型判型），输出脱敏口径不含对话原文。数据范围止于已落库画像，复用本域 B1 的取数链路。
2. 鉴权：平台对外签发的集成凭据（方向与对上游的集成密钥相反，凭据形态实现期确定）；无凭据或凭据失效拒绝访问。
3. 边界：只读查询，无写入、订阅与批量拉取。字段级定义与限流策略在外部对接需求明确后按 specs §5.3.2 立项补齐。
4. 人员标识届时以外部系统持有的标识（预期为上游 user_id）为准，与本域内部 staff_name 键的映射在实现期定义（§1.3 已预留扩展位）。

### 4.2 计算常量

| 常量 | 初值 | 说明 |
|------|------|------|
| trendWindowSize | 4 | 走势期数（specs §4.2.2 D 近 4 期），service 包内常量 |
| companyAvgMinPeople | 3 | 公司均分对照的最小有数据人数（specs §4.2.4 规则4：< 3 人隐藏对照线，接口侧置 null） |
| confidenceHighSignals | 10 / confidenceLowSignals | 置信度阈值：会话信号 ≥10 high、2-9 medium、<2 low（specs §4.2.4 规则5），service 包内常量 |

三者随源码发版（specs §4.2.4 规则2 确定性口径，阈值属实现常量非在线配置）。

### 4.3 服务端组装的查询口径（开发契约）

```
A1/A2 列表组装：
  1. userapi.WalkStaffPages 全量拉取名单（姓名筛选透传 keyword，§A1 说明）
  2. 名单 staff_name 集合批量 IN 查询四表（避免逐人查询，specs §5.1.4 规则2）：
     - aggregate_scores：每人每模块最新聚合行（§1.9 每模块各自最新）
     - dimension_scores：每模块最新聚合周期同窗维度行（降权计数与短板集合，短板判据含 include_overview=true，§1.9）
     - activity_stats：每人最新周期行
     - assessment_test_results JOIN assessment_test_tasks：每人最新 scored 判型行（§1.8）
  3. 内存组装行数据（缺失按 specs §4.1.4 规则2）、应用筛选、姓名升序排序、分页

B1 详情组装：
  1. 三表落库周期并集生成区间列表（新到旧），校验所选区间在列表内（否则 2001）
  2. 所选区间三表取数 + 最新 scored 判型行
  3. 区间列表下一项聚合行算 change_vs_prev（取整口径，§1.5）
  4. 同人各期维度评分行组装各维度 trend（近 4 期）
  5. 公司均分：所选区间全公司维度行（剔除 insufficient 与 failed）聚合，< 3 人置 null
  6. 姓名经 userapi 解析（keyword 精确匹配），失败 1305 / 查无回退 token_name（§1.7）
```

**需求追溯：** specs §5.1.2、§5.2.2 处理流程。

### 4.4 导出实现依赖

xlsx 生成引入 Go 侧 excelize 依赖（单文件工作簿、五列、行数据复用 A1 查询链路）。文件流直写响应体，不落临时文件。

---

## 5. 错误码

新增 20xx 段共 1 个码；上游名单失败复用既有 1305，参数错误复用 1400。

| 错误码 | 常量 | 含义 | 使用场景 |
|--------|------|------|---------|
| 2001 | ProfilePeriodInvalid | 评估区间不在该人区间列表内 | B1 所选区间查无对应落库周期行（specs §5.2.4 规则1） |

A1/A2 的 dimension_code 校验失败复用 1400（筛选条件非资源定位，不引入维度域 1201）。

---

## 6. 与相邻域的边界

- **对 P2_ASM_001（F6 批次/对话分析）**：只读消费 activity_stats / dimension_scores（source=conversation）/ aggregate_scores 三表落库行；不调用其服务、不触发评估、不写任何表。批次运营数据（批次列表、告警）归 F6 页面消费，本域不触碰。
- **对 P2_TST_001（F7 主动测试）**：只读消费 dimension_scores（source=active_test）、assessment_test_results（经 tasks join 取人）；判型 degraded 占位行的过滤口径见 §1.8。任务运营接口不触碰。
- **对 P2_DIM_001（维度配置）**：只读消费维度启用集合与显示名（短板筛选选项、详情维度明细、data_status 的 missing 基准集合）；复用 GET /api/dimensions/tree。
- **对 P2_SYS_001（配置/集成）**：上游名单经 userapi 客户端 + 集成密钥解析（失败统一 1305，与 /api/staffs 同源降级）；本域零 LLM 调用，无大模型配置依赖。
- **对 F10（团队看板，未建）**：看板下钻个人跳转本域详情页（staff_name 传参），跳转入口归 F10 定义；短板人群识别共用维度口径（specs §8.1 短板集合术语）。
- **对 F11（工作台，未建）**：无交集（工作台消费批次态势与任务逾期，本域消费画像数据）。
- **对外部消费系统（未定）**：画像数据接口预留（§4.1），本期无 HTTP 面。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1 列表页（筛选查询、重置、查看画像跳转、导出名单、页面加载自动查询）→ A1/A2；4.2 详情页（返回列表、区间切换、维度展开、tab 切换）→ B1 + 前端交互（返回与展开无接口依赖）；5.1 列表聚合接口 → A1/A2（§4.3）；5.2 详情聚合接口 → B1（§4.3）；5.3 预留接口 → §4.1 契约转记不实现。
- [x] 字段定义与 specs 一致：4.1.2 查询/显示字段、4.2.2 五区字段在请求/响应结构一一对应；三处适配已声明（分数精度与等级分工 §1.5、区间含止日口径 §1.4、九型降级行口径 §1.8）。
- [x] 业务规则在接口层落地：缺失值口径（null + 前端待评估/-，§A1/B1 字段表）、降权标注（degraded 布尔与计数）、短板集合口径（§1.9）、列表数据时点（每模块最新，无区间参数）、区间一致性（三表并集 + 2001 校验）、公司均分（实时聚合 + <3 人 null）、置信度映射（§4.2 常量）、隐私红线（§2.6）。
- [x] 状态定义与 specs 第 6 章一致：normal/insufficient/missing 映射 dimension_scores 行状态，pending 映射模块无聚合行；均为上游落库状态的只读映射，无用户触发转换。
- [x] 权限规则一致：全部接口挂 JWT，无角色差异（specs §2.2），员工侧无 HTTP 面（员工非平台用户）。
- [x] 技术层偏差已声明：全 GET 归一化（§1.2）、staff_name 人员键（§1.3）、区间换算（§1.4）、分数分工（§1.5）、导出流旁路（§1.6）、姓名回退（§1.7）、降级判型过滤（§1.8）、每模块最新周期（§1.9）。
- [x] 接口与数据模型一致性：响应字段均为上游四表列的投影或查询期派生，无新表新列（[04_model_interface.md](04_model_interface.md) 适用性结论与索引核对）。

---

## 8. 不涉及的设计

- 画像数据对外接口的实现（路由、凭据签发、限流）：specs §5.3.2 明确首期不实现，仅契约转记（§4.1）。
- 任何画像数据写路径（评分修正、画像备注、培训建议录入）：specs §1.1 纯只读消费域，无写操作。
- 公司均分与画像快照的物化落库（表、缓存、定时任务）：specs §4.2.4 规则4 展示期实时聚合不落库。
- 核心结论的文本生成（LLM 或后端模板）：specs §4.2.4 规则2 前端规则拼装，接口只供数据。
- 团队看板（F10）与工作台（F11）的聚合接口：归各自 Feature。
- 员工本人查看画像的入口：员工非平台用户（specs §2.1），无此面。

---

**文档版本：** v1.0
**最后更新：** 2026-10-01
**作者：** lixuetao
