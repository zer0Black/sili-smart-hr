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

// fakeQuestionBatchService 是 service.QuestionBatchService 的假实现，入参以 last* 字段记录便于断言。
type fakeQuestionBatchService struct {
	listCalls int
	listRes   []service.BatchCardDTO
	listErr   error

	questionsLastID int64
	questionsRes    *service.BatchQuestionsDTO
	questionsErr    error

	confirmLastID      int64
	confirmLastRejects []service.RejectItem
	confirmRes         *service.ConfirmResult
	confirmErr         error

	voidLastID int64
	voidErr    error

	resubmitLast service.ResubmitInput
	resubmitRes  *service.ResubmitResult
	resubmitErr  error
}

func (f *fakeQuestionBatchService) ListPendingBatches(_ context.Context) ([]service.BatchCardDTO, error) {
	f.listCalls++
	return f.listRes, f.listErr
}

func (f *fakeQuestionBatchService) GetBatchQuestions(_ context.Context, batchID int64) (*service.BatchQuestionsDTO, error) {
	f.questionsLastID = batchID
	return f.questionsRes, f.questionsErr
}

func (f *fakeQuestionBatchService) ConfirmBatch(_ context.Context, batchID int64, rejected []service.RejectItem) (*service.ConfirmResult, error) {
	f.confirmLastID = batchID
	f.confirmLastRejects = rejected
	return f.confirmRes, f.confirmErr
}

func (f *fakeQuestionBatchService) VoidBatch(_ context.Context, batchID int64) error {
	f.voidLastID = batchID
	return f.voidErr
}

func (f *fakeQuestionBatchService) ResubmitQuestion(_ context.Context, in service.ResubmitInput) (*service.ResubmitResult, error) {
	f.resubmitLast = in
	return f.resubmitRes, f.resubmitErr
}

var _ service.QuestionBatchService = (*fakeQuestionBatchService)(nil)

// newQuestionBatchRouter 构造批次四路由 + resubmit 委托路由的测试引擎。
// questionSvc 传 nil：QuestionHandler 的题目五接口不在本测试触达范围。
func newQuestionBatchRouter(batchSvc *fakeQuestionBatchService) *gin.Engine {
	h := handler.NewQuestionBatchHandler(batchSvc)
	qh := handler.NewQuestionHandler(nil, nil, batchSvc)
	r := gin.New()
	r.GET("/api/question-batches", h.ListBatches)
	r.GET("/api/question-batches/:id/questions", h.BatchQuestions)
	r.POST("/api/question-batches/:id/confirm", h.Confirm)
	r.POST("/api/question-batches/:id/void", h.Void)
	r.POST("/api/questions/resubmit", qh.Resubmit)
	return r
}

// TestQuestionBatchListSuccess：卡区列表响应 data 为 {list,total}，total 等于 list 长度。
func TestQuestionBatchListSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{
		listRes: []service.BatchCardDTO{{
			ID: "1785000000000000001", BatchNo: "#G0921", Title: "授权与分工 ×2",
			Source: "AI", BatchType: "GENERATE", QuestionCount: 12,
		}},
	}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches", "")
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
	if data["total"].(float64) != 1 {
		t.Fatalf("data.total = %v, want 1", data["total"])
	}
	list, _ := data["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("data.list len = %d, want 1", len(list))
	}
	row, _ := list[0].(map[string]any)
	if row["id"] != "1785000000000000001" || row["batch_no"] != "#G0921" || row["question_count"].(float64) != 12 {
		t.Fatalf("data.list[0] mismatch: %v", row)
	}
}

// TestQuestionBatchListEmpty：空批次列表返回空 list（非 null）与 total 0。
func TestQuestionBatchListEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches", "")
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
	list, _ := data["list"].([]any)
	if list == nil || len(list) != 0 {
		t.Fatalf("data.list should be empty array, got %v", data["list"])
	}
	if data["total"].(float64) != 0 {
		t.Fatalf("data.total = %v, want 0", data["total"])
	}
}

// TestQuestionBatchListMapsError：service 错误透传 code。
func TestQuestionBatchListMapsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{listErr: service.NewError(errcode.Internal)}
	r := newQuestionBatchRouter(svc)

	_, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches", "")
	if got := codeOf(t, resp); got != errcode.Internal {
		t.Fatalf("code = %d, want %d", got, errcode.Internal)
	}
}

