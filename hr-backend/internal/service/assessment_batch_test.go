// Package service_test 对 assessment_batch 批次查询业务层做黑盒单元测试。
//
// fakeBatchRepo / fakeConfigRepo / fakeDimRepo / fakeSecretRepo / fakeUserapiClient 驱动，
// 不依赖真实 DB / 外部 HTTP。覆盖：
//   - List：停滞派生、进度向下取整与除零兜底、名单摘要（前 2 人）与全量快照、
//     all 模式空 brief、枚举校验 1400
//   - Stats：本期跑批间隔窗口口径（有/无历史定时批次）、人次计数批次圈定、进行中剔除停滞
//   - Plan：specified 名单摘要与全量、all 模式上游不可达降级 target_count=0、维度分组计数
package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeBatchRepo 是 repository.AssessmentBatchRepository 的测试假实现，字段挂返回与探针。
type fakeBatchRepo struct {
	list            []domain.AssessmentBatch
	listTotal       int64
	listErr         error
	latestScheduled *domain.AssessmentBatch
	latestSchedErr  error
	byID            *domain.AssessmentBatch
	byIDErr         error
	failedList      []domain.AssessmentBatchPerson
	failedListErr   error
	countInRange    int64
	countInRangeErr error
	idsBetween      []int64
	idsBetweenErr   error
	countSuccess    int64
	countSuccessErr error
	countRunning    int64
	countRunningErr error

	lastFilter            repository.BatchFilter
	failedListGotID       int64
	countInRangeArgs      [2]time.Time
	idsBetweenArgs        [2]time.Time
	countSuccessGotIDs    []int64
	countRunningThreshold time.Time
}

func (f *fakeBatchRepo) Create(_ context.Context, _ *domain.AssessmentBatch) error { return nil }
func (f *fakeBatchRepo) CreatePersons(_ context.Context, _ []domain.AssessmentBatchPerson) error {
	return nil
}
func (f *fakeBatchRepo) GetByID(_ context.Context, _ int64) (*domain.AssessmentBatch, error) {
	return f.byID, f.byIDErr
}
func (f *fakeBatchRepo) ListFailedByBatch(_ context.Context, batchID int64) ([]domain.AssessmentBatchPerson, error) {
	f.failedListGotID = batchID
	return f.failedList, f.failedListErr
}
func (f *fakeBatchRepo) FindLatestRunningScheduled(_ context.Context) (*domain.AssessmentBatch, error) {
	return nil, nil
}
func (f *fakeBatchRepo) ListByFilter(_ context.Context, bf repository.BatchFilter) ([]domain.AssessmentBatch, int64, error) {
	f.lastFilter = bf
	return f.list, f.listTotal, f.listErr
}
func (f *fakeBatchRepo) UpdateTotalSessions(_ context.Context, _ int64, _ int, _ map[string]int) error {
	return nil
}
func (f *fakeBatchRepo) AdvancePersonTerminal(_ context.Context, _ int64, _, _, _ string, _ int) error {
	return nil
}
func (f *fakeBatchRepo) FinalizeBatch(_ context.Context, _ int64, _ string, _ float64) error {
	return nil
}
func (f *fakeBatchRepo) FailWholeBatch(_ context.Context, _ int64, _ string) error { return nil }
func (f *fakeBatchRepo) CountInRange(_ context.Context, start, end time.Time) (int64, error) {
	f.countInRangeArgs = [2]time.Time{start, end}
	return f.countInRange, f.countInRangeErr
}
func (f *fakeBatchRepo) CountRunningNonStalled(_ context.Context, stalledBefore time.Time) (int64, error) {
	f.countRunningThreshold = stalledBefore
	return f.countRunning, f.countRunningErr
}
func (f *fakeBatchRepo) CountSuccessSideInRanges(_ context.Context, ids []int64) (int64, error) {
	f.countSuccessGotIDs = ids
	return f.countSuccess, f.countSuccessErr
}
func (f *fakeBatchRepo) ListBatchIDsTriggeredBetween(_ context.Context, start, end time.Time) ([]int64, error) {
	f.idsBetweenArgs = [2]time.Time{start, end}
	return f.idsBetween, f.idsBetweenErr
}
func (f *fakeBatchRepo) FindLatestScheduled(_ context.Context) (*domain.AssessmentBatch, error) {
	return f.latestScheduled, f.latestSchedErr
}

