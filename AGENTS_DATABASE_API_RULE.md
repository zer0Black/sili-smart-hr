# 数据库与 API 接口设计规则（项目级）

本文件是 sili-smart-hr 项目接口数据设计的权威规则来源，承载跨 Feature 的数据库与 API 约定。内容从 [CLAUDE.md](CLAUDE.md)、[context/03_architecture/architecture.md](context/03_architecture/architecture.md) 与已落地的 account 全链路代码提炼固化，供「接口数据设计」技能及后续业务域 Feature 复用。

技术实现层面的冲突以本文件为准；业务需求层面以各 Feature 的功能需求规格说明书（specs）为准。

---

## 一、数据库规则

### 1.1 技术选型

多库可切换，按 `SQL_DSN` 前缀选 dialector：空或 `local` 前缀走 SQLite（开发测试，纯 Go glebarez 驱动，路径 `data/sili-smart-hr.db`），`postgres://`/`postgresql://` 前缀走 PostgreSQL，其余走 MySQL。ORM 用 GORM，建表以 AutoMigrate 为主，类型变更与数据回填补手写幂等迁移。生产推荐 PostgreSQL 或 MySQL，SQLite 因库级写锁仅用于开发测试。

MySQL 字符集 utf8mb4；PostgreSQL 编码 UTF-8。

### 1.2 主键策略

| 配置项 | 值 | 说明 |
|-------|------|------|
| 主键类型 | `int64` / `BIGINT` | Go 侧 int64，DB 侧 BIGINT NOT NULL，无自增 |
| 生成方式 | 雪花 ID（应用层生成） | GORM 全局 Create 回调反射赋值，模型 tag 只写 `gorm:"primaryKey"` |
| 自增 | 不采用 | ID 不可枚举、不暴露业务量，分布式部署天然不冲突 |
| NodeID | `SNOWFLAKE_NODE_ID` 环境变量 | 默认 1，多实例部署须唯一（0 到 1023） |
| 前端传输 | JSON string | 雪花超 2^53 超过 JS 安全整数，ID 字段一律 `json:"id,string"` 序列化为 string，前端类型用 string |

实现：雪花生成器封装在 [internal/pkg/snowflake](hr-backend/internal/pkg/snowflake/snowflake.go)，由 [model.InitDB](hr-backend/internal/model/db.go) 启动期初始化并注册全局 GORM Create 回调（`registerSnowflakeIDCallback`），对任何带 `ID int64` 且为 0 的模型透明赋值。domain 模型保持纯净，只把 tag 从 `primaryKey;autoIncrement` 改为 `primaryKey`，新模型零配置自动覆盖。

**前端传输约束（JS 精度坑，硬性）。** 雪花 ID 是 int64，量级可达 9.2×10^18 远超 JS `Number.MAX_SAFE_INTEGER`（2^53-1 ≈ 9×10^15），凡承载雪花 ID 的字段一旦以 JSON number 下发，前端 `JSON.parse` 会让低位归零，ID 不可用、按 ID 查询与关联全部指错对象。规避方式是序列化为 JSON string，但作用域不止主键：

- **主键 `ID`**：domain 模型与 service DTO 都打 `json:"id,string"`（domain 与 DTO 是两条独立序列化路径，必须双层覆盖，account 链路是样板）。
- **外键关联字段**（如 `AccountID`/`AssessmentID`/`QuestionID`）：它们不参与 Create 回调赋值（回调只认字段名 `ID`），但取值来自关联记录的雪花主键，同样超 2^53，json tag 一律带 `,string`（如 `json:"account_id,string"`）。
- **DTO/响应结构里的 ID 转运字段**：凡是值来自雪花 ID 的 int64 字段，无论字段名如何，都要 string 化。

前端对应字段类型一律定义为 `string`，不做数值运算。分页 `total`（int64，条数量级远低于 2^53）与 JWT claims 内的 `AccountID`（后端解析使用、不下发前端）不在此列。

### 1.3 公共字段

业务表统一包含两个时间戳字段，由 GORM 自动维护：

| 字段名 | 模型类型 | GORM tag | 说明 |
|-------|---------|---------|------|
| `created_at` | `time.Time` | `autoCreateTime` | 创建时间，记录写入时赋值 |
| `updated_at` | `time.Time` | `autoUpdateTime` | 更新时间，记录变更时刷新 |

软删除按需引入：需要逻辑删除的表增加 `deleted_at gorm.DeletedAt`（GORM 内建软删除，查询自动过滤，Delete 时自动赋值）。

不引入 `creator`/`updater`/`tenant_id`。项目无多租户、无审计人字段追踪要求，账号体量与运营场景不支撑额外审计列成本。可空业务时间用 `*time.Time`（如 `last_login_at`）。

### 1.4 字段命名规范

snake_case。时间字段命名 `created_at`/`updated_at`/`deleted_at`，对应 `xxx_at` 后缀语义，不使用 `create_time`/`update_time`。布尔字段用语义化正名（`enabled`），不加 `is_` 前缀。

### 1.5 字段类型约束

