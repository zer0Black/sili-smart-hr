package service_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// TestMain 初始化 snowflake 节点供 dimension 测试使用。
// account 测试自填 ID 不触发，dimension 的 CreateDimension 调用真实 snowflake.NextID()。
func TestMain(m *testing.M) {
	if err := snowflake.Init(1); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// dimFakeRepo 是 repository.DimensionRepository 的测试假实现。
// 字段挂可配置返回值 + 探针，覆盖 8 个方法。
type dimFakeRepo struct {
	// FindByID 返回值
	findByIDDim   *domain.Dimension
	findByIDErr   error
	findByIDCalls int

	// ListAll 返回值
	listAllDim []domain.Dimension
	listAllErr error

	// FindByCodeExcludingDeleted：返回值队列，每次调用消费一个；空则返回 NotFound。
	// 用于模拟「第一次查重冲突、第二次查重通过」等场景。
	// alwaysCodeConflict=true 时无视队列恒返回一个存在行，模拟编码永久冲突。
	findByCodeResults  []findByCodeResult
	alwaysCodeConflict bool

	// Create 探针
	createDim  *domain.Dimension
	createErr  error
	createCall bool

	// UpdateWithVersion 返回的 RowsAffected 与 err。
	updateRows int64
	updateErr  error
	// 更新成功后 FindByID 回读的快照
	updatedSnapshot *domain.Dimension

	// SoftDeleteWithVersion 返回的 RowsAffected 与 err。
	deleteRows int64
	deleteErr  error

	// GetActivitySetting 返回值
	setting    *domain.DimensionSetting
	settingErr error

	// UpdateActivitySetting 探针与回写后的 setting 快照
	updateActivityCall   bool
	updateActivityActive int
	updateActivityLow    int
	afterUpdateSetting   *domain.DimensionSetting
}

type findByCodeResult struct {
	dim *domain.Dimension
	err error
}

func (r *dimFakeRepo) ListAll(_ context.Context) ([]domain.Dimension, error) {
	return r.listAllDim, r.listAllErr
}

func (r *dimFakeRepo) ListEnabledFullByDataSource(_ context.Context, _ string) ([]domain.Dimension, error) {
	return nil, nil
}

func (r *dimFakeRepo) FindByID(_ context.Context, _ int64) (*domain.Dimension, error) {
	r.findByIDCalls++
	return r.findByIDDim, r.findByIDErr
}

func (r *dimFakeRepo) FindByCodeExcludingDeleted(_ context.Context, _ string) (*domain.Dimension, error) {
	if r.alwaysCodeConflict {
		// 返回非 nil + nil err 触发「编码已存在」语义。
		return &domain.Dimension{Code: "CONFLICT"}, nil
	}
	if len(r.findByCodeResults) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	res := r.findByCodeResults[0]
	r.findByCodeResults = r.findByCodeResults[1:]
	return res.dim, res.err
}

func (r *dimFakeRepo) Create(_ context.Context, d *domain.Dimension) error {
	r.createCall = true
	r.createDim = d
	return r.createErr
}

func (r *dimFakeRepo) UpdateWithVersion(_ context.Context, _ int64, _ int, _ map[string]any) (int64, error) {
	return r.updateRows, r.updateErr
}

func (r *dimFakeRepo) SoftDeleteWithVersion(_ context.Context, _ int64, _ int) (int64, error) {
	return r.deleteRows, r.deleteErr
}

func (r *dimFakeRepo) GetActivitySetting(_ context.Context) (*domain.DimensionSetting, error) {
	// SaveActivityRule 校验后先 UpdateActivitySetting 再回读，
	// 此时若 afterUpdateSetting 非空优先返回它。
	if r.updateActivityCall && r.afterUpdateSetting != nil {
		return r.afterUpdateSetting, nil
	}
	return r.setting, r.settingErr
}

func (r *dimFakeRepo) UpdateActivitySetting(_ context.Context, active, low int) error {
	r.updateActivityCall = true
	r.updateActivityActive = active
	r.updateActivityLow = low
	return nil
}

var _ repository.DimensionRepository = (*dimFakeRepo)(nil)

func newDimSvc(repo repository.DimensionRepository) service.DimensionService {
	return service.NewDimensionService(repo)
}

func wantDimCode(t *testing.T, err error, code int) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("want code %d, got %v", code, err)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// === GetTree ===

func TestGetTree_Structure(t *testing.T) {
	gc := domain.GroupBase
	t1 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	_ = t1
	repo := &dimFakeRepo{
		listAllDim: []domain.Dimension{
			{ID: 1, Code: "AI_X", Name: "需求澄清", ModuleCode: domain.ModuleAIUsage, GroupCode: &gc, DataSource: domain.SourceConversation, Weight: 5, IncludeOverview: true, Enabled: true},
			{ID: 2, Code: "ACT_Y", Name: "会话频率", ModuleCode: domain.ModuleActivity, DataSource: domain.SourceRule, Weight: 0, IncludeOverview: false, Enabled: true},
		},
	}
	tree, err := newDimSvc(repo).GetTree(context.Background())
	if err != nil {
		t.Fatalf("get tree: %v", err)
	}
	if len(tree.Modules) != 4 {
		t.Fatalf("expected 4 modules, got %d", len(tree.Modules))
	}
	// 固定顺序：ACTIVITY/AI_USAGE/AI_MGMT/ENNEAGRAM
	want := []string{domain.ModuleActivity, domain.ModuleAIUsage, domain.ModuleAIMgmt, domain.ModuleEnneagram}
	for i, m := range tree.Modules {
		if m.ModuleCode != want[i] {
			t.Fatalf("module %d want %s got %s", i, want[i], m.ModuleCode)
		}
	}
	// ACTIVITY 直属维度非空、groups 为 nil。
	activity := tree.Modules[0]
	if activity.Groups != nil || len(activity.Dimensions) != 1 {
		t.Fatalf("ACTIVITY should have 1 direct dim, dims=%d groups=%v", len(activity.Dimensions), activity.Groups)
	}
	// AI_USAGE 直属维度为 nil、groups 含 BASE/UPPER 两个固定分组。
	aiUsage := tree.Modules[1]
	if aiUsage.Dimensions != nil {
		t.Fatalf("AI_USAGE dimensions want nil, got %v", aiUsage.Dimensions)
	}
	if len(aiUsage.Groups) != 2 {
		t.Fatalf("AI_USAGE want 2 groups, got %d", len(aiUsage.Groups))
	}
	if aiUsage.Groups[0].GroupCode != domain.GroupBase || aiUsage.Groups[1].GroupCode != domain.GroupUpper {
		t.Fatalf("group order want BASE then UPPER, got %s %s", aiUsage.Groups[0].GroupCode, aiUsage.Groups[1].GroupCode)
	}
	if len(aiUsage.Groups[0].Dimensions) != 1 || aiUsage.Groups[0].Dimensions[0].Code != "AI_X" {
		t.Fatalf("BASE group want AI_X, got %+v", aiUsage.Groups[0].Dimensions)
	}
	if len(aiUsage.Groups[1].Dimensions) != 0 {
		t.Fatalf("UPPER group want empty, got %d", len(aiUsage.Groups[1].Dimensions))
	}
	// 模块派生属性（specs §4.1.5）。
	if aiUsage.DataSource != domain.SourceConversation || aiUsage.IsReference != false {
		t.Fatalf("AI_USAGE preset wrong: %+v", aiUsage)
	}
	enneagram := tree.Modules[3]
	if !enneagram.IsReference {
		t.Fatal("ENNEAGRAM should be reference module")
	}
}

func TestGetTree_RepoError(t *testing.T) {
	repo := &dimFakeRepo{listAllErr: errors.New("db down")}
	_, err := newDimSvc(repo).GetTree(context.Background())
	if err == nil || !errors.Is(err, repo.listAllErr) {
		t.Fatalf("expected wrapped repo err, got %v", err)
	}
}

// === GetDetail ===

func TestGetDetail_NotFound(t *testing.T) {
	repo := &dimFakeRepo{findByIDErr: gorm.ErrRecordNotFound}
	_, err := newDimSvc(repo).GetDetail(context.Background(), 1)
	wantDimCode(t, err, errcode.DimensionNotFound)
}

func TestGetDetail_Success(t *testing.T) {
	t1 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	repo := &dimFakeRepo{findByIDDim: &domain.Dimension{
		ID: 7, Code: "AI_X", Name: "需求澄清", ModuleCode: domain.ModuleAIUsage,
		DataSource: domain.SourceConversation, Weight: 5, IncludeOverview: true,
		Enabled: true, Version: 3, CreatedAt: t1, UpdatedAt: t1,
	}}
	d, err := newDimSvc(repo).GetDetail(context.Background(), 7)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if d.ID != 7 || d.Code != "AI_X" || d.Version != 3 {
		t.Fatalf("unexpected detail: %+v", d)
	}
	if d.IsReference {
		t.Fatal("AI_USAGE should not be reference")
	}
}

// === CreateDimension ===

// TestCreateDimension_Success_AIUsage 正常新增 AI_USAGE 维度：联动默认 + 编码生成 + 入库。
func TestCreateDimension_Success_AIUsage(t *testing.T) {
	repo := &dimFakeRepo{
		// FindByCodeExcludingDeleted 第一次即返回 NotFound（编码可用）。
		findByCodeResults: []findByCodeResult{{err: gorm.ErrRecordNotFound}},
	}
	in := service.CreateDimensionInput{
		Name:       "需求澄清能力",
		ModuleCode: domain.ModuleAIUsage,
		GroupCode:  strPtr(domain.GroupBase),
		DataSource: domain.SourceConversation,
		Prompt:     "你是一位能力评估专家...",
		Anchor:     "高（90-100）：...",
	}
	res, err := newDimSvc(repo).CreateDimension(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 联动默认：weight=5、include=true（specs §4.2.4 规则1）。
	if res.Weight != 5 || !res.IncludeOverview {
		t.Fatalf("AI_USAGE defaults want w5/include, got w%d/include%v", res.Weight, res.IncludeOverview)
	}
	if !res.Enabled || res.Version != 1 {
		t.Fatalf("new dim want enabled=true version=1, got enabled%v v%d", res.Enabled, res.Version)
	}
	if res.Code != "AI_XUQIUCHENGQINGNENGLI" {
		t.Fatalf("expected generated code, got %s", res.Code)
	}
	if res.GroupCode == nil || *res.GroupCode != domain.GroupBase {
		t.Fatalf("group_code want BASE, got %v", res.GroupCode)
	}
	// 入库维度字段断言。
	if !repo.createCall || repo.createDim == nil {
		t.Fatal("repo.Create not called")
	}
	if repo.createDim.ID == 0 {
		t.Fatal("snowflake ID not assigned")
	}
	if !repo.createDim.Enabled || repo.createDim.Version != 1 {
		t.Fatalf("persisted dim want enabled=true v1, got enabled%v v%d", repo.createDim.Enabled, repo.createDim.Version)
	}
}

// TestCreateDimension_NameInvalid 核心 + specs §4.1.2 B：名称 <2 或 >30 字符返 1205。
func TestCreateDimension_NameInvalid(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "一", ModuleCode: domain.ModuleActivity, Anchor: "x",
	})
	wantDimCode(t, err, errcode.DimensionNameInvalid)
}

