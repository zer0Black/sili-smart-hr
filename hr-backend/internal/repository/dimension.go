// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// DimensionRepository 是能力维度域的数据访问接口，承载 dimensions 表与
// dimension_settings 单行表两类操作（减少 provider 数量，不另立 ActivityRuleRepository）。
type DimensionRepository interface {
	// ListAll 全量未软删除维度，按 code ASC 排序（spec §4.1.5），供维度树组装。
	ListAll(ctx context.Context) ([]domain.Dimension, error)
	// FindByID 主键查，软删除自动过滤（GORM DeletedAt）。
	FindByID(ctx context.Context, id int64) (*domain.Dimension, error)
	// FindByCodeExcludingDeleted 编码唯一性校验，排除软删除
	// （WHERE code=? AND deleted_at IS NULL）。返回 ErrRecordNotFound 表示编码可用。
	FindByCodeExcludingDeleted(ctx context.Context, code string) (*domain.Dimension, error)
	// Create 插入新维度。
	Create(ctx context.Context, d *domain.Dimension) error
	// UpdateWithVersion 乐观锁更新：WHERE id=? AND version=? AND deleted_at IS NULL，
	// 更新可变字段列并 version=version+1。返回 RowsAffected（1=成功，0=版本冲突或不存在）。
	UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error)
	// SoftDeleteWithVersion 软删除带乐观锁：WHERE id=? AND version=?，GORM Delete 返回 RowsAffected。
	SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error)
	// GetActivitySetting 读 dimension_settings 单行表首行（系统级单例，固定取首行）。
	GetActivitySetting(ctx context.Context) (*domain.DimensionSetting, error)
	// UpdateActivitySetting UPDATE dimension_settings 单行（保存即覆盖，无乐观锁）。
	UpdateActivitySetting(ctx context.Context, activeThreshold, lowFrequencyThreshold int) error
}

type dimensionRepository struct {
	db *gorm.DB
}

// NewDimensionRepository 返回 DimensionRepository 接口实现。
func NewDimensionRepository(db *gorm.DB) DimensionRepository {
	return &dimensionRepository{db: db}
}

// ListAll 全量未软删除维度，按 code ASC 排序（spec §4.1.5），供维度树组装。
// 仅 Select 树组装所需的 brief 字段，跳过 prompt/anchor/description 等长文本列，
// 减少 TEXT 列全量载入的内存与传输开销。GetTree/toBriefList 只读这些字段。
func (r *dimensionRepository) ListAll(ctx context.Context) ([]domain.Dimension, error) {
	var list []domain.Dimension
	if err := r.db.WithContext(ctx).
		Select("id, code, name, module_code, group_code, data_source, weight, include_overview, enabled").
		Order("code ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *dimensionRepository) FindByID(ctx context.Context, id int64) (*domain.Dimension, error) {
	var d domain.Dimension
	if err := r.db.WithContext(ctx).First(&d, id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// FindByCodeExcludingDeleted 显式写 deleted_at IS NULL，与 GORM 自动过滤等效，
// 语义上明确「唯一性校验只看未删除行」。
func (r *dimensionRepository) FindByCodeExcludingDeleted(ctx context.Context, code string) (*domain.Dimension, error) {
	var d domain.Dimension
	if err := r.db.WithContext(ctx).
		Where("code = ? AND deleted_at IS NULL", code).
		First(&d).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *dimensionRepository) Create(ctx context.Context, d *domain.Dimension) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// UpdateWithVersion 仅更新可变字段：name/prompt/anchor/weight/include_overview/enabled/description，
// 不可变字段 code/module_code/group_code/data_source 不在 updates（spec §4.1.2 B + 规则8）。
// version 通过 gorm.Expr 自增，避免与 updates 字典冲突。外层 Model 让 GORM 自动追加
// deleted_at IS NULL，但显式写一次以与 FindByCodeExcludingDeleted 语义对齐。
func (r *dimensionRepository) UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error) {
	updates["version"] = gorm.Expr("version + 1")
	res := r.db.WithContext(ctx).Model(&domain.Dimension{}).
		Where("id = ? AND version = ? AND deleted_at IS NULL", id, version).
		Updates(updates)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// SoftDeleteWithVersion 用 GORM Delete 配合 WHERE id/version 实现软删除带乐观锁。
// Delete 对含 gorm.DeletedAt 的模型自动写 deleted_at，不再物理删除。
func (r *dimensionRepository) SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND version = ?", id, version).
		Delete(&domain.Dimension{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// GetActivitySetting 读单行表首行。生产环境 migrateDB 会 seed 一行，
// 未 seed 时返回 ErrRecordNotFound 让 service 层显式处理。
func (r *dimensionRepository) GetActivitySetting(ctx context.Context) (*domain.DimensionSetting, error) {
	var s domain.DimensionSetting
	if err := r.db.WithContext(ctx).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateActivitySetting 单行覆盖更新，无乐观锁（系统级单例，保存即生效）。
// Updates 只写两列，不动 ID 与时间戳（autoUpdateTime 自动刷新）。
func (r *dimensionRepository) UpdateActivitySetting(ctx context.Context, activeThreshold, lowFrequencyThreshold int) error {
	return r.db.WithContext(ctx).Model(&domain.DimensionSetting{}).
		Where("1 = 1").
		Updates(map[string]any{
			"active_threshold":        activeThreshold,
			"low_frequency_threshold": lowFrequencyThreshold,
		}).Error
}
