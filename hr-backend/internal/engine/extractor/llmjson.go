package extractor

// llmjson.go LLM JSON 输出的宽容解析共享件：评分/判型/特征抽取三引擎对同一
// 输出形态的容忍度同源维护（evaluator、grading 消费）。

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ScoreNumber 容忍模型输出的整数与浮点整型两种数值形态（78 与 78.0）：OpenAI
// 兼容通道在无严格 integer schema 约束时部分模型习惯性输出 .0 形态，*int 会
// 直接解码失败。字符串形态显式拒绝——json.Number 本质 string，Unmarshal 会
// 剥引号静默接受，须按 JSON 首字节排除。0-100 值域校验由消费方兜底。
type ScoreNumber struct {
	N int
}

// UnmarshalJSON 按 json.Number 解码：整数值原样承载，浮点形态仅容忍无小数部分
//（78.0 收敛 78，78.5 判解码失败）。ParseFloat 回退同时覆盖科学计数法整型浮点
//（7e1 收敛 70）；非 int64 范围值判失败（int(f) 超范围转换是未定义行为）。
func (s *ScoreNumber) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return fmt.Errorf("score 非数值形态: %s", b)
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		return err
	}
	i, err := strconv.ParseInt(num.String(), 10, 64)
	if err == nil {
		s.N = int(i)
		return nil
	}
	f, ferr := strconv.ParseFloat(num.String(), 64)
	if ferr != nil || f != math.Trunc(f) || f < math.MinInt64 || f > math.MaxInt64 {
		return fmt.Errorf("score 非整数形态: %s", num.String())
	}
	s.N = int(f)
	return nil
}

// FirstJSONObject 宽容解析骨架：剥围栏后 Unmarshal，失败逐个截取顶层平衡 JSON
// 对象重试（防前导示例对象劫持）。probe 在候选解码成功且判键命中时放行（判键
// 随解码结果写入 out，闭包读取）。返回是否成功，错误语义由调用方包装。
func FirstJSONObject[T any](raw string, out *T, probe func() bool) bool {
	body := StripFences(strings.TrimSpace(raw))
	if err := json.Unmarshal([]byte(body), out); err == nil && probe() {
		return true
	}
	from := 0
	for from < len(body) {
		cand := FirstBalancedJSONObject(body[from:])
		if cand == "" {
			break
		}
		if json.Unmarshal([]byte(cand), out) == nil && probe() {
			return true
		}
		from += strings.Index(body[from:], cand) + len(cand)
	}
	return false
}
