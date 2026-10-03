// profile_test 对个人画像域业务层做黑盒单元测试（specs P2_PRF_001 §5.1/§5.2）。
//
// 五仓储 fake + fakeUserapiClient 驱动，不依赖真实 DB / 外部 HTTP。覆盖：
//   - 左联缺失行口径：无行 unused / 分数 nil / 九型 nil、insufficient 维度行降权（specs §4.1.4 规则2/3）
//   - 姓名 keyword 透传上游 + 拉回内存包含校验兜底（03 A1）
//   - 上游名单失败整体 1305，后半段查询不执行（specs §5.1.4 规则1）
//   - 短板维度筛选：included_json 判据、最低分并列全入选、failed 行不计分（specs §4.1.4 规则4、03 §1.9）
//   - 仅看未使用覆盖活跃度筛选（specs §4.1.2 A）
//   - 姓名升序排序与内存分页（specs §8.3 偏离记录）
//   - B1 详情：区间并集/selected 校验/评分卡状态优先级/置信度/走势/公司均分/空画像/姓名回退（specs §5.2）
package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeProfileDimScores 是 repository.DimensionScoreRepository 的测试假实现：
// ListLatestByTokens 供 A1 消费面，ListByToken / ListByPeriodAllCompany 供 B1 详情。
type fakeProfileDimScores struct {
	latest    []domain.DimensionScore
	byToken   []domain.DimensionScore
	company   []domain.DimensionScore
	companyIn map[string]time.Time // ListByPeriodAllCompany 最后一次入参探针
	called    bool
	err       error
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

func (f *fakeProfileDimScores) ListByToken(_ context.Context, _ string) ([]domain.DimensionScore, error) {
	return f.byToken, f.err
}

func (f *fakeProfileDimScores) ListLatestByTokens(_ context.Context, _ []string) ([]domain.DimensionScore, error) {
	f.called = true
	return f.latest, f.err
}

func (f *fakeProfileDimScores) ListByPeriodAllCompany(_ context.Context, start, end int64) ([]domain.DimensionScore, error) {
	if f.companyIn == nil {
		f.companyIn = map[string]time.Time{}
	}
	f.companyIn["start"] = time.Unix(start, 0).UTC()
	f.companyIn["end"] = time.Unix(end, 0).UTC()
	return f.company, f.err
}

var _ repository.DimensionScoreRepository = (*fakeProfileDimScores)(nil)

// fakeProfileAggScores 是 repository.AggregateScoreRepository 的测试假实现。
type fakeProfileAggScores struct {
	latest  []domain.AggregateScore
	byToken []domain.AggregateScore
	called  bool
	err     error
}

func (f *fakeProfileAggScores) UpsertAll(context.Context, string, int64, int64, []domain.AggregateScore) error {
	return nil
}

func (f *fakeProfileAggScores) ListByToken(_ context.Context, _ string) ([]domain.AggregateScore, error) {
	return f.byToken, f.err
}

func (f *fakeProfileAggScores) ListLatestModuleRowsByTokens(_ context.Context, _ []string) ([]domain.AggregateScore, error) {
	f.called = true
	return f.latest, f.err
}

var _ repository.AggregateScoreRepository = (*fakeProfileAggScores)(nil)

// fakeProfileActivityStats 是 repository.ActivityStatRepository 的测试假实现。
type fakeProfileActivityStats struct {
	latest  []domain.ActivityStat
	byToken []domain.ActivityStat
	err     error
}

func (f *fakeProfileActivityStats) Upsert(context.Context, *domain.ActivityStat) error {
	return nil
}

func (f *fakeProfileActivityStats) ListByToken(_ context.Context, _ string) ([]domain.ActivityStat, error) {
	return f.byToken, f.err
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

// profileWeek 第 n 周测试周期（n=0 为本期，负数往前推）。落库行模拟生产口径：
// 本地零点 Unix 秒经 .UTC() 显示（domain 注释同口径），与 time.Local 解析参数等值匹配。
func profileWeek(n int) (time.Time, time.Time) {
	return time.Date(2026, 9, 22, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n),
		time.Date(2026, 9, 29, 0, 0, 0, 0, time.Local).UTC().AddDate(0, 0, 7*n)
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
	return service.NewProfileService(ds, ag, ac, rs, dims, ua, secretRepo, encKey)
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
		ua, &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1}}, encKey)
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

