package handler

import (
	"sili-smart-hr/backend/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// HealthHandler 承载健康检查接口 GET /health。
type HealthHandler struct{}

// NewHealthHandler 构造 HealthHandler。
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health 处理 GET /health，无需鉴权。
func (h *HealthHandler) Health(c *gin.Context) {
	response.OKWithData(c, gin.H{"status": "ok"})
}