// TestQuestionBatchQuestionsSuccess：明细响应 data 含 batch 与 questions，路径 ID 解析透传。
func TestQuestionBatchQuestionsSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{
		questionsRes: &service.BatchQuestionsDTO{
			Batch: &service.BatchCardDTO{ID: "1785000000000000001", BatchNo: "#G0921"},
			Questions: []service.ReviewQuestionDTO{{
				ID: "1785000000000000101", QuestionNo: "Q-AG-0101",
				DimensionID: "1780000000000000100", DimensionName: "授权与分工",
				AnswerMode: "CHAT", Scenario: "情境", Requirement: "要求", FocusPoint: "考察点",
				RejectReason: "",
			}},
		},
	}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches/1785000000000000001/questions", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.questionsLastID != 1785000000000000001 {
		t.Fatalf("svc.GetBatchQuestions id = %d", svc.questionsLastID)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data missing: %v", resp)
	}
	batch, _ := data["batch"].(map[string]any)
	if batch == nil || batch["id"] != "1785000000000000001" {
		t.Fatalf("data.batch mismatch: %v", data["batch"])
	}
	questions, _ := data["questions"].([]any)
	if len(questions) != 1 {
		t.Fatalf("data.questions len = %d, want 1", len(questions))
	}
	q, _ := questions[0].(map[string]any)
	if q["id"] != "1785000000000000101" || q["dimension_id"] != "1780000000000000100" {
		t.Fatalf("data.questions[0] mismatch: %v", q)
	}
}

// TestQuestionBatchQuestionsBadID：路径 ID 非数字返 1400。
func TestQuestionBatchQuestionsBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches/abc/questions", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestQuestionBatchQuestionsMapsError：service 返 1702/1706 透传。
func TestQuestionBatchQuestionsMapsError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.QuestionBatchNotFound), errcode.QuestionBatchNotFound},
		{service.NewError(errcode.QuestionBatchClosed), errcode.QuestionBatchClosed},
	} {
		svc := &fakeQuestionBatchService{questionsErr: tc.err}
		r := newQuestionBatchRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodGet, "/api/question-batches/1785000000000000001/questions", "")
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}

// TestBatchConfirmEmptyBody 核心断言：POST confirm 无 body 或 {} 成功等价全通过
//（rejected 缺省 nil 透传 service，03 §3.9 空/缺省为全部默认通过）。
func TestBatchConfirmEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{"", `{}`} {
		svc := &fakeQuestionBatchService{
			confirmRes: &service.ConfirmResult{
				BatchID: "1785000000000000001", BatchStatus: "CLOSED",
				AdmittedCount: 12, RejectedCount: 0,
			},
		}
		r := newQuestionBatchRouter(svc)

		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", body)
		if code != http.StatusOK {
			t.Fatalf("body=%q status = %d, want 200", body, code)
		}
		if got := codeOf(t, resp); got != 0 {
			t.Fatalf("body=%q code = %d, want 0", body, got)
		}
		if svc.confirmLastID != 1785000000000000001 {
			t.Fatalf("body=%q confirmLastID = %d", body, svc.confirmLastID)
		}
		if svc.confirmLastRejects != nil {
			t.Fatalf("body=%q rejected should be nil（全通过）, got %v", body, svc.confirmLastRejects)
		}
		data, _ := resp["data"].(map[string]any)
		if data == nil || data["batch_status"] != "CLOSED" ||
			data["admitted_count"].(float64) != 12 || data["rejected_count"].(float64) != 0 {
			t.Fatalf("body=%q data mismatch: %v", body, data)
		}
	}
}

// TestBatchConfirmWithRejects：question_id string 经 parseID 转整型，reason 透传。
func TestBatchConfirmWithRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{
		confirmRes: &service.ConfirmResult{BatchID: "1", BatchStatus: "CLOSED", AdmittedCount: 11, RejectedCount: 1},
	}
	r := newQuestionBatchRouter(svc)

	body := `{"rejected":[{"question_id":"1785000000000000102","reason":"情境场景迁移性差"}]}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if len(svc.confirmLastRejects) != 1 {
		t.Fatalf("rejected len = %d, want 1", len(svc.confirmLastRejects))
	}
	item := svc.confirmLastRejects[0]
	if item.QuestionID != 1785000000000000102 || item.Reason != "情境场景迁移性差" {
		t.Fatalf("reject item mismatch: %+v", item)
	}
}

// TestBatchConfirmEmptyRejectsArray：空数组同样等价全通过（rejected 非 nil 空 slice）。
func TestBatchConfirmEmptyRejectsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{confirmRes: &service.ConfirmResult{}}
	r := newQuestionBatchRouter(svc)

	code, _ := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", `{"rejected":[]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if svc.confirmLastRejects == nil || len(svc.confirmLastRejects) != 0 {
		t.Fatalf("rejected should be empty non-nil slice, got %v", svc.confirmLastRejects)
	}
}