// ===== B1 详情聚合（specs §5.2）=====

// evidenceJSONFor 构造 conversation 来源 evidence_json（session_keys 逐个生成 + summary 快照）。
func evidenceJSONFor(sessionCount int, summary map[string]int) string {
	keys := make([]string, 0, sessionCount)
	for i := 0; i < sessionCount; i++ {
		keys = append(keys, fmt.Sprintf("s-%02d", i))
	}
	b, err := json.Marshal(map[string]any{
		"session_keys": keys,
		"summary":      summary,
		"dimension_specs": []map[string]any{
			{"code": "A", "weight": 100, "in_overview": true},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// detailEnv B1 测试标准环境：1 个启用 AI_USAGE 维度 A + 1 个启用 AI_MGMT 维度 M1，
// userapi 可解析张敏。
func detailEnv() (*fakeProfileDimensionRepo, *fakeProfileDimScores, *fakeProfileAggScores,
	*fakeProfileActivityStats, *fakeProfileResults, *fakeUserapiClient) {
	dims := &fakeProfileDimensionRepo{all: []domain.Dimension{
		{Code: "A", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		{Code: "M1", ModuleCode: domain.ModuleAIMgmt, Enabled: true},
	}}
	ds := &fakeProfileDimScores{}
	ag := &fakeProfileAggScores{}
	ac := &fakeProfileActivityStats{}
	rs := &fakeProfileResults{}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "1", StaffName: "张敏"}}, total: 1}
	return dims, ds, ag, ac, rs, ua
}

// TestProfileDetail_PeriodUnionAndDefault 验证区间列表为三表周期并集新到旧、
// is_current 仅首项 true、未传区间默认 selected=首项（specs §4.2.4 规则1）。
func TestProfileDetail_PeriodUnionAndDefault(t *testing.T) {
	// 期次：w0（本期）、w-1、w-2。activity 2 期（w0/w-1）、dimension_score 3 期
	//（w-2 独有）、aggregate 2 期（w0/w-1）→ 并集 3 期。
	p0s, p0e := profileWeek(0)
	p1s, p1e := profileWeek(-1)
	p2s, p2e := profileWeek(-2)
	score := 82.35
	dims, ds, ag, ac, rs, ua := detailEnv()
	ac.byToken = []domain.ActivityStat{
		{TokenName: "张敏", ActiveLevel: domain.ActiveLevelActive, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "张敏", ActiveLevel: domain.ActiveLevelLowFreq, PeriodStartAt: p1s, PeriodEndAt: p1e},
	}
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 85,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 82,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p1s, PeriodEndAt: p1e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 79,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p2s, PeriodEndAt: p2e, Source: domain.ScoreSourceConversation},
	}
	ag.byToken = []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: p0s, PeriodEndAt: p0e, IncludedJSON: `[{"code":"A","weight":100}]`},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: p1s, PeriodEndAt: p1e, IncludedJSON: `[{"code":"A","weight":100}]`},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(res.Periods) != 3 {
		t.Fatalf("periods 并集去重 want 3, got %d", len(res.Periods))
	}
	wantStarts := []string{"2026-09-22", "2026-09-15", "2026-09-08"}
	wantEnds := []string{"2026-09-28", "2026-09-21", "2026-09-14"}
	for i := range wantStarts {
		if res.Periods[i].PeriodStart != wantStarts[i] || res.Periods[i].PeriodEnd != wantEnds[i] {
			t.Fatalf("periods[%d] want %s~%s, got %s~%s", i, wantStarts[i], wantEnds[i], res.Periods[i].PeriodStart, res.Periods[i].PeriodEnd)
		}
		if res.Periods[i].IsCurrent != (i == 0) {
			t.Fatalf("periods[%d].is_current want %v", i, i == 0)
		}
	}
	if res.SelectedPeriod == nil || res.SelectedPeriod.PeriodStart != "2026-09-22" || res.SelectedPeriod.PeriodEnd != "2026-09-28" {
		t.Fatalf("未传区间 selected 应为最新期，got %+v", res.SelectedPeriod)
	}
	if res.ActivityLevel != "active" {
		t.Fatalf("activity_level want active（所选区间行）, got %s", res.ActivityLevel)
	}

	// 传区间命中 w-1：activity 降级 low_freq，is_current 标注不变（首项仍 true）。
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "2026-09-15", "2026-09-21")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.SelectedPeriod == nil || res.SelectedPeriod.PeriodStart != "2026-09-15" {
		t.Fatalf("传入区间 selected 应为 w-1, got %+v", res.SelectedPeriod)
	}
	if res.ActivityLevel != "low_freq" {
		t.Fatalf("w-1 activity_level want low_freq, got %s", res.ActivityLevel)
	}
}

