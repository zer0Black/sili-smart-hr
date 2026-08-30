# 维度与权重配置 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| 所属 Feature | P2_DIM_001_FEAT_维度与权重配置 |
| 文档版本 | v1.0 |
| 创建日期 | 2026-08-11 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书.md](01_功能需求规格说明书.md)、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md) |

---

## 一、基础信息

**模块代号：** DIM（能力模型维度域）

**接口协议：** HTTP，简化 GET/POST 模式（GET 仅查询，POST 仅变更，POST 以路径区分动作）

**Base URL：** `/api`（无版本前缀，遵循规则文件 §2.1）

**认证方式：** Bearer JWT，`Authorization: Bearer {token}`，全部接口挂 `_authenticated` 受保护路由组

**模块前缀说明：** 遵循项目规则文件 §1.9，表名沿用 GORM 默认实体复数化（`dimensions`、`dimension_settings`），不强制模块前缀。接口路径以业务实体 `dimensions` 为根，模块代号 DIM 仅用于错误码段（11xx）与文档分区，不进路径也不进表名。

---

## 二、核心设计原则

| 方法 | 用途 | 请求体 | 适用场景 |
|------|------|--------|---------|
| GET | 查询数据 | 无 | 维度树查询、单维度详情、活跃度规则查询 |
| POST | 变更数据 | 有 | 新增、编辑、启停、删除、活跃度规则保存 |

**雪花 ID 传输约定（规则文件 §1.2 强制）。** 凡承载雪花 ID 的字段（维度 ID）JSON 序列化一律带 `,string`，前端类型定义为 string，不做数值运算。下文响应示例中 ID 均以字符串字面量呈现。

**统一响应格式（规则文件 §2.2）：**

```json
{ "code": 0, "message": "ok", "data": { } }
```

`code = 0` 成功，非 0 为业务错误码。`data` 字段无 `omitempty`，成功无载荷时为 `null`。

**配置生效时机（specs 4.1.4 规则1）。** 接口仅负责持久化配置变更，不触发即时重算。维度属性、权重、评分提示词、评分锚点、活跃度阈值的任何保存，均由评估引擎在下次跑批时读取生效，当前评估区间已产出的历史画像保留快照不重算。

---

## 三、接口列表

需求追溯编号 F-001 ~ F-008 对应 [01_功能需求规格说明书.md](01_功能需求规格说明书.md) 第 4 章页面功能点。

| 序号 | 方法 | 路径 | 用途 | 需求追溯 |
|------|------|------|------|---------|
| 1 | GET | /api/dimensions/tree | 维度树全量查询 | F-001 维度树浏览与搜索 |
| 2 | GET | /api/dimensions/{id} | 单维度详情 | F-002 叶子维度配置加载 |
| 3 | POST | /api/dimensions/create | 新增维度 | F-003 新增维度（弹窗） |
| 4 | POST | /api/dimensions/update | 编辑维度（含启停） | F-004 保存维度配置、F-006 维度启停 |
| 5 | POST | /api/dimensions/delete | 删除维度（软删除） | F-005 删除维度 |
| 6 | GET | /api/dimensions/activity-rule | 活跃度规则查询 | F-007 活跃度规则读取 |
| 7 | POST | /api/dimensions/activity-rule/save | 活跃度规则保存 | F-008 活跃度规则保存 |

**模块汇总视图（specs 4.1.2 D）无独立接口。** 汇总数据由前端聚合维度树响应（按 `module_code` 分组、对 `include_overview=true && enabled=true` 的维度权重求和），后端不单独提供汇总接口。

**维度树搜索（specs 4.1.5）。** 树响应为四模块全量结构，前端按维度名称本地模糊过滤，后端不提供关键词搜索参数。

---

### 3.1 维度树全量查询

**接口路径：** `GET /api/dimensions/tree`

**需求追溯：** F-001 维度树浏览与搜索

**查询参数：** 无（全量返回四模块维度树完整结构）

**请求示例：**