func TestCreateDimension_NameTooLong(t *testing.T) {
	long := stringRepeat("测", 31)
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: long, ModuleCode: domain.ModuleActivity, Anchor: "x",
	})
	wantDimCode(t, err, errcode.DimensionNameInvalid)
}

// TestCreateDimension_AnchorRequired 核心断言 + specs 规则7：anchor 空返 1206。
func TestCreateDimension_AnchorRequired(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试", ModuleCode: domain.ModuleActivity, Anchor: "  ",
	})
	wantDimCode(t, err, errcode.DimensionAnchorRequired)
}

// TestCreateDimension_PromptRequired 核心断言 + specs 规则6：CONVERSATION 空 prompt 返 1207。
func TestCreateDimension_PromptRequired(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: domain.ModuleAIUsage, GroupCode: strPtr(domain.GroupBase),
		DataSource: domain.SourceConversation, Prompt: "", Anchor: "锚点",
	})
	wantDimCode(t, err, errcode.DimensionPromptRequired)
}

// TestCreateDimension_ModuleWeightViolation 核心断言：ACTIVITY 显式传 weight=5 返 1400。
func TestCreateDimension_ModuleWeightViolation(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "活跃度维度", ModuleCode: domain.ModuleActivity, Anchor: "锚点",
		Weight: intPtr(5), // ACTIVITY 强制 weight=0
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_ModuleIncludeViolation ACTIVITY 显式传 include_overview=true 返 1400。
func TestCreateDimension_ModuleIncludeViolation(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "活跃度维度", ModuleCode: domain.ModuleActivity, Anchor: "锚点",
		IncludeOverview: boolPtr(true),
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_InvalidModule 未知模块返 1400。
func TestCreateDimension_InvalidModule(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: "UNKNOWN", Anchor: "锚点",
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_GroupOnNonAIUsage 非 AI_USAGE 模块传 group_code 返 1400（specs §4.2.2）。
func TestCreateDimension_GroupOnNonAIUsage(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: domain.ModuleActivity, Anchor: "锚点",
		GroupCode: strPtr(domain.GroupBase),
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_AIUsageGroupRequired AI_USAGE 缺 group_code 返 1400。
func TestCreateDimension_AIUsageGroupRequired(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: domain.ModuleAIUsage, Anchor: "锚点",
		DataSource: domain.SourceConversation, Prompt: "p",
		// GroupCode 缺失
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_AIUsageInvalidGroup AI_USAGE 传非 BASE/UPPER 的 group_code 返 1400。
func TestCreateDimension_AIUsageInvalidGroup(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: domain.ModuleAIUsage, Anchor: "锚点",
		GroupCode: strPtr("OTHER"), DataSource: domain.SourceConversation, Prompt: "p",
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_DataSourceMismatch 显式传 data_source 与模块联动不一致返 1400（specs 规则8）。
func TestCreateDimension_DataSourceMismatch(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试维度", ModuleCode: domain.ModuleActivity, Anchor: "锚点",
		DataSource: domain.SourceConversation, // ACTIVITY 应是 RULE
	})
	wantDimCode(t, err, errcode.BadRequest)
}

// TestCreateDimension_CodeConflict 核心断言 + specs §4.2.4 规则2：编码冲突且无法去重返 1202。
func TestCreateDimension_CodeConflict(t *testing.T) {
	repo := &dimFakeRepo{
		alwaysCodeConflict: true, // 所有查重恒命中存在行，最终触发 1202。
	}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试", ModuleCode: domain.ModuleAIUsage, GroupCode: strPtr(domain.GroupBase),
		DataSource: domain.SourceConversation, Prompt: "p", Anchor: "a",
	})
	wantDimCode(t, err, errcode.DimensionCodeExists)
}

// TestCreateDimension_CodeDedupOK 编码冲突一次后追加 _2 成功。
func TestCreateDimension_CodeDedupOK(t *testing.T) {
	repo := &dimFakeRepo{
		findByCodeResults: []findByCodeResult{
			{dim: &domain.Dimension{Code: "AI_CESHI"}}, // 第一次冲突
			{err: gorm.ErrRecordNotFound},              // 第二次（带 _2 后缀）通过
			{err: gorm.ErrRecordNotFound},              // 最终查重通过
		},
	}
	in := service.CreateDimensionInput{
		Name: "测试", ModuleCode: domain.ModuleAIUsage, GroupCode: strPtr(domain.GroupBase),
		DataSource: domain.SourceConversation, Prompt: "p", Anchor: "a",
	}
	res, err := newDimSvc(repo).CreateDimension(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Code != "AI_CESHI_2" {
		t.Fatalf("expected deduped code AI_CESHI_2, got %s", res.Code)
	}
}

// TestCreateDimension_CreateRepoError repo.Create 错误透传 wrap。
func TestCreateDimension_CreateRepoError(t *testing.T) {
	dbErr := errors.New("insert failed")
	repo := &dimFakeRepo{
		findByCodeResults: []findByCodeResult{{err: gorm.ErrRecordNotFound}},
		createErr:         dbErr,
	}
	_, err := newDimSvc(repo).CreateDimension(context.Background(), service.CreateDimensionInput{
		Name: "测试", ModuleCode: domain.ModuleActivity, Anchor: "a",
	})
	if err == nil || !errors.Is(err, dbErr) {
		t.Fatalf("expected wrapped create err, got %v", err)
	}
}

// === UpdateDimension ===

// TestUpdateDimension_NotFound 目标不存在返 1201。
func TestUpdateDimension_NotFound(t *testing.T) {
	repo := &dimFakeRepo{findByIDErr: gorm.ErrRecordNotFound}
	in := service.UpdateDimensionInput{ID: 1, Name: "新名", Anchor: "a", Version: 1}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.DimensionNotFound)
}

// TestUpdateDimension_VersionConflict 核心断言 + specs 规则9：RowsAffected=0 返 1204。
func TestUpdateDimension_VersionConflict(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, Code: "AI_X", Name: "旧名", ModuleCode: domain.ModuleAIUsage,
			DataSource: domain.SourceConversation, Weight: 5, Version: 5},
		updateRows: 0, // 版本不一致
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "新名", Anchor: "a", Weight: 5, IncludeOverview: true, Enabled: true, Version: 3, Prompt: "p"}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.DimensionVersionConflict)
}

