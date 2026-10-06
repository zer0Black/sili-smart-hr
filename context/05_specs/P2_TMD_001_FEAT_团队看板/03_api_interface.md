# 团队看板 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_TMD_001_FEAT_团队看板 |
| 模块代号 | TMD（团队看板域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-05 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、权限、状态），技术实现层面以规则文件为准（方法约定、响应结构、错误码段位）。冲突与技术层补充逐条在 §1.2-1.9 声明。

### 1.2 HTTP 方法归一化：本域全 GET

specs 未指定 HTTP 方法。本域两个页面均为只读消费（specs §2.2：查看、切区间、跳转），HTTP 面只有两个聚合查询接口，全部 GET（规则文件 §2.1 GET 仅查询）。培训建议生成是 Asynq 后台任务（specs §5.1），无 HTTP 触发入口，任务契约见 §4。

### 1.3 评估区间参数：yyyy-MM-dd 双端含止日

沿用 P2_ASM_001 §2.5 与 F9 03 §1.4 既定口径：`period_start` / `period_end` 均为 `yyyy-MM-dd` 且两端均含止日。后端换算：start 按当日 00:00:00 本地时区、end 按次日 00:00:00 本地时区，与落库行 `period_start_at` / `period_end_at` 双界精确匹配。响应中的区间（`periods` / `selected_period` / `current_period` / `history` 条目）同为含止日展示口径，前端原样渲染。

### 1.4 分数精度：后端取整直出，判定用原始值

specs §4.1.4 规则3 与 §4.2.4 规则2 的展示口径均为「四舍五入取整」，且短板判定、综合分与参考线计算以未取整原始值参与、展示前取整。分工裁定（与 F9 03 §1.5 的「后端返浮点、前端取整」不同，原因是 specs 表述相反）：

- 后端以未取整原始值完成共性短板判定（含并列截断）、综合分计算、全维度均值参考线计算，**响应中的分值字段一律返回取整后的 int**（维度均分、综合分、走势分值、变化值）。
- 占比类字段（活跃三态占比、低分占比、九型占比、覆盖率）返回一位小数 float，前端原样渲染。
- 较上期变化按取整后分差计算（specs §4.2.4 规则2 明文）。

### 1.5 培训建议展示状态语义（specs 未覆盖边界补充）

specs §4.1.2 E 区分「已生成展示内容」「无建议行或生成中/生成失败显示提示态」。接口裁定：`suggestion` 对象**恒返回**，以 `status` 字段四态承载：`generated`（内容齐全）/ `generating` / `failed` / `none`（无建议行）。非 generated 态内容字段为空值（modules 空数组、summary 空串、区间与时间 null），前端按 status 分支渲染提示态。

### 1.6 空态跳过全员名单调用（specs 未覆盖边界补充）

specs §4.1.5 空态为「系统无任何落库区间」，错误态含全员名单上游失败（1305）。两态叠加场景（无区间且上游故障）specs 未定义。裁定：无任何落库区间时接口直接返回空态结构（§A1 空态语义），**跳过 userapi 全员名单调用**——空态页面不渲染任何数据块，全员人数无消费方，不应让上游故障把空态渲染成错误态。有区间时名单失败仍整体 1305（specs §5.2.4 规则2）。

### 1.7 建议生成任务的两段式拆分（技术层裁决）

specs §5.1 描述为单一周期任务（每分钟 tick + LLM 调用），但 §5.1.4 规则5 同时给出「tick 每分钟至多拾取一条批次」与「任务级超时 240s」。若 tick 内联 LLM 调用，每分钟触发的任务实例会在 240s 超时窗口内重叠堆积。裁定参照 batch-tick → batch-run 既有先例拆为两段：

- `dashboard:suggest-tick`（每分钟 cron，任务级超时 60s）：只做扫描拾取、幂等建行（生成中行即拾取锁）、投递生成任务。
- `dashboard:suggest-generate`（default 队列，任务级超时 240s）：素材汇总、LLM 调用、校验脱敏、终态落库。

specs 的 240s 任务级超时落在 generate 任务；生成中行存在性锁与 period 唯一约束兜底跨 tick 并发（specs §5.1.4 规则5 语义不变）。

### 1.8 数据更新时间无批次记录的兜底（specs 未覆盖边界补充）

specs §4.1.2 A 定义数据更新时间为「该区间聚合数据对应批次记录的终态时间，同区间多条批次时取最新终态批次；无批次记录时显示区间周期终点」。裁定：按 period 双界匹配备次行（trigger_type 不限，手动定向批次与周期批次同窗时一并参与），取 `finished_at` 最新非空行；无批次记录时取该区间 `period_end_at` 时刻（窗口右开界）。展示格式 `yyyy-MM-dd HH:mm`。

