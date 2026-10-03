# 子计划 02 最终评审报告 — 不通过（重试 2/3）

评审范围：8d2628da5e6e3957cc1aeb58eded7a773c149316..5da1d5464fb4ba8e2f3315d3ec8e2006cb2173ec（第 2 轮）
评审者：最终评审子代理（独立上下文，2026-10-03）

## 裁决：不通过，2 项 Important

## 前轮修复复核
两项修复均正确：翼型空串与 '0' 双兼容；风险摘要 M = missing_count + failed_count 与后端计数无重叠，仅 failed 场景新用例补齐。

## 有效 finding（控制器已核实）

1. Important：核心结论区可见文案插值未本地化。conclusion.ts:87-88 将 scoreGrade 英文枚举（excellent/good 等）直接作 headlineParams，conclusion.ts:151、172 把 module 原始码（AI_USAGE/AI_MGMT）插入 riskCounts/trendUp/trendDown；conclusion-panel.tsx:58 t(headlineKey, params) 原样插值，中文界面渲染中英混排。zh.json 已备 module.aiUsage 与 summary.grade.* 键（控制器核实 zh.json:1178-1193 存在）未接上，违背 T7 [BR2] 可见文案走 i18next。修复：渲染前映射 t(summary.grade.*)/t(module.*) 再插值（保持纯函数可测，映射在 ConclusionPanel 渲染层做或 params 承载 i18n 键）。

2. Important：严重不足判定与风险摘要缺失口径不一致。conclusion.ts:63 isLimited 用 insufficient+missing 不含 failed；同规则（§4.2.4 规则2）的风险类 M 已按规则 8 裁定为 missing+failed，规则 8 明文「存在 failed 行或无行维度（即有数据缺失维度）」，同一规则体系两处缺失应同口径。影响：降权 1 + failed 3 时主文仍正常概括，漏出谨慎参考提示。计划 T3 断言仅以 missing_count 构造，带 plan-mandated 成分（控制器注：specs §4.2.4 规则2 原文「降权与缺失维度计数之和 ≥ 4」，缺失一词在规则 8 有明确定义含 failed，按 specs 口径补全属覆盖不全路径，非断言矛盾，授权修复）。修复：isLimited 改 insufficient+missing+failed ≥ 4，补 failed 参与阈值用例。

## ⚠️ 无法从 diff 定论
- 中文名跳转真实浏览器行为（jsdom 已覆盖路由往返编码）；Recharts 真实渲染效果。联调时过一遍。
