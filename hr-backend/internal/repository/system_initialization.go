package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// SystemInitializationRepository 是系统初始化状态的数据访问接口。
// 仅暴露 Exists：初始化记录的写入由 SetupService 在事务内直接用 tx.Create 编排，
// repository 持有 root db 无法参与 service 事务边界。
type SystemInitializationRepository interface {
	// Exists 判定 system_initializations 是否存在记录（记录存在即已初始化）。
	// 单行查询走 LIMIT 1，开销可忽略不缓存（04 §5）。
	Exists(ctx context.Context) (bool, error)
}

type systemInitializationRepository struct {
	db *gorm.DB
}

// NewSystemInitializationRepository 返回注入 root db 的 SystemInitializationRepository。
func NewSystemInitializationRepository(db *gorm.DB) SystemInitializationRepository {
	return &systemInitializationRepository{db: db}
}

// Exists 通过 Count Limit 1 判定记录存在性，错误透传（04 §5）。
func (r *systemInitializationRepository) Exists(ctx context.Context) (bool, error) {
	var n int64
	if err := r.db.WithContext(ctx).
		Model(&domain.SystemInitialization{}).
		Limit(1).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}
