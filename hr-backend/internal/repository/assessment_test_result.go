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
	// DegradeTask 降级终态单事务：enneagram 先落降级行再推任务行 grading_status
	// degraded（ai_mgmt 无判型行仅推任务行），两步原子防半降级（specs §5.2.5）。
	DegradeTask(ctx context.Context, taskID int64, enneagram bool) error
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

// resultUpsertColumns 撞 uk_result_task 时覆盖的业务列（空串照写，degraded
// 占位值不被零值跳过）。
var resultUpsertColumns = []string{
	"main_type", "wing_type", "distribution_json", "rationale",
	"model_name", "prompt_version", "grading_status", "updated_at",
}

// UpsertByTaskID clause.OnConflict upsert（与 dimension_score.SaveAll 同范式）：
// DoUpdates 显式列清单不被零值跳过，degraded 占位空串照写覆盖。
// 首写时间显式 UTC（session_feature 范式），防同列混存偏移串。
func (r *assessmentTestResultRepository) UpsertByTaskID(ctx context.Context, res *domain.AssessmentTestResult) error {
	res.CreatedAt, res.UpdatedAt = utcNow(), utcNow()
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}},
			DoUpdates: clause.AssignmentColumns(resultUpsertColumns),
		}).
		Create(res).Error
}

// resultDegradeRationale 降级行判定依据（04 §3.3：rationale 记降级说明）。
const resultDegradeRationale = "AI 阅卷重试耗尽，已降级终态；作答数据与统计上下文保留，可重新发起测试补偿。"

// DegradeTask 降级两步同事务：enneagram 先 upsert 降级行（判型字段占位、
// rationale 记降级说明）再条件更新任务行 grading→degraded，任一失败整体回滚
// 防「降级行落库但任务停 grading」或反序半态（specs §5.2.5）。
func (r *assessmentTestResultRepository) DegradeTask(ctx context.Context, taskID int64, enneagram bool) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if enneagram {
			row := &domain.AssessmentTestResult{
				TaskID:        taskID,
				Rationale:     resultDegradeRationale,
				GradingStatus: domain.GradingStatusDegraded,
			}
			if err := upsertResult(tx, row); err != nil {
				return err
			}
		}
		return tx.Model(&domain.AssessmentTestTask{}).
			Where("id = ? AND grading_status = ?", taskID, domain.GradingStatusGrading).
			Updates(map[string]any{
				"grading_status": domain.GradingStatusDegraded,
				"updated_at":     utcNow(),
			}).Error
	})
}

// upsertResult 事务通道内的结果行 upsert（DegradeTask 复用 UpsertByTaskID 形态）。
func upsertResult(tx *gorm.DB, row *domain.AssessmentTestResult) error {
	row.CreatedAt, row.UpdatedAt = utcNow(), utcNow()
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "task_id"}},
		DoUpdates: clause.AssignmentColumns(resultUpsertColumns),
	}).Create(row).Error
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
