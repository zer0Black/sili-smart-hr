package conversationlog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startTestServer 启动一个按 handler 返回响应的 httptest 服务，返回其可达 baseURL。
func startTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestPingURLContract 落地 specs §2.4 能力3 与 4.3 改造清单：探活 URL 两处修正
// （page→p、路径补尾斜杠）。捕获实际请求断言路径 /api/conversation-log/、
// query p=1&page_size=1 与 Bearer 头（BR1）。
func TestPingURLContract(t *testing.T) {
	var gotPath, gotP, gotPageSize, gotAuth string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotP = r.URL.Query().Get("p")
		gotPageSize = r.URL.Query().Get("page_size")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"code":0,"message":"ok","data":{"list":[],"total":0}}`)
	})

	c := NewClient(baseURL)
	if err := c.Ping(context.Background(), "test-secret"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/api/conversation-log/" {
		t.Fatalf("path: got %q want /api/conversation-log/", gotPath)
	}
	if gotP != "1" {
		t.Fatalf("p: got %q want 1", gotP)
	}
	if gotPageSize != "1" {
		t.Fatalf("page_size: got %q want 1", gotPageSize)
	}
	if gotAuth != "Bearer test-secret" {
		t.Fatalf("Authorization: got %q want Bearer test-secret", gotAuth)
	}
}

// TestPingErrorClassification 落地 specs §2.3 错误分类（Ping 接线 sentinel）：
// 401/403 → ErrUnauthorized；其余非 2xx（500）→ ErrUnexpectedStatus；
// 探活单次语义不重试。文案断言锚定「语义短语 + 状态码」子串（specs §6.4
// 401/403 分流定位信息承载在文案），前端经 1304 透传渲染，此处是文案的唯一守护。
func TestPingErrorClassification(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr error
		wantMsg string
	}{
		{name: "401 unauthorized", status: http.StatusUnauthorized, wantErr: ErrUnauthorized, wantMsg: "密钥无效 (HTTP 401)"},
		{name: "403 forbidden", status: http.StatusForbidden, wantErr: ErrUnauthorized, wantMsg: "密钥无效 (HTTP 403)"},
		{name: "500 unexpected", status: http.StatusInternalServerError, wantErr: ErrUnexpectedStatus, wantMsg: "接口异常 (HTTP 500)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"code":1003,"message":"err"}`)
			})
			c := NewClient(baseURL)
			err := c.Ping(context.Background(), "bad-secret")
			if err == nil {
				t.Fatal("expect non-nil error, got nil")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is: got %v want %v", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error message: got %q want contains %q (frontend-facing via 1304)", err.Error(), tc.wantMsg)
			}
			if calls != 1 {
				t.Fatalf("ping must stay single-shot (no retry): got %d calls", calls)
			}
		})
	}
}

// TestPingNetworkError 落地 specs §2.2 超时机制：父 ctx 传入短超时，
// handler 睡 200ms，Ping 被父 ctx 预算中止，直通 context.DeadlineExceeded
// 不误归 ErrNetwork（与 doWithRetry 取消口径一致）。真实网络不可达路径
// 的 ErrNetwork 断言由 TestPing_Unreachable 覆盖（BR2）。
func TestPingNetworkError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Ping(ctx, "test-secret")
	if err == nil {
		t.Fatal("expect non-nil error on timeout, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is: got %v want context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrNetwork) {
		t.Fatalf("caller-side timeout must not classify as ErrNetwork: %v", err)
	}
}

// TestPingEmptyBaseURL 落地 specs §2.3 ErrNotConfigured：baseURL 空串直接返错，
// 不发起网络调用（BR4）。
func TestPingEmptyBaseURL(t *testing.T) {
	c := NewClient("")
	if err := c.Ping(context.Background(), "test-secret"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
}

// TestPing_Unreachable 上游不可达（关闭的端口），断言归为 ErrNetwork 且文案含可读语义。
func TestPing_Unreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	err := c.Ping(context.Background(), "test-secret")
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("errors.Is: got %v want ErrNetwork", err)
	}
	if !strings.Contains(err.Error(), "网络不可达或超时") {
		t.Fatalf("error message: got %q want 网络不可达或超时 semantic", err.Error())
	}
}

// TestPing_CanceledCtx 父 ctx 显式取消直通 context.Canceled，不误归 ErrNetwork
//（与 doWithRetry 口径一致，调用方放弃不计网络故障）。
func TestPing_CanceledCtx(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(baseURL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Ping(ctx, "test-secret")
	if err == nil {
		t.Fatal("expect non-nil error on canceled ctx, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is: got %v want context.Canceled", err)
	}
	if errors.Is(err, ErrNetwork) {
		t.Fatalf("caller cancel must not classify as ErrNetwork: %v", err)
	}
}

// TestPing_NoRetryOnNetworkError 落地 specs §2.4 能力4 范围限定：探活不经 doWithRetry。
// 关停的 httptest 服务制造连接拒绝，单次语义下应远小于 initialBackoff（10s）即返回；
// 若误走重试退避，首轮回退等待就超 10s。
func TestPing_NoRetryOnNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close()

	c := NewClient(srv.URL)
	start := time.Now()
	err := c.Ping(context.Background(), "test-secret")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expect non-nil error on unreachable host, got nil")
	}
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("errors.Is: got %v want ErrNetwork", err)
	}
	if elapsed >= initialBackoff {
		t.Fatalf("ping must be single-shot without backoff, took %v", elapsed)
	}
}

// TestPing_BearerHeader 断言 Authorization 头为 "Bearer {secret}" 格式。
func TestPing_BearerHeader(t *testing.T) {
	var gotAuth string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(baseURL)
	_ = c.Ping(context.Background(), "my-secret-key")
	if gotAuth != "Bearer my-secret-key" {
		t.Fatalf("Authorization: got %q want Bearer my-secret-key", gotAuth)
	}
}

// TestPing_NoEmpNo 断言探活不涉及任何人员字段（「系统无工号」约束）。
func TestPing_NoEmpNo(t *testing.T) {
	var gotQuery = make(map[string][]string)
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(baseURL)
	_ = c.Ping(context.Background(), "test-secret")
	for k := range gotQuery {
		kl := strings.ToLower(k)
		if strings.Contains(kl, "emp") || strings.Contains(kl, "staff") || strings.Contains(kl, "employee") {
			t.Fatalf("query leaked employee-related field: %s", k)
		}
	}
}

// TestNewClient_BaseURLTrailingSlash 断言 baseURL 末尾斜杠容忍语义保留。
func TestNewClient_BaseURLTrailingSlash(t *testing.T) {
	var gotPath string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(baseURL + "/")
	if err := c.Ping(context.Background(), "test-secret"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/api/conversation-log/" {
		t.Fatalf("path: got %q want /api/conversation-log/", gotPath)
	}
}
