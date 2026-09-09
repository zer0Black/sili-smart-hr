// Package service_test 对 SystemService 做黑盒单元测试（fake repo + 可控探针）。
//
// 覆盖 GetSummary 与 HealthCheck 两条主链路及错误分支，逐用例红绿推进。
// 复用 account_test.go 同包既有的 wantCode 辅助与 setup_test.go 的 constProbe。
package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sili-smart-hr/backend/internal/model"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeSystemInitRepo 是 repository.SystemInitializationRepository 的测试假实现，
// 用 exists/err 驱动 GetSummary 的 Exists 分支，编译期断言保证接口契约。
type fakeSystemInitRepo struct {
	exists bool
	err    error
}

func (f *fakeSystemInitRepo) Exists(_ context.Context) (bool, error) {
	return f.exists, f.err
}

var _ repository.SystemInitializationRepository = (*fakeSystemInitRepo)(nil)

// stubProbe 是 service.DependencyProbe 的测试假实现，返回预设 llm/integ 状态值，
// 驱动 HealthCheck 的自定义 probe 分支（区别于 NoopDependencyProbe 的固定 not_configured）。
type stubProbe struct {
	llm   string
	integ string
}

func (s stubProbe) ProbeLLM(_ context.Context) string         { return s.llm }
func (s stubProbe) ProbeIntegration(_ context.Context) string { return s.integ }

var _ service.DependencyProbe = (*stubProbe)(nil)

// withStartedAt 临时覆盖 model.StartedAt 并在测试结束后还原，避免污染同包其他用例。
// model.StartedAt 是导出全局变量，package service_test 可直接写入。
func withStartedAt(t *testing.T, ts time.Time) {
	t.Helper()
	orig := model.StartedAt
	model.StartedAt = ts
	t.Cleanup(func() { model.StartedAt = orig })
}

// TestGetSummary_NotInitialized：fake exists=false → Initialized=false、DBType=当前库类型默认值、
// Version="v0.1.0"（model.Version 默认值）、StartedAt 取 model.StartedAt（specs §5.3.2 / BR1）。
func TestGetSummary_NotInitialized(t *testing.T) {
	startedAt := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	withStartedAt(t, startedAt)
	repo := &fakeSystemInitRepo{exists: false}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), service.NoopDependencyProbe{})

	summary, err := svc.GetSummary(context.Background())
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary.Initialized {
		t.Fatal("Initialized want false when exists=false")
	}
	if summary.DBType != string(model.Current()) {
		t.Fatalf("DBType want %q, got %q", string(model.Current()), summary.DBType)
	}
	if summary.Version != "v0.1.0" {
		t.Fatalf("Version want v0.1.0, got %q", summary.Version)
	}
	if !summary.StartedAt.Equal(startedAt) {
		t.Fatalf("StartedAt want %v, got %v", startedAt, summary.StartedAt)
	}
}

// TestGetSummary_Initialized：fake exists=true → Initialized=true（specs §5.3.2 / BR1）。
func TestGetSummary_Initialized(t *testing.T) {
	withStartedAt(t, time.Now())
	repo := &fakeSystemInitRepo{exists: true}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), service.NoopDependencyProbe{})

	summary, err := svc.GetSummary(context.Background())
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if !summary.Initialized {
		t.Fatal("Initialized want true when exists=true")
	}
}

// TestGetSummary_ExistsError：fake err=errors.New("db down") → Initialized=false（出错内化）、nil error 返回（specs §5.3.5）。
// 验证查询失败不返错、不阻断响应，initialized 降级为 false。
func TestGetSummary_ExistsError(t *testing.T) {
	withStartedAt(t, time.Now())
	repo := &fakeSystemInitRepo{err: errors.New("db down")}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), service.NoopDependencyProbe{})

	summary, err := svc.GetSummary(context.Background())
	if err != nil {
		t.Fatalf("GetSummary should not return error on repo failure, got %v", err)
	}
	if summary.Initialized {
		t.Fatal("Initialized want false when Exists returns error")
	}
	if summary.Version != "v0.1.0" {
		t.Fatalf("Version still populated on repo error, got %q", summary.Version)
	}
}

