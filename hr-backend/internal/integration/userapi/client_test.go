package userapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/integration/userapi"
)

const testSecret = "test-secret"

// startTestServer 启动一个按条件返回响应的 httptest 服务，返回其可达 baseURL。
func startTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// wantRequest 验证路径、鉴权头与查询参数符合新契约，不通过即 t.Fatal。
func wantRequest(t *testing.T, r *http.Request, wantUsername string, wantPage, wantPageSize int) {
	t.Helper()
	if got := r.URL.Path; got != "/api/conversation-log/users" {
		t.Fatalf("path: got %q want /api/conversation-log/users", got)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testSecret {
		t.Fatalf("Authorization: got %q want Bearer %s", got, testSecret)
	}
	q := r.URL.Query()
	if got := q.Get("username"); got != wantUsername {
		t.Fatalf("username: got %q want %q", got, wantUsername)
	}
	if got := q.Get("p"); got != fmt.Sprintf("%d", wantPage) {
		t.Fatalf("p: got %q want %d", got, wantPage)
	}
	if got := q.Get("page_size"); got != fmt.Sprintf("%d", wantPageSize) {
		t.Fatalf("page_size: got %q want %d", got, wantPageSize)
	}
}

// TestListStaffs_Success 模拟 sili-smart-api 返回 200 + success:true + items，
// 断言 Client 正确解析 user_id/username 并映射为 Staff，total 透传。
func TestListStaffs_Success(t *testing.T) {
	const wantUsername = "张"
	const wantPage, wantPageSize = 1, 20
	body := `{"success":true,"message":"","data":{"page":1,"page_size":20,"total":1,"items":[{"user_id":9001,"username":"张三","tokens":[{"token_id":10,"token_name":"prod-key"}]}]}}`
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantRequest(t, r, wantUsername, wantPage, wantPageSize)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})

	c := userapi.NewClient(baseURL)
	items, total, err := c.ListStaffs(context.Background(), testSecret, wantUsername, wantPage, wantPageSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d want 1", total)
	}
	if len(items) != 1 {
		t.Fatalf("items len: got %d want 1", len(items))
	}
	if items[0].StaffID != "9001" {
		t.Fatalf("StaffID: got %q want 9001", items[0].StaffID)
	}
	if items[0].StaffName != "张三" {
		t.Fatalf("StaffName: got %q want 张三", items[0].StaffName)
	}
}

// TestListStaffs_EmptyResult 模拟空列表响应，断言不报错且 items 为空切片、total 为 0。
func TestListStaffs_EmptyResult(t *testing.T) {
	body := `{"success":true,"message":"","data":{"page":1,"page_size":20,"total":0,"items":[]}}`
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})

	c := userapi.NewClient(baseURL)
	items, total, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Fatalf("total: got %d want 0", total)
	}
	if len(items) != 0 {
		t.Fatalf("items len: got %d want 0", len(items))
	}
}

// TestListStaffs_ServerError 模拟上游 500，断言返非 nil error。
func TestListStaffs_ServerError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal server error")
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on 500, got nil")
	}
}

// TestListStaffs_Unauthorized 模拟 401，断言返鉴权失败 error。
func TestListStaffs_Unauthorized(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"success":false,"code":"CONVERSATION_LOG_INVALID_KEY","message":"invalid key"}`)
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("error should mention unauthorized, got %v", err)
	}
}

// TestListStaffs_Forbidden 模拟 403（服务端未配置密钥），断言返鉴权失败 error。
func TestListStaffs_Forbidden(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"success":false,"code":"CONVERSATION_LOG_KEY_NOT_CONFIGURED","message":"key not configured"}`)
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error should mention forbidden, got %v", err)
	}
}