// TestUpdateDimension_Success 正常更新成功，回读新行返回新 version。
func TestUpdateDimension_Success(t *testing.T) {
	t1 := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, Code: "AI_X", Name: "旧名", ModuleCode: domain.ModuleAIUsage,
			DataSource: domain.SourceConversation, Weight: 5, Version: 3},
		updateRows: 1,
		// 回读快照：version=4、name=新名、updated_at=t1。
		updatedSnapshot: &domain.Dimension{ID: 1, Code: "AI_X", Name: "新名", ModuleCode: domain.ModuleAIUsage,
			DataSource: domain.SourceConversation, Weight: 25, IncludeOverview: true, Enabled: true,
			Version: 4, UpdatedAt: t1},
	}
	// 模拟回读：第二次 FindByID 返回 updatedSnapshot。
	repo.findByIDDim = repo.updatedSnapshot
	in := service.UpdateDimensionInput{ID: 1, Name: "新名", Anchor: "a", Weight: 25, IncludeOverview: true, Enabled: true, Version: 3, Prompt: "p"}
	res, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Version != 4 {
		t.Fatalf("expected version 4, got %d", res.Version)
	}
	if res.UpdatedAt == "" {
		t.Fatal("expected updated_at set")
	}
}

// TestUpdateDimension_ActivityWeightGuard ACTIVITY weight≠0 返 1400（specs 规则5）。
func TestUpdateDimension_ActivityWeightGuard(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, ModuleCode: domain.ModuleActivity, DataSource: domain.SourceRule, Version: 1},
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "活跃", Anchor: "a", Weight: 10, IncludeOverview: false, Enabled: true, Version: 1}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.BadRequest)
}

