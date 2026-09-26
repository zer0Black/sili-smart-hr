// question_generation 生成会话 HTTP 处理器：薄壳，绑定/转换/响应，业务在
// service（03 §3.13/§3.14）。enqueuer 落位 service 构造（CreateGeneration 编排
// 建行→投递），handler 不再持有。
package handler

import (
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// QuestionGenerationHandler 承载生成域三接口：发起、进度轮询、取消。
type QuestionGenerationHandler struct {
	svc service.QuestionGenerationService
}

func NewQuestionGenerationHandler(svc service.QuestionGenerationService) *QuestionGenerationHandler {
	return &QuestionGenerationHandler{svc: svc}
}

// createGenerationRequest 对齐 03 §3.13：dimension_ids 雪花 string 数组（JS 精度
// 约定），count 整数。两项必填由 binding 与手动双保险（元素级校验在循环内）。
type createGenerationRequest struct {
	DimensionIDs []string `json:"dimension_ids" binding:"required"`
	Count        int      `json:"count" binding:"required"`
}

// Create 处理 POST /api/question-generations/create：元素级 parseID 转整型后
// 委托 service（空数组元素/非数字项返 1400）。
func (h *QuestionGenerationHandler) Create(c *gin.Context) {
	var req createGenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	dims := make([]int64, 0, len(req.DimensionIDs))
	for _, s := range req.DimensionIDs {
		id, err := parseID(s)
		if err != nil {
			response.Fail(c, http.StatusOK, errcode.BadRequest)
			return
		}
		dims = append(dims, id)
	}
	res, err := h.svc.CreateGeneration(c.Request.Context(), dims, req.Count)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Progress 处理 GET /api/question-generations/:id：路径 ID 解析后委托 service。
func (h *QuestionGenerationHandler) Progress(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	res, err := h.svc.GetProgress(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Cancel 处理 POST /api/question-generations/:id/cancel：无请求体，终态幂等成功
// 语义由 service/repo 承载。
func (h *QuestionGenerationHandler) Cancel(c *gin.Context) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return
	}
	if err := h.svc.CancelGeneration(c.Request.Context(), id); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c)
}
