// Package repository_test 对 question_batch 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite，内联注册反射版雪花 Create 回调（覆盖 Question 与
// QuestionBatch 及其 slice，与生产 model 包全局回调等效），AutoMigrate 两表。
// 覆盖卡区列表/建批事务/批次号生成/确认入库/作废/重新送审归批的真实 SQL 行为。
package repository_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newQBatchTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate Question 与
// QuestionBatch 两表，注册反射版雪花 Create 回调（struct 与 slice 均覆盖，
// 支撑 CreateInBatches 的分段 dest）。
func newQBatchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_batch_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignQBatchTestSnowflakeID(tx.Statement.Dest)
	})
	if err := db.AutoMigrate(&domain.Question{}, &domain.QuestionBatch{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func assignQBatchTestSnowflakeID(dest any) {
	v := reflect.ValueOf(dest)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		setQBatchTestID(v)
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			for elem.Kind() == reflect.Ptr {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				setQBatchTestID(elem)
			}
		}
	}
}

func setQBatchTestID(v reflect.Value) {
	f := v.FieldByName("ID")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 && f.Int() == 0 {
		f.SetInt(snowflake.NextID())
	}
}

// seedBatch 写入一行批次，返回带 ID 的实体。
func seedBatch(t *testing.T, db *gorm.DB, batchNo, source, batchType, status string, count int) domain.QuestionBatch {
	t.Helper()
	b := domain.QuestionBatch{
		BatchNo:       batchNo,
		Title:         "标题-" + batchNo,
		Source:        source,
		BatchType:     batchType,
		Status:        status,
		QuestionCount: count,
	}
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("seed batch %s: %v", batchNo, err)
	}
	return b
}

// seedBatchQuestion 写入一行挂批题目，version 置 1（业务约定）。
func seedBatchQuestion(t *testing.T, db *gorm.DB, batchID int64, questionNo, source, status, rejectReason string) domain.Question {
	t.Helper()
	q := domain.Question{
		QuestionNo:   questionNo,
		Source:       source,
		DimensionID:  101,
		Scenario:     "情境-" + questionNo,
		Requirement:  "要求-" + questionNo,
		FocusPoint:   "考察-" + questionNo,
		Status:       status,
		RejectReason: rejectReason,
		BatchID:      batchID,
		Version:      1,
	}
	if err := db.Create(&q).Error; err != nil {
		t.Fatalf("seed question %s: %v", questionNo, err)
	}
	return q
}

// TestQuestionBatchListPending 卡区列表：只返回 PENDING，按 created_at DESC。
func TestQuestionBatchListPending(t *testing.T) {
	db := newQBatchTestDB(t)
	p1 := seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 3)
	p2 := seedBatch(t, db, "#G0925-2", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 5)
	seedBatch(t, db, "#G0924", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 2)
	seedBatch(t, db, "#G0923", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusVoided, 2)
	// 手动错开 created_at：p1 晚于 p2，期望 DESC 序 [p1 p2]。
	db.Model(&domain.QuestionBatch{}).Where("id = ?", p1.ID).Update("created_at", time.Now())
	db.Model(&domain.QuestionBatch{}).Where("id = ?", p2.ID).Update("created_at", time.Now().Add(-time.Hour))

	repo := repository.NewQuestionBatchRepository(db)
	list, err := repo.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(list) != 2 || list[0].ID != p1.ID || list[1].ID != p2.ID {
		t.Fatalf("want [p1 p2] PENDING only, got %+v", list)
	}
}

