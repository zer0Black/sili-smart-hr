package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/service"
)

// fakeSetupService 是 service.SetupService 的假实现，承载可配置返回值字段。
// Initialize 入参以 lastInit 字段记录便于断言。
type fakeSetupService struct {
	getStatusStatus *service.SetupStatus
	getStatusErr    error

	initLast struct {
		Username, Name, PasswordCipher, KeyID string
	}
	initResult *service.SetupResult
	initErr    error
}

func (f *fakeSetupService) GetStatus(_ context.Context) (*service.SetupStatus, error) {
	return f.getStatusStatus, f.getStatusErr
}

func (f *fakeSetupService) Initialize(_ context.Context, username, name, cipher, keyID string) (*service.SetupResult, error) {
	f.initLast.Username = username
	f.initLast.Name = name
	f.initLast.PasswordCipher = cipher
	f.initLast.KeyID = keyID
	return f.initResult, f.initErr
}

var _ service.SetupService = (*fakeSetupService)(nil)

// TestSetupHandler_Status 覆盖核心断言：GET /api/setup/status 响应 code==0，
// data.initialized/db_type/checks.database.connected/checks.redis.connected/block_submit
// 字段齐备且值映射正确（specs §3.1）。
func TestSetupHandler_Status(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSetupService{
		getStatusStatus: &service.SetupStatus{
			Initialized:       false,
			DBType:            "sqlite",
			DatabaseConnected: true,
			RedisConnected:    true,
			BlockSubmit:       false,
		},
	}
	h := handler.NewSetupHandler(svc)
	r := gin.New()
	r.GET("/api/setup/status", h.Status)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Initialized bool   `json:"initialized"`
			DBType      string `json:"db_type"`
			Checks      struct {
				Database struct{ Connected bool } `json:"database"`
				Redis    struct{ Connected bool } `json:"redis"`
			} `json:"checks"`
			BlockSubmit bool `json:"block_submit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Initialized {
		t.Fatalf("data.initialized = true, want false")
	}
	if resp.Data.DBType != "sqlite" {
		t.Fatalf("data.db_type = %q, want %q", resp.Data.DBType, "sqlite")
	}
	if !resp.Data.Checks.Database.Connected {
		t.Fatalf("data.checks.database.connected = false, want true")
	}
	if !resp.Data.Checks.Redis.Connected {
		t.Fatalf("data.checks.redis.connected = false, want true")
	}
	if resp.Data.BlockSubmit {
		t.Fatalf("data.block_submit = true, want false")
	}
}

// TestSetupHandler_Initialize_Success 覆盖核心断言：POST /api/setup/initialize 成功，
// fake.Initialize 返 {Initialized:true, Account:{ID:12,...}}；响应 code==0，
// data.initialized==true，data.account.id=="12"（雪花 ID JSON string 化，BR12 关键断言）。
func TestSetupHandler_Initialize_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSetupService{
		initResult: &service.SetupResult{
			Initialized: true,
			Account:     &service.AccountDTO{ID: 12, Username: "x", Name: "y", Enabled: true},
		},
	}
	h := handler.NewSetupHandler(svc)
	r := gin.New()
	r.POST("/api/setup/initialize", h.Initialize)

	body := `{"username":"x","name":"y","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/setup/initialize", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Initialized bool `json:"initialized"`
			Account     struct {
				ID string `json:"id"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !resp.Data.Initialized {
		t.Fatalf("data.initialized = false, want true")
	}
	if resp.Data.Account.ID != "12" {
		t.Fatalf("data.account.id = %q, want %q (雪花 ID string 化, BR12)", resp.Data.Account.ID, "12")
	}
	// 入参透传到 service（探针）。
	if svc.initLast.Username != "x" || svc.initLast.Name != "y" || svc.initLast.PasswordCipher != "c" || svc.initLast.KeyID != "k" {
		t.Fatalf("svc.Initialize args mismatch: %+v", svc.initLast)
	}
}

// TestSetupHandler_Initialize_AlreadyInit 覆盖核心断言：fake.Initialize 返 SystemAlreadyInitialized(1101)，
// 响应 code==1101（HTTP 200，业务错误非鉴权统一 200 带 code）。
func TestSetupHandler_Initialize_AlreadyInit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSetupService{
		initErr: service.NewError(errcode.SystemAlreadyInitialized),
	}
	h := handler.NewSetupHandler(svc)
	r := gin.New()
	r.POST("/api/setup/initialize", h.Initialize)

	body := `{"username":"x","name":"y","passwordCipher":"c","keyId":"k"}`
	w := doJSON(r, "/api/setup/initialize", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (业务错误统一 200)", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.SystemAlreadyInitialized {
		t.Fatalf("code = %d, want %d", resp.Code, errcode.SystemAlreadyInitialized)
	}
}

// TestSetupHandler_Initialize_BindError 验证请求体缺字段绑定失败，返回 HTTP 400 + code 1400。
func TestSetupHandler_Initialize_BindError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeSetupService{}
	h := handler.NewSetupHandler(svc)
	r := gin.New()
	r.POST("/api/setup/initialize", h.Initialize)

	// 缺 name/passwordCipher/keyId。
	body := `{"username":"x"}`
	w := doJSON(r, "/api/setup/initialize", body)

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
		t.Fatalf("code = %d, want %d", resp.Code, errcode.BadRequest)
	}

	// 防御性：service 未被触达（绑定失败不应进业务层）。
	if svc.initLast.Username != "" {
		t.Fatalf("svc.Initialize should not be called on bind error, got username=%q", svc.initLast.Username)
	}
}
