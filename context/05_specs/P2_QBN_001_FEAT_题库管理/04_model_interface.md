# 题库管理 数据库模型设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| 所属 Feature | P2_QBN_001_FEAT_题库管理 |
| 文档版本 | v1.0 |
| 创建日期 | 2026-09-23 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书.md](01_功能需求规格说明书.md)、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md) |

---

## 一、基础信息

**模块代号：** QBN（题库域）

**数据库类型：** 多库可切换，按 `SQL_DSN` 前缀选 dialector（SQLite/MySQL/PostgreSQL），生产推荐 PostgreSQL 或 MySQL。规则文件 §1.1。

**字符集：** MySQL utf8mb4；PostgreSQL UTF-8。

**建表方式：** GORM AutoMigrate 为主（架构文档 4.6），下文 GORM 模型 struct 为建表权威，MySQL/PostgreSQL DDL 仅供字段语义参考。新模型须在 [model/migrate.go](../../../hr-backend/internal/model/migrate.go) 的 `allModels()` 登记。

**表命名：** 沿用 GORM 默认 NamingPolicy 实体复数化（规则文件 §1.9），不强制模块前缀。

---

## 二、设计总览

### 2.1 实体识别

业务实体三层：

| 实体 | 表名 | 形态 | 说明 |
|------|------|------|------|
| 题目 | `questions` | 多行业务表（软删除） | AI 管理题与量表题统一承载，全生命周期状态机 |
| 审核批次 | `question_batches` | 多行业务表（无软删除） | 审核单元，来源单一不混审，终态保留追溯 |
| 生成会话 | `question_generations` | 多行过程表 | LLM 生成的过程状态与暂存区，终态后批次接棒 |

复用已有表：`dimensions`（题目维度归属，量表引入时补 seed ENNEAGRAM 型别倾向维度，见 §4.2）。

### 2.2 关键设计决策

**统一题目表而非按来源分表。** AI 管理题与量表题共享字段集（编号、维度归属、三段文本、状态、批次、引用计数），差异仅在来源语义与文本字段含义（情境描述/题项陈述、考察点/计分键），用 `source` 枚举区分即可。作答方式（对话作答/Likert 5 级）由 source 派生，不落库。

**批次是审核单元而非仅日志。** 每个批次只含一个来源的题目（specs 规则 1），确认入库与作废都以批次为事务边界整批操作。批次三态（待审核/已关闭/已作废）均为保留态，不物理删除也不软删除，历史批次号与审核结果长期可追溯。

**生成过程独立成 question_generations 表。** specs 4.3.4 规则 3 要求生成失败整批作废且已生成部分随之逻辑删除。若生成中逐题写 questions 表，失败时需要回删行并消耗题目编号。改为生成中题目暂存 generation 行的 `staging` 列，全部完成后一个事务落库（建批次 + 批量插题目 + 回填批次 ID）；失败或取消仅置 generation 终态，questions 表零残留。staging 承载满额批次可达数百 KB（30 题 × 单题文本上限约 315KB），tag 保持通用 `text`（规则文件 §1.5 禁库特定类型），MySQL 落 TEXT（64KB）容量不足，经 migrateDB 幂等方言钩子升 MEDIUMTEXT（见 §3.3）。进度（已生成 n/N、当前维度）从 generation 行读，前端轮询（specs 4.3.4 规则 3 授权接口设计阶段确定进度机制，取轮询弃 SSE 的理由见 03 文档 §3.9）。

**题目编号系统生成、只增不复用。** AI 题 `Q-AG-{序号}`、量表题 `Q-Scale-{序号}`，序号按前缀独立递增，超四位自然扩展（specs 4.1.2 B）。已删除行的编号永久占用（软删行保留原值，新号恒大于旧号，永不撞唯一索引），因此无需规则文件 §1.10 的软删占位码改写方案，UNIQUE 索引只用于拦截并发分配冲突。序号分配在批量创建事务内连续取段：按前缀查最大序号（含软删行，Unscoped）后递增，UNIQUE 兜底并发窗口。

**批次号同日序号规则。** 前缀字母由批次类型决定（生成 G / 量表引入 S / 重新送审 R）+ MMdd，同日同前缀从第 2 批起追加 `-序号`（specs 4.1.2 C）。按服务器本地日（time.Local，与 scheduler 先例一致）判定同日。

**量表模板内置代码不建表。** 标准量表（Riso-Hudson 144 题、Essence 108 题）是固定文本，任何界面不提供编辑（specs 规则 5），随代码版本管理比入库更合适，且与 seed_dimensions 内置 seed 先例一致。引入操作从内置模板幂等展开成 questions 行。「已引入」判定查 questions 表（specs 规则 8：存在该量表未逻辑删除的题目即已引入），模板不承载引入状态。

**量表引入事务内补 seed 九型型别维度。** 量表题的维度归属是九型 9 个型别倾向维度（dimensions 表 ENNEAGRAM 模块）。P2_DIM_001 的 seed 刻意不写入九型维度（归量表引入落地），本 Feature 在引入事务内 ensure：型别维度不存在则按内置定义创建（ENNEAGRAM 模块、TEST 数据来源、启用态），已存在（含停用）则复用。

**被引用计数由下游写入。** `reference_count` 字段本 Feature 只读展示与删除前置校验（specs 规则 4），增量维护归主动测试评估运营功能（F7，specs 7.2 被依赖方）。

### 2.3 ER 图

```
question_generations（生成过程）            内置量表模板（代码常量，不建表）
        │ 完成时建批+批量落题                        │ 引入时展开
        ▼                                           ▼
question_batches（审核批次）──1:N──questions（题目，软删除）
        │ batch_type 决定作废语义                     │ dimension_id ──[逻辑关联]── dimensions
        │ （RESUBMIT 回退，GENERATE/IMPORT 随批删）    │ （ENNEAGRAM 型别维度随量表引入 ensure；
        └────────────────────────────────────────────┘ AI_MGMT 子能力维度由维度配置维护）

questions.reference_count ←[由 F7 主动测试写入，本功能只读]
```

