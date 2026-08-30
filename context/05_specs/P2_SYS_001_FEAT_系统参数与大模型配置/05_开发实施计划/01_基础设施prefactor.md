# 基础设施 prefactor 实现计划

> **目标：** 把系统参数与大模型配置三个业务子计划（02/03/04）共同依赖的横切耦合点提前做成干净接缝，包括数据库模型基建、密钥加解密、配置项、错误码、前端契约类型与测试基建，使后续业务子计划各自独立实现、终审累积 diff 不互相纠缠。
> **架构：** 后端按 account 四层样板新增 domain 模型并登记进 migrate.go，新增 crypto 包承载 AES-256-GCM 存储加密与掩码，rsakey 升 3072 扩大传输层明文上限，config 新增 LLM_SECRET_KEY 与外部 API 地址；前端搭建 Vitest 测试基建并在 contracts.ts 集中定义三个业务域的共享契约类型。本子计划是纯基建，不含任何业务接口与页面。
> **技术栈：** Go 1.25（crypto/aes + crypto/cipher + crypto/rsa + crypto/sha256 标准库）、Vitest + @testing-library/react + jsdom、TypeScript。

---

## 文件结构

### 新建文件
- `hr-backend/internal/domain/assessment_config.go` — AssessmentConfig 与 AssessmentConfigMember 两个 GORM 模型
- `hr-backend/internal/domain/llm_config.go` — LLMConfig GORM 模型
- `hr-backend/internal/domain/integration_secret.go` — IntegrationSecret GORM 模型
- `hr-backend/internal/pkg/crypto/aesgcm.go` — AES-256-GCM 加解密、密钥派生、掩码三类纯函数
- `hr-frontend/vitest.config.ts` — Vitest 配置（jsdom 环境、@ 别名、setup 文件）
- `hr-frontend/src/test/setup.ts` — 测试全局 setup（@testing-library/jest-dom 类型扩展）
- `hr-frontend/src/test/example.test.tsx` — 基建自检示例测试（验证 Vitest 可跑）

### 修改文件
- `hr-backend/internal/model/migrate.go` — allModels() 追加四个模型指针，C 段追加两个单例 seed 钩子
- `hr-backend/internal/pkg/errcode/errcode.go` — 追加 13xx 段六个错误码常量与 messages，更新段位注释
- `hr-backend/internal/config/config.go` — 新增 LLMSettings 与 IntegrationSettings 结构、BindEnv 两个环境变量、applyDefaults 默认值、IsDefaultLLMSecretKey 辅助函数
- `hr-backend/cmd/server/main.go` — 启动序列追加 LLM_SECRET_KEY 生产熔断
- `hr-backend/internal/pkg/rsakey/rsakey.go` — RSA 密钥位长 2048 升 3072
- `hr-backend/configs/config.yaml` — 追加 llm 与 integration 两段（双重公开默认值）
- `hr-frontend/src/lib/contracts.ts` — 追加 13xx ErrCode 常量与三个业务域的 DTO 类型
- `hr-frontend/package.json` — 新增 vitest 等测试依赖与 test 脚本
- `hr-frontend/CLAUDE.md` — 更新测试基建描述（去掉"未配测试框架"表述，补 Vitest 约定）

---

## 数据模型来源

| 来源 | 文件路径 | 状态 |
|------|----------|------|
| 04_model_interface.md | `/context/05_specs/P2_SYS_001_FEAT_系统参数与大模型配置/04_model_interface.md` | ✅ 存在 |

**引用说明：** 本子计划数据库任务引用 04_model_interface.md 的四张表 DDL（assessment_configs、assessment_config_members、llm_configs、integration_secrets）与 §4 数据初始化的 seed 规则。GORM 模型 tag 为权威，DDL 供参考。

---

## 任务清单

