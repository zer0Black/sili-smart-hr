// Package response 提供统一响应结构 {code,message,data} 与分页结构 {list,total,page,page_size}。
//
// Data 与 List 用 any 透传，类型由各 handler 在返回处具体化；
// 前端契约同构（data/list 为 unknown），消费时再断言。
package response

import (
	"sili-smart-hr/backend/internal/pkg/errcode"

	"github.com/gin-gonic/gin"
)

// Response 是全局统一响应结构。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// Page 是分页承载结构，作为 data 透传。
type Page struct {
	List     any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// OK 返回成功响应（code=0，无 data）。
func OK(c *gin.Context) {
	c.JSON(200, Response{Code: 0, Message: "ok"})
}

// OKWithData 返回携带 data 的成功响应。
func OKWithData(c *gin.Context, data any) {
	c.JSON(200, Response{Code: 0, Message: "ok", Data: data})
}

// OKWithPage 返回携带分页 data 的成功响应。
func OKWithPage(c *gin.Context, list any, total int64, page, pageSize int) {
	c.JSON(200, Response{
		Code:    0,
		Message: "ok",
		Data:    Page{List: list, Total: total, Page: page, PageSize: pageSize},
	})
}

// Fail 返回失败响应，文案取错误码默认值。
func Fail(c *gin.Context, httpStatus, code int) {
	c.JSON(httpStatus, Response{Code: code, Message: errcode.Message(code)})
}

// FailWithMsg 返回失败响应并使用自定义文案。
func FailWithMsg(c *gin.Context, httpStatus, code int, msg string) {
	c.JSON(httpStatus, Response{Code: code, Message: msg})
}
