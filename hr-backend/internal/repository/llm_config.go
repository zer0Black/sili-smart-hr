// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// LLMConfigRepository 是大模型配置域的数据访问接口。
// 排他启用（EnableExclusive）自带事务，全表唯一 enabled=true 由该方法保证。
type LLMConfigRepository interface {
	// List 全量返回，按 created_at 升序（删除转启顺序稳定）。keyword 非空时按 name 或 model_id 字面 LIKE。
	List(ctx context.Context, keyword string) ([]domain.LLMConfig, error)
	FindByID(ctx context.Context, id int64) (*domain.LLMConfig, error)
	Create(ctx context.Context, cfg *domain.LLMConfig) error
	// CreateExclusiveFirst 在单事务内创建配置：若当前全表为空（首条），创建后启用该行并保证排他；
	// 若全表非空，按传入的 enabled 值创建。事务内用全表 UPDATE enabled=false 再 INSERT 启用行，
	// 收敛「Count 读后写」竞态窗口，避免两个并发首条 Create 各自插入 enabled=true。
	// 返回创建后的配置（含雪花 ID 与最终 enabled 值）。
	CreateExclusiveFirst(ctx context.Context, cfg *domain.LLMConfig) error
	// Update 乐观锁更新元数据与密钥列（WHERE id AND version），返回 RowsAffected（0 表示版本冲突）。
	Update(ctx context.Context, cfg *domain.LLMConfig) (int64, error)
	// Delete 物理删除（specs §1.3、04 §3.2.1）。
	Delete(ctx context.Context, id int64) error
	Count(ctx context.Context) (int64, error)
	// FindFirstEnabled 返回当前启用项（排他启用下全表至多一行）。
	FindFirstEnabled(ctx context.Context) (*domain.LLMConfig, error)
	// FindFirstByIDOrder 按列表顺序（created_at 升序）取首个剩余，排除 excludeID（删除转启候选）。
	FindFirstByIDOrder(ctx context.Context, excludeID int64) (*domain.LLMConfig, error)
	// EnableExclusive 事务内全表停用再启用目标，保证排他（specs §4.2.4 规则1、04 §3.2.1）。
	EnableExclusive(ctx context.Context, id int64) error
	// DeleteAndTransferEnable 单事务内删 deleteID，再排他启用 enableID（specs §4.2.4 规则1、03 T2 契约）。
	// enableID 为 0 时只删不启，对应非启用态删除分支。事务保证「Delete + 转启」原子落库，
	// 规避删除成功但转启失败导致的全表零启用状态。
	DeleteAndTransferEnable(ctx context.Context, deleteID int64, enableID int64) error
}

type llmConfigRepository struct {
	db *gorm.DB
}

func NewLLMConfigRepository(db *gorm.DB) LLMConfigRepository {
	return &llmConfigRepository{db: db}
}

// List 全量返回。keyword 经 likeescape.EscapeLike 转义通配符，配合 ESCAPE '\' 字面匹配。
// 按 created_at 升序，与 FindFirstByIDOrder 共享排序，保证删除转启的列表顺序稳定（specs §4.2.4 规则3）。
func (r *llmConfigRepository) List(ctx context.Context, keyword string) ([]domain.LLMConfig, error) {
	query := r.db.WithContext(ctx).Model(&domain.LLMConfig{})
	if keyword != "" {
		pat := "%" + likeescape.EscapeLike(keyword) + "%"
		query = query.Where("name LIKE ? ESCAPE '\\' OR model_id LIKE ? ESCAPE '\\'", pat, pat)
	}
	var list []domain.LLMConfig
	if err := query.Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *llmConfigRepository) FindByID(ctx context.Context, id int64) (*domain.LLMConfig, error) {
	var cfg domain.LLMConfig
	if err := r.db.WithContext(ctx).First(&cfg, id).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *llmConfigRepository) Create(ctx context.Context, cfg *domain.LLMConfig) error {
	return r.db.WithContext(ctx).Create(cfg).Error
}

// CreateExclusiveFirst 单事务内完成「判首条 + 创建 + 排他启用」。事务内 SELECT COUNT 拿一致视图，
// count==0 视为首条：先全表 UPDATE enabled=false 收敛可能脏数据，再置 cfg.Enabled=true 后 Create。
// count>0 时按 cfg.Enabled 原值创建。事务收口 Count 与 Create，消除两个并发首条各自插入 enabled=true 的竞态。
// Create 触发雪花 ID 回调，事务提交后 cfg.ID/Enabled 即最终落库值。
func (r *llmConfigRepository) CreateExclusiveFirst(ctx context.Context, cfg *domain.LLMConfig) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&domain.LLMConfig{}).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			// 首条：先全表停用兜底脏数据，再启用新行。
			if err := tx.Model(&domain.LLMConfig{}).Where("enabled = ?", true).
				Update("enabled", false).Error; err != nil {
				return err
			}
			cfg.Enabled = true
		}
		return tx.Create(cfg).Error
	})
}

