# 子计划 02 最终评审报告 — 不通过（重试 1/3）

评审范围：8d2628da5e6e3957cc1aeb58eded7a773c149316..2bdd34adde0c72cf775b504667e86679b12bba29
评审者：最终评审子代理（独立上下文，2026-10-03）

## 裁决：不通过，2 项 Important

## 做得好的
- contracts.ts 画像域类型与 03 A1/B1 逐字段一致（含 null 语义与枚举值）。
- exportProfiles 对 HTTP 200 + JSON blob 主路径判别准确，与 03 §1.6 一致且测试锁定。
- buildConclusion 参与聚合三条件与 03 §1.9 及后端 buildShortboardSets 口径对齐。
- 2001 回落（toast + 清 selected 重查、不走整页占位）实现与测试闭合。
- 九型中文名与后端 profile_export.go 常量逐一对齐；zh/en 1059 键差集为空。
- 各任务核心断言全部有对应测试且预期值正确。

## 有效 finding（控制器已逐条核实）

1. Important：九型翼型空串渲染空白。enneagram-panel.tsx:43-48 只对 wing_type==='0' 显示无显著翼型；后端 schema.go:125 已把 "0" 归一为空串落库，03 B1 字段表明文「无显著翼型为空串」。真实数据翼型栏显示空白，noWing 文案永不触发。修复：空串与 '0' 均走 noWing。

2. Important：风险摘要漏计 failed 维度。conclusion.ts:144-156 riskCounts 只用 insufficient_count 与 missing_count；specs §4.2.4 规则8 与 §4.2.2 D 均把 failed 归入数据缺失口径（评分卡 data_status 判定含 failed_count>0 → missing 态）。仅 1 维 failed 时评分卡显示部分维度缺失而结论区无风险条目，同页口径矛盾。修复：M 取 missing_count + failed_count。

## 控制器核实记录
- finding 1：schema.go:125 `if wing == "0" { wing = "" }`、domain assessment_test_result.go:13 注释「无显著翼型空串」、03:342 字段表，证据链完整，成立。
- finding 2：specs §4.2.2 D「failed 行与无行维度按数据缺失口径」、§4.2.4 规则8「存在 failed 行或无行维度为部分维度缺失」，与 conclusion.ts riskCounts 实现比对，成立。conclusion.test.ts:349 的既有断言是实现裁定锁定，随修复一并改。

## 低价值建议（口头，不落盘）
- 风险/趋势条目 module 插值输出 AI_USAGE/AI_MGMT 编码，中文界面中英混排，可改插 t(module.*) 键。
- profile-table.tsx:97 onQuery 中 isError 分支 refetch 多打一次旧 queryKey 请求，无功能影响。

## ⚠️ 无法从 diff 定论
- 真实浏览器导出下载链路、中文 staff_name URL 往返（jsdom 已测，浏览器实测未见证据）、Recharts 真实渲染效果，联调时过一遍。
