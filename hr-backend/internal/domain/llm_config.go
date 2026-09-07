// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// LLMConfig 承载大模型清单与排他启用（全表至多一行 enabled=true，业务层事务保证）。
package domain

import "time"

// LLMConfig 是大模型配置实体。每行一个模型配置，含调用参数与加密的 API Key。
// 模型删除为物理删除，不引入软删除（specs §1.3）。
type LLMConfig struct {
	ID           int64     `gorm:"primaryKey" json:"id,string"`                      // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑；与 model_id 各自独立字段（specs §8.1）
	Name         string    `gorm:"type:varchar(64);not null" json:"name"`            // 模型显示名称，如「主力模型」
	Provider     string    `gorm:"type:varchar(32);not null" json:"provider"`        // 服务商，枚举 deepseek/openai/zhipu/anthropic，由 Go 侧常量承载
	ModelID      string    `gorm:"type:varchar(100);not null" json:"model_id"`       // 模型调用参数（文本，如 deepseek-chat），传入服务商 model 参数，非雪花 ID
	APIURL       string    `gorm:"type:varchar(500);not null" json:"api_url"`        // API 地址，空串表示走服务商默认地址
	APIKeyCipher string    `gorm:"type:text;not null" json:"-"`                      // API Key 密文，AES-256-GCM 加密存储（格式 nonce:ciphertext），json:"-" 杜绝任何序列化路径泄露明文（specs §7，BR1）
	APIKeyMasked string    `gorm:"type:varchar(255);not null" json:"api_key_masked"` // API Key 掩码快照，写入时生成，列表读取此列不解密密文
	Enabled      bool      `gorm:"index" json:"enabled"`                             // 启用状态，全表唯一 true（排他启用，业务层事务保证，不加 default tag，新建记录由业务层显式置值）
	Version      int       `gorm:"not null" json:"version"`                          // 乐观锁版本号，新建置 1，更新自增
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
