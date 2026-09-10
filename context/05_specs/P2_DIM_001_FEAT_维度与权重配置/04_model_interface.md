# 维度与权重配置 数据库模型设计文档

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

**数据库类型：** 多库可切换，按 `SQL_DSN` 前缀选 dialector（SQLite/MySQL/PostgreSQL），生产推荐 PostgreSQL 或 MySQL。规则文件 §1.1。

**字符集：** MySQL utf8mb4；PostgreSQL UTF-8。

**建表方式：** GORM AutoMigrate 为主（架构文档 4.6），下文 GORM 模型 struct 为建表权威，MySQL/PostgreSQL DDL 仅供字段语义参考。

**表命名：** 沿用 GORM 默认 NamingPolicy 实体复数化（规则文件 §1.9），不强制模块前缀。

---

## 二、设计总览

### 2.1 实体识别

业务实体两层：

| 实体 | 表名 | 形态 | 说明 |
|------|------|------|------|
| 维度 | `dimensions` | 多行业务表 | 承载四模块全部叶子维度，统一一张表 |
| 活跃度规则 | `dimension_settings` | 单行配置表 | 系统级单例，活跃/低频判定阈值 |

### 2.2 关键设计决策

**四模块与分组不入库。** 使用活跃度、AI 使用能力、AI 管理能力、九型人格四个模块是固定系统级单例（specs 1.2、术语表），AI 使用能力下的底层能力/上层能力两个分组同样是固定两层结构。它们在前端硬编码，不建表、不入库。`dimensions` 表用 `module_code` 与 `group_code` 字段记录维度的归属，模块与分组本身的展示属性（名称、数据来源、是否参考性）由前端据 `module_code` 派生。

**统一维度表而非分模块建表。** 四模块维度共享相同的字段集（名称、编码、权重、启停、评分锚点等），差异仅在数据来源与提示词适用性，用 `data_source` 与可空的 `prompt` 区分即可。分模块建表会引入四张结构近乎一致的表，无收益反而增加评估引擎读取口径的分支成本。

**活跃度阈值独立单行表。** 阈值是模块级全局配置而非维度级属性，挂在维度上语义不通，单列 `dimension_settings` 单行表承载。

### 2.3 ER 图

```
dimensions（叶子维度，四模块统一）
   module_code  ──[逻辑归属]──  四模块（前端硬编码，不入库）
   group_code   ──[逻辑归属]──  AI_USAGE 底层/上层（前端硬编码，不入库）

dimension_settings（系统级单例配置）
   无关联，独立单行
```

两表之间无关联。活跃度阈值是全局配置，不挂具体维度。

---

## 三、表结构定义

### 3.1 核心业务表：dimensions

**表名：** `dimensions`

**用途：** 承载能力模型全部叶子维度，覆盖使用活跃度、AI 使用能力、AI 管理能力、九型人格四个模块。一条记录对应一个可被评估引擎读取的评分口径单元。

---

**GORM 模型 struct（建表权威）：**

```go
// Dimension 能力模型叶子维度，四模块统一承载
type Dimension struct {
    ID             int64          `gorm:"primaryKey" json:"id,string"`
    Code           string         `gorm:"type:varchar(64);not null;uniqueIndex:uk_dimension_code" json:"code"`   // 维度编码，系统生成，全大写下划线，UNIQUE 索引兜底查重 TOCTOU（软删改写占位码释放原 code，见规则文件 §1.10 兜底方案）
    Name           string         `gorm:"type:varchar(64);not null" json:"name"`                              // 维度名称，2~30 字符
    ModuleCode     string         `gorm:"type:varchar(32);not null;index:idx_module_group" json:"module_code"` // 所属模块：ACTIVITY/AI_USAGE/AI_MGMT/ENNEAGRAM
    GroupCode      *string        `gorm:"type:varchar(32);index:idx_module_group" json:"group_code"`          // 所属分组，仅 AI_USAGE 非 nil：BASE/UPPER，其余模块为 nil
    DataSource     string         `gorm:"type:varchar(32);not null" json:"data_source"`                       // 数据来源：RULE/CONVERSATION/TEST，与 module_code 联动
    Prompt         string         `gorm:"type:text" json:"prompt"`                                            // 评分提示词，仅 CONVERSATION 维度非空，≤2000 字符
    Anchor         string         `gorm:"type:varchar(500);not null" json:"anchor"`                           // 评分锚点，绝对分标准，必填，≤500 字符
    Weight         int            `gorm:"type:int;not null" json:"weight"`                                    // 聚合权重百分比 0~100，业务层显式置值
    IncludeOverview bool          `gorm:"not null" json:"include_overview"`                                   // 是否参与总览分，业务层显式置值（不加 default tag）
    Enabled        bool           `gorm:"not null" json:"enabled"`                                            // 是否启用，业务层显式置值
    IsReference    bool           `gorm:"not null" json:"is_reference"`                                       // 是否参考性维度，由 module_code 派生（仅 ENNEAGRAM 为 true）
    Description    string         `gorm:"type:varchar(300)" json:"description"`                               // 维度说明，≤300 字符
    Version        int            `gorm:"type:int;not null" json:"version"`                                   // 乐观锁版本号，新建置 1，更新自增
    DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`                                                     // 软删除标记，json:"-" 不回显
    CreatedAt      time.Time      `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt      time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}
```