// TestListStaffs_BusinessError 模拟 HTTP 200 + success:false，断言返带 message 的 error。
func TestListStaffs_BusinessError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"message":"database error"}`)
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on success:false, got nil")
	}
	if !strings.Contains(err.Error(), "database error") {
		t.Fatalf("error should carry upstream message, got %v", err)
	}
}

// TestListStaffs_BadJSON 模拟响应非 JSON，断言返非 nil error。
func TestListStaffs_BadJSON(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not-json")
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on bad json, got nil")
	}
}

// TestListStaffs_ItemsMissing 模拟 data 内 items 缺失，断言不 panic 且返空切片。
func TestListStaffs_ItemsMissing(t *testing.T) {
	body := `{"success":true,"message":"","data":{}}`
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})

	c := userapi.NewClient(baseURL)
	items, total, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Fatalf("total: got %d want 0 (missing total defaults to 0)", total)
	}
	if len(items) != 0 {
		t.Fatalf("items len: got %d want 0 (missing items defaults to empty)", len(items))
	}
}

// TestListStaffs_Unreachable 上游不可达（关闭的端口），断言返非 nil error。
func TestListStaffs_Unreachable(t *testing.T) {
	c := userapi.NewClient("http://127.0.0.1:1")
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on unreachable host, got nil")
	}
}

// TestListStaffs_EmptyBaseURL 断言 baseURL 为空时直接返 error，不发起网络调用。
func TestListStaffs_EmptyBaseURL(t *testing.T) {
	c := userapi.NewClient("")
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on empty baseURL, got nil")
	}
}

// TestListStaffs_NoEmpNo 断言响应即使含员工编号字段，映射结果也不暴露任何工号。
// 落地系统无工号约束：Staff 结构体只有 StaffID 与 StaffName。
func TestListStaffs_NoEmpNo(t *testing.T) {
	body := `{"success":true,"message":"","data":{"total":1,"items":[{"user_id":9001,"username":"张三","emp_no":"E001","employee_number":"E001","tokens":[]}]}}`
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	})

	c := userapi.NewClient(baseURL)
	items, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len: got %d want 1", len(items))
	}
	// Staff 结构体字段固定，仅 StaffID 与 StaffName。编码后再解码验证未夹带额外字段。
	raw, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dumped := string(raw)
	if strings.Contains(dumped, "E001") || strings.Contains(strings.ToLower(dumped), "emp_no") || strings.Contains(strings.ToLower(dumped), "employee_number") {
		t.Fatalf("staff payload leaked employee number: %s", dumped)
	}
}

// TestListStaffs_RequestParams 断言 username/p/page_size 被正确写入查询参数。
func TestListStaffs_RequestParams(t *testing.T) {
	var gotQuery url.Values
	var gotAuth string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"total":0,"items":[]}}`)
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "李四", 2, 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery.Get("username") != "李四" {
		t.Fatalf("username: got %q want 李四", gotQuery.Get("username"))
	}
	if gotQuery.Get("p") != "2" {
		t.Fatalf("p: got %q want 2", gotQuery.Get("p"))
	}
	if gotQuery.Get("page_size") != "15" {
		t.Fatalf("page_size: got %q want 15", gotQuery.Get("page_size"))
	}
	if gotAuth != "Bearer "+testSecret {
		t.Fatalf("Authorization: got %q want Bearer %s", gotAuth, testSecret)
	}
}

// TestListStaffs_OversizedBody 断言响应体超过 maxBodySize 时视为异常，避免 OOM。
func TestListStaffs_OversizedBody(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 输出一个远超 10MB 的响应，触发 LimitReader 上限保护。
		w.WriteHeader(http.StatusOK)
		// 先写一个合法 JSON 前缀，再灌大量填充数据让其超过阈值。
		fmt.Fprint(w, `{"success":true,"data":{"items":[`)
		chunk := strings.Repeat("x", 1<<20) // 1MB
		for i := 0; i < 12; i++ {
			fmt.Fprint(w, chunk)
		}
	})

	c := userapi.NewClient(baseURL)
	_, _, err := c.ListStaffs(context.Background(), testSecret, "", 1, 20)
	if err == nil {
		t.Fatal("expect non-nil error on oversized body, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error should mention too large, got %v", err)
	}
}
