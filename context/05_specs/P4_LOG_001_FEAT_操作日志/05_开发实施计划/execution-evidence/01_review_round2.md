# 子计划 01 终审第二轮报告（P4_LOG_001/01）

- 被审范围：67eb7bf..6a0b2ca
- 结论：不通过（1 项 Important，0 Critical）
- 终审重试计数器：2/3（本轮有效失败）

## 第一轮修复复核

6a0b2ca defer 化修复正确且完整：登录 panic 摘要维持反枚举口径、GET 早退零副作用、re-panic 后外层 Recovery 接管写 500。三个复核点全部通过。

## Important 发现（控制器已逐条验证为有效）

1. 仓储测试的内联雪花回调缺切片分支，InsertBatch 的雪花断言是虚假覆盖。
   - 位置：hr-backend/internal/repository/operation_log_test.go:30-39（回调只做 `tx.Statement.Dest.(*domain.OperationLog)` 单条断言）
   - 控制器验证：属实。InsertBatch（repository/operation_log.go:55）传 `Create(&logs)`，Dest 是 `*[]domain.OperationLog`，类型断言必不命中；生产版 model/db.go:186-204 的 assignSnowflakeID 有 Slice 分支，测试内联版没有。文件头注释「含切片批量场景」与代码不符。
   - 后果：Recorder 主落库路径 InsertBatch 的雪花 ID 机制零测试守护。
   - 修复方向：测试回调补 `*[]domain.OperationLog` 切片分支（或复用反射版逻辑），断言批量行 id 为雪花量级。

## ⚠️ 项（非阻断，保持第一轮口径）

- DeleteBefore 的 IN 子查询直嵌目标表在 MySQL 有 ERROR 1093 风险，需 MySQL 环境实测；SQLite/PG 已验证。

## 低价值建议（用户自行决定）

- recorder 侧 cleanBatchSize 常量无消费方，与 repository 侧同名常量漂移风险。
- Recorder.Close 后滞留请求调 Record 会向已关闭 channel 发送而 panic，窗口极小且被 Recovery 兜底，可加防护。
