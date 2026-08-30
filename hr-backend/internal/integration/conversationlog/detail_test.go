package conversationlog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// detailRespJSON 是兄弟仓库 P1_TECH_001 03_api_interface.md 会话详情响应示例原文
//（3 turns、6 messages，含 user/assistant/tool 角色与 text/tool_use/tool_result kind）。
const detailRespJSON = `{
  "success": true,
  "message": "",
  "data": {
    "session": {
      "session_key": "conv_8f3a2b",
      "first_turn_time": 1781234567,
      "last_turn_time": 1781237890,
      "turn_count": 3,
      "token_name": "my-token",
      "username": "user1",
      "user_id": 2,
      "model_name": "gpt-4o"
    },
    "turns": [
      { "id": 1, "created_at": 1781234567, "request_id": "req_1", "turn_kind": "first" },
      { "id": 2, "created_at": 1781236220, "request_id": "req_2", "turn_kind": "normal" },
      { "id": 3, "created_at": 1781237890, "request_id": "req_3", "turn_kind": "tool_round" }
    ],
    "messages": [
      { "role": "user", "kind": "text", "text": "你好" },
      { "role": "assistant", "kind": "text", "text": "你好，有什么可以帮你？" },
      { "role": "user", "kind": "text", "text": "帮我查天气" },
      { "role": "assistant", "kind": "tool_use", "text": "get_weather args=17" },
      { "role": "tool", "kind": "tool_result", "text": "tool_result result=18" },
      { "role": "assistant", "kind": "text", "text": "北京今天晴天，气温 25 度。" }
    ]
  }
}`

// TestGetSessionDetailFields 落地 BR1：回放兄弟仓库文档详情响应示例，
// 断言 session 复用 toSessionSummary 映射、turns/messages 按上游顺序全量保持。
func TestGetSessionDetailFields(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, detailRespJSON)
	})

	c := newSleepClient(baseURL)
	detail, err := c.GetSessionDetail(context.Background(), "test-secret", "conv_8f3a2b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail == nil {
		t.Fatal("detail must be non-nil")
	}
	if detail.Session.SessionKey != "conv_8f3a2b" {
		t.Errorf("Session.SessionKey: got %q", detail.Session.SessionKey)
	}
	if len(detail.Turns) != 3 {
		t.Fatalf("Turns len: got %d want 3", len(detail.Turns))
	}
	if detail.Turns[0].RequestID != "req_1" {
		t.Errorf("Turns[0].RequestID: got %q want req_1", detail.Turns[0].RequestID)
	}
	if detail.Turns[2].TurnKind != "tool_round" {
		t.Errorf("Turns[2].TurnKind: got %q want tool_round", detail.Turns[2].TurnKind)
	}
	if len(detail.Messages) != 6 {
		t.Fatalf("Messages len: got %d want 6", len(detail.Messages))
	}
	if detail.Messages[3].Kind != "tool_use" {
		t.Errorf("Messages[3].Kind: got %q want tool_use", detail.Messages[3].Kind)
	}
	if detail.Messages[3].Text != "get_weather args=17" {
		t.Errorf("Messages[3].Text: got %q want get_weather args=17", detail.Messages[3].Text)
	}
	// 顺序与元数据完整（specs §5.1 详情正常解包：Messages 按上游顺序保持）。
	if detail.Turns[1].ID != 2 || detail.Turns[1].CreatedAt != 1781236220 {
		t.Errorf("Turns[1]: got id=%d created_at=%d want 2/1781236220", detail.Turns[1].ID, detail.Turns[1].CreatedAt)
	}
	if detail.Messages[0].Role != "user" || detail.Messages[4].Role != "tool" {
		t.Errorf("Messages role order: got [0]=%q [4]=%q want user/tool", detail.Messages[0].Role, detail.Messages[4].Role)
	}
	if detail.Messages[4].Kind != "tool_result" {
		t.Errorf("Messages[4].Kind: got %q want tool_result", detail.Messages[4].Kind)
	}
	// session 段复用列表映射（T4 toSessionSummary 同构）。
	if detail.Session.Username != "user1" || detail.Session.UserID != 2 || detail.Session.TurnCount != 3 {
		t.Errorf("Session meta: got %+v", detail.Session)
	}
}