```
GET /api/dimensions/tree
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "modules": [
      {
        "module_code": "ACTIVITY",
        "name": "使用活跃度",
        "data_source": "RULE",
        "is_reference": false,
        "groups": null,
        "dimensions": [
          {
            "id": "1780000000000000001",
            "code": "ACT_USAGE_FREQUENCY",
            "name": "会话频率",
            "module_code": "ACTIVITY",
            "group_code": null,
            "data_source": "RULE",
            "weight": 0,
            "include_overview": false,
            "enabled": true
          }
        ]
      },
      {
        "module_code": "AI_USAGE",
        "name": "AI 使用能力",
        "data_source": "CONVERSATION",
        "is_reference": false,
        "groups": [
          {
            "group_code": "BASE",
            "name": "底层能力",
            "dimensions": [
              {
                "id": "1780000000000000010",
                "code": "AI_REQUIREMENT_CLARITY",
                "name": "需求澄清能力",
                "module_code": "AI_USAGE",
                "group_code": "BASE",
                "data_source": "CONVERSATION",
                "weight": 25,
                "include_overview": true,
                "enabled": true
              }
            ]
          },
          {
            "group_code": "UPPER",
            "name": "上层能力",
            "dimensions": []
          }
        ],
        "dimensions": null
      },
      {
        "module_code": "AI_MGMT",
        "name": "AI 管理能力",
        "data_source": "TEST",
        "is_reference": false,
        "groups": null,
        "dimensions": []
      },
      {
        "module_code": "ENNEAGRAM",
        "name": "九型人格",
        "data_source": "TEST",
        "is_reference": true,
        "groups": null,
        "dimensions": []
      }
    ]
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| modules | array | 四模块固定顺序：使用活跃度、AI 使用能力、AI 管理能力、九型人格（specs 4.1.5） |
| modules[].module_code | string | 模块枚举：ACTIVITY / AI_USAGE / AI_MGMT / ENNEAGRAM |
| modules[].name | string | 模块显示名（前端硬编码，响应回传便于直接渲染） |
| modules[].data_source | string | 模块联动数据来源：RULE / CONVERSATION / TEST |
| modules[].is_reference | boolean | 是否参考性模块（仅 ENNEAGRAM 为 true，specs 术语表） |
| modules[].groups | array \| null | 模块下分组，仅 AI_USAGE 非 null；其余模块为 null |
| modules[].dimensions | array \| null | 模块直属维度，AI_USAGE 为 null（维度挂 groups 下），其余模块为非 null 数组 |
| groups[].group_code | string | 分组枚举：BASE（底层能力）/ UPPER（上层能力） |
| dimensions[].id | string | 维度 ID（雪花，string 化） |
| dimensions[].code | string | 维度编码（系统生成，全大写下划线） |
| dimensions[].weight | integer | 聚合权重百分比 0-100 |
| dimensions[].include_overview | boolean | 是否参与总览分 |
| dimensions[].enabled | boolean | 是否启用 |

**业务规则：**

- 维度按 `code` 升序排列（specs 4.1.5：模块内叶子维度按维度编码升序）。
- 已软删除的维度（`deleted_at` 非空）不出现在树中。
- 响应仅含树渲染与模块汇总所需的轻量字段，长文本字段（评分提示词、评分锚点、维度说明）由详情接口提供。

---

### 3.2 单维度详情

**接口路径：** `GET /api/dimensions/{id}`

**需求追溯：** F-002 叶子维度配置加载

**路径参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| id | string | 维度 ID（雪花，string 化） |

**请求示例：**

```
GET /api/dimensions/1780000000000000010
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000010",
    "code": "AI_REQUIREMENT_CLARITY",
    "name": "需求澄清能力",
    "module_code": "AI_USAGE",
    "group_code": "BASE",
    "data_source": "CONVERSATION",
    "prompt": "你是一位能力评估专家，请根据以下会话特征评估该用户的需求澄清能力...",
    "anchor": "高（90-100）：能主动澄清模糊需求...\n中（60-89）：在多数场景下能识别...\n低（0-59）：需求理解存在明显偏差...",
    "weight": 25,
    "include_overview": true,
    "enabled": true,
    "is_reference": false,
    "description": "衡量用户在与 AI 协作时澄清模糊需求、对齐目标的能力",
    "version": 3,
    "created_at": "2026-08-11T10:00:00Z",
    "updated_at": "2026-08-11T11:30:00Z"
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 维度 ID（雪花，string 化） |
| code | string | 维度编码（编辑态只读） |
| name | string | 维度名称 |
| module_code | string | 所属模块（编辑态只读，specs 4.1.2 B） |
| group_code | string \| null | 所属分组，仅 AI_USAGE 非 null |
| data_source | string | 数据来源（编辑态只读，specs 规则8） |
| prompt | string \| null | 评分提示词，仅 `data_source=CONVERSATION` 非空（specs 规则6） |
| anchor | string | 评分锚点（必填，绝对分标准） |
| weight | integer | 聚合权重 0-100 |
| include_overview | boolean | 是否参与总览分 |
| enabled | boolean | 是否启用 |
| is_reference | boolean | 是否参考性维度（由 module_code 派生，仅 ENNEAGRAM 为 true） |
| description | string \| null | 维度说明 |
| version | integer | 乐观锁版本号，提交编辑/删除时原样回传 |
| created_at | string | 创建时间（ISO 8601） |
| updated_at | string | 更新时间（ISO 8601） |