无外键约束，关联靠业务字段（规则文件 §1.5），关联完整性由应用层校验。

---

## 三、表结构定义

### 3.1 核心业务表：questions

**表名：** `questions`

**用途：** 题库全量题目，AI 管理题与量表题统一承载。一条记录对应一道可被主动测试指派的题目，覆盖待审核、启用、已停用、已驳回四态与软删除终态（specs 6.1）。

---

**GORM 模型 struct（建表权威）：**

```go
// Question 题库题目，AI 管理题与量表题统一承载。
// 三段文本按 source 语义换名：AI 题为情境描述/作答要求/考察点，量表题为题项陈述/作答方式说明/计分键。
type Question struct {
    ID             int64          `gorm:"primaryKey" json:"id,string"`                                        // 雪花 ID，string 化规避前端 JS 精度坑
    QuestionNo     string         `gorm:"type:varchar(32);not null;uniqueIndex:uk_questions_question_no" json:"question_no"` // AI 题 Q-AG-xxxx / 量表题 Q-Scale-xxxx，系统生成只增不复用
    Source         string         `gorm:"type:varchar(16);not null;index:idx_questions_source_status" json:"source"`         // 来源：AI（AI 管理题）/ SCALE（九型量表），决定可编辑性与审核提示
    DimensionID    int64          `gorm:"not null;index:idx_questions_dimension" json:"dimension_id,string"`  // 所属维度雪花 ID：AI 题为 AI_MGMT 子能力，量表题为 ENNEAGRAM 型别倾向
    ScaleKey       string         `gorm:"type:varchar(32);not null;index:idx_questions_scale" json:"scale_key"` // 量表标识：RISO_HUDSON/ESSENCE，AI 题空串；承载「已引入」判定
    Scenario       string         `gorm:"type:text;not null" json:"scenario"`                                 // 情境描述（AI，≤1000）/ 题项陈述（量表）
    Requirement    string         `gorm:"type:text;not null" json:"requirement"`                              // 作答要求（AI，≤2000，选项 A/B/C/D 行内书写）/ 作答方式说明（量表）
    FocusPoint     string         `gorm:"type:varchar(500);not null" json:"focus_point"`                      // 考察点（AI）/ 计分键（量表型别归属聚合规则）
    Status         string         `gorm:"type:varchar(16);not null;index:idx_questions_source_status" json:"status"` // PENDING/ACTIVE/DISABLED/REJECTED，已删除走 deleted_at
    RejectReason   string         `gorm:"type:varchar(500)" json:"reject_reason"`                             // 驳回原因，仅 REJECTED 非空；重新送审批次作废回退时保留
    BatchID        int64          `gorm:"not null;index:idx_questions_batch" json:"batch_id,string"`          // 当前所属批次雪花 ID，重新送审时改挂新批次
    ReferenceCount int            `gorm:"type:int;not null" json:"reference_count"`                           // 被主动测试指派累计次数，由 F7 写入，本域只读；>0 拒绝删除
    Version        int            `gorm:"type:int;not null" json:"version"`                                   // 乐观锁版本号，编辑/启停/删除并发保护
    DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`                                                     // 软删除标记，承载 specs 6.1 已删除终态
    CreatedAt      time.Time      `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt      time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}
```

**MySQL DDL（参考，实际以 AutoMigrate 为准）：**

```sql
CREATE TABLE `questions` (
  `id` BIGINT NOT NULL COMMENT '主键ID（雪花ID）',
  `question_no` VARCHAR(32) NOT NULL COMMENT '题目编号，系统生成只增不复用',
  `source` VARCHAR(16) NOT NULL COMMENT '来源：AI/SCALE',
  `dimension_id` BIGINT NOT NULL COMMENT '所属维度ID（逻辑关联 dimensions.id）',
  `scale_key` VARCHAR(32) NOT NULL COMMENT '量表标识：RISO_HUDSON/ESSENCE，AI 题空串',
  `scenario` TEXT NOT NULL COMMENT '情境描述（AI）/题项陈述（量表）',
  `requirement` TEXT NOT NULL COMMENT '作答要求（AI）/作答方式说明（量表）',
  `focus_point` VARCHAR(500) NOT NULL COMMENT '考察点（AI）/计分键（量表）',
  `status` VARCHAR(16) NOT NULL COMMENT '状态：PENDING/ACTIVE/DISABLED/REJECTED',
  `reject_reason` VARCHAR(500) NULL COMMENT '驳回原因，仅 REJECTED 非空',
  `batch_id` BIGINT NOT NULL COMMENT '当前所属批次ID',
  `reference_count` INT NOT NULL COMMENT '被引用次数（F7 写入）',
  `version` INT NOT NULL COMMENT '乐观锁版本号',
  `deleted_at` DATETIME NULL COMMENT '软删除标记',
  `created_at` DATETIME NULL COMMENT '创建时间',
  `updated_at` DATETIME NULL COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE INDEX `uk_questions_question_no` (`question_no`),
  INDEX `idx_questions_source_status` (`source`, `status`),
  INDEX `idx_questions_dimension` (`dimension_id`),
  INDEX `idx_questions_batch` (`batch_id`),
  INDEX `idx_questions_scale` (`scale_key`),
  INDEX `idx_questions_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='题库题目表';
