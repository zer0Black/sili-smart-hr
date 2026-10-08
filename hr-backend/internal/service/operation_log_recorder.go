// OperationLogRecorder 异步落库通道与请求级埋点 Sink（specs P4_LOG_001 §5.1.2 步骤5、
// 03 §4.1）：Record 非阻塞投递，后台单 goroutine 攒批落库；sink 经 context 注入，
// 仅单请求生命周期内有效（specs §5.1.4 规则3）。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// 计算常量（03 §4.4，随源码发版）。
const (
	recordBufferSize  = 1024  // 异步通道容量，超限丢最旧保业务（specs §5.1.5 第三行）
	recordFlushSize   = 100   // 攒批条数上限
	recordFlushPeriod = 500 * time.Millisecond // 攒批时间窗
	retentionDays     = 180   // 日志保留窗口（T5 清理任务消费）
)

// PendingLog 是一次待落库操作的事件载荷：中间件/埋点侧只组装业务字段，
// operator 姓名由消费侧落库前解析（03 §1.4 现查口径），业务侧只投递不等待。
type PendingLog struct {
	AccountID          int64                 // JWT 路径账号主键；登录/系统任务 0
	FallbackName       string                // 回退名：登录=请求 username 原值；worker 系统任务=「系统」常量；JWT 路径空串走 AccountID 现查
	FallbackUsername   string                // JWT 路径的 username，现查失败兜底；登录/worker 路径空串
	Module, Target     string
	Summary, Result    string
	Detail, RequestPath string
	Changes            []domain.ChangeItem
}

// PendingLogRecorder 是 middleware/engine/worker 三处共用的窄投递接口。
type PendingLogRecorder interface {
	Record(entry PendingLog)
}

// sinkKey 未导出 struct{} 键，context 惯例防碰撞（specs §5.1.4 规则3 单请求生命周期）。
type sinkKey struct{}

// WithSink 把埋点 sink 注入 ctx（中间件与测试共用）。
func WithSink(ctx context.Context, s *OpSink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

// SinkFromContext 取回注入的 sink，无 sink 返回 nil。
func SinkFromContext(ctx context.Context) *OpSink {
	if v, ok := ctx.Value(sinkKey{}).(*OpSink); ok {
		return v
	}
	return nil
}

// OpSink 是请求级埋点收集器：Service 在写操作成功路径写入，中间件响应后
// Snapshot 组装日志记录。字段仅供 Set*/Snapshot 访问，外部不直接依赖。
type OpSink struct {
	module  string
	target  string
	summary string
	detail  string
	changes []domain.ChangeItem
}

func (s *OpSink) SetModule(v string)   { s.module = v }
func (s *OpSink) SetTarget(v string)   { s.target = v }
func (s *OpSink) SetSummary(v string)  { s.summary = v }
func (s *OpSink) SetDetail(v string)   { s.detail = v }
func (s *OpSink) SetChanges(v []domain.ChangeItem) { s.changes = v }

// Snapshot 读出已写埋点（中间件组装与测试断言用），返回值是字段浅拷贝。
func (s *OpSink) Snapshot() PendingLog {
	return PendingLog{
		Module:  s.module,
		Target:  s.target,
		Summary: s.summary,
		Detail:  s.detail,
		Changes: s.changes,
	}
}

// OperationLogRecorder 缓冲通道 + 仓储 + 账号名解析。Start 后开始消费，
// Close 关通道并等排空（flush），进程优雅关闭时按 ctx 超时返回 err（03 §4.1 步骤4）。
type OperationLogRecorder struct {
	repo     repository.OperationLogRepository
	accounts repository.AccountRepository
	buffer   chan PendingLog
	tick     time.Duration // 攒批时间窗，测试可缩短

	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
}

// NewOperationLogRecorder 注入日志仓储与账号仓储（operator 现查用）。
func NewOperationLogRecorder(repo repository.OperationLogRepository, accounts repository.AccountRepository) *OperationLogRecorder {
	return NewOperationLogRecorderWithBuffer(repo, accounts, recordBufferSize, recordFlushPeriod)
}

// NewOperationLogRecorderWithBuffer 测试变体：buffer 容量与攒批时间窗可注入
//（TestRecorderChannelFull 缩容量验丢弃）。生产路径走 NewOperationLogRecorder。
func NewOperationLogRecorderWithBuffer(repo repository.OperationLogRepository, accounts repository.AccountRepository, bufSize int, tick time.Duration) *OperationLogRecorder {
	return &OperationLogRecorder{
		repo:     repo,
		accounts: accounts,
		buffer:   make(chan PendingLog, bufSize),
		tick:     tick,
	}
}

// Start 起后台消费 goroutine，幂等，重复调忽略。
func (r *OperationLogRecorder) Start() {
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go r.consume()
	})
}

