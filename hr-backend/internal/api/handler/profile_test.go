// profile_test 对个人画像域三接口做 handler 黑盒测试（03 A1/A2/B1、§1.6、§2.1）。
//
// fake service + 裸 engine 覆盖三 handler 行为；生产同构 router 覆盖三路由
// 挂 JWT 鉴权组（03 §2.1 / BR3，无 token 统一 401+1003）。
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
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

// fakeProfileSvc 是 service.ProfileService 的假实现：入参探针 + 注入返回。
type fakeProfileSvc struct {
	listLast service.ProfileFilter
	listRes  *service.ProfileListResult
	listErr  error

	exportLast service.ProfileFilter
	exportData []byte
	exportName string
	exportErr  error

	detailLast struct {
		StaffName, PeriodStart, PeriodEnd string
	}
	detailRes *service.ProfileDetailDTO
	detailErr error
}

func (f *fakeProfileSvc) List(_ context.Context, flt service.ProfileFilter) (*service.ProfileListResult, error) {
	f.listLast = flt
	return f.listRes, f.listErr
}

func (f *fakeProfileSvc) Export(_ context.Context, flt service.ProfileFilter) ([]byte, string, error) {
	f.exportLast = flt
	return f.exportData, f.exportName, f.exportErr
}

func (f *fakeProfileSvc) Detail(_ context.Context, staffName, periodStart, periodEnd string) (*service.ProfileDetailDTO, error) {
	f.detailLast.StaffName = staffName
	f.detailLast.PeriodStart = periodStart
	f.detailLast.PeriodEnd = periodEnd
	return f.detailRes, f.detailErr
}

var _ service.ProfileService = (*fakeProfileSvc)(nil)

// newProfileRouter 挂三条路径的裸 engine（无中间件），供 handler 层行为测试。
func newProfileRouter(svc *fakeProfileSvc) *gin.Engine {
	h := handler.NewProfileHandler(svc)
	r := gin.New()
	r.GET("/api/profiles", h.List)
	r.GET("/api/profiles/export", h.Export)
	r.GET("/api/profiles/detail", h.Detail)
	return r
}

// TestProfileHandler_List_OK 覆盖核心断言：fake 返回 2 行，HTTP 200、code 0、
// data.list 长度 2、staff_name 序列化正确（03 A1 统一响应）。
func TestProfileHandler_List_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{
		listRes: &service.ProfileListResult{
			List: []service.ProfileListItem{
				{StaffName: "张敏", ActivityLevel: "active"},
				{StaffName: "张伟", ActivityLevel: "unused"},
			},
			Total:    2,
			Page:     1,
			PageSize: 10,
		},
	}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles?page=1&page_size=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List  []service.ProfileListItem `json:"list"`
			Total int64                     `json:"total"`
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
	if resp.Data.List[0].StaffName != "张敏" || resp.Data.List[1].StaffName != "张伟" {
		t.Fatalf("staff_name 序列化错误: [%q %q]", resp.Data.List[0].StaffName, resp.Data.List[1].StaffName)
	}
	if resp.Data.Total != 2 {
		t.Fatalf("data.total = %d, want 2", resp.Data.Total)
	}
}

// TestProfileHandler_List_BadBool_1400 覆盖核心断言：unused_only=abc 解析失败
// → HTTP 200 + code 1400（03 A1 错误码表），service 不被调用。
func TestProfileHandler_List_BadBool_1400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles?unused_only=abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (业务错误统一 200)", w.Code)
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
	if svc.listLast.Name != "" || svc.listLast.Page != 0 {
		t.Fatalf("非法 unused_only 不应触达 service: %+v", svc.listLast)
	}
}

// TestProfileHandler_List_Params 验证 query 全量透传：name/activity_level/
// dimension_code/unused_only=true 及分页参数进入 ProfileFilter（03 A1）。
func TestProfileHandler_List_Params(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{
		listRes: &service.ProfileListResult{List: []service.ProfileListItem{}},
	}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/profiles?name=%E5%BC%A0&activity_level=active&dimension_code=AI_BASE_CLARITY&unused_only=true&page=3&page_size=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	f := svc.listLast
	if f.Name != "张" || f.ActivityLevel != "active" || f.DimensionCode != "AI_BASE_CLARITY" {
		t.Fatalf("筛选透传错误: %+v", f)
	}
	if !f.UnusedOnly {
		t.Fatalf("unused_only = false, want true")
	}
	if f.Page != 3 || f.PageSize != 20 {
		t.Fatalf("page/page_size = %d/%d, want 3/20", f.Page, f.PageSize)
	}
}

