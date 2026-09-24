// Package errcode 集中定义业务错误码。code=0 成功，非 0 为业务错误。
package errcode

// 通用、account 域、系统初始化域、dimension 域与 config 域错误码。
// 段位：account 1001-1007，系统初始化 1101-1102，dimension 1201-1209，config 1301-1309，通用 1400/1500，
// assessment batch 1601-1603，题库管理 1701-1709+1713。
// 百位区分域：0=account，1=system，2=dimension，3=config，6=assessment batch，7=questionbank。
// dimension 刻意用 12xx 段避让 11xx，config 刻意用 13xx 段避让 11xx/12xx。
const (
	Success                          = 0
	InvalidCredentials               = 1001 // 账号不存在或密码错误
	AccountDisabled                  = 1002 // 账号被禁用（登录路径刻意不返回，反枚举）
	Unauthorized                     = 1003 // token 缺失或无效
	AccountNotFound                  = 1004 // 用户管理目标账号不存在
	UsernameExists                   = 1005 // 账号已存在（唯一性校验排除软删除）
	LastEnabledAccount               = 1006 // 至少保留一个启用的账号
	PasswordInvalid                  = 1007 // 密码格式不符
	SystemAlreadyInitialized         = 1101 // 系统已初始化（初始化提交接口一次性可用）
	EnvironmentNotReady              = 1102 // 环境就绪自检未通过（数据库或缓存连通阻断）
	DimensionNotFound                = 1201 // 维度不存在
	DimensionCodeExists              = 1202 // 维度编码已存在（唯一性冲突）
	DimensionEnabledNotDeletable     = 1203 // 启用态维度不可删除
	DimensionVersionConflict         = 1204 // 维度版本号冲突（并发更新）
	DimensionNameInvalid             = 1205 // 维度名称非法
	DimensionAnchorRequired          = 1206 // 行为锚点必填
	DimensionPromptRequired          = 1207 // 评分提示词必填
	ActivityThresholdInvalid         = 1208 // 活跃度阈值非法
	DimensionCodeUnavailable         = 1209 // 维度编码不可用（同名去重耗尽或追加序号后超长）
	LLMConfigNotFound                = 1301 // 大模型不存在
	LastLLMConfig                    = 1302 // 至少保留一个大模型
	IntegrationSecretNotConfigured   = 1303 // 集成密钥未配置
	IntegrationSecretTestFailed      = 1304 // 连通验证失败
	StaffListUnavailable             = 1305 // 人员列表暂不可用（外部用户体系不可达）
	ConfigVersionConflict            = 1306 // 评估周期配置版本冲突（并发更新）
	SecretDecryptFailed              = 1307 // 密文不可读（AES key 轮换或密文损坏，需重新输入密钥）
	LLMConfigVersionConflict         = 1308 // 大模型配置版本冲突（并发更新）
	IntegrationSecretVersionConflict = 1309 // 集成密钥版本冲突（并发更新）
	BadRequest                       = 1400 // 请求参数错误
	Internal                         = 1500 // 服务内部错误
	// assessment batch 域 1601-1603（specs P2_ASM_001 03 §5 错误码表）。
	BatchNotFound      = 1601 // 批次不存在
	BatchPeriodInvalid = 1602 // 评估时段非法
	BatchTargetInvalid = 1603 // 评估对象非法
	// 题库管理 17xx 段（03 §4.1 全量）：1710-1712 段内预留。
	QuestionNotFound        = 1701 // 题目不存在（含已软删除）
	QuestionBatchNotFound   = 1702 // 批次不存在
	GenerationNotFound      = 1703 // 生成会话不存在
	ScaleAlreadyImported    = 1704 // 量表已引入（specs 规则 8）
	QuestionReferenced      = 1705 // 题目已被测试引用，只可停用
	QuestionBatchClosed     = 1706 // 批次已关闭或已作废
	QuestionNotEditable     = 1707 // 题目不可编辑（量表题或状态不符）
	QuestionStatusInvalid   = 1708 // 题目状态转换前置校验失败
	LLMNotConfigured        = 1709 // 大模型未配置（无排他启用模型）
	QuestionVersionConflict = 1713 // 题目乐观锁版本冲突（并发变更）
)

var messages = map[int]string{
	Success:                          "ok",
	InvalidCredentials:               "invalid credentials",
	AccountDisabled:                  "account disabled",
	Unauthorized:                     "unauthorized",
	AccountNotFound:                  "account not found",
	UsernameExists:                   "username exists",
	LastEnabledAccount:               "last enabled account",
	PasswordInvalid:                  "password invalid",
	SystemAlreadyInitialized:         "system already initialized",
	EnvironmentNotReady:              "environment not ready",
	DimensionNotFound:                "dimension not found",
	DimensionCodeExists:              "dimension code exists",
	DimensionEnabledNotDeletable:     "dimension enabled not deletable",
	DimensionVersionConflict:         "dimension version conflict",
	DimensionNameInvalid:             "dimension name invalid",
	DimensionAnchorRequired:          "dimension anchor required",
	DimensionPromptRequired:          "dimension prompt required",
	ActivityThresholdInvalid:         "activity threshold invalid",
	DimensionCodeUnavailable:         "dimension code unavailable",
	LLMConfigNotFound:                "llm config not found",
	LastLLMConfig:                    "last llm config",
	IntegrationSecretNotConfigured:   "integration secret not configured",
	IntegrationSecretTestFailed:      "integration secret test failed",
	StaffListUnavailable:             "staff list unavailable",
	ConfigVersionConflict:            "config version conflict",
	SecretDecryptFailed:              "secret decrypt failed",
	LLMConfigVersionConflict:         "llm config version conflict",
	IntegrationSecretVersionConflict: "integration secret version conflict",
	BadRequest:                       "bad request",
	Internal:                         "internal error",
	BatchNotFound:                    "batch not found",
	BatchPeriodInvalid:               "batch period invalid",
	BatchTargetInvalid:               "batch target invalid",
	QuestionNotFound:                 "question not found",
	QuestionBatchNotFound:            "question batch not found",
	GenerationNotFound:               "generation not found",
	ScaleAlreadyImported:             "scale already imported",
	QuestionReferenced:               "question referenced",
	QuestionBatchClosed:              "question batch closed",
	QuestionNotEditable:              "question not editable",
	QuestionStatusInvalid:            "question status invalid",
	LLMNotConfigured:                 "llm not configured",
	QuestionVersionConflict:          "question version conflict",
}

// Message 返回错误码对应文案，未注册返回 "error"。
func Message(code int) string {
	if msg, ok := messages[code]; ok {
		return msg
	}
	return "error"
}
