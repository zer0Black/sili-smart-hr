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
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/evaluator"
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/engine/pipeline"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

var errFake = errors.New("fake failure")

// fakeBatchRepo 批次仓储 fake：Create 支持注入错误（模拟撞 uk_batch_no）。
// advanceErrs 按 token_name 注入 AdvancePersonTerminal 恒错（BR6 回写失败探针）。
type fakeBatchRepo struct {
	mu           sync.Mutex
	createErrs   []error // 逐次消费，耗尽后返回 nil
	created      []*domain.AssessmentBatch
	persons      []domain.AssessmentBatchPerson // CreatePersons 捕获的明细行
	stored       *domain.AssessmentBatch        // GetByID 返回的批次（RunBatch 入口）
	runningScheduled *domain.AssessmentBatch    // FindLatestRunningScheduled 返回的批次（tick 同源阻塞探针）
	advanceErrs  map[string]error
	advanceCalls map[string]int
	advanceErr   map[string]string
	finalizeCalls []string

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
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stored, nil
}

func (f *fakeBatchRepo) CreatePersons(ctx context.Context, persons []domain.AssessmentBatchPerson) error {
	f.persons = append(f.persons, persons...)
	return nil
}
func (f *fakeBatchRepo) FindLatestRunningScheduled(ctx context.Context) (*domain.AssessmentBatch, error) {
	return f.runningScheduled, nil
}
func (f *fakeBatchRepo) ListByFilter(ctx context.Context, bf repository.BatchFilter) ([]domain.AssessmentBatch, int64, error) {
	return nil, 0, nil
}
func (f *fakeBatchRepo) UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.utsCalled = true
	f.utsBatchID = batchID
	f.utsTotal = totalSessions
	f.utsPersons = personSessions
	if f.stored != nil {
		f.stored.TotalSessionCount = totalSessions
	}
	for _, p := range f.persons {
		f.utsPersonsKeys = append(f.utsPersonsKeys, p.TokenName)
	}
	return nil
}
// AdvancePersonTerminal 内存模拟仓储推进：幂等守卫（已终态跳过）、按终态累计计数、
// errorSummary 按 255 rune 截断（对齐真实仓储 truncateRunes），按人可注入恒错。
func (f *fakeBatchRepo) AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName, personStatus, errorSummary string, sessionCount int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.advanceErrs[tokenName]; ok {
		return err
	}
	for i := range f.persons {
		p := &f.persons[i]
		if p.BatchID != batchID || p.TokenName != tokenName {
			continue
		}
		if p.Status != domain.PersonStatusPending {
			return nil // 重入已终态：跳过计数自增
		}
		p.Status = personStatus
		if personStatus == domain.PersonStatusFailed {
			runes := []rune(errorSummary)
			if len(runes) > 255 {
				errorSummary = string(runes[:255])
			}
			p.ErrorSummary = errorSummary
		} else {
			p.ErrorSummary = ""
		}
		f.stored.EvaluatedCount++
		if personStatus == domain.PersonStatusFailed {
			f.stored.FailedCount++
		} else {
			f.stored.CoveredSessionCount += sessionCount
		}
		if f.advanceCalls == nil {
			f.advanceCalls = map[string]int{}
		}
		f.advanceCalls[tokenName]++
		if f.advanceErr == nil {
			f.advanceErr = map[string]string{}
		}
		f.advanceErr[tokenName] = errorSummary
		return nil
	}
	return nil
}

// FinalizeBatch 内存模拟（同真实仓储 WHERE status='running' 守卫）。
func (f *fakeBatchRepo) FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalizeCalls = append(f.finalizeCalls, status)
	if f.stored.Status != domain.BatchStatusRunning {
		return nil
	}
	f.stored.Status = status
	ratio := sessionFailRatio
	f.stored.SessionFailRatio = &ratio
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
func (f *fakeBatchRepo) ListBatchIDsTriggeredBetween(ctx context.Context, start, end time.Time) ([]int64, error) {
	return nil, nil
}
func (f *fakeBatchRepo) FindLatestScheduled(ctx context.Context) (*domain.AssessmentBatch, error) {
	return nil, nil
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

// fakeFeatureRepo 特征档案仓储 fake：只承载 T3 消费面 CountFailedByTokenNames。
type fakeFeatureRepo struct {
	failed int64
	names  []string
	start  int64
	end    int64
	calls  int
}

var _ repository.SessionFeatureRepository = (*fakeFeatureRepo)(nil)

func (f *fakeFeatureRepo) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	return nil, nil
}
func (f *fakeFeatureRepo) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	return false, nil
}
func (f *fakeFeatureRepo) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	return nil, nil
}
func (f *fakeFeatureRepo) CountFailedByTokenNames(ctx context.Context, tokenNames []string, start, end int64) (int64, error) {
	f.calls++
	f.names = tokenNames
	f.start, f.end = start, end
	return f.failed, nil
}

