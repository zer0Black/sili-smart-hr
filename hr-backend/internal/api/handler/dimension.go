// dimension 能力维度域 HTTP 处理器：薄壳，绑定/转换/响应，业务在 service。
package handler

import (
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// DimensionHandler 承载 dimension 域 7 接口：树、详情、增、改、删、活跃度规则读写。
type DimensionHandler struct {
	svc service.DimensionService
}

func NewDimensionHandler(svc service.DimensionService) *DimensionHandler {
	return &DimensionHandler{svc: svc}
}

// createDimensionRequest：group_code 仅 AI_USAGE 接受，其余模块为 nil。
// Weight/IncludeOverview 用指针区分缺省与显式值，与 service.CreateDimensionInput 对齐。
type createDimensionRequest struct {
	Name            string  `json:"name" binding:"required"`
	ModuleCode      string  `json:"module_code" binding:"required"`
	GroupCode       *string `json:"group_code"`
	DataSource      string  `json:"data_source"`
	Prompt          string  `json:"prompt"`
	Anchor          string  `json:"anchor" binding:"required"`
	Weight          *int    `json:"weight"`
	IncludeOverview *bool   `json:"include_overview"`
	Description     string  `json:"description"`
}

// updateDimensionRequest：id 为 string（雪花 ID JSON string 化）经 parseID，version 乐观锁。
// weight/include_overview/enabled 用指针区分缺省与显式零值（如 ACTIVITY 合法 weight=0），
// 与 service.UpdateDimensionInput 对齐。
type updateDimensionRequest struct {
	ID              string `json:"id" binding:"required"`
	Name            string `json:"name" binding:"required"`
	Prompt          string `json:"prompt"`
	Anchor          string `json:"anchor" binding:"required"`
	Weight          *int   `json:"weight"`
	IncludeOverview *bool  `json:"include_overview"`
	Enabled         *bool  `json:"enabled"`
	Description     string `json:"description"`
	Version         int    `json:"version" binding:"required"`
}

type deleteDimensionRequest struct {
	ID      string `json:"id" binding:"required"`
	Version int    `json:"version" binding:"required"`
}

type activityRuleRequest struct {
	ActiveThreshold       int `json:"active_threshold" binding:"required"`
	LowFrequencyThreshold int `json:"low_frequency_threshold" binding:"required"`
}

// Tree 处理 GET /api/dimensions/tree。
func (h *DimensionHandler) Tree(c *gin.Context) {
	res, err := h.svc.GetTree(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Detail 处理 GET /api/dimensions/:id。
func (h *DimensionHandler) Detail(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	res, err := h.svc.GetDetail(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Create 处理 POST /api/dimensions/create。
func (h *DimensionHandler) Create(c *gin.Context) {
	var req createDimensionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	in := service.CreateDimensionInput{
		Name:            req.Name,
		ModuleCode:      req.ModuleCode,
		GroupCode:       req.GroupCode,
		DataSource:      req.DataSource,
		Prompt:          req.Prompt,
		Anchor:          req.Anchor,
		Weight:          req.Weight,
		IncludeOverview: req.IncludeOverview,
		Description:     req.Description,
	}
	res, err := h.svc.CreateDimension(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Update 处理 POST /api/dimensions/update。
func (h *DimensionHandler) Update(c *gin.Context) {
	var req updateDimensionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	in := service.UpdateDimensionInput{
		ID:              id,
		Name:            req.Name,
		Prompt:          req.Prompt,
		Anchor:          req.Anchor,
		Weight:          req.Weight,
		IncludeOverview: req.IncludeOverview,
		Enabled:         req.Enabled,
		Description:     req.Description,
		Version:         req.Version,
	}
	res, err := h.svc.UpdateDimension(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Delete 处理 POST /api/dimensions/delete。
func (h *DimensionHandler) Delete(c *gin.Context) {
	var req deleteDimensionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	id, err := parseID(req.ID)
	if err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	in := service.DeleteDimensionInput{
		ID:      id,
		Version: req.Version,
	}
	if err := h.svc.DeleteDimension(c.Request.Context(), in); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c)
}

// ActivityRule 处理 GET /api/dimensions/activity-rule。
func (h *DimensionHandler) ActivityRule(c *gin.Context) {
	res, err := h.svc.GetActivityRule(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// SaveActivityRule 处理 POST /api/dimensions/activity-rule/save。
func (h *DimensionHandler) SaveActivityRule(c *gin.Context) {
	var req activityRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusBadRequest, errcode.BadRequest)
		return
	}
	in := service.ActivityRuleInput{
		ActiveThreshold:       req.ActiveThreshold,
		LowFrequencyThreshold: req.LowFrequencyThreshold,
	}
	res, err := h.svc.SaveActivityRule(c.Request.Context(), in)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
