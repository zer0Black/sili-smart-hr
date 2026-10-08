// workspace_test 对工作台域单接口做 handler 黑盒测试（specs P2_WRK_001 §5.1，03 W1）。
//
// fake service + 裸 engine 覆盖 handler 行为（成功与 1500 整体失败两条路径）；
// 生产同构 router 覆盖路由挂 JWT 鉴权 auth 组（specs §2.2 / BR1，无 token 401+1003）。
package handler_test

import (
	"context"
	"encoding/json"
	"errors"
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

// fakeWorkspaceSvc 是 service.WorkspaceService 的假实现：调用探针 + 注入返回。
type fakeWorkspaceSvc struct {
	overviewCalls int
	overviewRes   *service.WorkspaceDTO
	overviewErr   error
}

func (f *fakeWorkspaceSvc) Overview(_ context.Context) (*service.WorkspaceDTO, error) {
	f.overviewCalls++
	return f.overviewRes, f.overviewErr
}

var _ service.WorkspaceService = (*fakeWorkspaceSvc)(nil)

// errWorkspaceBoom 非 *service.Error 的基础设施错误，锚 500/1500 映射（03 §2.3）。
var errWorkspaceBoom = errors.New("db down")

// newWorkspaceRouter 挂单路径裸 engine（无中间件），供 handler 层行为测试。
func newWorkspaceRouter(svc *fakeWorkspaceSvc) *gin.Engine {
	h := handler.NewWorkspaceHandler(svc)
	r := gin.New()
	r.GET("/api/workspace", h.Overview)
	return r
}

// fixedWorkspaceDTO 构造覆盖各区块 JSON tag 的固定 DTO（03 W1 响应字段抽样）。
func fixedWorkspaceDTO() *service.WorkspaceDTO {
	status := "success"
	next := "2026-10-14 23:00"
	alert, overdue := 1, 3
	updated := "2026-10-08 02:12"
	pp, unused := -1.2, 5
	change, current := 2, 67
	score := 65
	days := 12
	usage, mgmt := 58, 59
	return &service.WorkspaceDTO{
		Batch: &service.WorkspaceBatchDTO{
			Status:        &status,
			NextTriggerAt: &next,
			AlertCount:    &alert,
			OverdueCount:  &overdue,
			DataUpdatedAt: &updated,
		},
		CurrentPeriod: &service.WorkspacePeriodDTO{PeriodStart: "2026-10-01", PeriodEnd: "2026-10-07"},
		Trend: &service.WorkspaceTrendDTO{
			Periods: []service.WorkspacePeriodDTO{
				{PeriodStart: "2026-09-24", PeriodEnd: "2026-09-30"},
				{PeriodStart: "2026-10-01", PeriodEnd: "2026-10-07"},
			},
			Series: []service.WorkspaceTrendSeriesDTO{
				{Module: "AI_USAGE", Scores: []*int{&score, &current}, CurrentScore: &current, ChangeVsPrev: &change},
				{Module: "AI_MGMT", Scores: []*int{nil, &mgmt}, CurrentScore: &mgmt, ChangeVsPrev: &change},
			},
			Activity: &service.WorkspaceTrendActivityDTO{
				ActiveRatio: 76.5, ActiveChangePP: &pp, UnusedCount: unused, UnusedChange: &change,
			},
		},
		Profile: &service.WorkspaceProfileDTO{
			Modules: []service.WorkspaceModuleDTO{
				{
					Module: "AI_USAGE", OverallAvg: &usage,
					Dimensions: []service.WorkspaceDimItemDTO{
						{DimensionCode: "code_review", DimensionName: "代码评审", AvgScore: &score, IsWeakness: true},
					},
				},
			},
			Weaknesses: []service.WorkspaceWeaknessDTO{
				{Module: "AI_USAGE", DimensionCode: "code_review", DimensionName: "代码评审", LowRatio: 41.2},
			},
			Suggestion: service.WorkspaceSuggestionDTO{Status: "generated", Summary: "团队整体稳定"},
		},
		Attention: []service.WorkspaceAttentionRow{
			{
				StaffName: "张三", Category: "weak", ActivityLevel: "normal",
				AIUsageScore: &usage, AIMGMTScore: &mgmt,
				WeakModules: []service.WorkspaceWeakModuleDTO{
					{Module: "AI_USAGE", Score: 58, WeakDims: []string{"code_review"}},
				},
			},
			{
				StaffName: "李四", Category: "unused", ActivityLevel: "unused",
				DaysSinceActive: &days, WeakModules: []service.WorkspaceWeakModuleDTO{},
			},
		},
	}
}

// TestWorkspaceHandlerOverview_OK 覆盖核心断言：fake 返回固定 DTO 时 GET /api/workspace
// 返回 HTTP 200、code 0、data.batch.status 等字段序列化正确（03 W1 统一响应）。
func TestWorkspaceHandlerOverview_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeWorkspaceSvc{overviewRes: fixedWorkspaceDTO()}
	r := newWorkspaceRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Batch struct {
				Status        *string `json:"status"`
				NextTriggerAt *string `json:"next_trigger_at"`
				AlertCount    *int    `json:"alert_count"`
				OverdueCount  *int    `json:"overdue_count"`
				DataUpdatedAt *string `json:"data_updated_at"`
			} `json:"batch"`
			CurrentPeriod *struct {
				PeriodStart string `json:"period_start"`
				PeriodEnd   string `json:"period_end"`
			} `json:"current_period"`
			Trend struct {
				Series []struct {
					Module       string `json:"module"`
					Scores       []*int `json:"scores"`
					CurrentScore *int   `json:"current_score"`
					ChangeVsPrev *int   `json:"change_vs_prev"`
				} `json:"series"`
				Activity *struct {
					ActiveRatio   float64 `json:"active_ratio"`
					UnusedCount   int     `json:"unused_count"`
					UnusedChange  *int    `json:"unused_change"`
					ActiveChangePP *float64 `json:"active_change_pp"`
				} `json:"activity"`
			} `json:"trend"`
			Profile *struct {
				Weaknesses []struct {
					Module        string  `json:"module"`
					DimensionCode string  `json:"dimension_code"`
					LowRatio      float64 `json:"low_ratio"`
				} `json:"weaknesses"`
				Suggestion struct {
					Status string `json:"status"`
				} `json:"suggestion"`
			} `json:"profile"`
			Attention []struct {
				StaffName       string `json:"staff_name"`
				Category        string `json:"category"`
				DaysSinceActive *int   `json:"days_since_active"`
			} `json:"attention"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Batch.Status == nil || *resp.Data.Batch.Status != "success" {
		t.Fatalf("data.batch.status = %v, want success", resp.Data.Batch.Status)
	}
	if resp.Data.Batch.NextTriggerAt == nil || *resp.Data.Batch.NextTriggerAt != "2026-10-14 23:00" {
		t.Fatalf("data.batch.next_trigger_at = %v", resp.Data.Batch.NextTriggerAt)
	}
	if resp.Data.Batch.AlertCount == nil || *resp.Data.Batch.AlertCount != 1 {
		t.Fatalf("data.batch.alert_count = %v, want 1", resp.Data.Batch.AlertCount)
	}
	if resp.Data.Batch.OverdueCount == nil || *resp.Data.Batch.OverdueCount != 3 {
		t.Fatalf("data.batch.overdue_count = %v, want 3", resp.Data.Batch.OverdueCount)
	}
	if resp.Data.CurrentPeriod == nil || resp.Data.CurrentPeriod.PeriodStart != "2026-10-01" || resp.Data.CurrentPeriod.PeriodEnd != "2026-10-07" {
		t.Fatalf("data.current_period = %+v", resp.Data.CurrentPeriod)
	}
	if len(resp.Data.Trend.Series) != 2 {
		t.Fatalf("trend.series len = %d, want 2", len(resp.Data.Trend.Series))
	}
	aiUsage := resp.Data.Trend.Series[0]
	if aiUsage.Module != "AI_USAGE" || aiUsage.CurrentScore == nil || *aiUsage.CurrentScore != 67 || aiUsage.ChangeVsPrev == nil || *aiUsage.ChangeVsPrev != 2 {
		t.Fatalf("trend.series[0] = %+v", aiUsage)
	}
	if len(aiUsage.Scores) != 2 || aiUsage.Scores[0] == nil || *aiUsage.Scores[0] != 65 {
		t.Fatalf("trend.series[0].scores = %+v", aiUsage.Scores)
	}
	aiMgmt := resp.Data.Trend.Series[1]
	if len(aiMgmt.Scores) != 2 || aiMgmt.Scores[0] != nil || aiMgmt.Scores[1] == nil || *aiMgmt.Scores[1] != 59 {
		t.Fatalf("trend.series[1].scores 断点序列化错误: %+v", aiMgmt.Scores)
	}
	if resp.Data.Trend.Activity == nil || resp.Data.Trend.Activity.ActiveRatio != 76.5 || resp.Data.Trend.Activity.UnusedCount != 5 {
		t.Fatalf("trend.activity = %+v", resp.Data.Trend.Activity)
	}
	if resp.Data.Trend.Activity.ActiveChangePP == nil || *resp.Data.Trend.Activity.ActiveChangePP != -1.2 {
		t.Fatalf("trend.activity.active_change_pp = %v, want -1.2", resp.Data.Trend.Activity.ActiveChangePP)
	}
	if resp.Data.Profile == nil || len(resp.Data.Profile.Weaknesses) != 1 || resp.Data.Profile.Weaknesses[0].DimensionCode != "code_review" || resp.Data.Profile.Weaknesses[0].LowRatio != 41.2 {
		t.Fatalf("profile.weaknesses = %+v", resp.Data.Profile.Weaknesses)
	}
	if resp.Data.Profile.Suggestion.Status != "generated" {
		t.Fatalf("profile.suggestion.status = %q, want generated", resp.Data.Profile.Suggestion.Status)
	}
	if len(resp.Data.Attention) != 2 {
		t.Fatalf("attention len = %d, want 2", len(resp.Data.Attention))
	}
	if resp.Data.Attention[1].Category != "unused" || resp.Data.Attention[1].DaysSinceActive == nil || *resp.Data.Attention[1].DaysSinceActive != 12 {
		t.Fatalf("attention[1] = %+v", resp.Data.Attention[1])
	}
	if svc.overviewCalls != 1 {
		t.Fatalf("svc.Overview 调用 %d 次, want 1", svc.overviewCalls)
	}
}

// TestWorkspaceHandlerOverview_EmptyDTO 覆盖核心断言外的正常分支：区块降级空标记
//（trend/profile nil、attention 空数组）原样透传（specs §5.1.4 规则2 降级语义）。
func TestWorkspaceHandlerOverview_EmptyDTO(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeWorkspaceSvc{overviewRes: &service.WorkspaceDTO{
		Batch:     &service.WorkspaceBatchDTO{},
		Attention: []service.WorkspaceAttentionRow{},
	}}
	r := newWorkspaceRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Batch     json.RawMessage `json:"batch"`
			Trend     json.RawMessage `json:"trend"`
			Profile   json.RawMessage `json:"profile"`
			Attention json.RawMessage `json:"attention"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if string(resp.Data.Trend) != "null" || string(resp.Data.Profile) != "null" {
		t.Fatalf("降级区块应为 null: trend=%s profile=%s", resp.Data.Trend, resp.Data.Profile)
	}
	if string(resp.Data.Attention) != "[]" {
		t.Fatalf("attention 空标记应为 [], got %s", resp.Data.Attention)
	}
	if string(resp.Data.Batch) == "null" {
		t.Fatalf("batch 恒为对象, got %s", resp.Data.Batch)
	}
}