**业务规则：**

- 目标维度不存在或已软删除返回 1201 DimensionNotFound。
- 权重控件是否禁用由前端按 `module_code`/`is_reference` 判断（specs 规则5），后端仅回传原始字段。

---

### 3.3 新增维度

**接口路径：** `POST /api/dimensions/create`

**需求追溯：** F-003 新增维度（4.2 新增维度弹窗）

**请求参数：**

```json
{
  "name": "需求澄清能力",
  "module_code": "AI_USAGE",
  "group_code": "BASE",
  "data_source": "CONVERSATION",
  "prompt": "你是一位能力评估专家...",
  "anchor": "高（90-100）：...",
  "weight": 5,
  "include_overview": true,
  "description": "衡量用户澄清模糊需求的能力"
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| name | string | 是 | 2~30 字符 | 维度名称，编码据此生成（specs 4.2.2） |
| module_code | string | 是 | 四选一枚举 | 所属模块，提交后不可改 |
| group_code | string | 否 | 仅 AI_USAGE 接受 BASE/UPPER，其余模块须为 null | 所属分组 |
| data_source | string | 是 | 三选一枚举，须与 module_code 联动一致 | 数据来源，提交后只读 |
| prompt | string | 条件必填 | `data_source=CONVERSATION` 时必填，≤2000 字符 | 评分提示词（specs 规则6） |
| anchor | string | 是 | ≤500 字符 | 评分锚点（specs 规则7） |
| weight | integer | 否 | 0~100 整数，默认按模块联动 | 聚合权重 |
| include_overview | boolean | 否 | 默认按模块联动 | 是否参与总览分 |
| description | string | 否 | ≤300 字符 | 维度说明 |

**模块联动默认值（specs 4.2.4 规则1）：**

| module_code | data_source | weight | include_overview |
|-------------|-------------|--------|------------------|
| ACTIVITY | RULE | 0 | false |
| ENNEAGRAM | TEST | 0 | false |
| AI_MGMT | TEST | 5 | true |
| AI_USAGE | CONVERSATION | 5 | true |

请求未显式传 weight/include_overview 时按上表置默认；显式传值则校验须与模块约束一致（ACTIVITY/ENNEAGRAM 强制 weight=0 且 include_overview=false，specs 规则5）。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000020",
    "code": "AI_REQUIREMENT_CLARITY",
    "name": "需求澄清能力",
    "module_code": "AI_USAGE",
    "group_code": "BASE",
    "data_source": "CONVERSATION",
    "weight": 5,
    "include_overview": true,
    "enabled": true,
    "version": 1
  }
}
```

**业务规则：**