// TestQuestionBatchFindByID 主键查与不存在哨兵。
func TestQuestionBatchFindByID(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#R0925", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending, 1)

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.FindByID(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != b.ID || got.BatchNo != "#R0925" || got.BatchType != domain.QuestionBatchTypeResubmit {
		t.Fatalf("want batch %s, got %+v", b.BatchNo, got)
	}
	if _, err := repo.FindByID(context.Background(), 12345); err != gorm.ErrRecordNotFound {
		t.Fatalf("missing id want ErrRecordNotFound, got %v", err)
	}
}

// TestFindPendingResubmitBatch 待并入批判定：source=AI 且 RESUBMIT 且 PENDING 中
// created_at 最早一条；非 AI/非 RESUBMIT/非 PENDING 均排除。
func TestFindPendingResubmitBatch(t *testing.T) {
	db := newQBatchTestDB(t)
	late := seedBatch(t, db, "#R0925", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending, 2)
	early := seedBatch(t, db, "#R0925-2", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending, 1)
	seedBatch(t, db, "#R0924", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusClosed, 1)
	seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 3)
	seedBatch(t, db, "#S0925", domain.QuestionSourceScale, domain.QuestionBatchTypeImport, domain.QuestionBatchStatusPending, 144)
	db.Model(&domain.QuestionBatch{}).Where("id = ?", late.ID).Update("created_at", time.Now())
	db.Model(&domain.QuestionBatch{}).Where("id = ?", early.ID).Update("created_at", time.Now().Add(-time.Hour))

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.FindPendingResubmitBatch(context.Background())
	if err != nil {
		t.Fatalf("FindPendingResubmitBatch: %v", err)
	}
	if got.ID != early.ID {
		t.Fatalf("want earliest batch %s, got %s", early.BatchNo, got.BatchNo)
	}
}

// TestFindPendingResubmitBatch_Empty 无待并入批返回 ErrRecordNotFound。
func TestFindPendingResubmitBatch_Empty(t *testing.T) {
	db := newQBatchTestDB(t)
	seedBatch(t, db, "#R0925", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusClosed, 1)

	repo := repository.NewQuestionBatchRepository(db)
	if _, err := repo.FindPendingResubmitBatch(context.Background()); err != gorm.ErrRecordNotFound {
		t.Fatalf("no pending resubmit want ErrRecordNotFound, got %v", err)
	}
}

// TestCreateBatchWithQuestions 建批事务：批次行落库、题目编号按前缀分流连续分配
// （含软删行占号）、status 强制 PENDING、batch_id 回填、version 补 1。
func TestCreateBatchWithQuestions(t *testing.T) {
	db := newQBatchTestDB(t)
	// 预存占号：AI 已到 0005，量表已到 0002（软删行同样占号）。
	seedAI(t, db, "Q-AG-0005", domain.QuestionStatusActive, "情境五", 101)
	seedQuestion(t, db, "Q-Scale-0002", domain.QuestionSourceScale, domain.QuestionStatusActive, "陈述二", 201)

	batch := domain.QuestionBatch{
		BatchNo: "#S0925", Title: "Essence 精简量表", Source: domain.QuestionSourceScale,
		BatchType: domain.QuestionBatchTypeImport, Status: domain.QuestionBatchStatusPending,
		QuestionCount: 5, ScaleKey: domain.ScaleKeyEssence,
	}
	questions := []domain.Question{
		{Source: domain.QuestionSourceScale, DimensionID: 301, Scenario: "s1", Requirement: "r1", FocusPoint: "f1"},
		{Source: domain.QuestionSourceScale, DimensionID: 302, Scenario: "s2", Requirement: "r2", FocusPoint: "f2"},
		{Source: domain.QuestionSourceAI, DimensionID: 101, Scenario: "s3", Requirement: "r3", FocusPoint: "f3"},
		{Source: domain.QuestionSourceAI, DimensionID: 102, Scenario: "s4", Requirement: "r4", FocusPoint: "f4"},
		{Source: domain.QuestionSourceAI, DimensionID: 103, Scenario: "s5", Requirement: "r5", FocusPoint: "f5"},
	}

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.CreateBatchWithQuestions(context.Background(), nil, &batch, &questions); err != nil {
		t.Fatalf("CreateBatchWithQuestions: %v", err)
	}
	if batch.ID == 0 {
		t.Fatal("batch ID want assigned by snowflake callback")
	}
	wantNos := []string{"Q-Scale-0003", "Q-Scale-0004", "Q-AG-0006", "Q-AG-0007", "Q-AG-0008"}
	for i, q := range questions {
		if q.ID == 0 {
			t.Fatalf("question %d ID want assigned", i)
		}
		if q.QuestionNo != wantNos[i] {
			t.Fatalf("question %d no want %s, got %s", i, wantNos[i], q.QuestionNo)
		}
		if q.Status != domain.QuestionStatusPending || q.BatchID != batch.ID {
			t.Fatalf("question %d want PENDING + batch_id, got status=%s batch_id=%d", i, q.Status, q.BatchID)
		}
		if q.Version != 1 {
			t.Fatalf("question %d version want 1, got %d", i, q.Version)
		}
	}
	// DB 落库验证：批次行存在、题目行与出参一致。
	gotBatch, err := repo.FindByID(context.Background(), batch.ID)
	if err != nil {
		t.Fatalf("FindByID batch: %v", err)
	}
	if gotBatch.BatchNo != "#S0925" || gotBatch.QuestionCount != 5 {
		t.Fatalf("batch row mismatch: %+v", gotBatch)
	}
	var rows []domain.Question
	if err := db.Where("batch_id = ?", batch.ID).Order("question_no").Find(&rows).Error; err != nil {
		t.Fatalf("load questions: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 question rows, got %d", len(rows))
	}
	gotNos := map[string]bool{}
	for _, row := range rows {
		if row.Status != domain.QuestionStatusPending {
			t.Fatalf("row %s want PENDING, got %s", row.QuestionNo, row.Status)
		}
		gotNos[row.QuestionNo] = true
	}
	for _, want := range wantNos {
		if !gotNos[want] {
			t.Fatalf("want question row %s persisted, got %v", want, gotNos)
		}
	}
}

