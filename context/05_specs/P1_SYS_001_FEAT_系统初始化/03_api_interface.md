# 系统初始化 接口设计

## 文档信息

| 项目 | 内容 |
|------|------|
| 所属 Feature | P1_SYS_001_FEAT_系统初始化 |
| 上游 SSOT | [01_功能需求规格说明书.md](01_功能需求规格说明书.md) |
| 规则依据 | [AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md) |
| 架构依据 | [context/03_architecture/architecture.md](../../03_architecture/architecture.md) 第 4 章 |
| 配套模型 | [04_model_interface.md](04_model_interface.md) |
| 设计日期 | 2026-08-11 |

> 接口设计覆盖 specs 第 5 章四个后端服务与第 4 章页面承载的所有功能点。字段定义、业务规则、权限规则以 specs 为准；Base URL、方法约定、响应结构、错误码、传输安全以 AGENTS_DATABASE_API_RULE.md 为准。

---

## 1. 设计总览

### 1.1 接口清单

| 序号 | 路径 | 方法 | 鉴权 | 限流 | 用途 | 需求追溯 |
|-----|------|------|------|------|------|---------|
| 1 | /api/setup/status | GET | 公开 | IP 60/min | 初始化状态检测，返回就绪状态与环境自检结果，供首访探针与向导页加载 | specs 5.1、4.1.3、3.2 |
| 2 | /api/setup/initialize | POST | 公开（前置校验未初始化） | IP 10/min | 首次初始化提交，创建首个账号并写初始化记录锁定状态，一次性 | specs 5.2、4.1.3 |
| 3 | /api/system/status | GET | 公开 | IP 60/min | 系统状态摘要，返回初始化状态、库类型、版本、启动时间，供状态页与外部探针 | specs 5.3、4.2.2 |
| 4 | /api/system/health-check | POST | JWT | account 10/min | 组件健康测试，逐项探测数据库、Redis、大模型、会话日志集成连通性 | specs 5.4、4.2.3 |

路径分两组：`/api/setup/*` 承载向导就绪与提交（系统未初始化阶段活跃），`/api/system/*` 承载运行状态观测（初始化后长期使用）。与前端路由 `/setup`、`/system/status` 同源对应。

### 1.2 方法约定

遵循规则文件 2.1：GET 仅查询（接口 1、3），POST 仅变更（接口 2 创建账号与初始化记录、接口 4 触发外部探测动作）。POST 以路径后缀区分动作（`initialize`、`health-check`），与现有 `/api/accounts/create` 等风格一致。

### 1.3 认证方式

| 接口 | 认证 | 说明 |
|------|------|------|
| /api/setup/status | 无 | specs 2.3，首访探针与向导加载调用，此时可能不存在任何账号 |
| /api/setup/initialize | 无（前置校验未初始化） | specs 2.3，一次性可用，已初始化后由 1101 拒绝 |
| /api/system/status | 无 | specs 2.3，公开供外部监控探针，仅暴露非敏感摘要 |
| /api/system/health-check | Bearer JWT | specs 2.3，归入 `r.Group("/api", middleware.JWT(jwtMgr))` 鉴权组 |

公开接口（1、2、3）不挂 JWT 中间件，与 `/health`、`/api/login`、`/api/auth/public-key` 同属公开路由组。接口 4 归入受保护路由组，token 缺失或无效返回 401 + code 1003，由现有 JWT 中间件统一处理。

### 1.4 限流策略

沿用现有 `middleware.RateLimit`（Redis 计数，超限 429）。Redis 键前缀与维度：

| 接口 | Redis 键前缀 | 维度 | 阈值 | 理由 |
|------|-------------|------|------|------|
| /api/setup/status | sili-smart-hr:rl:setup-status: | ClientIP | 60/min | 首访探针高频调用，放宽阈值避免误伤正常首访 |
| /api/setup/initialize | sili-smart-hr:rl:setup-init: | ClientIP | 10/min | 公开创建账号入口，收紧阈值防滥用与暴力试探 |
| /api/system/status | sili-smart-hr:rl:sys-status: | ClientIP | 60/min | 公开摘要供探针轮询，放宽 |
| /api/system/health-check | sili-smart-hr:rl:sys-health: | AccountID | 10/min | 探测发起外部大模型与集成请求，收紧避免触发对侧 429（specs 5.4.4） |