// TestProfileDetail_BadParams_1400 验证 staff_name 空串、区间只传一端、日期非法均 1400。
func TestProfileDetail_BadParams_1400(t *testing.T) {
	dims, ds, ag, ac, rs, ua := detailEnv()
	svc := newProfileSvc(dims, ds, ag, ac, rs, ua)
	cases := []struct{ ps, pe string }{
		{"", ""},
		{"2026-09-22", ""},
		{"", "2026-09-28"},
		{"2026/09/22", "2026-09-28"},
		{"2026-09-22", "09-28-2026"},
	}
	for _, c := range cases {
		_, err := svc.Detail(context.Background(), "", c.ps, c.pe)
		wantCode(t, err, errcode.BadRequest)
	}
	// 空姓名配正常区间同样 1400。
	_, err := svc.Detail(context.Background(), "张敏", "2026-09-22", "")
	wantCode(t, err, errcode.BadRequest)
}

// TestProfileDetail_PeriodInvalid_2001 验证传入双界不在区间列表的区间返 2001。
func TestProfileDetail_PeriodInvalid_2001(t *testing.T) {
	ps, pe := profileWeek(0)
	score := 82.35
	dims, ds, ag, ac, rs, ua := detailEnv()
	ag.byToken = []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: ps, PeriodEndAt: pe},
	}
	_, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "2026-09-01", "2026-09-07")
	wantCode(t, err, errcode.ProfilePeriodInvalid)
}

// TestProfileDetail_ChangeVsPrev_Rounding 验证较上期按取整分差、无上一期 nil（03 §1.5）。
func TestProfileDetail_ChangeVsPrev_Rounding(t *testing.T) {
	cur, prev := 82.35, 78.6
	p0s, p0e := profileWeek(0)
	p1s, p1e := profileWeek(-1)
	dims, ds, ag, ac, rs, ua := detailEnv()
	ag.byToken = []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &cur, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &prev, PeriodStartAt: p1s, PeriodEndAt: p1e},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	usage := res.Modules[0]
	if usage.ChangeVsPrev == nil || *usage.ChangeVsPrev != 3 {
		t.Fatalf("change_vs_prev want 3（round(82.35)-round(78.6)=82-79）, got %v", usage.ChangeVsPrev)
	}
	if usage.Score == nil || *usage.Score != 82.35 {
		t.Fatalf("模块分应原始浮点直出 82.35, got %v", usage.Score)
	}
	mgmt := res.Modules[1]
	if mgmt.ChangeVsPrev != nil {
		t.Fatalf("AI_MGMT 无聚合行 change_vs_prev 应 nil, got %v", *mgmt.ChangeVsPrev)
	}

	// 仅有本期（无下一区间）→ nil。
	ag.byToken = ag.byToken[:1]
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Modules[0].ChangeVsPrev != nil {
		t.Fatalf("无下一区间 change_vs_prev 应 nil, got %v", *res.Modules[0].ChangeVsPrev)
	}
}

