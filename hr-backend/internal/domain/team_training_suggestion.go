// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// TeamTrainingSuggestion 承载团队看板域：团队级培训方向建议的落库载体
//（specs P2_TMD_001 §5.1.3，一行一评估区间）。
package domain

import "time"

// 建议行状态机（specs §6 唯一权威）：generating → generated / failed，
// generated / failed → generating（同 period 重跑批次终态成功后重置续作）。
const (
	SuggestionStatusGenerating = "generating"
	SuggestionStatusGenerated  = "generated"
	SuggestionStatusFailed     = "failed"
)

// TeamTrainingSuggestion 是团队培训方向建议行，一区间一行，uk_suggestion_period
// 承载幂等 upsert 冲突键（specs §5.1.4 规则2）。本表无 HTTP 序列化路径
//（03 §2.4 建议内容按 period 定位），不加 json tag。
type TeamTrainingSuggestion struct {
	ID            int64     `gorm:"primaryKey"`                                       // 雪花 ID（应用层生成），无 HTTP 序列化路径
	BatchNo       string    `gorm:"type:varchar(32);not null"`                        // 最新生成所属周期批次号冗余，随重置续作更新
	PeriodStartAt time.Time `gorm:"not null;uniqueIndex:uk_suggestion_period"`        // 评估周期起点（含），冲突键第一列
	PeriodEndAt   time.Time `gorm:"not null;uniqueIndex:uk_suggestion_period"`        // 评估周期终点（不含），冲突键第二列
	Status        string    `gorm:"type:varchar(16);not null"`                        // 三态见 SuggestionStatus* 常量，业务层置 generating
	ModulesJSON   string    `gorm:"type:text;not null"`                               // 两模块建议清单 JSON（脱敏后），建行置空串占位
	Summary       string    `gorm:"type:text;not null"`                               // 团队综合研判（脱敏后），建行置空串占位
	ModelName     string    `gorm:"type:varchar(128);not null"`                       // 生成时启用模型 ModelID 快照，建行置空串
	PromptVersion string    `gorm:"type:varchar(16);not null"`                        // 建议 prompt 模板版本，建行置空串
	ErrorSummary  string    `gorm:"type:varchar(255);not null"`                       // 失败原因摘要，非 failed 为空串
	GeneratedAt   *time.Time                                                           // 生成完成时间，非 generated 为 NULL
	CreatedAt     time.Time `gorm:"autoCreateTime"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime"`
}

// TableName 显式落表名 team_training_suggestions（specs §5.1.3 明文实体名）。
func (TeamTrainingSuggestion) TableName() string { return "team_training_suggestions" }
