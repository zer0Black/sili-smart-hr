package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeOpLogRepo 记录 InsertBatch 收到的批次，供 Recorder 测试断言。
// blockInsert 非 nil 时落库阻塞直至该通道关闭（Close 超时分支用）。
type fakeOpLogRepo struct {
	mu          sync.Mutex
	lastBatch   []domain.OperationLog
	insertErr   error
	blockInsert chan struct{}
}

func (f *fakeOpLogRepo) InsertBatch(_ context.Context, logs []domain.OperationLog) error {
	if f.blockInsert != nil {
		<-f.blockInsert
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.lastBatch = append(f.lastBatch[:0], logs...)
	return nil
}

func (f *fakeOpLogRepo) Insert(_ context.Context, _ *domain.OperationLog) error { return nil }

func (f *fakeOpLogRepo) ListPage(_ context.Context, _ repository.OperationLogFilter, _, _ int) ([]domain.OperationLog, int64, error) {
	return nil, 0, nil
}

func (f *fakeOpLogRepo) ListByFilter(_ context.Context, _ repository.OperationLogFilter, _ int) ([]domain.OperationLog, error) {
	return nil, nil
}

func (f *fakeOpLogRepo) DeleteBefore(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

var _ repository.OperationLogRepository = (*fakeOpLogRepo)(nil)

func (f *fakeOpLogRepo) batch() []domain.OperationLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.OperationLog(nil), f.lastBatch...)
}

// fakeOpAccounts 驱动 FindByIDUnscoped 三态：命中（含软删行）/记录不存在/其他错误。
type fakeOpAccounts struct {
	mu       sync.Mutex
	accounts map[int64]*domain.Account
	err      error
	lookups  []int64
}

func (f *fakeOpAccounts) FindByIDUnscoped(_ context.Context, id int64) (*domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, id)
	if f.err != nil {
		return nil, f.err
	}
	if acc, ok := f.accounts[id]; ok {
		return acc, nil
	}
	return nil, gorm.ErrRecordNotFound
}

// FindByIDUnscoped 未命中统一 ErrRecordNotFound，与真实 GORM 实现口径一致。
func (f *fakeOpAccounts) lookupsSnapshot() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.lookups...)
}

// 其余 AccountRepository 方法测试不触达，空实现满足接口。
func (f *fakeOpAccounts) FindByUsername(context.Context, string) (*domain.Account, error) { return nil, nil }
func (f *fakeOpAccounts) FindByID(context.Context, int64) (*domain.Account, error)       { return nil, nil }
func (f *fakeOpAccounts) UpdateLastLoginAt(context.Context, int64, time.Time) error      { return nil }
func (f *fakeOpAccounts) ListAccounts(context.Context, string, int, int) ([]domain.Account, int64, error) {
	return nil, 0, nil
}
func (f *fakeOpAccounts) Create(context.Context, *domain.Account) error                 { return nil }
func (f *fakeOpAccounts) Update(context.Context, *domain.Account) error                 { return nil }
func (f *fakeOpAccounts) Delete(context.Context, int64) error                            { return nil }
func (f *fakeOpAccounts) UpdateEnabled(context.Context, int64, bool) error               { return nil }
func (f *fakeOpAccounts) DemoteEnabledIfNotLast(context.Context, int64) (int64, error)   { return 0, nil }
func (f *fakeOpAccounts) DeleteIfNotLastEnabled(context.Context, int64) (int64, error)   { return 0, nil }

var _ repository.AccountRepository = (*fakeOpAccounts)(nil)

// flush 等待通道排空并关停 Recorder，Close 即 flush 语义（03 §4.1 步骤4）。
func flush(t *testing.T, r *service.OperationLogRecorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() err=%v", err)
	}
}

