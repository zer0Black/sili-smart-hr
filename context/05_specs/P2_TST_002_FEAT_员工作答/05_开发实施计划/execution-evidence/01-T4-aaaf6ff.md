# T4 错误码 19xx 段定义 验证证据

- 提交: aaaf6fff450544b809699ebe9ccfd40c69154e3d
- 验证命令: cd hr-backend && go build ./... && go test ./internal/pkg/errcode -count=1
- 控制器复跑结果: BUILD-OK；ok sili-smart-hr/backend/internal/pkg/errcode 0.070s
- diff 核对: 常量 AnswerTokenInvalid=1901 / AnswerReplyInvalid=1902 / AnswerIncomplete=1903，文案 "answer token invalid" / "answer reply invalid" / "answer incomplete"，与任务契约逐字一致；段头注释与文件头段位说明同步更新

## 控制器自检（三项）
- 检查项一（核心断言存在性）: 纯常量登记任务，errcode.Message(AnswerTokenInvalid)="answer token invalid" 由 errcode_test.go 新增 TestMessages 表用例承载（编译+测试通过），通过
- 检查项二（BRn 落地证据）: 任务无 [BRn] 索引（纯技术任务），跳过
- 检查项三（文件清单核对）: git diff 4dc57d5..aaaf6ff = {pkg/errcode/errcode.go 修改, pkg/errcode/errcode_test.go 修改}，errcode_test.go 为合理测试衍生文件，通过

## 结论
三项自检全部通过。
