package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// fakeAssessmentBatchService 是 service.AssessmentBatchService 的假实现，承载可配置返回值。
// 入参以 last* 字段记录便于断言。
type fakeAssessmentBatchService struct {
	listFilter service.BatchListFilter
	listRes    []service.BatchListDTO
	listTotal  int64
	listErr    error

	statsRes *service.BatchStatsDTO
	statsErr error

	planRes *service.BatchPlanDTO
	planErr error

	targetsID  int64
	targetsRes *service.BatchTargetsDTO
	targetsErr error

	failuresID  int64
	failuresRes *service.BatchFailuresDTO
	failuresErr error

	createPayload service.CreateBatchPayload
	createRes     *service.CreateBatchResult
	createErr     error
}

func (f *fakeAssessmentBatchService) List(_ context.Context, flt service.BatchListFilter) ([]service.BatchListDTO, int64, error) {
	f.listFilter = flt
	return f.listRes, f.listTotal, f.listErr
}

func (f *fakeAssessmentBatchService) Stats(_ context.Context) (*service.BatchStatsDTO, error) {
	return f.statsRes, f.statsErr
}

func (f *fakeAssessmentBatchService) Plan(_ context.Context) (*service.BatchPlanDTO, error) {
	return f.planRes, f.planErr
}

func (f *fakeAssessmentBatchService) Targets(_ context.Context, batchID int64) (*service.BatchTargetsDTO, error) {
	f.targetsID = batchID
	return f.targetsRes, f.targetsErr
}

func (f *fakeAssessmentBatchService) Failures(_ context.Context, batchID int64) (*service.BatchFailuresDTO, error) {
	f.failuresID = batchID
	return f.failuresRes, f.failuresErr
}

func (f *fakeAssessmentBatchService) Create(_ context.Context, p service.CreateBatchPayload) (*service.CreateBatchResult, error) {
	f.createPayload = p
	return f.createRes, f.createErr
}

var _ service.AssessmentBatchService = (*fakeAssessmentBatchService)(nil)

func newBatchRouter(svc *fakeAssessmentBatchService) *gin.Engine {
	h := handler.NewAssessmentBatchHandler(svc)
	r := gin.New()
	r.GET("/api/assessment/batches", h.List)
	r.GET("/api/assessment/batches/stats", h.Stats)
	r.GET("/api/assessment/batches/plan", h.Plan)
	r.GET("/api/assessment/batches/targets", h.Targets)
	r.GET("/api/assessment/batches/failures", h.Failures)
	r.POST("/api/assessment/batches/create", h.Create)
	return r
}

type batchResp struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

// TestListRouteRegistered 覆盖核心断言：GET /api/assessment/batches?trigger_type=scheduled
// 返 HTTP 200、code=0、data.list 为数组；trigger_type 透传 fake。
func TestListRouteRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{
		listRes: []service.BatchListDTO{
			{ID: 1845700000000000001, BatchNo: "B20260913-001", TriggerType: "scheduled", TotalCount: 3},
		},
		listTotal: 1,
	}
	r := newBatchRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches?trigger_type=scheduled", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List []map[string]any `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if len(resp.Data.List) != 1 {
		t.Fatalf("data.list len = %d, want 1", len(resp.Data.List))
	}
	if svc.listFilter.TriggerType != "scheduled" {
		t.Fatalf("filter.TriggerType = %q, want %q", svc.listFilter.TriggerType, "scheduled")
	}
}

// TestListPageFallback 覆盖 page/page_size 兜底：非法值回退 1/10，>100 钳 100。
func TestListPageFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		url          string
		wantPage     int
		wantPageSize int
	}{
		{"/api/assessment/batches", 1, 10},
		{"/api/assessment/batches?page=abc&page_size=xyz", 1, 10},
		{"/api/assessment/batches?page=-2&page_size=0", 1, 10},
		{"/api/assessment/batches?page=2&page_size=200", 2, 100},
		{"/api/assessment/batches?page=3&page_size=50&status=running", 3, 50},
	}
	for _, tc := range cases {
		svc := &fakeAssessmentBatchService{listRes: []service.BatchListDTO{}}
		r := newBatchRouter(svc)
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", tc.url, w.Code)
		}
		if svc.listFilter.Page != tc.wantPage || svc.listFilter.PageSize != tc.wantPageSize {
			t.Fatalf("%s filter = (%d,%d), want (%d,%d)", tc.url,
				svc.listFilter.Page, svc.listFilter.PageSize, tc.wantPage, tc.wantPageSize)
		}
	}
	// status 透传
	svc := &fakeAssessmentBatchService{listRes: []service.BatchListDTO{}}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches?status=partial_failed", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if svc.listFilter.Status != "partial_failed" {
		t.Fatalf("filter.Status = %q, want %q", svc.listFilter.Status, "partial_failed")
	}
}

// TestListServiceError 覆盖 handleServiceError 映射：业务错误 HTTP 200 带 code。
func TestListServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{listErr: service.NewError(errcode.BadRequest)}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.BadRequest)
	}
}

// TestStatsRoute 覆盖 GET /api/assessment/batches/stats 成功路径。
func TestStatsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{
		statsRes: &service.BatchStatsDTO{EvalCount: 5, EvaluatedPersonCount: 42, RunningBatchCount: 1},
	}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp batchResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if got, ok := resp.Data["evaluated_person_count"].(float64); !ok || got != 42 {
		t.Fatalf("data.evaluated_person_count = %v, want 42", resp.Data["evaluated_person_count"])
	}
}

