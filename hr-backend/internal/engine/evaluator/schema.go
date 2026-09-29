package evaluator

import (
	"errors"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
)

// ErrSchemaInvalid 评分输出未通过 schema 校验（specs §2.3 错误码表：重试一次，
// 仍失败落 failed 评分行）。
var ErrSchemaInvalid = errors.New("evaluator: score schema invalid")

// insufficientDefaultRationale 缺失维度补行与空提示词维度的缺省理由文案
// （specs §2.4 能力1 校验规则：缺失的按 insufficient 补行）。
const insufficientDefaultRationale = "证据不足：周期内该维度有效证据不足以支撑评分。"

// scoreOutput LLM 输出 schema（specs §2.4 能力1 评分输出 schema）。
type scoreOutput struct {
	Dimensions []scoreDimension `json:"dimensions"`
}

// scoreDimension 单维度输出：score null 表示 insufficient。
type scoreDimension struct {
	Code         string                  `json:"code"`
	Score        *extractor.ScoreNumber `json:"score"`
	Insufficient bool                    `json:"insufficient"`
	Rationale    string                  `json:"rationale"`
}

// parseScoreOutput 宽容解析（specs §2.4 能力1，extractor.FirstJSONObject 共享骨架）：
// 剥围栏后 Unmarshal，失败截取首个含 dimensions 键的顶层平衡 JSON 对象重试
//（防前导示例对象劫持），仍失败或无 dimensions 键返回 ErrSchemaInvalid。
func parseScoreOutput(raw string) (*scoreOutput, error) {
	var out scoreOutput
	if !extractor.FirstJSONObject(raw, &out, func() bool { return out.Dimensions != nil }) {
		return nil, ErrSchemaInvalid
	}
	return &out, nil
}

// validateAndConverge schema 校验与白名单收敛（specs §2.4 能力1 校验规则）：
// code 白名单 = specs 集合（多出丢弃、缺失补 insufficient 行）；Score 非 nil
// 时须 0-100 整数；Rationale 超 MaxRationaleChars×2 判失败；insufficient=true
// 且 Score 非 null 时收敛为 Score=0。返回逐 spec 定位的收敛结果。
func validateAndConverge(out *scoreOutput, specs []DimensionSpec) ([]domain.DimensionScore, error) {
	byCode := make(map[string]scoreDimension, len(out.Dimensions))
	for _, d := range out.Dimensions {
		if _, ok := byCode[d.Code]; !ok {
			byCode[d.Code] = d // 同 code 重复时取首个
		}
	}
	rows := make([]domain.DimensionScore, 0, len(specs))
	for _, spec := range specs {
		d, ok := byCode[spec.Code]
		if !ok {
			rows = append(rows, insufficientRow(spec))
			continue
		}
		score := 0
		if d.Score != nil {
			if d.Score.N < ScoreMin || d.Score.N > ScoreMax {
				return nil, ErrSchemaInvalid
			}
			score = d.Score.N
		}
		if utf8.RuneCountInString(d.Rationale) > MaxRationaleChars*2 {
			return nil, ErrSchemaInvalid
		}
		row := domain.DimensionScore{
			DimensionCode: spec.Code,
			Module:        spec.Module,
			Rationale:     d.Rationale,
			Insufficient:  d.Insufficient,
			Source:        domain.ScoreSourceConversation,
			Status:        domain.ScoreStatusSuccess,
		}
		if d.Insufficient || d.Score == nil {
			row.Score = 0
			row.Insufficient = true
			if d.Rationale == "" {
				row.Rationale = insufficientDefaultRationale
			}
		} else {
			row.Score = score
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// insufficientRow 缺失维度补行（specs §2.4 能力1：score 落 0、标记 true、
// status=success，理由取缺省文案）。
func insufficientRow(spec DimensionSpec) domain.DimensionScore {
	return domain.DimensionScore{
		DimensionCode: spec.Code,
		Module:        spec.Module,
		Score:         0,
		Rationale:     insufficientDefaultRationale,
		Insufficient:  true,
		Source:        domain.ScoreSourceConversation,
		Status:        domain.ScoreStatusSuccess,
	}
}
