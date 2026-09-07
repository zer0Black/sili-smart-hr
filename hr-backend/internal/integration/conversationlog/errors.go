package conversationlog

import (
	"errors"
	"strings"
)

// sentinel 错误，specs §2.3 错误码定义，调用方经 errors.Is 精确分类。
var (
	// ErrNotConfigured baseURL 未配置（空串），部署配置缺失。
	ErrNotConfigured = errors.New("conversationlog: baseURL not configured")
	// ErrUnauthorized 401/403，确定性错误不重试，按状态码分流定位（§2.3）。
	ErrUnauthorized = errors.New("conversationlog: unauthorized")
	// ErrUpstreamBusiness HTTP 200 但 success=false，载体为 *UpstreamError（§2.3）。
	ErrUpstreamBusiness = errors.New("conversationlog: upstream business error")
	// ErrBadRequest 400 参数非法，确定性错误不重试（§2.3）。
	ErrBadRequest = errors.New("conversationlog: bad request")
	// ErrNotFound HTTP 404：记录不存在（REST 形态的记录缺失表达），确定性错误
	// 不重试，详情拉取侧按业务空处理（与信封 success=false + IsNotFound 白名单
	// 同语义双通道）。
	ErrNotFound = errors.New("conversationlog: not found")
	// ErrNetwork 网络不可达、超时、5xx（重试耗尽后）（§2.3）。
	ErrNetwork = errors.New("conversationlog: network error")
	// ErrDecode JSON 解析失败或响应体超 10MB（§2.3）。
	ErrDecode = errors.New("conversationlog: decode error")
	// ErrUnexpectedStatus 其余非 2xx 兜底桶，确定性错误不重试（§2.3）。
	ErrUnexpectedStatus = errors.New("conversationlog: unexpected status")
	// ErrRateLimited 429 限流：瞬时错误，语义同 ErrNetwork 走重试与 error 通道
	//（§2.3）。上游或中间网关限流时批量请求会集中命中，终态化会整批丢会话。
	ErrRateLimited = errors.New("conversationlog: rate limited")
)

// UpstreamError 是 ErrUpstreamBusiness 的载体：HTTP 200 + success=false。
type UpstreamError struct {
	// Msg 上游 message 原文（i18n 文案，语言随请求 Accept-Language 而定）。
	Msg string
}

// Error 返回上游 Msg 可读信息；不带哨兵前缀，避免 wrapErr 包装后重复拼接
// （specs 330：错误文案只携带定位信息，哨兵英文串仅作日志 code，不进用户可见文案）。
func (e *UpstreamError) Error() string {
	return "upstream: " + e.Msg
}

// Unwrap 恒返回哨兵 ErrUpstreamBusiness，errors.Is(err, ErrUpstreamBusiness) 为真。
func (e *UpstreamError) Unwrap() error { return ErrUpstreamBusiness }

// notFoundMsgs 是记录不存在白名单（specs §2.3 记录不存在识别）。
// 英文条目统一小写，与归一化后的 Msg 比对；中文条目原样匹配。
var notFoundMsgs = map[string]struct{}{
	"会话不存在":                  {},
	"conversation not found": {},
}

// IsNotFound 判定记录不存在：Msg 经 trim 与小写归一后命中白名单。
// 命中即记录不存在，调用方按业务空处理；未命中按会话级失败（§2.3）。
func (e *UpstreamError) IsNotFound() bool {
	msg := strings.ToLower(strings.TrimSpace(e.Msg))
	_, ok := notFoundMsgs[msg]
	return ok
}

// wrapErr 包装 sentinel 与原始错误保留双链：errors.Is 同时命中两者（§2.3）。
// Error() 只输出 cause 可读文案，sentinel 英文串不进文案（specs 330 定位信息白名单，
// 1304 透传前端与通用日志消费 Error()，哨兵串仅用于日志 code 与 errors.Is 分类）。
func wrapErr(sentinel, cause error) error {
	return &classifiedError{sentinel: sentinel, cause: cause}
}

// classifiedError 是 wrapErr 的载体：双链 Unwrap 保留 errors.Is/As 分类能力。
type classifiedError struct {
	sentinel error
	cause    error
}

func (e *classifiedError) Error() string { return e.cause.Error() }

func (e *classifiedError) Unwrap() []error { return []error{e.sentinel, e.cause} }
