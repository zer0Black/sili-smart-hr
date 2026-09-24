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

// fakeQuestionService 是 service.QuestionService 的假实现，入参以 last* 字段记录便于断言。
type fakeQuestionService struct {
	listLast service.QuestionListInput
	listRes  *service.QuestionListResult
	listErr  error

	getLastID int64
	getRes    *service.QuestionDetail
	getErr    error

	updateLast service.UpdateQuestionInput
	updateRes  *service.QuestionMutationResult
	updateErr  error

	toggleLastID      int64
	toggleLastStatus  string
	toggleLastVersion int
	toggleRes         *service.QuestionMutationResult
	toggleErr         error

	deleteLastID      int64
	deleteLastVersion int
	deleteErr         error
}

func (f *fakeQuestionService) ListQuestions(_ context.Context, in service.QuestionListInput) (*service.QuestionListResult, error) {
	f.listLast = in
	return f.listRes, f.listErr
}

func (f *fakeQuestionService) GetQuestion(_ context.Context, id int64) (*service.QuestionDetail, error) {
	f.getLastID = id
	return f.getRes, f.getErr
}

func (f *fakeQuestionService) UpdateQuestion(_ context.Context, in service.UpdateQuestionInput) (*service.QuestionMutationResult, error) {
	f.updateLast = in
	return f.updateRes, f.updateErr
}

func (f *fakeQuestionService) ToggleQuestionStatus(_ context.Context, id int64, targetStatus string, version int) (*service.QuestionMutationResult, error) {
	f.toggleLastID, f.toggleLastStatus, f.toggleLastVersion = id, targetStatus, version
	return f.toggleRes, f.toggleErr
}

func (f *fakeQuestionService) DeleteQuestion(_ context.Context, id int64, version int) error {
	f.deleteLastID, f.deleteLastVersion = id, version
	return f.deleteErr
}

var _ service.QuestionService = (*fakeQuestionService)(nil)

func newQuestionRouter(svc *fakeQuestionService) *gin.Engine {
	// dimSvc/batchSvc 本测试未触达（resubmit 归 batch 测试文件），传 nil。
	h := handler.NewQuestionHandler(svc, nil, nil)
	r := gin.New()
	r.GET("/api/questions", h.List)
	r.GET("/api/questions/:id", h.Detail)
	r.POST("/api/questions/update", h.Update)
	r.POST("/api/questions/toggle-status", h.ToggleStatus)
	r.POST("/api/questions/delete", h.Delete)
	return r
}

func doQuestionJSON(t *testing.T, r *gin.Engine, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	return w.Code, resp
}

func codeOf(t *testing.T, resp map[string]any) int {
	t.Helper()
	v, ok := resp["code"].(float64)
	if !ok {
		t.Fatalf("code field missing or not number: %v", resp)
	}
	return int(v)
}

// TestQuestionHandlerListMissingSource 核心断言：GET /api/questions 无 source 返 HTTP 200 + code 1400。
func TestQuestionHandlerListMissingSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerListInvalidSource：source 非 AI/SCALE 返 1400。
func TestQuestionHandlerListInvalidSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions?source=FOO", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerListSuccess：合法 query 透传 service，响应 {list,total,page,page_size}。
func TestQuestionHandlerListSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{
		listRes: &service.QuestionListResult{
			List: []service.QuestionListItem{{
				ID: "1785000000000000101", QuestionNo: "Q-AG-0001", Source: "AI",
				DimensionID: "1780000000000000100", DimensionName: "授权与分工",
				AnswerMode: "CHAT", Status: "ACTIVE", Summary: "下属使用 AI 工具完成季度规划",
			}},
			Total: 32, Page: 2, PageSize: 10,
		},
	}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions?source=AI&dimension_id=1780000000000000100&status=ACTIVE&keyword=%E6%8E%88%E6%9D%83&page=2&page_size=10", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	// 入参透传。
	if svc.listLast.Source != "AI" || svc.listLast.Status != "ACTIVE" || svc.listLast.Keyword != "授权" ||
		svc.listLast.Page != 2 || svc.listLast.PageSize != 10 {
		t.Fatalf("svc.ListQuestions args mismatch: %+v", svc.listLast)
	}
	if !svc.listLast.DimensionIDSet || svc.listLast.DimensionID != 1780000000000000100 {
		t.Fatalf("dimension_id mismatch: set=%v id=%d", svc.listLast.DimensionIDSet, svc.listLast.DimensionID)
	}
	// 响应结构。
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data missing: %v", resp)
	}
	if data["total"].(float64) != 32 || data["page"].(float64) != 2 || data["page_size"].(float64) != 10 {
		t.Fatalf("data pagination mismatch: %v", data)
	}
	list, _ := data["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("data.list len = %d, want 1", len(list))
	}
	row, _ := list[0].(map[string]any)
	if row["id"] != "1785000000000000101" {
		t.Fatalf("data.list[0].id = %v, want string 雪花", row["id"])
	}
}

