# 账号台账API 实现计划

> **目标：** 在 account 后端链路上补齐用户管理 6 个接口（列表/新增/编辑/删除/启停/重置密码），落地账号唯一性、至少保留一个启用账号、密码格式校验、软删除等业务规则。
> **架构：** domain.Account 加 DeletedAt 软删除字段并把 username 降为普通索引；repository 扩展列表/增改/软删除/启停/启用计数；service 扩展 6 个方法承载业务规则并复用子计划1 的 PasswordDecryptor 解密密码；handler 加 6 个接口；router 注册到受保护组。
> **技术栈：** Go + GORM（软删除自动过滤）+ bcrypt + 既有 errcode 体系。

---

## 文件结构

### 新建文件
- `hr-backend/internal/repository/account_test.go` — repository 集成测试（:memory: SQLite + AutoMigrate Account）

### 修改文件
- `hr-backend/internal/domain/account.go` — Username 去 uniqueIndex 改普通 index；新增 DeletedAt gorm.DeletedAt 字段
- `hr-backend/internal/pkg/errcode/errcode.go` — 新增 AccountNotFound(1004)/UsernameExists(1005)/LastEnabledAccount(1006)/PasswordInvalid(1007) 与文案
- `hr-backend/internal/repository/account.go` — 接口扩展 ListAccounts/Create/Update/Delete/UpdateEnabled/CountEnabled 及实现
- `hr-backend/internal/service/account.go` — AccountListItemDTO；AccountService 扩展 ListAccounts/CreateAccount/UpdateAccount/DeleteAccount/ToggleEnabled/ResetPassword；密码与账号格式校验；业务规则
- `hr-backend/internal/service/account_test.go` — fakeRepo 扩展新方法；新增用户管理方法测试
- `hr-backend/internal/api/handler/account.go` — 6 个 handler 方法、请求结构、雪花 ID string→int64 解析；handler 测试
- `hr-backend/internal/api/router/router.go` — auth 组注册 6 个受保护路由
- `hr-backend/wire_gen.go` — `go generate ./...` 再生（router/handler 构造签名未变则无实质变化，仍需确认）

---

## 数据模型来源

| 来源 | 文件路径 | 状态 |
|------|----------|------|
| 04_model_interface.md | `/context/05_specs/P1_ACC_001_FEAT_登录与用户管理/04_model_interface.md` | ✅ 存在 |

**引用说明：** T1 的模型变更引用 04_model_interface.md → accounts 表「模型定义」与「username 唯一性策略说明」。本项目用 GORM AutoMigrate + 开发态删库重建承载变更，不写 SQL 迁移文件（04_model「建表方式」明确）。

---

## 任务清单