// TestPlanRoute 覆盖 GET /api/assessment/batches/plan 成功路径。
func TestPlanRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{
		planRes: &service.BatchPlanDTO{NextTriggerAt: "2026-09-20 23:00", Period: "weekly", TargetMode: "all"},
	}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches/plan", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp batchResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data["period"] != "weekly" {
		t.Fatalf("data.period = %v, want weekly", resp.Data["period"])
	}
}

// TestTargetsMissingBatchID 覆盖核心断言：无 batch_id 调用断言 code==1400。
func TestTargetsMissingBatchID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{}
	r := newBatchRouter(svc)
	for _, url := range []string{
		"/api/assessment/batches/targets",
		"/api/assessment/batches/targets?batch_id=",
		"/api/assessment/batches/targets?batch_id=abc",
		"/api/assessment/batches/targets?batch_id=0",
		"/api/assessment/batches/targets?batch_id=-5",
	} {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", url, w.Code)
		}
		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s unmarshal: %v", url, err)
		}
		if resp.Code != errcode.BadRequest {
			t.Fatalf("%s code = %d, want %d", url, resp.Code, errcode.BadRequest)
		}
	}
}

// TestTargetsOK 覆盖 batch_id 解析透传与成功响应。
func TestTargetsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{
		targetsRes: &service.BatchTargetsDTO{BatchID: 1845700000000000002, Names: []string{"张三", "李四"}, Total: 2},
	}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches/targets?batch_id=1845700000000000002", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.targetsID != 1845700000000000002 {
		t.Fatalf("targetsID = %d, want 1845700000000000002", svc.targetsID)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			BatchID string `json:"batch_id"`
			Total   int    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.BatchID != "1845700000000000002" {
		t.Fatalf("batch_id = %q, want string 化雪花 ID", resp.Data.BatchID)
	}
}

// TestFailuresBatchID 覆盖 failures 的 batch_id 校验（与 targets 同款）。
func TestFailuresBatchID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/batches/failures", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.BadRequest)
	}

	svc.failuresRes = &service.BatchFailuresDTO{BatchID: 99, BatchNo: "B1", List: []service.BatchFailureItem{}}
	req = httptest.NewRequest(http.MethodGet, "/api/assessment/batches/failures?batch_id=99", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.failuresID != 99 {
		t.Fatalf("failuresID = %d, want 99", svc.failuresID)
	}
}

// TestCreateBindsPayload 覆盖核心断言：POST 合法 body 断言 fake 收到
// TargetMode/PeriodStart 等字段且响应含 batch_no。
func TestCreateBindsPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{
		createRes: &service.CreateBatchResult{ID: 1845700000000000003, BatchNo: "B20260913-002", Status: "running", TotalCount: 2},
	}
	r := newBatchRouter(svc)
	body := `{
		"target_mode": "specified",
		"staffs": [{"staff_id": "u1", "staff_name": "张三"}, {"staff_id": "u2", "staff_name": "李四"}],
		"period_start": "2026-09-01",
		"period_end": "2026-09-10"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment/batches/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.createPayload.TargetMode != "specified" {
		t.Fatalf("payload.TargetMode = %q, want specified", svc.createPayload.TargetMode)
	}
	if svc.createPayload.PeriodStart != "2026-09-01" || svc.createPayload.PeriodEnd != "2026-09-10" {
		t.Fatalf("payload period = (%q,%q), want (2026-09-01,2026-09-10)",
			svc.createPayload.PeriodStart, svc.createPayload.PeriodEnd)
	}
	if len(svc.createPayload.Staffs) != 2 || svc.createPayload.Staffs[0].StaffName != "张三" {
		t.Fatalf("payload.Staffs = %+v, want 2 staffs with names", svc.createPayload.Staffs)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			BatchNo string `json:"batch_no"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.BatchNo != "B20260913-002" {
		t.Fatalf("data.batch_no = %q, want B20260913-002", resp.Data.BatchNo)
	}
	if resp.Data.Status != "running" {
		t.Fatalf("data.status = %q, want running", resp.Data.Status)
	}
}

// TestCreateBindingError 覆盖 ShouldBindJSON 失败走 handleServiceError：HTTP 200 + code 1400。
func TestCreateBindingError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentBatchService{}
	r := newBatchRouter(svc)
	for _, body := range []string{
		``,                                             // 空 body
		`{"target_mode": "all"}`,                        // 缺 period_start/period_end
		`{"period_start": "2026-09-01", "period_end": "2026-09-10"}`, // 缺 target_mode
		`{invalid json`,                                 // 非法 JSON
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/assessment/batches/create", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("body=%q status = %d, want 200", body, w.Code)
		}
		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body=%q unmarshal: %v", body, err)
		}
		if resp.Code != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want %d", body, resp.Code, errcode.BadRequest)
		}
	}
}

// TestCreateServiceError 覆盖 service 业务错误透传（如 1305/1602）与内部错误映射。
func TestCreateServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"target_mode": "all", "period_start": "2026-09-01", "period_end": "2026-09-10"}`

	// 业务错误（1602）HTTP 200 带 code
	svc := &fakeAssessmentBatchService{createErr: service.NewError(errcode.BatchPeriodInvalid)}
	r := newBatchRouter(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/assessment/batches/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != errcode.BatchPeriodInvalid {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.BatchPeriodInvalid)
	}

	// 非 service.Error → HTTP 500 + 1500
	svc2 := &fakeAssessmentBatchService{createErr: errors.New("db down")}
	r2 := newBatchRouter(svc2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/assessment/batches/create", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w2.Code)
	}
	var resp2 struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp2.Code != errcode.Internal {
		t.Fatalf("code = %d, want %d", resp2.Code, errcode.Internal)
	}
}
