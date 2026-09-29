package grading

// schema.go 阅卷输出宽容解析与校验（specs TST §5.2.2 步骤3、04 §3.3）：
// 剥围栏 + 首个平衡 JSON 对象重试（evaluator schema.go 范式）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"sili-smart-hr/backend/internal/engine/extractor"
)

// ErrSchemaInvalid 阅卷输出未通过 schema 校验（specs §5.2.5：视同调用失败进重试）。
// 解析路径 wrap 本哨兵时携带 LLM 原始输出截断摘要（specs §5.2.5 排障日志），
// errors.Is 仍可命中。
var ErrSchemaInvalid = errors.New("grading: output schema invalid")

// schemaExcerptLen 摘要截断长度（rune 计）：区分解析失败原因即可，不承载全文。
const schemaExcerptLen = 200

// schemaErrWithExcerpt 哨兵 wrap 截断摘要（多行与首尾空白压成单行防日志膨胀）。
func schemaErrWithExcerpt(raw string) error {
	excerpt := strings.Join(strings.Fields(raw), " ")
	r := []rune(excerpt)
	if len(r) > schemaExcerptLen {
		excerpt = string(r[:schemaExcerptLen]) + "…"
	}
	return fmt.Errorf("%w: output excerpt=%q", ErrSchemaInvalid, excerpt)
}

// distributionTolerance 分布总和容差（04 §3.3：100±2 闭区间）。
const distributionTolerance = 2.0

// distributionSumMin/Max 容差闭区间边界（浮点边界比较经 epsilon 收敛防精度误判）。
const (
	distributionSumMin = 100 - distributionTolerance
	distributionSumMax = 100 + distributionTolerance
)

// floatEpsilon 边界值浮点比较容差（0.1 颗粒度下 1e-9 足够）。
const floatEpsilon = 1e-9

// scoreOutput ai_mgmt 阅卷输出 schema（03 §4.4 步骤3a）。
type scoreOutput struct {
	Dimensions []scoreDimension `json:"dimensions"`
}

// scoreDimension 单维度输出：score null 表示 insufficient。
type scoreDimension struct {
	Code         string       `json:"code"`
	Score        *scoreNumber `json:"score"`
	Insufficient bool         `json:"insufficient"`
	Rationale    string       `json:"rationale"`
}

// scoreNumber 容忍整数与浮点整型两种数值形态（78 与 78.0），字符串形态显式拒绝
// （evaluator schema.go scoreNumber 同口径）。
type scoreNumber struct {
	n int
}

// UnmarshalJSON 按 json.Number 解码：整数值原样承载，浮点形态仅容忍无小数部分，
// 非 int64 范围值判失败（0-100 校验由上层兜底拒绝）。
func (s *scoreNumber) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return fmt.Errorf("score 非数值形态: %s", b)
	}
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
	if ferr != nil || f != math.Trunc(f) || f < math.MinInt64 || f > math.MaxInt64 {
		return fmt.Errorf("score 非整数形态: %s", num.String())
	}
	s.n = int(f)
	return nil
}

// enneagramOutput 判型输出 schema（03 §4.4 步骤3b）。
type enneagramOutput struct {
	MainType     string             `json:"main_type"`
	WingType     string             `json:"wing_type"`
	Distribution map[string]float64 `json:"distribution"`
	Rationale    string             `json:"rationale"`
}

// typeNumber 主翼型数值形态：容忍 JSON 数字与字符串两种载体，统一收敛数字串。
type typeNumber struct {
	s string
}

func (t *typeNumber) UnmarshalJSON(b []byte) error {
	// 数字形态直接承载文本；字符串形态剥引号重验数值；空串为无显著翼型合法形态。
	raw := strings.TrimSpace(string(b))
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		raw = s
	}
	if raw == "" {
		t.s = ""
		return nil
	}
	if _, err := strconv.Atoi(raw); err != nil {
		return fmt.Errorf("type 非数字形态: %s", raw)
	}
	t.s = raw
	return nil
}

