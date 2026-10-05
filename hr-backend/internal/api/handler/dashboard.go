// dashboard 团队看板域 HTTP 处理器：A1 看板全量 / A2 能力逐期趋势（specs P2_TMD_001 §5.2）。
//
// 业务规则：
//   - BR1 两接口挂 JWT 鉴权 auth 组，任意已登录账号可访问（specs §2.2）。
//   - BR2 type 非法值按 use 兜底不报错（specs §5.2.2 步2，兜底归 service，handler 透传）。
//   - BR3 只读聚合接口，聚合实时计算不落库（specs §5.2.1）。
package handler

import (
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// DashboardHandler 团队看板域 HTTP 处理器（03 A1/A2，全 GET）。
type DashboardHandler struct {
	svc service.DashboardService
}

// NewDashboardHandler 构造团队看板域处理器。
func NewDashboardHandler(svc service.DashboardService) *DashboardHandler {
	return &DashboardHandler{svc: svc}
}

// Overview 处理 GET /api/dashboard：query 绑定 period_start/period_end（空时默认最新
// 落库区间）。参数校验与 2101 判定归 service，handler 原样透传。
func (h *DashboardHandler) Overview(c *gin.Context) {
	dto, err := h.svc.Overview(c.Request.Context(), c.Query("period_start"), c.Query("period_end"))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// Trend 处理 GET /api/dashboard/trend：query 绑定 type（use|manage，非法值兜底归
// service），handler 原样透传。
func (h *DashboardHandler) Trend(c *gin.Context) {
	dto, err := h.svc.Trend(c.Request.Context(), c.Query("type"))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}
