# 抽取规则分层 接口设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_TECH_004_TECH_抽取规则分层 |
| 模块代号 | TECH（技术组件，engine/extractor 子域结构重构） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-08-28 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[04_model_interface.md](04_model_interface.md)、基线 [P2_TECH_003 03_api_interface.md](../P2_TECH_003_TECH_会话特征抽取/03_api_interface.md) |

---

## 1. 设计范围声明

本组件是后端进程内 Go 技术组件，与基线一致**无本系统对外 HTTP 接口**。本文档是基线 03 文档的**变更版**：只承载本 Feature 引入的契约增量与语义变更，未提及的契约（Asynq 任务契约、组件方法契约、仓储契约、参数读取契约、错误通道边界）全部沿用基线文档，此处不重复。

增量契约清单：

| 契约类型 | 变更性质 | 承载方式 |
|---------|---------|---------|
| 客户端规则注册表 | 新增 | rules 包 ClientRules 接口 + Register/AllClientRules + 客户端标识常量 |
| 客户端签名探测 | 新增 | rules.DetectClient 包级纯函数 |
| 前缀黑名单生效集组装 | 语义变更 | 出厂集从平面 factory 改为客户端并集组装器；参数键 extractor.inject_prefixes 改追加语义 |
| client 列落库 | 新增 | domain.SessionFeature 新增 Client 字段（表结构见 [04_model_interface.md](04_model_interface.md) §3.1） |
| 日志与观测 | 增量 | 日志字段追加 client；新增 unknown_client_ratio 观测口径 |

---

## 2. 模块信息

**代码落位：** `hr-backend/internal/engine/extractor/`（specs §2.4 能力8 目标结构）：

```
extractor/
├── rules/                  # 新增子包：分层规则与注册表
│   ├── registry.go         # Register/AllClientRules + 客户端标识常量（七值单源）
│   ├── generic.go          # 通用层前缀与共享形态常量（role 兜底、打断等判定逻辑
│   │                       # 留 classify.go，归属以注释标注）
│   ├── claudecode.go       # Claude Code：前缀集与探测特征（通道归属以 classify.go 注释标注）
│   ├── opencode.go         # OpenCode：环境头、播种形态的前缀与探测特征
│   ├── workbuddy.go        # WorkBuddy：user_query、team/user-context 的前缀与探测特征
│   ├── omo.go              # OMO 类插件：工具转写、SYSTEM DIRECTIVE 的前缀与探测特征
│   └── automation.go       # 自动化客户端：碎片家族、# System、count 的前缀与探测特征
├── detect.go               # DetectClient 计分裁决（specs 目标结构落 extractor 包根）
├── classify.go             # 判定链编排（变薄，classifyMsg 签名与 classifyResult 不变）
└── config.go               # 常量与追加参数读取（InjectPrefixes 改并集组装器，见 §5）
```

规则文件只承载 ClientRules 四方法（前缀集 + 探测特征），判定链与内容语义通道的实现留 classify.go，通道归属以注释标注（specs §2.4 能力1 第 3 条）。

**依赖方向：** `extractor → extractor/rules` 单向。rules 包不 import extractor 包（防环），ClientRules 接口与七值常量定义在 rules 包内自足；extractor.detect.go 与落库路径引用 `rules.ClientXxx` 常量。

**客户端标识常量单源（七值，specs §2.3 值域）：** 定义在 rules/registry.go，各客户端规则的 `Client()` 方法返回本包常量，探测的 mixed/unknown 同源；domain 层 client 列是纯 string，Go 侧常量就地表达到 rules 包为止，domain 不引入 import。

**Wire 装配：** 无新增 provider、不改 wire.go。注册表经 rules 包 init 自挂载，装配层无感（specs §4.2）。

---

## 3. 新增契约：客户端规则注册表

落位 `extractor/rules/registry.go`（specs §2.2 配置项说明的忠实落地）：

```go
// ClientRules 是客户端规则注册契约：每个客户端一个实现文件，registry 集中注册。
type ClientRules interface {
    Client() string                          // 客户端标识（七值常量之一的稳定字符串）
    UserPrefixes() []string                  // 该客户端贡献的 user 侧注入前缀集
    NonUserPrefixes() []string               // 非 user 侧收窄前缀集（空集表示无 assistant 侧规则）
    DetectFeatures() []DetectFeature         // 签名特征（探测计分用）
}

// DetectFeature 是客户端签名的一条特征锚点。
type DetectFeature struct {
    Kind    string // prefix（消息前缀）/ marker（子串存在性）
    Pattern string // 字面量
    Weight  int    // 命中计分权重 ≥1；专属强特征 2-3、共享形态 1（specs §2.4 能力7）
    Once    bool   // true 全会话命中一次即计（前缀类）；false 逐消息累计
}

// Register init 时注册，判定链执行期只读。新客户端接入 = rules/<client>.go + 一行注册。
func Register(r ClientRules)

// AllClientRules 返回全部已注册规则（注册顺序按文件名稳定）。并集执行输入，
// 亦是 5.1 注册表完备性测试的断言入口。
func AllClientRules() []ClientRules
```

