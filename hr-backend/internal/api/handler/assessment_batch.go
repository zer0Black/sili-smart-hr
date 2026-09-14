// assessment_batch 批次域 HTTP 处理器（六接口，03 §3：A1 列表、A2 统计、A3 计划、
// A4 名单、A5 失败明细、B1 发起手动定向分析）。binding/解析失败与业务错误均走
// handleServiceError（HTTP 200 + code，前端统一解包契约）。
package handler

import (
	"strconv"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// AssessmentBatchHandler 承载批次域六接口。
type AssessmentBatchHandler struct {
	svc service.AssessmentBatchService
}

// NewAssessmentBatchHandler 构造批次域处理器。
func NewAssessmentBatchHandler(svc service.AssessmentBatchService) *AssessmentBatchHandler {
	return &AssessmentBatchHandler{svc: svc}
}

// List 处理 GET /api/assessment/batches。page 缺省 1、page_size 缺省 10（≤0 或非法兜底），
// page_size >100 钳 100；trigger_type/status 空串=全部透传，枚举合法性由 service 层判定。
func (h *AssessmentBatchHandler) List(c *gin.Context) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	pageSize, err := strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	list, total, err := h.svc.List(c.Request.Context(), service.BatchListFilter{
		TriggerType: c.Query("trigger_type"),
		Status:      c.Query("status"),
		Page:        page,
		PageSize:    pageSize,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithPage(c, list, total, page, pageSize)
}

// Stats 处理 GET /api/assessment/batches/stats（03 A2 跑批态势统计卡）。
func (h *AssessmentBatchHandler) Stats(c *gin.Context) {
	dto, err := h.svc.Stats(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// Plan 处理 GET /api/assessment/batches/plan（03 A3 跑批计划卡）。
func (h *AssessmentBatchHandler) Plan(c *gin.Context) {
	dto, err := h.svc.Plan(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// parseBatchID 解析 batch_id 查询参数，缺失、非数字或 ≤0 均返 error（调用方映射 1400）。
func parseBatchID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Query("batch_id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, service.NewError(errcode.BadRequest)
	}
	return id, nil
}

// Targets 处理 GET /api/assessment/batches/targets（03 A4 评估对象名单）。batch_id 必填。
func (h *AssessmentBatchHandler) Targets(c *gin.Context) {
	batchID, err := parseBatchID(c)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	dto, err := h.svc.Targets(c.Request.Context(), batchID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// Failures 处理 GET /api/assessment/batches/failures（03 A5 失败明细）。batch_id 必填。
func (h *AssessmentBatchHandler) Failures(c *gin.Context) {
	batchID, err := parseBatchID(c)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	dto, err := h.svc.Failures(c.Request.Context(), batchID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// createBatchStaff 是 B1 请求体 staffs 元素结构（系统无工号：staff_id 为上游用户标识）。
type createBatchStaff struct {
	StaffID   string `json:"staff_id"`
	StaffName string `json:"staff_name"`
}

// createBatchReq 是 POST /api/assessment/batches/create 的请求体（03 B1）。
// target_mode/period_start/period_end 必填；staffs 在 target_mode=specified 时由 service 层判空。
type createBatchReq struct {
	TargetMode  string             `json:"target_mode" binding:"required"`
	Staffs      []createBatchStaff `json:"staffs"`
	PeriodStart string             `json:"period_start" binding:"required"`
	PeriodEnd   string             `json:"period_end" binding:"required"`
}

// Create 处理 POST /api/assessment/batches/create（03 B1 发起手动定向分析）。
// binding 失败走 handleServiceError（HTTP 200 + 1400），业务校验全部下沉 service 层。
func (h *AssessmentBatchHandler) Create(c *gin.Context) {
	var req createBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	staffs := make([]service.StaffDTO, 0, len(req.Staffs))
	for i := range req.Staffs {
		staffs = append(staffs, service.StaffDTO{
			StaffID:   req.Staffs[i].StaffID,
			StaffName: req.Staffs[i].StaffName,
		})
	}
	res, err := h.svc.Create(c.Request.Context(), service.CreateBatchPayload{
		TargetMode:  req.TargetMode,
		Staffs:      staffs,
		PeriodStart: req.PeriodStart,
		PeriodEnd:   req.PeriodEnd,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
