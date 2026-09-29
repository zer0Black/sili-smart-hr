// Package handler 对主动测试域 handler 做黑盒测试。用内部包（而非 handler_test）
// 是为与既有批次 handler 测试（TestCreateBindingError 等同名锚点）隔离，本任务
// 验收锚点的测试名按计划原样保留。
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// fakeAssessmentTestTaskService 是 service.AssessmentTestTaskService 的假实现。
// List 内联复刻 service 层枚举校验（test_type 必填二值、status 五值或空），
// 对齐真实行为使 test_type 缺失路径可断言（03 A1）。
type fakeAssessmentTestTaskService struct {
	listFilter service.ListTestTaskFilter
	listRes    []service.TestTaskListDTO
	listTotal  int64

	pollCountsRes *service.TestTaskPollCountsDTO
	pollCountsErr error

	createPayload service.CreateTestTaskPayload
	createRes     *service.CreateTestTaskResult
	createErr     error

	scaleStatusRes *service.TestScaleStatusDTO
	scaleStatusErr error

	linkID  int64
	linkRes *service.TestTaskLinkDTO
	linkErr error

	resendID  int64
	resendRes *service.TestTaskLinkDTO
	resendErr error

	cancelID  int64
	cancelRes *service.TestTaskCancelDTO
	cancelErr error
}

func validFakeTestType(v string) bool { return v == "ai_mgmt" || v == "enneagram" }

func validFakeTestStatus(v string) bool {
	switch v {
	case "", "pending", "in_progress", "completed", "expired", "canceled":
		return true
	}
	return false
}

func (f *fakeAssessmentTestTaskService) List(_ context.Context, flt service.ListTestTaskFilter) ([]service.TestTaskListDTO, int64, error) {
	if !validFakeTestType(flt.TestType) || !validFakeTestStatus(flt.Status) {
		return nil, 0, service.NewError(errcode.BadRequest)
	}
	f.listFilter = flt
	return f.listRes, f.listTotal, nil
}

func (f *fakeAssessmentTestTaskService) PollCounts(_ context.Context) (*service.TestTaskPollCountsDTO, error) {
	return f.pollCountsRes, f.pollCountsErr
}

func (f *fakeAssessmentTestTaskService) Create(_ context.Context, p service.CreateTestTaskPayload) (*service.CreateTestTaskResult, error) {
	f.createPayload = p
	return f.createRes, f.createErr
}

func (f *fakeAssessmentTestTaskService) ScaleStatus(_ context.Context) (*service.TestScaleStatusDTO, error) {
	return f.scaleStatusRes, f.scaleStatusErr
}

func (f *fakeAssessmentTestTaskService) Link(_ context.Context, taskID int64) (*service.TestTaskLinkDTO, error) {
	f.linkID = taskID
	return f.linkRes, f.linkErr
}

func (f *fakeAssessmentTestTaskService) Resend(_ context.Context, taskID int64) (*service.TestTaskLinkDTO, error) {
	f.resendID = taskID
	return f.resendRes, f.resendErr
}

func (f *fakeAssessmentTestTaskService) Cancel(_ context.Context, taskID int64) (*service.TestTaskCancelDTO, error) {
	f.cancelID = taskID
	return f.cancelRes, f.cancelErr
}

// CompleteTask/StartSession 无 HTTP 面（03 §1.3/§1.5），测试桩恒 nil 仅保接口完整。
func (f *fakeAssessmentTestTaskService) CompleteTask(_ context.Context, _ int64) error { return nil }
func (f *fakeAssessmentTestTaskService) StartSession(_ context.Context, _ int64) error { return nil }

var _ service.AssessmentTestTaskService = (*fakeAssessmentTestTaskService)(nil)

// newTestTaskRouter 挂载与 router.go 相同的七条路径（03 §3 A1/A2/B1/B2/C1/C2/C3）。
func newTestTaskRouter(svc *fakeAssessmentTestTaskService) *gin.Engine {
	h := NewAssessmentTestTaskHandler(svc)
	r := gin.New()
	r.GET("/api/assessment/test-tasks", h.List)
	r.GET("/api/assessment/test-tasks/poll-counts", h.PollCounts)
	r.GET("/api/assessment/test-tasks/scale-status", h.ScaleStatus)
	r.GET("/api/assessment/test-tasks/link", h.Link)
	r.POST("/api/assessment/test-tasks/create", h.Create)
	r.POST("/api/assessment/test-tasks/resend", h.Resend)
	r.POST("/api/assessment/test-tasks/cancel", h.Cancel)
	return r
}

