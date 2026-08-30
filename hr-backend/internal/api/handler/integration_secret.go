// integration_secret 集成密钥域 HTTP 处理器。
//
// 业务规则（specs §2.3 接口鉴权矩阵 + 03 §C1-C4 + §4.2.4 规则5）：
//   - BR1 四接口路径与方法：GET /api/integration-secret（卡片，返 configured + masked）、
//     GET /api/integration-secret/detail（临时查看明文）、
//     POST /api/integration-secret/update（覆盖密钥）、
//     POST /api/integration-secret/test（连通验证），统一响应 {code,message,data}。
//   - BR2 03 §C4 错误码：1303 未配置（detail/test 前置拦截）、1304 连通失败携带动态原因、
//     1400 参数错误（update 绑定失败）。
//
// binding 失败刻意走 handleServiceError（HTTP 200 + code 1400），与 llm_config handler 同款：
// 前端按统一解包契约消费（code 非 0 抛 ApiError），HTTP 状态非核心信号。
package handler

import (
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// IntegrationSecretHandler 承载集成密钥域四接口：get/detail/update/test。
type IntegrationSecretHandler struct {
	svc service.IntegrationSecretService
}

// NewIntegrationSecretHandler 构造集成密钥域处理器。
func NewIntegrationSecretHandler(svc service.IntegrationSecretService) *IntegrationSecretHandler {
	return &IntegrationSecretHandler{svc: svc}
}

// updateSecretReq 是 POST /api/integration-secret/update 的请求体。
// Secret 为前端 RSA-OAEP 密文，KeyID 配套 RSA 公钥 ID，Version 为客户端回传的乐观锁凭证（必填）。
type updateSecretReq struct {
	Secret  string `json:"secret" binding:"required"` // RSA 密文
	KeyID   string `json:"keyId" binding:"required"`
	Version int    `json:"version" binding:"required"`
}

// Get 处理 GET /api/integration-secret：返卡片 {configured, secret_masked}（03 §C1）。
func (h *IntegrationSecretHandler) Get(c *gin.Context) {
	res, err := h.svc.Get(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Detail 处理 GET /api/integration-secret/detail：临时查看明文（03 §C2）。
// 未配置时 service 返 1303，handler 透传 200 + code（03 §C4 / BR2）。
func (h *IntegrationSecretHandler) Detail(c *gin.Context) {
	res, err := h.svc.Detail(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Update 处理 POST /api/integration-secret/update：绑定失败走 handleServiceError 映射 1400（03 §C3 / BR2）。
func (h *IntegrationSecretHandler) Update(c *gin.Context) {
	var req updateSecretReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Update(c.Request.Context(), req.Version, req.Secret, req.KeyID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Test 处理 POST /api/integration-secret/test：无请求体，连通验证（03 §C4）。
// service 层 1303（未配置）/1304（连通失败携带动态原因）由 handleServiceError 透传（BR2）。
func (h *IntegrationSecretHandler) Test(c *gin.Context) {
	res, err := h.svc.Test(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
