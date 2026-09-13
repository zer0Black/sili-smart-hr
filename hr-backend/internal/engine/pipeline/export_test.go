// export_test.go 黑盒测试辅助：暴露包内未导出符号（Go 惯例 export_test 出口，
// 仅 _test 参与编译，不进生产二进制）。
package pipeline

import (
	"time"

	"sili-smart-hr/backend/internal/engine/evaluator"
)

// PersonTerminal 是 personTerminal 的黑盒测试出口。
func PersonTerminal(res *evaluator.EvaluateResult) string { return personTerminal(res) }

// SetRetryBaseForTest 覆盖退避基准（测试用，默认 PersonEvalRetryBase）。
func (o *Orchestrator) SetRetryBaseForTest(d time.Duration) { o.retryBase = d }

// SetExtractWaitForTest 覆盖抽取落库等待参数（测试用，默认 ExtractWaitTimeout/ExtractPollInterval）。
func (o *Orchestrator) SetExtractWaitForTest(timeout, interval time.Duration) {
	o.extractWaitTimeout = timeout
	o.extractPollInterval = interval
}
