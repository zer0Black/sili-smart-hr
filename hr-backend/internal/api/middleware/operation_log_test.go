package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/middleware"
	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/service"
)

// changeItemAlias 测试侧构造 changes 用（domain.ChangeItem 别名）。
type changeItemAlias = domain.ChangeItem

// fakeRecorder 收集中间件投递的 PendingLog，测试断言用。
type fakeRecorder struct {
	recorded []service.PendingLog
}

func (f *fakeRecorder) Record(entry service.PendingLog) {
	f.recorded = append(f.recorded, entry)
}

// compile-time：装配侧传 *service.OperationLogRecorder，须满足窄接口。
var _ service.PendingLogRecorder = (*service.OperationLogRecorder)(nil)

// newLogEngine 构造挂 OperationLog 中间件的 engine，handler 前置一个模拟
// JWT 注入的中间件（account_id/username），仿真实挂载形态（JWT 之后）。
func newLogEngine(rec *fakeRecorder, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("account_id", int64(1845700000000000012))
		c.Set("username", "admin")
		c.Next()
	}, middleware.OperationLog(rec))
	r.POST("/api/accounts/create", handler)
	r.GET("/api/accounts/list", handler)
	return r
}

// serveJSON 发一次请求并返回响应码。
func serveJSON(r http.Handler, method, path, body string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestOperationLogMiddlewareSkipsGET 核心断言：GET 不进记录路径，零投递。
func TestOperationLogMiddlewareSkipsGET(t *testing.T) {
	rec := &fakeRecorder{}
	r := newLogEngine(rec, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0}) })

	if code := serveJSON(r, http.MethodGet, "/api/accounts/list", ""); code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code)
	}
	if len(rec.recorded) != 0 {
		t.Fatalf("recorded = %d, want 0", len(rec.recorded))
	}
}

// TestOperationLogMiddlewareFallback 核心断言：无埋点降级路径级语义，
// 成败按 HTTP 200 且 body code==0 双判，失败摘响应 message。
func TestOperationLogMiddlewareFallback(t *testing.T) {
	rec := &fakeRecorder{}
	r := newLogEngine(rec, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0}) })

	if code := serveJSON(r, http.MethodPost, "/api/accounts/create", `{}`); code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", code)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Module != "account" {
		t.Fatalf("Module = %q, want account", got.Module)
	}
	if got.Target != "POST /api/accounts/create" {
		t.Fatalf("Target = %q, want POST /api/accounts/create", got.Target)
	}
	if got.Result != "success" {
		t.Fatalf("Result = %q, want success", got.Result)
	}
	if got.Summary != "用户管理接口调用：成功" {
		t.Fatalf("Summary = %q, want 用户管理接口调用：成功", got.Summary)
	}
	if got.RequestPath != "POST /api/accounts/create" {
		t.Fatalf("RequestPath = %q, want POST /api/accounts/create", got.RequestPath)
	}
	if got.AccountID != 1845700000000000012 || got.FallbackUsername != "admin" {
		t.Fatalf("operator = %d/%q, want 1845700000000000012/admin", got.AccountID, got.FallbackUsername)
	}

	// 失败路径：body code 非 0，Result=fail 且 Summary 摘响应 message。
	rec2 := &fakeRecorder{}
	r2 := newLogEngine(rec2, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "凭证校验未通过"})
	})
	serveJSON(r2, http.MethodPost, "/api/accounts/create", `{}`)
	if len(rec2.recorded) != 1 {
		t.Fatalf("fail path recorded = %d, want 1", len(rec2.recorded))
	}
	fail := rec2.recorded[0]
	if fail.Result != "fail" {
		t.Fatalf("fail Result = %q, want fail", fail.Result)
	}
	if fail.Summary != "凭证校验未通过" {
		t.Fatalf("fail Summary = %q, want 凭证校验未通过", fail.Summary)
	}
}

