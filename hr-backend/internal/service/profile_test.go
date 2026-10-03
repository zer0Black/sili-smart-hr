// profile_test 对个人画像域 A1 列表聚合业务层做黑盒单元测试（specs P2_PRF_001 §5.1）。
//
// 五仓储 fake + fakeUserapiClient 驱动，不依赖真实 DB / 外部 HTTP。覆盖：
//   - 左联缺失行口径：无行 unused / 分数 nil / 九型 nil、insufficient 维度行降权（specs §4.1.4 规则2/3）
//   - 姓名 keyword 透传上游 + 拉回内存包含校验兜底（03 A1）
//   - 上游名单失败整体 1305，后半段查询不执行（specs §5.1.4 规则1）
//   - 短板维度筛选：included_json 判据、最低分并列全入选、failed 行不计分（specs §4.1.4 规则4、03 §1.9）
//   - 仅看未使用覆盖活跃度筛选（specs §4.1.2 A）
//   - 姓名升序排序与内存分页（specs §8.3 偏离记录）
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeProfileDimScores 是 repository.DimensionScoreRepository 的测试假实现，
// 仅 ListLatestByTokens 返回预设（A1 消费面），其余方法零值。
type fakeProfileDimScores struct {
	latest []domain.DimensionScore
	called bool
	err    error
}

func (f *fakeProfileDimScores) ListByPersonPeriodExact(context.Context, string, int64, int64) ([]domain.DimensionScore, error) {
	return nil, nil
}

func (f *fakeProfileDimScores) DeleteConversationFailed(context.Context, string, int64, int64) (int64, error) {
	return 0, nil
}

func (f *fakeProfileDimScores) SaveAll(context.Context, string, int64, int64, []domain.DimensionScore) error {
	return nil
}

func (f *fakeProfileDimScores) ListByToken(context.Context, string) ([]domain.DimensionScore, error) {
	return nil, nil
}

func (f *fakeProfileDimScores) ListLatestByTokens(_ context.Context, _ []string) ([]domain.DimensionScore, error) {
	f.called = true
	return f.latest, f.err
}

func (f *fakeProfileDimScores) ListByPeriodAllCompany(context.Context, int64, int64) ([]domain.DimensionScore, error) {
	return nil, nil
}

var _ repository.DimensionScoreRepository = (*fakeProfileDimScores)(nil)

// fakeProfileAggScores 是 repository.AggregateScoreRepository 的测试假实现。
type fakeProfileAggScores struct {
	latest []domain.AggregateScore
	called bool
	err    error
}

func (f *fakeProfileAggScores) UpsertAll(context.Context, string, int64, int64, []domain.AggregateScore) error {
	return nil
}

func (f *fakeProfileAggScores) ListByToken(context.Context, string) ([]domain.AggregateScore, error) {
	return nil, nil
}

func (f *fakeProfileAggScores) ListLatestModuleRowsByTokens(_ context.Context, _ []string) ([]domain.AggregateScore, error) {
	f.called = true
	return f.latest, f.err
}

var _ repository.AggregateScoreRepository = (*fakeProfileAggScores)(nil)

// fakeProfileActivityStats 是 repository.ActivityStatRepository 的测试假实现。
type fakeProfileActivityStats struct {
	latest []domain.ActivityStat
	err    error
}

func (f *fakeProfileActivityStats) Upsert(context.Context, *domain.ActivityStat) error {
	return nil
}

func (f *fakeProfileActivityStats) ListByToken(context.Context, string) ([]domain.ActivityStat, error) {
	return nil, nil
}

func (f *fakeProfileActivityStats) ListLatestByTokens(_ context.Context, _ []string) ([]domain.ActivityStat, error) {
	return f.latest, f.err
}

var _ repository.ActivityStatRepository = (*fakeProfileActivityStats)(nil)

