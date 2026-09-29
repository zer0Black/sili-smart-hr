// assessment_test_task 主动测试域 HTTP 处理器（七接口，03 §3：A1 列表、A2 轮询
// 计数、B1 发起、B2 量表就绪、C1 链接查询、C2 重发、C3 取消）。binding/解析失败
// 与业务错误均走 handleServiceError（HTTP 200 + code，前端统一解包契约）。
package handler

import (
	"strconv"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// AssessmentTestTaskHandler 承载主动测试域七接口。
type AssessmentTestTaskHandler struct {
	svc service.AssessmentTestTaskService
}

// NewAssessmentTestTaskHandler 构造主动测试域处理器。
func NewAssessmentTestTaskHandler(svc service.AssessmentTestTaskService) *AssessmentTestTaskHandler {
	return &AssessmentTestTaskHandler{svc: svc}
}

// List 处理 GET /api/assessment/test-tasks。page/page_size 缺省与钳制（缺省 10、
// 上限 100）由 repository.ClampPageSize 统一收口；test_type 必填与 status 枚举
// 校验在 service 层（03 A1：缺省与非法统一 1400）。
func (h *AssessmentTestTaskHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	list, total, err := h.svc.List(c.Request.Context(), service.ListTestTaskFilter{
		TestType: c.Query("test_type"),
		Status:   c.Query("status"),
		Keyword:  c.Query("keyword"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithPage(c, list, total, page, pageSize)
}

// PollCounts 处理 GET /api/assessment/test-tasks/poll-counts（03 A2 轮询探针计数）。
func (h *AssessmentTestTaskHandler) PollCounts(c *gin.Context) {
	dto, err := h.svc.PollCounts(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// ScaleStatus 处理 GET /api/assessment/test-tasks/scale-status（03 B2 九型量表就绪查询）。
func (h *AssessmentTestTaskHandler) ScaleStatus(c *gin.Context) {
	dto, err := h.svc.ScaleStatus(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// parseTestTaskID 解析 task_id 参数（GET 查询/POST body 字符串），缺失、非数字或
// ≤0 均返 error（调用方映射 1400，雪花 ID string 传输口径 03 §2.4）。
func parseTestTaskID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, service.NewError(errcode.BadRequest)
	}
	return id, nil
}

// Link 处理 GET /api/assessment/test-tasks/link（03 C1）。task_id 必填。
func (h *AssessmentTestTaskHandler) Link(c *gin.Context) {
	id, err := parseTestTaskID(c.Query("task_id"))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	dto, err := h.svc.Link(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// createTestTaskReq 是 POST /api/assessment/test-tasks/create 的请求体（03 B1）。
// test_type/staff_id/staff_name 必填；dimension_ids 条件校验（ai_mgmt 必填非空）
// 归 service 层。
type createTestTaskReq struct {
	TestType     string   `json:"test_type" binding:"required"`
	StaffID      string   `json:"staff_id" binding:"required"`
	StaffName    string   `json:"staff_name" binding:"required"`
	DimensionIDs []string `json:"dimension_ids"`
}

// Create 处理 POST /api/assessment/test-tasks/create（03 B1 发起主动测试）。
// binding 失败走 handleServiceError（HTTP 200 + 1400），业务校验全部下沉 service 层。
func (h *AssessmentTestTaskHandler) Create(c *gin.Context) {
	var req createTestTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Create(c.Request.Context(), service.CreateTestTaskPayload{
		TestType:     req.TestType,
		StaffID:      req.StaffID,
		StaffName:    req.StaffName,
		DimensionIDs: req.DimensionIDs,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// taskIDReq 是 C2/C3 的请求体：task_id 为雪花 ID 十进制字符串（03 §2.4）。
type taskIDReq struct {
	TaskID string `json:"task_id" binding:"required"`
}

// Resend 处理 POST /api/assessment/test-tasks/resend（03 C2 重发作答链接）。
func (h *AssessmentTestTaskHandler) Resend(c *gin.Context) {
	id, err := h.parseTaskIDBody(c)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	dto, err := h.svc.Resend(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// Cancel 处理 POST /api/assessment/test-tasks/cancel（03 C3 取消测试任务）。
func (h *AssessmentTestTaskHandler) Cancel(c *gin.Context) {
	id, err := h.parseTaskIDBody(c)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	dto, err := h.svc.Cancel(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// parseTaskIDBody 绑定 body 并解析 task_id：binding 失败或值非法统一 1400。
func (h *AssessmentTestTaskHandler) parseTaskIDBody(c *gin.Context) (int64, error) {
	var req taskIDReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return 0, service.NewError(errcode.BadRequest)
	}
	return parseTestTaskID(req.TaskID)
}