// TestWorkspaceHandlerOverview_InternalError 覆盖核心断言：fake 返回非 *service.Error
// 普通错误时 HTTP 500 + code 1500（03 §1.10 唯一整体失败路径）。
func TestWorkspaceHandlerOverview_InternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeWorkspaceSvc{overviewErr: errWorkspaceBoom}
	r := newWorkspaceRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.Internal {
		t.Fatalf("code = %d, want 1500", resp.Code)
	}
}

// TestWorkspaceHandlerOverview_QueryIgnored 边界补充：携带任意 query 不报错、
// 不改变 service 调用（W1 无入参，多余参数被忽略，03 W1「查询参数：无」）。
func TestWorkspaceHandlerOverview_QueryIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeWorkspaceSvc{overviewRes: &service.WorkspaceDTO{Batch: &service.WorkspaceBatchDTO{}}}
	r := newWorkspaceRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace?period_start=2026-01-01&foo=bar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.overviewCalls != 1 {
		t.Fatalf("svc.Overview 调用 %d 次, want 1", svc.overviewCalls)
	}
}

// newWorkspaceEngine 起生产同构 router（miniredis 供中间件），workspace handler 注入
// fake，参数位与生产 NewRouter 一致（workspaceHandler 在 dashboardHandler 之后）。
func newWorkspaceEngine(t *testing.T, svc *fakeWorkspaceSvc) http.Handler {
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
		jwt.NewManager("workspace-test-secret", time.Hour),
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
		nil, // dashboardHandler
		handler.NewWorkspaceHandler(svc),
		nil, // operationLogHandler
		nil, // recorder（操作日志记录通道，GET 路由不触达）
		rdb,
	)
}

