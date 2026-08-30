package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 轮询等待条件成立，替代 sleep 防 flasy，超时 fatal。
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}

// queued 返回 FIFO 等待队列当前长度，测试白盒观测用于同步。
func (g *gate) queued() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

// idleSlots 返回空闲槽位数 = cap - 已占用，测试白盒观测。
func (g *gate) idleSlots() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cap(g.slots) - len(g.slots)
}

// TestGateMaxConcurrency 并发 10 个 Acquire，同时在飞恒不超过 4 且上限被打满，全部成功。
func TestGateMaxConcurrency(t *testing.T) {
	g := newGate(4, 1024)
	const total = 10
	var wg sync.WaitGroup
	var inFlight, maxInFlight atomic.Int64
	start := make(chan struct{})
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := g.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			cur := inFlight.Add(1)
			for {
				m := maxInFlight.Load()
				if cur <= m || maxInFlight.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			inFlight.Add(-1)
			g.Release()
		}()
	}
	close(start)
	wg.Wait()
	if got := maxInFlight.Load(); got > 4 {
		t.Fatalf("max in-flight = %d, want <= 4", got)
	}
	if got := maxInFlight.Load(); got != 4 {
		t.Fatalf("max in-flight = %d, want exactly 4 (limit should be saturated)", got)
	}
	if got := inFlight.Load(); got != 0 {
		t.Fatalf("residual in-flight = %d, want 0", got)
	}
}

// TestGateQueueFull 槽 1 队列 2 时并发 5 个 Acquire，超出排队容量返回 ErrQueueFull。
func TestGateQueueFull(t *testing.T) {
	g := newGate(1, 2)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("initial Acquire: %v", err)
	}
	const callers = 5
	var wg sync.WaitGroup
	var fullCount atomic.Int64
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := g.Acquire(context.Background()); err == nil {
				g.Release()
				return
			} else if errors.Is(err, ErrQueueFull) {
				fullCount.Add(1)
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	waitFor(t, func() bool { return fullCount.Load() == 3 }, "3 callers should hit ErrQueueFull immediately")
	g.Release() // 放行队首，链式放行第二个排队者
	wg.Wait()
	if got := fullCount.Load(); got != 3 {
		t.Fatalf("ErrQueueFull count = %d, want 3 (>= 2)", got)
	}
}

// TestGateFIFO 槽 1 时 5 个等待者按到达顺序放行。
func TestGateFIFO(t *testing.T) {
	g := newGate(1, 8)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("initial Acquire: %v", err)
	}
	const n = 5
	released := make(chan int, n)
	for i := 0; i < n; i++ {
		go func(id int) {
			if err := g.Acquire(context.Background()); err != nil {
				t.Errorf("goroutine %d Acquire: %v", id, err)
				return
			}
			released <- id
			g.Release()
		}(i)
		// 白盒确认第 i 个已入队再启动下一个，到达顺序确定
		want := i + 1
		waitFor(t, func() bool { return g.queued() == want }, "waiter should enqueue in order")
	}
	g.Release()
	order := make([]int, 0, n)
	for i := 0; i < n; i++ {
		select {
		case id := <-released:
			order = append(order, id)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout at position %d, order so far %v", i, order)
		}
	}
	for i, id := range order {
		if id != i {
			t.Fatalf("FIFO violated: position %d released goroutine %d, order %v", i, id, order)
		}
	}
}

// TestGateCtxCancel 排队者 ctx 取消立即返回 context.Canceled，且名额无泄漏。
func TestGateCtxCancel(t *testing.T) {
	g := newGate(1, 8)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("initial Acquire: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.Acquire(ctx) }()
	waitFor(t, func() bool { return g.queued() == 1 }, "waiter should be queued")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Acquire = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire blocked after ctx cancel")
	}
	waitFor(t, func() bool { return g.queued() == 0 }, "cancelled waiter should leave queue")
	// 守恒回归：取消者不得泄漏在飞名额
	g.Release()
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire after cancel-leak check: %v", err)
	}
	g.Release()
}

// TestGateSlotConservationUnderCancelReleaseRace 并发 Release 与 ctx 取消交错下槽位守恒：
// 每轮占满全部槽，等待者带超短 ctx 在被放行前后随机取消，同时并发 Release 归还，
// 轮末 idleSlots 必须恢复满额。修复前 Release 锁外 close(wait) 与 removeSelf 锁外
// 非阻塞 select 存在竞态：已转让的槽无人认领即永久泄漏。
func TestGateSlotConservationUnderCancelReleaseRace(t *testing.T) {
	const rounds = 50
	for r := 0; r < rounds; r++ {
		g := newGate(4, 1024)
		for i := 0; i < 4; i++ {
			if err := g.Acquire(context.Background()); err != nil {
				t.Fatalf("round %d fill slot %d: %v", r, i, err)
			}
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%3)*time.Millisecond)
				defer cancel()
				if err := g.Acquire(ctx); err == nil {
					g.Release()
				}
			}(i)
		}
		// 与等待者取消交错地归还全部槽
		for i := 0; i < 4; i++ {
			g.Release()
		}
		wg.Wait()
		waitFor(t, func() bool { return g.queued() == 0 }, "queue should drain after round")
		if got := g.idleSlots(); got != 4 {
			t.Fatalf("round %d: idle slots = %d, want 4 (slot leaked via cancel/release race)", r, got)
		}
	}
}

// TestGateQueuedDoesNotOccupyInflight 排队不占在飞额度：槽满时等待者在队，空闲令牌为 0。
func TestGateQueuedDoesNotOccupyInflight(t *testing.T) {
	g := newGate(2, 4)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := g.Acquire(context.Background()); err != nil {
			t.Errorf("waiter Acquire: %v", err)
		}
	}()
	waitFor(t, func() bool { return g.queued() == 1 }, "third caller should queue")
	if got := g.idleSlots(); got != 0 {
		t.Fatalf("idle slots = %d, want 0 (queued caller must not occupy inflight quota)", got)
	}
	g.Release()
	g.Release()
}

// TestGateNonPositiveParams 非法参数归一：max<1 视为 1，queue<0 视为 0（立即 ErrQueueFull）。
func TestGateNonPositiveParams(t *testing.T) {
	g := newGate(0, -1)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	err := g.Acquire(context.Background())
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("second Acquire = %v, want ErrQueueFull", err)
	}
	g.Release()
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	g.Release()
}

// TestGateDuplicateReleaseSafety 队列空时重复 Release 不 panic 且不凭空放行名额。
func TestGateDuplicateReleaseSafety(t *testing.T) {
	g := newGate(2, 4)
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.Release()
	g.Release()
	g.Release()
	if got := g.idleSlots(); got != 2 {
		t.Fatalf("idle slots = %d, want 2 (duplicate Release must not inflate quota)", got)
	}
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := g.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := g.Acquire(context.Background()); err != nil {
			t.Errorf("third Acquire: %v", err)
		}
	}()
	waitFor(t, func() bool { return g.queued() == 1 }, "third caller should queue, not pass")
	g.Release()
	g.Release()
	g.Release()
}
