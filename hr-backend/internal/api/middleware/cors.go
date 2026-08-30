// Package middleware 承载 HTTP 中间件：CORS、JWT 鉴权、Recovery、请求日志。
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS 是最小手写的跨域中间件，不引外部依赖。
// 放通 allowedOrigins 命中的来源，回填 Allow-Origin/Allow-Headers（含 Authorization），
// 对 OPTIONS 预检回 204 并 abort，不走后续中间件。
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Credentials", "true")
				c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
				c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				c.Header("Access-Control-Max-Age", "86400")
				c.Header("Vary", "Origin")
			}
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
