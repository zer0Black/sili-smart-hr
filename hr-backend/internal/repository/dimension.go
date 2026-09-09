// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"errors"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

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
	// UpdateActivitySetting 覆盖更新单行（保存即覆盖，无乐观锁）。行缺失时自愈补行
	//（migrateDB seed 缺失或存量库行丢失的兜底），语义与 seed 的 GetOrCreate 对齐。
	UpdateActivitySetting(ctx context.Context, activeThreshold, lowFrequencyThreshold int) error
	// ListEnabledFullByDataSource 取指定数据来源的启用未删维度全字段（含 prompt/anchor，
	// 评分口径快照的数据来源），WHERE enabled AND data_source=? AND deleted_at IS NULL，
	// 按 code ASC 排序。
	ListEnabledFullByDataSource(ctx context.Context, dataSource string) ([]domain.Dimension, error)
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
// Delete 对含 gorm.DeletedAt 的模型自动写 deleted_at，不再物理删除；
// 同时把 code 改写为占位码（原 code__D<id>），释放原 code 供新建复用
//（uk_dimension_code 唯一索引下软删行与新行同 code 会冲突，三库通吃的占位方案）。
func (r *dimensionRepository) SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error) {
	var cur domain.Dimension
	if err := r.db.WithContext(ctx).
		Where("id = ? AND version = ?", id, version).
		First(&cur).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	res := r.db.WithContext(ctx).Model(&domain.Dimension{}).
		Where("id = ? AND version = ?", id, version).
		Updates(map[string]any{
			"code":       domain.DeletedCode(cur.Code, id),
			"deleted_at": time.Now(),
		})
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
// 行缺失时自愈补行（默认值取入参，与 migrateDB seed 语义对齐），防保存静默 0 行、回读
// NotFound 恒 1500。两次循环收敛并发：UPDATE id=1 → UPDATE 任意行（兜存量雪花主键行）
// → 补行，补行撞 UniqueViolation 说明对端并发建行，重试 UPDATE 写入本端值。
func (r *dimensionRepository) UpdateActivitySetting(ctx context.Context, activeThreshold, lowFrequencyThreshold int) error {
	updates := map[string]any{
		"active_threshold":        activeThreshold,
		"low_frequency_threshold": lowFrequencyThreshold,
	}
	db := r.db.WithContext(ctx)
	for range 2 {
		res := db.Model(&domain.DimensionSetting{}).Where("id = ?", domain.SingleRowID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		// 兜存量库主键为雪花值的已 seed 行。
		res = db.Model(&domain.DimensionSetting{}).Where("1 = 1").Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		err := db.Create(&domain.DimensionSetting{
			ID:                    domain.SingleRowID,
			ActiveThreshold:       activeThreshold,
			LowFrequencyThreshold: lowFrequencyThreshold,
		}).Error
		if err == nil {
			return nil
		}
		if !dberr.UniqueViolation(err) {
			return err
		}
		// 并发对端已建行，下一轮 UPDATE 命中写入本端值。
	}
	return nil
}

// ListEnabledFullByDataSource 取启用未删指定来源维度全字段。与 ListAll 的 brief 投影
// 相反，此处含 prompt/anchor 等长文本列：调用方（评分口径快照）需要全字段原文。
func (r *dimensionRepository) ListEnabledFullByDataSource(ctx context.Context, dataSource string) ([]domain.Dimension, error) {
	var list []domain.Dimension
	if err := r.db.WithContext(ctx).
		Where("enabled = ? AND data_source = ? AND deleted_at IS NULL", true, dataSource).
		Order("code ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
