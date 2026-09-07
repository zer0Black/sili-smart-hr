package evaluator

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
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
	Code         string `json:"code"`
	Score        *scoreNumber `json:"score"`
	Insufficient bool   `json:"insufficient"`
	Rationale    string `json:"rationale"`
}

// scoreNumber 容忍模型输出的整数与浮点两种数值形态（78 与 78.0）：OpenAI 兼容
// 通道在无严格 integer schema 约束时部分模型习惯性输出 .0 形态，*int 会直接
// 解码失败。非整数值由 validateAndConverge 的 0-100 整数校验兜底拒绝。
type scoreNumber struct {
	n int
}

// UnmarshalJSON 按 json.Number 解码：整数值原样承载，浮点形态仅容忍无小数部分
//（78.0 收敛 78，78.5 判解码失败）。
func (s *scoreNumber) UnmarshalJSON(b []byte) error {
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		return err
	}
	i, err := strconv.ParseInt(num.String(), 10, 64)
	if err == nil {
		s.n = int(i)
		return nil
	}
	f, ferr := strconv.ParseFloat(num.String(), 64)
	if ferr != nil || f != math.Trunc(f) {
		return fmt.Errorf("score 非整数形态: %s", num.String())
	}
	s.n = int(f)
	return nil
}

// parseScoreOutput 宽容解析（specs §2.4 能力1，同 extractor schema.go 范式）：
// 剥 markdown 围栏（extractor.StripFences），Unmarshal 失败时截取首个含 dimensions
// 键的顶层平衡 JSON 对象重试（防前导示例对象劫持），仍失败返回 ErrSchemaInvalid。
func parseScoreOutput(raw string) (*scoreOutput, error) {
	body := extractor.StripFences(strings.TrimSpace(raw))
	var out scoreOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		if trimmed := firstScoreObject(body); trimmed != "" {
			body = trimmed
			err = json.Unmarshal([]byte(body), &out)
		}
		if err != nil {
			return nil, ErrSchemaInvalid
		}
	}
	if out.Dimensions == nil {
		return nil, ErrSchemaInvalid // 无 dimensions 键（{} 或 null）
	}
	return &out, nil
}

// firstScoreObject 截取首个含 dimensions 键的顶层平衡 JSON 对象：候选须含
// dimensions 键才采纳，防前导说明文字中的示例对象（{"ok":true}）劫持。
func firstScoreObject(s string) string {
	from := 0
	for from < len(s) {
		cand := extractor.FirstBalancedJSONObject(s[from:])
		if cand == "" {
			return ""
		}
		var probe scoreOutput
		if json.Unmarshal([]byte(cand), &probe) == nil && probe.Dimensions != nil {
			return cand
		}
		from += strings.Index(s[from:], cand) + len(cand)
	}
	return ""
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
			if d.Score.n < ScoreMin || d.Score.n > ScoreMax {
				return nil, ErrSchemaInvalid
			}
			score = d.Score.n
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
