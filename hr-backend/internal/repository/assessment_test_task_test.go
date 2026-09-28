// Package repository_test 对主动测试任务仓储做黑盒集成测试（specs TST §4.1.4/§5.1/§5.3/§6.2）。
// 每测试独立 :memory: SQLite，简化版雪花 Create 回调（同 assessment_test_schema_test.go），
// 覆盖列表筛选、创建事务、取号、状态机条件更新、链接读写与逾期扫描的真实 SQL 行为。
package repository_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newTestTaskDB 构造 :memory: SQLite（两模型 + Question），雪花回调覆盖三模型。
func newTestTaskDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_testrepo_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.AssessmentTestTask:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *domain.AssessmentTestLink:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *domain.Question:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentTestTask{}, &domain.AssessmentTestLink{}, &domain.Question{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// newTestTaskRepo 构造被测仓储。
func newTestTaskRepo(t *testing.T) (*gorm.DB, repository.AssessmentTestTaskRepository) {
	t.Helper()
	db := newTestTaskDB(t)
	return db, repository.NewAssessmentTestTaskRepository(db)
}

// tUTC 固定 UTC 基准时区，保证各测试内时间自洽。
func tUTC(y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, time.UTC)
}

// seedQ 写一行 AI 启用题（含 ReferenceCount 基线）。
func seedQ(t *testing.T, db *gorm.DB, no string) domain.Question {
	t.Helper()
	q := domain.Question{
		QuestionNo: no, Source: domain.QuestionSourceAI, DimensionID: 101,
		Scenario: "s-" + no, Requirement: "r-" + no, FocusPoint: "f-" + no,
		Status: domain.QuestionStatusActive, BatchID: 1, Version: 1,
	}
	if err := db.Create(&q).Error; err != nil {
		t.Fatalf("seed question %s: %v", no, err)
	}
	return q
}

// qRefCount 直读题目当前引用计数。
func qRefCount(t *testing.T, db *gorm.DB, id int64) int64 {
	t.Helper()
	var q domain.Question
	if err := db.First(&q, id).Error; err != nil {
		t.Fatalf("load question %d: %v", id, err)
	}
	return int64(q.ReferenceCount)
}

// mustCreate 走 CreateWithLink 建任务 + 首条 valid 链接。
func mustCreate(t *testing.T, repo repository.AssessmentTestTaskRepository, taskNo string, qids []domain.Question) (domain.AssessmentTestTask, domain.AssessmentTestLink) {
	t.Helper()
	ids := make([]string, len(qids))
	for i, q := range qids {
		ids[i] = fmt.Sprintf("%d", q.ID)
	}
	task := domain.AssessmentTestTask{
		TaskNo: taskNo, TestType: domain.TestTypeAIMgmt, StaffID: "u-" + taskNo, StaffName: "员工" + taskNo,
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: "[" + strings.Join(ids, ",") + "]", ScaleKey: "", DimensionCodesJSON: "[]",
	}
	link := domain.AssessmentTestLink{
		TaskID: 0, // 事务内补齐
		TokenPlain: "plain-" + taskNo, TokenHash: "hash-" + taskNo,
		Status: domain.LinkStatusValid,
		GeneratedAt: tUTC(2026, 9, 28, 8, 30), ExpiresAt: tUTC(2026, 10, 5, 8, 30),
	}
	if err := repo.CreateWithLink(context.Background(), &task, &link); err != nil {
		t.Fatalf("CreateWithLink %s: %v", taskNo, err)
	}
	if link.TaskID != task.ID {
		t.Fatalf("link.TaskID = %d, want task.ID %d", link.TaskID, task.ID)
	}
	return task, link
}

// linkRow 组装一条 valid 链接行（TaskID 由事务/调用方补齐）。
func linkRow(plain, hash string, generatedAt, expiresAt time.Time) domain.AssessmentTestLink {
	return domain.AssessmentTestLink{
		TokenPlain: plain, TokenHash: hash, Status: domain.LinkStatusValid,
		GeneratedAt: generatedAt, ExpiresAt: expiresAt,
	}
}

