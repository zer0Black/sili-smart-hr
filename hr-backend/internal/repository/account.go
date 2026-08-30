// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// AccountRepository 是 account 域的数据访问接口。
type AccountRepository interface {
	FindByUsername(ctx context.Context, username string) (*domain.Account, error)
	FindByID(ctx context.Context, id int64) (*domain.Account, error)
	UpdateLastLoginAt(ctx context.Context, id int64, at time.Time) error
	// ListAccounts 分页列出账号（created_at DESC）。keyword 非空时对 username/name 做字面 LIKE。
	ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]domain.Account, int64, error)
	Create(ctx context.Context, acc *domain.Account) error
	// Update 整行 Save 更新。
	Update(ctx context.Context, acc *domain.Account) error
	Delete(ctx context.Context, id int64) error
	UpdateEnabled(ctx context.Context, id int64, enabled bool) error
	// DemoteEnabledIfNotLast 原子降停：仅当 id 当前启用且启用总数 > 1 时切 false。
	// 返回受影响行数（1=已降停，0=最后一个启用或已禁用/不存在）。
	DemoteEnabledIfNotLast(ctx context.Context, id int64) (int64, error)
	// DeleteIfNotLastEnabled 原子软删除：id 禁用或启用总数 > 1 时生效。
	// 返回受影响行数（1=已删除，0=最后一个启用或不存在）。
	DeleteIfNotLastEnabled(ctx context.Context, id int64) (int64, error)
}

type accountRepository struct {
	db *gorm.DB
}

func NewAccountRepository(db *gorm.DB) AccountRepository {
	return &accountRepository{db: db}
}

func (r *accountRepository) FindByUsername(ctx context.Context, username string) (*domain.Account, error) {
	var acc domain.Account
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&acc).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *accountRepository) FindByID(ctx context.Context, id int64) (*domain.Account, error) {
	var acc domain.Account
	if err := r.db.WithContext(ctx).First(&acc, id).Error; err != nil {
		return nil, err
	}
	return &acc, nil
}

func (r *accountRepository) UpdateLastLoginAt(ctx context.Context, id int64, at time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.Account{}).
		Where("id = ?", id).
		Update("last_login_at", at).Error
}

// ListAccounts 按条件分页查询。page 应 >= 1（由调用方保证）。
// keyword 经 likeescape.EscapeLike 转义通配符，配合 ESCAPE '\' 字面匹配，调用方无需再处理。
func (r *accountRepository) ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]domain.Account, int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.Account{})
	if keyword != "" {
		pat := "%" + likeescape.EscapeLike(keyword) + "%"
		query = query.Where("username LIKE ? ESCAPE '\\' OR name LIKE ? ESCAPE '\\'", pat, pat)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []domain.Account
	if err := query.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *accountRepository) Create(ctx context.Context, acc *domain.Account) error {
	return r.db.WithContext(ctx).Create(acc).Error
}

func (r *accountRepository) Update(ctx context.Context, acc *domain.Account) error {
	return r.db.WithContext(ctx).Save(acc).Error
}

func (r *accountRepository) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&domain.Account{}, id).Error
}

func (r *accountRepository) UpdateEnabled(ctx context.Context, id int64, enabled bool) error {
	return r.db.WithContext(ctx).Model(&domain.Account{}).
		Where("id = ?", id).
		Update("enabled", enabled).Error
}

// DemoteEnabledIfNotLast 把「校验是否最后一个启用」与「切 enabled」收敛进单条 UPDATE，消除 TOCTOU。
// 注意：外层 Model 让 GORM 自动追加 deleted_at IS NULL，但子查询是手写 SQL，软删除过滤不作用其中，
// deleted_at IS NULL 必须显式写。
func (r *accountRepository) DemoteEnabledIfNotLast(ctx context.Context, id int64) (int64, error) {
	res := r.db.WithContext(ctx).Model(&domain.Account{}).
		Where("id = ? AND enabled = ? AND (SELECT COUNT(*) FROM accounts WHERE enabled = ? AND deleted_at IS NULL) > ?",
			id, true, true, 1).
		Update("enabled", false)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// DeleteIfNotLastEnabled 同理把判定与软删除收敛进单条 SQL，消除 TOCTOU。子查询同样需显式 deleted_at IS NULL。
func (r *accountRepository) DeleteIfNotLastEnabled(ctx context.Context, id int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND (enabled = ? OR (SELECT COUNT(*) FROM accounts WHERE enabled = ? AND deleted_at IS NULL) > ?)",
			id, false, true, 1).
		Delete(&domain.Account{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
