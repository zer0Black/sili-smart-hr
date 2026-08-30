# 登录与用户管理 接口设计

## 文档信息

- 所属 Feature：P1_ACC_001_FEAT_登录与用户管理
- 规则依据：[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)（技术实现 SSOT）
- 业务依据：[01_功能需求规格说明书.md](01_功能需求规格说明书.md)（业务需求 SSOT）
- 架构依据：[context/03_architecture/architecture.md](../../03_architecture/architecture.md) §4.3、§4.5

业务需求与规则文件冲突时，业务字段/规则以 specs 为准，技术实现（响应结构、错误码、传输协议）以规则文件为准。需求追溯标注 specs 章节号。

---

## 基础信息

**业务域：** account（账号与认证）

**接口协议：** HTTP，简化 GET/POST 模式（GET 仅查询，POST 仅变更）

**Base URL：** `/api`（无版本前缀，与已落地的 /api/login、/api/me 一致）

**认证方式：** Bearer JWT，请求头 `Authorization: Bearer {token}`；公开接口不挂 JWT，受保护接口经 JWT 中间件鉴权，失败返回 401 + code 1003

**统一响应格式：**

```json
{ "code": 0, "message": "ok", "data": { } }
```

`code = 0` 成功；非 0 业务错误码。`data` 无 `omitempty`，无载荷时为 `null`。分页 `data` 为 `{list, total, page, page_size}`。

---

## 核心设计原则

| 方法 | 用途 | 请求体 | 场景 |
|------|------|--------|------|
| GET | 查询 | 无（URL 参数） | 公钥获取、当前账号、账号列表 |
| POST | 变更 | JSON | 登录、新增、编辑、删除、启停、重置密码 |

POST 操作以路径后缀区分动作：`/create`、`/update`、`/delete`、`/toggle-enabled`、`/reset-password`。更新与删除类操作以请求体 `id` 标识目标资源。

**ID 类型约定：** 全系统主键为雪花 ID（int64，应用层生成，去自增，见规则文件 §1.2）。雪花值超过 2^53，超过 JavaScript 安全整数范围，所有接口的 `id` 字段（请求参数与响应字段）一律以 JSON string 传输，后端 `json:"id,string"` 序列化，前端类型为 string。下文示例与字段表中 id 值为雪花字符串占位。

---

## 接口列表

### 一、公开组（不挂 JWT）

#### 1.1 获取 RSA 公钥

**接口路径：** `GET /api/auth/public-key`

**需求追溯：** specs §4.1 后台自动流程「获取 RSA 公钥」、§4.1.4 规则2 密码传输加密

**认证：** 无（登录页加载时调用，未登录可访问）

**限流：** 按 IP 维度限流（建议 60 次/分钟），超限返回 429，防公钥滥用与密钥对生成消耗

**功能说明：** 后端按需生成 RSA-OAEP 密钥对，公钥连同 `keyId` 返回前端；私钥以 `keyId` 为后缀存 Redis（TTL 5 分钟，详见 [04_model_interface.md](04_model_interface.md) Redis 键设计）。前端用公钥加密密码明文后提交登录或账号弹窗。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "publicKey": "-----BEGIN PUBLIC KEY-----\nMIIBIjANBgkqhki...\n-----END PUBLIC KEY-----",
    "keyId": "a1b2c3d4e5f6",
    "expiresIn": 300
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| publicKey | string | RSA 公钥 PEM（SubjectPublicKeyInfo，base64 编码），前端 node-forge RSA-OAEP 加密用 |
| keyId | string | 密钥对标识，提交密文时原样回传，后端据此从 Redis 取私钥解密 |
| expiresIn | integer | 公钥有效期（秒），与 Redis 私钥 TTL 一致，固定 300（5 分钟） |

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 密钥对生成失败 | 500 | 1500 | Internal，罕见，Redis 或随机源异常 |

---

#### 1.2 登录

**接口路径：** `POST /api/login`

**需求追溯：** specs §4.1.3 账号密码登录、§4.1.4 规则1 反枚举、规则2 传输加密、规则4 登录成功态

**认证：** 无