var _ repository.AssessmentBatchRepository = (*fakeBatchRepo)(nil)

// fakeDimRepo 是 repository.DimensionRepository 的测试假实现，仅 ListEnabledFullByDataSource 有行为。
type fakeDimRepo struct {
	dims []domain.Dimension
	err  error

	gotDataSource string
}

func (f *fakeDimRepo) ListAll(_ context.Context) ([]domain.Dimension, error) { return nil, nil }
func (f *fakeDimRepo) FindByID(_ context.Context, _ int64) (*domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDimRepo) FindByCodeExcludingDeleted(_ context.Context, _ string) (*domain.Dimension, error) {
	return nil, nil
}
func (f *fakeDimRepo) Create(_ context.Context, _ *domain.Dimension) error { return nil }
func (f *fakeDimRepo) UpdateWithVersion(_ context.Context, _ int64, _ int, _ map[string]any) (int64, error) {
	return 0, nil
}
func (f *fakeDimRepo) SoftDeleteWithVersion(_ context.Context, _ int64, _ int) (int64, error) {
	return 0, nil
}
func (f *fakeDimRepo) GetActivitySetting(_ context.Context) (*domain.DimensionSetting, error) {
	return nil, nil
}
func (f *fakeDimRepo) UpdateActivitySetting(_ context.Context, _, _ int) error { return nil }
func (f *fakeDimRepo) ListEnabledFullByDataSource(_ context.Context, dataSource string) ([]domain.Dimension, error) {
	f.gotDataSource = dataSource
	return f.dims, f.err
}

var _ repository.DimensionRepository = (*fakeDimRepo)(nil)

// fakeSecretRepo 返回空密文，驱动 batch 服务 Plan 的全员计数降级路径（resolveSecret 失败 → 0）。
type fakeBatchSecretRepo struct {
	get *domain.IntegrationSecret
	err error
}

func (f *fakeBatchSecretRepo) Get(_ context.Context) (*domain.IntegrationSecret, error) {
	return f.get, f.err
}
func (f *fakeBatchSecretRepo) Update(_ context.Context, _ *domain.IntegrationSecret) (int64, error) {
	return 0, nil
}

var _ repository.IntegrationSecretRepository = (*fakeBatchSecretRepo)(nil)

var batchEncKey = crypto.DeriveKey("test-assessment-batch")

// encryptedSecret 用测试 key 加密明文，供 Plan 全员计数成功路径注入。
func encryptedSecret(t *testing.T, plain string) string {
	t.Helper()
	c, err := crypto.Encrypt(batchEncKey, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return c
}

// batchFixedNow 固定本地时区时刻，窗口与停滞断言以此为锚。
func batchFixedNow() time.Time { return time.Date(2026, 9, 12, 10, 0, 0, 0, time.Local) }

// newBatchSvc 组装 batch service 三查询侧实现；configRepo 与 dims 由调用方按场景注入。
func newBatchSvc(batchRepo *fakeBatchRepo, cfgRepo repository.AssessmentConfigRepository, dimRepo *fakeDimRepo, ua *fakeUserapiClient, secretRepo repository.IntegrationSecretRepository) service.AssessmentBatchService {
	return service.NewAssessmentBatchService(batchRepo, cfgRepo, dimRepo, ua, secretRepo, batchEncKey, batchFixedNow)
}

// cfgWeekly 返回 weekly/23:00/specified 配置 fake（List/Stats/Plan 共享）。
func cfgWeekly(members []domain.AssessmentConfigMember) repository.AssessmentConfigRepository {
	return &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 1, Period: "weekly", TriggerTime: "23:00", TargetMode: "specified", Version: 3,
		},
		members: members,
	}
}

