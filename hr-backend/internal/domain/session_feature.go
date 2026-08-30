// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// SessionFeature 承载会话特征抽取域：逐会话特征档案的幂等持久化（specs TECH_003 §2.3）。
package domain

import "time"

// 档案状态三态：success 四块完整 / failed 仅统计块 / skipped 仅元数据。
const (
	FeatureStatusSuccess = "success"
	FeatureStatusFailed  = "failed"
	FeatureStatusSkipped = "skipped"
)

// SessionFeature 是会话特征档案，一会话一行，session_key 为幂等键。
// 本组件无 HTTP 面，主键无 JSON 序列化路径，不加 json tag；T5 经 DTO 下发时再双层 string 化。
type SessionFeature struct {
	ID          int64     `gorm:"primaryKey"`                                            // 雪花 ID（应用层生成）
	SessionKey  string    `gorm:"type:varchar(128);not null;uniqueIndex:uk_session_key"` // 上游会话键，详情拉取凭据与幂等键，原样透传
	TokenName   string    `gorm:"type:varchar(64);not null;index:idx_token_first_turn"`  // 人员归属（上游调用令牌名），喂 LLM 前剥离、落库时回填
	Status      string    `gorm:"type:varchar(16);not null"`  // 档案状态，FeatureStatus* 常量承载
	Client      string    `gorm:"type:varchar(32);not null"` // 主客户端标识（探测七值），detail_invalid 行为空串
	TurnCount   int       `gorm:"not null"`                  // 窗口内轮次数，skipped 行的唯一计数载体
	FirstTurnAt time.Time `gorm:"not null;index:idx_token_first_turn"`                   // 窗口内首轮时间，上游 Unix 秒转换写入
	LastTurnAt  time.Time `gorm:"not null"`                                              // 窗口内末轮时间，同上转换口径
	ProfileJSON string    `gorm:"type:text;not null"`                                    // 特征档案四块 JSON 脱敏后序列化；failed 仅统计块、skipped 空串，业务层显式置值
	ErrorCode   string    `gorm:"type:varchar(64);not null"`                             // failed 记组件错误码、skipped 记跳过原因、success 空串，业务层显式置值
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}
