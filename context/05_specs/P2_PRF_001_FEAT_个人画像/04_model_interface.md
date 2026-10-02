# 个人画像 数据库模型设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_PRF_001_FEAT_个人画像 |
| 模块代号 | PRF（个人画像域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-01 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[03_api_interface.md](03_api_interface.md) |

---

## 1. 适用性结论：本 Feature 不新建表、不改动既有表

**结论：数据模型设计不适用（无 DDL 交付），本文档承载消费表清单与查询口径契约。**

适用性判定（按技能交付要求逐项核对）：

| 判定项 | 结论 | 规格依据 |
|-------|------|---------|
| 是否有新增持久化实体 | 否 | specs §1.1：本 feature 是纯只读消费域，「不产生任何新评分、不调 LLM、不改上游数据，全部数据来自已落库的四张表与上游人员名单」 |
| 是否有既有表的字段变更 | 否 | specs 第 4/5 章全部字段的数据来源均指向既有表列（见 §2 消费清单），无一处要求扩列 |
| 是否有 API 变化 | 是 | [03_api_interface.md](03_api_interface.md) 适用，3 个新接口 |
| 纯前端展示且无契约变化 | 否 | 有新接口（A1/A2/B1），API 设计适用 |

按「API 变化 + 数据库无变化」的组合，本文档的交付形态是：消费表清单（§2）、只读查询的索引与性能核对（§3）、不落库计算的字段口径（§4），供开发计划核对取数范围与 FM 验收，不产出任何 DDL、字段说明表格或字典 INSERT 语句（无新建表故无适用对象）。

---

## 2. 消费表清单（全部只读）

本域读六张既有表加一个上游系统，读写属性为**全部只读**（specs §1.1；架构 2.2 结果消费层对上游数据是只读关系、经 repository 直接读取）。表结构定义与字段说明归各自 Feature 的模型文档，此处只登记取数口径。

| 表 | 来源 Feature | 取数口径 | 消费场景 |
|----|-------------|---------|---------|
| activity_stats | P2_TECH_005（F6 链路落库） | 每人最新周期行（列表，03 §1.9）；所选区间行（详情） | 活跃度列/筛选、概览条、区间列表并集来源之一 |
| dimension_scores | P2_TECH_005 + P2_TST_001（conversation 源与 active_test 源并列落库） | 每模块最新聚合周期同窗维度行（列表）；所选区间行 + 同人各期行（详情走势，近 4 期）；所选区间全公司行（公司均分，剔除 insufficient 与 failed） | 模块分旁降权判定、短板集合、维度明细（分数/状态/解读/证据）、trend、company_avg |
| aggregate_scores | P2_TECH_005（scorer 聚合落库） | 每人每模块最新聚合行（列表）；所选区间行 + 区间列表下一项行（详情较上期） | 两模块总分、较上期变化、评估时间、区间列表并集来源之一 |
| assessment_test_results | P2_TST_001（enneagram 阅卷落库） | 经 task_id JOIN assessment_test_tasks 取 staff_name，每人最新 grading_status=scored 且 main_type 非空行（03 §1.8 过滤降级占位行） | 九型主型（列表列、概览条、九型区） |
| assessment_test_tasks | P2_TST_001 | 仅作 results 取人的 JOIN 中间表（test_type=enneagram 的任务行） | 九型判型行到人的映射 |
| dimensions | P2_DIM_001 | 启用且 module ∈ {AI_USAGE, AI_MGMT} 的维度集合（短板筛选选项、维度明细基准集合） | 短板维度下拉、详情维度行组装、missing_count 基准 |
| sili-smart-api 用户体系 | 外部（integration/userapi） | WalkStaffPages 翻页全员名单（列表基表）；keyword 精确匹配单人（详情姓名解析） | 列表人群基表、详情姓名 |

人员归属键：以上表的 `token_name` 列与上游 `staff_name`（username）同源（P2_ASM_001 §1.6 既定契约，03 §1.3 转记），联接键为字符串等值匹配。

---

## 3. 索引与查询性能核对

既有表的索引已覆盖本域查询形态，逐表核对（索引定义为来源 Feature 交付，此处只验证匹配性）：