// fakePersonEvaluator 单人评估 fake：perName 结果切片逐次消费（重试耗尽按次数
// 给错误），缺省返回正常结果。
type fakePersonEvaluator struct {
	perName map[string][]evalAttempt
	calls   map[string]int
	mu      sync.Mutex
}

// evalAttempt 一次尝试的返回：err 非 nil 上抛，否则返回 res（res 为 nil 视为正常成功）。
type evalAttempt struct {
	res *evaluator.EvaluateResult
	err error
}

var _ pipeline.PersonEvaluator = (*fakePersonEvaluator)(nil)

func (f *fakePersonEvaluator) EvaluatePerson(ctx context.Context, tokenName string, period activity.Period, sessions []conversationlog.SessionSummary) (*evaluator.EvaluateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[tokenName]++
	attempts := f.perName[tokenName]
	if len(attempts) == 0 {
		return &evaluator.EvaluateResult{}, nil
	}
	idx := f.calls[tokenName] - 1
	if idx >= len(attempts) {
		idx = len(attempts) - 1
	}
	a := attempts[idx]
	if a.err != nil {
		return nil, a.err
	}
	if a.res != nil {
		return a.res, nil
	}
	return &evaluator.EvaluateResult{}, nil
}

// scoreRow 构造评分行（status 由用例指定）。
func scoreRow(status string) domain.DimensionScore {
	return domain.DimensionScore{Status: status}
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

// newRunBatchOrch 组装 RunBatch 链路编排器（T2 段只到投递，评估器与特征仓储以
// 零值 fake 承载防止 T3 逐人评估段空指针，断言面不涉及其计数）。
func newRunBatchOrch(repo *fakeBatchRepo, alertRepo *fakeAlertRepo, fetcher *fakeSessionFetcher, sessionEnq *fakeSessionEnqueuer) *pipeline.Orchestrator {
	o := pipeline.NewOrchestrator(repo, alertRepo, &fakeFeatureRepo{}, nil, fetcher, nil,
		func(ctx context.Context) (string, error) { return "secret", nil },
		&fakePersonEvaluator{}, alertWriter(alertRepo), nil, sessionEnq)
	o.SetRetryBaseForTest(time.Millisecond)
	return o
}

// runBatchFixture 建 running 批次：3 人明细、窗口 2026-09-07 至 2026-09-13（含止日）。
func runBatchFixture(repo *fakeBatchRepo) *domain.AssessmentBatch {
	namesJSON, _ := json.Marshal([]string{"张敏", "李芳", "王强"})
	repo.persons = []domain.AssessmentBatchPerson{
		{BatchID: 1, TokenName: "张敏", Status: domain.PersonStatusPending},
		{BatchID: 1, TokenName: "李芳", Status: domain.PersonStatusPending},
		{BatchID: 1, TokenName: "王强", Status: domain.PersonStatusPending},
	}
	batch := &domain.AssessmentBatch{
		ID:              1,
		BatchNo:         "B202609070800001",
		TriggerType:     domain.BatchTriggerScheduled,
		TargetMode:      domain.BatchTargetSpecified,
		TargetNamesJSON: string(namesJSON),
		TotalCount:      3,
		Status:          domain.BatchStatusRunning,
		PeriodStartAt:   time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		PeriodEndAt:     time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
		TriggeredAt:     time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
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

// ---- RunBatch 逐人评估 + 终态推进 + 告警（specs §5.2.2 步骤4-7、§5.2.4、§5.2.5、§5.3.4） ----

// TestPersonTerminal 终态映射表（03 §4.6）：Reused 标志优先、Skipped 标志次之、
// 含 failed 评分行降级、正常返回 success。
func TestPersonTerminal(t *testing.T) {
	cases := []struct {
		name string
		res  *evaluator.EvaluateResult
		want string
	}{
		{"正常返回", &evaluator.EvaluateResult{Scores: []domain.DimensionScore{scoreRow(domain.ScoreStatusSuccess)}}, domain.PersonStatusSuccess},
		{"复用标志优先", &evaluator.EvaluateResult{Reused: true, Skipped: true, Scores: []domain.DimensionScore{scoreRow(domain.ScoreStatusFailed)}}, domain.PersonStatusReused},
		{"跳过标志", &evaluator.EvaluateResult{Skipped: true}, domain.PersonStatusSkipped},
		{"含 failed 评分行降级", &evaluator.EvaluateResult{Scores: []domain.DimensionScore{scoreRow(domain.ScoreStatusSuccess), scoreRow(domain.ScoreStatusFailed)}}, domain.PersonStatusDegraded},
		{"空评分行正常", &evaluator.EvaluateResult{}, domain.PersonStatusSuccess},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pipeline.PersonTerminal(c.res); got != c.want {
				t.Errorf("personTerminal = %q, want %q", got, c.want)
			}
		})
	}
}

// evalOrch 组装含评估器与特征仓储的 RunBatch 全链编排器（退避置 1ms 免测试等待）。
func evalOrch(repo *fakeBatchRepo, alertRepo *fakeAlertRepo, fetcher *fakeSessionFetcher, featureRepo *fakeFeatureRepo, pe *fakePersonEvaluator) *pipeline.Orchestrator {
	o := pipeline.NewOrchestrator(repo, alertRepo, featureRepo, nil, fetcher, nil,
		func(ctx context.Context) (string, error) { return "secret", nil },
		pe, alertWriter(alertRepo), nil, &fakeSessionEnqueuer{})
	o.SetRetryBaseForTest(time.Millisecond)
	return o
}

func sessionsOf(names map[string]int) [][]conversationlog.SessionSummary {
	var page []conversationlog.SessionSummary
	i := 0
	for name, n := range names {
		for j := 0; j < n; j++ {
			page = append(page, conversationlog.SessionSummary{
				SessionKey: fmt.Sprintf("sk-%d-%d", i, j),
				TokenName:  name,
			})
		}
		i++
	}
	return [][]conversationlog.SessionSummary{page}
}

func TestRunBatchTerminalMapping(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	// 张敏 2 会话正常，李芳 1 会话 Reused，王强 1 会话重试耗尽 error。
	fetcher := &fakeSessionFetcher{pages: sessionsOf(map[string]int{"张敏": 2, "李芳": 1, "王强": 1})}
	pe := &fakePersonEvaluator{perName: map[string][]evalAttempt{
		"李芳": {{res: &evaluator.EvaluateResult{Reused: true}}},
		"王强": {{err: errFake}, {err: errFake}, {err: errFake}, {err: errFake}},
	}}
	featureRepo := &fakeFeatureRepo{failed: 1} // 1 个 failed 档案：比例 25.00
	alertRepo := &fakeAlertRepo{}
	o := evalOrch(repo, alertRepo, fetcher, featureRepo, pe)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if batch.Status != domain.BatchStatusPartialFailed {
		t.Errorf("Status = %q, want partial_failed（失败占比 33.33 > 10.00）", batch.Status)
	}
	if batch.FailedCount != 1 {
		t.Errorf("FailedCount = %d, want 1", batch.FailedCount)
	}
	if batch.EvaluatedCount != 3 {
		t.Errorf("EvaluatedCount = %d, want 3", batch.EvaluatedCount)
	}
	// 成功侧两人会话数 2+1=3，失败人员会话不计入（§5.2.4 规则3）。
	if batch.CoveredSessionCount != 3 {
		t.Errorf("CoveredSessionCount = %d, want 3（只含成功侧两人）", batch.CoveredSessionCount)
	}
	if pe.calls["王强"] != 4 {
		t.Errorf("王强评估尝试 = %d 次, want 4（首次+3 重试，§5.3.4 规则1）", pe.calls["王强"])
	}
	// 人员终态映射：正常 success、复用 reused、耗尽 failed。
	wantStatus := map[string]string{"张敏": domain.PersonStatusSuccess, "李芳": domain.PersonStatusReused, "王强": domain.PersonStatusFailed}
	for name := range wantStatus {
		if repo.advanceCalls[name] != 1 {
			t.Errorf("%s AdvancePersonTerminal 调用 = %d, want 1", name, repo.advanceCalls[name])
		}
	}
	for _, p := range repo.persons {
		if p.Status != wantStatus[p.TokenName] {
			t.Errorf("%s 人员终态 = %q, want %q", p.TokenName, p.Status, wantStatus[p.TokenName])
		}
	}
	if repo.advanceErr["张敏"] != "" || repo.advanceErr["李芳"] != "" {
		t.Error("成功侧终态 errorSummary 应为空串")
	}
	if repo.advanceErr["王强"] == "" {
		t.Error("失败终态应带错误摘要")
	}
	// 33.33 > 10.00 超阈写告警。
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("超阈应写 1 条告警，实际 %d", len(alertRepo.alerts))
	}
	if alertRepo.alerts[0].FailedRatio != 33.33 {
		t.Errorf("告警占比 = %v, want 33.33", alertRepo.alerts[0].FailedRatio)
	}
	// 会话级失败比例分子查询入参：批次名单与窗口半开区间（子计划01 T4 口径）。
	if len(featureRepo.names) != 3 {
		t.Errorf("CountFailedByTokenNames 名单 = %v, want 3 人", featureRepo.names)
	}
	if featureRepo.start != batch.PeriodStartAt.Unix() || featureRepo.end != batch.PeriodEndAt.AddDate(0, 0, 1).Unix() {
		t.Errorf("失败会话统计窗口 = [%d, %d), want [%d, %d)",
			featureRepo.start, featureRepo.end,
			batch.PeriodStartAt.Unix(), batch.PeriodEndAt.AddDate(0, 0, 1).Unix())
	}
	if batch.SessionFailRatio == nil || *batch.SessionFailRatio != 25.00 {
		t.Errorf("SessionFailRatio = %v, want 25.00（1/4 失败档案）", batch.SessionFailRatio)
	}
}

