package pipeline_test

// tick_trigger_test.go TickTrigger 周期触发判定测试
// （specs §5.1.2/§5.1.4/§5.1.5，03 §4.3）。fake 复用 orchestrator_test.go 同包件。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/pipeline"
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
	return tickFixtureWithAlerts(repo, cfgRepo, fetcher, enq, &fakeAlertRepo{})
}

// tickFixtureWithAlerts 同 tickFixture，另注入可断言的告警仓储。
func tickFixtureWithAlerts(repo *fakeBatchRepo, cfgRepo *fakeConfigRepo, fetcher pipeline.StaffFetcher, enq *fakeBatchEnqueuer, alertRepo *fakeAlertRepo) *pipeline.Orchestrator {
	return tickFixtureWithAlertsAndRecorder(repo, cfgRepo, fetcher, enq, alertRepo, nil)
}

// tickFixtureWithAlertsAndRecorder 同上，另注入节点投递 fake（specs P4_LOG_001 §5.2）。
func tickFixtureWithAlertsAndRecorder(repo *fakeBatchRepo, cfgRepo *fakeConfigRepo, fetcher pipeline.StaffFetcher, enq *fakeBatchEnqueuer, alertRepo *fakeAlertRepo, rec *nodeRecorder) *pipeline.Orchestrator {
	return pipeline.NewOrchestrator(repo, nil, cfgRepo, nil, fetcher,
		func(ctx context.Context) (string, error) { return "secret", nil },
		nil, alertWriter(alertRepo), enq, nil, rec)
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

// TestTickTriggerAllMode 全员模式（specs §5.1.2 步骤2）：建批只落骨架（名单留空
// 由 RunBatch 异步展开），tick 同步路径不触上游翻页（60s 任务预算防击穿）。
func TestTickTriggerAllMode(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfg := weeklyCfg()
	cfg.TargetMode = domain.BatchTargetAll
	cfgRepo := &fakeConfigRepo{cfg: cfg}
	// fetcher 传 nil：tick 触上游会 panic，以此断言不翻页。
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	b := repo.created[0]
	if b.TargetMode != domain.BatchTargetAll || b.TotalCount != 0 || b.TargetNamesJSON != "[]" {
		t.Errorf("骨架批次 = (mode %q, total %d, json %q), want (all, 0, \"[]\")", b.TargetMode, b.TotalCount, b.TargetNamesJSON)
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

// TestTickTriggerStaffFetchFail 密钥探测失败（all 骨架建批前置检查）上抛哨兵
//（specs §5.1.5 名单链路失败）。
func TestTickTriggerStaffFetchFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfg := weeklyCfg()
	cfg.TargetMode = domain.BatchTargetAll
	cfgRepo := &fakeConfigRepo{cfg: cfg}
	o := pipeline.NewOrchestrator(repo, nil, cfgRepo, nil, nil,
		func(ctx context.Context) (string, error) { return "", errFake },
		nil, nil, &fakeBatchEnqueuer{}, nil, nil)

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("密钥解析失败应上抛")
	} else if !errors.Is(err, pipeline.ErrSecretResolveFailed) {
		t.Errorf("err = %v, want errors.Is 命中 ErrSecretResolveFailed 哨兵", err)
	}
	if len(repo.created) != 0 {
		t.Error("密钥探测失败不应建批")
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

// TestTickTriggerEnqueueFail 投递失败回滚本次建批后上抛交任务级重试
//（specs §5.1.5：重试耗尽跳过至下个周期；回滚是重试可达的前提，否则已建批
// 判定拦截重试、批次滞留 running 成孤儿）。不落 failed 终态、不写告警。
func TestTickTriggerEnqueueFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{err: errFake}
	alertRepo := &fakeAlertRepo{}
	o := tickFixtureWithAlerts(repo, cfgRepo, nil, enq, alertRepo)

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("入队失败应上抛交任务级重试，得到 nil")
	}
	if len(repo.created) != 0 {
		t.Fatalf("入队失败应回滚建批（不残留批次行），实际 %d 条", len(repo.created))
	}
	if !repo.deleted {
		t.Error("入队失败应调 DeleteBatch 回滚本次建批")
	}
	if repo.failCalled {
		t.Error("入队失败不应落 failed 终态（交任务级重试重建）")
	}
	if len(alertRepo.alerts) != 0 {
		t.Errorf("回滚路径不应写告警，实际 %d 条", len(alertRepo.alerts))
	}
}

// TestTickTriggerEnqueueFailRollbackFail 入队且回滚也失败（DB 抖动）：
// 复合错误上抛交任务级重试（重放时 DeleteBatch 幂等，批次已删则守卫无命中）。
func TestTickTriggerEnqueueFailRollbackFail(t *testing.T) {
	repo := &fakeBatchRepo{deleteErr: errFake}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{err: errFake}
	o := tickFixtureWithAlerts(repo, cfgRepo, nil, enq, &fakeAlertRepo{})

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("回滚失败应上抛交任务级重试，得到 nil")
	}
	if len(repo.created) != 1 {
		t.Fatalf("回滚失败时批次行仍在，实际 %d 条", len(repo.created))
	}
}

