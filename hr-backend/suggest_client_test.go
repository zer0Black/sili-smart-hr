package app

// suggest_client_test.go 建议生成专用 LLM client 装配契约测试（specs P2_TMD_001
// §5.1.4 规则5、03 §4.4）：Timeout 180s 小于任务级 240s 保重试边界自洽。
// llm.Client 只暴露 StreamChat，Timeout 不在接口面，180s 经包级导出常量
// SuggestGenLLMClientTimeout 锚定，client 可用性经解析失败 fake 快速路径验证。

import (
	"context"
	"testing"

	"sili-smart-hr/backend/internal/integration/llm"
)

// fakeSuggestModelProvider 启用模型解析失败 fake：调用路径不触真实网络。
type fakeSuggestModelProvider struct{}

func (f *fakeSuggestModelProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	return llm.ModelConfig{}, llm.ErrProviderUnavailable
}

var _ llm.EnabledModelProvider = (*fakeSuggestModelProvider)(nil)

// TestSuggestGenLLMClientTimeout 核心锚点：client Timeout 常量精确 180s
//（03 §4.4 表），且小于任务级超时 240s（worker/task.suggestGenerateTimeout，
// task 包内已单点断言）保重试边界自洽。
func TestSuggestGenLLMClientTimeout(t *testing.T) {
	if SuggestGenLLMClientTimeout != 180e9 { // 180 * time.Second，直接数值防 import 循环
		t.Errorf("SuggestGenLLMClientTimeout = %v, want 180s", SuggestGenLLMClientTimeout)
	}
	client := NewSuggestGenLLMClient(&fakeSuggestModelProvider{})
	if client == nil {
		t.Fatal("NewSuggestGenLLMClient 应返回非 nil client")
	}
	// 解析失败 fake 下快速返错（不触超时与网络路径），证明 client 组装可用。
	if _, err := client.StreamChat(context.Background(), llm.ChatRequest{}); err == nil {
		t.Fatal("解析失败 fake 下 StreamChat 应返回错误")
	}
}