initialize 不叠 username 维度：username 是新建值不在库中，username 维度限流无意义，IP 维度足够。health-check 用 account 维度而非 IP：已鉴权接口按账号限流更精确，且一个账号背后通常一个固定出口 IP，账号维度天然防单账号滥用。

### 1.5 响应格式

统一 `{code, message, data}`（规则文件 2.2）。`code=0` 成功，非 0 业务错误码。`data` 无 omitempty，无载荷时为 null。所有接口成功返回 HTTP 200；鉴权失败（code 1003）返回 HTTP 401；其余业务错误返回 HTTP 200 带 code。前端 httpClient 据此解包，与现有 account 链路一致。

---

## 2. 错误码

### 2.1 新增错误码（系统初始化域，11xx 段）

在 [internal/pkg/errcode/errcode.go](../../../hr-backend/internal/pkg/errcode/errcode.go) 新增：

| 错误码 | 常量 | 含义 | 触发接口 | 触发场景 |
|--------|------|------|---------|---------|
| 1101 | SystemAlreadyInitialized | 系统已初始化 | /api/setup/initialize | 已存在初始化记录时再次调用提交接口（specs 规则5、5.2.5） |
| 1102 | EnvironmentNotReady | 环境就绪自检未通过 | /api/setup/initialize | 数据库或缓存连通性阻断项未通过（specs 规则3、5.2.5） |

文案注册到 errcode.messages map：`SystemAlreadyInitialized: "system already initialized"`、`EnvironmentNotReady: "environment not ready"`。

### 2.2 复用错误码

| 错误码 | 常量 | 含义 | 触发接口 | 触发场景 |
|--------|------|------|---------|---------|
| 1003 | Unauthorized | token 缺失或无效 | /api/system/health-check | JWT 校验失败，由中间件统一返回 |
| 1005 | UsernameExists | 账号已存在 | /api/setup/initialize | 并发或存量数据致 username 冲突（首个账号库空一般不触发） |
| 1007 | PasswordInvalid | 密码格式不符 | /api/setup/initialize | 密码强度不达 ≥8 位且字母+数字 |
| 1400 | BadRequest | 请求参数错误 | /api/setup/initialize | username/name 格式不符、RSA 解密失败、JSON 绑定失败 |
| 1500 | Internal | 服务内部错误 | 全部 | 初始化记录查询失败、事务失败等（specs 5.1.5、5.2.5） |

错误码段位规划：account 域 1001-1007，系统初始化域 1101-1102，通用 1400/1500。千位前缀 1 统一标识业务错误类别，百位区分域（0=account，1=system），与现有 1400/1500 的段位思路一致。

---

## 3. 接口详细设计

### 3.1 初始化状态检测

`GET /api/setup/status`

**需求追溯：** specs 5.1 初始化状态检测服务、4.1.3 环境就绪自检加载（后台自动流程）、3.2 流程说明1（首访探针）。

**鉴权：** 公开。**限流：** IP 60/min。

**请求：** 无 query 参数。