// TestProfileDetail_EvaluatedAt 验证评估时间为 period_end 前一日且走 Local 转换（BR9）。
func TestProfileDetail_EvaluatedAt(t *testing.T) {
	score := 82.35
	p0s, p0e := profileWeek(0)
	p1s, p1e := profileWeek(-1)
	dims, ds, ag, ac, rs, ua := detailEnv()
	ag.byToken = []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "张敏", Module: domain.ModuleAIMgmt, ModuleScore: &score, PeriodStartAt: p1s, PeriodEndAt: p1e},
	}
	// 两模块区间错位（specs §4.2.4 规则1：除九型外统一按所选区间取数）：
	// selected=w-1，AI_USAGE 无该期聚合行 → nil；AI_MGMT 命中 w-1 行。
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "2026-09-15", "2026-09-21")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Modules[0].EvaluatedAt != nil || res.Modules[0].Score != nil {
		t.Fatalf("AI_USAGE 在 w-1 无聚合行应 nil, got %+v", res.Modules[0])
	}
	if res.Modules[1].EvaluatedAt == nil || *res.Modules[1].EvaluatedAt != "2026-09-21" {
		t.Fatalf("AI_MGMT 所选区间 w-1（09-15~09-21 含止日）evaluated_at want 2026-09-21, got %v", derefStr(res.Modules[1].EvaluatedAt))
	}
	if res.Modules[1].Score == nil || *res.Modules[1].Score != 82.35 {
		t.Fatalf("AI_MGMT 分数应取所选区间行 82.35, got %v", res.Modules[1].Score)
	}

	// 默认最新区间 w0：AI_USAGE evaluated_at=2026-09-28（end 前一日，Local 转换）。
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Modules[0].EvaluatedAt == nil || *res.Modules[0].EvaluatedAt != "2026-09-28" {
		t.Fatalf("AI_USAGE w0 evaluated_at want 2026-09-28, got %v", res.Modules[0].EvaluatedAt)
	}
}

// TestProfileDetail_DataStatus_Priority 验证状态优先级 pending > missing > degraded >
// complete 与三计数（specs §4.2.4 规则8）。
func TestProfileDetail_DataStatus_Priority(t *testing.T) {
	p0s, p0e := profileWeek(0)
	score := 82.35
	mgmtScore := 76.0
	// AI_MGMT 无聚合行 → pending；AI_USAGE 1 failed + 1 insufficient → missing（缺失优先）。
	dims, ds, ag, ac, rs, ua := detailEnv()
	dims.all = append(dims.all,
		domain.Dimension{Code: "A2", ModuleCode: domain.ModuleAIUsage, Enabled: true})
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 0,
			Status: domain.ScoreStatusFailed, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A2", Score: 70,
			Status: domain.ScoreStatusSuccess, Insufficient: true, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
	}
	ag.byToken = []domain.AggregateScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, ModuleScore: &score, PeriodStartAt: p0s, PeriodEndAt: p0e},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	usage := res.Modules[0]
	if usage.DataStatus != "missing" {
		t.Fatalf("AI_USAGE failed+insufficient 并存 want missing, got %s", usage.DataStatus)
	}
	if usage.FailedCount != 1 || usage.InsufficientCount != 1 {
		t.Fatalf("AI_USAGE 计数 want failed=1 insufficient=1, got %+v", usage)
	}
	mgmt := res.Modules[1]
	if mgmt.DataStatus != "pending" {
		t.Fatalf("AI_MGMT 无聚合行 want pending, got %s", mgmt.DataStatus)
	}
	if mgmt.FailedCount != 0 || mgmt.InsufficientCount != 0 || mgmt.MissingCount != 1 {
		t.Fatalf("AI_MGMT 计数 want 0/0/1（M1 无行 missing）, got %+v", mgmt)
	}

	// 仅 insufficient（无 failed、无缺失维度）→ degraded。
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 70,
			Status: domain.ScoreStatusSuccess, Insufficient: true, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A2", Score: 71,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
	}
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Modules[0].DataStatus != "degraded" {
		t.Fatalf("AI_USAGE 仅 insufficient want degraded, got %s", res.Modules[0].DataStatus)
	}

	// 全 success 无缺失 → complete（AI_MGMT 造聚合行 + M1 评分行）。
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 70,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A2", Score: 71,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 76,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceActiveTest},
	}
	ag.byToken = append(ag.byToken, domain.AggregateScore{
		TokenName: "张敏", Module: domain.ModuleAIMgmt, ModuleScore: &mgmtScore, PeriodStartAt: p0s, PeriodEndAt: p0e})
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Modules[0].DataStatus != "complete" || res.Modules[1].DataStatus != "complete" {
		t.Fatalf("全 success want complete/complete, got %s/%s", res.Modules[0].DataStatus, res.Modules[1].DataStatus)
	}
}

