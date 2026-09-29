// Package repository_test 对主动测试判型结果仓储做黑盒集成测试（specs TST §5.2.4/§5.2.5）。
// 覆盖 UpsertByTaskID 幂等收敛（uk_result_task）与 FindBatchByTaskIDs 批量现读。
package repository_test

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newTestResultDB 在 newTestTaskDB（task/link/question 三模型）之上补判型结果表
// 与其雪花 Create 回调。
func newTestResultDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newTestTaskDB(t)
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_testrepo_result", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if dest, ok := tx.Statement.Dest.(*domain.AssessmentTestResult); ok && dest.ID == 0 {
			dest.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentTestResult{}); err != nil {
		t.Fatalf("auto migrate result: %v", err)
	}
	return db
}

// seedResult 直插一行判型结果（测试基线，不走被测 upsert 通道）。
func seedResult(t *testing.T, db *gorm.DB, taskID int64, mainType string) domain.AssessmentTestResult {
	t.Helper()
	r := domain.AssessmentTestResult{
		TaskID: taskID, MainType: mainType, WingType: "",
		DistributionJSON: `{"1":5.0}`, Rationale: "判定-" + mainType,
		ModelName: "m-" + mainType, PromptVersion: "v1", GradingStatus: domain.GradingStatusScored,
	}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed result task %d: %v", taskID, err)
	}
	return r
}

// countResultRows 直查结果行数。
func countResultRows(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&domain.AssessmentTestResult{}).Where(query, args...).Count(&n).Error; err != nil {
		t.Fatalf("count results: %v", err)
	}
	return n
}

// TestUpsertByTaskID 同 taskID 两次 Upsert 后仅一行且取第二次值（BR3 §5.2.4 规则1
// 幂等收敛：重试与补偿收敛到同一份结果，全业务列覆盖）。
func TestUpsertByTaskID(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	ctx := context.Background()

	first := domain.AssessmentTestResult{
		TaskID: 1001, MainType: "3", WingType: "2",
		DistributionJSON: `{"1":5.0,"3":45.5}`, Rationale: "first",
		ModelName: "m-1", PromptVersion: "v1", GradingStatus: domain.GradingStatusScored,
	}
	if err := repo.UpsertByTaskID(ctx, &first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("snowflake id not assigned")
	}

	second := domain.AssessmentTestResult{
		TaskID: 1001, MainType: "9", WingType: "1",
		DistributionJSON: `{"9":52.3}`, Rationale: "second",
		ModelName: "m-2", PromptVersion: "v1", GradingStatus: domain.GradingStatusDegraded,
	}
	if err := repo.UpsertByTaskID(ctx, &second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if n := countResultRows(t, db, "task_id = ?", int64(1001)); n != 1 {
		t.Fatalf("rows for task 1001 = %d, want 1", n)
	}
	var got domain.AssessmentTestResult
	if err := db.Where("task_id = ?", int64(1001)).First(&got).Error; err != nil {
		t.Fatalf("load converged result: %v", err)
	}
	if got.MainType != "9" || got.WingType != "1" || got.DistributionJSON != `{"9":52.3}` ||
		got.Rationale != "second" || got.ModelName != "m-2" ||
		got.GradingStatus != domain.GradingStatusDegraded {
		t.Fatalf("second upsert values not converged: %+v", got)
	}

	// 异任务互不干扰：另一 taskID 首次 upsert 独立成行。
	other := domain.AssessmentTestResult{
		TaskID: 1002, MainType: "5", DistributionJSON: `{"5":61.2}`,
		Rationale: "other", PromptVersion: "v1", GradingStatus: domain.GradingStatusScored,
	}
	if err := repo.UpsertByTaskID(ctx, &other); err != nil {
		t.Fatalf("other upsert: %v", err)
	}
	if n := countResultRows(t, db, "task_id = ?", int64(1002)); n != 1 {
		t.Fatalf("rows for task 1002 = %d, want 1", n)
	}
}

// TestFindBatchByTaskIDs 批量读命中、无命中与空输入三态：空 map 不炸（F9 画像按
// 任务集批量消费路径）。
func TestFindBatchByTaskIDs(t *testing.T) {
	db := newTestResultDB(t)
	repo := repository.NewAssessmentTestResultRepository(db)
	ctx := context.Background()
	seedResult(t, db, 2001, "3")
	seedResult(t, db, 2002, "9")

	got, err := repo.FindBatchByTaskIDs(ctx, []int64{2001, 2002})
	if err != nil {
		t.Fatalf("FindBatchByTaskIDs: %v", err)
	}
	if len(got) != 2 || got[2001].MainType != "3" || got[2002].MainType != "9" {
		t.Fatalf("batch map = %v, want both rows keyed by task_id", got)
	}

	miss, err := repo.FindBatchByTaskIDs(ctx, []int64{9999})
	if err != nil || len(miss) != 0 {
		t.Fatalf("missing ids: map=%v err=%v, want empty nil", miss, err)
	}
	empty, err := repo.FindBatchByTaskIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("nil ids: map=%v err=%v, want empty nil", empty, err)
	}
}