**响应：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "initialized": false,
    "db_type": "sqlite",
    "checks": {
      "database": { "connected": true },
      "redis": { "connected": true },
      "jwt_secret": { "secure": false }
    },
    "block_submit": false
  }
}
```

**响应字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| initialized | boolean | 系统是否已初始化，`system_initializations` 存在记录即 true（specs 规则1） |
| db_type | string | 当前数据库类型，值域 `sqlite` / `mysql` / `postgres`，取自 model.DatabaseType |
| checks.database.connected | boolean | 数据库实时连通性，阻断项（specs 4.1.2） |
| checks.redis.connected | boolean | Redis 实时连通性，阻断项，Asynq 底层（specs 4.1.2） |
| checks.jwt_secret.secure | boolean | JWT 密钥是否由 JWT_SECRET 覆盖为安全态；SQLite 默认公开密钥时为 false 仅提示不阻断（specs 4.1.2） |
| block_submit | boolean | 是否阻断提交，= `!database.connected \|\| !redis.connected`（specs 规则3）；jwt_secret 不参与阻断 |

**处理流程（specs 5.1.2）：**

1. 查 `system_initializations` 是否存在记录，得出 initialized。
2. 探测数据库连通（一次轻量查询）、Redis 连通（一次 ping）。
3. 探测 JWT 密钥状态（config.IsDefaultJWTSecret 判定）。
4. 读取当前数据库类型。
5. 组装 initialized、db_type、checks、block_submit 返回。

**异常处理（specs 5.1.5）：** 初始化记录表查询失败时，initialized 返回 false 且 database.connected 返回 false（自检全部未通过，阻断提交），记录 slog 错误日志；缓存探测超时时 redis.connected 返回 false 阻断提交，记录 slog 警告日志。探测失败不改变 HTTP 200 + code 0 的响应结构，仅反映为对应字段 false。

**前端消费：** 首访探针据 initialized 决定跳转向导（false）或放行登录（true）；接口调用失败（网络超时、5xx）按未初始化处理跳转向导页，由本接口复测暴露真实环境问题（specs 3.2 流程说明1）。向导页据 checks 渲染四项自检图标，据 block_submit 决定完成初始化按钮是否置灰。

---

### 3.2 首次初始化提交

`POST /api/setup/initialize`

**需求追溯：** specs 5.2 首次初始化提交服务、4.1.3 完成初始化、6.2 状态转换。

**鉴权：** 公开，前置校验系统未初始化。**限流：** IP 10/min。

**请求：**

```json
{
  "username": "admin",
  "name": "管理员",
  "passwordCipher": "<RSA-OAEP base64 密文>",
  "keyId": "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
}
```

**请求字段说明：**

| 字段 | 类型 | 必填 | 校验规则 | 说明 |
|------|------|------|---------|------|
| username | string | 是 | `^[A-Za-z0-9_]{3,30}$`，全平台唯一 | 首个账号登录账号，写入 accounts.username；校验复用 account 规则（specs 4.1.2 声明完整校验以 P1_ACC_001 为准） |
| name | string | 是 | 1-20 字符（utf8.RuneCountInString） | 账号显示名，写入 accounts.name；校验复用 account 规则 |
| passwordCipher | string | 是 | RSA-OAEP 密文，解密后明文 ≥8 位且字母+数字 | 登录密码密文，前端 RSA-OAEP 加密传输，后端解密明文 bcrypt 哈希写入 accounts.password_hash（规则文件 2.5） |
| keyId | string | 是 | 来自 GET /api/auth/public-key，TTL 5 分钟，一次性 | RSA 密钥对标识，解密成功即删 |

**确认密码口径：** specs 4.1.2 表单含"确认密码"字段，但该字段为前端交互层校验项（specs 4.1.5 实时红字提示），不纳入后端请求体，与 account 域 `createAccountRequest` 无 confirm 字段的实现一致。specs 5.2.2"确认密码一致"在前端提交前完成，后端信任前端校验后的提交。

**响应（成功）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "initialized": true,
    "account": {
      "id": "1234567890123456789",
      "username": "admin",
      "name": "管理员",
      "enabled": true
    }
  }
}
```

**响应字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| initialized | boolean | 提交成功后恒为 true，状态已锁定 |
| account.id | string | 首个账号雪花 ID，JSON string 化规避前端精度坑；同时作为 `system_initializations.creator_account_id` 写入 |
| account.username | string | 登录账号 |
| account.name | string | 显示名 |
| account.enabled | boolean | 恒为 true，业务层显式置值（specs 规则4） |

`account` 复用 account 域 `AccountDTO` 结构，password_hash 不回显。

**处理流程（specs 5.2.2）：**

