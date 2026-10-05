# T6 验证证据（P2_TMD_001/01/T6）

- commit: a5ebe9b2be8627b601326b40d610834541c8b51b
- base: c0b217c50bed24c05f07258c1cd370912249a413

## 验收命令（控制器重跑）

```
cd hr-backend && go test ./internal/service -run 'TestTickScan|TestGenerate_|TestMarkFailedIfExhausted' -v
```

实际输出：ok 0.233s（-count=1 非缓存；实现者报告 10 用例全 PASS，含契约 9 锚点 + MaterialAggregation 补充）。

回归（实现者报告）：go test ./internal/service -count=1 ok；go vet 无告警；go build ./... 通过。

## 控制器对两处实现决策的核验

1. 异常判定三表化（dim+agg+activity 均无行才 MarkFailed）：控制器已读 specs §5.1.5 异常表原文核对——「判定式：该 period 在 dimension_scores 与 aggregate_scores 均无任何行（两表有一即视为有素材）……仅 activity_stats 有行的正常周期（如启用首周全员未使用）不属异常，按可用素材照常生成」。specs 原文判定式与 TestGenerate_ActivityOnlyStillRuns 锚点在计划正文内互相矛盾（仅 activity 有行场景同时命中两表均无行判定），按冲突裁决表 specs 业务语义为准，实现取三表均无行口径同时满足两条 specs 明文。属覆盖不全补全，非断言改写。裁决通过。
2. NewSuggestService 追加第 10 参 inject SuggestGeneratorInjector（nil 时即计划原语义）：Generator 为具体类型、service 包既有 fake 注入惯例经小接口窄面（llm.Client 窄面同思路），属计划明示的实现自由度（「fake 需经小接口抽象…属实现自由度」）。生产装配 T7 传 nil 即计划签名语义。核验通过，交终审兜底确认 wire 装配形态。

## 控制器三项自检

1. 核心断言存在性：计划 9 个命名断言全部存在（grep 9 处定义）。通过。
2. BRn 落地证据：[BR1]-[BR6] 均有文件行号 + 测试用例名证据，逐条独占一行，格式合规。通过。
3. 文件清单核对：diff = 计划两个文件，无缺失无多余。通过。