**契约裁定：**

1. **无反注册与运行时变更**：规则集随二进制固定，热调走参数页全局追加（§5）。registry 用切片承载，Register 在 init 期单 goroutine 执行，无锁。
2. **前缀与形态的归属可追溯**：各客户端文件头注释标注前缀来源（基线 config.go injectPrefixesFactory 的分组注释随形态迁移），保证语义视图与代码视图可对照。
3. **新客户端接入零侵入**：specs §2.5 高级用法示例即全部代码增量（init 注册 + 四方法实现），既有客户端文件零改动，由 5.1「新客户端接入零侵入」测试锚定。

---

## 4. 新增契约：客户端签名探测

落位 `extractor/detect.go`（specs §2.3/§2.4 能力7）：

```go
// DetectClient 客户端签名探测：对 Detail.Messages 逐条跑全部已注册客户端的
// DetectFeatures 计分，总分最高者为主客户端；次高分 > 0 且最高分与次高分分差 < 2
// 返回 mixed（严格并列分差为 0 含于该条件）；次高点为零分时单客户端信号即定归属
// （不判 mixed，防按 client 聚合的观测被弱信号稀释）；全部零分返回 unknown。
// 纯函数不调 LLM，永不返回 error。
func DetectClient(detail *conversationlog.SessionDetail) string
```

| 项 | 契约 |
|----|------|
| 输入 | 与裁剪同一份 Detail（specs §2.2：探测在组件内部消费，无新增入参） |
| 输出 | 七值标识：claude_code / opencode / workbuddy / omo / automation / mixed / unknown |
| 失败态 | 无。零特征命中落 unknown，属正常态不记错误日志（specs §2.4 能力7） |
| 性能预算 | ≤ 10ms / 会话（500 消息内）；实现并入裁剪遍历单趟产出（specs §3.1） |
| 正确性边界 | 识别结果只供观测归因与 client 落库，并集执行不依赖识别正确性（specs §2.4 能力1） |

**实现约束：** 计分并入 classifySequence 单趟（避免二次全量扫描），DetectClient 保持独立函数形态供测试直调（specs §2.4 能力7 注意事项）；探测特征集随客户端规则文件演进，无独立维护的特征清单。

---

## 5. 变更契约：前缀黑名单生效集组装（追加语义）

### 5.1 生效集公式

运行时有效黑名单（specs §2.4 能力1 第 2 条）：

```
effective = 通用层前缀 ∪ ⋃ UserPrefixes(全部已注册客户端) ∪ paramAppend（参数页追加集）
```

- 并集去重：多客户端贡献相同前缀字面时单次匹配（specs 注意事项）。
- 出厂态（paramAppend 为空）下 effective 与基线出厂集严格相等，由 5.1 注册表完备性测试守护（UserPrefixes 并集 ∪ 通用层前缀 == 基线 injectPrefixesFactory，排序后逐元素相等）。
- 非 user 角色收窄：`NonUserPrefixes 并集 == 基线 assistantPrefixesFactory`（集合相等同受测试守护）；非 user 角色按「生效集 ∩ 收窄集」判定，基线语义不变。参数页追加条目通常不在收窄集，天然只作用于 user 侧。

### 5.2 config.go 演进

```go
// InjectPrefixes 语义演进：从平面 factory 拷贝改为并集组装器，返回
// 通用层前缀与全部客户端 UserPrefixes 并集（去重后）的拷贝。
// 供 5.1 完备性断言、migrate 测试锚定与装配层引用，函数签名不变。
func InjectPrefixes() []string
```

基线 injectPrefixesFactory / assistantPrefixesFactory 平面变量随形态迁移拆除，等集关系转由测试守护（specs §7.1 术语「两视图一致」）。

### 5.3 参数键 extractor.inject_prefixes 语义变更

参数键名不变（specs §2.2 ParamKeyInjectPrefixes），承载语义从**替换**改为**追加**：