**功能说明：** 校验账号密码，成功签发 JWT 返回前端，前端写入持久存储跳转工作台。密码以 RSA-OAEP 密文传输，后端用 keyId 关联的私钥解密后 bcrypt 比对。

**请求参数：**

```json
{
  "username": "admin",
  "passwordCipher": "base64-oaep-ciphertext...",
  "keyId": "a1b2c3d4e5f6"
}
```

| 字段 | 类型 | 必填 | 校验 | 说明 |
|------|------|------|------|------|
| username | string | 是 | 必填，自动去前后空格 | 登录账号 |
| passwordCipher | string | 是 | 必填 | 密码明文经 RSA-OAEP 公钥加密后的 base64 密文 |
| keyId | string | 是 | 必填 | 公钥接口返回的 keyId，后端据此取私钥 |

**响应示例（成功）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "account": {
      "id": "1845700000000000001",
      "username": "admin",
      "name": "管理员"
    }
  }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 参数缺失/格式错 | 200 | 1400 | BadRequest，缺字段或 keyId 空走参数校验，非反枚举范围 |
| 账号不存在 | 200 | 1001 | InvalidCredentials，反枚举统一路径 |
| 密码错误 | 200 | 1001 | InvalidCredentials，反枚举统一路径 |
| 账号禁用 | 200 | 1001 | InvalidCredentials，反枚举统一路径，刻意不返回 1002 |
| RSA 解密失败/keyId 过期/私钥不存在/密文损坏 | 200 | 1001 | InvalidCredentials，反枚举统一路径 |
| 参数绑定失败 | 400 | 1400 | BadRequest，JSON 解析失败 |

**反枚举时序收敛（specs §4.1.4 规则1）：** 账号不存在、密码错误、账号禁用、RSA 解密失败四类路径各执行一次等效 bcrypt 比对收敛时序侧信道。顺序敏感：先 RSA 解密，再 bcrypt 比对密码，最后判 Enabled。前端登录页只识别凭证错误（1001）与通用错误两类，不区分失败原因，统一提示"账号或密码错误"。

**与现有脚手架的偏差：** 现有 `POST /api/login`（[handler/account.go](../../../hr-backend/internal/api/handler/account.go)）请求体为 `{username, password}` 明文，属脚手架占位。本规格将其规格化为 RSA-OAEP 密文传输，dev 阶段需调整 `loginRequest` 结构与 `service.AccountService.Login` 签名（接收 `passwordCipher`、`keyId`，内部解密）。

---

### 二、受保护组（挂 JWT）

以下接口归入 `r.Group("/api", middleware.JWT(jwtMgr))`，请求头须带 `Authorization: Bearer {token}`。token 缺失或无效返回 401 + code 1003，前端触发登出。

#### 2.1 当前账号

**接口路径：** `GET /api/me`

**需求追溯：** specs §4.1.4 规则4 登录成功态（token 过期触发登出链路闭合）

**功能说明：** 已落地接口，闭合鉴权链路。经 JWT 中间件注入的 `account_id` 查当前账号，返回脱敏账号。本 Feature 不改其行为，列入仅为接口清单完整。

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "account": {
      "id": "1845700000000000001",
      "username": "admin",
      "name": "管理员"
    }
  }
}
```

---

#### 2.2 账号列表

**接口路径：** `GET /api/accounts`

**需求追溯：** specs §4.2.3 查询、后台自动流程「列表加载」、§4.2.2 显示字段

**功能说明：** 账号台账分页查询，支持按账号与姓名模糊匹配。软删除记录不返回（GORM 自动过滤 `deleted_at`）。平台账号统一全权，无数据范围隔离。

**查询参数：**

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| page | integer | 否 | 1 | 页码，从 1 开始 |
| page_size | integer | 否 | 20 | 每页数量 |
| keyword | string | 否 | 空 | 模糊匹配 username 与 name，空则返回全部 |

**请求示例：**

```
GET /api/accounts?page=1&page_size=20&keyword=adm
```

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "1845700000000000001",
        "username": "admin",
        "name": "管理员",
        "enabled": true,
        "last_login_at": "2026-08-10T15:30:00Z"
      }
    ],
    "total": 1,
    "page": 1,
    "page_size": 20
  }
}
```

