package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

var errFake = errors.New("fake failure")

// fakeBatchRepo 批次仓储 fake：Create 支持注入错误（模拟撞 uk_batch_no）。
type fakeBatchRepo struct {
	createErrs []error // 逐次消费，耗尽后返回 nil
	created    []*domain.AssessmentBatch
	persons    []domain.AssessmentBatchPerson // CreatePersons 捕获的明细行
	failCalled bool
	failReason string
	failBatch  int64
}

var _ repository.AssessmentBatchRepository = (*fakeBatchRepo)(nil)

func (f *fakeBatchRepo) Create(ctx context.Context, b *domain.AssessmentBatch) error {
	if len(f.createErrs) > 0 {
		err := f.createErrs[0]
		f.createErrs = f.createErrs[1:]
		if err != nil {
			return err
		}
	}
	b.ID = int64(len(f.created) + 1)
	f.created = append(f.created, b)
	return nil
}

func (f *fakeBatchRepo) GetByID(ctx context.Context, id int64) (*domain.AssessmentBatch, error) {
	return nil, nil
}

func (f *fakeBatchRepo) CreatePersons(ctx context.Context, persons []domain.AssessmentBatchPerson) error {
	f.persons = append(f.persons, persons...)
	return nil
}
func (f *fakeBatchRepo) FindLatestRunningScheduled(ctx context.Context) (*domain.AssessmentBatch, error) {
	return nil, nil
}
func (f *fakeBatchRepo) ListByFilter(ctx context.Context, bf repository.BatchFilter) ([]domain.AssessmentBatch, int64, error) {
	return nil, 0, nil
}
func (f *fakeBatchRepo) UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error {
	return nil
}
func (f *fakeBatchRepo) AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName, personStatus, errorSummary string, sessionCount int) error {
	return nil
}
func (f *fakeBatchRepo) FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error {
	return nil
}
func (f *fakeBatchRepo) FailWholeBatch(ctx context.Context, batchID int64, reason string) error {
	f.failCalled = true
	f.failReason = reason
	f.failBatch = batchID
	return nil
}
func (f *fakeBatchRepo) CountInRange(ctx context.Context, start, end time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeBatchRepo) CountRunningNonStalled(ctx context.Context, stalledBefore time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeBatchRepo) CountSuccessSideInRanges(ctx context.Context, batchIDs []int64) (int64, error) {
	return 0, nil
}

// fakeAlertRepo 告警仓储 fake。
type fakeAlertRepo struct {
	alerts []domain.AssessmentAlert
}

var _ repository.AssessmentAlertRepository = (*fakeAlertRepo)(nil)

func (f *fakeAlertRepo) UpsertByBatch(ctx context.Context, alert *domain.AssessmentAlert) error {
	f.alerts = append(f.alerts, *alert)
	return nil
}

// alertWriter 构造接 fake 仓储的真实 AlertWriter。
func alertWriter(repo *fakeAlertRepo) *fallback.AlertWriter {
	return fallback.NewAlertWriter(repo)
}

// fakeStaffFetcher 人员拉取 fake：pages 顺序按调用次序返回，耗尽返回满页终止信号。
type fakeStaffFetcher struct {
	pages [][]userapi.Staff
	err   error
	calls int
}

var _ pipeline.StaffFetcher = (*fakeStaffFetcher)(nil)

func (f *fakeStaffFetcher) ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) ([]userapi.Staff, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	idx := f.calls
	f.calls++
	if idx >= len(f.pages) {
		return nil, 0, nil // 页片耗尽，上游无更多数据
	}
	total := int64(0)
	for _, p := range f.pages {
		total += int64(len(p))
	}
	return f.pages[idx], total, nil
}

// fakeBatchEnqueuer 投递 fake。
type fakeBatchEnqueuer struct {
	err     error
	enqIDs  []int64
}

var _ pipeline.BatchEnqueuer = (*fakeBatchEnqueuer)(nil)