```

**PostgreSQL DDL（参考）：**

```sql
CREATE TABLE "questions" (
  id BIGINT NOT NULL,
  question_no VARCHAR(32) NOT NULL,
  source VARCHAR(16) NOT NULL,
  dimension_id BIGINT NOT NULL,
  scale_key VARCHAR(32) NOT NULL,
  scenario TEXT NOT NULL,
  requirement TEXT NOT NULL,
  focus_point VARCHAR(500) NOT NULL,
  status VARCHAR(16) NOT NULL,
  reject_reason VARCHAR(500),
  batch_id BIGINT NOT NULL,
  reference_count INTEGER NOT NULL,
  version INTEGER NOT NULL,
  deleted_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE,
  updated_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT "questions_pkey" PRIMARY KEY (id)
);
CREATE UNIQUE INDEX "uk_questions_question_no" ON "questions"("question_no");
CREATE INDEX "idx_questions_source_status" ON "questions"("source", "status");
CREATE INDEX "idx_questions_dimension" ON "questions"("dimension_id");
CREATE INDEX "idx_questions_batch" ON "questions"("batch_id");
CREATE INDEX "idx_questions_scale" ON "questions"("scale_key");
CREATE INDEX "idx_questions_deleted_at" ON "questions"("deleted_at");
```

---

**字段说明：**

| 字段名 | 类型（MySQL） | 类型（PostgreSQL） | 类型（SQLite） | 必填 | 默认值 | 说明 |
|--------|------|------|------|------|--------|------|
| id | BIGINT | BIGINT | INTEGER | 是 | 雪花生成 | 主键ID（雪花ID，应用层生成，规则文件 §1.2） |
| question_no | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 系统生成 | 题目编号。AI 题 Q-AG-四位流水，量表题 Q-Scale-四位流水，超四位自然扩展，全系统唯一（specs 4.1.2 B）。序号按前缀独立递增只增不复用，批量创建事务内连续取段，UNIQUE 索引兜底并发分配冲突 [长度来源：specs 编号规则，列宽留余量] |
| source | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 来源枚举：AI（AI 管理题）/ SCALE（九型量表题）。决定文本字段语义、可编辑性（量表题不可编辑，specs 规则 5）、审核提示文案与作答方式派生（AI 对话作答 / SCALE Likert 5 级） |
| dimension_id | BIGINT | BIGINT | INTEGER | 是 | 无 | 所属维度雪花 ID，逻辑关联 dimensions.id。AI 题为 AI_MGMT 模块子能力维度，量表题为 ENNEAGRAM 模块型别倾向维度。维度停用/软删后题目保留原值展示（specs 4.1.2 E）。json 带 `,string` |
| scale_key | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 空串 | 量表标识：RISO_HUDSON（Riso-Hudson 标准量表）/ ESSENCE（Essence 精简量表），AI 题空串。「已引入」判定字段（specs 规则 8：存在该量表未逻辑删除题目即已引入，含待审核/启用/已停用/已驳回态） |
| scenario | TEXT | TEXT | TEXT | 是 | 无 | AI 题为管理情境描述，1~1000 字符（specs 4.1.2 E、8.3 偏离记录）；量表题为题项陈述原文。长文本取 TEXT（规则文件 §1.6） [长度来源：specs] |
| requirement | TEXT | TEXT | TEXT | 是 | 无 | AI 题为作答要求含 A/B/C/D 选项全文，1~2000 字符（specs 4.1.2 E、8.3 偏离记录）；量表题为 1-5 级选择说明。取 TEXT [长度来源：specs] |
| focus_point | VARCHAR(500) | VARCHAR(500) | TEXT | 是 | 无 | AI 题为考察点，1~500 字符（specs 4.1.2 E）；量表题为型别归属聚合规则（计分键） [长度来源：specs] |
| status | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 题目状态机（specs 6.1）：PENDING（待审核）/ ACTIVE（启用）/ DISABLED（已停用）/ REJECTED（已驳回）。已删除终态走 deleted_at 软删除，不占枚举值 |
| reject_reason | VARCHAR(500) | VARCHAR(500) | TEXT | 否 | 空串 | 驳回原因，纯文本 ≤500 字符（specs 规则 3），仅 REJECTED 态非空。重新提交时保留（重新送审批次作废回退仍需展示，specs 6.3），再次驳回时覆盖 |
| batch_id | BIGINT | BIGINT | INTEGER | 是 | 无 | 当前所属批次雪花 ID，逻辑关联 question_batches.id。生成/引入时写入，重新送审时改挂重新送审批次（并入或新建，specs 规则 6）。json 带 `,string` |
| reference_count | INT | INTEGER | INTEGER | 是 | 0 | 被主动测试指派的累计次数（specs 术语表）。由主动测试评估运营功能（F7）写入，本域只读；大于 0 时删除被拒绝（specs 规则 4） |
| version | INT | INTEGER | INTEGER | 是 | 1 | 乐观锁版本号，新建置 1，更新自增。编辑/启停/删除提交时校验一致性，题目已被他人删除或变更时返 1713（specs 规则 9 并发冲突） |
| deleted_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 否 | NULL | 软删除标记，GORM DeletedAt 自动维护。承载已删除终态（行级删除、生成/量表批次作废随批删），查询自动过滤 |
| created_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoCreateTime | 创建时间，题目生成/引入落库时刻，查看弹窗「入库时间」以此为准。列可空（DDL 无 NOT NULL），应用层 autoCreateTime 恒填值 |
| updated_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoUpdateTime | 更新时间，列表默认按此倒序（specs 4.1.5）。列可空（DDL 无 NOT NULL），应用层 autoUpdateTime 恒填值 |

**索引说明：**

| 索引名 | 类型 | 字段 | 用途 |
|--------|------|------|------|
| PRIMARY | PRIMARY KEY | id | 主键索引，雪花 ID |
| uk_questions_question_no | UNIQUE | question_no | 编号唯一性兜底：拦截序号并发分配的 TOCTOU 窗口；编号只增不复用，软删行占号与新号永不冲突（无需占位码改写） |
| idx_questions_source_status | INDEX | (source, status) | 列表 tab 主查询路径：按来源（tab）加状态筛选分页，status 恒排除 PENDING |
| idx_questions_dimension | INDEX | dimension_id | 维度下拉筛选 |
| idx_questions_batch | INDEX | batch_id | 批次题目明细、确认入库与作废的批内题目定位 |
| idx_questions_scale | INDEX | scale_key | 「已引入」判定与量表批次随批删除定位 |
| idx_questions_deleted_at | INDEX | deleted_at | GORM 软删除自动查询过滤 |

**业务规则：**

- 状态机（specs 6.1/6.3）：PENDING→ACTIVE/DISABLED 前须经批次确认（未标记→ACTIVE，标记→REJECTED）；ACTIVE↔DISABLED 行内启停互切；REJECTED→PENDING 仅经重新提交；PENDING→已删除仅随生成/量表批次作废；REJECTED 回退仅随重新送审批次作废。全部转换在 service 层校验前置状态，非法转换拒绝。
- 待审核题目隔离（specs 4.3.4 规则 2）：列表 tab 查询恒排除 `status = PENDING`，待审核题仅在批次审核视图可见。
- 软删除（规则文件 §1.3）：行级删除、生成/量表批次作废随批删均置 deleted_at。软删行的编号与量表占位保留，「已引入」判定与编号分配的 Unscoped 查询可见。
- 量表题保护（specs 规则 5）：source=SCALE 的行，scenario/requirement/focus_point/dimension_id 生成后任何路径不更新，仅 status（启停）与 deleted_at 可变。
- 乐观锁：编辑/启停/删除 WHERE 附带 version，影响行数 0 即并发冲突返 1713。
- 关键词搜索（specs 4.1.2 A）：question_no 与 scenario 的 LIKE OR 匹配，输入经 likeescape.EscapeLike（internal/pkg/likeescape）转义并显式 `ESCAPE '\'`（规则文件 §1.11）。

