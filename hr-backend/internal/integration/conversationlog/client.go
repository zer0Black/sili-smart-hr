package conversationlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 包级常量（specs §2.2、§2.4）。
const (
	maxRetries     = 3                // 网络层失败重试次数
	initialBackoff = 10 * time.Second // 指数退避初始值
	pingTimeout    = 5 * time.Second  // 单次探活超时
	listTimeout    = 30 * time.Second // 单次列表请求超时
	detailTimeout  = 30 * time.Second // 单次详情请求超时
	maxBodySize    = 10 << 20         // 响应体上限 10MB
)

// Client 是 sili-smart-api 会话日志 HTTP 客户端。
// 不设全局 Timeout，超时按请求分档经 ctx 派生；sleepFn 为重试等待注入点。
type Client struct {
	baseURL    string
	httpClient *http.Client
	sleepFn    func(ctx context.Context, d time.Duration) error
}

// NewClient 构造客户端。baseURL 为空仍返回 Client，调用时返 ErrNotConfigured。
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			// 超时经 ctx 分档注入，不设全局 Timeout。
			Timeout: 0,
		},
	}
}

// Ping 探活会话日志通道，单次执行不经重试（specs §2.4）。
// 只看状态码：2xx 成功，分类经 classifyStatus(with400=false)。
// 只校验集成密钥连通性，LLM 连通性在评估引擎调用时校验。
func (c *Client) Ping(ctx context.Context, secret string) error {
	if c.baseURL == "" {
		return wrapErr(ErrNotConfigured, errors.New("检查 config.Integration.SmartAPIBaseURL"))
	}

	reqURL := c.baseURL + "/api/conversation-log/?p=1&page_size=1"
	reqCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	req, err := newGetRequest(reqCtx, reqURL, secret)
	if err != nil {
		return wrapErr(ErrNotConfigured, fmt.Errorf("构造请求失败: %w", err))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 父 ctx 取消直通，其余统一归网络错误。
		if perr := ctx.Err(); perr != nil {
			return wrapErr(perr, fmt.Errorf("探活被调用方中止: %w", err))
		}
		return wrapErr(ErrNetwork, fmt.Errorf("网络不可达或超时: %w", err))
	}
	defer resp.Body.Close()

	// drain 后连接可复用；探活口径 400 落兜底桶。
	sentinel, msg := classifyStatus(resp.StatusCode, false)
	_ = drainAndClose(resp.Body)
	if sentinel == nil {
		return nil
	}
	return wrapErr(sentinel, fmt.Errorf("%s (HTTP %d)", msg, resp.StatusCode))
}

// ListSessions 拉取一页会话列表（specs §2.4），翻页循环归调用方。
// UserID 非 0 时内存过滤，total 保持上游口径（勿用 len 判末页）。
// 空结果归一为空切片非 nil；错误分类见 specs §2.3。
func (c *Client) ListSessions(ctx context.Context, secret string, req ListSessionsRequest) (items []SessionSummary, total int64, err error) {
	if c.baseURL == "" {
		return nil, 0, wrapErr(ErrNotConfigured, errors.New("检查 config.Integration.SmartAPIBaseURL"))
	}

	listURL := c.baseURL + "/api/conversation-log/?" + buildListQuery(req).Encode()
	start := time.Now()

	resp, err := c.doGet(ctx, listTimeout, listURL, secret)
	if err != nil {
		// 重试耗尽已在 retry 层输出 ERROR failed；构造失败返 ErrNotConfigured。
		return nil, 0, err
	}
	defer resp.Body.Close()

	if ferr := failStatus(resp, start); ferr != nil {
		_ = drainAndClose(resp.Body) // 非 2xx 提前返回，drain 后连接可复用
		return nil, 0, ferr
	}

	body, berr := readBody(ctx, resp, start)
	if berr != nil {
		return nil, 0, berr
	}

	parsed, derr := decodeEnvelope[apiListData](body, resp, start)
	if derr != nil {
		return nil, 0, derr
	}

	requestCompleted(resp, start)

	items = make([]SessionSummary, 0, len(parsed.Data.Items))
	for _, raw := range parsed.Data.Items {
		if req.UserID != 0 && raw.UserID != req.UserID {
			continue // UserID 内存过滤，total 保持上游口径
		}
		items = append(items, toSessionSummary(raw))
	}
	return items, parsed.Data.Total, nil
}

