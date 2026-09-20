# FLOW-BATCH-RUN 观察记录

## 批次 B202609202233003（specified 李雪涛+王莹，2026-09-19 单日，298 会话）

- 22:33:18 建批（B1 API），batch-run 立即消费：拉列表 298 条（去重后 237，跨页重复 61 条被去重，符合 T4 §3.2 重复页处置）
- 投递 237 个抽取任务入 extract 队列，等待落库屏障逐 10s 轮询
- 22:33~23:0x 抽取陆续完成（单会话 LLM 调用 70-130s，含排队），个别会话 ErrLLMUpstream 经 Asynq 重试恢复
- 评估阶段：李雪涛 140 会话 → degraded（存在 failed 评分行，LLM 段失败降级保留统计）；王莹 97 会话 → success
- 批次终态 success：evaluated 2/2，failed 0，covered_session_count=237（140+97 成功侧各人会话数之和），total_session_count=237，session_fail_ratio=29.54（70/237 failed 档案，百分比口径两位小数）
- 无告警（失败人数 0/2=0% ≤ 10%）

## 各检查点结论

- RUN-LIFECYCLE：running → success 推进正确；终态判定与失败占比一致（0% ≤ 10% → success）
- RUN-PROGRESS：轮询期间进度 0/2 → 2/2 单调不减
- RUN-COVER：covered(237) ≤ total(237)，失败人员会话不计入（本批无失败人员）；degraded 人员的会话计入（140 计入，符合 §5.2.4 规则3 降级保留统计）
- RUN-ALERT：失败占比 0% 未超阈，无告警记录（对照：100% 失败的三个批次各有告警，每批次一条）
- RUN-IDEMPOTENT：degraded 的语义验证了 failed 评分行降级路径；同人同区间复评（reused）未能实测——重跑同时段会重复抽 237 个会话（档案已存在会幂等跳过，评估走 reused），成本可接受但耗时 40+ 分钟，标记为待人工决策是否追加

## 补充观察

- 跨页重复去重生效：上游返回 298 条中 61 条重复页被去重
- 会话级失败比例 29.54% 落库（供 F11 消费，不参与告警判定）
- degraded 终态计入成功侧且覆盖会话数照常累计，符合 specs §4.6 表