---

### 3.2 核心业务表：question_batches

**表名：** `question_batches`

**用途：** 审核批次，一次生成、一次量表引入或一次重新送审形成的待审核题目集合（specs 术语表）。批次是确认入库与作废的事务边界，来源单一不混审（specs 规则 1）。

---

**GORM 模型 struct（建表权威）：**

```go
// QuestionBatch 题库审核批次，确认入库与作废的事务边界。
// 终态（已关闭/已作废）永久保留供追溯，无软删除。
type QuestionBatch struct {
    ID            int64      `gorm:"primaryKey" json:"id,string"`
    BatchNo       string     `gorm:"type:varchar(32);not null;uniqueIndex:uk_question_batches_batch_no" json:"batch_no"` // #GMMdd/#SMMdd/#RMMdd，同日多批 -序号
    Title         string     `gorm:"type:varchar(255);not null" json:"title"`        // 批次标题：生成批次为维度组合描述，量表批次为量表名，重新送审为「重新送审」
    Source        string     `gorm:"type:varchar(16);not null" json:"source"`        // 来源：AI/SCALE，决定审核重点提示文案
    BatchType     string     `gorm:"type:varchar(16);not null" json:"batch_type"`    // 批次类型：GENERATE/IMPORT/RESUBMIT，决定批次号前缀与作废语义
    Status        string     `gorm:"type:varchar(16);not null;index:idx_question_batches_status,priority:1" json:"status"` // PENDING/CLOSED/VOIDED，与 created_at 组复合索引
    QuestionCount int        `gorm:"type:int;not null" json:"question_count"`        // 批内题目总数，建批写入、并批累加，批次卡展示
    ScaleKey      string     `gorm:"type:varchar(32);not null" json:"scale_key"`     // 量表标识，仅 IMPORT 批次非空
    DimensionIDs  string     `gorm:"type:text" json:"-"`                             // 生成批次维度 ID 集合快照（JSON 数组），追溯用
    ClosedAt      *time.Time `json:"closed_at"`                                     // 确认入库时刻
    VoidedAt      *time.Time `json:"voided_at"`                                     // 作废时刻
    CreatedAt     time.Time  `gorm:"autoCreateTime;index:idx_question_batches_status,priority:2" json:"created_at"`
    UpdatedAt     time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
```

**MySQL DDL（参考）：**

```sql
CREATE TABLE `question_batches` (
  `id` BIGINT NOT NULL COMMENT '主键ID（雪花ID）',
  `batch_no` VARCHAR(32) NOT NULL COMMENT '批次号，同日同前缀唯一',
  `title` VARCHAR(255) NOT NULL COMMENT '批次标题',
  `source` VARCHAR(16) NOT NULL COMMENT '来源：AI/SCALE',
  `batch_type` VARCHAR(16) NOT NULL COMMENT '批次类型：GENERATE/IMPORT/RESUBMIT',
  `status` VARCHAR(16) NOT NULL COMMENT '状态：PENDING/CLOSED/VOIDED',
  `question_count` INT NOT NULL COMMENT '批内题目总数',
  `scale_key` VARCHAR(32) NOT NULL COMMENT '量表标识，仅 IMPORT 批次非空',
  `dimension_ids` TEXT NULL COMMENT '生成批次维度ID快照（JSON数组）',
  `closed_at` DATETIME NULL COMMENT '确认入库时刻',
  `voided_at` DATETIME NULL COMMENT '作废时刻',
  `created_at` DATETIME NULL COMMENT '创建时间',
  `updated_at` DATETIME NULL COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE INDEX `uk_question_batches_batch_no` (`batch_no`),
  INDEX `idx_question_batches_status` (`status`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='题库审核批次表';
```

