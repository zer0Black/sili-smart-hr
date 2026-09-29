package app

// grading_client_test.go 阅卷专用 LLM client 装配契约测试（specs P2_TST_001
// §5.2.4 规则3、03 §4.5）：Timeout 240s 小于任务级 300s 保重试边界自洽。
// llm.Client 只暴露 StreamChat，Timeout 不在接口面，240s 经包级常量
// GradingLLMClientTimeout 锚定，client 可用性经解析失败 fake 快速路径验证。

import (
	"context"
	"testing"

	"sili-smart-hr/backend/internal/integration/llm"
)

// fakeGradingModelProvider 启用模型解析失败 fake：调用路径不触真实网络。
type fakeGradingModelProvider struct{}

func (f *fakeGradingModelProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	return llm.ModelConfig{}, llm.ErrProviderUnavailable
}

var _ llm.EnabledModelProvider = (*fakeGradingModelProvider)(nil)

// TestGradingLLMClientTimeout 核心锚点（BR2）：client Timeout 常量精确 240s
//（specs §5.2.4 规则3 初值，03 §4.5 表），且小于任务级超时 300s
//（worker/task.testGradeTimeout，task 包内已单点断言）保重试边界自洽。
func TestGradingLLMClientTimeout(t *testing.T) {
	if GradingLLMClientTimeout != 240e9 { // 240 * time.Second，直接数值防 import 循环
		t.Errorf("GradingLLMClientTimeout = %v, want 240s", GradingLLMClientTimeout)
	}
	client := NewGradingLLMClient(&fakeGradingModelProvider{})
	if client == nil {
		t.Fatal("NewGradingLLMClient 应返回非 nil client")
	}
	// 解析失败 fake 下快速返错（不触超时与网络路径），证明 client 组装可用。
	if _, err := client.StreamChat(context.Background(), llm.ChatRequest{}); err == nil {
		t.Fatal("解析失败 fake 下 StreamChat 应返回错误")
	}
}