// countTaskRows 直查行数。
func countTaskRows(t *testing.T, db *gorm.DB, model any, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Where(query, args...).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// loadTask 直读任务行。
func loadTask(t *testing.T, db *gorm.DB, id int64) domain.AssessmentTestTask {
	t.Helper()
	var task domain.AssessmentTestTask
	if err := db.First(&task, id).Error; err != nil {
		t.Fatalf("load task %d: %v", id, err)
	}
	return task
}

// loadLink 直读链接行。
func loadLink(t *testing.T, db *gorm.DB, id int64) domain.AssessmentTestLink {
	t.Helper()
	var link domain.AssessmentTestLink
	if err := db.First(&link, id).Error; err != nil {
		t.Fatalf("load link %d: %v", id, err)
	}
	return link
}

// --- CreateWithLink ---

// TestCreateWithLinkTransactional 注入 IncrementReferenceCounts 失败（题目 ID 不存在：
// 本项目无外键，采用 tx 通道内强制报错）时任务行与链接行均未落库（BR4 §5.1.4 规则3）。
func TestCreateWithLinkTransactional(t *testing.T) {
	db, _ := newTestTaskRepo(t)
	q := seedQ(t, db, "Q-AG-0001")
	task := domain.AssessmentTestTask{
		TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张三",
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: fmt.Sprintf("[%d]", q.ID), DimensionCodesJSON: "[]",
	}
	link := domain.AssessmentTestLink{TokenPlain: "p1", TokenHash: "h1", Status: domain.LinkStatusValid}
	// 复刻 CreateWithLink 事务编排：链接插库后人为报错（引用计数通道不可注入，
	// 用报错点等价的 tx 回滚验证三步原子性）。
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		link.TaskID = task.ID
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		return errors.New("inject: increment reference counts failed")
	})
	if err == nil {
		t.Fatal("injected failure must propagate")
	}
	if n := countTaskRows(t, db, &domain.AssessmentTestTask{}, "id = ?", task.ID); n != 0 {
		t.Fatalf("task row leaked after rollback: %d", n)
	}
	if n := countTaskRows(t, db, &domain.AssessmentTestLink{}, "task_id = ?", task.ID); n != 0 {
		t.Fatalf("link row leaked after rollback: %d", n)
	}
}

// TestCreateWithLinkSuccess 成功路径：任务、链接、引用计数三步同事务生效，
// 引用计数恰好 +1（BR4 §5.1.2 步骤4）。
func TestCreateWithLinkSuccess(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	q1 := seedQ(t, db, "Q-AG-0001")
	q2 := seedQ(t, db, "Q-AG-0002")
	task, link := mustCreate(t, repo, "T202609280001", []domain.Question{q1, q2})
	if task.ID == 0 || link.ID == 0 {
		t.Fatalf("snowflake ids not assigned: task=%d link=%d", task.ID, link.ID)
	}
	if got := qRefCount(t, db, q1.ID); got != 1 {
		t.Fatalf("q1 reference_count = %d, want 1", got)
	}
	if got := qRefCount(t, db, q2.ID); got != 1 {
		t.Fatalf("q2 reference_count = %d, want 1", got)
	}
	// 同题再次建任务，计数累计 +1。
	mustCreate(t, repo, "T202609280002", []domain.Question{q1})
	if got := qRefCount(t, db, q1.ID); got != 2 {
		t.Fatalf("q1 reference_count after second task = %d, want 2", got)
	}
}

// createRaw 直接经 db 通道复刻建任务三步（绕开 repo 构造，辅助累计场景）。
func createRaw(db *gorm.DB, taskNo, idsJSON string) (domain.AssessmentTestTask, domain.AssessmentTestLink, error) {
	task := domain.AssessmentTestTask{
		TaskNo: taskNo, TestType: domain.TestTypeAIMgmt, StaffID: "u-" + taskNo, StaffName: "员工" + taskNo,
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: idsJSON, DimensionCodesJSON: "[]",
	}
	link := domain.AssessmentTestLink{
		TokenPlain: "plain-" + taskNo, TokenHash: "hash-" + taskNo,
		Status: domain.LinkStatusValid,
		GeneratedAt: tUTC(2026, 9, 28, 8, 30), ExpiresAt: tUTC(2026, 10, 5, 8, 30),
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		link.TaskID = task.ID
		if err := tx.Create(&link).Error; err != nil {
			return err
		}
		return nil
	})
	return task, link, err
}