1. 前置校验：查 `system_initializations`，存在记录返回 1101。
2. 复核环境阻断项：探测数据库与 Redis 连通，任一未通过返回 1102。
3. 字段校验：username 格式、name 长度、RSA 解密、密码强度，不符返回对应码。
4. 事务内：复用 account 账号创建能力写入 accounts（bcrypt 哈希，enabled 业务层置 true）；写 `system_initializations`（creator_account_id 取本次账号 ID，db_type 取当前库类型快照）。
5. 任一失败整体回滚（specs 5.2.4），返回 1500。
6. 成功返回 initialized=true 与脱敏账号，前端跳转登录页。

**事务边界：** 账号创建与初始化记录写入在同一 DB 事务内。复用 account 域底层账号写入能力但将其纳入本事务，不直接调用 `AccountService.CreateAccount`（该方法自带 mu 锁与独立事务边界，无法延伸到初始化记录写入）。具体由初始化提交 service 编排，repository 提供事务入口或共用 GORM `db.Transaction`。

**错误响应：**

| 场景 | HTTP | code | message |
|------|------|------|---------|
| 系统已初始化 | 200 | 1101 | system already initialized |
| 环境阻断项未通过 | 200 | 1102 | environment not ready |
| username 格式错误 / name 超长 / RSA 解密失败 | 200 | 1400 | bad request |
| JSON 绑定失败（请求体格式错，handler 层） | 400 | 1400 | bad request |
| 密码强度不符 | 200 | 1007 | password invalid |
| 账号已存在 | 200 | 1005 | username exists |
| 事务失败 / 初始化记录查询失败 | 200 | 1500 | internal error |

**反枚举考量：** initialize 是公开创建入口，但仅在系统未初始化时可用，已初始化后直接 1101 拒绝，窗口期极短（首访到完成初始化之间）。无需如登录般做时序收敛，限流 IP 10/min 已足够防窗口期滥用。

---

### 3.3 系统状态摘要

`GET /api/system/status`

**需求追溯：** specs 5.3 系统状态摘要服务、4.2.2 运行状态摘要。

**鉴权：** 公开（供外部监控探针）。**限流：** IP 60/min。

**请求：** 无 query 参数。

**响应：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "initialized": true,
    "db_type": "sqlite",
    "version": "v1.0.0",
    "started_at": "2026-08-11T09:00:00Z"
  }
}
```

**响应字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| initialized | boolean | 系统是否已初始化 |
| db_type | string | 当前数据库类型实时值，值域 `sqlite` / `mysql` / `postgres`（与 system_initializations.db_type 快照可能因切库不一致，展示以实时值为准） |
| version | string | 系统版本号，取自构建期 ldflags 注入或常量 |
| started_at | string | 进程本次启动时间，ISO 8601 UTC，前端按 yyyy-MM-dd HH:mm:ss 本地格式化展示（specs 4.2.2） |

**处理流程（specs 5.3.2）：**

1. 查 `system_initializations` 得 initialized。
2. 读取当前数据库类型（model.DatabaseType）、系统版本（构建信息）、进程启动时间（main 启动记录的内存变量）。
3. 组装返回。

**异常处理（specs 5.3.5）：** 初始化记录查询失败时 initialized 返回 false，其余摘要字段正常返回，记录 slog 错误日志。接口始终返回 HTTP 200 + code 0。

**安全口径（specs 5.3.4）：** 公开接口仅暴露非敏感摘要，不含密钥、不含账号信息、不含组件连通细节（组件健康走鉴权接口 3.4）。外部监控探针可据此判定平台存活与就绪态。

**下游消费：** 工作台据此判断哪些运行时配置未完成并就近提示，提示逻辑归工作台模块（specs 4.2.4 规则3）。本接口不返回"未完成配置项"列表，仅提供 initialized 等基础态，工作台自行结合 config 模块数据组织提示。

---

### 3.4 组件健康测试

`POST /api/system/health-check`

**需求追溯：** specs 5.4 组件健康测试服务、4.2.3 立即测试、4.2.2 组件健康状态值枚举。

**鉴权：** Bearer JWT（平台账号）。**限流：** account 10/min。

**请求：** 无 body（空对象或省略）。

**响应：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "database": "connected",
    "redis": "connected",
    "llm": "not_configured",
    "integration": "not_configured"
  }
}
```