func TestRunBatchAllFailed(t *testing.T) {
	repo := &fakeBatchRepo{}
	batch := runBatchFixture(repo)
	fetcher := &fakeSessionFetcher{pages: sessionsOf(map[string]int{"张敏": 1, "李芳": 1, "王强": 1})}
	attempts := []evalAttempt{{err: errFake}, {err: errFake}, {err: errFake}, {err: errFake}}
	pe := &fakePersonEvaluator{perName: map[string][]evalAttempt{
		"张敏": attempts, "李芳": attempts, "王强": attempts,
	}}
	alertRepo := &fakeAlertRepo{}
	o := evalOrch(repo, alertRepo, fetcher, &fakeFeatureRepo{}, pe)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if batch.Status != domain.BatchStatusFailed {
		t.Errorf("Status = %q, want failed（失败占比 100.00）", batch.Status)
	}
	if batch.FailedCount != 3 {
		t.Errorf("FailedCount = %d, want 3", batch.FailedCount)
	}
	if batch.CoveredSessionCount != 0 {
		t.Errorf("CoveredSessionCount = %d, want 0（无成功侧）", batch.CoveredSessionCount)
	}
	// 占比 100.00 超阈必写告警（§5.2.4 规则4）。
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("应写 1 条告警，实际 %d", len(alertRepo.alerts))
	}
	if alertRepo.alerts[0].FailedRatio != 100.00 || alertRepo.alerts[0].FailedCount != 3 {
		t.Errorf("告警 = (%.2f, %d), want (100.00, 3)", alertRepo.alerts[0].FailedRatio, alertRepo.alerts[0].FailedCount)
	}
	if alertRepo.alerts[0].BatchNo != batch.BatchNo {
		t.Errorf("告警批次号 = %q, want %q", alertRepo.alerts[0].BatchNo, batch.BatchNo)
	}
}

