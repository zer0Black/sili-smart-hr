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

// fakeIntegrationSecretService 是 service.IntegrationSecretService 的假实现，承载可配置返回值。
// 入参以 last* 字段记录便于断言。四方法分别对应 Get/Detail/Update/Test 四个 handler 路径。
type fakeIntegrationSecretService struct {
	// Get
	getRes *service.IntegrationSecretDTO
	getErr error

	// Detail
	detailRes *service.IntegrationSecretDetailDTO
	detailErr error

	// Update
	updateLast struct {
		Version int
		Secret  string
		KeyID   string
	}
	updateRes *service.UpdateSecretResult
	updateErr error

	// Test
	testRes *service.SecretTestResult
	testErr error
}

func (f *fakeIntegrationSecretService) Get(_ context.Context) (*service.IntegrationSecretDTO, error) {
	return f.getRes, f.getErr
}

func (f *fakeIntegrationSecretService) Detail(_ context.Context) (*service.IntegrationSecretDetailDTO, error) {
	return f.detailRes, f.detailErr
}

func (f *fakeIntegrationSecretService) Update(_ context.Context, version int, secretCipher, keyID string) (*service.UpdateSecretResult, error) {
	f.updateLast.Version = version
	f.updateLast.Secret = secretCipher
	f.updateLast.KeyID = keyID
	return f.updateRes, f.updateErr
}

func (f *fakeIntegrationSecretService) Test(_ context.Context) (*service.SecretTestResult, error) {
	return f.testRes, f.testErr
}

var _ service.IntegrationSecretService = (*fakeIntegrationSecretService)(nil)

func newIntegrationSecretHandler(svc *fakeIntegrationSecretService) *handler.IntegrationSecretHandler {
	return handler.NewIntegrationSecretHandler(svc)
}

// TestIntegrationSecret_Get 覆盖核心断言 + BR1：
// GET /api/integration-secret 返 200、code==0、data.configured 为 bool、data.secret_masked 存在。
func TestIntegrationSecret_Get(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		getRes: &service.IntegrationSecretDTO{
			ID:           1780000000000000201,
			SecretMasked: "sk-***ab12",
			Configured:   true,
		},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.GET("/api/integration-secret", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/integration-secret", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Configured   bool   `json:"configured"`
			SecretMasked string `json:"secret_masked"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !resp.Data.Configured {
		t.Fatalf("data.configured = false, want true")
	}
	if resp.Data.SecretMasked != "sk-***ab12" {
		t.Fatalf("data.secret_masked = %q, want %q", resp.Data.SecretMasked, "sk-***ab12")
	}
}

// TestIntegrationSecret_Get_Unconfigured 验证 configured=false 分支仍返 200、code==0。
func TestIntegrationSecret_Get_Unconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		getRes: &service.IntegrationSecretDTO{
			ID:           1780000000000000201,
			SecretMasked: "",
			Configured:   false,
		},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.GET("/api/integration-secret", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/integration-secret", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Configured bool `json:"configured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Configured {
		t.Fatalf("data.configured = true, want false")
	}
}

// TestIntegrationSecret_Detail_Success 覆盖核心断言：成功时 data.secret 明文存在。
func TestIntegrationSecret_Detail_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		detailRes: &service.IntegrationSecretDetailDTO{
			ID:     1780000000000000201,
			Secret: "sk-1a2b3c4d5e6fab12",
		},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.GET("/api/integration-secret/detail", h.Detail)

	req := httptest.NewRequest(http.MethodGet, "/api/integration-secret/detail", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.Secret != "sk-1a2b3c4d5e6fab12" {
		t.Fatalf("data.secret = %q, want 明文", resp.Data.Secret)
	}
}

// TestIntegrationSecret_Detail_NotConfigured 覆盖核心断言 + BR2：
// fake svc 返 1303 时 handler 正确映射 code==1303。
func TestIntegrationSecret_Detail_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		detailErr: service.NewError(errcode.IntegrationSecretNotConfigured),
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.GET("/api/integration-secret/detail", h.Detail)

	req := httptest.NewRequest(http.MethodGet, "/api/integration-secret/detail", nil)
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
	if resp.Code != errcode.IntegrationSecretNotConfigured {
		t.Fatalf("code = %d, want 1303", resp.Code)
	}
}