| 查询形态（03 §4.3） | 命中索引 | 判定 |
|---------------------|---------|------|
| dimension_scores 按 token_name + period 双界精确取行 | uk_person_period_dim (token_name, period_start_at, dimension_code) 前缀 | 已覆盖（评估与聚合链路同款查询，TECH_005 已验证） |
| dimension_scores 按 token_name 全期取行走势 | uk_person_period_dim 首列 token_name | 已覆盖（同人行全周期扫描，列内含全部所需字段） |
| dimension_scores 按所选区间全公司取行（公司均分） | idx_period_status (period_start_at, status) | 已覆盖（period 等值 + status 过滤，行数级为全员 × 维度数，百人级量级无压力） |
| aggregate_scores 按人全期取（最新行/上一区间行） | uk_person_period_module (token_name, period_start_at, module) 前缀 | 已覆盖 |
| activity_stats 按人取最新/区间行 | uk_person_period (token_name, period_start_at) 前缀 | 已覆盖 |
| assessment_test_results 按 tasks JOIN 取人 | uk_result_task（task_id 点查）+ tasks 表 idx_staff_name / idx_type_status_created | 已覆盖（先按 staff_name+test_type 圈任务，再逐任务点查结果行） |
| 列表页四表批量 IN 查询（specs §5.1.4 规则2 防逐人查询） | 同上各索引（IN 列表走同索引） | 已覆盖 |

**结论：无需新增索引。** 本域是既有评估/聚合链路查询形态的只读复用，最重查询为列表页的全员四表批量 IN（百人级 × 单行/少量行），远低于这些表在跑批链路中的写入与查询压力。

---

## 4. 不落库的数据口径（查询期派生）

以下数据为展示期实时计算或规则映射，刻意不落库（specs 明文或按只读域定位推导），开发时按 03 文档执行，此处汇总防误建表：

| 数据 | 计算位置 | 不落库依据 |
|------|---------|-----------|
| 公司均分对照 | B1 查询期对全公司维度行实时聚合 | specs §4.2.4 规则4：均分为展示期实时聚合，不落库 |
| 短板集合 | 列表查询期按每人参与聚合维度最低分计算 | specs §4.1.4 规则4 为筛选判据定义，非持久化实体 |
| 置信度标签（high/medium/low） | 查询期按 evidence_json 统计摘要规则映射 | specs §4.2.4 规则5：规则映射的展示标签，非 LLM 输出字段 |
| 数据状态（complete/degraded/missing/pending） | 查询期按模块内维度行计数映射 | specs §4.2.4 规则8：展示层判定映射，specs 第 6 章明示为上游落库状态的映射 |
| 较上期变化 | 查询期按两期聚合行取整分差 | specs §5.2.2 步骤4：接口计算，无存储语义 |
| 核心结论文本 | 前端规则拼装 | specs §4.2.4 规则2：前端常量拼装，零 LLM 成本，阈值调整不发版 |
| 等级标签（优秀/良好/中等/待提升） | 前端常量映射 | specs §4.2.4 规则3：前端展示常量，与聚合权重口径解耦 |
| 导出 xlsx | 请求期流式生成 | specs §4.1.3：文件下载物，无持久化语义（03 §1.6） |

---

## 5. SSOT 合规与一致性

- [x] 无新增实体遗漏：specs 第 4/5 章无任何「新建表/新增字段」表述，§1.1 反向明示只读消费；画像快照、看板物化等易误建对象已在 §4 逐项排除。
- [x] 主键/公共字段/命名/类型约束：无新建表，规则文件 §1.2-1.9 对本域无新落点；既有表不受触碰。
- [x] 与 03 接口文档一致：消费清单（§2）与 03 §4.3 查询契约逐条对应；索引核对（§3）覆盖 03 §4.3 全部查询形态。
- [x] 架构一致：结果消费层经 repository 直接读取、数据依赖而非服务调用（架构 2.2/2.3），无本域独有 repository 写方法。

---

## 6. 不涉及的设计

- 任何 DDL、迁移钩子、AutoMigrate 登记：无新模型，`model/migrate.go` 的 allModels() 不变。
- 画像/看板快照物化表：specs §4.2.4 规则4 明确实时聚合（§4 已列）。
- Redis 缓存（名单缓存、画像缓存）：specs §4.1.4 规则1 明确不缓存上游名单（防降级误读全员规模）；画像数据量级不支撑缓存复杂度。
- 对外预留接口（specs §5.3）的凭据存储表：首期不实现（03 §4.1），凭据形态实现期确定。

---

**文档版本：** v1.0
**最后更新：** 2026-10-01
**作者：** lixuetao