// TestProfileDetail_ConfidenceMapping 验证置信度映射（specs §4.2.4 规则5）：
// conversation 按 session_keys 数量 ≥10 high / 2-9 medium / <2 low；
// active_test 恒 high、session_count 0、summary 空 object。
func TestProfileDetail_ConfidenceMapping(t *testing.T) {
	p0s, p0e := profileWeek(0)
	updatedAt := time.Date(2026, 9, 28, 23, 15, 0, 0, time.UTC)
	dims, ds, ag, ac, rs, ua := detailEnv()
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 85,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e,
			Source: domain.ScoreSourceConversation, UpdatedAt: updatedAt,
			EvidenceJSON: evidenceJSONFor(12, map[string]int{"total_turns": 156, "instruction_segments": 23})},
		{TokenName: "张敏", Module: domain.ModuleAIMgmt, DimensionCode: "M1", Score: 76,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e,
			Source: domain.ScoreSourceActiveTest, UpdatedAt: updatedAt,
			EvidenceJSON: `{"session_keys":[],"summary":{},"dimension_specs":[{"code":"M1","weight":100}]}`},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(res.Dimensions) != 2 {
		t.Fatalf("dimensions want 2, got %d", len(res.Dimensions))
	}
	a := res.Dimensions[0]
	if len(a.Evidences) != 1 {
		t.Fatalf("A evidences want 1, got %d", len(a.Evidences))
	}
	ev := a.Evidences[0]
	if ev.Source != "conversation" || ev.Confidence != "high" || ev.SessionCount != 12 {
		t.Fatalf("A 证据 want conversation/high/12, got %+v", ev)
	}
	if ev.Time != updatedAt.Local().Format("2006-01-02 15:04") {
		t.Fatalf("A 证据 time want %s, got %s", updatedAt.Local().Format("2006-01-02 15:04"), ev.Time)
	}
	if ev.Summary["total_turns"] != 156 || ev.Summary["instruction_segments"] != 23 {
		t.Fatalf("A summary 透传失败: %+v", ev.Summary)
	}
	m1 := res.Dimensions[1]
	if len(m1.Evidences) != 1 {
		t.Fatalf("M1 evidences want 1, got %d", len(m1.Evidences))
	}
	mev := m1.Evidences[0]
	if mev.Source != "active_test" || mev.Confidence != "high" || mev.SessionCount != 0 {
		t.Fatalf("M1 证据 want active_test/high/0, got %+v", mev)
	}
	if len(mev.Summary) != 0 {
		t.Fatalf("M1 summary want 空 object, got %+v", mev.Summary)
	}

	// session_keys 1 个 → low；非法 JSON 降级 low + 空 summary。
	ds.byToken[0].EvidenceJSON = evidenceJSONFor(1, map[string]int{"total_turns": 3})
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Dimensions[0].Evidences[0].Confidence != "low" || res.Dimensions[0].Evidences[0].SessionCount != 1 {
		t.Fatalf("1 信号 want low/1, got %+v", res.Dimensions[0].Evidences[0])
	}
	ds.byToken[0].EvidenceJSON = "{not-json"
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	ev = res.Dimensions[0].Evidences[0]
	if ev.Confidence != "low" || ev.SessionCount != 0 || len(ev.Summary) != 0 {
		t.Fatalf("非法 JSON 降级 want low/0/空, got %+v", ev)
	}
}

