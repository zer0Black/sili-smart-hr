// Package repository_test 对 assessment_alert 仓储做黑盒集成测试。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并注册
// Create 回调处理 AssessmentAlert。
//
// 核心断言（BR1）：UpsertByBatch 按 uk_alert_batch 幂等覆盖，同批次至多一行
// （specs §5.2.4 规则4）。
package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// alertBaseUnix 告警测试基准 Unix 秒。
const alertBaseUnix = int64(1757900000)

// newAlertTestDB 构造独立 :memory: SQLite，AutoMigrate assessment_alerts 表。
func newAlertTestDB(t *testing.T) *gorm.DB {
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
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_alert_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		if dest, ok := tx.Statement.Dest.(*domain.AssessmentAlert); ok && dest.ID == 0 {
			dest.ID = snowflake.NextID()
		}
	})
	if err := db.AutoMigrate(&domain.AssessmentAlert{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// newAlert 构造字段完整的告警信号。
func newAlert(batchID int64, batchNo string, failed, total int, ratio float64) domain.AssessmentAlert {
	return domain.AssessmentAlert{
		BatchID:     batchID,
		BatchNo:     batchNo,
		FailedCount: failed,
		TotalCount:  total,
		FailedRatio: ratio,
		SignaledAt:  time.Unix(alertBaseUnix, 0).UTC(),
	}
}

func loadAlerts(t *testing.T, db *gorm.DB, batchID int64) []domain.AssessmentAlert {
	t.Helper()
	var rows []domain.AssessmentAlert
	if err := db.Where("batch_id = ?", batchID).Find(&rows).Error; err != nil {
		t.Fatalf("load alerts: %v", err)
	}
	return rows
}

// TestAlertUpsertIdempotent 核心断言：同 batch_id 写两次，表内仅 1 行且
// FailedRatio 被覆盖为第二次的 40.00（specs §5.2.4 规则4，重复判定幂等覆盖）。
func TestAlertUpsertIdempotent(t *testing.T) {
	db := newAlertTestDB(t)
	repo := repository.NewAssessmentAlertRepository(db)
	ctx := context.Background()

	first := newAlert(1001, "B001", 5, 20, 25.00)
	if err := repo.UpsertByBatch(ctx, &first); err != nil {
		t.Fatalf("首次 upsert: %v", err)
	}
	second := newAlert(1001, "B001", 8, 20, 40.00)
	second.SignaledAt = time.Unix(alertBaseUnix+3600, 0).UTC()
	if err := repo.UpsertByBatch(ctx, &second); err != nil {
		t.Fatalf("二次 upsert: %v", err)
	}

	rows := loadAlerts(t, db, 1001)
	if len(rows) != 1 {
		t.Fatalf("同批次 want 仅 1 行, got %d", len(rows))
	}
	got := rows[0]
	if got.FailedRatio != 40.00 {
		t.Fatalf("FailedRatio want 40.00（幂等覆盖）, got %v", got.FailedRatio)
	}
	if got.FailedCount != 8 || got.TotalCount != 20 {
		t.Fatalf("计数字段 want (8,20), got (%d,%d)", got.FailedCount, got.TotalCount)
	}
	if !got.SignaledAt.Equal(second.SignaledAt) {
		t.Fatalf("SignaledAt want 被覆盖为第二次值, got %v", got.SignaledAt)
	}
	if got.ID != first.ID {
		t.Fatalf("覆盖更新应保留原主键: want %d, got %d", first.ID, got.ID)
	}
}

// TestAlertUpsertDistinctBatches 不同 batch_id 各自落行，互不影响。
func TestAlertUpsertDistinctBatches(t *testing.T) {
	db := newAlertTestDB(t)
	repo := repository.NewAssessmentAlertRepository(db)
	ctx := context.Background()

	a := newAlert(2001, "B201", 5, 20, 25.00)
	b := newAlert(2002, "B202", 3, 10, 30.00)
	if err := repo.UpsertByBatch(ctx, &a); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := repo.UpsertByBatch(ctx, &b); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	if rows := loadAlerts(t, db, 2001); len(rows) != 1 || rows[0].FailedRatio != 25.00 {
		t.Fatalf("batch 2001 want 1 行 ratio 25, got %+v", rows)
	}
	if rows := loadAlerts(t, db, 2002); len(rows) != 1 || rows[0].FailedRatio != 30.00 {
		t.Fatalf("batch 2002 want 1 行 ratio 30, got %+v", rows)
	}
}

// TestAlertUpsertSnowflakeID 首写经雪花回调赋值主键。
func TestAlertUpsertSnowflakeID(t *testing.T) {
	db := newAlertTestDB(t)
	repo := repository.NewAssessmentAlertRepository(db)
	ctx := context.Background()

	alert := newAlert(3001, "B301", 1, 4, 25.00)
	if err := repo.UpsertByBatch(ctx, &alert); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if alert.ID == 0 {
		t.Fatal("雪花 ID 未被回调赋值")
	}
}
