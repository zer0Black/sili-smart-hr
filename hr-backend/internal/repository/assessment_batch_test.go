// Package repository_test 对 assessment_batch 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并注册
// Create 回调处理 AssessmentBatch 与 AssessmentBatchPerson（含 slice 途径）。
//
// 核心断言（BR1/BR2/BR3/BR4/BR6）：
//   - AdvancePersonTerminal 成功侧累计会话、失败侧不计（specs §5.2.4 规则3）
//   - AdvancePersonTerminal 重入幂等：批次计数不双计（specs §5.2.4 规则2）
//   - FinalizeBatch 终态不可逆：affected==0 幂等返回 nil（specs §6.2）
//   - FailWholeBatch 整批失败：人员行全 failed、批次计数置满 N/N（specs §5.2.5）
package repository_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// batchBaseUnix 批次测试基准 Unix 秒（独立于评分域基准，语义各自承载）。
const batchBaseUnix = int64(1757800000)

// newBatchTestDB 构造独立 :memory: SQLite，AutoMigrate 批次域两表。
func newBatchTestDB(t *testing.T) *gorm.DB {
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
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_batch_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.AssessmentBatch:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *domain.AssessmentBatchPerson:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *[]domain.AssessmentBatchPerson:
			for i := range *dest {
				if (*dest)[i].ID == 0 {
					(*dest)[i].ID = snowflake.NextID()
				}
			}
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentBatch{}, &domain.AssessmentBatchPerson{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// newBatch 构造字段完整的 running 批次。
func newBatch(batchNo, triggerType string, total int) domain.AssessmentBatch {
	return domain.AssessmentBatch{
		BatchNo:         batchNo,
		TriggerType:     triggerType,
		TargetMode:      domain.BatchTargetAll,
		TargetNamesJSON: `[]`,
		TotalCount:      total,
		Status:          domain.BatchStatusRunning,
		PeriodStartAt:   time.Unix(batchBaseUnix, 0).UTC(),
		PeriodEndAt:     time.Unix(batchBaseUnix+7*86400, 0).UTC(),
		TriggeredAt:     time.Unix(batchBaseUnix, 0).UTC(),
	}
}

// createBatchWithPersons 创建批次与其 pending 人员行。
func createBatchWithPersons(t *testing.T, db *gorm.DB, batch domain.AssessmentBatch, names []string) domain.AssessmentBatch {
	t.Helper()
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	persons := make([]domain.AssessmentBatchPerson, len(names))
	for i, name := range names {
		persons[i] = domain.AssessmentBatchPerson{
			BatchID:   batch.ID,
			TokenName: name,
			Status:    domain.PersonStatusPending,
		}
	}
	if len(persons) > 0 {
		if err := db.Create(&persons).Error; err != nil {
			t.Fatalf("create persons: %v", err)
		}
	}
	return batch
}

func loadBatch(t *testing.T, db *gorm.DB, id int64) domain.AssessmentBatch {
	t.Helper()
	var b domain.AssessmentBatch
	if err := db.First(&b, id).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	return b
}

func loadPersons(t *testing.T, db *gorm.DB, batchID int64) []domain.AssessmentBatchPerson {
	t.Helper()
	var rows []domain.AssessmentBatchPerson
	if err := db.Where("batch_id = ?", batchID).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load persons: %v", err)
	}
	return rows
}

// TestAdvancePersonTerminalSuccessSide 核心断言：成功侧累计会话、失败侧不计。
func TestAdvancePersonTerminalSuccessSide(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B001", domain.BatchTriggerScheduled, 2), []string{"张三", "李四"})

	if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusSuccess, "", 5); err != nil {
		t.Fatalf("advance 张三: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if got.EvaluatedCount != 1 || got.CoveredSessionCount != 5 || got.FailedCount != 0 {
		t.Fatalf("成功侧推进后 want (1,5,0), got (%d,%d,%d)",
			got.EvaluatedCount, got.CoveredSessionCount, got.FailedCount)
	}

	if err := repo.AdvancePersonTerminal(ctx, b.ID, "李四", domain.PersonStatusFailed, "x", 3); err != nil {
		t.Fatalf("advance 李四: %v", err)
	}
	got = loadBatch(t, db, b.ID)
	if got.EvaluatedCount != 2 || got.FailedCount != 1 || got.CoveredSessionCount != 5 {
		t.Fatalf("失败侧推进后 want (2,5,1), got (%d,%d,%d)",
			got.EvaluatedCount, got.CoveredSessionCount, got.FailedCount)
	}
	persons := loadPersons(t, db, b.ID)
	if persons[0].Status != domain.PersonStatusSuccess || persons[0].ErrorSummary != "" || persons[0].FinishedAt == nil {
		t.Fatalf("张三人员行终态不符: %+v", persons[0])
	}
	if persons[1].Status != domain.PersonStatusFailed || persons[1].ErrorSummary != "x" || persons[1].FinishedAt == nil {
		t.Fatalf("李四人员行终态不符: %+v", persons[1])
	}
}

// TestAdvancePersonTerminalIdempotent 核心断言：重入双计防护（BR6）。
func TestAdvancePersonTerminalIdempotent(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B002", domain.BatchTriggerScheduled, 2), []string{"张三", "李四"})

	if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusSuccess, "", 5); err != nil {
		t.Fatalf("首次推进: %v", err)
	}
	// 模拟 batch-run 重试重入：同一人员行再次推进（终态不同仍须幂等）。
	if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusFailed, "retry", 5); err != nil {
		t.Fatalf("重入推进: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if got.EvaluatedCount != 1 || got.CoveredSessionCount != 5 || got.FailedCount != 0 {
		t.Fatalf("重入后计数 want 不变 (1,5,0), got (%d,%d,%d)",
			got.EvaluatedCount, got.CoveredSessionCount, got.FailedCount)
	}
	persons := loadPersons(t, db, b.ID)
	if persons[0].Status != domain.PersonStatusSuccess {
		t.Fatalf("终态不应被重入改写: %q", persons[0].Status)
	}
}