**MySQL DDL（参考，实际以 AutoMigrate 为准）：**

```sql
CREATE TABLE `dimensions` (
  `id` BIGINT NOT NULL COMMENT '主键ID（雪花ID）',
  `code` VARCHAR(64) NOT NULL COMMENT '维度编码，系统生成，业务层校验唯一',
  `name` VARCHAR(64) NOT NULL COMMENT '维度名称，2~30 字符',
  `module_code` VARCHAR(32) NOT NULL COMMENT '所属模块：ACTIVITY/AI_USAGE/AI_MGMT/ENNEAGRAM',
  `group_code` VARCHAR(32) NULL COMMENT '所属分组，仅 AI_USAGE 非 null：BASE/UPPER',
  `data_source` VARCHAR(32) NOT NULL COMMENT '数据来源：RULE/CONVERSATION/TEST',
  `prompt` TEXT NULL COMMENT '评分提示词，仅 CONVERSATION 维度非空',
  `anchor` VARCHAR(500) NOT NULL COMMENT '评分锚点，必填',
  `weight` INT NOT NULL COMMENT '聚合权重百分比 0~100',
  `include_overview` TINYINT(1) NOT NULL COMMENT '是否参与总览分',
  `enabled` TINYINT(1) NOT NULL COMMENT '是否启用',
  `is_reference` TINYINT(1) NOT NULL COMMENT '是否参考性维度',
  `description` VARCHAR(300) NULL COMMENT '维度说明',
  `version` INT NOT NULL COMMENT '乐观锁版本号',
  `deleted_at` DATETIME NULL COMMENT '软删除标记',
  `created_at` DATETIME NULL COMMENT '创建时间',
  `updated_at` DATETIME NULL COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE INDEX `uk_dimension_code` (`code`),
  INDEX `idx_module_group` (`module_code`, `group_code`),
  INDEX `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='能力模型叶子维度表';