### 1.9 九型降级占位行的过滤口径（沿用 F9 03 §1.8）

判型行过滤 `grading_status='scored'` 且 `main_type` 非空，每人取最新一行；阅卷重试耗尽的降级占位行视为无判型。与 F9 详情页同口径，复用 `ListLatestScoredByStaffNames` 既有仓储方法。

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

本域全部接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号可见全公司整体看板（specs §2.1）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 不单独限流，沿用受保护路由组既有策略。两页面均为页面加载与手动切换触发，无轮询通道（specs 第 4 章无轮询功能点），单账号稳态调用量远低于需限流门槛。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`。本域两接口均为聚合对象响应，无分页（specs §8.3 偏离记录：两页面无列表分页，人群行级定位经跳转 F9 承接）。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500、批次 16xx、题库 17xx、主动测试 18xx、员工作答 19xx、个人画像 20xx。团队看板域分配 **21xx 段**（代码事实核对：20xx 为当前最大段，21xx 起空闲），本功能新增码见 §5。上游名单失败复用既有 1305。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

本域接口**不回显任何雪花 ID**：区间是日期对、维度标识是 `dimension_code` 字符串、九型是型别数字串、建议内容按 period 定位，响应无实体主键与外键转运字段。规则文件 §1.2 的双层 string 化约束在本域 HTTP 面无落点（建议表 domain 模型无 HTTP 序列化路径，worker payload 内部消费，见 04 文档 §3.1）。

### 2.5 时间与日期格式

- 评估区间（`periods` / `selected_period` / `current_period` / `history` 条目 / 请求参数）：`yyyy-MM-dd` 双端含止日（§1.3）。
- 数据更新时间（`data_updated_at`）与建议生成时间（`generated_at`）：`yyyy-MM-dd HH:mm` 本地时区直出（与 F6/F7/F9 列表时间同口径，DTO 层 Format 直出），前端原样渲染。

### 2.6 隐私边界

- 两接口任何字段只呈现聚合统计（均分、人数、占比）与脱敏建议文案，无对话原文、无单人维度分数明细、无评分理由透传（specs §4.1.4 规则7 隐私红线）。
- 建议内容为落库脱敏文本（生成链路 Redact，§4.2），接口只读透传。
- 聚合实时计算不落库、不加服务端缓存（specs §5.2.4 规则3）。

---

## 3. 接口列表

共 2 个新接口，按页面组织：团队看板页（A1）、能力逐期趋势页（A2）。无复用接口：维度显示名由 A1/A2 响应自带（现读维度配置），跳转 F9 由其前端路由承接（§4.5）。

需求追溯编号对应 specs 第 4/5 章功能（F10 为 PRD 5.2.2 的模块编号）。

### A. 团队看板页

#### A1. 查询团队看板全量

**接口路径：** `GET /api/dashboard`

**需求追溯：** [需求：F10-4.1.2A] [需求：F10-4.1.2B] [需求：F10-4.1.2C] [需求：F10-4.1.2D] [需求：F10-4.1.2E] [需求：F10-4.1.3 评估区间切换/共性短板人群跳转/未使用人群跳转/维度雷达悬浮查看/页面加载自动查询] [需求：F10-4.1.4 规则1/2/3/4/5/6/7] [需求：F10-4.1.5] [需求：F10-5.2]

**说明：** 一次请求返回所选区间的看板全量数据：区间列表（全公司三表落库周期并集，新到旧）、全员人数、活跃度三态与环比、两模块维度均分雷达与共性短板、九型构成快照、培训建议。区间参数空时默认最新落库区间。九型区与培训建议恒取最新快照，不随区间参数变化（specs §4.1.4 规则5/规则6）。全员人数经 userapi 翻页聚合计数，失败整体 1305；无任何落库区间时返回空态结构且跳过上游调用（§1.6）。共性短板判定（含并列截断）、维度均分取整、参考线计算均由后端完成（§1.4）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| period_start | string | 条件必填 | 空（最新区间） | 所选评估区间起点 `yyyy-MM-dd`（含）；与 period_end 成对出现，要么都传要么都不传 |
| period_end | string | 条件必填 | 空（最新区间） | 所选评估区间止日 `yyyy-MM-dd`（含，§1.3）；只传一端或格式非法 1400 |

**请求示例：**

```
GET /api/dashboard
GET /api/dashboard?period_start=2026-09-29&period_end=2026-10-05
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "periods": [
      { "period_start": "2026-09-29", "period_end": "2026-10-05", "is_current": true },
      { "period_start": "2026-09-22", "period_end": "2026-09-28", "is_current": false }
    ],
    "selected_period": { "period_start": "2026-09-29", "period_end": "2026-10-05" },
    "data_updated_at": "2026-10-05 02:12",
    "staff_total": 120,
    "activity": {
      "active": { "count": 50, "ratio": 41.7 },
      "low_freq": { "count": 30, "ratio": 25.0 },
      "unused": { "count": 40, "ratio": 33.3 },
      "mom": { "active_change": 5, "unused_change": -3 }
    },
    "modules": [
      {
        "module": "AI_USAGE",
        "dimensions": [
          { "dimension_code": "AI_BASE_CLARITY", "dimension_name": "指令清晰度", "avg_score": 76, "low_ratio": 23.3, "is_weakness": false },
          { "dimension_code": "AI_UPPER_PLAN", "dimension_name": "任务规划", "avg_score": 58, "low_ratio": 66.7, "is_weakness": true }
        ],
        "overall_avg": 74
      },
      {
        "module": "AI_MGMT",
        "dimensions": [
          { "dimension_code": "MGT_DELEGATION", "dimension_name": "授权分工", "avg_score": 61, "low_ratio": 40.0, "is_weakness": true }
        ],
        "overall_avg": null
      }
    ],
    "enneagram": {
      "scored_count": 80,
      "coverage_ratio": 66.7,
      "distribution": [
        { "type": "1", "count": 8, "ratio": 10.0 },
        { "type": "2", "count": 10, "ratio": 12.5 },
        { "type": "3", "count": 18, "ratio": 22.5 }
      ],
      "dominant_type": "3",
      "dominant_ratio": 22.5,
      "secondary_type": "2",
      "secondary_ratio": 12.5
    },
    "suggestion": {
      "status": "generated",
      "period_start": "2026-09-29",
      "period_end": "2026-10-05",
      "generated_at": "2026-10-05 03:00",
      "modules": [
        {
          "module": "AI_USAGE",
          "suggestions": [
            { "name": "提示词工程专项训练营", "description": "针对任务规划维度低分占比 66.7%……" }
          ]
        },
        {
          "module": "AI_MGMT",
          "suggestions": [
            { "name": "授权分工案例研讨", "description": "……" }
          ]
        }
      ],
      "summary": "团队整体 AI 使用能力稳健，任务规划为共性短板……"
    }
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| periods | array | 全公司评估区间列表（activity_stats / dimension_scores / aggregate_scores 三表落库周期并集，新到旧，specs §4.1.2 A 口径同 F9 详情页），条目含 period_start / period_end / is_current（最新项 true，前端标注「本期」）；无任何落库区间为空数组（空态语义） |
| selected_period | object \| null | 实际生效区间（请求区间或默认最新区间）；空态为 null |
| data_updated_at | string \| null | 所选区间数据更新时间：该区间批次终态时间（多条批次取最新终态，无批次取区间终点，§1.8）；空态为 null |
| staff_total | integer | 全员人数（userapi 全员名单计数，统计分母，specs §4.1.2 B）；空态为 0 |
| activity | object \| null | 活跃度概览（所选区间）；空态为 null |
| activity.active / low_freq / unused | object | 三态分级：count 人数 + ratio 占全员比例一位小数（specs §4.1.4 规则2）。unused 含无统计行与统计行判未使用两类；无任何统计行时三态 count 全 0 |
| activity.mom | object \| null | 环比：active_change / unused_change 为本期减上一落库区间计数差（int 带符号，前端渲染方向色）；所选区间为最早区间时 null（specs §4.1.2 B） |
| modules | array | 两模块雷达卡数据，恒含 AI_USAGE 与 AI_MGMT 两行（specs §4.1.2 C）；空态为空数组 |
| modules[].module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT] |
| modules[].dimensions | array | 模块内当前启用维度全集（雷达轴与短板标签同源，维度集合随维度配置动态），AI_USAGE 取 source=conversation 行、AI_MGMT 取 source=active_test 行聚合（specs §4.1.4 规则3） |
| modules[].dimensions[].dimension_code | string | 维度编码（维度配置 code） |
| modules[].dimensions[].dimension_name | string | 维度显示名（维度配置现读） |
| modules[].dimensions[].avg_score | integer \| null | 该维度全员均分（参与聚合行剔除 insufficient 与 failed 后的算术平均，取整，§1.4）；有数据人数 < 3 为 null（雷达该轴断开，specs §4.1.4 规则3） |
| modules[].dimensions[].low_ratio | float \| null | 低分占比：参与聚合人数中分数 < 60 的人数占比一位小数（specs §4.1.4 规则4）；avg_score 为 null 时同置 null |
| modules[].dimensions[].is_weakness | boolean | 是否共性短板：后端按未取整原始值在参与聚合人数 ≥ 3 的维度中取均分最低 2 维判定，并列取满至多 3 项、超 3 项按均分升序取前 3、仍并列按维度编码升序截断（specs §4.1.4 规则4）；无满足条件维度时全 false |
| modules[].overall_avg | integer \| null | 全维度均值参考线：各维度均分以未取整原始值参与的算术平均，展示前取整（specs §4.1.4 规则3）；任一维度置空（< 3 人）时为 null（前端隐藏参考线） |
| enneagram | object \| null | 九型构成快照（全员最新判型行集合，不随区间变化，specs §4.1.4 规则5）；无任何判型行为 null（前端整区显示未参与提示） |
| enneagram.scored_count | integer | 测评覆盖数：有最新 scored 判型行的人数（specs §4.1.2 D） |
| enneagram.coverage_ratio | float | 覆盖率：scored_count / staff_total 一位小数 |
| enneagram.distribution | array | 九型分布，恒 9 项按型别序号 "1"-"9" 升序（无人型别 count 0 / ratio 0.0），count 为人数、ratio 为占 scored_count 比例一位小数 |
| enneagram.dominant_type / dominant_ratio | string / float | 主导型数字串与占比（占比最高型，并列按型别序号升序取先，specs §4.1.4 规则5）；型名映射与固定型别特征描述由前端 i18n 承载（specs §4.1.2 D 构成摘要为前端规则拼装） |
| enneagram.secondary_type / secondary_ratio | string / float | 次主导型数字串与占比，同上判定 |
| suggestion | object | 团队培训方向建议（恒返回，status 区分四态，§1.5）；取 period 起止新到旧第一条建议行（specs §4.1.4 规则6） |
| suggestion.status | string | [可选值：generated 已生成/generating 生成中/failed 生成失败/none 无建议行]；generating/failed/none 时前端显示「培训建议生成中或本期无批量批次」提示态 |
| suggestion.period_start / period_end | string \| null | 建议对应评估区间（行 period 起止，含止日）；非 generated 为 null |
| suggestion.generated_at | string \| null | 建议生成时间（yyyy-MM-dd HH:mm）；非 generated 为 null |
| suggestion.modules | array | 两模块建议（非 generated 为空数组），每模块 2 至 4 条（specs §5.1.2 步4） |
| suggestion.modules[].module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT] |
| suggestion.modules[].suggestions | array | 培训方向建议条目：name 方向名称 + description 说明（脱敏落库原文透传） |
| suggestion.summary | string | 团队综合研判（脱敏落库原文透传）；非 generated 为空串 |