// TestOperationLogMiddlewareSinkUsed 核心断言：埋点字段优先于路径级兜底。
func TestOperationLogMiddlewareSinkUsed(t *testing.T) {
	rec := &fakeRecorder{}
	r := newLogEngine(rec, func(c *gin.Context) {
		if sink := service.SinkFromContext(c.Request.Context()); sink != nil {
			sink.SetModule("dimension")
			sink.SetTarget("维度「任务适配判断力」")
		}
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})

	serveJSON(r, http.MethodPost, "/api/accounts/create", `{}`)
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Module != "dimension" {
		t.Fatalf("Module = %q, want dimension", got.Module)
	}
	if got.Target != "维度「任务适配判断力」" {
		t.Fatalf("Target = %q, want 维度「任务适配判断力」", got.Target)
	}
}

// TestOperationLogLoginMiddleware 核心断言：登录形态无 JWT，操作人取请求
// username 原值，失败摘要统一「凭证校验未通过」。
func TestOperationLogLoginMiddleware(t *testing.T) {
	rec := &fakeRecorder{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/login", middleware.OperationLogLogin(rec), func(c *gin.Context) {
		// 模拟业务 handler 完整消费 body（peek 回填后仍可绑定）。
		var body struct {
			Username string `json:"username"`
		}
		_ = c.ShouldBindJSON(&body)
		c.JSON(http.StatusOK, gin.H{"code": 1001, "message": "凭证校验未通过"})
	})

	if code := serveJSON(r, http.MethodPost, "/api/login", `{"username":"admin"}`); code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", code)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.FallbackName != "admin" {
		t.Fatalf("FallbackName = %q, want admin", got.FallbackName)
	}
	if got.Module != "login" {
		t.Fatalf("Module = %q, want login", got.Module)
	}
	if got.Summary != "凭证校验未通过" {
		t.Fatalf("Summary = %q, want 凭证校验未通过", got.Summary)
	}
	if got.Result != "fail" {
		t.Fatalf("Result = %q, want fail", got.Result)
	}
	if got.Target != "登录" {
		t.Fatalf("Target = %q, want 登录", got.Target)
	}

	// 成功路径：summary「登录成功」，Result=success。
	recOK := &fakeRecorder{}
	r2 := gin.New()
	r2.POST("/api/login", middleware.OperationLogLogin(recOK), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	serveJSON(r2, http.MethodPost, "/api/login", `{"username":"admin"}`)
	if len(recOK.recorded) != 1 {
		t.Fatalf("ok recorded = %d, want 1", len(recOK.recorded))
	}
	ok := recOK.recorded[0]
	if ok.Summary != "登录成功" || ok.Result != "success" {
		t.Fatalf("ok summary/result = %q/%q, want 登录成功/success", ok.Summary, ok.Result)
	}
}

// TestOperationLogMiddlewarePanicRecordedFail 补充：panic 路径走真实 Recovery
// 链（Recovery 在最外层，与生产 router 挂载顺序一致），记录中间件在 defer 内
// 落 fail 后 re-panic，断言 500 + 摘「服务内部错误」（03 §1.5 HTTP 非 200 口径）。
func TestOperationLogMiddlewarePanicRecordedFail(t *testing.T) {
	rec := &fakeRecorder{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(middleware.OperationLog(rec))
	r.POST("/api/dimensions/create", func(c *gin.Context) { panic("boom") })

	if code := serveJSON(r, http.MethodPost, "/api/dimensions/create", `{}`); code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", code)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Result != "fail" {
		t.Fatalf("Result = %q, want fail", got.Result)
	}
	if got.Summary != "服务内部错误" {
		t.Fatalf("Summary = %q, want 服务内部错误", got.Summary)
	}
	if got.Module != "dimension" {
		t.Fatalf("Module = %q, want dimension", got.Module)
	}
}