- 维度编码由服务端按 specs 规则4 生成：模块前缀（AI/MGT/ACT/ENN）加维度名称拼音，全大写下划线连接，≤40 字符，同名维度拼音重复时追加序号去重，不可手填。
- 编码生成冲突（specs 4.2.4 规则2）返回 1202 DimensionCodeExists，提示稍后重试，服务端不自动改写，由用户重新提交触发再次生成。
- 新增维度 `enabled` 强制为 true（specs 6.1 新维度进入启用态）、`version` 初始为 1。
- 新增成功后前端按所属模块与分组归位并自动选中新维度（specs 4.2.3）。

---

### 3.4 编辑维度（含启停）

**接口路径：** `POST /api/dimensions/update`

**需求追溯：** F-004 保存维度配置、F-006 维度启停

**请求参数：**

```json
{
  "id": "1780000000000000010",
  "name": "需求澄清能力",
  "prompt": "你是一位能力评估专家...",
  "anchor": "高（90-100）：...",
  "weight": 25,
  "include_overview": true,
  "enabled": true,
  "description": "衡量用户澄清模糊需求的能力",
  "version": 3
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| id | string | 是 | 雪花 ID | 目标维度 ID |
| name | string | 是 | 2~30 字符 | 维度名称 |
| prompt | string | 条件必填 | `data_source=CONVERSATION` 时必填，≤2000 字符 | 评分提示词 |
| anchor | string | 是 | ≤500 字符 | 评分锚点 |
| weight | integer | 是 | 0~100 整数 | 聚合权重（受 specs 规则5 限制，前端禁用，后端兜底校验） |
| include_overview | boolean | 是 | - | 是否参与总览分 |
| enabled | boolean | 是 | - | 是否启用，承载启停切换 |
| description | string | 否 | ≤300 字符 | 维度说明 |
| version | integer | 是 | 正整数 | 乐观锁版本号，取自详情响应 |

**不可变字段：** `code`、`module_code`、`group_code`、`data_source` 提交后只读（specs 4.1.2 B、规则8），编辑请求不接收这些字段，服务端忽略任何尝试修改。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000010",
    "code": "AI_REQUIREMENT_CLARITY",
    "name": "需求澄清能力",
    "module_code": "AI_USAGE",
    "group_code": "BASE",
    "data_source": "CONVERSATION",
    "weight": 25,
    "include_overview": true,
    "enabled": true,
    "version": 4,
    "updated_at": "2026-08-11T12:00:00Z"
  }
}
```

**业务规则：**

- 乐观锁：服务端校验请求 `version` 与当前库内 `version` 一致方可更新，更新后 `version` 自增 1。不一致返回 1204 DimensionVersionConflict（specs 规则9 并发冲突）。
- 目标维度不存在或已软删除返回 1201 DimensionNotFound，亦经 version 冲突路径提示刷新。
- 启停切换复用本接口（仅 enabled 字段变更），不单独设接口。specs 2.3 的"维度启停"归此处理。
- 权重兜底校验：ACTIVITY/ENNEAGRAM 模块的更新请求若 weight≠0 或 include_overview=true，返回 1400 BadRequest（specs 规则5，前端已禁用控件，后端兜底防绕过）。

---

### 3.5 删除维度

**接口路径：** `POST /api/dimensions/delete`

**需求追溯：** F-005 删除维度

**请求参数：**