模型 tag 一律用 GORM 通用类型（`varchar`/`text`/`int`/`bigint`），禁止写库特定类型（`jsonb`/`timestamptz`/`bigserial`），让 GORM 按当前 dialector 翻译，从源头规避多库方言坑。

半结构化数据（特征档案、评分理由、配置快照）用 `TEXT` 列存序列化字符串，禁止使用 JSON/JSONB 类型列。表关联不开外键约束，靠业务字段（主键 ID）关联，关联完整性由应用层校验。布尔字段在模型层用 `bool`，禁止加 `default` tag，规避 MySQL 与 PostgreSQL 布尔默认值规范化差异导致 AutoMigrate 每次启动判定需要 ALTER 形成抖动，新建记录由业务层显式置值。保留字列名（`group`/`key`/`order` 等）仅在手写原生 SQL 时用 `model.QuoteIdent` 包裹，业务代码优先用 GORM 链式 API。

### 1.6 VARCHAR 长度标准

字段长度优先服从 specs 校验规则中明确给出的上下限；specs 未指定的，按下表语义取值：

| 用途 | 长度 | 典型字段 |
|---------|---------|---------|
| 登录账号/标识符 | `varchar(64)` | username（specs 上限 30，列宽留余量） |
| 人员姓名/短名称 | `varchar(64)` | name（specs 上限 20，列宽留余量） |
| 哈希/摘要/令牌 | `varchar(255)` | password_hash（bcrypt 固定 60） |
| 标题/简介 | `varchar(255)` | 配置项名称 |
| 长文本/序列化结构 | `TEXT` | 特征档案、评分理由 |

### 1.7 日期时间字段类型

一律 `time.Time` 配 `autoCreateTime`/`autoUpdateTime`，不写 `DATETIME`/`TIMESTAMP` 字面量。DB 实际类型由 GORM 按 dialector 翻译（MySQL DATETIME、PostgreSQL timestamp with time zone、SQLite TEXT）。可空时间用 `*time.Time`，查询走指针判空。

### 1.8 字典数据设计

项目不引入 `system_dict_type`/`system_dict_data` 字典表体系。状态与枚举用布尔字段（如 `enabled`）或 Go 侧常量加业务字段承载，字段说明不使用 `[字典：xxx]` 标注，亦不生成字典表 INSERT 语句。引入字典表会与现有 account 模型及无多租户约束冲突，本规则显式排除。

### 1.9 表命名规范

沿用 GORM 默认 NamingPolicy，实体复数化：`Account` → `accounts`。不强制模块前缀（如 `acc_`/`sys_`），避免与已落地的 `accounts` 表冲突；新表用语义化复数实体名消歧（`dimensions`、`assessments`、`llm_configs`）。需要时以 `TableName()` 显式指定。

### 1.10 唯一性与软删除协同

凡需软删除且业务键要求唯一的字段，按业务键来源二选一：

**默认方案（用户自选键，如 `username`）：** 数据库层不设单列 `UNIQUE` 约束，改普通 `INDEX`，唯一性由业务层校验时排除软删除记录（`WHERE username = ? AND deleted_at IS NULL`）。软删行保留原键值，历史引用可解释。样板：[repository/account.go](hr-backend/internal/repository/account.go)。

**兜底方案（系统生成键，如 `dimensions.code`）：** 数据库层设 `UNIQUE` 索引拦截查重与写入之间的 TOCTOU 并发窗口，软删时把该行键值改写为占位码（`原值__D<id>`）释放原键供新建复用。适用于键由系统派生、消费方以快照值引用历史（改写软删行不影响历史数据）的场景；MySQL 不支持 partial index（`WHERE deleted_at IS NULL` 形态）三库通吃只能走占位码改写。样板：[domain/dimension.go](hr-backend/internal/domain/dimension.go) 的 `Code` + `DeletedCode`、[model/migrate.go](hr-backend/internal/model/migrate.go) 的存量数据收敛钩子。

默认方案的理由（兜底方案以占位码改写消解同一问题）：`gorm.DeletedAt` 的 `deleted_at` 在未删除时为 NULL，标准 SQL 中 NULL 不参与唯一性判定，多库（含 SQLite/MySQL/PostgreSQL）行为一致地无法拦截多条 `deleted_at IS NULL` 的同名记录；且单列 UNIQUE 会把「软删行 + 同名新行」一并拦下，阻碍 specs 要求的"软删除后键值可复用"。唯一性判定收敛到一处是项目"外键约束不开，关联靠业务字段"思路的延伸。

无软删除的表（如 `session_features.session_key`、`system_params.param_key`、各评分表的 person+period 组合）不在此约束内，直接用 UNIQUE 索引。

### 1.11 模糊查询（LIKE）通配符转义

凡接收用户输入做 `LIKE` 模糊匹配的查询（如关键词搜索 username/name），必须用 [model.EscapeLike](hr-backend/internal/model/db.go) 转义输入里的 `%`、`_`、`\`，并在 SQL 里显式写 `ESCAPE '\'` 子句，让输入按字面匹配而非被当通配符。占位符绑定只防 SQL 注入，不解决通配符字面化，两者各自独立。

