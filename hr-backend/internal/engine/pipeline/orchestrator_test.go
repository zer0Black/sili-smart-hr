package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/integration/conversationlog"
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
	stored     *domain.AssessmentBatch        // GetByID 返回的批次（RunBatch 入口）
	failCalled bool
	failReason string
	failBatch  int64

	utsCalled      bool
	utsBatchID     int64
	utsTotal       int
	utsPersons     map[string]int
	utsPersonsKeys []string // 写入顺序，供确定性断言
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
	return f.stored, nil
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
	f.utsCalled = true
	f.utsBatchID = batchID
	f.utsTotal = totalSessions
	f.utsPersons = personSessions
	for _, p := range f.persons {
		f.utsPersonsKeys = append(f.utsPersonsKeys, p.TokenName)
	}
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
	err    error
	enqIDs []int64
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

// ---- RunBatch 展开分组与抽取投递（specs §5.2.2 步骤2-3） ----

// fakeSessionFetcher 会话列表 fake：pages 逐页返回，耗尽返空页终止；lastReq 记录末次请求。
type fakeSessionFetcher struct {
	pages   [][]conversationlog.SessionSummary
	err     error
	calls   int
	lastReq conversationlog.ListSessionsRequest
}

func (f *fakeSessionFetcher) ListSessions(ctx context.Context, secret string, req conversationlog.ListSessionsRequest) ([]conversationlog.SessionSummary, int64, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, 0, f.err
	}
	idx := f.calls
	f.calls++
	if idx >= len(f.pages) {
		return nil, 0, nil
	}
	var total int64
	for _, p := range f.pages {
		total += int64(len(p))
	}
	return f.pages[idx], total, nil
}

// fakeSessionEnqueuer 会话抽取投递 fake：逐次记录入参，errs 注入逐次错误。
type fakeSessionEnqueuer struct {
	errs   []error // 逐次消费，耗尽后 nil
	calls  int
	keys   []string
	tokens []string
}

var _ pipeline.SessionEnqueuer = (*fakeSessionEnqueuer)(nil)

func (f *fakeSessionEnqueuer) EnqueueSessionExtract(ctx context.Context, sessionKey, tokenName string) error {
	f.calls++
	f.keys = append(f.keys, sessionKey)
	f.tokens = append(f.tokens, tokenName)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return nil
}

// newRunBatchOrch 组装 RunBatch 链路编排器。
func newRunBatchOrch(repo *fakeBatchRepo, alertRepo *fakeAlertRepo, fetcher *fakeSessionFetcher, sessionEnq *fakeSessionEnqueuer) *pipeline.Orchestrator {
	return pipeline.NewOrchestrator(repo, alertRepo, nil, nil, fetcher, nil,
		func(ctx context.Context) (string, error) { return "secret", nil },
		nil, alertWriter(alertRepo), nil, sessionEnq)
}

// runBatchFixture 建 running 批次：3 人明细、窗口 2026-09-07 至 2026-09-13（含止日）。
func runBatchFixture(repo *fakeBatchRepo) *domain.AssessmentBatch {
	repo.persons = []domain.AssessmentBatchPerson{
		{BatchID: 1, TokenName: "张敏", Status: domain.PersonStatusPending},
		{BatchID: 1, TokenName: "李芳", Status: domain.PersonStatusPending},
		{BatchID: 1, TokenName: "王强", Status: domain.PersonStatusPending},
	}
	batch := &domain.AssessmentBatch{
		ID:            1,
		BatchNo:       "B202609070800001",
		TriggerType:   domain.BatchTriggerScheduled,
		TargetMode:    domain.BatchTargetSpecified,
		TotalCount:    3,
		Status:        domain.BatchStatusRunning,
		PeriodStartAt: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		PeriodEndAt:   time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
		TriggeredAt:   time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
	}
	repo.stored = batch
	return batch
}

// padFullPage 把页片前 3 行后补 filler 凑满 100 行（满页才会翻到下一页）。
// filler 用页外 token_name（名单快照外人员），不影响主断言的分组口径。
func padFullPage(rows []conversationlog.SessionSummary, fillerPrefix string) []conversationlog.SessionSummary {
	out := append([]conversationlog.SessionSummary{}, rows...)
	for i := len(rows); i < 100; i++ {
		out = append(out, conversationlog.SessionSummary{
			SessionKey: fmt.Sprintf("%s-filler-%d", fillerPrefix, i),
			TokenName:  fillerPrefix + "路人",
		})
	}
	return out
}

