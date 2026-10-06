// WorkspaceQueryRepository 承载工作台域只读聚合查询（specs P2_WRK_001
// §5.1.3、03 §4.2），仿 DashboardQueryRepository 形态。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// WorkspaceQueryRepository 是工作台聚合只读查询接口。
type WorkspaceQueryRepository interface {
	// ListLatestBatch assessment_batches triggered_at 最新行；表空返 (nil, nil)。
	ListLatestBatch(ctx context.Context) (*domain.AssessmentBatch, error)
	// ListByPeriodBounds period 双界匹配全部批次行（trigger_type 不限，03 §1.4）。
	ListByPeriodBounds(ctx context.Context, start, end int64) ([]domain.AssessmentBatch, error)
	// ExistsAlertByBatchIDs assessment_alerts batch_id IN 存在性（任意一行存在即 true）。
	ExistsAlertByBatchIDs(ctx context.Context, batchIDs []int64) (bool, error)
	// CountExpiredTasks assessment_test_tasks status=expired 全表计数（两类合计）。
	CountExpiredTasks(ctx context.Context) (int64, error)
	// ListAllActivity activity_stats 全表行，列级投影 token_name/period_end_at/active_level。
	ListAllActivity(ctx context.Context) ([]domain.ActivityStat, error)
	// ListAllLatestScored 全员最新 scored 判型行（经 tasks JOIN，免名单参数，
	// 过滤 grading_status=scored 且 main_type <> ''，形态复用
	// ListLatestScoredByStaffNames，返回 map[staff_name]domain.AssessmentTestResult）。
	ListAllLatestScored(ctx context.Context) (map[string]domain.AssessmentTestResult, error)
}

type workspaceQueryRepository struct {
	db *gorm.DB
}

// NewWorkspaceQueryRepository 返回 WorkspaceQueryRepository 接口实现。
func NewWorkspaceQueryRepository(db *gorm.DB) WorkspaceQueryRepository {
	return &workspaceQueryRepository{db: db}
}

func (r *workspaceQueryRepository) ListLatestBatch(ctx context.Context) (*domain.AssessmentBatch, error) {
	var batches []domain.AssessmentBatch
	err := r.db.WithContext(ctx).
		Order("triggered_at DESC").
		Limit(1).
		Find(&batches).Error
	if err != nil {
		return nil, err
	}
	if len(batches) == 0 {
		return nil, nil
	}
	return &batches[0], nil
}

func (r *workspaceQueryRepository) ListByPeriodBounds(ctx context.Context, start, end int64) ([]domain.AssessmentBatch, error) {
	var list []domain.AssessmentBatch
	err := r.db.WithContext(ctx).
		Where("period_start_at = ? AND period_end_at = ?",
			time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *workspaceQueryRepository) ExistsAlertByBatchIDs(ctx context.Context, batchIDs []int64) (bool, error) {
	if len(batchIDs) == 0 {
		return false, nil
	}
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.AssessmentAlert{}).
		Where("batch_id IN ?", batchIDs).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *workspaceQueryRepository) CountExpiredTasks(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.AssessmentTestTask{}).
		Where("status = ?", domain.TestTaskStatusExpired).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListAllActivity Select 指定列时 GORM 只扫描投影字段，未投影列为零值
//（调用方仅消费三列，03 §4.2）。
func (r *workspaceQueryRepository) ListAllActivity(ctx context.Context) ([]domain.ActivityStat, error) {
	var list []domain.ActivityStat
	err := r.db.WithContext(ctx).
		Model(&domain.ActivityStat{}).
		Select("token_name", "period_end_at", "active_level").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListAllLatestScored 复制 ListLatestScoredByStaffNames 两段查询，第一段只按
// test_type=enneagram 圈任务（免名单参数）；同人取 created_at 最新 scored 行，
// 降级占位行（main_type 空串）被 WHERE 过滤。
func (r *workspaceQueryRepository) ListAllLatestScored(ctx context.Context) (map[string]domain.AssessmentTestResult, error) {
	latest := make(map[string]domain.AssessmentTestResult)
	var tasks []domain.AssessmentTestTask
	if err := r.db.WithContext(ctx).
		Where("test_type = ?", domain.TestTypeEnneagram).
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
