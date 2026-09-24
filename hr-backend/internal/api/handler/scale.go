// scale 量表引入域 HTTP 处理器：薄壳，绑定/转换/响应，业务在 service（03 §3.11/§3.12）。
package handler

import (
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// ScaleHandler 承载量表域两接口：候选列表、引入九型量表。
type ScaleHandler struct {
	svc service.ScaleService
}

func NewScaleHandler(svc service.ScaleService) *ScaleHandler {
	return &ScaleHandler{svc: svc}
}

type importScaleRequest struct {
	ScaleKey string `json:"scale_key" binding:"required"`
}

// List 处理 GET /api/scales：data 用 {list} 包装（03 §3.11 无 total）。
func (h *ScaleHandler) List(c *gin.Context) {
	list, err := h.svc.ListScales(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if list == nil {
		list = []service.ScaleCandidateDTO{} // 空 list 非 null，前端契约稳定
	}
	response.OKWithData(c, gin.H{"list": list})
}

// Import 处理 POST /api/scales/import：请求体 {scale_key}（03 §3.12）。
func (h *ScaleHandler) Import(c *gin.Context) {
	var req importScaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	res, err := h.svc.ImportScale(c.Request.Context(), req.ScaleKey)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