// TestUpdateDimension_EnneagramIncludeGuard ENNEAGRAM include_overview=true 返 1400。
func TestUpdateDimension_EnneagramIncludeGuard(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, ModuleCode: domain.ModuleEnneagram, DataSource: domain.SourceTest, Version: 1},
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "完美型", Anchor: "a", Weight: 0, IncludeOverview: true, Enabled: true, Version: 1}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.BadRequest)
}

// TestUpdateDimension_NameInvalid 更新 name 不符返 1205。
func TestUpdateDimension_NameInvalid(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, ModuleCode: domain.ModuleAIUsage, DataSource: domain.SourceConversation, Version: 1},
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "x", Anchor: "a", Weight: 5, IncludeOverview: true, Enabled: true, Version: 1, Prompt: "p"}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.DimensionNameInvalid)
}

// TestUpdateDimension_AnchorRequired 更新 anchor 空返 1206。
func TestUpdateDimension_AnchorRequired(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, ModuleCode: domain.ModuleAIUsage, DataSource: domain.SourceConversation, Version: 1},
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "测试", Anchor: "", Weight: 5, IncludeOverview: true, Enabled: true, Version: 1, Prompt: "p"}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.DimensionAnchorRequired)
}

// TestUpdateDimension_PromptRequired CONVERSATION 维度更新空 prompt 返 1207。
func TestUpdateDimension_PromptRequired(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, ModuleCode: domain.ModuleAIUsage, DataSource: domain.SourceConversation, Version: 1},
	}
	in := service.UpdateDimensionInput{ID: 1, Name: "测试", Anchor: "a", Weight: 5, IncludeOverview: true, Enabled: true, Version: 1, Prompt: ""}
	_, err := newDimSvc(repo).UpdateDimension(context.Background(), in)
	wantDimCode(t, err, errcode.DimensionPromptRequired)
}