// doReq 执行请求并断言 HTTP 200，返回解包后的 code。
func doReq(t *testing.T, r *gin.Engine, method, url, body string) int {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, url, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, want 200, body=%s", method, url, w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s %s unmarshal: %v, body=%s", method, url, err, w.Body.String())
	}
	return resp.Code
}

// TestRoutesRegistered 核心断言：七路径全部可路由（非 404），GET 200 带 code。
func TestRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{
		listRes:        []service.TestTaskListDTO{},
		pollCountsRes:  &service.TestTaskPollCountsDTO{},
		scaleStatusRes: &service.TestScaleStatusDTO{},
	}
	r := newTestTaskRouter(svc)
	gets := []string{
		"/api/assessment/test-tasks?test_type=ai_mgmt",
		"/api/assessment/test-tasks/poll-counts",
		"/api/assessment/test-tasks/scale-status",
	}
	for _, url := range gets {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("%s 返回 404，路由未注册", url)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", url, w.Code)
		}
		var resp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s unmarshal: %v", url, err)
		}
		if resp.Code != 0 {
			t.Fatalf("%s code = %d, want 0", url, resp.Code)
		}
	}
	posts := []struct{ url, body string }{
		{"/api/assessment/test-tasks/create", `{"test_type":"enneagram","staff_id":"9001","staff_name":"张敏"}`},
		{"/api/assessment/test-tasks/resend", `{"task_id":"1790000000000000001"}`},
		{"/api/assessment/test-tasks/cancel", `{"task_id":"1790000000000000001"}`},
	}
	for _, p := range posts {
		req := httptest.NewRequest(http.MethodPost, p.url, strings.NewReader(p.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("%s 返回 404，路由未注册", p.url)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", p.url, w.Code, w.Body.String())
		}
	}
}

// TestListRequiresTestType 核心断言：GET /api/assessment/test-tasks 缺 test_type 返 code 1400。
func TestListRequiresTestType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, url := range []string{
		"/api/assessment/test-tasks",
		"/api/assessment/test-tasks?test_type=",
		"/api/assessment/test-tasks?test_type=bogus",
	} {
		svc := &fakeAssessmentTestTaskService{}
		r := newTestTaskRouter(svc)
		if code := doReq(t, r, http.MethodGet, url, ""); code != errcode.BadRequest {
			t.Fatalf("%s code = %d, want %d", url, code, errcode.BadRequest)
		}
	}
}

// TestListBindsQuery 覆盖查询参数透传与分页兜底（同批次 List 范式）。
func TestListBindsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{listRes: []service.TestTaskListDTO{
		{ID: 1790000000000000001, TaskNo: "T202609280001", TestType: "ai_mgmt", StaffName: "张敏", Status: "pending"},
	}}
	r := newTestTaskRouter(svc)
	url := "/api/assessment/test-tasks?test_type=ai_mgmt&status=pending&keyword=张&page=2&page_size=50"
	code := doReq(t, r, http.MethodGet, url, "")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if svc.listFilter.TestType != "ai_mgmt" || svc.listFilter.Status != "pending" || svc.listFilter.Keyword != "张" {
		t.Fatalf("filter = %+v", svc.listFilter)
	}
	if svc.listFilter.Page != 2 || svc.listFilter.PageSize != 50 {
		t.Fatalf("page = (%d,%d), want (2,50)", svc.listFilter.Page, svc.listFilter.PageSize)
	}

	// 兜底：非法分页值回退 1/10，>100 钳 100。
	svc2 := &fakeAssessmentTestTaskService{listRes: []service.TestTaskListDTO{}}
	r2 := newTestTaskRouter(svc2)
	doReq(t, r2, http.MethodGet, "/api/assessment/test-tasks?test_type=enneagram&page=abc&page_size=-1", "")
	if svc2.listFilter.Page != 1 || svc2.listFilter.PageSize != 10 {
		t.Fatalf("fallback page = (%d,%d), want (1,10)", svc2.listFilter.Page, svc2.listFilter.PageSize)
	}
	svc3 := &fakeAssessmentTestTaskService{listRes: []service.TestTaskListDTO{}}
	r3 := newTestTaskRouter(svc3)
	doReq(t, r3, http.MethodGet, "/api/assessment/test-tasks?test_type=enneagram&page_size=200", "")
	if svc3.listFilter.PageSize != 100 {
		t.Fatalf("clamped page_size = %d, want 100", svc3.listFilter.PageSize)
	}
}

