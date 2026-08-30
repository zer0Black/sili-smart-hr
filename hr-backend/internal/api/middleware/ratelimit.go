// Package middleware 承载 HTTP 中间件：限流、JWT 鉴权、CORS、日志、recovery。
package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// KeyFunc 从请求上下文提取限流桶标识，空串表示该维度不计桶（fail-open）。
type KeyFunc func(*gin.Context) string

func ClientIPKey(c *gin.Context) string { return c.ClientIP() }

// AccountIDKey 以 JWT 中间件注入的 account_id 作限流桶标识。
// 用于已鉴权接口按账号限流；未鉴权（account_id 缺失）返回空串 fail-open。
func AccountIDKey(c *gin.Context) string {
	id, ok := AccountIDFromContext(c)
	if !ok {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// JSONFieldKey 返回一个 KeyFunc：peek 请求体取指定字段，trim 并小写归一化作桶标识，
// 随后复位 body 供后续 handler 绑定。归一化让 "Admin"/" admin " 合并到同桶。读取或解析失败返回 ""。
func JSONFieldKey(field string) KeyFunc {
	return func(c *gin.Context) string {
		if c.Request == nil || c.Request.Body == nil {
			return ""
		}
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			// 读取中途出错（如客户端连接中断）：raw 是截断数据，不能复位给下游。
			// body 已被 ReadAll 消费，置空 reader 让下游拿到 EOF 而非残缺 JSON，fail-open 返回 ""。
			c.Request.Body = io.NopCloser(bytes.NewReader(nil))
			return ""
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return ""
		}
		s, ok := m[field].(string)
		if !ok {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
}

// rateLimitScript 原子地 INCR 并在首计时设过期。把两步收敛进单脚本，消除「INCR 成功
// 但 EXPIRE 未设」导致的无 TTL 残留键持续累加。
var rateLimitScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return n
`)

// RateLimit 基于 Redis 计数的固定窗口限流，按 keyFn 提取的标识分桶。
// keyFn 返回空串或 Redis 异常时 fail-open：限流仅作反滥用辅助，反枚举主防线仍是 bcrypt 时序收敛。超限返回 429。
func RateLimit(rdb *redis.Client, keyPrefix string, keyFn KeyFunc, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ident := keyFn(c)
		if ident == "" {
			c.Next()
			return
		}
		key := keyPrefix + ident
		n, err := rateLimitScript.Run(c.Request.Context(), rdb, []string{key}, window.Milliseconds()).Int64()
		if err != nil {
			slog.Warn("ratelimit redis failed, fail open", "key", key, "err", err)
			c.Next()
			return
		}
		if n > int64(limit) {
			response.FailWithMsg(c, http.StatusTooManyRequests, errcode.Internal, "too many requests")
			c.Abort()
			return
		}
		c.Next()
	}
}