- [x] **T1: 四表 domain 模型与迁移登记 + 单例 seed**

  **文件：**
  - 引用：`/context/05_specs/P2_SYS_001_FEAT_系统参数与大模型配置/04_model_interface.md` → assessment_configs / assessment_config_members / llm_configs / integration_secrets 建表
  - 引用：`/context/05_specs/P2_SYS_001_FEAT_系统参数与大模型配置/04_model_interface.md` → §4 数据初始化（两个单例 seed）
  - 创建：`hr-backend/internal/domain/assessment_config.go`
  - 创建：`hr-backend/internal/domain/llm_config.go`
  - 创建：`hr-backend/internal/domain/integration_secret.go`
  - 修改：`hr-backend/internal/model/migrate.go:48-56`（allModels 函数体）
  - 修改：`hr-backend/internal/model/migrate.go:32-44`（C 段追加两个 seed 钩子）
  - 测试：`hr-backend/internal/domain/assessment_config_test.go`（由实现者按 TDD 指导创建）

  **契约：**

  `domain/assessment_config.go`：
  ```go
  type AssessmentConfig struct {
      ID          int64     `gorm:"primaryKey" json:"id,string"`
      Period      string    `gorm:"type:varchar(16);not null" json:"period"`
      TriggerTime string    `gorm:"type:varchar(8);not null" json:"trigger_time"`
      TargetMode  string    `gorm:"type:varchar(16);not null" json:"target_mode"`
      Version     int       `gorm:"not null" json:"version"`
      CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
      UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
  }

  type AssessmentConfigMember struct {
      ID                 int64     `gorm:"primaryKey" json:"id,string"`
      AssessmentConfigID int64     `gorm:"not null;index" json:"assessment_config_id,string"`
      StaffID            string    `gorm:"type:varchar(64);not null;index" json:"staff_id"`
      StaffName          string    `gorm:"type:varchar(64);not null" json:"staff_name"`
      CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
      UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
  }
  ```

  `domain/llm_config.go`：
  ```go
  type LLMConfig struct {
      ID           int64     `gorm:"primaryKey" json:"id,string"`
      Name         string    `gorm:"type:varchar(64);not null" json:"name"`
      Provider     string    `gorm:"type:varchar(32);not null" json:"provider"`
      ModelID      string    `gorm:"type:varchar(100);not null" json:"model_id"`
      APIURL       string    `gorm:"type:varchar(500);not null" json:"api_url"`
      APIKeyCipher string    `gorm:"type:text;not null" json:"-"`
      APIKeyMasked string    `gorm:"type:varchar(255);not null" json:"api_key_masked"`
      Enabled      bool      `gorm:"index" json:"enabled"`
      CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
      UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
  }
  ```

  `domain/integration_secret.go`：
  ```go
  type IntegrationSecret struct {
      ID           int64     `gorm:"primaryKey" json:"id,string"`
      SecretCipher string    `gorm:"type:text;not null" json:"-"`
      SecretMasked string    `gorm:"type:varchar(255);not null" json:"secret_masked"`
      CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
      UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
  }
  ```

  关键逻辑：
  - 三组模型严格对齐 04 §3 表结构字段名与类型。密文列 APIKeyCipher/SecretCipher 用 `json:"-"` 杜绝任何序列化路径泄露明文（仿 domain.Account.PasswordHash）。雪花 ID 主键一律 `json:"id,string"`，外键 AssessmentConfigID 同样 `,string` 化。Enabled 为 bool 不加 default tag，由业务层显式置值。无软删除（模型物理删除，单例覆盖更新）。
  - migrate.go 的 `allModels()` 追加 `&domain.AssessmentConfig{}`、`&domain.AssessmentConfigMember{}`、`&domain.LLMConfig{}`、`&domain.IntegrationSecret{}` 四个指针。雪花 ID 由全局 Create 回调透明赋值（模型字段名为 `ID int64` 即自动生效，seed 显式用 snowflake.NextID）。
  - migrate.go 的 C 段仿照现有 dimension_settings seed 模式（`db.Where("1 = 1").First(&SomeModel{}); errors.Is(err, gorm.ErrRecordNotFound)` 判空），追加两个单例 seed：AssessmentConfig 默认行（Period="weekly"、TriggerTime="23:00"、TargetMode="all"、Version=1、ID=snowflake.NextID()），IntegrationSecret 空密钥行（SecretCipher=""、SecretMasked=""、ID=snowflake.NextID()）。两个 seed 各自独立判空，互不影响。LLMConfig 与 AssessmentConfigMember 首启为空不 seed。

  **验收锚点：**
  - 核心断言（迁移测试，白盒 package model，用 :memory: SQLite）：`migrateDB(db)` 后 `db.First(&domain.AssessmentConfig{})` 返回 nil error 且记录 Period=="weekly"、TriggerTime=="23:00"、TargetMode=="all"、Version==1；`db.First(&domain.IntegrationSecret{})` 返回 nil error 且 SecretCipher==""、SecretMasked==""；重跑 migrateDB 后两表仍各只 1 行（幂等）；`db.AutoMigrate` 已建出 llm_configs 与 assessment_config_members 空表（`db.Find(&[]domain.LLMConfig{})` 返回空切片 nil error）。
  - 验证命令：`cd hr-backend && go test ./internal/model -run TestMigrateSysConfigSeed -v`
  - 预期输出：`ok sili-smart-hr/backend/internal/model`，测试日志含 period=weekly、trigger_time=23:00、secret_cipher="" 断言通过。
  - 附加验证：`cd hr-backend && go vet ./internal/domain/... ./internal/model/...` 无报错。

  **specs 依据：**
  - 章节范围：§4.1.2（涉及字段：周期长度/触发时点/评估对象）、§4.2.2C（集成密钥卡片字段）、§4.3.2（新增/编辑模型弹窗字段）、§6.1（数据状态：集成密钥未配置态）、§7（密钥存储安全要求：加密存储禁止明文落库）、§8.1（术语：模型主键 ID 与模型 ID 区分）
  - 业务规则索引：
    - [BR1] §7 密钥存储安全：API Key 与集成密钥数据库加密存储禁止明文落库（对应密文列 json:"-"）
    - [BR2] §6.1 + 04 §4：集成密钥未配置为首启默认态，首启 seed 空密钥行
    - [BR3] §8.1 术语区分：llm_configs.id（雪花 string 化）与 model_id（文本）各自独立字段

  **依赖：** 无

  **提交信息：** feat(SYS_001): 新增系统配置域四表 domain 模型与首启单例 seed（prefactor）

