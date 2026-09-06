package app

// adapters_test.go 评估装配适配器契约测试（03 §5.2）：
// ActivityThresholdReader / DimensionSpecReaderAdapter 把 DimensionRepository
// 适配为消费侧窄接口，读取失败一律 wrap ErrDimensionConfigRead 上抛。

import (
	"context"
	"errors"
	"testing"

	gorm "gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/evaluator"
	"sili-smart-hr/backend/internal/repository"
)

// fakeDimensionRepo DimensionRepository 最小 fake：仅适配器消费的两个读取方法
// 有行为，其余方法 panic 占位（本包不触达）。
type fakeDimensionRepo struct {
	getErr         error
	setting        *domain.DimensionSetting
	dims           []domain.Dimension
	lastDataSource string
}

func (f *fakeDimensionRepo) GetActivitySetting(ctx context.Context) (*domain.DimensionSetting, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.setting != nil {
		return f.setting, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeDimensionRepo) ListEnabledFullByDataSource(ctx context.Context, dataSource string) ([]domain.Dimension, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.lastDataSource = dataSource
	return f.dims, nil
}

func (f *fakeDimensionRepo) ListAll(ctx context.Context) ([]domain.Dimension, error) { panic("not used") }
func (f *fakeDimensionRepo) FindByID(ctx context.Context, id int64) (*domain.Dimension, error) {
	panic("not used")
}
func (f *fakeDimensionRepo) FindByCodeExcludingDeleted(ctx context.Context, code string) (*domain.Dimension, error) {
	panic("not used")
}
func (f *fakeDimensionRepo) Create(ctx context.Context, d *domain.Dimension) error { panic("not used") }
func (f *fakeDimensionRepo) UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error) {
	panic("not used")
}
func (f *fakeDimensionRepo) SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error) {
	panic("not used")
}
func (f *fakeDimensionRepo) UpdateActivitySetting(ctx context.Context, activeThreshold, lowFrequencyThreshold int) error {
	panic("not used")
}

var _ repository.DimensionRepository = (*fakeDimensionRepo)(nil)

// mkSetting 构造阈值单行。
func mkSetting(active, lowFreq int) *domain.DimensionSetting {
	return &domain.DimensionSetting{ActiveThreshold: active, LowFrequencyThreshold: lowFreq}
}

// TestThresholdAdapterReadFailure 核心锚点：GetActivitySetting 任何失败（含
// ErrRecordNotFound）→ wrap ErrDimensionConfigRead 上抛，无默认值回退（BR6）。
func TestThresholdAdapterReadFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"普通错误", errors.New("db down")},
		{"NotFound", gorm.ErrRecordNotFound},
	}
	for _, tc := range cases {
		r := NewActivityThresholdReader(&fakeDimensionRepo{getErr: tc.err})
		_, _, err := r.ActivityThresholds(context.Background())
		if err == nil {
			t.Errorf("%s: 应上抛错误", tc.name)
			continue
		}
		if !errors.Is(err, evaluator.ErrDimensionConfigRead) {
			t.Errorf("%s: 应 wrap ErrDimensionConfigRead, got %v", tc.name, err)
		}
	}
}

// TestThresholdAdapterPassThrough 核心锚点：正常读取透传两阈值（BR6）。
func TestThresholdAdapterPassThrough(t *testing.T) {
	r := NewActivityThresholdReader(&fakeDimensionRepo{setting: mkSetting(20, 5)})
	active, lowFreq, err := r.ActivityThresholds(context.Background())
	if err != nil {
		t.Fatalf("正常读取应 nil: %v", err)
	}
	if active != 20 || lowFreq != 5 {
		t.Errorf("阈值 = (%d, %d), want (20, 5)", active, lowFreq)
	}
}

// TestDimensionSpecAdapterAssemble 核心锚点：ListEnabledFullByDataSource 结果
// 组装 []DimensionSpec（Module 取 ModuleCode 原值），读取失败 wrap
// ErrDimensionConfigRead（BR6）。
func TestDimensionSpecAdapterAssemble(t *testing.T) {
	repo := &fakeDimensionRepo{dims: []domain.Dimension{
		{Code: "AI_INSTRUCTION", Name: "指令清晰", ModuleCode: "AI_USAGE", Prompt: "p1", Anchor: "a1", Weight: 40, IncludeOverview: true},
		{Code: "AI_REVIEW", Name: "复盘深度", ModuleCode: "AI_USAGE", Prompt: "p2", Anchor: "a2", Weight: 30, IncludeOverview: false},
	}}
	r := NewDimensionSpecReader(repo)
	specs, err := r.ListEnabledConversationSpecs(context.Background())
	if err != nil {
		t.Fatalf("正常读取应 nil: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("specs 数 = %d, want 2", len(specs))
	}
	s0 := specs[0]
	if s0.Code != "AI_INSTRUCTION" || s0.Name != "指令清晰" || s0.Module != "AI_USAGE" ||
		s0.PromptText != "p1" || s0.AnchorText != "a1" || s0.Weight != 40 || !s0.InOverview {
		t.Errorf("specs[0] = %+v", s0)
	}
	if specs[1].InOverview {
		t.Error("specs[1].InOverview 应透传 false")
	}

	bad := NewDimensionSpecReader(&fakeDimensionRepo{getErr: errors.New("db down")})
	if _, err := bad.ListEnabledConversationSpecs(context.Background()); !errors.Is(err, evaluator.ErrDimensionConfigRead) {
		t.Errorf("读取失败应 wrap ErrDimensionConfigRead, got %v", err)
	}
}

// TestDimensionSpecAdapterDataSource 边界补充：data_source 固定传 CONVERSATION（BR6）。
func TestDimensionSpecAdapterDataSource(t *testing.T) {
	repo := &fakeDimensionRepo{}
	_, _ = NewDimensionSpecReader(repo).ListEnabledConversationSpecs(context.Background())
	if repo.lastDataSource != domain.SourceConversation {
		t.Errorf("data_source = %q, want %q", repo.lastDataSource, domain.SourceConversation)
	}
}

// TestThresholdAdapterEmptyFixture 边界补充：setting 缺省（nil 且无错误注入）时
// fake 回 NotFound，适配器同样 wrap 上抛（无零值回退分支的又一证据）。
func TestThresholdAdapterEmptyFixture(t *testing.T) {
	r := NewActivityThresholdReader(&fakeDimensionRepo{})
	_, _, err := r.ActivityThresholds(context.Background())
	if !errors.Is(err, evaluator.ErrDimensionConfigRead) {
		t.Errorf("缺省 setting 应走 NotFound 上抛, got %v", err)
	}
}
