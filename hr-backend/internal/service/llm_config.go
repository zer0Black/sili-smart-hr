package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"

	"gorm.io/gorm"
)

// LLMConfigDTO 是大模型配置列表项，仅暴露掩码与基础属性（specs §4.2.4 规则4 最小暴露）。
// Version 透传给前端作下次更新的乐观锁凭证（dimension 域同模式）。
type LLMConfigDTO struct {
	ID           int64  `json:"id,string"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ModelID      string `json:"model_id"`
	APIURL       string `json:"api_url"`
	APIKeyMasked string `json:"api_key_masked"`
	Enabled      bool   `json:"enabled"`
	Version      int    `json:"version"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// LLMConfigDetailDTO 承载详情接口的明文 API Key（specs §4.4 临时查看）。
// Version 透传乐观锁凭证。
type LLMConfigDetailDTO struct {
	ID        int64  `json:"id,string"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	ModelID   string `json:"model_id"`
	APIURL    string `json:"api_url"`
	APIKey    string `json:"api_key"`
	Enabled   bool   `json:"enabled"`
	Version   int    `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// CreateLLMResult 是 Create/Update 的返回，回写雪花 ID 与乐观锁 version（id string 化规避 JS 精度坑）。
type CreateLLMResult struct {
	ID      int64 `json:"id,string"`
	Version int   `json:"version"`
}

// DeleteLLMResult 承载删除结果，transferred_enabled_id 为删启用态转启的剩余模型 id，
// 未触发转启（删非启用态或仅剩一个被拦截）时为 nil 序列化为 JSON null（specs §4.2.4 规则3、03 §B4）。
type DeleteLLMResult struct {
	ID                   int64  `json:"id,string"`
	TransferredEnabledID *int64 `json:"transferred_enabled_id,string"`
}

// LLMEnableResult 承载排他启用结果，id 雪花 string 化，enabled 恒 true（03 §B5）。
type LLMEnableResult struct {
	ID      int64 `json:"id,string"`
	Enabled bool  `json:"enabled"`
}

// LLMConfigService 是大模型配置域业务接口。
type LLMConfigService interface {
	List(ctx context.Context, keyword string) ([]LLMConfigDTO, error)
	Detail(ctx context.Context, id int64) (*LLMConfigDetailDTO, error)
	Create(ctx context.Context, name, provider, modelID, apiURL, apiKeyCipher, keyID string) (*CreateLLMResult, error)
	Update(ctx context.Context, id int64, version int, name, provider, modelID, apiURL string, apiKeyCipher, keyID string, hasAPIKey bool) (*CreateLLMResult, error)
	Delete(ctx context.Context, id int64) (*DeleteLLMResult, error)
	Enable(ctx context.Context, id int64) (*LLMEnableResult, error)
}

// validProviders 是服务商枚举（specs §4.3.2、§4.2.4 规则7）。
var validProviders = map[string]bool{
	"deepseek": true,
	"openai":   true,
	"zhipu":    true,
	"anthropic": true,
}

type llmConfigService struct {
	repo    repository.LLMConfigRepository
	dec     PasswordDecryptor
	encKey  []byte
}

// NewLLMConfigService 注入 repo、RSA 解密器与 AES 对称密钥。
func NewLLMConfigService(repo repository.LLMConfigRepository, dec PasswordDecryptor, encKey []byte) LLMConfigService {
	return &llmConfigService{repo: repo, dec: dec, encKey: encKey}
}

// List 全量返回掩码化 DTO，时间格式化为 RFC3339 字符串。
func (s *llmConfigService) List(ctx context.Context, keyword string) ([]LLMConfigDTO, error) {
	list, err := s.repo.List(ctx, keyword)
	if err != nil {
		return nil, fmt.Errorf("list llm configs: %w", err)
	}
	dtos := make([]LLMConfigDTO, 0, len(list))
	for i := range list {
		dtos = append(dtos, toLLMConfigDTO(&list[i]))
	}
	return dtos, nil
}

// Detail 解密密钥明文返回（specs §4.4 临时查看）。
func (s *llmConfigService) Detail(ctx context.Context, id int64) (*LLMConfigDetailDTO, error) {
	cfg, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.LLMConfigNotFound)
		}
		return nil, fmt.Errorf("find llm config by id: %w", err)
	}
	plaintext, derr := crypto.Decrypt(s.encKey, cfg.APIKeyCipher)
	if derr != nil {
		// 密文不可读（AES key 轮换或密文损坏）映射为业务码 1307，提示经 Update 重新输入密钥，而非通用 500。
		return nil, NewErrorWithMsg(errcode.SecretDecryptFailed, "api key decrypt failed, please re-enter via update")
	}
	return &LLMConfigDetailDTO{
		ID:        cfg.ID,
		Name:      cfg.Name,
		Provider:  cfg.Provider,
		ModelID:   cfg.ModelID,
		APIURL:    cfg.APIURL,
		APIKey:    plaintext,
		Enabled:   cfg.Enabled,
		Version:   cfg.Version,
		CreatedAt: cfg.CreatedAt.Format(time.RFC3339),
		UpdatedAt: cfg.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// Create 校验参数，解密 RSA 密文得到明文，AES 加密落库，首条记录自动启用（specs §4.3.4 规则1）。
func (s *llmConfigService) Create(ctx context.Context, name, provider, modelID, apiURL, apiKeyCipher, keyID string) (*CreateLLMResult, error) {
	if msg := validateLLMFields(name, provider, modelID, apiURL); msg != "" {
		return nil, NewErrorWithMsg(errcode.BadRequest, msg)
	}
	if apiKeyCipher == "" {
		return nil, NewErrorWithMsg(errcode.BadRequest, "api_key is required")
	}
	plaintext, derr := s.dec.Decrypt(ctx, keyID, apiKeyCipher)
	if derr != nil {
		return nil, NewErrorWithMsg(errcode.BadRequest, "api_key decrypt failed")
	}
	if utf8.RuneCountInString(plaintext) > 200 {
		return nil, NewErrorWithMsg(errcode.BadRequest, "api_key too long")
	}

	cipher, err := crypto.Encrypt(s.encKey, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt llm api key: %w", err)
	}
	masked := crypto.Mask(plaintext)

	cfg := &domain.LLMConfig{
		Name:         name,
		Provider:     provider,
		ModelID:      modelID,
		APIURL:       apiURL,
		APIKeyCipher: cipher,
		APIKeyMasked: masked,
		Version:      1, // 乐观锁初值
	}
	// Enabled 与排他启用交由 repo.CreateExclusiveFirst 事务内按「全表是否首条」决定，消除并发首条竞态。
	if err := s.repo.CreateExclusiveFirst(ctx, cfg); err != nil {
		return nil, fmt.Errorf("create llm config: %w", err)
	}
	return &CreateLLMResult{ID: cfg.ID, Version: cfg.Version}, nil
}

// Update 校验字段，存在性先查；hasAPIKey=false 保留原 cipher/masked，true 则重新加密（specs §4.3.4 规则2）。
// version 由客户端回传（从列表/详情 DTO 取得），作乐观锁 WHERE 条件，覆盖 FindByID 读回的当前值，
// 以此检测 stale-form：他人已改过（version 自增）时 WHERE 不命中 → affected==0 → 1308，而非静默覆盖（dimension 域同模式）。
func (s *llmConfigService) Update(ctx context.Context, id int64, version int, name, provider, modelID, apiURL, apiKeyCipher, keyID string, hasAPIKey bool) (*CreateLLMResult, error) {
	if msg := validateLLMFields(name, provider, modelID, apiURL); msg != "" {
		return nil, NewErrorWithMsg(errcode.BadRequest, msg)
	}
	cfg, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.LLMConfigNotFound)
		}
		return nil, fmt.Errorf("find llm config by id: %w", err)
	}

	cfg.Name = name
	cfg.Provider = provider
	cfg.ModelID = modelID
	cfg.APIURL = apiURL

	if hasAPIKey {
		if apiKeyCipher == "" {
			return nil, NewErrorWithMsg(errcode.BadRequest, "api_key is required")
		}
		plaintext, derr := s.dec.Decrypt(ctx, keyID, apiKeyCipher)
		if derr != nil {
			return nil, NewErrorWithMsg(errcode.BadRequest, "api_key decrypt failed")
		}
		if utf8.RuneCountInString(plaintext) > 200 {
			return nil, NewErrorWithMsg(errcode.BadRequest, "api_key too long")
		}
		cipher, cerr := crypto.Encrypt(s.encKey, plaintext)
		if cerr != nil {
			return nil, fmt.Errorf("encrypt llm api key: %w", cerr)
		}
		cfg.APIKeyCipher = cipher
		cfg.APIKeyMasked = crypto.Mask(plaintext)
	}

	// 用客户端回传的 version 作乐观锁凭证，覆盖 FindByID 读回的当前值。
	cfg.Version = version

	affected, err := s.repo.Update(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("update llm config: %w", err)
	}
	if affected == 0 {
		return nil, NewError(errcode.LLMConfigVersionConflict)
	}
	return &CreateLLMResult{ID: cfg.ID, Version: version + 1}, nil
}

