# 系统参数与大模型配置 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_SYS_001_FEAT_系统参数与大模型配置 |
| 模块代号 | CFG（系统配置域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-08-12 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md) |

---

## 1. 设计依据与冲突说明

### 1.1 SSOT 与优先级

本设计以功能需求规格说明书（specs）为业务真实来源，以 [AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md) 为技术实现权威。冲突处理遵循技能约定：业务需求层面 specs 优先，技术实现层面规则文件优先。

### 1.2 HTTP 方法归一化（对 specs 的技术实现修正）

specs §2.3 接口矩阵使用了 PUT、DELETE、POST 三种变更方法。规则文件 §2.1 明确规定方法约定为 GET 仅查询、POST 仅变更，POST 以路径区分动作（/create、/update、/delete 等）。本设计按规则文件归一化所有变更接口为 POST + 动作路径，与已落地的 `/api/login`、`/api/setup/initialize`、`/api/system/health-check` 风格一致。接口语义与 specs 完全对齐，仅方法表达方式调整。

### 1.3 表名与路径前缀

规则文件 §1.9 沿用 GORM 默认 NamingPolicy 实体复数化，不强制模块前缀。表名用语义化复数（`assessment_configs`、`llm_configs`、`integration_secrets`），接口路径用语义化资源名（`/api/assessment-config`、`/api/llm-configs`、`/api/integration-secret`），均不挂 `cfg_` 前缀。

### 1.4 术语区分（防同名歧义）

本功能存在两个同名概念，须严格区分，下游开发与前端契约须各自独立表达：

- **模型主键 ID**：应用层生成的雪花 ID（int64），数据库主键，承载 JS 精度风险，json 序列化带 `,string`，前端类型用 string。
- **模型 ID**：用户填写的大模型调用参数（文本，如 `deepseek-chat`），传入服务商 `model` 参数，不涉及精度，直接 string。

---

## 2. 通用约定

### 2.1 基础配置