// fakeProfileResults 是 repository.AssessmentTestResultRepository 的测试假实现。
type fakeProfileResults struct {
	byStaff map[string]domain.AssessmentTestResult
	err     error
}

func (f *fakeProfileResults) UpsertByTaskID(context.Context, *domain.AssessmentTestResult) error {
	return nil
}

func (f *fakeProfileResults) DegradeTask(context.Context, int64, bool) error {
	return nil
}

func (f *fakeProfileResults) ListLatestScoredByStaffNames(_ context.Context, _ []string) (map[string]domain.AssessmentTestResult, error) {
	return f.byStaff, f.err
}

var _ repository.AssessmentTestResultRepository = (*fakeProfileResults)(nil)

// fakeProfileDimensionRepo 是 repository.DimensionRepository 的测试假实现，仅 ListAll 返回预设。
type fakeProfileDimensionRepo struct {
	all []domain.Dimension
	err error
}

func (f *fakeProfileDimensionRepo) ListAll(context.Context) ([]domain.Dimension, error) {
	return f.all, f.err
}

func (f *fakeProfileDimensionRepo) FindByID(context.Context, int64) (*domain.Dimension, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) FindByCodeExcludingDeleted(context.Context, string) (*domain.Dimension, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) Create(context.Context, *domain.Dimension) error {
	return nil
}

func (f *fakeProfileDimensionRepo) UpdateWithVersion(context.Context, int64, int, map[string]any) (int64, error) {
	return 0, nil
}

func (f *fakeProfileDimensionRepo) SoftDeleteWithVersion(context.Context, int64, int) (int64, error) {
	return 0, nil
}

func (f *fakeProfileDimensionRepo) GetActivitySetting(context.Context) (*domain.DimensionSetting, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) UpdateActivitySetting(context.Context, int, int) error {
	return nil
}

func (f *fakeProfileDimensionRepo) ListEnabledFullByDataSource(context.Context, string) ([]domain.Dimension, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) ListFullByCodesUnscoped(context.Context, []string) ([]domain.Dimension, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) CountEnabledByGroupCode(context.Context, string) (map[string]int, error) {
	return nil, nil
}

func (f *fakeProfileDimensionRepo) ListNamesByIDsUnscoped(context.Context, []int64) (map[int64]string, error) {
	return nil, nil
}

var _ repository.DimensionRepository = (*fakeProfileDimensionRepo)(nil)

// profilePeriod 统一测试周期（UTC，双界与落库行同构）。
func profilePeriod() (time.Time, time.Time) {
	return time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
}

// newProfileSvc 用五仓储 fake + 已配置密钥构造被测 service。
func newProfileSvc(dims *fakeProfileDimensionRepo, ds *fakeProfileDimScores, ag *fakeProfileAggScores,
	ac *fakeProfileActivityStats, rs *fakeProfileResults, ua *fakeUserapiClient) service.ProfileService {
	encKey := crypto.DeriveKey("test-profile")
	cipher, err := crypto.Encrypt(encKey, "profile-secret")
	if err != nil {
		panic(err)
	}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	return service.NewProfileService(ds, ag, ac, rs, dims, ua, secretRepo, string(encKey))
}

// findProfileItem 按姓名取行（顺序断言独立覆盖，避免测试耦合排序）。
func findProfileItem(t *testing.T, res *service.ProfileListResult, name string) *service.ProfileListItem {
	t.Helper()
	for i := range res.List {
		if res.List[i].StaffName == name {
			return &res.List[i]
		}
	}
	t.Fatalf("staff %s not in list", name)
	return nil
}

