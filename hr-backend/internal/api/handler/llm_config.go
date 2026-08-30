// llm_config 大模型配置域 HTTP 处理器。
//
// 业务规则（specs §2.3 接口鉴权矩阵 + 03 §B1-B6 + §2.4 id string 化）：
//   - BR1 六接口路径与方法：GET /api/llm-configs（list，非分页数组），
//     POST /api/llm-configs/create|update|delete|enable，GET /api/llm-configs/:id，
//     统一响应 {code,message,data}。
//   - BR2 模型主键 id 与 transferred_enabled_id 均雪花 ID，json string 化（§2.4），
//     service 层 DTO 已双层覆盖，handler 仅透传。
//
// binding 失败刻意走 handleServiceError（HTTP 200 + code 1400），与 assessment handler 同款：
// 前端按统一解包契约消费（code 非 0 抛 ApiError），HTTP 状态非核心信号。
package handler

import (
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// LLMConfigHandler 承载大模型配置域六接口：list/create/update/delete/enable/detail。
type LLMConfigHandler struct {
	svc service.LLMConfigService
}

// NewLLMConfigHandler 构造大模型配置域处理器。
func NewLLMConfigHandler(svc service.LLMConfigService) *LLMConfigHandler {
	return &LLMConfigHandler{svc: svc}
}

// createLLMReq 是 POST /api/llm-configs/create 的请求体。api_key 为前端 RSA-OAEP 密文。
type createLLMReq struct {
	Name     string `json:"name" binding:"required"`
	Provider string `json:"provider" binding:"required"`
	ModelID  string `json:"model_id" binding:"required"`
	APIURL   string `json:"api_url"`
	APIKey   string `json:"api_key" binding:"required"` // RSA 密文
	KeyID    string `json:"keyId" binding:"required"`
}

// updateLLMReq 是 POST /api/llm-configs/update 的请求体。
// api_key 空串表示不改原值；非空时 keyId 必填。Version 为客户端回传的乐观锁凭证（必填）。
type updateLLMReq struct {
	ID       string `json:"id" binding:"required"`
	Version  int    `json:"version" binding:"required"`
	Name     string `json:"name" binding:"required"`
	Provider string `json:"provider" binding:"required"`
	ModelID  string `json:"model_id" binding:"required"`
	APIURL   string `json:"api_url"`
	APIKey   string `json:"api_key"` // 空串表示不改
	KeyID    string `json:"keyId"`   // api_key 非空时必填
}

// idReq 是 delete/enable 的请求体，id 为雪花 ID 字符串（§2.4）。
type idReq struct {
	ID string `json:"id" binding:"required"`
}

// List 处理 GET /api/llm-configs?keyword=：非分页，data 直接是数组（03 §B1）。
func (h *LLMConfigHandler) List(c *gin.Context) {
	keyword := c.Query("keyword")
	list, err := h.svc.List(c.Request.Context(), keyword)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, list)
}

// Create 处理 POST /api/llm-configs/create。绑定失败走 handleServiceError 映射 1400。
func (h *LLMConfigHandler) Create(c *gin.Context) {
	var req createLLMReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Create(c.Request.Context(), req.Name, req.Provider, req.ModelID, req.APIURL, req.APIKey, req.KeyID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Update 处理 POST /api/llm-configs/update。区分 api_key 空否决定是否重写密文。
func (h *LLMConfigHandler) Update(c *gin.Context) {
	var req updateLLMReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	hasAPIKey := req.APIKey != ""
	res, err := h.svc.Update(c.Request.Context(), id, req.Version, req.Name, req.Provider, req.ModelID, req.APIURL, req.APIKey, req.KeyID, hasAPIKey)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Delete 处理 POST /api/llm-configs/delete。data.transferred_enabled_id 为 string 或 null（03 §B4）。
func (h *LLMConfigHandler) Delete(c *gin.Context) {
	var req idReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Delete(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Enable 处理 POST /api/llm-configs/enable。排他启用，data 返回 {id, enabled:true}（03 §B5）。
func (h *LLMConfigHandler) Enable(c *gin.Context) {
	var req idReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Enable(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Detail 处理 GET /api/llm-configs/:id。路径参数 :id（Gin 静态优先，与 /llm-configs list 不冲突）。
// data.api_key 为后端 AES 解密后的明文（03 §B6）。
func (h *LLMConfigHandler) Detail(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Detail(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
