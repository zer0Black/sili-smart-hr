# T2 修复后重新验证证据（P4_LOG_001/01/T2，终审第二轮修复）

- 提交：edd1d7ad9076dd047f9c257c77b643919e00da13（test(log): 仓储测试雪花回调补切片分支）
- 背景：终审第二轮发现 Important（内联雪花回调缺切片分支，InsertBatch 雪花断言虚假覆盖），最终修复者已修复，受影响任务 T2 重新登记提交与验证。

## 修复内容核对
- operation_log_test.go:36-50：内联回调改 switch 双分支，补 `*[]domain.OperationLog` 切片分支逐元素赋雪花 ID，与生产 assignSnowflakeID 对齐。
- operation_log_test.go:76-88：InsertBatch 挪到 Insert 之前（空表 rowid 从 1 起，避免 max+1 掩盖量级断言）。
- operation_log_test.go:97-101：断言增强为 `row.ID < 1e15` 失败（量级断言区分雪花与 rowid 回填）。
- 修复者按评审建议做了断言变红验证：bug 态回调下 TestOperationLogRepositoryInsert 变红（got 1），补切片分支后恢复绿态。

## 验收锚点重跑
- 控制器实跑：go test ./internal/repository -run TestOperationLogRepository → ok 0.311s（6/6）。

## 控制器自检
- 核心断言仍覆盖（六测试函数未删，断言增强）。
- 文件清单：仅 repository/operation_log_test.go，与修复范围一致。