// TestProfileList_LeftJoinMissingRows 验证左联缺失行口径（specs §4.1.4 规则2/3）：
// 张伟四表全无行 → unused + 双分数 nil + 九型 nil + degraded false；
// 张敏 AI_USAGE 聚合 82.35 + 1 行 insufficient 维度行 → ai_usage_degraded=true。
func TestProfileList_LeftJoinMissingRows(t *testing.T) {
	ps, pe := profilePeriod()
	score := 82.35
	dims := &fakeProfileDimensionRepo{}
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 82,
			Status: domain.ScoreStatusSuccess, Insufficient: true, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score,
			PeriodStartAt: ps, PeriodEndAt: pe, IncludedJSON: `[{"code":"A","weight":100}]`},
	}}
	ac := &fakeProfileActivityStats{latest: []domain.ActivityStat{
		{TokenName: "张敏", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	rs := &fakeProfileResults{byStaff: map[string]domain.AssessmentTestResult{
		"张敏": {MainType: "3"},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "张伟"},
	}, total: 2}

	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 2 || len(res.List) != 2 {
		t.Fatalf("want total 2 / len 2, got %d / %d", res.Total, len(res.List))
	}
	zw := findProfileItem(t, res, "张伟")
	if zw.ActivityLevel != "unused" {
		t.Fatalf("张伟 activity_level want unused, got %s", zw.ActivityLevel)
	}
	if zw.AIUsageScore != nil || zw.AIMGMTScore != nil {
		t.Fatalf("张伟 分数应均 nil，got %+v %+v", zw.AIUsageScore, zw.AIMGMTScore)
	}
	if zw.EnneagramMainType != nil {
		t.Fatalf("张伟 enneagram 应 nil，got %v", *zw.EnneagramMainType)
	}
	if zw.AIUsageDegraded || zw.AIMGMTDegraded {
		t.Fatal("张伟 degraded 应恒 false")
	}
	zm := findProfileItem(t, res, "张敏")
	if zm.AIUsageScore == nil || *zm.AIUsageScore != 82.35 {
		t.Fatalf("张敏 ai_usage_score want 82.35, got %v", zm.AIUsageScore)
	}
	if !zm.AIUsageDegraded {
		t.Fatal("张敏 ai_usage_degraded want true（insufficient 维度行）")
	}
	if zm.AIMGMTScore != nil || zm.AIMGMTDegraded {
		t.Fatal("张敏 AI_MGMT 无聚合行应 nil 且不降权")
	}
	if zm.ActivityLevel != "active" {
		t.Fatalf("张敏 activity_level want active, got %s", zm.ActivityLevel)
	}
	if zm.EnneagramMainType == nil || *zm.EnneagramMainType != "3" {
		t.Fatalf("张敏 enneagram want 3, got %v", zm.EnneagramMainType)
	}
}

// TestProfileList_StaffFilter_KeywordPassthrough 验证姓名筛选 keyword 透传上游，
// 拉回后内存包含校验兜底（fake 不做服务端过滤时仍只剩名字含「张」的人）。
func TestProfileList_StaffFilter_KeywordPassthrough(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "李四"}, {StaffID: "3", StaffName: "张伟"},
	}, total: 3}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{Name: "张"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !ua.called || ua.lastKW != "张" {
		t.Fatalf("userapi keyword 透传失败: called=%v kw=%q", ua.called, ua.lastKW)
	}
	if res.Total != 2 || len(res.List) != 2 {
		t.Fatalf("want total 2, got %d / len %d", res.Total, len(res.List))
	}
	findProfileItem(t, res, "张敏")
	findProfileItem(t, res, "张伟")

	// 首尾空格防御性裁剪后再透传。
	if _, err := svc.List(context.Background(), service.ProfileFilter{Name: "  张 "}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if ua.lastKW != "张" {
		t.Fatalf("trim 后 keyword want 张, got %q", ua.lastKW)
	}
}

// TestProfileList_UpstreamFail_1305 验证上游名单任一页失败整体 1305，后半段查询不执行。
func TestProfileList_UpstreamFail_1305(t *testing.T) {
	ua := &fakeUserapiClient{err: errors.New("upstream timeout")}
	ag := &fakeProfileAggScores{}
	ds := &fakeProfileDimScores{}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, ds, ag,
		&fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{})
	wantCode(t, err, errcode.StaffListUnavailable)
	if res != nil {
		t.Fatalf("失败时应无结果，got %+v", res)
	}
	if ag.called || ds.called {
		t.Fatal("上游失败后不应执行画像查询后半段")
	}
}