// TestFinalizeBatchIdempotent 核心断言：终态不可逆（BR3）。
func TestFinalizeBatchIdempotent(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B003", domain.BatchTriggerManual, 2), []string{"张三", "李四"})

	if err := repo.FinalizeBatch(ctx, b.ID, domain.BatchStatusSuccess, 0); err != nil {
		t.Fatalf("首次终态: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if got.Status != domain.BatchStatusSuccess || got.FinishedAt == nil || got.SessionFailRatio == nil || *got.SessionFailRatio != 0 {
		t.Fatalf("首次终态落库不符: %+v", got)
	}
	// 已 success 再调 partial_failed：affected==0 静默返回 nil 且不改写状态。
	if err := repo.FinalizeBatch(ctx, b.ID, domain.BatchStatusPartialFailed, 12.5); err != nil {
		t.Fatalf("二次终态 want nil, got %v", err)
	}
	got = loadBatch(t, db, b.ID)
	if got.Status != domain.BatchStatusSuccess {
		t.Fatalf("终态不可逆: want success, got %q", got.Status)
	}
	if got.SessionFailRatio == nil || *got.SessionFailRatio != 0 {
		t.Fatalf("session_fail_ratio 不应被改写: %+v", got.SessionFailRatio)
	}
}

// TestFailWholeBatch 核心断言：整批失败按实况计数（BR4）；部分已终态者不虚报、
// 已终态批次不可被改写。
func TestFailWholeBatch(t *testing.T) {
	t.Run("全 pending 整批落 N/N", func(t *testing.T) {
		db := newBatchTestDB(t)
		repo := repository.NewAssessmentBatchRepository(db)
		ctx := context.Background()
		b := createBatchWithPersons(t, db, newBatch("B004", domain.BatchTriggerScheduled, 3),
			[]string{"张三", "李四", "王五"})

		failed, total, err := repo.FailWholeBatch(ctx, b.ID, "上游会话列表不可用")
		if err != nil {
			t.Fatalf("fail whole batch: %v", err)
		}
		if failed != 3 || total != 3 {
			t.Fatalf("返回计数 want (3,3), got (%d,%d)", failed, total)
		}
		got := loadBatch(t, db, b.ID)
		if got.Status != domain.BatchStatusFailed || got.EvaluatedCount != 3 || got.FailedCount != 3 {
			t.Fatalf("批次 want (failed,3,3), got (%q,%d,%d)", got.Status, got.EvaluatedCount, got.FailedCount)
		}
		if got.ErrorSummary != "上游会话列表不可用" || got.FinishedAt == nil {
			t.Fatalf("批次失败原因/终态时间未落: %+v", got)
		}
		for _, p := range loadPersons(t, db, b.ID) {
			if p.Status != domain.PersonStatusFailed || p.ErrorSummary == "" || p.FinishedAt == nil {
				t.Fatalf("人员行未落 failed 终态: %+v", p)
			}
		}
	})

	t.Run("部分已终态按实况计数", func(t *testing.T) {
		db := newBatchTestDB(t)
		repo := repository.NewAssessmentBatchRepository(db)
		ctx := context.Background()
		b := createBatchWithPersons(t, db, newBatch("B005", domain.BatchTriggerScheduled, 3),
			[]string{"张三", "李四", "王五"})
		// 张三已 success（重放场景），剩余 2 pending 落 failed。
		if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusSuccess, "", 1); err != nil {
			t.Fatalf("advance: %v", err)
		}

		failed, total, err := repo.FailWholeBatch(ctx, b.ID, "上游会话列表不可用")
		if err != nil {
			t.Fatalf("fail whole batch: %v", err)
		}
		if failed != 2 || total != 3 {
			t.Fatalf("实况计数 want (2,3)（张三 success 不改写）, got (%d,%d)", failed, total)
		}
		got := loadBatch(t, db, b.ID)
		if got.EvaluatedCount != 3 || got.FailedCount != 2 {
			t.Fatalf("批次计数 want (3,2), got (%d,%d)", got.EvaluatedCount, got.FailedCount)
		}
		persons := loadPersons(t, db, b.ID)
		byName := map[string]domain.AssessmentBatchPerson{}
		for _, p := range persons {
			byName[p.TokenName] = p
		}
		if byName["张三"].Status != domain.PersonStatusSuccess {
			t.Fatalf("张三已终态不应被改写: %+v", byName["张三"])
		}
		if byName["李四"].Status != domain.PersonStatusFailed || byName["王五"].Status != domain.PersonStatusFailed {
			t.Fatalf("pending 行应落 failed: %+v", persons)
		}
	})

	t.Run("已终态批次幂等不改写", func(t *testing.T) {
		db := newBatchTestDB(t)
		repo := repository.NewAssessmentBatchRepository(db)
		ctx := context.Background()
		b := createBatchWithPersons(t, db, newBatch("B006", domain.BatchTriggerScheduled, 2),
			[]string{"张三", "李四"})
		if err := repo.FinalizeBatch(ctx, b.ID, domain.BatchStatusSuccess, 0); err != nil {
			t.Fatalf("finalize: %v", err)
		}

		failed, total, err := repo.FailWholeBatch(ctx, b.ID, "迟到投递")
		if err != nil {
			t.Fatalf("已终态幂等 want nil, got %v", err)
		}
		if failed != 0 || total != 0 {
			t.Fatalf("已终态幂等 want (0,0), got (%d,%d)", failed, total)
		}
		got := loadBatch(t, db, b.ID)
		if got.Status != domain.BatchStatusSuccess {
			t.Fatalf("终态不可逆: want success, got %q", got.Status)
		}
	})
}

