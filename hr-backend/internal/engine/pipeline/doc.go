// Package pipeline 是跑批管线编排子域：编排整个周期跑批的执行顺序。
// 已落 schedule 纯函数（触发判定、周期窗口推算、下次执行推算、停滞判定），
// 编排引擎仍待实现，归后续子计划。
package pipeline