// TestProfileList_DimensionFilter_Shortboard 验证短板筛选：短板集合按 included_json 判据
// 取最低分（specs §4.1.4 规则4）；dimension_code 未启用/非两模块 code 返 1400。
func TestProfileList_DimensionFilter_Shortboard(t *testing.T) {
	ps, pe := profilePeriod()
	dims := &fakeProfileDimensionRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true},
	}}
	zmScore, lsScore := 70.0, 65.0
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &zmScore, PeriodStartAt: ps, PeriodEndAt: pe,
			IncludedJSON: `[{"code":"A","weight":50},{"code":"B","weight":50}]`},
		{TokenName: "李四", Module: domain.ModuleAIUsage, ModuleScore: &lsScore, PeriodStartAt: ps, PeriodEndAt: pe,
			IncludedJSON: `[{"code":"A","weight":100}]`},
	}}
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 70, Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 50, Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "李四", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 65, Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "李四"},
	}, total: 2}
	svc := newProfileSvc(dims, ds, ag, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	// 短板集合：张敏={B}（70 vs 50），李四={A}（唯一维度）。
	res, err := svc.List(context.Background(), service.ProfileFilter{DimensionCode: "B"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 || len(res.List) != 1 || res.List[0].StaffName != "张敏" {
		t.Fatalf("筛 B 应仅张敏命中，got %+v", res.List)
	}
	res, err = svc.List(context.Background(), service.ProfileFilter{DimensionCode: "A"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 || res.List[0].StaffName != "李四" {
		t.Fatalf("筛 A 应仅李四命中，got %+v", res.List)
	}

	// 未注册 code → 1400。
	_, err = svc.List(context.Background(), service.ProfileFilter{DimensionCode: "C"})
	wantCode(t, err, errcode.BadRequest)

	// 停用维度与 ENNEAGRAM 模块维度均不在合法选项集。
	dims.all = append(dims.all,
		domain.Dimension{Code: "C", ModuleCode: domain.ModuleAIUsage, Enabled: false},
		dimensionEnneagramEnabled(),
	)
	_, err = svc.List(context.Background(), service.ProfileFilter{DimensionCode: "C"})
	wantCode(t, err, errcode.BadRequest)
	_, err = svc.List(context.Background(), service.ProfileFilter{DimensionCode: "ENN_X"})
	wantCode(t, err, errcode.BadRequest)
}

// dimensionEnneagramEnabled 造一个启用但模块为 ENNEAGRAM 的维度（校验选项集排除用）。
func dimensionEnneagramEnabled() domain.Dimension {
	return domain.Dimension{Code: "ENN_X", ModuleCode: domain.ModuleEnneagram, Enabled: true}
}

// TestProfileList_UnusedOnlyOverridesActivity 验证仅看未使用与活跃度叠加时以未使用为准
// （specs §4.1.2 A）。
func TestProfileList_UnusedOnlyOverridesActivity(t *testing.T) {
	ps, pe := profilePeriod()
	ac := &fakeProfileActivityStats{latest: []domain.ActivityStat{
		{TokenName: "张敏", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "李四", ActiveLevel: domain.ActiveLevelUnused, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "李四"},
	}, total: 2}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, ac, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{ActivityLevel: "active", UnusedOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 || len(res.List) != 1 || res.List[0].StaffName != "李四" {
		t.Fatalf("UnusedOnly 应强制 unused 仅剩李四，got %+v", res.List)
	}

	// 关闭 UnusedOnly 时 ActivityLevel 正常生效（仅 active 张敏）。
	res, err = svc.List(context.Background(), service.ProfileFilter{ActivityLevel: "active"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 || res.List[0].StaffName != "张敏" {
		t.Fatalf("activity_level=active 应仅张敏，got %+v", res.List)
	}
}

// TestProfileList_SortAndPaging 验证姓名升序排序（specs §8.3）与内存分页。
func TestProfileList_SortAndPaging(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "王五"}, {StaffID: "2", StaffName: "张敏"}, {StaffID: "3", StaffName: "李四"},
	}, total: 3}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"张敏", "李四", "王五"} // UTF-8 字符串序：张 < 李 < 王
	if len(res.List) != 3 {
		t.Fatalf("len want 3, got %d", len(res.List))
	}
	for i, name := range want {
		if res.List[i].StaffName != name {
			t.Fatalf("list[%d] want %s, got %s", i, name, res.List[i].StaffName)
		}
	}

	res2, err := svc.List(context.Background(), service.ProfileFilter{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if res2.Total != 3 {
		t.Fatalf("total want 3, got %d", res2.Total)
	}
	if len(res2.List) != 1 || res2.List[0].StaffName != "王五" {
		t.Fatalf("page2/size2 应仅剩王五，got %+v", res2.List)
	}
	if res2.Page != 2 || res2.PageSize != 2 {
		t.Fatalf("回显分页参数错误: page=%d size=%d", res2.Page, res2.PageSize)
	}
}

// TestProfileList_BadActivityLevel 验证 activity_level 非枚举值返 1400 且不触上游。
func TestProfileList_BadActivityLevel(t *testing.T) {
	ua := &fakeUserapiClient{}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)
	for _, level := range []string{"frequent", "ACTIVE", "unknown"} {
		_, err := svc.List(context.Background(), service.ProfileFilter{ActivityLevel: level})
		wantCode(t, err, errcode.BadRequest)
	}
	if ua.called {
		t.Fatal("校验失败不应触达上游")
	}
}

// TestProfileList_PageSizeClampAndDefaults 验证分页参数默认值与钳位。
func TestProfileList_PageSizeClampAndDefaults(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("默认分页 want 1/10, got %d/%d", res.Page, res.PageSize)
	}
	res, err = svc.List(context.Background(), service.ProfileFilter{PageSize: 500})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.PageSize != 100 {
		t.Fatalf("page_size 钳位 want 100, got %d", res.PageSize)
	}
	res, err = svc.List(context.Background(), service.ProfileFilter{Page: -1, PageSize: -3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("负值回退 want 1/10, got %d/%d", res.Page, res.PageSize)
	}
}

// TestProfileList_EmptyStaffList 验证上游空名单返回空 List + total 0（错误态判定归前端）。
func TestProfileList_EmptyStaffList(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{}, total: 0}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)
	res, err := svc.List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("total want 0, got %d", res.Total)
	}
	if res.List == nil || len(res.List) != 0 {
		t.Fatalf("List 应为非 nil 空切片, got %+v", res.List)
	}
}

// TestProfileList_SecretNotConfigured 验证密钥未配置映射 1305 且不触上游。
func TestProfileList_SecretNotConfigured(t *testing.T) {
	ua := &fakeUserapiClient{}
	encKey := crypto.DeriveKey("test-profile")
	svc := service.NewProfileService(&fakeProfileDimScores{}, &fakeProfileAggScores{},
		&fakeProfileActivityStats{}, &fakeProfileResults{}, &fakeProfileDimensionRepo{},
		ua, &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1}}, string(encKey))
	_, err := svc.List(context.Background(), service.ProfileFilter{})
	wantCode(t, err, errcode.StaffListUnavailable)
	if ua.called {
		t.Fatal("密钥未配置不应触达上游")
	}
}

