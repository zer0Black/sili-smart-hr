package scaledata

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 术语表型别名措辞来源 specs 8.1（调停型、规则型），全集按领域知识补全。
var wantDimensionNames = map[string]string{
	"ENNE_TYPE_1_REFORMER":      "完美型",
	"ENNE_TYPE_2_HELPER":        "助人型",
	"ENNE_TYPE_3_ACHIEVER":      "成就型",
	"ENNE_TYPE_4_INDIVIDUALIST": "自我型",
	"ENNE_TYPE_5_INVESTIGATOR":  "智慧型",
	"ENNE_TYPE_6_LOYALIST":      "忠诚型",
	"ENNE_TYPE_7_ENTHUSIAST":    "活跃型",
	"ENNE_TYPE_8_CHALLENGER":    "领袖型",
	"ENNE_TYPE_9_PEACEMAKER":    "调停型",
}

func TestTemplateIntegrity(t *testing.T) {
	tpls := Templates()
	if len(tpls) != 2 {
		t.Fatalf("Templates() 应返回两套模板，实际 %d", len(tpls))
	}

	keys := make(map[string]bool)
	codeSets := make([]map[string]bool, 0, len(tpls))
	for _, tpl := range tpls {
		// 两套 ScaleKey 互异
		if keys[tpl.ScaleKey] {
			t.Errorf("ScaleKey %q 重复", tpl.ScaleKey)
		}
		keys[tpl.ScaleKey] = true

		// Items 非空、QuestionCount==len(Items)
		if len(tpl.Items) == 0 {
			t.Errorf("模板 %q Items 为空", tpl.ScaleKey)
		}
		if tpl.QuestionCount != len(tpl.Items) {
			t.Errorf("模板 %q QuestionCount=%d 与 len(Items)=%d 不一致", tpl.ScaleKey, tpl.QuestionCount, len(tpl.Items))
		}

		// 9 个型别维度，Code 唯一且命名符合全集
		if len(tpl.Dimensions) != 9 {
			t.Errorf("模板 %q 型别维度数=%d，应为 9", tpl.ScaleKey, len(tpl.Dimensions))
		}
		dimCodes := make(map[string]bool, len(tpl.Dimensions))
		for _, d := range tpl.Dimensions {
			if dimCodes[d.Code] {
				t.Errorf("模板 %q 型别维度 Code %q 重复", tpl.ScaleKey, d.Code)
			}
			dimCodes[d.Code] = true
			if want, ok := wantDimensionNames[d.Code]; !ok {
				t.Errorf("模板 %q 出现全集外维度 Code %q", tpl.ScaleKey, d.Code)
			} else if d.Name != want {
				t.Errorf("维度 %q Name=%q，应为 %q", d.Code, d.Name, want)
			}
		}
		codeSets = append(codeSets, dimCodes)

		// 样本期题数 9-18，覆盖全部 9 个型别维度
		if len(tpl.Items) < 9 || len(tpl.Items) > 18 {
			t.Errorf("模板 %q 样本题数=%d，应在 9~18 区间", tpl.ScaleKey, len(tpl.Items))
		}
		hit := make(map[string]bool)
		for i, item := range tpl.Items {
			// DimensionCode 命中 Dimensions 集合
			if !dimCodes[item.DimensionCode] {
				t.Errorf("模板 %q 第 %d 题 DimensionCode=%q 未命中型别维度集合", tpl.ScaleKey, i+1, item.DimensionCode)
			}
			hit[item.DimensionCode] = true
			// Statement/Requirement/FocusPoint 非空且 FocusPoint ≤500 rune
			if strings.TrimSpace(item.Statement) == "" {
				t.Errorf("模板 %q 第 %d 题 Statement 为空", tpl.ScaleKey, i+1)
			}
			if strings.TrimSpace(item.Requirement) == "" {
				t.Errorf("模板 %q 第 %d 题 Requirement 为空", tpl.ScaleKey, i+1)
			}
			if strings.TrimSpace(item.FocusPoint) == "" {
				t.Errorf("模板 %q 第 %d 题 FocusPoint 为空", tpl.ScaleKey, i+1)
			}
			if utf8.RuneCountInString(item.FocusPoint) > 500 {
				t.Errorf("模板 %q 第 %d 题 FocusPoint 超 500 rune", tpl.ScaleKey, i+1)
			}
		}
		if len(hit) != 9 {
			t.Errorf("模板 %q 题项仅覆盖 %d 个型别维度，应全覆盖 9 个", tpl.ScaleKey, len(hit))
		}
	}

	// 两套型别维度 Code 集合一致（共享 ensure 目标）
	for code := range codeSets[1] {
		if !codeSets[0][code] {
			t.Errorf("两套模板型别维度 Code 集合不一致，缺少 %q", code)
		}
	}
}

func TestFindByKey(t *testing.T) {
	if _, ok := FindByKey("RISO_HUDSON"); !ok {
		t.Error(`FindByKey("RISO_HUDSON") 应命中`)
	}
	if _, ok := FindByKey("ESSENCE"); !ok {
		t.Error(`FindByKey("ESSENCE") 应命中`)
	}
	if _, ok := FindByKey("NOPE"); ok {
		t.Error(`FindByKey("NOPE") 应返回 false`)
	}

	// 命中结果与 Templates 同源
	tpl, ok := FindByKey("RISO_HUDSON")
	if !ok || tpl.ScaleKey != "RISO_HUDSON" {
		t.Errorf("FindByKey 命中结果 ScaleKey=%q 异常", tpl.ScaleKey)
	}

	// 边界：空串与大小写敏感
	if _, ok := FindByKey(""); ok {
		t.Error(`FindByKey("") 应返回 false`)
	}
	if _, ok := FindByKey("riso_hudson"); ok {
		t.Error(`FindByKey 应大小写敏感，"riso_hudson" 不应命中`)
	}
}

func TestTemplateMetadata(t *testing.T) {
	for _, tpl := range Templates() {
		if tpl.ScaleKey != "RISO_HUDSON" && tpl.ScaleKey != "ESSENCE" {
			t.Errorf("意外 ScaleKey %q", tpl.ScaleKey)
		}
		if strings.TrimSpace(tpl.Name) == "" {
			t.Errorf("模板 %q Name 为空", tpl.ScaleKey)
		}
		if tpl.EstimatedMinutes <= 0 {
			t.Errorf("模板 %q EstimatedMinutes=%d 应大于 0", tpl.ScaleKey, tpl.EstimatedMinutes)
		}
		if strings.TrimSpace(tpl.Description) == "" {
			t.Errorf("模板 %q Description 为空", tpl.ScaleKey)
		}
	}

	// BR1：预估时长样本期按题数等比折算（全集口径 25/18 分钟，替换全集时改回）
	rh, _ := FindByKey("RISO_HUDSON")
	if rh.EstimatedMinutes != 3 {
		t.Errorf("RISO_HUDSON EstimatedMinutes=%d，应为 3", rh.EstimatedMinutes)
	}
	es, _ := FindByKey("ESSENCE")
	if es.EstimatedMinutes != 2 {
		t.Errorf("ESSENCE EstimatedMinutes=%d，应为 2", es.EstimatedMinutes)
	}
}
