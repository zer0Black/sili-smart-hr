package conversationlog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// listRespJSON 是兄弟仓库 P1_TECH_001 03_api_interface.md 会话列表响应示例原文。
const listRespJSON = `{
  "success": true,
  "message": "",
  "data": {
    "page": 1,
    "page_size": 20,
    "total": 1,
    "items": [
      {
        "session_key": "conv_8f3a2b",
        "first_turn_time": 1781234567,
        "last_turn_time": 1781237890,
        "turn_count": 3,
        "token_name": "my-token",
        "username": "user1",
        "user_id": 2,
        "model_name": "gpt-4o"
      }
    ]
  }
}`

// newSleepClient 构造注入零等待 sleepFn 的 Client，重试退避不拖慢测试。
func newSleepClient(baseURL string) *Client {
	c := NewClient(baseURL)
	c.sleepFn = func(ctx context.Context, d time.Duration) error { return nil }
	return c
}

// TestListSessionsFields 落地 BR4：回放兄弟仓库文档响应示例（1 会话 conv_8f3a2b），
// 断言 SessionSummary 字段一一映射且 total 为上游口径。
func TestListSessionsFields(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, listRespJSON)
	})

	c := newSleepClient(baseURL)
	items, total, err := c.ListSessions(context.Background(), "test-secret", ListSessionsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Fatalf("total: got %d want 1", total)
	}
	if len(items) != 1 {
		t.Fatalf("items len: got %d want 1", len(items))
	}
	got := items[0]
	if got.SessionKey != "conv_8f3a2b" {
		t.Errorf("SessionKey: got %q", got.SessionKey)
	}
	if got.UserID != 2 {
		t.Errorf("UserID: got %d want 2", got.UserID)
	}
	if got.TurnCount != 3 {
		t.Errorf("TurnCount: got %d want 3", got.TurnCount)
	}
	if got.LastTurnTime != 1781237890 {
		t.Errorf("LastTurnTime: got %d want 1781237890", got.LastTurnTime)
	}
	if got.FirstTurnTime != 1781234567 {
		t.Errorf("FirstTurnTime: got %d want 1781234567", got.FirstTurnTime)
	}
	if got.TokenName != "my-token" {
		t.Errorf("TokenName: got %q", got.TokenName)
	}
	if got.Username != "user1" {
		t.Errorf("Username: got %q", got.Username)
	}
	if got.ModelName != "gpt-4o" {
		t.Errorf("ModelName: got %q", got.ModelName)
	}
}

// TestListSessionsQueryContract 落地 BR1/BR5：传 Page=1、PageSize=500（钳到 100）、
// StartTime、Username 时 query 契约正确；不传 Username 时无 username 键；
// 零值时间窗不拼接对应参数。
func TestListSessionsQueryContract(t *testing.T) {
	var withUser, withoutUser url.Values
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["username"]; ok {
			withUser = r.URL.Query()
		} else {
			withoutUser = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"total":0,"items":[]}}`)
	})

	c := newSleepClient(baseURL)
	if _, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{
		Username:  "user1",
		StartTime: 1781234567,
		EndTime:   1781237890,
		Page:      1,
		PageSize:  500,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if withUser.Get("p") != "1" {
		t.Errorf("p: got %q want 1", withUser.Get("p"))
	}
	if withUser.Get("page_size") != "100" {
		t.Errorf("page_size: got %q want 100 (500 clamped)", withUser.Get("page_size"))
	}
	if withUser.Get("start_timestamp") != "1781234567" {
		t.Errorf("start_timestamp: got %q", withUser.Get("start_timestamp"))
	}
	if withUser.Get("end_timestamp") != "1781237890" {
		t.Errorf("end_timestamp: got %q", withUser.Get("end_timestamp"))
	}
	if withUser.Get("username") != "user1" {
		t.Errorf("username: got %q want user1", withUser.Get("username"))
	}
	if _, ok := withUser["page"]; ok {
		t.Error("query must use p, not page")
	}

	// 不传 Username、不传时间窗：username/时间键缺位，Page=0 视为 1、PageSize=0 钳到 100。
	if _, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := withoutUser["username"]; ok {
		t.Error("empty Username must not be sent")
	}
	if _, ok := withoutUser["start_timestamp"]; ok {
		t.Error("zero StartTime must not be sent")
	}
	if _, ok := withoutUser["end_timestamp"]; ok {
		t.Error("zero EndTime must not be sent")
	}
	if withoutUser.Get("p") != "1" {
		t.Errorf("p default: got %q want 1", withoutUser.Get("p"))
	}
	if withoutUser.Get("page_size") != "100" {
		t.Errorf("page_size default: got %q want 100", withoutUser.Get("page_size"))
	}
}

// TestListSessionsPageSizeClamp 落地 BR1：传 PageSize=500 被钳到上游上限 100
//（与 QueryContract 中的钳制断言互为独立复核）。
func TestListSessionsPageSizeClamp(t *testing.T) {
	var gotPageSize string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPageSize = r.URL.Query().Get("page_size")
		fmt.Fprint(w, `{"success":true,"data":{"total":0,"items":[]}}`)
	})

	c := newSleepClient(baseURL)
	if _, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 500}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPageSize != "100" {
		t.Fatalf("page_size: got %q want 100 (500 clamped to upstream cap)", gotPageSize)
	}
}

// TestListSessionsUserIDFilter 落地 BR2：UserID 非 0 时内存过滤只留目标用户，
// total 保持上游口径（len(items) 可能小于 total 属契约行为）。
func TestListSessionsUserIDFilter(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"total":3,"items":[
			{"session_key":"c1","user_id":2,"username":"user1"},
			{"session_key":"c2","user_id":3,"username":"user2"},
			{"session_key":"c3","user_id":2,"username":"user1"}
		]}}`)
	})

	c := newSleepClient(baseURL)
	items, total, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{UserID: 3, Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len: got %d want 1 (filtered in memory)", len(items))
	}
	if items[0].UserID != 3 {
		t.Fatalf("UserID: got %d want 3", items[0].UserID)
	}
	if items[0].SessionKey != "c2" {
		t.Fatalf("SessionKey: got %q want c2", items[0].SessionKey)
	}
	if total != 3 {
		t.Fatalf("total must stay upstream: got %d want 3", total)
	}
}