**空态语义：** 无任何落库区间时返回 `{periods: [], selected_period: null, data_updated_at: null, staff_total: 0, activity: null, modules: [], enneagram: null, suggestion: {status: "none", ...空值}}`，不视为错误，前端走空态提示（specs §4.1.5）。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数格式错误（区间只传一端、日期格式非法） |
| 2101 | 所选区间不在落库区间列表内（前端按通用错误态整页占位处理） |
| 1305 | 全员名单上游拉取失败（specs §5.2.4 规则2，接口整体失败不用部分数据降级渲染） |
| 1500 | 服务内部错误 |

---

### B. 能力逐期趋势页

#### A2. 查询能力逐期趋势

**接口路径：** `GET /api/dashboard/trend`

**需求追溯：** [需求：F10-4.2.2A] [需求：F10-4.2.2B] [需求：F10-4.2.2C] [需求：F10-4.2.3 返回看板/能力类型切换/页面加载自动查询] [需求：F10-4.2.4 规则1/2/3] [需求：F10-4.2.5] [需求：F10-5.2]

**说明：** 按能力类型返回单模块近 8 期逐期序列：综合分摘要、各维度走势（置空期次保留断线）、本期较上期变化、本期短板标识。数据固定取最新期次，无区间切换参数（specs §4.2.4 规则1）。区间序列由三表落库周期并集推导，同 A1 口径。`type` 非法值按 use 兜底（specs §5.2.2 步2），不报错。本接口不调 userapi（无全员分母语义），无 1305 路径。各期维度均分与综合分逐期按 A1 §modules 同口径计算（同期剔除 insufficient 与 failed、< 3 人置空），短板标识按本期共性短板判定结果标注（specs §4.2.4 规则3）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| type | string | 否 | use | 能力类型 [可选值：use AI 使用能力/manage AI 管理能力]，映射模块 AI_USAGE（source=conversation）/ AI_MGMT（source=active_test）；非法值按 use 兜底（specs §5.2.2 步2） |

