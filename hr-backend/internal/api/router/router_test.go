package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/service"
)

// fakeAccountSvc 是 router 集成测试用的 service.AccountService 桩。
// Login 恒返回业务错误，验证请求穿透中间件进入 handler；
// loginHits 探针记录实际进入 handler 的次数。
// 其余方法（用户管理）为编译期接口契约桩，返回零值不参与路由层断言。
type fakeAccountSvc struct {
	loginHits int32
}

func (f *fakeAccountSvc) Login(ctx context.Context, username, passwordCipher, keyID string) (*service.LoginResult, error) {
	atomic.AddInt32(&f.loginHits, 1)
	return nil, service.NewError(1401) // 任意业务错误码都表示请求穿透中间件进入 handler
}

func (f *fakeAccountSvc) GetCurrent(ctx context.Context, accountID int64) (*service.AccountDTO, error) {
	return nil, nil
}

func (f *fakeAccountSvc) ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]service.AccountListItemDTO, int64, error) {
	return nil, 0, nil
}

func (f *fakeAccountSvc) CreateAccount(ctx context.Context, username, name, passwordCipher, keyID string, enabled bool) (*service.AccountDTO, error) {
	return nil, nil
}

func (f *fakeAccountSvc) UpdateAccount(ctx context.Context, id int64, name, passwordCipher, keyID string, hasPassword bool, enabled bool) (*service.AccountDTO, error) {
	return nil, nil
}

func (f *fakeAccountSvc) DeleteAccount(ctx context.Context, id int64) error {
	return nil
}

func (f *fakeAccountSvc) ToggleEnabled(ctx context.Context, id int64, enabled bool) error {
	return nil
}

func (f *fakeAccountSvc) ResetPassword(ctx context.Context, id int64, passwordCipher, keyID string) error {
	return nil
}

// newAuthRouter 构造一个与生产 NewRouter 同构的 engine，公开区含公钥接口（60/min IP）
// 与登录接口（IP 30/min + username 20/min 两层）的限流，rdb 由 miniredis 提供。
// setupHandler/systemHandler/dimensionHandler 由调用方传入：auth 相关测试传 nil（不触达对应路由），
// 对应测试注入真实 handler。
func newAuthRouter(t *testing.T, svc service.AccountService, setupHandler *handler.SetupHandler, systemHandler *handler.SystemHandler, dimensionHandler *handler.DimensionHandler) (http.Handler, *fakeAccountSvc, *redis.Client) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	rsaMgr := rsakey.NewManager(rdb, 5*time.Minute)
	accountHandler := handler.NewAccountHandler(svc, rsaMgr)
	healthHandler := handler.NewHealthHandler()
	jwtMgr := jwt.NewManager("router-test-secret", time.Hour)
	cfg := &config.Config{}

	engine := router.NewRouter(cfg, jwtMgr, accountHandler, healthHandler, setupHandler, systemHandler, dimensionHandler, nil, nil, nil, rdb)
	return engine, svc.(*fakeAccountSvc), rdb
}

// TestLoginRoute_IPRateLimited 覆盖核心断言：同 IP 连发 31 次 POST /api/login，
// 前 30 次进入 handler（任何 code 都算通过限流），第 31 次返回 HTTP 429。
func TestLoginRoute_IPRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	engine, fake, _ := newAuthRouter(t, fake, nil, nil, nil)

	// 每轮用不同 username 避开 username 维度限流（中间件层，20/min），
	// 专测 IP 维度 30/min。前 30 次必须全部进入 handler。
	for i := 0; i < 30; i++ {
		body := `{"username":"u` + strconv.Itoa(i) + `","passwordCipher":"y","keyId":"z"}`
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("req #%d unexpectedly rate-limited, status=429, body=%s", i+1, w.Body.String())
		}
	}
	if got := atomic.LoadInt32(&fake.loginHits); got != 30 {
		t.Fatalf("login handler hits = %d, want 30", got)
	}

	// 第 31 次同 IP 被 IP 维度限流拦截（n=31 > 30），走不到 username 维度。
	finalBody := `{"username":"final","passwordCipher":"y","keyId":"z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(finalBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("req #31: status = %d, want 429, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&fake.loginHits); got != 30 {
		t.Fatalf("login handler hits after block = %d, want still 30", got)
	}
}

// TestLoginRoute_UsernameRateLimited 覆盖 username 维度限流（中间件层 JSONFieldKey，20/min）：
// 用同一 username 的不同大小写/空白写法（"Admin"/"admin"/" admin "）连发 21 次，
// 验证 JSONFieldKey 的 trim+lower 归一化让三者合并到同一桶：前 20 次进入 handler，
// 第 21 次被 username 维度拦截 HTTP 429。全程同 IP 21 次 < IP 维度 30/min，不触发 IP 维度。
// 归一化若失效，三变体各计 7 次都不超 20，21 次将全部进入 handler，loginHits=21 使断言失败。
func TestLoginRoute_UsernameRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	engine, fake, _ := newAuthRouter(t, fake, nil, nil, nil)

	variants := []string{`"Admin"`, `"admin"`, `" admin "`}
	for i := 0; i < 21; i++ {
		body := `{"username":` + variants[i%len(variants)] + `,"passwordCipher":"y","keyId":"z"}`
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if i < 20 {
			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("req #%d unexpectedly rate-limited, body=%s", i+1, w.Body.String())
			}
		} else {
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("req #21: status = %d, want 429 (username 维度), body=%s", w.Code, w.Body.String())
			}
		}
	}
	if got := atomic.LoadInt32(&fake.loginHits); got != 20 {
		t.Fatalf("login handler hits = %d, want 20 (归一化后同桶，第 21 次被 username 维度拦截)", got)
	}
}

// TestPublicKeyRoute_RateLimited 验证公钥接口 IP 限流（60/min）：连发 61 次，
// 前 60 次进入 handler（200），第 61 次返回 429。
func TestPublicKeyRoute_RateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	engine, _, _ := newAuthRouter(t, fake, nil, nil, nil)

	for i := 0; i < 60; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/public-key", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("req #%d: status = %d, want 200", i+1, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/public-key", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("req #61: status = %d, want 429", w.Code)
	}
}

// TestPublicKeyRoute_Registered 验证公钥路由已注册并返回 specs §1.1 规定的字段。
func TestPublicKeyRoute_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	engine, _, _ := newAuthRouter(t, fake, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/public-key", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, key := range []string{`"publicKey"`, `"keyId"`, `"expiresIn"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("response body missing field %s, body=%s", key, body)
		}
	}
}

