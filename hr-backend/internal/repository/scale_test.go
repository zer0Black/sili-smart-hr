// Package repository_test 对 scale 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（SQLite 忽略 FOR UPDATE 锁子句，事务语义与查询
// 行为仍真实），AutoMigrate dimensions/questions/question_batches 三表，复用
// question_batch_test 的反射版雪花 Create 回调。覆盖已引入判定计数、型别维度
// 行锁查询、ensure 幂等补建、引入事务的批次/题目/维度三表落库与释放重引入。
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
	"sili-smart-hr/backend/internal/questionbank/scaledata"
	"sili-smart-hr/backend/internal/repository"
)

// newScaleTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate 量表引入涉及的
// 三表，注册反射版雪花 Create 回调（struct 与 slice 均覆盖，支撑 CreateInBatches）。
func newScaleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_scale_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		assignQBatchTestSnowflakeID(tx.Statement.Dest)
	})
	if err := db.AutoMigrate(&domain.Dimension{}, &domain.Question{}, &domain.QuestionBatch{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedEnneDimension seed 一个型别维度既有行（复用判定靶子），字段取非默认值
// 供「复用不改字段」断言；enabled 由调用方控制（BR4 含停用复用）。
func seedEnneDimension(t *testing.T, db *gorm.DB, code, name string, enabled bool) domain.Dimension {
	t.Helper()
	d := domain.Dimension{
		Code:        code,
		Name:        name,
		ModuleCode:  domain.ModuleEnneagram,
		DataSource:  domain.SourceTest,
		Anchor:      "既有锚点-" + code,
		Weight:      7,
		Enabled:     enabled,
		Version:     1,
		Description: "既有描述-" + code,
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("seed dimension %s: %v", code, err)
	}
	return d
}

// softDeleteEnneDimension 模拟仓储软删：code 改占位码 + deleted_at 落值。
func softDeleteEnneDimension(t *testing.T, db *gorm.DB, d domain.Dimension) {
	t.Helper()
	if err := db.Model(&domain.Dimension{}).Where("id = ?", d.ID).Updates(map[string]any{
		"code":       domain.DeletedCode(d.Code, d.ID),
		"deleted_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("soft delete dimension %s: %v", d.Code, err)
	}
}

// seedScaleQuestion seed 一行量表题（已引入判定靶子），version 置 1。
func seedScaleQuestion(t *testing.T, db *gorm.DB, questionNo, status, scaleKey string) domain.Question {
	t.Helper()
	q := domain.Question{
		QuestionNo:  questionNo,
		Source:      domain.QuestionSourceScale,
		DimensionID: 201,
		ScaleKey:    scaleKey,
		Scenario:    "陈述-" + questionNo,
		Requirement: "要求-" + questionNo,
		FocusPoint:  "计分键-" + questionNo,
		Status:      status,
		Version:     1,
	}
	if err := db.Create(&q).Error; err != nil {
		t.Fatalf("seed question %s: %v", questionNo, err)
	}
	return q
}

// TestImportScaleCreatesBatchAndQuestions 空库引入 RISO_HUDSON：批次 #S 前缀/
// SCALE/IMPORT/PENDING/question_count=len(Items)；DB 题目 Q-Scale 连续编号、
// PENDING、dimension_id 命中 ensure 的 9 型别维度（BR5）。
func TestImportScaleCreatesBatchAndQuestions(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	tpl, ok := scaledata.FindByKey("RISO_HUDSON")
	if !ok {
		t.Fatal("RISO_HUDSON template missing")
	}
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	batch, err := repo.ImportScale(context.Background(), tpl, now)
	if err != nil {
		t.Fatalf("ImportScale: %v", err)
	}
	if batch.BatchNo != "#S0925" || !strings.HasPrefix(batch.BatchNo, "#S") {
		t.Fatalf("batch_no want #S0925, got %s", batch.BatchNo)
	}
	if batch.Source != domain.QuestionSourceScale || batch.BatchType != domain.QuestionBatchTypeImport {
		t.Fatalf("batch source/type mismatch: %+v", batch)
	}
	if batch.Status != domain.QuestionBatchStatusPending {
		t.Fatalf("batch status want PENDING, got %s", batch.Status)
	}
	if batch.QuestionCount != len(tpl.Items) {
		t.Fatalf("question_count want %d, got %d", len(tpl.Items), batch.QuestionCount)
	}
	if batch.ScaleKey != tpl.ScaleKey || batch.Title != tpl.Name {
		t.Fatalf("batch scale_key/title mismatch: %+v", batch)
	}

	var dims []domain.Dimension
	if err := db.Where("module_code = ?", domain.ModuleEnneagram).Find(&dims).Error; err != nil {
		t.Fatalf("load dimensions: %v", err)
	}
	if len(dims) != 9 {
		t.Fatalf("want 9 enneagram dimensions ensured, got %d", len(dims))
	}
	dimIDs := map[int64]bool{}
	for _, d := range dims {
		dimIDs[d.ID] = true
	}

	var rows []domain.Question
	if err := db.Where("batch_id = ?", batch.ID).Order("question_no").Find(&rows).Error; err != nil {
		t.Fatalf("load questions: %v", err)
	}
	if len(rows) != len(tpl.Items) {
		t.Fatalf("want %d question rows, got %d", len(tpl.Items), len(rows))
	}
	usedDims := map[int64]bool{}
	for i, row := range rows {
		wantNo := fmt.Sprintf("Q-Scale-%04d", i+1)
		if row.QuestionNo != wantNo {
			t.Fatalf("question %d no want %s, got %s", i, wantNo, row.QuestionNo)
		}
		if row.Status != domain.QuestionStatusPending {
			t.Fatalf("question %s want PENDING, got %s", row.QuestionNo, row.Status)
		}
		if row.Source != domain.QuestionSourceScale || row.ScaleKey != tpl.ScaleKey {
			t.Fatalf("question %s source/scale_key mismatch: %+v", row.QuestionNo, row)
		}
		if !dimIDs[row.DimensionID] {
			t.Fatalf("question %s dimension_id %d not in ensured set", row.QuestionNo, row.DimensionID)
		}
		if row.Version != 1 || row.ReferenceCount != 0 {
			t.Fatalf("question %s version/reference_count mismatch: %+v", row.QuestionNo, row)
		}
		usedDims[row.DimensionID] = true
	}
	if len(usedDims) != 9 {
		t.Fatalf("want all 9 dimensions referenced, got %d", len(usedDims))
	}
}

// TestImportScaleIdempotentReject 已引入后再引入返回 ErrScaleImported 且三表无新行（BR1）。
func TestImportScaleIdempotentReject(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	tpl, _ := scaledata.FindByKey("RISO_HUDSON")
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	if _, err := repo.ImportScale(context.Background(), tpl, now); err != nil {
		t.Fatalf("first ImportScale: %v", err)
	}
	if _, err := repo.ImportScale(context.Background(), tpl, now); !errors.Is(err, repository.ErrScaleImported) {
		t.Fatalf("second ImportScale want ErrScaleImported, got %v", err)
	}
	var batchN, questionN, dimN int64
	db.Model(&domain.QuestionBatch{}).Count(&batchN)
	db.Model(&domain.Question{}).Count(&questionN)
	db.Model(&domain.Dimension{}).Count(&dimN)
	if batchN != 1 || questionN != int64(len(tpl.Items)) || dimN != 9 {
		t.Fatalf("after reject want 1 batch / %d questions / 9 dims, got %d/%d/%d",
			len(tpl.Items), batchN, questionN, dimN)
	}
}

// TestImportScaleEnsuresDimensionsOnce 既有 3 个型别活跃行（1 个停用）时引入只
// 补建缺失 6 个，既有行复用且字段不变（BR4 前半）。
func TestImportScaleEnsuresDimensionsOnce(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	existing := []domain.Dimension{
		seedEnneDimension(t, db, "ENNE_TYPE_1_REFORMER", "完美型", true),
		seedEnneDimension(t, db, "ENNE_TYPE_2_HELPER", "助人型", false),
		seedEnneDimension(t, db, "ENNE_TYPE_3_ACHIEVER", "成就型", true),
	}
	tpl, _ := scaledata.FindByKey("RISO_HUDSON")
	if _, err := repo.ImportScale(context.Background(), tpl, time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("ImportScale: %v", err)
	}

	var dims []domain.Dimension
	if err := db.Where("module_code = ?", domain.ModuleEnneagram).Order("code").Find(&dims).Error; err != nil {
		t.Fatalf("load dimensions: %v", err)
	}
	if len(dims) != 9 {
		t.Fatalf("want 9 dimensions (3 reused + 6 created), got %d", len(dims))
	}
	byCode := map[string]domain.Dimension{}
	for _, d := range dims {
		byCode[d.Code] = d
	}
	existingCodes := map[string]bool{}
	for _, e := range existing {
		existingCodes[e.Code] = true
		got := byCode[e.Code]
		if got.ID != e.ID {
			t.Fatalf("dimension %s want reused id %d, got %d", e.Code, e.ID, got.ID)
		}
		if got.Weight != 7 || got.Anchor != "既有锚点-"+e.Code || got.Version != 1 || got.Enabled != e.Enabled {
			t.Fatalf("reused dimension %s fields changed: %+v", e.Code, got)
		}
	}
	for _, d := range tpl.Dimensions {
		if existingCodes[d.Code] {
			continue
		}
		got := byCode[d.Code]
		if got.DataSource != domain.SourceTest || got.Weight != 0 || !got.Enabled ||
			got.IncludeOverview || !got.IsReference || got.Version != 1 {
			t.Fatalf("created dimension %s template fields mismatch: %+v", d.Code, got)
		}
		if !strings.Contains(got.Anchor, d.Name) {
			t.Fatalf("created dimension %s anchor want type name, got %q", d.Code, got.Anchor)
		}
	}
}

// TestImportReleasesAfterVoid 引入→批次作废（题目随批软删）→CountImported 归 0
// →可再引入成功，题号续段不回收（BR2）。
func TestImportReleasesAfterVoid(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	batchRepo := repository.NewQuestionBatchRepository(db)
	tpl, _ := scaledata.FindByKey("RISO_HUDSON")
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	first, err := repo.ImportScale(context.Background(), tpl, now)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	counts, err := repo.CountImported(context.Background(), nil, []string{tpl.ScaleKey})
	if err != nil {
		t.Fatalf("CountImported: %v", err)
	}
	if counts[tpl.ScaleKey] != int64(len(tpl.Items)) {
		t.Fatalf("imported count want %d, got %d", len(tpl.Items), counts[tpl.ScaleKey])
	}

	if err := batchRepo.VoidBatch(context.Background(), first.ID); err != nil {
		t.Fatalf("VoidBatch: %v", err)
	}
	counts, err = repo.CountImported(context.Background(), nil, []string{tpl.ScaleKey})
	if err != nil {
		t.Fatalf("CountImported after void: %v", err)
	}
	if counts[tpl.ScaleKey] != 0 {
		t.Fatalf("after void imported count want 0, got %d", counts[tpl.ScaleKey])
	}

	second, err := repo.ImportScale(context.Background(), tpl, now)
	if err != nil {
		t.Fatalf("re-import after void: %v", err)
	}
	if second.ID == first.ID || second.BatchNo != "#S0925-2" {
		t.Fatalf("re-import want new batch #S0925-2, got %s", second.BatchNo)
	}
	var row domain.Question
	if err := db.Where("batch_id = ?", second.ID).Order("question_no").First(&row).Error; err != nil {
		t.Fatalf("load re-imported question: %v", err)
	}
	if row.QuestionNo != fmt.Sprintf("Q-Scale-%04d", len(tpl.Items)+1) {
		t.Fatalf("re-import first no want Q-Scale-%04d (soft-deleted kept numbering), got %s",
			len(tpl.Items)+1, row.QuestionNo)
	}
}

// TestImportScaleTwoScalesCoexist 两套量表各自独立成批同时在库（BR3）。
func TestImportScaleTwoScalesCoexist(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	riso, _ := scaledata.FindByKey("RISO_HUDSON")
	essence, _ := scaledata.FindByKey("ESSENCE")
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)

	b1, err := repo.ImportScale(context.Background(), riso, now)
	if err != nil {
		t.Fatalf("import RISO_HUDSON: %v", err)
	}
	b2, err := repo.ImportScale(context.Background(), essence, now)
	if err != nil {
		t.Fatalf("import ESSENCE (coexist allowed): %v", err)
	}
	if b1.ID == b2.ID || b2.BatchNo != "#S0925-2" {
		t.Fatalf("two scales want independent batches #S0925/#S0925-2, got %s/%s", b1.BatchNo, b2.BatchNo)
	}
	var risoN, essenceN int64
	db.Model(&domain.Question{}).Where("scale_key = ?", riso.ScaleKey).Count(&risoN)
	db.Model(&domain.Question{}).Where("scale_key = ?", essence.ScaleKey).Count(&essenceN)
	if risoN != int64(len(riso.Items)) || essenceN != int64(len(essence.Items)) {
		t.Fatalf("coexist counts want %d/%d, got %d/%d",
			len(riso.Items), len(essence.Items), risoN, essenceN)
	}
}

// TestImportScaleSoftDeletedDimensionRebuilt 型别维度软删行视为不存在，引入时
// 原 code 新建活跃行（BR4 后半）。
func TestImportScaleSoftDeletedDimensionRebuilt(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	soft := seedEnneDimension(t, db, "ENNE_TYPE_1_REFORMER", "完美型", true)
	softDeleteEnneDimension(t, db, soft)

	tpl, _ := scaledata.FindByKey("RISO_HUDSON")
	if _, err := repo.ImportScale(context.Background(), tpl, time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("ImportScale: %v", err)
	}
	var rebuilt domain.Dimension
	if err := db.Where("code = ? AND deleted_at IS NULL", "ENNE_TYPE_1_REFORMER").First(&rebuilt).Error; err != nil {
		t.Fatalf("soft-deleted code want rebuilt as live row: %v", err)
	}
	if rebuilt.ID == soft.ID {
		t.Fatal("rebuilt row want new id, got reused soft-deleted row")
	}
}

// TestCountImported 已引入判定计数口径：四态（PENDING/ACTIVE/DISABLED/REJECTED）
// 全算，软删行与空 scale_key 的 AI 题不计，两 key 一次查回，tx 通道等效（BR1）。
func TestCountImported(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	seedScaleQuestion(t, db, "Q-Scale-0001", domain.QuestionStatusPending, "RISO_HUDSON")
	seedScaleQuestion(t, db, "Q-Scale-0002", domain.QuestionStatusActive, "RISO_HUDSON")
	seedScaleQuestion(t, db, "Q-Scale-0003", domain.QuestionStatusDisabled, "RISO_HUDSON")
	seedScaleQuestion(t, db, "Q-Scale-0004", domain.QuestionStatusRejected, "RISO_HUDSON")
	deleted := seedScaleQuestion(t, db, "Q-Scale-0005", domain.QuestionStatusActive, "RISO_HUDSON")
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatalf("seed delete: %v", err)
	}
	seedScaleQuestion(t, db, "Q-Scale-0006", domain.QuestionStatusActive, "ESSENCE")
	seedScaleQuestion(t, db, "Q-Scale-0007", domain.QuestionStatusActive, "ESSENCE")
	seedAI(t, db, "Q-AG-0001", domain.QuestionStatusActive, "情境一", 101)

	counts, err := repo.CountImported(context.Background(), nil, []string{"RISO_HUDSON", "ESSENCE"})
	if err != nil {
		t.Fatalf("CountImported: %v", err)
	}
	if counts["RISO_HUDSON"] != 4 || counts["ESSENCE"] != 2 {
		t.Fatalf("want RISO=4 ESSENCE=2 (soft-deleted & AI excluded), got %v", counts)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		counts, err = repo.CountImported(context.Background(), tx, []string{"RISO_HUDSON"})
		return err
	})
	if err != nil {
		t.Fatalf("CountImported in tx: %v", err)
	}
	if counts["RISO_HUDSON"] != 4 {
		t.Fatalf("tx channel want RISO=4, got %v", counts)
	}
}