**响应字段：**

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 账号 ID |
| username | string | 登录账号，列表主展示项 |
| name | string | 真实姓名，账号列副行展示 |
| enabled | boolean | 启用状态，行内可切换 |
| last_login_at | string\|null | 最近登录成功时间（RFC 3339），空前端显示「-」 |

**密码字段处理：** 列表不返回任何密码字段。specs §4.2.2 明确密码列为掩码占位、系统预设，前端固定展示掩码（如 ●●●●●●），后端不回显，`PasswordHash` 永不出现在响应。

**全系统无工号：** 列表不出现工号列，keyword 不支持工号匹配（specs §4.2.2 注）。

---

#### 2.3 新增账号

**接口路径：** `POST /api/accounts/create`

**需求追溯：** specs §4.2.3 新增账号、§4.3.3 保存、§4.3.2 涉及字段、§4.2.4 规则1 账号唯一

**功能说明：** 创建新账号，密码 RSA-OAEP 密文传输，后端解密后 bcrypt 哈希存储。新建账号默认启用。

**请求参数：**

```json
{
  "username": "zhangsan",
  "name": "张三",
  "passwordCipher": "base64-oaep-ciphertext...",
  "keyId": "a1b2c3d4e5f6",
  "enabled": true
}
```

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| username | string | 是 | 3 到 30 位字母、数字或下划线；全局唯一（排除软删除）；自动去前后空格 | 登录账号，保存后不可改 [长度来源：specs §4.3.2] |
| name | string | 是 | 上限 20 字符 | 真实姓名 [长度来源：specs §4.3.2] |
| passwordCipher | string | 是 | RSA-OAEP 密文，明文不少于 8 位且同时含字母与数字 | 登录密码密文 |
| keyId | string | 是 | 公钥接口返回 | 解密用 keyId |
| enabled | boolean | 否 | 无 | 控制可否登录，缺省 true，业务层显式置值 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1845700000000000012",
    "username": "zhangsan",
    "name": "张三",
    "enabled": true
  }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| username 格式不符 | 200 | 1400 | BadRequest |
| name 超长 | 200 | 1400 | BadRequest |
| username 已存在 | 200 | 1005 | UsernameExists，唯一性校验排除软删除记录 |
| 密码明文格式不符 | 200 | 1007 | PasswordInvalid，少于 8 位或缺少字母/数字 |
| RSA 解密失败/keyId 过期 | 200 | 1400 | BadRequest，用户管理路径非反枚举范围，提示重新获取公钥 |

**业务规则关联：** 账号唯一性校验查询时排除软删除（`WHERE username = ? AND deleted_at IS NULL`），软删除账号名可复用（specs §4.2.4 规则4）。

---

#### 2.4 编辑账号

**接口路径：** `POST /api/accounts/update`

**需求追溯：** specs §4.2.3 编辑、§4.3.3 保存、§4.3.4 规则1 账号不可改、规则2 编辑降停保留校验

**功能说明：** 修改姓名与启用状态，可选重置密码。账号字段不可改，请求不接收 username。密码选填，留空表示不改。编辑模式回填数据来源是列表行（账号、姓名、启用状态），无独立详情接口。

**请求参数：**

```json
{
  "id": "1845700000000000012",
  "name": "张三丰",
  "passwordCipher": "base64-oaep-ciphertext...",
  "keyId": "a1b2c3d4e5f6",
  "enabled": false
}
```

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| id | string | 是 | 必填，目标账号存在且未软删除 | 目标账号 |
| name | string | 是 | 上限 20 字符 | 真实姓名 |
| passwordCipher | string | 否 | 留空不改；非空时同新增校验 | 新密码密文，留空保持原密码 |
| keyId | string | 条件必填 | passwordCipher 非空时必填 | 解密用 keyId |
| enabled | boolean | 否 | 无 | 启用状态 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "1845700000000000012",
    "username": "zhangsan",
    "name": "张三丰",
    "enabled": false
  }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 目标账号不存在 | 200 | 1004 | AccountNotFound |
