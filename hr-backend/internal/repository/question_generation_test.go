// Package repository_test 对 question_generation 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite，复用 question_batch_test 的反射版雪花 Create 回调
// （覆盖 Question/QuestionBatch/QuestionGeneration 及 slice），AutoMigrate 三表。
// 覆盖 Create/FindByID/MarkRunning CAS/SaveProgress/FinishCompleted 完成事务/
// FinishTerminal 幂等终态/RequestCancel 协作式取消的真实 SQL 行为。
package repository_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// newQGenTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate Question、
// QuestionBatch、QuestionGeneration 三表并注册雪花 Create 回调（复用
// question_batch_test 的反射版实现，支持本文件新增的 QuestionGeneration）。
func newQGenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newQBatchTestDB(t)
	if err := db.AutoMigrate(&domain.QuestionGeneration{}); err != nil {
		t.Fatalf("auto migrate question_generations: %v", err)
	}
	return db
}

// seedGeneration 写入一行生成会话，返回带 ID 的实体。
func seedGeneration(t *testing.T, db *gorm.DB, status string) domain.QuestionGeneration {
	t.Helper()
	g := domain.QuestionGeneration{
		DimensionIDs: "[101,102]",
		Count:        5,
		Status:       status,
	}
	if err := db.Create(&g).Error; err != nil {
		t.Fatalf("seed generation %s: %v", status, err)
	}
	return g
}

// loadGeneration 主键重读一行，测试内断言 DB 落库态。
func loadGeneration(t *testing.T, db *gorm.DB, id int64) domain.QuestionGeneration {
	t.Helper()
	var g domain.QuestionGeneration
	if err := db.First(&g, id).Error; err != nil {
		t.Fatalf("load generation %d: %v", id, err)
	}
	return g
}

// timeNowMMdd 当日批次号后缀（与 NextBatchNo 同口径 time.Local），撞号测试构造
// 既有批次用。
func timeNowMMdd(t *testing.T) string {
	t.Helper()
	return time.Now().In(time.Local).Format("0102")
}

// genQuestions 组装 n 道 AI 题目出参（Generator 不预填 question_no/status/batch_id）。
func genQuestions(n int) []domain.Question {
	questions := make([]domain.Question, n)
	for i := range questions {
		questions[i] = domain.Question{
			Source:      domain.QuestionSourceAI,
			DimensionID: int64(101 + i%2),
			Scenario:    fmt.Sprintf("情境-%d", i+1),
			Requirement: fmt.Sprintf("要求-%d", i+1),
			FocusPoint:  fmt.Sprintf("考察-%d", i+1),
		}
	}
	return questions
}