// TestCountImported_EmptyKeys 空 key 列表返回空 map 非 error。
func TestCountImported_EmptyKeys(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	counts, err := repo.CountImported(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("CountImported empty keys: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("want empty map, got %v", counts)
	}
}

// TestLockEnneagramDimensions 只命中活跃行：软删行不在锁集合（SQLite 忽略锁
// 子句，断言查询语义）。
func TestLockEnneagramDimensions(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	seedEnneDimension(t, db, "ENNE_TYPE_1_REFORMER", "完美型", true)
	soft := seedEnneDimension(t, db, "ENNE_TYPE_2_HELPER", "助人型", true)
	softDeleteEnneDimension(t, db, soft)

	tpl, _ := scaledata.FindByKey("RISO_HUDSON")
	codes := make([]string, len(tpl.Dimensions))
	for i, d := range tpl.Dimensions {
		codes[i] = d.Code
	}
	dims, err := repo.LockEnneagramDimensions(context.Background(), nil, codes)
	if err != nil {
		t.Fatalf("LockEnneagramDimensions: %v", err)
	}
	if len(dims) != 1 || dims[0].Code != "ENNE_TYPE_1_REFORMER" {
		t.Fatalf("want only live TYPE_1 row returned, got %+v", dims)
	}
}

// TestLockEnneagramDimensions_EmptyCodes 空 code 列表返回空切片非 error。
func TestLockEnneagramDimensions_EmptyCodes(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	dims, err := repo.LockEnneagramDimensions(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("LockEnneagramDimensions empty codes: %v", err)
	}
	if dims == nil || len(dims) != 0 {
		t.Fatalf("want non-nil empty slice, got %v", dims)
	}
}

// TestEnsureEnneagramDimensions 空库 ensure 建 9 行且字段按模板（04 §4.2），
// 返回 code→ID 映射命中行 ID；二次 ensure 幂等不新建。
func TestEnsureEnneagramDimensions(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	tpl, _ := scaledata.FindByKey("RISO_HUDSON")

	ids, err := repo.EnsureEnneagramDimensions(context.Background(), nil, tpl.Dimensions)
	if err != nil {
		t.Fatalf("EnsureEnneagramDimensions: %v", err)
	}
	if len(ids) != 9 {
		t.Fatalf("want 9 code→id mappings, got %d", len(ids))
	}
	var dims []domain.Dimension
	if err := db.Where("module_code = ?", domain.ModuleEnneagram).Find(&dims).Error; err != nil {
		t.Fatalf("load dimensions: %v", err)
	}
	if len(dims) != 9 {
		t.Fatalf("want 9 rows created, got %d", len(dims))
	}
	for _, d := range dims {
		if ids[d.Code] != d.ID {
			t.Fatalf("mapping %s want id %d, got %d", d.Code, d.ID, ids[d.Code])
		}
		if d.DataSource != domain.SourceTest || d.Weight != 0 || !d.Enabled ||
			d.IncludeOverview || !d.IsReference || d.Version != 1 {
			t.Fatalf("dimension %s template fields mismatch: %+v", d.Code, d)
		}
		if !strings.Contains(d.Anchor, d.Name) || len([]rune(d.Anchor)) > 500 {
			t.Fatalf("dimension %s anchor want type name ≤500 runes, got %q", d.Code, d.Anchor)
		}
	}

	ids2, err := repo.EnsureEnneagramDimensions(context.Background(), nil, tpl.Dimensions)
	if err != nil {
		t.Fatalf("EnsureEnneagramDimensions again: %v", err)
	}
	var n int64
	db.Model(&domain.Dimension{}).Count(&n)
	if n != 9 {
		t.Fatalf("second ensure want still 9 rows, got %d", n)
	}
	for code, id := range ids {
		if ids2[code] != id {
			t.Fatalf("second ensure mapping %s want stable id %d, got %d", code, id, ids2[code])
		}
	}
}

// TestEnsureEnneagramDimensions_EmptyDims 空维度列表返回空 map 非 error。
func TestEnsureEnneagramDimensions_EmptyDims(t *testing.T) {
	db := newScaleTestDB(t)
	repo := repository.NewScaleRepository(db)
	ids, err := repo.EnsureEnneagramDimensions(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("EnsureEnneagramDimensions empty dims: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("want empty map, got %v", ids)
	}
}