// Delete 删除模型：清单仅剩一个拦截（1302）；删启用态自动转启首个剩余（specs §4.2.4 规则3）。
// 「Delete + 转启」由 repository.DeleteAndTransferEnable 在单事务内原子完成（03 T2 契约），
// 转启失败即整体回滚，杜绝删除已落库但全表零启用的中间态。
func (s *llmConfigService) Delete(ctx context.Context, id int64) (*DeleteLLMResult, error) {
	cfg, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.LLMConfigNotFound)
		}
		return nil, fmt.Errorf("find llm config by id: %w", err)
	}

	count, err := s.repo.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count llm configs: %w", err)
	}
	if count <= 1 {
		return nil, NewError(errcode.LastLLMConfig)
	}

	result := &DeleteLLMResult{ID: id}

	if cfg.Enabled {
		// 删启用态：先查转启候选（只读查询不影响原子性），再单事务内 Delete + 排他启用候选。
		candidate, cerr := s.repo.FindFirstByIDOrder(ctx, id)
		if cerr != nil {
			return nil, fmt.Errorf("find first remaining llm config: %w", cerr)
		}
		if err := s.repo.DeleteAndTransferEnable(ctx, id, candidate.ID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, NewError(errcode.LLMConfigNotFound)
			}
			return nil, fmt.Errorf("delete and transfer enable llm config: %w", err)
		}
		cid := candidate.ID
		result.TransferredEnabledID = &cid
	} else {
		// 删非启用态：事务内只 Delete（enableID=0 跳过排他启用步骤）。
		if err := s.repo.DeleteAndTransferEnable(ctx, id, 0); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, NewError(errcode.LLMConfigNotFound)
			}
			return nil, fmt.Errorf("delete llm config: %w", err)
		}
	}

	return result, nil
}