// TestCreateBatchWithQuestions_SoftDeletedSeq 编号只增不复用：软删行占号后新号续段。
func TestCreateBatchWithQuestions_SoftDeletedSeq(t *testing.T) {
	db := newQBatchTestDB(t)
	deleted := seedAI(t, db, "Q-AG-0007", domain.QuestionStatusActive, "情境七", 101)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	batch := domain.QuestionBatch{
		BatchNo: "#G0925", Title: "授权与分工 ×2", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending, QuestionCount: 2,
	}
	questions := []domain.Question{
		{Source: domain.QuestionSourceAI, DimensionID: 101, Scenario: "s1", Requirement: "r1", FocusPoint: "f1"},
		{Source: domain.QuestionSourceAI, DimensionID: 102, Scenario: "s2", Requirement: "r2", FocusPoint: "f2"},
	}

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.CreateBatchWithQuestions(context.Background(), nil, &batch, &questions); err != nil {
		t.Fatalf("CreateBatchWithQuestions: %v", err)
	}
	if questions[0].QuestionNo != "Q-AG-0008" || questions[1].QuestionNo != "Q-AG-0009" {
		t.Fatalf("want Q-AG-0008/0009 (soft-deleted 0007 counted), got %s/%s",
			questions[0].QuestionNo, questions[1].QuestionNo)
	}
}

// TestCreateBatchWithQuestions_ExternalTxRollback tx 传入外层事务复用：外层回滚时
// 批次与题目一并不落库（证明未自开事务提交）。
func TestCreateBatchWithQuestions_ExternalTxRollback(t *testing.T) {
	db := newQBatchTestDB(t)
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	batch := domain.QuestionBatch{
		BatchNo: "#G0925", Title: "授权与分工 ×1", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending, QuestionCount: 1,
	}
	questions := []domain.Question{
		{Source: domain.QuestionSourceAI, DimensionID: 101, Scenario: "s1", Requirement: "r1", FocusPoint: "f1"},
	}

	repo := repository.NewQuestionBatchRepository(db)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := repo.CreateBatchWithQuestions(context.Background(), tx, &batch, &questions); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("outer transaction want rollback error")
	}
	var batchN, questionN int64
	db.Model(&domain.QuestionBatch{}).Count(&batchN)
	db.Model(&domain.Question{}).Count(&questionN)
	if batchN != 0 || questionN != 1 {
		t.Fatalf("after rollback want 0 batch rows and only seeded question, got batch=%d question=%d", batchN, questionN)
	}
}