- [x] **T2: config 新增 LLM_SECRET_KEY 与外部 API 地址配置 + 生产熔断**

  **文件：**
  - 修改：`hr-backend/internal/config/config.go`（Config 根结构 + Load 的 BindEnv + applyDefaults + 辅助函数）
  - 修改：`hr-backend/cmd/server/main.go`（启动熔断，接 JWT_SECRET 熔断之后）
  - 修改：`hr-backend/configs/config.yaml`（追加 llm、integration 两段）
  - 测试：`hr-backend/internal/config/config_test.go`（扩展，由实现者按 TDD 指导）

  **契约：**

  config.go 新增结构（避免与 domain.LLMConfig 同名，用 Settings 后缀）：
  ```go
  type LLMSettings struct {
      SecretKey string `mapstructure:"secret_key"`
  }
  type IntegrationSettings struct {
      SmartAPIBaseURL string `mapstructure:"smart_api_base_url"`
  }
  ```
  Config 根结构追加字段 `LLM LLMSettings \`mapstructure:"llm"\`` 与 `Integration IntegrationSettings \`mapstructure:"integration"\``。

  Load 函数追加两条 BindEnv：
  ```go
  v.BindEnv("llm.secret_key", "LLM_SECRET_KEY")
  v.BindEnv("integration.smart_api_base_url", "SMART_API_BASE_URL")
  ```

  新增常量与辅助函数：
  ```go
  const defaultLLMSecretKey = "sili-smart-hr-dev-llm-secret-3b7f1e9d4a2c8f60"
  func (c *Config) IsDefaultLLMSecretKey() bool { return c.LLM.SecretKey == defaultLLMSecretKey }
  ```

  applyDefaults 追加：
  ```go
  if c.LLM.SecretKey == "" { c.LLM.SecretKey = defaultLLMSecretKey }
  if c.Integration.SmartAPIBaseURL == "" { c.Integration.SmartAPIBaseURL = "http://localhost:8081" }
  ```

  main.go 启动序列追加（紧跟现有 JWT_SECRET 熔断块之后）：
  ```go
  if !a.Config.IsSQLite() && a.Config.IsDefaultLLMSecretKey() {
      slog.Error("refusing to start: non-sqlite database requires LLM_SECRET_KEY env override; current key is the public default")
      os.Exit(1)
  }
  ```

  configs/config.yaml 追加：
  ```yaml
  llm:
    secret_key: "sili-smart-hr-dev-llm-secret-3b7f1e9d4a2c8f60"
  integration:
    smart_api_base_url: "http://localhost:8081"
  ```

  关键逻辑：
  - LLM_SECRET_KEY 复刻 JWT_SECRET 的"开发默认值双重公开 + 生产熔断"范式（config.go 常量与 config.yaml 同值公开，生产非 SQLite 未覆盖即拒绝启动）。AES 对称密钥由 crypto.DeriveKey 从该字符串派生（见 T3）。
  - SMART_API_BASE_URL 为实现侧补充配置：specs §7.1 把 sili-smart-api 列为外部集成系统，03/04 未提供其 base URL 的配置落点，但 A3 人员列表查询（子计划 02）与 C4 连通验证（子计划 04）需真实调用该外部系统，必须有可配置地址。开发默认值指向 localhost:8081（sili-smart-api 本地端口），生产由 SMART_API_BASE_URL 环境变量覆盖。此配置不熔断（外部系统地址允许默认值上线，调用失败由 1305/1304 业务错误承接）。
  - IsSQLite/IsDefaultLLMSecretKey 辅助函数仿现有 IsSQLite/IsDefaultJWTSecret 写法。

  **验收锚点：**
  - 核心断言（config_test.go 黑盒 package config_test）：空配置 Load 后 `cfg.LLM.SecretKey == defaultLLMSecretKey`（== "sili-smart-hr-dev-llm-secret-3b7f1e9d4a2c8f60"）、`cfg.Integration.SmartAPIBaseURL == "http://localhost:8081"`；通过 SetEnv 设 LLM_SECRET_KEY="x" 后 Load 得 `cfg.IsDefaultLLMSecretKey() == false`；未设时 `cfg.IsDefaultLLMSecretKey() == true`。
  - 验证命令：`cd hr-backend && go test ./internal/config -run TestLLMSecretDefault -v`
  - 预期输出：`ok sili-smart-hr/backend/internal/config`，断言默认值与 IsDefault 判定通过。
  - 附加验证（SQLite 放行启动）：`cd hr-backend && go build ./cmd/server` 编译通过。

  **specs 依据：**
  - 章节范围：§7（密钥存储安全：对称密钥由环境变量提供，生产强制覆盖）、§7.1（外部系统集成：sili-smart-api 集成方式 API）
  - 业务规则索引：
    - [BR1] §7 + 04 §1.6/§7：LLM_SECRET_KEY 生产强制覆盖，与 JWT_SECRET 同级熔断，开发默认值随源码公开
    - [BR2] §7.1：sili-smart-api 为外部集成系统，A3/C4 调用所需 base URL 可配置（实现侧补充配置落点）

  **依赖：** 无

  **提交信息：** feat(SYS_001): config 新增 LLM_SECRET_KEY 与外部 API 地址配置及生产熔断（prefactor）

