// Package repository_test 对 question 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite，内联注册简化版雪花 Create 回调（与生产 model 包
// 全量反射版等效），AutoMigrate 建 Question 表。
// 覆盖 ListPage/FindByID/UpdateWithVersion/SoftDeleteWithVersion/ListByIDs/
// MaxQuestionSeq 的真实 SQL 行为。
package repository_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newQuestionTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate Question 表，
// 并内联注册仅处理 *domain.Question 的雪花 Create 回调。
func newQuestionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_question_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if q, ok := tx.Statement.Dest.(*domain.Question); ok && q.ID == 0 {
			q.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.Question{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedQuestion 写入一行题目，返回带 ID 的实体。version 置 1（业务约定）。
func seedQuestion(t *testing.T, db *gorm.DB, questionNo, source, status, scenario string, dimensionID int64) domain.Question {
	t.Helper()
	q := domain.Question{
		QuestionNo:  questionNo,
		Source:      source,
		DimensionID: dimensionID,
		Scenario:    scenario,
		Requirement: "要求-" + questionNo,
		FocusPoint:  "考察-" + questionNo,
		Status:      status,
		Version:     1,
	}
	if err := db.Create(&q).Error; err != nil {
		t.Fatalf("seed question %s: %v", questionNo, err)
	}
	return q
}

// seedAI 写入一行 source=AI 题目的便捷封装。
func seedAI(t *testing.T, db *gorm.DB, questionNo, status, scenario string, dimensionID int64) domain.Question {
	t.Helper()
	return seedQuestion(t, db, questionNo, domain.QuestionSourceAI, status, scenario, dimensionID)
}

// TestListPageExcludesPending 列表 tab 恒排除 PENDING：插入 ACTIVE/DISABLED/REJECTED/PENDING
// 各一行，查 source=AI 返回 3 行且不含 PENDING 行（BR2 §4.3.4 规则 2）。
func TestListPageExcludesPending(t *testing.T) {
	db := newQuestionTestDB(t)
	active := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	disabled := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusDisabled, "情境二", 101)
	rejected := seedAI(t, db, "Q-AG-0003", domain.QuestionStatusRejected, "情境三", 101)
	pending := seedAI(t, db, "Q-AG-0004", domain.QuestionStatusPending, "情境四", 101)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("want total=3 len=3, got total=%d len=%d", total, len(list))
	}
	got := map[int64]bool{}
	for _, q := range list {
		got[q.ID] = true
	}
	for _, want := range []domain.Question{active, disabled, rejected} {
		if !got[want.ID] {
			t.Fatalf("want row %s in list, missing", want.QuestionNo)
		}
	}
	if got[pending.ID] {
		t.Fatal("PENDING row must not appear in list tab")
	}
}

// TestListPage_SourceAndStatus 组合筛选：source=SCALE 只返回量表行，叠加 status=ACTIVE
// 进一步收窄。
func TestListPage_SourceAndStatus(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	scaleActive := seedQuestion(t, db, "Q-Scale-0001", domain.QuestionSourceScale, domain.QuestionStatusActive, "陈述一", 201)
	seedQuestion(t, db, "Q-Scale-0002", domain.QuestionSourceScale, domain.QuestionStatusDisabled, "陈述二", 202)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceScale, 0, false, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("scale tab want total=2, got total=%d len=%d", total, len(list))
	}

	list, total, err = repo.ListPage(context.Background(), domain.QuestionSourceScale, 0, false, domain.QuestionStatusActive, "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != scaleActive.ID {
		t.Fatalf("scale+active want only %s, got total=%d", scaleActive.QuestionNo, total)
	}
}

// TestListPage_DimensionFilter 维度筛选：dimensionIDSet=true 且 dimensionID 匹配时只返回
// 该维度行；dimensionIDSet=false 时忽略维度条件（全部维度）。
func TestListPage_DimensionFilter(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	d2 := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "情境二", 202)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 202, true, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != d2.ID {
		t.Fatalf("dimension filter want only %s, got total=%d list=%+v", d2.QuestionNo, total, list)
	}

	// 未传维度（dimensionIDSet=false）：dimensionID 的 0 值不进 WHERE。
	list, total, err = repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage no dimension: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("no dimension filter want total=2, got total=%d len=%d", total, len(list))
	}
}

