package domain

import (
	"time"
)

// 批次状态机（specs 6.2）：两终态均永久保留，无出边。
const (
	QuestionBatchStatusPending = "PENDING" // 待审核
	QuestionBatchStatusClosed  = "CLOSED"  // 已关闭（确认入库完成）
	QuestionBatchStatusVoided  = "VOIDED"  // 已作废
)

// 批次类型枚举：决定批次号前缀（G/S/R）与作废语义。
const (
	QuestionBatchTypeGenerate = "GENERATE" // AI 生成
	QuestionBatchTypeImport   = "IMPORT"   // 量表引入
	QuestionBatchTypeResubmit = "RESUBMIT" // 重新送审
)

// QuestionBatch 题库审核批次，确认入库与作废的事务边界。
// 终态（已关闭/已作废）永久保留供追溯，无软删除。
type QuestionBatch struct {
	ID            int64      `gorm:"primaryKey" json:"id,string"`
	BatchNo       string     `gorm:"type:varchar(32);not null;uniqueIndex:uk_question_batches_batch_no" json:"batch_no"`   // #GMMdd/#SMMdd/#RMMdd，同日多批 -序号
	Title         string     `gorm:"type:varchar(255);not null" json:"title"`                                              // 批次标题：生成批次为维度组合描述，量表批次为量表名，重新送审为「重新送审」
	Source        string     `gorm:"type:varchar(16);not null" json:"source"`                                              // 来源：AI/SCALE，决定审核重点提示文案
	BatchType     string     `gorm:"type:varchar(16);not null" json:"batch_type"`                                          // 批次类型：GENERATE/IMPORT/RESUBMIT，决定批次号前缀与作废语义
	Status        string     `gorm:"type:varchar(16);not null;index:idx_question_batches_status,priority:1" json:"status"` // PENDING/CLOSED/VOIDED，与 created_at 组复合索引
	QuestionCount int        `gorm:"type:int;not null" json:"question_count"`                                              // 批内题目总数，建批写入、并批累加，批次卡展示
	ScaleKey      string     `gorm:"type:varchar(32);not null" json:"scale_key"`                                           // 量表标识，仅 IMPORT 批次非空
	DimensionIDs  string     `gorm:"type:text" json:"-"`                                                                   // 生成批次维度 ID 集合快照（JSON 数组），追溯用
	ClosedAt      *time.Time `json:"closed_at"`                                                                            // 确认入库时刻
	VoidedAt      *time.Time `json:"voided_at"`                                                                            // 作废时刻
	CreatedAt     time.Time  `gorm:"autoCreateTime;index:idx_question_batches_status,priority:2" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
