// Package handler 对 answer 域三接口做黑盒测试（03 §3 A1/A2/A3）。内部包
// 与 handler_test 二选一（沿用 assessment_test_task_test.go 范式）。
package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// fakeAnswerSvc 是 service.AnswerService 的假实现：命中计数 + 参数探针 + 注入返回。
type fakeAnswerSvc struct {
	contextHits   int32
	contextRes    *service.AnswerContextResult
	contextErr    error
	contextTok    string
	contextCalled bool

	replyTok     string
	replyContent string
	replyCalled  bool
	replyRes     *service.AnswerReplyResult
	replyErr     error

	submitTok    string
	submitCalled bool
	submitRes    *service.AnswerSubmitResult
	submitErr    error
}

func (f *fakeAnswerSvc) Context(_ context.Context, token string) (*service.AnswerContextResult, error) {
	atomic.AddInt32(&f.contextHits, 1)
	f.contextCalled = true
	f.contextTok = token
	return f.contextRes, f.contextErr
}

func (f *fakeAnswerSvc) Reply(_ context.Context, token, content string) (*service.AnswerReplyResult, error) {
	f.replyCalled = true
	f.replyTok = token
	f.replyContent = content
	return f.replyRes, f.replyErr
}

func (f *fakeAnswerSvc) Submit(_ context.Context, token string) (*service.AnswerSubmitResult, error) {
	f.submitCalled = true
	f.submitTok = token
	return f.submitRes, f.submitErr
}

var _ service.AnswerService = (*fakeAnswerSvc)(nil)

// fakeAnswerAccountSvc 是生产同构 router 装配所需的 AccountService 桩（answer 测试
// 不触达 account 路由，方法全零值）。复用 router_test.go 的 fakeAccountSvc 形态。
type fakeAnswerAccountSvc struct{}

func (f *fakeAnswerAccountSvc) Login(_ context.Context, _, _, _ string) (*service.LoginResult, error) {
	return nil, service.NewError(errcode.InvalidCredentials)
}
func (f *fakeAnswerAccountSvc) GetCurrent(_ context.Context, _ int64) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeAnswerAccountSvc) ListAccounts(_ context.Context, _ string, _, _ int) ([]service.AccountListItemDTO, int64, error) {
	return nil, 0, nil
}
func (f *fakeAnswerAccountSvc) CreateAccount(_ context.Context, _, _, _, _ string, _ bool) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeAnswerAccountSvc) UpdateAccount(_ context.Context, _ int64, _, _, _ string, _, _ bool) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeAnswerAccountSvc) DeleteAccount(_ context.Context, _ int64) error         { return nil }
func (f *fakeAnswerAccountSvc) ToggleEnabled(_ context.Context, _ int64, _ bool) error { return nil }
func (f *fakeAnswerAccountSvc) ResetPassword(_ context.Context, _ int64, _, _ string) error {
	return nil
}

var _ service.AccountService = (*fakeAnswerAccountSvc)(nil)

// errBoom 非 service.Error 的基础设施错误，锚 500 映射。
var errAnswerBoom = errors.New("db down")

// newAnswerEngine 起生产同构 router（miniredis 供限流桶），answer handler 注入 fake。
func newAnswerEngine(t *testing.T, svc *fakeAnswerSvc) http.Handler {
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
	// 参数位与生产 NewRouter 一致：answerHandler 在 assessmentTestTaskHandler 之后。
	return router.NewRouter(
		&config.Config{},
		jwt.NewManager("answer-test-secret", time.Hour),
		handler.NewAccountHandler(&fakeAnswerAccountSvc{}, rsaMgr),
		handler.NewHealthHandler(),
		nil, // setupHandler
		nil, // systemHandler
		nil, // dimensionHandler
		nil, // assessmentConfigHandler
		nil, // assessmentBatchHandler
		nil, // assessmentTestTaskHandler
		handler.NewAnswerHandler(svc),
		nil, // llmConfigHandler
		nil, // integrationSecretHandler
		nil, // questionHandler
		nil, // questionBatchHandler
		nil, // questionGenerationHandler
		nil, // scaleHandler
		nil, // profileHandler
		nil, // dashboardHandler
		nil, // workspaceHandler
		rdb,
	)
}

// newAnswerRouter 挂三条路径的裸 engine（无限流），供 handler 层行为测试。
func newAnswerRouter(svc *fakeAnswerSvc) *gin.Engine {
	h := handler.NewAnswerHandler(svc)
	r := gin.New()
	r.POST("/api/answer/context", h.Context)
	r.POST("/api/answer/reply", h.Reply)
	r.POST("/api/answer/submit", h.Submit)
	return r
}

// doAnswerReq 执行 POST 并断言 HTTP 200，返回解包后的 code。
func doAnswerReq(t *testing.T, r http.Handler, url, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST %s status = %d, want 200, body=%s", url, w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("POST %s unmarshal: %v, body=%s", url, err, w.Body.String())
	}
	return resp.Code
}

