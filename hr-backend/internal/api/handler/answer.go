// answer 员工作答域 HTTP 处理器（三接口，03 §3 A1/A2/A3，全 POST 挂公开路由组）。
// 令牌自证鉴权：不读 Authorization 头，错误经 handleServiceError 全走 HTTP 200
// 带 code（本域无 401 路径，03 §2.3）。
package handler

import (
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// AnswerHandler 承载 answer 域三接口（03 §3 A1/A2/A3，全 POST）。
type AnswerHandler struct {
	svc service.AnswerService
}

// NewAnswerHandler 构造 answer 域处理器。
func NewAnswerHandler(svc service.AnswerService) *AnswerHandler {
	return &AnswerHandler{svc: svc}
}

// answerTokenReq 是 A1/A3 的请求体：token 必填（03 各接口错误码表 1400）。
type answerTokenReq struct {
	Token string `json:"token" binding:"required"`
}

// answerReplyReq 是 A2 的请求体：token 缺失 1400；content 无 required 绑定，
// 空串与纯空白经 service 格式校验归 1902（03 A2 错误码表：1400=字段缺失、
// 1902=空串，两者是不同归类）。
type answerReplyReq struct {
	Token   string `json:"token" binding:"required"`
	Content string `json:"content"`
}

// Context 处理 POST /api/answer/context（A1 作答页上下文）。
func (h *AnswerHandler) Context(c *gin.Context) {
	var req answerTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Context(c.Request.Context(), req.Token)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Reply 处理 POST /api/answer/reply（A2 逐题落库）。服务端按已落库记录数推算
// 当前题（specs §5.2.4 规则1），请求体已无客户端题号字段。
func (h *AnswerHandler) Reply(c *gin.Context) {
	var req answerReplyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Reply(c.Request.Context(), req.Token, req.Content)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Submit 处理 POST /api/answer/submit（A3 提交作答）。
func (h *AnswerHandler) Submit(c *gin.Context) {
	var req answerTokenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		handleServiceError(c, service.NewError(errcode.BadRequest))
		return
	}
	res, err := h.svc.Submit(c.Request.Context(), req.Token)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}