// === DeleteDimension ===

// TestDeleteDimension_NotFound 目标不存在返 1201。
func TestDeleteDimension_NotFound(t *testing.T) {
	repo := &dimFakeRepo{findByIDErr: gorm.ErrRecordNotFound}
	err := newDimSvc(repo).DeleteDimension(context.Background(), service.DeleteDimensionInput{ID: 1, Version: 1})
	wantDimCode(t, err, errcode.DimensionNotFound)
}

// TestDeleteDimension_EnabledNotDeletable 核心断言 + specs 规则3：启用态返 1203。
func TestDeleteDimension_EnabledNotDeletable(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, Enabled: true, Version: 1},
	}
	err := newDimSvc(repo).DeleteDimension(context.Background(), service.DeleteDimensionInput{ID: 1, Version: 1})
	wantDimCode(t, err, errcode.DimensionEnabledNotDeletable)
}

// TestDeleteDimension_VersionConflict 停用态但版本冲突返 1204。
func TestDeleteDimension_VersionConflict(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, Enabled: false, Version: 5},
		deleteRows:  0, // 版本不一致
	}
	err := newDimSvc(repo).DeleteDimension(context.Background(), service.DeleteDimensionInput{ID: 1, Version: 3})
	wantDimCode(t, err, errcode.DimensionVersionConflict)
}

