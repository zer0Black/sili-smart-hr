// Package domain 之主动测试域（TST）：九型判型结果模型（specs P2_TST_001 §5.2.2
// 步骤3c/4，04 §3.3）。AI 管理能力任务的阅卷产出落既有 dimension_scores，不进本表。
package domain

import "time"

// AssessmentTestResult 是九型人格任务的阅卷产出载体（04 §3.3）：主型、翼型、
// 9 型倾向分布与判定依据。一任务至多一行（uk_result_task 幂等）。
type AssessmentTestResult struct {
	ID               int64     `gorm:"primaryKey" json:"id,string"`                              // 雪花 ID（应用层生成）
	TaskID           int64     `gorm:"not null;uniqueIndex:uk_result_task" json:"task_id,string"` // 所属任务主键，唯一索引承载一任务至多一行
	MainType         string    `gorm:"type:varchar(4);not null" json:"main_type"`                // 主型 "1"-"9" 数字串，型名映射由前端 i18n 承载
	WingType         string    `gorm:"type:varchar(4);not null" json:"wing_type"`                // 翼型：相邻主型数字串，无显著翼型空串
	DistributionJSON string    `gorm:"type:text;not null" json:"distribution_json"`               // 9 型倾向分布百分比 JSON（{"1":12.5,...,"9":22.1}）
	Rationale        string    `gorm:"type:text;not null" json:"rationale"`                       // 判定依据（脱敏后落库）
	ModelName        string    `gorm:"type:varchar(128);not null" json:"model_name"`              // 阅卷时启用模型 ModelID
	PromptVersion    string    `gorm:"type:varchar(16);not null" json:"prompt_version"`           // 阅卷 prompt 模板版本
	GradingStatus    string    `gorm:"type:varchar(16);not null" json:"grading_status"`           // 结果行状态快照 scored/degraded，与任务行同批写入
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`                          // 首次判型落库时刻
	UpdatedAt        time.Time `gorm:"autoUpdateTime" json:"updated_at"`                          // upsert 覆盖时刷新
}
