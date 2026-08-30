// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// IntegrationSecretRepository 是集成密钥单例的数据访问接口。
//
// integration_secrets 为系统级单例（migrateDB 首启幂等 seed 空密钥行），
// Get 恒有数据；Update 用乐观锁（UPDATE WHERE id AND version），并发冲突由 service 层映射 1309。
// 用 map[string]any 而非 struct Updates，确保空串 SecretCipher 也能写入
// （GORM struct Updates 会忽略 zero-value，specs §6.1 + 04 §3.3.1）。
type IntegrationSecretRepository interface {
	// Get 读取单例。seed 保证恒有数据，ErrRecordNotFound 理论不触发，仍向上冒泡。
	Get(ctx context.Context) (*domain.IntegrationSecret, error)
	// Update 乐观锁覆盖 secret_cipher 与 secret_masked：UPDATE WHERE id=? AND version=?，
	// affected==1 成功（version 自增），affected==0 表示 version 不匹配（并发冲突），由 service 映射 1309。
	// 用 map 让空串也能落库（清空场景）。
	Update(ctx context.Context, secret *domain.IntegrationSecret) (int64, error)
}

type integrationSecretRepository struct {
	db *gorm.DB
}

// NewIntegrationSecretRepository 返回 IntegrationSecretRepository 接口实现。
func NewIntegrationSecretRepository(db *gorm.DB) IntegrationSecretRepository {
	return &integrationSecretRepository{db: db}
}

// Get 读单例首行（migrateDB seed 保证恒有数据，specs §6.1 + 04 §3.3.1 BR1）。
func (r *integrationSecretRepository) Get(ctx context.Context) (*domain.IntegrationSecret, error) {
	var secret domain.IntegrationSecret
	if err := r.db.WithContext(ctx).First(&secret).Error; err != nil {
		return nil, err
	}
	return &secret, nil
}

// Update 按 id 定位单例，Updates 用 map[string]any 显式列举两列，
// 规避 struct Updates 对空串 zero-value 的忽略（specs §4.5.4 规则1、04 §3.3.1 BR2）。
// Where 带 id AND version 双条件实现乐观锁，version 自增；affected==0 表示并发冲突。
func (r *integrationSecretRepository) Update(ctx context.Context, secret *domain.IntegrationSecret) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.IntegrationSecret{}).
		Where("id = ? AND version = ?", secret.ID, secret.Version).
		Updates(map[string]any{
			"secret_cipher": secret.SecretCipher,
			"secret_masked": secret.SecretMasked,
			"version":       gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