func (f *fakeBatchEnqueuer) EnqueueBatchRun(ctx context.Context, batchID int64) error {
	if f.err != nil {
		return f.err
	}
	f.enqIDs = append(f.enqIDs, batchID)
	return nil
}

func staffs(names ...string) []userapi.Staff {
	out := make([]userapi.Staff, 0, len(names))
	for i, n := range names {
		out = append(out, userapi.Staff{StaffID: string(rune('1' + i)), StaffName: n})
	}
	return out
}

func newOrch(repo *fakeBatchRepo, alertRepo *fakeAlertRepo, fetcher pipeline.StaffFetcher, enq *fakeBatchEnqueuer) *pipeline.Orchestrator {
	return pipeline.NewOrchestrator(repo, alertRepo, nil, nil, nil, fetcher,
		func(ctx context.Context) (string, error) { return "secret", nil },
		nil, alertWriter(alertRepo), enq, nil)
}

func manualReq(names []string) pipeline.CreateBatchRequest {
	return pipeline.CreateBatchRequest{
		TriggerType: domain.BatchTriggerManual,
		TargetMode:  domain.BatchTargetSpecified,
		TargetNames: names,
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
	}
}

func TestCreateBatchSpecified(t *testing.T) {
	repo := &fakeBatchRepo{}
	alertRepo := &fakeAlertRepo{}
	o := newOrch(repo, alertRepo, nil, &fakeBatchEnqueuer{})

	batch, err := o.CreateBatch(context.Background(), manualReq([]string{"张三", "李四", "张三"}))
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if len(repo.persons) != 2 {
		t.Fatalf("人员明细 = %d 行, want 2", len(repo.persons))
	}
	for _, p := range repo.persons {
		if p.Status != domain.PersonStatusPending {
			t.Errorf("人员明细 Status = %q, want pending", p.Status)
		}
		if p.SessionCount != 0 || p.ErrorSummary != "" {
			t.Errorf("人员明细初始值 = (%d, %q), want (0, \"\")", p.SessionCount, p.ErrorSummary)
		}
	}
	if batch.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", batch.TotalCount)
	}
	if batch.Status != domain.BatchStatusRunning {
		t.Errorf("Status = %q, want running", batch.Status)
	}
	if batch.TriggerType != domain.BatchTriggerManual {
		t.Errorf("TriggerType = %q, want manual", batch.TriggerType)
	}
	var names []string
	if err := json.Unmarshal([]byte(batch.TargetNamesJSON), &names); err != nil {
		t.Fatalf("TargetNamesJSON 反序列化: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("名单快照人数 = %d, want 2", len(names))
	}
}

func TestCreateBatchAllFetchesStaffs(t *testing.T) {
	repo := &fakeBatchRepo{}
	fetcher := &fakeStaffFetcher{pages: [][]userapi.Staff{
		staffs("甲", "乙", "丙"),
		staffs("丁", "戊"),
	}}
	o := newOrch(repo, &fakeAlertRepo{}, fetcher, &fakeBatchEnqueuer{})

	req := manualReq(nil)
	req.TargetMode = domain.BatchTargetAll
	batch, err := o.CreateBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if batch.TotalCount != 5 {
		t.Errorf("TotalCount = %d, want 5", batch.TotalCount)
	}
	var names []string
	if err := json.Unmarshal([]byte(batch.TargetNamesJSON), &names); err != nil {
		t.Fatalf("TargetNamesJSON 反序列化: %v", err)
	}
	if len(names) != 5 {
		t.Errorf("名单快照人数 = %d, want 5", len(names))
	}
}

func TestCreateBatchAllFails(t *testing.T) {
	repo := &fakeBatchRepo{}
	fetcher := &fakeStaffFetcher{err: errFake}
	o := newOrch(repo, &fakeAlertRepo{}, fetcher, &fakeBatchEnqueuer{})

	req := manualReq(nil)
	req.TargetMode = domain.BatchTargetAll
	_, err := o.CreateBatch(context.Background(), req)
	if err == nil {
		t.Fatal("预期返回错误，得到 nil")
	}
	if len(repo.created) != 0 {
		t.Errorf("名单拉取失败不应落批次，已落 %d 条", len(repo.created))
	}
}