// TestListSessionsEmptyNotSliceNil 落地 BR3/BR7：data 为 {}（items 缺失）与
// data:null、items:[] 均不报错，返回空切片非 nil，不属 ErrDecode。
func TestListSessionsEmptyNotSliceNil(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "items missing in data", body: `{"success":true,"message":"","data":{}}`},
		{name: "data null", body: `{"success":true,"message":"","data":null}`},
		{name: "items empty", body: `{"success":true,"message":"","data":{"total":0,"items":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			})
			c := newSleepClient(baseURL)
			items, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if items == nil {
				t.Fatal("items must be non-nil empty slice")
			}
			if len(items) != 0 {
				t.Fatalf("items len: got %d want 0", len(items))
			}
		})
	}
}

// TestListSessionsBusinessError 落地 ErrUpstreamBusiness：200 + success=false，
// errors.Is 命中哨兵且 errors.As 后 Msg 携带上游 message。
func TestListSessionsBusinessError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"message":"会话不存在"}`)
	})

	c := newSleepClient(baseURL)
	items, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if err == nil {
		t.Fatal("expect non-nil error, got nil")
	}
	if !errors.Is(err, ErrUpstreamBusiness) {
		t.Fatalf("errors.Is: got %v want ErrUpstreamBusiness", err)
	}
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatal("errors.As *UpstreamError: failed")
	}
	if ue.Msg != "会话不存在" {
		t.Fatalf("ue.Msg: got %q want 会话不存在", ue.Msg)
	}
	if items != nil {
		t.Fatalf("items must be nil on error, got %v", items)
	}
}

// TestListSessionsEnvelopeMissingSuccess 落地信封契约保护：success 键缺失、
// body 为字面 null、或上游误配返本系统风格 {code,...} 信封时，不得因零值
// false 误归 ErrUpstreamBusiness（UpstreamError.Msg 空串），统一归 ErrDecode。
func TestListSessionsEnvelopeMissingSuccess(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "success key missing", body: `{"message":"ok","data":{"total":0,"items":[]}}`},
		{name: "success null", body: `{"success":null,"message":"ok","data":{"total":0,"items":[]}}`},
		{name: "body null", body: `null`},
		{name: "wrong envelope style", body: `{"code":0,"message":"ok","data":{"list":[],"total":0}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			c := newSleepClient(baseURL)
			_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
			if !errors.Is(err, ErrDecode) {
				t.Fatalf("errors.Is: got %v want ErrDecode", err)
			}
			if errors.Is(err, ErrUpstreamBusiness) {
				t.Fatalf("missing success must not classify as ErrUpstreamBusiness: %v", err)
			}
		})
	}
}

// TestListSessionsUnauthorized 落地 401/403 → ErrUnauthorized，确定性不重试（计数 1）。
func TestListSessionsUnauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls int
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
			})
			c := newSleepClient(baseURL)
			_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("errors.Is: got %v want ErrUnauthorized", err)
			}
			if calls != 1 {
				t.Fatalf("request calls: got %d want 1 (no retry)", calls)
			}
		})
	}
}

// TestListSessionsBadRequest 落地 BR8：400 → ErrBadRequest，确定性不重试（计数 1）。
func TestListSessionsBadRequest(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"success":false,"message":"参数非法"}`)
	})

	c := newSleepClient(baseURL)
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("errors.Is: got %v want ErrBadRequest", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1 (deterministic, no retry)", calls)
	}
}