```

**PostgreSQL DDL（参考）：**

```sql
CREATE TABLE "dimensions" (
  id BIGINT NOT NULL,
  code VARCHAR(64) NOT NULL,
  name VARCHAR(64) NOT NULL,
  module_code VARCHAR(32) NOT NULL,
  group_code VARCHAR(32),
  data_source VARCHAR(32) NOT NULL,
  prompt TEXT,
  anchor VARCHAR(500) NOT NULL,
  weight INTEGER NOT NULL,
  include_overview BOOLEAN NOT NULL,
  enabled BOOLEAN NOT NULL,
  is_reference BOOLEAN NOT NULL,
  description VARCHAR(300),
  version INTEGER NOT NULL,
  deleted_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE,
  updated_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT "dimensions_pkey" PRIMARY KEY (id)
);
CREATE INDEX "idx_dimensions_module_group" ON "dimensions"("module_code", "group_code");
CREATE UNIQUE INDEX "uk_dimension_code" ON "dimensions"("code");
CREATE INDEX "idx_dimensions_deleted_at" ON "dimensions"("deleted_at");
```

---

**字段说明：**

| 字段名 | 类型（MySQL） | 类型（PostgreSQL） | 类型（SQLite） | 必填 | 默认值 | 说明 |
|--------|------|------|------|------|--------|------|
| id | BIGINT | BIGINT | INTEGER | 是 | 雪花生成 | 主键ID（雪花ID，应用层生成）[长度来源：规则文件 §1.2] |
| code | VARCHAR(64) | VARCHAR(64) | TEXT | 是 | 系统生成 | 维度编码，模块前缀加名称拼音全大写下划线，≤40 字符（specs 规则4），列宽留余量取 64。UNIQUE 索引兜底查重 TOCTOU，软删改写占位码（规则文件 §1.10 兜底方案）[长度来源：specs/规则文件 §1.6] |
| name | VARCHAR(64) | VARCHAR(64) | TEXT | 是 | 无 | 维度名称，2~30 字符（specs 4.1.2 B），列宽留余量取 64 [长度来源：specs/规则文件 §1.6] |
| module_code | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 无 | 所属模块枚举：ACTIVITY（使用活跃度）/AI_USAGE（AI 使用能力）/AI_MGMT（AI 管理能力）/ENNEAGRAM（九型人格），提交后不可改 |
| group_code | VARCHAR(32) | VARCHAR(32) | TEXT | 否 | NULL | 所属分组枚举：BASE（底层能力）/UPPER（上层能力）。仅 module_code=AI_USAGE 时非空，其余模块为 NULL |
| data_source | VARCHAR(32) | VARCHAR(32) | TEXT | 是 | 无 | 数据来源枚举：RULE（规则统计）/CONVERSATION（对话分析）/TEST（主动测试）。与 module_code 联动（specs 规则8），提交后只读 |
| prompt | TEXT | TEXT | TEXT | 否 | NULL | 评分提示词，喂给 LLM 的维度评分指令。仅 data_source=CONVERSATION 时必填非空（specs 规则6），≤2000 字符 [长度来源：specs 4.1.2 B] |
| anchor | VARCHAR(500) | VARCHAR(500) | TEXT | 是 | 无 | 评分锚点，固定分值区间对应的能力表现描述，绝对分标准，必填（specs 规则7），≤500 字符 [长度来源：specs 4.1.2 B] |
| weight | INT | INTEGER | INTEGER | 是 | 业务层置值 | 聚合权重百分比 0~100 整数。ACTIVITY/ENNEAGRAM 强制 0（specs 规则5）。不加 default tag，新建由业务层显式置值 |
| include_overview | TINYINT(1) | BOOLEAN | INTEGER | 是 | 业务层置值 | 是否参与总览分聚合。ACTIVITY/ENNEAGRAM 强制 false。布尔字段不加 default tag（规则文件 §1.5），业务层显式置值 |
| enabled | TINYINT(1) | BOOLEAN | INTEGER | 是 | 业务层置值 | 是否启用进入评估。新维度强制 true（specs 6.1），可切换停用。业务层显式置值 |
| is_reference | TINYINT(1) | BOOLEAN | INTEGER | 是 | 业务层置值 | 是否参考性维度，由 module_code 派生：仅 ENNEAGRAM 为 true（specs 术语表），其余 false。冗余字段便于查询过滤 |
| description | VARCHAR(300) | VARCHAR(300) | TEXT | 否 | NULL | 维度含义、信号来源与评判要点说明，≤300 字符（specs 4.1.2 B）[长度来源：specs] |
| version | INT | INTEGER | INTEGER | 是 | 1 | 乐观锁版本号，新建置 1，每次更新自增。更新/删除校验一致性防并发覆盖（specs 规则9） |
| deleted_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 否 | NULL | 软删除标记，GORM DeletedAt 自动维护。非 NULL 表示已软删除，查询自动过滤 |
| created_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 是 | autoCreateTime | 创建时间，GORM 自动维护 |
| updated_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 是 | autoUpdateTime | 更新时间，GORM 自动维护 |

**索引说明：**

| 索引名 | 类型 | 字段 | 用途 |
|--------|------|------|------|
| PRIMARY | PRIMARY KEY | id | 主键索引，雪花 ID |
| idx_module_group | INDEX | (module_code, group_code) | 维度树按模块与分组聚合查询，覆盖四模块树渲染主路径 |
| uk_dimension_code | UNIQUE | code | 维度编码查询与唯一性兜底：拦截查重与 Create 之间的 TOCTOU 并发窗口（规则文件 §1.10 兜底方案），软删时 repository 改写占位码释放原 code |
| idx_deleted_at | INDEX | deleted_at | GORM 软删除自动查询过滤 |

**业务规则：**

- 编码唯一性（规则文件 §1.10 兜底方案）：`code` 设 `uk_dimension_code` UNIQUE 索引兜底查重与 Create 之间的 TOCTOU 并发窗口，撞键映射 1202。软删时把 code 改写为占位码（`原code__D<id>`）释放原 code 供新建复用（MySQL 不支持 partial index，三库通吃只能走占位码改写）；索引落地前的存量数据（软删行、查重窗口期产生的重复行）由 migrateDB 的幂等收敛钩子统一改写。编码由系统生成（specs 规则4），冲突时追加序号去重，序号耗尽或超长返回 1209。
- 布尔字段无 default tag（规则文件 §1.5）：`include_overview`/`enabled`/`is_reference` 三个布尔字段模型层不加 `default` tag，规避 MySQL 与 PostgreSQL 布尔默认值规范化差异导致 AutoMigrate 抖动，新建记录由 service 层按模块联动规则（specs 4.2.4 规则1）显式置值。
- 软删除（规则文件 §1.3）：`deleted_at gorm.DeletedAt`，GORM 内建软删除，查询自动过滤，Delete 时自动赋值。承载 specs 6.1 已删除终态。
- 乐观锁：`version` 字段配合 GORM 乐观锁机制，更新时 WHERE 附带 `version = ?`，更新成功自增；不一致则影响行数 0，service 层判定为并发冲突返 1104。
- 不可变字段：`code`/`module_code`/`group_code`/`data_source` 在编辑接口（POST /update）不接收，service 层更新时只写可变字段（specs 4.1.2 B、规则8）。
- 模块联动派生：`data_source`/`is_reference` 可由 `module_code` 派生，但落库冗余存储以便评估引擎单表读取评分口径，无需 JOIN 或前端常量映射。

---

### 3.2 核心业务表：dimension_settings

**表名：** `dimension_settings`

**用途：** 系统级单例配置表，承载使用活跃度模块的活跃/低频判定阈值。全表仅一行记录，首次启动由 seed 写入默认值。

---

**GORM 模型 struct（建表权威）：**

```go
// DimensionSetting 维度域系统级单例配置，当前承载活跃度判定阈值
type DimensionSetting struct {
    ID                    int64     `gorm:"primaryKey" json:"id,string"`
    ActiveThreshold       int       `gorm:"type:int;not null" json:"active_threshold"`         // 活跃判定下限，1~999，有效对话达此值判活跃
    LowFrequencyThreshold int       `gorm:"type:int;not null" json:"low_frequency_threshold"`  // 低频判定下限，1~999，须小于活跃下限
    CreatedAt             time.Time `gorm:"autoCreateTime" json:"created_at"`
    UpdatedAt             time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
```

**MySQL DDL（参考）：**

```sql
CREATE TABLE `dimension_settings` (
  `id` BIGINT NOT NULL COMMENT '主键ID，单行表固定 SingleRowID(=1)，首启 seed 写入',
  `active_threshold` INT NOT NULL COMMENT '活跃判定下限，1~999',
  `low_frequency_threshold` INT NOT NULL COMMENT '低频判定下限，1~999，须小于活跃下限',
  `created_at` DATETIME NULL COMMENT '创建时间',
  `updated_at` DATETIME NULL COMMENT '更新时间',
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='维度域系统级单例配置表';
```

**PostgreSQL DDL（参考）：**

```sql
CREATE TABLE "dimension_settings" (
  id BIGINT NOT NULL,
  active_threshold INTEGER NOT NULL,
  low_frequency_threshold INTEGER NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE,
  updated_at TIMESTAMP WITH TIME ZONE,
  CONSTRAINT "dimension_settings_pkey" PRIMARY KEY (id)
);
```

---

**字段说明：**

| 字段名 | 类型（MySQL） | 类型（PostgreSQL） | 类型（SQLite） | 必填 | 默认值 | 说明 |
|--------|------|------|------|------|--------|------|
| id | BIGINT | BIGINT | INTEGER | 是 | 固定 SingleRowID(=1) | 主键ID。单行表固定主键防 seed 重入（并发双 seed 撞主键由 UniqueViolation 容错），存量库主键为雪花值的已 seed 行兼容保留 |
| active_threshold | INT | INTEGER | INTEGER | 是 | 10（seed） | 活跃判定下限，1~999 整数。本评估区间有效对话达此值判为活跃（specs 4.1.2 C） |
| low_frequency_threshold | INT | INTEGER | INTEGER | 是 | 5（seed） | 低频判定下限，1~999 整数，须 < active_threshold。达此值且低于活跃下限判低频，低于此值判未使用 |
| created_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 是 | autoCreateTime | 创建时间 |
| updated_at | DATETIME | TIMESTAMP WITH TIME ZONE | TEXT | 是 | autoUpdateTime | 更新时间 |

**索引说明：**

| 索引名 | 类型 | 字段 | 用途 |
|--------|------|------|------|
| PRIMARY | PRIMARY KEY | id | 主键索引，单行表 |

**业务规则：**

- 单行约束：表设计为仅一行系统级配置，seed 与自愈补行都以固定主键 `domain.SingleRowID`(=1) 写入，service 层读取固定取首行，保存即 UPDATE 该行。无软删除需求（配置永不可删），不引入 `deleted_at`。
- 阈值校验（specs 4.1.2 C）：service 层校验 `1 ≤ low_frequency_threshold < active_threshold ≤ 999`，违反返 1108。
- 无乐观锁：运营体量小，阈值变更低频，最后写入覆盖可接受，不引入 version 字段。
- 统计区间不落本表：specs 4.1.2 C 明确统计区间跟随系统配置页评估区间，由 config 域 Feature 承载，本表不存区间值。

---

## 四、种子数据

### 4.1 dimension_settings 首启 seed

系统首启时由 `migrateDB` 编排 seed 写入一行默认配置（specs 4.1.2 C 默认值：活跃下限 10、低频下限 5）。仅当表为空时写入，幂等。

```go
// 首启 seed，表为空时写入默认活跃度阈值（specs 4.1.2 C 默认值）
// 单行表固定主键 SingleRowID(=1) 防重入：并发双 seed 撞主键由 UniqueViolation 容错收敛；
// Where("1 = 1") 兼容存量库主键为雪花值的已 seed 行。
var setting domain.DimensionSetting
err := db.Where("1 = 1").Attrs(domain.DimensionSetting{
    ID:                    domain.SingleRowID,
    ActiveThreshold:       10,
    LowFrequencyThreshold: 5,
}).FirstOrCreate(&setting).Error
if dberr.UniqueViolation(err) {
    err = nil // 并发双 seed 对端胜出，行已落库
}
```

### 4.2 dimensions 默认维度 seed

系统首启时由 `migrateDB` 编排 seed 写入默认对话分析维度：AI 使用能力 8 维（底层 4 维 + 上层 4 维），均为 AI_USAGE 模块、CONVERSATION 数据来源、启用态、参与总览分，权重合计 100（15/12/10/13 + 15/12/13/10）。锚点与提示词口径源自 UI 原型 dimension-config-list.html，2026-09-10 经 4 个真实会话（重工具/高互动/计划驱动/技能驱动四形态）全链实测后重写为证据锚定版：每维提示词给出指向评分员实际可见证据形态（逐会话块 summary/instruction/behavior 与统计段字段）的判读规则，锚点档位改为过程信号可判表述（去除团队复用、落地去向等日志不可见档），并内置人机边界规则（tool_top10 属 AI 侧行为仅作参考、AI 自主编排不计入用户能力、无创建证据一律 insufficient 禁止顺延推断）。`dimensions` 表存在任何行（含软删行，Unscoped 计数）即跳过 seed，运营维护过的库不触碰；整批写入包事务，并发双实例同窗空库撞 `uk_dimension_code` 由 UniqueViolation 容错收敛。AI 管理能力 5 维与九型人格维度不随本 seed 写入，分别由对应 Feature（F5/F7、量表引入）落地。实现见 [seed_dimensions.go](../../../hr-backend/internal/model/seed_dimensions.go)。

---

## 五、字典数据处理

遵循规则文件 §1.8，项目不引入 `system_dict_type`/`system_dict_data` 字典表体系。本功能的枚举值处理如下：

| 枚举字段 | 取值 | 承载方式 |
|---------|------|---------|
| module_code | ACTIVITY / AI_USAGE / AI_MGMT / ENNEAGRAM | Go 侧常量 + 数据库 VARCHAR 列 |
| group_code | BASE / UPPER | Go 侧常量 + 数据库 VARCHAR 列 |
| data_source | RULE / CONVERSATION / TEST | Go 侧常量 + 数据库 VARCHAR 列 |
| enabled / include_overview / is_reference | true / false | 布尔字段 |

字段说明不使用 `[字典：xxx]` 标注，亦不生成字典表 INSERT 语句。

---

## 六、索引策略

### 6.1 主键索引

所有表主键 `id` 为雪花 ID（int64/BIGINT），应用层生成，GORM 全局 Create 回调透明赋值（规则文件 §1.2）。无自增。

### 6.2 维度树查询索引

`dimensions` 表的维度树渲染主路径是按模块与分组聚合全量维度，复合索引 `idx_module_group (module_code, group_code)` 覆盖。四模块维度总量有限（数十量级），树查询走全表扫描复合索引即可，无需进一步优化。

### 6.3 编码唯一性索引

`uk_dimension_code (code)` UNIQUE 索引承载新增时的编码冲突兜底（TOCTOU 并发窗口）与详情查询。软删除经占位码改写协同，见规则文件 §1.10 兜底方案。

### 6.4 分表分库

不适用。维度配置为系统级单例数据，总量在数十到百条量级，单表单库完全承载，无分片需求。

---

## 七、性能与安全

### 7.1 性能

- 维度配置为低频读写、高频读取（评估跑批读取评分口径）的场景。跑批读取由评估引擎经 repository 一次性拉取全量启用维度到内存，单次查询，无 N+1。
- 热点配置缓存：架构文档 1.1 提及 Redis 承载维度权重等评估运行前必须就绪的热点配置缓存，降低跑批期内配置读取开销。缓存键建议 `dimension:active:snapshot`（全量启用维度与权重的序列化快照），缓存失效策略为配置变更后由 service 层主动驱逐（Cache-Aside），TTL 兜底。缓存层属运行时优化，本 Feature 落库为权威源。

### 7.2 安全

- 无密码、无敏感个人数据，不涉及加密存储与脱敏展示。
- SQL 注入防护：全程 GORM 链式 API 加占位符绑定。本功能维度树与详情不接收用户输入做 LIKE 匹配（树全量返回、详情按主键查），无通配符转义场景（规则文件 §1.11）。
- 软删除保障历史画像可解释：删除维度仅置 `deleted_at`，已产出的历史评分与画像快照保留不重算（specs 规则3）。

### 7.3 数据备份

随主数据库统一备份策略，无独立要求。

---

## 八、SSOT 合规说明

| specs 需求点 | 数据库落地 | 状态 |
|-------------|-----------|------|
| 维度名称 2~30 字符（4.1.2 B） | name VARCHAR(64)，specs 上限 30 列宽留余量 | 合规 |
| 维度编码 ≤40 字符（规则4） | code VARCHAR(64)，specs 上限 40 列宽留余量 | 合规 |
| 评分提示词 ≤2000 字符（4.1.2 B） | prompt TEXT | 合规 |
| 评分锚点 ≤500 字符（4.1.2 B） | anchor VARCHAR(500) | 合规 |
| 维度说明 ≤300 字符（4.1.2 B） | description VARCHAR(300) | 合规 |
| 聚合权重 0~100 整数（4.1.2 B） | weight INT，业务层校验范围 | 合规 |
| 活跃/低频阈值 1~999 整数（4.1.2 C） | active_threshold/low_frequency_threshold INT | 合规 |
| 数据状态：启用/停用/已删除（6.1） | enabled 布尔 + deleted_at 软删除 | 合规 |
| 编码自动生成不可手填（规则4） | code 由 service 层生成，create 接口不接收 | 合规 |
| 评分提示词仅对话分析维度（规则6） | prompt 可空，业务层按 data_source 校验必填 | 合规 |
| 配置延迟到下次评估生效（规则1） | 接口仅持久化，不触发重算，引擎跑批读取 | 合规 |
| 并发冲突反馈（规则9） | version 乐观锁 + 错误码 1104 | 合规 |

**字段长度来源汇总：** specs 明确的字段长度（name 30、code 40、prompt 2000、anchor 500、description 300）全部遵从；列宽按规则文件 §1.6 在 specs 上限基础上留余量取整（VARCHAR(64)/VARCHAR(500)/VARCHAR(300)），prompt 取 TEXT。

---

## 九、变更记录

| 版本 | 日期 | 变更内容 | 作者 |
|------|------|---------|------|
| v1.0 | 2026-08-11 | 初始版本，dimensions 与 dimension_settings 两表 | lixuetao |
| v1.1 | 2026-09-09 | code 唯一性改 UNIQUE 索引 + 软删占位码方案（规则文件 §1.10 兜底方案）；seed 主键改固定 SingleRowID；同步 GORM struct 与 DDL | lixuetao |
