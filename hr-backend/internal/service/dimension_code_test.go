package service_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/service"
)

// TestGenerateDimensionCode_AIUsage 验证核心断言：AI_USAGE + 「需求澄清能力」前缀 AI_ 且含 XUQIU 拼音段。
func TestGenerateDimensionCode_AIUsage(t *testing.T) {
	code := service.GenerateDimensionCode(domain.ModuleAIUsage, "需求澄清能力")
	if !strings.HasPrefix(code, "AI_") {
		t.Fatalf("expected AI_ prefix, got %s", code)
	}
	if !strings.Contains(code, "XUQIU") {
		t.Fatalf("expected pinyin segment XUQIU, got %s", code)
	}
	if !strings.Contains(code, "CHENGQING") {
		t.Fatalf("expected pinyin segment CHENGQING, got %s", code)
	}
	if !strings.Contains(code, "NENGLI") {
		t.Fatalf("expected pinyin segment NENGLI, got %s", code)
	}
	// 全大写、各字拼音连写、前缀与主体一个下划线分隔。
	if code != "AI_XUQIUCHENGQINGNENGLI" {
		t.Fatalf("expected AI_XUQIUCHENGQINGNENGLI, got %s", code)
	}
}

// TestGenerateDimensionCode_AllModules 验证四个模块前缀映射（specs 规则4）。
func TestGenerateDimensionCode_AllModules(t *testing.T) {
	cases := []struct {
		module  string
		name    string
		prefix  string
	}{
		{domain.ModuleActivity, "会话频率", "ACT"},
		{domain.ModuleAIUsage, "需求澄清能力", "AI"},
		{domain.ModuleAIMgmt, "团队赋能", "MGT"},
		{domain.ModuleEnneagram, "完美型", "ENN"},
	}
	for _, c := range cases {
		code := service.GenerateDimensionCode(c.module, c.name)
		if !strings.HasPrefix(code, c.prefix+"_") {
			t.Fatalf("module %s expected prefix %s_, got %s", c.module, c.prefix, code)
		}
	}
}

// TestGenerateDimensionCode_Truncate 验证超长名称截断 ≤40 字符（specs 规则4 编码上限）。
func TestGenerateDimensionCode_Truncate(t *testing.T) {
	// 构造超长中文名，每字拼音至少 3 字符以上，确保编码超 40。
	long := "维度名称测试用例超长字符串需要被截断到四十字符以内并保证多字节安全"
	code := service.GenerateDimensionCode(domain.ModuleAIUsage, long)
	if utf8.RuneCountInString(code) > 40 {
		t.Fatalf("expected code ≤40 runes, got %d (%s)", utf8.RuneCountInString(code), code)
	}
}

// TestGenerateDimensionCode_EmptyName 容错：空名称返回纯前缀。
func TestGenerateDimensionCode_EmptyName(t *testing.T) {
	code := service.GenerateDimensionCode(domain.ModuleActivity, "")
	if code != "ACT" {
		t.Fatalf("expected ACT for empty name, got %s", code)
	}
}

// TestGenerateDimensionCode_UpperCase 验证拼音全大写连写。
func TestGenerateDimensionCode_UpperCase(t *testing.T) {
	code := service.GenerateDimensionCode(domain.ModuleEnneagram, "测试")
	if code != "ENN_CESHI" {
		t.Fatalf("expected ENN_CESHI, got %s", code)
	}
}