// TestQuestionHandlerListDefaultPagingAndNoDimension：page/page_size 缺省兜底 1/20，空 dimension_id 不置 Set。
func TestQuestionHandlerListDefaultPagingAndNoDimension(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{listRes: &service.QuestionListResult{}}
	r := newQuestionRouter(svc)

	code, _ := doQuestionJSON(t, r, http.MethodGet, "/api/questions?source=SCALE", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if svc.listLast.Page != 1 || svc.listLast.PageSize != 20 {
		t.Fatalf("page/page_size 兜底失败: %d/%d", svc.listLast.Page, svc.listLast.PageSize)
	}
	if svc.listLast.DimensionIDSet || svc.listLast.DimensionID != 0 {
		t.Fatalf("空 dimension_id 应不置 Set: %+v", svc.listLast)
	}
}

// TestQuestionHandlerListBadDimensionID：dimension_id 非数字返 1400。
func TestQuestionHandlerListBadDimensionID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions?source=AI&dimension_id=abc", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerListMapsError：service 业务错误（1708）透传 code。
func TestQuestionHandlerListMapsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{listErr: service.NewError(errcode.QuestionStatusInvalid)}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions?source=AI", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.QuestionStatusInvalid {
		t.Fatalf("code = %d, want 1708", got)
	}
}

// TestQuestionHandlerDetailBadID 核心断言：/api/questions/abc 返 1400。
func TestQuestionHandlerDetailBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions/abc", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerDetailSuccess：合法 ID 透传并返回详情（含 string 化 ID）。
func TestQuestionHandlerDetailSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{
		getRes: &service.QuestionDetail{
			ID: "1785000000000000101", QuestionNo: "Q-AG-0002", Source: "AI",
			DimensionID: "1780000000000000110", DimensionName: "风险与担责",
			AnswerMode: "CHAT", Status: "REJECTED", Version: 2, ReferenceCount: 3,
		},
	}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions/1785000000000000101", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.getLastID != 1785000000000000101 {
		t.Fatalf("svc.GetQuestion id = %d, want 1785000000000000101", svc.getLastID)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["id"] != "1785000000000000101" {
		t.Fatalf("data.id mismatch: %v", data)
	}
}

// TestQuestionHandlerDetailNotFound：service 返 1701 时透传。
func TestQuestionHandlerDetailNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{getErr: service.NewError(errcode.QuestionNotFound)}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions/1785000000000000999", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.QuestionNotFound {
		t.Fatalf("code = %d, want 1701", got)
	}
}

// TestQuestionHandlerUpdateMapsVersionConflict 核心断言：service 返回 1713 时 body code=1713 且 HTTP 200。
func TestQuestionHandlerUpdateMapsVersionConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{updateErr: service.NewError(errcode.QuestionVersionConflict)}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"情境","requirement":"要求","focus_point":"考察点","version":2}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/update", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.QuestionVersionConflict {
		t.Fatalf("code = %d, want 1713", got)
	}
}

// TestQuestionHandlerUpdateSuccess：合法 body 透传五个字段，响应无 status 字段。
func TestQuestionHandlerUpdateSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{
		updateRes: &service.QuestionMutationResult{ID: "1785000000000000101", Version: 3},
	}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"情境","requirement":"要求","focus_point":"考察点","version":2}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/update", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.updateLast.ID != 1785000000000000101 || svc.updateLast.DimensionID != 1780000000000000110 ||
		svc.updateLast.Version != 2 || svc.updateLast.Scenario != "情境" ||
		svc.updateLast.Requirement != "要求" || svc.updateLast.FocusPoint != "考察点" {
		t.Fatalf("svc.UpdateQuestion args mismatch: %+v", svc.updateLast)
	}
	data, _ := resp["data"].(map[string]any)
	if _, has := data["status"]; has {
		t.Fatalf("update 响应不应含 status 字段: %v", data)
	}
}

