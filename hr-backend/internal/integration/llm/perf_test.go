package llm

// 性能测试（specs §3.1 性能要求 / §5.3 性能测试）：
// 全部经 fake adapter / fake provider 驱动，不触碰网络，排除服务商往返与网络抖动干扰。
// 并发路径设计上无数据竞态：fake 与统计均用原子计数或 per-index 切片，无共享可变状态。

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// installAny 把任意 adapter 装进两个工厂变量，installProbe 的推广形式，
// 供 perf 专用的 request-aware fake（mix/delay）使用。
func installAny(t *testing.T, a adapter) {
	t.Helper()
	oldO, oldA := newOpenaiAdapterFn, newAnthropicAdapterFn
	newOpenaiAdapterFn = func(ModelConfig, time.Duration) adapter { return a }
	newAnthropicAdapterFn = func(ModelConfig, time.Duration) adapter { return a }
	t.Cleanup(func() { newOpenaiAdapterFn, newAnthropicAdapterFn = oldO, oldA })
}

// --- §3.1/§5.3 底座本地开销：适配+预检+排队 P95 ≤ 50ms（不含网络往返）---

func TestPerfLocalOverhead(t *testing.T) {
	fa := &fakeAdapter{} // fn=nil：立即返回空流，零网络零延迟
	installProbe(t, fa)
	c := New(Config{MaxConcurrency: 4, QueueSize: 1024}, &fakeProvider{mc: clientMC})

	const n = 100
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		s, err := c.StreamChat(context.Background(), userReq())
		elapsed := time.Since(start) // Start 到 StreamChat 返回，不含 Close
		if err != nil {
			t.Fatalf("第 %d 次 StreamChat 错误: %v", i+1, err)
		}
		s.Close() // 释放槽供下一轮复用
		durations = append(durations, elapsed)
	}
	if got := fa.calls.Load(); got != n {
		t.Errorf("adapter 调用 %d 次, want %d", got, n)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(95*n+99)/100-1] // n=100 → 第 95 小值（下标 94）
	t.Logf("本地开销 P95=%v max=%v（阈值 50ms，不含服务商网络往返）", p95, durations[n-1])
	if p95 > 50*time.Millisecond {
		t.Errorf("本地开销 P95 = %v, want ≤ 50ms（specs §3.1/§5.3）", p95)
	}
}

// --- §5.3 高并发稳定：1000 次混合 429/5xx/成功，无死锁、错误码分类正确 ---

// mixAdapter 按请求标记混合返回 429/500/成功。fakeAdapter.fn 只见全局调用序号
// 无法区分请求归属，故按请求内容分发，保证每类错误的断言确定性。
type mixAdapter struct{ calls atomic.Int32 }

const (
	perfRate   = "perf-rate"   // 恒 429 → 重试耗尽 → ErrRateLimited
	perfServer = "perf-server" // 恒 500 → 重试耗尽 → ErrRetryExhausted
)

func (a *mixAdapter) StreamChat(_ context.Context, _ ModelConfig, req ChatRequest) (Stream, error) {
	a.calls.Add(1)
	switch req.Messages[0].Content {
	case perfRate:
		return nil, &httpError{status: 429, message: "rate limit"}
	case perfServer:
		return nil, &httpError{status: 500, message: "internal error"}
	default:
		return &fakeStream{}, nil
	}
}

