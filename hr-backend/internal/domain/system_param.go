// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// SystemParam 承载通用 key-value 系统参数（共享 SYS 能力，specs TECH_003 04_model §3.2）。
package domain

import "time"

// SystemParam 是通用系统参数，param_key 点分模块前缀命名（如 extractor.inject_prefixes），
// 按键点查是唯一读取路径；参数页管理 HTTP 接口归后续系统参数域变更 Feature。
type SystemParam struct {
	ID          int64     `gorm:"primaryKey"`                                          // 雪花 ID（应用层生成）
	ParamKey    string    `gorm:"type:varchar(128);not null;uniqueIndex:uk_param_key"` // 参数键，全局唯一
	ParamValue  string    `gorm:"type:text;not null"`                                  // 参数值，结构化值存 JSON 序列化字符串
	Description string    `gorm:"type:varchar(255);not null"`                          // 参数用途说明，参数页展示，业务层置值
	Version     int       `gorm:"not null"`                                            // 乐观锁版本号，新建置 1
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}
