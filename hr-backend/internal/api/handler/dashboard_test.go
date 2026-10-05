// dashboard_test 对团队看板域两接口做 handler 黑盒测试（specs P2_TMD_001 §5.2.1/§5.2.2）。
//
// fake service + 裸 engine 覆盖两 handler 行为；生产同构 router 覆盖两路由
// 挂 JWT 鉴权组（specs §2.2 / BR1，无 token 统一 401+1003）。
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/api/router"
	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/service"
)

// fakeDashboardSvc 是 service.DashboardService 的假实现：入参探针 + 注入返回。
type fakeDashboardSvc struct {
	overviewLast struct {
		PeriodStart, PeriodEnd string
	}
	overviewRes *service.DashboardOverviewDTO
	overviewErr error

	trendLastType string
	trendRes      *service.DashboardTrendDTO
	trendErr      error
}

func (f *fakeDashboardSvc) Overview(_ context.Context, periodStart, periodEnd string) (*service.DashboardOverviewDTO, error) {
	f.overviewLast.PeriodStart = periodStart
	f.overviewLast.PeriodEnd = periodEnd
	return f.overviewRes, f.overviewErr
}

func (f *fakeDashboardSvc) Trend(_ context.Context, abilityType string) (*service.DashboardTrendDTO, error) {
	f.trendLastType = abilityType
	return f.trendRes, f.trendErr
}

var _ service.DashboardService = (*fakeDashboardSvc)(nil)

// newDashboardRouter 挂两条路径的裸 engine（无中间件），供 handler 层行为测试。
func newDashboardRouter(svc *fakeDashboardSvc) *gin.Engine {
	h := handler.NewDashboardHandler(svc)
	r := gin.New()
	r.GET("/api/dashboard", h.Overview)
	r.GET("/api/dashboard/trend", h.Trend)
	return r
}

// TestDashboardOverview_OK 覆盖核心断言：fake 返回固定 DTO 时 GET /api/dashboard
// 返回 HTTP 200、code 0、data.staff_total 透传（03 A1 统一响应）。
func TestDashboardOverview_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeDashboardSvc{
		overviewRes: &service.DashboardOverviewDTO{
			Periods:    []service.DashboardPeriodItem{},
			Modules:    []service.DashboardModuleDTO{},
			StaffTotal: 42,
		},
	}
	r := newDashboardRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			StaffTotal int `json:"staff_total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.StaffTotal != 42 {
		t.Fatalf("data.staff_total = %d, want 42", resp.Data.StaffTotal)
	}
}

// TestDashboardOverview_Params 验证区间参数透传：period_start/period_end 原样进入
// service（校验与 2101 判定归 service，handler 薄透传，03 A1）。
func TestDashboardOverview_Params(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeDashboardSvc{
		overviewRes: &service.DashboardOverviewDTO{
			Periods: []service.DashboardPeriodItem{},
			Modules: []service.DashboardModuleDTO{},
		},
	}
	r := newDashboardRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?period_start=2026-09-22&period_end=2026-09-28", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.overviewLast.PeriodStart != "2026-09-22" || svc.overviewLast.PeriodEnd != "2026-09-28" {
		t.Fatalf("区间透传错误: %+v", svc.overviewLast)
	}
}

// TestDashboardOverview_2101 覆盖核心断言：fake 返 *service.Error{2101} 时
// HTTP 200 + body code 2101（specs §5.2.4 规则2 错误语义，前端提示并回落最新区间）。
func TestDashboardOverview_2101(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeDashboardSvc{overviewErr: service.NewError(errcode.DashboardPeriodInvalid)}
	r := newDashboardRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?period_start=2026-01-01&period_end=2026-01-07", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (业务错误统一 200), body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.DashboardPeriodInvalid {
		t.Fatalf("code = %d, want 2101", resp.Code)
	}
}

// TestDashboardTrend_TypeParam 覆盖核心断言：GET /api/dashboard/trend?type=manage 时
// svc 收到 "manage"（specs §5.2.2 步2：use|manage，非法值兜底归 service，handler 透传）。
func TestDashboardTrend_TypeParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeDashboardSvc{
		trendRes: &service.DashboardTrendDTO{
			Periods:    []service.DashboardPeriodItem{},
			Dimensions: []service.DashboardTrendDim{},
		},
	}
	r := newDashboardRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/trend?type=manage", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.trendLastType != "manage" {
		t.Fatalf("svc 收到 type = %q, want manage", svc.trendLastType)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
}

// newDashboardEngine 起生产同构 router（miniredis 供中间件），dashboard handler 注入
// fake，参数位与生产 NewRouter 一致（dashboardHandler 在 profileHandler 之后）。
func newDashboardEngine(t *testing.T, svc *fakeDashboardSvc) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rsaMgr := rsakey.NewManager(rdb, 5*time.Minute)
	return router.NewRouter(
		&config.Config{},
		jwt.NewManager("dashboard-test-secret", time.Hour),
		handler.NewAccountHandler(&fakeProfileAccountSvc{}, rsaMgr),
		handler.NewHealthHandler(),
		nil, // setupHandler
		nil, // systemHandler
		nil, // dimensionHandler
		nil, // assessmentConfigHandler
		nil, // assessmentBatchHandler
		nil, // assessmentTestTaskHandler
		nil, // answerHandler
		nil, // llmConfigHandler
		nil, // integrationSecretHandler
		nil, // questionHandler
		nil, // questionBatchHandler
		nil, // questionGenerationHandler
		nil, // scaleHandler
		nil, // profileHandler
		handler.NewDashboardHandler(svc),
		rdb,
	)
}

// TestDashboardRoutes_Auth 覆盖核心断言（specs §2.2 / BR1）：两路由均挂 JWT 鉴权
// auth 组，无 Authorization 头统一 401 + code 1003，不进入 handler；带合法 token
// 时可达 handler（区分 404 与 401）。
func TestDashboardRoutes_Auth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeDashboardSvc{}
	engine := newDashboardEngine(t, svc)

	for _, path := range []string{"/api/dashboard", "/api/dashboard/trend"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401, body=%s", path, w.Code, w.Body.String())
		}
		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
		}
		if resp.Code != errcode.Unauthorized {
			t.Fatalf("GET %s code = %d, want 1003", path, resp.Code)
		}
	}
	if svc.overviewLast.PeriodStart != "" || svc.trendLastType != "" {
		t.Fatalf("无 token 请求不应触达 handler: %+v", svc)
	}

	// 带合法 token 路由可达（任意已登录账号可访问，specs §2.2）。
	reach := &fakeDashboardSvc{
		overviewRes: &service.DashboardOverviewDTO{Periods: []service.DashboardPeriodItem{}, Modules: []service.DashboardModuleDTO{}},
		trendRes:    &service.DashboardTrendDTO{Periods: []service.DashboardPeriodItem{}, Dimensions: []service.DashboardTrendDim{}},
	}
	engine2 := newDashboardEngine(t, reach)
	jwtMgr := jwt.NewManager("dashboard-test-secret", time.Hour)
	token, err := jwtMgr.Generate(1, "dashboard-tester")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	for _, path := range []string{"/api/dashboard", "/api/dashboard/trend"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		engine2.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("GET %s 返回 404，路由未注册", path)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200, body=%s", path, w.Code, w.Body.String())
		}
	}
}