- [x] **T3: errcode 追加 config 域 13xx 段错误码**

  **文件：**
  - 修改：`hr-backend/internal/pkg/errcode/errcode.go:5-28`（const 块）与 `:30-51`（messages map）与段位注释
  - 测试：`hr-backend/internal/pkg/errcode/errcode_test.go`（由实现者按 TDD 指导，如不存在则新建）

  **契约：**

  const 块在 `ActivityThresholdInvalid = 1208` 之后、`BadRequest = 1400` 之前追加：
  ```go
  LLMConfigNotFound              = 1301 // 大模型不存在
  LastLLMConfig                  = 1302 // 至少保留一个大模型
  IntegrationSecretNotConfigured = 1303 // 集成密钥未配置
  IntegrationSecretTestFailed    = 1304 // 连通验证失败
  StaffListUnavailable           = 1305 // 人员列表暂不可用（外部用户体系不可达）
  ConfigVersionConflict          = 1306 // 评估周期配置版本冲突（并发更新）
  ```

  messages map 追加六条（键值与 03 §4.1 错误码总表一致）：
  ```go
  LLMConfigNotFound:              "llm config not found",
  LastLLMConfig:                  "last llm config",
  IntegrationSecretNotConfigured: "integration secret not configured",
  IntegrationSecretTestFailed:    "integration secret test failed",
  StaffListUnavailable:           "staff list unavailable",
  ConfigVersionConflict:          "config version conflict",
  ```

  段位注释更新为：`account 1001-1007，系统初始化 1101-1102，dimension 1201-1208，config 1301-1306，通用 1400/1500。百位区分域：0=account，1=system，2=dimension，3=config。`

  关键逻辑：常量名与文案严格对齐 03 §4.1 错误码总表，段位 13xx 避让 11xx/12xx。handler 侧无需改（handleServiceError 已按 code 段通用映射，13xx 落 HTTP 200 带 code）。

  **验收锚点：**
  - 核心断言（errcode_test.go）：`errcode.Message(errcode.LLMConfigNotFound) == "llm config not found"`、`errcode.Message(errcode.ConfigVersionConflict) == "config version conflict"`、六个新码值连续 1301-1306 且 `errcode.Message(1307) == "error"`（未注册回退）。
  - 验证命令：`cd hr-backend && go test ./internal/pkg/errcode -v`
  - 预期输出：`ok sili-smart-hr/backend/internal/pkg/errcode`，六条 Message 断言通过。

  **specs 依据：**
  - 章节范围：03 §4.1（错误码总表 config 域 13xx 段）、03 §4.3（错误响应格式）、§4.1.4 规则5（操作异常反馈）、§4.2.4 规则6（操作异常反馈）
  - 业务规则索引：
    - [BR1] 03 §4.1：六个错误码常量、含义、触发场景、HTTP 状态（均 200）与总表严格一致
    - [BR2] §4.1.4 规则5 + §4.2.4 规则6：并发冲突 1306、参数错误 1400、网络异常 1500 的错误反馈分类

  **依赖：** 无

  **提交信息：** feat(SYS_001): errcode 追加 config 域 13xx 段错误码（prefactor）

