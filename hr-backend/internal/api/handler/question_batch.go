// question_batch 题库批次域 HTTP 处理器：薄壳，绑定/转换/响应，业务在 service（03 §3.7-§3.10）。
package handler

import (
	"net/http"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// QuestionBatchHandler 承载批次域四接口：卡区列表、批次明细、确认入库、作废。
type QuestionBatchHandler struct {
	svc service.QuestionBatchService
}

func NewQuestionBatchHandler(svc service.QuestionBatchService) *QuestionBatchHandler {
	return &QuestionBatchHandler{svc: svc}
}

// confirmBatchRequest 对齐 03 §3.9：rejected 可缺省（nil 视为全通过）。
type confirmBatchRequest struct {
	Rejected []confirmRejectItem `json:"rejected"`
}

type confirmRejectItem struct {
	QuestionID string `json:"question_id"`
	Reason     string `json:"reason"`
}

// batchIDParam 路径批次 ID 解析，非数字返 1400。
func batchIDParam(c *gin.Context) (int64, bool) {
	id, err := parseID(c.Param("id"))
	if err != nil {
		response.Fail(c, http.StatusOK, errcode.BadRequest)
		return 0, false
	}
	return id, true
}

// ListBatches 处理 GET /api/question-batches：仅 PENDING、不分页，data 用 {list,total} 包装。
func (h *QuestionBatchHandler) ListBatches(c *gin.Context) {
	list, err := h.svc.ListPendingBatches(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if list == nil {
		list = []service.BatchCardDTO{} // 无待审核批次时空 list 非 null（03 §3.7）
	}
	response.OKWithData(c, gin.H{"list": list, "total": len(list)})
}

// BatchQuestions 处理 GET /api/question-batches/:id/questions。
func (h *QuestionBatchHandler) BatchQuestions(c *gin.Context) {
	id, ok := batchIDParam(c)
	if !ok {
		return
	}
	res, err := h.svc.GetBatchQuestions(c.Request.Context(), id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Confirm 处理 POST /api/question-batches/:id/confirm：整批一次性提交标记结果
//（specs §4.2.4 规则 1）。无 body 或 {} 等价全通过，rejected 缺省透传 nil。
func (h *QuestionBatchHandler) Confirm(c *gin.Context) {
	id, ok := batchIDParam(c)
	if !ok {
		return
	}
	var req confirmBatchRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Fail(c, http.StatusOK, errcode.BadRequest)
			return
		}
	}
	// rejected 缺省保持 nil 透传（全通过）；显式空数组按非 nil 空 slice 透传，service 两者等价。
	var rejected []service.RejectItem
	if req.Rejected != nil {
		rejected = make([]service.RejectItem, 0, len(req.Rejected))
		for _, item := range req.Rejected {
			qid, err := parseID(item.QuestionID)
			if err != nil {
				response.Fail(c, http.StatusOK, errcode.BadRequest)
				return
			}
			rejected = append(rejected, service.RejectItem{QuestionID: qid, Reason: item.Reason})
		}
	}
	res, err := h.svc.ConfirmBatch(c.Request.Context(), id, rejected)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Void 处理 POST /api/question-batches/:id/void：无请求体，二次确认由前端承载
//（specs §4.1.3、03 §3.10）。
func (h *QuestionBatchHandler) Void(c *gin.Context) {
	id, ok := batchIDParam(c)
	if !ok {
		return
	}
	if err := h.svc.VoidBatch(c.Request.Context(), id); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c)
}