**PostgreSQL DDL（参考）：**

```sql
CREATE TABLE "question_batches" (
  id BIGINT NOT NULL,
  batch_no VARCHAR(32) NOT NULL,
  title VARCHAR(255) NOT NULL,
  source VARCHAR(16) NOT NULL,
  batch_type VARCHAR(16) NOT NULL,
  status VARCHAR(16) NOT NULL,
  question_count INTEGER NOT NULL,
  scale_key VARCHAR(32) NOT NULL,
  dimension_ids TEXT,
  closed_at TIMESTAMP WITH TIME ZONE,
  voided_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE,
  updated_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT "question_batches_pkey" PRIMARY KEY (id)
);
CREATE UNIQUE INDEX "uk_question_batches_batch_no" ON "question_batches"("batch_no");
CREATE INDEX "idx_question_batches_status" ON "question_batches"("status", "created_at");
```

---

**字段说明：**

| 字段名 | 类型（MySQL） | 类型（PostgreSQL） | 类型（SQLite） | 必填 | 默认值 | 说明 |
|--------|------|------|------|------|--------|------|
| id | BIGINT | BIGINT | INTEGER | 是 | 雪花生成 | 主键ID（雪花ID，应用层生成）。json 带 `,string` |
| batch_no | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 系统生成 | 批次号：生成批次 #G+MMdd、量表批次 #S+MMdd、重新送审批次 #R+MMdd，同日同前缀从第 2 批起追加 -序号（如 #G0921-2）保证唯一（specs 4.1.2 C）。UNIQUE 索引兜底并发建批冲突 [长度来源：specs 编号规则，列宽留余量] |
| title | VARCHAR(255) | VARCHAR(255) | TEXT | 是 | 无 | 批次标题：生成批次为维度组合描述（如「授权与分工 ×2」），量表批次为量表名，重新送审批次为「重新送审」（specs 4.1.2 C）[长度来源：规则文件 §1.6 标题档] |
| source | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 来源枚举：AI（AI 管理题）/ SCALE（九型量表）。决定审核重点提示文案（specs 规则 1）。RESUBMIT 批次恒为 AI（量表题无重新送审路径） |
| batch_type | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 批次类型枚举：GENERATE（AI 生成）/ IMPORT（量表引入）/ RESUBMIT（重新送审）。决定批次号前缀（G/S/R）与作废语义（RESUBMIT 作废题目回退已驳回，GENERATE/IMPORT 作废题目随批软删，specs 4.1.3 作废批次） |
| status | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 批次状态机（specs 6.2）：PENDING（待审核）/ CLOSED（已关闭，确认入库完成）/ VOIDED（已作废）。两终态均永久保留 |
| question_count | INT | INTEGER | INTEGER | 是 | 无 | 批内题目总数。建批时写入（生成批为题数、量表批为模板题数），RESUBMIT 并批时累加。批次卡展示 |
| scale_key | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 空串 | 量表标识，仅 IMPORT 批次为 RISO_HUDSON/ESSENCE，其余空串 |
| dimension_ids | TEXT | TEXT | TEXT | 否 | NULL | 生成批次的维度 ID 集合快照，JSON 数组字符串存 TEXT 列（规则文件 §1.5 半结构化约定）。追溯用，json 不下发 |
| closed_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 否 | NULL | 确认入库成功时刻，仅 CLOSED 态非空 |
| voided_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 否 | NULL | 作废完成时刻，仅 VOIDED 态非空 |
| created_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoCreateTime | 建批时间，批次卡「生成时间」展示。列可空（DDL 无 NOT NULL），应用层恒填值 |
| updated_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoUpdateTime | 更新时间。列可空（DDL 无 NOT NULL），应用层恒填值 |

**索引说明（question_batches）：**

| 索引名 | 类型 | 字段 | 用途 |
|--------|------|------|------|
| PRIMARY | PRIMARY KEY | id | 主键索引，雪花 ID |
| uk_question_batches_batch_no | UNIQUE | batch_no | 批次号唯一性兜底：拦截同日并发建批的序号分配 TOCTOU 窗口 |
| idx_question_batches_status | INDEX | (status, created_at) | 待审核批次卡区查询（status=PENDING 按 created_at 倒序），批次量小，复合索引足够 |

**业务规则：**

- 状态机（specs 6.2/6.3）：PENDING→CLOSED（确认入库成功）、PENDING→VOIDED（作废），两终态无出边。确认入库与作废都要求批次当前为 PENDING，否则返 1706（specs 4.2.4 规则 1、规则 7）。
- 确认入库事务：批次置 CLOSED + 未标记题目批量置 ACTIVE + 标记题目批量置 REJECTED 并写驳回原因，单事务提交（specs 4.2.3 确认入库）。
- 作废事务（specs 4.1.3 作废批次）：批次置 VOIDED；GENERATE/IMPORT 批次批内题目批量软删（不进入题库）；RESUBMIT 批次批内题目回退 REJECTED 并保留原驳回原因（题库既有题目不作废）。
- RESUBMIT 并批（specs 规则 6）：重新提交时存在 source=AI 且 batch_type=RESUBMIT 且 status=PENDING 的批次则并入（question_count+1，题目 batch_id 改挂），否则新建。并批判定与建批在同一事务，批次号唯一索引兜底并发。
- 批次号生成：按 batch_type 前缀 + 服务器本地日 MMdd，查同日同前缀最大批次号推导序号，首批无后缀、后续 -2/-3 递增。
- 批内题目数上限：IMPORT 批次随模板（144/108），GENERATE 批次 5~30（specs 4.3.2），RESUBMIT 并批自然增长无上限；确认入库单请求整批提交（specs 4.2.4 规则 1）。
- 无软删除：批次是审核凭证，作废是业务状态而非删除，终态行永久保留。