// TestCreateBatchWithQuestions_ExternalTxCommit tx 传入外层事务正常提交路径。
func TestCreateBatchWithQuestions_ExternalTxCommit(t *testing.T) {
	db := newQBatchTestDB(t)
	batch := domain.QuestionBatch{
		BatchNo: "#G0925", Title: "授权与分工 ×1", Source: domain.QuestionSourceAI,
		BatchType: domain.QuestionBatchTypeGenerate, Status: domain.QuestionBatchStatusPending, QuestionCount: 1,
	}
	questions := []domain.Question{
		{Source: domain.QuestionSourceAI, DimensionID: 101, Scenario: "s1", Requirement: "r1", FocusPoint: "f1"},
	}

	repo := repository.NewQuestionBatchRepository(db)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return repo.CreateBatchWithQuestions(context.Background(), tx, &batch, &questions)
	}); err != nil {
		t.Fatalf("outer transaction: %v", err)
	}
	var batchN int64
	db.Model(&domain.QuestionBatch{}).Count(&batchN)
	if batchN != 1 {
		t.Fatalf("after commit want 1 batch row, got %d", batchN)
	}
	if questions[0].BatchID != batch.ID || questions[0].QuestionNo != "Q-AG-0001" {
		t.Fatalf("question want batch_id=%d no=Q-AG-0001, got batch_id=%d no=%s",
			batch.ID, questions[0].BatchID, questions[0].QuestionNo)
	}
}

// TestConfirmBatchSplitsStatus 确认入库核心：12 题 PENDING 批标 1 题驳回后 Confirm，
// admitted=11、rejectedCount=1；DB 11 行 ACTIVE、1 行 REJECTED 且 reason 落值；
// 批次 CLOSED、closed_at 非零。
func TestConfirmBatchSplitsStatus(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 12)
	var rejectedID int64
	for i := 1; i <= 12; i++ {
		q := seedBatchQuestion(t, db, b.ID, fmt.Sprintf("Q-AG-%04d", i), domain.QuestionSourceAI, domain.QuestionStatusPending, "")
		if i == 5 {
			rejectedID = q.ID
		}
	}

	repo := repository.NewQuestionBatchRepository(db)
	admitted, rejectedCount, err := repo.ConfirmBatch(context.Background(), b.ID, map[int64]string{
		rejectedID: "情境仅绑定互联网广告销售，场景迁移性差",
	})
	if err != nil {
		t.Fatalf("ConfirmBatch: %v", err)
	}
	if admitted != 11 || rejectedCount != 1 {
		t.Fatalf("want admitted=11 rejected=1, got %d/%d", admitted, rejectedCount)
	}

	var activeN, rejectedN int64
	db.Model(&domain.Question{}).Where("batch_id = ? AND status = ?", b.ID, domain.QuestionStatusActive).Count(&activeN)
	db.Model(&domain.Question{}).Where("batch_id = ? AND status = ?", b.ID, domain.QuestionStatusRejected).Count(&rejectedN)
	if activeN != 11 || rejectedN != 1 {
		t.Fatalf("DB want 11 ACTIVE + 1 REJECTED, got %d/%d", activeN, rejectedN)
	}
	var rejectedRow domain.Question
	if err := db.Where("id = ?", rejectedID).First(&rejectedRow).Error; err != nil {
		t.Fatalf("load rejected row: %v", err)
	}
	if rejectedRow.RejectReason != "情境仅绑定互联网广告销售，场景迁移性差" {
		t.Fatalf("reject_reason want saved, got %q", rejectedRow.RejectReason)
	}
	if rejectedRow.Version != 2 {
		t.Fatalf("rejected row version want 2, got %d", rejectedRow.Version)
	}

	gotBatch, err := repo.FindByID(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("FindByID batch: %v", err)
	}
	if gotBatch.Status != domain.QuestionBatchStatusClosed {
		t.Fatalf("batch status want CLOSED, got %s", gotBatch.Status)
	}
	if gotBatch.ClosedAt == nil || gotBatch.ClosedAt.IsZero() {
		t.Fatal("closed_at want non-zero")
	}
	if gotBatch.VoidedAt != nil {
		t.Fatal("voided_at want nil on confirm")
	}
}

// TestConfirmBatchAllAdmitted 无驳回全入库（BR1 规则 2：未标记题全部启用入库）。
func TestConfirmBatchAllAdmitted(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#S0925", domain.QuestionSourceScale, domain.QuestionBatchTypeImport, domain.QuestionBatchStatusPending, 3)
	for i := 1; i <= 3; i++ {
		seedBatchQuestion(t, db, b.ID, fmt.Sprintf("Q-Scale-%04d", i), domain.QuestionSourceScale, domain.QuestionStatusPending, "")
	}

	repo := repository.NewQuestionBatchRepository(db)
	admitted, rejectedCount, err := repo.ConfirmBatch(context.Background(), b.ID, nil)
	if err != nil {
		t.Fatalf("ConfirmBatch: %v", err)
	}
	if admitted != 3 || rejectedCount != 0 {
		t.Fatalf("want 3/0, got %d/%d", admitted, rejectedCount)
	}
	var n int64
	db.Model(&domain.Question{}).Where("batch_id = ? AND status = ?", b.ID, domain.QuestionStatusActive).Count(&n)
	if n != 3 {
		t.Fatalf("want 3 ACTIVE rows, got %d", n)
	}
}

