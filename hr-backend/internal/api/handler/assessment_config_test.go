package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// fakeAssessmentConfigService 是 service.AssessmentConfigService 的假实现，承载可配置返回值。
// 入参以 last* 字段记录便于断言。三方法分别对应 Get / Save / ListStaffs 三个 handler 路径。
type fakeAssessmentConfigService struct {
	// Get
	getRes *service.AssessmentConfigDTO
	getErr error

	// Save
	saveLast struct {
		Period, TriggerTime, TargetMode string
		Members                         []service.StaffDTO
		Version                         int
	}
	saveRes *service.SaveAssessmentResult
	saveErr error

	// ListStaffs
	listLast struct {
		Keyword        string
		Page, PageSize int
	}
	listRes   []service.StaffDTO
	listTotal int64
	listErr   error
}

func (f *fakeAssessmentConfigService) Get(_ context.Context) (*service.AssessmentConfigDTO, error) {
	return f.getRes, f.getErr
}

func (f *fakeAssessmentConfigService) Save(
	_ context.Context,
	period, triggerTime, targetMode string,
	members []service.StaffDTO,
	version int,
) (*service.SaveAssessmentResult, error) {
	f.saveLast.Period = period
	f.saveLast.TriggerTime = triggerTime
	f.saveLast.TargetMode = targetMode
	f.saveLast.Members = members
	f.saveLast.Version = version
	return f.saveRes, f.saveErr
}

func (f *fakeAssessmentConfigService) ListStaffs(_ context.Context, keyword string, page, pageSize int) ([]service.StaffDTO, int64, error) {
	f.listLast.Keyword = keyword
	f.listLast.Page = page
	f.listLast.PageSize = pageSize
	return f.listRes, f.listTotal, f.listErr
}

var _ service.AssessmentConfigService = (*fakeAssessmentConfigService)(nil)

func newAssessmentConfigHandler(svc *fakeAssessmentConfigService) *handler.AssessmentConfigHandler {
	return handler.NewAssessmentConfigHandler(svc)
}

// TestAssessmentConfig_Get 覆盖 BR1：GET /api/assessment-config 返 HTTP 200、code==0、data.period=="weekly"。
func TestAssessmentConfig_Get(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		getRes: &service.AssessmentConfigDTO{
			ID:               1845700000000000042,
			Period:           "weekly",
			TriggerTime:      "23:00",
			TargetMode:       "all",
			SpecifiedMembers: []service.StaffDTO{},
			Version:          3,
		},
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.GET("/api/assessment-config", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/assessment-config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID      string `json:"id"`
			Period  string `json:"period"`
			Version int    `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Period != "weekly" {
		t.Fatalf("data.period = %q, want %q", resp.Data.Period, "weekly")
	}
	// 雪花 ID 必须 string 化（BR2）。
	if resp.Data.ID != "1845700000000000042" {
		t.Fatalf("data.id = %q, want %q (雪花 ID 应 string 化)", resp.Data.ID, "1845700000000000042")
	}
}

// TestAssessmentConfig_Save_Success 覆盖核心断言：合法 body 返 200、code==0、data.version==原+1。
func TestAssessmentConfig_Save_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		saveRes: &service.SaveAssessmentResult{ID: 1845700000000000042, Version: 4},
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.POST("/api/assessment-config/save", h.Save)

	body := `{"period":"weekly","trigger_time":"23:00","target_mode":"all","version":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment-config/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Version != 4 {
		t.Fatalf("data.version = %d, want 4 (原+1)", resp.Data.Version)
	}
	// 入参透传到 service 层。
	if svc.saveLast.Period != "weekly" || svc.saveLast.TriggerTime != "23:00" ||
		svc.saveLast.TargetMode != "all" || svc.saveLast.Version != 3 {
		t.Fatalf("svc.Save args mismatch: %+v", svc.saveLast)
	}
}

// TestAssessmentConfig_Save_SpecifiedMembers 验证 specified_members 切片透传到 service 层转 StaffDTO。
func TestAssessmentConfig_Save_SpecifiedMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		saveRes: &service.SaveAssessmentResult{ID: 1, Version: 2},
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.POST("/api/assessment-config/save", h.Save)

	body := `{"period":"weekly","trigger_time":"23:00","target_mode":"specified","specified_members":[{"staff_id":"u1","staff_name":"张三"}],"version":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment-config/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if len(svc.saveLast.Members) != 1 {
		t.Fatalf("svc.Save members len = %d, want 1", len(svc.saveLast.Members))
	}
	if svc.saveLast.Members[0].StaffID != "u1" || svc.saveLast.Members[0].StaffName != "张三" {
		t.Fatalf("svc.Save members[0] = %+v", svc.saveLast.Members[0])
	}
}

// TestAssessmentConfig_Save_MissingRequired 覆盖核心断言：缺 period 字段返 HTTP 200、code==1400。
// 与 account handler 不同：binding 失败走 handleServiceError 映射，统一 HTTP 200 带 code。
func TestAssessmentConfig_Save_MissingRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.POST("/api/assessment-config/save", h.Save)

	// 缺 period/trigger_time/target_mode。
	body := `{"version":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment-config/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (binding 失败统一 200)", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", resp.Code)
	}
}