**请求示例：**

```
GET /api/dashboard/trend?type=manage
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "type": "manage",
    "module": "AI_MGMT",
    "periods": [
      { "period_start": "2026-09-17", "period_end": "2026-09-23", "is_current": false },
      { "period_start": "2026-09-24", "period_end": "2026-09-30", "is_current": false },
      { "period_start": "2026-10-01", "period_end": "2026-10-07", "is_current": true }
    ],
    "current_period": { "period_start": "2026-10-01", "period_end": "2026-10-07" },
    "data_updated_at": "2026-10-05 02:12",
    "composite": { "score": 74, "change_vs_prev": 3, "dimension_count": 5 },
    "dimensions": [
      {
        "dimension_code": "MGT_DELEGATION",
        "dimension_name": "授权分工",
        "is_weakness": true,
        "history": [
          { "period_start": "2026-09-17", "period_end": "2026-09-23", "score": null },
          { "period_start": "2026-09-24", "period_end": "2026-09-30", "score": 72 },
          { "period_start": "2026-10-01", "period_end": "2026-10-07", "score": 76 }
        ],
        "current_score": 76,
        "prev_score": 72,
        "change": 4
      }
    ]
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| type | string | 实际生效能力类型 [可选值：use/manage]（兜底后回显） |
| module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT]，type 的模块映射 |
| periods | array | 观察窗口：三表落库周期并集新到旧取前 8 项再反转旧到新（不足 8 期按实际，specs §4.2.4 规则1）；条目含 period_start / period_end / is_current（最新期 true）；该模块无任何落库数据时仍可能有区间（并集含另一模块的期次），空态判定见 composite 与 dimensions |
| current_period | object \| null | 本期区间（窗口最新项）；窗口为空为 null |
| data_updated_at | string \| null | 本期数据更新时间（同 A1 §data_updated_at 口径，取本期区间批次终态时间） |
| composite | object \| null | 综合分摘要卡；本期该模块全部维度置空（无参与维度）为 null（空态语义） |
| composite.score | integer | 模块综合分：本期各维度全员均分（未取整原始值）的算术平均，取整（specs §4.2.2 A、§4.2.4 规则2） |
| composite.change_vs_prev | integer \| null | 较上期变化：本期减上一落库区间综合分，按取整后综合分之差计算；上一区间不在窗口、无数据或综合分为空为 null（前端不显示） |
| composite.dimension_count | integer | 评估维度数：本期参与综合分计算的维度计数（置空维度剔除，specs §4.2.2 A 观察窗口元信息） |
| dimensions | array | 模块内当前启用维度全集走势（specs §4.2.4 规则3：走势序列含全部维度，标识仅作视觉引导）；空态为空数组 |
| dimensions[].dimension_code / dimension_name | string | 维度编码与显示名（维度配置现读） |
| dimensions[].is_weakness | boolean | 本期共性短板标识（按 A1 同款判定结果的本期口径标注，历史期次不回溯，specs §4.2.4 规则3） |
| dimensions[].history | array | 近窗口期数逐期均分序列（与 periods 一一对应，旧到新）：score 为该期该维度全员均分取整；该期置空（剔除后 < 3 人或无行）为 null（前端走势断线、变化表显示 -，specs §4.2.4 规则2） |
| dimensions[].current_score | integer \| null | 本期分（history 末项，冗余直出便于变化表渲染）；置空为 null |
| dimensions[].prev_score | integer \| null | 上期分（history 倒数第二项）；窗口不足两期或上期置空为 null |
| dimensions[].change | integer \| null | 变化 = 取整后 current_score 减 prev_score；任一期缺失为 null（前端显示 -） |

**空态语义：** 该模块无任何落库维度数据时返回 `{periods: [...窗口区间或空], current_period: <窗口最新项或 null>, data_updated_at: null, composite: null, dimensions: []}`，不视为错误，前端整页空态提示（specs §4.2.5）。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1500 | 服务内部错误 |

（type 非法按 use 兜底不报错；无区间参数故无 1400/2101 路径；不调上游无 1305 路径。）

---

## 4. 非页面功能契约

### 4.1 建议生成 tick 任务（dashboard:suggest-tick）

| 项 | 值 |
|----|----|
| 任务类型 | `dashboard:suggest-tick`（TypeSuggestTick 常量） |
| 触发 | scheduler 每分钟 cron（`*/1 * * * *`，与 batch-tick 同频独立注册） |
| 队列 | default（tick 类轻任务，不与 batch/extract 抢占消费并发） |
| 任务级超时 | 60s |
| payload | 空（无参数，扫描全量候选） |

**处理流程：**

```
输入:  无 payload
流程:  1. 扫描周期批次（trigger_type=scheduled）中状态为已终态
          （success/partial_failed/failed）且需生成建议的批次，
          按批次时间最早取一条为待处理批次。
          需生成建议的判定（满足其一，specs §5.1.4 规则2）：
          a. 该 period（双界）在 team_training_suggestions 无对应建议行
          b. 该 period 存在建议行，且存在比建议行所属批次更新的同 period
             重跑批次，其终态为 success（部分失败/失败的重跑不覆盖既有建议）
       2. 无命中直接返回 nil（生成中行存在期间扫描条件不命中，
          即拾取锁，specs §5.1.4 规则5）
       3. 幂等建行/续作（specs §5.1.2 步2）：
          a. 无行 → INSERT 生成中行（status=generating，携带批次号与 period）
          b. 重生成场景（判定 b 命中）→ 既有终态行重置为 generating 续作，
             批次号随行更新（period 唯一约束兜底，uk_suggestion_period）
       4. 投递 dashboard:suggest-generate 任务（payload 携批次雪花 ID 十进制
          字符串），投递失败 err 透传交 Asynq 任务级重试（行保持 generating，
          重试 tick 被生成中拾取锁拦下空转，恢复由步5 滞留续投兜底）
       5. 滞留续投兜底（specs/03 原始定义未覆盖的实现期补充）：
          生成中行超 suggestStuckThreshold（20min）未更新视为任务丢失，
          touch 更新时间后按行内 batch_no 重投生成任务；行内批次已不存在
          （批次被删）则落 failed 终结，下个 tick 走终态重置续作
