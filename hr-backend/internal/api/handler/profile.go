// profile 个人画像域 HTTP 处理器：A1 列表 / A2 导出 / B1 详情（specs P2_PRF_001）。
//
// 业务规则：
//   - BR1 本域全 GET，三接口均为查询语义。
//   - BR2 导出成功直写 xlsx 二进制流旁路统一响应结构；失败返回统一 JSON 错误结构。
//   - BR3 三接口挂 JWT 鉴权 auth 组，未带 token 由中间件统一 401+1003。
package handler

import (
	"net/url"
	"strconv"

	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/response"
	"sili-smart-hr/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// xlsxMIME 是 xlsx 文件的 MIME 类型。
const xlsxMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// ProfileHandler 个人画像域 HTTP 处理器（03 A1/A2/B1，全 GET）。
type ProfileHandler struct {
	svc service.ProfileService
}

// NewProfileHandler 构造个人画像域处理器。
func NewProfileHandler(svc service.ProfileService) *ProfileHandler {
	return &ProfileHandler{svc: svc}
}

// List 处理 GET /api/profiles：query 绑定 name/activity_level/dimension_code/unused_only/page/page_size。
// unused_only 非法布尔 → 1400；page/page_size 非法用默认值 1/10（钳位归 service）。
func (h *ProfileHandler) List(c *gin.Context) {
	f, ok := parseProfileFilter(c)
	if !ok {
		return
	}
	page, pageSize := parseProfilePaging(c)
	f.Page, f.PageSize = page, pageSize
	res, err := h.svc.List(c.Request.Context(), f)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, res)
}

// Export 处理 GET /api/profiles/export：成功直写 xlsx 二进制流（Content-Type xlsx
// MIME + Content-Disposition attachment，RFC 5987 filename* 承载中文名）；
// 失败按 Content-Type 判别返回统一 JSON 错误结构（handleServiceError）。
func (h *ProfileHandler) Export(c *gin.Context) {
	f, ok := parseProfileFilter(c)
	if !ok {
		return
	}
	data, filename, err := h.svc.Export(c.Request.Context(), f)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	disposition := "attachment; filename=\"profiles.xlsx\"; filename*=UTF-8''" + url.PathEscape(filename)
	c.Header("Content-Type", xlsxMIME)
	c.Header("Content-Disposition", disposition)
	c.Data(200, xlsxMIME, data)
}

// Detail 处理 GET /api/profiles/detail：query 绑定 staff_name/period_start/period_end。
// 参数校验（缺失/只传一端/日期格式）归 service 层 1400，handler 原样透传。
func (h *ProfileHandler) Detail(c *gin.Context) {
	dto, err := h.svc.Detail(c.Request.Context(), c.Query("staff_name"), c.Query("period_start"), c.Query("period_end"))
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OKWithData(c, dto)
}

// parseProfileFilter 解析 A1/A2 共用筛选 query（03 A1）：unused_only 非法布尔 1400，
// name/activity_level/dimension_code 原样透传（枚举校验归 service）。
func parseProfileFilter(c *gin.Context) (service.ProfileFilter, bool) {
	var f service.ProfileFilter
	if raw := c.Query("unused_only"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			handleServiceError(c, service.NewError(errcode.BadRequest))
			return f, false
		}
		f.UnusedOnly = v
	}
	f.Name = c.Query("name")
	f.ActivityLevel = c.Query("activity_level")
	f.DimensionCode = c.Query("dimension_code")
	return f, true
}

// parseProfilePaging 解析分页 query：缺失或非法兜底 1/10，
// 越界钳位归 service（1-100 接受、>100 钳 100）。
func parseProfilePaging(c *gin.Context) (page, pageSize int) {
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
