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

// fakeScaleService 是 service.ScaleService 的假实现，入参以 last* 字段记录便于断言。
type fakeScaleService struct {
	listRes []service.ScaleCandidateDTO
	listErr error

	importLast string
	importRes  *service.ImportScaleResult
	importErr  error
}

func (f *fakeScaleService) ListScales(_ context.Context) ([]service.ScaleCandidateDTO, error) {
	return f.listRes, f.listErr
}

func (f *fakeScaleService) ImportScale(_ context.Context, scaleKey string) (*service.ImportScaleResult, error) {
	f.importLast = scaleKey
	return f.importRes, f.importErr
}

var _ service.ScaleService = (*fakeScaleService)(nil)

func newScaleRouter(svc *fakeScaleService) *gin.Engine {
	h := handler.NewScaleHandler(svc)
	r := gin.New()
	r.GET("/api/scales", h.List)
	r.POST("/api/scales/import", h.Import)
	return r
}

// TestScaleListSuccess：响应 data 为 {list}（03 §3.11 无 total），字段 json tag 对齐。
func TestScaleListSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeScaleService{
		listRes: []service.ScaleCandidateDTO{{
			ScaleKey: "RISO_HUDSON", Name: "Riso-Hudson 标准量表",
			QuestionCount: 144, EstimatedMinutes: 25,
			Description: "主流九型量表，覆盖 9 型别全维度题项", Imported: true,
		}},
	}
	r := newScaleRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/scales", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data missing: %v", resp)
	}
	if _, hasTotal := data["total"]; hasTotal {
		t.Fatalf("data.total should be absent per 03 §3.11, got %v", data["total"])
	}
	list, _ := data["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("data.list len = %d, want 1", len(list))
	}
	row, _ := list[0].(map[string]any)
	if row["scale_key"] != "RISO_HUDSON" || row["name"] != "Riso-Hudson 标准量表" ||
		row["question_count"].(float64) != 144 || row["estimated_minutes"].(float64) != 25 ||
		row["imported"] != true {
		t.Fatalf("data.list[0] mismatch: %v", row)
	}
}

// TestScaleListEmpty：service 返 nil 时响应 list 为空数组非 null。
func TestScaleListEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeScaleService{}
	r := newScaleRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/scales", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	data, _ := resp["data"].(map[string]any)
	list, _ := data["list"].([]any)
	if list == nil || len(list) != 0 {
		t.Fatalf("data.list should be empty array, got %v", data["list"])
	}
}

// TestScaleListMapsError：service 错误透传 code。
func TestScaleListMapsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeScaleService{listErr: service.NewError(errcode.Internal)}
	r := newScaleRouter(svc)

	_, resp := doQuestionJSON(t, r, http.MethodGet, "/api/scales", "")
	if got := codeOf(t, resp); got != errcode.Internal {
		t.Fatalf("code = %d, want %d", got, errcode.Internal)
	}
}

// TestScaleImportSuccess：请求体 scale_key 透传 service，响应 data 含批次三字段。
func TestScaleImportSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeScaleService{
		importRes: &service.ImportScaleResult{BatchID: "1785000000000000002", BatchNo: "#S0923", QuestionCount: 144},
	}
	r := newScaleRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/scales/import", `{"scale_key":"RISO_HUDSON"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.importLast != "RISO_HUDSON" {
		t.Fatalf("svc.ImportScale scale_key = %q", svc.importLast)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["batch_id"] != "1785000000000000002" ||
		data["batch_no"] != "#S0923" || data["question_count"].(float64) != 144 {
		t.Fatalf("data mismatch: %v", data)
	}
}

// TestScaleImportMissingKey：缺 scale_key 返 1400 且不触达 service。
func TestScaleImportMissingKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeScaleService{}
	r := newScaleRouter(svc)

	for _, body := range []string{`{}`, `{"scale_key":""}`, ``, `{not-json`} {
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/scales/import", body)
		if code != http.StatusOK {
			t.Fatalf("body=%q status = %d, want 200", body, code)
		}
		if got := codeOf(t, resp); got != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want 1400", body, got)
		}
		if svc.importLast != "" {
			t.Fatalf("body=%q service must not be invoked", body)
		}
	}
}

// TestScaleImportMapsErrors：service 业务错误（1400/1704）透传 code（BR2 1704 承载）。
func TestScaleImportMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.BadRequest), errcode.BadRequest},
		{service.NewError(errcode.ScaleAlreadyImported), errcode.ScaleAlreadyImported},
	} {
		svc := &fakeScaleService{importErr: tc.err}
		r := newScaleRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/scales/import", `{"scale_key":"RISO_HUDSON"}`)
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}