- [x] **T4: crypto 包 AES-256-GCM 加解密 + 密钥派生 + 掩码**

  **文件：**
  - 创建：`hr-backend/internal/pkg/crypto/aesgcm.go`
  - 测试：`hr-backend/internal/pkg/crypto/aesgcm_test.go`（由实现者按 TDD 指导）

  **契约：**

  ```go
  package crypto

  // DeriveKey 把任意长度密钥字符串派生为 AES-256 的 32 字节 key（SHA-256）。
  func DeriveKey(secret string) []byte

  // Encrypt 用 AES-256-GCM 加密明文，返回 base64 编码的 "nonce:ciphertext"。
  // nonce 每次随机生成 12 字节，与密文一起 base64 编码。
  func Encrypt(key []byte, plaintext string) (string, error)

  // Decrypt 解密 Encrypt 产出的 base64 串，返回明文。格式不符或校验失败返 error。
  func Decrypt(key []byte, ciphertextB64 string) (string, error)

  // Mask 生成密钥掩码快照：保留前 4 位与末 4 位，中段以星号替代；
  // 明文长度 < 8 时全掩码（全星号，星号数为明文长度）；空串返回空串。
  func Mask(plaintext string) string
  ```

  关键逻辑：
  - Encrypt：`aes.NewCipher(key)` → `cipher.NewGCM(block)` → `gcm.Seal(nonce, nonce, []byte(plaintext), nil)`，nonce 用 `crypto/rand` 生成 12 字节（gcm.NonceSize()）。输出 `base64.StdEncoding.EncodeToString(append(nonce, ciphertext...))`，与 04 §1.6 约定的 `nonce:ciphertext` 格式一致（实现可选纯拼接或冒号分隔，Decrypt 须对称；推荐 base64(nonce+ciphertext) 单段，Decrypt 按 NonceSize 切分）。
  - Decrypt：base64 解码 → 前 NonceSize 字节为 nonce、其余为 ciphertext → `gcm.Open(nil, nonce, ciphertext, nil)`，校验失败（密文被篡改/ key 不符）返 error。
  - DeriveKey：`sha256.Sum256([]byte(secret))` 返回 32 字节，满足 AES-256。调用方从 config.LLM.SecretKey 取字符串传入。
  - Mask：对明文长度 len≥8 时返回 `plaintext[:4] + strings.Repeat("*", len-8) + plaintext[len-4:]`；len<8 且 >0 时返回 `strings.Repeat("*", len)`；len==0 返回 ""。掩码规则对齐 03 §2.5 与 04 §1.6"前 4 末 4 保留，中段星号，<8 全掩码"。

  **验收锚点：**
  - 核心断言（aesgcm_test.go 黑盒 package crypto_test）：
    - `Encrypt` 后 `Decrypt` 还原：`pt := "sk-1a2b3c4d5e6f7890"; ct, _ := crypto.Encrypt(key, pt); got, _ := crypto.Decrypt(key, ct); got == pt`（key = crypto.DeriveKey("any-secret")）
    - 同一明文两次 Encrypt 产生不同密文（nonce 随机）：`ct1 != ct2`
    - 错误 key 解密失败：用 key2 解密 key1 加密的密文返非 nil error
    - Mask 断言：`Mask("sk-1a2b3c4d5e6f7890ab12") == "sk-1**************ab12"`（前4末4保留，中段星号数为 len-8）；`Mask("short") == "*****"`（5 字符全掩码）；`Mask("") == ""`
  - 验证命令：`cd hr-backend && go test ./internal/pkg/crypto -v`
  - 预期输出：`ok sili-smart-hr/backend/internal/pkg/crypto`，加解密往返、nonce 随机性、掩码三类断言通过。

  **specs 依据：**
  - 章节范围：§7（密钥存储安全）、§4.2.4 规则4（密钥掩码化）、03 §2.5（密钥传输与存储安全：AES-256-GCM 加密落库、掩码规则）、04 §1.6（密钥存储安全：nonce:ciphertext 格式、掩码快照独立列）
  - 业务规则索引：
    - [BR1] §7 + 04 §1.6：API Key 与集成密钥 AES-256-GCM 对称加密存储，密文与 nonce 写 TEXT 列，明文禁止落库
    - [BR2] §4.2.4 规则4 + 03 §2.5：掩码保留前 4 末 4、中段星号、<8 全掩码，掩码快照独立列存

  **依赖：** T2（DeriveKey 接受 config.LLM.SecretKey 派生，但 crypto 包本身只接 []byte，解耦；测试自备 key）

  **提交信息：** feat(SYS_001): 新增 crypto 包 AES-256-GCM 加解密与掩码工具（prefactor）