```json
{
  "id": "1780000000000000010",
  "version": 4
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 目标维度 ID |
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

- 删除前置（specs 规则3）：仅停用态（`enabled=false`）维度可删除。启用态调用返回 1203 DimensionEnabledNotDeletable。
- 软删除：置 `deleted_at`，维度从配置树移除；历史画像快照保留不重算，仅后续评估不再产出该维度分。
- 已配评分提示词一并随维度软删除（同条记录，无需额外清理）。
- 乐观锁校验同编辑接口，冲突返回 1204。
- 删除成功后前端切换选中到首个可用叶子维度（specs 4.1.5）。

---

### 3.6 活跃度规则查询

**接口路径：** `GET /api/dimensions/activity-rule`

**需求追溯：** F-007 活跃度规则读取

**请求参数：** 无

**请求示例：**

```
GET /api/dimensions/activity-rule
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "active_threshold": 10,
    "low_frequency_threshold": 5,
    "updated_at": "2026-08-11T11:30:00Z"
  }
}
```

**字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| active_threshold | integer | 活跃判定下限，1~999，本评估区间有效对话达此值判为活跃 |
| low_frequency_threshold | integer | 低频判定下限，1~999，达此值且低于活跃下限判为低频，低于此值判为未使用 |
| updated_at | string | 上次保存时间（ISO 8601） |

**业务规则：**

- 统计区间为只读标签，跟随系统配置页评估区间（specs 4.1.2 C），本接口不下发区间值，由 config 域 Feature 建立后另接口提供。
- 判定关系：`≥active_threshold` 判活跃、`[low_frequency_threshold, active_threshold-1]` 判低频、`<low_frequency_threshold` 判未使用（specs 4.1.5）。

---

### 3.7 活跃度规则保存

**接口路径：** `POST /api/dimensions/activity-rule/save`

**需求追溯：** F-008 活跃度规则保存

**请求参数：**

```json
{
  "active_threshold": 15,
  "low_frequency_threshold": 8
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| active_threshold | integer | 是 | 1~999 整数 | 活跃判定下限 |
| low_frequency_threshold | integer | 是 | 1~999 整数，须 < active_threshold | 低频判定下限 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "active_threshold": 15,
    "low_frequency_threshold": 8,
    "updated_at": "2026-08-11T12:10:00Z"
  }
}
```

**业务规则：**

- `low_frequency_threshold ≥ active_threshold` 返回 1208 ActivityThresholdInvalid（specs 4.1.2 C 校验）。
- 阈值在下次评估跑批时生效（specs 规则1），当前区间已产出的活跃度分级不重算。
- 配置为系统级单例，保存即 update 单行记录，无需乐观锁（运营体量小，最后写入覆盖可接受）。

---

## 四、错误码规范

错误码集中定义于 [hr-backend/internal/pkg/errcode/errcode.go](../../../hr-backend/internal/pkg/errcode/errcode.go)，规则文件 §2.4 规定千位 1 标识业务错误。account 域占用 10xx 段，系统初始化域占用 1101/1102，dimension 域使用 12xx 段（百位区分域：0=account，1=system，2=dimension）。

### 4.1 dimension 域错误码

| 错误码 | 常量 | 含义 | 触发场景 |
|--------|------|------|---------|
| 1201 | DimensionNotFound | 维度不存在 | 详情/编辑/删除目标维度不存在或已软删除 |
| 1202 | DimensionCodeExists | 维度编码已存在 | 新增时编码生成冲突（specs 4.2.4 规则2） |
| 1203 | DimensionEnabledNotDeletable | 启用态维度不可删除 | 删除接口收到启用态维度（specs 规则3） |
| 1204 | DimensionVersionConflict | 维度配置已变更 | 乐观锁版本不一致，并发冲突（specs 规则9） |
| 1205 | DimensionNameInvalid | 维度名称校验失败 | 名称长度不符 2~30 字符 |
| 1206 | DimensionAnchorRequired | 评分锚点必填缺失 | anchor 为空（specs 规则7） |
| 1207 | DimensionPromptRequired | 评分提示词必填缺失 | CONVERSATION 维度 prompt 为空（specs 规则6） |
| 1208 | ActivityThresholdInvalid | 活跃度阈值校验失败 | 阈值越界或低频 ≥ 活跃下限 |

### 4.2 通用错误码（复用）

| 错误码 | 常量 | 含义 |
|--------|------|------|
| 0 | Success | 成功 |
| 1400 | BadRequest | 请求参数格式错误（含模块联动约束被绕过、字段超长） |
| 1500 | Internal | 服务内部错误 |

### 4.3 错误响应格式

遵循规则文件 §2.2 统一响应结构，HTTP 状态码映射遵循 §2.4：

```json
{
  "code": 1204,
  "message": "该维度配置已变更，请刷新后重试",
  "data": null
}
```

**HTTP 状态码映射：**

- 所有成功：HTTP 200，code=0。
- `code = 1003`（Unauthorized，token 失效）：HTTP 401，前端触发登出。
- 其余业务错误（含 11xx、1400）：HTTP 200 带 code，前端据 code 渲染字段内联报错或 toast。
- 非 `*service.Error` 异常：HTTP 500 带 1500。

### 4.4 异常反馈策略（specs 4.1.4 规则9）

| 异常类型 | 错误码 | 前端反馈 |
|---------|--------|---------|
| 表单校验失败 | 1205/1206/1207/1208/1400 | 字段内联报错，保留已填输入，不关闭弹窗、不切换面板 |
| 并发冲突 | 1204 | toast「该维度配置已变更，请刷新后重试」，中止本次提交 |
| 编码生成冲突 | 1202 | toast「操作失败，请稍后重试」，允许重新提交再次生成 |
| 网络或服务异常 | 1500 / 网络错误 | toast「操作失败，请稍后重试」，允许重试 |

---

## 五、安全说明

### 5.1 认证与授权

- 全部接口挂受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))`，JWT 中间件从 `Authorization: Bearer` 取 token 校验签名与过期，注入 `account_id`，失败返 401 code 1003（规则文件 §2.6）。
- 系统已全量移除角色概念（specs 2.1），所有已登录平台账号共享同一份系统级维度配置，无行级数据隔离，接口层不做额外授权判断。

