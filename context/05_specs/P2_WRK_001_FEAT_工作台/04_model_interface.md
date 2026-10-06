# 工作台 数据库模型设计文档

## 文档信息

| 项目 | 内容 |
|------|------|
| Feature | P2_WRK_001_FEAT_工作台 |
| 模块代号 | WRK（工作台域） |
| 文档版本 | v1.0 |
| 创建日期 | 2026-10-06 |
| 作者 | lixuetao |
| 依据 | [01_功能需求规格说明书](01_功能需求规格说明书.md)（SSOT）、[AGENTS_DATABASE_API_RULE.md](../../../AGENTS_DATABASE_API_RULE.md)、[architecture.md](../../03_architecture/architecture.md)、[03_api_interface.md](03_api_interface.md) |

---

## 1. 适用性结论

**status: not_applicable（零新表、零既有表变更）**

规格依据：specs §5.1.1「聚合实时计算不落库」、§5.1.4 规则1「纯只读聚合，无写入行为，聚合口径与 F10 看板接口共用的部分保持同源计算」、§4.1.4 规则5「研判与短板直接复用 F10 同源数据，工作台不新建生成逻辑、不落库」。specs §5.1.3 涉及数据表全部为 F6/F7/T5/F10 已建表的只读消费，无新增持久化实体。

本域与 F9（P2_PRF_001）、F10（P2_TMD_001）同属只读聚合域：无 domain 新模型、无 model/migrate.go 登记、无迁移钩子、无 seed、无 Redis 键。开发交付物是 service 层聚合编排与 repository 层只读查询方法（03 §4.1/§4.2），不触碰表结构。

---

## 2. 消费表清单（全部只读）

工作台查询链路读十张既有表加一个上游系统，读写属性为**全部只读**（specs §1.1 只读消费定位；架构 2.2 结果消费层经 repository 直接读取）。表结构定义归各自 Feature 的模型文档，此处登记取数口径。

| 表 | 来源 Feature | 取数口径 | 消费场景（03 W1 字段） |
|----|-------------|---------|----------------------|
| assessment_batches | P2_ASM_001 | triggered_at 最新行（空态判定与回退）；period 双界匹配行的 status 与 finished_at（最新终态定位，03 §1.4） | batch.status、batch.data_updated_at |
| assessment_alerts | P2_ASM_001 | batch_id IN 区间批次集的存在性（每批次至多一条，uk_alert_batch） | batch.alert_count 0/1 |
| assessment_configs | P2_SYS_001（单例） | 单例行 period + trigger_time 经 pipeline.NextTriggerAt 推算 | batch.next_trigger_at |
| assessment_test_tasks | P2_TST_001 | status=expired 全表计数（两类测试合计） | batch.overdue_count |
| activity_stats | P2_TECH_005 | 本期全行（三态计数与未使用合成）+ 全表行（每人最近非未使用行，列级投影 token_name/period_end_at/active_level） | trend.activity、attention 未使用人群排序 |
| dimension_scores | P2_TECH_005 + P2_TST_001 | 近 8 期窗口批量取行逐期聚合、本期全公司行聚合（剔除 insufficient/failed，< 3 人置空）；本期行个人短板集合（参与聚合维度最低分并列全选） | trend.series、profile.modules、attention.weak_modules |
| aggregate_scores | P2_TECH_005 | 本期两模块行 module_score（取整 < 60 判短板人群与排序）；三表区间并集来源之一 | current_period、trend.periods、attention 分数 |
| assessment_test_results | P2_TST_001（enneagram 阅卷落库） | 经 tasks JOIN 全员最新判型行（新增 ListAllLatestScored，过滤 grading_status=scored 且 main_type 非空，同 F10 §1.9；判型行自带 staff_name 免名单参数） | profile.enneagram |
| team_training_suggestions | P2_TMD_001 | 最新建议行（FindLatest，同 F10 取行口径） | profile.suggestion |
| dimensions | P2_DIM_001 | ListAll 返回全量维度（含停用），service 层内存过滤 enabled 且 module ∈ {AI_USAGE, AI_MGMT}（同 F10 dashboard 取数形态），得启用维度集合（雷达轴、短板判定、weak_modules 维度名基准） | profile.modules、profile.weaknesses |
| sili-smart-api 用户体系 | 外部（integration/userapi） | WalkStaffPages 翻页全员名单（活跃率分母、未使用候选、关注人群姓名） | trend.activity、attention |

人员归属键：上游四表的 `token_name` 列与 userapi `staff_name`（username）同源（P2_ASM_001 §1.6 既定契约），联接键为字符串等值匹配。本域响应不回显雪花 ID（03 §2.4），取数经仓储行投影，无 JSON 序列化路径。

---

## 3. 索引与查询性能核对

新增查询方法（03 §4.2）逐项核对既有索引匹配性（索引定义为来源 Feature 交付，此处验证匹配性）：