// parseScoreOutput 宽容解析 ai_mgmt 输出：剥围栏后 Unmarshal，失败截取首个含
// dimensions 键的顶层平衡 JSON 对象重试，仍失败或无 dimensions 键判 ErrSchemaInvalid
//（wrap 原始输出摘要）。
func parseScoreOutput(raw string) (*scoreOutput, error) {
	var out scoreOutput
	if err := firstJSONObject(raw, &out, func() bool { return out.Dimensions != nil }); err != nil {
		return nil, schemaErrWithExcerpt(raw)
	}
	return &out, nil
}

// parseEnneagramOutput 宽容解析判型输出并校验：main_type 1-9、wing_type 1-9 或
// 空（无显著翼型）、distribution 恰九键各项 0-100、总和 100±2 闭区间。
// main_type/wing_type 收敛为数字串（0 形态的翼型转空串），rationale 不可缺失。
func parseEnneagramOutput(raw string) (*enneagramOutput, error) {
	var doc enneagramDoc
	if err := firstJSONObject(raw, &doc, func() bool { return doc.MainType.s != "" }); err != nil {
		return nil, schemaErrWithExcerpt(raw)
	}

	main := doc.MainType.s
	wing := doc.WingType.s
	if !validTypeCode(main) {
		return nil, schemaErrWithExcerpt(raw)
	}
	if wing == "0" {
		wing = "" // 无显著翼型归一空串（04 §3.3 落库形态）
	}
	if wing != "" && !validTypeCode(wing) {
		return nil, schemaErrWithExcerpt(raw)
	}
	if !validDistribution(doc.Distribution) {
		return nil, schemaErrWithExcerpt(raw)
	}
	if strings.TrimSpace(doc.Rationale) == "" {
		return nil, schemaErrWithExcerpt(raw)
	}
	return &enneagramOutput{
		MainType:     main,
		WingType:     wing,
		Distribution: doc.Distribution,
		Rationale:    doc.Rationale,
	}, nil
}

// enneagramDoc 原始解码形态：主翼型经 typeNumber 收敛。
type enneagramDoc struct {
	MainType     typeNumber         `json:"main_type"`
	WingType     typeNumber         `json:"wing_type"`
	Distribution map[string]float64 `json:"distribution"`
	Rationale    string             `json:"rationale"`
}

// firstJSONObject 宽容解析骨架（evaluator 范式收敛）：剥围栏后 Unmarshal，
// 失败逐个截取顶层平衡 JSON 对象重试（防前导示例劫持）。probe 在候选解码成功
// 且判键命中时放行（判键随解码结果写入 out，闭包读取）。
func firstJSONObject[T any](raw string, out *T, probe func() bool) error {
	body := extractor.StripFences(strings.TrimSpace(raw))
	if err := json.Unmarshal([]byte(body), out); err == nil && probe() {
		return nil
	}
	from := 0
	for from < len(body) {
		cand := extractor.FirstBalancedJSONObject(body[from:])
		if cand == "" {
			break
		}
		if json.Unmarshal([]byte(cand), out) == nil && probe() {
			return nil
		}
		from += strings.Index(body[from:], cand) + len(cand)
	}
	return ErrSchemaInvalid
}

// validTypeCode 1-9 数字串判定。
func validTypeCode(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 9
}

// validDistribution 九键齐全、各项 0-100、总和落 100±2 闭区间（边界含）。
func validDistribution(dist map[string]float64) bool {
	if len(dist) != 9 {
		return false
	}
	sum := 0.0
	for i := 1; i <= 9; i++ {
		key := strconv.Itoa(i)
		v, ok := dist[key]
		if !ok || v < 0 || v > 100 {
			return false
		}
		sum += v
	}
	return sum >= distributionSumMin-floatEpsilon && sum <= distributionSumMax+floatEpsilon
}
