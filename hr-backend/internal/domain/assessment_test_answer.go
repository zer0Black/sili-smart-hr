// Package domain 之作答域（TST）：作答对话记录模型（specs P2_TST_002 §5.2.3，
// 04 §3.1）。一行一条合法回复，只增不改无更新路径，updated_at 恒与 created_at
// 同值；记录随任务物理保留，无删除与软删。
package domain

import "time"

// AssessmentTestAnswer 是员工作答全过程逐题回复的持久化载体（04 §3.1）：
// A2 每次合法回复写入一行，A1 断点恢复、A3 完整性计数与 grading 阅卷
// 对话段共用消费（specs §5.2.3）。题目定位按快照内 1-based 序号，不落题目
// 雪花 ID（经任务快照 question_ids_json 数组下标换算，04 §1.6）。
type AssessmentTestAnswer struct {
	ID          int64     `gorm:"primaryKey" json:"id,string"`                       // 雪花 ID（应用层生成）
	TaskID      int64     `gorm:"not null;index:idx_task_seq" json:"task_id,string"` // 所属任务主键，业务字段关联无外键
	QuestionSeq int       `gorm:"type:int;not null;index:idx_task_seq" json:"question_seq"` // 题目序号，快照内 1-based
	Content     string    `gorm:"type:text;not null" json:"content"`                  // 员工回复内容（ai_mgmt≤500 字符 / enneagram 1-5 数字串）
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`                   // 创建时间即回复时刻（04 §1.6 不设独立 replied_at）
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`                   // 无更新路径，恒与 created_at 同值
}