// TestRunBatchNoAlertUnderThreshold 10 人 1 失败：占比 10.00 未超阈（≤10.00），不写告警。
func TestRunBatchNoAlertUnderThreshold(t *testing.T) {
	repo := &fakeBatchRepo{}
	names := []string{"甲", "乙", "丙", "丁", "戊", "己", "庚", "辛", "壬", "癸"}
	namesJSON, _ := json.Marshal(names)
	for _, n := range names {
		repo.persons = append(repo.persons, domain.AssessmentBatchPerson{BatchID: 1, TokenName: n, Status: domain.PersonStatusPending})
	}
	batch := &domain.AssessmentBatch{
		ID: 1, BatchNo: "B202609070800001", TriggerType: domain.BatchTriggerScheduled,
		TargetMode: domain.BatchTargetSpecified, TotalCount: 10, Status: domain.BatchStatusRunning,
		TargetNamesJSON: string(namesJSON),
		PeriodStartAt:   time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		PeriodEndAt:     time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
		TriggeredAt:     time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
	}
	repo.stored = batch
	fetcher := &fakeSessionFetcher{pages: [][]conversationlog.SessionSummary{nil}} // 全员无会话
	pe := &fakePersonEvaluator{perName: map[string][]evalAttempt{
		"甲": {{err: errFake}, {err: errFake}, {err: errFake}, {err: errFake}},
	}}
	alertRepo := &fakeAlertRepo{}
	featureRepo := &fakeFeatureRepo{}
	o := evalOrch(repo, alertRepo, fetcher, featureRepo, pe)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}
	if batch.FailedCount != 1 || batch.EvaluatedCount != 10 {
		t.Fatalf("计数 = (%d/%d), want (1/10)", batch.FailedCount, batch.EvaluatedCount)
	}
	if batch.Status != domain.BatchStatusSuccess {
		t.Errorf("Status = %q, want success（10.00 ≤ 阈值）", batch.Status)
	}
	if len(alertRepo.alerts) != 0 {
		t.Errorf("占比 10.00 未超阈不应写告警，实际 %d 条", len(alertRepo.alerts))
	}
	if featureRepo.calls != 0 {
		t.Errorf("全员无会话分母 0 不应查询失败会话计数，实际 %d 次", featureRepo.calls)
	}
	if batch.SessionFailRatio == nil || *batch.SessionFailRatio != 0.00 {
		t.Errorf("SessionFailRatio = %v, want 0.00（分母 0 口径）", batch.SessionFailRatio)
	}
}

