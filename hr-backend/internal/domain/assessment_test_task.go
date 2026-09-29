// Package domain 之主动测试域（TST）：测试任务与作答链接两模型
// （specs P2_TST_001 §5.1.3，04 §3.1/§3.2）。九型判型结果表归后续任务。
package domain

import "time"

// 任务状态五态与阅卷状态四态（specs §6.1 唯一权威）：completed/canceled 终态；
// grading 轴与 status 轴独立双轴，取消不冻结阅卷（specs §5.2.4 规则4）。
const (
	TestTaskStatusPending    = "pending"
	TestTaskStatusInProgress = "in_progress"
	TestTaskStatusCompleted  = "completed"
	TestTaskStatusExpired    = "expired"
	TestTaskStatusCanceled   = "canceled"

	GradingStatusWaiting  = "waiting"
	GradingStatusGrading  = "grading"
	GradingStatusScored   = "scored"
	GradingStatusDegraded = "degraded"
)

// 链接状态三态（specs §6.1）：used/invalid 终态，链接状态随任务状态联动不独立操作。
const (
	LinkStatusValid   = "valid"
	LinkStatusUsed    = "used"
	LinkStatusInvalid = "invalid"
)

// 测试类型（specs §4.1.2）：决定任务号前缀、组卷来源与阅卷产出落点。
const (
	TestTypeAIMgmt    = "ai_mgmt"
	TestTypeEnneagram = "enneagram"
)

// AssessmentTestTask 是一次主动测评的持久化载体（04 §3.1），任务状态机与
// 阅卷状态机的双轴持有者。快照三列（question_ids_json/scale_key/
// dimension_codes_json）创建后不再刷新（specs §5.1.4 规则2，BR3）。
type AssessmentTestTask struct {
	ID                 int64      `gorm:"primaryKey" json:"id,string"`                                              // 雪花 ID（应用层生成）
	TaskNo             string     `gorm:"type:varchar(32);not null;uniqueIndex:uk_task_no" json:"task_no"`          // 任务号 T/E前缀+日期+4位序号，取号并发兜底
	TestType           string     `gorm:"type:varchar(16);not null;index:idx_type_status_created" json:"test_type"` // ai_mgmt / enneagram
	StaffID            string     `gorm:"type:varchar(64);not null" json:"staff_id"`                                // 上游 user_id 字符串快照，F8 会话对齐辅助键
	StaffName          string     `gorm:"type:varchar(64);not null;index:idx_staff_name" json:"staff_name"`         // 人员归属主键（系统无工号）
	Status             string     `gorm:"type:varchar(16);not null;index:idx_type_status_created" json:"status"`    // 任务五态，业务层置 pending
	GradingStatus      string     `gorm:"type:varchar(16);not null" json:"grading_status"`                          // 阅卷四态，业务层置 waiting
	QuestionIDsJSON    string     `gorm:"type:text;not null" json:"question_ids_json"`                              // 题目雪花 ID 数组 JSON 快照
	ScaleKey           string     `gorm:"type:varchar(32);not null" json:"scale_key"`                               // 量表标识，仅 enneagram 非空，ai_mgmt 空串
	DimensionCodesJSON string     `gorm:"type:text;not null" json:"dimension_codes_json"`                           // 子能力维度编码数组 JSON，仅 ai_mgmt 非空
	CompletedAt        *time.Time `json:"completed_at"`                                                             // 作答提交时刻，未完成为 NULL
	CreatedAt          time.Time  `gorm:"autoCreateTime" json:"created_at"`                                         // 创建即发起时间，列表倒序键
	UpdatedAt          time.Time  `gorm:"autoUpdateTime" json:"updated_at"`                                         // 状态推进时刷新
}

// AssessmentTestLink 是一次性作答令牌与链接生命周期的载体（04 §3.2）。
// 一任务多行：创建与每次重发各一行，当前链接为最新一行；重发不删旧行。
type AssessmentTestLink struct {
	ID          int64      `gorm:"primaryKey" json:"id,string"`                                           // 雪花 ID（应用层生成）
	TaskID      int64      `gorm:"not null;index:idx_task_generated" json:"task_id,string"`               // 所属任务主键，业务字段关联无外键
	TokenPlain  string     `gorm:"type:varchar(64);not null" json:"token_plain"`                          // 令牌原文，支撑链接弹窗展示链接全文
	TokenHash   string     `gorm:"type:varchar(64);not null;uniqueIndex:uk_token_hash" json:"token_hash"` // SHA-256 hex，F8 校验点查与唯一性兜底
	Status      string     `gorm:"type:varchar(16);not null;index:idx_status_expires" json:"status"`      // 链接三态，业务层置 valid
	GeneratedAt time.Time  `gorm:"not null;index:idx_task_generated" json:"generated_at"`                 // 本条链接生成时刻
	ExpiresAt   time.Time  `gorm:"not null;index:idx_status_expires" json:"expires_at"`                   // 到期时刻 = generated_at + 7 天
	UsedAt      *time.Time `json:"used_at"`                                                               // 提交消耗时刻，未使用为 NULL
	CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