- [x] **T5: rsakey 升 3072 扩大传输层明文上限**

  **文件：**
  - 修改：`hr-backend/internal/pkg/rsakey/rsakey.go`（Generate 内 rsa.GenerateKey 的 bits 参数）
  - 测试：`hr-backend/internal/pkg/rsakey/rsakey_test.go`（由实现者按 TDD 指导）

  **契约：**

  rsakey.go 的 Generate 方法（或 NewManager 内部生成密钥对处）把 `rsa.GenerateKey(rand.Reader, 2048)` 改为 `rsa.GenerateKey(rand.Reader, 3072)`。其余逻辑（公钥 PEM 输出、私钥存 Redis keyId 关联、TTL 5 分钟、Decrypt 的 GETDEL 一次性取出）不变。

  新增一个导出常量或注释标注当前位长：
  ```go
  const rsaKeyBits = 3072
  ```
  Generate 内引用该常量。

  关键逻辑：
  - RSA-3072 的 RSA-OAEP-SHA256 单块明文上限约 318 字节（公式：keySizeBytes - 2*hashSize - 2 = 384 - 64 - 2 = 318），覆盖 specs 限定的 API Key 与集成密钥明文 ≤200 字符（03 §2.5 指出 RSA-2048 单块上限 190 字节不足以覆盖 200 字符，故升级）。
  - 前端 node-forge 的 `pub.encrypt(plaintext, 'RSA-OAEP', {md, mgf1})` 不感知服务端位长，自动按 PEM 内公钥的 modulus 加密，无需前端配合改动。
  - account 域密码加密同步受益（密码明文远小于上限，无回归风险）。keyId 一次性 GETDEL 机制保证升级后无历史兼容问题（旧 keyId 的私钥 5 分钟 TTL 自然过期消失）。

  **验收锚点：**
  - 核心断言（rsakey_test.go）：生成密钥对后，用公钥加密一段 200 字符明文，Manager.Decrypt 能还原（明文用 strings.Repeat("a", 200)）；密文长度对应 3072 位（384 字节，base64 后约 512 字符）。若测试用 Redis，用 miniredis 或 ExistingManager 的内存替身；若 Manager 强依赖 *redis.Client，测试改为断言 Generate 返回的 publicKeyPEM 解析出 `*(rsa.PublicKey)` 的 N.BitLen()==3072。
  - 验证命令：`cd hr-backend && go test ./internal/pkg/rsakey -run TestRSA3072 -v`
  - 预期输出：`ok sili-smart-hr/backend/internal/pkg/rsakey`，位长 3072 或 200 字符加解密往返断言通过。
  - 附加验证：`cd hr-backend && go test ./internal/service -run TestLogin` 确认 account 登录链路（RSA 解密密码）无回归。

  **specs 依据：**
  - 章节范围：§7（密钥传输安全）、03 §2.5（传输层 RSA-OAEP 加密，明文 ≤200 字符超出 RSA-2048 单块上限，方案留开发计划定）、03 §5.1（传输安全）
  - 业务规则索引：
    - [BR1] 03 §2.5：密钥类字段明文 ≤200 字符，RSA-OAEP 加密传输须覆盖该长度，2048 位不足故升 3072

  **依赖：** 无（rsakey 包独立，account 域已有测试兜底回归）

  **提交信息：** feat(SYS_001): rsakey 密钥位长升 3072 覆盖密钥明文传输上限（prefactor）