| 要点 | 说明 |
|------|------|
| 转义对象 | `%`（任意串）、`_`（单字符）、`\`（转义符自身） |
| 工具函数 | `model.EscapeLike(s)`，先转义反斜杠自身再转义 `%`/`_`，顺序敏感避免二次替换 |
| SQL 子句 | `WHERE col LIKE ? ESCAPE '\\'`，占位符绑定 `"%"+EscapeLike(k)+"%"` |
| 跨方言 | SQLite/MySQL/PostgreSQL 的 LIKE 均支持 ESCAPE 子句，反斜杠语义一致 |
| 反例 | 直接 `LIKE "%"+k+"%"` 会让搜 "a_b" 命中 "axb"、搜 "50%" 命中全表 |

GORM 链式 `Where` 透传 ESCAPE 子句，与软删除自动追加的 `deleted_at IS NULL` 叠加无冲突。account 域 `ListAccounts` 是首个落地样板，新增域的关键词搜索照此实现。

---

## 二、API 接口规则

### 2.1 基础配置

| 配置项 | 值 | 说明 |
|-------|------|------|
| Base URL 前缀 | `/api` | 与已落地 /api/login、/api/me 一致，无版本前缀 |
| 版本控制 | 不分段 | 本系统单仓内部演进，靠 Feature 与变更组管理，URL 不挂 /v1 |
| 方法约定 | GET 仅查询，POST 仅变更 | POST 以路径区分动作：/create /update /delete /reset-password 等 |
| 认证方式 | Bearer JWT | `Authorization: Bearer {token}`，公开接口不挂 JWT |

前端不硬编码后端地址，dev 走 rsbuild devServer proxy 转发 /api，生产走 Nginx 同域反代免跨域。后端另配最小手写 CORS 中间件兜底，放通 `config.cors.allowed_origins`。

### 2.2 统一响应格式

```json
{
  "code": 0,
  "message": "ok",
  "data": { }
}
```

`code = 0` 成功，非 0 为业务错误码。`data` 字段无 `omitempty`，成功无载荷时为 `null`。`message` 字段名是 `message`（非 `msg`）。分页结构作为 `data` 透传：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [ ],
    "total": 100,
    "page": 1,
    "page_size": 20
  }
}
```

### 2.3 分页参数

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `page` | integer | 否 | 1 | 页码，从 1 开始 |
| `page_size` | integer | 否 | 20 | 每页数量 |

分页响应用 `list`/`total`/`page`/`page_size` 四字段，不使用 `pageNo`/`totalPages`。

### 2.4 错误码规范

错误码集中在 [internal/pkg/errcode/errcode.go](hr-backend/internal/pkg/errcode/errcode.go)，分两段：`0` 成功，`1xxx` 业务错误（千位前缀 1 标识业务错误类别）。当前 account 域错误码：

| 错误码 | 常量 | 含义 |
|--------|------|------|
| 0 | Success | 成功 |
| 1001 | InvalidCredentials | 凭证错误（账号不存在/密码错误/账号禁用/RSA 解密失败统一返回） |
| 1002 | AccountDisabled | 账号禁用（定义但登录路径刻意不返回，反枚举） |
| 1003 | Unauthorized | token 缺失或无效 |
| 1004 | AccountNotFound | 用户管理操作的目标账号不存在 |
| 1005 | UsernameExists | 账号已存在 |
| 1006 | LastEnabledAccount | 至少保留一个启用的账号 |
| 1007 | PasswordInvalid | 密码格式不符（不少于 8 位且同时含字母与数字） |
| 1400 | BadRequest | 请求参数格式错误 |
| 1500 | Internal | 服务内部错误 |

Handler 错误映射（见 [handler/account.go](hr-backend/internal/api/handler/account.go) `handleServiceError`）：所有成功 HTTP 200；`code == 1003` 返回 HTTP 401；其余业务错误返回 HTTP 200 带 code；非 `*service.Error` 返回 HTTP 500 带 1500。前端响应拦截器看 body code 而非 HTTP status。

### 2.5 传输安全

所有密码提交入口（登录、新增账号、编辑改密码、重置密码）的密码字段一律前端 RSA-OAEP 加密为密文传输，明文不落请求体。密码库内只存 bcrypt 哈希，`PasswordHash` 字段 `json:"-"`，任何序列化路径不回显。

RSA 密钥对动态生成，私钥以 `keyId` 关联存 Redis（复用 Asynq 已有 Redis 实例），TTL 5 分钟，解密成功即删做到一次性；公钥接口供前端取公钥与 keyId，需限流防滥用。Redis 键命名见各 Feature 的数据库设计文档。

### 2.6 路由组织

全局中间件顺序：Recovery → Logger → CORS（recovery 最外层兜底 panic）。公开路由组（`GET /health`、`POST /api/login`、`GET /api/auth/public-key`）不挂 JWT。受保护路由组 `r.Group("/api", middleware.JWT(jwtMgr))` 承载 `/me` 与全部业务接口，新业务接口归入此组。

---

**文档版本：** v1.0
**创建日期：** 2026-08-10
**作者：** lixuetao
