# 子计划 01 终审第一轮报告（P4_LOG_001/01）

- 被审范围：67eb7bf..5a7dc46（SUBPLAN_BASE_SHA 到 HEAD）
- 结论：不通过（1 项 Important，0 Critical）
- 终审重试计数器：1/3（本轮有效失败）

## Important 发现（控制器已逐条验证为有效）

1. panic 路径零落库，且测试掩盖缺口。
   - 位置：hr-backend/internal/api/middleware/operation_log.go:66-112（组装逻辑在 c.Next() 之后裸执行，无 defer）
   - 控制器验证：属实。router.go:52 Recovery 挂最外层；handler panic 时栈展开直接跳过 OperationLog 的全部后置代码，panic → HTTP 500 的 POST 请求不会被记录。违反计划 T4 契约「HTTP 非 200（panic 被 Recovery 接住后 Fail 500）记 fail 摘要『服务内部错误』」与 03 §1.5（T4-BR4）。
   - 测试虚假覆盖验证：属实。operation_log_test.go:199-215 TestOperationLogMiddlewarePanicRecordedFail 在外层 recover 里手工重调 middleware.OperationLog(rec)(c)，构造了生产中不存在的执行序。
   - 修复方向：组装移入 defer；defer 内 recover 判定 panic 时按 fail +「服务内部错误」记录后 re-panic 交外层 Recovery 写 500；OperationLogLogin 同法；测试走真实 Recovery 链验证。

## ⚠️ 项（非阻断，交后续验证）

- DeleteBefore 的 id IN (SELECT ... LIMIT ?) 形态在 MySQL 有 ERROR 1093 已知风险（同表 IN 子查询删除），SQLite 已测、PG 支持。建议 MySQL 环境实测；如报错改派生表双层包装。本环境仅 SQLite（docker 可选 PG/MySQL），暂以注释与规格口径为据，记入待验证清单。

## 低价值建议（用户自行决定，不落盘修复）

- recorder 文件 cleanBatchSize 常量无消费方（实际批量上限在 repository 包内同名常量），两处同名易漂移。
- repository 测试内联雪花回调注释「含切片批量场景」与实际命中形态不符（断言仍有效）。

## 评审者复核记录

- 实现者自报的 Record 并发窗口经评审复核判非缺陷（方向保守，符合 specs §5.1.5 保业务内存安全意图）。
- 核心断言全部语义对齐；BRn 落地除上述 panic 项外语义正确。