// TestAssessmentConfig_Save_VersionConflict 验证 service 返 ConfigVersionConflict(1306) 时 handler 正确映射。
func TestAssessmentConfig_Save_VersionConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		saveErr: service.NewError(errcode.ConfigVersionConflict),
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.POST("/api/assessment-config/save", h.Save)

	body := `{"period":"weekly","trigger_time":"23:00","target_mode":"all","version":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/assessment-config/save", strings.NewReader(body))
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
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.ConfigVersionConflict {
		t.Fatalf("code = %d, want 1306", resp.Code)
	}
}

// TestAssessmentConfig_Staffs 覆盖核心断言：GET /api/staffs?keyword=张&page=1&page_size=20 返 200、code==0，
// data.list 与 data.total 结构正确。
func TestAssessmentConfig_Staffs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		listRes: []service.StaffDTO{
			{StaffID: "u1", StaffName: "张三"},
			{StaffID: "u2", StaffName: "张四"},
		},
		listTotal: 2,
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.GET("/api/staffs", h.Staffs)

	req := httptest.NewRequest(http.MethodGet, "/api/staffs?keyword=张&page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List     []service.StaffDTO `json:"list"`
			Total    int64              `json:"total"`
			Page     int                `json:"page"`
			PageSize int                `json:"page_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if len(resp.Data.List) != 2 {
		t.Fatalf("data.list len = %d, want 2", len(resp.Data.List))
	}
	if resp.Data.Total != 2 {
		t.Fatalf("data.total = %d, want 2", resp.Data.Total)
	}
	if resp.Data.Page != 1 || resp.Data.PageSize != 20 {
		t.Fatalf("data.page/page_size = %d/%d, want 1/20", resp.Data.Page, resp.Data.PageSize)
	}
	// query 透传到 service 层。
	if svc.listLast.Keyword != "张" || svc.listLast.Page != 1 || svc.listLast.PageSize != 20 {
		t.Fatalf("svc.ListStaffs args mismatch: %+v", svc.listLast)
	}
}

// TestAssessmentConfig_Staffs_DefaultPaging 验证 page/page_size 缺失时兜底 1/20。
func TestAssessmentConfig_Staffs_DefaultPaging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.GET("/api/staffs", h.Staffs)

	req := httptest.NewRequest(http.MethodGet, "/api/staffs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.listLast.Page != 1 || svc.listLast.PageSize != 20 {
		t.Fatalf("page/page_size 兜底失败: page=%d page_size=%d, want 1/20",
			svc.listLast.Page, svc.listLast.PageSize)
	}
}

// TestAssessmentConfig_Staffs_Unavailable 验证 service 返 StaffListUnavailable(1305) 时 handler 正确映射。
func TestAssessmentConfig_Staffs_Unavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAssessmentConfigService{
		listErr: service.NewError(errcode.StaffListUnavailable),
	}
	h := newAssessmentConfigHandler(svc)
	r := gin.New()
	r.GET("/api/staffs", h.Staffs)

	req := httptest.NewRequest(http.MethodGet, "/api/staffs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.StaffListUnavailable {
		t.Fatalf("code = %d, want 1305", resp.Code)
	}
}