| 配置项 | 值 | 来源 |
|-------|------|------|
| Base URL 前缀 | `/api` | 规则文件 §2.1 |
| 版本控制 | 不分段，无 /v1 | 规则文件 §2.1 |
| 方法约定 | GET 仅查询，POST 仅变更（动作路径区分） | 规则文件 §2.1 |
| 认证方式 | Bearer JWT（`Authorization: Bearer {token}`） | 规则文件 §2.1 |
| 路由归属 | 受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))` | 规则文件 §2.6 |

本功能全部接口挂 JWT 鉴权，无公开接口。未携带或无效 token 由 JWT 中间件统一返回 HTTP 401 + code 1003。

### 2.2 统一响应格式

成功响应遵循规则文件 §2.2：

```json
{
  "code": 0,
  "message": "ok",
  "data": { }
}
```

`data` 字段无 omitempty，成功无载荷时为 `null`。分页结构作为 `data` 透传，四字段 `list`/`total`/`page`/`page_size`。

### 2.3 错误码段位

沿用规则文件 §2.4 与 errcode.go 的千位段位约定：account 10xx、系统初始化 11xx、dimension 12xx、通用 1400/1500。本功能属系统配置域，分配 **13xx 段**（百位 1 标识 system 大类，config 子域用 3 避让 1101-1102 与 1201-1208）。本功能新增错误码见 §4。

Handler 错误映射沿用 `handleServiceError`：成功 HTTP 200；鉴权类 code 1003 返 HTTP 401；其余业务错误返 HTTP 200 带 code；非 `*service.Error` 返 HTTP 500 带 1500。前端拦截器看 body code 而非 HTTP status。

### 2.4 雪花 ID 传输约束

承载雪花 ID 的字段 json 序列化一律带 `,string`，前端类型用 string：

- 模型主键 `id`：domain 模型与 service DTO 双层 `json:"id,string"`（样板见 account 链路）。
- 评估配置关联外键 `assessment_config_id`：值来自雪花主键，`json:"assessment_config_id,string"`。
- 人员标识 `staff_id`：来自 sili-smart-api 用户体系，若为数值 ID 同样超 2^53，统一按 string 承载，前端类型 string。

分页 `total` 与模型调用参数 `model_id`（文本）不在此列。

### 2.5 密钥传输与存储安全

specs §7 把 API Key 与集成密钥定性为高敏感凭据，要求双层保障。

**传输层（前端提交）**：密钥类字段（新增/编辑模型的 `api_key`、更新集成密钥的 `secret`）一律前端 RSA-OAEP 加密为密文传输，明文不落请求体，复用 `GET /api/auth/public-key` 公钥与 keyId 机制。后端按 keyId 从 Redis 取私钥解密，解密成功即删，一次性。

明文长度约束：specs 限定 API Key 与集成密钥均 ≤200 字符。RSA-OAEP-SHA256 在 2048 位密钥下明文上限约 190 字节，无法单块覆盖 200 字符。实现侧二选一：RSA 密钥位长提升至 ≥3072（单块上限约 318 字节），或采用 AES-256-GCM 加密明文再用 RSA-OAEP 加密 AES 密钥的混合方案（JWE 风格）。具体由开发计划阶段定，本文档仅约束传输层不得明文。

**存储层（数据库落库）**：API Key 与集成密钥一律 AES-256-GCM 对称加密存储，密文与 nonce 一并写入 TEXT 列，明文禁止落库。对称密钥由环境变量 `LLM_SECRET_KEY` 提供，生产强制覆盖（与 `JWT_SECRET` 同级的环境变量约定）。

**展示层（掩码）**：列表与卡片仅返回掩码快照，掩码规则保留前缀前 4 位与末 4 位、中段以星号替代，短于 8 位的密钥全掩码。明文仅在详情查询（查看弹窗、临时查看）按需解密返回，详情接口以外不暴露明文。为避免列表查询把全量明文解密进内存（最小暴露），掩码值在密钥写入时同步生成快照存独立列，列表读取快照不解密明文。

---

## 3. 接口列表

按业务域分三组：评估周期配置（A）、大模型配置（B）、集成密钥（C）。共 13 个接口。

需求追溯编号对应 specs 第 4 章页面功能：F-SYS-xxx 对应系统参数页，F-LLM-xxx 对应大模型配置页。

### A. 评估周期配置

#### A1. 查询评估周期配置

**接口路径：** `GET /api/assessment-config`

**需求追溯：** [需求：F-SYS-4.1.5] 系统参数页加载回填

**说明：** 返回当前评估周期单例配置。首启 migrateDB 幂等插入默认行（周期 weekly、触发时点 23:00、评估对象全员），该接口恒有数据。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000001",
    "period": "weekly",
    "trigger_time": "23:00",
    "target_mode": "all",
    "specified_members": [],
    "version": 3
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 评估配置主键（雪花 ID，string 化） |
| period | string | 周期长度 [可选值：daily/weekly/monthly] |
| trigger_time | string | 触发时点，HH:mm 格式 |
| target_mode | string | 评估对象模式 [可选值：all/specified] |
| specified_members | array | 指定人员列表，target_mode=all 时为空数组；每项含 staff_id（string）、staff_name（string） |
| version | integer | 乐观锁版本号，保存时回传 |

**错误码：** 通用 1500。

---

#### A2. 保存评估周期配置

**接口路径：** `POST /api/assessment-config/save`

**需求追溯：** [需求：F-SYS-4.1.3 保存配置] [需求：F-SYS-4.1.4 规则1/2/5]

**说明：** 保存周期长度、触发时点、评估对象，下次跑批生效。采用乐观锁：请求须带回当前 version，后端 `UPDATE WHERE id=? AND version=?`，affected=0 返回 1306 配置版本冲突。target_mode=all 时清空指定人员关联，target_mode=specified 时用 specified_members 全量覆盖关联表（事务内先删后插）。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| period | string | 是 | 周期长度 [可选值：daily/weekly/monthly] |
| trigger_time | string | 是 | 触发时点 HH:mm |
| target_mode | string | 是 | 评估对象模式 [可选值：all/specified] |
| specified_members | array | 条件必填 | target_mode=specified 时必填且非空；每项含 staff_id、staff_name |
| version | integer | 是 | 当前版本号，乐观锁 |

**请求示例：**

```json
{
  "period": "monthly",
  "trigger_time": "23:00",
  "target_mode": "specified",
  "specified_members": [
    { "staff_id": "usr_9001", "staff_name": "张三" },
    { "staff_id": "usr_9002", "staff_name": "李四" }
  ],
  "version": 3
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000001",
    "version": 4
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1306 | 配置版本冲突（并发更新，提示刷新后重试） |
| 1400 | 参数错误（period/trigger_time/target_mode 非法、specified_members 为空、trigger_time 格式不符 HH:mm） |
| 1500 | 服务内部错误 |

---

#### A3. 查询人员列表

**接口路径：** `GET /api/staffs`

**需求追溯：** [需求：F-SYS-4.1.4 规则4] [需求：F-SYS-4.1.5] 评估对象下拉数据源

**说明：** 评估对象指定人员下拉的数据源，代理查询 sili-smart-api 用户体系。本系统不持久化人员主数据，仅在选中后把快照写入 `assessment_config_members`。支持按人名关键词过滤与分页。系统无工号，人员选项不含员工编号。

当 sili-smart-api 用户体系不可达时返回 1305，前端据 code 降级为仅全员模式（toast 提示「人员数据暂不可用，已切换为仅全员模式」，自动选中全员并置灰指定人员）。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| keyword | string | 否 | 空 | 按人名模糊过滤 |
| page | integer | 否 | 1 | 页码 |
| page_size | integer | 否 | 20 | 每页数量 |

**请求示例：**

```
GET /api/staffs?keyword=张&page=1&page_size=20
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      { "staff_id": "usr_9001", "staff_name": "张三" },
      { "staff_id": "usr_9015", "staff_name": "张明" }
    ],
    "total": 2,
    "page": 1,
    "page_size": 20
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1305 | 人员列表暂不可用（外部用户体系不可达） |
| 1500 | 服务内部错误 |

---

### B. 大模型配置

#### B1. 查询大模型列表

**接口路径：** `GET /api/llm-configs`

**需求追溯：** [需求：F-LLM-4.2.2B] [需求：F-LLM-4.2.5] 模型列表浏览与搜索

**说明：** 返回全部模型，不分页（specs 4.2.5 模型列表不分页，数量通常个位数）。API Key 仅返回掩码快照，明文不回显。支持按模型名称或模型 ID 模糊搜索（LIKE 通配符转义，见规则文件 §1.11）。底部统计的模型总数与当前启用模型名由前端据列表数据计算。

**查询参数：**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| keyword | string | 否 | 按模型名称或模型 ID 模糊过滤 |

**请求示例：**

```
GET /api/llm-configs?keyword=deepseek
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "id": "1780000000000000101",
      "name": "主力模型",
      "provider": "deepseek",
      "model_id": "deepseek-chat",
      "api_url": "",
      "api_key_masked": "sk-1***************************ab12",
      "enabled": true,
      "created_at": "2026-08-12T10:00:00Z",
      "updated_at": "2026-08-12T10:00:00Z"
    }
  ]
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 模型主键（雪花 ID，string 化） |
| name | string | 模型显示名称 |
| provider | string | 服务商 [可选值：deepseek/openai/zhipu/anthropic] |
| model_id | string | 模型调用参数（文本，如 deepseek-chat），非雪花 ID |
| api_url | string | API 地址，空串表示走服务商默认地址 |
| api_key_masked | string | API Key 掩码快照，明文不回显 |
| enabled | boolean | 启用状态，全表唯一 true |
| created_at | string | 创建时间（ISO 8601） |
| updated_at | string | 更新时间（ISO 8601） |