// TestQuestionHandlerUpdateBadID：id 非数字返 1400。
func TestQuestionHandlerUpdateBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	body := `{"id":"abc","dimension_id":"1780000000000000110","scenario":"情境","requirement":"要求","focus_point":"考察点","version":2}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/update", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerUpdateMissingRequired：缺必填字段（version）binding 失败返 1400。
func TestQuestionHandlerUpdateMissingRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"情境","requirement":"要求","focus_point":"考察点"}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/update", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerUpdateMalformedJSON：非法 JSON 返 1400。
func TestQuestionHandlerUpdateMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/update", `{not-json`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerToggleStatusSuccess：id/target_status/version 透传，响应含新状态。
func TestQuestionHandlerToggleStatusSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{
		toggleRes: &service.QuestionMutationResult{ID: "1785000000000000101", Status: "DISABLED", Version: 4},
	}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000101","target_status":"DISABLED","version":3}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/toggle-status", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.toggleLastID != 1785000000000000101 || svc.toggleLastStatus != "DISABLED" || svc.toggleLastVersion != 3 {
		t.Fatalf("svc.ToggleQuestionStatus args mismatch: id=%d status=%s version=%d",
			svc.toggleLastID, svc.toggleLastStatus, svc.toggleLastVersion)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["status"] != "DISABLED" {
		t.Fatalf("data.status mismatch: %v", data)
	}
}

// TestQuestionHandlerToggleStatusMissingTarget：缺 target_status 返 1400。
func TestQuestionHandlerToggleStatusMissingTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/toggle-status", `{"id":"1785000000000000101","version":3}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionHandlerToggleStatusMapsNotEditable：service 返 1707 时透传。
func TestQuestionHandlerToggleStatusMapsNotEditable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{toggleErr: service.NewError(errcode.QuestionNotEditable)}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000101","target_status":"ACTIVE","version":3}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/toggle-status", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.QuestionNotEditable {
		t.Fatalf("code = %d, want 1707", got)
	}
}

// TestQuestionHandlerDeleteSuccess：id/version 透传，响应 data 为 null。
func TestQuestionHandlerDeleteSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	body := `{"id":"1785000000000000104","version":5}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/delete", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.deleteLastID != 1785000000000000104 || svc.deleteLastVersion != 5 {
		t.Fatalf("svc.DeleteQuestion args mismatch: id=%d version=%d", svc.deleteLastID, svc.deleteLastVersion)
	}
	if resp["data"] != nil {
		t.Fatalf("data should be null, got %v", resp["data"])
	}
}

// TestQuestionHandlerDeleteMapsReferenced：service 返 1705（被引用拒删）时透传。
func TestQuestionHandlerDeleteMapsReferenced(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{deleteErr: service.NewError(errcode.QuestionReferenced)}
	r := newQuestionRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/delete", `{"id":"1785000000000000104","version":5}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.QuestionReferenced {
		t.Fatalf("code = %d, want 1705", got)
	}
}

// TestQuestionHandlerDeleteBadBody：缺 version 与非法 id 双路径均 1400。
func TestQuestionHandlerDeleteBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{}
	r := newQuestionRouter(svc)

	for _, body := range []string{`{"id":"abc","version":5}`, `{"id":"1785000000000000104"}`} {
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/delete", body)
		if code != http.StatusOK {
			t.Fatalf("body=%s status = %d, want 200", body, code)
		}
		if got := codeOf(t, resp); got != errcode.BadRequest {
			t.Fatalf("body=%s code = %d, want 1400", body, got)
		}
	}
}

// TestQuestionHandlerRouteCoexistence：/questions 静态路由与 /questions/:id 参数路由共存不冲突。
func TestQuestionHandlerRouteCoexistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionService{
		getRes: &service.QuestionDetail{ID: "1785000000000000101"},
	}
	r := newQuestionRouter(svc)

	// 静态路径命中 List（source 缺失走 1400，证明进的是 List 而非 Detail 的 parseID）。
	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/questions", "")
	if code != http.StatusOK || codeOf(t, resp) != errcode.BadRequest {
		t.Fatalf("GET /api/questions 应命中 List 并返 1400: code=%d resp=%v", code, resp)
	}
	// 参数路径命中 Detail。
	code, resp = doQuestionJSON(t, r, http.MethodGet, "/api/questions/1785000000000000101", "")
	if code != http.StatusOK || codeOf(t, resp) != 0 {
		t.Fatalf("GET /api/questions/:id 应命中 Detail: code=%d resp=%v", code, resp)
	}
	if svc.getLastID != 1785000000000000101 {
		t.Fatalf("Detail id = %d", svc.getLastID)
	}
}