**响应字段说明：**

| 字段 | 类型 | 说明 |
|------|------|------|
| database | string | 数据库连通性，值域 `connected` / `disconnected` |
| redis | string | Redis 连通性，值域 `connected` / `disconnected`，Asynq 底层 |
| llm | string | 大模型可达性，值域见下表 |
| integration | string | 会话日志集成可达性，值域见下表 |

**组件健康状态值枚举（specs 4.2.2）：**

| 状态值 | 触发条件 | 适用组件 | 适用阶段 |
|--------|---------|---------|---------|
| connected / disconnected | 实时探测数据库或 Redis 连通成功 / 失败 | database、redis | 全阶段 |
| reachable | config 已配置，连通探测成功 | llm、integration | config 落地后 |
| unreachable | config 已配置，连通探测失败或超时 | llm、integration | config 落地后 |
| not_configured_model | config 已落地但未启用模型 | llm | config 落地后 |
| not_configured_key | config 已落地但未配置集成密钥 | integration | config 落地后 |
| not_configured | config 模块未落地，对应配置项不存在 | llm、integration | config 落地前，两项统一返回 |

database 与 redis 固定 connected/disconnected，不受 config 阶段影响。config 落地后 not_configured 文案不再出现。

**处理流程（specs 5.4.2）：**

1. JWT 鉴权，取 account_id。
2. 探测数据库连通（一次轻量查询）。
3. 探测 Redis 连通（一次 ping）。
4. 读取 config 当前启用模型，发起一次最小大模型请求探测可达性；config 未落地或未启用模型返回对应降级值。
5. 读取 config sili-smart-ap日志集成密钥，发起一次鉴权探测；config 未落地或未配置密钥返回对应降级值。
6. 汇总返回各组件状态。

**异常处理（specs 5.4.5）：** 大模型探测超时或失败该项标 unreachable，集成密钥探测失败该项标 unreachable，数据库或缓存探测失败对应项 disconnected，单项失败不影响其他项，分别记录 slog 警告或错误日志。接口始终返回 HTTP 200 + code 0（探测失败是业务结果而非接口错误）。仅鉴权失败返回 401 + code 1003。

**只读约束（specs 5.4.4 规则1）：** 探测过程对大模型发起一次最小请求、对集成密钥发起一次鉴权探测，均为只读，不修改任何配置与数据。大模型探测受独立限流约束（本接口 account 10/min），单次探测超时上限 10 秒，避免健康测试触发模型侧 429。

**前端消费：** 首次进入系统状态页组件健康区显示"点击立即测试"空状态引导，不自动触发探测（specs 4.2.5）。点击立即测试后调用本接口，探测进行中显示加载态，结果以状态图标加文案呈现。

---

## 4. 口径说明与 SSOT 合规

### 4.1 字段长度与校验口径

specs 4.1.2 给出首个账号表单字段上限（账号 3-64、姓名 1-64、密码 ≥8），同时声明"完整校验以 P1_ACC_001 为准"。实际校验复用 account 域既有规则：

| 字段 | specs 表单描述 | 实际校验（复用 account） | 说明 |
|------|--------------|----------------------|------|
| username | 3-64 字符 | `^[A-Za-z0-9_]{3,30}$` | specs 的 64 是 accounts.username varchar(64) 列宽上限；格式校验取 account 的 3-30 字符正则 |
| name | 1-64 字符 | utf8.RuneCountInString ≤ 20 | specs 的 64 是列宽上限；长度校验取 account 的 ≤20 字符 |
| password | ≥8 字符 | ≥8 位且字母+数字 | specs 4.1.2 仅写 ≥8，但 specs 7.1 声明复用 account 创建能力，实际走 account 的 validatePassword，故强度要求字母+数字 |

