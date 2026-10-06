# 工作台 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_WRK_001_FEAT_工作台 |
| 模块代号 | WRK（工作台域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-06 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[04_model_interface.md](04_model_interface.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

业务需求层面以 specs 为准（字段、规则、权限、状态），技术实现层面以规则文件为准（方法约定、响应结构、错误码段位）。冲突与技术层补充逐条在 §1.2-1.10 声明。

### 1.2 HTTP 方法归一化：本域全 GET

specs 未指定 HTTP 方法。工作台是纯只读消费页（specs §2.2：查看、跳转），HTTP 面只有一个聚合查询接口，GET（规则文件 §2.1 GET 仅查询）。跳转全部经前端路由承接（§4.4），无 POST。

### 1.3 单接口全量聚合与区块空标记降级（specs §5.1 的直接落地）

specs §5.1.1 明文「一个只读聚合接口，一次返回工作台全量数据」，本域只建 `GET /api/workspace` 一个接口。与 F10 看板「上游失败整体 1305」的处置不同（specs §5.1.4 规则2 与 F10 §5.2.4 规则2 表述相反，各自 SSOT 各自落地）：工作台任一数据源查询失败时对应区块返回空标记（null / 空值对象），接口整体不失败、不返回 1305。降级粒度裁定：

| 失败源 | 降级范围（区块/字段级） | 前端呈现 |
|--------|----------------------|---------|
| assessment_configs 读取（下次跑批推算） | `batch.next_trigger_at` null | 该字段空态，态势卡其余照常 |
| assessment_batches / assessment_alerts / test_tasks 查询 | `batch.status` / `batch.alert_count` / `batch.overdue_count` / `batch.data_updated_at` 各自 null | 态势卡对应字段空态 |
| 三表区间并集（ListPeriods） | `current_period` / `trend` / `profile` / `attention` 全 null | 演进、画像、关注三区块空态 |
| userapi 全员名单 | `trend.activity` null + `attention` null | 演进区活跃两张卡与关注表空态（specs §5.1.5 首行） |
| dimension_scores / dimensions | `trend` null + `profile` null + `attention` null | 演进、画像与关注三区块空态（specs §5.1.5 次行；weak_dims 依赖 dimension_scores 同期行） |
| activity_stats | `trend.activity` null + `attention` null | 同名单失败范围 |
| aggregate_scores（ListModuleAggScoresByPeriods） | `attention` null | 关注表空态（短板人群与两模块总分不可判定，specs §5.1.5 聚合数据行；三表并集来源之一失败已由 ListPeriods 行覆盖） |
| assessment_test_results（九型） | `profile.enneagram` null | 九型柱状区空态 |
| team_training_suggestions | `profile.suggestion` 降级为 `{status:"none", summary:""}` | 研判区暂无数据，短板标签照常（specs §4.1.4 规则5） |

区块 null 与区块正常空数据（如九型无人判型）呈现相同（区块空态提示），前端无区分消费场景（specs §4.1.4 规则4 各区块独立空态），null 统一承载。

九型取数对名单的处理：判型行到人的映射不需要上游名单（assessment_test_results 经 tasks 自带 staff_name，§4.2 ListAllLatestScored 一次取全员），但覆盖率的分母语义在本域无消费位（速览区不展示覆盖率，§1.7）。因此九型统计独立于名单拉取，名单失败时 `profile.enneagram` 照常返回。

### 1.4 批次态势的定位口径（specs 未覆盖边界补充）

specs §4.1.2 A 定义批次状态取「最新落库区间批次的终态；该区间多条批次取最新终态批次；无终态批次取触发时间最新批次的状态值」。批次定位口径裁定（对齐 F10 §1.8 data_updated_at 的取批次形态）：

1. 三表区间并集首项为最新落库区间，按 period 双界匹配取该区间全部批次行（trigger_type 不限，手动补跑批次一并参与）。
2. 区间内有终态批次（success/partial_failed/failed）→ 取 `finished_at` 最新非空行的状态。
3. 区间内无终态批次 → 取 `triggered_at` 最新行的状态（running，进行中）。
4. 三表无任何落库区间（首批跑批进行中、聚合未落库）→ 回退批次表 `triggered_at` 最新行的状态；批次表也为空 → `batch.status` null（「尚未发起评估」空态，specs §4.1.4 规则4）。

告警计数与数据更新时间恒以最新落库区间为基准：三表无区间时 `alert_count` 计 0（无落库区间即无告警归属）、`data_updated_at` 取回退批次行状态对应的口径（有批次行取该行 finished_at，进行中为 null；无批次行 null）。

### 1.5 需要关注的人的判定与排序口径

specs §4.1.4 规则3 的接口层落地口径：

- **短板人群**：最新落库区间 aggregate_scores 两模块行中 `module_score` 四舍五入取整后 < 60 的人（判定与排序统一取整口径，specs 术语「总分取整后判定」）；按该人两模块（仅取低于 60 的模块参与最小值）取整总分的最小值升序取前 5。`weak_dims` 为该人该模块本期个人短板维度集合，口径同 F9 规则 4（本期参与聚合维度中分数最低的维度，并列全部入选；参与聚合以聚合行 included_json 的 code 集合判据，与 buildShortboardSets 同源）。
- **未使用人群**：本期未使用 = 本期 activity_stats 行判 unused 的人 + 全员名单中无本期统计行的人（口径同 F10 buildActivity 的 unused 合成）；按最近活跃距今天数降序取前 5。最近活跃距今天数 = 最新落库区间 `period_end_at` 与该人最近一条非未使用（active/low_freq）activity_stats 行 `period_end_at` 之差折算天数（两端均本地零点，Unix 秒差 / 86400 取整）；无任何统计行者自首个落库区间（并集最旧项）`period_start_at` 起算。
- **合并与排序**：短板人群在前、未使用人群在后，合计至多 10 行，两类各自至多 5 人、不跨类补足（specs §4.1.4 规则3）。短板与未使用按口径天然互斥（未使用者无聚合行），接口不做去重。

### 1.6 活跃率环比的 pp 口径（与 F10 计数差口径的偏差）

specs §4.1.2 B 明文活跃率卡为「±pp 环比、占比与环比均保留一位小数」。F10 activity.mom 是计数差（int），工作台演进区活跃率环比按**百分点差（float 一位小数）**计算：本期活跃率减上一落库区间活跃率（两期占比均一位小数精度参与计算，差值再取一位小数）。未使用人数卡环比沿用计数差 int（specs 同字段表述为「±环比」同 F10 口径）。两模块综合分卡环比为取整后综合分之差 int（specs §4.1.2 B「取最新区间值与上一区间差」，同 F10 A2 change_vs_prev）。

### 1.7 画像速览区的精简投影（复用 F10 同源口径）

specs §4.1.4 规则5 明文画像速览区直接复用 F10 同源数据、不新建生成逻辑。接口层复用 F10 的聚合口径（维度均分、共性短板判定、九型统计、建议行取数），响应做消费裁剪：雷达只带 avg_score/is_weakness（工作台无 low_ratio 展示位，低分占比仅短板标签消费）；九型只带 distribution/dominant_type（速览无覆盖率与次主导消费位，specs §4.1.2 C「主导型高亮」）；研判只带 status/summary（工作台不消费建议清单 modules）。裁剪只动响应字段，聚合计算与 F10 共用同一套 service 内聚实现，避免两套口径（specs §5.1.4 规则1）。

### 1.8 演进区序列的综合分口径

两模块综合分 = 该模块本期各维度全员均分（未取整原始值，剔除 insufficient 与 failed 行、< 3 人维度置空剔除）的算术平均取整，与 F10 A2 趋势页 composite 完全同口径（specs §4.1.2 B「口径同 F10 趋势页」）。近 8 期窗口 = 三表区间并集新到旧取前 8 项再反转旧到新（同 F10 A2）；某模块某期无数据该点位 null（折线断开）。当前期综合分或上一期任一缺失时 change_vs_prev null（前端环比区段不显示，specs §4.1.5）。

### 1.9 区间与时间格式

沿用 F10 §1.3 既定口径：区间 `yyyy-MM-dd` 双端含止日（period_end 是该区间最后一个自然日），与落库行双界精确匹配。`next_trigger_at` / `data_updated_at` 为 `yyyy-MM-dd HH:mm` 分钟精度（specs §8.3 偏离第 2 条），本地时区直出。

### 1.10 零新增错误码

本域无入参（无 1400 路径）、上游与数据源失败全部区块降级不报错（无 1305 路径）、无区间校验（固定本期，无 2101 类路径），唯一整体失败路径是内部错误 1500。工作台域 **22xx 段预留给后续 feature，本功能零新增错误码**。

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

本域接口挂 JWT 鉴权，无公开接口。平台不设角色权限模型，任意已登录账号可见全公司口径工作台（specs §2.1）。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

**限流：** 不单独限流，沿用受保护路由组既有策略。页面加载单次触发、本页无轮询（specs §4.1.3 前端自动流程明文），单账号稳态调用量远低于需限流门槛。

### 2.2 统一响应格式

遵循规则文件 §2.2。`data` 无 omitempty，成功无载荷时为 `null`。本接口为聚合对象响应，无分页（specs §8.3 偏离第 1 条：关注表固定 10 行速览不分页）。

### 2.3 错误码段位

沿用千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500、批次 16xx、题库 17xx、主动测试 18xx、员工作答 19xx、个人画像 20xx、团队看板 21xx。工作台域分配 **22xx 段**（代码事实核对：21xx 为当前最大段，22xx 起空闲），本功能零新增（§1.10），段位登记供后续 feature 使用。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

本域接口**不回显任何雪花 ID**：人员标识是 `staff_name` 字符串、维度标识是 `dimension_code` 字符串、区间是日期对，响应无实体主键与外键转运字段。规则文件 §1.2 的双层 string 化约束在本域 HTTP 面无落点（取数经仓储行投影，见 04 文档）。

### 2.5 隐私边界

- 响应任何字段只呈现聚合统计（均分、人数、占比、计数）、脱敏研判文本与人员姓名/活跃分级/模块总分，无对话原文、无单人维度分数明细（短板维度只给维度编码集合）、无评分理由透传。
- 研判摘要为落库脱敏文本（F10 生成链路 Redact）只读透传。
- 聚合实时计算不落库、不加服务端缓存（specs §5.1.4 规则1，与 F10 §5.2.4 规则3 同源）。

---

## 3. 接口列表

共 1 个新接口。无复用接口：维度显示名由本接口响应自带（现读维度配置），跳转经前端路由承接（§4.4）。

需求追溯编号对应 specs 第 4/5 章功能（F11 为 PRD 5.2.2 的模块编号）。

### W. 工作台首页

#### W1. 查询工作台全量

**接口路径：** `GET /api/workspace`

**需求追溯：** [需求：F11-4.1.2A] [需求：F11-4.1.2B] [需求：F11-4.1.2C] [需求：F11-4.1.2D] [需求：F11-4.1.3 页面数据自动加载/全部跳转入口] [需求：F11-4.1.4 规则1/2/3/4/5] [需求：F11-4.1.5] [需求：F11-5.1]

**说明：** 一次请求返回工作台全量数据：最新落库区间批次态势（状态、下次跑批时点、告警 0/1 计数、逾期任务数、数据更新时间）、两模块近 8 期综合分序列与四卡环比、活跃率与未使用计数、双模块维度均分雷达、九型快照、共性短板标签、研判摘要、需要关注的人（至多 10 行）。固定本期（最新落库区间），无区间参数（specs §4.1.4 规则1）。聚合实时计算不落库（specs §5.1.4 规则1）。任一数据源失败时对应区块空标记降级，接口整体不失败（specs §5.1.4 规则2，降级矩阵见 §1.3）。

**查询参数：** 无。

**请求示例：**

```
GET /api/workspace
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "batch": {
      "status": "success",
      "next_trigger_at": "2026-10-14 23:00",
      "alert_count": 1,
      "overdue_count": 3,
      "data_updated_at": "2026-10-08 02:12"
    },
    "current_period": { "period_start": "2026-10-01", "period_end": "2026-10-07" },
    "trend": {
      "periods": [
        { "period_start": "2026-09-10", "period_end": "2026-09-16" },
        { "period_start": "2026-09-17", "period_end": "2026-09-23" },
        { "period_start": "2026-09-24", "period_end": "2026-09-30" },
        { "period_start": "2026-10-01", "period_end": "2026-10-07" }
      ],
      "series": [
        {
          "module": "AI_USAGE",
          "scores": [58, 62, 65, 67],
          "current_score": 67,
          "change_vs_prev": 2
        },
        {
          "module": "AI_MGMT",
          "scores": [null, null, 59, 61],
          "current_score": 61,
          "change_vs_prev": 2
        }
      ],
      "activity": {
        "active_ratio": 41.7,
        "active_change_pp": 2.5,
        "unused_count": 40,
        "unused_change": -3
      }
    },
    "profile": {
      "modules": [
        {
          "module": "AI_USAGE",
          "dimensions": [
            { "dimension_code": "AI_BASE_CLARITY", "dimension_name": "指令清晰度", "avg_score": 76, "is_weakness": false },
            { "dimension_code": "AI_UPPER_PLAN", "dimension_name": "任务规划", "avg_score": 58, "is_weakness": true }
          ],
          "overall_avg": 67
        },
        {
          "module": "AI_MGMT",
          "dimensions": [
            { "dimension_code": "MGT_DELEGATION", "dimension_name": "授权分工", "avg_score": 61, "is_weakness": true }
          ],
          "overall_avg": 61
        }
      ],
      "enneagram": {
        "distribution": [
          { "type": "1", "count": 8, "ratio": 10.0 },
          { "type": "2", "count": 10, "ratio": 12.5 },
          { "type": "3", "count": 18, "ratio": 22.5 },
          { "type": "4", "count": 7, "ratio": 8.8 },
          { "type": "5", "count": 6, "ratio": 7.5 },
          { "type": "6", "count": 9, "ratio": 11.3 },
          { "type": "7", "count": 12, "ratio": 15.0 },
          { "type": "8", "count": 5, "ratio": 6.3 },
          { "type": "9", "count": 5, "ratio": 6.3 }
        ],
        "dominant_type": "3",
        "dominant_ratio": 22.5
      },
      "weaknesses": [
        { "module": "AI_USAGE", "dimension_code": "AI_UPPER_PLAN", "dimension_name": "任务规划", "low_ratio": 66.7 },
        { "module": "AI_MGMT", "dimension_code": "MGT_DELEGATION", "dimension_name": "授权分工", "low_ratio": 40.0 }
      ],
      "suggestion": { "status": "generated", "summary": "团队整体 AI 使用能力稳健，任务规划为共性短板……" }
    },
    "attention": [
      {
        "staff_name": "李芳",
        "category": "weak",
        "activity_level": "low_freq",
        "ai_usage_score": 52,
        "ai_mgmt_score": null,
        "weak_modules": [
          { "module": "AI_USAGE", "score": 52, "weak_dims": ["AI_UPPER_PLAN", "AI_BASE_LOGIC"] }
        ],
        "days_since_active": null
      },
      {
        "staff_name": "王强",
        "category": "unused",
        "activity_level": "unused",
        "ai_usage_score": null,
        "ai_mgmt_score": null,
        "weak_modules": [],
        "days_since_active": 12
      }
    ]
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| batch | object | 态势与待处理统计卡区（specs §4.1.2 A）。恒返回对象，内部字段按数据源独立降级（§1.3），全字段可 null |
| batch.status | string \| null | 最新周期批次状态 [可选值：running 进行中/success 成功/partial_failed 部分失败/failed 失败]（口径同 F6 批次状态，定位口径见 §1.4）；批次表为空（尚未发起评估）或查询失败为 null，前端按 null 区分「尚未发起评估」空态与引导入口（specs §4.1.4 规则4） |
| batch.next_trigger_at | string \| null | 下次跑批时点 `yyyy-MM-dd HH:mm`，评估周期配置推算（口径同 F6 计划卡，pipeline.NextTriggerAt）；配置读取失败为 null |
| batch.alert_count | integer \| null | 告警次数 0/1 布尔计数：最新落库区间全部批次的告警信号记录整体存在性判定，存在任一即 1，多批次不清零不累计（specs §4.1.4 规则2）；三表无任何落库区间时计 0（无告警归属，§1.4）；查询失败为 null |
| batch.overdue_count | integer \| null | 主动测试逾期任务数：assessment_test_tasks 当前 status=expired 计数（ai_mgmt 与 enneagram 两类合计，specs §4.1.2 A）；查询失败为 null |
| batch.data_updated_at | string \| null | 数据更新时间 `yyyy-MM-dd HH:mm`：最新落库区间批次终态时间（多批次取最新终态，无批次取区间周期终点，同 F10 §1.8 口径；§1.4 回退场景有批次进行中为 null）；无区间且无批次或查询失败为 null |
| current_period | object \| null | 本期区间（最新落库区间，含止日展示口径）；三表无任何落库区间为 null（演进、画像、关注区块同步空态） |
| trend | object \| null | 团队能力演进区（specs §4.1.2 B）；区间并集或维度聚合失败为 null，三表无区间为 null |
| trend.periods | array | 近 8 期窗口（三表区间并集新到旧取前 8 反转旧到新，不足按实际，同 F10 A2），条目含 period_start / period_end（yyyy-MM-dd 含止日） |
| trend.series | array | 两模块综合分序列，恒含 AI_USAGE 与 AI_MGMT 两行 |
| trend.series[].module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT]，AI_USAGE 取 source=conversation 行、AI_MGMT 取 source=active_test 行聚合 |
| trend.series[].scores | array | 与 periods 一一对应的逐期综合分（各维度未取整均分算术平均取整，< 3 人维度置空剔除，全置空该期为 null 断点，§1.8）；窗口内该模块无任何落库行为全 null 数组 |
| trend.series[].current_score | integer \| null | 本期综合分（scores 末项冗余直出）；本期该模块无数据为 null（卡片空态） |
| trend.series[].change_vs_prev | integer \| null | 环比：本期减上一落库区间综合分（取整后分差）；任一期缺失为 null（环比区段不显示，specs §4.1.5） |
| trend.activity | object \| null | 活跃率与未使用两卡（specs §4.1.2 B 后两卡）；名单或 activity_stats 失败为 null，三表无区间为 null |
| trend.activity.active_ratio | float | 本期活跃率：活跃人数 / 全员人数，一位小数（活跃口径同 F10：本期行判 active + 无统计行不计，分母为 userapi 全员名单计数） |
| trend.activity.active_change_pp | float \| null | 活跃率环比百分点差（本期减上一落库区间活跃率，一位小数，§1.6）；本期为最早区间或上期无数据为 null |
| trend.activity.unused_count | integer | 本期未使用人数（统计行判 unused + 无统计行两类合成，口径同 F10 buildActivity） |
| trend.activity.unused_change | integer \| null | 未使用环比计数差（本期减上一落库区间）；本期为最早区间为 null |
| profile | object \| null | 本期团队整体画像速览区（specs §4.1.2 C）；区间并集、维度配置或维度聚合失败为 null |
| profile.modules | array | 两模块雷达卡，恒含 AI_USAGE 与 AI_MGMT 两行（聚合口径同 F10 A1 modules，§1.7 精简投影） |
| profile.modules[].module | string | 模块编码 [可选值：AI_USAGE/AI_MGMT] |
| profile.modules[].dimensions | array | 模块内当前启用维度全集（雷达轴随维度配置动态），条目含 dimension_code / dimension_name / avg_score（全员均分取整，< 3 人为 null 该轴断开）/ is_weakness（共性短板标识） |
| profile.modules[].overall_avg | integer \| null | 全维度均值参考线（各维度未取整均分的算术平均取整）；任一维度置空为 null（前端隐藏参考线） |
| profile.enneagram | object \| null | 九型构成快照（全员最新 scored 判型行集合，恒取最新快照不随区间，聚合口径同 F10 §1.9：过滤 grading_status=scored 且 main_type 非空）；无任何判型行或查询失败为 null（前端整区空态）。速览区无覆盖率消费位（§1.7），分布占比分母为 scored_count 本身 |
| profile.enneagram.distribution | array | 九型分布，恒 9 项按型别序号 "1"-"9" 升序，条目含 type / count / ratio（占 scored_count 一位小数）；型名映射与固定型别特征由前端 i18n 承载 |
| profile.enneagram.dominant_type / dominant_ratio | string / float | 主导型数字串与占比（占比最高型，并列按型别序号升序取先，口径同 F10）；主导型柱高亮由前端按 dominant_type 渲染 |
| profile.weaknesses | array | 共性短板标签列表：两模块 is_weakness=true 维度的合并清单（判定口径同 F10 §4.1.4 规则4：参与聚合人数 ≥ 3 的维度中未取整均分最低 2 维、并列至多 3 项），条目含 module / dimension_code / dimension_name / low_ratio（低分占比一位小数，标签副文案）；无满足条件维度为空数组；点击携 dimension_code 跳 F9（§4.4） |
| profile.suggestion | object | 研判摘要（恒返回对象，specs §4.1.4 规则5）：取 team_training_suggestions 最新建议行（取行口径同 F10：恒最新快照）；查询失败降级为 `{status:"none", summary:""}`（§1.3） |
| profile.suggestion.status | string | [可选值：generated 已生成/generating 生成中/failed 生成失败/none 无建议行或查询失败]；非 generated 前端研判区显示暂无数据，不阻断短板标签（specs §4.1.4 规则5） |
| profile.suggestion.summary | string | 团队综合研判段落（脱敏落库原文透传，与团队看板同源同文）；非 generated 为空串 |
| attention | array \| null | 需要关注的人表格行（specs §4.1.2 D，至多 10 行：短板人群前 5 在前 + 未使用人群前 5 在后，两类不跨类补足，判定与排序口径见 §1.5）；名单、activity_stats 或聚合行查询失败为 null，无命中人群为空数组 |
| attention[].staff_name | string | 姓名（上游人名，人员标识与 token_name 同源）；无工号（系统约束）。头像为前端以 staff_name 派生的占位样式（如首字母圆块，同原型 person-avatar 位），无接口字段（userapi 无头像数据） |
| attention[].category | string | 上榜类别 [可选值：weak 短板人群/unused 未使用人群]（前端标注与关注原因拼装依据） |
| attention[].activity_level | string | 该人本期活跃分级 [可选值：active/low_freq/unused]（三态标签样式沿用 F9）；无本期统计行按 unused |
| attention[].ai_usage_score | integer \| null | 该人本期 AI 使用能力总分（aggregate_scores AI_USAGE 行 module_score 取整）；无聚合行为 null（前端显示待评估，specs §8.3 偏离第 3 条）；< 60 红色渲染由前端按值判定。展示与判定同口径取整（specs §4.1.4 规则3），与 F9 列表页原始小数展示为有意的跨页差异 |
| attention[].ai_mgmt_score | integer \| null | 同上口径，AI_MGMT 行 |
| attention[].weak_modules | array | 短板人群关注原因载体：低于 60 的各模块条目（多模块全部列出，specs §4.1.2 D），条目含 module / score（取整总分）/ weak_dims（个人短板维度编码集合，F9 规则 4 口径，§1.5）；未使用人群为空数组 |
| attention[].days_since_active | integer \| null | 未使用人群关注原因载体：最近活跃距今天数（§1.5 口径）；短板人群为 null |

**空态语义：** 系统尚无任何落库批次时返回 `{batch: {status: null, next_trigger_at: <配置正常则返>, alert_count: 0, overdue_count: <任务表正常则返>, data_updated_at: null}, current_period: null, trend: null, profile: null, attention: null}`，不视为错误（specs §4.1.4 规则4：态势区「尚未发起评估」引导、其余区块空态）；首批跑批进行中（有批次无聚合区间）时 batch.status 回退 running（§1.4）、current_period 及下游三区块 null。各区块独立空态，不因单一区块无数据阻断整页渲染。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1500 | 服务内部错误（区块降级已吸收单源失败，仅组装级意外错误整体失败） |

---

## 4. 非页面功能契约

### 4.1 服务端组装的查询口径（开发契约）

```
W1 工作台组装（service/workspace.go，独立 WorkspaceService，不复用
    DashboardService 接口但共享聚合纯函数与仓储方法）：
  1. 态势卡（各源独立降级，互不阻断）：
     a. assessment_configs 单例 + pipeline.NextTriggerAt 推下次跑批时点
        （口径同 F6 Plan），失败仅 next_trigger_at null
     b. assessment_batches triggered_at 最新行（批次表空 → status null 空态）
     c. 三表区间并集（复用 DashboardQueryRepository.ListPeriods）：
        - 非空 → 按 §1.4 定位最新落库区间批次集，推 status 与
          data_updated_at（复用 FindLatestFinishedAt），区间批次 ID 集
          查 assessment_alerts 存在性得 alert_count 0/1
        - 空 → status 取步骤 b 回退行，alert_count 0，data_updated_at
          取回退行 finished_at（running 为 null）
     d. assessment_test_tasks status=expired 全表计数得 overdue_count
        （两类合计，一次 count）
  2. 演进区：并集前 8 期窗口反转；ListDimScoresByPeriods 窗口批量取行
     （既有方法），逐期按模块 aggregateDimScores 同口径算均分与综合分
     （< 3 人置空）；activity 三态计数与环比按 F10 buildActivity 同口径，
     活跃率环比按 pp 差（§1.6）
  3. 画像区：本期 ListDimScoresByPeriod + dimensions.ListAll 组装雷达与
     共性短板（weaknessSet 同口径；ListAll 返回全量维度，service 层
     内存过滤 enabled+module，同 F10 dashboard）；九型经 ListAllLatestScored
     （新增，免名单参数）集合统计分布与主导型；suggestions.FindLatest 取研判行
  4. 关注人群：
     a. 短板：本期 ListModuleAggScoresByPeriods（既有方法）两模块行，
        module_score 取整 < 60 入选，按取整最低总分升序前 5；weak_dims
        按 F9 buildShortboardSets 同口径（included_json 参与集合 +
        dimension_scores 同期行最低分并列全选）
     b. 未使用：本期 activity 行判 unused + 名单无统计行合成候选；全量
        activity_stats 行（含历史期）内存聚合每人最近非未使用行
        period_end_at，算最近活跃距今天数（§1.5）；无任何统计行者自
        并集最旧区间起点起算；按天数降序前 5
  5. 全员名单：userapi.WalkStaffPages 全量拉取（活跃率分母与未使用
     候选），失败仅 trend.activity 与 attention 降级（§1.3）
  6. 组装返回：全部字段无雪花 ID（§2.4），区块降级互不阻断
```

**需求追溯：** specs §5.1.2 步1-5、§5.1.4 规则1/2、§5.1.5 异常表。

### 4.2 新增仓储方法（workspace_query.go，仿 DashboardQueryRepository 形态）

| 方法 | 数据源 | 用途 |
|------|--------|------|
| ListLatestBatch | assessment_batches | triggered_at 最新行（§1.4 回退与空态判定） |
| ListByPeriodBounds | assessment_batches | period 双界匹配全部批次行（status 定位与批次 ID 集） |
| ExistsAlertByBatchIDs | assessment_alerts | batch_id IN 存在性（alert_count 0/1） |
| CountExpiredTasks | assessment_test_tasks | status=expired 计数（overdue_count） |
| ListAllActivity | activity_stats | 全表行（每人最近非未使用行聚合，列级投影 token_name/period_end_at/active_level） |
| ListAllLatestScored | assessment_test_results 经 tasks JOIN | 全员最新 scored 判型行集合（九型统计输入，形态复用 ListLatestScoredByStaffNames 免名单参数） |

既有方法复用：ListPeriods / ListDimScoresByPeriods / ListDimScoresByPeriod / ListModuleAggScoresByPeriods / FindLatestFinishedAt（DashboardQueryRepository）、ListLatestScoredByStaffNames（AssessmentTestResultRepository）、FindLatest（TeamTrainingSuggestionRepository）、ListAll（DimensionRepository）、Get（AssessmentConfigRepository）。索引匹配核对见 04 文档 §3。

九型取数裁定：速览区不展示覆盖率（§1.7 无分母消费位），判型行自带 staff_name 无需名单映射，新增 ListAllLatestScored 一次取全员最新 scored 行（与 F9/F10 同过滤口径），名单失败时九型照常返回（§1.3）。

### 4.3 计算常量

| 常量 | 初值 | 说明 |
|------|------|------|
| trendWindowSize | 8 | 演进观察窗口期数（specs §4.1.2 B 近 8 期，与 F10 同值同源） |
| avgMinPeople | 3 | 维度均分最小有数据人数（< 3 置空，与 F10/F9 同值） |
| weaknessMaxCount / weaknessHardCap | 2 / 3 | 共性短板每模块最低 2 维、并列至多 3 项（同 F10） |
| lowScoreLine | 60 | 低分占比分数线（同 F10，与 F9 待提升等级线一致） |
| attentionLimitPerCategory | 5 | 关注人群每类配额（specs §4.1.4 规则3 各至多 5、不跨类补足） |
| weakScoreLine | 60 | 短板人群模块总分判定线（取整后 < 60，F9 待提升等级线，specs §4.1.4 规则3） |

与 F10 dashboard.go 常量同值部分优先同源引用（常量收敛单点），跨包重复定义需注释互指。随源码发版，非在线配置。

### 4.4 跳转契约（纯前端，无接口）

specs §3.2 全部跳转经前端路由 search 参数承载，本域零接口变更：

| 跳转来源 | 目标路由 | search 参数 |
|---------|---------|------------|
| 跑批态势卡 | F6 评测运营中心（AI 使用能力 tab） | 无参数（F6 默认 tab 即 AI 使用能力，无需 URL 预填） |
| 告警统计卡 | F6 评测运营中心 | 无参数 |
| 逾期统计卡 | F7 评测运营中心测试任务 tab | `status=expired`（F7 任务列表 URL 预填，specs §7.2 声明的随本 feature 前端改造项，接口已有 status 筛选参数无变更） |
| 未使用人数卡 | F9 人员画像列表页 | `unused_only=true`（F9 预填能力已由 F10 feature 交付） |
| 共性短板标签 | F9 人员画像列表页 | `dimension_code=<短板维度 code>`（同上） |
| 查看完整团队看板 | F10 团队看板页 | 无参数 |
| 关注表「查看画像」 | F9 个人画像详情页 | `staff_name=<人员标识>` |
| 关注表「查看全员画像」 | F9 人员画像列表页 | 无参数 |

### 4.5 前端路由与页面

工作台首页为 `_authenticated` 根路由 `/`（登录后默认落地，specs §3.1），前端 feature 命名 `workspace`（路由、i18n 命名空间、TanStack Query key 同名）。本节仅登记路由归属，页面结构与组件设计归开发计划。

---

## 5. 错误码

本功能零新增错误码（§1.10）。工作台域 22xx 段位登记预留，后续 feature 使用。

---

## 6. 与相邻域的边界

- **对 P2_ASM_001（F6 批次/告警/配置）**：只读消费 assessment_batches（status 定位、data_updated_at、告警归属）、assessment_alerts（存在性计数）、assessment_configs + pipeline.NextTriggerAt（下次跑批推算，纯函数复用不调 F6 service）；不触发评估、不写批次。
- **对 P2_TST_001（F7 主动测试）**：只读消费 assessment_test_tasks（status=expired 计数）；逾期筛选承接经 F7 前端 URL 预填（§4.4），任务运营接口不触碰。
- **对 P2_PRF_001（F9 个人画像）**：跳转经前端路由承接（§4.4）；共用个人短板集合口径（F9 规则 4 与 §1.5 同源）与待评估展示语义。
- **对 P2_TMD_001（F10 团队看板）**：维度均分、共性短板、九型、建议行取数复用同源口径与既有仓储方法（specs §4.1.4 规则5，§1.7 精简投影）；聚合纯函数与计算常量同源引用，不新建第二套口径；不调 DashboardService 接口（工作台独立 service 编排）。
- **对 P2_SYS_001（配置/集成）**：全员名单经 userapi 客户端 + 集成密钥解析（失败区块降级，与 F10 整体 1305 的处置不同，§1.3）。
- **对 P1_ACC_001（登录鉴权）**：挂 auth 组 JWT 鉴权，登录落地默认路由归前端 `_authenticated` 布局。

---

## 7. SSOT 合规与一致性

- [x] 页面功能全覆盖：4.1.2 四区块字段 → W1 响应四对象一一对应；4.1.3 八个跳转入口 + 页面加载自动查询 → §4.4 跳转契约 + W1 单请求；4.1.4 规则1-5 → 固定本期（无区间参数）/ 告警 0/1（batch.alert_count）/ 关注人群（§1.5）/ 空态（区块 null 语义）/ 研判同源（profile.suggestion）；5.1 聚合接口 → W1（§4.1）。
- [x] 字段定义与 specs 一致：4.1.2 A/B/C/D 五表字段在响应结构一一对应；九处适配已声明（方法归一化 §1.2、降级矩阵 §1.3、批次态势定位 §1.4、关注人群口径 §1.5、pp 环比 §1.6、精简投影 §1.7、综合分口径 §1.8、时间格式 §1.9、零错误码 §1.10）。
- [x] 业务规则在接口层落地：固定本期（三表并集首项，无参数无校验）、告警存在性（区间批次集 IN 存在性）、两类人群判定与排序（取整口径 + 配额 + 互斥）、独立空态（区块 null）、研判同源（FindLatest 同 F10）。
- [x] 状态定义与 specs 第 6 章一致：工作台无自有状态机（specs 第 6 章明文），批次/任务/告警状态由 F6/F7 定义，本域只读展示（batch.status 四值枚举透传）。
- [x] 权限规则一致：接口挂 JWT，无角色差异（specs §2.2 纯只读消费页），员工侧无 HTTP 面。
- [x] 技术层偏差已声明：§1.2-1.10 九条。
- [x] 接口与数据模型一致性：W1 响应字段均为既有表列投影或查询期派生，零新表零变更（[04_model_interface.md](04_model_interface.md) 消费表清单与索引核对）。

---

## 8. 不涉及的设计

- 工作台聚合结果的物化落库与服务端缓存（表、Redis）：specs §5.1.4 规则1 聚合实时计算不落库，与 F10 §5.2.4 规则3 同源。
- 区间切换参数与历史趋势消费：specs §4.1.4 规则1 固定本期，历史趋势经 F10 区间切换与逐期趋势页承载。
- 工作台配置提示（向导未完成配置项就近提醒）：specs §1.3 已决策取消，运行配置就绪由系统状态页承载。
- 页面轮询通道：specs §4.1.3 前端自动流程明文不做轮询，进行中批次跟进经跑批态势卡跳 F6（该页已有轮询先例）。
- 员工侧工作台入口：员工非平台用户（specs §2.1），无此面。
- 关注人群的跨类补足与超 5 人扩展：specs §4.1.4 规则3 明文各至多 5、不跨类补足。

---

**文档版本：** v1.0
**最后更新：** 2026-10-06
**作者：** lixuetao