func TestCreateBatchEmptyNamesRejected(t *testing.T) {
	repo := &fakeBatchRepo{}
	o := newOrch(repo, &fakeAlertRepo{}, nil, &fakeBatchEnqueuer{})

	_, err := o.CreateBatch(context.Background(), manualReq(nil))
	if err == nil {
		t.Fatal("空名单预期返回错误，得到 nil")
	}
	if len(repo.created) != 0 {
		t.Error("空名单不应落批次")
	}
}

func TestCreateBatchUniqueRetry(t *testing.T) {
	repo := &fakeBatchRepo{createErrs: []error{gorm.ErrDuplicatedKey}}
	o := newOrch(repo, &fakeAlertRepo{}, nil, &fakeBatchEnqueuer{})

	batch, err := o.CreateBatch(context.Background(), manualReq([]string{"张三"}))
	if err != nil {
		t.Fatalf("撞唯一键后应重取序号成功: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应落 1 条批次，实际 %d", len(repo.created))
	}
	if matched, _ := regexp.MatchString(`^B\d{12}\d{3}$`, batch.BatchNo); !matched {
		t.Errorf("批次号格式不符: %q", batch.BatchNo)
	}
}

func TestSubmitManualBatchEnqueueFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	alertRepo := &fakeAlertRepo{}
	enq := &fakeBatchEnqueuer{err: errFake}
	o := newOrch(repo, alertRepo, nil, enq)

	_, err := o.SubmitManualBatch(context.Background(), manualReq([]string{"张三"}))
	if err == nil {
		t.Fatal("入队失败预期返回错误，得到 nil")
	}
	if !repo.failCalled {
		t.Error("入队失败应调 FailWholeBatch 落 failed 终态")
	}
	if len(alertRepo.alerts) != 1 {
		t.Errorf("占比 100.00 超阈应写告警，实际 %d 条", len(alertRepo.alerts))
	} else if alertRepo.alerts[0].FailedRatio != 100.00 {
		t.Errorf("告警占比 = %v, want 100.00", alertRepo.alerts[0].FailedRatio)
	}
}

func TestSubmitManualBatchEnqueueOK(t *testing.T) {
	repo := &fakeBatchRepo{}
	alertRepo := &fakeAlertRepo{}
	enq := &fakeBatchEnqueuer{}
	o := newOrch(repo, alertRepo, nil, enq)

	batch, err := o.SubmitManualBatch(context.Background(), manualReq([]string{"张三"}))
	if err != nil {
		t.Fatalf("SubmitManualBatch: %v", err)
	}
	if len(enq.enqIDs) != 1 || enq.enqIDs[0] != batch.ID {
		t.Errorf("入队批次 ID 不符: %v vs %d", enq.enqIDs, batch.ID)
	}
	if repo.failCalled {
		t.Error("入队成功不应调 FailWholeBatch")
	}
}

func TestBatchNoFormat(t *testing.T) {
	repo := &fakeBatchRepo{}
	o := newOrch(repo, &fakeAlertRepo{}, nil, &fakeBatchEnqueuer{})

	batch, err := o.CreateBatch(context.Background(), manualReq([]string{"张三"}))
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if matched, _ := regexp.MatchString(`^B\d{12}\d{3}$`, batch.BatchNo); !matched {
		t.Errorf("批次号格式不符（B+12位时间+3位序号，共16字符）: %q", batch.BatchNo)
	}
}

// 并发调用安全探针：fake 间无共享态交叉即可，主要守护序号生成无数据竞争语义。
func TestCreateBatchConcurrent(t *testing.T) {
	repo := &fakeBatchRepo{}
	o := newOrch(repo, &fakeAlertRepo{}, nil, &fakeBatchEnqueuer{})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = o.CreateBatch(context.Background(), manualReq([]string{"并发人"}))
		}()
	}
	wg.Wait()
}