返回:  err == nil → 成功（含无命中空转）
       err != nil → 交 Asynq 任务级重试
幂等:  tick 幂等扫描；生成中行即拾取锁；漏生成（停机窗口错过的批次）
       由后续 tick 自动补齐（specs §5.1.4 规则2）
日志:  拾取记 INFO 含批次号；扫描异常记 ERROR
```

**需求追溯：** specs §5.1.2 步1/步2/步6、§5.1.4 规则1/2/5。

### 4.2 建议生成任务（dashboard:suggest-generate）

| 项 | 值 |
|----|----|
| 任务类型 | `dashboard:suggest-generate`（TypeSuggestGenerate 常量） |
| 触发 | suggest-tick 投递（无 cron 注册） |
| 队列 | default |
| 任务级超时 | 240s（specs §5.1.4 规则5 初值，对齐阅卷链路量级） |
| MaxRetry | 3（specs §5.1.4 规则3 基准 3 次，投递侧收紧） |
| payload | `{"batch_id": "<雪花ID十进制字符串>"}`；坏格式/非正数直接返回 nil 丢弃记 ERROR |

**处理流程：**

```
输入:  batch_id（定位批次行与 period）
流程:  1. 读批次行定位 period；查建议行确认 generating（行缺失或非
          generating 为竞态残留，幂等返回 nil）
       2. 汇总素材（specs §5.1.2 步3，口径同看板 A1）：
          a. 两模块各维度全员均分与低分占比（剔除 insufficient/failed）
          b. 两模块聚合分（aggregate_scores 模块行 module_score 平均，
             上游加权平均口径，与趋势页综合分的维度均分算术平均口径不同）
          c. 活跃度三态计数（activity_stats 该区间全行）
          d. 共性短板维度清单与维度口径快照（dimensions 评分锚点摘要）
       3. 批次数据异常判定（specs §5.1.5 末行）：该 period 在
          dimension_scores、aggregate_scores 与 activity_stats 三表均无
          任何行（任一表有行即视为有素材）→ 建议行落 failed（记因）返回
          nil，避免 tick 反复重扫；仅 activity_stats 有行的正常周期按可用
          素材照常生成
       4. 组装提示词（固定模板 + 结构化素材，不含任何单人明细与对话原文，
          specs §5.1.4 规则4）调用当前启用大模型（专用 client，§4.4），
          要求产出两模块各 2-4 条培训方向建议（每条含方向名称与说明）
          与一段团队综合研判
       5. 校验 LLM 返回结构（两模块建议条数 2-4、字段完整性，schema 校验
          白名单收敛），rationale 类文案（建议说明与综合研判）经 Redact
          脱敏（evaluator/grading 同款，DB 故障回退出厂正则集）
       6. 建议行落终态（specs §5.1.2 步5）：status=generated，携带批次号、
          period 起止、两模块建议 JSON、综合研判、generated_at