// TestProfileDetail_DimensionRowStatus 验证维度行三态映射与缺失口径（specs §4.2.2 D）：
// success 直出（insufficient 照常出分标 insufficient）、failed/无行 missing 全空。
func TestProfileDetail_DimensionRowStatus(t *testing.T) {
	p0s, p0e := profileWeek(0)
	updatedAt := time.Date(2026, 9, 28, 23, 15, 0, 0, time.UTC)
	dims, ds, ag, ac, rs, ua := detailEnv()
	dims.all = append(dims.all,
		domain.Dimension{Code: "A2", ModuleCode: domain.ModuleAIUsage, Enabled: true, GroupCode: ptr("BASE")},
		domain.Dimension{Code: "A3", ModuleCode: domain.ModuleAIUsage, Enabled: true},
		domain.Dimension{Code: "A4", ModuleCode: domain.ModuleAIUsage, Enabled: false},
		domain.Dimension{Code: "ENN_X", ModuleCode: domain.ModuleEnneagram, Enabled: true})
	dims.all[0].Name = "指令清晰度"
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 85,
			Status: domain.ScoreStatusSuccess, Rationale: "清晰", PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation,
			UpdatedAt: updatedAt, EvidenceJSON: evidenceJSONFor(10, map[string]int{"total_turns": 20})},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A2", Score: 70,
			Status: domain.ScoreStatusSuccess, Insufficient: true, Rationale: "样本不足", PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A3", Score: 0,
			Status: domain.ScoreStatusFailed, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	// 启用且 module ∈ 两模块：A/M1/A2/A3（A4 停用、ENN_X 排除），输出序随维度配置。
	if len(res.Dimensions) != 4 {
		t.Fatalf("dimensions want 4, got %d（%+v）", len(res.Dimensions), res.Dimensions)
	}
	byCode := map[string]service.ProfileDimensionRow{}
	for _, d := range res.Dimensions {
		byCode[d.DimensionCode] = d
	}
	a, a2, a3, m1 := byCode["A"], byCode["A2"], byCode["A3"], byCode["M1"]
	if a.DimensionName != "指令清晰度" || a.Status != "normal" || a.Score == nil || *a.Score != 85 {
		t.Fatalf("A 行 want normal/85/指令清晰度, got %+v", a)
	}
	if len(a.Evidences) != 1 || a.Evidences[0].Time != updatedAt.Local().Format("2006-01-02 15:04") {
		t.Fatalf("A 行证据时间取行 updated_at 本地时区: %+v", a.Evidences)
	}
	if a.GroupCode != nil {
		t.Fatalf("A 行 group_code 应 nil（维度配置 nil）, got %v", *a.GroupCode)
	}
	if a2.Status != "insufficient" || a2.Score == nil || *a2.Score != 70 {
		t.Fatalf("A2 行 want insufficient/70 照常出分, got %+v", a2)
	}
	if a2.GroupCode == nil || *a2.GroupCode != "BASE" {
		t.Fatalf("A2 行 group_code want BASE, got %+v", a2.GroupCode)
	}
	if a3.Status != "missing" || a3.Score != nil || a3.Rationale != "" || len(a3.Evidences) != 0 || len(a3.Trend) != 0 {
		t.Fatalf("A3 failed 行 want missing 全空, got %+v", a3)
	}
	if m1.Status != "missing" || m1.Score != nil {
		t.Fatalf("M1 无行 want missing, got %+v", m1)
	}
}

// ptr 字符串指针便捷构造。
func ptr(s string) *string { return &s }

