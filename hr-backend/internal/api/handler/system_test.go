package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/service"
)

// fakeSystemService 是 service.SystemService 的假实现，承载可配置返回值字段，
// 仿 fakeSetupService 模式驱动 handler 单元测试（specs §5.3 / §5.4）。
type fakeSystemService struct {
	summary    *service.SystemSummary
	summaryErr error
	health     *service.HealthResult
	healthErr  error
}

func (f *fakeSystemService) GetSummary(_ context.Context) (*service.SystemSummary, error) {
	return f.summary, f.summaryErr
}

func (f *fakeSystemService) HealthCheck(_ context.Context) (*service.HealthResult, error) {
	return f.health, f.healthErr
}

var _ service.SystemService = (*fakeSystemService)(nil)

// TestSystemHandler_GetSummary 覆盖核心断言：GET /api/system/status 响应 code==0，
// data.initialized/db_type/version/started_at 字段齐备且值映射正确（specs §3.3）。
// started_at 用已知 time.Date 构造，断言 RFC3339 UTC 字符串透传。
func TestSystemHandler_GetSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	started := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	svc := &fakeSystemService{
		summary: &service.SystemSummary{
			Initialized: true,
			DBType:      "sqlite",
			Version:     "v0.1.0",
			StartedAt:   started,
		},
	}
	h := handler.NewSystemHandler(svc)
	r := gin.New()
	r.GET("/api/system/status", h.GetSummary)

	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Initialized bool   `json:"initialized"`
			DBType      string `json:"db_type"`
			Version     string `json:"version"`
			StartedAt   string `json:"started_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !resp.Data.Initialized {
		t.Fatalf("data.initialized = false, want true")
	}
	if resp.Data.DBType != "sqlite" {
		t.Fatalf("data.db_type = %q, want %q", resp.Data.DBType, "sqlite")
	}
	if resp.Data.Version != "v0.1.0" {
		t.Fatalf("data.version = %q, want %q", resp.Data.Version, "v0.1.0")
	}
	if want := started.Format(time.RFC3339); resp.Data.StartedAt != want {
		t.Fatalf("data.started_at = %q, want %q (RFC3339)", resp.Data.StartedAt, want)
	}
}

// TestSystemHandler_HealthCheck 覆盖核心断言：POST /api/system/health-check 响应 code==0，
// data.database/redis/llm/integration 字段齐备且值透传正确（specs §3.4）。
func TestSystemHandler_HealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSystemService{
		health: &service.HealthResult{
			Database:    service.StatusConnected,
			Redis:       service.StatusConnected,
			LLM:         service.StatusNotConfigured,
			Integration: service.StatusNotConfigured,
		},
	}
	h := handler.NewSystemHandler(svc)
	r := gin.New()
	r.POST("/api/system/health-check", h.HealthCheck)

	// 无请求体（specs §3.4 请求：无 body 或空对象）。
	req := httptest.NewRequest(http.MethodPost, "/api/system/health-check", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Database    string `json:"database"`
			Redis       string `json:"redis"`
			LLM         string `json:"llm"`
			Integration string `json:"integration"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Database != service.StatusConnected {
		t.Fatalf("data.database = %q, want %q", resp.Data.Database, service.StatusConnected)
	}
	if resp.Data.Redis != service.StatusConnected {
		t.Fatalf("data.redis = %q, want %q", resp.Data.Redis, service.StatusConnected)
	}
	if resp.Data.LLM != service.StatusNotConfigured {
		t.Fatalf("data.llm = %q, want %q", resp.Data.LLM, service.StatusNotConfigured)
	}
	if resp.Data.Integration != service.StatusNotConfigured {
		t.Fatalf("data.integration = %q, want %q", resp.Data.Integration, service.StatusNotConfigured)
	}
}
