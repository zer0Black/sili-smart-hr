package service_test

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/service"
)

// TestLLMEnabledProvider_NoEnabled：FindFirstEnabled 返回 ErrRecordNotFound →
// GetEnabledModel 返回 ErrLLMModelNotEnabled 哨兵（probe 据此区分 not_configured_model）。
func TestLLMEnabledProvider_NoEnabled(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: gorm.ErrRecordNotFound}
	p := service.NewLLMEnabledProvider(repo, crypto.DeriveKey("test-key"))

	_, err := p.GetEnabledModel(context.Background())
	if !errors.Is(err, service.ErrLLMModelNotEnabled) {
		t.Fatalf("want ErrLLMModelNotEnabled, got %v", err)
	}
}

// TestLLMEnabledProvider_RepoError：仓库非 NotFound 错误原样透传（不误判为未启用）。
func TestLLMEnabledProvider_RepoError(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: errors.New("db down")}
	p := service.NewLLMEnabledProvider(repo, crypto.DeriveKey("test-key"))

	_, err := p.GetEnabledModel(context.Background())
	if err == nil || errors.Is(err, service.ErrLLMModelNotEnabled) {
		t.Fatalf("want raw repo error, got %v", err)
	}
}

// TestLLMEnabledProvider_DecryptFailed：密文与密钥不匹配（模拟 AES key 轮换）→ 返回解密错误。
func TestLLMEnabledProvider_DecryptFailed(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{APIKeyCipher: "not-a-valid-cipher"}}
	p := service.NewLLMEnabledProvider(repo, crypto.DeriveKey("test-key"))

	if _, err := p.GetEnabledModel(context.Background()); err == nil {
		t.Fatal("want decrypt error, got nil")
	}
}

// TestLLMEnabledProvider_Success：真实 crypto 加密造密文，断言四字段映射
// （Provider/ModelID 直取，APIURL→BaseURL 空串透传走服务商默认，APIKey 为解密明文）。
func TestLLMEnabledProvider_Success(t *testing.T) {
	key := crypto.DeriveKey("test-key")
	cipher, err := crypto.Encrypt(key, "sk-plaintext")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		Provider:     "anthropic",
		ModelID:      "claude-sonnet-4-5",
		APIURL:       "",
		APIKeyCipher: cipher,
	}}
	p := service.NewLLMEnabledProvider(repo, key)

	mc, err := p.GetEnabledModel(context.Background())
	if err != nil {
		t.Fatalf("GetEnabledModel: %v", err)
	}
	if mc.Provider != "anthropic" || mc.ModelID != "claude-sonnet-4-5" || mc.BaseURL != "" || mc.APIKey != "sk-plaintext" {
		t.Fatalf("ModelConfig mapping mismatch: %+v", mc)
	}
}

// 编译期断言：wire 装配的生产实现满足底座接口。
var _ llm.EnabledModelProvider = service.NewLLMEnabledProvider(&fakeLLMRepo{}, nil)