// TestRunBatchAdvanceFailLeavesRunning 回写失败行（§5.2.5）：1 人恒错不阻塞其余人员，
// RunBatch 返回 nil，FinalizeBatch 不调，批次留 running 按停滞处置。
func TestRunBatchAdvanceFailLeavesRunning(t *testing.T) {
	repo := &fakeBatchRepo{advanceErrs: map[string]error{"李芳": errFake}}
	batch := runBatchFixture(repo)
	fetcher := &fakeSessionFetcher{pages: sessionsOf(map[string]int{"张敏": 1, "李芳": 1, "王强": 1})}
	pe := &fakePersonEvaluator{}
	alertRepo := &fakeAlertRepo{}
	o := evalOrch(repo, alertRepo, fetcher, &fakeFeatureRepo{}, pe)

	if err := o.RunBatch(context.Background(), batch.ID); err != nil {
		t.Fatalf("回写失败不应上抛（返回 nil），得到 %v", err)
	}
	if len(repo.finalizeCalls) != 0 {
		t.Errorf("FinalizeBatch 不应被调用（批次留 running 停滞处置），实际 %d 次", len(repo.finalizeCalls))
	}
	if batch.Status != domain.BatchStatusRunning {
		t.Errorf("Status = %q, want running", batch.Status)
	}
	// 错误者外其余人员照常推进（§5.2.5：不阻塞其余人员）。
	if batch.EvaluatedCount != 2 {
		t.Errorf("EvaluatedCount = %d, want 2（李芳回写失败不计）", batch.EvaluatedCount)
	}
	if repo.persons[0].Status != domain.PersonStatusSuccess || repo.persons[2].Status != domain.PersonStatusSuccess {
		t.Error("张敏、王强应照常推进 success 终态")
	}
	if repo.persons[1].Status != domain.PersonStatusPending {
		t.Errorf("李芳回写失败应仍 pending，实际 %q", repo.persons[1].Status)
	}
	if len(alertRepo.alerts) != 0 {
		t.Error("未终态批次不应写告警")
	}
}