func TestPerfHighConcurrency(t *testing.T) {
	ma := &mixAdapter{}
	installAny(t, ma)
	// MaxRetries=1 + 1ms 级退避控制测试时长；QueueSize 缺省 1024 容纳 1000 并发排队。
	cfg := Config{MaxConcurrency: 4, MaxRetries: 1, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	c := New(cfg, &fakeProvider{mc: clientMC})

	const total = 1000
	errs := make([]error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := "perf-ok"
			switch i % 3 {
			case 0:
				content = perfRate
			case 1:
				content = perfServer
			}
			req := ChatRequest{Messages: []ChatMessage{{Role: "user", Content: content}}}
			s, err := c.StreamChat(context.Background(), req)
			if err != nil {
				errs[i] = err
				return
			}
			s.Close() // 成功路径必须 Close 释放槽，否则槽泄漏死锁
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done: // 正常收敛即无死锁（测试超时兜底）
	case <-time.After(60 * time.Second):
		t.Fatal("1000 并发混合调用 60s 未完成，疑似死锁")
	}

	var rateN, serverN, okN int
	for i, err := range errs {
		var want error
		switch i % 3 {
		case 0:
			want, rateN = ErrRateLimited, rateN+1
		case 1:
			want, serverN = ErrRetryExhausted, serverN+1
		default:
			okN++
		}
		if !errors.Is(err, want) {
			t.Errorf("第 %d 个调用 err = %v, want %v", i+1, err, want)
		}
	}
	if rateN != 334 || serverN != 333 || okN != 333 {
		t.Errorf("混合分类计数 rate=%d server=%d ok=%d, want 334/333/333", rateN, serverN, okN)
	}
	// 失败类各 2 次尝试（MaxRetries=1），成功类 1 次
	want := int32(334*2 + 333*2 + 333)
	if got := ma.calls.Load(); got != want {
		t.Errorf("adapter 调用 %d 次, want %d", got, want)
	}
	// 槽位全数归还：并发收敛后新调用立即可用（无泄漏）
	s, err := c.StreamChat(context.Background(), userReq())
	if err != nil {
		t.Fatalf("收敛后 StreamChat 错误: %v（疑似槽位泄漏）", err)
	}
	s.Close()
}

// --- §5.3 队列吞吐：MaxConcurrency=4 恒定延迟，在飞恒 4、吞吐稳定、排队等待记入统计 ---

// delayAdapter 恒定延迟 fake：持槽期间 sleep，复用 inflightTracker 统计在飞峰值。
type delayAdapter struct {
	delay   time.Duration
	tracker *inflightTracker
	calls   atomic.Int32
}

func (a *delayAdapter) StreamChat(_ context.Context, _ ModelConfig, _ ChatRequest) (Stream, error) {
	a.calls.Add(1)
	a.tracker.enter()
	defer a.tracker.exit()
	time.Sleep(a.delay) // 恒定延迟拉开在飞窗口
	return &fakeStream{}, nil
}

func TestPerfQueueThroughput(t *testing.T) {
	const (
		delay    = 10 * time.Millisecond
		batches  = 5
		perBatch = 20
	)
	tr := &inflightTracker{}
	da := &delayAdapter{delay: delay, tracker: tr}
	installAny(t, da)
	cfg := fastCfg() // 恒成功路径不触发重试，紧凑退避参数仅兜底
	cfg.MaxConcurrency = 4
	cfg.QueueSize = 128
	c := New(cfg, &fakeProvider{mc: clientMC})

	batchDur := make([]time.Duration, batches)
	var failN atomic.Int32
	for b := 0; b < batches; b++ {
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < perBatch; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := c.StreamChat(context.Background(), userReq())
				if err != nil {
					failN.Add(1)
					return
				}
				s.Close() // 释放槽放行排队者
			}()
		}
		wg.Wait()
		batchDur[b] = time.Since(start) // 含每调用的排队等待（自批次起点计）
	}

	if got := failN.Load(); got != 0 {
		t.Errorf("失败 %d 次, want 0", got)
	}
	if got := da.calls.Load(); got != batches*perBatch {
		t.Errorf("adapter 调用 %d 次, want %d", got, batches*perBatch)
	}
	// 在飞恒 4：≤4 为硬上限，==4 为满载观测（20 并发抢 4 槽、10ms 持槽窗口内必凑齐）
	if got := tr.max.Load(); got != 4 {
		t.Errorf("同时在飞峰值 = %d, want 4", got)
	}
	// 打包下界：20 次 × 10ms ÷ 4 槽 = 50ms 理论最短批次，验证在飞确为 4 且排队等待计入时长
	for b, d := range batchDur {
		if minDur := time.Duration(4.5 * float64(delay)); d < minDur {
			t.Errorf("批次 %d 时长 %v < %.1f×delay（在飞未打满或统计丢排队等待）", b+1, d, 4.5)
		}
	}
	// 吞吐稳定：无随时间退化（后续批次不显著慢于首批）
	for b, d := range batchDur[1:] {
		if limit := 2*batchDur[0] + 25*time.Millisecond; d > limit {
			t.Errorf("批次 %d 时长 %v 超过退化上限 %v（吞吐随时间退化）", b+2, d, limit)
		}
	}
	t.Logf("批次时长 %v，在飞峰值 %d", batchDur, tr.max.Load())
}
