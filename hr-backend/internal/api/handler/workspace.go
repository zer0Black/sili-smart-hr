// workspace 工作台域 HTTP 处理器：W1 工作台全量聚合（specs P2_WRK_001 §5.1，03 W1）。
//
// 业务规则：
//   - BR1 接口挂 JWT 鉴权 auth 组，任意已登录平台账号可访问（specs §2.2）。
//   - BR2 单一只读聚合接口，无入参，多余 query 被忽略（specs §5.1.1，03 W1）。
//   - BR3 零新增错误码，唯一整体失败路径 1500（03 §1.10；单源失败已在 service 区块降级吸收）。
package handler

import (
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// WorkspaceHandler 工作台域 HTTP 处理器（03 W1，单 GET）。
type WorkspaceHandler struct {
	svc service.WorkspaceService
}

// NewWorkspaceHandler 构造工作台域处理器。
func NewWorkspaceHandler(svc service.WorkspaceService) *WorkspaceHandler {
	return &WorkspaceHandler{svc: svc}
}

// Overview 处理 GET /api/workspace：无查询参数，service 错误经
// handleServiceError 映射（成功 200，唯一整体失败路径 1500）。
func (h *WorkspaceHandler) Overview(c *gin.Context) {
	dto, err := h.svc.Overview(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}