| 查询形态 | 命中索引 | 判定 |
|---------|---------|------|
| assessment_batches triggered_at 最新行 | idx_triggered_at | 已覆盖（排序取首行点查） |
| assessment_batches period 双界匹配全部行 | 全表扫描或 idx_status_triggered 辅助 | 已覆盖（批次行数级为周频一条加重跑，量级极小，F10 FindLatestFinishedAt 同款形态） |
| assessment_alerts batch_id IN 存在性 | uk_alert_batch（唯一索引点查） | 已覆盖（每批次至多一条，IN 集合为区间批次数，个位数） |
| assessment_test_tasks status=expired 计数 | 覆盖索引全扫或表扫（idx_type_status_created 组合列仅 (test_type, status)，status-only 等值无法前缀命中） | 已覆盖（任务表行数级为运营发起频次，量级小，无需新增索引） |
| activity_stats 本期全行 | idx_period_level (period_start_at, active_level) | 已覆盖（F10 buildActivity 同款） |
| activity_stats 全表行（最近非未使用行聚合） | 全表扫描 | 已覆盖（行数级为全员 × 期数，百人级 × 周频数十期，万行级量级；列级投影单趟扫描，无逐人查询） |
| dimension_scores 近 8 期窗口批量取行 | idx_period_status (period_start_at, status) | 已覆盖（F10 A2 同款，period_start_at IN 窗口区间集） |
| dimension_scores 本期全公司行 | idx_period_status | 已覆盖（F10 A1 同款） |
| aggregate_scores 本期两模块行 | uk_person_period_module (token_name, period_start_at, ...) 前两列或 idx 辅助 | 已覆盖（period 等值过滤；行数级为全员 × 模块数） |
| assessment_test_results 全员最新判型行 | uk_result_task（task_id 点查）+ tasks 表 idx_staff_name / idx_type_status_created | 已覆盖（自连接取最新行形态与 ListLatestScoredByStaffNames 同款，免名单参数版本；行数级为判型人数） |
| team_training_suggestions 最新建议行 | uk_suggestion_period | 已覆盖（FindLatest 既有方法，行数级同期数） |

**结论：无需新增索引。** 全部查询形态为既有评估与看板链路查询形态的只读复用，最重查询为 activity_stats 全表单趟扫描（万行级）与 8 期维度行批量 IN 聚合（F10 A2 已验证同量级），远低于这些表在跑批链路中的写入与查询压力。specs §5.1.4 规则1 批量取数（禁止逐人逐维度查询）的约束在 03 §4.1 编排契约中落地。

---

## 4. 不落库的数据口径（查询期派生）

以下数据为展示期实时计算或规则映射，刻意不落库（specs 明文或按只读消费定位推导），开发时按 03 文档执行，此处汇总防误建列建表：

| 数据 | 计算位置 | 不落库依据 |
|------|---------|-----------|
| 批次态势定位（最新终态/进行中回退） | W1 查询期内存判定 | specs §4.1.2 A 展示口径 |
| 告警 0/1 存在性计数 | W1 查询期判定 | specs §4.1.4 规则2 布尔计数 |
| 逾期任务计数 | W1 count 查询 | specs §4.1.2 A 计数口径 |
| 两模块综合分序列与环比 | W1 查询期逐期计算 | specs §4.1.2 B 同 F10 A2 口径，§5.1.4 规则1 不落库 |
| 活跃率与 pp 环比、未使用合成 | W1 查询期计数 | specs §4.1.2 B，同 F10 buildActivity 口径 |
| 维度均分雷达与共性短板 | W1 查询期聚合判定 | specs §4.1.4 规则5 复用 F10，§5.1.4 规则1 不落库 |
| 九型分布与主导型统计 | W1 查询期统计 | specs §4.1.2 C 快照口径实时统计 |
| 关注人群判定与排序（短板/未使用） | W1 查询期判定 | specs §4.1.4 规则3 展示期口径 |
| 最近活跃距今天数 | W1 查询期计算 | specs §8.1 术语表展示口径 |
| 服务端缓存 | 无 | specs §5.1.4 规则1（同 F10 §5.2.4 规则3） |

---

## 5. 数据初始化与 seed

无 seed 行、无新增系统参数键。本域零表结构变更，model/migrate.go 无登记项，计算常量（窗口 8 期、配额 5 人、判定线 60 等）为 service 包内常量随源码发版（03 §4.3），与 F10 常量同源引用。

---

## 6. SSOT 合规与一致性

- [x] 无新增实体遗漏：specs 无新增持久化实体（§5.1.3 全部为只读消费）；聚合快照、工作台物化、关注人群清单落库等易误建对象已在 §4 逐项排除。
- [x] 与 03 接口文档一致：W1 响应字段均为消费表列投影或查询期派生（§2 取数口径对应），零表结构依赖；新增仓储方法（03 §4.2）索引匹配性已核对（§3）。
- [x] 架构一致：结果消费层经 repository 直接读取、数据依赖而非服务调用（架构 2.2/2.3）；只读聚合域无 worker 任务、无 LLM 调用、无 HTTP 写路径。
- [x] 规则文件合规：无主键/公共字段/命名/类型约束落点（零新表）；雪花 ID 无 HTTP 序列化路径（03 §2.4）；无字典表、无外键、无软删除议题。

---

## 7. 不涉及的设计

- 任何既有表的 DDL 变更、索引新增、迁移钩子：消费表全部只读（§3 结论）。
- 工作台聚合物化表与 Redis 缓存键：specs §5.1.4 规则1 实时计算不落库不加缓存。
- 关注人群清单的持久化与变更通知：specs §4.1.4 规则3 展示期实时判定。
- 跳转点击埋点与访问审计表：specs 无此需求（无审计人字段追踪，规则文件 §1.3）。

---

**文档版本：** v1.0
**最后更新：** 2026-10-06
**作者：** lixuetao