// TestAnswerRoutesRegistered 核心断言：POST /api/answer/context 带 {token:"x"}
// 到达 handler（fake svc 桩计数）且响应 HTTP 200（生产同构 router 公开段，无 JWT）。
func TestAnswerRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAnswerSvc{contextRes: &service.AnswerContextResult{TaskNo: "T202609290001"}}
	engine := newAnswerEngine(t, svc)

	req := httptest.NewRequest(http.MethodPost, "/api/answer/context", strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&svc.contextHits); got != 1 {
		t.Fatalf("context hits = %d, want 1（请求须到达 handler）", got)
	}
	if svc.contextTok != "x" {
		t.Fatalf("context token = %q, want x", svc.contextTok)
	}

	// reply/submit 同段可路由（404 即未挂载）。
	for _, p := range []struct{ url, body string }{
		{"/api/answer/reply", `{"token":"x","content":"C"}`},
		{"/api/answer/submit", `{"token":"x"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, p.url, strings.NewReader(p.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("%s 返回 404，路由未注册", p.url)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", p.url, w.Code, w.Body.String())
		}
	}
}

// TestAnswerReplyBinding 核心断言：token 缺失/空串 → HTTP 200 + code=1400；
// content 空串放行至 svc（1902 归类归 service 格式校验，03 A2 错误码表）。
func TestAnswerReplyBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAnswerSvc{replyRes: &service.AnswerReplyResult{Action: service.AnswerActionNext}}
	r := newAnswerRouter(svc)

	for _, body := range []string{
		``,                // 空 body
		`{"content":"C"}`, // 缺 token
		`{invalid json`,   // 非法 JSON
		`{"token":"x"}`,   // content 键缺失（03 A2：1400 字段缺失）
	} {
		if code := doAnswerReq(t, r, "/api/answer/reply", body); code != errcode.BadRequest {
			t.Fatalf("body=%q code = %d, want %d", body, code, errcode.BadRequest)
		}
	}
	if svc.replyCalled {
		t.Fatal("binding 失败不应触达 svc")
	}

	// content 空串：handler 放行（1400=字段缺失，空串归 1902 由 service 判定）。
	if code := doAnswerReq(t, r, "/api/answer/reply", `{"token":"x","content":""}`); code != 0 {
		t.Fatalf("content 空串 code = %d, want 0（放行至 svc）", code)
	}
	if !svc.replyCalled || svc.replyContent != "" {
		t.Fatalf("svc 应收到空串 content，got called=%v content=%q", svc.replyCalled, svc.replyContent)
	}
}

// TestAnswerBusinessErrorPassthrough 核心断言：fake svc 返回 NewError(1901) →
// HTTP 200 + code=1901 + message="answer token invalid"（本域无 401 路径，03 §2.3）。
func TestAnswerBusinessErrorPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAnswerSvc{contextErr: service.NewError(errcode.AnswerTokenInvalid)}
	r := newAnswerRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/answer/context", strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（本域无 401 路径）", w.Code)
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != errcode.AnswerTokenInvalid {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.AnswerTokenInvalid)
	}
	if resp.Message != "answer token invalid" {
		t.Fatalf("message = %q, want %q", resp.Message, "answer token invalid")
	}
}

// TestAnswerRateLimit 核心断言：按端点分桶（miniredis）——context/submit 各 30/min、
// reply 120/min（03 §2.1 分桶口径）。context 第 31 次 429；reply 计数独立，三路
// 交替各 30 次不互相挤占（reply 远未触顶）。
func TestAnswerRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAnswerSvc{contextRes: &service.AnswerContextResult{}}
	engine := newAnswerEngine(t, svc)

	doPost := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}

	// 三接口交替发 30 次：分桶口径下 reply 与 submit 各自计数未触顶，均 200。
	paths := []string{"/api/answer/context", "/api/answer/reply", "/api/answer/submit"}
	bodies := []string{`{"token":"x"}`, `{"token":"x","content":"C"}`, `{"token":"x"}`}
	for i := 0; i < 30; i++ {
		idx := i % 3
		if code := doPost(paths[idx], bodies[idx]); code == http.StatusTooManyRequests {
			t.Fatalf("req #%d unexpectedly rate-limited (path=%s)", i+1, paths[idx])
		}
	}
	// context 已耗 10 次，补足至 30 后下一次 429（脚本 n>limit 拦截，第 31 次计数 31）。
	for i := 0; i < 20; i++ {
		if code := doPost(paths[0], bodies[0]); code == http.StatusTooManyRequests {
			t.Fatalf("context warm-up #%d unexpectedly rate-limited", i+1)
		}
	}
	if code := doPost("/api/answer/context", `{"token":"x"}`); code != http.StatusTooManyRequests {
		t.Fatalf("context req #31: status = %d, want 429", code)
	}
	// reply 桶独立计数：context 触顶后 reply 仍 200（120/min 未达）。
	if code := doPost("/api/answer/reply", `{"token":"x","content":"C"}`); code != http.StatusOK {
		t.Fatalf("reply after context exhausted: status = %d, want 200（分桶互不挤占）", code)
	}
}

// TestAnswerContextBindsData 覆盖 A1 成功响应组装与 A2/A3 参数透传。
func TestAnswerContextBindsData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAnswerSvc{
		contextRes: &service.AnswerContextResult{
			TaskNo: "T202609290001", TestType: "ai_mgmt", QuestionTotal: 5, AnsweredCount: 2,
			Finished:  false,
			Questions: []service.AnswerQuestionItem{{Seq: 1, DimensionName: "目标设定与对齐", Scenario: "s", Requirement: "r"}},
			Replies:   []service.AnswerReplyItem{{Seq: 1, Content: "C"}},
		},
		replyRes:  &service.AnswerReplyResult{QuestionSeq: 3, AnsweredCount: 3, QuestionTotal: 5, Action: service.AnswerActionNext, NextQuestion: &service.AnswerQuestionItem{Seq: 4, Scenario: "s2"}},
		submitRes: &service.AnswerSubmitResult{TaskNo: "T202609290001"},
	}
	r := newAnswerRouter(svc)

	// A1
	req := httptest.NewRequest(http.MethodPost, "/api/answer/context", strings.NewReader(`{"token":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			TaskNo        string `json:"task_no"`
			TestType      string `json:"test_type"`
			QuestionTotal int    `json:"question_total"`
			AnsweredCount int    `json:"answered_count"`
			Finished      bool   `json:"finished"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Code != 0 || resp.Data.TaskNo != "T202609290001" || resp.Data.QuestionTotal != 5 || resp.Data.AnsweredCount != 2 || resp.Data.Finished {
		t.Fatalf("A1 resp = %+v", resp)
	}

	// A2 成功：token/content 透传。
	if code := doAnswerReq(t, r, "/api/answer/reply", `{"token":"t1","content":"C，因为"}`); code != 0 {
		t.Fatalf("A2 code = %d, want 0", code)
	}
	if svc.replyTok != "t1" || svc.replyContent != "C，因为" {
		t.Fatalf("A2 透传 = (%q,%q)", svc.replyTok, svc.replyContent)
	}

	// A3 成功：token 透传。
	if code := doAnswerReq(t, r, "/api/answer/submit", `{"token":"t2"}`); code != 0 {
		t.Fatalf("A3 code = %d, want 0", code)
	}
	if svc.submitTok != "t2" {
		t.Fatalf("A3 submitTok = %q, want t2", svc.submitTok)
	}
}

// TestAnswerContextSubmitBinding 覆盖 A1/A3 的 token binding：缺失/空串/坏 JSON 均 1400。
func TestAnswerContextSubmitBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, url := range []string{"/api/answer/context", "/api/answer/submit"} {
		for _, body := range []string{``, `{"token":""}`, `{invalid`, `{"other":"y"}`} {
			svc := &fakeAnswerSvc{}
			r := newAnswerRouter(svc)
			if code := doAnswerReq(t, r, url, body); code != errcode.BadRequest {
				t.Fatalf("%s body=%q code = %d, want %d", url, body, code, errcode.BadRequest)
			}
			if svc.contextCalled || svc.submitCalled {
				t.Fatalf("%s binding 失败不应触达 svc", url)
			}
		}
	}
}

// TestAnswerErrorVariants 覆盖 1902/1903 业务码透传与非 service.Error 的 500 映射。
func TestAnswerErrorVariants(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		svc  *fakeAnswerSvc
		url  string
		body string
		code int
		http int
	}{
		{"reply 1902", &fakeAnswerSvc{replyErr: service.NewError(errcode.AnswerReplyInvalid)}, "/api/answer/reply", `{"token":"x","content":"bad"}`, errcode.AnswerReplyInvalid, http.StatusOK},
		{"submit 1903", &fakeAnswerSvc{submitErr: service.NewError(errcode.AnswerIncomplete)}, "/api/answer/submit", `{"token":"x"}`, errcode.AnswerIncomplete, http.StatusOK},
		{"reply non-service error", &fakeAnswerSvc{replyErr: errAnswerBoom}, "/api/answer/reply", `{"token":"x","content":"C"}`, errcode.Internal, http.StatusInternalServerError},
		{"submit non-service error", &fakeAnswerSvc{submitErr: errAnswerBoom}, "/api/answer/submit", `{"token":"x"}`, errcode.Internal, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newAnswerRouter(tc.svc)
			req := httptest.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.http {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, tc.http, w.Body.String())
			}
			var resp struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp.Code != tc.code {
				t.Fatalf("code = %d, want %d", resp.Code, tc.code)
			}
		})
	}
}