// TestAccountsRoute_RequiresAuth 覆盖核心断言：6 个账号台账受保护路由均挂 JWT 中间件，
// 无 Authorization 头访问 GET /api/accounts 时被中间件拦截，返回 401 + code 1003，
// 不进入 handler（specs 03 受保护组约定）。
func TestAccountsRoute_RequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	engine, _, _ := newAuthRouter(t, fake, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":1003`) {
		t.Fatalf("response body missing code 1003, body=%s", w.Body.String())
	}
	// 6 个台账动作接口同样应被 JWT 拦截在 handler 之外（一致性扫一遍）。
	for _, path := range []string{
		"/api/accounts/create",
		"/api/accounts/update",
		"/api/accounts/delete",
		"/api/accounts/toggle-enabled",
		"/api/accounts/reset-password",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"code":1003`) {
			t.Fatalf("path %s: status=%d body=%s, want 401 + code 1003", path, w.Code, w.Body.String())
		}
	}
}

// fakeSetupSvc 是 router 集成测试用的 service.SetupService 桩，返回固定未初始化状态。
type fakeSetupSvc struct{}

func (f *fakeSetupSvc) GetStatus(context.Context) (*service.SetupStatus, error) {
	return &service.SetupStatus{
		Initialized:       false,
		DBType:            "sqlite",
		DatabaseConnected: true,
		RedisConnected:    true,
		BlockSubmit:       false,
	}, nil
}

func (f *fakeSetupSvc) Initialize(context.Context, string, string, string, string) (*service.SetupResult, error) {
	return nil, nil
}

var _ service.SetupService = (*fakeSetupSvc)(nil)

// TestSetupStatusRoute_Registered 验证 setup status 路由已注册并返回 specs §3.1 规定的字段，
// 且处于公开路由组（无 Authorization 头可访问，不挂 JWT 中间件，BR11）。
func TestSetupStatusRoute_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	setupH := handler.NewSetupHandler(&fakeSetupSvc{})
	engine, _, _ := newAuthRouter(t, fake, setupH, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (公开路由不挂 JWT)", w.Code)
	}
	body := w.Body.String()
	for _, key := range []string{`"initialized"`, `"db_type"`, `"checks"`, `"database"`, `"redis"`, `"block_submit"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("response body missing field %s, body=%s", key, body)
		}
	}
}

// fakeSystemSvc 是 router 集成测试用的 service.SystemService 桩，返回固定摘要。
type fakeSystemSvc struct{}

func (f *fakeSystemSvc) GetSummary(context.Context) (*service.SystemSummary, error) {
	return &service.SystemSummary{
		Initialized: true,
		DBType:      "sqlite",
		Version:     "v0.1.0",
		StartedAt:   time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
	}, nil
}

func (f *fakeSystemSvc) HealthCheck(context.Context) (*service.HealthResult, error) {
	return &service.HealthResult{
		Database:    service.StatusConnected,
		Redis:       service.StatusConnected,
		LLM:         service.StatusNotConfigured,
		Integration: service.StatusNotConfigured,
	}, nil
}

var _ service.SystemService = (*fakeSystemSvc)(nil)

// TestSystemStatusRoute_Registered 验证 system status 路由已注册并返回 specs §3.3 规定的字段，
// 且处于公开路由组（无 Authorization 头可访问，不挂 JWT 中间件，BR23）。
func TestSystemStatusRoute_Registered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	systemH := handler.NewSystemHandler(&fakeSystemSvc{})
	engine, _, _ := newAuthRouter(t, fake, nil, systemH, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (公开路由不挂 JWT)", w.Code)
	}
	body := w.Body.String()
	for _, key := range []string{`"initialized"`, `"db_type"`, `"version"`, `"started_at"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("response body missing field %s, body=%s", key, body)
		}
	}
}

// TestSystemHealthCheck_RequiresAuth 验证 health-check 归入 auth 组：
// 无 Authorization 头访问 POST /api/system/health-check 被 JWT 中间件拦截返 401 + code 1003（BR23）。
func TestSystemHealthCheck_RequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccountSvc{}
	systemH := handler.NewSystemHandler(&fakeSystemSvc{})
	engine, _, _ := newAuthRouter(t, fake, nil, systemH, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/system/health-check", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":1003`) {
		t.Fatalf("response body missing code 1003, body=%s", w.Body.String())
	}
}