---

### 3.3 过程表：question_generations

**表名：** `question_generations`

**用途：** LLM 生成 AI 管理题的过程会话。承载生成进度（已生成 n/N、当前维度）、已生成题目暂存区与终态结果，供前端轮询。完成后题目落 questions 表并由批次接棒，本表行降级为过程记录。

---

**GORM 模型 struct（建表权威）：**

```go
// QuestionGeneration LLM 生成会话，生成过程状态与暂存区。
// 生成中题目攒在 Staging，完成时单事务落库；失败/取消仅置终态，questions 表零残留。
type QuestionGeneration struct {
    ID                 int64     `gorm:"primaryKey" json:"id,string"`
    DimensionIDs       string    `gorm:"type:text;not null" json:"-"`           // 维度 ID 集合快照（JSON 数组），出题参数留痕
    Count              int       `gorm:"type:int;not null" json:"count"`         // 目标题数 5~30
    Status             string    `gorm:"type:varchar(16);not null" json:"status"` // QUEUED/RUNNING/COMPLETED/FAILED/CANCELED
    GeneratedCount     int       `gorm:"type:int;not null" json:"generated_count"` // 已生成题数，轮询进度
    CurrentDimensionID int64     `gorm:"not null" json:"current_dimension_id,string"` // 当前正在构造的维度，轮询进度提示
    Staging            string    `gorm:"type:text" json:"-"`                     // 已生成题目暂存（JSON 数组），满额批次可达数百 KB，MySQL 经 migrateDB 钩子升 MEDIUMTEXT，终态清空
    BatchID            int64     `gorm:"not null" json:"batch_id,string"`        // 完成回填批次 ID，未完成为 0
    ErrorCode          string    `gorm:"type:varchar(32)" json:"error_code"`     // 失败归类：LLM_FAILED/LLM_TIMEOUT/CANCELED/INTERNAL
    CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
```

**MySQL DDL（参考）：**

```sql
CREATE TABLE `question_generations` (
  `id` BIGINT NOT NULL COMMENT '主键ID（雪花ID）',
  `dimension_ids` TEXT NOT NULL COMMENT '维度ID集合快照（JSON数组）',
  `count` INT NOT NULL COMMENT '目标题数 5~30',
  `status` VARCHAR(16) NOT NULL COMMENT '状态：QUEUED/RUNNING/COMPLETED/FAILED/CANCELED',
  `generated_count` INT NOT NULL COMMENT '已生成题数',
  `current_dimension_id` BIGINT NOT NULL COMMENT '当前正在构造的维度ID',
  `staging` TEXT NULL COMMENT '已生成题目暂存（JSON数组），终态清空。满额批次超 64KB，migrateDB 钩子升 MEDIUMTEXT',
  `batch_id` BIGINT NOT NULL COMMENT '完成回填批次ID，未完成为0',
  `error_code` VARCHAR(32) NULL COMMENT '失败归类',
  `created_at` DATETIME NULL COMMENT '创建时间',
  `updated_at` DATETIME NULL COMMENT '更新时间',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='题库LLM生成会话表';
```

**PostgreSQL DDL（参考）：**

```sql
CREATE TABLE "question_generations" (
  id BIGINT NOT NULL,
  dimension_ids TEXT NOT NULL,
  "count" INTEGER NOT NULL,
  status VARCHAR(16) NOT NULL,
  generated_count INTEGER NOT NULL,
  current_dimension_id BIGINT NOT NULL,
  staging TEXT,
  batch_id BIGINT NOT NULL,
  error_code VARCHAR(32),
  created_at TIMESTAMP WITH TIME ZONE,
  updated_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT "question_generations_pkey" PRIMARY KEY (id)
);
```

> PostgreSQL 参考 DDL 中 count 为保留字，实际建表走 GORM AutoMigrate 由 NamingPolicy 生成列名（`count` 列在 GORM 链式操作下无冲突；手写原生 SQL 时按架构文档 4.6 用 model.QuoteIdent 包裹）。MySQL 反引号已包裹。

---

**字段说明：**

| 字段名 | 类型（MySQL） | 类型（PostgreSQL） | 类型（SQLite） | 必填 | 默认值 | 说明 |
|--------|------|------|------|------|--------|------|
| id | BIGINT | BIGINT | INTEGER | 是 | 雪花生成 | 主键ID（雪花ID，应用层生成），即前端轮询的 generation_id。json 带 `,string` |
| dimension_ids | TEXT | TEXT | TEXT | 是 | 无 | 发起生成时选定的维度 ID 集合快照，JSON 数组字符串存 TEXT 列（规则文件 §1.5）。出题参数留痕，json 不下发 |
| count | INT | INTEGER | INTEGER | 是 | 无 | 目标题数，5~30 整数（specs 4.3.2），接口层校验 |
| status | VARCHAR(16) | VARCHAR(16) | TEXT | 是 | 无 | 会话状态：QUEUED（已入队）/ RUNNING（生成中）/ COMPLETED（完成）/ FAILED（失败整批作废）/ CANCELED（运营放弃）。终态后不变 |
| generated_count | INT | INTEGER | INTEGER | 是 | 0 | 已生成题数，worker 每生成一题更新，前端轮询展示「已生成 n/N」 |
| current_dimension_id | BIGINT | BIGINT | INTEGER | 是 | 0 | 当前正在构造的维度 ID，前端轮询回显维度名（specs 4.3.5 进度提示）。未开始为 0 |
| staging | TEXT | TEXT | TEXT | 否 | NULL | 已生成题目暂存，JSON 数组字符串（题号未分配前的题目全文）。满额批次（30 题 × 单题文本上限）可达数百 KB，超出 MySQL TEXT 64KB 上限：tag 保持通用 `text`（规则文件 §1.5），MySQL 经 migrateDB 幂等方言钩子探测列类型后 ALTER 为 MEDIUMTEXT（16MB，仿 aggregate_scores float→double 先例），PostgreSQL/SQLite 的 TEXT 无上限不动。终态（COMPLETED 落库后 / FAILED / CANCELED）清空。json 不下发 |
| batch_id | BIGINT | BIGINT | INTEGER | 是 | 0 | 生成完成时建批并回填批次 ID，未完成为 0。COMPLETED 态响应据此返回批次号 |
| error_code | VARCHAR(32) | VARCHAR(32) | TEXT | 否 | 空串 | 失败归类枚举：LLM_FAILED（调用失败）/ LLM_TIMEOUT（超时）/ CANCELED（取消即终因）/ INTERNAL。前端映射固定失败文案（specs 4.3.4 规则 3 失败原因展示），不透传底层错误串 |
| created_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoCreateTime | 发起时刻。列可空（DDL 无 NOT NULL），应用层恒填值 |
| updated_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 业务必填 | autoUpdateTime | 进度更新时刻。列可空（DDL 无 NOT NULL），应用层恒填值 |