// TestRecorderAsyncFlush 核心断言：3 条 Record 经 Close flush 落库，FallbackName
//（系统任务「系统」）路径 Operator 直取，Changes 序列化为合法 JSON 数组。
func TestRecorderAsyncFlush(t *testing.T) {
	repo := &fakeOpLogRepo{}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{}}
	r := service.NewOperationLogRecorder(repo, accounts)
	r.Start()
	r.Start() // 幂等：重复 Start 只起一个消费 goroutine

	r.Record(service.PendingLog{
		FallbackName: domain.OpOperatorSystem,
		Module:       domain.OpModuleSystemJob,
		Target:       "批次 B1",
		Summary:      "批次创建完成",
		Result:       domain.OpResultSuccess,
		Detail:       "对象全员",
		RequestPath:  "POST /api/assessment/batches",
		Changes: []domain.ChangeItem{
			{Field: "权重", Before: "30", After: "50"},
		},
	})
	r.Record(service.PendingLog{
		FallbackName: domain.OpOperatorSystem,
		Module:       domain.OpModuleSystemJob,
		Summary:      "区间执行完成",
		Result:       domain.OpResultSuccess,
	})
	r.Record(service.PendingLog{
		FallbackName: domain.OpOperatorSystem,
		Module:       domain.OpModuleSystemJob,
		Summary:      "建议生成完成",
		Result:       domain.OpResultSuccess,
	})

	flush(t, r)

	batch := repo.batch()
	if len(batch) != 3 {
		t.Fatalf("lastBatch len=%d, want 3", len(batch))
	}
	if batch[0].Operator != "系统" {
		t.Errorf("batch[0].Operator=%q, want %q", batch[0].Operator, "系统")
	}
	var items []domain.ChangeItem
	if err := json.Unmarshal([]byte(batch[0].ChangesJSON), &items); err != nil {
		t.Fatalf("ChangesJSON 不是合法 JSON: %v (raw=%q)", err, batch[0].ChangesJSON)
	}
	if len(items) != 1 || items[0].Field != "权重" || items[0].Before != "30" || items[0].After != "50" {
		t.Errorf("ChangesJSON 内容不匹配: %+v", items)
	}
	if batch[1].ChangesJSON != "" {
		t.Errorf("Changes nil 应落空串, got %q", batch[1].ChangesJSON)
	}
	if batch[0].AccountID != 0 || batch[0].Module != domain.OpModuleSystemJob || batch[0].Result != domain.OpResultSuccess {
		t.Errorf("batch[0] 字段不匹配: %+v", batch[0])
	}
}

// TestRecorderChannelFull 核心断言：buffer 容量缩 2 后投 5 条，进程不 panic，
// Close 后落库条数 ≤ 2（满即丢最旧，specs §5.1.5 第三行）。先投满再 Start，
// 排除消费 goroutine 并发排空对丢弃路径的稀释，使断言确定。
func TestRecorderChannelFull(t *testing.T) {
	repo := &fakeOpLogRepo{}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{}}
	r := service.NewOperationLogRecorderWithBuffer(repo, accounts, 2, 5*time.Second)

	for i := 0; i < 5; i++ {
		r.Record(service.PendingLog{
			FallbackName: domain.OpOperatorSystem,
			Module:       domain.OpModuleSystemJob,
			Summary:      "记录",
			Result:       domain.OpResultSuccess,
		})
	}
	r.Start()

	flush(t, r)

	if n := len(repo.batch()); n > 2 {
		t.Fatalf("落库条数=%d, want ≤ 2（通道满应丢最旧）", n)
	}
}

// TestRecorderOperatorFallback 核心断言：AccountID 命中软删行仍取该行姓名
//（FindByIDUnscoped 口径，03 §1.4）；查无此号才回退 FallbackUsername。
func TestRecorderOperatorFallback(t *testing.T) {
	softDeletedID := int64(701)
	missingID := int64(999)
	repo := &fakeOpLogRepo{}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{
		softDeletedID: {ID: softDeletedID, Name: "已删账号姓名"},
	}}
	r := service.NewOperationLogRecorder(repo, accounts)
	r.Start()

	r.Record(service.PendingLog{
		AccountID:        softDeletedID,
		FallbackUsername: "fallback",
		Module:           domain.OpModuleAccount,
		Summary:          "更新账号",
		Result:           domain.OpResultSuccess,
	})
	r.Record(service.PendingLog{
		AccountID:        missingID,
		FallbackUsername: "admin",
		Module:           domain.OpModuleAccount,
		Summary:          "更新账号",
		Result:           domain.OpResultSuccess,
	})

	flush(t, r)

	batch := repo.batch()
	if len(batch) != 2 {
		t.Fatalf("lastBatch len=%d, want 2", len(batch))
	}
	if batch[0].Operator != "已删账号姓名" {
		t.Errorf("软删行 Operator=%q, want %q", batch[0].Operator, "已删账号姓名")
	}
	if batch[1].Operator != "admin" {
		t.Errorf("查无此号 Operator=%q, want FallbackUsername %q", batch[1].Operator, "admin")
	}
	lookups := accounts.lookupsSnapshot()
	if len(lookups) != 2 || lookups[0] != softDeletedID || lookups[1] != missingID {
		t.Errorf("FindByIDUnscoped 查询序列不匹配: %v", lookups)
	}
}