// TestProfileHandler_List_DefaultPaging 验证 page/page_size 缺失或非法时兜底 1/10
//（specs A1 默认值；ParseInt 失败用默认值）。
func TestProfileHandler_List_DefaultPaging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{
		listRes: &service.ProfileListResult{List: []service.ProfileListItem{}},
	}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles?page=abc&page_size=xyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.listLast.Page != 1 || svc.listLast.PageSize != 10 {
		t.Fatalf("page/page_size 兜底失败: %d/%d, want 1/10", svc.listLast.Page, svc.listLast.PageSize)
	}
}

// TestProfileHandler_List_UpstreamFail_1305 验证 service 返 StaffListUnavailable(1305)
// 时 handler 正确映射（specs §5.1.4 规则1 整体失败）。
func TestProfileHandler_List_UpstreamFail_1305(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{listErr: service.NewError(errcode.StaffListUnavailable)}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
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

// TestProfileHandler_Export_Stream 覆盖核心断言：Content-Type 为 xlsx MIME、
// Content-Disposition 含 filename*=UTF-8'' 且 RFC 5987 编码承载中文名、body 与
// bytes 完全一致（不走 JSON 解包，03 §1.6 二进制流旁路）。
func TestProfileHandler_Export_Stream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	data := []byte("PK\x03\x04fake-xlsx-bytes")
	svc := &fakeProfileSvc{
		exportData: data,
		exportName: "人员画像名单_20260101.xlsx",
	}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/export?activity_level=active", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("Content-Type = %q, want xlsx MIME", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !bytes.Contains([]byte(cd), []byte("attachment;")) {
		t.Fatalf("Content-Disposition 缺 attachment: %q", cd)
	}
	if !bytes.Contains([]byte(cd), []byte("filename*=UTF-8''")) {
		t.Fatalf("Content-Disposition 缺 RFC 5987 filename*: %q", cd)
	}
	wantEnc := "filename*=UTF-8''%E4%BA%BA%E5%91%98%E7%94%BB%E5%83%8F%E5%90%8D%E5%8D%95_20260101.xlsx"
	if !bytes.Contains([]byte(cd), []byte(wantEnc)) {
		t.Fatalf("filename* 编码不符: got %q, want 含 %q", cd, wantEnc)
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("body 与导出 bytes 不一致: got %q, want %q", w.Body.String(), string(data))
	}
	// A2 无分页参数：filter 只承载筛选条件（03 A2）。
	if svc.exportLast.ActivityLevel != "active" || svc.exportLast.Page != 0 || svc.exportLast.PageSize != 0 {
		t.Fatalf("export filter 透传错误: %+v", svc.exportLast)
	}
}

// TestProfileHandler_Export_BadBool_1400 验证 A2 同样校验 unused_only 非布尔 1400。
func TestProfileHandler_Export_BadBool_1400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/export?unused_only=notabool", nil)
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
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400", resp.Code)
	}
}

// TestProfileHandler_Export_Fail_JSONError 覆盖核心断言：fake 返 *service.Error(1305)
// 时响应 Content-Type application/json、body code 1305（失败回统一 JSON 错误结构，03 §1.6）。
func TestProfileHandler_Export_Fail_JSONError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{exportErr: service.NewError(errcode.StaffListUnavailable)}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", ct)
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