// GetSessionDetail 拉取单会话完整详情（specs §2.4），sessionKey 须来自列表返回值，
// messages 按时间升序全量不分页。错误分类同 ListSessions。
// 对话原文仅在内存临时持有，不写日志与存储（specs §3.3）。
func (c *Client) GetSessionDetail(ctx context.Context, secret string, sessionKey string) (*SessionDetail, error) {
	if c.baseURL == "" {
		return nil, wrapErr(ErrNotConfigured, errors.New("检查 config.Integration.SmartAPIBaseURL"))
	}
	// 空 sessionKey 会使 URL 退化为列表端点，静默拿回错误数据，前置拦截。
	if sessionKey == "" {
		return nil, wrapErr(ErrBadRequest, errors.New("sessionKey 不能为空，须来自列表接口返回值"))
	}

	detailURL := c.baseURL + "/api/conversation-log/" + url.PathEscape(sessionKey)
	start := time.Now()

	resp, err := c.doGet(ctx, detailTimeout, detailURL, secret)
	if err != nil {
		// 重试耗尽已在 retry 层输出 ERROR failed；构造失败返 ErrNotConfigured。
		return nil, err
	}
	defer resp.Body.Close()

	if ferr := failStatus(resp, start); ferr != nil {
		_ = drainAndClose(resp.Body) // 非 2xx 提前返回，drain 后连接可复用
		return nil, ferr
	}

	body, berr := readBody(ctx, resp, start)
	if berr != nil {
		return nil, berr
	}

	parsed, derr := decodeEnvelope[apiDetailData](body, resp, start)
	if derr != nil {
		return nil, derr
	}

	// session 为详情契约必带字段，缺失属契约破坏。
	if parsed.Data.Session.SessionKey == "" {
		requestFailed(resp, start, ErrDecode)
		return nil, wrapErr(ErrDecode, errors.New("响应缺 session 字段"))
	}

	requestCompleted(resp, start)

	turns := make([]TurnMeta, 0, len(parsed.Data.Turns))
	for _, raw := range parsed.Data.Turns {
		turns = append(turns, TurnMeta{
			ID:        raw.ID,
			CreatedAt: raw.CreatedAt,
			RequestID: raw.RequestID,
			TurnKind:  raw.TurnKind,
		})
	}
	messages := make([]Message, 0, len(parsed.Data.Messages))
	for _, raw := range parsed.Data.Messages {
		messages = append(messages, Message{
			Role: raw.Role,
			Kind: raw.Kind,
			Text: raw.Text,
		})
	}
	return &SessionDetail{
		Session:  toSessionSummary(parsed.Data.Session),
		Turns:    turns,
		Messages: messages,
	}, nil
}

// buildListQuery 组装列表 query：仅拼非零字段，PageSize 钳到上游上限 100。
func buildListQuery(req ListSessionsRequest) url.Values {
	q := url.Values{}
	if req.Username != "" {
		q.Set("username", req.Username) // 精确匹配
	}
	if req.StartTime != 0 {
		q.Set("start_timestamp", strconv.FormatInt(req.StartTime, 10))
	}
	if req.EndTime != 0 {
		q.Set("end_timestamp", strconv.FormatInt(req.EndTime, 10))
	}
	if req.Page < 1 {
		req.Page = 1
	}
	q.Set("p", strconv.Itoa(req.Page)) // 上游页码参数名是 p
	if req.PageSize < 1 || req.PageSize > 100 {
		req.PageSize = 100
	}
	q.Set("page_size", strconv.Itoa(req.PageSize))
	return q
}

// newGetRequest 构造带 Accept 与 Bearer 头的 GET 请求。
func newGetRequest(ctx context.Context, reqURL, secret string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	return r, nil
}

// doGet 包 doWithRetry + newGetRequest，列表与详情共用。
func (c *Client) doGet(ctx context.Context, timeout time.Duration, reqURL, secret string) (*http.Response, error) {
	return c.doWithRetry(ctx, timeout, func(reqCtx context.Context) (*http.Request, error) {
		return newGetRequest(reqCtx, reqURL, secret)
	})
}

