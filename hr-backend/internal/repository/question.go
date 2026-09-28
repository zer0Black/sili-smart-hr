// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"strconv"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// QuestionRepository 是题库域的数据访问接口。
type QuestionRepository interface {
	// ListPage 分页查询列表 tab 主路径：source 必选；status 空=全部；恒排除 PENDING；
	// keyword 对 question_no 与 scenario 做 EscapeLike 转义后的 LIKE OR 匹配（ESCAPE '\'）；
	// 排序 updated_at DESC, id DESC。dimensionID 非 nil 才进 WHERE（nil=全部维度）。
	// 返回行集（长文本截断在 service）与 total。
	ListPage(ctx context.Context, source string, dimensionID *int64, status, keyword string, page, pageSize int) ([]domain.Question, int64, error)
	// FindByID 主键查，软删自动过滤。
	FindByID(ctx context.Context, id int64) (*domain.Question, error)
	// UpdateWithVersion 乐观锁更新：WHERE id=? AND version=? AND deleted_at IS NULL，version 自增。
	// 返回 RowsAffected（0=冲突或不存在）。
	UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error)
	// SoftDeleteWithVersion 乐观锁软删：WHERE id=? AND version=?，GORM Delete 置 deleted_at。返回 RowsAffected。
	SoftDeleteWithVersion(ctx context.Context, id int64, version int) (int64, error)
	// MaxQuestionSeq 按编号前缀查最大序号（含软删行 Unscoped），无行返回 0。SP3/SP4 编号分配复用。
	MaxQuestionSeq(ctx context.Context, prefix string) (int64, error)
	// ListActiveByDimensionIDs 主动测试 AI 取题：source=AI AND status=ACTIVE AND dimension_id IN (?)，
	// question_no ASC（specs TST §4.2.4 规则1 全取口径，软删自动过滤；返回行由调用方按 dimension_id 分组消费）。
	ListActiveByDimensionIDs(ctx context.Context, dimensionIDs []int64) ([]domain.Question, error)
	// ListActiveByScaleKey 主动测试九型取题：source=SCALE AND status=ACTIVE AND scale_key=?，
	// question_no ASC（specs TST §4.2.4 规则2 固定量表全量）。
	ListActiveByScaleKey(ctx context.Context, scaleKey string) ([]domain.Question, error)
	// IncrementReferenceCounts 题目引用计数 +1：在传入 tx 通道执行
	// UPDATE questions SET reference_count = reference_count + 1 WHERE id IN (?)
	//（specs TST §5.1.2 步骤4，与任务创建同事务；tx 为 nil 则走自身 db 通道）。
	IncrementReferenceCounts(ctx context.Context, tx *gorm.DB, questionIDs []int64) error
}

type questionRepository struct {
	db *gorm.DB
}

// NewQuestionRepository 返回 QuestionRepository 接口实现。
func NewQuestionRepository(db *gorm.DB) QuestionRepository {
	return &questionRepository{db: db}
}

// ListPage 列表 tab 主路径（spec §4.1.2 A/B）。dimensionID nil=全部维度（查询参数
// 空串），非 nil 才进 WHERE。分页钳制 page<1 归 1、pageSize 1~100。
func (r *questionRepository) ListPage(ctx context.Context, source string, dimensionID *int64, status, keyword string, page, pageSize int) ([]domain.Question, int64, error) {
	// BR2 §4.3.4 规则 2：待审核题目不在列表 tab 出现，恒排除 PENDING。
	query := r.db.WithContext(ctx).Model(&domain.Question{}).
		Where("source = ? AND status <> ?", source, domain.QuestionStatusPending)
	if dimensionID != nil {
		query = query.Where("dimension_id = ?", *dimensionID)
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
	page, pageSize = ClampPage(page, pageSize)
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
// 拷贝入参字典再补 version 键，不改写调用方传入的 map（与 ResubmitToBatch 同形态）。
// 显式写 deleted_at IS NULL 与维度仓储先例语义对齐。
func (r *questionRepository) UpdateWithVersion(ctx context.Context, id int64, version int, updates map[string]any) (int64, error) {
	merged := make(map[string]any, len(updates)+1)
	for k, v := range updates {
		merged[k] = v
	}
	merged["version"] = gorm.Expr("version + 1")
	res := r.db.WithContext(ctx).Model(&domain.Question{}).
		Where("id = ? AND version = ? AND deleted_at IS NULL", id, version).
		Updates(merged)
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

// MaxQuestionSeq 取前缀下 question_no 序号最大值：长度降序+字典序降序取首行后 Go 侧
// 解析（等宽数字下与数值序一致，兼容 9999 与 10000 共存）。Unscoped 含软删行：编号
// 只增不复用（spec §4.1.2 B）。不用 CAST：MySQL 目标类型仅 SIGNED/UNSIGNED，裸
// INTEGER 是 1064 语法错误。
func (r *questionRepository) MaxQuestionSeq(ctx context.Context, prefix string) (int64, error) {
	var top []string
	if err := r.db.WithContext(ctx).Unscoped().Model(&domain.Question{}).
		Select("question_no").
		Where("question_no LIKE ? ESCAPE '\\'", likeescape.EscapeLike(prefix)+"%").
		Order("LENGTH(question_no) DESC, question_no DESC").
		Limit(1).
		Pluck("question_no", &top).Error; err != nil {
		return 0, err
	}
	if len(top) == 0 {
		return 0, nil
	}
	seq, err := strconv.ParseInt(top[0][len(prefix):], 10, 64)
	if err != nil {
		// 非数字残留（理论不存在）按 0 处理，不参与最大值。
		return 0, nil
	}
	return seq, nil
}

// ListActiveByDimensionIDs 空维度集直接返回空切片，避免空 IN () 语义漂移。
func (r *questionRepository) ListActiveByDimensionIDs(ctx context.Context, dimensionIDs []int64) ([]domain.Question, error) {
	if len(dimensionIDs) == 0 {
		return []domain.Question{}, nil
	}
	var list []domain.Question
	if err := r.db.WithContext(ctx).
		Where("source = ? AND status = ? AND dimension_id IN ?", domain.QuestionSourceAI, domain.QuestionStatusActive, dimensionIDs).
		Order("question_no ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *questionRepository) ListActiveByScaleKey(ctx context.Context, scaleKey string) ([]domain.Question, error) {
	var list []domain.Question
	if err := r.db.WithContext(ctx).
		Where("source = ? AND status = ? AND scale_key = ?", domain.QuestionSourceScale, domain.QuestionStatusActive, scaleKey).
		Order("question_no ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// IncrementReferenceCounts gorm.Expr 自增防读改写竞态；空 ID 集 no-op。
// tx 是任务创建的外层事务通道，直接在其上执行而不自开事务（specs §5.1.2 步骤4）。
func (r *questionRepository) IncrementReferenceCounts(ctx context.Context, tx *gorm.DB, questionIDs []int64) error {
	if len(questionIDs) == 0 {
		return nil
	}
	run := r.db
	if tx != nil {
		run = tx
	}
	return run.WithContext(ctx).Model(&domain.Question{}).
		Where("id IN ?", questionIDs).
		Update("reference_count", gorm.Expr("reference_count + 1")).Error
}