// Update 乐观锁更新元数据与密钥列。WHERE id AND version 双条件，affected==0 表示版本冲突，
// 由 service 层映射 1308。用 map 显式列举字段规避 struct Updates 对 zero-value 的忽略，
// version 经 gorm.Expr 自增（与 assessment_config.UpdateWithVersion 同模式）。
func (r *llmConfigRepository) Update(ctx context.Context, cfg *domain.LLMConfig) (int64, error) {
	res := r.db.WithContext(ctx).Model(&domain.LLMConfig{}).
		Where("id = ? AND version = ?", cfg.ID, cfg.Version).
		Updates(map[string]any{
			"name":           cfg.Name,
			"provider":       cfg.Provider,
			"model_id":       cfg.ModelID,
			"api_url":        cfg.APIURL,
			"api_key_cipher": cfg.APIKeyCipher,
			"api_key_masked": cfg.APIKeyMasked,
			"version":        gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// Delete 物理删除（LLMConfig 无 DeletedAt，GORM Delete 直接 DELETE）。
func (r *llmConfigRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&domain.LLMConfig{}, id).Error
}

// Count 返回全表行数，用于判定清单是否仅剩一个（specs §4.2.4 规则3 删除前置）。
func (r *llmConfigRepository) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&domain.LLMConfig{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// FindFirstEnabled 返回首个 enabled=true 记录（排他启用下唯一）。
func (r *llmConfigRepository) FindFirstEnabled(ctx context.Context) (*domain.LLMConfig, error) {
	var cfg domain.LLMConfig
	if err := r.db.WithContext(ctx).Where("enabled = ?", true).First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// FindFirstByIDOrder 按列表顺序取首个剩余（排除 excludeID），用作删除启用项后的转启候选。
// 排序与 List 一致（created_at ASC），保证转启结果可预期（specs §4.2.4 规则3）。
func (r *llmConfigRepository) FindFirstByIDOrder(ctx context.Context, excludeID int64) (*domain.LLMConfig, error) {
	var cfg domain.LLMConfig
	if err := r.db.WithContext(ctx).
		Where("id <> ?", excludeID).
		Order("created_at ASC").
		First(&cfg).Error; err != nil {
		return nil, err
	}
	return &cfg, nil
}

// EnableExclusive 自带事务：先全表 UPDATE enabled=false，再 UPDATE 目标 enabled=true。
// 目标行的 UPDATE 若 RowsAffected==0（并发删除或 id 本就不存在）视为不变量破坏，
// 返回 ErrRecordNotFound 触发事务回滚，避免「全表已停用、目标未启用」的零启用中间态。
// 事务收口与子计划 02 UpdateWithMembers 思路一致，service 层直接调用即可（specs §4.2.4 规则1、04 §3.2.1）。
func (r *llmConfigRepository) EnableExclusive(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.LLMConfig{}).Where("enabled = ?", true).
			Update("enabled", false).Error; err != nil {
			return err
		}
		res := tx.Model(&domain.LLMConfig{}).Where("id = ?", id).
			Update("enabled", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// DeleteAndTransferEnable 单事务收口「Delete + 排他启用」，与子计划 02 UpdateWithMembers 同模式。
// enableID 为 0 时跳过排他启用步骤，对应非启用态删除分支；非 0 时全表停用再启用该 id。
// deleteID 的 Delete 若 RowsAffected==0（service FindByID 后被并发删除，或本就不存在），返回 ErrRecordNotFound
// 触发事务回滚，避免「删除已落空却仍全表停用转启」或静默成功的错误语义。候选 enableID 的 UPDATE 若
// RowsAffected==0（候选已被删或不存在）同样返 ErrRecordNotFound，避免「deleteID 已删但无启用项」的零启用中间态。
func (r *llmConfigRepository) DeleteAndTransferEnable(ctx context.Context, deleteID int64, enableID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		delRes := tx.Delete(&domain.LLMConfig{}, deleteID)
		if delRes.Error != nil {
			return delRes.Error
		}
		if delRes.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if enableID == 0 {
			return nil
		}
		if err := tx.Model(&domain.LLMConfig{}).Where("enabled = ?", true).
			Update("enabled", false).Error; err != nil {
			return err
		}
		res := tx.Model(&domain.LLMConfig{}).Where("id = ?", enableID).
			Update("enabled", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}
