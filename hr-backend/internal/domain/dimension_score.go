// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// DimensionScore 承载综合评估域：单维度单周期评分记录的幂等持久化（specs TECH_005 §2.3）。
package domain

import "time"

// 评分来源与状态枚举：source 取维度配置 data_source 的落库形态，status 无 skipped 态。
const (
	ScoreSourceConversation = "conversation" // 对话分析（本组件写入）
	ScoreSourceActiveTest   = "active_test"  // 主动测试（F7 阅卷写入）
	ScoreStatusSuccess      = "success"      // 含 insufficient 标记行
	ScoreStatusFailed       = "failed"       // LLM 段失败全维度占位，error_code 记因
)

// ErrorCodeSkipNoLLM 零 LLM 跳过路径（零档案/签名命中）success 行的 error_code
// 落库标记：区分 LLM 产出行，评估重跑时按先删后评处置（评估先于抽取落库的自愈通道）。
const ErrorCodeSkipNoLLM = "skip_no_llm"

// DimensionScore 是维度评分记录，一人一周期一维度一行。
// 唯一索引 (token_name, period_start_at, dimension_code) 不含 source：维度数据来源
// 为 DIM 配置单选，conversation 与 active_test 的 dimension_code 集合恒不相交，天然防撞。
// 本组件无 HTTP 面，不加 json tag；F6/F9/F10 经 DTO 下发时再双层 string 化。
type DimensionScore struct {
	ID            int64     `gorm:"primaryKey"`                                                        // 雪花 ID（应用层生成）
	TokenName     string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_person_period_dim"`        // 人员归属（上游调用令牌名），喂 LLM 前剥离、落库时回填
	PeriodStartAt time.Time `gorm:"not null;uniqueIndex:uk_person_period_dim;index:idx_period_status"` // 评估周期起点（含），Period 的 Unix 秒经 time.Unix(n,0).UTC() 转换写入
	PeriodEndAt   time.Time `gorm:"not null"`                                                          // 评估周期终点（不含），同上转换口径
	DimensionCode string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_person_period_dim"`        // 维度编码，维度配置 code 原值快照
	Module        string    `gorm:"type:varchar(32);not null;index:idx_module_insufficient"`           // 所属模块编码（AI_USAGE/AI_MGMT/ENNEAGRAM），聚合按此列分组
	Score         int       `gorm:"not null"`                                                          // 维度分 0-100 整数，insufficient 与 failed 行落 0
	Rationale     string    `gorm:"type:text;not null"`                                                // 评分理由（脱敏后），业务层显式置值
	Insufficient  bool      `gorm:"not null;index:idx_module_insufficient"`                            // 证据不足标记，true 时聚合剔除，业务层显式置值（不加 default tag）
	EvidenceJSON  string    `gorm:"type:text;not null"`                                                // 证据与口径快照 JSON（session_key 清单+统计摘要+维度口径摘要），业务层显式置值
	Source        string    `gorm:"type:varchar(16);not null"`                                         // 数据来源 conversation/active_test，ScoreSource* 常量承载
	ModelName     string    `gorm:"type:varchar(128);not null"`                                        // 评分时启用模型 ID，未调 LLM 行空串，业务层置空串
	PromptVersion string    `gorm:"type:varchar(16);not null"`                                         // 评分 prompt 模板版本（evaluator 包内常量）
	Status        string    `gorm:"type:varchar(16);not null;index:idx_period_status"`                 // 评分状态 success/failed，ScoreStatus* 常量承载
	ErrorCode     string    `gorm:"type:varchar(64);not null"`                                         // failed 记组件错误码、success 空串，业务层显式置空串
	CreatedAt     time.Time `gorm:"autoCreateTime"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime"`
}