- [x] **T6: 前端 Vitest 测试基建搭建**

  **文件：**
  - 创建：`hr-frontend/vitest.config.ts`
  - 创建：`hr-frontend/src/test/setup.ts`
  - 创建：`hr-frontend/src/test/example.test.tsx`
  - 修改：`hr-frontend/package.json`（devDependencies 追加测试依赖，scripts 追加 test/test:ui）
  - 修改：`hr-frontend/tsconfig.json`（如需，types 纳入 vitest/globals 与 jest-dom）

  **契约：**

  vitest.config.ts：
  ```ts
  import { defineConfig } from 'vitest/config';
  import { fileURLToPath } from 'node:url';
  import path from 'node:path';

  export default defineConfig({
    resolve: {
      alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
    },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      css: false,
    },
  });
  ```
  resolve.alias 须与 rsbuild.config.ts / tsconfig.json 的 `@/* → ./src/*` 一致。

  src/test/setup.ts：
  ```ts
  import '@testing-library/jest-dom/vitest';
  import { afterEach } from 'vitest';
  import { cleanup } from '@testing-library/react';
  afterEach(() => { cleanup(); });
  ```

  package.json scripts 追加：
  ```json
  "test": "vitest run",
  "test:watch": "vitest"
  ```
  devDependencies 追加：`vitest`、`@testing-library/react`、`@testing-library/jest-dom`、`jsdom`、`@testing-library/user-event`（版本由 pnpm 解析最新稳定）。

  src/test/example.test.tsx（基建自检，验证渲染与断言可用）：
  ```tsx
  import { describe, it, expect } from 'vitest';
  import { render, screen } from '@testing-library/react';
  import { Button } from '@/components/ui/button';

  describe('vitest 基建自检', () => {
    it('渲染 Button 并断言文本', () => {
      render(<Button>确认</Button>);
      expect(screen.getByRole('button', { name: '确认' })).toBeInTheDocument();
    });
  });
  ```

  关键逻辑：
  - Vitest 走 jsdom 环境（DOM 组件测试需要），globals:true 让 describe/it/expect 全局可用（与 tsconfig 的 types 配合）。
  - setup.ts 引入 jest-dom 的 vitest 版匹配器（toBeInTheDocument 等），并在每个 test 后 cleanup 防止组件状态泄漏。
  - 本任务只搭基建与一个自检测试，业务组件测试由各业务子计划按 TDD 编写。
  - 别名 `@` 必须与现有 rsbuild/tsconfig 一致，否则 example.test.tsx 的 `@/components/ui/button` import 解析失败。

  **验收锚点：**
  - 核心断言：`pnpm test` 执行后 example.test.tsx 的「渲染 Button 并断言文本」通过（screen.getByRole 能定位按钮、toBeInTheDocument 匹配器可用）。
  - 验证命令：`cd hr-frontend && pnpm install && pnpm test`
  - 预期输出：`✓ src/test/example.test.tsx (1)` 与 `Test Files 1 passed (1)`，退出码 0。
  - 附加验证：`cd hr-frontend && pnpm type-check` 仍通过（tsconfig 调整不破坏现有类型检查）。

  **specs 依据：**
  - 章节范围：无（纯前端测试基建搭建，不对应 specs 业务章节）
  - 业务规则索引：无。理由：本任务是为本 feature 前端任务引入 TDD 的基建搭建（用户决策：前端引入 Vitest 走 TDD），属于技术组件初始化，specs 不承载测试框架选型。

  **依赖：** 无

  **提交信息：** feat(SYS_001): 前端搭建 Vitest 测试基建（prefactor）

