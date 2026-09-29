// pagination 分页参数钳制的单一来源：page<1 归 1，pageSize 1~100（缺省值随域）。
package repository

// ClampPage 钳制分页参数（缺省 20），repo 取行与 service 组装响应共用，防两处漂移。
func ClampPage(page, pageSize int) (int, int) {
	return clampPage(page, pageSize, 20)
}

// ClampPageSize 钳制分页参数并指定缺省 pageSize（域缺省非 20 时用，如 TST 列表 10）。
func ClampPageSize(page, pageSize, defPageSize int) (int, int) {
	return clampPage(page, pageSize, defPageSize)
}

// clampPage 钳制实现：page<1 归 1，pageSize<1 归 def，>100 钳 100。
func clampPage(page, pageSize, def int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = def
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