// TestSinkFromContext 核心断言：注入后取回同一指针，Snapshot 反映 Set 写入；
// 裸 ctx 取回 nil。
func TestSinkFromContext(t *testing.T) {
	if got := service.SinkFromContext(context.Background()); got != nil {
		t.Errorf("裸 ctx SinkFromContext=%v, want nil", got)
	}
	s := &service.OpSink{}
	ctx := service.WithSink(context.Background(), s)
	if got := service.SinkFromContext(ctx); got != s {
		t.Errorf("SinkFromContext 未取回同一指针: %p want %p", got, s)
	}
	s.SetModule("dimension")
	if m := service.SinkFromContext(ctx).Snapshot().Module; m != "dimension" {
		t.Errorf("Snapshot().Module=%q, want %q", m, "dimension")
	}
}

// TestSinkSnapshotFields 补充边界：OpSink 各 Set 写入与 Snapshot 读出一致，
// 空 sink 快照为零值（BR3 单请求生命周期内独立实例）。
func TestSinkSnapshotFields(t *testing.T) {
	s := &service.OpSink{}
	if p := s.Snapshot(); p.Module != "" || p.Target != "" || p.Summary != "" || p.Detail != "" || p.Changes != nil {
		t.Errorf("空 sink 快照应零值: %+v", p)
	}
	changes := []domain.ChangeItem{{Field: "名称", Before: "a", After: "b"}}
	s.SetModule(domain.OpModuleDimension)
	s.SetTarget("维度 D1")
	s.SetSummary("更新维度")
	s.SetDetail("文本详情")
	s.SetChanges(changes)
	p := s.Snapshot()
	if p.Module != domain.OpModuleDimension || p.Target != "维度 D1" || p.Summary != "更新维度" || p.Detail != "文本详情" {
		t.Errorf("Snapshot 字段不匹配: %+v", p)
	}
	if len(p.Changes) != 1 || p.Changes[0] != changes[0] {
		t.Errorf("Snapshot Changes 不匹配: %+v", p.Changes)
	}
}

// TestRecorderInsertFailureDropsBatch 补充异常：落库失败仅丢弃批次不 panic，
// Close 仍正常返回（specs §5.1.5 第一行、§5.1.4 规则2）。
func TestRecorderInsertFailureDropsBatch(t *testing.T) {
	repo := &fakeOpLogRepo{insertErr: errors.New("db down")}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{}}
	r := service.NewOperationLogRecorder(repo, accounts)
	r.Start()
	r.Record(service.PendingLog{FallbackName: "系统", Summary: "s", Result: domain.OpResultSuccess})
	flush(t, r)
	if n := len(repo.batch()); n != 0 {
		t.Errorf("失败批次不应留存, got %d", n)
	}
}

// TestRecorderCloseTimeout 补充异常：消费侧滞留（落库阻塞）排空超 ctx 期限时
// Close 返回 DeadlineExceeded。解除阻塞后再次 Close 等到正常收尾返回 nil。
func TestRecorderCloseTimeout(t *testing.T) {
	unblock := make(chan struct{})
	repo := &fakeOpLogRepo{blockInsert: unblock}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{}}
	r := service.NewOperationLogRecorder(repo, accounts)
	r.Start()
	r.Record(service.PendingLog{FallbackName: "系统", Summary: "s", Result: domain.OpResultSuccess})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close err=%v, want context.DeadlineExceeded", err)
	}

	close(unblock)
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("解除阻塞后 Close err=%v, want nil", err)
	}
}

// TestRecorderRecordWithoutStart 补充边界：未 Start 直接 Record 不 panic，
// 条目滞留通道，Close 无消费 goroutine 时 wg 即返（wait 零值不阻塞）。
func TestRecorderRecordWithoutStart(t *testing.T) {
	repo := &fakeOpLogRepo{}
	accounts := &fakeOpAccounts{accounts: map[int64]*domain.Account{}}
	r := service.NewOperationLogRecorder(repo, accounts)
	r.Record(service.PendingLog{FallbackName: "系统", Summary: "s", Result: domain.OpResultSuccess})
	flush(t, r)
	if n := len(repo.batch()); n != 0 {
		t.Errorf("未 Start 不应落库, got %d", n)
	}
}
