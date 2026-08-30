package service

import (
	"context"
	"errors"
	"fmt"

	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// ErrLLMModelNotEnabled 无启用模型的哨兵错误，realDependencyProbe 据此区分 not_configured_model
// 与探测失败（specs 03 §3.4 状态枚举）。
var ErrLLMModelNotEnabled = errors.New("llm: no enabled model")

// llmEnabledProvider 把大模型配置域桥接为 llm 底座的 EnabledModelProvider（specs §5.1）：
// 读启用配置、AES 解密 API key、映射 ModelConfig。每次调用现查库，模型切换即时生效，
// 适配器缓存按配置变更自动重建，无需本层做缓存失效。
type llmEnabledProvider struct {
	repo   repository.LLMConfigRepository
	encKey []byte
}

var _ llm.EnabledModelProvider = (*llmEnabledProvider)(nil)

// NewLLMEnabledProvider 注入大模型配置仓库与 AES 对称密钥。
func NewLLMEnabledProvider(repo repository.LLMConfigRepository, encKey []byte) llm.EnabledModelProvider {
	return &llmEnabledProvider{repo: repo, encKey: encKey}
}

// GetEnabledModel 返回当前排他启用模型的调用参数。
// APIURL 空串语义与两侧对齐：domain 允许空表默认地址，adapter 空 BaseURL 走服务商默认。
func (p *llmEnabledProvider) GetEnabledModel(ctx context.Context) (llm.ModelConfig, error) {
	cfg, err := p.repo.FindFirstEnabled(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return llm.ModelConfig{}, ErrLLMModelNotEnabled
		}
		return llm.ModelConfig{}, fmt.Errorf("find enabled llm config: %w", err)
	}
	apiKey, derr := crypto.Decrypt(p.encKey, cfg.APIKeyCipher)
	if derr != nil {
		// AES key 轮换或密文损坏，提示经 Update 重新录入密钥
		return llm.ModelConfig{}, fmt.Errorf("decrypt llm api key: %w", derr)
	}
	return llm.ModelConfig{
		Provider: cfg.Provider,
		ModelID:  cfg.ModelID,
		BaseURL:  cfg.APIURL,
		APIKey:   apiKey,
	}, nil
}
