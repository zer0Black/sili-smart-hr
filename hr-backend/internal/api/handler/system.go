package handler

import (
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// SystemHandler 承载系统状态与组件健康域接口：摘要与组件探测（specs §3.3、§3.4）。
// status 公开供外部探针，health-check 鉴权后触发外部依赖可达性探测。
type SystemHandler struct {
	svc service.SystemService
}

func NewSystemHandler(svc service.SystemService) *SystemHandler {
	return &SystemHandler{svc: svc}
}

// GetSummary 处理 GET /api/system/status（specs §3.3）。
// service 始终返 *SystemSummary + nil error（specs §5.3.5），
// handler 仍兜底处理可能的非 nil error 走 handleServiceError。成功返 200 + code 0。
func (h *SystemHandler) GetSummary(c *gin.Context) {
	sum, err := h.svc.GetSummary(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{
		"initialized": sum.Initialized,
		"db_type":     sum.DBType,
		"version":     sum.Version,
		"started_at":  sum.StartedAt,
	})
}

// HealthCheck 处理 POST /api/system/health-check（specs §3.4）。
// 探测失败是业务结果而非接口错误（specs §5.4.5），service 始终返 *HealthResult + nil error。
// handler 仍兜底处理可能的非 nil error 走 handleServiceError。成功返 200 + code 0。
func (h *SystemHandler) HealthCheck(c *gin.Context) {
	res, err := h.svc.HealthCheck(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, gin.H{
		"database":    res.Database,
		"redis":       res.Redis,
		"llm":         res.LLM,
		"integration": res.Integration,
	})
}
