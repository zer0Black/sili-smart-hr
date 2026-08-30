package extractor

import (
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// 脱敏占位符（specs §2.4 能力5、§5.1 验收表）：出厂正则命中输出类型化占位符
// [PATH]/[SECRET]/[ADDR]，运维自定义正则不携带类型信息，兜底 [REDACTED]。
const (
	redactPlaceholderPath   = "[PATH]"
	redactPlaceholderSecret = "[SECRET]"
	redactPlaceholderAddr   = "[ADDR]"
	redactPlaceholderCustom = "[REDACTED]"
)

// redactRule 出厂正则到占位符的映射，顺序固定为路径→密钥→内网地址保证确定性。
type redactRule struct {
	pattern string
	re      *regexp.Regexp
	replace string
	// addrGroup > 0 时地址体独立成组，走 replaceGroupKeepDelims 组区间替换
	addrGroup int
}

// redactDefaults 出厂脱敏规则（specs §2.4 能力5），pattern 取 config.go 单源正则常量
// （守护测试 TestRedactPatternsMatchDefaults 锁定），多占位符映射（路径两类共享 [PATH]、
// 内网三段合一）供出厂回退分支与注入集类型化命中共用。包级预编译，避免逐次 Redact 重编译。
// Unix 路径与 sk- 密钥模板 ${1} 回填前导定界组；内网地址走组区间替换（见 addrGroup）。
var redactDefaults = []redactRule{
	{redactWinPathPattern, regexp.MustCompile(redactWinPathPattern), `${1}` + redactPlaceholderPath, 0},
	{redactUnixPathPattern, regexp.MustCompile(redactUnixPathPattern), `${1}` + redactPlaceholderPath, 0},
	{redactSKKeyPattern, regexp.MustCompile(redactSKKeyPattern), `${1}` + redactPlaceholderSecret, 0},
	// 组2 是地址体（组1/组3 夹边界，RE2 无 lookahead），组区间替换保相邻地址可连续命中。
	{redactIntranetPattern, regexp.MustCompile(redactIntranetPattern), redactPlaceholderAddr, 2},
}

// replaceGroupKeepDelims 只替换目标组（groupIdx）区间为占位符，首尾定界组原样保留：
// 续扫起点放在组末尾（尾定界符留在剩余文本里），相邻地址以尾定界符作前导组再次命中
// （RE2 无 lookahead，回填式替换会消费分隔符使相邻第二地址漏拦）。
func replaceGroupKeepDelims(text string, re *regexp.Regexp, groupIdx int, placeholder string) string {
	var b strings.Builder
	from := 0
	for from <= len(text) {
		loc := re.FindStringSubmatchIndex(text[from:])
		if loc == nil {
			break
		}
		gs := from + loc[2*groupIdx]
		ge := from + loc[2*groupIdx+1]
		// 组未参与本次匹配（loc 为 -1）或零宽组（gs==ge）都不产出可替换区间：
		// 前者切片越界 panic，后者不前进会死循环，统一跳过一个字节续扫。
		if gs < from || ge <= from {
			gs, ge = from, from+1
			b.WriteString(text[from:gs])
			from = ge
			continue
		}
		b.WriteString(text[from:gs])
		b.WriteString(placeholder)
		from = ge
	}
	b.WriteString(text[from:])
	return b.String()
}

// redactCacheEntry 是注入分支 pattern 编译结果的缓存条目，re 与 err 二选一，
// 编译失败也缓存（nil, err），防同一坏 pattern 逐消息反复编译报错。
type redactCacheEntry struct {
	re  *regexp.Regexp
	err error
}

// redactCompileCache 按注入 pattern 串缓存编译结果：互斥锁 + map 实现，超上限
// （运维低频调参，512 足够日常 pattern 集）整体清空重建，防包级缓存永久增长。
var (
	redactCompileCacheMu sync.Mutex
	redactCompileCache   = map[string]redactCacheEntry{}
)

const redactCompileCacheLimit = 512

// compileRedactPattern 取缓存或编译注入 pattern，编译失败记 DEBUG、含捕获组
// 记 WARN。日志挂在编译路径（缓存命中不重打），同条 pattern 生命周期内只记一次。
func compileRedactPattern(pattern string) redactCacheEntry {
	redactCompileCacheMu.Lock()
	if v, ok := redactCompileCache[pattern]; ok {
		redactCompileCacheMu.Unlock()
		return v
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		slog.Debug("redact pattern skipped", "pattern", pattern, "err", err)
	}
	if err == nil && re.NumSubexp() > 0 {
		slog.Warn("redact pattern has capture groups, delimiters may be swallowed and adjacent matches missed",
			"pattern", pattern, "groups", re.NumSubexp())
	}
	entry := redactCacheEntry{re: re, err: err}
	if len(redactCompileCache) >= redactCompileCacheLimit {
		redactCompileCache = map[string]redactCacheEntry{pattern: entry} // 超限整体重建，热点条目下次重编译即回填
	} else {
		redactCompileCache[pattern] = entry
	}
	redactCompileCacheMu.Unlock()
	return entry
}

// applyRedactRule 单条规则分派：组替换形态走组区间替换，其余走模板替换。
func applyRedactRule(text string, rule redactRule) string {
	if rule.addrGroup > 0 {
		return replaceGroupKeepDelims(text, rule.re, rule.addrGroup, rule.replace)
	}
	return rule.re.ReplaceAllString(text, rule.replace)
}

// Redact 对文本执行正则脱敏（specs §2.4 能力5）：路径/密钥/内网地址替换为类型化
// 占位符 [PATH]/[SECRET]/[ADDR]。注入集条目精确命中出厂字面量走类型化替换，其余
// 为自定义正则兜底 [REDACTED]。空集 nil 恒回退出厂（安全机制，与 inject_prefixes
// 空集放开刻意相反）；单条编译失败或空串条目跳过不中断。
func Redact(text string, patterns []string) string {
	if len(patterns) == 0 {
		for _, rule := range redactDefaults {
			text = applyRedactRule(text, rule)
		}
		return text
	}
	for _, p := range patterns {
		if p == "" {
			continue // 空正则零宽匹配会把全文逐位替换为占位符，误配条目跳过
		}
		typed := false
		for _, rule := range redactDefaults {
			if p == rule.pattern {
				text = applyRedactRule(text, rule)
				typed = true
				break
			}
		}
		if !typed {
			text = redactApply(text, p, redactPlaceholderCustom)
		}
	}
	return text
}

// redactApply 编译单条正则（结果缓存）并替换，编译失败跳过不中断。
// 含捕获组的注入正则替换为裸占位符：RE2 无 lookahead，定界组会被整段匹配吞掉
// （相邻第二目标因前导定界符被消费而漏拦），WARN 在编译路径单次记录。
func redactApply(text, pattern, replace string) string {
	entry := compileRedactPattern(pattern)
	if entry.err != nil {
		return text
	}
	return entry.re.ReplaceAllString(text, replace)
}