**索引说明：**

| 索引名 | 类型 | 字段 | 用途 |
|--------|------|------|------|
| PRIMARY | PRIMARY KEY | id | 主键索引。轮询按 generation_id 主键查，worker 逐题前检查取消标记同走主键，无需二级索引 |

**业务规则：**

- 状态流转：QUEUED→RUNNING（worker 领取）→COMPLETED/FAILED；QUEUED/RUNNING→CANCELED（运营放弃）。终态不可逆。
- staging 列 MySQL 方言钩子：满额 30 题批次暂存量级数百 KB，超 TEXT 上限。迁移钩子放 migrateDB 的 A 段（AutoMigrate 之前），Migrator 探测 staging 列当前类型为 TEXT（tinytext/text）时按 MySQL 方言 ALTER 为 MEDIUMTEXT，其余方言与既有 MEDIUMTEXT 跳过；幂等，重复启动无 ALTER。
- 协作式取消：worker 每题生成前按主键读行检查 status，CANCELED 即停止并清空 staging；cancel 接口只置状态，不强杀 Asynq 任务。
- 完成事务：建批次（GENERATE）+ staging 批量展开插 questions（连续分配 Q-AG 编号、状态 PENDING）+ 回填 batch_id + 置 COMPLETED + 清空 staging，单事务提交（specs 4.3.4 规则 2 批次自动入队）。
- 失败零残留（specs 4.3.4 规则 3）：LLM 调用失败、任务超时或取消即置 FAILED/CANCELED 并清空 staging，不建批次、questions 表无行。
- 过程行不清理：行体量小（每次生成一行），保留作生成历史追溯，无归档需求。
- 无软删除、无乐观锁：过程会话单写者（worker）加只读轮询，无并发更新冲突面。

---

## 四、种子数据与内置数据

### 4.1 内置量表模板（代码常量，不建表）

两套候选量表的题目全文与型别归属以 Go 侧内置数据承载（建议 `internal/questionbank/scaledata` 包，144 + 108 题静态数组，随代码版本管理），结构：

```go
type ScaleTemplate struct {
    ScaleKey         string           // RISO_HUDSON / ESSENCE
    Name             string           // 量表名（批次标题）
    QuestionCount    int              // 144 / 108
    EstimatedMinutes int              // 25 / 18
    Description      string           // 弹窗卡片描述
    Dimensions       []ScaleDimension // 九型 9 型别维度定义（名、编码），引入时 ensure 到 dimensions
    Items            []ScaleItem      // 题目全文：陈述、作答方式说明、计分键、型别归属
}
```

理由见 §2.2。量表题文本属于标准量表固定内容，版权与准确性由数据准备阶段保障，本设计仅约束结构；引入操作按模板幂等展开，不重复建 dimensions 已存在的型别维度。

### 4.2 九型型别维度 ensure（引入事务内）

量表引入事务内检查并补建 ENNEAGRAM 模块 9 个型别倾向维度（module_code=ENNEAGRAM、data_source=TEST、enabled=true、weight=0、include_overview=false、is_reference=true），已存在（任意状态）则复用现有行。anchor 为 NOT NULL 必填，型别维度不参与聚合评分，置模板内置的型别一句话描述（如「九型 {型别名} 型别倾向，量表题聚合归属」），长度 ≤500。复用判定按内置 code 查活跃行（GORM 默认过滤软删），软删行 code 已改写占位码不可见，视为不存在并新建。与 P2_DIM_001 seed 的约定衔接：九型维度归量表引入落地，dimensions 表不在 migrateDB seed。AI_MGMT 子能力维度同样由运营在维度配置页维护（specs 4.3.2「随维度配置变动」），本 Feature 不 seed。

### 4.3 无首启 seed

questions、question_batches、question_generations 三表首启均空，无 migrateDB seed。

---

## 五、字典数据处理

遵循规则文件 §1.8，项目不引入字典表体系。本功能枚举值处理：

| 枚举字段 | 取值 | 承载方式 |
|---------|------|---------|
| questions.source | AI / SCALE | Go 侧常量 + VARCHAR 列 |
| questions.status | PENDING / ACTIVE / DISABLED / REJECTED | Go 侧常量 + VARCHAR 列 |
| question_batches.source | AI / SCALE | Go 侧常量 + VARCHAR 列 |
| question_batches.batch_type | GENERATE / IMPORT / RESUBMIT | Go 侧常量 + VARCHAR 列 |
| question_batches.status | PENDING / CLOSED / VOIDED | Go 侧常量 + VARCHAR 列 |
| question_generations.status | QUEUED / RUNNING / COMPLETED / FAILED / CANCELED | Go 侧常量 + VARCHAR 列 |
| questions.scale_key | RISO_HUDSON / ESSENCE（空串=AI 题） | Go 侧常量 + VARCHAR 列 |

