// Package scaledata 内置两套九型标准量表模板（04 §4.1，代码常量不建表）。
// 样本期题项为已确认的先导子集，替换全集时仅扩充 Items，结构不变。
package scaledata

// ScaleDimension 型别维度定义，量表引入事务内 ensure 到 dimensions 表。
type ScaleDimension struct {
	Code string // 型别维度编码，全大写下划线（如 ENNE_TYPE_9_PEACEMAKER）
	Name string // 型别名（调停型/规则型/…九型之一）
}

// ScaleItem 量表题项全文，引入时逐条展开为 questions 行。
type ScaleItem struct {
	Statement     string // 题项陈述原文
	Requirement   string // 作答方式说明（Likert 5 级选择说明）
	FocusPoint    string // 计分键（型别归属聚合规则描述）
	DimensionCode string // 归属型别维度编码（须命中 Dimensions 内某 Code）
}

// ScaleTemplate 一套候选量表的完整模板。
type ScaleTemplate struct {
	ScaleKey         string           // RISO_HUDSON / ESSENCE
	Name             string           // 量表名（批次标题）
	QuestionCount    int              // 题目数量（与 len(Items) 一致，数据完整性测试守护）
	EstimatedMinutes int              // 预估作答时长（分钟，按全集口径）
	Description      string           // 弹窗卡片描述
	Dimensions       []ScaleDimension // 九型 9 型别维度定义
	Items            []ScaleItem      // 题目全文
}

// likertRequirement 统一作答方式说明（specs 4.1.2 F：Likert 5 级、9 型倾向聚合）。
const likertRequirement = "请根据您的实际感受，从 1 到 5 选择最符合的程度：1＝很不符合，2＝较不符合，3＝一般，4＝较符合，5＝很符合。"

// enneDimensions 九型 9 个型别维度（两套模板共享同一 Code 集合，ensure 目标一致）。
var enneDimensions = []ScaleDimension{
	{Code: "ENNE_TYPE_1_REFORMER", Name: "完美型"},
	{Code: "ENNE_TYPE_2_HELPER", Name: "助人型"},
	{Code: "ENNE_TYPE_3_ACHIEVER", Name: "成就型"},
	{Code: "ENNE_TYPE_4_INDIVIDUALIST", Name: "自我型"},
	{Code: "ENNE_TYPE_5_INVESTIGATOR", Name: "智慧型"},
	{Code: "ENNE_TYPE_6_LOYALIST", Name: "忠诚型"},
	{Code: "ENNE_TYPE_7_ENTHUSIAST", Name: "活跃型"},
	{Code: "ENNE_TYPE_8_CHALLENGER", Name: "领袖型"},
	{Code: "ENNE_TYPE_9_PEACEMAKER", Name: "调停型"},
}

// Templates 返回两套候选量表模板。
func Templates() []ScaleTemplate {
	return []ScaleTemplate{risoHudsonTemplate(), essenceTemplate()}
}

// FindByKey 按量表标识查模板，未命中返回 false。
func FindByKey(key string) (*ScaleTemplate, bool) {
	for _, tpl := range Templates() {
		if tpl.ScaleKey == key {
			return &tpl, true
		}
	}
	return nil, false
}
