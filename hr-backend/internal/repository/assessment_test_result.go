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
	// ListLatestScoredByStaffNames 批量取每人最新 scored 判型行：JOIN
	// assessment_test_tasks（staff_name IN + test_type=enneagram）圈任务，再逐任务
	// 取 grading_status='scored' 且 main_type <> '' 的最新行（created_at DESC，同人
	// 多任务取最新），降级占位行视为无判型（03 §1.8）。
	ListLatestScoredByStaffNames(ctx context.Context, staffNames []string) (map[string]domain.AssessmentTestResult, error)
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
	return upsertResult(r.db.WithContext(ctx), res)
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

// upsertResult 结果行 upsert 单点实现：首写时间显式 UTC（session_feature 范式），
// UpsertByTaskID 与 DegradeTask 事务通道共用，防两处 OnConflict 列清单漂移。
func upsertResult(db *gorm.DB, row *domain.AssessmentTestResult) error {
	row.CreatedAt, row.UpdatedAt = utcNow(), utcNow()
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "task_id"}},
		DoUpdates: clause.AssignmentColumns(resultUpsertColumns),
	}).Create(row).Error
}

// ListLatestScoredByStaffNames 两段查询（04 §3：tasks 按 idx_staff_name 圈
// staff_name IN + enneagram 任务主键，results 按 task_id IN 取 scored 且
// main_type<>''，同人取 created_at 最新，降级行被过滤）。批量 IN 单条 SQL，
// 禁止逐人查询（specs P2_PRF_001 §5.1.4 规则2）。
func (r *assessmentTestResultRepository) ListLatestScoredByStaffNames(ctx context.Context, staffNames []string) (map[string]domain.AssessmentTestResult, error) {
	latest := make(map[string]domain.AssessmentTestResult)
	if len(staffNames) == 0 {
		return latest, nil
	}
	var tasks []domain.AssessmentTestTask
	if err := r.db.WithContext(ctx).
		Where("staff_name IN ? AND test_type = ?", staffNames, domain.TestTypeEnneagram).
		Find(&tasks).Error; err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return latest, nil
	}
	taskStaff := make(map[int64]string, len(tasks))
	taskIDs := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		taskStaff[t.ID] = t.StaffName
		taskIDs = append(taskIDs, t.ID)
	}
	var rows []domain.AssessmentTestResult
	if err := r.db.WithContext(ctx).
		Where("task_id IN ? AND grading_status = ? AND main_type <> ''",
			taskIDs, domain.GradingStatusScored).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	// created_at ASC 遍历，同人后行覆盖前行，收敛为每人最新 scored 行。
	for _, row := range rows {
		staff, ok := taskStaff[row.TaskID]
		if !ok {
			continue
		}
		latest[staff] = row
	}
	return latest, nil
}