| name 超长 | 200 | 1400 | BadRequest |
| 密码明文格式不符 | 200 | 1007 | PasswordInvalid |
| RSA 解密失败/keyId 过期 | 200 | 1400 | BadRequest |
| 降停且为最后一个启用账号 | 200 | 1006 | LastEnabledAccount，specs §4.3.4 规则2 |

**业务规则关联：** enabled 由 true 变为 false 时，若该账号是最后一个启用账号则被拦截（specs §4.2.4 规则2、§4.3.4 规则2）。

---

#### 2.5 删除账号

**接口路径：** `POST /api/accounts/delete`

**需求追溯：** specs §4.2.3 删除、§4.2.4 规则4 软删除、§6.2 状态转换

**功能说明：** 软删除账号，列表不再展示，账号名可复用。前端二次确认后提交。

**请求参数：**

```json
{ "id": "1845700000000000012" }
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 目标账号 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": { "id": "1845700000000000012" }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 目标账号不存在 | 200 | 1004 | AccountNotFound |
| 删除且为最后一个启用账号 | 200 | 1006 | LastEnabledAccount，specs §6.2 启用→已删除需非最后启用 |

**业务规则关联：** 软删除由 GORM `deleted_at` 承载，删除即赋值 `deleted_at`，后续查询自动过滤。停用账号删除无约束（specs §6.2 停用→已删除无约束）。

---

#### 2.6 启停切换

**接口路径：** `POST /api/accounts/toggle-enabled`

**需求追溯：** specs §4.2.3 启停切换、§4.2.4 规则2 至少保留一个启用账号、规则3 启停即时生效

**功能说明：** 切换账号启用状态，即时写入，停用账号即时无法登录（再次登录走凭证错误路径）。

**请求参数：**

```json
{ "id": "1845700000000000012", "enabled": false }
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| id | string | 是 | 目标账号 |
| enabled | boolean | 是 | 目标启用状态 |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": { "id": "1845700000000000012", "enabled": false }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 目标账号不存在 | 200 | 1004 | AccountNotFound |
| 停用且为最后一个启用账号 | 200 | 1006 | LastEnabledAccount |

---

#### 2.7 重置密码

**接口路径：** `POST /api/accounts/reset-password`

**需求追溯：** specs §4.2.3 重置密码、§4.4.3 确认重置、§4.4.4 规则1 重置即时失效

**功能说明：** 为指定账号设置新密码，原密码立即失效，新密码即时可用于登录。停用账号亦可重置，重置不改变启用状态。新密码 RSA-OAEP 密文传输。

**请求参数：**

```json
{
  "id": "1845700000000000012",
  "passwordCipher": "base64-oaep-ciphertext...",
  "keyId": "a1b2c3d4e5f6"
}
```

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| id | string | 是 | 目标账号存在 | 目标账号 |
| passwordCipher | string | 是 | RSA-OAEP 密文，明文不少于 8 位且同时含字母与数字 | 新密码密文 |
| keyId | string | 是 | 公钥接口返回 | 解密用 keyId |

**响应示例：**

```json
{
  "code": 0,
  "message": "ok",
  "data": { "id": "1845700000000000012" }
}
```

**错误场景：**

| 场景 | HTTP | code | 说明 |
|------|------|------|------|
| 目标账号不存在 | 200 | 1004 | AccountNotFound |
| 密码明文格式不符 | 200 | 1007 | PasswordInvalid |
| RSA 解密失败/keyId 过期 | 200 | 1400 | BadRequest |

---

## 错误码规范

错误码集中在 [internal/pkg/errcode/errcode.go](../../../hr-backend/internal/pkg/errcode/errcode.go)，本 Feature 新增 1004 到 1007 四个账号管理错误码，已在 [AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md) §2.4 固化。

