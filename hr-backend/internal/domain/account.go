// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// Account 遵循系统无工号约束：不含员工编号字段。
package domain

import (
	"time"

	"gorm.io/gorm"
)

// Account 是账号台账实体。
type Account struct {
	ID           int64          `gorm:"primaryKey" json:"id,string"` // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	Username     string         `gorm:"type:varchar(64);index;not null" json:"username"`
	PasswordHash string         `gorm:"type:varchar(255);not null" json:"-"` // json:"-" 杜绝任何序列化路径泄露哈希
	Name         string         `gorm:"type:varchar(64);not null" json:"name"`
	Enabled      bool           `json:"enabled"` // 不加 default tag，规避 MySQL/PG 布尔默认值差异致 AutoMigrate 抖动，新建账号由业务层置值
	LastLoginAt  *time.Time     `gorm:"index" json:"last_login_at"`
	CreatedAt    time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"` // 软删除：删除时自动赋值，查询自动过滤
}