// TestDegradeTaskTransactional 降级两步同事务（specs §5.2.5）：enneagram 耗尽
// 落降级行（判型占位、rationale 记降级说明）+ 任务行 grading→degraded；已有
// scored 判型行的任务降级时降级行覆盖旧值（upsert 全列）；ai_mgmt 只推任务行。
// 任务行不在 grading 前置态时（终态守卫语义）降级行照落但任务行不推进。
func TestDegradeTaskTransactional(t *testing.T) {
	ctx := context.Background()

	seedGradingTask := func(t *testing.T, db *gorm.DB, taskNo, testType, grading string) domain.AssessmentTestTask {
		t.Helper()
		task := domain.AssessmentTestTask{
			TaskNo: taskNo, TestType: testType, StaffID: "u-1", StaffName: "张敏",
			Status: domain.TestTaskStatusCompleted, GradingStatus: grading,
			QuestionIDsJSON: "[]", DimensionCodesJSON: "[]",
		}
		if err := db.Create(&task).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
		return task
	}

	t.Run("enneagram 两步同事务", func(t *testing.T) {
		db := newTestResultDB(t)
		repo := repository.NewAssessmentTestResultRepository(db)
		task := seedGradingTask(t, db, "E202609280001", domain.TestTypeEnneagram, domain.GradingStatusGrading)
		if err := repo.DegradeTask(ctx, task.ID, true); err != nil {
			t.Fatalf("DegradeTask: %v", err)
		}
		var row domain.AssessmentTestResult
		if err := db.Where("task_id = ?", task.ID).First(&row).Error; err != nil {
			t.Fatalf("降级行未落库: %v", err)
		}
		if row.MainType != "" || row.WingType != "" || row.DistributionJSON != "" {
			t.Errorf("判型字段应占位空值, got %q/%q/%q", row.MainType, row.WingType, row.DistributionJSON)
		}
		if row.Rationale == "" || row.GradingStatus != domain.GradingStatusDegraded {
			t.Errorf("rationale/grading_status = %q/%s, want 降级说明/degraded", row.Rationale, row.GradingStatus)
		}
		var after domain.AssessmentTestTask
		if err := db.First(&after, task.ID).Error; err != nil {
			t.Fatalf("load task: %v", err)
		}
		if after.GradingStatus != domain.GradingStatusDegraded {
			t.Fatalf("任务行 grading_status = %s, want degraded", after.GradingStatus)
		}
	})

	t.Run("ai_mgmt 仅推任务行", func(t *testing.T) {
		db := newTestResultDB(t)
		repo := repository.NewAssessmentTestResultRepository(db)
		task := seedGradingTask(t, db, "T202609280001", domain.TestTypeAIMgmt, domain.GradingStatusGrading)
		if err := repo.DegradeTask(ctx, task.ID, false); err != nil {
			t.Fatalf("DegradeTask: %v", err)
		}
		if n := countResultRows(t, db, "task_id = ?", task.ID); n != 0 {
			t.Fatalf("ai_mgmt 不落判型行, got %d", n)
		}
		var after domain.AssessmentTestTask
		db.First(&after, task.ID)
		if after.GradingStatus != domain.GradingStatusDegraded {
			t.Fatalf("任务行应 degraded, got %s", after.GradingStatus)
		}
	})

	t.Run("重复降级幂等收敛", func(t *testing.T) {
		db := newTestResultDB(t)
		repo := repository.NewAssessmentTestResultRepository(db)
		task := seedGradingTask(t, db, "E202609280002", domain.TestTypeEnneagram, domain.GradingStatusGrading)
		for i := 0; i < 2; i++ {
			if err := repo.DegradeTask(ctx, task.ID, true); err != nil {
				t.Fatalf("DegradeTask %d: %v", i+1, err)
			}
		}
		if n := countResultRows(t, db, "task_id = ?", task.ID); n != 1 {
			t.Fatalf("重复降级应收敛一行, got %d", n)
		}
	})
}