| 错误码 | 常量 | 含义 | 本 Feature 是否新增 |
|--------|------|------|------|
| 0 | Success | 成功 | 否（现有） |
| 1001 | InvalidCredentials | 凭证错误（登录反枚举统一返回） | 否（现有） |
| 1002 | AccountDisabled | 账号禁用（登录路径刻意不返回） | 否（现有） |
| 1003 | Unauthorized | token 缺失或无效 | 否（现有） |
| 1004 | AccountNotFound | 用户管理目标账号不存在 | 是 |
| 1005 | UsernameExists | 账号已存在 | 是 |
| 1006 | LastEnabledAccount | 至少保留一个启用的账号 | 是 |
| 1007 | PasswordInvalid | 密码格式不符 | 是 |
| 1400 | BadRequest | 请求参数格式错误 | 否（现有） |
| 1500 | Internal | 服务内部错误 | 否（现有） |

**HTTP 状态映射（与现有 `handleServiceError` 一致）：** code 1003 返回 HTTP 401；其余业务错误（含 1001、1004 到 1007、1400）返回 HTTP 200 带 code；JSON 绑定失败返回 HTTP 400 + 1400；非 `*service.Error` 返回 HTTP 500 + 1500。前端响应拦截器看 body code 而非 HTTP status。

**错误响应示例：**

```json
{ "code": 1005, "message": "username exists", "data": null }
```

`message` 取 errcode 默认文案，handler 可经 `response.FailWithMsg` 覆盖为中文场景文案（如「账号已存在」），前端按 code 处理而非解析文案。

---

## 安全说明

**密码传输：** 所有密码提交入口（登录、新增、编辑改密码、重置密码）的密码字段一律前端 RSA-OAEP 加密为密文传输，明文不落请求体。前端进入登录页或打开含密码字段弹窗时先调 `GET /api/auth/public-key` 取公钥与 keyId。

**密码存储：** 库内只存 bcrypt 哈希，`PasswordHash` 字段 `json:"-"`，任何序列化路径不回显，列表与详情均不返回密码字段。

**RSA 动态密钥：** 后端按需生成 RSA 密钥对，私钥以 keyId 关联存 Redis（复用 Asynq 已有 Redis），TTL 5 分钟，解密成功即删做到一次性，私钥不落库不落盘。Redis 键设计见 [04_model_interface.md](04_model_interface.md)。

**登录反枚举：** 账号不存在、密码错误、账号禁用、RSA 解密失败四类路径统一返回 1001，各执行一次等效 bcrypt 比对收敛时序侧信道，顺序敏感（先 RSA 解密，再 bcrypt，最后判 Enabled）。

**限流：** 公钥接口按 IP 限流防密钥对生成滥用；登录接口按 IP 加 username 维度限流作为反枚举辅助。超限返回 429。

**JWT：** HS256 对称签名，claims 含 `account_id`、`username`、`exp`，强制 issuer 校验防跨服务重放，强制校验签名方法防 alg=none 攻击。生产（非 SQLite）必须由 `JWT_SECRET` 环境变量覆盖默认公开密钥，否则进程拒绝启动。

---

## 接口清单总览

| 分组 | 方法 | 路径 | 功能 | 认证 | 需求追溯 |
|------|------|------|------|------|---------|
| 公开 | GET | /api/auth/public-key | 获取 RSA 公钥 | 无 | §4.1 |
| 公开 | POST | /api/login | 登录 | 无 | §4.1.3 |
| 受保护 | GET | /api/me | 当前账号 | JWT | §4.1.4 |
| 受保护 | GET | /api/accounts | 账号列表 | JWT | §4.2.3 |
| 受保护 | POST | /api/accounts/create | 新增账号 | JWT | §4.2.3 §4.3 |
| 受保护 | POST | /api/accounts/update | 编辑账号 | JWT | §4.2.3 §4.3 |
| 受保护 | POST | /api/accounts/delete | 删除账号 | JWT | §4.2.3 §6.2 |
| 受保护 | POST | /api/accounts/toggle-enabled | 启停切换 | JWT | §4.2.3 |
| 受保护 | POST | /api/accounts/reset-password | 重置密码 | JWT | §4.2.3 §4.4 |

---

**文档版本：** v1.0
**创建日期：** 2026-08-10
**作者：** lixuetao