// Enable 排他启用目标模型（specs §4.2.4 规则1）。返 {id, enabled:true}（03 §B5）。
func (s *llmConfigService) Enable(ctx context.Context, id int64) (*LLMEnableResult, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.LLMConfigNotFound)
		}
		return nil, fmt.Errorf("find llm config by id: %w", err)
	}
	if err := s.repo.EnableExclusive(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.LLMConfigNotFound)
		}
		return nil, fmt.Errorf("enable exclusive llm config: %w", err)
	}
	return &LLMEnableResult{ID: id, Enabled: true}, nil
}

// toLLMConfigDTO 把领域模型转列表 DTO，掩码直取不重新计算（落库时已生成快照）。
func toLLMConfigDTO(cfg *domain.LLMConfig) LLMConfigDTO {
	return LLMConfigDTO{
		ID:           cfg.ID,
		Name:         cfg.Name,
		Provider:     cfg.Provider,
		ModelID:      cfg.ModelID,
		APIURL:       cfg.APIURL,
		APIKeyMasked: cfg.APIKeyMasked,
		Enabled:      cfg.Enabled,
		Version:      cfg.Version,
		CreatedAt:    cfg.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    cfg.UpdatedAt.Format(time.RFC3339),
	}
}

// validateLLMFields 校验 name/provider/model_id/api_url，返回非空 message 表示失败（specs §4.3.2）。
func validateLLMFields(name, provider, modelID, apiURL string) string {
	if name == "" || utf8.RuneCountInString(name) > 50 {
		return "name is invalid"
	}
	if !validProviders[provider] {
		return "provider is invalid"
	}
	if modelID == "" || utf8.RuneCountInString(modelID) > 100 {
		return "model_id is invalid"
	}
	if apiURL != "" {
		if utf8.RuneCountInString(apiURL) > 500 {
			return "api_url too long"
		}
		// 强制 http(s) scheme 且 host 非空，与前端 Zod /^https?:\/\/.+/ 对齐，
		// 拒绝 file://、//host、mailto: 等非 http(s) 绝对 URI 绕过入库。
		u, perr := url.Parse(apiURL)
		if perr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return "api_url is invalid"
		}
	}
	return ""
}

// NewErrorWithMsg 构造带自定义文案的业务错误，供字段级校验提示。
func NewErrorWithMsg(code int, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}