// Close 关闭通道并等消费排空。ctx 超时返回 err（剩余记录交由进程退出丢弃，
// specs §5.1.4 规则2 落库失败不影响业务的旁路口径）。
func (r *OperationLogRecorder) Close(ctx context.Context) error {
	r.stopOnce.Do(func() {
		close(r.buffer)
	})
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Record 非阻塞投递。通道满丢弃最旧一条再投（specs §5.1.5 第三行，保业务内存安全）。
func (r *OperationLogRecorder) Record(entry PendingLog) {
	select {
	case r.buffer <- entry:
	default:
		select {
		case old := <-r.buffer:
			slog.Warn("operation log channel full, drop oldest entry",
				"summary", old.Summary, "module", old.Module)
		default:
		}
		select {
		case r.buffer <- entry:
		default:
			// 消费侧刚取走并发窗口内投递失败则放弃本条，保业务不阻塞。
		}
	}
}

// CleanExpired 删除 created_at 早于 now-180d 的日志（specs §5.5.2）：循环
// DeleteBefore 至 0 行累计条数（单批 1000 避免长事务锁表），err 透传交
// Asynq 重试（重复删除幂等，§5.5.4 规则2）。
func (r *OperationLogRecorder) CleanExpired(ctx context.Context, now time.Time) (int64, error) {
	before := now.Add(-retentionDays * 24 * time.Hour)
	var total int64
	for {
		n, err := r.repo.DeleteBefore(ctx, before)
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

// consume 单 goroutine 消费循环：攒批满 100 条或时间窗到即 flush；
// 通道关闭且排空后退出（Close 的 flush 语义）。
func (r *OperationLogRecorder) consume() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	batch := make([]domain.OperationLog, 0, recordFlushSize)
	for {
		select {
		case entry, ok := <-r.buffer:
			if !ok {
				r.flush(batch)
				return
			}
			batch = append(batch, r.buildLog(entry))
			if len(batch) >= recordFlushSize {
				batch = r.flush(batch)
			}
		case <-ticker.C:
			batch = r.flush(batch)
		}
	}
}

// flush 落库当前批并返回空批（复用容量）。失败 ERROR 留痕后丢弃，不重试
// （specs §5.1.5 第一行、§5.1.4 规则2）。
func (r *OperationLogRecorder) flush(batch []domain.OperationLog) []domain.OperationLog {
	if len(batch) == 0 {
		return batch
	}
	if err := r.repo.InsertBatch(context.Background(), batch); err != nil {
		slog.Error("operation log batch insert failed, drop batch",
			"count", len(batch), "err", err)
	}
	return batch[:0]
}

// buildLog 组装落库行：operator 解析按 03 §1.4，FallbackName 非空直接用
//（登录/系统任务路径），否则 AccountID 现查（软删行照取），查不到回退
// FallbackUsername；Changes 非 nil 序列化进 ChangesJSON，nil 落空串。
func (r *OperationLogRecorder) buildLog(entry PendingLog) domain.OperationLog {
	changesJSON := ""
	if entry.Changes != nil {
		data, err := json.Marshal(entry.Changes)
		if err != nil {
			// 序列化失败按无对比数据处理，不留脏 JSON 进列。
			slog.Error("operation log changes marshal failed", "err", err)
		} else {
			changesJSON = string(data)
		}
	}
	return domain.OperationLog{
		AccountID:   entry.AccountID,
		Operator:    r.resolveOperator(entry),
		Module:      entry.Module,
		Target:      entry.Target,
		Summary:     entry.Summary,
		Result:      entry.Result,
		ChangesJSON: changesJSON,
		Detail:      entry.Detail,
		RequestPath: entry.RequestPath,
	}
}

// resolveOperator 操作人姓名解析（03 §1.4）：FallbackName 优先（登录 username 原值/
// 系统任务「系统」），AccountID 经 FindByIDUnscoped 现查（软删行照取姓名），
// 查不到或 err 回退 FallbackUsername。
func (r *OperationLogRecorder) resolveOperator(entry PendingLog) string {
	if entry.FallbackName != "" {
		return entry.FallbackName
	}
	if entry.AccountID != 0 {
		acc, err := r.accounts.FindByIDUnscoped(context.Background(), entry.AccountID)
		if err == nil && acc != nil {
			return acc.Name
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Error("operation log operator lookup failed", "account_id", entry.AccountID, "err", err)
		}
	}
	return entry.FallbackUsername
}