// failStatus 对非 2xx 响应记日志并返回分类哨兵错误，2xx 返回 nil。
func failStatus(resp *http.Response, start time.Time) error {
	sentinel, msg := classifyStatus(resp.StatusCode, true)
	if sentinel == nil {
		return nil
	}
	requestFailed(resp, start, sentinel)
	return wrapErr(sentinel, fmt.Errorf("%s (HTTP %d)", msg, resp.StatusCode))
}

// classifyStatus 是状态码→哨兵分类纯函数。with400=false 时 400 落兜底桶（Ping 探活口径）。
// 404 单独归 ErrNotFound：REST 形态的记录不存在，详情拉取侧按业务空处理（不重试）。
// 429 单独归 ErrRateLimited：限流是瞬时错误，走传输错误同款重试路径。
func classifyStatus(status int, with400 bool) (error, string) {
	switch {
	case status >= 200 && status < 300:
		return nil, ""
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ErrUnauthorized, "密钥无效"
	case status == http.StatusNotFound:
		return ErrNotFound, "记录不存在"
	case status == http.StatusTooManyRequests:
		return ErrRateLimited, "请求被限流"
	case with400 && status == http.StatusBadRequest:
		return ErrBadRequest, "参数非法"
	default:
		return ErrUnexpectedStatus, "接口异常"
	}
}

// readBody 读取响应体，上限 10MB，超限或读取失败归 ErrDecode。
// 父 ctx 取消直通 ctx 哨兵，其余中断归 ErrNetwork。
func readBody(ctx context.Context, resp *http.Response, start time.Time) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		if perr := ctx.Err(); perr != nil {
			requestFailed(resp, start, perr)
			return nil, wrapErr(perr, fmt.Errorf("读取响应体被调用方中止: %w", err))
		}
		// 读体中断本质是网络故障，不误报解码失败。
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isNetError(err) {
			requestFailed(resp, start, ErrNetwork)
			return nil, wrapErr(ErrNetwork, fmt.Errorf("读取响应体中断: %w", err))
		}
		requestFailed(resp, start, ErrDecode)
		return nil, wrapErr(ErrDecode, fmt.Errorf("读取响应体失败: %w", err))
	}
	if int64(len(body)) > maxBodySize {
		requestFailed(resp, start, ErrDecode)
		return nil, wrapErr(ErrDecode, fmt.Errorf("响应体超上限 (%d bytes)", maxBodySize))
	}
	return body, nil
}

// isNetError 判定错误是否网络层语义。
func isNetError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// decodeEnvelope 解码上游信封 {success, message, data}：
// 非法 JSON 或 success 键缺失/null → ErrDecode；显式 false → ErrUpstreamBusiness。
func decodeEnvelope[T any](body []byte, resp *http.Response, start time.Time) (apiEnvelope[T], error) {
	var parsed apiEnvelope[T]
	if err := json.Unmarshal(body, &parsed); err != nil {
		requestFailed(resp, start, ErrDecode)
		return parsed, wrapErr(ErrDecode, fmt.Errorf("解码响应失败: %w", err))
	}
	// 指针区分键缺失/null 与显式 false。
	if parsed.Success == nil {
		requestFailed(resp, start, ErrDecode)
		return parsed, wrapErr(ErrDecode, errors.New("响应缺 success 字段（信封契约破坏）"))
	}
	if !*parsed.Success {
		requestFailed(resp, start, ErrUpstreamBusiness)
		return parsed, wrapErr(ErrUpstreamBusiness, &UpstreamError{Msg: parsed.Message})
	}
	return parsed, nil
}

// requestCompleted 输出 DEBUG 级成功日志。
func requestCompleted(resp *http.Response, start time.Time) {
	slog.Debug("conversationlog request completed",
		"method", resp.Request.Method,
		"path", resp.Request.URL.Path,
		"status", resp.StatusCode,
		"duration", time.Since(start).String(),
	)
}

// requestFailed 输出 ERROR 级失败日志，字段不含密钥与对话原文。
func requestFailed(resp *http.Response, start time.Time, code error) {
	slog.Error("conversationlog request failed",
		"method", resp.Request.Method,
		"path", resp.Request.URL.Path,
		"status", resp.StatusCode,
		"duration", time.Since(start).String(),
		"code", code.Error(),
	)
}
