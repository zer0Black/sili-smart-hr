// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// AggregateScore 承载综合评估域：单模块单周期聚合结果的幂等持久化（specs TECH_005 §2.3）。
package domain

import "time"

// ModuleOverview 是聚合结果总览行的 module 哨兵值：与 DIM 模块枚举
// （ACTIVITY/AI_USAGE/AI_MGMT/ENNEAGRAM）无碰撞，活跃度模块无聚合行。
const ModuleOverview = "overview"

// AggregateScore 是聚合结果，一人一周期一模块一行加一行总览。
// 总览行 module 落哨兵值 overview、module_score 恒 NULL；业务模块行 overview_score 恒 NULL。
// 本组件无 HTTP 面，不加 json tag；F6/F9/F10 经 DTO 下发时再双层 string 化。
type AggregateScore struct {
	ID            int64     `gorm:"primaryKey"`                                                    // 雪花 ID（应用层生成）
	TokenName     string    `gorm:"type:varchar(64);not null;uniqueIndex:uk_person_period_module"` // 人员归属，同 dimension_scores 口径
	PeriodStartAt time.Time `gorm:"not null;uniqueIndex:uk_person_period_module"`                  // 评估周期起点（含），与评分行同转换口径
	PeriodEndAt   time.Time `gorm:"not null"`                                                      // 评估周期终点（不含），同上
	Module        string    `gorm:"type:varchar(32);not null;uniqueIndex:uk_person_period_module"` // 模块编码，业务模块行落 module_code 原值，总览行落哨兵值 overview
	ModuleScore   *float64  `gorm:"type:double precision"`                                           // 模块分（模块内 InOverview 且参与维度的加权平均），总览行与全剔除模块为 NULL；double precision 保三库双精度（PG/MySQL 皆合法，SQLite 亲和收 REAL；裸 double 在 PG 是非法类型名）
	OverviewScore *float64  `gorm:"type:double precision"`                                           // 总览分（跨模块直接加权平均），仅总览行落值，全剔除为 NULL
	IncludedJSON  string    `gorm:"type:text;not null"`                                            // 参与聚合维度编码与权重快照 JSON，业务层置值
	ExcludedJSON  string    `gorm:"type:text;not null"`                                            // 剔除维度清单 JSON（insufficient 与 failed），业务层置值
	CreatedAt     time.Time `gorm:"autoCreateTime"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime"`
}
