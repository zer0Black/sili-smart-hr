package activity

import (
	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/conversationlog"
)

// 签名 Kind 枚举（specs §2.4 能力4 签名表）。
const (
	KindNormal             = "normal"
	KindAutoClient         = "auto_client"
	KindBypassOrchestrator = "bypass_orchestrator"
	KindThresholdOverskip  = "threshold_overskip"
	KindWorkTC1            = "work_tc1"
)

// evidence_note 文案键：只落枚举键不落中文文案（specs §2.4 能力4 注意事项，
// 文案变更不动数据，i18n 前端消费）。
const (
	NoteAutoClient = "evidence_note.auto_client"
	NoteBypass     = "evidence_note.bypass"
	NoteThreshold  = "evidence_note.threshold"
	NoteWorkTC1    = "evidence_note.work_tc1"
)

// successRatioLine 趋零区分线：档案 success 占比 ≥ 20% 走工作型路径。
const successRatioLine = 20

// tc1RatioLine 工作型判据线：tc=1 会话占比 ≥ 50%。
const tc1RatioLine = 50

// failedDominanceLine failed 主导排除线：failed 行占比 ≥ 50% 前置排除。
const failedDominanceLine = 50

// PopulationSignature 人群签名识别产出。
type PopulationSignature struct {
	Kind string // normal / auto_client / bypass_orchestrator / threshold_overskip / work_tc1
	Note string // 空串=normal；evidence_note.auto_client / .bypass / .threshold / .work_tc1
}

// IdentifyPopulation 人群签名识别（specs §2.4 能力4 判定次序唯一权威）：
// failed 主导排除 → work_tc1 → bypass_orchestrator → auto_client → threshold 首期保留不生效。
// sessions 空且 profiles 非空时跳过列表量门槛判据（Evaluate 独立调用退化形态）。
func IdentifyPopulation(sessions []conversationlog.SessionSummary, profiles []ProfileDigest, lowFreqThreshold int) PopulationSignature {
	total := len(profiles)
	var success, failed int
	allBypassClient := true
	for _, p := range profiles {
		switch p.Status {
		case domain.FeatureStatusSuccess:
			success++
		case domain.FeatureStatusFailed:
			failed++
		}
		if !isBypassClient(p.Client) {
			allBypassClient = false
		}
	}

	// ① failed 主导前置排除：抽取异常形态不落任何人群签名。
	if total > 0 && ratio(failed, total) >= failedDominanceLine {
		return PopulationSignature{Kind: KindNormal}
	}

	// 空档案集边界（specs 判定次序设计段）：判据分母为零按列表量单独判。
	if total == 0 {
		if len(sessions) >= lowFreqThreshold {
			return PopulationSignature{Kind: KindAutoClient, Note: NoteAutoClient}
		}
		return PopulationSignature{Kind: KindNormal}
	}

	// ② 工作型路径：success 达线先核 status 分布再定性（C09 签名重合区分）。
	successOK := ratio(success, total) >= successRatioLine
	if successOK {
		if ratio(tc1Count(sessions), len(sessions)) >= tc1RatioLine {
			return PopulationSignature{Kind: KindWorkTC1, Note: NoteWorkTC1}
		}
		return PopulationSignature{Kind: KindNormal} // 命中即止：success 达线不落趋零签名
	}

	// ③④ 档案趋零形态：列表量门槛缺席（sessions 空的退化分支）时跳过门槛判据。
	if len(sessions) > 0 && len(sessions) < lowFreqThreshold {
		return PopulationSignature{Kind: KindNormal}
	}
	if allBypassClient {
		return PopulationSignature{Kind: KindBypassOrchestrator, Note: NoteBypass}
	}
	// threshold_overskip 判据是本分支子集，按序命中即止首期恒落 auto_client（常量保留待启用）。
	return PopulationSignature{Kind: KindAutoClient, Note: NoteAutoClient}
}

// isBypassClient 旁路族四值：workbuddy/omo/unknown/空串（specs §2.4 能力4 bypass 行
// client 聚合口径：unknown 与空串视同旁路族并入，存在任一 claude_code/opencode/mixed
// 正常客户端行即不判旁路）。
func isBypassClient(client string) bool {
	switch client {
	case "workbuddy", "omo", "unknown", "":
		return true
	}
	return false
}

// tc1Count 统计列表中 TurnCount==1 的会话数（工作型判据分子）。
func tc1Count(sessions []conversationlog.SessionSummary) int {
	n := 0
	for _, s := range sessions {
		if s.TurnCount == 1 {
			n++
		}
	}
	return n
}

// ratio 百分比取整：den 为 0 时返回 0（空 sessions 的 tc=1 判据分母保护）。
func ratio(num, den int) int {
	if den == 0 {
		return 0
	}
	return num * 100 / den
}