// TestGetSessionDetailEmptyKey 空 sessionKey 前置拦截返 ErrBadRequest 且零网络请求：
// 空 key 会令 detailURL 退化为列表端点，静默返回零值空壳详情（specs §2.2 sessionKey 必填）。
func TestGetSessionDetailEmptyKey(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, listRespJSON)
	})

	c := newSleepClient(baseURL)
	detail, err := c.GetSessionDetail(context.Background(), "s", "")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("errors.Is: got %v want ErrBadRequest", err)
	}
	if detail != nil {
		t.Fatalf("detail must be nil on error, got %+v", detail)
	}
	if calls != 0 {
		t.Fatalf("must fail fast without network call: got %d calls", calls)
	}
}

// TestGetSessionDetailNotFound 落地 BR3：200 + success=false「conversation not found」，
// ErrUpstreamBusiness 哨兵命中且 IsNotFound 白名单识别为记录不存在。
func TestGetSessionDetailNotFound(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":false,"message":"conversation not found"}`)
	})

	c := newSleepClient(baseURL)
	detail, err := c.GetSessionDetail(context.Background(), "s", "conv_missing")
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
	if !ue.IsNotFound() {
		t.Fatalf("IsNotFound: Msg=%q must hit not-found whitelist", ue.Msg)
	}
	if detail != nil {
		t.Fatalf("detail must be nil on error, got %+v", detail)
	}
}

// TestGetSessionDetailNotFoundZh 中文文案「会话不存在」同走白名单识别（specs §2.3）。
func TestGetSessionDetailNotFoundZh(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"message":"会话不存在"}`)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_missing")
	var ue *UpstreamError
	if !errors.As(err, &ue) || !ue.IsNotFound() {
		t.Fatalf("zh message must hit not-found whitelist, got %v", err)
	}
}

// TestGetSessionDetailBusinessOtherMessage 上游内部错误等其他文案未命中白名单，
// 按会话级失败处理（specs §2.3 记录不存在识别兜底方向）。
func TestGetSessionDetailBusinessOtherMessage(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"message":"upstream internal error"}`)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("errors.As *UpstreamError: failed, err=%v", err)
	}
	if ue.IsNotFound() {
		t.Fatalf("IsNotFound must be false for non-whitelist message: %q", ue.Msg)
	}
}

// TestGetSessionDetailPathEscape 落地 URL 构造契约：sessionKey 含特殊字符时经
// url.PathEscape 转义后拼路径，请求可达且服务端可还原原始值（不破坏请求）。
func TestGetSessionDetailPathEscape(t *testing.T) {
	const rawKey = `a b/c`
	var gotEscaped, gotDecoded, gotPath string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		gotDecoded = r.URL.Path
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"data":{"session":{"session_key":"x"},"turns":[],"messages":[]}}`)
	})

	c := newSleepClient(baseURL)
	if _, err := c.GetSessionDetail(context.Background(), "s", rawKey); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 转义段拼入路径不破坏请求：服务端解码后末段即原始值。
	if !strings.HasSuffix(gotPath, rawKey) {
		t.Fatalf("server decoded path: got %q want suffix %q", gotPath, rawKey)
	}
	_ = gotDecoded
	// 转义后的请求路径中原始 / 与空格以 %2F/%20 形态出现（转义已生效）。
	if !strings.Contains(gotEscaped, "a%20b%2Fc") {
		t.Fatalf("escaped path: got %q, want contains a%%20b%%2Fc", gotEscaped)
	}
}

// TestGetSessionDetailNormalKeyPath 常规 session_key（conv_8f3a2b）拼路径形态：
// /api/conversation-log/conv_8f3a2b（specs §2.4 能力2）。
func TestGetSessionDetailNormalKeyPath(t *testing.T) {
	var gotPath string
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"success":true,"data":{"session":{"session_key":"conv_8f3a2b"},"turns":[],"messages":[]}}`)
	})

	c := newSleepClient(baseURL)
	if _, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/api/conversation-log/conv_8f3a2b" {
		t.Fatalf("path: got %q want /api/conversation-log/conv_8f3a2b", gotPath)
	}
}

