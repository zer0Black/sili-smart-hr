// question 题库域 HTTP 处理器：薄壳，绑定/转换/响应，业务在 service（03 §3.1-§3.4/§3.6）。
package handler

import (
	"net/http"
	"strconv"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// QuestionHandler 承载题库域题目行级接口：列表、详情、编辑、启停、删除、重新提交。
// batchSvc 承载重新提交归批（03 §3.5），行级维护与归批分属两个 service。
type QuestionHandler struct {
	svc      service.QuestionService
	batchSvc service.QuestionBatchService
}

func NewQuestionHandler(svc service.QuestionService, batchSvc service.QuestionBatchService) *QuestionHandler {
	return &QuestionHandler{svc: svc, batchSvc: batchSvc}
}

// updateQuestionRequest 对齐 03 §3.3：ID 类字段 string 经 parseID，version 乐观锁。
type updateQuestionRequest struct {
	ID          string `json:"id" binding:"required"`
	DimensionID string `json:"dimension_id" binding:"required"`
	Scenario    string `json:"scenario" binding:"required"`
	Requirement string `json:"requirement" binding:"required"`
	FocusPoint  string `json:"focus_point" binding:"required"`
	Version     int    `json:"version" binding:"required"`
}

type toggleQuestionStatusRequest struct {
	ID           string `json:"id" binding:"required"`
	TargetStatus string `json:"target_status" binding:"required"`
	Version      int    `json:"version" binding:"required"`
}

type deleteQuestionRequest struct {
	ID      string `json:"id" binding:"required"`
	Version int    `json:"version" binding:"required"`
}

// questionQueryPaging 列表分页参数兜底：缺省或非法取 1/20。
func questionQueryPaging(c *gin.Context) (page, pageSize int) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	pageSize, err = strconv.Atoi(c.Query("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = 20
	}
	return page, pageSize
}

// validVersion 乐观锁 version 须为正整数（03 §3.3-§3.6），负数/零在入口即拒 1400。
func validVersion(v int) bool { return v > 0 }

// List 处理 GET /api/questions。source 必填限 AI/SCALE；dimension_id 传值即置 Set；
// page/page_size 缺省或非法兜底 1/20；业务错误统一 HTTP 200 带 code。
func (h *QuestionHandler) List(c *gin.Context) {
	source := c.Query("source")
	if source != domain.QuestionSourceAI && source != domain.QuestionSourceScale {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	in := service.QuestionListInput{
		Source:  source,
		Status:  c.Query("status"),
		Keyword: c.Query("keyword"),
	}
	if dim := c.Query("dimension_id"); dim != "" {
		id, err := parseID(dim)
		if err != nil {
			response.Fail(c, http.StatusOK, errcode.BadRequest)
			return
		}
		in.DimensionID, in.DimensionIDSet = id, true
	}
	in.Page, in.PageSize = questionQueryPaging(c)
	res, err := h.svc.ListQuestions(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Detail 处理 GET /api/questions/:id（与 /questions 静态路由不冲突，Gin 静态优先）。
func (h *QuestionHandler) Detail(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	res, err := h.svc.GetQuestion(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Update 处理 POST /api/questions/update。
func (h *QuestionHandler) Update(c *gin.Context) {
	var req updateQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validVersion(req.Version) {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	dimID, err := parseID(req.DimensionID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	in := service.UpdateQuestionInput{
		ID:          id,
		DimensionID: dimID,
		Scenario:    req.Scenario,
		Requirement: req.Requirement,
		FocusPoint:  req.FocusPoint,
		Version:     req.Version,
	}
	res, err := h.svc.UpdateQuestion(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// ToggleStatus 处理 POST /api/questions/toggle-status。
func (h *QuestionHandler) ToggleStatus(c *gin.Context) {
	var req toggleQuestionStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validVersion(req.Version) {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	res, err := h.svc.ToggleQuestionStatus(c.Request.Context(), id, req.TargetStatus, req.Version)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Resubmit 处理 POST /api/questions/resubmit：请求体同编辑（03 §3.5），委托 batchSvc 归批。
func (h *QuestionHandler) Resubmit(c *gin.Context) {
	var req updateQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validVersion(req.Version) {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	dimID, err := parseID(req.DimensionID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	in := service.ResubmitInput{
		ID:          id,
		DimensionID: dimID,
		Scenario:    req.Scenario,
		Requirement: req.Requirement,
		FocusPoint:  req.FocusPoint,
		Version:     req.Version,
	}
	res, err := h.batchSvc.ResubmitQuestion(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Delete 处理 POST /api/questions/delete。
func (h *QuestionHandler) Delete(c *gin.Context) {
	var req deleteQuestionRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validVersion(req.Version) {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	if err := h.svc.DeleteQuestion(c.Request.Context(), id, req.Version); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c)
}
