# 子计划 02 作答页 终审报告（第 1 轮，不通过）

- 日期: 2026-09-30
- 审查范围: 2a295e01b41ab066ffa11a563a0ebcb90f4712e7..c42c4e81aff2c7bff7c5ca4d57f7497c479b420d
- 披露项核验: applyReplySuccess content 尾参裁决正确（reply 响应无 content 字段，内容只能从调用方闭包传入）；ref→refPrefix 拆分与 specs §4.3.2 更贴合。两处披露通过

## 有效 finding（控制器已逐条验证代码属实）
1. [Important] reply/submit 收到 1901 只 toast 不转失效态。answer-chat.tsx:87-89/:114-116，1901 分支仅 toast invalid.title 后停留作答态，输入区仍可继续发送反复失败。计划 T3 关键逻辑明文「发送/提交中收到 1901 转 invalid（specs §4.1.4 规则5）」，AnswerChat 未向 AnswerPage 暴露 onInvalid 回调，通道缺失。控制器验证：代码属实（1901 分支 toast 后 return）
2. [Important] 作答中对话进度不更新。answer-page.tsx:123/:142/:145，进度由 ctx.answered_count（一次性快照）驱动，reply 成功仅更新 script 与 currentSeq，answered_count 不再变化，员工从 0/5 答到提交进度恒显示进入页面值。specs §4.1.2 B（每题确认后更新）与 §4.1.5（每次确认后即时更新）明文要求，reply 响应已携带 answered_count 未被消费。控制器验证：代码属实（answered = ctx.answered_count 无推进路径）

## 低价值建议（口头）
- progressPercent 无组件消费，answer-page Progress 重复同公式，宜收敛单点
- answer-hero.tsx:24 疑漏跑 prettier

## 无法从 diff 定论
- 真实后端联调冒烟（断点恢复、429 后续答）留收尾阶段

## 综合裁决
不通过。2 项 Important，均为局部修复（一个回调通道、一个计数上提），不动契约与整体结构。
