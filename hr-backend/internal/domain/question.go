// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// Question 承载题库域全量题目：AI 管理题与量表题统一表，全生命周期状态机与软删除终态。
package domain

import (
	"time"

	"gorm.io/gorm"
)

// 题目来源枚举：决定文本字段语义、可编辑性与审核提示。
const (
	QuestionSourceAI    = "AI"    // AI 管理题
	QuestionSourceScale = "SCALE" // 九型量表题
)

// 题目状态机（specs 6.1）：已删除终态走 deleted_at 软删除，不占枚举值。
const (
	QuestionStatusPending  = "PENDING"  // 待审核
	QuestionStatusActive   = "ACTIVE"   // 启用
	QuestionStatusDisabled = "DISABLED" // 已停用
	QuestionStatusRejected = "REJECTED" // 已驳回
)

// 量表标识枚举：AI 题空串；承载「已引入」判定。
const (
	ScaleKeyRisoHudson = "RISO_HUDSON" // Riso-Hudson 标准量表
	ScaleKeyEssence    = "ESSENCE"     // Essence 精简量表
)

// 题目三段文本长度上限（specs §4.1.2 E）：service 校验与 questiongen prompt 约束
// 共用，单一来源防漂移。
const (
	QuestionScenarioMax    = 1000
	QuestionRequirementMax = 2000
	QuestionFocusMax       = 500
)

// Question 题库题目，AI 管理题与量表题统一承载。
// 三段文本按 source 语义换名：AI 题为情境描述/作答要求/考察点，量表题为题项陈述/作答方式说明/计分键。
type Question struct {
	ID             int64          `gorm:"primaryKey" json:"id,string"`                                                        // 雪花 ID，string 化规避前端 JS 精度坑
	QuestionNo     string         `gorm:"type:varchar(32);not null;uniqueIndex:uk_questions_question_no" json:"question_no"` // AI 题 Q-AG-xxxx / 量表题 Q-Scale-xxxx，系统生成只增不复用
	Source         string         `gorm:"type:varchar(16);not null;index:idx_questions_source_status" json:"source"`         // 来源：AI（AI 管理题）/ SCALE（九型量表），决定可编辑性与审核提示
	DimensionID    int64          `gorm:"not null;index:idx_questions_dimension" json:"dimension_id,string"`                  // 所属维度雪花 ID：AI 题为 AI_MGMT 子能力，量表题为 ENNEAGRAM 型别倾向
	ScaleKey       string         `gorm:"type:varchar(32);not null;index:idx_questions_scale" json:"scale_key"`               // 量表标识：RISO_HUDSON/ESSENCE，AI 题空串；承载「已引入」判定
	Scenario       string         `gorm:"type:text;not null" json:"scenario"`                                                 // 情境描述（AI，≤1000）/ 题项陈述（量表）
	Requirement    string         `gorm:"type:text;not null" json:"requirement"`                                              // 作答要求（AI，≤2000，选项 A/B/C/D 行内书写）/ 作答方式说明（量表）
	FocusPoint     string         `gorm:"type:varchar(500);not null" json:"focus_point"`                                      // 考察点（AI）/ 计分键（量表型别归属聚合规则）
	Status         string         `gorm:"type:varchar(16);not null;index:idx_questions_source_status" json:"status"`          // PENDING/ACTIVE/DISABLED/REJECTED，已删除走 deleted_at
	RejectReason   string         `gorm:"type:varchar(500)" json:"reject_reason"`                                             // 驳回原因，仅 REJECTED 非空；重新送审批次作废回退时保留
	BatchID        int64          `gorm:"not null;index:idx_questions_batch" json:"batch_id,string"`                          // 当前所属批次雪花 ID，重新送审时改挂新批次
	ReferenceCount int            `gorm:"type:int;not null" json:"reference_count"`                                           // 被主动测试指派累计次数，由 F7 写入，本域只读；>0 拒绝删除
	Version        int            `gorm:"type:int;not null" json:"version"`                                                   // 乐观锁版本号，编辑/启停/删除并发保护
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`                                                     // 软删除标记，承载 specs 6.1 已删除终态
	CreatedAt      time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}
