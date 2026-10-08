// operation_log 操作日志域 HTTP 处理器：A1 列表 / A2 导出，全 GET 查询语义
//（specs P4_LOG_001 §2.2/§5.3/§5.4、03 §3）。
//
// 业务规则：
//   - BR1 两接口挂 JWT 鉴权 auth 组，已登录账号可查询导出，无角色差异。
//   - BR2 导出成功直写 xlsx 二进制流旁路统一响应结构；失败返回统一 JSON 错误结构。
package handler

import (
	"net/url"
	"strconv"
	"strings"

	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// OperationLogHandler 操作日志域 HTTP 处理器（03 A1/A2，全 GET 只读不记日志）。
type OperationLogHandler struct {
	svc service.OperationLogService
}

// NewOperationLogHandler 构造操作日志域处理器。
func NewOperationLogHandler(svc service.OperationLogService) *OperationLogHandler {
	return &OperationLogHandler{svc: svc}
}

// List 处理 GET /api/operation-logs：四维筛选 + 分页；枚举/日期校验归 service
// 层 1400，分页缺失或非法兜底 1/10（钳位归 service）。
func (h *OperationLogHandler) List(c *gin.Context) {
	q := parseOperationLogQuery(c)
	q.Page, q.PageSize = parseOperationLogPaging(c)
	res, err := h.svc.List(c.Request.Context(), q)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithPage(c, res.List, res.Total, res.Page, res.PageSize)
}

// Export 处理 GET /api/operation-logs/export：成功直写 xlsx 二进制流
//（Content-Disposition 双轨，RFC 5987 filename* 承载中文名）；失败按
// Content-Type 判别返回统一 JSON 错误结构（handleServiceError）。
func (h *OperationLogHandler) Export(c *gin.Context) {
	q := parseOperationLogQuery(c)
	data, filename, err := h.svc.Export(c.Request.Context(), q)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	disposition := "attachment; filename=\"operation-logs.xlsx\"; filename*=UTF-8''" + url.PathEscape(filename)
	c.Header("Content-Type", xlsxMIME)
	c.Header("Content-Disposition", disposition)
	c.Data(200, xlsxMIME, data)
}

// parseOperationLogQuery 解析 A1/A2 共用筛选 query：operator 去首尾空格，
// 其余原样透传（枚举/日期校验归 service 1400）。
func parseOperationLogQuery(c *gin.Context) service.OperationLogQuery {
	return service.OperationLogQuery{
		Operator:  strings.TrimSpace(c.Query("operator")),
		Module:    c.Query("module"),
		Result:    c.Query("result"),
		StartDate: c.Query("start_date"),
		EndDate:   c.Query("end_date"),
	}
}

// parseOperationLogPaging 解析分页 query：缺失或非法兜底 1/10，越界钳位归 service。
func parseOperationLogPaging(c *gin.Context) (page, pageSize int) {
	page = 1
	pageSize = 10
	if v, err := strconv.Atoi(c.Query("page")); err == nil {
		page = v
	}
	if v, err := strconv.Atoi(c.Query("page_size")); err == nil {
		pageSize = v
	}
	return page, pageSize
}
