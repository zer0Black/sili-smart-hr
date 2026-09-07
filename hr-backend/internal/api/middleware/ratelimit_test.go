package middleware_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"sili-smart-hr/backend/internal/api/middleware"
)

// newTestRedis 起一个内存 Redis 并返回客户端与 miniredis 句柄，测试结束自动清理。
func newTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

// doRequest 向 engine 发一次 GET /，返回响应状态码。
func doRequest(r http.Handler, remoteAddr string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestRateLimit_AllowsThenBlocks 覆盖核心断言：limit=2 时前两次放行（计数 handler 命中 2 次），
// 第三次返回 HTTP 429 且 handler 不再被命中。
func TestRateLimit_AllowsThenBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, _ := newTestRedis(t)

	var hits int32
	r := gin.New()
	r.Use(middleware.RateLimit(rdb, "t:", middleware.ClientIPKey, 2, time.Minute))
	r.GET("/", func(c *gin.Context) {
		atomic.AddInt32(&hits, 1)
		c.Status(http.StatusOK)
	})

	// gin 默认 RemoteAddr（httptest 给 192.0.2.1）作为 ClientIP，同 IP 累加计数。
	for i := 0; i < 2; i++ {
		if code := doRequest(r, ""); code != http.StatusOK {
			t.Fatalf("req #%d: status = %d, want 200", i+1, code)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("handler hits = %d, want 2", got)
	}

	if code := doRequest(r, ""); code != http.StatusTooManyRequests {
		t.Fatalf("req #3: status = %d, want 429", code)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("handler hits after block = %d, want 2", got)
	}
}

// TestRateLimit_DifferentIPsIndependent 验证不同 IP 各自独立计数，互不影响。
func TestRateLimit_DifferentIPsIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, _ := newTestRedis(t)

	r := gin.New()
	r.Use(middleware.RateLimit(rdb, "t:", middleware.ClientIPKey, 1, time.Minute))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	if code := doRequest(r, "10.0.0.1:1111"); code != http.StatusOK {
		t.Fatalf("ip A first: status = %d, want 200", code)
	}
	if code := doRequest(r, "10.0.0.2:2222"); code != http.StatusOK {
		t.Fatalf("ip B first: status = %d, want 200", code)
	}
	// 两个 IP 都已用尽各自的 1 次配额，再请求都应被限流。
	if code := doRequest(r, "10.0.0.1:1111"); code != http.StatusTooManyRequests {
		t.Fatalf("ip A second: status = %d, want 429", code)
	}
	if code := doRequest(r, "10.0.0.2:2222"); code != http.StatusTooManyRequests {
		t.Fatalf("ip B second: status = %d, want 429", code)
	}
}

// TestRateLimit_WindowExpiry 验证窗口过期后计数重置：miniredis 快进越过窗口后请求重新放行。
func TestRateLimit_WindowExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, mr := newTestRedis(t)

	r := gin.New()
	r.Use(middleware.RateLimit(rdb, "t:", middleware.ClientIPKey, 1, time.Minute))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	if code := doRequest(r, ""); code != http.StatusOK {
		t.Fatalf("first: status = %d, want 200", code)
	}
	if code := doRequest(r, ""); code != http.StatusTooManyRequests {
		t.Fatalf("second within window: status = %d, want 429", code)
	}

	// 快进 61 秒越过窗口，TTL 到期，key 被清，计数应从头开始。
	mr.FastForward(61 * time.Second)

	if code := doRequest(r, ""); code != http.StatusOK {
		t.Fatalf("after window expiry: status = %d, want 200", code)
	}
}

// TestRateLimit_RedisErrorFailsOpen 验证 Redis 异常分支采用 fail-open 策略：
// 限流是反滥用辅助，Redis 抖动不应阻断业务请求。
func TestRateLimit_RedisErrorFailsOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rdb, mr := newTestRedis(t)
	mr.SetError("redis down")

	r := gin.New()
	r.Use(middleware.RateLimit(rdb, "t:", middleware.ClientIPKey, 1, time.Minute))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	if code := doRequest(r, ""); code != http.StatusOK {
		t.Fatalf("redis error: status = %d, want 200 (fail-open)", code)
	}
}

// TestAccountIDKey_WithID 验证 JWT 中间件注入的 account_id（int64 雪花值）被格式化为字符串桶标识。
func TestAccountIDKey_WithID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("account_id", int64(1845700000000000012))
	if got := middleware.AccountIDKey(c); got != "1845700000000000012" {
		t.Fatalf("AccountIDKey = %q, want \"1845700000000000012\"", got)
	}
}

// TestAccountIDKey_NoID 验证未鉴权（无 account_id）返回空串 fail-open。
func TestAccountIDKey_NoID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := middleware.AccountIDKey(c); got != "" {
		t.Fatalf("AccountIDKey = %q, want \"\"", got)
	}
}

// TestJSONFieldKey 覆盖 body peek + rewind + 归一化的核心断言：
// 取值正确、大小写与空白归一化、rewind 后 body 可被后续 handler 重新绑定、
// body 缺失/格式错/字段缺失时返回空串（fail-open）。
func TestJSONFieldKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 正常：取字段并 trim+lower 归一化。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"username":" Admin ","x":1}`))
	if got := middleware.JSONFieldKey("username")(c); got != "admin" {
		t.Fatalf("normalized key = %q, want \"admin\"", got)
	}
	// rewind：peek 后 body 仍可被 json.Decoder 完整读出（原始未归一化内容）。
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		t.Fatalf("rewind decode: %v", err)
	}
	if body.Username != " Admin " {
		t.Fatalf("rewind body = %q, want raw \" Admin \"", body.Username)
	}

	// body 格式错 → ""。
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{bad`))
	if got := middleware.JSONFieldKey("username")(c2); got != "" {
		t.Fatalf("malformed body key = %q, want \"\"", got)
	}

	// 字段缺失 → ""。
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"x"}`))
	if got := middleware.JSONFieldKey("username")(c3); got != "" {
		t.Fatalf("missing field key = %q, want \"\"", got)
	}

	// 无 body（GET 等）→ ""。
	c4, _ := gin.CreateTestContext(httptest.NewRecorder())
	c4.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := middleware.JSONFieldKey("username")(c4); got != "" {
		t.Fatalf("nil body key = %q, want \"\"", got)
	}

	// ReadAll 中途出错（如客户端连接中断）→ ""，且下游 body 应为空（EOF）而非截断数据。
	c5, _ := gin.CreateTestContext(httptest.NewRecorder())
	c5.Request = httptest.NewRequest(http.MethodPost, "/", &errReader{data: []byte(`{"username":"admin"}`)})
	if got := middleware.JSONFieldKey("username")(c5); got != "" {
		t.Fatalf("read error key = %q, want \"\"", got)
	}
	if left, _ := io.ReadAll(c5.Request.Body); len(left) != 0 {
		t.Fatalf("downstream body = %q, want empty (EOF)", left)
	}
}

// errReader 模拟读取中途失败：先返回 data 前缀字节，Read 到末尾时报 err。
type errReader struct {
	data []byte
	pos  int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, errors.New("simulated read error")
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
