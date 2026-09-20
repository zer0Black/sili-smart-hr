# FLOW-UPSTREAM-FAIL 观察记录

## 改坏密钥 → 整批失败
- 23:21:0x 经 /api/integration-secret/update 改密钥为无效值（version 3）
- 发起 B202609202321001（specified 李雪涛+王莹，昨日单日窗口）
- 15 秒内批次落 failed 终态：failed_count=2/2（N/N 全员计入），状态正确
- 失败明细：每个人员 error_summary 均为「会话列表拉取失败: activity: session list fetch failed: 密钥无效 (HTTP 401)」批次级原因，符合 specs §4.3.2
- 告警表新增一条（100% 失败超阈）

## 恢复密钥 → 补跑收敛
- 恢复真实密钥（version 4）
- 按失败对象重新发起 B202609202321002（同对象同时段，staff_name 补跑口径）
- 收敛过程：王莹 reused（97 会话，档案已存在幂等复用未调 LLM，符合 specs §4.1.4 规则3）；李雪涛 degraded（140 会话，failed 评分行重评）
- 终态 success 2/2，covered=237
- 原失败批次 B202609202321001 状态保持 failed 不变（终态不可逆，符合 specs §6.2）

## 顺带验证
- RUN-IDEMPOTENT（FLOW-BATCH-RUN 的遗留检查点）：reused 终态直接证据在此拿到
- B1 补跑预填口径：仅需 staff_name 可建批（staff_id 省略验证通过）