// TestListPage_StatusFilter 状态筛选：status 非空只返回该状态行。
func TestListPage_StatusFilter(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	rej := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusRejected, "情境二", 101)
	seedAI(t, db, "Q-AG-0003", domain.QuestionStatusDisabled, "情境三", 101)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, domain.QuestionStatusRejected, "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != rej.ID {
		t.Fatalf("status filter want only %s, got total=%d", rej.QuestionNo, total)
	}
}

// TestListPage_KeywordEscapes 关键词转义：scenario 含 % 字面量的行，keyword="100%"
// 只命中字面量行，不被当通配符（BR1 §4.1.2 A）。
func TestListPageKeywordEscapes(t *testing.T) {
	db := newQuestionTestDB(t)
	literal := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "完成度 100% 的项目复盘", 101)
	seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "完成度的项目复盘", 101)
	seedAI(t, db, "Q-AG-0003", domain.QuestionStatusActive, "完成度 100x 的项目复盘", 101)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "100%", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != literal.ID {
		t.Fatalf("keyword 100%% want only literal row %s, got total=%d", literal.QuestionNo, total)
	}
}

// TestListPage_KeywordMatchesQuestionNo 关键词命中 question_no 列（BR1：编号与情境 OR 匹配）。
func TestListPage_KeywordMatchesQuestionNo(t *testing.T) {
	db := newQuestionTestDB(t)
	target := seedAI(t, db, "Q-AG-0007", domain.QuestionStatusActive, "无关情境", 101)
	seedAI(t, db, "Q-AG-0008", domain.QuestionStatusActive, "无关情境二", 101)

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "0007", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != target.ID {
		t.Fatalf("keyword question_no want only %s, got total=%d", target.QuestionNo, total)
	}
}

// TestListPage_OrderBy updated_at DESC, id DESC 排序。
func TestListPage_OrderBy(t *testing.T) {
	db := newQuestionTestDB(t)
	a := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	b := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "情境二", 101)

	// 把第一行 updated_at 改晚，验证按 updated_at DESC 而非插入序。
	db.Model(&domain.Question{}).Where("id = ?", a.ID).
		Update("updated_at", b.UpdatedAt.Add(3600e9))

	repo := repository.NewQuestionRepository(db)
	list, _, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("want order [a b] by updated_at DESC, got [%d %d]", list[0].ID, list[1].ID)
	}
}

// TestListPage_Pagination 分页与钳制：page=1 pageSize=2 只回前 2 行；page=0、pageSize=0/200
// 钳到 1/20/100。
func TestListPage_Pagination(t *testing.T) {
	db := newQuestionTestDB(t)
	for i := 1; i <= 5; i++ {
		seedAI(t, db, fmt.Sprintf("Q-AG-000%d", i), domain.QuestionStatusActive, "情境", 101)
	}

	repo := repository.NewQuestionRepository(db)
	// page=0 钳到 1：仍返回第一页数据。
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 0, 2)
	if err != nil {
		t.Fatalf("ListPage page=0: %v", err)
	}
	if total != 5 || len(list) != 2 {
		t.Fatalf("page=0 clamped to 1 want total=5 len=2, got total=%d len=%d", total, len(list))
	}
	// pageSize=0 钳到 20：全量返回。
	list, total, err = repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 0)
	if err != nil {
		t.Fatalf("ListPage pageSize=0: %v", err)
	}
	if total != 5 || len(list) != 5 {
		t.Fatalf("pageSize=0 clamped to 20 want total=5 len=5, got total=%d len=%d", total, len(list))
	}
	// pageSize=200 钳到 100 上限：不报错，全量返回。
	list, _, err = repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 200)
	if err != nil {
		t.Fatalf("ListPage pageSize=200: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("pageSize=200 clamped want len=5, got %d", len(list))
	}
	// 第二页：只剩 1 行。
	list, _, err = repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 3, 2)
	if err != nil {
		t.Fatalf("ListPage page=3: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("page=3 want 1 row, got %d", len(list))
	}
}

// TestListPage_ExcludesSoftDeleted 软删行不进列表（GORM DeletedAt 自动过滤）。
func TestListPage_ExcludesSoftDeleted(t *testing.T) {
	db := newQuestionTestDB(t)
	keep := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	del := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "情境二", 101)
	if err := db.Delete(&del).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewQuestionRepository(db)
	list, total, err := repo.ListPage(context.Background(), domain.QuestionSourceAI, 0, false, "", "", 1, 20)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != keep.ID {
		t.Fatalf("soft-deleted row must not appear, got total=%d", total)
	}
}

