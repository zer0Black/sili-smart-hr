// AssessmentTestResultRepository 承载九型判型结果的幂等读写（specs P2_TST_001
// §5.2.4 规则1，04 §3.3）：一任务至多一行，撞 uk_result_task upsert 全业务列覆盖。
package repository

import (
	"context"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AssessmentTestResultRepository 是九型判型结果的数据访问接口。
type AssessmentTestResultRepository interface {
	// UpsertByTaskID 撞 uk_result_task 时 UPDATE 全业务列（列名形态，空串照写），
	// 重试与补偿收敛到同一份结果（specs §5.2.4 规则1 幂等）。
	UpsertByTaskID(ctx context.Context, r *domain.AssessmentTestResult) error
	// FindBatchByTaskIDs 按任务 ID 集批量读，返回 task_id → 结果行映射；
	// 无结果的任务不出现在 map，空集返回空 map。
	FindBatchByTaskIDs(ctx context.Context, taskIDs []int64) (map[int64]domain.AssessmentTestResult, error)
}

type assessmentTestResultRepository struct {
	db *gorm.DB
}

// NewAssessmentTestResultRepository 返回 AssessmentTestResultRepository 接口实现。
func NewAssessmentTestResultRepository(db *gorm.DB) AssessmentTestResultRepository {
	return &assessmentTestResultRepository{db: db}
}

// UpsertByTaskID clause.OnConflict upsert（与 dimension_score.SaveAll 同范式）：
// DoUpdates 显式列清单不被零值跳过，degraded 占位空串照写覆盖。
func (r *assessmentTestResultRepository) UpsertByTaskID(ctx context.Context, res *domain.AssessmentTestResult) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "task_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"main_type", "wing_type", "distribution_json", "rationale",
				"model_name", "prompt_version", "grading_status", "updated_at",
			}),
		}).
		Create(res).Error
}

// FindBatchByTaskIDs task_id IN (?) 一次取回，Go 侧组装 map（uk_result_task
// 保证至多一行，无归组歧义）。
func (r *assessmentTestResultRepository) FindBatchByTaskIDs(ctx context.Context, taskIDs []int64) (map[int64]domain.AssessmentTestResult, error) {
	out := make(map[int64]domain.AssessmentTestResult, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	var list []domain.AssessmentTestResult
	if err := r.db.WithContext(ctx).
		Where("task_id IN ?", taskIDs).
		Find(&list).Error; err != nil {
		return nil, err
	}
	for _, row := range list {
		out[row.TaskID] = row
	}
	return out, nil
}