// TestConfirmBatchNotPending 非 PENDING 批 Confirm 拒绝：CLOSED 与 VOIDED 均返
// ErrBatchNotPending 且题目无变更（BR4/BR6）。
func TestConfirmBatchNotPending(t *testing.T) {
	db := newQBatchTestDB(t)
	closed := seedBatch(t, db, "#G0924", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 2)
	for i := 1; i <= 2; i++ {
		seedBatchQuestion(t, db, closed.ID, fmt.Sprintf("Q-AG-%04d", i), domain.QuestionSourceAI, domain.QuestionStatusPending, "")
	}
	voided := seedBatch(t, db, "#G0923", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusVoided, 1)
	seedBatchQuestion(t, db, voided.ID, "Q-AG-0003", domain.QuestionSourceAI, domain.QuestionStatusPending, "")

	repo := repository.NewQuestionBatchRepository(db)
	admitted, rejectedCount, err := repo.ConfirmBatch(context.Background(), closed.ID, nil)
	if !errors.Is(err, repository.ErrBatchNotPending) {
		t.Fatalf("closed batch want ErrBatchNotPending, got %v", err)
	}
	if admitted != 0 || rejectedCount != 0 {
		t.Fatalf("want 0/0 on not pending, got %d/%d", admitted, rejectedCount)
	}
	if _, _, err := repo.ConfirmBatch(context.Background(), voided.ID, nil); !errors.Is(err, repository.ErrBatchNotPending) {
		t.Fatalf("voided batch want ErrBatchNotPending, got %v", err)
	}
	// 批次不存在同样拒绝。
	if _, _, err := repo.ConfirmBatch(context.Background(), 999999, nil); !errors.Is(err, repository.ErrBatchNotPending) {
		t.Fatalf("missing batch want ErrBatchNotPending, got %v", err)
	}
	// 题目无变更：仍 PENDING、version 未动。
	var n int64
	db.Model(&domain.Question{}).Where("batch_id = ? AND status = ? AND version = 1", closed.ID, domain.QuestionStatusPending).Count(&n)
	if n != 2 {
		t.Fatalf("questions must be untouched, want 2 PENDING v1 rows, got %d", n)
	}
}

// TestVoidGenerateSoftDeletes GENERATE 批作废：批内题目批量软删、批次 VOIDED（BR5）。
func TestVoidGenerateSoftDeletes(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 2)
	seedBatchQuestion(t, db, b.ID, "Q-AG-0001", domain.QuestionSourceAI, domain.QuestionStatusPending, "")
	seedBatchQuestion(t, db, b.ID, "Q-AG-0002", domain.QuestionSourceAI, domain.QuestionStatusPending, "")

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.VoidBatch(context.Background(), b.ID); err != nil {
		t.Fatalf("VoidBatch: %v", err)
	}
	var rows []domain.Question
	if err := db.Unscoped().Where("batch_id = ?", b.ID).Find(&rows).Error; err != nil {
		t.Fatalf("load unscoped rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 physical rows, got %d", len(rows))
	}
	for _, row := range rows {
		if !row.DeletedAt.Valid {
			t.Fatalf("question %s deleted_at want set", row.QuestionNo)
		}
	}
	gotBatch, err := repo.FindByID(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("FindByID batch: %v", err)
	}
	if gotBatch.Status != domain.QuestionBatchStatusVoided {
		t.Fatalf("batch status want VOIDED, got %s", gotBatch.Status)
	}
	if gotBatch.VoidedAt == nil || gotBatch.VoidedAt.IsZero() {
		t.Fatal("voided_at want non-zero")
	}
}