- [x] **T1: 数据库模型变更（Account 软删除 + username 普通索引）**

  **文件：**
  - 引用：`/context/05_specs/P1_ACC_001_FEAT_登录与用户管理/04_model_interface.md` → accounts 模型定义（DeletedAt + username 普通 index）
  - 修改：`hr-backend/internal/domain/account.go`

  **步骤1：写入迁移。** 本项目不使用 SQL 迁移文件，按 04_model_interface.md 的目标模型修改 domain/account.go：
  - import 补 `"gorm.io/gorm"`
  - Username tag 从 `gorm:"type:varchar(64);uniqueIndex;not null"` 改为 `gorm:"type:varchar(64);index;not null"`（去 uniqueIndex 改普通 index）
  - 新增字段 `DeletedAt gorm.DeletedAt \`gorm:"index" json:"-"\``（与 CreatedAt/UpdatedAt 同块）
  - 其余字段（ID/PasswordHash/Name/Enabled/LastLoginAt/CreatedAt/UpdatedAt）与 04_model 目标模型一致，不改。

  **步骤2：执行迁移。** 开发态删 `hr-backend/data/sili-smart-hr.db` 后重启，AutoMigrate 按目标模型重建 accounts 表（普通索引 idx_accounts_username、idx_accounts_deleted_at），首启重新播种 admin / hzwlsoft.com。PG/MySQL 生产同样按目标模型建表，无存量数据需照顾。

  **步骤3：验证变更结果。**
  - 验证命令：
    - `cd hr-backend && rm -f data/sili-smart-hr.db && go run ./cmd/server`（启动后看到 "database ready" + "initial account seeded" 即 Ctrl-C）
    - `sqlite3 data/sili-smart-hr.db "PRAGMA table_info(accounts);"`（确认含 deleted_at 列）
    - `sqlite3 data/sili-smart-hr.db "PRAGMA index_list(accounts);"`（确认 username 索引 unique=0）
  - 预期输出：table_info 含 deleted_at（type DATETIME，notnull 0）；index_list 中 idx_accounts_username 的 unique 列为 0（非唯一）。

  **步骤4：提交。** 迁移变更（domain struct 改动）由控制器在任务自检通过后统一提交，执行者不执行 git add/commit。提交前确认 domain struct 与 04_model 目标模型一致；后续对 accounts 的结构变更应改 struct 后删库重建或走 migrateDB 三段式，不回退本次 tag。

  **specs 依据：**
  - 章节范围：§4.2.4 规则4 软删除、§6.1 数据状态、04_model_interface accounts 表结构与 username 唯一性策略
  - 业务规则索引：
    - [BR1] §4.2.4 规则4：删除为软删除，软删除账号不参与账号唯一性校验，账号名可被重新创建
    - [BR2] 04_model_interface username 唯一性策略：username 只建普通 index，唯一性收敛到业务层查询排除软删除
    - [BR3] 04_model_interface：DeletedAt gorm.DeletedAt 承载软删除，Delete 时自动赋值，查询自动过滤

  **依赖：** 无

  **提交信息：** feat(domain): Account 软删除字段与 username 普通索引

- [x] **T2: errcode 新增 1004-1007**

  **文件：**
  - 修改：`hr-backend/internal/pkg/errcode/errcode.go`

  **契约：**
  - const 块新增：`AccountNotFound = 1004`、`UsernameExists = 1005`、`LastEnabledAccount = 1006`、`PasswordInvalid = 1007`（附中文/英文语义注释，与 03_api_interface 错误码规范对齐）
  - messages map 补：`AccountNotFound: "account not found"`、`UsernameExists: "username exists"`、`LastEnabledAccount: "last enabled account"`、`PasswordInvalid: "password invalid"`
  - import 无变化。

  **验收锚点：**
  - 核心断言（新增 TestMessages）：`errcode.AccountNotFound == 1004`；`errcode.UsernameExists == 1005`；`errcode.LastEnabledAccount == 1006`；`errcode.PasswordInvalid == 1007`；`errcode.Message(errcode.UsernameExists) == "username exists"`；`errcode.Message(errcode.LastEnabledAccount) == "last enabled account"`。
  - 验证命令：`cd hr-backend && go test ./internal/pkg/errcode -run TestMessages -v`
  - 预期输出：PASS。

  **specs 依据：**
  - 章节范围：03_api_interface「错误码规范」、§4.2.4、§4.3.4、§4.4.4、§6.2
  - 业务规则索引：
    - [BR1] 03_api_interface 错误码：1004 AccountNotFound 用户管理目标账号不存在
    - [BR2] 03_api_interface 错误码：1005 UsernameExists 账号已存在（唯一性排除软删除）
    - [BR3] §4.2.4 规则2 / 03 错误码：1006 LastEnabledAccount 至少保留一个启用的账号
    - [BR4] 03 错误码：1007 PasswordInvalid 密码格式不符（少于 8 位或缺少字母/数字）

  **依赖：** 无

  **提交信息：** feat(errcode): 新增账号管理错误码 1004-1007

