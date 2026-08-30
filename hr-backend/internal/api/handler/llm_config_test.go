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

// fakeLLMConfigService 是 service.LLMConfigService 的假实现，承载可配置返回值。
// 入参以 last* 字段记录便于断言。六方法分别对应 List/Detail/Create/Update/Delete/Enable 六个 handler 路径。
type fakeLLMConfigService struct {
	// List
	listLast struct {
		Keyword string
	}
	listRes []service.LLMConfigDTO
	listErr error

	// Detail
	detailLast struct {
		ID int64
	}
	detailRes *service.LLMConfigDetailDTO
	detailErr error

	// Create
	createLast struct {
		Name, Provider, ModelID, APIURL, APIKey, KeyID string
	}
	createRes *service.CreateLLMResult
	createErr error

	// Update
	updateLast struct {
		ID                            int64
		Version                       int
		Name, Provider, ModelID       string
		APIURL, APIKey, KeyID         string
		HasAPIKey                     bool
	}
	updateRes *service.CreateLLMResult
	updateErr error

	// Delete
	deleteLast struct {
		ID int64
	}
	deleteRes *service.DeleteLLMResult
	deleteErr error

	// Enable
	enableLast struct {
		ID int64
	}
	enableRes *service.LLMEnableResult
	enableErr error
}

func (f *fakeLLMConfigService) List(_ context.Context, keyword string) ([]service.LLMConfigDTO, error) {
	f.listLast.Keyword = keyword
	return f.listRes, f.listErr
}

func (f *fakeLLMConfigService) Detail(_ context.Context, id int64) (*service.LLMConfigDetailDTO, error) {
	f.detailLast.ID = id
	return f.detailRes, f.detailErr
}

func (f *fakeLLMConfigService) Create(
	_ context.Context,
	name, provider, modelID, apiURL, apiKeyCipher, keyID string,
) (*service.CreateLLMResult, error) {
	f.createLast.Name = name
	f.createLast.Provider = provider
	f.createLast.ModelID = modelID
	f.createLast.APIURL = apiURL
	f.createLast.APIKey = apiKeyCipher
	f.createLast.KeyID = keyID
	return f.createRes, f.createErr
}

func (f *fakeLLMConfigService) Update(
	_ context.Context,
	id int64,
	version int,
	name, provider, modelID, apiURL, apiKeyCipher, keyID string,
	hasAPIKey bool,
) (*service.CreateLLMResult, error) {
	f.updateLast.ID = id
	f.updateLast.Version = version
	f.updateLast.Name = name
	f.updateLast.Provider = provider
	f.updateLast.ModelID = modelID
	f.updateLast.APIURL = apiURL
	f.updateLast.APIKey = apiKeyCipher
	f.updateLast.KeyID = keyID
	f.updateLast.HasAPIKey = hasAPIKey
	return f.updateRes, f.updateErr
}

func (f *fakeLLMConfigService) Delete(_ context.Context, id int64) (*service.DeleteLLMResult, error) {
	f.deleteLast.ID = id
	return f.deleteRes, f.deleteErr
}

func (f *fakeLLMConfigService) Enable(_ context.Context, id int64) (*service.LLMEnableResult, error) {
	f.enableLast.ID = id
	return f.enableRes, f.enableErr
}

var _ service.LLMConfigService = (*fakeLLMConfigService)(nil)

func newLLMConfigHandler(svc *fakeLLMConfigService) *handler.LLMConfigHandler {
	return handler.NewLLMConfigHandler(svc)
}