// TestPollCountsRoute 覆盖 A2 计数直出。
func TestPollCountsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{
		pollCountsRes: &service.TestTaskPollCountsDTO{AIMgmtActive: 3, EnneagramActive: 1},
	}
	r := newTestTaskRouter(svc)
	code := doReq(t, r, http.MethodGet, "/api/assessment/test-tasks/poll-counts", "")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

// TestScaleStatusRoute 覆盖 B2 就绪查询直出。
func TestScaleStatusRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{
		scaleStatusRes: &service.TestScaleStatusDTO{Ready: true, ScaleKey: "RISO_HUDSON"},
	}
	r := newTestTaskRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/api/assessment/test-tasks/scale-status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Ready    bool   `json:"ready"`
			ScaleKey string `json:"scale_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 || !resp.Data.Ready || resp.Data.ScaleKey != "RISO_HUDSON" {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestCreateBindingError 核心断言：POST create 缺 staff_name 等必填返 1400。
func TestCreateBindingError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		``,                                                    // 空 body
		`{"staff_id":"9001","staff_name":"张敏"}`,               // 缺 test_type
		`{"test_type":"ai_mgmt","staff_name":"张敏"}`,           // 缺 staff_id
		`{"test_type":"ai_mgmt","staff_id":"9001"}`,            // 缺 staff_name
		`{invalid json`,                                       // 非法 JSON
	} {
		svc := &fakeAssessmentTestTaskService{}
		r := newTestTaskRouter(svc)
		if code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/create", body); code != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want %d", body, code, errcode.BadRequest)
		}
	}
}

// TestCreateBindsPayload 覆盖合法请求透传 dimension_ids 与响应组装。
func TestCreateBindsPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{
		createRes: &service.CreateTestTaskResult{
			ID: 1790000000000000001, TaskNo: "T202609280001", TestType: "ai_mgmt",
			StaffName: "张敏", Status: "pending", AnswerURL: "/answer/tok",
		},
	}
	r := newTestTaskRouter(svc)
	body := `{"test_type":"ai_mgmt","staff_id":"9001","staff_name":"张敏","dimension_ids":["1780000000000000100","1780000000000000101"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment/test-tasks/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.createPayload.TestType != "ai_mgmt" || svc.createPayload.StaffID != "9001" || svc.createPayload.StaffName != "张敏" {
		t.Fatalf("payload = %+v", svc.createPayload)
	}
	if len(svc.createPayload.DimensionIDs) != 2 {
		t.Fatalf("dimension_ids len = %d, want 2", len(svc.createPayload.DimensionIDs))
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID     string `json:"id"`
			TaskNo string `json:"task_no"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 || resp.Data.ID != "1790000000000000001" || resp.Data.TaskNo != "T202609280001" {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestCreateServiceError 覆盖 service 业务错误（1803）与非 service.Error（1500/500）映射。
func TestCreateServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := `{"test_type":"ai_mgmt","staff_id":"9001","staff_name":"张敏","dimension_ids":["1780000000000000100"]}`

	svc := &fakeAssessmentTestTaskService{createErr: service.NewError(errcode.TestDimensionQuestionsEmpty)}
	r := newTestTaskRouter(svc)
	if code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/create", body); code != errcode.TestDimensionQuestionsEmpty {
		t.Fatalf("code = %d, want %d", code, errcode.TestDimensionQuestionsEmpty)
	}

	svc2 := &fakeAssessmentTestTaskService{createErr: errors.New("db down")}
	r2 := newTestTaskRouter(svc2)
	req := httptest.NewRequest(http.MethodPost, "/api/assessment/test-tasks/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r2.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// TestLinkTaskID 覆盖 C1 task_id 校验：缺失/非法 1400，合法透传。
func TestLinkTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, url := range []string{
		"/api/assessment/test-tasks/link",
		"/api/assessment/test-tasks/link?task_id=",
		"/api/assessment/test-tasks/link?task_id=abc",
		"/api/assessment/test-tasks/link?task_id=0",
		"/api/assessment/test-tasks/link?task_id=-5",
	} {
		svc := &fakeAssessmentTestTaskService{}
		r := newTestTaskRouter(svc)
		if code := doReq(t, r, http.MethodGet, url, ""); code != errcode.BadRequest {
			t.Fatalf("%s code = %d, want %d", url, code, errcode.BadRequest)
		}
	}

	svc := &fakeAssessmentTestTaskService{
		linkRes: &service.TestTaskLinkDTO{TaskID: 1790000000000000001, TaskNo: "T202609280001", LinkStatus: "valid"},
	}
	r := newTestTaskRouter(svc)
	doReq(t, r, http.MethodGet, "/api/assessment/test-tasks/link?task_id=1790000000000000001", "")
	if svc.linkID != 1790000000000000001 {
		t.Fatalf("linkID = %d, want 1790000000000000001", svc.linkID)
	}
}

// TestResendTaskID 覆盖 C2：body task_id 缺失/非法 1400，合法解析透传。
func TestResendTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{``, `{"task_id":""}`, `{invalid`, `{"task_id":"abc"}`, `{"task_id":"0"}`} {
		svc := &fakeAssessmentTestTaskService{}
		r := newTestTaskRouter(svc)
		if code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/resend", body); code != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want %d", body, code, errcode.BadRequest)
		}
	}

	svc := &fakeAssessmentTestTaskService{
		resendRes: &service.TestTaskLinkDTO{TaskID: 1790000000000000001, LinkStatus: "valid"},
	}
	r := newTestTaskRouter(svc)
	code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/resend", `{"task_id":"1790000000000000001"}`)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if svc.resendID != 1790000000000000001 {
		t.Fatalf("resendID = %d, want 1790000000000000001", svc.resendID)
	}
}

// TestResendServiceError 覆盖 1802 状态不允许重发透传。
func TestResendServiceError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentTestTaskService{resendErr: service.NewError(errcode.TestTaskStatusInvalid)}
	r := newTestTaskRouter(svc)
	code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/resend", `{"task_id":"1790000000000000001"}`)
	if code != errcode.TestTaskStatusInvalid {
		t.Fatalf("code = %d, want %d", code, errcode.TestTaskStatusInvalid)
	}
}

// TestCancelTaskID 覆盖 C3：task_id 校验、解析透传与 1801 业务错误。
func TestCancelTaskID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{``, `{"task_id":"xyz"}`, `{"task_id":"-1"}`} {
		svc := &fakeAssessmentTestTaskService{}
		r := newTestTaskRouter(svc)
		if code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/cancel", body); code != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want %d", body, code, errcode.BadRequest)
		}
	}

	svc := &fakeAssessmentTestTaskService{
		cancelRes: &service.TestTaskCancelDTO{TaskID: 1790000000000000001, Status: "canceled"},
	}
	r := newTestTaskRouter(svc)
	code := doReq(t, r, http.MethodPost, "/api/assessment/test-tasks/cancel", `{"task_id":"1790000000000000001"}`)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if svc.cancelID != 1790000000000000001 {
		t.Fatalf("cancelID = %d, want 1790000000000000001", svc.cancelID)
	}

	svc2 := &fakeAssessmentTestTaskService{cancelErr: service.NewError(errcode.TestTaskNotFound)}
	r2 := newTestTaskRouter(svc2)
	if code := doReq(t, r2, http.MethodPost, "/api/assessment/test-tasks/cancel", `{"task_id":"1"}`); code != errcode.TestTaskNotFound {
		t.Fatalf("code = %d, want %d", code, errcode.TestTaskNotFound)
	}
}
