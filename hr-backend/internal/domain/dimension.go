// Package domain 定义领域实体（GORM 模型 struct），纯数据结构。
// Dimension 与 DimensionSetting 承载能力模型维度域：四模块统一维度表 + 活跃度阈值单行配置。
package domain

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 模块枚举：四模块为固定系统级单例，不入库，前端据 module_code 派生展示属性。
const (
	ModuleActivity  = "ACTIVITY"  // 使用活跃度
	ModuleAIUsage   = "AI_USAGE"  // AI 使用能力
	ModuleAIMgmt    = "AI_MGMT"   // AI 管理能力
	ModuleEnneagram = "ENNEAGRAM" // 九型人格
)

// 分组枚举：仅 AI_USAGE 模块下的两层结构，固定，不入库。
const (
	GroupBase  = "BASE"  // 底层能力
	GroupUpper = "UPPER" // 上层能力
)

// 数据来源枚举：与 module_code 联动，提交后只读。
const (
	SourceRule         = "RULE"         // 规则统计（活跃度）
	SourceConversation = "CONVERSATION" // 对话分析（AI 使用/管理能力）
	SourceTest         = "TEST"         // 主动测试（九型人格）
)

// Dimension 能力模型叶子维度，四模块统一承载。
type Dimension struct {
	ID              int64          `gorm:"primaryKey" json:"id,string"`                                         // 雪花 ID（应用层生成），string 化规避前端 JS 精度坑
	// uniqueIndex 是查重兜底：查重与 Create 之间无锁，并发同名请求靠索引拦截重复 code。
	// 软删行释放 code 的方式是软删时改写 code（见 Dimension.DeletedCode），非 partial index（MySQL 不支持）。
	Code            string         `gorm:"type:varchar(64);not null;uniqueIndex:uk_dimension_code" json:"code"`   // 维度编码，系统生成，全大写下划线，唯一（排除软删除）
	Name            string         `gorm:"type:varchar(64);not null" json:"name"`                               // 维度名称，2~30 字符
	ModuleCode      string         `gorm:"type:varchar(32);not null;index:idx_module_group" json:"module_code"` // 所属模块：ACTIVITY/AI_USAGE/AI_MGMT/ENNEAGRAM
	GroupCode       *string        `gorm:"type:varchar(32);index:idx_module_group" json:"group_code"`           // 所属分组，仅 AI_USAGE 非 nil：BASE/UPPER，其余模块为 nil
	DataSource      string         `gorm:"type:varchar(32);not null" json:"data_source"`                        // 数据来源：RULE/CONVERSATION/TEST，与 module_code 联动
	Prompt          string         `gorm:"type:text" json:"prompt"`                                             // 评分提示词，仅 CONVERSATION 维度非空，≤2000 字符
	Anchor          string         `gorm:"type:varchar(500);not null" json:"anchor"`                            // 评分锚点，绝对分标准，必填，≤500 字符
	Weight          int            `gorm:"type:int;not null" json:"weight"`                                     // 聚合权重百分比 0~100，业务层显式置值
	IncludeOverview bool           `gorm:"not null" json:"include_overview"`                                    // 是否参与总览分，业务层显式置值（不加 default tag）
	Enabled         bool           `gorm:"not null" json:"enabled"`                                             // 是否启用，业务层显式置值
	IsReference     bool           `gorm:"not null" json:"is_reference"`                                        // 是否参考性维度，由 module_code 派生（仅 ENNEAGRAM 为 true）
	Description     string         `gorm:"type:varchar(300)" json:"description"`                                // 维度说明，≤300 字符
	Version         int            `gorm:"type:int;not null" json:"version"`                                    // 乐观锁版本号，新建置 1，更新自增
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`                                                      // 软删除标记，json:"-" 不回显
	CreatedAt       time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}

// DeletedCode 由原 code 派生软删占位码，保证唯一索引下原 code 可被新建复用
//（普通 unique index 不支持「软删行与新行同 code」共存，三库通吃的占位方案）。
func DeletedCode(code string, id int64) string {
	return fmt.Sprintf("%s__D%d", code, id)
}

// SingleRowID 是系统级单行表的固定主键：三张单行表（dimension_settings 等）共用，
// seed 与自愈补行都以它写行，并发双写撞主键由 UniqueViolation 容错收敛。
const SingleRowID int64 = 1

// DimensionSetting 维度域系统级单例配置，当前承载活跃度判定阈值。
type DimensionSetting struct {
	ID                    int64     `gorm:"primaryKey" json:"id,string"`                      // 固定 SingleRowID，单行表恒一行
	ActiveThreshold       int       `gorm:"type:int;not null" json:"active_threshold"`        // 活跃判定下限，1~999，有效对话达此值判活跃
	LowFrequencyThreshold int       `gorm:"type:int;not null" json:"low_frequency_threshold"` // 低频判定下限，1~999，须小于活跃下限
	CreatedAt             time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt             time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}
