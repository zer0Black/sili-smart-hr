package handler

import (
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// SetupHandler 承载系统初始化域接口：状态查询与首次管理员账号提交。
// 两接口均为公开路由，不挂 JWT 中间件（specs §1.3 / BR11）。
type SetupHandler struct {
	svc service.SetupService
}

func NewSetupHandler(svc service.SetupService) *SetupHandler {
	return &SetupHandler{svc: svc}
}

// initializeRequest 承载 POST /api/setup/initialize 请求体。
// passwordCipher 是经 RSA-OAEP 加密的密码密文，keyId 标识一次性密钥（specs §3.2）。
type initializeRequest struct {
	Username       string `json:"username" binding:"required"`
	Name           string `json:"name" binding:"required"`
	PasswordCipher string `json:"passwordCipher" binding:"required"`
	KeyID          string `json:"keyId" binding:"required"`
}

// Status 处理 GET /api/setup/status（specs §3.1）。
// service 始终返 *SetupStatus + nil error，异常内化为字段 false；
// handler 仍兜底处理可能的非 nil error 走 handleServiceError。成功返 200 + code 0。
func (h *SetupHandler) Status(c *gin.Context) {
	st, err := h.svc.GetStatus(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{
		"initialized": st.Initialized,
		"db_type":     st.DBType,
		"checks": gin.H{
			"database":   gin.H{"connected": st.DatabaseConnected},
			"redis":      gin.H{"connected": st.RedisConnected},
			"jwt_secret": gin.H{"secure": st.JwtSecretSecure},
		},
		"block_submit": st.BlockSubmit,
	})
}

// Initialize 处理 POST /api/setup/initialize（specs §3.2）。
// 绑定失败返 400/1400；service 业务错误（1101/1102/1400/1007/1005）走 handleServiceError 统一 200 带 code；
// 成功返 200 + code 0，account.id 以 JSON string 化规避 JS 精度坑（BR12）。
func (h *SetupHandler) Initialize(c *gin.Context) {
	var req initializeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	res, err := h.svc.Initialize(c.Request.Context(), req.Username, req.Name, req.PasswordCipher, req.KeyID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{
		"initialized": true,
		"account":     res.Account,
	})
}
