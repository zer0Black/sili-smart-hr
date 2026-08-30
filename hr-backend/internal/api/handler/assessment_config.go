// assessment_config 评估周期配置域 HTTP 处理器。
//
// 业务规则（specs §4.1.2 + §4.1.4 + 03 §A1/A2/A3）：
//   - BR1 三接口路径 GET /api/assessment-config、POST /api/assessment-config/save、GET /api/staffs，
//     统一响应 {code,message,data}。
//   - BR2 三接口挂 JWT 鉴权 auth 组；承载雪花 ID 的字段（AssessmentConfigDTO.ID / SaveAssessmentResult.ID）
//     json 一律 string 化，domain 与 DTO 是两条独立序列化路径，service 层 DTO 已双层覆盖。
//
// binding 失败刻意走 handleServiceError（HTTP 200 + code 1400），与 account handler 直接 400 不同：
// 评估配置前端按统一解包契约消费（code 非 0 抛 ApiError），HTTP 状态非核心信号。
package handler

import (
	"strconv"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// AssessmentConfigHandler 承载评估周期配置域三接口：详情读取、保存、人员列表代理。
type AssessmentConfigHandler struct {
	svc service.AssessmentConfigService
}

// NewAssessmentConfigHandler 构造评估周期配置域处理器。
func NewAssessmentConfigHandler(svc service.AssessmentConfigService) *AssessmentConfigHandler {
	return &AssessmentConfigHandler{svc: svc}
}

// saveAssessmentMember 是请求体 specified_members 元素的结构（系统无工号：仅标识与姓名）。
type saveAssessmentMember struct {
	StaffID   string `json:"staff_id"`
	StaffName string `json:"staff_name"`
}

// saveAssessmentReq 是 POST /api/assessment-config/save 的请求体。
// period/trigger_time/target_mode 必填；version 用于乐观锁；specified_members 在 target_mode=specified 时必填。
type saveAssessmentReq struct {
	Period           string                  `json:"period" binding:"required"`
	TriggerTime      string                  `json:"trigger_time" binding:"required"`
	TargetMode       string                  `json:"target_mode" binding:"required"`
	SpecifiedMembers []saveAssessmentMember  `json:"specified_members"`
	Version          int                     `json:"version"`
}

// Get 处理 GET /api/assessment-config：读取单例配置与指定人员列表。
func (h *AssessmentConfigHandler) Get(c *gin.Context) {
	dto, err := h.svc.Get(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// Save 处理 POST /api/assessment-config/save：字段校验在 service 层，handler 只做绑定与 DTO 转换。
// binding 失败统一走 handleServiceError（HTTP 200 + code 1400）。
func (h *AssessmentConfigHandler) Save(c *gin.Context) {
	var req saveAssessmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	members := make([]service.StaffDTO, 0, len(req.SpecifiedMembers))
	for i := range req.SpecifiedMembers {
		members = append(members, service.StaffDTO{
			StaffID:   req.SpecifiedMembers[i].StaffID,
			StaffName: req.SpecifiedMembers[i].StaffName,
		})
	}
	res, err := h.svc.Save(c.Request.Context(), req.Period, req.TriggerTime, req.TargetMode, members, req.Version)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Staffs 处理 GET /api/staffs：代理外部用户体系查询。page/page_size 缺省或非法兜底 1/20。
func (h *AssessmentConfigHandler) Staffs(c *gin.Context) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	pageSize, err := strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = 20
	}
	keyword := c.Query("keyword")
	items, total, err := h.svc.ListStaffs(c.Request.Context(), keyword, page, pageSize)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithPage(c, items, total, page, pageSize)
}