// TestWorkspaceRoutes_Auth 覆盖核心断言（specs §2.2 / BR1）：GET /api/workspace 挂
// JWT 鉴权 auth 组，无 Authorization 头 401 + code 1003，不进入 handler；带合法
// token 任意已登录账号可达 handler。
func TestWorkspaceRoutes_Auth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeWorkspaceSvc{}
	engine := newWorkspaceEngine(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/workspace status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.Unauthorized {
		t.Fatalf("code = %d, want 1003", resp.Code)
	}
	if svc.overviewCalls != 0 {
		t.Fatalf("无 token 请求不应触达 handler, 调用 %d 次", svc.overviewCalls)
	}

	// 带合法 token 路由可达（任意已登录账号可访问，specs §2.2）。
	reach := &fakeWorkspaceSvc{overviewRes: &service.WorkspaceDTO{Batch: &service.WorkspaceBatchDTO{}, Attention: []service.WorkspaceAttentionRow{}}}
	engine2 := newWorkspaceEngine(t, reach)
	jwtMgr := jwt.NewManager("workspace-test-secret", time.Hour)
	token, err := jwtMgr.Generate(1, "workspace-tester")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	engine2.ServeHTTP(w2, req2)
	if w2.Code == http.StatusNotFound {
		t.Fatalf("GET /api/workspace 返回 404，路由未注册")
	}
	if w2.Code != http.StatusOK {
		t.Fatalf("GET /api/workspace status = %d, want 200, body=%s", w2.Code, w2.Body.String())
	}
}
