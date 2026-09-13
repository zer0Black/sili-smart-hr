package pipeline_test

// tick_trigger_test.go TickTrigger 周期触发判定测试
// （specs §5.1.2/§5.1.4/§5.1.5，03 §4.3）。fake 复用 orchestrator_test.go 同包件。

import (
	"context"
	"errors"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/repository"
)

// fakeConfigRepo 配置仓储 fake：cfg/getErr 承载单例读取，members/memberErr
// 承载 specified 名单读取。
type fakeConfigRepo struct {
	cfg       *domain.AssessmentConfig
	getErr    error
	members   []domain.AssessmentConfigMember
	memberErr error
}

var _ repository.AssessmentConfigRepository = (*fakeConfigRepo)(nil)

func (f *fakeConfigRepo) Get(ctx context.Context) (*domain.AssessmentConfig, error) {
	return f.cfg, f.getErr
}
func (f *fakeConfigRepo) UpdateWithVersion(ctx context.Context, cfg *domain.AssessmentConfig) (int64, error) {
	return 0, nil
}
func (f *fakeConfigRepo) ListMembers(ctx context.Context, configID int64) ([]domain.AssessmentConfigMember, error) {
	return f.members, f.memberErr
}
func (f *fakeConfigRepo) ReplaceMembers(ctx context.Context, configID int64, members []domain.AssessmentConfigMember) error {
	return nil
}
func (f *fakeConfigRepo) UpdateWithMembers(ctx context.Context, cfg *domain.AssessmentConfig, members []domain.AssessmentConfigMember) (int64, error) {
	return 0, nil
}

// membersOf 构造指定人员关联行。
func membersOf(names ...string) []domain.AssessmentConfigMember {
	out := make([]domain.AssessmentConfigMember, 0, len(names))
	for _, n := range names {
		out = append(out, domain.AssessmentConfigMember{AssessmentConfigID: 1, StaffName: n})
	}
	return out
}

// tickFixture 组装 TickTrigger 链路编排器。
func tickFixture(repo *fakeBatchRepo, cfgRepo *fakeConfigRepo, fetcher pipeline.StaffFetcher, enq *fakeBatchEnqueuer) *pipeline.Orchestrator {
	return pipeline.NewOrchestrator(repo, &fakeAlertRepo{}, nil, cfgRepo, nil, fetcher,
		func(ctx context.Context) (string, error) { return "secret", nil },
		nil, alertWriter(&fakeAlertRepo{}), enq, nil)
}

func weeklyCfg() *domain.AssessmentConfig {
	return &domain.AssessmentConfig{ID: 1, Period: "weekly", TriggerTime: "23:00", TargetMode: domain.BatchTargetSpecified, Version: 1}
}

// tickNow 周日 23:00（本地时区锚定：TriggerHit 与窗口推算均按 time.Local）。
func tickNow() time.Time {
	return time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local)
}

// TestTickTriggerPeriodEnd 核心锚点：weekly 23:00 命中，CreateBatchRequest
// PeriodStart=2026-09-07 00:00、PeriodEnd=2026-09-13 00:00（含止日口径，
// specs §5.1.4 规则1 不重叠不遗漏），且 CreateBatch 后入队 batch-run。
func TestTickTriggerPeriodEnd(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	b := repo.created[0]
	wantStart := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local)
	if !b.PeriodStartAt.Equal(wantStart) {
		t.Errorf("PeriodStartAt = %v, want %v", b.PeriodStartAt, wantStart)
	}
	if !b.PeriodEndAt.Equal(wantEnd) {
		t.Errorf("PeriodEndAt = %v, want %v（窗口 end 前一日零点，含止日）", b.PeriodEndAt, wantEnd)
	}
	if b.TriggerType != domain.BatchTriggerScheduled {
		t.Errorf("TriggerType = %q, want scheduled", b.TriggerType)
	}
	if b.TargetMode != domain.BatchTargetSpecified {
		t.Errorf("TargetMode = %q, want specified", b.TargetMode)
	}
	if len(enq.enqIDs) != 1 || enq.enqIDs[0] != b.ID {
		t.Errorf("应入队 batch-run 一次，enqIDs = %v", enq.enqIDs)
	}
}

// TestTickTriggerNotHit 未命中触发时刻直接返回 nil，无任何副作用（03 §4.3 步骤3）。
func TestTickTriggerNotHit(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	// 周一 23:00：weekly 触发日为周日，不命中。
	miss := time.Date(2026, 9, 14, 23, 0, 0, 0, time.Local)
	if err := o.TickTrigger(context.Background(), miss); err != nil {
		t.Fatalf("未命中应返回 nil，得到 %v", err)
	}
	if len(repo.created) != 0 || len(enq.enqIDs) != 0 {
		t.Error("未命中不应建批或入队")
	}
}