// TestBatchConfirmBadID：路径 ID 非数字返 1400。
func TestBatchConfirmBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/abc/confirm", `{}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestBatchConfirmBadQuestionID：rejected[].question_id 非数字返 1400。
func TestBatchConfirmBadQuestionID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	body := `{"rejected":[{"question_id":"not-a-number","reason":"原因"}]}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestBatchConfirmMalformedJSON：非法 JSON 返 1400。
func TestBatchConfirmMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", `{not-json`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestBatchConfirmMapsErrors：service 业务错误（1706/1713）透传 code。
func TestBatchConfirmMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.QuestionBatchClosed), errcode.QuestionBatchClosed},
		{service.NewError(errcode.QuestionVersionConflict), errcode.QuestionVersionConflict},
	} {
		svc := &fakeQuestionBatchService{confirmErr: tc.err}
		r := newQuestionBatchRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000001/confirm", `{}`)
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}

// TestBatchVoidSuccess 核心断言补充：无请求体成功，data 为 null。
func TestBatchVoidSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000002/void", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	if svc.voidLastID != 1785000000000000002 {
		t.Fatalf("voidLastID = %d", svc.voidLastID)
	}
	if resp["data"] != nil {
		t.Fatalf("data should be null, got %v", resp["data"])
	}
}

// TestBatchVoidBadID 核心断言：路径 ID 非数字返 1400。
func TestBatchVoidBadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/xyz/void", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", got)
	}
}

// TestBatchVoidMapsErrors：service 返 1702/1706 透传。
func TestBatchVoidMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.QuestionBatchNotFound), errcode.QuestionBatchNotFound},
		{service.NewError(errcode.QuestionBatchClosed), errcode.QuestionBatchClosed},
	} {
		svc := &fakeQuestionBatchService{voidErr: tc.err}
		r := newQuestionBatchRouter(svc)
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/question-batches/1785000000000000002/void", "")
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}

// TestQuestionResubmitSuccess：resubmit 挂 QuestionHandler 委托 batchSvc，五字段透传，响应含归批信息。
func TestQuestionResubmitSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{
		resubmitRes: &service.ResubmitResult{
			ID: "1785000000000000101", Status: "PENDING",
			BatchID: "1785000000000000009", BatchNo: "#R0923", Version: 3,
		},
	}
	r := newQuestionBatchRouter(svc)

	body := `{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"情境（修正后）","requirement":"要求（修正后）","focus_point":"考察点（修正后）","version":2}`
	code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/resubmit", body)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := codeOf(t, resp); got != 0 {
		t.Fatalf("code = %d, want 0", got)
	}
	in := svc.resubmitLast
	if in.ID != 1785000000000000101 || in.DimensionID != 1780000000000000110 || in.Version != 2 ||
		in.Scenario != "情境（修正后）" || in.Requirement != "要求（修正后）" || in.FocusPoint != "考察点（修正后）" {
		t.Fatalf("svc.ResubmitQuestion args mismatch: %+v", in)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil || data["status"] != "PENDING" || data["batch_id"] != "1785000000000000009" ||
		data["batch_no"] != "#R0923" || data["version"].(float64) != 3 {
		t.Fatalf("data mismatch: %v", data)
	}
}

// TestQuestionResubmitBadBody：缺必填（version）与非法 id 双路径均 1400。
func TestQuestionResubmitBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeQuestionBatchService{}
	r := newQuestionBatchRouter(svc)

	for _, body := range []string{
		`{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"s","requirement":"r","focus_point":"f"}`,
		`{"id":"abc","dimension_id":"1780000000000000110","scenario":"s","requirement":"r","focus_point":"f","version":2}`,
	} {
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/resubmit", body)
		if code != http.StatusOK {
			t.Fatalf("body=%s status = %d, want 200", body, code)
		}
		if got := codeOf(t, resp); got != errcode.BadRequest {
			t.Fatalf("body=%s code = %d, want 1400", body, got)
		}
	}
}

// TestQuestionResubmitMapsErrors：service 返 1707（不可重新提交）/1713（乐观锁）透传。
func TestQuestionResubmitMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err  error
		want int
	}{
		{service.NewError(errcode.QuestionNotEditable), errcode.QuestionNotEditable},
		{service.NewError(errcode.QuestionVersionConflict), errcode.QuestionVersionConflict},
	} {
		svc := &fakeQuestionBatchService{resubmitErr: tc.err}
		r := newQuestionBatchRouter(svc)
		body := `{"id":"1785000000000000101","dimension_id":"1780000000000000110","scenario":"s","requirement":"r","focus_point":"f","version":2}`
		code, resp := doQuestionJSON(t, r, http.MethodPost, "/api/questions/resubmit", body)
		if code != http.StatusOK {
			t.Fatalf("err=%v status = %d, want 200", tc.err, code)
		}
		if got := codeOf(t, resp); got != tc.want {
			t.Fatalf("code = %d, want %d", got, tc.want)
		}
	}
}