// TestListDerivesStalled：running 批次 triggered_at=now-8 天（超 weekly 停滞边界 +7 天）派生 stalled=true；
// triggered_at=now-3 天派生 stalled=false。
func TestListDerivesStalled(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 1, BatchNo: "B1", TriggerType: "scheduled", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "running", TriggeredAt: batchFixedNow().AddDate(0, 0, -8), PeriodStartAt: batchFixedNow().AddDate(0, 0, -14), PeriodEndAt: batchFixedNow().AddDate(0, 0, -8)},
			{ID: 2, BatchNo: "B2", TriggerType: "manual", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "running", TriggeredAt: batchFixedNow().AddDate(0, 0, -3), PeriodStartAt: batchFixedNow().AddDate(0, 0, -3), PeriodEndAt: batchFixedNow().AddDate(0, 0, -3)},
		},
		listTotal: 2,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, total, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", total, len(list))
	}
	if !list[0].Stalled {
		t.Errorf("list[0].Stalled = false, want true（triggered_at=now-8d 超 weekly 边界）")
	}
	if list[1].Stalled {
		t.Errorf("list[1].Stalled = true, want false（triggered_at=now-3d 未超边界）")
	}
}

// TestListDerivesStalled_TerminalNeverStalled：终态批次即便超过停滞边界也不派生停滞标识。
func TestListDerivesStalled_TerminalNeverStalled(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 3, BatchNo: "B3", TriggerType: "scheduled", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "success", TriggeredAt: batchFixedNow().AddDate(0, 0, -30), PeriodStartAt: batchFixedNow().AddDate(0, 0, -30), PeriodEndAt: batchFixedNow().AddDate(0, 0, -24)},
		},
		listTotal: 1,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list[0].Stalled {
		t.Errorf("终态批次 Stalled = true, want false")
	}
}

// TestListProgressPercent：evaluated=42/total=128 向下取整 32；total=0 时 0（后端除零兜底，03 A1）。
func TestListProgressPercent(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 1, BatchNo: "B1", TriggerType: "scheduled", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "running", TriggeredAt: batchFixedNow(), PeriodStartAt: batchFixedNow(), PeriodEndAt: batchFixedNow(),
				EvaluatedCount: 42, TotalCount: 128},
			{ID: 2, BatchNo: "B2", TriggerType: "manual", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "running", TriggeredAt: batchFixedNow(), PeriodStartAt: batchFixedNow(), PeriodEndAt: batchFixedNow(),
				EvaluatedCount: 0, TotalCount: 0},
		},
		listTotal: 2,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list[0].ProgressPercent != 32 {
		t.Errorf("ProgressPercent = %d, want 32（42/128 向下取整）", list[0].ProgressPercent)
	}
	if list[1].ProgressPercent != 0 {
		t.Errorf("total=0 时 ProgressPercent = %d, want 0", list[1].ProgressPercent)
	}
}