- [x] **T3: repository 扩展**

  **文件：**
  - 修改：`hr-backend/internal/repository/account.go`
  - 测试：`hr-backend/internal/repository/account_test.go`（由实现者按 TDD 指导创建，:memory: SQLite）

  **契约：**
  - AccountRepository 接口新增方法：
    - `ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]domain.Account, int64, error)`：GORM DeletedAt 自动过滤 deleted_at IS NULL；keyword != "" 时 `.Where("username LIKE ? OR name LIKE ?", "%"+keyword+"%", "%"+keyword+"%")`；分页 `.Offset((page-1)*pageSize).Limit(pageSize)`；`.Order("created_at DESC")`（新建账号在前，specs §4.2.5）；返回 (list, total, err)，total 由同条件的 `.Count()` 得到。
    - `Create(ctx context.Context, acc *domain.Account) error`：`r.db.WithContext(ctx).Create(acc)`，雪花 ID 由全局 Create 回调赋。
    - `Update(ctx context.Context, acc *domain.Account) error`：`r.db.WithContext(ctx).Save(acc)`（整行更新，含 PasswordHash/Name/Enabled）。
    - `Delete(ctx context.Context, id int64) error`：`r.db.WithContext(ctx).Delete(&domain.Account{}, id)`，GORM 软删除自动赋 deleted_at。
    - `UpdateEnabled(ctx context.Context, id int64, enabled bool) error`：`r.db.WithContext(ctx).Model(&domain.Account{}).Where("id = ?", id).Update("enabled", enabled)`。
    - `CountEnabled(ctx context.Context) (int64, error)`：`r.db.WithContext(ctx).Model(&domain.Account{}).Where("enabled = ?", true).Count(&n)`（DeletedAt 自动过滤）。
    - FindByUsername / FindByID / UpdateLastLoginAt 保留（FindByUsername 与 FindByID 因 DeletedAt 存在自动排除软删除记录）。
  - 关键逻辑：唯一性校验复用 FindByUsername（软删除账号因 deleted_at IS NULL 过滤被天然排除，specs §4.2.4 规则4 账号名可复用）；CountEnabled 用于"至少保留一个启用账号"判定。

  **验收锚点：**
  - 核心断言（account_test.go，:memory: SQLite + gorm.Open(sqlite.Open(":memory:")) + AutoMigrate(&domain.Account{})）：
    - `TestListAccounts_Keyword`：插入 {admin,张三} 与 {ops,李四}，ListAccounts(ctx,"adm",1,10) 返回 list 长度 1 且 list[0].Username=="admin"，total==1。
    - `TestListAccounts_Order`：先插 admin 后插 zhangsan，ListAccounts(ctx,"",1,10) 返回 list[0].Username=="zhangsan"（created_at DESC）。
    - `TestCreate_AssignsSnowflakeID`：Create(&Account{Username:"x",PasswordHash:"h",Name:"n",Enabled:true}) 后 acc.ID != 0。
    - `TestDelete_SoftDelete`：Create 后 Delete(id)，再 FindByID(id) 返回 gorm.ErrRecordNotFound；但 `db.Unscoped().Where("id = ?", id).Count` == 1（原始行仍在）。
    - `TestCountEnabled`：插 2 启用 1 停用，CountEnabled(ctx)==2。
  - 验证命令：`cd hr-backend && go test ./internal/repository -v`
  - 预期输出：全部 PASS。注意测试用 `glebarez/sqlite`（与生产同驱动），import `"github.com/glebarez/sqlite"`。

  **specs 依据：**
  - 章节范围：§4.2.2 显示字段、§4.2.4 规则1/2/4、§4.2.5 交互逻辑、04_model_interface accounts 表
  - 业务规则索引：
    - [BR1] §4.2.4 规则1：账号唯一性校验排除软删除（FindByUsername 自动过滤）
    - [BR2] §4.2.4 规则2：CountEnabled 支撑"至少保留一个启用账号"校验
    - [BR3] §4.2.4 规则4：Delete 为软删除，列表与 FindByID 自动过滤已删除
    - [BR4] §4.2.2：列表模糊匹配 username 与 name；无工号列
    - [BR5] §4.2.5：新建账号插入列表顶部（created_at DESC）

  **依赖：** T1（DeletedAt 字段）

  **提交信息：** feat(repository): account 仓储扩展列表/增改/软删除/启停/启用计数