// TestTickTriggerAlreadyCreatedThisPeriod 宽限窗内不重复建批：本周期已有定时批次
//（triggered_at 落在本期触发点之后）时，宽限窗内的重复 tick 直接返回不建批。
func TestTickTriggerAlreadyCreatedThisPeriod(t *testing.T) {
	repo := &fakeBatchRepo{runningScheduled: &domain.AssessmentBatch{
		ID: 9, BatchNo: "B202609132300001", TriggerType: domain.BatchTriggerScheduled,
		Status: domain.BatchStatusRunning, TriggeredAt: time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local),
	}}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	// 触发点 1 分钟后（宽限窗内）：已建批判定拦截，不重复建批。
	if err := o.TickTrigger(context.Background(), tickNow().Add(time.Minute)); err != nil {
		t.Fatalf("已建批应返回 nil: %v", err)
	}
	if len(repo.created) != 0 || len(enq.enqIDs) != 0 {
		t.Error("本周期已建批不应重复建批或入队")
	}
}

// TestTickTriggerAlreadyCreatedThisPeriodTerminal 快速终态批次同样拦截重复建批：
// FindLatestScheduled 不限状态，批次秒级落 failed/success 后宽限窗内的剩余
// tick 仍能查到它，不会同周期建第二个批次。
func TestTickTriggerAlreadyCreatedThisPeriodTerminal(t *testing.T) {
	for _, status := range []string{domain.BatchStatusFailed, domain.BatchStatusSuccess} {
		t.Run(status, func(t *testing.T) {
			repo := &fakeBatchRepo{runningScheduled: &domain.AssessmentBatch{
				ID: 9, BatchNo: "B202609132300001", TriggerType: domain.BatchTriggerScheduled,
				Status: status, TriggeredAt: time.Date(2026, 9, 13, 23, 0, 0, 0, time.Local),
			}}
			cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
			enq := &fakeBatchEnqueuer{}
			o := tickFixture(repo, cfgRepo, nil, enq)

			if err := o.TickTrigger(context.Background(), tickNow().Add(time.Minute)); err != nil {
				t.Fatalf("快速终态批次应拦截重复建批: %v", err)
			}
			if len(repo.created) != 0 || len(enq.enqIDs) != 0 {
				t.Error("快速终态批次存在时不应重复建批或入队")
			}
		})
	}
}

// TestTickTriggerSingleQuery 同一 tick 内同源批次查询只发一次：已建批判定与
// 同源阻塞判定共用同一快照（探针计数）。
func TestTickTriggerSingleQuery(t *testing.T) {
	repo := &fakeBatchRepo{}
	repo.findCalls = new(int)
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	o := tickFixture(repo, cfgRepo, nil, &fakeBatchEnqueuer{})

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if *repo.findCalls != 1 {
		t.Errorf("FindLatestScheduled 调用 = %d, want 1（同 tick 复用快照）", *repo.findCalls)
	}
}

// TestTickTriggerMidnightGraceWindow 深夜触发点宽限窗跨零点（daily 23:59，
// tick 延迟到次日 00:00:30 消费）：窗口须锚定触发点（昨日），评估的是完整
// 的上一周期，而非消费时刻所在的新周期。旧实现按 now 推窗会评估零数据的
// 新窗口，上一周期被永久跳过。
func TestTickTriggerMidnightGraceWindow(t *testing.T) {
	repo := &fakeBatchRepo{}
	// daily 23:59：2026-09-13 23:59 触发，宽限窗 [23:59, 00:01)。
	cfg := &domain.AssessmentConfig{ID: 1, Period: domain.PeriodDaily, TriggerTime: "23:59", TargetMode: domain.BatchTargetSpecified, Version: 1}
	cfgRepo := &fakeConfigRepo{cfg: cfg, members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	// 消费时刻已跨零点：2026-09-14 00:00:30。
	consumed := time.Date(2026, 9, 14, 0, 0, 30, 0, time.Local)
	if err := o.TickTrigger(context.Background(), consumed); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	b := repo.created[0]
	// 窗口锚定触发点 09-13（含止日）：[09-13 00:00, 09-14 00:00)。
	wantStart := time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 9, 13, 0, 0, 0, 0, time.Local) // 含止日 = 窗口 end 前一日
	if !b.PeriodStartAt.Equal(wantStart) || !b.PeriodEndAt.Equal(wantEnd) {
		t.Errorf("跨零点窗口 = [%v, %v], want [%v, %v]（锚定触发点周期）",
			b.PeriodStartAt, b.PeriodEndAt, wantStart, wantEnd)
	}
}