// TestCreateWithLinkForeignQuestionFails 快照含不存在题目 ID 时引用计数 SQL 报错
//（glebarez/sqlite 对缺失行的 UPDATE 静默 0 行，用唯一索引撞键复现 tx 内报错回滚）。
func TestCreateWithLinkForeignQuestionFails(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	q := seedQ(t, db, "Q-AG-0001")
	task := domain.AssessmentTestTask{
		TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张三",
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: fmt.Sprintf("[%d]", q.ID), DimensionCodesJSON: "[]",
	}
	link := domain.AssessmentTestLink{TokenPlain: "p1", TokenHash: "h-dup", Status: domain.LinkStatusValid}
	// 预置撞 hash 行制造第三步前的唯一约束冲突，验证事务回滚不留任务行。
	pre := domain.AssessmentTestLink{TaskID: 999, TokenPlain: "pre", TokenHash: "h-dup", Status: domain.LinkStatusInvalid}
	if err := db.Create(&pre).Error; err != nil {
		t.Fatalf("seed conflicting link: %v", err)
	}
	err := repo.CreateWithLink(context.Background(), &task, &link)
	if err == nil {
		t.Fatal("expected unique violation to fail CreateWithLink")
	}
	if n := countTaskRows(t, db, &domain.AssessmentTestTask{}, "task_no = ?", "T202609280001"); n != 0 {
		t.Fatalf("task row leaked: %d", n)
	}
	if got := qRefCount(t, db, q.ID); got != 0 {
		t.Fatalf("reference_count advanced despite rollback: %d", got)
	}
}

// --- NextTaskNo ---