- [x] **T4: service 扩展（用户管理方法与业务规则）**

  **文件：**
  - 修改：`hr-backend/internal/service/account.go`
  - 修改：`hr-backend/internal/service/account_test.go`（fakeRepo 扩展 + 新方法测试）

  **契约：**
  - 新增 DTO：`type AccountListItemDTO struct { ID int64 \`json:"id,string"\`; Username string \`json:"username"\`; Name string \`json:"name"\`; Enabled bool \`json:"enabled"\`; LastLoginAt *time.Time \`json:"last_login_at"\` }`
  - AccountService 接口新增方法：
    - `ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]AccountListItemDTO, int64, error)`
    - `CreateAccount(ctx context.Context, username, name, passwordCipher, keyID string, enabled bool) (*AccountDTO, error)`
    - `UpdateAccount(ctx context.Context, id int64, name, passwordCipher, keyID string, hasPassword bool, enabled bool) (*AccountDTO, error)`（hasPassword 区分是否改密；enabled 由前端总传）
    - `DeleteAccount(ctx context.Context, id int64) error`
    - `ToggleEnabled(ctx context.Context, id int64, enabled bool) error`
    - `ResetPassword(ctx context.Context, id int64, passwordCipher, keyID string) error`
  - 关键逻辑：
    - 两个校验工具（包级私有）：`func validateUsername(s string) bool`（regexp `^[A-Za-z0-9_]{3,30}$`）；`func validatePassword(s string) bool`（len≥8 且含字母且含数字，regexp `[A-Za-z]` 与 `[0-9]`）。import 补 `regexp`。
    - CreateAccount：
      1. !validateUsername(username) → NewError(BadRequest)
      2. utf8.RuneCountInString(name) > 20 → NewError(BadRequest)（用 utf8 计中文长度，import "unicode/utf8"）
      3. plaintext, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)；derr != nil → NewError(BadRequest)（用户管理路径非反枚举，提示重新获取公钥）
      4. !validatePassword(plaintext) → NewError(PasswordInvalid)
      5. repo.FindByUsername(username)；err==nil → NewError(UsernameExists)；err 非 ErrRecordNotFound → 包装返回
      6. hash := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
      7. repo.Create(&domain.Account{Username:username, PasswordHash:string(hash), Name:name, Enabled:enabled})；返回 toDTO（toDTO 需用新创建账号的 ID）
    - UpdateAccount：
      1. acc, err := repo.FindByID(id)；ErrRecordNotFound → NewError(AccountNotFound)
      2. utf8.RuneCountInString(name) > 20 → BadRequest
      3. hasPassword 时：Decrypt + validatePassword + bcrypt，acc.PasswordHash = 新 hash
      4. acc.Enabled==true && enabled==false（降停）：n,_ := repo.CountEnabled(ctx)；n<=1 → NewError(LastEnabledAccount)
      5. acc.Name = name; acc.Enabled = enabled; repo.Update(acc)；返回 toDTO(acc)
    - DeleteAccount：FindByID 存在校验；acc.Enabled && CountEnabled<=1 → LastEnabledAccount；repo.Delete(id)
    - ToggleEnabled：FindByID；enabled==false 且 acc.Enabled==true 且 CountEnabled<=1 → LastEnabledAccount；repo.UpdateEnabled(id, enabled)
    - ResetPassword：FindByID 存在校验；Decrypt + validatePassword + bcrypt；repo.Update(acc)（仅改 PasswordHash，不改 Enabled）
    - ListAccounts：repo.ListAccounts；遍历转 AccountListItemDTO。
  - import 补：`regexp`、`unicode/utf8`、`golang.org/x/crypto/bcrypt`（已有）。
  - 注意：toDTO 当前返回 AccountDTO（ID/Username/Name）。CreateAccount/UpdateAccount 返回 *AccountDTO（不含 enabled），与 03 §2.3/2.4 响应一致（响应含 enabled，可在 handler 层用 gin.H 补 enabled 字段，或扩展 AccountDTO 加 Enabled）。计划约定：扩展 AccountDTO 加 `Enabled bool \`json:"enabled"\``，toDTO 填充，使登录 me 与用户管理响应统一含 enabled（前端 Account 类型也补 enabled，子计划3 处理）。

  **验收锚点：**
  - 核心断言（service_test.go，扩展 fakeRepo 实现 6 个新方法 + 探针）：
    - `TestCreateAccount_Success`：fakeRepo.FindByUsername 返回 ErrRecordNotFound；fakeDecryptor 返回 "Pass1234"；CreateAccount(ctx,"zhangsan","张三",cipher,"kid",true) 返回 dto.Username=="zhangsan" 且 dto.Enabled==true；fakeRepo.created 探针为 true。
    - `TestCreateAccount_UsernameExists`：fakeRepo.FindByUsername 返回已存在账号 → 返回 UsernameExists(1005)。
    - `TestCreateAccount_BadUsername`：username "ab" → BadRequest(1400)。
    - `TestCreateAccount_BadPassword`：fakeDecryptor 返回 "short" → PasswordInvalid(1007)。
    - `TestCreateAccount_DecryptFail`：fakeDecryptor 返回 err → BadRequest(1400)。
    - `TestToggleEnabled_LastEnabled`：fakeRepo.CountEnabled==1 且目标账号 Enabled==true → ToggleEnabled(ctx,id,false) 返回 LastEnabledAccount(1006)。
    - `TestDeleteAccount_LastEnabled`：目标 Enabled==true 且 CountEnabled==1 → 1006。
    - `TestDeleteAccount_DisabledOK`：目标 Enabled==false → DeleteAccount 不拦，fakeRepo.deleted 探针为 true。
    - `TestUpdateAccount_NotFound`：FindByID 返回 ErrRecordNotFound → AccountNotFound(1004)。
    - `TestUpdateAccount_DemoteLastEnabled`：acc.Enabled==true，enabled=false，CountEnabled==1 → 1006。
    - `TestResetPassword_Success`：fakeDecryptor "NewPass123"，ResetPassword 后 fakeRepo.updated 探针为 true，且 acc.Enabled 未变。
  - 验证命令：`cd hr-backend && go test ./internal/service -run "TestCreateAccount|TestToggleEnabled|TestDeleteAccount|TestUpdateAccount|TestResetPassword" -v`
  - 预期输出：全部 PASS。

  **specs 依据：**
  - 章节范围：§4.2.4 规则1/2/3/4、§4.3.2 字段校验、§4.3.4 规则1/2、§4.4.4 规则1、§6.2 状态转换、03_api_interface §2.2-2.7
  - 业务规则索引：
    - [BR1] §4.3.2 字段校验：username 3-30 位字母数字下划线；name ≤20 字符；密码 ≥8 位且同时含字母与数字
    - [BR2] §4.2.4 规则1 / §4.3.4 规则1：账号全局唯一，新增重复返回 1005，唯一性排除软删除
    - [BR3] §4.2.4 规则2 / §4.3.4 规则2 / §6.2：至少保留一个启用账号，停用/删除/编辑降停最后一个启用账号返回 1006
    - [BR4] §4.2.4 规则3：启停即时生效（ToggleEnabled 即时写入，停用账号登录走凭证错误路径由 Login 保证）
    - [BR5] §4.4.4 规则1：重置即时失效，停用账号亦可重置，重置不改变启用状态
    - [BR6] §4.1.4 规则2 / 03 §2.3：用户管理路径密码 RSA 解密后 bcrypt 存储；解密失败/keyId 过期返回 1400（非反枚举范围）

  **依赖：** T1（DeletedAt）、T2（errcode）、T3（repo）、子计划1 T3（PasswordDecryptor 接口已定义并注入 accountService）

  **提交信息：** feat(account): 用户管理 service 方法与业务规则（唯一/保留启用/密码校验/软删除）

