// Package evaluator 是跨会话综合评估子域（specs P2_TECH_005 能力1/2/6）：
// 读单人在本周期内的全部特征档案，档案集分层组装后一次 LLM 调用评完全部
// 对话分析维度，评分行经白名单收敛、脱敏兜底后幂等落库。Evaluate 主流程、
// AssembleProfileSet 档案组装、EvaluatePerson 原子入口（活跃度 → 评估 →
// 聚合组合编排，不包事务）均已实现。
package evaluator