// TestExpandTargets all 骨架批次异步展开：'[]' 守卫只展开一次，重复调用不双插明细。
func TestExpandTargets(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := newBatch("B007", domain.BatchTriggerScheduled, 0)
	b.TargetNamesJSON = `[]`
	b.TotalCount = 0
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.ExpandTargets(ctx, b.ID, []string{"张三", "李四", "王五"}); err != nil {
		t.Fatalf("expand: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if got.TotalCount != 3 || got.TargetNamesJSON != `["张三","李四","王五"]` {
		t.Fatalf("want (3, 名单 JSON), got (%d,%s)", got.TotalCount, got.TargetNamesJSON)
	}
	if len(loadPersons(t, db, b.ID)) != 3 {
		t.Fatal("want 3 行明细")
	}

	// 重放：名单已非 '[]'，幂等返回不双插。
	if err := repo.ExpandTargets(ctx, b.ID, []string{"张三", "李四", "王五"}); err != nil {
		t.Fatalf("重放 expand: %v", err)
	}
	if len(loadPersons(t, db, b.ID)) != 3 {
		t.Fatalf("重放不应双插明细, got %d 行", len(loadPersons(t, db, b.ID)))
	}
}

// TestGetByIDNotFound 无行返 (nil, nil)。
func TestGetByIDNotFound(t *testing.T) {	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	got, err := repo.GetByID(context.Background(), 999)
	if err != nil || got != nil {
		t.Fatalf("无行 want (nil,nil), got (%v,%v)", got, err)
	}
}

// TestFindLatestScheduled 取最近触发的定时批次（不限状态，防快速终态后宽限窗内
// 重复建批）；无行返 (nil, nil)。
func TestFindLatestScheduled(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()

	got, err := repo.FindLatestScheduled(ctx)
	if err != nil || got != nil {
		t.Fatalf("空表 want (nil,nil), got (%v,%v)", got, err)
	}

	older := newBatch("B010", domain.BatchTriggerScheduled, 1)
	older.TriggeredAt = time.Unix(batchBaseUnix, 0).UTC()
	newer := newBatch("B011", domain.BatchTriggerScheduled, 1)
	newer.TriggeredAt = time.Unix(batchBaseUnix+3600, 0).UTC()
	manual := newBatch("B012", domain.BatchTriggerManual, 1)
	manual.TriggeredAt = time.Unix(batchBaseUnix+7200, 0).UTC()
	finished := newBatch("B013", domain.BatchTriggerScheduled, 1)
	finished.Status = domain.BatchStatusSuccess
	finished.TriggeredAt = time.Unix(batchBaseUnix+10800, 0).UTC()
	for _, b := range []domain.AssessmentBatch{older, newer, manual, finished} {
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// 不限状态：终态 scheduled（B013）也算，manual 排除。
	got, err = repo.FindLatestScheduled(ctx)
	if err != nil || got == nil {
		t.Fatalf("want 最新 scheduled（含终态）, got (%v,%v)", got, err)
	}
	if got.BatchNo != "B013" {
		t.Fatalf("want B013（manual 排除、终态不排除）, got %q", got.BatchNo)
	}
}

// TestListByFilter 空筛全量、组合筛选、倒序分页与 total。
func TestListByFilter(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	seed := []struct {
		no        string
		trigger   string
		status    string
		triggerAt int64
	}{
		{"B020", domain.BatchTriggerScheduled, domain.BatchStatusRunning, batchBaseUnix},
		{"B021", domain.BatchTriggerManual, domain.BatchStatusSuccess, batchBaseUnix + 100},
		{"B022", domain.BatchTriggerScheduled, domain.BatchStatusSuccess, batchBaseUnix + 200},
	}
	for _, s := range seed {
		b := newBatch(s.no, s.trigger, 1)
		b.Status = s.status
		b.TriggeredAt = time.Unix(s.triggerAt, 0).UTC()
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	list, total, err := repo.ListByFilter(ctx, repository.BatchFilter{Page: 1, PageSize: 10})
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("空筛 want total 3, got (%d,%v)", total, err)
	}
	if list[0].BatchNo != "B022" || list[2].BatchNo != "B020" {
		t.Fatalf("倒序不符: %v", []string{list[0].BatchNo, list[1].BatchNo, list[2].BatchNo})
	}

	list, total, err = repo.ListByFilter(ctx, repository.BatchFilter{TriggerType: domain.BatchTriggerScheduled, Status: domain.BatchStatusSuccess, Page: 1, PageSize: 10})
	if err != nil || total != 1 || len(list) != 1 || list[0].BatchNo != "B022" {
		t.Fatalf("组合筛选 want B022 total 1, got (%d,%v)", total, err)
	}

	list, total, err = repo.ListByFilter(ctx, repository.BatchFilter{Page: 2, PageSize: 2})
	if err != nil || total != 3 || len(list) != 1 || list[0].BatchNo != "B020" {
		t.Fatalf("分页第2页 want B020, got (%d,%v)", total, err)
	}
}

// TestUpdateTotalSessions 单事务回填会话总数与逐人会话数，名单内无会话者为 0。
func TestUpdateTotalSessions(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B030", domain.BatchTriggerScheduled, 2), []string{"张三", "李四"})

	if err := repo.UpdateTotalSessions(ctx, b.ID, 8, map[string]int{"张三": 5}); err != nil {
		t.Fatalf("update total sessions: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if got.TotalSessionCount != 8 {
		t.Fatalf("total_session_count want 8, got %d", got.TotalSessionCount)
	}
	persons := loadPersons(t, db, b.ID)
	if persons[0].SessionCount != 5 {
		t.Fatalf("张三 session_count want 5, got %d", persons[0].SessionCount)
	}
	if persons[1].SessionCount != 0 {
		t.Fatalf("李四（无会话）session_count want 0, got %d", persons[1].SessionCount)
	}
}

// TestAdvancePersonTerminalErrorSummaryTruncated 失败摘要截断 255。
func TestAdvancePersonTerminalErrorSummaryTruncated(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B040", domain.BatchTriggerScheduled, 1), []string{"张三"})

	long := strings.Repeat("啊", 300)
	if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusFailed, long, 0); err != nil {
		t.Fatalf("advance: %v", err)
	}
	p := loadPersons(t, db, b.ID)[0]
	if len([]rune(p.ErrorSummary)) != 255 {
		t.Fatalf("error_summary want 截断 255 字符, got %d", len([]rune(p.ErrorSummary)))
	}
}

// TestFailWholeBatchTruncated 批次级原因截断 255。
func TestFailWholeBatchTruncated(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B041", domain.BatchTriggerScheduled, 1), []string{"张三"})

	if _, _, err := repo.FailWholeBatch(ctx, b.ID, strings.Repeat("e", 300)); err != nil {
		t.Fatalf("fail whole batch: %v", err)
	}
	got := loadBatch(t, db, b.ID)
	if len(got.ErrorSummary) != 255 {
		t.Fatalf("批次 error_summary want 255 字节, got %d", len(got.ErrorSummary))
	}
}

// TestCountInRange 本期跑批间隔计数 [start, end)。
func TestCountInRange(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	for i, offset := range []int64{-1, 0, 3600, 7199, 7200} {
		b := newBatch(fmt.Sprintf("B05%d", i), domain.BatchTriggerScheduled, 1)
		b.TriggeredAt = time.Unix(batchBaseUnix+offset, 0).UTC()
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	n, err := repo.CountInRange(ctx, time.Unix(batchBaseUnix, 0).UTC(), time.Unix(batchBaseUnix+7200, 0).UTC())
	if err != nil || n != 3 {
		t.Fatalf("半开区间 [start,end) want 3, got (%d,%v)", n, err)
	}
}

// TestListRunningTriggeredAt 取全部 running 批次触发时刻（终态排除）。
func TestListRunningTriggeredAt(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	stalled := newBatch("B060", domain.BatchTriggerScheduled, 1)
	stalled.TriggeredAt = time.Unix(batchBaseUnix, 0).UTC()
	fresh := newBatch("B061", domain.BatchTriggerManual, 1)
	fresh.TriggeredAt = time.Unix(batchBaseUnix+3600, 0).UTC()
	finished := newBatch("B062", domain.BatchTriggerScheduled, 1)
	finished.Status = domain.BatchStatusSuccess
	finished.TriggeredAt = time.Unix(batchBaseUnix+7200, 0).UTC()
	for _, b := range []domain.AssessmentBatch{stalled, fresh, finished} {
		if err := db.Create(&b).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	ats, err := repo.ListRunningTriggeredAt(ctx)
	if err != nil || len(ats) != 2 {
		t.Fatalf("want 2（仅 running，终态排除）, got (%d,%v)", len(ats), err)
	}
}

// TestCountSuccessSideTriggeredBetween 按 triggered_at 范围圈定批次计数成功侧四态；
// 范围外批次的人员行不计。
func TestCountSuccessSideTriggeredBetween(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()

	n, err := repo.CountSuccessSideTriggeredBetween(ctx,
		time.Unix(batchBaseUnix, 0).UTC(), time.Unix(batchBaseUnix+7200, 0).UTC())
	if err != nil || n != 0 {
		t.Fatalf("空库 want 0, got (%d,%v)", n, err)
	}

	b1 := createBatchWithPersons(t, db, newBatch("B070", domain.BatchTriggerScheduled, 1), nil)
	b2 := newBatch("B071", domain.BatchTriggerScheduled, 1)
	b2.TriggeredAt = time.Unix(batchBaseUnix+99999, 0).UTC() // 范围外批次
	if err := db.Create(&b2).Error; err != nil {
		t.Fatalf("create b2: %v", err)
	}
	statuses := []string{
		domain.PersonStatusSuccess, domain.PersonStatusReused, domain.PersonStatusDegraded,
		domain.PersonStatusSkipped, domain.PersonStatusFailed, domain.PersonStatusPending,
	}
	rows := make([]domain.AssessmentBatchPerson, len(statuses))
	for i, s := range statuses {
		rows[i] = domain.AssessmentBatchPerson{BatchID: b1.ID, TokenName: fmt.Sprintf("人%d", i), Status: s}
	}
	rows = append(rows, domain.AssessmentBatchPerson{BatchID: b2.ID, TokenName: "外人", Status: domain.PersonStatusSuccess})
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create persons: %v", err)
	}

	// b1 triggered_at=batchBaseUnix 在 [start,end) 内；b2 在范围外不计其成功行。
	n, err = repo.CountSuccessSideTriggeredBetween(ctx,
		time.Unix(batchBaseUnix, 0).UTC(), time.Unix(batchBaseUnix+7200, 0).UTC())
	if err != nil || n != 4 {
		t.Fatalf("want 4（范围内 b1 成功侧四态）, got (%d,%v)", n, err)
	}
}

// TestCreateWithPersonsAtomic 批次行与人员明细同事务：正常路径 3 行落库、
// 初值 pending、BatchID 关联正确；撞批次号唯一索引返回错误。
func TestCreateWithPersonsAtomic(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := newBatch("B080", domain.BatchTriggerScheduled, 3)
	err := repo.CreateWithPersons(ctx, &b, func(batchID int64) []domain.AssessmentBatchPerson {
		return []domain.AssessmentBatchPerson{
			{BatchID: batchID, TokenName: "张三", Status: domain.PersonStatusPending},
			{BatchID: batchID, TokenName: "李四", Status: domain.PersonStatusPending},
			{BatchID: batchID, TokenName: "王五", Status: domain.PersonStatusPending},
		}
	})
	if err != nil {
		t.Fatalf("CreateWithPersons: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("雪花 ID 未被回调赋值")
	}
	var rows []domain.AssessmentBatchPerson
	if err := db.Where("batch_id = ?", b.ID).Find(&rows).Error; err != nil {
		t.Fatalf("query persons: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("人员明细 = %d 行, want 3", len(rows))
	}
	for _, r := range rows {
		if r.BatchID != b.ID {
			t.Errorf("BatchID = %d, want %d", r.BatchID, b.ID)
		}
		if r.Status != domain.PersonStatusPending {
			t.Errorf("Status = %q, want pending", r.Status)
		}
		if r.ID == 0 {
			t.Error("雪花 ID 未被回调赋值")
		}
	}
	dup := newBatch("B080", domain.BatchTriggerManual, 1)
	if err := repo.CreateWithPersons(ctx, &dup, func(int64) []domain.AssessmentBatchPerson {
		return nil
	}); err == nil {
		t.Fatal("撞批次号 want error")
	}
}

// TestFailWholeBatchPendingGuard pending 守卫：已终态成功者不被整批失败覆盖。
func TestFailWholeBatchPendingGuard(t *testing.T) {
	db := newBatchTestDB(t)
	repo := repository.NewAssessmentBatchRepository(db)
	ctx := context.Background()
	b := createBatchWithPersons(t, db, newBatch("B081", domain.BatchTriggerManual, 2), []string{"张三", "李四"})

	// 张三先成功终态，随后整批失败：张三保持 success，李四落 failed。
	if err := repo.AdvancePersonTerminal(ctx, b.ID, "张三", domain.PersonStatusSuccess, "", 1); err != nil {
		t.Fatalf("advance 张三: %v", err)
	}
	if _, _, err := repo.FailWholeBatch(ctx, b.ID, "整批失败"); err != nil {
		t.Fatalf("fail whole batch: %v", err)
	}
	persons := loadPersons(t, db, b.ID)
	if persons[0].Status != domain.PersonStatusSuccess {
		t.Errorf("已终态成功者被覆盖: %q", persons[0].Status)
	}
	if persons[1].Status != domain.PersonStatusFailed {
		t.Errorf("pending 者应落 failed: %q", persons[1].Status)
	}
}
