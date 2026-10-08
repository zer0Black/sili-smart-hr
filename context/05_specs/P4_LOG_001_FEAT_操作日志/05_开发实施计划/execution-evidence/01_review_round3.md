# 子计划 01 终审第三轮报告（P4_LOG_001/01）

- 被审范围：67eb7bf..edd1d7a
- 结论：不通过（1 项 Important，plan-mandated 待用户裁决，0 Critical）
- 终审重试计数器：3/3（已达上限，触发用户介入）

## 前两轮修复复核

- 6a0b2ca（defer 化 panic 路径）：复核通过。
- edd1d7a（仓储测试雪花回调切片分支）：复核通过，断言变红验证逻辑成立。

## Important 发现（控制器已逐条验证为有效，plan-mandated）

1. HTTP 400 参数校验失败被误记「服务内部错误」。
   - 位置：hr-backend/internal/api/middleware/operation_log.go:111-112
   - 控制器验证：属实。`if r != nil || c.Writer.Status() != http.StatusOK { msg = internalErrMsg }` 把一切非 200 的 body message 覆盖为「服务内部错误」。而存量 handler binding 失败走 `response.Fail(c, http.StatusBadRequest, errcode.BadRequest)`（account.go:54/155/173/178 等多处），响应体是 HTTP 400 + JSON {code:1400, message:...}（response.go:48-50），message 可解析。03 §1.5 明文 400 形态「同记 fail 并摘错误信息」；specs §4.1.4 规则 2 要求失败记业务错误摘要。
   - 计划 T4 的「现有 handler 的 binding 失败统一走 handleServiceError 返 HTTP 200 + code 1400」前提与存量代码不符（部分 handler 直接 Fail 400），该句为计划的事实性错误前提，故标 plan-mandated。
   - 修复方向（一行条件改动）：仅当 `r != nil || msg == ""` 时用 internalErrMsg；400 路径保留 body message。panic 时 body 为空 msg 本就空串，行为不变。

## ⚠️ 项（保持非阻断）

- DeleteBefore 的 IN 子查询带 LIMIT 在 MySQL 有 ERROR 1235/1093 风险，环境实测项。

## 口头建议（不落盘）

- recorder 侧 cleanBatchSize 常量与 repository 侧同名常量双份声明，可在子计划 02 收敛。
- 子计划 03 落 A1 DTO 时按 04 §1.3 补雪花 ID 双层 string 化。
