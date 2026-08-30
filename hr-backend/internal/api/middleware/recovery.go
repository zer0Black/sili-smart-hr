package middleware

import (
	"log/slog"
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// Recovery 捕获 handler 链中的 panic，记录并返回 500，避免单请求 panic 打垮进程。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic recovered",
					"error", r,
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
				)
				response.Fail(c, http.StatusInternalServerError, errcode.Internal)
				c.Abort()
			}
		}()
		c.Next()
	}
}
