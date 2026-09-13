package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
)

// TestAssessmentTablesMigrated 验证批次域三表迁移（04 §3.1/§3.2/§3.3）：
// 三表建出、列集合齐全（assessment_batches 19 列 / assessment_batch_persons 9 列 /
// assessment_alerts 9 列）、比例列可空、唯一索引与观测索引落地。
// 批次状态四态（BR1）与进度/覆盖会话/失败计数承载列（BR2）由列集合断言守护，
// 告警每批次至多一条（BR3）由 uk_alert_batch 唯一索引断言守护。
func TestAssessmentTablesMigrated(t *testing.T) {
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 三表存在。
	for _, table := range []string{"assessment_batches", "assessment_batch_persons", "assessment_alerts"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected %s table exists", table)
		}
	}

	// assessment_batches 全列（04 §3.1，BR1 四态 status、BR2 进度/覆盖会话/失败计数/终态）。
	batchCols := []string{
		"id", "batch_no", "trigger_type", "target_mode", "target_names_json",
		"total_count", "evaluated_count", "covered_session_count", "failed_count",
		"total_session_count", "session_fail_ratio", "status", "error_summary",
		"period_start_at", "period_end_at", "triggered_at", "finished_at",
		"created_at", "updated_at",
	}
	if len(batchCols) != 19 {
		t.Fatalf("batchCols spec drift: got %d, want 19", len(batchCols))
	}
	for _, col := range batchCols {
		if !db.Migrator().HasColumn(&domain.AssessmentBatch{}, col) {
			t.Fatalf("expected assessment_batches.%s column exists", col)
		}
	}

	// assessment_batch_persons 全列（04 §3.2）。
	personCols := []string{
		"id", "batch_id", "token_name", "status", "session_count",
		"error_summary", "finished_at", "created_at", "updated_at",
	}
	if len(personCols) != 9 {
		t.Fatalf("personCols spec drift: got %d, want 9", len(personCols))
	}
	for _, col := range personCols {
		if !db.Migrator().HasColumn(&domain.AssessmentBatchPerson{}, col) {
			t.Fatalf("expected assessment_batch_persons.%s column exists", col)
		}
	}

	// assessment_alerts 全列（04 §3.3）。
	alertCols := []string{
		"id", "batch_id", "batch_no", "failed_count", "total_count",
		"failed_ratio", "signaled_at", "created_at", "updated_at",
	}
	if len(alertCols) != 9 {
		t.Fatalf("alertCols spec drift: got %d, want 9", len(alertCols))
	}
	for _, col := range alertCols {
		if !db.Migrator().HasColumn(&domain.AssessmentAlert{}, col) {
			t.Fatalf("expected assessment_alerts.%s column exists", col)
		}
	}

	// 比例列可空：session_fail_ratio 未终态为 NULL（04 §3.1 字段说明）。
	colTypes, err := db.Migrator().ColumnTypes(&domain.AssessmentBatch{})
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	for _, c := range colTypes {
		if c.Name() == "session_fail_ratio" {
			nullable, ok := c.Nullable()
			if !ok || !nullable {
				t.Fatalf("assessment_batches.session_fail_ratio must be nullable, got ok=%v nullable=%v", ok, nullable)
			}
		}
	}

	// 索引：批次表 uk_batch_no / idx_status_triggered / idx_triggered_at，
	// 人员表 uk_batch_person / idx_batch_status，告警表 uk_alert_batch（BR3 每批次至多一条）/ idx_signaled_at。
	for _, idx := range []struct {
		model any
		name  string
	}{
		{&domain.AssessmentBatch{}, "uk_batch_no"},
		{&domain.AssessmentBatch{}, "idx_status_triggered"},
		{&domain.AssessmentBatch{}, "idx_triggered_at"},
		{&domain.AssessmentBatchPerson{}, "uk_batch_person"},
		{&domain.AssessmentBatchPerson{}, "idx_batch_status"},
		{&domain.AssessmentAlert{}, "uk_alert_batch"},
		{&domain.AssessmentAlert{}, "idx_signaled_at"},
	} {
		if !db.Migrator().HasIndex(idx.model, idx.name) {
			t.Fatalf("expected %s index exists", idx.name)
		}
	}
}
