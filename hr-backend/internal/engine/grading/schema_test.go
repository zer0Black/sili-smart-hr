package grading

// schema_test.go 契约测试：双形态输出宽容解析与校验（specs TST §5.2.2 步骤3、
// 04 §3.3 distribution 口径：各项 0-100 一位小数、总和容差 100±2 闭区间）。

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// ---- parseScoreOutput ----

// TestParseScoreOutputValid 锚点：裸 JSON 与围栏包裹均解析成功。
func TestParseScoreOutputValid(t *testing.T) {
	raw := `{"dimensions":[{"code":"MGT_DELEGATE","score":82,"insufficient":false,"rationale":"授权清晰"},` +
		`{"code":"MGT_REVIEW","score":null,"insufficient":true,"rationale":"证据不足"}]}`
	for name, body := range map[string]string{"裸JSON": raw, "围栏": "```json\n" + raw + "\n```"} {
		out, err := parseScoreOutput(body)
		if err != nil {
			t.Fatalf("%s 解析失败: %v", name, err)
		}
		if len(out.Dimensions) != 2 {
			t.Fatalf("%s 维度数 %d, want 2", name, len(out.Dimensions))
		}
		if out.Dimensions[0].Score == nil || out.Dimensions[0].Score.n != 82 {
			t.Errorf("%s score = %v, want 82", name, out.Dimensions[0].Score)
		}
		if out.Dimensions[1].Score != nil {
			t.Errorf("%s null score 应保 nil", name)
		}
	}
}

// TestParseScoreOutputRejectsInvalid 补充：空文本、无 dimensions 键、前导示例
// 劫持均判 ErrSchemaInvalid；字符串分数与非整数分在解码阶段拒绝。
func TestParseScoreOutputRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"空文本":    "",
		"纯文字":    "这不是 JSON",
		"无维度键":   `{"ok":true}`,
		"示例无维度键": `示例 {"ok":true}（无 dimensions 键可提取）`,
		"字符串分数":  `{"dimensions":[{"code":"X","score":"72","insufficient":false,"rationale":"r"}]}`,
		"非整数分":   `{"dimensions":[{"code":"X","score":72.5,"insufficient":false,"rationale":"r"}]}`,
	}
	for name, raw := range cases {
		if _, err := parseScoreOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
			t.Errorf("%s 应判 ErrSchemaInvalid, got %v", name, err)
		}
	}
}

// TestParseScoreOutputFloatIntegralForm 补充：78.0 浮点整型形态收敛 78
// （evaluator scoreNumber 同口径）。
func TestParseScoreOutputFloatIntegralForm(t *testing.T) {
	out, err := parseScoreOutput(`{"dimensions":[{"code":"X","score":78.0,"insufficient":false,"rationale":"r"}]}`)
	if err != nil {
		t.Fatalf("78.0 应解析成功: %v", err)
	}
	if out.Dimensions[0].Score.n != 78 {
		t.Errorf("78.0 应收敛 78, got %d", out.Dimensions[0].Score.n)
	}
}

// ---- parseEnneagramOutput ----

// goodEnneagram 恰 100 合法判型输出。
func goodEnneagram() string {
	return `{"main_type":9,"wing_type":8,"distribution":{"1":10.0,"2":10.0,"3":10.0,"4":10.0,"5":10.0,"6":10.0,"7":10.0,"8":15.0,"9":15.0},"rationale":"倾向和平"}`
}

// TestParseEnneagramOutputValid 锚点：合法输出解析成功，数字与字符串主型形态均可、空翼型可。
func TestParseEnneagramOutputValid(t *testing.T) {
	out, err := parseEnneagramOutput(goodEnneagram())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if out.MainType != "9" || out.WingType != "8" {
		t.Errorf("main/wing = %q/%q, want 9/8", out.MainType, out.WingType)
	}
	if len(out.Distribution) != 9 || out.Distribution["9"] != 15.0 {
		t.Errorf("distribution = %v", out.Distribution)
	}

	// 字符串主型与空翼型（04 §3.3：无显著翼型落空串）。
	strForm := strings.Replace(goodEnneagram(), `"main_type":9,"wing_type":8`, `"main_type":"9","wing_type":""`, 1)
	out2, err := parseEnneagramOutput(strForm)
	if err != nil {
		t.Fatalf("字符串形态解析失败: %v", err)
	}
	if out2.MainType != "9" || out2.WingType != "" {
		t.Errorf("字符串形态 main/wing = %q/%q, want 9/空", out2.MainType, out2.WingType)
	}
}

// TestParseEnneagramOutputRejectsInvalid 补充：坏 JSON、主型越界、翼型非法、
// 分布缺键、单项越界、缺 rationale 均判 ErrSchemaInvalid。
func TestParseEnneagramOutputRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"坏JSON":      "not json",
		"主型0":        strings.Replace(goodEnneagram(), `"main_type":9`, `"main_type":0`, 1),
		"主型10":       strings.Replace(goodEnneagram(), `"main_type":9`, `"main_type":10`, 1),
		"翼型非法":       strings.Replace(goodEnneagram(), `"wing_type":8`, `"wing_type":"x"`, 1),
		"单项越界":       strings.Replace(goodEnneagram(), `"1":10.0`, `"1":110.0`, 1),
		"缺rationale": strings.Replace(goodEnneagram(), `,"rationale":"倾向和平"`, ``, 1),
		"分布缺键":       strings.Replace(goodEnneagram(), `"1":10.0,`, ``, 1),
	}
	for name, raw := range cases {
		if _, err := parseEnneagramOutput(raw); !errors.Is(err, ErrSchemaInvalid) {
			t.Errorf("%s 应判 ErrSchemaInvalid, got %v", name, err)
		}
	}
}

// TestParseEnneagramDistributionTolerance 核心断言（BR2，控制器修正后口径）：
// 总和 97.9 与 102.1 拒绝（100±2 闭区间外），98、98.5、102 接受（闭区间内含边界）。
func TestParseEnneagramDistributionTolerance(t *testing.T) {
	if _, err := parseEnneagramOutput(sumFor(97.9)); !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("总和 97.9 应拒绝, got %v", err)
	}
	if _, err := parseEnneagramOutput(sumFor(102.1)); !errors.Is(err, ErrSchemaInvalid) {
		t.Errorf("总和 102.1 应拒绝, got %v", err)
	}
	for _, ok := range []float64{98, 98.5, 102} {
		if _, err := parseEnneagramOutput(sumFor(ok)); err != nil {
			t.Errorf("总和 %v 应接受（100±2 闭区间）, got %v", ok, err)
		}
	}
}

// sumFor 构造总和精确为 total 的九键分布（末键吸收余量）。
func sumFor(total float64) string {
	each := round1(total / 9)
	parts := make([]string, 0, 9)
	used := 0.0
	for i := 1; i <= 9; i++ {
		v := each
		if i == 9 {
			v = round1(total - used)
		} else {
			used = round1(used + each)
		}
		parts = append(parts, `"`+string(rune('0'+i))+`":`+fmtPct(v))
	}
	return `{"main_type":9,"wing_type":8,"distribution":{` + strings.Join(parts, ",") + `},"rationale":"r"}`
}

// fmtPct 一位小数文本（10.0 输出 "10" 等，JSON 数值等价）。
func fmtPct(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
