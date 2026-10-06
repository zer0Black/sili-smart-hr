# 终审第 2 轮：不通过（P2_WRK_001/01）

- 被审范围: 72a54d2e..96179aa（六任务累积 + 修复轮）
- 上轮两条 Important 均确认修复正确落地（综合分卡移出 activity 条件；weak_dims 按模块拆分作用域）
- 新 Important（控制器核实有效）: workspace.go buildWeakCandidates 候选构建 c.scores 只收录 < 60 模块行，AIUsageScore/AIMGMTScore 从 c.scores 取值，另一模块 >= 60 时总分列落 null 前端显示待评估；03 响应表与 specs §4.1.2 D 口径为「无聚合行为 null」，有聚合行即应透传
- 核实依据: workspace.go:699-708 continue 裁剪 + :747-752 取值；03:296-297；specs:130-131
- 处置: 终审失败计数 2/3；派最终修复者按「总分按有无聚合行透传，WeakModules 维持仅 < 60 模块」修复并补测试