// TestGetSessionDetailMissingNormalize 落地 turns/messages 缺失归一化：
// 空切片非 nil、长度 0、不报错（specs §5.1 响应结构缺失归一化同口径）。
// data null 因连带 session 缺失走 ErrDecode 契约保护，另由
// TestGetSessionDetailMissingSession 覆盖。
func TestGetSessionDetailMissingNormalize(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "turns messages missing", body: `{"success":true,"message":"","data":{"session":{"session_key":"c1"}}}`},
		{name: "turns messages null", body: `{"success":true,"data":{"session":{"session_key":"c1"},"turns":null,"messages":null}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			c := newSleepClient(baseURL)
			detail, err := c.GetSessionDetail(context.Background(), "s", "c1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if detail.Turns == nil {
				t.Fatal("Turns must be non-nil empty slice")
			}
			if detail.Messages == nil {
				t.Fatal("Messages must be non-nil empty slice")
			}
			if len(detail.Turns) != 0 || len(detail.Messages) != 0 {
				t.Fatalf("len: turns=%d messages=%d want 0/0", len(detail.Turns), len(detail.Messages))
			}
		})
	}
}

// TestGetSessionDetailMissingSession 落地详情契约保护：data.session 缺失或 null
// 不得静默返回零值空壳成功（SessionKey 空串、时间全 0），归 ErrDecode。
func TestGetSessionDetailMissingSession(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "session missing", body: `{"success":true,"message":"","data":{"turns":[],"messages":[]}}`},
		{name: "session null", body: `{"success":true,"data":{"session":null,"turns":[],"messages":[]}}`},
		{name: "data null", body: `{"success":true,"message":"","data":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			c := newSleepClient(baseURL)
			detail, err := c.GetSessionDetail(context.Background(), "s", "conv_x")
			if !errors.Is(err, ErrDecode) {
				t.Fatalf("errors.Is: got %v want ErrDecode", err)
			}
			if detail != nil {
				t.Fatalf("detail must be nil on error, got %+v", detail)
			}
		})
	}
}

// TestGetSessionDetailUnauthorized 落地 401 → ErrUnauthorized，确定性不重试（计数 1）。
func TestGetSessionDetailUnauthorized(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("errors.Is: got %v want ErrUnauthorized", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1 (deterministic, no retry)", calls)
	}
}

