// Package likeescape 提供 LIKE 通配符转义。上提自 model 包：repository 曾经
// import model 仅为这一个函数，构成 model→repository 的反向依赖边（与
// model→extractor→repository 叠加成环），断在 pkg 层后 model 可自由依赖 extractor。
package likeescape

import "strings"

// EscapeLike 转义用户输入里的 LIKE 通配符（%、_、\），配合 ESCAPE '\' 字面匹配。
// 顺序敏感：先转义反斜杠自身，再转义 % 与 _，避免二次替换。调用方必须在 LIKE 后写 ESCAPE '\'。
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