// TestQuestionFindByID 主键查询返回行。
func TestQuestionFindByID(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	repo := repository.NewQuestionRepository(db)
	got, err := repo.FindByID(context.Background(), q.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != q.ID || got.QuestionNo != "Q-AG-0001" || got.Scenario != "情境一" {
		t.Fatalf("FindByID want %s, got %+v", q.QuestionNo, got)
	}
}

// TestQuestionFindByID_SoftDeleted 软删行返回 ErrRecordNotFound。
func TestQuestionFindByID_SoftDeleted(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	if err := db.Delete(&q).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewQuestionRepository(db)
	if _, err := repo.FindByID(context.Background(), q.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("after delete want ErrRecordNotFound, got %v", err)
	}
}

// TestQuestionFindByID_NotFound 不存在的主键返回 ErrRecordNotFound。
func TestQuestionFindByID_NotFound(t *testing.T) {
	db := newQuestionTestDB(t)
	repo := repository.NewQuestionRepository(db)
	if _, err := repo.FindByID(context.Background(), 12345); err != gorm.ErrRecordNotFound {
		t.Fatalf("missing id want ErrRecordNotFound, got %v", err)
	}
}

// TestUpdateWithVersion 版本匹配更新成功：RowsAffected=1，字段写入且 version 自增。
func TestUpdateWithVersion(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	repo := repository.NewQuestionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), q.ID, 1, map[string]any{
		"status":          domain.QuestionStatusDisabled,
		"scenario":        "改后情境",
		"reject_reason":   "",
		"dimension_id":    int64(202),
		"reference_count": 3,
	})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1, got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), q.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != domain.QuestionStatusDisabled || got.Scenario != "改后情境" ||
		got.DimensionID != 202 || got.ReferenceCount != 3 {
		t.Fatalf("fields not updated: %+v", got)
	}
	if got.Version != 2 {
		t.Fatalf("version want 2 after update, got %d", got.Version)
	}
}

// TestUpdateWithVersionConflict 错误 version：RowsAffected=0 且 DB 行未变。
func TestUpdateWithVersionConflict(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "原情境", 101)

	repo := repository.NewQuestionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), q.ID, 99, map[string]any{
		"scenario": "不应写入",
		"status":   domain.QuestionStatusDisabled,
	})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 on version conflict, got %d", rows)
	}
	got, err := repo.FindByID(context.Background(), q.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Scenario != "原情境" || got.Status != domain.QuestionStatusActive || got.Version != 1 {
		t.Fatalf("row must be untouched on conflict, got %+v", got)
	}
}

// TestQuestionUpdateWithVersion_DeletedRow 软删行更新返回 0。
func TestQuestionUpdateWithVersion_DeletedRow(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	if err := db.Delete(&q).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewQuestionRepository(db)
	rows, err := repo.UpdateWithVersion(context.Background(), q.ID, 1, map[string]any{"scenario": "不应写入"})
	if err != nil {
		t.Fatalf("UpdateWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 on deleted row, got %d", rows)
	}
}

