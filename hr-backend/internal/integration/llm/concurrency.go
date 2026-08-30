package llm

import (
	"context"
	"sync"
)

// gate 是底座全局在飞限制 + FIFO 排队器（specs §2.4 能力4）。
// slots 容量 MaxConcurrency；queue 为 FIFO 等待队列，容量 QueueSize。
// 排队不占用在飞额度，放行后才计入（§2.4 注意事项）。
type gate struct {
	slots     chan struct{}
	queueSize int
	mu        sync.Mutex
	queue     []chan struct{} // 每个等待者一个 closed-on-admit 信号，append 入尾、队首放行，天然 FIFO
}

func newGate(maxConcurrency, queueSize int) *gate {
	if maxConcurrency < 1 {
		maxConcurrency = 1 // specs：在飞硬上限不可禁用
	}
	if queueSize < 0 {
		queueSize = 0
	}
	return &gate{
		slots:     make(chan struct{}, maxConcurrency),
		queueSize: queueSize,
	}
}

// Acquire 排队获取在飞槽位：有槽立即占用；无槽进入 FIFO 队列；
// 队列满返回 ErrQueueFull；ctx 取消返回 ctx.Err() 而非无限等待。
func (g *gate) Acquire(ctx context.Context) error {
	// 快路径：非阻塞取槽
	select {
	case g.slots <- struct{}{}:
		return nil
	default:
	}
	// 慢路径：入队前判满（排队长度 >= QueueSize 即拒绝）
	g.mu.Lock()
	if len(g.queue) >= g.queueSize {
		g.mu.Unlock()
		return ErrQueueFull
	}
	wait := make(chan struct{})
	g.queue = append(g.queue, wait)
	// 锁内二次取槽：覆盖快路径失败到加锁之间 Release 归还槽的窗口，
	// 取到则立即离队，避免空槽与等待者并存的丢失唤醒
	select {
	case g.slots <- struct{}{}:
		g.queue = g.queue[:len(g.queue)-1]
		g.mu.Unlock()
		return nil
	default:
	}
	g.mu.Unlock()

	select {
	case <-wait: // 被 Release 指名放行，槽已转让
		return nil
	case <-ctx.Done():
		g.removeSelf(wait)
		return ctx.Err()
	}
}

// removeSelf 把已取消的等待者移出队列并结算槽位：
// 与 Release 的放行存在竞态窗口，若此刻恰好已被放行（已出队且 wait 已 close），
// 转让来的槽属于自己，经 Release 归还防泄漏。
func (g *gate) removeSelf(wait chan struct{}) {
	g.mu.Lock()
	for i, w := range g.queue {
		if w == wait {
			g.queue = append(g.queue[:i], g.queue[i+1:]...)
			g.mu.Unlock()
			return
		}
	}
	g.mu.Unlock()
	select {
	case <-wait:
		g.Release()
	default:
	}
}

// QueueDepth 返回当前排队等待数，供日志与监控（specs §6.1 queue_depth）。
func (g *gate) QueueDepth() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

// Release 释放在飞槽位：优先放行队首等待者（槽直接转让，池内令牌数不变），
// 队列空则把槽归还池。pop、close 与归还均在锁内完成：removeSelf 拿锁时
// 要么等待者仍在队可移除，要么 pop+close 已完成、锁外 select 必成功认领，
// 消除「已转让无人认领」与「空槽与等待者并存」两类竞态。
// 调用方保证单次释放，select 兜底防重复放行。
func (g *gate) Release() {
	g.mu.Lock()
	if len(g.queue) > 0 {
		wait := g.queue[0]
		g.queue = g.queue[1:]
		close(wait) // 锁内 close：与 removeSelf 的出队/认领判定互斥
		g.mu.Unlock()
		return
	}
	select {
	case <-g.slots: // 槽归还池；池空说明是多余 Release，静默丢弃
	default:
	}
	g.mu.Unlock()
}