// TestListTargetBrief：specified 名单 [张敏,李芳,王强] brief 取前 2 人、names 返全量快照；
// all 模式 brief 为空数组、names 同返快照（03 A1 v1.5）。
func TestListTargetBrief(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 1, BatchNo: "B1", TriggerType: "manual", TargetMode: "specified",
				TargetNamesJSON: `["张敏","李芳","王强"]`, TotalCount: 3,
				Status: "success", TriggeredAt: batchFixedNow(), PeriodStartAt: batchFixedNow(), PeriodEndAt: batchFixedNow()},
			{ID: 2, BatchNo: "B2", TriggerType: "scheduled", TargetMode: "all",
				TargetNamesJSON: `["张敏","李芳","王强","赵磊"]`, TotalCount: 4,
				Status: "success", TriggeredAt: batchFixedNow(), PeriodStartAt: batchFixedNow(), PeriodEndAt: batchFixedNow()},
		},
		listTotal: 2,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := list[0].TargetBrief; len(got) != 2 || got[0] != "张敏" || got[1] != "李芳" {
		t.Errorf("specified TargetBrief = %v, want [张敏 李芳]", got)
	}
	if got := list[0].TargetNames; len(got) != 3 {
		t.Errorf("specified TargetNames = %v, want 全量 3 人", got)
	}
	if got := list[1].TargetBrief; len(got) != 0 {
		t.Errorf("all 模式 TargetBrief = %v, want 空数组", got)
	}
	if got := list[1].TargetNames; len(got) != 4 {
		t.Errorf("all 模式 TargetNames = %v, want 快照 4 人", got)
	}
}

// TestListInvalidFilter：trigger_type/status 非枚举值返 *service.Error code==1400。
func TestListInvalidFilter(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	for _, f := range []service.BatchListFilter{
		{TriggerType: "foo", Page: 1, PageSize: 10},
		{Status: "foo", Page: 1, PageSize: 10},
	} {
		_, _, err := svc.List(context.Background(), f)
		serr, ok := err.(*service.Error)
		if !ok {
			t.Fatalf("filter %+v err 类型 %T, want *service.Error", f, err)
		}
		if serr.Code != errcode.BadRequest {
			t.Errorf("filter %+v code = %d, want %d", f, serr.Code, errcode.BadRequest)
		}
	}
}

