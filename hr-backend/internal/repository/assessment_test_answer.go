// AssessmentTestAnswerRepository 承载作答域数据访问：逐条落库、按任务计数与
// 有序读取、链接令牌哈希点查（specs P2_TST_002 §5.1.2/§5.2.2/§5.2.4）。
// 对话记录只增不改（§5.2.4 规则3），故无 Update/Delete 方法。
package repository

import (
	"context"
	"errors"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// AssessmentTestAnswerRepository answer 域数据访问接口（双重接口范式）。
type AssessmentTestAnswerRepository interface {
	// Insert 落库一条回复（雪花 ID 由 GORM 全局回调生成，时间 UTC 显式写入
	// 与 CreateWithLink 同范式）。
	Insert(ctx context.Context, row *domain.AssessmentTestAnswer) error
	// CountByTask 该任务已落库回复数（进度推算与完成判定，idx_task_seq 前缀）。
	CountByTask(ctx context.Context, taskID int64) (int64, error)
	// ListByTask 按任务取全部回复，question_seq 升序（A1 断点恢复与阅卷消费）。
	ListByTask(ctx context.Context, taskID int64) ([]domain.AssessmentTestAnswer, error)
	// FindLinkByTokenHash SHA-256 hex 点查 assessment_test_links（uk_token_hash），
	// 无行返 (nil, nil)。
	FindLinkByTokenHash(ctx context.Context, tokenHash string) (*domain.AssessmentTestLink, error)
}

type assessmentTestAnswerRepository struct {
	db *gorm.DB
}

// NewAssessmentTestAnswerRepository 返回 AssessmentTestAnswerRepository 接口实现。
func NewAssessmentTestAnswerRepository(db *gorm.DB) AssessmentTestAnswerRepository {
	return &assessmentTestAnswerRepository{db: db}
}

// Insert 首写时间显式 UTC（CreateWithLink 同范式）：autoCreate/autoUpdate 回调
// 填本地时区会让同列混存两种偏移串。
func (r *assessmentTestAnswerRepository) Insert(ctx context.Context, row *domain.AssessmentTestAnswer) error {
	row.CreatedAt, row.UpdatedAt = utcNow(), utcNow()
	return r.db.WithContext(ctx).Create(row).Error
}

// CountByTask 服务端进度权威的计数来源（specs §5.2.4 规则1：当前题号由已落库
// 记录数推算），idx_task_seq 前缀命中。
func (r *assessmentTestAnswerRepository) CountByTask(ctx context.Context, taskID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.AssessmentTestAnswer{}).
		Where("task_id = ?", taskID).
		Count(&n).Error
	return n, err
}

// ListByTask question_seq 升序：A1 断点恢复按题号有序返回，阅卷按序拼接对话段。
func (r *assessmentTestAnswerRepository) ListByTask(ctx context.Context, taskID int64) ([]domain.AssessmentTestAnswer, error) {
	var rows []domain.AssessmentTestAnswer
	err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("question_seq ASC").
		Find(&rows).Error
	return rows, err
}

// FindLinkByTokenHash answer 域对 F7 链接表的只读点查，valid 判定归 service。
func (r *assessmentTestAnswerRepository) FindLinkByTokenHash(ctx context.Context, tokenHash string) (*domain.AssessmentTestLink, error) {
	var link domain.AssessmentTestLink
	err := r.db.WithContext(ctx).
		Where("token_hash = ?", tokenHash).
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}