// TestTickTriggerMonthEndGraceWindow 月末 23:59 宽限窗跨月：monthly 8/31 触发、
// 9/1 00:00:30 消费，窗口须评估 8 月而非 9 月。
func TestTickTriggerMonthEndGraceWindow(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfg := &domain.AssessmentConfig{ID: 1, Period: domain.PeriodMonthly, TriggerTime: "23:59", TargetMode: domain.BatchTargetSpecified, Version: 1}
	cfgRepo := &fakeConfigRepo{cfg: cfg, members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	o := tickFixture(repo, cfgRepo, nil, enq)

	consumed := time.Date(2026, 9, 1, 0, 0, 30, 0, time.Local)
	if err := o.TickTrigger(context.Background(), consumed); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	b := repo.created[0]
	wantStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	wantEnd := time.Date(2026, 8, 31, 0, 0, 0, 0, time.Local)
	if !b.PeriodStartAt.Equal(wantStart) || !b.PeriodEndAt.Equal(wantEnd) {
		t.Errorf("跨月窗口 = [%v, %v], want [%v, %v]（锚定 8 月）",
			b.PeriodStartAt, b.PeriodEndAt, wantStart, wantEnd)
	}
}

// ---- 建批完成节点（specs P4_LOG_001 §5.2） ----

// TestTickTriggerWritesNodeLog 核心锚点：TickTrigger 建批且入队成功后记
// 「周期批次创建完成」节点；SubmitManualBatch 人工路径不记（避免双行，
// 由 assessment 埋点承载）。
func TestTickTriggerWritesNodeLog(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{}
	rec := &nodeRecorder{}
	o := tickFixtureWithAlertsAndRecorder(repo, cfgRepo, nil, enq, &fakeAlertRepo{}, rec)

	if err := o.TickTrigger(context.Background(), tickNow()); err != nil {
		t.Fatalf("TickTrigger: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("前置：应落 1 条批次，实际 %d", len(repo.created))
	}
	entries := rec.snapshot()
	if len(entries) != 1 {
		t.Fatalf("节点条数 = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Module != "system_job" || e.FallbackName != "系统" || e.Result != "success" {
		t.Errorf("节点头字段异常: %+v", e)
	}
	if e.Summary != "周期批次创建完成" {
		t.Errorf("Summary = %q, want 周期批次创建完成", e.Summary)
	}
	if !strings.Contains(e.Target, repo.created[0].BatchNo) {
		t.Errorf("Target = %q, want 含批次号 %s", e.Target, repo.created[0].BatchNo)
	}
	if !strings.Contains(e.Detail, "指定人员") {
		t.Errorf("Detail = %q, want 含对象模式中文名", e.Detail)
	}
	if e.RequestPath != "engine:batch-tick" {
		t.Errorf("RequestPath = %q, want engine:batch-tick", e.RequestPath)
	}
}

// TestTickTriggerEnqueueFailNoNode 入队失败回滚路径不记建批节点（批次未成立）。
func TestTickTriggerEnqueueFailNoNode(t *testing.T) {
	repo := &fakeBatchRepo{}
	cfgRepo := &fakeConfigRepo{cfg: weeklyCfg(), members: membersOf("张敏")}
	enq := &fakeBatchEnqueuer{err: errFake}
	rec := &nodeRecorder{}
	o := tickFixtureWithAlertsAndRecorder(repo, cfgRepo, nil, enq, &fakeAlertRepo{}, rec)

	if err := o.TickTrigger(context.Background(), tickNow()); err == nil {
		t.Fatal("入队失败应上抛")
	}
	if len(rec.snapshot()) != 0 {
		t.Fatalf("回滚路径不应记建批节点, got %d 条", len(rec.snapshot()))
	}
}

// TestSubmitManualBatchNoNode 人工建批路径不记节点（assessment 埋点承载，避免双行）。
func TestSubmitManualBatchNoNode(t *testing.T) {
	repo := &fakeBatchRepo{}
	enq := &fakeBatchEnqueuer{}
	rec := &nodeRecorder{}
	o := pipeline.NewOrchestrator(repo, nil, nil, nil, nil,
		func(ctx context.Context) (string, error) { return "secret", nil },
		nil, nil, enq, nil, rec)

	if _, err := o.SubmitManualBatch(context.Background(), manualReq([]string{"张三"})); err != nil {
		t.Fatalf("SubmitManualBatch: %v", err)
	}
	if len(rec.snapshot()) != 0 {
		t.Fatalf("人工建批不应记节点, got %d 条", len(rec.snapshot()))
	}
}
