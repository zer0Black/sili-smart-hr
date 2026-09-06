// Package scorer 是多维打分与聚合子域（specs P2_TECH_005 能力5）：读同人同
// 周期全部 source 评分行（conversation 与 active_test 并列），按评分行
// evidence_json 口径摘要聚合出模块分与总览分，insufficient 与 failed 维度剔除
// 后剩余权重归一化，全剔除 nil 显式落空，幂等 upsert 落库。
package scorer