| 项 | 基线（替换语义） | 本变更（追加语义） |
|----|----------------|------------------|
| param_value 语义 | 生效黑名单全量 | 运维追加条目（对所有客户端消息生效） |
| 出厂 seed | InjectPrefixes() 全集 | 空数组 `[]`（specs §5.1：配置集不承载出厂前缀） |
| 键缺失/损坏回退 | 出厂全集 | 空追加集（等效无追加） |
| 配置删除条目 | 从生效集移除（含出厂前缀） | 只收回运维自己追加的条目；代码前缀并集不受配置影响，放开走代码变更发版 |
| 显式清空 `[]` | 放开全部过滤（回退 role 兜底） | 无追加，生效集 = 出厂并集 |

**消费侧变更（extract.go 参数读取段）：** `loaded[ParamKeyInjectPrefixes]` 的 nil（键缺失/损坏）与 `[]`（显式清空）统一按空追加集处理，二者语义在追加模式下天然收敛，无需区分；生效集按 §5.1 公式组装。redact_patterns 维持基线替换语义与出厂回退，处理逻辑零改动。

**装配侧变更（providers.go）：** NewExtractorSysParamDefaults 的 defaulter map 移除 inject_prefixes 键（回退 nil 即空追加），redact_patterns 键保留出厂回退。

### 5.4 与基线集成测试的已知行为差异

基线 03 §5.2 黑名单参数联动的替换语义预期在本变更后翻转：配置删除出厂前缀不再收回该前缀（代码并集仍在生效集）。specs §5.2 已将该差异列为新预期，属裁定的行为变更而非回归。

---

## 6. 不变契约清单（沿用基线，此处仅列差异点）

- **组件方法契约**：Extract / ExtractByKey / Trim / ShouldSkip / Redact 签名、返回结构、错误码全集与基线严格一致（specs §2.3），新增 DetectClient 见 §4。
- **Asynq 任务契约**：TypeSessionExtract 任务类型、payload schema、handler 行为约定、超时推导全部不变（探测在 doExtract 内部执行，对编排入口透明）。勘误：handler ctx 预算以基线 specs §2.3 的 ≥ 550s 为准（Retry-After 封顶 60s 已计入），基线 03 §3.3 的 440s 属 specs v1.1 修订前的滞后口径，dev_plan 排超时配置任务时以 550s 落地。
- **错误通道边界**：基线 03 §4.2 的场景矩阵全部不变；探测与分层执行零新增错误码（探测失败静默落 unknown，分层执行是纯内存判定无失败态）。
- **仓储契约**：Save / ListByPersonAndRange 签名不变；SessionFeature 结构新增 `Client string` 字段（`gorm:"type:varchar(32);not null"`），Save 透传该列，幂等状态机与 client 列的交互规则见 [04_model_interface.md](04_model_interface.md) §3.1。
- **日志规范**：字段在基线「session_key、token_name、计数、错误码」基础上追加 client（枚举值非消息内容，隐私口径不变，specs §6.1）。
- **观测口径**：基线四指标定义不变，观测维度可按 client 列拆分；新增 unknown_client_ratio = client=unknown 行数 / 周期会话总量（分母同 extract_fail_ratio 的列表口径），单周期 ≥ 20% 低严重度提示，非阻断告警（specs §6.2-6.3）。

---

## 7. SSOT 合规与一致性

- [x] specs §2.2 配置项说明（ClientRules/DetectFeature/Register/AllClientRules/ParamKeyInjectPrefixes）逐符号落地于 §3/§5，接口签名与注释语义一致。
- [x] specs §2.3 输出定义：DetectClient 签名与七值值域、client 列口径与 04 文档一一对应；错误码零新增与 specs「探测与分层执行不新增错误码」一致。
- [x] specs §2.4 能力1（并集执行、前缀拆分、追加语义、通道归属、入口变薄）在 §2/§5 承载；能力7/8 在 §3/§4 承载。
- [x] specs §5.1 测试依赖的断言入口（AllClientRules、DetectClient、InjectPrefixes 组装器）均在契约中导出。
- [x] 无 HTTP 接口与 specs §1.4（进程内组件）一致；装配零变更与 specs §4.2 一致。
- [x] 常量单源（七值在 rules 包）与依赖单向（extractor → rules）裁定消除了环依赖，specs 目标结构保持。

---

## 8. 不涉及的设计

- 提取通道参数化（按识别结果定向路由裁剪规则）：specs §1.3/§2.4 能力7 明确为后续演进方向，本期不交付。
- T5 按 client 列拆人群签名的消费逻辑（C08-C12 增强维度，归 T5）。
- 参数页管理 HTTP 接口与界面（归系统参数域，沿用基线 03 §6 边界）。
- 新客户端的具体规则文件内容（claudecode.go 等五文件的形态清单属实现，dev_plan 排任务，契约只锁接口）。

---

**文档版本：** v1.1
**最后更新：** 2026-08-28
**作者：** lixuetao