// TestVoidImportSoftDeletes IMPORT 批作废与 GENERATE 同语义：题目随批软删。
func TestVoidImportSoftDeletes(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#S0925", domain.QuestionSourceScale, domain.QuestionBatchTypeImport, domain.QuestionBatchStatusPending, 1)
	seedBatchQuestion(t, db, b.ID, "Q-Scale-0001", domain.QuestionSourceScale, domain.QuestionStatusPending, "")

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.VoidBatch(context.Background(), b.ID); err != nil {
		t.Fatalf("VoidBatch: %v", err)
	}
	var row domain.Question
	if err := db.Unscoped().Where("batch_id = ?", b.ID).First(&row).Error; err != nil {
		t.Fatalf("load unscoped row: %v", err)
	}
	if !row.DeletedAt.Valid {
		t.Fatal("import batch question deleted_at want set")
	}
}

// TestVoidResubmitRestoresRejected RESUBMIT 批作废：题目回退 REJECTED 且保留原
// 驳回原因不覆盖（BR5）。
func TestVoidResubmitRestoresRejected(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#R0925", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending, 1)
	seedBatchQuestion(t, db, b.ID, "Q-AG-0009", domain.QuestionSourceAI, domain.QuestionStatusPending, "旧原因")

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.VoidBatch(context.Background(), b.ID); err != nil {
		t.Fatalf("VoidBatch: %v", err)
	}
	var row domain.Question
	if err := db.Where("batch_id = ?", b.ID).First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Status != domain.QuestionStatusRejected {
		t.Fatalf("status want REJECTED, got %s", row.Status)
	}
	if row.RejectReason != "旧原因" {
		t.Fatalf("reject_reason want preserved '旧原因', got %q", row.RejectReason)
	}
	if row.DeletedAt.Valid {
		t.Fatal("resubmit question must not be soft-deleted on void")
	}
}

// TestVoidBatchNotPending 非 PENDING 批作废拒绝。
func TestVoidBatchNotPending(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#G0924", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	q := seedBatchQuestion(t, db, b.ID, "Q-AG-0001", domain.QuestionSourceAI, domain.QuestionStatusPending, "")

	repo := repository.NewQuestionBatchRepository(db)
	if err := repo.VoidBatch(context.Background(), b.ID); !errors.Is(err, repository.ErrBatchNotPending) {
		t.Fatalf("closed batch want ErrBatchNotPending, got %v", err)
	}
	// 题目未被软删。
	var row domain.Question
	if err := db.Where("id = ?", q.ID).First(&row).Error; err != nil {
		t.Fatalf("question must remain visible: %v", err)
	}
}

// TestListQuestionsByBatchID 批内题目全量列表：question_no 升序、软删自动过滤、
// 其他批次题目不混入。
func TestListQuestionsByBatchID(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 3)
	other := seedBatch(t, db, "#S0925", domain.QuestionSourceScale, domain.QuestionBatchTypeImport, domain.QuestionBatchStatusPending, 1)
	seedBatchQuestion(t, db, b.ID, "Q-AG-0002", domain.QuestionSourceAI, domain.QuestionStatusPending, "")
	seedBatchQuestion(t, db, b.ID, "Q-AG-0001", domain.QuestionSourceAI, domain.QuestionStatusPending, "")
	deleted := seedBatchQuestion(t, db, b.ID, "Q-AG-0003", domain.QuestionSourceAI, domain.QuestionStatusPending, "")
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	seedBatchQuestion(t, db, other.ID, "Q-Scale-0001", domain.QuestionSourceScale, domain.QuestionStatusPending, "")

	repo := repository.NewQuestionBatchRepository(db)
	rows, err := repo.ListQuestionsByBatchID(context.Background(), b.ID)
	if err != nil {
		t.Fatalf("ListQuestionsByBatchID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (soft-deleted filtered, foreign excluded), got %d", len(rows))
	}
	if rows[0].QuestionNo != "Q-AG-0001" || rows[1].QuestionNo != "Q-AG-0002" {
		t.Fatalf("want question_no ASC [Q-AG-0001 Q-AG-0002], got [%s %s]", rows[0].QuestionNo, rows[1].QuestionNo)
	}
}

// TestListQuestionsByBatchID_EmptyBatch 空批与不存在批均返回空列表非 nil 非 error。
func TestListQuestionsByBatchID_EmptyBatch(t *testing.T) {
	db := newQBatchTestDB(t)
	b := seedBatch(t, db, "#G0925", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusPending, 0)

	repo := repository.NewQuestionBatchRepository(db)
	for _, id := range []int64{b.ID, 999999} {
		rows, err := repo.ListQuestionsByBatchID(context.Background(), id)
		if err != nil {
			t.Fatalf("empty batch %d: %v", id, err)
		}
		if rows == nil || len(rows) != 0 {
			t.Fatalf("empty batch %d want non-nil empty slice, got %v", id, rows)
		}
	}
}

