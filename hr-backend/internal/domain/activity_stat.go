// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// ActivityStat 承载综合评估域：单人单周期活跃度统计的幂等持久化（specs TECH_005 §2.3）。
package domain

import "time"

// 活跃分级三态：按 ValidSessionCount 与 dimension_settings 阈值比较，auto_client 签名强制 unused。
const (
	ActiveLevelActive  = "active"   // 有效数 ≥ 活跃下限
	ActiveLevelLowFreq = "low_freq" // ≥ 低频下限
	ActiveLevelUnused  = "unused"   // 未使用
)

// ActivityStat 是使用活跃度统计，一人一周期一行。
// 唯一索引 (token_name, period_start_at) 不含 period_end：定向窗口起点与批量起点
// 重合时按幂等覆盖语义处理。本组件无 HTTP 面，不加 json tag。
type ActivityStat struct {
	ID                int64     `gorm:"primaryKey"`                                           // 雪花 ID（应用层生成）
	TokenName         string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_person_period"` // 人员归属，同 dimension_scores 口径
	PeriodStartAt     time.Time `gorm:"not null;uniqueIndex:uk_person_period;index:idx_period_level;index:idx_period_note"` // 评估周期起点（含），同转换口径
	PeriodEndAt       time.Time `gorm:"not null"`                                             // 评估周期终点（不含），同上
	SessionCount      int       `gorm:"not null"`                                             // 窗口内会话总数（列表口径）
	ValidSessionCount int       `gorm:"not null"`                                             // 有效会话数，档案 status ∈ {success, failed} 行数，分级判据
	SkippedCount      int       `gorm:"not null"`                                             // skipped 档案行数（同集合口径）
	TotalTurns        int       `gorm:"not null"`                                             // 轮次合计（列表口径含 skipped 会话），表征使用强度
	ActiveLevel       string    `gorm:"type:varchar(16);not null;index:idx_period_level"`      // 活跃分级 active/low_freq/unused，ActiveLevel* 常量承载
	PopulationNote    string    `gorm:"type:varchar(64);not null;index:idx_period_note"`       // 人群签名文案键（i18n 键），空串=normal，业务层置空串
	ClientDistJSON    string    `gorm:"type:text;not null"`                                   // 按档案行 client 列聚合的分布 JSON，业务层置值
	CreatedAt         time.Time `gorm:"autoCreateTime"`
	UpdatedAt         time.Time `gorm:"autoUpdateTime"`
}
