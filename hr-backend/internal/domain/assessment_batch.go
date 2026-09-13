// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// AssessmentBatch 系承载批次域：周期批量评估跑批的批次记录、人员明细与告警信号
//（specs P2_ASM_001 §5.2.3/§5.2.4）。
package domain

import "time"

// 批次状态机（specs §6.1）：running 进行中，可转 success / partial_failed / failed
// 三终态；终态无出边，重新评估通过新批次表达。
const (
	BatchStatusRunning       = "running"
	BatchStatusSuccess       = "success"
	BatchStatusPartialFailed = "partial_failed"
	BatchStatusFailed        = "failed"

	BatchTriggerScheduled = "scheduled"
	BatchTriggerManual    = "manual"

	BatchTargetAll       = "all"
	BatchTargetSpecified = "specified"

	// 单人评估终态（04 §3.2）：前四态计成功侧，failed 计失败。
	PersonStatusPending  = "pending"
	PersonStatusSuccess  = "success"
	PersonStatusReused   = "reused"
	PersonStatusDegraded = "degraded"
	PersonStatusSkipped  = "skipped"
	PersonStatusFailed   = "failed"
)

// AssessmentBatch 是一次批量评估执行的批次记录，批次状态机唯一持有载体。
// 状态四态见 BatchStatus* 常量（specs §6.1，BR1）；计数列承载进度分子/分母、
// 覆盖会话数、失败计数与会话总数（specs §5.2.3，BR2）。
type AssessmentBatch struct {
	ID                  int64      `gorm:"primaryKey" json:"id,string"` // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	BatchNo             string     `gorm:"type:varchar(32);not null;uniqueIndex:uk_batch_no" json:"batch_no"`
	TriggerType         string     `gorm:"type:varchar(16);not null" json:"trigger_type"`          // scheduled / manual
	TargetMode          string     `gorm:"type:varchar(16);not null" json:"target_mode"`           // all / specified
	TargetNamesJSON     string     `gorm:"type:text;not null" json:"target_names_json"`            // 名单快照 JSON，创建时一次性落库后不刷新
	TotalCount          int        `gorm:"not null" json:"total_count"`                            // 批次总人数（进度分母）
	EvaluatedCount      int        `gorm:"not null" json:"evaluated_count"`                        // 已终态单人评估数（进度分子），SQL 原子自增只增不减
	CoveredSessionCount int        `gorm:"not null" json:"covered_session_count"`                  // 覆盖会话数：成功侧终态各人窗口内会话数之和
	FailedCount         int        `gorm:"not null" json:"failed_count"`                           // 失败人数（重试耗尽计数）
	TotalSessionCount   int        `gorm:"not null" json:"total_session_count"`                    // 批次展开拉取的会话总数（会话级失败比例分母）
	SessionFailRatio    *float64   `gorm:"type:double precision" json:"session_fail_ratio"`        // 会话级失败比例（百分比两位小数），未终态为 NULL；double precision 保三库双精度
	Status              string     `gorm:"type:varchar(16);not null;index:idx_status_triggered" json:"status"`
	ErrorSummary        string     `gorm:"type:varchar(255);not null" json:"error_summary"` // 批次级失败原因摘要，正常批次为空串
	PeriodStartAt       time.Time  `gorm:"not null" json:"period_start_at"`
	PeriodEndAt         time.Time  `gorm:"not null" json:"period_end_at"`
	TriggeredAt         time.Time  `gorm:"not null;index:idx_status_triggered;index:idx_triggered_at" json:"triggered_at"`
	FinishedAt          *time.Time `json:"finished_at"` // 终态落定时间，进行中为 NULL
	CreatedAt           time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt           time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// AssessmentBatchPerson 是批次内一人一行的单人评估终态载体（specs §5.2.2 步骤5）。
// 终态枚举见 PersonStatus* 常量；success/reused/degraded/skipped 计成功侧，failed 计失败。
type AssessmentBatchPerson struct {
	ID           int64      `gorm:"primaryKey" json:"id,string"`                                        // 雪花 ID（应用层生成）
	BatchID      int64      `gorm:"not null;uniqueIndex:uk_batch_person;index:idx_batch_status" json:"batch_id,string"` // 所属批次主键，业务字段关联无外键
	TokenName    string     `gorm:"type:varchar(64);not null;uniqueIndex:uk_batch_person" json:"token_name"`            // 上游调用令牌名（人名），与 session_features 同口径
	Status       string     `gorm:"type:varchar(16);not null;index:idx_batch_status" json:"status"`                     // 单人评估终态，业务层置 pending
	SessionCount int        `gorm:"not null" json:"session_count"`                                      // 该人评估时段内会话数（展开分组口径快照）
	ErrorSummary string     `gorm:"type:varchar(255);not null" json:"error_summary"`                    // 失败原因摘要，非失败为空串
	FinishedAt   *time.Time `json:"finished_at"`                                                        // 单人终态落定时间，未终态为 NULL
	CreatedAt    time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 覆写 GORM 复数化（person→people），按 04 §3.2 落 assessment_batch_persons。
func (AssessmentBatchPerson) TableName() string { return "assessment_batch_persons" }

// AssessmentAlert 是批次失败人数占比超阈时写入的告警信号（specs §5.2.2 步骤7）。
// uk_alert_batch 唯一索引兜底每批次至多一条，重复判定幂等覆盖（specs §5.2.4 规则4，BR3）。
type AssessmentAlert struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`                       // 雪花 ID（应用层生成）
	BatchID     int64     `gorm:"not null;uniqueIndex:uk_alert_batch" json:"batch_id,string"` // 批次主键，业务字段关联无外键
	BatchNo     string    `gorm:"type:varchar(32);not null" json:"batch_no"`         // 批次号冗余，消费方免联表
	FailedCount int       `gorm:"not null" json:"failed_count"`
	TotalCount  int       `gorm:"not null" json:"total_count"`
	FailedRatio float64   `gorm:"type:double precision;not null" json:"failed_ratio"` // 失败人数占比（百分比两位小数）；double precision 保三库双精度
	SignaledAt  time.Time `gorm:"not null;index:idx_signaled_at" json:"signaled_at"`  // 告警产生时间（批次终态判定时刻）
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