// TestNextBatchNoFirst 同日同前缀无既有批：首批无后缀。
func TestNextBatchNoFirst(t *testing.T) {
	db := newQBatchTestDB(t)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.NextBatchNo(context.Background(), "G", now)
	if err != nil {
		t.Fatalf("NextBatchNo: %v", err)
	}
	if got != "#G0923" {
		t.Fatalf("want #G0923, got %s", got)
	}
}

// TestNextBatchNoSameDay 同日已有 #G0923 与 #G0923-2，推导 #G0923-3。
func TestNextBatchNoSameDay(t *testing.T) {
	db := newQBatchTestDB(t)
	seedBatch(t, db, "#G0923", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	seedBatch(t, db, "#G0923-2", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	now := time.Date(2026, 9, 23, 15, 0, 0, 0, time.Local)

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.NextBatchNo(context.Background(), "G", now)
	if err != nil {
		t.Fatalf("NextBatchNo: %v", err)
	}
	if got != "#G0923-3" {
		t.Fatalf("want #G0923-3, got %s", got)
	}
}

// TestNextBatchNoSuffixGrowth 后缀跨位：-9 之后是 -10（长度序取最大，多库兼容）。
func TestNextBatchNoSuffixGrowth(t *testing.T) {
	db := newQBatchTestDB(t)
	seedBatch(t, db, "#G0923", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	for i := 2; i <= 9; i++ {
		seedBatch(t, db, fmt.Sprintf("#G0923-%d", i), domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	}
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.NextBatchNo(context.Background(), "G", now)
	if err != nil {
		t.Fatalf("NextBatchNo: %v", err)
	}
	if got != "#G0923-10" {
		t.Fatalf("want #G0923-10, got %s", got)
	}
}

// TestNextBatchNoIsolatedByPrefixAndDay 前缀与日期双重隔离：他前缀/他日批次不参与推导。
func TestNextBatchNoIsolatedByPrefixAndDay(t *testing.T) {
	db := newQBatchTestDB(t)
	seedBatch(t, db, "#G0923", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	seedBatch(t, db, "#G0923-2", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	seedBatch(t, db, "#G0831-5", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.Local)

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.NextBatchNo(context.Background(), "S", now)
	if err != nil {
		t.Fatalf("NextBatchNo S: %v", err)
	}
	if got != "#S0923" {
		t.Fatalf("other prefix want #S0923, got %s", got)
	}
	got, err = repo.NextBatchNo(context.Background(), "G", time.Date(2026, 8, 31, 10, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("NextBatchNo G other day: %v", err)
	}
	if got != "#G0831-6" {
		t.Fatalf("other day want #G0831-6, got %s", got)
	}
}

// TestResubmitMergesIntoPendingBatch 并入待审核 RESUBMIT 批：question_count +1、
// 题目 batch_id 改挂、status 置 PENDING、文本更新、reject_reason 保留（BR3）。
func TestResubmitMergesIntoPendingBatch(t *testing.T) {
	db := newQBatchTestDB(t)
	pending := seedBatch(t, db, "#R0925", domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending, 2)
	oldGen := seedBatch(t, db, "#G0920", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	q := seedBatchQuestion(t, db, oldGen.ID, "Q-AG-0009", domain.QuestionSourceAI, domain.QuestionStatusRejected, "旧原因")

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.ResubmitToBatch(context.Background(), &q, map[string]any{
		"scenario":     "修正后的情境",
		"requirement":  "修正后的要求",
		"focus_point":  "修正后的考察点",
		"dimension_id": int64(102),
	})
	if err != nil {
		t.Fatalf("ResubmitToBatch: %v", err)
	}
	if got.ID != pending.ID || got.QuestionCount != 3 {
		t.Fatalf("want merged into batch %d count=3, got batch %d count=%d", pending.ID, got.ID, got.QuestionCount)
	}
	// DB 批次计数落值。
	dbBatch, err := repo.FindByID(context.Background(), pending.ID)
	if err != nil {
		t.Fatalf("FindByID batch: %v", err)
	}
	if dbBatch.QuestionCount != 3 {
		t.Fatalf("DB question_count want 3, got %d", dbBatch.QuestionCount)
	}
	// 题目改挂与状态推进。
	var row domain.Question
	if err := db.Where("id = ?", q.ID).First(&row).Error; err != nil {
		t.Fatalf("load question: %v", err)
	}
	if row.BatchID != pending.ID {
		t.Fatalf("question batch_id want %d, got %d", pending.ID, row.BatchID)
	}
	if row.Status != domain.QuestionStatusPending {
		t.Fatalf("status want PENDING, got %s", row.Status)
	}
	if row.Scenario != "修正后的情境" || row.Requirement != "修正后的要求" || row.FocusPoint != "修正后的考察点" || row.DimensionID != 102 {
		t.Fatalf("text fields not updated: %+v", row)
	}
	if row.Version != 2 {
		t.Fatalf("version want 2, got %d", row.Version)
	}
	if row.RejectReason != "旧原因" {
		t.Fatalf("reject_reason want preserved, got %q", row.RejectReason)
	}
}

// TestResubmitCreatesNewBatch 无待并入批：新建 RESUBMIT 批 question_count=1、
// 批次号 #R+MMdd、题目挂新批（BR3 否则新建分支）。
func TestResubmitCreatesNewBatch(t *testing.T) {
	db := newQBatchTestDB(t)
	oldGen := seedBatch(t, db, "#G0920", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	q := seedBatchQuestion(t, db, oldGen.ID, "Q-AG-0009", domain.QuestionSourceAI, domain.QuestionStatusRejected, "旧原因")

	repo := repository.NewQuestionBatchRepository(db)
	got, err := repo.ResubmitToBatch(context.Background(), &q, map[string]any{"scenario": "修正后的情境"})
	if err != nil {
		t.Fatalf("ResubmitToBatch: %v", err)
	}
	wantNo := "#R" + time.Now().In(time.Local).Format("0102")
	if got.BatchNo != wantNo {
		t.Fatalf("batch_no want %s, got %s", wantNo, got.BatchNo)
	}
	if got.BatchType != domain.QuestionBatchTypeResubmit || got.Source != domain.QuestionSourceAI ||
		got.Status != domain.QuestionBatchStatusPending || got.QuestionCount != 1 {
		t.Fatalf("new batch fields mismatch: %+v", got)
	}
	var row domain.Question
	if err := db.Where("id = ?", q.ID).First(&row).Error; err != nil {
		t.Fatalf("load question: %v", err)
	}
	if row.BatchID != got.ID || row.Status != domain.QuestionStatusPending {
		t.Fatalf("question want batch %d PENDING, got batch %d %s", got.ID, row.BatchID, row.Status)
	}
}

// TestResubmitVersionConflict 乐观锁失败：version 不符返回 ErrVersionConflict，
// 题目未变且未产生新批。
func TestResubmitVersionConflict(t *testing.T) {
	db := newQBatchTestDB(t)
	oldGen := seedBatch(t, db, "#G0920", domain.QuestionSourceAI, domain.QuestionBatchTypeGenerate, domain.QuestionBatchStatusClosed, 1)
	q := seedBatchQuestion(t, db, oldGen.ID, "Q-AG-0009", domain.QuestionSourceAI, domain.QuestionStatusRejected, "旧原因")
	stale := q
	stale.Version = 99

	repo := repository.NewQuestionBatchRepository(db)
	if _, err := repo.ResubmitToBatch(context.Background(), &stale, map[string]any{"scenario": "不应写入"}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stale version want ErrVersionConflict, got %v", err)
	}
	var row domain.Question
	if err := db.Where("id = ?", q.ID).First(&row).Error; err != nil {
		t.Fatalf("load question: %v", err)
	}
	if row.Scenario != "情境-Q-AG-0009" || row.Status != domain.QuestionStatusRejected || row.Version != 1 || row.BatchID != oldGen.ID {
		t.Fatalf("question must be untouched on conflict, got %+v", row)
	}
	var batchN int64
	db.Model(&domain.QuestionBatch{}).Count(&batchN)
	if batchN != 1 {
		t.Fatalf("want no new batch on conflict, got %d rows", batchN)
	}
}