// TestOperationLogLoginMiddlewarePanicRecordedFail 补充：登录 handler panic
// 同样走真实 Recovery 链，落 fail 后 re-panic，摘要维持反枚举口径（03 §1.6）。
func TestOperationLogLoginMiddlewarePanicRecordedFail(t *testing.T) {
	rec := &fakeRecorder{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Recovery())
	r.POST("/api/login", middleware.OperationLogLogin(rec), func(c *gin.Context) { panic("boom") })

	if code := serveJSON(r, http.MethodPost, "/api/login", `{"username":"admin"}`); code != http.StatusInternalServerError {
		t.Fatalf("login panic status = %d, want 500", code)
	}
	if len(rec.recorded) != 1 {
		t.Fatalf("recorded = %d, want 1", len(rec.recorded))
	}
	got := rec.recorded[0]
	if got.Result != "fail" {
		t.Fatalf("Result = %q, want fail", got.Result)
	}
	if got.Summary != "凭证校验未通过" {
		t.Fatalf("Summary = %q, want 凭证校验未通过", got.Summary)
	}
	if got.FallbackName != "admin" {
		t.Fatalf("FallbackName = %q, want admin", got.FallbackName)
	}
}

// TestOperationLogMiddlewareFallbackModuleTable 补充：前缀表逐项与默认归
// system_params 的推断（03 §4.1 步骤3），并覆盖 body 解析失败按 fail。
func TestOperationLogMiddlewareFallbackModuleTable(t *testing.T) {
	cases := []struct {
		path   string
		module string
	}{
		{"/api/dimensions/update", "dimension"},
		{"/api/assessment-config/save", "system_params"},
		{"/api/llm-configs/enable", "llm_config"},
		{"/api/integration-secret/update", "llm_config"},
		{"/api/questions/update", "question_bank"},
		{"/api/question-batches/123/confirm", "question_bank"},
		{"/api/question-generations/create", "question_bank"},
		{"/api/scales/import", "question_bank"},
		{"/api/assessment/batches/create", "assessment"},
		{"/api/system/health-check", "system_params"},
	}
	for _, tc := range cases {
		rec := &fakeRecorder{}
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(middleware.OperationLog(rec))
		r.POST(tc.path, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0}) })
		serveJSON(r, http.MethodPost, tc.path, `{}`)
		if len(rec.recorded) != 1 {
			t.Fatalf("%s: recorded = %d, want 1", tc.path, len(rec.recorded))
		}
		if got := rec.recorded[0].Module; got != tc.module {
			t.Fatalf("%s: Module = %q, want %q", tc.path, got, tc.module)
		}
	}

	// body 非合法 JSON（截断/非统一结构）：解析失败按 fail。
	rec := &fakeRecorder{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.OperationLog(rec))
	r.POST("/api/accounts/create", func(c *gin.Context) { c.String(http.StatusOK, "not json") })
	serveJSON(r, http.MethodPost, "/api/accounts/create", `{}`)
	if len(rec.recorded) != 1 {
		t.Fatalf("bad body recorded = %d, want 1", len(rec.recorded))
	}
	if got := rec.recorded[0].Result; got != "fail" {
		t.Fatalf("bad body Result = %q, want fail", got)
	}
}

// TestOperationLogMiddlewareSinkDetailPassedThrough 补充：埋点 detail 与
// changes 从 Snapshot 透传（任务契约），失败路径丢弃埋点数据。
func TestOperationLogMiddlewareSinkDetailPassedThrough(t *testing.T) {
	rec := &fakeRecorder{}
	r := newLogEngine(rec, func(c *gin.Context) {
		if sink := service.SinkFromContext(c.Request.Context()); sink != nil {
			sink.SetModule("dimension")
			sink.SetTarget("t")
			sink.SetDetail("d")
			sink.SetChanges([]changeItemAlias{{Field: "权重", Before: "30", After: "50"}})
		}
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	serveJSON(r, http.MethodPost, "/api/accounts/create", `{}`)
	got := rec.recorded[0]
	if got.Detail != "d" || len(got.Changes) != 1 || got.Changes[0].Field != "权重" {
		t.Fatalf("detail/changes not passed through: %q/%v", got.Detail, got.Changes)
	}
}