// TestProfileHandler_Detail_OK 验证三 query 透传与统一响应（03 B1）。
func TestProfileHandler_Detail_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{
		detailRes: &service.ProfileDetailDTO{
			StaffName:     "张敏",
			ActivityLevel: "active",
			Periods:       []service.ProfilePeriod{},
		},
	}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet,
		"/api/profiles/detail?staff_name=%E5%BC%A0%E6%95%8F&period_start=2026-09-22&period_end=2026-09-28", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			StaffName     string `json:"staff_name"`
			ActivityLevel string `json:"activity_level"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.StaffName != "张敏" {
		t.Fatalf("data.staff_name = %q, want 张敏", resp.Data.StaffName)
	}
	if svc.detailLast.StaffName != "张敏" || svc.detailLast.PeriodStart != "2026-09-22" || svc.detailLast.PeriodEnd != "2026-09-28" {
		t.Fatalf("detail 透传错误: %+v", svc.detailLast)
	}
}

// TestProfileHandler_Detail_PeriodInvalid_2001 覆盖核心断言：fake 返 2001 时
// HTTP 200 + body code 2001（specs §5.2.4 规则1，前端提示并回落最新区间）。
func TestProfileHandler_Detail_PeriodInvalid_2001(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{detailErr: service.NewError(errcode.ProfilePeriodInvalid)}
	r := newProfileRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/detail?staff_name=%E5%BC%A0%E6%95%8F&period_start=2026-01-01&period_end=2026-01-07", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.ProfilePeriodInvalid {
		t.Fatalf("code = %d, want 2001", resp.Code)
	}
}

// fakeProfileAccountSvc 是生产同构 router 装配所需的 AccountService 桩
//（profile 测试不触达 account 路由，方法全零值，沿用 answer_test 范式）。
type fakeProfileAccountSvc struct{}

func (f *fakeProfileAccountSvc) Login(_ context.Context, _, _, _ string) (*service.LoginResult, error) {
	return nil, service.NewError(errcode.InvalidCredentials)
}
func (f *fakeProfileAccountSvc) GetCurrent(_ context.Context, _ int64) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeProfileAccountSvc) ListAccounts(_ context.Context, _ string, _, _ int) ([]service.AccountListItemDTO, int64, error) {
	return nil, 0, nil
}
func (f *fakeProfileAccountSvc) CreateAccount(_ context.Context, _, _, _, _ string, _ bool) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeProfileAccountSvc) UpdateAccount(_ context.Context, _ int64, _, _, _ string, _, _ bool) (*service.AccountDTO, error) {
	return nil, nil
}
func (f *fakeProfileAccountSvc) DeleteAccount(_ context.Context, _ int64) error         { return nil }
func (f *fakeProfileAccountSvc) ToggleEnabled(_ context.Context, _ int64, _ bool) error { return nil }
func (f *fakeProfileAccountSvc) ResetPassword(_ context.Context, _ int64, _, _ string) error {
	return nil
}

var _ service.AccountService = (*fakeProfileAccountSvc)(nil)

// newProfileEngine 起生产同构 router（miniredis 供中间件），profile handler 注入 fake，
// 参数位与生产 NewRouter 一致（profileHandler 在 scaleHandler 之后）。
func newProfileEngine(t *testing.T, svc *fakeProfileSvc) http.Handler {
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
		jwt.NewManager("profile-test-secret", time.Hour),
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
		handler.NewProfileHandler(svc),
		nil, // dashboardHandler
		nil, // workspaceHandler
		nil, // operationLogHandler
		nil, // recorder（操作日志记录通道，GET 路由不触达）
		rdb,
	)
}

// TestProfileRoutes_RequiresAuth 覆盖核心断言（03 §2.1 / BR3）：三路由均挂
// JWT 鉴权 auth 组，无 Authorization 头统一 401 + code 1003，不进入 handler。
func TestProfileRoutes_RequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{}
	engine := newProfileEngine(t, svc)

	for _, path := range []string{"/api/profiles", "/api/profiles/export", "/api/profiles/detail"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s status = %d, want 401, body=%s", path, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte(`"code":1003`)) {
			t.Fatalf("GET %s body missing code 1003, body=%s", path, w.Body.String())
		}
	}
	if svc.listLast.Page != 0 || svc.detailLast.StaffName != "" || svc.exportLast.Page != 0 {
		t.Fatalf("无 token 请求不应触达 handler: %+v", svc)
	}
}

// TestProfileRoutes_Registered 验证带合法 token 时三路由可达 handler（区分 404 与 401）。
func TestProfileRoutes_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeProfileSvc{
		listRes:    &service.ProfileListResult{List: []service.ProfileListItem{}},
		detailRes:  &service.ProfileDetailDTO{Periods: []service.ProfilePeriod{}},
		exportData: []byte("PK\x03\x04fake-xlsx-bytes"),
		exportName: "人员画像名单_20260101.xlsx",
	}
	engine := newProfileEngine(t, svc)
	jwtMgr := jwt.NewManager("profile-test-secret", time.Hour)
	token, err := jwtMgr.Generate(1, "profile-tester")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	for _, path := range []string{"/api/profiles", "/api/profiles/detail", "/api/profiles/export"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Fatalf("GET %s 返回 404，路由未注册", path)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200, body=%s", path, w.Code, w.Body.String())
		}
	}
}
