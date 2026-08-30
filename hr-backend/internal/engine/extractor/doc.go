// Package extractor 是会话级特征抽取子域：逐会话读入原始对话全文，
// 裁剪框架注入噪音后压缩成结构化会话特征档案（specs P2_TECH_003）。
// 承载裁剪、抽取、落库、过滤、脱敏五能力与 ExtractByKey 编排入口，
// worker/task 的 engine:session-extract 任务经后者逐会话调度。
package extractor
