// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// QuestionRepository 是题库域的数据访问接口。
type QuestionRepository interface {
	// ListPage 分页查询列表 tab 主路径：source 必选；status 空=全部；恒排除 PENDING；
	// keyword 对 question_no 与 scenario 做 EscapeLike 转义后的 LIKE OR 匹配（ESCAPE '\'）；
	// 排序 updated_at DESC, id DESC。返回行集（长文本截断在 service）与 total。
	ListPage(ctx context.Context, source string, dimensionID int64, dimensionIDSet bool, status, keyword string, page, pageSize int) ([]domain.Question, int64, error)
	// FindByID 主键查，软删自动过滤。
	FindByID(ctx context.Context, id int64) (*domain.Question, error)
	// UpdateWithVersion 乐观锁更新：WHERE id=? AND version=? AND deleted_at IS NULL，version 自增。
	// 返回 RowsAffected（0=冲突或不存在）。
	UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error)
	// SoftDeleteWithVersion 乐观锁软删：WHERE id=? AND version=?，GORM Delete 置 deleted_at。返回 RowsAffected。
	SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error)
	// ListByIDs 批量按 ID 查（SP2 确认入库的批内题目定位复用），软删自动过滤。
	ListByIDs(ctx context.Context, ids []int64) ([]domain.Question, error)
	// MaxQuestionSeq 按编号前缀查最大序号（含软删行 Unscoped），无行返回 0。SP3/SP4 编号分配复用。
	MaxQuestionSeq(ctx context.Context, prefix string) (int64, error)
}

type questionRepository struct {
	db *gorm.DB
}

// NewQuestionRepository 返回 QuestionRepository 接口实现。
func NewQuestionRepository(db *gorm.DB) QuestionRepository {
	return &questionRepository{db: db}
}

// ListPage 列表 tab 主路径（spec §4.1.2 A/B）。dimensionIDSet 区分「未传维度」与
// 「传空」（查询参数空串=全部维度）：false 时 dimensionID 不进 WHERE。分页钳制
// page<1 归 1、pageSize 1~100。
func (r *questionRepository) ListPage(ctx context.Context, source string, dimensionID int64, dimensionIDSet bool, status, keyword string, page, pageSize int) ([]domain.Question, int64, error) {
	// BR2 §4.3.4 规则 2：待审核题目不在列表 tab 出现，恒排除 PENDING。
	query := r.db.WithContext(ctx).Model(&domain.Question{}).
		Where("source = ? AND status <> ?", source, domain.QuestionStatusPending)
	if dimensionIDSet {
		query = query.Where("dimension_id = ?", dimensionID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if keyword != "" {
		pat := "%" + likeescape.EscapeLike(keyword) + "%"
		query = query.Where("(question_no LIKE ? ESCAPE '\\' OR scenario LIKE ? ESCAPE '\\')", pat, pat)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var list []domain.Question
	if err := query.
		Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *questionRepository) FindByID(ctx context.Context, id int64) (*domain.Question, error) {
	var q domain.Question
	if err := r.db.WithContext(ctx).First(&q, id).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

// UpdateWithVersion 乐观锁更新，version 经 gorm.Expr 自增避免与 updates 字典冲突。
// 显式写 deleted_at IS NULL 与维度仓储先例语义对齐。
func (r *questionRepository) UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error) {
	updates["version"] = gorm.Expr("version + 1")
	res := r.db.WithContext(ctx).Model(&domain.Question{}).
		Where("id = ? AND version = ? AND deleted_at IS NULL", id, version).
		Updates(updates)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// SoftDeleteWithVersion 乐观锁软删，GORM Delete 对含 gorm.DeletedAt 的模型自动置
// deleted_at。question_no 只增不复用（spec §4.1.2 B），无需改写占位码释放编号。
func (r *questionRepository) SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND version = ?", id, version).
		Delete(&domain.Question{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ListByIDs 批量按 ID 查，GORM DeletedAt 自动过滤软删行。空 ID 列表直接返回空集，
// 避免 GORM 对空 IN 子句生成无 WHERE 的全表查询。
func (r *questionRepository) ListByIDs(ctx context.Context, ids []int64) ([]domain.Question, error) {
	if len(ids) == 0 {
		return []domain.Question{}, nil
	}
	var list []domain.Question
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// MaxQuestionSeq 取前缀下 question_no 序号部分的 SQL 最大值（CAST 后比较），
// Unscoped 含软删行：编号只增不复用（spec §4.1.2 B），序号空间被软删行继续占用。
// CAST(... AS INTEGER) 三库通用（SQLite/MySQL/PG），非数字残留（理论不存在）转 0 不参与最大值。
func (r *questionRepository) MaxQuestionSeq(ctx context.Context, prefix string) (int64, error) {
	var seq *int64
	if err := r.db.WithContext(ctx).Unscoped().Model(&domain.Question{}).
		Select("MAX(CAST(SUBSTR(question_no, ?) AS INTEGER))", len(prefix)+1).
		Where("question_no LIKE ? ESCAPE '\\'", likeescape.EscapeLike(prefix)+"%").
		Scan(&seq).Error; err != nil {
		return 0, err
	}
	if seq == nil {
		return 0, nil
	}
	return *seq, nil
}