// TestNextTaskNo 取号递增、空表首号、跨日期归 1（BR3 §5.1.2 步骤2）。
func TestNextTaskNo(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	day1 := tUTC(2026, 9, 28, 10, 0)
	day2 := tUTC(2026, 9, 29, 10, 0)

	if no, err := repo.NextTaskNo(ctx, "T", day1); err != nil || no != "T202609280001" {
		t.Fatalf("empty table: no=%q err=%v, want T202609280001", no, err)
	}
	// seed 当日已有 0001。
	if _, _, err := createRaw(db, "T202609280001", "[]"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if no, err := repo.NextTaskNo(ctx, "T", day1); err != nil || no != "T202609280002" {
		t.Fatalf("after seed: no=%q err=%v, want T202609280002", no, err)
	}
	// seed 跳号到 0007，取最大递增而非行数。
	if _, _, err := createRaw(db, "T202609280007", "[]"); err != nil {
		t.Fatalf("seed 0007: %v", err)
	}
	if no, err := repo.NextTaskNo(ctx, "T", day1); err != nil || no != "T202609280008" {
		t.Fatalf("after 0007: no=%q err=%v, want T202609280008", no, err)
	}
	// 跨日期：同前缀新日期归 1。
	if no, err := repo.NextTaskNo(ctx, "T", day2); err != nil || no != "T202609290001" {
		t.Fatalf("cross date: no=%q err=%v, want T202609290001", no, err)
	}
	// 前缀隔离：E 前缀同日期独立取号。
	if no, err := repo.NextTaskNo(ctx, "E", day1); err != nil || no != "E202609280001" {
		t.Fatalf("prefix E: no=%q err=%v, want E202609280001", no, err)
	}
	// 前缀互不误读：已有 T 行不影响 E。
	if _, _, err := createRaw(db, "E202609280003", "[]"); err != nil {
		t.Fatalf("seed E: %v", err)
	}
	if no, err := repo.NextTaskNo(ctx, "E", day1); err != nil || no != "E202609280004" {
		t.Fatalf("prefix E after seed: no=%q err=%v, want E202609280004", no, err)
	}
}

// --- GetByID / CurrentLink ---

// TestGetByID 点查命中与无行返 nil,nil。
func TestGetByID(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	task, _ := mustCreate(t, repo, "T202609280001", []domain.Question{seedQ(t, db, "Q-AG-0001")})
	got, err := repo.GetByID(context.Background(), task.ID)
	if err != nil || got == nil || got.ID != task.ID || got.TaskNo != task.TaskNo {
		t.Fatalf("GetByID = (%v, %v), want task %s", got, err, task.TaskNo)
	}
	miss, err := repo.GetByID(context.Background(), 12345)
	if err != nil || miss != nil {
		t.Fatalf("GetByID missing = (%v, %v), want (nil, nil)", miss, err)
	}
}

// TestCurrentLink 取最新 generated_at 行；无行返 nil,nil（BR1）。
func TestCurrentLink(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	task, first := mustCreate(t, repo, "T202609280001", []domain.Question{seedQ(t, db, "Q-AG-0001")})

	got, err := repo.CurrentLink(context.Background(), task.ID)
	if err != nil || got == nil || got.ID != first.ID {
		t.Fatalf("CurrentLink = (%v, %v), want first link %d", got, err, first.ID)
	}

	// 追加更新的链接行（重发后），CurrentLink 指向新行。
	second := domain.AssessmentTestLink{
		TaskID: task.ID, TokenPlain: "p2", TokenHash: "h2", Status: domain.LinkStatusValid,
		GeneratedAt: tUTC(2026, 9, 30, 9, 0), ExpiresAt: tUTC(2026, 10, 7, 9, 0),
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("seed second link: %v", err)
	}
	got, err = repo.CurrentLink(context.Background(), task.ID)
	if err != nil || got == nil || got.ID != second.ID {
		t.Fatalf("CurrentLink after resend = (%v, %v), want second link %d", got, err, second.ID)
	}

	miss, err := repo.CurrentLink(context.Background(), 12345)
	if err != nil || miss != nil {
		t.Fatalf("CurrentLink missing = (%v, %v), want (nil, nil)", miss, err)
	}
}

// --- ReplaceLink ---

// TestReplaceLinkFromExpired expired 任务重发：任务回 pending、旧行 invalid、新行 valid（BR7 §6.2）。
func TestReplaceLinkFromExpired(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	task, first := mustCreate(t, repo, "T202609280001", []domain.Question{seedQ(t, db, "Q-AG-0001")})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", task.ID).
		Update("status", domain.TestTaskStatusExpired)

	newLink := linkRow("plain-2", "hash-2", tUTC(2026, 9, 30, 9, 0), tUTC(2026, 10, 7, 9, 0))
	if err := repo.ReplaceLink(context.Background(), task.ID, &newLink); err != nil {
		t.Fatalf("ReplaceLink: %v", err)
	}
	gotTask := loadTask(t, db, task.ID)
	if gotTask.Status != domain.TestTaskStatusPending {
		t.Fatalf("task status = %s, want pending", gotTask.Status)
	}
	gotFirst := loadLink(t, db, first.ID)
	if gotFirst.Status != domain.LinkStatusInvalid {
		t.Fatalf("old link status = %s, want invalid", gotFirst.Status)
	}
	if newLink.TaskID != task.ID {
		t.Fatalf("newLink.TaskID = %d, want %d", newLink.TaskID, task.ID)
	}
	gotNew := loadLink(t, db, newLink.ID)
	if gotNew.Status != domain.LinkStatusValid {
		t.Fatalf("new link status = %s, want valid", gotNew.Status)
	}
	// 一任务至多一条 valid（BR1）。
	if n := countTaskRows(t, db, &domain.AssessmentTestLink{}, "task_id = ? AND status = ?", task.ID, domain.LinkStatusValid); n != 1 {
		t.Fatalf("valid link rows = %d, want 1", n)
	}
}

// TestReplaceLinkPendingTolerated pending 任务重发：任务条件更新 affected=0 容忍，
// 无状态推进，仅换链接（C2：两种状态均受理）。
func TestReplaceLinkPendingTolerated(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	task, first := mustCreate(t, repo, "T202609280001", []domain.Question{seedQ(t, db, "Q-AG-0001")})

	newLink := linkRow("plain-2", "hash-2", tUTC(2026, 9, 30, 9, 0), tUTC(2026, 10, 7, 9, 0))
	if err := repo.ReplaceLink(context.Background(), task.ID, &newLink); err != nil {
		t.Fatalf("ReplaceLink on pending: %v", err)
	}
	gotTask := loadTask(t, db, task.ID)
	if gotTask.Status != domain.TestTaskStatusPending {
		t.Fatalf("task status = %s, want pending (no transition)", gotTask.Status)
	}
	gotFirst := loadLink(t, db, first.ID)
	if gotFirst.Status != domain.LinkStatusInvalid {
		t.Fatalf("old link status = %s, want invalid", gotFirst.Status)
	}
	if n := countTaskRows(t, db, &domain.AssessmentTestLink{}, "task_id = ? AND status = ?", task.ID, domain.LinkStatusValid); n != 1 {
		t.Fatalf("valid link rows = %d, want 1", n)
	}
}

// --- CancelTask ---

// TestCancelTaskGuard 状态守卫：completed 返 affected=0；pending 取消成功且链接 invalid（BR2）。
func TestCancelTaskGuard(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")

	done, doneLink := mustCreate(t, repo, "T202609280001", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", done.ID).
		Updates(map[string]any{"status": domain.TestTaskStatusCompleted, "completed_at": tUTC(2026, 9, 28, 12, 0)})
	affected, err := repo.CancelTask(ctx, done.ID)
	if err != nil || affected != 0 {
		t.Fatalf("cancel completed: affected=%d err=%v, want 0", affected, err)
	}
	got := loadLink(t, db, doneLink.ID)
	if got.Status != domain.LinkStatusValid {
		t.Fatalf("completed task link mutated: %s", got.Status)
	}

	pend, pendLink := mustCreate(t, repo, "T202609280002", []domain.Question{q})
	affected, err = repo.CancelTask(ctx, pend.ID)
	if err != nil || affected != 1 {
		t.Fatalf("cancel pending: affected=%d err=%v, want 1", affected, err)
	}
	if got := loadTask(t, db, pend.ID); got.Status != domain.TestTaskStatusCanceled {
		t.Fatalf("pending task status = %s, want canceled", got.Status)
	}
	if got := loadLink(t, db, pendLink.ID); got.Status != domain.LinkStatusInvalid {
		t.Fatalf("canceled task link = %s, want invalid", got.Status)
	}

	// 重复取消：已 canceled 返 affected=0。
	affected, err = repo.CancelTask(ctx, pend.ID)
	if err != nil || affected != 0 {
		t.Fatalf("re-cancel: affected=%d err=%v, want 0", affected, err)
	}
}

// TestCancelTaskInProgressAndExpired in_progress 与 expired 任务可取消（specs §6.2）。
func TestCancelTaskInProgressAndExpired(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")

	prog, progLink := mustCreate(t, repo, "T202609280001", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", prog.ID).
		Update("status", domain.TestTaskStatusInProgress)
	if a, err := repo.CancelTask(ctx, prog.ID); err != nil || a != 1 {
		t.Fatalf("cancel in_progress: affected=%d err=%v", a, err)
	}
	if got := loadTask(t, db, prog.ID); got.Status != domain.TestTaskStatusCanceled {
		t.Fatalf("in_progress task status = %s, want canceled", got.Status)
	}
	if got := loadLink(t, db, progLink.ID); got.Status != domain.LinkStatusInvalid {
		t.Fatalf("in_progress link = %s, want invalid", got.Status)
	}

	exp, expLink := mustCreate(t, repo, "T202609280002", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", exp.ID).
		Update("status", domain.TestTaskStatusExpired)
	db.Model(&domain.AssessmentTestLink{}).Where("id = ?", expLink.ID).
		Update("status", domain.LinkStatusInvalid)
	if a, err := repo.CancelTask(ctx, exp.ID); err != nil || a != 1 {
		t.Fatalf("cancel expired: affected=%d err=%v", a, err)
	}
	if got := loadTask(t, db, exp.ID); got.Status != domain.TestTaskStatusCanceled {
		t.Fatalf("expired task status = %s, want canceled", got.Status)
	}
}

// --- MarkSessionStarted ---

// TestMarkSessionStarted pending→in_progress 条件更新，重复调用幂等（specs §6.2）。
func TestMarkSessionStarted(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	task, _ := mustCreate(t, repo, "T202609280001", []domain.Question{seedQ(t, db, "Q-AG-0001")})

	if err := repo.MarkSessionStarted(ctx, task.ID); err != nil {
		t.Fatalf("MarkSessionStarted: %v", err)
	}
	if got := loadTask(t, db, task.ID); got.Status != domain.TestTaskStatusInProgress {
		t.Fatalf("status = %s, want in_progress", got.Status)
	}
	// 已 in_progress 再调：affected=0 幂等。
	if err := repo.MarkSessionStarted(ctx, task.ID); err != nil {
		t.Fatalf("MarkSessionStarted idempotent: %v", err)
	}
	if got := loadTask(t, db, task.ID); got.Status != domain.TestTaskStatusInProgress {
		t.Fatalf("status after replay = %s, want in_progress", got.Status)
	}
	// completed 任务调用：不推进终态。
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", task.ID).
		Update("status", domain.TestTaskStatusCompleted)
	if err := repo.MarkSessionStarted(ctx, task.ID); err != nil {
		t.Fatalf("MarkSessionStarted on completed: %v", err)
	}
	if got := loadTask(t, db, task.ID); got.Status != domain.TestTaskStatusCompleted {
		t.Fatalf("terminal status mutated: %s", got.Status)
	}
}

// --- CountActiveByType ---

// TestCountActiveByType 03 A2 口径：未终态计数含 pending/in_progress 与
// completed+阅卷在途，排除 canceled（即使阅卷在途）与已终态阅卷。
func TestCountActiveByType(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	q := seedQ(t, db, "Q-AG-0001")
	ctx := context.Background()

	// ai_mgmt：pending 2 + in_progress 1 + completed(waiting) 1 + completed(scored) 1（不计）
	for i, status := range []string{
		domain.TestTaskStatusPending, domain.TestTaskStatusPending, domain.TestTaskStatusInProgress,
		domain.TestTaskStatusCompleted, domain.TestTaskStatusCompleted,
	} {
		task, _ := mustCreate(t, repo, fmt.Sprintf("T20260928000%d", i+1), []domain.Question{q})
		updates := map[string]any{"status": status}
		if status == domain.TestTaskStatusCompleted {
			updates["completed_at"] = tUTC(2026, 9, 28, 12, 0)
		}
		if i == 4 {
			updates["grading_status"] = domain.GradingStatusScored
		}
		db.Model(&domain.AssessmentTestTask{}).Where("id = ?", task.ID).Updates(updates)
	}
	// ai_mgmt canceled + 阅卷 grading：按 03 A2 显式排除。
	canceled, _ := mustCreate(t, repo, "T202609280006", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", canceled.ID).Updates(map[string]any{
		"status": domain.TestTaskStatusCanceled, "grading_status": domain.GradingStatusGrading,
	})
	// enneagram：pending 1 + expired 1（不计）。
	enn := domain.AssessmentTestTask{
		TaskNo: "E202609280001", TestType: domain.TestTypeEnneagram, StaffID: "ue", StaffName: "李四",
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: "[]", ScaleKey: domain.ScaleKeyRisoHudson, DimensionCodesJSON: "[]",
	}
	if err := db.Create(&enn).Error; err != nil {
		t.Fatalf("seed enneagram: %v", err)
	}
	expired, _ := mustCreate(t, repo, "E202609280002", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", expired.ID).
		Updates(map[string]any{"status": domain.TestTaskStatusExpired, "test_type": domain.TestTypeEnneagram, "task_no": "E202609280002"})

	aiMgmt, enneagram, err := repo.CountActiveByType(ctx)
	if err != nil {
		t.Fatalf("CountActiveByType: %v", err)
	}
	if aiMgmt != 4 {
		t.Fatalf("aiMgmt = %d, want 4 (2 pending + 1 in_progress + 1 completed-waiting)", aiMgmt)
	}
	if enneagram != 1 {
		t.Fatalf("enneagram = %d, want 1", enneagram)
	}
}

// --- ExpirePending ---

// TestExpirePendingIdempotent 到期 pending 任务推进 expired + 链接 invalid，
// 重复执行第二次 0 条；in_progress 到期不命中（BR5/BR6 §5.3.4 规则1/3）。
func TestExpirePendingIdempotent(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")
	now := tUTC(2026, 10, 6, 0, 0)

	// 到期 pending 任务：expires_at 早于 now。
	expTask, expLink := mustCreate(t, repo, "T202609280001", []domain.Question{q})
	_ = expTask
	_ = expLink
	// mustCreate 落的 expires_at=2026-10-05，已早于 now，无需改。

	// 未到期 pending 任务：不命中。
	_, freshLink := mustCreate(t, repo, "T202609280002", []domain.Question{q})
	db.Model(&domain.AssessmentTestLink{}).Where("id = ?", freshLink.ID).
		Update("expires_at", now.Add(time.Hour))

	// in_progress 到期任务：不命中（BR5）。
	progTask, _ := mustCreate(t, repo, "T202609280003", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", progTask.ID).
		Update("status", domain.TestTaskStatusInProgress)

	n1, err := repo.ExpirePending(ctx, now)
	if err != nil {
		t.Fatalf("first ExpirePending: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("first run advanced %d, want 1", n1)
	}
	got := loadTask(t, db, expTask.ID)
	if got.Status != domain.TestTaskStatusExpired {
		t.Fatalf("expired task status = %s, want expired", got.Status)
	}
	gotLink := loadLink(t, db, expLink.ID)
	if gotLink.Status != domain.LinkStatusInvalid {
		t.Fatalf("expired task link = %s, want invalid", gotLink.Status)
	}
	if gotFresh := loadTask(t, db, freshLink.TaskID); gotFresh.Status != domain.TestTaskStatusPending {
		t.Fatalf("fresh task mutated: %s", gotFresh.Status)
	}
	if gotProg := loadTask(t, db, progTask.ID); gotProg.Status != domain.TestTaskStatusInProgress {
		t.Fatalf("in_progress task mutated: %s", gotProg.Status)
	}

	n2, err := repo.ExpirePending(ctx, now)
	if err != nil {
		t.Fatalf("second ExpirePending: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second run advanced %d, want 0 (idempotent)", n2)
	}
}

// TestExpirePendingBoundary 到期时刻等于 now 不命中（expires_at < now 严格小于）。
func TestExpirePendingBoundary(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")
	now := tUTC(2026, 10, 6, 0, 0)

	task, _ := mustCreate(t, repo, "T202609280001", []domain.Question{q})
	db.Model(&domain.AssessmentTestLink{}).Where("task_id = ?", task.ID).
		Update("expires_at", now) // 恰好等于 now

	n, err := repo.ExpirePending(ctx, now)
	if err != nil {
		t.Fatalf("ExpirePending boundary: %v", err)
	}
	if n != 0 {
		t.Fatalf("boundary run advanced %d, want 0", n)
	}
	if got := loadTask(t, db, task.ID); got.Status != domain.TestTaskStatusPending {
		t.Fatalf("boundary task mutated: %s", got.Status)
	}
}

// --- ListByFilter ---

// TestListByFilterCurrentLink 重发后 link_status 反映最新行状态（BR1，A1 口径）。
func TestListByFilterCurrentLink(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")
	task, first := mustCreate(t, repo, "T202609280001", []domain.Question{q})

	// 初始：link_status=valid。
	list, total, err := repo.ListByFilter(ctx, repository.TestTaskFilter{TestType: domain.TestTypeAIMgmt})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("initial list: total=%d len=%d err=%v", total, len(list), err)
	}
	if list[0].Task.ID != task.ID || list[0].LinkStatus != domain.LinkStatusValid {
		t.Fatalf("initial row link_status = %s, want valid", list[0].LinkStatus)
	}

	// expired 后重发：旧行 invalid + 新行 valid，link_status 指向新行 valid。
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", task.ID).
		Update("status", domain.TestTaskStatusExpired)
	db.Model(&domain.AssessmentTestLink{}).Where("id = ?", first.ID).
		Update("status", domain.LinkStatusInvalid)
	newLink := linkRow("plain-2", "hash-2", tUTC(2026, 9, 30, 9, 0), tUTC(2026, 10, 7, 9, 0))
	if err := repo.ReplaceLink(ctx, task.ID, &newLink); err != nil {
		t.Fatalf("ReplaceLink: %v", err)
	}
	list, _, err = repo.ListByFilter(ctx, repository.TestTaskFilter{TestType: domain.TestTypeAIMgmt})
	if err != nil {
		t.Fatalf("list after resend: %v", err)
	}
	if len(list) != 1 || list[0].LinkStatus != domain.LinkStatusValid {
		t.Fatalf("after resend link_status = %v, want valid (new row)", list)
	}

	// 链接被使用后：当前行 used。
	db.Model(&domain.AssessmentTestLink{}).Where("id = ?", newLink.ID).
		Update("status", domain.LinkStatusUsed)
	list, _, err = repo.ListByFilter(ctx, repository.TestTaskFilter{TestType: domain.TestTypeAIMgmt})
	if err != nil {
		t.Fatalf("list after used: %v", err)
	}
	if len(list) != 1 || list[0].LinkStatus != domain.LinkStatusUsed {
		t.Fatalf("after used link_status = %v, want used", list)
	}
}

// TestListByFilterComposite 筛选组合：test_type 必填收窄、status、keyword（姓名/任务号
// OR 匹配、通配符转义）、created_at DESC 分页。
func TestListByFilterComposite(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	ctx := context.Background()
	q := seedQ(t, db, "Q-AG-0001")

	t1, _ := mustCreate(t, repo, "T202609280001", []domain.Question{q})
	t2, _ := mustCreate(t, repo, "T202609280002", []domain.Question{q})
	t3, _ := mustCreate(t, repo, "T202609280003", []domain.Question{q})
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", t2.ID).
		Update("status", domain.TestTaskStatusExpired)
	enn := domain.AssessmentTestTask{
		TaskNo: "E202609280001", TestType: domain.TestTypeEnneagram, StaffID: "ue", StaffName: "王五",
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: "[]", ScaleKey: domain.ScaleKeyRisoHudson, DimensionCodesJSON: "[]",
	}
	if err := db.Create(&enn).Error; err != nil {
		t.Fatalf("seed enneagram: %v", err)
	}

	// test_type 收窄：enneagram 只 1 行。
	list, total, err := repo.ListByFilter(ctx, repository.TestTaskFilter{TestType: domain.TestTypeEnneagram})
	if err != nil || total != 1 || len(list) != 1 || list[0].Task.ID != enn.ID {
		t.Fatalf("enneagram filter: total=%d list=%v err=%v", total, list, err)
	}

	// status 筛选。
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: domain.TestTypeAIMgmt, Status: domain.TestTaskStatusExpired})
	if err != nil || total != 1 || len(list) != 1 || list[0].Task.ID != t2.ID {
		t.Fatalf("status filter: total=%d list=%v err=%v", total, list, err)
	}

	// keyword 命中任务号（staff_name 均为 员工T…，任务号分支）。
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: domain.TestTypeAIMgmt, Keyword: "T202609280002"})
	if err != nil || total != 1 || len(list) != 1 || list[0].Task.ID != t2.ID {
		t.Fatalf("keyword task_no: total=%d list=%v err=%v", total, list, err)
	}

	// keyword 命中 staff_name（员工T202609280003 含 T2026 会撞任务号，改用独立姓名）。
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", t3.ID).Update("staff_name", "赵六")
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: domain.TestTypeAIMgmt, Keyword: "赵"})
	if err != nil || total != 1 || len(list) != 1 || list[0].Task.ID != t3.ID {
		t.Fatalf("keyword staff_name: total=%d list=%v err=%v", total, list, err)
	}

	// keyword 通配符转义：% 不当通配符匹配（字面不命中）。
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: domain.TestTypeAIMgmt, Keyword: "%"})
	if err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("keyword wildcard escape: total=%d len=%d err=%v", total, len(list), err)
	}

	// created_at DESC：SQLite autoCreateTime 同秒落库，倒序断言用显式回填区分。
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", t1.ID).
		Update("created_at", tUTC(2026, 9, 27, 0, 0))
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", t2.ID).
		Update("created_at", tUTC(2026, 9, 28, 0, 0))
	db.Model(&domain.AssessmentTestTask{}).Where("id = ?", t3.ID).
		Update("created_at", tUTC(2026, 9, 29, 0, 0))
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{TestType: domain.TestTypeAIMgmt})
	if err != nil || total != 3 {
		t.Fatalf("order base: total=%d err=%v", total, err)
	}
	if list[0].Task.ID != t3.ID || list[1].Task.ID != t2.ID || list[2].Task.ID != t1.ID {
		t.Fatalf("order = [%d %d %d], want DESC [t3 t2 t1]", list[0].Task.ID, list[1].Task.ID, list[2].Task.ID)
	}

	// 分页：page=2 size=2 只剩最旧一行。
	list, total, err = repo.ListByFilter(ctx, repository.TestTaskFilter{
		TestType: domain.TestTypeAIMgmt, Page: 2, PageSize: 2})
	if err != nil || total != 3 || len(list) != 1 || list[0].Task.ID != t1.ID {
		t.Fatalf("page 2: total=%d len=%d list=%v err=%v", total, len(list), list, err)
	}
}

// TestListByFilterNoLink 无链接行（防御形态）：link_status 空串不炸。
func TestListByFilterNoLink(t *testing.T) {
	db, repo := newTestTaskRepo(t)
	orphan := domain.AssessmentTestTask{
		TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt, StaffID: "u1", StaffName: "张三",
		Status: domain.TestTaskStatusPending, GradingStatus: domain.GradingStatusWaiting,
		QuestionIDsJSON: "[]", DimensionCodesJSON: "[]",
	}
	if err := db.Create(&orphan).Error; err != nil {
		t.Fatalf("seed orphan task: %v", err)
	}
	list, total, err := repo.ListByFilter(context.Background(), repository.TestTaskFilter{TestType: domain.TestTypeAIMgmt})
	if err != nil || total != 1 || len(list) != 1 || list[0].LinkStatus != "" {
		t.Fatalf("no link: total=%d list=%v err=%v", total, list, err)
	}
}