func TestRunBatchDedupAndGroup(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	// 2 页：page1 满页（3 行名单会话 + 97 filler），page2 短页含 sk-a 跨页重复与
	// 王强 1 行；名单内去重后 张敏 1、李芳 2、王强 1；filler 会话属名单外人员，
	// 计入 total_session_count（全量口径）但不投递给名单内断言面。
	fetcher := &fakeSessionFetcher{pages: [][]conversationlog.SessionSummary{
		padFullPage([]conversationlog.SessionSummary{
			{SessionKey: "sk-a", TokenName: "张敏"},
			{SessionKey: "sk-b", TokenName: "李芳"},
			{SessionKey: "sk-c", TokenName: "李芳"},
		}, "p1"),
		{
			{SessionKey: "sk-a", TokenName: "张敏"}, // 跨页重复（BR4）
			{SessionKey: "sk-d", TokenName: "王强"},
		},
	}}
	sessionEnq := &fakeSessionEnqueuer{}
	o := newRunBatchOrch(repo, &fakeAlertRepo{}, fetcher, sessionEnq)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if fetcher.calls != 2 {
		t.Errorf("ListSessions 调用 = %d, want 2（满页后短页终止）", fetcher.calls)
	}
	// 全量拉取口径：无按人过滤字段（T4 §3.2 禁令）。
	if fetcher.lastReq.Username != "" || fetcher.lastReq.UserID != 0 || fetcher.lastReq.PageSize != 100 {
		t.Errorf("lastReq = %+v, want PageSize=100 且无按人过滤", fetcher.lastReq)
	}
	// Period 半开区间：Start 当日零点、End 加一天零点（03 §2.5）。
	if fetcher.lastReq.StartTime != batch.PeriodStartAt.Unix() {
		t.Errorf("StartTime = %d, want %d", fetcher.lastReq.StartTime, batch.PeriodStartAt.Unix())
	}
	if fetcher.lastReq.EndTime != batch.PeriodEndAt.AddDate(0, 0, 1).Unix() {
		t.Errorf("EndTime = %d, want PeriodEnd+1 天 %d", fetcher.lastReq.EndTime, batch.PeriodEndAt.AddDate(0, 0, 1).Unix())
	}
	if !repo.utsCalled {
		t.Fatal("UpdateTotalSessions 未被调用")
	}
	if repo.utsBatchID != batch.ID {
		t.Errorf("UpdateTotalSessions batchID = %d, want %d", repo.utsBatchID, batch.ID)
	}
	// 名单内人员分组口径（specs §5.2.2 步骤2 展开分组）：去重后 张敏 1、李芳 2、王强 1。
	want := map[string]int{"张敏": 1, "李芳": 2, "王强": 1}
	for name, cnt := range want {
		if repo.utsPersons[name] != cnt {
			t.Errorf("personSessions[%q] = %d, want %d（全量 %v）", name, repo.utsPersons[name], cnt, repo.utsPersons)
		}
	}
	// 总数 = 去重后全部会话（含名单外 filler：100 + 2 - 1 重复 = 101）。
	if repo.utsTotal != 101 {
		t.Errorf("totalSessions = %d, want 101（去重后全量）", repo.utsTotal)
	}
}

func TestRunBatchEnqueuesExtract(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	fetcher := &fakeSessionFetcher{pages: [][]conversationlog.SessionSummary{
		padFullPage([]conversationlog.SessionSummary{
			{SessionKey: "sk-a", TokenName: "张敏"},
			{SessionKey: "sk-b", TokenName: "李芳"},
			{SessionKey: "sk-c", TokenName: "李芳"},
		}, "p1"),
		{
			{SessionKey: "sk-a", TokenName: "张敏"}, // 跨页重复不重复投递（BR4）
			{SessionKey: "sk-d", TokenName: "王强"},
		},
	}}
	sessionEnq := &fakeSessionEnqueuer{}
	o := newRunBatchOrch(repo, &fakeAlertRepo{}, fetcher, sessionEnq)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if sessionEnq.calls != 101 {
		t.Fatalf("EnqueueSessionExtract 调用 = %d, want 101（去重后全量会话数）", sessionEnq.calls)
	}
	for i, k := range sessionEnq.keys {
		if k == "" {
			t.Errorf("第 %d 次投递 session_key 为空", i)
		}
	}
	// 名单内会话归属与去重口径：sk-a 只投递一次，token_name 与会话随行携带。
	got := map[string]string{}
	dup := 0
	for i, k := range sessionEnq.keys {
		if _, ok := got[k]; ok {
			dup++
		}
		got[k] = sessionEnq.tokens[i]
	}
	if dup != 0 {
		t.Errorf("重复投递 %d 次（跨页重复应去重，BR4）", dup)
	}
	want := map[string]string{"sk-a": "张敏", "sk-b": "李芳", "sk-c": "李芳", "sk-d": "王强"}
	for k, tok := range want {
		if got[k] != tok {
			t.Errorf("投递 %q 的 token_name = %q, want %q", k, got[k], tok)
		}
	}
}