- [x] **T5: handler 扩展（6 个接口）**

  **文件：**
  - 修改：`hr-backend/internal/api/handler/account.go`
  - 测试：`hr-backend/internal/api/handler/account_test.go`（由实现者按 TDD 指导创建，httptest + gin + fakeAccountService）

  **契约：**
  - 请求结构（handler 包私有）：
    - createAccountRequest：`{ Username, Name, PasswordCipher, KeyID string; Enabled *bool }`（json tag username/name/passwordCipher/keyId/enabled）
    - updateAccountRequest：`{ ID string \`json:"id,string"\`; Name, PasswordCipher, KeyID string; Enabled bool }`
    - deleteAccountRequest：`{ ID string \`json:"id,string"\` }`
    - toggleEnabledRequest：`{ ID string \`json:"id,string"\`; Enabled bool }`
    - resetPasswordRequest：`{ ID string \`json:"id,string"\`; PasswordCipher, KeyID string }`
    - list 走 query 参数：page/page_size/keyword（c.Query，默认 page=1 page_size=20）
  - handler 方法：
    - `func (h *AccountHandler) List(c *gin.Context)`：解析 page/page_size（strconv.Atoi，错误兜底默认值）；keyword := c.Query("keyword")；list,total,err := svc.ListAccounts；err → handleServiceError；成功 response.OKWithPage(c, list, total, page, pageSize)
    - `func (h *AccountHandler) Create(c *gin.Context)`：绑定 createAccountRequest；enabled := true，若 req.Enabled != nil 则 enabled = *req.Enabled；res,err := svc.CreateAccount(ctx, req.Username, req.Name, req.PasswordCipher, req.KeyID, enabled)；成功 OKWithData(c, res)
    - `func (h *AccountHandler) Update(c *gin.Context)`：绑定 updateAccountRequest；id := parseID(req.ID)；hasPassword := req.PasswordCipher != ""；res,err := svc.UpdateAccount(ctx, id, req.Name, req.PasswordCipher, req.KeyID, hasPassword, req.Enabled)
    - `func (h *AccountHandler) Delete(c *gin.Context)`：绑定 deleteAccountRequest；id := parseID(req.ID)；err := svc.DeleteAccount(ctx, id)；成功 OKWithData(c, gin.H{"id": req.ID})
    - `func (h *AccountHandler) ToggleEnabled(c *gin.Context)`：绑定 toggleEnabledRequest；id := parseID(req.ID)；err := svc.ToggleEnabled(ctx, id, req.Enabled)；成功 OKWithData(c, gin.H{"id": req.ID, "enabled": req.Enabled})
    - `func (h *AccountHandler) ResetPassword(c *gin.Context)`：绑定 resetPasswordRequest；id := parseID(req.ID)；err := svc.ResetPassword(ctx, id, req.PasswordCipher, req.KeyID)；成功 OKWithData(c, gin.H{"id": req.ID})
  - 工具：`func parseID(s string) int64`（strconv.ParseInt(s,10,64)，错误由调用方按需处理；可返回 0 + err，调用方判错 → response.Fail(BadRequest)）。雪花 ID 前端传 string，handler 转 int64 传 service（AGENTS_DATABASE_API_RULE §1.2）。
  - 错误映射复用既有 handleServiceError（1004-1007/1400 走 HTTP 200 带 code，与 httpStatusFor 一致）。
  - import 补：`strconv`。

  **验收锚点：**
  - 核心断言（account_test.go，定义 fakeAccountService 实现 service.AccountService 全部方法 + 编译期断言）：
    - `TestHandler_Create_Success`：POST /api/accounts/create body `{username:"x",name:"y",passwordCipher:"c",keyId:"k"}`，fakeAccountService.CreateAccount 返回 {ID:12,Username:"x",Name:"y",Enabled:true}；响应 code==0，data.username=="x"，data.enabled==true。
    - `TestHandler_Create_UsernameExists`：fake 返回 service.NewError(errcode.UsernameExists)；响应 code==1005，HTTP 200。
    - `TestHandler_List`：GET /api/accounts?page=1&page_size=20，fake.ListAccounts 返回 (list,1)；响应 code==0，data.list 长度 1，data.total==1，data.page==1，data.page_size==20。
    - `TestHandler_ToggleEnabled_LastEnabled`：fake 返回 LastEnabledAccount；响应 code==1006。
    - `TestHandler_Delete`：fake.DeleteAccount 返回 nil；响应 code==0，data.id 与请求 id 一致。
    - `TestHandler_ResetPassword_BadPassword`：fake 返回 PasswordInvalid；响应 code==1007。
  - 验证命令：`cd hr-backend && go test ./internal/api/handler -v`
  - 预期输出：全部 PASS。

  **specs 依据：**
  - 章节范围：§4.2.2 显示字段、§4.2.3 功能与按钮、§4.3.3 保存、§4.4.3 确认重置、03_api_interface §2.2-2.7
  - 业务规则索引：
    - [BR1] §4.2.3 查询：keyword 模糊匹配，分页 page/page_size
    - [BR2] §4.2.3 新增/编辑/删除/启停/重置：6 个接口路径与请求体字段
    - [BR3] 03_api_interface ID 类型约定：id 字段以 JSON string 传输，handler ParseInt 转 int64
    - [BR4] §4.2.2：列表响应不含任何密码字段（AccountListItemDTO 无 PasswordHash）

  **依赖：** T4（service 方法）

  **提交信息：** feat(handler): 账号管理 6 个接口 handler