// TestIntegrationSecret_Update_Success 覆盖核心断言：
// 合法 body 返 200、code==0、data.configured==true。
func TestIntegrationSecret_Update_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		updateRes: &service.UpdateSecretResult{
			ID:         1780000000000000201,
			Configured: true,
		},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/update", h.Update)

	body := `{"secret":"cipher","keyId":"kid","version":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Configured bool `json:"configured"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !resp.Data.Configured {
		t.Fatalf("data.configured = false, want true")
	}
	// 入参透传。
	if svc.updateLast.Secret != "cipher" || svc.updateLast.KeyID != "kid" {
		t.Fatalf("svc.Update args mismatch: %+v", svc.updateLast)
	}
	if svc.updateLast.Version != 3 {
		t.Fatalf("svc.Update version = %d, want 3 (乐观锁凭证透传)", svc.updateLast.Version)
	}
}

// TestIntegrationSecret_Update_MissingSecret 覆盖核心断言 + BR2：缺 secret 返 1400。
func TestIntegrationSecret_Update_MissingSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/update", h.Update)

	// 缺 secret（补 version 隔离测试意图：仅缺 secret 走 1400）。
	body := `{"keyId":"kid","version":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/update", strings.NewReader(body))
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

// TestIntegrationSecret_Update_MissingKeyID 验证缺 keyId 同样走 1400。
func TestIntegrationSecret_Update_MissingKeyID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/update", h.Update)

	body := `{"secret":"cipher","version":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400 (缺 keyId)", resp.Code)
	}
}

// TestIntegrationSecret_Update_MissingVersion 验证缺 version（乐观锁凭证必填）走 1400。
func TestIntegrationSecret_Update_MissingVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/update", h.Update)

	body := `{"secret":"cipher","keyId":"kid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400 (缺 version)", resp.Code)
	}
}

// TestIntegrationSecret_Test_Connected 覆盖核心断言：
// fake svc 返 Connected==true 时 code==0、data.connected==true。
func TestIntegrationSecret_Test_Connected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		testRes: &service.SecretTestResult{Connected: true},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/test", h.Test)

	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Connected bool `json:"connected"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if !resp.Data.Connected {
		t.Fatalf("data.connected = false, want true")
	}
}

// TestIntegrationSecret_Test_Failed 覆盖核心断言 + BR2：
// fake svc 返 1304 时 code==1304 且 message 含失败原因（动态文案透传）。
// fake 文案对齐 classifiedError.Error() 只输出 cause 的真实格式（无 conversationlog: 哨兵前缀）。
func TestIntegrationSecret_Test_Failed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		testErr: &service.Error{Code: errcode.IntegrationSecretTestFailed, Msg: "密钥无效 (HTTP 401)"},
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/test", h.Test)

	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (业务错误统一 200)", w.Code)
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.IntegrationSecretTestFailed {
		t.Fatalf("code = %d, want 1304", resp.Code)
	}
	if !strings.Contains(resp.Message, "密钥无效") {
		t.Fatalf("message = %q, want 含失败原因（动态文案透传）", resp.Message)
	}
	// 守护 specs 330 口径：哨兵英文串不得泄漏进用户可见文案。
	if strings.Contains(resp.Message, "conversationlog:") {
		t.Fatalf("message leaked sentinel prefix, got %q", resp.Message)
	}
}

// TestIntegrationSecret_Test_NotConfigured 验证 1303 透传（BR2）。
func TestIntegrationSecret_Test_NotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeIntegrationSecretService{
		testErr: service.NewError(errcode.IntegrationSecretNotConfigured),
	}
	h := newIntegrationSecretHandler(svc)
	r := gin.New()
	r.POST("/api/integration-secret/test", h.Test)

	req := httptest.NewRequest(http.MethodPost, "/api/integration-secret/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.IntegrationSecretNotConfigured {
		t.Fatalf("code = %d, want 1303", resp.Code)
	}
}