字段说明不使用 `[字典：xxx]` 标注，亦不生成字典表 INSERT 语句。前端展示名（启用/已停用/已驳回、AI 管理题/九型量表等）由 i18n 文案层映射。

---

## 六、索引策略

### 6.1 主键索引

三表主键均为雪花 ID（int64/BIGINT），应用层生成，GORM 全局 Create 回调透明赋值（规则文件 §1.2）。无自增。

### 6.2 查询路径覆盖

- 列表 tab 主路径：`idx_questions_source_status (source, status)` 覆盖 tab + 状态筛选 + 分页；keyword 走 scenario LIKE 全扫，题库千行量级可接受。
- 批次操作路径：`idx_questions_batch (batch_id)` 支撑批次明细、确认入库、作废的批内题目定位；`idx_questions_scale (scale_key)` 支撑「已引入」判定。
- 待审核卡区：`idx_question_batches_status (status, created_at)`，PENDING 态批次量个位数量级。

### 6.3 分表分库

不适用。题库量级为千行（两套量表 252 题 + AI 题累积），单表单库完全承载，无分片需求。

---

## 七、性能与安全

### 7.1 性能

- 量表引入单事务批量插 144/108 行，SQLite 库级写锁下也在秒级完成；生成完成落库同批量级。
- 确认入库批量更新走 `idx_questions_batch` 定位，单事务整批提交，无逐题往返。
- LLM 生成经调用底座全局并发限制（在飞上限 4、FIFO 排队），多运营同时生成不放大上游压力（架构文档 3.1.2）。
- 无 Redis 缓存需求：题库读写均为运营低频操作，无跑批热读场景（主动测试取题的缓存策略归 F7）。

### 7.2 安全

- 无密码、无敏感个人数据，RSA 通道不适用（规则文件 §2.5）。
- SQL 注入防护：全程 GORM 链式 API 加占位符绑定；关键词搜索经 likeescape.EscapeLike（internal/pkg/likeescape）转义加 `ESCAPE '\'` 子句（规则文件 §1.11）。
- 题库为系统级共享数据，所有平台账号可见可操作，无行级隔离（specs 2.1）。
- 员工侧无题库浏览入口，作答页经一次性令牌仅见被指派题目（specs 2.1，接口归 F8）。

### 7.3 数据备份

随主数据库统一备份策略，无独立要求。

---

## 八、SSOT 合规说明

| specs 需求点 | 数据库落地 | 状态 |
|-------------|-----------|------|
| 题目编号 Q-AG/Q-Scale-四位流水、唯一、超四位扩展（4.1.2 B） | question_no 系统生成只增不复用 + UNIQUE 索引 | 合规 |
| AI 题文本长度 1000/2000/500（4.1.2 E、8.3） | scenario/requirement 取 TEXT，focus_point VARCHAR(500) | 合规 |
| 驳回原因必填且留痕 ≤500（规则 3） | reject_reason VARCHAR(500)，REJECTED 非空，回退保留 | 合规 |
| 被引用次数 >0 拒删（规则 4） | reference_count 字段，删除前置校验 | 合规 |
| 量表题文本不可编辑（规则 5） | source=SCALE 行仅 status/deleted_at 可变，service 层拦截 | 合规 |
| 题目状态机 6.1（待审核/启用/已停用/已驳回/已删除） | status 四值 + deleted_at 软删除终态 | 合规 |
| 批次状态机 6.2（待审核/已关闭/已作废） | question_batches.status 三值，终态保留 | 合规 |
| 重新送审并入待审核 RESUBMIT 批次（规则 6） | batch_id 改挂 + 并批判定事务 | 合规 |
| 批次确认后关闭（规则 7） | CLOSED 终态，重复确认返 1706 | 合规 |
| 量表引入唯一性与并存、作废释放引入位（规则 8） | scale_key + 软删行可见性判定 | 合规 |
| 并发冲突反馈（规则 9） | questions.version 乐观锁 | 合规 |
| 生成失败整批作废零残留（4.3.4 规则 3） | staging 暂存 + 终态清空，失败不建批；MySQL 列容量经 migrateDB 方言钩子升 MEDIUMTEXT | 合规 |
| 批次号 #G/#S/#R+MMdd 同日序号（4.1.2 C） | batch_no 系统生成 + UNIQUE 兜底 | 合规 |
| 作答方式（量表 Likert 5 级/AI 对话作答） | source 派生，不落库 | 合规 |

**specs 与规则文件冲突处理：** specs 2.3 鉴权矩阵含 PUT/DELETE 方法，按规则文件 §2.1 收敛到 GET/POST（技术实现层面规则文件优先），POST 以路径区分动作，与 DIM 域先例一致。specs 4.3.3 生成按钮「同步 LLM 生成请求」按 specs 4.3.4 规则 3 的授权（进度机制由接口设计阶段确定）落地为 Asynq 异步任务 + 前端轮询，页面交互语义不变（同步等待、离开放弃），HTTP 侧不再维持长连接。

**字段长度来源汇总：** specs 明确的长度（scenario 1000、requirement 2000、focus_point 500、reject_reason 500）全部遵从；编号/批次号/标题按规则文件 §1.6 语义档取值并在字段说明标注。

---

## 九、变更记录

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|---------|------|
| v1.0 | 2026-09-23 | 初始版本：questions、question_batches、question_generations 三表，量表模板内置、九型维度引入事务 ensure | lixuetao |
