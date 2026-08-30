// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// IntegrationSecret 承载sili-smart-ap日志集成密钥单例：访问 sili-smart-api 会话日志接口的
// Bearer 密钥，AES-256-GCM 加密存储。系统级单例，首启 migrateDB 幂等插入空密钥行。
package domain

import "time"

// IntegrationSecret 是集成密钥实体，系统级单例。
// 配置状态由 SecretCipher 是否为空推导（configured = secret_cipher != ""），
// 不另设状态字段。首启 seed 空密钥行表示未配置（specs §6.1 + 04 §4，BR2）。
type IntegrationSecret struct {
	ID           int64     `gorm:"primaryKey" json:"id,string"`                     // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	SecretCipher string    `gorm:"type:text;not null" json:"-"`                     // 集成密钥密文，AES-256-GCM 加密存储（格式 nonce:ciphertext），空串表示未配置；json:"-" 杜绝任何序列化路径泄露明文（specs §7，BR1）
	SecretMasked string    `gorm:"type:varchar(255);not null" json:"secret_masked"` // 集成密钥掩码快照，写入时生成，未配置时为空串
	Version      int       `gorm:"not null" json:"version"`                         // 乐观锁版本号，新建置 1，更新自增
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