**错误码：** 通用 1500。

---

#### B2. 新增模型

**接口路径：** `POST /api/llm-configs/create`

**需求追溯：** [需求：F-LLM-4.2.3 新增模型] [需求：F-LLM-4.2.4 规则1] [需求：F-LLM-4.3]

**说明：** 新建模型。首个模型（清单为空）提交后自动 enabled=true，其余新增默认 enabled=false。api_key 字段前端 RSA-OAEP 加密传输，后端解密后 AES-256-GCM 加密落库，同时生成掩码快照。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| name | string | 是 | 模型名称，≤50 字符 |
| provider | string | 是 | 服务商 [可选值：deepseek/openai/zhipu/anthropic] |
| model_id | string | 是 | 模型调用参数，≤100 字符 |
| api_url | string | 否 | API 地址，填写须 URL 格式，≤500 字符；留空走服务商默认 |
| api_key | string | 是 | API Key 密文（RSA-OAEP 加密），明文 ≤200 字符 |

**请求示例：**

```json
{
  "name": "主力模型",
  "provider": "deepseek",
  "model_id": "deepseek-chat",
  "api_url": "",
  "api_key": "RSA-OAEP-encrypted-ciphertext-base64"
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000102"
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数错误（name/provider/model_id 缺失或超长、provider 非枚举值、api_url 非 URL 格式、api_key 缺失、密文解密失败） |
| 1500 | 服务内部错误 |

---

#### B3. 编辑模型

**接口路径：** `POST /api/llm-configs/update`

**需求追溯：** [需求：F-LLM-4.2.3 编辑模型] [需求：F-LLM-4.3.4 规则2]

**说明：** 保存模型属性。api_key 留空（空串或 null）表示保持原 Key 不变，填入新值则覆盖并刷新掩码快照。填入新值时前端同样 RSA-OAEP 加密传输。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 模型主键（雪花 ID，string 化） |
| name | string | 是 | 模型名称，≤50 字符 |
| provider | string | 是 | 服务商 [可选值：deepseek/openai/zhipu/anthropic] |
| model_id | string | 是 | 模型调用参数，≤100 字符 |
| api_url | string | 否 | API 地址，规则同新增 |
| api_key | string | 否 | API Key 密文；留空不改原值，填新值则覆盖 |

**请求示例：**

```json
{
  "id": "1780000000000000101",
  "name": "主力模型-改",
  "provider": "deepseek",
  "model_id": "deepseek-chat",
  "api_url": "https://proxy.example.com/v1",
  "api_key": ""
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000101"
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1301 | 大模型不存在（id 无效） |
| 1400 | 参数错误（字段缺失或超长、provider 非枚举、api_url 非格式、密文解密失败） |
| 1500 | 服务内部错误 |

---

#### B4. 删除模型

**接口路径：** `POST /api/llm-configs/delete`

**需求追溯：** [需求：F-LLM-4.2.3 删除模型] [需求：F-LLM-4.2.4 规则3]

**说明：** 物理删除，删除后无法恢复。事务内处理：清单仅剩一个时拦截（1302）；删除启用态模型时，剩余模型按列表顺序取首个自动 enabled=true。前端做二次确认。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 模型主键（雪花 ID，string 化） |

**请求示例：**

```json
{
  "id": "1780000000000000101"
}
```

**响应示例（删除启用项并触发转启）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000101",
    "transferred_enabled_id": "1780000000000000102"
  }
}
```

`transferred_enabled_id` 为删除启用项后自动转启的剩余模型主键，未触发转启时为 null。前端据此前端提示转启结果。

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1301 | 大模型不存在（id 无效） |
| 1302 | 至少保留一个大模型（清单仅剩一个时拦截） |
| 1500 | 服务内部错误 |

---

#### B5. 启用模型（排他）

**接口路径：** `POST /api/llm-configs/enable`

**需求追溯：** [需求：F-LLM-4.2.3 切换启用状态] [需求：F-LLM-4.2.4 规则1/2]

**说明：** 排他启用目标模型。事务内将其余模型 enabled 置 false、目标置 true。接口语义仅启用，不提供 disable（specs 规则2：停用当前启用项的唯一路径是启用另一个模型，启用项不可直接关闭）。前端开关在当前启用项上禁用关闭方向。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 待启用模型主键（雪花 ID，string 化） |

**请求示例：**

```json
{
  "id": "1780000000000000102"
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000102",
    "enabled": true
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1301 | 大模型不存在（id 无效） |
| 1500 | 服务内部错误 |

---

#### B6. 查询模型详情

**接口路径：** `GET /api/llm-configs/{id}`

**需求追溯：** [需求：F-LLM-4.2.3 查看模型] [需求：F-LLM-4.4 查看模型弹窗]

**说明：** 查看弹窗加载，返回模型完整属性含 API Key 明文。明文由后端 AES-256-GCM 解密后下发，仅本接口与集成密钥详情接口提供明文。前端在弹窗关闭即丢弃，不缓存。

**路径参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| id | string | 模型主键（雪花 ID，string 化） |

**请求示例：**

```
GET /api/llm-configs/1780000000000000101
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000101",
    "name": "主力模型",
    "provider": "deepseek",
    "model_id": "deepseek-chat",
    "api_url": "",
    "api_key": "sk-1a2b3c4d5e6f...ab12",
    "enabled": true,
    "created_at": "2026-08-12T10:00:00Z",
    "updated_at": "2026-08-12T10:00:00Z"
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1301 | 大模型不存在（id 无效） |
| 1500 | 服务内部错误 |

---

### C. 集成密钥

#### C1. 查询集成密钥

**接口路径：** `GET /api/integration-secret`

**需求追溯：** [需求：F-LLM-4.2.2C] [需求：F-LLM-4.2.5] 集成密钥卡片加载

**说明：** 返回集成密钥掩码与配置状态。集成密钥为单例，首启 migrateDB 插入空密钥行。配置状态由密文是否为空推导。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000201",
    "secret_masked": "smart***-key****7890",
    "configured": true
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 集成密钥主键（雪花 ID，string 化） |
| secret_masked | string | 集成密钥掩码快照；未配置时为空串 |
| configured | boolean | 配置状态，true 已配置 / false 未配置 |

**错误码：** 通用 1500。

---

#### C2. 查询集成密钥详情

**接口路径：** `GET /api/integration-secret/detail`

**需求追溯：** [需求：F-LLM-4.2.3 集成密钥临时查看]

**说明：** 临时查看与查看场景返回集成密钥明文。明文由后端解密下发，前端临时可见，刷新恢复掩码。未配置时不允许调用（前端置灰），后端兜底返回 1303。

**请求参数：** 无。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000201",
    "secret": "smart-api-integration-key-7890"
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1303 | 集成密钥未配置（无法临时查看） |
| 1500 | 服务内部错误 |

---

#### C3. 更新集成密钥

**接口路径：** `POST /api/integration-secret/update`

**需求追溯：** [需求：F-LLM-4.2.3 更新集成密钥] [需求：F-LLM-4.5]

**说明：** 更新集成密钥，新值覆盖旧值，旧密钥立即失效。secret 字段前端 RSA-OAEP 加密传输，后端解密后 AES-256-GCM 加密落库并刷新掩码快照。首次配置（旧值为空）与更新（旧值非空）共用此接口，前端按配置状态切换文案。

**请求参数：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| secret | string | 是 | 新集成密钥密文（RSA-OAEP 加密），明文 ≤200 字符 |

**请求示例：**

```json
{
  "secret": "RSA-OAEP-encrypted-ciphertext-base64"
}
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1780000000000000201",
    "configured": true
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1400 | 参数错误（secret 缺失、密文解密失败） |
| 1500 | 服务内部错误 |

---

#### C4. 连通验证

**接口路径：** `POST /api/integration-secret/test`

**需求追溯：** [需求：F-LLM-4.2.3 连通验证] [需求：F-LLM-4.2.4 规则5]

**说明：** 用当前集成密钥探活 sili-smart-api 会话日志接口（架构文档 3.1.1 的 `/api/conversation-log`），返回连通结果。集成密钥未配置时不允许验证，返回 1303。探活发轻量请求（如拉取 1 条会话列表或健康探测），密钥无效或接口不可达返回 1304 并带失败原因。本接口只覆盖集成密钥对会话日志通道的探活，不对 LLM 模型做连通验证（specs 规则5：LLM 连通性在评估引擎实际调用时校验）。

**请求参数：** 无（使用当前已配置密钥）。

**请求示例：**

```
POST /api/integration-secret/test
```

**响应示例（成功）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "connected": true
  }
}
```

**错误码：**

| 错误码 | 说明 |
|--------|------|
| 1303 | 集成密钥未配置（无法验证） |
| 1304 | 连通验证失败（message 携带失败原因：密钥无效、接口超时、网络不可达等） |
| 1500 | 服务内部错误 |

---

## 4. 错误码总表

### 4.1 本功能新增错误码（config 域 13xx 段）

新增常量须在 [internal/pkg/errcode/errcode.go](../../../hr-backend/internal/pkg/errcode/errcode.go) 注册并补 messages 映射，段位注释更新为 account 10xx、系统初始化 11xx、dimension 12xx、config 13xx、通用 1400/1500。

| 错误码 | 常量 | 含义 | 触发场景 | HTTP 状态 |
|--------|------|------|---------|-----------|
| 1301 | LLMConfigNotFound | 大模型不存在 | 编辑/删除/启用/详情的目标 id 无效 | 200 |
| 1302 | LastLLMConfig | 至少保留一个大模型 | 删除时清单仅剩一个 | 200 |
| 1303 | IntegrationSecretNotConfigured | 集成密钥未配置 | 临时查看、连通验证前置拦截 | 200 |
| 1304 | IntegrationSecretTestFailed | 连通验证失败 | 探活 sili-smart-api 不通过 | 200 |
| 1305 | StaffListUnavailable | 人员列表暂不可用 | sili-smart-api 用户体系不可达 | 200 |
| 1306 | ConfigVersionConflict | 配置版本冲突 | 评估周期配置乐观锁并发更新 | 200 |

### 4.2 复用错误码

| 错误码 | 常量 | 用途 |
|--------|------|------|
| 1003 | Unauthorized | token 缺失或无效，JWT 中间件统一返回 |
| 1400 | BadRequest | 字段必填、长度、格式、枚举值、密文解密失败等参数校验 |
| 1500 | Internal | 网络或服务异常 |

### 4.3 错误响应格式

```json
{
  "code": 1302,
  "message": "last llm config",
  "data": null
}
```

字段校验失败时 message 携带具体字段提示（如 `"model_id required"`），前端据 message 做字段内联报错并保留已填输入。

---

## 5. 安全说明

### 5.1 传输安全

生产强制 HTTPS。密钥类字段（api_key、secret）一律前端 RSA-OAEP 加密传输，明文不落请求体，复用 `/api/auth/public-key` 的 keyId 与 Redis 私钥一次性机制。RSA 明文长度约束与解法见 §2.5。

### 5.2 存储安全

API Key 与集成密钥 AES-256-GCM 加密落库，对称密钥由 `LLM_SECRET_KEY` 环境变量提供，生产强制覆盖。密文与 nonce 写入 TEXT 列。明文禁止落库，掩码快照独立列存。

### 5.3 展示安全

列表与卡片返回掩码快照，详情查询按需解密。明文仅在查看模型弹窗（B6）、集成密钥临时查看（C2）短时可见。前端关闭弹窗即丢弃明文，不落 localStorage、不进 query 缓存。

### 5.4 输入验证

所有字段后端复校验：类型、长度（name ≤50、model_id ≤100、api_url ≤500、api_key/secret 明文 ≤200）、格式（trigger_time 为 HH:mm、api_url 为 URL）、枚举值（period、target_mode、provider）。LIKE 搜索关键词经 `model.EscapeLike` 转义并配 `ESCAPE '\'` 子句（规则文件 §1.11）。

### 5.5 访问控制

本功能无角色区分，所有已登录平台账号共享同一份系统级配置（specs §2.1）。鉴权由 JWT 中间件统一承担，无额外权限校验。

---

**文档版本：** v1.0
**最后更新：** 2026-08-12
**作者：** lixuetao
