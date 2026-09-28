// Package repository_test 对主动测试域两表做 schema 断言测试（04 §3.1/§3.2）。
// 独立 :memory: SQLite，AutoMigrate 建两表后经 Migrator 断言列集合与索引。
package repository_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
)

// newAssessmentTestSchemaDB 构造 :memory: SQLite，注册简化版雪花 Create 回调
// （与生产 model 包全量反射版等效，先例 question_test.go），AutoMigrate 两模型。
func newAssessmentTestSchemaDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_test_test", func(tx *gorm.DB) {
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
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentTestTask{}, &domain.AssessmentTestLink{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// assertColumns 断言模型表列集合与期望完全一致（无多余无缺失）。
func assertColumns(t *testing.T, db *gorm.DB, model any, table string, want []string) {
	t.Helper()
	cols, err := db.Migrator().ColumnTypes(model)
	if err != nil {
		t.Fatalf("read %s column types: %v", table, err)
	}
	got := make([]string, 0, len(cols))
	for _, c := range cols {
		got = append(got, c.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("%s columns = %v, want %v", table, got, want)
	}
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	for _, g := range got {
		if !set[g] {
			t.Fatalf("%s columns = %v, want %v", table, got, want)
		}
	}
}

// TestSchemaAssessmentTestTaskColumns 断言 assessment_test_tasks 列集合与 04 §3.1
// 字段说明表一致（BR1 五态、BR3 题目快照列承载）。
func TestSchemaAssessmentTestTaskColumns(t *testing.T) {
	db := newAssessmentTestSchemaDB(t)
	m := db.Migrator()
	if !m.HasTable(&domain.AssessmentTestTask{}) {
		t.Fatal("table assessment_test_tasks not created")
	}
	assertColumns(t, db, &domain.AssessmentTestTask{}, "assessment_test_tasks", []string{
		"id", "task_no", "test_type", "staff_id", "staff_name", "status",
		"grading_status", "question_ids_json", "scale_key", "dimension_codes_json",
		"completed_at", "created_at", "updated_at",
	})
}

// TestSchemaAssessmentTestTaskIndexes 断言唯一索引 uk_task_no 与复合/单列索引存在。
func TestSchemaAssessmentTestTaskIndexes(t *testing.T) {
	db := newAssessmentTestSchemaDB(t)
	m := db.Migrator()
	if !m.HasIndex(&domain.AssessmentTestTask{}, "uk_task_no") {
		t.Fatal("unique index uk_task_no not created")
	}
	if !m.HasIndex(&domain.AssessmentTestTask{}, "idx_type_status_created") {
		t.Fatal("index idx_type_status_created not created")
	}
	if !m.HasIndex(&domain.AssessmentTestTask{}, "idx_staff_name") {
		t.Fatal("index idx_staff_name not created")
	}
}

// TestSchemaAssessmentTestLinkColumns 断言 assessment_test_links 列集合与 04 §3.2
// 字段说明表一致（BR2 三态）。
func TestSchemaAssessmentTestLinkColumns(t *testing.T) {
	db := newAssessmentTestSchemaDB(t)
	m := db.Migrator()
	if !m.HasTable(&domain.AssessmentTestLink{}) {
		t.Fatal("table assessment_test_links not created")
	}
	assertColumns(t, db, &domain.AssessmentTestLink{}, "assessment_test_links", []string{
		"id", "task_id", "token_plain", "token_hash", "status",
		"generated_at", "expires_at", "used_at", "created_at", "updated_at",
	})
}

// TestSchemaAssessmentTestLinkIndexes 断言唯一索引 uk_token_hash 与复合索引存在。
func TestSchemaAssessmentTestLinkIndexes(t *testing.T) {
	db := newAssessmentTestSchemaDB(t)
	m := db.Migrator()
	if !m.HasIndex(&domain.AssessmentTestLink{}, "uk_token_hash") {
		t.Fatal("unique index uk_token_hash not created")
	}
	if !m.HasIndex(&domain.AssessmentTestLink{}, "idx_task_generated") {
		t.Fatal("index idx_task_generated not created")
	}
	if !m.HasIndex(&domain.AssessmentTestLink{}, "idx_status_expires") {
		t.Fatal("index idx_status_expires not created")
	}
}

// TestSchemaAssessmentTestUniqueEnforced 落行为证据：task_no 与 token_hash 的
// 唯一约束真实生效（撞键报错，BR1/BR2 唯一性兜底），雪花 ID 零配置自动赋值。
func TestSchemaAssessmentTestUniqueEnforced(t *testing.T) {
	db := newAssessmentTestSchemaDB(t)
	task := domain.AssessmentTestTask{
		TaskNo: "T202609280001", TestType: domain.TestTypeAIMgmt,
		StaffID: "u1", StaffName: "张三", Status: domain.TestTaskStatusPending,
		GradingStatus:   domain.GradingStatusWaiting,
		QuestionIDsJSON: "[1,2]", ScaleKey: "", DimensionCodesJSON: "[]",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if task.ID == 0 {
		t.Fatal("snowflake id not assigned")
	}
	link := domain.AssessmentTestLink{
		TaskID: task.ID, TokenPlain: "tok-a", TokenHash: "hash-a",
		Status: domain.LinkStatusValid,
	}
	if err := db.Create(&link).Error; err != nil {
		t.Fatalf("seed link: %v", err)
	}

	dupNo := domain.AssessmentTestTask{TaskNo: "T202609280001"}
	if err := db.Create(&dupNo).Error; err == nil {
		t.Fatal("duplicate task_no should violate uk_task_no")
	}
	dupHash := domain.AssessmentTestLink{TaskID: task.ID, TokenHash: "hash-a"}
	if err := db.Create(&dupHash).Error; err == nil {
		t.Fatal("duplicate token_hash should violate uk_token_hash")
	}
}
