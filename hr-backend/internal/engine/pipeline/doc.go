// Package pipeline 是跑批管线编排子域：编排整个周期跑批的执行顺序。
// 已实现 schedule 纯函数（触发判定、周期窗口推算、下次执行推算、停滞判定）
// 与 Orchestrator 编排器（批次创建、周期触发判定、逐人评估与终态推进），
// 经 Wire 全链装配接入 worker 跑批通道。
package pipeline
