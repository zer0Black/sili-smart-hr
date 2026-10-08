// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// OperationLog 承载操作日志域：全平台操作审计日志的唯一载体
//（specs P4_LOG_001 §5.1.3，一行一次操作，追加型无修改路径）。
package domain

import "time"

// 操作类型八类枚举（specs §4.1.4 规则 1），埋点侧、查询校验与导出中文名映射同源引用。
const (
	OpModuleLogin        = "login"
	OpModuleAccount      = "account"
	OpModuleDimension    = "dimension"
	OpModuleSystemParams = "system_params"
	OpModuleLLMConfig    = "llm_config"
	OpModuleQuestionBank = "question_bank"
	OpModuleAssessment   = "assessment"
	OpModuleSystemJob    = "system_job"
)

// 操作结果两态（specs 第 6 章落库一次性判定无转换）。
const (
	OpResultSuccess = "success"
	OpResultFail    = "fail"
)

// OpOperatorSystem 是系统自动任务的操作人姓名（specs §5.2.2）。
const OpOperatorSystem = "系统"

// OperationLog 是操作日志行：追加只写，落库后无修改路径，唯一出口是清理任务
// 物理删除（specs 第 6 章），故无软删除列。AccountID 仅落库排障关联不回显
// HTTP（04 §1.3），不加 json tag，雪花 ID 的 string 化由 service DTO 承载。
type OperationLog struct {
	ID          int64     `gorm:"primaryKey"`                                                         // 雪花 ID（应用层生成，Create 回调透明赋值），HTTP 回显由 DTO string 化
	AccountID   int64     `gorm:"not null"`                                                           // 操作人账号主键（业务关联无外键），登录接口与系统任务为 0
	Operator    string    `gorm:"type:varchar(64);not null;index:idx_oplog_operator"`                 // 操作人姓名冗余快照，系统任务为「系统」，登录失败为请求 username 原值
	Module      string    `gorm:"type:varchar(16);not null;index:idx_oplog_module_created,priority:1"` // 操作类型八类，见 OpModule* 常量
	Target      string    `gorm:"type:varchar(255);not null"`                                         // 操作对象描述（埋点为业务对象，兜底为 POST+路径）
	Summary     string    `gorm:"type:varchar(500);not null"`                                         // 一句话操作摘要，失败行为业务错误摘要
	Result      string    `gorm:"type:varchar(16);not null"`                                          // 操作结果两态 success/fail，见 OpResult* 常量
	ChangesJSON string    `gorm:"type:text;not null"`                                                 // 字段级变更对比 JSON 数组，无对比数据空串占位
	Detail      string    `gorm:"type:text;not null"`                                                 // 文本详情段落，无为空串
	RequestPath string    `gorm:"type:varchar(255);not null"`                                         // 请求路径（POST /api/xxx），排障与兜底对象素材
	CreatedAt   time.Time `gorm:"autoCreateTime;index:idx_oplog_module_created,priority:2;index:idx_oplog_created"` // 操作时间，列表排序与清理边界列
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`                                                     // 更新时间，追加型数据落库后恒不变
}

// TableName 显式落表名 operation_logs（specs §5.1.3 明文实体名）。
func (OperationLog) TableName() string { return "operation_logs" }

// ChangeItem 是字段级变更对比单项（03 §1.7 结构）：field 为业务字段中文名，
// before/after 为字符串化值，序列化落 changes_json；密钥类字段以掩码值进入。
type ChangeItem struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}