// TestProfileList_DedupStaffNames 验证名单按 staff_name 去重保首见。
func TestProfileList_DedupStaffNames(t *testing.T) {
	ua := &fakeUserapiClient{staffs: []userapi.Staff{
		{StaffID: "1", StaffName: "张敏"}, {StaffID: "2", StaffName: "李四"}, {StaffID: "3", StaffName: "张敏"},
	}, total: 3}
	svc := newProfileSvc(&fakeProfileDimensionRepo{}, &fakeProfileDimScores{},
		&fakeProfileAggScores{}, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)
	res, err := svc.List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 2 || len(res.List) != 2 {
		t.Fatalf("去重后 want 2, got %d / %d", res.Total, len(res.List))
	}
	findProfileItem(t, res, "张敏")
	findProfileItem(t, res, "李四")
}

// TestProfileList_DegradedFailedRow 验证 failed 维度行同样触发模块降权标记（specs §4.1.4 规则3）。
func TestProfileList_DegradedFailedRow(t *testing.T) {
	ps, pe := profilePeriod()
	score := 66.0
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 0,
			Status: domain.ScoreStatusFailed, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIMgmt, ModuleScore: &score,
			PeriodStartAt: ps, PeriodEndAt: pe, IncludedJSON: `[]`},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
	res, err := newProfileSvc(&fakeProfileDimensionRepo{}, ds, ag,
		&fakeProfileActivityStats{}, &fakeProfileResults{}, ua).List(context.Background(), service.ProfileFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	zm := findProfileItem(t, res, "张敏")
	if !zm.AIMGMTDegraded {
		t.Fatal("failed 维度行应触发 ai_mgmt_degraded")
	}
	if zm.AIUsageDegraded {
		t.Fatal("AI_USAGE 无该周期维度行不应降权")
	}
}