// TestStatsWindow：最近定时批次 triggered=09-06 23:00、配置 weekly 23:00、now=09-12 10:00，
// 断言 CountInRange 窗口为 [09-06 23:00, 09-13 23:00)（specs §4.1.2A 本期跑批间隔）。
func TestStatsWindow(t *testing.T) {
	wantStart := time.Date(2026, 9, 6, 23, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local)
	batchRepo := &fakeBatchRepo{
		latestScheduled: &domain.AssessmentBatch{ID: 9, TriggerType: "scheduled", TriggeredAt: wantStart},
		countInRange:    2,
		idsBetween:      []int64{9, 10},
		countSuccess:    96,
		countRunning:    1,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if !batchRepo.countInRangeArgs[0].Equal(wantStart) || !batchRepo.countInRangeArgs[1].Equal(wantEnd) {
		t.Errorf("CountInRange 窗口 = [%v, %v), want [%v, %v)",
			batchRepo.countInRangeArgs[0], batchRepo.countInRangeArgs[1], wantStart, wantEnd)
	}
	if !batchRepo.idsBetweenArgs[0].Equal(wantStart) || !batchRepo.idsBetweenArgs[1].Equal(wantEnd) {
		t.Errorf("ListBatchIDsTriggeredBetween 窗口 = [%v, %v), want [%v, %v)",
			batchRepo.idsBetweenArgs[0], batchRepo.idsBetweenArgs[1], wantStart, wantEnd)
	}
	if len(batchRepo.countSuccessGotIDs) != 2 || batchRepo.countSuccessGotIDs[0] != 9 {
		t.Errorf("CountSuccessSideInRanges ids = %v, want [9 10]", batchRepo.countSuccessGotIDs)
	}
	// weekly 停滞边界 7 天：now-7d = 09-05 10:00。
	wantThreshold := batchFixedNow().AddDate(0, 0, -7)
	if !batchRepo.countRunningThreshold.Equal(wantThreshold) {
		t.Errorf("CountRunningNonStalled 边界 = %v, want %v", batchRepo.countRunningThreshold, wantThreshold)
	}
	if dto.EvalCount != 2 || dto.EvaluatedPersonCount != 96 || dto.RunningBatchCount != 1 {
		t.Errorf("dto = %+v, want {2 96 1}", dto)
	}
}

// TestStatsWindow_NoHistoryScheduled：无历史定时批次时下界按 NextTriggerAt(now) 回推一个周期长度。
func TestStatsWindow_NoHistoryScheduled(t *testing.T) {
	batchRepo := &fakeBatchRepo{countInRange: 1, countRunning: 0}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	if _, err := svc.Stats(context.Background()); err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// NextTriggerAt(09-12 10:00, weekly, 23:00)=09-13 23:00，回推 7 天得 09-06 23:00。
	wantStart := time.Date(2026, 9, 6, 23, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local)
	if !batchRepo.countInRangeArgs[0].Equal(wantStart) || !batchRepo.countInRangeArgs[1].Equal(wantEnd) {
		t.Errorf("窗口 = [%v, %v), want [%v, %v)",
			batchRepo.countInRangeArgs[0], batchRepo.countInRangeArgs[1], wantStart, wantEnd)
	}
}

// TestPlanSpecified：specified 模式 brief 前 2 人名、names 全量、target_count=名单条数、
// 维度分组计数（BASE/UPPER 非 nil 计数，nil 不计）。
func TestPlanSpecified(t *testing.T) {
	members := []domain.AssessmentConfigMember{
		{StaffID: "u1", StaffName: "张敏"},
		{StaffID: "u2", StaffName: "李芳"},
		{StaffID: "u3", StaffName: "王强"},
	}
	base, upper := domain.GroupBase, domain.GroupUpper
	dimRepo := &fakeDimRepo{dims: []domain.Dimension{
		{Code: "A", GroupCode: &base, DataSource: domain.SourceConversation},
		{Code: "B", GroupCode: &base, DataSource: domain.SourceConversation},
		{Code: "C", GroupCode: &upper, DataSource: domain.SourceConversation},
		{Code: "D", GroupCode: nil, DataSource: domain.SourceConversation},
	}}
	svc := newBatchSvc(&fakeBatchRepo{}, cfgWeekly(members), dimRepo, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if dto.NextTriggerAt != "2026-09-13 23:00" {
		t.Errorf("NextTriggerAt = %q, want 2026-09-13 23:00", dto.NextTriggerAt)
	}
	if dto.Period != "weekly" || dto.TargetMode != "specified" {
		t.Errorf("Period/TargetMode = %q/%q", dto.Period, dto.TargetMode)
	}
	if got := dto.TargetBrief; len(got) != 2 || got[0] != "张敏" || got[1] != "李芳" {
		t.Errorf("TargetBrief = %v, want [张敏 李芳]", got)
	}
	if len(dto.TargetNames) != 3 || dto.TargetCount != 3 {
		t.Errorf("TargetNames/TargetCount = %v/%d, want 全量 3 人", dto.TargetNames, dto.TargetCount)
	}
	if dto.DimensionBaseCount != 2 || dto.DimensionUpperCount != 1 {
		t.Errorf("维度计数 = %d/%d, want 2/1（nil GroupCode 不计）", dto.DimensionBaseCount, dto.DimensionUpperCount)
	}
	if dimRepo.gotDataSource != domain.SourceConversation {
		t.Errorf("ListEnabledFullByDataSource 入参 = %q, want CONVERSATION", dimRepo.gotDataSource)
	}
}

// TestPlanAllDegraded：all 模式 fake staffs 返错，断言 TargetCount==0 且不返回 error；
// brief 与 names 均为空数组（03 A3）。
func TestPlanAllDegraded(t *testing.T) {
	cfgRepo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 1, Period: "weekly", TriggerTime: "23:00", TargetMode: "all", Version: 1,
		},
	}
	svc := newBatchSvc(&fakeBatchRepo{}, cfgRepo, &fakeDimRepo{}, &fakeUserapiClient{err: errors.New("upstream down")}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan 不应返回 error: %v", err)
	}
	if dto.TargetCount != 0 {
		t.Errorf("TargetCount = %d, want 0（上游不可达降级）", dto.TargetCount)
	}
	if len(dto.TargetBrief) != 0 || len(dto.TargetNames) != 0 {
		t.Errorf("all 模式 brief/names = %v/%v, want 双空数组", dto.TargetBrief, dto.TargetNames)
	}
	if dto.TargetBrief == nil || dto.TargetNames == nil {
		t.Errorf("all 模式 brief/names 应为空数组而非 nil")
	}
}