// TestTickTriggerBlockedByRunning 同源阻塞（specs §5.1.4 规则2）：存在 running
// 且非停滞的定时批次时跳过本次触发返回 nil，不建批不入队。
func TestTickTriggerBlockedByRunning(t *testing.T) {
	repo := &fakeBatchRepo{runningScheduled: &domain.AssessmentBatch{
		ID: 9, BatchNo: "B202609062300001", TriggerType: domain.BatchTriggerScheduled,
		Status: domain.BatchStatusRunning, TriggeredAt: time.Date(2026, 9, 6, 23, 0, 0, 0, time.Local),
	}}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("同源阻塞应跳过返回 nil，得到 %v", err)
	}
	if len(repo.created) != 0 || len(enq.enqIDs) != 0 {
		t.Error("同源阻塞不应建批或入队")
	}
}

// TestTickTriggerStalledNotBlocking 停滞批次不阻塞（specs §5.1.4 规则2）：
// running 定时批次已过停滞边界（weekly +7 天）时照常建批。
func TestTickTriggerStalledNotBlocking(t *testing.T) {
	repo := &fakeBatchRepo{runningScheduled: &domain.AssessmentBatch{
		ID: 9, BatchNo: "B202608302300001", TriggerType: domain.BatchTriggerScheduled,
		Status: domain.BatchStatusRunning, TriggeredAt: time.Date(2026, 8, 30, 23, 0, 0, 0, time.Local),
	}}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("停滞批次不阻塞，应建批成功: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	if len(enq.enqIDs) != 1 {
		t.Errorf("应入队一次，enqIDs = %v", enq.enqIDs)
	}
}

// TestTickTriggerAllMode 全员模式（specs §5.1.2 步骤2）：经 StaffFetcher 拉全员名单快照。
func TestTickTriggerAllMode(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfg := weeklyCfg()
	cfg.TargetMode = domain.BatchTargetAll
	cfgRepo := &fakeConfigRepo{cfg: cfg}
	fetcher := &fakeStaffFetcher{pages: [][]userapi.Staff{staffs("甲", "乙")}}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, fetcher, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	b := repo.created[0]
	if b.TargetMode != domain.BatchTargetAll || b.TotalCount != 2 {
		t.Errorf("批次 = (mode %q, total %d), want (all, 2)", b.TargetMode, b.TotalCount)
	}
}

// TestTickTriggerConfigReadFail 配置读取失败上抛交任务级重试（specs §5.1.5）。
func TestTickTriggerConfigReadFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{getErr: errFake}
	o := tickFixture(repo, cfgRepo, nil, &fakeBatchEnqueuer{})

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("配置读取失败应上抛")
	}
	if len(repo.created) != 0 {
		t.Error("配置读取失败不应建批")
	}
}

// TestTickTriggerMembersReadFail 指定名单读取失败上抛（specs §5.1.5 名单拉取失败）。
func TestTickTriggerMembersReadFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), memberErr: errFake}
	o := tickFixture(repo, cfgRepo, nil, &fakeBatchEnqueuer{})

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("名单读取失败应上抛")
	}
	if len(repo.created) != 0 {
		t.Error("名单读取失败不应建批")
	}
}

// TestTickTriggerStaffFetchFail 全员名单拉取失败上抛（specs §5.1.5）。
func TestTickTriggerStaffFetchFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfg := weeklyCfg()
	cfg.TargetMode = domain.BatchTargetAll
	cfgRepo := &fakeConfigRepo{cfg: cfg}
	fetcher := &fakeStaffFetcher{err: errFake}
	o := tickFixture(repo, cfgRepo, fetcher, &fakeBatchEnqueuer{})

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("全员名单拉取失败应上抛")
	} else if !errors.Is(err, pipeline.ErrStaffFetchFailed) {
		t.Errorf("err = %v, want errors.Is 命中 ErrStaffFetchFailed 哨兵", err)
	}
	if len(repo.created) != 0 {
		t.Error("名单拉取失败不应建批")
	}
}

// TestTickTriggerCreateFail 建批落库失败上抛（specs §5.1.5）。
func TestTickTriggerCreateFail(t *testing.T) {
	repo := &fakeBatchRepo{createErrs: []error{errFake}}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	o := tickFixture(repo, cfgRepo, nil, &fakeBatchEnqueuer{})

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("建批落库失败应上抛")
	}
}

// TestTickTriggerEnqueueFail 投递失败上抛（specs §5.1.5 批次创建请求投递失败）。
func TestTickTriggerEnqueueFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{err: errFake}
	o := tickFixture(repo, cfgRepo, nil, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("入队失败应上抛交任务级重试")
	}
	if len(repo.created) != 1 {
		t.Errorf("入队失败时批次已落库（重试经同源阻塞拦截），实际 %d 条", len(repo.created))
	}
}