// TestQuestionGenerationCreateAndFind 建行与主键查：Create 落 QUEUED 行（雪花 ID
// 由回调赋值），FindByID 字段一致，不存在返回 ErrRecordNotFound。
func TestQuestionGenerationCreateAndFind(t *testing.T) {
	db := newQGenTestDB(t)
	repo := repository.NewQuestionGenerationRepository(db)

	g := domain.QuestionGeneration{
		DimensionIDs:   "[101,102]",
		Count:          5,
		Status:         domain.QuestionGenStatusQueued,
		GeneratedCount: 0,
		Staging:        "[]",
	}
	if err := repo.Create(context.Background(), &g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.ID == 0 {
		t.Fatal("generation ID want assigned by snowflake callback")
	}
	got, err := repo.FindByID(context.Background(), g.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.DimensionIDs != "[101,102]" || got.Count != 5 ||
		got.Status != domain.QuestionGenStatusQueued || got.Staging != "[]" {
		t.Fatalf("row mismatch: %+v", got)
	}
	if _, err := repo.FindByID(context.Background(), 999999); err != gorm.ErrRecordNotFound {
		t.Fatalf("missing id want ErrRecordNotFound, got %v", err)
	}
}

// TestMarkRunningCAS worker 领取 CAS：QUEUED 行 MarkRunning 成功置 RUNNING；
// 再次调用 RowsAffected=0 返回 ErrNotQueued（核心断言）。
func TestQuestionGenerationMarkRunningCAS(t *testing.T) {
	db := newQGenTestDB(t)
	g := seedGeneration(t, db, domain.QuestionGenStatusQueued)
	repo := repository.NewQuestionGenerationRepository(db)

	if err := repo.MarkRunning(context.Background(), g.ID); err != nil {
		t.Fatalf("MarkRunning on QUEUED: %v", err)
	}
	if row := loadGeneration(t, db, g.ID); row.Status != domain.QuestionGenStatusRunning {
		t.Fatalf("status want RUNNING, got %s", row.Status)
	}
	if err := repo.MarkRunning(context.Background(), g.ID); !errors.Is(err, repository.ErrNotQueued) {
		t.Fatalf("second MarkRunning want ErrNotQueued, got %v", err)
	}
	// 已取消行同样不可领取（BR1：离开生成进行态放弃本批）。
	canceled := seedGeneration(t, db, domain.QuestionGenStatusCanceled)
	if err := repo.MarkRunning(context.Background(), canceled.ID); !errors.Is(err, repository.ErrNotQueued) {
		t.Fatalf("MarkRunning on CANCELED want ErrNotQueued, got %v", err)
	}
	if row := loadGeneration(t, db, canceled.ID); row.Status != domain.QuestionGenStatusCanceled {
		t.Fatalf("canceled row must stay CANCELED, got %s", row.Status)
	}
}

// TestMarkRunningMissing 不存在行领取返回 ErrNotQueued。
func TestQuestionGenerationMarkRunningMissing(t *testing.T) {
	db := newQGenTestDB(t)
	repo := repository.NewQuestionGenerationRepository(db)
	if err := repo.MarkRunning(context.Background(), 999999); !errors.Is(err, repository.ErrNotQueued) {
		t.Fatalf("missing row want ErrNotQueued, got %v", err)
	}
}

// TestSaveProgress 进度与暂存原子覆盖：RUNNING 行三列一次 UPDATE 落值，
// 旧 staging 整串被替换（读-改-写在 engine 单写者内串行，无合并语义）。
func TestQuestionGenerationSaveProgress(t *testing.T) {
	db := newQGenTestDB(t)
	g := seedGeneration(t, db, domain.QuestionGenStatusRunning)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("staging", `[{"scenario":"旧题"}]`)
	repo := repository.NewQuestionGenerationRepository(db)

	if err := repo.SaveProgress(context.Background(), g.ID, 3, 102, `[{"scenario":"新题一"},{"scenario":"新题二"}]`); err != nil {
		t.Fatalf("SaveProgress: %v", err)
	}
	row := loadGeneration(t, db, g.ID)
	if row.GeneratedCount != 3 || row.CurrentDimensionID != 102 ||
		row.Staging != `[{"scenario":"新题一"},{"scenario":"新题二"}]` {
		t.Fatalf("progress mismatch: count=%d dim=%d staging=%s", row.GeneratedCount, row.CurrentDimensionID, row.Staging)
	}
	if row.Status != domain.QuestionGenStatusRunning {
		t.Fatalf("status want untouched RUNNING, got %s", row.Status)
	}
}

// TestFinishCompletedSingleTx 完成事务（核心断言）：RUNNING 行 FinishCompleted 后
// 置 COMPLETED、batch_id 落值、staging 清空，同事务落 GENERATE 批次与题目行：
// 题目 Q-AG 编号续既有最大号连续分配、status=PENDING、batch_id 回填。
func TestQuestionGenerationFinishCompletedSingleTx(t *testing.T) {
	db := newQGenTestDB(t)
	seedAI(t, db, "Q-AG-0007", domain.QuestionStatusActive, "情境七", 101)
	g := seedGeneration(t, db, domain.QuestionGenStatusRunning)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("generated_count", 2)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("staging", `[{"scenario":"暂存题"}]`)
	repo := repository.NewQuestionGenerationRepository(db)

	batch := domain.QuestionBatch{
		Title: "授权与分工 ×2", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending,
		QuestionCount: 2, DimensionIDs: "[101,102]",
	}
	questions := genQuestions(2)
	if err := repo.FinishCompleted(context.Background(), g.ID, &batch, &questions); err != nil {
		t.Fatalf("FinishCompleted: %v", err)
	}

	// generation 行：COMPLETED + batch_id 回填 + staging 清空。
	row := loadGeneration(t, db, g.ID)
	if row.Status != domain.QuestionGenStatusCompleted {
		t.Fatalf("status want COMPLETED, got %s", row.Status)
	}
	if row.BatchID == 0 || row.BatchID != batch.ID {
		t.Fatalf("batch_id want %d backfilled, got %d", batch.ID, row.BatchID)
	}
	if row.Staging != "" {
		t.Fatalf("staging want cleared, got %q", row.Staging)
	}

	// 批次行落库，批次号 #G+MMdd。
	var batchRow domain.QuestionBatch
	if err := db.First(&batchRow, batch.ID).Error; err != nil {
		t.Fatalf("load batch: %v", err)
	}
	if len(batchRow.BatchNo) < 6 || batchRow.BatchNo[:2] != "#G" {
		t.Fatalf("batch_no want #GMMdd..., got %s", batchRow.BatchNo)
	}
	if batchRow.BatchType != domain.QuestionBatchTypeGenerate ||
		batchRow.Status != domain.QuestionBatchStatusPending || batchRow.QuestionCount != 2 {
		t.Fatalf("batch row mismatch: %+v", batchRow)
	}

	// 题目行：编号连续分配（既有最大 0007 → 0008/0009）、PENDING、挂批。
	var qRows []domain.Question
	if err := db.Where("batch_id = ?", batch.ID).Order("question_no").Find(&qRows).Error; err != nil {
		t.Fatalf("load questions: %v", err)
	}
	if len(qRows) != 2 {
		t.Fatalf("want 2 question rows, got %d", len(qRows))
	}
	if qRows[0].QuestionNo != "Q-AG-0008" || qRows[1].QuestionNo != "Q-AG-0009" {
		t.Fatalf("want Q-AG-0008/0009, got %s/%s", qRows[0].QuestionNo, qRows[1].QuestionNo)
	}
	for _, q := range qRows {
		if q.Status != domain.QuestionStatusPending {
			t.Fatalf("question %s want PENDING, got %s", q.QuestionNo, q.Status)
		}
	}
	// 出参同事务回写：编号、状态、挂批与 DB 一致。
	if questions[0].QuestionNo != "Q-AG-0008" || questions[1].BatchID != batch.ID {
		t.Fatalf("out params not backfilled: %+v", questions)
	}
}

// TestFinishCompletedNotRunning 非 RUNNING 前置拒绝（契约 WHERE status=RUNNING）：
// QUEUED 与 COMPLETED 行均拒绝且零落库（无批次、无题目、generation 不变）。
func TestQuestionGenerationFinishCompletedNotRunning(t *testing.T) {
	db := newQGenTestDB(t)
	queued := seedGeneration(t, db, domain.QuestionGenStatusQueued)
	done := seedGeneration(t, db, domain.QuestionGenStatusCompleted)
	repo := repository.NewQuestionGenerationRepository(db)

	for _, id := range []int64{queued.ID, done.ID, 999999} {
		batch := domain.QuestionBatch{Title: "t", Source: domain.QuestionSourceAI,
			BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending, QuestionCount: 1}
		questions := genQuestions(1)
		if err := repo.FinishCompleted(context.Background(), id, &batch, &questions); !errors.Is(err, repository.ErrNotRunning) {
			t.Fatalf("generation %d want ErrNotRunning, got %v", id, err)
		}
	}
	var batchN, questionN int64
	db.Model(&domain.QuestionBatch{}).Count(&batchN)
	db.Model(&domain.Question{}).Count(&questionN)
	if batchN != 0 || questionN != 0 {
		t.Fatalf("want zero rows persisted, got batch=%d question=%d", batchN, questionN)
	}
	if row := loadGeneration(t, db, queued.ID); row.Status != domain.QuestionGenStatusQueued || row.BatchID != 0 {
		t.Fatalf("queued row must be untouched: %+v", row)
	}
}

// TestFinishCompletedRollback 事务失败上抛回滚：批次号撞唯一索引时整批回滚，
// generation 保持 RUNNING、staging 保留、questions 零残留。
func TestQuestionGenerationFinishCompletedRollback(t *testing.T) {
	db := newQGenTestDB(t)
	g := seedGeneration(t, db, domain.QuestionGenStatusRunning)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("staging", `[{"scenario":"暂存题"}]`)
	today := "#G" + timeNowMMdd(t)
	db.Create(&domain.QuestionBatch{
		BatchNo: today, Title: "占位批", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending,
		QuestionCount: 0,
	})
	repo := repository.NewQuestionGenerationRepository(db)

	batch := domain.QuestionBatch{
		BatchNo: today, Title: "撞号批", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending,
		QuestionCount: 2,
	}
	questions := genQuestions(2)
	if err := repo.FinishCompleted(context.Background(), g.ID, &batch, &questions); err == nil {
		t.Fatal("batch_no collision want error propagated")
	}
	row := loadGeneration(t, db, g.ID)
	if row.Status != domain.QuestionGenStatusRunning || row.BatchID != 0 || row.Staging == "" {
		t.Fatalf("generation must stay RUNNING with staging, got %+v", row)
	}
	var questionN int64
	db.Model(&domain.Question{}).Count(&questionN)
	if questionN != 0 {
		t.Fatalf("questions want zero residue on rollback, got %d", questionN)
	}
}

// TestFinishTerminalIdempotent 失败终态（核心断言）：RUNNING 行 FinishTerminal 置
// FAILED + error_code 落值 + staging 清空；FAILED 行再调用返回 ErrAlreadyTerminal
// 且状态不变（BR2）。
func TestQuestionGenerationFinishTerminalIdempotent(t *testing.T) {
	db := newQGenTestDB(t)
	g := seedGeneration(t, db, domain.QuestionGenStatusRunning)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", g.ID).
		Update("staging", `[{"scenario":"暂存题"}]`)
	repo := repository.NewQuestionGenerationRepository(db)

	if err := repo.FinishTerminal(context.Background(), g.ID, domain.QuestionGenStatusFailed, domain.QuestionGenErrorLLMFailed); err != nil {
		t.Fatalf("FinishTerminal: %v", err)
	}
	row := loadGeneration(t, db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorLLMFailed {
		t.Fatalf("want FAILED + LLM_FAILED, got %s/%s", row.Status, row.ErrorCode)
	}
	if row.Staging != "" {
		t.Fatalf("staging want cleared on terminal, got %q", row.Staging)
	}
	if err := repo.FinishTerminal(context.Background(), g.ID, domain.QuestionGenStatusCanceled, domain.QuestionGenErrorCanceled); !errors.Is(err, repository.ErrAlreadyTerminal) {
		t.Fatalf("terminal re-entry want ErrAlreadyTerminal, got %v", err)
	}
	if row = loadGeneration(t, db, g.ID); row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorLLMFailed {
		t.Fatalf("terminal state must be immutable, got %s/%s", row.Status, row.ErrorCode)
	}
}

// TestFinishTerminalFromQueued QUEUED 行可直达终态（超时未领取即失败场景，
// 契约 WHERE status IN (RUNNING,QUEUED)）。
func TestQuestionGenerationFinishTerminalFromQueued(t *testing.T) {
	db := newQGenTestDB(t)
	g := seedGeneration(t, db, domain.QuestionGenStatusQueued)
	repo := repository.NewQuestionGenerationRepository(db)

	if err := repo.FinishTerminal(context.Background(), g.ID, domain.QuestionGenStatusFailed, domain.QuestionGenErrorLLMTimeout); err != nil {
		t.Fatalf("FinishTerminal from QUEUED: %v", err)
	}
	row := loadGeneration(t, db, g.ID)
	if row.Status != domain.QuestionGenStatusFailed || row.ErrorCode != domain.QuestionGenErrorLLMTimeout {
		t.Fatalf("want FAILED + LLM_TIMEOUT, got %s/%s", row.Status, row.ErrorCode)
	}
}

// TestRequestCancelQueuedAndRunning QUEUED/RUNNING 行取消置 CANCELED + error_code
// CANCELED + staging 清空（BR1：进行态离开放弃本批）。
func TestQuestionGenerationRequestCancelQueuedAndRunning(t *testing.T) {
	db := newQGenTestDB(t)
	queued := seedGeneration(t, db, domain.QuestionGenStatusQueued)
	running := seedGeneration(t, db, domain.QuestionGenStatusRunning)
	db.Model(&domain.QuestionGeneration{}).Where("id = ?", running.ID).
		Update("staging", `[{"scenario":"暂存题"}]`)
	repo := repository.NewQuestionGenerationRepository(db)

	for _, id := range []int64{queued.ID, running.ID} {
		if err := repo.RequestCancel(context.Background(), id); err != nil {
			t.Fatalf("RequestCancel %d: %v", id, err)
		}
		row := loadGeneration(t, db, id)
		if row.Status != domain.QuestionGenStatusCanceled || row.ErrorCode != domain.QuestionGenErrorCanceled {
			t.Fatalf("want CANCELED + error_code CANCELED, got %s/%s", row.Status, row.ErrorCode)
		}
		if row.Staging != "" {
			t.Fatalf("staging want cleared on cancel, got %q", row.Staging)
		}
	}
}

// TestRequestCancelTerminalNoop 已终态行取消幂等成功（核心断言）：COMPLETED 行
// RequestCancel 返回 nil 且状态仍 COMPLETED（BR1：终态不可逆）。
func TestQuestionGenerationRequestCancelTerminalNoop(t *testing.T) {
	db := newQGenTestDB(t)
	done := seedGeneration(t, db, domain.QuestionGenStatusCompleted)
	failed := seedGeneration(t, db, domain.QuestionGenStatusFailed)
	repo := repository.NewQuestionGenerationRepository(db)

	if err := repo.RequestCancel(context.Background(), done.ID); err != nil {
		t.Fatalf("cancel on COMPLETED want nil, got %v", err)
	}
	if row := loadGeneration(t, db, done.ID); row.Status != domain.QuestionGenStatusCompleted {
		t.Fatalf("status must stay COMPLETED, got %s", row.Status)
	}
	if err := repo.RequestCancel(context.Background(), failed.ID); err != nil {
		t.Fatalf("cancel on FAILED want nil, got %v", err)
	}
	if row := loadGeneration(t, db, failed.ID); row.Status != domain.QuestionGenStatusFailed {
		t.Fatalf("status must stay FAILED, got %s", row.Status)
	}
	// 不存在行同样幂等成功（RowsAffected=0 视为已终态）。
	if err := repo.RequestCancel(context.Background(), 999999); err != nil {
		t.Fatalf("cancel on missing row want nil, got %v", err)
	}
}