// TestSoftDeleteWithVersion 软删成功：RowsAffected=1，行对 FindByID 不可见，底层 deleted_at 已置。
func TestSoftDeleteWithVersion(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	repo := repository.NewQuestionRepository(db)
	rows, err := repo.SoftDeleteWithVersion(context.Background(), q.ID, 1)
	if err != nil {
		t.Fatalf("SoftDeleteWithVersion: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows want 1, got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), q.ID); err != gorm.ErrRecordNotFound {
		t.Fatalf("after soft delete want ErrRecordNotFound, got %v", err)
	}
	var raw struct {
		DeletedAt gorm.DeletedAt
		Version   int
	}
	if err := db.Unscoped().Model(&domain.Question{}).Select("deleted_at, version").
		Where("id = ?", q.ID).Scan(&raw).Error; err != nil {
		t.Fatalf("scan raw row: %v", err)
	}
	if !raw.DeletedAt.Valid {
		t.Fatal("deleted_at want valid after soft delete")
	}
}

// TestSoftDeleteWithVersion_Conflict 版本冲突软删：RowsAffected=0，行仍可见。
func TestSoftDeleteWithVersion_Conflict(t *testing.T) {
	db := newQuestionTestDB(t)
	q := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	repo := repository.NewQuestionRepository(db)
	rows, err := repo.SoftDeleteWithVersion(context.Background(), q.ID, 99)
	if err != nil {
		t.Fatalf("SoftDeleteWithVersion: %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows want 0 on conflict, got %d", rows)
	}
	if _, err := repo.FindByID(context.Background(), q.ID); err != nil {
		t.Fatalf("row must still exist after conflict, got %v", err)
	}
}

// TestListByIDs 批量按 ID 查：返回全部命中行；软删行排除；空 ID 列表返回空集。
func TestListByIDs(t *testing.T) {
	db := newQuestionTestDB(t)
	a := seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	b := seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "情境二", 101)
	del := seedAI(t, db, "Q-AG-0003", domain.QuestionStatusActive, "情境三", 101)
	if err := db.Delete(&del).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewQuestionRepository(db)
	list, err := repo.ListByIDs(context.Background(), []int64{a.ID, b.ID, del.ID, 999999})
	if err != nil {
		t.Fatalf("ListByIDs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 rows (soft-deleted excluded), got %d", len(list))
	}
	got := map[int64]bool{}
	for _, q := range list {
		got[q.ID] = true
	}
	if !got[a.ID] || !got[b.ID] {
		t.Fatalf("want a and b, got %+v", list)
	}

	empty, err := repo.ListByIDs(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListByIDs nil: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("nil ids want empty, got %d rows", len(empty))
	}
}

// TestMaxQuestionSeqIncludesSoftDeleted 编号序号取含软删行的最大值：插入 Q-AG-0003 后软删，
// MaxQuestionSeq("Q-AG-") 返回 3（编号只增不复用）。
func TestMaxQuestionSeqIncludesSoftDeleted(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)
	deleted := seedAI(t, db, "Q-AG-0003", domain.QuestionStatusActive, "情境三", 101)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}

	repo := repository.NewQuestionRepository(db)
	seq, err := repo.MaxQuestionSeq(context.Background(), "Q-AG-")
	if err != nil {
		t.Fatalf("MaxQuestionSeq: %v", err)
	}
	if seq != 3 {
		t.Fatalf("seq want 3 (soft-deleted counted), got %d", seq)
	}
}

// TestMaxQuestionSeq_NoRows 无行返回 0；其他前缀互不干扰。
func TestMaxQuestionSeq_NoRows(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0005", domain.QuestionStatusActive, "情境一", 101)

	repo := repository.NewQuestionRepository(db)
	seq, err := repo.MaxQuestionSeq(context.Background(), "Q-Scale-")
	if err != nil {
		t.Fatalf("MaxQuestionSeq: %v", err)
	}
	if seq != 0 {
		t.Fatalf("no rows want 0, got %d", seq)
	}
}

// TestMaxQuestionSeq_PicksMax 多行取最大而非任意行。
func TestMaxQuestionSeq_PicksMax(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-0002", domain.QuestionStatusActive, "情境二", 101)
	seedAI(t, db, "Q-AG-0011", domain.QuestionStatusActive, "情境十一", 101)
	seedAI(t, db, "Q-AG-0007", domain.QuestionStatusActive, "情境七", 101)

	repo := repository.NewQuestionRepository(db)
	seq, err := repo.MaxQuestionSeq(context.Background(), "Q-AG-")
	if err != nil {
		t.Fatalf("MaxQuestionSeq: %v", err)
	}
	if seq != 11 {
		t.Fatalf("seq want 11 (max), got %d", seq)
	}
}

// TestMaxQuestionSeq_FiveDigitOverflow 超四位扩展序号：Q-AG-9999 与 Q-AG-10000
// 共存时长度优先+字典序取 10000 而非 9999（不依赖 CAST，MySQL 兼容）。
func TestMaxQuestionSeq_FiveDigitOverflow(t *testing.T) {
	db := newQuestionTestDB(t)
	seedAI(t, db, "Q-AG-9999", domain.QuestionStatusActive, "情境九九九九", 101)
	seedAI(t, db, "Q-AG-10000", domain.QuestionStatusActive, "情境一万", 101)

	repo := repository.NewQuestionRepository(db)
	seq, err := repo.MaxQuestionSeq(context.Background(), "Q-AG-")
	if err != nil {
		t.Fatalf("MaxQuestionSeq: %v", err)
	}
	if seq != 10000 {
		t.Fatalf("seq want 10000 (five-digit max), got %d", seq)
	}
}