// TestPlanAllCountOK：all 模式上游可达时 TargetCount 取 ListStaffs total（03 A3）。
func TestPlanAllCountOK(t *testing.T) {
	cfgRepo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 1, Period: "weekly", TriggerTime: "23:00", TargetMode: "all", Version: 1,
		},
	}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{{StaffID: "u1", StaffName: "张敏"}}, total: 12}
	secretRepo := &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1, SecretCipher: encryptedSecret(t, "sec")}}
	svc := newBatchSvc(&fakeBatchRepo{}, cfgRepo, &fakeDimRepo{}, ua, secretRepo)

	dto, err := svc.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if dto.TargetCount != 12 {
		t.Errorf("TargetCount = %d, want 12", dto.TargetCount)
	}
	if !ua.called || ua.lastSecret != "sec" || ua.lastKW != "" || ua.lastPage != 1 || ua.lastPSize != 1 {
		t.Errorf("ListStaffs 调用参数不符合约定: called=%v secret=%q kw=%q page=%d size=%d",
			ua.called, ua.lastSecret, ua.lastKW, ua.lastPage, ua.lastPSize)
	}
}

// TestListRepoError：仓储错误包装上抛（非业务错误）。
func TestListRepoError(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{listErr: errors.New("db down")}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})
	_, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	var serr *service.Error
	if err == nil || errors.As(err, &serr) {
		t.Errorf("err = %v, want 非 service.Error 的包装错误", err)
	}
}

// TestListDateFormats：评估时段与触发时间按本地时区格式化 yyyy-MM-dd / yyyy-MM-dd HH:mm。
func TestListDateFormats(t *testing.T) {
	ps := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	pe := time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local)
	tr := time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local)
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 1, BatchNo: "B1", TriggerType: "scheduled", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "success", TriggeredAt: tr, PeriodStartAt: ps, PeriodEndAt: pe,
				EvaluatedCount: 3, TotalCount: 4, CoveredSessionCount: 517, FailedCount: 1},
		},
		listTotal: 1,
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0]
	if got.PeriodStart != "2026-09-07" || got.PeriodEnd != "2026-09-13" || got.TriggeredAt != "2026-09-13 23:00" {
		t.Errorf("日期格式 = %q/%q/%q", got.PeriodStart, got.PeriodEnd, got.TriggeredAt)
	}
	if got.ID != 1 || got.BatchNo != "B1" || got.TriggerType != "scheduled" || got.TargetMode != "all" ||
		got.Status != "success" || got.EvaluatedCount != 3 || got.TotalCount != 4 ||
		got.CoveredSessionCount != 517 || got.FailedCount != 1 {
		t.Errorf("字段透传不符: %+v", got)
	}
}

// TestListConfigErrorStalledDegrade：配置读取失败时按非停滞处理（WARN 降级），不阻断列表。
func TestListConfigErrorStalledDegrade(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		list: []domain.AssessmentBatch{
			{ID: 1, BatchNo: "B1", TriggerType: "scheduled", TargetMode: "all", TargetNamesJSON: "[]",
				Status: "running", TriggeredAt: batchFixedNow().AddDate(0, 0, -100), PeriodStartAt: batchFixedNow(), PeriodEndAt: batchFixedNow()},
		},
		listTotal: 1,
	}
	svc := newBatchSvc(batchRepo, &fakeAssessmentConfigRepo{cfgErr: fmt.Errorf("db down")}, &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	list, _, err := svc.List(context.Background(), service.BatchListFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list[0].Stalled {
		t.Errorf("配置读取失败应降级为非停滞，Stalled = true")
	}
}

// TestStatsErrors：仓储计数错误包装上抛。
func TestStatsErrors(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{countInRangeErr: errors.New("db down")}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})
	if _, err := svc.Stats(context.Background()); err == nil {
		t.Error("CountInRange 错误未上抛")
	}
}

