package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// IntegrationSecretDTO 是集成密钥卡片的对外视图，暴露掩码、配置状态与乐观锁 version
// （specs §4.2.2C 字段、§4.2.4 规则4 掩码化、§6.1 配置状态推导）。Version 透传前端作下次更新的乐观锁凭证。
type IntegrationSecretDTO struct {
	ID           int64  `json:"id,string"`
	SecretMasked string `json:"secret_masked"`
	Configured   bool   `json:"configured"`
	Version      int    `json:"version"`
}

// IntegrationSecretDetailDTO 承载临时查看的明文密钥（specs §4.4 临时查看、§4.2.4 规则5）。
type IntegrationSecretDetailDTO struct {
	ID     int64  `json:"id,string"`
	Secret string `json:"secret"`
}

// UpdateSecretResult 是 Update 的返回，configured 恒 true（更新即覆盖，specs §4.5.4 规则1），
// Version 回写乐观锁成功后的新版本号供前端下次更新携带。
type UpdateSecretResult struct {
	ID         int64 `json:"id,string"`
	Configured bool  `json:"configured"`
	Version    int   `json:"version"`
}

// SecretTestResult 承载连通验证结果（specs §4.2.4 规则5、03 §C4）。
type SecretTestResult struct {
	Connected bool `json:"connected"`
}

// ConversationlogPinger 由 integration/conversationlog.Client 实现，
// service 仅依赖探活能力，便于测试注入 fake（03 §C4、T4 由 wire.Bind 绑定）。
type ConversationlogPinger interface {
	Ping(ctx context.Context, secret string) error
}

// IntegrationSecretService 是集成密钥域业务接口。
type IntegrationSecretService interface {
	Get(ctx context.Context) (*IntegrationSecretDTO, error)
	Detail(ctx context.Context) (*IntegrationSecretDetailDTO, error)
	Update(ctx context.Context, version int, secretCipher, keyID string) (*UpdateSecretResult, error)
	Test(ctx context.Context) (*SecretTestResult, error)
}

type integrationSecretService struct {
	repo    repository.IntegrationSecretRepository
	dec     PasswordDecryptor
	encKey  []byte
	convLog ConversationlogPinger
}

// NewIntegrationSecretService 注入 repo、RSA 解密器、AES 对称密钥与会话日志探活客户端。
func NewIntegrationSecretService(repo repository.IntegrationSecretRepository, dec PasswordDecryptor, encKey []byte, convLog ConversationlogPinger) IntegrationSecretService {
	return &integrationSecretService{repo: repo, dec: dec, encKey: encKey, convLog: convLog}
}

// ErrIntegrationSecretNotConfigured 集成密钥未配置的哨兵错误，
// ProbeIntegration 用 errors.Is 据此区分 not_configured_key 与 unreachable。
var ErrIntegrationSecretNotConfigured = errors.New("integration secret not configured")

// ResolveIntegrationSecret 是集成密钥明文解析的单一收敛点：repo 取密文 → cipher 空判 →
// AES 解密。Detail/Test/assessment_config.resolveSecret 经 decryptSecretCipher 复用解密段，
// 装配层（extractor SecretProvider）与 dependency_probe 走本函数拿明文；未配置返回哨兵
// ErrIntegrationSecretNotConfigured（供 errors.Is 判定），其余为包装错误。
func ResolveIntegrationSecret(ctx context.Context, repo repository.IntegrationSecretRepository, encKey []byte) (string, error) {
	secret, err := repo.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("get integration secret: %w", err)
	}
	if secret.SecretCipher == "" {
		return "", ErrIntegrationSecretNotConfigured
	}
	plaintext, derr := crypto.Decrypt(encKey, secret.SecretCipher)
	if derr != nil {
		return "", fmt.Errorf("decrypt integration secret: %w", derr)
	}
	return plaintext, nil
}

// decryptSecretCipher 解密 AES-GCM 密文得明文：cipher 空 → 1303（未配置），crypto.Decrypt 失败 → 1307
// （AES key 轮换或密文损坏，提示经 Update 重新输入）。Detail/Test 与 assessment_config.resolveSecret 共用，
// 各调用方保留自己的错误码映射语义（resolveSecret 统一降级 1305，Detail/Test 精确区分 1303/1307）。
func decryptSecretCipher(encKey []byte, cipher string) (string, *Error) {
	if cipher == "" {
		return "", NewError(errcode.IntegrationSecretNotConfigured)
	}
	plaintext, derr := crypto.Decrypt(encKey, cipher)
	if derr != nil {
		return "", NewErrorWithMsg(errcode.SecretDecryptFailed, "secret decrypt failed, please re-enter via update")
	}
	return plaintext, nil
}