// TestHealthCheck_AllConnected_NoopProbe：db/redis 探针返 true + NoopDependencyProbe →
// Database/Redis="connected"、LLM/Integration="not_configured"（specs §5.4.2、§4.2.2 / BR20 / BR19）。
func TestHealthCheck_AllConnected_NoopProbe(t *testing.T) {
	repo := &fakeSystemInitRepo{}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), service.NoopDependencyProbe{})

	res, err := svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if res.Database != service.StatusConnected {
		t.Fatalf("Database want %q, got %q", service.StatusConnected, res.Database)
	}
	if res.Redis != service.StatusConnected {
		t.Fatalf("Redis want %q, got %q", service.StatusConnected, res.Redis)
	}
	if res.LLM != service.StatusNotConfigured {
		t.Fatalf("LLM want %q, got %q", service.StatusNotConfigured, res.LLM)
	}
	if res.Integration != service.StatusNotConfigured {
		t.Fatalf("Integration want %q, got %q", service.StatusNotConfigured, res.Integration)
	}
}

// TestHealthCheck_DBDown：dbProbe 返 false、redisProbe 返 true → Database="disconnected"、
// Redis="connected"（单项失败不影响其他项，specs §5.4.5 / BR21）。
func TestHealthCheck_DBDown(t *testing.T) {
	repo := &fakeSystemInitRepo{}
	svc := service.NewSystemService(repo, constProbe(false), constProbe(true), service.NoopDependencyProbe{})

	res, err := svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if res.Database != service.StatusDisconnected {
		t.Fatalf("Database want %q, got %q", service.StatusDisconnected, res.Database)
	}
	if res.Redis != service.StatusConnected {
		t.Fatalf("Redis want %q (unaffected by db failure), got %q", service.StatusConnected, res.Redis)
	}
	if res.LLM != service.StatusNotConfigured {
		t.Fatalf("LLM want %q (unaffected by db failure), got %q", service.StatusNotConfigured, res.LLM)
	}
}

// TestHealthCheck_CustomProbe：注入 stubProbe{llm:"reachable", integ:"unreachable"} + db/redis true →
// LLM="reachable"、Integration="unreachable"，验证 DependencyProbe 注入点与单项独立性（specs §5.4.2 / BR19）。
func TestHealthCheck_CustomProbe(t *testing.T) {
	repo := &fakeSystemInitRepo{}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), stubProbe{llm: service.StatusReachable, integ: service.StatusUnreachable})

	res, err := svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if res.Database != service.StatusConnected {
		t.Fatalf("Database want %q, got %q", service.StatusConnected, res.Database)
	}
	if res.Redis != service.StatusConnected {
		t.Fatalf("Redis want %q, got %q", service.StatusConnected, res.Redis)
	}
	if res.LLM != service.StatusReachable {
		t.Fatalf("LLM want %q, got %q", service.StatusReachable, res.LLM)
	}
	if res.Integration != service.StatusUnreachable {
		t.Fatalf("Integration want %q, got %q", service.StatusUnreachable, res.Integration)
	}
}

// panicProbe 的 LLM 分支 panic、Integration 正常返回，验证单项 panic 不击穿健康接口。
type panicProbe struct{ integ string }

func (panicProbe) ProbeLLM(_ context.Context) string { panic("boom") }
func (p panicProbe) ProbeIntegration(_ context.Context) string {
	return p.integ
}

var _ service.DependencyProbe = panicProbe{}

// TestHealthCheck_ProbePanic_Isolated：LLM 探测 panic → 该项降级 unreachable，
// Integration 与 db/redis 项不受影响（specs §5.4.4 规则1 单项隔离）。
func TestHealthCheck_ProbePanic_Isolated(t *testing.T) {
	repo := &fakeSystemInitRepo{}
	svc := service.NewSystemService(repo, constProbe(true), constProbe(true), panicProbe{integ: service.StatusReachable})

	res, err := svc.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck should survive probe panic, got %v", err)
	}
	if res.LLM != service.StatusUnreachable {
		t.Fatalf("LLM want unreachable on panic, got %q", res.LLM)
	}
	if res.Integration != service.StatusReachable {
		t.Fatalf("Integration want %q (unaffected by llm panic), got %q", service.StatusReachable, res.Integration)
	}
	if res.Database != service.StatusConnected || res.Redis != service.StatusConnected {
		t.Fatalf("db/redis want connected, got %q/%q", res.Database, res.Redis)
	}
}
