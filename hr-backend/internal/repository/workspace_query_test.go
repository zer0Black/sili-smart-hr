// Package repository_test 对工作台域只读聚合查询仓储做黑盒集成测试
//（specs P2_WRK_001 §5.1.3、03 §4.2）。
// 覆盖六方法：最新批次、period 双界批次、告警存在性、逾期计数、全量活跃度
// 投影、全员最新 scored 判型。SQLite :memory: 种子行形态参照 dashboard_query_test.go
// 与 profile_query_test.go。
package repository_test

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/glebarez/sqlite"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// wrkBaseUnix 工作台测试基准 Unix 秒。
const wrkBaseUnix = int64(1762000000)

// newWorkspaceTestDB 构造独立 :memory: SQLite，AutoMigrate 工作台域消费五表，
// 雪花回调覆盖全部种子模型（单行与切片两种 Dest）。
func newWorkspaceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_workspace_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assign := func(id *int64) {
			if *id == 0 {
				*id = snowflake.NextID()
			}
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.AssessmentBatch:
			assign(&dest.ID)
		case *domain.AssessmentAlert:
			assign(&dest.ID)
		case *domain.AssessmentTestTask:
			assign(&dest.ID)
		case *domain.AssessmentTestResult:
			assign(&dest.ID)
		case *domain.ActivityStat:
			assign(&dest.ID)
		case *[]domain.AssessmentBatch:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.AssessmentAlert:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.AssessmentTestTask:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.AssessmentTestResult:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		case *[]domain.ActivityStat:
			for i := range *dest {
				assign(&(*dest)[i].ID)
			}
		}
	})
	if err := db.AutoMigrate(
		&domain.AssessmentBatch{},
		&domain.AssessmentAlert{},
		&domain.AssessmentTestTask{},
		&domain.AssessmentTestResult{},
		&domain.ActivityStat{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeWorkspaceBatch 构造批次行：triggered_at/finished_at 由测试另行覆盖。
func makeWorkspaceBatch(batchNo, triggerType, status string, start, end, triggeredAt int64) domain.AssessmentBatch {
	return domain.AssessmentBatch{
		BatchNo:         batchNo,
		TriggerType:     triggerType,
		TargetMode:      domain.BatchTargetAll,
		TargetNamesJSON: `[]`,
		TotalCount:      3,
		Status:          status,
		PeriodStartAt:   time.Unix(start, 0).UTC(),
		PeriodEndAt:     time.Unix(end, 0).UTC(),
		TriggeredAt:     time.Unix(triggeredAt, 0).UTC(),
	}
}

// makeWorkspaceTask 构造测试任务行：status 由测试指定。
func makeWorkspaceTask(taskNo, testType, staffName, status string, createdAt time.Time) domain.AssessmentTestTask {
	return domain.AssessmentTestTask{
		TaskNo: taskNo, TestType: testType, StaffID: "u-" + staffName, StaffName: staffName,
		Status: status, GradingStatus: domain.GradingStatusScored,
		QuestionIDsJSON: "[]", DimensionCodesJSON: "[]",
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

// makeWorkspaceResult 构造判型结果行。
func makeWorkspaceResult(taskID int64, mainType, grading string, createdAt time.Time) domain.AssessmentTestResult {
	return domain.AssessmentTestResult{
		TaskID: taskID, MainType: mainType, WingType: "2",
		DistributionJSON: `{"3":45.5}`, Rationale: "判型依据",
		ModelName: "test-model", PromptVersion: "v1", GradingStatus: grading,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

// TestListLatestBatch 核心断言：两条批次 triggered_at 一前一后返回较新行；
// 空表返回 (nil, nil)（03 §4.2 回退与空态判定输入）。
func TestListLatestBatch(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	start, end := wrkBaseUnix, wrkBaseUnix+7*86400

	early := makeWorkspaceBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, start, end, start)
	late := makeWorkspaceBatch("B002", domain.BatchTriggerManual, domain.BatchStatusRunning, start, end, start+86400)
	for _, b := range []domain.AssessmentBatch{early, late} {
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("seed %s: %v", b.BatchNo, err)
		}
	}

	got, err := repo.ListLatestBatch(ctx)
	if err != nil {
		t.Fatalf("ListLatestBatch: %v", err)
	}
	if got == nil || got.BatchNo != "B002" {
		t.Fatalf("want triggered_at 最新行 B002, got %+v", got)
	}

	emptyDB := newWorkspaceTestDB(t)
	emptyRepo := repository.NewWorkspaceQueryRepository(emptyDB)
	none, err := emptyRepo.ListLatestBatch(ctx)
	if err != nil {
		t.Fatalf("空表 want nil error, got %v", err)
	}
	if none != nil {
		t.Fatalf("空表 want nil, got %+v", none)
	}
}

// TestListByPeriodBounds 核心断言：running + success 两批次同区间返回 2 行，
// 他区间批次隔离（03 §4.2 status 定位与批次 ID 集输入，trigger_type 不限）。
func TestListByPeriodBounds(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	start, end := wrkBaseUnix, wrkBaseUnix+7*86400

	seeds := []domain.AssessmentBatch{
		makeWorkspaceBatch("B001", domain.BatchTriggerScheduled, domain.BatchStatusRunning, start, end, start),
		makeWorkspaceBatch("B002", domain.BatchTriggerManual, domain.BatchStatusSuccess, start, end, start+3600),
		makeWorkspaceBatch("B003", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, end, end+7*86400, start+7200),
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	list, err := repo.ListByPeriodBounds(ctx, start, end)
	if err != nil {
		t.Fatalf("ListByPeriodBounds: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2, got %d: %+v", len(list), list)
	}
	for _, row := range list {
		if !row.PeriodStartAt.Equal(time.Unix(start, 0).UTC()) || !row.PeriodEndAt.Equal(time.Unix(end, 0).UTC()) {
			t.Fatalf("混入非本区间行: %v~%v", row.PeriodStartAt, row.PeriodEndAt)
		}
	}

	none, err := repo.ListByPeriodBounds(ctx, end+7*86400, end+14*86400)
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(none))
	}
}

// TestExistsAlertByBatchIDs 核心断言：写入一条告警行 batch_id=1，传 [1] 期待 true，
// 传 [2] 期待 false，传空切片期待 false 且无 SQL 执行（03 §4.2 alert_count 0/1）。
func TestExistsAlertByBatchIDs(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()

	alert := domain.AssessmentAlert{
		BatchID: 1, BatchNo: "B001", FailedCount: 2, TotalCount: 10,
		FailedRatio: 20.0, SignaledAt: time.Unix(wrkBaseUnix, 0).UTC(),
	}
	if err := db.Create(&alert).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	hit, err := repo.ExistsAlertByBatchIDs(ctx, []int64{1})
	if err != nil {
		t.Fatalf("传 [1]: %v", err)
	}
	if !hit {
		t.Fatal("传 [1] want true")
	}
	miss, err := repo.ExistsAlertByBatchIDs(ctx, []int64{2})
	if err != nil {
		t.Fatalf("传 [2]: %v", err)
	}
	if miss {
		t.Fatal("传 [2] want false")
	}

	// 空批次集直接返 false 免查：以关闭连接的库断言不发起 SQL。
	closed := newWorkspaceTestDB(t)
	if sqlDB, err := closed.DB(); err == nil {
		_ = sqlDB.Close()
	}
	closedRepo := repository.NewWorkspaceQueryRepository(closed)
	empty, err := closedRepo.ExistsAlertByBatchIDs(ctx, []int64{})
	if err != nil {
		t.Fatalf("空切片 want nil error（无 SQL 执行）, got %v", err)
	}
	if empty {
		t.Fatal("空切片 want false")
	}
}

// TestCountExpiredTasks 核心断言：expired 2 行 + pending 1 行返回 2
//（specs §5.1.3 测试任务：已逾期状态计数）。
func TestCountExpiredTasks(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	base := time.Unix(wrkBaseUnix, 0).UTC()

	seeds := []domain.AssessmentTestTask{
		makeWorkspaceTask("E202610010001", domain.TestTypeEnneagram, "张敏", domain.TestTaskStatusExpired, base),
		makeWorkspaceTask("T202610010002", domain.TestTypeAIMgmt, "李四", domain.TestTaskStatusExpired, base.Add(time.Hour)),
		makeWorkspaceTask("T202610010003", domain.TestTypeAIMgmt, "王五", domain.TestTaskStatusPending, base.Add(2*time.Hour)),
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	n, err := repo.CountExpiredTasks(ctx)
	if err != nil {
		t.Fatalf("CountExpiredTasks: %v", err)
	}
	if n != 2 {
		t.Fatalf("expired 计数 want 2, got %d", n)
	}

	emptyDB := newWorkspaceTestDB(t)
	emptyRepo := repository.NewWorkspaceQueryRepository(emptyDB)
	zero, err := emptyRepo.CountExpiredTasks(ctx)
	if err != nil {
		t.Fatalf("空表 want nil error, got %v", err)
	}
	if zero != 0 {
		t.Fatalf("空表 want 0, got %d", zero)
	}
}

// TestListAllActivity 核心断言：跨两期三行返回 3 行且 TokenName/PeriodEndAt/
// ActiveLevel 有值（03 §4.2 列级投影）。
func TestListAllActivity(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	p1, p2 := wrkBaseUnix, wrkBaseUnix+7*86400

	seeds := []domain.ActivityStat{
		makeActivityRow("张敏", p1, p1+7*86400, 20),
		makeActivityRow("李四", p1, p1+7*86400, 5),
		makeActivityRow("张敏", p2, p2+7*86400, 12),
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	list, err := repo.ListAllActivity(context.Background())
	if err != nil {
		t.Fatalf("ListAllActivity: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("返回行数 want 3, got %d: %+v", len(list), list)
	}
	for _, row := range list {
		if row.TokenName == "" {
			t.Fatalf("token_name 投影列应有值, got %+v", row)
		}
		if row.PeriodEndAt.IsZero() {
			t.Fatalf("period_end_at 投影列应有值, got %+v", row)
		}
		if row.ActiveLevel == "" {
			t.Fatalf("active_level 投影列应有值, got %+v", row)
		}
	}

	emptyDB := newWorkspaceTestDB(t)
	emptyRepo := repository.NewWorkspaceQueryRepository(emptyDB)
	none, err := emptyRepo.ListAllActivity(context.Background())
	if err != nil {
		t.Fatalf("空表 want nil error, got %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("空表 want 空列表, got %d 行", len(none))
	}
}

// TestListAllLatestScored 核心断言：同人两任务各一 scored 行返回最新 created_at 行，
// 降级占位行（main_type 空串）被过滤，ai_mgmt 任务不参与，空表返回空 map
//（03 §4.2 全员最新 scored 判型行，免名单参数）。
func TestListAllLatestScored(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	oldAt := time.Unix(wrkBaseUnix, 0).UTC()
	newAt := oldAt.Add(7 * 86400 * time.Second)

	// 张敏：两任务均 scored，取最新 created_at 行。
	zmOld := seedEnneagramTask(t, db, "张敏", oldAt)
	zmNew := seedEnneagramTask(t, db, "张敏", newAt)
	// 王五：旧任务 scored、新任务降级占位（main_type 空串），取旧 scored 行。
	// 任务号由 createdAt 生成，时间错开一小时避免与张敏撞号。
	wwOld := seedEnneagramTask(t, db, "王五", oldAt.Add(time.Hour))
	wwNew := seedEnneagramTask(t, db, "王五", newAt.Add(time.Hour))

	seeds := []domain.AssessmentTestResult{
		makeWorkspaceResult(zmOld.ID, "3", domain.GradingStatusScored, oldAt),
		makeWorkspaceResult(zmNew.ID, "5", domain.GradingStatusScored, newAt),
		makeWorkspaceResult(wwOld.ID, "7", domain.GradingStatusScored, oldAt),
		makeWorkspaceResult(wwNew.ID, "", domain.GradingStatusDegraded, newAt), // 降级占位：过滤
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed result %d: %v", i, err)
		}
	}

	got, err := repo.ListAllLatestScored(ctx)
	if err != nil {
		t.Fatalf("ListAllLatestScored: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("map 大小 want 2, got %d: %+v", len(got), got)
	}
	zm, ok := got["张敏"]
	if !ok {
		t.Fatal("map[张敏] 不存在")
	}
	if zm.TaskID != zmNew.ID || zm.MainType != "5" {
		t.Fatalf("张敏 want 最新 scored 行 (task=%d main=5), got task=%d main=%q", zmNew.ID, zm.TaskID, zm.MainType)
	}
	ww, ok := got["王五"]
	if !ok {
		t.Fatal("map[王五] 不存在")
	}
	if ww.TaskID != wwOld.ID || ww.MainType != "7" {
		t.Fatalf("王五 want 降级占位被过滤取旧 scored 行 (task=%d main=7), got task=%d main=%q", wwOld.ID, ww.TaskID, ww.MainType)
	}

	emptyDB := newWorkspaceTestDB(t)
	emptyRepo := repository.NewWorkspaceQueryRepository(emptyDB)
	none, err := emptyRepo.ListAllLatestScored(ctx)
	if err != nil {
		t.Fatalf("空表 want nil error, got %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("空表 want 空 map, got %d 项", len(none))
	}
}

// TestListAllLatestScored_ExcludesAIMgmt ai_mgmt 任务同名同人：JOIN 圈任务时排除，
// 仅 enneagram 任务判型行进 map（03 §4.2 复用 ListLatestScoredByStaffNames 口径）。
func TestListAllLatestScored_ExcludesAIMgmt(t *testing.T) {
	db := newWorkspaceTestDB(t)
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	createdAt := time.Unix(wrkBaseUnix, 0).UTC()

	aiMgmt := makeWorkspaceTask("T202610010001", domain.TestTypeAIMgmt, "张敏", domain.TestTaskStatusCompleted, createdAt)
	if err := db.Create(&aiMgmt).Error; err != nil {
		t.Fatalf("seed ai_mgmt task: %v", err)
	}
	row := makeWorkspaceResult(aiMgmt.ID, "3", domain.GradingStatusScored, createdAt)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed result: %v", err)
	}

	got, err := repo.ListAllLatestScored(ctx)
	if err != nil {
		t.Fatalf("ListAllLatestScored: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ai_mgmt 任务行不应进 map, got %+v", got)
	}
}

// TestWorkspaceQueryErrorClosedConnection 连接关闭后六方法错误透传
//（specs §5.1.4 规则2：查询失败由 service 层做区块降级）。
func TestWorkspaceQueryErrorClosedConnection(t *testing.T) {
	db := newWorkspaceTestDB(t)
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	repo := repository.NewWorkspaceQueryRepository(db)
	ctx := context.Background()
	start, end := wrkBaseUnix, wrkBaseUnix+7*86400

	if _, err := repo.ListLatestBatch(ctx); err == nil {
		t.Fatal("ListLatestBatch 关闭连接 want error")
	}
	if _, err := repo.ListByPeriodBounds(ctx, start, end); err == nil {
		t.Fatal("ListByPeriodBounds 关闭连接 want error")
	}
	if _, err := repo.ExistsAlertByBatchIDs(ctx, []int64{1}); err == nil {
		t.Fatal("ExistsAlertByBatchIDs 关闭连接 want error")
	}
	if _, err := repo.CountExpiredTasks(ctx); err == nil {
		t.Fatal("CountExpiredTasks 关闭连接 want error")
	}
	if _, err := repo.ListAllActivity(ctx); err == nil {
		t.Fatal("ListAllActivity 关闭连接 want error")
	}
	if _, err := repo.ListAllLatestScored(ctx); err == nil {
		t.Fatal("ListAllLatestScored 关闭连接 want error")
	}
}
