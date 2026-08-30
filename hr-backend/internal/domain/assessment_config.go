// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// AssessmentConfig 与 AssessmentConfigMember 承载评估周期配置域：
// 单例周期参数表 + 指定人员关联表（target_mode=specified 时生效）。
package domain

import "time"

// AssessmentConfig 是评估周期配置实体，系统级单例（首启 migrateDB 幂等插入默认行）。
type AssessmentConfig struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`             // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	Period      string    `gorm:"type:varchar(16);not null" json:"period"` // 周期长度，枚举 daily/weekly/monthly，由 Go 侧常量承载
	TriggerTime string    `gorm:"type:varchar(8);not null" json:"trigger_time"` // 触发时点，HH:mm 格式（如 23:00）
	TargetMode  string    `gorm:"type:varchar(16);not null" json:"target_mode"` // 评估对象模式，枚举 all/specified，由 Go 侧常量承载
	Version     int       `gorm:"not null" json:"version"`                 // 乐观锁版本号，新建置 1，更新自增
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// AssessmentConfigMember 承载评估配置的指定人员关联。
// 仅当 AssessmentConfig.TargetMode=specified 时生效，target_mode=all 时此表清空。
// 人员数据来自 sili-smart-api 用户体系，本系统仅存选中快照，遵循系统无工号约束。
type AssessmentConfigMember struct {
	ID                 int64     `gorm:"primaryKey" json:"id,string"`                            // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	AssessmentConfigID int64     `gorm:"not null;index" json:"assessment_config_id,string"`     // 关联评估配置 ID，取自 assessment_configs.id（雪花 ID），同样超 2^53 必须 string 化
	StaffID            string    `gorm:"type:varchar(64);not null;index" json:"staff_id"`       // 人员标识，来自 sili-smart-api 用户体系，外部字符串标识（如 usr_9001）
	StaffName          string    `gorm:"type:varchar(64);not null" json:"staff_name"`           // 人员姓名快照
	CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
