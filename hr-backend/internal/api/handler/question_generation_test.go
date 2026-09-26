package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// fakeQuestionGenerationService 是 service.QuestionGenerationService 的假实现。
type fakeQuestionGenerationService struct {
	createLastDims  []int64
	createLastCount int
	createRes       *service.CreateGenerationDTO
	createErr       error

	progressLastID int64
	progressRes    *service.GenerationProgressDTO
	progressErr    error

	cancelLastID int64
	cancelErr    error
}

func (f *fakeQuestionGenerationService) CreateGeneration(_ context.Context, dimensionIDs []int64, count int) (*service.CreateGenerationDTO, error) {
	f.createLastDims = dimensionIDs
	f.createLastCount = count
	return f.createRes, f.createErr
}

func (f *fakeQuestionGenerationService) GetProgress(_ context.Context, id int64) (*service.GenerationProgressDTO, error) {
	f.progressLastID = id
	return f.progressRes, f.progressErr
}

func (f *fakeQuestionGenerationService) CancelGeneration(_ context.Context, id int64) error {
	f.cancelLastID = id
	return f.cancelErr
}

var _ service.QuestionGenerationService = (*fakeQuestionGenerationService)(nil)

// newQuestionGenerationRouter 构造生成三路由测试引擎。
func newQuestionGenerationRouter(svc *fakeQuestionGenerationService) *gin.Engine {
	h := handler.NewQuestionGenerationHandler(svc)
	r := gin.New()
	r.POST("/api/question-generations/create", h.Create)
	r.GET("/api/question-generations/:id", h.Progress)
	r.POST("/api/question-generations/:id/cancel", h.Cancel)
	return r
}

// TestGenerationCreateSuccess：发起响应透传 generation_id 与 status，dimension_ids
// string 逐项经 parseID 转整型、count 透传。
func TestGenerationCreateSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{
		createRes: &service.CreateGenerationDTO{GenerationID: "1785000000000003001", Status: "QUEUED"},
	}
	r := newQuestionGenerationRouter(svc)

	body := `{"dimension_ids":["1780000000000000100","1780000000000000110"],"count":12}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/create", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if len(svc.createLastDims) != 2 || svc.createLastDims[0] != 1780000000000000100 ||
		svc.createLastDims[1] != 1780000000000000110 || svc.createLastCount != 12 {
		t.Fatalf("svc.CreateGeneration args mismatch: %+v", svc.createLastDims)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["generation_id"] != "1785000000000003001" || data["status"] != "QUEUED" {
		t.Fatalf("data mismatch: %v", data)
	}
}

// TestGenerationCreateBadBody：非法 JSON / 缺必填 / count 非数字 / dimension_ids
// 含非数字项均 1400。
func TestGenerationCreateBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{not-json`,
		`{"dimension_ids":["1780000000000000100"]}`,
		`{"count":12}`,
		`{"dimension_ids":["1780000000000000100"],"count":"12"}`,
		`{"dimension_ids":["abc"],"count":12}`,
		`{"dimension_ids":[178],"count":12}`, // 元素须为 string（雪花精度约定）
	} {
		svc := &fakeQuestionGenerationService{}
		r := newQuestionGenerationRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/create", body)
		if code != http.StatusOK {
			t.Fatalf("body=%s status = %d, want 200", body, code)
		}
		if got := codeOf(t, resp); got != errcode.BadRequest {
			t.Fatalf("body=%s code = %d, want 1400", body, got)
		}
		if svc.createLastDims != nil {
			t.Fatalf("body=%s 不应触达 service", body)
		}
	}
}

// TestGenerationCreateMapsErrors：service 业务错误（1400/1709/1500）透传 code。
func TestGenerationCreateMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.BadRequest), errcode.BadRequest},
		{service.NewError(errcode.LLMNotConfigured), errcode.LLMNotConfigured},
		{service.NewError(errcode.Internal), errcode.Internal},
	} {
		svc := &fakeQuestionGenerationService{createErr: tc.err}
		r := newQuestionGenerationRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/create",
			`{"dimension_ids":["1780000000000000100"],"count":12}`)
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}

// TestGenerationProgressSuccess：轮询响应字段组装（RUNNING 带维度名回显），
// 路径 ID 解析透传。
func TestGenerationProgressSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{
		progressRes: &service.GenerationProgressDTO{
			GenerationID: "1785000000000003001", Status: "RUNNING",
			GeneratedCount: 6, Count: 12,
			CurrentDimensionID: "1780000000000000100", CurrentDimensionName: "授权与分工",
		},
	}
	r := newQuestionGenerationRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-generations/1785000000000003001", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.progressLastID != 1785000000000003001 {
		t.Fatalf("progressLastID = %d", svc.progressLastID)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["status"] != "RUNNING" ||
		data["generated_count"].(float64) != 6 || data["count"].(float64) != 12 ||
		data["current_dimension_id"] != "1780000000000000100" || data["current_dimension_name"] != "授权与分工" {
		t.Fatalf("data mismatch: %v", data)
	}
}

// TestGenerationProgressCompletedOmitempty：COMPLETED 无 error_code 字段（omitempty）。
func TestGenerationProgressCompletedOmitempty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{
		progressRes: &service.GenerationProgressDTO{
			GenerationID: "3001", Status: "COMPLETED",
			GeneratedCount: 12, Count: 12,
			CurrentDimensionID: "201", BatchID: "901", BatchNo: "#G0925",
		},
	}
	r := newQuestionGenerationRouter(svc)

	_, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-generations/3001", "")
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatal("data missing")
	}
	if _, ok := data["error_code"]; ok {
		t.Fatalf("COMPLETED 不应携带 error_code: %v", data)
	}
	if data["batch_id"] != "901" || data["batch_no"] != "#G0925" {
		t.Fatalf("batch fields mismatch: %v", data)
	}
}

// TestGenerationProgressBadID：路径 ID 非数字返 1400。
func TestGenerationProgressBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{}
	r := newQuestionGenerationRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-generations/abc", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestGenerationProgressMapsErrors：service 返 1703 透传。
func TestGenerationProgressMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{progressErr: service.NewError(errcode.GenerationNotFound)}
	r := newQuestionGenerationRouter(svc)

	_, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-generations/1785000000000003001", "")
	if got := codeOf(t, resp); got != errcode.GenerationNotFound {
		t.Fatalf("code = %d, want 1703", got)
	}
}

// TestGenerationCancelSuccess：无请求体取消成功，data 为 null。
func TestGenerationCancelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{}
	r := newQuestionGenerationRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/1785000000000003001/cancel", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.cancelLastID != 1785000000000003001 {
		t.Fatalf("cancelLastID = %d", svc.cancelLastID)
	}
	if resp["data"] != nil {
		t.Fatalf("data should be null, got %v", resp["data"])
	}
}

// TestGenerationCancelBadID：路径 ID 非数字返 1400。
func TestGenerationCancelBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{}
	r := newQuestionGenerationRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/xyz/cancel", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestGenerationCancelMapsErrors：service 返 1703 透传。
func TestGenerationCancelMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionGenerationService{cancelErr: service.NewError(errcode.GenerationNotFound)}
	r := newQuestionGenerationRouter(svc)

	_, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-generations/1785000000000003001/cancel", "")
	if got := codeOf(t, resp); got != errcode.GenerationNotFound {
		t.Fatalf("code = %d, want 1703", got)
	}
}
