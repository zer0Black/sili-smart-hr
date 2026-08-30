// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"fmt"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// AssessmentConfigRepository 是评估周期配置域的数据访问接口。
//
// AssessmentConfig 为系统级单例（首启 migrateDB 幂等 seed 默认行），Get 恒返回该行；
// 保存路径用乐观锁（UPDATE WHERE id AND version），并发冲突由 service 层映射 1306；
// target_mode=specified 时 AssessmentConfigMember 关联表随主配置全量覆盖。
type AssessmentConfigRepository interface {
	// Get 读取单例配置。seed 保证恒有数据，ErrRecordNotFound 理论不触发，仍向上冒泡。
	Get(ctx context.Context) (*domain.AssessmentConfig, error)
	// UpdateWithVersion 乐观锁更新主配置：UPDATE WHERE id=? AND version=?，affected==1 成功，
	// affected==0 表示 version 不匹配（并发冲突），由 service 映射 1306。
	// 用 map 而非 struct Updates，避免 zero-value 被忽略。成功 version 自增。
	UpdateWithVersion(ctx context.Context, cfg *domain.AssessmentConfig) (int64, error)
	// ListMembers 按 configID 列出指定人员关联。
	ListMembers(ctx context.Context, configID int64) ([]domain.AssessmentConfigMember, error)
	// ReplaceMembers 事务内全量覆盖关联表：先删后插。members 为空时仅执行 Delete（target_mode=all）。
	ReplaceMembers(ctx context.Context, configID int64, members []domain.AssessmentConfigMember) error
	// UpdateWithMembers 单事务内完成乐观锁更新 + 人员全量覆盖。
	// repo 收口事务，避免并发下 version 已更新但 members 未覆盖。
	// 返回乐观锁那一步的 RowsAffected（1=成功，0=版本冲突，事务整体回滚）。
	UpdateWithMembers(ctx context.Context, cfg *domain.AssessmentConfig, members []domain.AssessmentConfigMember) (int64, error)
}

type assessmentConfigRepository struct {
	db *gorm.DB
}

// NewAssessmentConfigRepository 返回 AssessmentConfigRepository 接口实现。
func NewAssessmentConfigRepository(db *gorm.DB) AssessmentConfigRepository {
	return &assessmentConfigRepository{db: db}
}

// Get 读单例配置首行（migrateDB seed 保证恒有数据）。
func (r *assessmentConfigRepository) Get(ctx context.Context) (*domain.AssessmentConfig, error) {
	var cfg domain.AssessmentConfig
	if err := r.db.WithContext(ctx).First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpdateWithVersion 用 map 列举可变字段（period/trigger_time/target_mode）+ version 自增，
// 规避 struct Updates 对 zero-value 的忽略。Where 带 id AND version 双条件实现乐观锁。
func (r *assessmentConfigRepository) UpdateWithVersion(ctx context.Context, cfg *domain.AssessmentConfig) (int64, error) {
	res := r.db.WithContext(ctx).Model(&domain.AssessmentConfig{}).
		Where("id = ? AND version = ?", cfg.ID, cfg.Version).
		Updates(map[string]any{
			"period":       cfg.Period,
			"trigger_time": cfg.TriggerTime,
			"target_mode":  cfg.TargetMode,
			"version":      gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ListMembers 按关联 configID 列出全部指定人员。
func (r *assessmentConfigRepository) ListMembers(ctx context.Context, configID int64) ([]domain.AssessmentConfigMember, error) {
	var members []domain.AssessmentConfigMember
	if err := r.db.WithContext(ctx).Where("assessment_config_id = ?", configID).Find(&members).Error; err != nil {
		return nil, err
	}
	return members, nil
}

// ReplaceMembers 事务内先删后插，实现全量覆盖。members 为空时仅执行 Delete。
func (r *assessmentConfigRepository) ReplaceMembers(ctx context.Context, configID int64, members []domain.AssessmentConfigMember) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("assessment_config_id = ?", configID).Delete(&domain.AssessmentConfigMember{}).Error; err != nil {
			return fmt.Errorf("clear members: %w", err)
		}
		if len(members) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(members, 100).Error; err != nil {
			return fmt.Errorf("insert members: %w", err)
		}
		return nil
	})
}

// UpdateWithMembers 收口单事务：先乐观锁更新主配置，affected==0 直接返回（事务内无写操作），
// 否则继续全量覆盖 members。事务保证原子性，避免 version 已更新但 members 未覆盖。
func (r *assessmentConfigRepository) UpdateWithMembers(ctx context.Context, cfg *domain.AssessmentConfig, members []domain.AssessmentConfigMember) (int64, error) {
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.AssessmentConfig{}).
			Where("id = ? AND version = ?", cfg.ID, cfg.Version).
			Updates(map[string]any{
				"period":       cfg.Period,
				"trigger_time": cfg.TriggerTime,
				"target_mode":  cfg.TargetMode,
				"version":      gorm.Expr("version + 1"),
			})
		if res.Error != nil {
			return res.Error
		}
		affected = res.RowsAffected
		if affected == 0 {
			return nil // 版本冲突，事务内无写操作自然结束，等价回滚
		}
		// 全量覆盖关联表（事务内）。
		if err := tx.Where("assessment_config_id = ?", cfg.ID).Delete(&domain.AssessmentConfigMember{}).Error; err != nil {
			return fmt.Errorf("clear members: %w", err)
		}
		if len(members) > 0 {
			if err := tx.CreateInBatches(members, 100).Error; err != nil {
				return fmt.Errorf("insert members: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}