// TestLLMConfig_List 覆盖核心断言 + BR1：GET /api/llm-configs?keyword=deep 返 200、code==0、data 为数组。
func TestLLMConfig_List(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		listRes: []service.LLMConfigDTO{
			{ID: 1780000000000000101, Name: "主力", Provider: "deepseek", ModelID: "deepseek-chat", APIKeyMasked: "sk-1***ab12", Enabled: true},
		},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.GET("/api/llm-configs", h.List)

	req := httptest.NewRequest(http.MethodGet, "/api/llm-configs?keyword=deep", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Provider string `json:"provider"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	// data 直接是数组（非分页）。
	if len(resp.Data) != 1 {
		t.Fatalf("data len = %d, want 1（非分页数组）", len(resp.Data))
	}
	if resp.Data[0].Name != "主力" {
		t.Fatalf("data[0].name = %q, want %q", resp.Data[0].Name, "主力")
	}
	// 雪花 ID 必须 string 化（BR2）。
	if resp.Data[0].ID != "1780000000000000101" {
		t.Fatalf("data[0].id = %q, want %q (BR2 雪花 ID string 化)", resp.Data[0].ID, "1780000000000000101")
	}
	// query 透传。
	if svc.listLast.Keyword != "deep" {
		t.Fatalf("svc.List keyword = %q, want %q", svc.listLast.Keyword, "deep")
	}
}

// TestLLMConfig_List_Empty 验证空列表仍返数组（非 null）。
func TestLLMConfig_List_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{listRes: []service.LLMConfigDTO{}}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.GET("/api/llm-configs", h.List)

	req := httptest.NewRequest(http.MethodGet, "/api/llm-configs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// 空 list 由 handler 构造为 [] 而非 nil。
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("empty list 应序列化为 []，body=%s", w.Body.String())
	}
}

// TestLLMConfig_Create_Success 覆盖核心断言：合法 body 返 200、code==0、data.id 为字符串。
func TestLLMConfig_Create_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		createRes: &service.CreateLLMResult{ID: 1780000000000000102},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/create", h.Create)

	body := `{"name":"主力","provider":"deepseek","model_id":"deepseek-chat","api_url":"","api_key":"cipher","keyId":"kid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

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
	if resp.Data.ID != "1780000000000000102" {
		t.Fatalf("data.id = %q, want %q", resp.Data.ID, "1780000000000000102")
	}
	// 入参透传。
	if svc.createLast.Name != "主力" || svc.createLast.Provider != "deepseek" ||
		svc.createLast.ModelID != "deepseek-chat" || svc.createLast.APIKey != "cipher" || svc.createLast.KeyID != "kid" {
		t.Fatalf("svc.Create args mismatch: %+v", svc.createLast)
	}
}

// TestLLMConfig_Create_MissingName 覆盖核心断言：缺 name 返 200、code==1400。
func TestLLMConfig_Create_MissingName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/create", h.Create)

	// 缺 name。
	body := `{"provider":"deepseek","model_id":"deepseek-chat","api_key":"cipher","keyId":"kid"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/create", strings.NewReader(body))
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

// TestLLMConfig_Update_Success 验证 update 透传 + hasAPIKey=false（api_key 留空不改）。
func TestLLMConfig_Update_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		updateRes: &service.CreateLLMResult{ID: 1780000000000000101},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/update", h.Update)

	body := `{"id":"1780000000000000101","version":3,"name":"主力-改","provider":"deepseek","model_id":"deepseek-chat","api_url":"https://x.example.com/v1","api_key":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if svc.updateLast.ID != 1780000000000000101 {
		t.Fatalf("svc.Update id = %d, want 1780000000000000101", svc.updateLast.ID)
	}
	if svc.updateLast.Version != 3 {
		t.Fatalf("svc.Update version = %d, want 3 (乐观锁凭证透传)", svc.updateLast.Version)
	}
	if svc.updateLast.HasAPIKey != false {
		t.Fatalf("svc.Update hasAPIKey = %v, want false（api_key 留空不改）", svc.updateLast.HasAPIKey)
	}
}

// TestLLMConfig_Update_WithAPIKey 验证 hasAPIKey=true 透传。
func TestLLMConfig_Update_WithAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		updateRes: &service.CreateLLMResult{ID: 1},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/update", h.Update)

	body := `{"id":"1780000000000000101","version":1,"name":"主力","provider":"deepseek","model_id":"deepseek-chat","api_key":"newcipher","keyId":"kid2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !svc.updateLast.HasAPIKey {
		t.Fatalf("svc.Update hasAPIKey = false, want true")
	}
	if svc.updateLast.APIKey != "newcipher" || svc.updateLast.KeyID != "kid2" {
		t.Fatalf("svc.Update api_key/keyId mismatch: %+v", svc.updateLast)
	}
}

// TestLLMConfig_Update_BadID 验证 id 非数字走 1400。
func TestLLMConfig_Update_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/update", h.Update)

	// 补 version 隔离测试意图：仅非数字 id 走 1400。
	body := `{"id":"abc","version":1,"name":"主力","provider":"deepseek","model_id":"deepseek-chat"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400 (非数字 id)", resp.Code)
	}
}

// TestLLMConfig_Update_MissingVersion 验证缺 version（乐观锁凭证必填）走 1400。
func TestLLMConfig_Update_MissingVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/update", h.Update)

	body := `{"id":"1780000000000000101","name":"主力","provider":"deepseek","model_id":"deepseek-chat"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/update", strings.NewReader(body))
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

// TestLLMConfig_Delete_Success 覆盖核心断言 + BR2：返 200、code==0、data.transferred_enabled_id 为字符串。
func TestLLMConfig_Delete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	transferred := int64(1780000000000000102)
	svc := &fakeLLMConfigService{
		deleteRes: &service.DeleteLLMResult{ID: 1780000000000000101, TransferredEnabledID: &transferred},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/delete", h.Delete)

	body := `{"id":"1780000000000000101"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID                   string `json:"id"`
			TransferredEnabledID string `json:"transferred_enabled_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.TransferredEnabledID != "1780000000000000102" {
		t.Fatalf("data.transferred_enabled_id = %q, want %q (BR2 string 化)",
			resp.Data.TransferredEnabledID, "1780000000000000102")
	}
}

// TestLLMConfig_Delete_NullTransferred 验证 transferred_enabled_id 为 null（删非启用态）。
func TestLLMConfig_Delete_NullTransferred(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		deleteRes: &service.DeleteLLMResult{ID: 1780000000000000101, TransferredEnabledID: nil},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/delete", h.Delete)

	body := `{"id":"1780000000000000101"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !strings.Contains(w.Body.String(), `"transferred_enabled_id":null`) {
		t.Fatalf("nil transferred 应序列化为 null, body=%s", w.Body.String())
	}
}

// TestLLMConfig_Delete_LastConfig 覆盖核心断言：fake svc 返 1302 时 handler 正确映射 code==1302。
func TestLLMConfig_Delete_LastConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		deleteErr: service.NewError(errcode.LastLLMConfig),
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/delete", h.Delete)

	body := `{"id":"1780000000000000101"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/delete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != errcode.LastLLMConfig {
		t.Fatalf("code = %d, want 1302", resp.Code)
	}
}

// TestLLMConfig_Enable_Success 验证 enable 透传 + 返 200 code==0 + data.{id,enabled:true}（03 §B5）。
func TestLLMConfig_Enable_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		enableRes: &service.LLMEnableResult{ID: 1780000000000000102, Enabled: true},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/enable", h.Enable)

	body := `{"id":"1780000000000000102"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/enable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if svc.enableLast.ID != 1780000000000000102 {
		t.Fatalf("svc.Enable id = %d, want 1780000000000000102", svc.enableLast.ID)
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
	if resp.Data.ID != "1780000000000000102" || !resp.Data.Enabled {
		t.Fatalf("data = %+v, want {id:1780000000000000102, enabled:true} (03 §B5)", resp.Data)
	}
}

// TestLLMConfig_Enable_NotFound 验证 service 返 1301 时映射正确。
func TestLLMConfig_Enable_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		enableErr: service.NewError(errcode.LLMConfigNotFound),
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.POST("/api/llm-configs/enable", h.Enable)

	body := `{"id":"1780000000000000199"}`
	req := httptest.NewRequest(http.MethodPost, "/api/llm-configs/enable", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.LLMConfigNotFound {
		t.Fatalf("code = %d, want 1301", resp.Code)
	}
}

// TestLLMConfig_Detail 覆盖核心断言：GET /api/llm-configs/:id 返 200、code==0、data.api_key 明文存在。
func TestLLMConfig_Detail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		detailRes: &service.LLMConfigDetailDTO{
			ID:       1780000000000000101,
			Name:     "主力",
			Provider: "deepseek",
			ModelID:  "deepseek-chat",
			APIURL:   "",
			APIKey:   "sk-1a2b3c4d5e6fab12",
			Enabled:  true,
		},
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.GET("/llm-configs/:id", h.Detail)

	req := httptest.NewRequest(http.MethodGet, "/llm-configs/1780000000000000101", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			ID     string `json:"id"`
			APIKey string `json:"api_key"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, want 0", resp.Code)
	}
	if resp.Data.APIKey != "sk-1a2b3c4d5e6fab12" {
		t.Fatalf("data.api_key = %q, want 明文", resp.Data.APIKey)
	}
	if resp.Data.ID != "1780000000000000101" {
		t.Fatalf("data.id = %q, want %q (BR2)", resp.Data.ID, "1780000000000000101")
	}
	if svc.detailLast.ID != 1780000000000000101 {
		t.Fatalf("svc.Detail id = %d, want 1780000000000000101", svc.detailLast.ID)
	}
}

// TestLLMConfig_Detail_BadID 验证路径参数非数字走 1400。
func TestLLMConfig_Detail_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.GET("/llm-configs/:id", h.Detail)

	req := httptest.NewRequest(http.MethodGet, "/llm-configs/abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.BadRequest {
		t.Fatalf("code = %d, want 1400 (非数字路径参数)", resp.Code)
	}
}

// TestLLMConfig_Detail_NotFound 验证 service 返 1301 时映射正确。
func TestLLMConfig_Detail_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeLLMConfigService{
		detailErr: service.NewError(errcode.LLMConfigNotFound),
	}
	h := newLLMConfigHandler(svc)
	r := gin.New()
	r.GET("/llm-configs/:id", h.Detail)

	req := httptest.NewRequest(http.MethodGet, "/llm-configs/1780000000000000199", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != errcode.LLMConfigNotFound {
		t.Fatalf("code = %d, want 1301", resp.Code)
	}
}