返回:  err == nil → 成功
       err != nil（LLM 失败/超时/结构校验失败/落库失败）→ 交 Asynq 任务级
       重试（30s 基准倍增退避），沿用既有 generating 行续作
       重试耗尽（asynq.GetRetryCount >= MaxRetry）→ 建议行落 failed 与
       失败原因，WARN 记批次号返回 nil（specs §5.1.4 规则3，不产告警信号）
幂等:  同 period 至多一行（uk_suggestion_period）；重试沿 generating 行
       续作，终态更新按条件写（status=generating 守卫）
日志:  记批次号与错误摘要，禁止输出 prompt 与建议原文
```

**需求追溯：** specs §5.1.2 步3-5、§5.1.4 规则3/4/5、§5.1.5 异常表。

### 4.3 服务端组装的查询口径（开发契约）

```
A1 看板组装：
  1. 三表全表 distinct period 双界归并区间列表（新到旧）；空列表走空态
     返回（跳过 userapi，§1.6）
  2. 定位 selected（未传取首项；传入须双界精确匹配，否则 2101）
  3. userapi.WalkStaffPages 全量拉取名单计数 staff_total（失败 1305）
  4. 批量 IN 查询（specs §5.2.4 规则1，禁止逐人逐维度查询）：
     - activity_stats：所选区间全行（三态计数）+ 上一落库区间全行（环比）
     - dimension_scores：所选区间全公司两模块行（IN 查询 + 内存聚合，
       剔除 insufficient/failed；AI_USAGE 取 source=conversation、
       AI_MGMT 取 source=active_test）
     - assessment_batches：period 双界匹配行的 finished_at（data_updated_at）
     - team_training_suggestions：period 新到旧第一条（建议取行口径，
       specs §4.1.4 规则6：恒最新快照不随所选区间）
  5. 九型：全员名单传入 ListLatestScoredByStaffNames（既有方法，
     grading_status=scored 且 main_type 非空过滤，§1.9），集合统计
     9 型占比、覆盖数与主导/次主导（并列按型别序号升序）
  6. 内存判定共性短板（未取整原始值，specs §4.1.4 规则4）后取整组装

