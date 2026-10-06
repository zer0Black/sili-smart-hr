// Package suggestgen 是团队培训建议生成引擎子域（specs P2_TMD_001 §5.1）：
// 周期批次终态后汇总两模块聚合素材，一次 LLM 调用产出两模块各 2-4 条培训
// 方向建议与一段综合研判，校验脱敏后交 T6 编排落库。
package suggestgen

import (
	"strconv"
	"strings"
)

// PromptVersion prompt 模板版本（模板变更时 +1），随生成链路落库溯源。
const PromptVersion = "v1"

// Material 生成素材：仅结构化聚合数字与维度口径（specs §5.1.4 规则4），
// 不含对话原文与单人明细。
type Material struct {
	PeriodStart    string
	PeriodEnd      string
	Modules        []ModuleMaterial
	Activity       ActivityMaterial
	WeakDimensions []WeakDimMaterial
	DimSpecs       []DimSpec
}

// ModuleMaterial 单模块聚合素材：各维度均分与低分占比、聚合分（*float64
// nil 表示该模块本期无聚合行，如实呈现交模型按可用数据研判）。
type ModuleMaterial struct {
	Module      string
	DimAverages []DimAvg
	AggScore    *float64
}

// DimAvg 单维度聚合：全员均分与低分占比（<60 分人数占比），nil 表示该维度
// 样本不足置空（<3 人，看板口径同 4.1.4 规则3）。
type DimAvg struct {
	Code     string
	Name     string
	Avg      *float64
	LowRatio *float64
}

// ActivityMaterial 活跃度三态计数与全员基数。
type ActivityMaterial struct {
	Active     int
	LowFreq    int
	Unused     int
	StaffTotal int
}

// WeakDimMaterial 共性短板维度（看板 4.1.4 规则4 判定结果透传）。
type WeakDimMaterial struct {
	Module string
	Code   string
	Name   string
	Avg    float64
}

// DimSpec 维度口径快照（评分锚点摘要，specs §5.1.2 步3）。
type DimSpec struct {
	Code   string
	Name   string
	Anchor string
}

// fmtPtr 浮点指针呈现：nil 输出「无数据」，非 nil 一位小数。
func fmtPtr(v *float64) string {
	if v == nil {
		return "无数据"
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}

// fmtInt 整数呈现。
func fmtInt(v int) string { return strconv.Itoa(v) }

// BuildPrompt 纯函数组装固定模板 prompt：系统角色 + 团队数据 + 维度口径 +
// 短板清单 + 输出要求（specs §5.1.2 步4），输出要求段写明 JSON schema 与
// 两模块各 2-4 条约束。素材即结构化聚合数字，无单人标识字段可注入。
func BuildPrompt(m Material) string {
	var b strings.Builder

	b.WriteString("你是一名企业 AI 能力建设顾问，负责基于团队评估数据给出培训规划建议。\n")
	b.WriteString("你只处理团队级聚合统计，不知道也无法推测任何个人的信息。\n\n")

	b.WriteString("【团队数据】\n")
	b.WriteString("评估区间：" + m.PeriodStart + " 至 " + m.PeriodEnd + "\n")
	b.WriteString("活跃度（全员 " + fmtInt(m.Activity.StaffTotal) + " 人）：活跃 " +
		fmtInt(m.Activity.Active) + " 人、低频 " + fmtInt(m.Activity.LowFreq) +
		" 人、未使用 " + fmtInt(m.Activity.Unused) + " 人\n")
	b.WriteString("模块说明：AI_USAGE=AI 使用能力（来源：对话分析）；AI_MGMT=AI 管理能力（来源：主动测试场景题）\n")
	for _, mod := range m.Modules {
		b.WriteString("模块 " + mod.Module + "：聚合分 " + fmtPtr(mod.AggScore) + "\n")
		for _, d := range mod.DimAverages {
			b.WriteString("- 维度 " + d.Code + "（" + d.Name + "）：均分 " + fmtPtr(d.Avg) +
				"，低分占比 " + fmtPtr(d.LowRatio) + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString("【维度口径】\n")
	for _, s := range m.DimSpecs {
		b.WriteString("- " + s.Code + " " + s.Name + "：" + s.Anchor + "\n")
	}
	b.WriteString("\n")

	b.WriteString("【共性短板】\n")
	for _, w := range m.WeakDimensions {
		b.WriteString("- 模块 " + w.Module + " 维度 " + w.Code + "（" + w.Name +
			"）：均分 " + strconv.FormatFloat(w.Avg, 'f', 1, 64) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(`【输出要求】
只输出一个 JSON 对象，不要输出任何其他文字、解释或 Markdown 围栏。结构如下：
{
  "modules": [
    {"module": "AI_USAGE", "suggestions": [{"name": "...", "description": "..."}]},
    {"module": "AI_MGMT", "suggestions": [{"name": "...", "description": "..."}]}
  ],
  "summary": "..."
}
约束：
1. modules 必须恰好包含 AI_USAGE 与 AI_MGMT 两个模块，各给出 2 至 4 条培训方向建议。
2. 每条建议 name 为方向名称（10 字以内），description 为具体说明（100 字以内，可落地、指向团队级培训动作）。
3. summary 为一段团队综合研判（200 字以内），概括整体能力态势、主要短板与推进优先级。
4. 建议须基于上述聚合数据，针对共性短板优先展开，不得虚构数据或引用任何个人。`)
	return b.String()
}
