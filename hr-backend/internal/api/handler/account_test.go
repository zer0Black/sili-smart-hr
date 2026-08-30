package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/service"
)

// newRSAHandler 起一个 miniredis + 真实 rsakey.Manager，构造 AccountHandler。
// PublicKey 路径不触达 service.AccountService，svc 传 nil。
func newRSAHandler(t *testing.T) *handler.AccountHandler {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rsaMgr := rsakey.NewManager(rdb, 5*time.Minute)
	return handler.NewAccountHandler(nil, rsaMgr)
}

// TestPublicKey_Handler 覆盖核心断言：GET /api/auth/public-key 返回 code==0，
// data.publicKey 含 "BEGIN PUBLIC KEY"，data.keyId 长度 32，data.expiresIn==300。
func TestPublicKey_Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newRSAHandler(t)

	r := gin.New()
	r.GET("/api/auth/public-key", h.PublicKey)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/public-key", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Code int    `json:"code"`
		Data struct {
			PublicKey string `json:"publicKey"`
			KeyID     string `json:"keyId"`
			ExpiresIn int    `json:"expiresIn"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !strings.Contains(resp.Data.PublicKey, "BEGIN PUBLIC KEY") {
		t.Fatalf("publicKey missing PUBLIC KEY header, got: %q", resp.Data.PublicKey)
	}
	if len(resp.Data.KeyID) != 32 {
		t.Fatalf("keyId length = %d, want 32", len(resp.Data.KeyID))
	}
	if resp.Data.ExpiresIn != 300 {
		t.Fatalf("expiresIn = %d, want 300", resp.Data.ExpiresIn)
	}
}

// fakeAccountService 是 service.AccountService 的全功能假实现，承载可配置返回值字段，
// 供 6 个用户管理接口的 handler 测试使用。每个方法的入参以 last* 字段记录便于断言。
type fakeAccountService struct {
	// Login / GetCurrent（保留以满足接口，本组测试不触达）
	loginRes *service.LoginResult
	loginErr error
	currRes  *service.AccountDTO
	currErr  error

	// CreateAccount
	createLast struct {
		Username, Name, PasswordCipher, KeyID string
		Enabled                               bool
	}
	createRes *service.AccountDTO
	createErr error

	// UpdateAccount
	updateLast struct {
		ID                                     int64
		Name, PasswordCipher, KeyID            string
		HasPassword, Enabled                   bool
	}
	updateRes *service.AccountDTO
	updateErr error

	// DeleteAccount
	deleteLastID int64
	deleteErr    error

	// ToggleEnabled
	toggleLastID int64
	toggleLastEn bool
	toggleErr    error

	// ResetPassword
	resetLast struct {
		ID                            int64
		PasswordCipher, KeyID         string
	}
	resetErr error

	// ListAccounts
	listLast struct {
		Keyword       string
		Page, PageSize int
	}
	listRes   []service.AccountListItemDTO
	listTotal int64
	listErr   error
}

func (f *fakeAccountService) Login(_ context.Context, _, _, _ string) (*service.LoginResult, error) {
	return f.loginRes, f.loginErr
}

func (f *fakeAccountService) GetCurrent(_ context.Context, _ int64) (*service.AccountDTO, error) {
	return f.currRes, f.currErr
}

func (f *fakeAccountService) CreateAccount(_ context.Context, username, name, cipher, keyID string, enabled bool) (*service.AccountDTO, error) {
	f.createLast.Username = username
	f.createLast.Name = name
	f.createLast.PasswordCipher = cipher
	f.createLast.KeyID = keyID
	f.createLast.Enabled = enabled
	return f.createRes, f.createErr
}

func (f *fakeAccountService) UpdateAccount(_ context.Context, id int64, name, cipher, keyID string, hasPassword, enabled bool) (*service.AccountDTO, error) {
	f.updateLast.ID = id
	f.updateLast.Name = name
	f.updateLast.PasswordCipher = cipher
	f.updateLast.KeyID = keyID
	f.updateLast.HasPassword = hasPassword
	f.updateLast.Enabled = enabled
	return f.updateRes, f.updateErr
}

func (f *fakeAccountService) DeleteAccount(_ context.Context, id int64) error {
	f.deleteLastID = id
	return f.deleteErr
}

func (f *fakeAccountService) ToggleEnabled(_ context.Context, id int64, enabled bool) error {
	f.toggleLastID = id
	f.toggleLastEn = enabled
	return f.toggleErr
}

func (f *fakeAccountService) ResetPassword(_ context.Context, id int64, cipher, keyID string) error {
	f.resetLast.ID = id
	f.resetLast.PasswordCipher = cipher
	f.resetLast.KeyID = keyID
	return f.resetErr
}

func (f *fakeAccountService) ListAccounts(_ context.Context, keyword string, page, pageSize int) ([]service.AccountListItemDTO, int64, error) {
	f.listLast.Keyword = keyword
	f.listLast.Page = page
	f.listLast.PageSize = pageSize
	return f.listRes, f.listTotal, f.listErr
}

var _ service.AccountService = (*fakeAccountService)(nil)

// newAcctHandler 构造挂载 fakeAccountService 的 AccountHandler，rsaMgr/rdb 传 nil（用户管理路径不触达）。
func newAcctHandler(svc *fakeAccountService) *handler.AccountHandler {
	return handler.NewAccountHandler(svc, nil)
}

// doJSON 构造 POST JSON 请求并返回 recorder。
func doJSON(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestHandler_Create_Success 覆盖核心断言：POST /api/accounts/create 成功路径。
// fake.CreateAccount 返回 {ID:12,Username:"x",Name:"y",Enabled:true}；
// 响应 code==0，data.username=="x"，data.enabled==true，且 enabled 缺省时业务层收到 true。
func TestHandler_Create_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		createRes: &service.AccountDTO{ID: 12, Username: "x", Name: "y", Enabled: true},
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/create", h.Create)

	body := `{"username":"x","name":"y","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/accounts/create", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			Enabled  bool   `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Username != "x" {
		t.Fatalf("data.username = %q, want %q", resp.Data.Username, "x")
	}
	if resp.Data.ID != "12" {
		t.Fatalf("data.id = %q, want %q", resp.Data.ID, "12")
	}
	if !resp.Data.Enabled {
		t.Fatalf("data.enabled = false, want true")
	}
	// enabled 缺省时业务层应收到 true（*bool 指针区分缺省与显式 false，BR5）。
	if !svc.createLast.Enabled {
		t.Fatalf("svc.CreateAccount enabled arg = false, want true (default)")
	}
}

// TestHandler_Create_UsernameExists 覆盖核心断言：fake.CreateAccount 返回 UsernameExists(1005)，
// 响应 code==1005 且 HTTP 200（业务错误非鉴权类，统一 200 带 code）。
func TestHandler_Create_UsernameExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		createErr: service.NewError(errcode.UsernameExists),
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/create", h.Create)

	body := `{"username":"x","name":"y","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/accounts/create", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (业务错误统一 200)", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.UsernameExists {
		t.Fatalf("code = %d, want 1005", resp.Code)
	}
}

// TestHandler_Create_ExplicitEnabledFalse 验证 enabled 显式传 false 时业务层收到 false。
// 与 Success 用例互补，覆盖 *bool 指针的"显式 false"语义（BR5）。
func TestHandler_Create_ExplicitEnabledFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		createRes: &service.AccountDTO{ID: 13, Username: "x", Name: "y", Enabled: false},
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/create", h.Create)

	body := `{"username":"x","name":"y","passwordCipher":"c","keyId":"k","enabled":false}`
	w := doJSON(r, "/api/accounts/create", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.createLast.Enabled {
		t.Fatalf("svc.CreateAccount enabled arg = true, want false (explicit)")
	}
}

// TestHandler_Create_BindError 验证请求体缺字段绑定失败，返回 HTTP 400 + code 1400。
func TestHandler_Create_BindError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/create", h.Create)

	// 缺 name/passwordCipher/keyId。
	body := `{"username":"x"}`
	w := doJSON(r, "/api/accounts/create", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
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

// TestHandler_List 覆盖核心断言：fake.ListAccounts 返回 list 长度 1、total 1；
// 响应 code==0，data.list 长度 1，data.total==1，data.page==1，data.page_size==20。
// 默认分页参数（page=1, page_size=20）在 query 缺失时生效（BR1）。
func TestHandler_List(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	svc := &fakeAccountService{
		listRes: []service.AccountListItemDTO{
			{ID: 1, Username: "admin", Name: "管理员", Enabled: true, LastLoginAt: &now},
		},
		listTotal: 1,
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.GET("/api/accounts", h.List)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts?keyword=adm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List     []service.AccountListItemDTO `json:"list"`
			Total    int64                        `json:"total"`
			Page     int                          `json:"page"`
			PageSize int                          `json:"page_size"`
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
	if resp.Data.Total != 1 {
		t.Fatalf("data.total = %d, want 1", resp.Data.Total)
	}
	if resp.Data.Page != 1 {
		t.Fatalf("data.page = %d, want 1 (默认页码)", resp.Data.Page)
	}
	if resp.Data.PageSize != 20 {
		t.Fatalf("data.page_size = %d, want 20 (默认每页)", resp.Data.PageSize)
	}
	// keyword 透传到业务层。
	if svc.listLast.Keyword != "adm" {
		t.Fatalf("svc.ListAccounts keyword arg = %q, want %q", svc.listLast.Keyword, "adm")
	}
	// BR4：列表项不应有任何密码字段。AccountListItemDTO 结构本身无 PasswordHash，
	// 序列化字符串里不应出现 password 任何形态。
	if strings.Contains(strings.ToLower(w.Body.String()), "password") {
		t.Fatalf("响应包含 password 字样，违反 BR4（列表不含密码字段）: %s", w.Body.String())
	}
}

// TestHandler_List_PageParamParseError 验证 page/page_size 非法时兜底为默认 1/20。
func TestHandler_List_PageParamParseError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.GET("/api/accounts", h.List)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts?page=abc&page_size=xyz", nil)
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

// TestHandler_ToggleEnabled_LastEnabled 覆盖核心断言：fake.ToggleEnabled 返回 LastEnabledAccount(1006)，
// 响应 code==1006。
func TestHandler_ToggleEnabled_LastEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		toggleErr: service.NewError(errcode.LastEnabledAccount),
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/toggle-enabled", h.ToggleEnabled)

	body := `{"id":"1845700000000000012","enabled":false}`
	w := doJSON(r, "/api/accounts/toggle-enabled", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.LastEnabledAccount {
		t.Fatalf("code = %d, want 1006", resp.Code)
	}
	// id 字段 string → int64 解析正确（BR3）。
	if svc.toggleLastID != 1845700000000000012 {
		t.Fatalf("svc.ToggleEnabled id arg = %d, want 1845700000000000012", svc.toggleLastID)
	}
	if svc.toggleLastEn {
		t.Fatalf("svc.ToggleEnabled enabled arg = true, want false")
	}
}

// TestHandler_ToggleEnabled_Success 验证成功路径返回 data.id/data.enabled。
func TestHandler_ToggleEnabled_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/toggle-enabled", h.ToggleEnabled)

	body := `{"id":"1845700000000000012","enabled":true}`
	w := doJSON(r, "/api/accounts/toggle-enabled", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.ID != "1845700000000000012" {
		t.Fatalf("data.id = %q, want %q", resp.Data.ID, "1845700000000000012")
	}
	if !resp.Data.Enabled {
		t.Fatalf("data.enabled = false, want true")
	}
}

// TestHandler_ToggleEnabled_BadID 验证 id 非法数字时返回 400/1400。
func TestHandler_ToggleEnabled_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/toggle-enabled", h.ToggleEnabled)

	body := `{"id":"not-a-number","enabled":true}`
	w := doJSON(r, "/api/accounts/toggle-enabled", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
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

// TestHandler_Delete 覆盖核心断言：fake.DeleteAccount 返回 nil；
// 响应 code==0，data.id 与请求 id 一致。
func TestHandler_Delete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/delete", h.Delete)

	body := `{"id":"1845700000000000099"}`
	w := doJSON(r, "/api/accounts/delete", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.ID != "1845700000000000099" {
		t.Fatalf("data.id = %q, want %q", resp.Data.ID, "1845700000000000099")
	}
	if svc.deleteLastID != 1845700000000000099 {
		t.Fatalf("svc.DeleteAccount id arg = %d, want 1845700000000000099", svc.deleteLastID)
	}
}

// TestHandler_ResetPassword_BadPassword 覆盖核心断言：fake.ResetPassword 返回 PasswordInvalid(1007)，
// 响应 code==1007。
func TestHandler_ResetPassword_BadPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		resetErr: service.NewError(errcode.PasswordInvalid),
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/reset-password", h.ResetPassword)

	body := `{"id":"1845700000000000012","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/accounts/reset-password", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.PasswordInvalid {
		t.Fatalf("code = %d, want 1007", resp.Code)
	}
}

// TestHandler_ResetPassword_Success 验证成功路径返回 data.id。
func TestHandler_ResetPassword_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/reset-password", h.ResetPassword)

	body := `{"id":"1845700000000000012","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/accounts/reset-password", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.ID != "1845700000000000012" {
		t.Fatalf("data.id = %q, want %q", resp.Data.ID, "1845700000000000012")
	}
}

// TestHandler_Update_Success 验证编辑成功路径，含密码改与不改两条分支。
func TestHandler_Update_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		updateRes: &service.AccountDTO{ID: 1845700000000000012, Username: "x", Name: "y", Enabled: false},
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/update", h.Update)

	// 带 passwordCipher：hasPassword 应为 true。
	body := `{"id":"1845700000000000012","name":"y","passwordCipher":"newc","keyId":"k","enabled":false}`
	w := doJSON(r, "/api/accounts/update", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Name != "y" {
		t.Fatalf("data.name = %q, want %q", resp.Data.Name, "y")
	}
	if resp.Data.Enabled {
		t.Fatalf("data.enabled = true, want false")
	}
	if !svc.updateLast.HasPassword {
		t.Fatalf("svc.UpdateAccount hasPassword = false, want true (passwordCipher 非空)")
	}
	if svc.updateLast.ID != 1845700000000000012 {
		t.Fatalf("svc.UpdateAccount id = %d, want 1845700000000000012", svc.updateLast.ID)
	}
}

// TestHandler_Update_NoPassword 验证 passwordCipher 留空时 hasPassword 为 false。
func TestHandler_Update_NoPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAccountService{
		updateRes: &service.AccountDTO{ID: 1845700000000000012, Username: "x", Name: "y", Enabled: true},
	}
	h := newAcctHandler(svc)
	r := gin.New()
	r.POST("/api/accounts/update", h.Update)

	body := `{"id":"1845700000000000012","name":"y","enabled":true}`
	w := doJSON(r, "/api/accounts/update", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.updateLast.HasPassword {
		t.Fatalf("svc.UpdateAccount hasPassword = true, want false (passwordCipher 留空)")
	}
}
