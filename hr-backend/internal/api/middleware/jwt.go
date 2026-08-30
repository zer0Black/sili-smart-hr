package middleware

import (
	"net/http"
	"strings"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

const authScheme = "Bearer "

// JWT 是鉴权中间件，挂载于受保护路由组。
// 从 Authorization: Bearer <token> 取 token，校验签名与过期，
// 注入 account_id 与 username 到 gin.Context，失败返回 401 code=1003。
func JWT(mgr *jwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, authScheme) {
			response.Fail(c, http.StatusUnauthorized, errcode.Unauthorized)
			c.Abort()
			return
		}
		token := strings.TrimPrefix(raw, authScheme)
		if token == "" {
			response.Fail(c, http.StatusUnauthorized, errcode.Unauthorized)
			c.Abort()
			return
		}
		claims, err := mgr.Parse(token)
		if err != nil {
			response.Fail(c, http.StatusUnauthorized, errcode.Unauthorized)
			c.Abort()
			return
		}
		c.Set("account_id", claims.AccountID)
		c.Set("username", claims.Username)
		c.Next()
	}
}

// AccountIDFromContext 取出 JWT 中间件注入的 account_id。
func AccountIDFromContext(c *gin.Context) (int64, bool) {
	v, ok := c.Get("account_id")
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}
