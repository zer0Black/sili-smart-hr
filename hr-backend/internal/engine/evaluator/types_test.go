package evaluator

// types.go 契约测试：常量值、DimensionSpec/EvaluateResult 字段面、New 七参构造。
// 黑盒测试包（同包名白盒：loadSpecs 为包内函数，直接可测）。

import (
	"context"
	"errors"
	"testing"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/repository"
)

// ---- fake 依赖 ----

// fakeSpecReader 可配置返回的维度口径 fake。
type fakeSpecReader struct {
	specs []DimensionSpec
	err   error
	calls int
}

func (f *fakeSpecReader) ListEnabledConversationSpecs(ctx context.Context) ([]DimensionSpec, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.specs, nil
}

// 编译期断言 fake 满足窄接口。
var _ DimensionSpecReader = (*fakeSpecReader)(nil)

// fakeThresholds 复用 activity.ThresholdReader 形态。
type fakeThresholds struct{ err error }

func (f *fakeThresholds) ActivityThresholds(ctx context.Context) (int, int, error) {
	return 10, 5, f.err
}

var _ activity.ThresholdReader = (*fakeThresholds)(nil)

// fakeSysParams SystemParamReader 内存 fake。
type fakeSysParams struct{ err error }

func (f *fakeSysParams) ReadStringArray(key string) ([]string, error) { return nil, f.err }

func (f *fakeSysParams) ReadStringArrays(keys ...string) (map[string][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return map[string][]string{}, nil
}

var _ repository.SystemParamReader = (*fakeSysParams)(nil)

// ---- 常量断言（specs §2.2 常量表）----

// TestConstants 锚点常量值与 specs §2.2 一致。
func TestConstants(t *testing.T) {
	if MinValidProfilesForEval != 1 {
		t.Errorf("MinValidProfilesForEval = %d, want 1", MinValidProfilesForEval)
	}
	if MaxProfileSetTokens != 30000 {
		t.Errorf("MaxProfileSetTokens = %d, want 30000", MaxProfileSetTokens)
	}
	if MaxProfileSetChars != 90000 {
		t.Errorf("MaxProfileSetChars = %d, want 90000", MaxProfileSetChars)
	}
	if MaxProfileSetChars != MaxProfileSetTokens*3 {
		t.Errorf("MaxProfileSetChars 应为 MaxProfileSetTokens×3 折算")
	}
	if MaxRationaleChars != 200 {
		t.Errorf("MaxRationaleChars = %d, want 200", MaxRationaleChars)
	}
	if ScoreMin != 0 || ScoreMax != 100 {
		t.Errorf("ScoreMin/ScoreMax = %d/%d, want 0/100", ScoreMin, ScoreMax)
	}
	if PromptVersion != "v3" {
		t.Errorf("PromptVersion = %q, want v3", PromptVersion)
	}
}

// ---- DimensionSpec / EvaluateResult 字段透传 ----

// TestDimensionSpecFields 字段全部可赋值可读回（口径快照载体）。
func TestDimensionSpecFields(t *testing.T) {
	s := DimensionSpec{
		Code: "AI_INSTRUCTION", Name: "指令能力", Module: "AI_USAGE",
		PromptText: "p", AnchorText: "a", Weight: 30, InOverview: true,
	}
	if s.Code != "AI_INSTRUCTION" || s.Name != "指令能力" || s.Module != "AI_USAGE" ||
		s.PromptText != "p" || s.AnchorText != "a" || s.Weight != 30 || !s.InOverview {
		t.Errorf("DimensionSpec 字段透传失败: %+v", s)
	}
}

// TestEvaluateResultFields Skipped/Reused 标记位与 Scores 切片字段。
func TestEvaluateResultFields(t *testing.T) {
	r := EvaluateResult{
		Scores:  []domain.DimensionScore{{DimensionCode: "AI_XXX", Score: 72}},
		Skipped: true,
		Reused:  false,
	}
	if len(r.Scores) != 1 || r.Scores[0].DimensionCode != "AI_XXX" || r.Scores[0].Score != 72 {
		t.Errorf("Scores 透传失败: %+v", r.Scores)
	}
	if !r.Skipped || r.Reused {
		t.Errorf("标记位 Skipped=%v Reused=%v, want true/false", r.Skipped, r.Reused)
	}
}

// ---- New 构造 ----

// TestNewConstructsEvaluator 九参构造返回非 nil（依赖零值 nil 可注入，
// 挂载方法触达前不访问依赖）。
func TestNewConstructsEvaluator(t *testing.T) {
	e := New(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if e == nil {
		t.Fatal("New 返回 nil")
	}
}

// TestNewWithFakes 带全 fake 注入构造（类型面编译校验）。
func TestNewWithFakes(t *testing.T) {
	e := New(nil, nil, nil, &fakeSpecReader{}, &fakeThresholds{}, nil, &fakeSysParams{}, nil, nil)
	if e == nil {
		t.Fatal("New(带 fake) 返回 nil")
	}
}

// ---- loadSpecs（specs §2.3 错误码表）----

// TestLoadSpecsEmpty 锚点：空集返回 ErrNoDimensions。
func TestLoadSpecsEmpty(t *testing.T) {
	_, err := loadSpecs(context.Background(), &fakeSpecReader{specs: []DimensionSpec{}})
	if !errors.Is(err, ErrNoDimensions) {
		t.Fatalf("err = %v, want ErrNoDimensions", err)
	}
}

// TestLoadSpecsNilSlice nil 切片同样视为空集。
func TestLoadSpecsNilSlice(t *testing.T) {
	_, err := loadSpecs(context.Background(), &fakeSpecReader{specs: nil})
	if !errors.Is(err, ErrNoDimensions) {
		t.Fatalf("err = %v, want ErrNoDimensions", err)
	}
}

// TestLoadSpecsReadFailure 锚点：读取失败原样上抛（适配层负责 wrap
// ErrDimensionConfigRead，消费侧不再二次包装）。
func TestLoadSpecsReadFailure(t *testing.T) {
	cause := errors.New("db down")
	_, err := loadSpecs(context.Background(), &fakeSpecReader{err: cause})
	if !errors.Is(err, cause) {
		t.Fatalf("wrap 链应保留底层错误, got %v", err)
	}
}

// TestLoadSpecsSuccess 锚点：返回 3 维，长度 3 且字段透传。
func TestLoadSpecsSuccess(t *testing.T) {
	in := []DimensionSpec{
		{Code: "AI_INSTRUCTION", Name: "指令", Module: "AI_USAGE", PromptText: "p1", AnchorText: "a1", Weight: 30, InOverview: true},
		{Code: "AI_VALUE", Name: "价值", Module: "AI_USAGE", PromptText: "p2", AnchorText: "a2", Weight: 40, InOverview: true},
		{Code: "AI_REVIEW", Name: "审查", Module: "AI_USAGE", PromptText: "p3", AnchorText: "a3", Weight: 30, InOverview: false},
	}
	got, err := loadSpecs(context.Background(), &fakeSpecReader{specs: in})
	if err != nil {
		t.Fatalf("loadSpecs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("spec[%d] = %+v, want %+v（字段透传）", i, got[i], in[i])
		}
	}
}