// TestProfileList_ShortboardTieAllSelected 验证最低分并列时全部入选短板集合（specs §4.1.4 规则4）。
func TestProfileList_ShortboardTieAllSelected(t *testing.T) {
	ps, pe := profilePeriod()
	dims := &fakeProfileDimensionRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true},
	}}
	score := 60.0
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: ps, PeriodEndAt: pe,
			IncludedJSON: `[{"code":"A","weight":50},{"code":"B","weight":50}]`},
	}}
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 50, Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 50, Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
	svc := newProfileSvc(dims, ds, ag, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	for _, code := range []string{"A", "B"} {
		res, err := svc.List(context.Background(), service.ProfileFilter{DimensionCode: code})
		if err != nil {
			t.Fatalf("List(%s): %v", code, err)
		}
		if res.Total != 1 {
			t.Fatalf("并列短板 %s 应命中张敏, total=%d", code, res.Total)
		}
	}
}

// TestProfileList_ShortboardFailedRowNotCounted 验证仅 status=success 维度行计入短板分数，
// failed 行即使出现在 included 集合也无有效分不参与最低分比较。
func TestProfileList_ShortboardFailedRowNotCounted(t *testing.T) {
	ps, pe := profilePeriod()
	dims := &fakeProfileDimensionRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "B", ModuleCode: domain.ModuleAIUsage, Enabled: true},
	}}
	score := 50.0
	ag := &fakeProfileAggScores{latest: []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: ps, PeriodEndAt: pe,
			IncludedJSON: `[{"code":"A","weight":50},{"code":"B","weight":50}]`},
	}}
	ds := &fakeProfileDimScores{latest: []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 0,
			Status: domain.ScoreStatusFailed, PeriodStartAt: ps, PeriodEndAt: pe},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "B", Score: 50,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe},
	}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
	svc := newProfileSvc(dims, ds, ag, &fakeProfileActivityStats{}, &fakeProfileResults{}, ua)

	res, err := svc.List(context.Background(), service.ProfileFilter{DimensionCode: "B"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("B 为唯一有效分维度应命中, total=%d", res.Total)
	}
	res, err = svc.List(context.Background(), service.ProfileFilter{DimensionCode: "A"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("A failed 行不计分不应命中, total=%d", res.Total)
	}
}