// derefStr 指针解引用（nil 返回 "<nil>"），仅失败信息展示用。
func derefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestProfileDetail_TrendWindow 验证近 4 期走势（specs §4.2.2 D）：全 success 旧到新
// 截最近 4 期、failed 期不入序列。
func TestProfileDetail_TrendWindow(t *testing.T) {
	dims, ds, ag, ac, rs, ua := detailEnv()
	// 6 期 [70,75,80,85,88,90]（w-5..w0），trend 取最近 4 期 [80,85,88,90]。
	scores := []int{70, 75, 80, 85, 88, 90}
	for i, sc := range scores {
		ps, pe := profileWeek(i - 5)
		ds.byToken = append(ds.byToken, domain.DimensionScore{
			TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: sc,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: ps, PeriodEndAt: pe, Source: domain.ScoreSourceConversation,
		})
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	trend := res.Dimensions[0].Trend
	if len(trend) != 4 {
		t.Fatalf("trend want 4（窗口截断）, got %d", len(trend))
	}
	wantScores := []int{80, 85, 88, 90}
	wantStarts := []string{"2026-09-01", "2026-09-08", "2026-09-15", "2026-09-22"}
	for i := range wantScores {
		if trend[i].Score != wantScores[i] || trend[i].PeriodStart != wantStarts[i] {
			t.Fatalf("trend[%d] want %d/%s, got %d/%s", i, wantScores[i], wantStarts[i], trend[i].Score, trend[i].PeriodStart)
		}
	}

	// 中间一期（w-2，score 85）改 failed → 不入序列；剩 5 期 success 取尾 4 = [75,80,88,90]。
	ds.byToken[3].Status = domain.ScoreStatusFailed
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	trend = res.Dimensions[0].Trend
	if len(trend) != 4 {
		t.Fatalf("failed 期不入序列后仍取最近 4 期 want 4, got %d", len(trend))
	}
	for _, tr := range trend {
		if tr.Score == 85 {
			t.Fatalf("failed 期 85 不应出现在 trend: %+v", trend)
		}
	}
	if trend[0].Score != 75 {
		t.Fatalf("trend[0] want 75, got %d", trend[0].Score)
	}
}

// TestProfileDetail_CompanyAvg 验证公司均分（specs §4.2.4 规则4）：剔 insufficient/failed
// 求算术平均、<3 人 nil。
func TestProfileDetail_CompanyAvg(t *testing.T) {
	p0s, p0e := profileWeek(0)
	dims, ds, ag, ac, rs, ua := detailEnv()
	// 本人落库行（避免空画像分支），公司侧数据独立注入。
	ds.byToken = []domain.DimensionScore{
		{TokenName: "张敏", Module: domain.ModuleAIUsage, DimensionCode: "A", Score: 85,
			Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e, Source: domain.ScoreSourceConversation},
	}
	// 全公司 4 行：张敏 85（本人）、李四 70、王五 insufficient 90（剔除）、赵六 failed 0（剔除）
	// → 有效 2 人 <3 → nil。
	ds.company = []domain.DimensionScore{
		{TokenName: "张敏", DimensionCode: "A", Score: 85, Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "李四", DimensionCode: "A", Score: 70, Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "王五", DimensionCode: "A", Score: 90, Status: domain.ScoreStatusSuccess, Insufficient: true, PeriodStartAt: p0s, PeriodEndAt: p0e},
		{TokenName: "赵六", DimensionCode: "A", Score: 0, Status: domain.ScoreStatusFailed, PeriodStartAt: p0s, PeriodEndAt: p0e},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Dimensions[0].CompanyAvg != nil {
		t.Fatalf("2 人有效 want nil, got %v", *res.Dimensions[0].CompanyAvg)
	}

	// 补第 3 个有效人孙七 72 → 3 人 (85+70+72)/3 = 75.666…。
	ds.company = append(ds.company, domain.DimensionScore{
		TokenName: "孙七", DimensionCode: "A", Score: 72, Status: domain.ScoreStatusSuccess, PeriodStartAt: p0s, PeriodEndAt: p0e})
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Dimensions[0].CompanyAvg == nil || math.Abs(*res.Dimensions[0].CompanyAvg-75.66666666666667) > 1e-9 {
		t.Fatalf("3 人均分 want ≈75.67, got %v", res.Dimensions[0].CompanyAvg)
	}

	// 全公司仅 2 行（含本人）→ nil。
	ds.company = ds.company[:2]
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Dimensions[0].CompanyAvg != nil {
		t.Fatalf("全公司仅 2 人 want nil, got %v", *res.Dimensions[0].CompanyAvg)
	}
}

// TestProfileDetail_Enneagram 验证九型取最新 scored 判型行、distribution 透传、无判型 nil。
func TestProfileDetail_Enneagram(t *testing.T) {
	dims, ds, ag, ac, rs, ua := detailEnv()
	rs.byStaff = map[string]domain.AssessmentTestResult{
		"张敏": {MainType: "3", WingType: "2", DistributionJSON: `{"1":8.2,"2":14.5,"3":32.1}`, Rationale: "脱敏判定"},
	}
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Enneagram == nil {
		t.Fatal("enneagram want 非 nil")
	}
	if res.Enneagram.MainType != "3" || res.Enneagram.WingType != "2" || res.Enneagram.Rationale != "脱敏判定" {
		t.Fatalf("enneagram 字段错误: %+v", res.Enneagram)
	}
	if res.Enneagram.Distribution["3"] != 32.1 || res.Enneagram.Distribution["1"] != 8.2 {
		t.Fatalf("distribution 透传失败: %+v", res.Enneagram.Distribution)
	}

	// 降级占位行（main_type 空串）视同无判型。
	rs.byStaff = map[string]domain.AssessmentTestResult{
		"张敏": {MainType: "", GradingStatus: "degraded", DistributionJSON: "{}"},
	}
	res, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.Enneagram != nil {
		t.Fatalf("降级占位行 want nil, got %+v", res.Enneagram)
	}
}

// TestProfileDetail_EmptyPerson 验证四表无行人返回空画像语义不报错（specs §5.2.4 规则1）。
func TestProfileDetail_EmptyPerson(t *testing.T) {
	dims, ds, ag, ac, rs, ua := detailEnv()
	svc := newProfileSvc(dims, ds, ag, ac, rs, ua)

	res, err := svc.Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(res.Periods) != 0 || res.SelectedPeriod != nil {
		t.Fatalf("空画像 want periods 空 + selected nil, got %+v", res)
	}
	if res.StaffName != "张敏" {
		t.Fatalf("StaffName 回填 want 张敏, got %s", res.StaffName)
	}
	if res.ActivityLevel != "unused" {
		t.Fatalf("空画像 activity_level want unused, got %s", res.ActivityLevel)
	}
	if res.Enneagram != nil {
		t.Fatal("空画像 enneagram want nil")
	}
	if len(res.Modules) != 2 {
		t.Fatalf("modules 恒两行, got %d", len(res.Modules))
	}
	for _, m := range res.Modules {
		if m.DataStatus != "pending" || m.Score != nil || m.ChangeVsPrev != nil || m.EvaluatedAt != nil {
			t.Fatalf("空画像模块行 want pending/nil 全空, got %+v", m)
		}
	}
	if len(res.Dimensions) != 2 {
		t.Fatalf("dimensions 按启用维度全 missing want 2, got %d", len(res.Dimensions))
	}
	for _, d := range res.Dimensions {
		if d.Status != "missing" || d.Score != nil || len(d.Evidences) != 0 || len(d.Trend) != 0 {
			t.Fatalf("空画像维度行 want missing, got %+v", d)
		}
	}

	// 空画像 + 传区间：periods 为空无匹配 → 2001（区间不在列表）。
	_, err = svc.Detail(context.Background(), "张敏", "2026-09-22", "2026-09-28")
	wantCode(t, err, errcode.ProfilePeriodInvalid)
}

// TestProfileDetail_NameFallback 验证姓名解析（03 §1.7）：上游查无精确匹配回退
// token_name 入参原值；上游调用失败整体 1305。
func TestProfileDetail_NameFallback(t *testing.T) {
	dims, ds, ag, ac, rs, ua := detailEnv()
	// 上游正常返回但无精确匹配行（离职员）。
	ua.staffs = []userapi.Staff{{StaffID: "9", StaffName: "王五"}}
	ua.total = 1
	res, err := newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if res.StaffName != "张敏" {
		t.Fatalf("查无匹配 StaffName 回退 want 张敏, got %s", res.StaffName)
	}
	if !ua.called || ua.lastKW != "张敏" {
		t.Fatalf("userapi 单人解析应透传 keyword, called=%v kw=%q", ua.called, ua.lastKW)
	}

	// 上游 err → 整体 1305。
	ua.err = errors.New("upstream timeout")
	_, err = newProfileSvc(dims, ds, ag, ac, rs, ua).Detail(context.Background(), "张敏", "", "")
	wantCode(t, err, errcode.StaffListUnavailable)
}