// TestListSessionsUnexpectedStatus 落地兜底桶：404 外的其余非 2xx（如 502）→
// ErrUnexpectedStatus，带 HTTP 状态码，确定性不重试。
func TestListSessionsUnexpectedStatus(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTeapot)
	})

	c := newSleepClient(baseURL)
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("errors.Is: got %v want ErrUnexpectedStatus", err)
	}
	if !strings.Contains(err.Error(), "418") {
		t.Fatalf("error message should carry status code: %v", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1 (no retry)", calls)
	}
}

// TestListSessionsNotConfigured 落地 BR9：baseURL 空串前置返 ErrNotConfigured，
// 不发起网络调用。
func TestListSessionsNotConfigured(t *testing.T) {
	c := NewClient("")
	items, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
	if items != nil {
		t.Fatalf("items must be nil on error, got %v", items)
	}
}

// TestListSessionsBodyTooLarge 落地 BR6：响应体超 10MB 上限归 ErrDecode。
func TestListSessionsBodyTooLarge(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(make([]byte, maxBodySize+1))
	})

	c := newSleepClient(baseURL)
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestListSessionsDecodeError 落地 BR6：200 但响应体非法 JSON 归 ErrDecode。
func TestListSessionsDecodeError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json`)
	})

	c := newSleepClient(baseURL)
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestListSessionsRequestBuildError 落地错误分类兜底路径：baseURL 含控制字符
// 使请求构造失败，属配置类问题直通返 ErrNotConfigured（确定性失败不重试，
// 不伪装网络故障误导调用方）。
func TestListSessionsRequestBuildError(t *testing.T) {
	c := newSleepClient("http://127.0.0.1:1/\x7f")
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), "构造请求失败") {
		t.Fatalf("error message: got %q want 构造请求失败 semantic", err.Error())
	}
}

// TestListSessionsBodyReadError 落地 BR6 补充路径：2xx 后响应体读取中断
//（声明 Content-Length 超过实际写入，服务端截断连接）归 ErrDecode。
func TestListSessionsBodyReadError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short")
	})

	c := newSleepClient(baseURL)
	_, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestListSessionsBodyReadTimeout 落地读体中断分类：2xx 响应头到手后 body 滴流，
// 父 ctx 200ms 到期级联取消子 ctx，读体错误直通 ctx 哨兵（与 Ping/doWithRetry
// 取消口径一致，调用方放弃不计网络故障）；非取消类读体中断（子 ctx 超时、
// 连接重置）才归 ErrNetwork（specs §2.3：ErrDecode 仅限 JSON 解析失败与超 10MB）。
func TestListSessionsBodyReadTimeout(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // 持有连接模拟慢速滴流 body，待 ctx 取消
	})

	c := newSleepClient(baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := c.ListSessions(ctx, "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is: got %v want context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrNetwork) {
		t.Fatalf("caller-side timeout must not classify as ErrNetwork: %v", err)
	}
}

// TestListSessionsNetworkRetryRecovered 落地全程经 doWithRetry：网络错误重试后恢复，
// 第三次成功，请求计数 3。
func TestListSessionsNetworkRetryRecovered(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			// 制造传输层网络错误：直接劫持底层连接断开。
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, listRespJSON)
	})

	c := newSleepClient(baseURL)
	items, total, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("request calls: got %d want 3", calls)
	}
	if total != 1 || len(items) != 1 || items[0].SessionKey != "conv_8f3a2b" {
		t.Fatalf("recovered result mismatch: total=%d len=%d", total, len(items))
	}
}