- [x] **T7: contracts.ts 追加 13xx ErrCode 与三业务域 DTO 类型**

  **文件：**
  - 修改：`hr-frontend/src/lib/contracts.ts`（ErrCode 对象追加六条、追加三组域类型）
  - 测试：`hr-frontend/src/lib/contracts.test.ts`（由实现者按 TDD 指导）

  **契约：**

  ErrCode 对象追加（紧跟现有 Dimension 段之后、BadRequest 之前）：
  ```ts
  LLMConfigNotFound: 1301,
  LastLLMConfig: 1302,
  IntegrationSecretNotConfigured: 1303,
  IntegrationSecretTestFailed: 1304,
  StaffListUnavailable: 1305,
  ConfigVersionConflict: 1306,
  ```

  评估周期配置域类型：
  ```ts
  export interface AssessmentConfig {
    id: string;
    period: 'daily' | 'weekly' | 'monthly';
    trigger_time: string;          // HH:mm
    target_mode: 'all' | 'specified';
    specified_members: StaffItem[];
    version: number;
  }
  export interface StaffItem { staff_id: string; staff_name: string; }
  export type StaffListPage = Page<StaffItem>;
  export interface SaveAssessmentPayload {
    period: string; trigger_time: string; target_mode: string;
    specified_members: StaffItem[]; version: number;
  }
  export interface AssessmentMutationResult { id: string; version: number; }
  ```

  大模型配置域类型：
  ```ts
  export interface LLMConfigItem {
    id: string;
    name: string;
    provider: 'deepseek' | 'openai' | 'zhipu' | 'anthropic';
    model_id: string;
    api_url: string;
    api_key_masked: string;
    enabled: boolean;
    created_at: string; updated_at: string;
  }
  export interface LLMConfigDetail extends Omit<LLMConfigItem, 'api_key_masked'> {
    api_key: string;   // 明文，仅查看弹窗
  }
  export interface CreateLLMPayload {
    name: string; provider: string; model_id: string;
    api_url?: string; api_key: string;   // api_key 为 RSA 加密 base64 密文 + 独立 keyId
    keyId: string;
  }
  export interface UpdateLLMPayload {
    id: string; name: string; provider: string; model_id: string;
    api_url?: string; api_key?: string;  // 留空 undefined 表示不改
    keyId?: string;
  }
  export interface LLMDeleteResult { id: string; transferred_enabled_id: string | null; }
  export interface LLMEnableResult { id: string; enabled: boolean; }
  ```

  集成密钥域类型：
  ```ts
  export interface IntegrationSecretView { id: string; secret_masked: string; configured: boolean; }
  export interface IntegrationSecretDetail { id: string; secret: string; }   // 明文
  export interface UpdateSecretPayload { secret: string; keyId: string; }     // secret 为 RSA 加密密文
  export interface SecretTestResult { connected: boolean; }
  ```

  关键逻辑：
  - 所有承载雪花 ID 的字段（id、assessment_config_id 如有）类型为 string（specs §2.3、§8.1，account 链路样板）。后端 snake_case 字段名（trigger_time、target_mode、api_key_masked、specified_members、model_id）原样保留到 TS interface，不转 camelCase（项目约定，见 contracts.ts 现有 last_login_at）。
  - provider/period/target_mode 用字面量联合类型约束枚举，便于前端表单 Zod schema 复用。
  - CreateLLMPayload.api_key 与 UpdateSecretPayload.secret 是 RSA-OAEP 加密后的 base64 密文，keyId 独立传递（复用 /api/auth/public-key 机制，每次加密单字段拉一次新公钥）。
  - 本任务只定义类型与常量，不含请求函数（请求函数在各业务域 feature 的 api.ts 实现，见子计划 02/03/04）。

  **验收锚点：**
  - 核心断言（contracts.test.ts）：`ErrCode.LLMConfigNotFound === 1301`、`ErrCode.ConfigVersionConflict === 1306`；类型层面由 type-check 保证（`as AssessmentConfig` 的字段名拼写正确）。
  - 验证命令：`cd hr-frontend && pnpm type-check`
  - 预期输出：tsc --noEmit 无错误，退出码 0。
  - 附加验证：`cd hr-frontend && pnpm test -- src/lib/contracts.test.ts` 通过。

  **specs 依据：**
  - 章节范围：§2.3（接口鉴权矩阵：雪花 ID string 化）、§8.1（术语：模型主键 ID 与模型 ID 区分）、03 §2.4（雪花 ID 传输约束）、03 A/B/C 全部接口的响应字段表与请求参数表
  - 业务规则索引：
    - [BR1] §2.3 + §8.1 + 03 §2.4：承载雪花 ID 的字段前端类型用 string，model_id 为文本非雪花
    - [BR2] 03 §3 各接口字段：DTO 字段名与后端响应/请求体逐一对齐（api_key_masked、specified_members、configured、transferred_enabled_id 等）

  **依赖：** T6（contracts.test.ts 依赖 Vitest 基建）

  **提交信息：** feat(SYS_001): contracts 追加 config 域错误码与三业务域 DTO 类型（prefactor）
