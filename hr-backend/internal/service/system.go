// Package service 承载业务逻辑，按业务域划分。
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sili-smart-hr/backend/internal/model"
	"sili-smart-hr/backend/internal/repository"
)

// 组件健康状态值（specs §4.2.2 枚举、03 §3.4 / 04 §8 不用字典表）。
const (
	StatusConnected          = "connected"
	StatusDisconnected       = "disconnected"
	StatusReachable          = "reachable"
	StatusUnreachable        = "unreachable"
	StatusNotConfiguredModel = "not_configured_model"
	StatusNotConfiguredKey   = "not_configured_key"
	StatusNotConfigured      = "not_configured"
)

// DependencyProbe 探测 config 落地后的外部依赖（大模型、会话日志集成）可达性（specs §5.4.2）。
// config 未落地前由 NoopDependencyProbe 返回 not_configured，落地后替换为真实探测实现（specs §4.2.4 规则2）。
type DependencyProbe interface {
	ProbeLLM(ctx context.Context) string
	ProbeIntegration(ctx context.Context) string
}

// NoopDependencyProbe 在 config 未落地阶段统一返回 not_configured，区分降级文案与 unreachable（specs §4.2.4 规则2）。
type NoopDependencyProbe struct{}

func (NoopDependencyProbe) ProbeLLM(_ context.Context) string         { return StatusNotConfigured }
func (NoopDependencyProbe) ProbeIntegration(_ context.Context) string { return StatusNotConfigured }

// SystemService 是系统状态与组件健康域的业务接口（specs §5.3、§5.4）。
type SystemService interface {
	GetSummary(ctx context.Context) (*SystemSummary, error)
	HealthCheck(ctx context.Context) (*HealthResult, error)
}

// SystemSummary 是 GET /api/system/status 的业务返回结构（specs §5.3.3）。
// DBType 取 model.Current() 进程级常量，Version/StartedAt 取 model 包导出变量（04 §5）。
type SystemSummary struct {
	Initialized bool
	DBType      string
	Version     string
	StartedAt   time.Time
}

// HealthResult 是 POST /api/system/health-check 的业务返回结构（specs §5.4.3）。
// 各字段取组件健康状态值枚举（specs §4.2.2）。
type HealthResult struct {
	Database    string
	Redis       string
	LLM         string
	Integration string
}

type systemService struct {
	systemInitRepo repository.SystemInitializationRepository
	dbProbe        func(context.Context) bool
	redisProbe     func(context.Context) bool
	probe          DependencyProbe
}

// NewSystemService 注入 system_initializations 仓库、db/redis 连通探针与外部依赖探测接口。
func NewSystemService(systemInitRepo repository.SystemInitializationRepository, dbProbe, redisProbe func(context.Context) bool, probe DependencyProbe) SystemService {
	return &systemService{systemInitRepo: systemInitRepo, dbProbe: dbProbe, redisProbe: redisProbe, probe: probe}
}

// GetSummary 返回系统运行摘要（specs §5.3.2，异常处理 §5.3.5）。
//
//	initialized 查 system_initializations 记录存在性判定（specs 规则1）。
//	Exists 出错时 initialized 降级为 false 并记 slog 错误日志，不返错，其余摘要字段正常返回。
func (s *systemService) GetSummary(ctx context.Context) (*SystemSummary, error) {
	initialized, err := s.systemInitRepo.Exists(ctx)
	if err != nil {
		slog.Error("query system initialization exists failed", "err", err)
		initialized = false
	}
	return &SystemSummary{
		Initialized: initialized,
		DBType:      string(model.Current()),
		Version:     model.Version,
		StartedAt:   model.StartedAt,
	}, nil
}

// HealthCheck 逐项探测组件连通性（specs §5.4.2）。单项探测失败仅影响对应项，
// 始终返回 nil error：探测失败是业务结果而非接口错误（specs §5.4.5）。
func (s *systemService) HealthCheck(ctx context.Context) (*HealthResult, error) {
	db := StatusConnected
	if !s.dbProbe(ctx) {
		db = StatusDisconnected
	}
	r := StatusConnected
	if !s.redisProbe(ctx) {
		r = StatusDisconnected
	}
	llmStatus, integStatus := s.probeParallel(ctx)
	return &HealthResult{
		Database:    db,
		Redis:       r,
		LLM:         llmStatus,
		Integration: integStatus,
	}, nil
}

// probeParallel 并行执行大模型（上限 10s）与会话日志探活（5s）收敛总耗时；
// 单项 panic 经 recover 降级 unreachable，不影响另一项与接口响应（specs §5.4.4 规则1）。
func (s *systemService) probeParallel(ctx context.Context) (llmStatus, integStatus string) {
	var wg sync.WaitGroup
	wg.Add(2)
	run := func(target *string, name string, f func(context.Context) string) {
		go func() {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					slog.Error("health probe panicked", "probe", name, "panic", rec)
				}
			}()
			*target = f(ctx)
		}()
	}
	run(&llmStatus, "llm", s.probe.ProbeLLM)
	run(&integStatus, "integration", s.probe.ProbeIntegration)
	wg.Wait()
	if llmStatus == "" {
		llmStatus = StatusUnreachable
	}
	if integStatus == "" {
		integStatus = StatusUnreachable
	}
	return llmStatus, integStatus
}