// TestPlanConfigError：配置读取失败包装上抛。
func TestPlanConfigError(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{}, &fakeAssessmentConfigRepo{cfgErr: fmt.Errorf("db down")}, &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})
	if _, err := svc.Plan(context.Background()); err == nil {
		t.Error("配置读取错误未上抛")
	}
}

// targetsBatchFixture 构造带名单快照与评估时段的批次行，供 Targets/Failures 用例共享。
func targetsBatchFixture() *domain.AssessmentBatch {
	return &domain.AssessmentBatch{
		ID:              7,
		BatchNo:         "B202609072300001",
		TriggerType:     domain.BatchTriggerScheduled,
		TargetMode:      domain.BatchTargetSpecified,
		TargetNamesJSON: `["张敏","李芳","王强"]`,
		TotalCount:      3,
		FailedCount:     3,
		Status:          domain.BatchStatusFailed,
		PeriodStartAt:   time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		PeriodEndAt:     time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local),
		TriggeredAt:     time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local),
	}
}

// TestTargetsFullList：名单 [张敏,李芳,王强] 全量返回且 Total=3，PeriodStart/End 为 yyyy-MM-dd（03 A4）。
func TestTargetsFullList(t *testing.T) {
	batchRepo := &fakeBatchRepo{byID: targetsBatchFixture()}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Targets(context.Background(), 7)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if dto.BatchID != 7 || dto.TargetMode != domain.BatchTargetSpecified {
		t.Errorf("BatchID/TargetMode = %d/%q", dto.BatchID, dto.TargetMode)
	}
	if len(dto.Names) != 3 || dto.Names[0] != "张敏" || dto.Names[1] != "李芳" || dto.Names[2] != "王强" {
		t.Errorf("Names = %v, want [张敏 李芳 王强] 全量", dto.Names)
	}
	if dto.Total != 3 {
		t.Errorf("Total = %d, want 3", dto.Total)
	}
	if dto.PeriodStart != "2026-09-07" || dto.PeriodEnd != "2026-09-13" {
		t.Errorf("Period = %q/%q, want 2026-09-07/2026-09-13", dto.PeriodStart, dto.PeriodEnd)
	}
}

// TestTargetsNotFound：GetByID 返 nil 时 Targets 返 *service.Error code==1601。
func TestTargetsNotFound(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	_, err := svc.Targets(context.Background(), 999)
	serr, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("err 类型 %T, want *service.Error", err)
	}
	if serr.Code != errcode.BatchNotFound {
		t.Errorf("code = %d, want %d", serr.Code, errcode.BatchNotFound)
	}
}