func TestRunBatchUpstreamFail(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	fetcher := &fakeSessionFetcher{err: errFake}
	sessionEnq := &fakeSessionEnqueuer{}
	alertRepo := &fakeAlertRepo{}
	o := newRunBatchOrch(repo, alertRepo, fetcher, sessionEnq)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch 整批失败终态应返回 nil（不重试），得到 %v", err)
	}
	if !repo.failCalled {
		t.Error("列表拉取失败应调 FailWholeBatch 落整批 failed")
	}
	if repo.failBatch != batch.ID {
		t.Errorf("FailWholeBatch batchID = %d, want %d", repo.failBatch, batch.ID)
	}
	if repo.failReason == "" {
		t.Error("FailWholeBatch reason 应记上游错误摘要")
	}
	if sessionEnq.calls != 0 {
		t.Errorf("整批失败不应投递抽取任务，实际 %d 次", sessionEnq.calls)
	}
	if repo.utsCalled {
		t.Error("整批失败不应回填会话数")
	}
	// 占比 100.00 超阈必写告警（§5.2.4 规则4）。
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("应写 1 条告警，实际 %d", len(alertRepo.alerts))
	}
	if alertRepo.alerts[0].FailedRatio != 100.00 {
		t.Errorf("告警占比 = %v, want 100.00", alertRepo.alerts[0].FailedRatio)
	}
	if alertRepo.alerts[0].FailedCount != batch.TotalCount || alertRepo.alerts[0].TotalCount != batch.TotalCount {
		t.Errorf("告警计数 = %d/%d, want %d/%d", alertRepo.alerts[0].FailedCount, alertRepo.alerts[0].TotalCount, batch.TotalCount, batch.TotalCount)
	}
}

func TestRunBatchIdempotentTerminal(t *testing.T) {
	for _, status := range []string{domain.BatchStatusSuccess, domain.BatchStatusPartialFailed, domain.BatchStatusFailed} {
		t.Run(status, func(t *testing.T) {
			repo := &fakeBatchRepo{}
			batch := runBatchFixture(repo)
			batch.Status = status
			fetcher := &fakeSessionFetcher{pages: [][]conversationlog.SessionSummary{
				{{SessionKey: "sk-a", TokenName: "张敏"}},
			}}
			sessionEnq := &fakeSessionEnqueuer{}
			alertRepo := &fakeAlertRepo{}
			o := newRunBatchOrch(repo, alertRepo, fetcher, sessionEnq)

			if err := o.RunBatch(context.Background(), batch.ID); err != nil {
				t.Fatalf("终态批次应幂等返回 nil，得到 %v", err)
			}
			if fetcher.calls != 0 {
				t.Error("终态批次不应拉取上游列表")
			}
			if sessionEnq.calls != 0 {
				t.Error("终态批次不应投递抽取任务")
			}
			if repo.utsCalled {
				t.Error("终态批次不应回填会话数")
			}
			if repo.failCalled {
				t.Error("终态批次不应调 FailWholeBatch")
			}
			if len(alertRepo.alerts) != 0 {
				t.Error("终态批次不应写告警")
			}
		})
	}
}

// TestRunBatchEnqueueFailContinues 补充（BR5 §5.2.5）：单次投递失败记日志继续，
// 其余会话照常投递，计数回填不受影响，RunBatch 返回 nil。
func TestRunBatchEnqueueFailContinues(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	fetcher := &fakeSessionFetcher{pages: [][]conversationlog.SessionSummary{
		{
			{SessionKey: "sk-a", TokenName: "张敏"},
			{SessionKey: "sk-b", TokenName: "李芳"},
		},
	}}
	sessionEnq := &fakeSessionEnqueuer{errs: []error{errFake, nil}}
	o := newRunBatchOrch(repo, &fakeAlertRepo{}, fetcher, sessionEnq)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("投递失败不阻断编排，应返回 nil，得到 %v", err)
	}
	if sessionEnq.calls != 2 {
		t.Errorf("投递失败后续会话应照常投递，调用 = %d, want 2", sessionEnq.calls)
	}
	if repo.utsTotal != 2 {
		t.Errorf("totalSessions = %d, want 2（漏计口径不影响回填总数）", repo.utsTotal)
	}
}

// TestRunBatchBatchNotFound 补充边界：批次记录不存在（GetByID 返回 nil）按
// 幂等终态同款返回 nil，不触碰上游与投递（重复消费已删批次不留副作用）。
func TestRunBatchBatchNotFound(t *testing.T) {
	repo := &fakeBatchRepo{} // stored 为 nil
	fetcher := &fakeSessionFetcher{}
	sessionEnq := &fakeSessionEnqueuer{}
	o := newRunBatchOrch(repo, &fakeAlertRepo{}, fetcher, sessionEnq)

	if err := o.RunBatch(context.Background(), 999); err != nil {
		t.Fatalf("批次不存在应返回 nil，得到 %v", err)
	}
	if fetcher.calls != 0 || sessionEnq.calls != 0 || repo.utsCalled {
		t.Error("批次不存在不应有任何副作用")
	}
}