- [x] **T6: router 注册 + wire 验证**

  **文件：**
  - 修改：`hr-backend/internal/api/router/router.go`
  - 修改：`hr-backend/wire_gen.go`（`go generate ./...` 再生确认）

  **契约：**
  - router.go：auth 组（`auth := r.Group("/api", middleware.JWT(jwtMgr))`）追加：
    - `auth.GET("/accounts", accountHandler.List)`
    - `auth.POST("/accounts/create", accountHandler.Create)`
    - `auth.POST("/accounts/update", accountHandler.Update)`
    - `auth.POST("/accounts/delete", accountHandler.Delete)`
    - `auth.POST("/accounts/toggle-enabled", accountHandler.ToggleEnabled)`
    - `auth.POST("/accounts/reset-password", accountHandler.ResetPassword)`
  - NewRouter 形参不变（accountHandler 已含新方法，子计划1 已加 rdb 参数）；如子计划1 未改 NewRouter 签名，则本任务也不改（rdb 仅公钥限流用）。
  - wire：NewAccountHandler/NewAccountService/NewRouter 签名若未变，wire_gen.go 无实质变化；仍跑 `go generate ./...` 确认。

  **验收锚点：**
  - 核心断言（装配类，无独立单元测试）：编译通过；6 个接口在 JWT 组下可访问，缺 token 返回 401 + 1003。
  - 验证命令：
    - `cd hr-backend && go generate ./... && go build ./cmd/server && go vet ./...`
    - 起后端，先 curl 登录拿 token：`curl -s ... /api/login`（用子计划1 的密文登录，或开发态直接用 sqlite 查 token 不现实，改为：登录页登录拿 token，或写一个临时脚本用公钥加密密码）。简化：本任务接口连通性靠子计划3 前端联调时验证，本任务验证编译 + 路由注册（启动日志无路由冲突）。
    - 无 token 调接口：`curl -s http://127.0.0.1:8080/api/accounts` 返回 401 + code 1003。
  - 预期输出：编译通过；未授权访问返回 401/1003。

  **specs 依据：**
  - 章节范围：§4.2.3 功能与按钮（含后台自动流程列表加载）、§4.3.3、§4.4.3、03_api_interface「接口清单总览」
  - 业务规则索引：
    - [BR1] 03_api_interface 接口清单：6 个受保护接口挂 JWT 组，路径以 /create /update /delete /toggle-enabled /reset-password 后缀区分动作
    - [BR2] §4.2.3 后台自动流程：页面加载与查询条件变更时前端自动拉取分页数据（GET /accounts）

  **依赖：** T5

  **提交信息：** feat(router): 注册账号管理 6 个受保护路由