// Get 返掩码化 DTO，Configured 由 SecretCipher 是否非空推导（specs §6.1、04 §3.3.1 BR1）。
func (s *integrationSecretService) Get(ctx context.Context) (*IntegrationSecretDTO, error) {
	secret, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get integration secret: %w", err)
	}
	return &IntegrationSecretDTO{
		ID:           secret.ID,
		SecretMasked: secret.SecretMasked,
		Configured:   secret.SecretCipher != "",
		Version:      secret.Version,
	}, nil
}

// Detail 解密密钥明文返回；未配置（cipher 空）前置拦截返 1303（specs §4.2.4 规则5、03 §C2/C4、BR3）。
func (s *integrationSecretService) Detail(ctx context.Context) (*IntegrationSecretDetailDTO, error) {
	secret, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get integration secret: %w", err)
	}
	plaintext, derr := decryptSecretCipher(s.encKey, secret.SecretCipher)
	if derr != nil {
		return nil, derr
	}
	return &IntegrationSecretDetailDTO{
		ID:     secret.ID,
		Secret: plaintext,
	}, nil
}

// Update 校验密文、RSA 解密、长度限制，AES 加密落库 + 生成掩码快照，覆盖旧密钥（specs §4.5.4 规则1、§4.5.2、BR5/BR6）。
// version 由客户端回传（从卡片 DTO 取得），作乐观锁 WHERE 条件，覆盖 repo.Get 读回的当前值，
// 以此检测 stale-form：他人已改过（version 自增）时 WHERE 不命中 → affected==0 → 1309，而非静默覆盖（llm_config 域同模式）。
func (s *integrationSecretService) Update(ctx context.Context, version int, secretCipher, keyID string) (*UpdateSecretResult, error) {
	if secretCipher == "" {
		return nil, NewErrorWithMsg(errcode.BadRequest, "secret is required")
	}
	plaintext, derr := s.dec.Decrypt(ctx, keyID, secretCipher)
	if derr != nil {
		return nil, NewErrorWithMsg(errcode.BadRequest, "secret decrypt failed")
	}
	if utf8.RuneCountInString(plaintext) > 200 {
		return nil, NewErrorWithMsg(errcode.BadRequest, "secret too long")
	}

	cipher, cerr := crypto.Encrypt(s.encKey, plaintext)
	if cerr != nil {
		return nil, fmt.Errorf("encrypt integration secret: %w", cerr)
	}
	masked := crypto.Mask(plaintext)

	secret, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get integration secret for update: %w", err)
	}
	// 用客户端回传的 version 作乐观锁凭证，覆盖 repo.Get 读回的当前值；repo 层 SQL 的 version+1 自增，
	// affected==0 即并发冲突（旧快照被覆盖）返 1309。
	secret.SecretCipher = cipher
	secret.SecretMasked = masked
	secret.Version = version
	affected, err := s.repo.Update(ctx, secret)
	if err != nil {
		return nil, fmt.Errorf("update integration secret: %w", err)
	}
	if affected == 0 {
		return nil, NewError(errcode.IntegrationSecretVersionConflict)
	}
	return &UpdateSecretResult{ID: secret.ID, Configured: true, Version: version + 1}, nil
}

// Test 连通验证：未配置前置拦截返 1303；解密明文后调 convLog.Ping，
// nil → Connected:true；error → 1304 且 Msg 动态携带失败原因（specs §4.2.4 规则5、03 §C4、BR3/BR4）。
func (s *integrationSecretService) Test(ctx context.Context) (*SecretTestResult, error) {
	secret, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get integration secret: %w", err)
	}
	plaintext, derr := decryptSecretCipher(s.encKey, secret.SecretCipher)
	if derr != nil {
		return nil, derr
	}
	if perr := s.convLog.Ping(ctx, plaintext); perr != nil {
		// 网络类、父 ctx 取消、baseURL 非法三类错误链均携带完整上游 URL（specs 330 白名单外），
		// 落日志、前端只给通用文案；401/403 等短文案保留透传。
		slog.Error("integration secret test failed", "err", perr)
		msg := perr.Error()
		if errors.Is(perr, conversationlog.ErrNetwork) ||
			errors.Is(perr, conversationlog.ErrNotConfigured) ||
			errors.Is(perr, context.Canceled) ||
			errors.Is(perr, context.DeadlineExceeded) {
			msg = "无法连通上游会话日志服务，请检查网络与上游服务状态"
		}
		return nil, &Error{Code: errcode.IntegrationSecretTestFailed, Msg: msg}
	}
	return &SecretTestResult{Connected: true}, nil
}
