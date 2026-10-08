package service

import (
	"context"
	"fmt"

	"sili-smart-hr/backend/internal/domain"
)

// fmtChange 组装单项变更对比：值字符串化（bool/数字统一 fmt 转字符串，03 §1.7），
// nil 输出空串兜底新增/删除类；密钥类字段由调用方先转脱敏掩码再传入（§5.1.4 规则4）。
func fmtChange(field string, before, after any) domain.ChangeItem {
	return domain.ChangeItem{
		Field:  field,
		Before: fmtValue(before),
		After:  fmtValue(after),
	}
}

func fmtValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// injectOperation 注入变更对比形态埋点（关键域更新/启停类，§4.1.4 规则3），
// 不碰 detail（对比表与文本段落互斥渲染）。
func injectOperation(ctx context.Context, module, target, summary string, changes []domain.ChangeItem) {
	s := SinkFromContext(ctx)
	if s == nil {
		return
	}
	s.SetModule(module)
	s.SetTarget(target)
	s.SetSummary(summary)
	s.SetChanges(changes)
}

// injectDetail 注入文本详情形态埋点（新增/删除类与其余域，§4.1.4 规则3），
// 不碰 changes。
func injectDetail(ctx context.Context, module, target, summary, detail string) {
	s := SinkFromContext(ctx)
	if s == nil {
		return
	}
	s.SetModule(module)
	s.SetTarget(target)
	s.SetSummary(summary)
	s.SetDetail(detail)
}
