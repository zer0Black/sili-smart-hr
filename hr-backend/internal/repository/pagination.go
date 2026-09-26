// pagination 分页参数钳制的单一来源：page<1 归 1，pageSize 1~100（缺省 20）。
package repository

// ClampPage 钳制分页参数，repo 取行与 service 组装响应共用，防两处漂移。
func ClampPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
