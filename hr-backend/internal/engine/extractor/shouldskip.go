package extractor

import (
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// SkipReason 值域（specs §2.3 错误码表）。SkipSessionNotFound 是业务空终态化的
// 补充 reason：上游已删除会话的残留 failed 行终态化为 skipped 时使用（评审裁定）。
const (
	SkipMinUserMessages = "min_user_messages"
	SkipEmptyShell      = "empty_shell"
	SkipDetailInvalid   = "detail_invalid"
	SkipSessionNotFound = "session_not_found"
)

// detailInvalid 是规则0 谓词：nil、缺会话标识或空消息（§2.3 ErrDetailInvalid）。
func detailInvalid(detail *conversationlog.SessionDetail) bool {
	return detail == nil || detail.Session.SessionKey == "" || len(detail.Messages) == 0
}

// ShouldSkip 纯规则判定，不调 LLM 不落库（specs §2.4 能力4）。
// 返回 (true, reason) 表示跳过，reason 取 min_user_messages/empty_shell/detail_invalid。
// 判定收敛在 prepareAndJudge 单一入口（与 Extract 同链防漂移），阈值入参供测试
// 锚定空区间分支（当前 MinUserMessages=1 时规则2 区间为空）。
func (e *Extractor) ShouldSkip(detail *conversationlog.SessionDetail, minUser, minKept int) (bool, string) {
	_, skip, reason := e.prepareAndJudge("", detail, minUser, minKept)
	return skip, reason
}

// shouldSkipPrepared 对 prepare 共享中间结构执行规则1-4，Extract 成功路径与
// ShouldSkip 独立调用消费同一形态，防两套判定漂移。
// turn_count 不参与任何判定（§2.4 能力4：请求边界语义无残缺判定力）。
func (e *Extractor) shouldSkipPrepared(p preparedData, minUser, minKept int) (bool, string) {
	switch {
	case p.profile.UserMsgCount >= minUser:
		// 规则1 放行，落入零响应统一复核。
	case p.profile.UserMsgCount > 0:
		// 规则2：0 < UserMsgCount < minUser（仅阈值调高后激活）。
		return true, SkipMinUserMessages
	case p.keptBeforeTruncation >= minKept:
		// 规则3 放行：零输入但保留类证据充足（截断前口径，与规则4 的截断不翻转
		// 证据同一裁决：预算截断是视图控制手段，不削减零输入会话的证据判定）。
	default:
		// 规则3 跳过：零输入且保留类不足 → 空壳。
		return true, SkipEmptyShell
	}

	// 规则4 零响应判定（两个放行组统一复核）：结构化信号按条目存在性取值，
	// [EVENT] 与用户行不构成工作证据；截断丢弃不翻转存在性（裁决见 buildView 注释）。
	if !p.hasAIResponse && !p.hasToolLines {
		return true, SkipEmptyShell
	}
	return false, ""
}