### 5.2 输入验证

- 类型与长度校验：handler 层对 name（2~30）、code（≤40）、prompt（≤2000）、anchor（≤500）、description（≤300）、weight（0~100）、阈值（1~999）逐项校验，超长或越界返 1400。
- 模块联动约束校验：服务端兜底校验 module_code 与 data_source/weight/include_overview 的联动一致性，防止前端绕过（specs 规则5、规则8、4.2.4 规则1）。
- SQL 注入防护：全程 GORM 链式 API 加占位符绑定，维度树与详情不接收用户输入做 LIKE 匹配，无通配符转义需求。

### 5.3 传输安全

- 本功能无密码、无敏感个人数据，RSA 加密通道不适用（规则文件 §2.5 针对密码入口）。
- 生产强制 HTTPS 由 Nginx 反代承载（架构文档 4.4）。

### 5.4 限流

- 运营体量小（平台账号规模有限），本功能接口不设接口级或用户级限流。JWT 鉴权已拦截未认证访问。

---

## 六、SSOT 合规说明

| specs 需求点 | 接口落地 | 状态 |
|-------------|---------|------|
| 维度树浏览与搜索（4.1.2 A、4.1.5） | GET /api/dimensions/tree 全量返回，前端本地搜索 | 覆盖 |
| 叶子维度配置加载（4.1.2 B） | GET /api/dimensions/{id} 全字段 | 覆盖 |
| 新增维度（4.2） | POST /api/dimensions/create | 覆盖 |
| 保存维度配置（4.1.3） | POST /api/dimensions/update | 覆盖 |
| 维度启停（specs 2.3） | 复用 update 的 enabled 字段 | 覆盖 |
| 删除维度（4.1.4 规则3） | POST /api/dimensions/delete，停用前置 | 覆盖 |
| 活跃度规则读写（4.1.2 C） | GET / activity-rule + POST / activity-rule/save | 覆盖 |
| 模块汇总视图（4.1.2 D） | 前端聚合树响应，无独立接口 | 覆盖 |
| 并发冲突反馈（规则9） | version 乐观锁 + 1204 | 覆盖 |
| 编码自动生成（规则4） | 服务端生成，前端不可填 | 覆盖 |

** specs 与规则文件冲突处理：** specs 2.3 接口鉴权矩阵写 PUT/DELETE 方法，项目规则文件 §2.1 规定 GET 仅查询、POST 仅变更。按 SSOT 规则技术实现层面 architecture > specs，统一收敛到 GET/POST，POST 以路径区分动作。

---

## 七、变更记录

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|---------|------|
| v1.0 | 2026-08-11 | 初始版本，7 个接口，错误码 11xx 段 | lixuetao |