口径统一原则：specs 4.1.2 与 specs 7.1（复用 account）之间存在隐含张力，本设计统一到 account 实际规则，因 initialize 复用 account 账号创建能力写入同一 accounts 表，校验口径必须与账号台账一致以避免漂移。specs 4.1.2 的字符上限描述对应列宽留余量，与实际格式校验不冲突。此口径在开发实施时须保持与 account 域同步，account 域校验规则变更时本接口跟随。

### 4.2 初始化记录判定 SSOT

specs 规则1 明确系统就绪状态以 `system_initializations` 记录是否存在为准，与 accounts 表是否有账号解耦。这意味着：即使因历史播种逻辑导致 accounts 表已有数据（如移除播种前的存量 SQLite 库），只要 `system_initializations` 为空，系统仍判定未初始化，首访探针跳转向导。该判定口径不依赖账号计数，避免与 account 域的增删产生耦合。

### 4.3 移除 SQLite 播种的接口影响

specs 7.2 前置变更项（移除 account SQLite 自动播种）落地后，SQLite 开发态首访无默认凭据，必须经 `/api/setup/initialize` 创建首个账号。该改动不影响本 feature 四个接口的设计，仅改变首次启动的数据初始状态（accounts 与 system_initializations 均为空）。详见 04_model_interface.md 第 7.2 节。

### 4.4 前端类型约定

承载雪花 ID 的字段（响应 account.id）前端类型一律 string，不做数值运算（规则文件 1.2）。db_type、version、组件健康状态值均为 string 字面量联合类型，前端用字面量联合约束而非枚举数值。

---

## 5. 验证检查清单

### 5.1 specs 覆盖

- [x] specs 5.1 初始化状态检测 → 接口 3.1 GET /api/setup/status
- [x] specs 5.2 首次初始化提交 → 接口 3.2 POST /api/setup/initialize
- [x] specs 5.3 系统状态摘要 → 接口 3.3 GET /api/system/status
- [x] specs 5.4 组件健康测试 → 接口 3.4 POST /api/system/health-check
- [x] specs 4.1.3 完成初始化按钮 → 接口 3.2
- [x] specs 4.1.3 环境就绪自检加载 → 接口 3.1 checks 字段
- [x] specs 4.2.3 立即测试按钮 → 接口 3.4
- [x] specs 4.2.3 刷新按钮 → 接口 3.3（前端重新拉取摘要）
- [x] specs 6.1/6.2 状态定义与单向锁定 → 04 文档 system_initializations 单行终态
- [x] specs 2.3 接口鉴权矩阵 → 1.3 认证方式逐接口声明

### 5.2 SSOT 合规

- [x] 字段定义与 specs 一致（username/name/password/enabled 写入 accounts，校验口径在 4.1 标注来源）
- [x] 业务规则在接口设计中体现（自检阻断 block_submit、一次性提交 1101、事务原子性）
- [x] 权限规则与 specs 2.3 一致（公开 1/2/3，JWT 4）
- [x] 状态定义与 specs 6.1 一致（记录存在即已初始化，单向终态）
- [x] 所有页面功能（4.1.3、4.2.3 按钮）有对应接口

### 5.3 规则文件合规

- [x] Base URL `/api`，GET 仅查询 POST 仅变更，POST 路径区分动作（规则 2.1）
- [x] 统一响应 `{code,message,data}`，分页不涉及（规则 2.2）
- [x] 错误码集中 errcode，新增 11xx 段不与 account 1xxx 冲突（规则 2.4）
- [x] 密码 RSA-OAEP 加密传输，复用 /api/auth/public-key（规则 2.5）
- [x] 公开路由不挂 JWT，鉴权接口归入 /api 鉴权组（规则 2.6）
- [x] 承载雪花 ID 字段 JSON string 化（规则 1.2）
- [x] 不引入字典表，状态用字符串枚举（规则 1.8）

### 5.4 一致性检查

- [x] 接口设计与 04 模型设计一致（creator_account_id 关联、db_type 快照口径一致）
- [x] 符合架构文档技术选型（Gin RESTful、JWT、GORM、slog）
- [x] 满足非功能需求（限流防滥用、探测只读、单向终态防重复）
