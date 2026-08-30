package extractor

// 系统参数键单源（specs §2.2）：黑名单与脱敏正则热调入口，装配层与迁移 seed 均引用。
const (
	ParamKeyInjectPrefixes = "extractor.inject_prefixes"
	ParamKeyRedactPatterns = "extractor.redact_patterns"
)
