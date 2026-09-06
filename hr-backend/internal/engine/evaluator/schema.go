package evaluator

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrSchemaInvalid 评分输出未通过 schema 校验（specs §2.3 错误码表：重试一次，
// 仍失败落 failed 评分行）。
var ErrSchemaInvalid = errors.New("evaluator: score schema invalid")

// insufficientDefaultRationale 缺失维度补行与空提示词维度的缺省理由文案
//（specs §2.4 能力1 校验规则：缺失的按 insufficient 补行）。
const insufficientDefaultRationale = "证据不足：周期内该维度有效证据不足以支撑评分。"

// scoreOutput LLM 输出 schema（specs §2.4 能力1 评分输出 schema）。
type scoreOutput struct {
	Dimensions []scoreDimension `json:"dimensions"`
}

// scoreDimension 单维度输出：score null 表示 insufficient。
type scoreDimension struct {
	Code         string `json:"code"`
	Score        *int   `json:"score"`
	Insufficient bool   `json:"insufficient"`
	Rationale    string `json:"rationale"`
}

// parseScoreOutput 宽容解析（specs §2.4 能力1，同 extractor schema.go 范式）：
// 剥 markdown 围栏，Unmarshal 失败时截取首个含 dimensions 键的顶层平衡 JSON
// 对象重试（防前导示例对象劫持），仍失败返回 ErrSchemaInvalid。逻辑复制不
// import 未导出函数。
func parseScoreOutput(raw string) (*scoreOutput, error) {
	body := stripFences(strings.TrimSpace(raw))
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
		cand := firstBalancedJSONObject(s[from:])
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

// firstBalancedJSONObject 截取首个顶层平衡 JSON 对象：从首个 { 起按括号深度
// 配对（字符串字面量内的括号不计数，含转义），无平衡对象返回空串。
func firstBalancedJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// stripFences 剥 markdown 代码围栏包裹（```json ... ``` 或裸 ``` 围栏），
// 只剥一层完整包裹，无围栏时原样返回（逻辑复制自 extractor schema.go 范式）。
func stripFences(s string) string {
	const tick = "```"
	if !strings.HasPrefix(s, tick) {
		return s
	}
	rest := s[len(tick):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		// 首行是语言标注才丢弃；首行已含 { 时它是内容行而非标注。
		if !strings.Contains(rest[:i], "{") {
			rest = rest[i+1:]
		} else {
			rest = trimLanguagePrefix(rest[:i]) + rest[i:]
		}
	} else {
		rest = trimLanguagePrefix(rest)
	}
	if j := indexClosingFence(rest); j >= 0 {
		rest = rest[:j]
	} else if k := strings.LastIndex(rest, tick); k > 0 && strings.HasPrefix(rest, "{") &&
		!strings.ContainsAny(rest[k+len(tick):], "{}[]") {
		rest = rest[:k]
	}
	return strings.TrimSpace(rest)
}

// trimLanguagePrefix 剥围栏开头的语言标注段（ASCII 字母加可选空白），标注后
// 须紧跟 { 才认定是语言标注。
func trimLanguagePrefix(rest string) string {
	i := 0
	for i < len(rest) && rest[i] >= 'a' && rest[i] <= 'z' {
		i++
	}
	if i == 0 {
		return rest
	}
	after := strings.TrimLeft(rest[i:], " \t")
	if !strings.HasPrefix(after, "{") {
		return rest
	}
	return after
}

// indexClosingFence 找首个独立成行的闭围栏位置，无则 -1。
func indexClosingFence(s string) int {
	from := 0
	for from < len(s) {
		to := strings.IndexByte(s[from:], '\n')
		var line string
		if to < 0 {
			line = s[from:]
			from = len(s)
		} else {
			line = s[from : from+to]
			from = from + to + 1
		}
		if strings.TrimSpace(line) == "```" {
			return from - len(line) - 1
		}
	}
	return -1
}

// validateAndConverge schema 校验与白名单收敛（specs §2.4 能力1 校验规则）：
// code 白名单 = specs 集合（多出丢弃、缺失补 insufficient 行）；Score 非 nil
// 时须 0-100 整数；Rationale 超 MaxRationaleChars×2 判失败；insufficient=true
// 且 Score 非 null 时收敛为 Score=0。返回逐 spec 定位的收敛结果。
func validateAndConverge(out *scoreOutput, specs []DimensionSpec) ([]DimensionScore, error) {
	byCode := make(map[string]scoreDimension, len(out.Dimensions))
	for _, d := range out.Dimensions {
		if _, ok := byCode[d.Code]; !ok {
			byCode[d.Code] = d // 同 code 重复时取首个
		}
	}
	rows := make([]DimensionScore, 0, len(specs))
	for _, spec := range specs {
		d, ok := byCode[spec.Code]
		if !ok {
			rows = append(rows, insufficientRow(spec))
			continue
		}
		score := 0
		if d.Score != nil {
			if *d.Score < ScoreMin || *d.Score > ScoreMax {
				return nil, ErrSchemaInvalid
			}
			score = *d.Score
		}
		if utf8.RuneCountInString(d.Rationale) > MaxRationaleChars*2 {
			return nil, ErrSchemaInvalid
		}
		row := DimensionScore{
			DimensionCode: spec.Code,
			Module:        spec.Module,
			Rationale:     d.Rationale,
			Insufficient:  d.Insufficient,
			Source:        "conversation",
			Status:        "success",
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
func insufficientRow(spec DimensionSpec) DimensionScore {
	return DimensionScore{
		DimensionCode: spec.Code,
		Module:        spec.Module,
		Score:         0,
		Rationale:     insufficientDefaultRationale,
		Insufficient:  true,
		Source:        "conversation",
		Status:        "success",
	}
}