// TestGetSessionDetailForbidden 403 同归 ErrUnauthorized（specs §2.3 分流定位 6.4 问题1）。
func TestGetSessionDetailForbidden(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("errors.Is: got %v want ErrUnauthorized", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
}

// TestGetSessionDetailBadRequest 落地 400 → ErrBadRequest，确定性不重试。
func TestGetSessionDetailBadRequest(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("errors.Is: got %v want ErrBadRequest", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
}

// TestGetSessionDetailHTTP404NotFound HTTP 404 单独归 ErrNotFound：记录不存在的
// REST 表达，确定性不重试，抽取侧按业务空处理（评审裁定，与信封白名单双通道）。
func TestGetSessionDetailHTTP404NotFound(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("errors.Is: got %v want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error message should carry status code: %v", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
}

// TestGetSessionDetailUnexpectedStatus 落地兜底桶：404 外的其余非 2xx（如 502）→
// ErrUnexpectedStatus 带状态码，确定性不重试。
func TestGetSessionDetailUnexpectedStatus(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTeapot)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("errors.Is: got %v want ErrUnexpectedStatus", err)
	}
	if !strings.Contains(err.Error(), "418") {
		t.Fatalf("error message should carry status code: %v", err)
	}
	if calls != 1 {
		t.Fatalf("request calls: got %d want 1", calls)
	}
}

// TestGetSessionDetailNotConfigured 落地 BR7：baseURL 空串前置返 ErrNotConfigured。
func TestGetSessionDetailNotConfigured(t *testing.T) {
	c := NewClient("")
	detail, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
	if detail != nil {
		t.Fatalf("detail must be nil on error, got %+v", detail)
	}
}

// TestGetSessionDetailDecodeError 200 但非法 JSON 归 ErrDecode。
func TestGetSessionDetailDecodeError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not-json`)
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestGetSessionDetailBodyTooLarge 响应体超 10MB 上限归 ErrDecode（LimitReader 保护）。
func TestGetSessionDetailBodyTooLarge(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxBodySize+1))
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestGetSessionDetailRequestBuildError 落地错误分类兜底路径：baseURL 含控制字符
// 使请求构造失败，属配置类问题直通返 ErrNotConfigured（确定性失败不重试，
// 不伪装网络故障）。
func TestGetSessionDetailRequestBuildError(t *testing.T) {
	c := newSleepClient("http://127.0.0.1:1/\x7f")
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errors.Is: got %v want ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), "构造请求失败") {
		t.Fatalf("error message: got %q want 构造请求失败 semantic", err.Error())
	}
}

// TestGetSessionDetailBodyReadError 落地 BR6 补充路径：2xx 后响应体读取中断
//（声明 Content-Length 超过实际写入）归 ErrDecode。
func TestGetSessionDetailBodyReadError(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, "short")
	})

	c := newSleepClient(baseURL)
	_, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("errors.Is: got %v want ErrDecode", err)
	}
}

// TestGetSessionDetailBodyReadTimeout 同 ListSessions 口径：2xx 后 body 滴流，
// 父 ctx 200ms 到期级联取消子 ctx，读体错误直通 ctx 哨兵（调用方放弃不计
// 网络故障，specs §2.3）。
func TestGetSessionDetailBodyReadTimeout(t *testing.T) {
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})

	c := newSleepClient(baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.GetSessionDetail(ctx, "s", "conv_8f3a2b")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is: got %v want context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrNetwork) {
		t.Fatalf("caller-side timeout must not classify as ErrNetwork: %v", err)
	}
}

// TestGetSessionDetailNetworkRetryRecovered 落地全程经 doWithRetry：传输错误重试后恢复，
// 第三次成功，请求计数 3。
func TestGetSessionDetailNetworkRetryRecovered(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		fmt.Fprint(w, detailRespJSON)
	})

	c := newSleepClient(baseURL)
	detail, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("request calls: got %d want 3", calls)
	}
	if detail.Session.SessionKey != "conv_8f3a2b" || len(detail.Turns) != 3 || len(detail.Messages) != 6 {
		t.Fatalf("recovered result mismatch: %+v", detail.Session)
	}
}

// TestGetSessionDetail5xxRetryRecovered 5xx 重试后恢复（doWithRetry 消化，第三次成功）。
func TestGetSessionDetail5xxRetryRecovered(t *testing.T) {
	var calls int
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, detailRespJSON)
	})

	c := newSleepClient(baseURL)
	detail, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("request calls: got %d want 3", calls)
	}
	if len(detail.Messages) != 6 {
		t.Fatalf("Messages len: got %d want 6", len(detail.Messages))
	}
}

// TestConcurrentPullStability 落地 specs §5.3 并发拉取稳定性：50 goroutine 并发
// 混合调用 ListSessions 与 GetSessionDetail，全部成功返回，无 panic。
// resp.Body 全关闭经 httptest 服务端连接正常复用验证（服务端正常处理完全部请求即证）。
// 注：-race 需 CGO_ENABLED=1，本环境不可用，留 T6 收尾统一处理。
func TestConcurrentPullStability(t *testing.T) {
	var listCalls, detailCalls, totalCalls int64
	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&totalCalls, 1)
		if strings.HasSuffix(r.URL.Path, "/api/conversation-log/") {
			atomic.AddInt64(&listCalls, 1)
			fmt.Fprint(w, listRespJSON)
			return
		}
		atomic.AddInt64(&detailCalls, 1)
		fmt.Fprint(w, detailRespJSON)
	})

	c := newSleepClient(baseURL)
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			items, _, err := c.ListSessions(context.Background(), "s", ListSessionsRequest{Page: 1, PageSize: 20})
			if err != nil {
				errs <- fmt.Errorf("list: %w", err)
				return
			}
			if len(items) != 1 || items[0].SessionKey != "conv_8f3a2b" {
				errs <- fmt.Errorf("list result mismatch: len=%d", len(items))
			}
		}()
		go func() {
			defer wg.Done()
			detail, err := c.GetSessionDetail(context.Background(), "s", "conv_8f3a2b")
			if err != nil {
				errs <- fmt.Errorf("detail: %w", err)
				return
			}
			if detail.Session.SessionKey != "conv_8f3a2b" || len(detail.Messages) != 6 {
				errs <- fmt.Errorf("detail result mismatch: %+v", detail.Session)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
	if atomic.LoadInt64(&listCalls) != n {
		t.Fatalf("list calls: got %d want %d", listCalls, n)
	}
	if atomic.LoadInt64(&detailCalls) != n {
		t.Fatalf("detail calls: got %d want %d", detailCalls, n)
	}
	if atomic.LoadInt64(&totalCalls) != 2*n {
		t.Fatalf("total calls: got %d want %d", totalCalls, 2*n)
	}
}

// TestDetailDecodeLatency 落地 specs §5.3 单会话详情拉取延迟：httptest 本地回放
// 1MB 量级 messages，解码耗时 < 500ms（排除网络，防解包路径异常放大回归；
// 上限按 -race 2-20x 开销与慢机抖动留裕量，实测值由 t.Logf 记录）。
func TestDetailDecodeLatency(t *testing.T) {
	// 构造 1MB 量级 messages：约 210 条消息，每条 text 约 5KB。
	const msgCount = 210
	const textSize = 5 * 1024
	big := strings.Repeat("好", textSize/3) // UTF-8 每汉字 3 字节，约 5KB
	var sb strings.Builder
	sb.WriteString(`{"success":true,"data":{"session":{"session_key":"conv_big"},"turns":[`)
	for i := 1; i <= 3; i++ {
		if i > 1 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%d,"created_at":1781234567,"request_id":"req_%d","turn_kind":"normal"}`, i, i)
	}
	sb.WriteString(`],"messages":[`)
	for i := 0; i < msgCount; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"role":"user","kind":"text","text":"%s"}`, big)
	}
	sb.WriteString(`]}}`)
	bodyLen := sb.Len()
	if bodyLen < 1<<20 {
		t.Fatalf("payload too small: %d bytes want >= 1MB", bodyLen)
	}

	baseURL := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sb.String())
	})

	c := newSleepClient(baseURL)
	// 预热一次排除 JIT/连接建立干扰，再计时。
	if _, err := c.GetSessionDetail(context.Background(), "s", "conv_big"); err != nil {
		t.Fatalf("warmup error: %v", err)
	}
	start := time.Now()
	detail, err := c.GetSessionDetail(context.Background(), "s", "conv_big")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(detail.Messages) != msgCount {
		t.Fatalf("Messages len: got %d want %d", len(detail.Messages), msgCount)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("decode latency: got %v want < 500ms (payload %d bytes)", elapsed, bodyLen)
	}
	t.Logf("decode latency: %v for %d bytes / %d messages", elapsed, bodyLen, msgCount)
}