// TestDeleteDimension_Success 停用态成功软删除。
func TestDeleteDimension_Success(t *testing.T) {
	repo := &dimFakeRepo{
		findByIDDim: &domain.Dimension{ID: 1, Enabled: false, Version: 1},
		deleteRows:  1,
	}
	if err := newDimSvc(repo).DeleteDimension(context.Background(), service.DeleteDimensionInput{ID: 1, Version: 1}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// === GetActivityRule / SaveActivityRule ===

func TestGetActivityRule_Success(t *testing.T) {
	t1 := time.Date(2026, 8, 11, 11, 30, 0, 0, time.UTC)
	repo := &dimFakeRepo{
		setting: &domain.DimensionSetting{ActiveThreshold: 10, LowFrequencyThreshold: 5, UpdatedAt: t1},
	}
	dto, err := newDimSvc(repo).GetActivityRule(context.Background())
	if err != nil {
		t.Fatalf("get rule: %v", err)
	}
	if dto.ActiveThreshold != 10 || dto.LowFrequencyThreshold != 5 {
		t.Fatalf("unexpected dto: %+v", dto)
	}
	if dto.UpdatedAt == "" {
		t.Fatal("expected updated_at")
	}
}

func TestGetActivityRule_NotFound(t *testing.T) {
	repo := &dimFakeRepo{settingErr: gorm.ErrRecordNotFound}
	_, err := newDimSvc(repo).GetActivityRule(context.Background())
	// 配置缺失属系统级初始化异常，走 wrap 上报 1500，非业务错误 *service.Error。
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var se *service.Error
	if errors.As(err, &se) {
		t.Fatalf("expected wrapped non-service error, got *service.Error code=%d", se.Code)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected wrapped ErrRecordNotFound, got %v", err)
	}
}

// TestSaveActivityRule_InvalidThreshold 核心断言 + specs §4.1.2 C：low≥active 返 1208。
func TestSaveActivityRule_InvalidThreshold(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).SaveActivityRule(context.Background(), service.ActivityRuleInput{
		ActiveThreshold:       5,
		LowFrequencyThreshold: 10, // low ≥ active 违规
	})
	wantDimCode(t, err, errcode.ActivityThresholdInvalid)
}

func TestSaveActivityRule_OutOfRange(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).SaveActivityRule(context.Background(), service.ActivityRuleInput{
		ActiveThreshold:       1000,
		LowFrequencyThreshold: 5,
	})
	wantDimCode(t, err, errcode.ActivityThresholdInvalid)
}

func TestSaveActivityRule_BelowRange(t *testing.T) {
	repo := &dimFakeRepo{}
	_, err := newDimSvc(repo).SaveActivityRule(context.Background(), service.ActivityRuleInput{
		ActiveThreshold:       0, // <1
		LowFrequencyThreshold: 0,
	})
	wantDimCode(t, err, errcode.ActivityThresholdInvalid)
}

// TestSaveActivityRule_Success 正常保存，回写单行后返回 DTO。
func TestSaveActivityRule_Success(t *testing.T) {
	t1 := time.Date(2026, 8, 11, 12, 10, 0, 0, time.UTC)
	repo := &dimFakeRepo{
		afterUpdateSetting: &domain.DimensionSetting{ActiveThreshold: 15, LowFrequencyThreshold: 8, UpdatedAt: t1},
	}
	dto, err := newDimSvc(repo).SaveActivityRule(context.Background(), service.ActivityRuleInput{
		ActiveThreshold:       15,
		LowFrequencyThreshold: 8,
	})
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	if dto.ActiveThreshold != 15 || dto.LowFrequencyThreshold != 8 {
		t.Fatalf("unexpected dto: %+v", dto)
	}
	if !repo.updateActivityCall {
		t.Fatal("repo.UpdateActivitySetting not called")
	}
	if repo.updateActivityActive != 15 || repo.updateActivityLow != 8 {
		t.Fatalf("update args wrong: active=%d low=%d", repo.updateActivityActive, repo.updateActivityLow)
	}
}

// helper
func stringRepeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