A2 趋势组装：
  1. 区间列表并集取前 8 项（新到旧）反转为窗口（旧到新）
  2. dimension_scores：该模块窗口全区间全公司行（source 按模块映射）
     批量取数，逐区间按 A1 同口径计算各维度均分（< 3 人置 null）
  3. 逐期综合分（参与维度均分的算术平均，任一维度置空按剩余维度算，
     全置空该期综合分为 null）；change_vs_prev 按取整后综合分之差
  4. 批次终态时间同 A1；短板标识复用本期判定结果
  5. type 非法按 use 兜底（不报错）
```

**需求追溯：** specs §5.2.2 步1/步2、§5.2.4 规则1/2/3。

### 4.4 专用 LLM client 与计算常量

| 常量 | 初值 | 说明 |
|------|------|------|
| 建议生成 LLM client Timeout | 180s | 独立装配的专用 client（与评估 180s、出题 120s、阅卷 240s 的专用 client 模式同构），小于任务级超时 240s 保重试边界自洽；并发 gate 独立（在飞上限 4，与全局/评估/出题/阅卷通道隔离） |
| suggestTickTimeout | 60s | tick 任务级超时（§4.1） |
| suggestGenerateTimeout | 240s | 生成任务级超时（specs §5.1.4 规则5 初值） |
| suggestMaxRetry | 3 | 生成任务 Asynq MaxRetry（specs §5.1.4 规则3 基准 3 次） |
| suggestgenMaxTokens | 6000 | 建议生成 LLM 输出 token 上限（引擎调用参数，非业务规则） |
| trendWindowSize | 8 | 趋势观察窗口期数（specs §4.2.4 规则1） |
| avgMinPeople | 3 | 维度均分最小有数据人数（< 3 置空，specs §4.1.4 规则3；与 F9 CompanyAvgMinPeople 同值异名常量） |
| weaknessMaxCount | 2 / weaknessHardCap 3 | 共性短板每模块最低 2 维、并列至多 3 项（specs §4.1.4 规则4） |
| lowScoreLine | 60 | 低分占比的分数线（specs §4.1.4 规则4，与 F9 核心结论短板阈值一致） |

以上随源码发版（specs §5.1.4 规则4 成本口径与 §5.2.4 规则3 缓存口径的确定性常量，非在线配置）。

### 4.5 对 F9 的跳转契约（纯前端，无接口）

specs §4.1.3 共性短板人群跳转与未使用人群跳转经前端路由 search 参数承载，F9 列表页补 URL 参数预填能力（specs §7.2 已声明的改造依赖）：

| 跳转来源 | 目标路由 | search 参数 |
|---------|---------|------------|
| 共性短板标签 | F9 人员画像列表页 | `dimension_code=<短板维度 code>` |
| 未使用人数卡 | F9 人员画像列表页 | `unused_only=true` |

F9 列表接口（GET /api/profiles）不变，预填为其前端路由层的查询参数初始化。跳转人群为 F9 本期口径（各数据项最新已落库周期），与看板所选历史区间可能不同期（specs §4.1.3 约束说明，接口侧无对齐动作）。

---

## 5. 错误码

新增 21xx 段共 1 个码；上游名单失败复用既有 1305，参数错误复用 1400。

| 错误码 | 常量 | 含义 | 使用场景 |
|--------|------|------|---------|
| 2101 | DashboardPeriodInvalid | 评估区间不在落库区间列表内 | A1 所选区间查无对应落库周期行（陈旧直链/并发场景可达；正常交互区间选项来自后端 periods 不触发；前端按通用错误态整页占位处理，与 specs §4.1.5 一致） |

---

## 6. 与相邻域的边界

- **对 P2_ASM_001（F6 批次/对话分析）**：只读消费 activity_stats、dimension_scores（source=conversation）、aggregate_scores 三表落库行与 assessment_batches 终态记录（data_updated_at、建议生成拾取）；不调用其服务、不触发评估。建议生成 tick 与批次 tick 相互独立，无跨域任务依赖。
- **对 P2_TST_001（F7 主动测试）**：只读消费 dimension_scores（source=active_test）与 assessment_test_results（经 tasks join 取人，§1.8 过滤口径）。任务运营接口不触碰。
- **对 P2_DIM_001（维度配置）**：只读消费维度启用集合与显示名（雷达轴、短板判定、走势分面的维度基准）；复用 dimensions 仓储现读（ListAll），响应自带 dimension_name，前端无需另调 /api/dimensions/tree。
- **对 P2_TECH_001（LLM 底座）**：建议生成走专用 LLM client（§4.4），模型取当前排他启用模型；看板查询链路零 LLM 调用。
- **对 P2_PRF_001（F9 个人画像）**：人群定位经前端路由跳转承接（§4.5），无服务间调用；共用维度均分聚合口径与九型降级过滤口径（specs §4.1.4 规则3/规则5 与 F9 规则4/§1.8 同源）。
- **对 P2_SYS_001（配置/集成）**：全员名单经 userapi 客户端 + 集成密钥解析（失败统一 1305，与 /api/staffs 同源降级）。
- **对 F11（工作台，未建）**：工作台聚合态势归 F11，本域不产出工作台数据；建议行与看板聚合可供 F11 只读复用（数据依赖，届时由 F11 设计定义）。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1 看板页（区间切换、三块数据、短板跳转、未使用跳转、趋势跳转、雷达悬浮、加载自动查询）→ A1 + 前端交互；4.2 趋势页（类型切换、返回看板、加载自动查询）→ A2 + 前端交互；5.1 建议生成 → §4.1/§4.2 任务契约；5.2 聚合查询接口 → A1/A2（§4.3）。
- [x] 字段定义与 specs 一致：4.1.2 五区字段、4.2.2 三区字段在请求/响应结构一一对应；七处适配已声明（方法归一化 §1.2、区间口径 §1.3、精度分工 §1.4、建议状态 §1.5、空态跳上游 §1.6、两段式任务 §1.7、无批次兜底 §1.8、降级行过滤 §1.9）。
- [x] 业务规则在接口层落地：区间一致性（三表并集 + 2101 校验 + 九型/建议不随区间）、活跃度口径（三态计数 + 无行计未使用 + 环比 + 占比一位小数）、均分聚合（剔除 insufficient/failed + < 3 人置空 + 取整口径）、共性短板（≥ 3 人最低 2 维 + 并列截断 + 低分占比）、九型快照（最新判型集合 + 并列型别序号）、培训建议（period 新到旧取行 + 状态感知 + 脱敏）、隐私红线（§2.6 聚合统计与脱敏文案 only）。
- [x] 状态定义与 specs 第 6 章一致：建议行三态（生成中/已生成/生成失败）映射 status 三值加 none 无行态，转换条件与触发方式见 04 文档 §3.1 业务规则；看板其余数据为上游落库状态聚合映射无用户触发转换。
- [x] 权限规则一致：全部接口挂 JWT，无角色差异（specs §2.2），员工侧无 HTTP 面（员工非平台用户）。
- [x] 技术层偏差已声明：§1.2-1.9 八条。
- [x] 接口与数据模型一致性：A1/A2 响应字段均为上游表列投影或查询期派生，唯一新表 team_training_suggestions 供建议区消费（[04_model_interface.md](04_model_interface.md) §3.1/§4 索引核对）。

---

## 8. 不涉及的设计

- 看板聚合结果的物化落库与服务端缓存（表、Redis 缓存、定时预热）：specs §5.2.4 规则3 聚合实时计算不落库不加缓存。
- 按部门钻取的看板分片：specs §2.1 首期无组织架构，看板为全公司整体聚合，部门维度后置。
- 历史建议回看接口（按区间查建议历史）：specs §4.1.4 规则6 明确回看历史建议本期不承载，仅取最新一条。
- 建议生成的手动触发/重生成 HTTP 接口：specs §5.1 触发方式仅 tick 扫描，无运营手动入口。
- F9 列表页 URL 预填的实现：前端路由层改造，随本 feature 开发计划交付（§4.5 契约已记），无接口变更。
- 员工侧看板入口：员工非平台用户（specs §2.1），无此面。

---

**文档版本：** v1.0
**最后更新：** 2026-10-05
**作者：** lixuetao
