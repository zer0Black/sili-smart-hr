// Package repository_test 对作答记录仓储做黑盒集成测试（specs P2_TST_002
// §5.1.2/§5.2.2/§5.2.4）。每测试独立 :memory: SQLite，简化版雪花 Create 回调
// （同 assessment_test_task_test.go），覆盖逐行落库、按任务计数与有序读取、
// 链接令牌哈希点查的真实 SQL 行为。
package repository_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newAnswerDB 构造 :memory: SQLite（作答 + 链接两模型），雪花回调覆盖两模型。
func newAnswerDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_test_answer_repo", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.AssessmentTestAnswer:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *domain.AssessmentTestLink:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentTestAnswer{}, &domain.AssessmentTestLink{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// newAnswerRepo 构造被测仓储。
func newAnswerRepo(t *testing.T) (*gorm.DB, repository.AssessmentTestAnswerRepository) {
	t.Helper()
	db := newAnswerDB(t)
	return db, repository.NewAssessmentTestAnswerRepository(db)
}

// answerRow 组装一条回复行（ID 与时间留空，由回调与 Insert 填充）。
func answerRow(taskID int64, seq int, content string) domain.AssessmentTestAnswer {
	return domain.AssessmentTestAnswer{TaskID: taskID, QuestionSeq: seq, Content: content}
}

// --- Insert / CountByTask ---

// TestInsertAndCount Insert 两行（seq 1/2）后 CountByTask 返回 2、雪花 ID 非 0；
// 空任务计数 0；落库行字段与首写时间完整；跨任务计数隔离（BR2 §5.2.4 规则1
// COUNT 推算口径）。
func TestInsertAndCount(t *testing.T) {
	db, repo := newAnswerRepo(t)
	ctx := context.Background()
	const taskID int64 = 1001

	if n, err := repo.CountByTask(ctx, taskID); err != nil || n != 0 {
		t.Fatalf("empty CountByTask = (%d, %v), want (0, nil)", n, err)
	}
	first := answerRow(taskID, 1, "第一题回复")
	second := answerRow(taskID, 2, "第二题回复")
	for _, row := range []*domain.AssessmentTestAnswer{&first, &second} {
		if err := repo.Insert(ctx, row); err != nil {
			t.Fatalf("Insert seq %d: %v", row.QuestionSeq, err)
		}
	}
	if first.ID == 0 || second.ID == 0 {
		t.Fatalf("snowflake ids not assigned: %d %d", first.ID, second.ID)
	}
	if n, err := repo.CountByTask(ctx, taskID); err != nil || n != 2 {
		t.Fatalf("CountByTask = (%d, %v), want (2, nil)", n, err)
	}

	// 落库行直读核对：业务字段与首写时间完整。
	var stored domain.AssessmentTestAnswer
	if err := db.First(&stored, first.ID).Error; err != nil {
		t.Fatalf("load first row: %v", err)
	}
	if stored.TaskID != taskID || stored.QuestionSeq != 1 || stored.Content != "第一题回复" {
		t.Fatalf("stored row = task %d/seq %d/content %q", stored.TaskID, stored.QuestionSeq, stored.Content)
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Fatalf("stored times zero: created %v / updated %v", stored.CreatedAt, stored.UpdatedAt)
	}

	// 跨任务隔离：他任务落库不影响本任务计数。
	other := answerRow(2002, 1, "他任务回复")
	if err := repo.Insert(ctx, &other); err != nil {
		t.Fatalf("Insert other task: %v", err)
	}
	if n, err := repo.CountByTask(ctx, taskID); err != nil || n != 2 {
		t.Fatalf("CountByTask after other insert = (%d, %v), want (2, nil)", n, err)
	}
	if n, err := repo.CountByTask(ctx, 2002); err != nil || n != 1 {
		t.Fatalf("CountByTask other task = (%d, %v), want (1, nil)", n, err)
	}
}

// --- ListByTask ---

// TestListByTaskOrder 乱序 Insert seq 3/1/2 后 ListByTask 返回 [1,2,3]
// question_seq 升序（A1 断点恢复按题号有序，04 §3.1）；空任务空集无错误；
// 跨任务行不串入（BR3 §5.2.4 规则3 只增不改，读取全量按序）。
func TestListByTaskOrder(t *testing.T) {
	_, repo := newAnswerRepo(t)
	ctx := context.Background()
	const taskID int64 = 1001

	for _, seq := range []int{3, 1, 2} {
		row := answerRow(taskID, seq, "回复"+strconv.Itoa(seq))
		if err := repo.Insert(ctx, &row); err != nil {
			t.Fatalf("Insert seq %d: %v", seq, err)
		}
	}
	rows, err := repo.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows len = %d, want 3", len(rows))
	}
	for i, want := range []int{1, 2, 3} {
		if rows[i].QuestionSeq != want {
			t.Fatalf("rows[%d].question_seq = %d, want %d", i, rows[i].QuestionSeq, want)
		}
	}

	// 空任务：空集无错误。
	empty, err := repo.ListByTask(ctx, 9999)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListByTask empty = (%v, %v), want empty/nil", empty, err)
	}

	// 跨任务隔离：他任务行不串入本任务结果。
	other := answerRow(2002, 1, "他任务回复")
	if err := repo.Insert(ctx, &other); err != nil {
		t.Fatalf("Insert other task: %v", err)
	}
	rows, err = repo.ListByTask(ctx, taskID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("ListByTask after other insert: len=%d err=%v, want 3/nil", len(rows), err)
	}
}

// --- FindLinkByTokenHash ---

// TestFindLinkByTokenHash 预插 F7 链接行（TokenHash="abc123"）后点查返回该行；
// 不存在哈希返回 (nil, nil) 无错误（BR1 §5.1.2 步骤1 token_hash 点查，
// valid 判定归 service）。
func TestFindLinkByTokenHash(t *testing.T) {
	db, repo := newAnswerRepo(t)
	ctx := context.Background()

	seed := domain.AssessmentTestLink{
		TaskID: 1001, TokenPlain: "plain-abc", TokenHash: "abc123",
		Status:      domain.LinkStatusValid,
		GeneratedAt: tUTC(2026, 9, 30, 8, 0), ExpiresAt: tUTC(2026, 10, 7, 8, 0),
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}
	got, err := repo.FindLinkByTokenHash(ctx, "abc123")
	if err != nil || got == nil || got.ID != seed.ID {
		t.Fatalf("FindLinkByTokenHash = (%v, %v), want seeded link %d", got, err, seed.ID)
	}
	if got.TokenHash != "abc123" || got.Status != domain.LinkStatusValid || got.TaskID != 1001 {
		t.Fatalf("found link = hash %s/status %s/task %d", got.TokenHash, got.Status, got.TaskID)
	}
	miss, err := repo.FindLinkByTokenHash(ctx, "deadbeef")
	if err != nil || miss != nil {
		t.Fatalf("FindLinkByTokenHash missing = (%v, %v), want (nil, nil)", miss, err)
	}
}

// --- 索引 ---

// TestIndexCreated AutoMigrate 后 idx_task_seq 存在（task_id+question_seq，
// 恢复/计数/阅卷全部消费路径共用索引，04 §3.1）。
func TestIndexCreated(t *testing.T) {
	db := newAnswerDB(t)
	if !db.Migrator().HasIndex(&domain.AssessmentTestAnswer{}, "idx_task_seq") {
		t.Fatal("index idx_task_seq not created")
	}
}
