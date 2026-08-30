package extractor

// 指纹类型常量（specs §2.2 SpecFingerprint.Kind：白名单三值，无 other 桶）。
const (
	FPClaudeMD = "claude_md"
	FPAgentsMD = "agents_md"
	FPSkillDoc = "skill_doc"
)

// SpecFingerprint 是规约类注入的轻量指纹（specs §2.2）。指纹计算收敛在 fingerprint.go
// 的 extractFingerprint 单点实现，类型定义留驻本文件（计划文件结构安排）。
type SpecFingerprint struct {
	Kind        string   // claude_md / agents_md / skill_doc
	CharCount   int      // 规约正文字符数（剥离 currentDate 后）
	Hash        string   // 规约正文 SHA-256 前 16 位（剥离 currentDate 后）
	SectionList []string // 规约正文 markdown 章节标题（已脱敏）
}

// FeatureProfile 四块档案（specs §2.2）：Stats 规则产出，Summary/Instruction/Behavior
// 由 LLM 按 schema 产出，合并为完整档案 TEXT 落库。
type FeatureProfile struct {
	Stats       ProfileStats
	Summary     string
	Instruction []InstructionSeg
	Behavior    ProfileBehavior
}

// InstructionSeg 用户指令片段（LLM 产出，已脱敏）。
type InstructionSeg struct {
	Text string `json:"text"`
}

// ProfileBehavior 行为特征块（LLM 产出，schema 见 prompt 模板）。
type ProfileBehavior struct {
	InstructionSpecificity string `json:"instruction_specificity"` // high/medium/low/no_evidence
	InterruptStyle         string `json:"interrupt_style"`         // frequent/occasional/rare/no_evidence
	ReviewRatio            string `json:"review_ratio"`            // high/medium/low/no_evidence
	PasteScale             string `json:"paste_scale"`             // heavy/moderate/light/none（数值引用 PasteCharCount 统一口径）
	NarrativeAbsent        bool   `json:"narrative_absent"`        // 零叙述会话标记
}
