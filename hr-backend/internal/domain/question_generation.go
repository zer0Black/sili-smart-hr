package domain

import (
	"time"
)

// 生成会话状态机（specs 04 §3.3）：QUEUED/RUNNING 可转 CANCELED，终态不可逆。
const (
	QuestionGenStatusQueued    = "QUEUED"    // 已入队
	QuestionGenStatusRunning   = "RUNNING"   // 生成中
	QuestionGenStatusCompleted = "COMPLETED" // 完成，题目已落库
	QuestionGenStatusFailed    = "FAILED"    // 失败整批作废
	QuestionGenStatusCanceled  = "CANCELED"  // 运营放弃
)

// 失败归类枚举：前端映射固定失败文案，不透传底层错误串。
const (
	QuestionGenErrorLLMFailed  = "LLM_FAILED"  // LLM 调用失败
	QuestionGenErrorLLMTimeout = "LLM_TIMEOUT" // 任务超时
	QuestionGenErrorCanceled   = "CANCELED"    // 取消即终因
	QuestionGenErrorInternal   = "INTERNAL"    // 其余内部错误
)

// QuestionGeneration LLM 生成会话，生成过程状态与暂存区。
// 生成中题目攒在 Staging，完成时单事务落库；失败/取消仅置终态，questions 表零残留。
type QuestionGeneration struct {
	ID                 int64     `gorm:"primaryKey" json:"id,string"`
	DimensionIDs       string    `gorm:"type:text;not null" json:"-"`                 // 维度 ID 集合快照（JSON 数组），出题参数留痕
	Count              int       `gorm:"type:int;not null" json:"count"`              // 目标题数 5~30
	Status             string    `gorm:"type:varchar(16);not null" json:"status"`     // QUEUED/RUNNING/COMPLETED/FAILED/CANCELED
	GeneratedCount     int       `gorm:"type:int;not null" json:"generated_count"`    // 已生成题数，轮询进度
	CurrentDimensionID int64     `gorm:"not null" json:"current_dimension_id,string"` // 当前正在构造的维度，轮询进度提示
	Staging            string    `gorm:"type:text" json:"-"`                          // 已生成题目暂存（JSON 数组），满额批次可达数百 KB，MySQL 经 migrateDB 钩子升 MEDIUMTEXT，终态清空
	BatchID            int64     `gorm:"not null" json:"batch_id,string"`             // 完成回填批次 ID，未完成为 0
	ErrorCode          string    `gorm:"type:varchar(32)" json:"error_code"`          // 失败归类：LLM_FAILED/LLM_TIMEOUT/CANCELED/INTERNAL
	CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