// TestFailuresOrderAndCount：失败清单按仓储返回序（finished_at 升序）透传，
// FailedCount 取批次行值，TotalCount/评估时段透传（specs §4.3.4 规则1）。
func TestFailuresOrderAndCount(t *testing.T) {
	batchRepo := &fakeBatchRepo{
		byID: targetsBatchFixture(),
		failedList: []domain.AssessmentBatchPerson{
			{TokenName: "张敏", ErrorSummary: "评估执行失败：LLM 上游不可用（重试 3 次耗尽）"},
			{TokenName: "李芳", ErrorSummary: "评估执行失败：LLM 上游不可用（重试 3 次耗尽）"},
			{TokenName: "王强", ErrorSummary: "评估执行失败：LLM 上游不可用（重试 3 次耗尽）"},
		},
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Failures(context.Background(), 7)
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	if batchRepo.failedListGotID != 7 {
		t.Errorf("ListFailedByBatch batchID = %d, want 7", batchRepo.failedListGotID)
	}
	if dto.FailedCount != 3 {
		t.Errorf("FailedCount = %d, want 3（取批次行值）", dto.FailedCount)
	}
	if dto.TotalCount != 3 || dto.BatchNo != "B202609072300001" {
		t.Errorf("TotalCount/BatchNo = %d/%q", dto.TotalCount, dto.BatchNo)
	}
	if dto.PeriodStart != "2026-09-07" || dto.PeriodEnd != "2026-09-13" {
		t.Errorf("Period = %q/%q", dto.PeriodStart, dto.PeriodEnd)
	}
	wantOrder := []string{"张敏", "李芳", "王强"}
	if len(dto.List) != 3 {
		t.Fatalf("List 条数 = %d, want 3", len(dto.List))
	}
	for i, item := range dto.List {
		if item.TokenName != wantOrder[i] {
			t.Errorf("List[%d].TokenName = %q, want %q（按仓储序透传）", i, item.TokenName, wantOrder[i])
		}
		if item.ErrorSummary == "" {
			t.Errorf("List[%d].ErrorSummary 为空", i)
		}
	}
}

// TestFailuresNotFound：GetByID 返 nil 时 Failures 返 *service.Error code==1601。
func TestFailuresNotFound(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	_, err := svc.Failures(context.Background(), 999)
	serr, ok := err.(*service.Error)
	if !ok {
		t.Fatalf("err 类型 %T, want *service.Error", err)
	}
	if serr.Code != errcode.BatchNotFound {
		t.Errorf("code = %d, want %d", serr.Code, errcode.BatchNotFound)
	}
}

// TestFailuresBatchLevelReason：批次级异常时每行 ErrorSummary 取人员行自身值
//（FailWholeBatch 已把批次级原因写入每行，service 不重复覆盖批次行值）。
func TestFailuresBatchLevelReason(t *testing.T) {
	b := targetsBatchFixture()
	b.ErrorSummary = "上游会话列表不可用"
	batchRepo := &fakeBatchRepo{
		byID: b,
		failedList: []domain.AssessmentBatchPerson{
			{TokenName: "张敏", ErrorSummary: "上游会话列表不可用"},
			{TokenName: "李芳", ErrorSummary: "上游会话列表不可用"},
			{TokenName: "王强", ErrorSummary: "上游会话列表不可用"},
		},
	}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})

	dto, err := svc.Failures(context.Background(), 7)
	if err != nil {
		t.Fatalf("Failures: %v", err)
	}
	for i, item := range dto.List {
		if item.ErrorSummary != "上游会话列表不可用" {
			t.Errorf("List[%d].ErrorSummary = %q, want 人员行自身值", i, item.ErrorSummary)
		}
	}
}

// TestTargetsRepoError：GetByID 仓储错误包装上抛（非业务错误）。
func TestTargetsRepoError(t *testing.T) {
	svc := newBatchSvc(&fakeBatchRepo{byIDErr: errors.New("db down")}, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})
	_, err := svc.Targets(context.Background(), 7)
	var serr *service.Error
	if err == nil || errors.As(err, &serr) {
		t.Errorf("err = %v, want 非 service.Error 的包装错误", err)
	}
}

// TestFailuresRepoError：失败清单仓储错误包装上抛（非业务错误）。
func TestFailuresRepoError(t *testing.T) {
	batchRepo := &fakeBatchRepo{byID: targetsBatchFixture(), failedListErr: errors.New("db down")}
	svc := newBatchSvc(batchRepo, cfgWeekly(nil), &fakeDimRepo{}, &fakeUserapiClient{}, &fakeBatchSecretRepo{get: &domain.IntegrationSecret{ID: 1}})
	_, err := svc.Failures(context.Background(), 7)
	var serr *service.Error
	if err == nil || errors.As(err, &serr) {
		t.Errorf("err = %v, want 非 service.Error 的包装错误", err)
	}
}