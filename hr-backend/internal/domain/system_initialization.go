// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
package domain

import "time"

// SystemInitialization 是系统初始化记录，系统级单行数据。
// 存在即判定已初始化（specs 规则1），写入即单向终态（specs 规则2）。
// 不设软删除：初始化记录不可删，重置只能重建库（specs 6.2）。
type SystemInitialization struct {
	ID               int64     `gorm:"primaryKey" json:"id,string"`                            // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	CreatorAccountID int64     `gorm:"index;not null" json:"creator_account_id,string"`       // 关联首个账号 ID，取自 accounts.id 雪花主键，同样超 2^53 必须 string 化
	DBType           string    `gorm:"type:varchar(32);not null" json:"db_type"`              // 初始化时数据库类型快照：sqlite / mysql / postgres
	CreatedAt        time.Time `gorm:"autoCreateTime" json:"created_at"`                      // 初始化完成时间
	UpdatedAt        time.Time `gorm:"autoUpdateTime" json:"updated_at"`                      // 记录变更时间，单向终态下通常与 created_at 一致
}
