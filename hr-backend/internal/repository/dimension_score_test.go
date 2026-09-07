// Package repository_test 对 dimension_score 仓储做黑盒集成测试。
//
// 覆盖 SaveAll 唯一索引 upsert 全列覆盖（specs TECH_005 §2.4 能力1：重评产出行
// 按唯一索引 upsert，空串与 false 须写入）、ListByPersonPeriodExact 双界精确匹配、
// DeleteConversationFailed 先删后评（仅 conversation failed 行，active_test 行不动）。
// 每测试独立 :memory: SQLite 实例隔离。
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

// scoreBaseUnix 评分测试基准 Unix 秒。
const scoreBaseUnix = int64(1700000000)

// newScoreTestDB 构造独立 :memory: SQLite 并 AutoMigrate DimensionScore，
// 内联注册雪花 Create 回调补主键（同 dimension_test 范式）。
func newScoreTestDB(t *testing.T) *gorm.DB {
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
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_score_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch src := tx.Statement.Dest.(type) {
		case *domain.DimensionScore:
			if src.ID == 0 {
				src.ID = snowflake.NextID()
			}
		case *[]domain.DimensionScore:
			for i := range *src {
				if (*src)[i].ID == 0 {
					(*src)[i].ID = snowflake.NextID()
				}
			}
		}
	})
	if err := db.AutoMigrate(&domain.DimensionScore{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeScoreRow 构造字段完整的评分行（success 形态）。周期取 start/end 双参，
// 与仓储入参口径一致。
func makeScoreRow(token, code string, start, end int64, score int) domain.DimensionScore {
	return domain.DimensionScore{
		TokenName:     token,
		PeriodStartAt: time.Unix(start, 0).UTC(),
		PeriodEndAt:   time.Unix(end, 0).UTC(),
		DimensionCode: code,
		Module:        domain.ModuleAIUsage,
		Score:         score,
		Rationale:     "理由",
		Insufficient:  false,
		EvidenceJSON:  `{"sessions":["sk-1"]}`,
		Source:        domain.ScoreSourceConversation,
		ModelName:     "test-model",
		PromptVersion: "v1",
		Status:        domain.ScoreStatusSuccess,
		ErrorCode:     "",
	}
}

// countScoreRows 数表内总行数。
func countScoreRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&domain.DimensionScore{}).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// loadScoreRow 直接按唯一键三列取库内行（绕过被测仓储，验证落库真值）。
func loadScoreRow(t *testing.T, db *gorm.DB, token, code string, start int64) domain.DimensionScore {
	t.Helper()
	var row domain.DimensionScore
	if err := db.Where("token_name = ? AND period_start_at = ? AND dimension_code = ?",
		token, time.Unix(start, 0).UTC(), code).First(&row).Error; err != nil {
		t.Fatalf("load row %s: %v", code, err)
	}
	return row
}

// TestDimensionScoreSaveAllUpsert 核心断言：同 (token, period, code) 二次 SaveAll 后
// 表内行数不变、新值覆盖旧值（Score 80→90）。覆盖 specs §2.4 能力1 重评 upsert 语义。
func TestDimensionScoreSaveAllUpsert(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeScoreRow("张三", "AI_INSTRUCTION", start, end, 80)
	if err := repo.SaveAll(ctx, "张三", start, end, []domain.DimensionScore{first}); err != nil {
		t.Fatalf("首次 SaveAll: %v", err)
	}
	firstID := loadScoreRow(t, db, "张三", "AI_INSTRUCTION", start).ID

	// 同键重评：Score 80→90，理由与证据同步变化。
	second := makeScoreRow("张三", "AI_INSTRUCTION", start, end, 90)
	second.Rationale = "重评理由"
	second.EvidenceJSON = `{"sessions":["sk-1","sk-2"]}`
	if err := repo.SaveAll(ctx, "张三", start, end, []domain.DimensionScore{second}); err != nil {
		t.Fatalf("二次 SaveAll: %v", err)
	}

	if n := countScoreRows(t, db); n != 1 {
		t.Fatalf("upsert 后表行数 want 1, got %d", n)
	}
	got := loadScoreRow(t, db, "张三", "AI_INSTRUCTION", start)
	if got.Score != 90 {
		t.Fatalf("Score want 覆盖为 90, got %d", got.Score)
	}
	if got.Rationale != "重评理由" || got.EvidenceJSON != `{"sessions":["sk-1","sk-2"]}` {
		t.Fatalf("全列覆盖未生效: rationale=%q evidence=%q", got.Rationale, got.EvidenceJSON)
	}
	if got.ID != firstID {
		t.Fatalf("覆盖应保留原行 ID: want %d, got %d", firstID, got.ID)
	}
}

// TestDimensionScoreSaveAllZeroValueOverwrite 零值列覆盖：二次写入把非零旧值覆盖为
// 空串与 false（map 形态防 struct Updates 跳过零值），Status 翻转 success→failed 同步。
func TestDimensionScoreSaveAllZeroValueOverwrite(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	first := makeScoreRow("李四", "AI_TOOL", start, end, 80)
	first.ModelName = "old-model"
	if err := repo.SaveAll(ctx, "李四", start, end, []domain.DimensionScore{first}); err != nil {
		t.Fatalf("首次 SaveAll: %v", err)
	}

	// LLM 段失败形态：Score 0、Insufficient false、ModelName 空串、failed + error_code。
	second := makeScoreRow("李四", "AI_TOOL", start, end, 0)
	second.Rationale = ""
	second.ModelName = ""
	second.Status = domain.ScoreStatusFailed
	second.ErrorCode = "ErrLLMEvalUpstream"
	if err := repo.SaveAll(ctx, "李四", start, end, []domain.DimensionScore{second}); err != nil {
		t.Fatalf("二次 SaveAll: %v", err)
	}

	got := loadScoreRow(t, db, "李四", "AI_TOOL", start)
	if got.ModelName != "" {
		t.Fatalf("ModelName want 覆盖为空串, got %q", got.ModelName)
	}
	if got.Rationale != "" || got.Score != 0 {
		t.Fatalf("零值列未覆盖: rationale=%q score=%d", got.Rationale, got.Score)
	}
	if got.Status != domain.ScoreStatusFailed || got.ErrorCode != "ErrLLMEvalUpstream" {
		t.Fatalf("status/error_code 未覆盖: status=%q error_code=%q", got.Status, got.ErrorCode)
	}
}

// TestDimensionScoreSaveAllTokenPeriodFromArgs 行 TokenName 与周期以入参为权威：
// 行内残留值不落库（喂 LLM 前剥离、落库回填契约，specs 能力1）。
func TestDimensionScoreSaveAllTokenPeriodFromArgs(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	row := makeScoreRow("残留名", "AI_DEBUG", start+999, end+999, 50)
	if err := repo.SaveAll(ctx, "王五", start, end, []domain.DimensionScore{row}); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	got := loadScoreRow(t, db, "王五", "AI_DEBUG", start)
	if got.TokenName != "王五" {
		t.Fatalf("token_name want 入参回填 王五, got %q", got.TokenName)
	}
	if got.PeriodStartAt != time.Unix(start, 0).UTC() || got.PeriodEndAt != time.Unix(end, 0).UTC() {
		t.Fatalf("周期列 want 入参回填: got start=%v end=%v", got.PeriodStartAt, got.PeriodEndAt)
	}
}

// TestDimensionScoreSaveAllEmpty 空切片入参：不报错不落行。
func TestDimensionScoreSaveAllEmpty(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	if err := repo.SaveAll(context.Background(), "张三", scoreBaseUnix, scoreBaseUnix+3600, nil); err != nil {
		t.Fatalf("空切片 SaveAll want nil error, got %v", err)
	}
	if n := countScoreRows(t, db); n != 0 {
		t.Fatalf("空切片 want 0 行, got %d", n)
	}
}

// TestDimensionScoreListByPersonPeriodExact 核心断言：预置同 start 不同 end 的行，
// 只返回双界精确匹配行（定向分析窄窗口行因 period_end 不同天然隔离，specs 能力5 规则1）。
func TestDimensionScoreListByPersonPeriodExact(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	seeds := []domain.DimensionScore{
		makeScoreRow("张三", "AI_A", start, end, 80),             // 双界精确命中
		makeScoreRow("张三", "AI_B", start, end, 70),             // 双界精确命中
		makeScoreRow("张三", "AI_C", start, end+3600, 60),        // 同 start 不同 end：隔离
		makeScoreRow("张三", "AI_D", start+86400, end+86400, 50), // 不同 start：隔离
		makeScoreRow("李四", "AI_A", start, end, 40),             // 他人：隔离
	}
	for i := range seeds {
		if err := db.Create(&seeds[i]).Error; err != nil {
			t.Fatalf("seed %s: %v", seeds[i].DimensionCode, err)
		}
	}

	list, err := repo.ListByPersonPeriodExact(ctx, "张三", start, end)
	if err != nil {
		t.Fatalf("ListByPersonPeriodExact: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2, got %d", len(list))
	}
	if list[0].DimensionCode != "AI_A" || list[1].DimensionCode != "AI_B" {
		t.Fatalf("want dimension_code ASC [AI_A AI_B], got [%s %s]", list[0].DimensionCode, list[1].DimensionCode)
	}
}

// TestDimensionScoreListByPersonPeriodExactEmpty 无匹配行返回空切片非 error。
func TestDimensionScoreListByPersonPeriodExactEmpty(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	list, err := repo.ListByPersonPeriodExact(context.Background(), "无人", scoreBaseUnix, scoreBaseUnix+3600)
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("无匹配 want 空列表, got %d 行", len(list))
	}
}

// TestDimensionScoreDeleteConversationFailed 核心断言：预置 conversation failed +
// active_test failed 各一行，删除后仅 conversation 行消失（active_test 行不动，
// specs 能力6 规则2）。
func TestDimensionScoreDeleteConversationFailed(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	ctx := context.Background()
	start, end := scoreBaseUnix, scoreBaseUnix+7*86400

	convFailed := makeScoreRow("张三", "AI_CONV", start, end, 0)
	convFailed.Status = domain.ScoreStatusFailed
	convFailed.ErrorCode = "ErrLLMEvalUpstream"

	testFailed := makeScoreRow("张三", "ENNEA_CORE", start, end, 0)
	testFailed.Source = domain.ScoreSourceActiveTest
	testFailed.Module = domain.ModuleEnneagram
	testFailed.Status = domain.ScoreStatusFailed
	testFailed.ErrorCode = "ErrLLMEvalUpstream"

	convSuccess := makeScoreRow("张三", "AI_OK", start, end, 85) // conversation success：不删
	otherPeriod := makeScoreRow("张三", "AI_OLD", start, end+3600, 0)
	otherPeriod.Status = domain.ScoreStatusFailed // 同人不同 end 的 conversation failed：不删

	for _, row := range []domain.DimensionScore{convFailed, testFailed, convSuccess, otherPeriod} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", row.DimensionCode, err)
		}
	}

	n, err := repo.DeleteConversationFailed(ctx, "张三", start, end)
	if err != nil {
		t.Fatalf("DeleteConversationFailed: %v", err)
	}
	if n != 1 {
		t.Fatalf("删除行数 want 1, got %d", n)
	}
	if n := countScoreRows(t, db); n != 3 {
		t.Fatalf("删后表行数 want 3, got %d", n)
	}

	var remaining []domain.DimensionScore
	if err := db.Order("dimension_code ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("取剩余行: %v", err)
	}
	for _, row := range remaining {
		if row.DimensionCode == "AI_CONV" {
			t.Fatal("conversation failed 行应被删除")
		}
	}
}

// TestDimensionScoreDeleteConversationFailedNoMatch 无匹配行返回 0 非 error。
func TestDimensionScoreDeleteConversationFailedNoMatch(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	n, err := repo.DeleteConversationFailed(context.Background(), "张三", scoreBaseUnix, scoreBaseUnix+3600)
	if err != nil {
		t.Fatalf("无匹配 want nil error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("无匹配 want 0, got %d", n)
	}
}

// TestDimensionScoreQueryErrorClosedConnection 连接关闭后三方法错误透传（BR5：
// 既有评分行读取失败交任务重试，仓储 error 透传承载）。
func TestDimensionScoreQueryErrorClosedConnection(t *testing.T) {
	db := newScoreTestDB(t)
	repo := repository.NewDimensionScoreRepository(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}

	if _, err := repo.ListByPersonPeriodExact(context.Background(), "张三", 0, 1); err == nil {
		t.Fatal("连接关闭后 ListByPersonPeriodExact want error")
	}
	if _, err := repo.DeleteConversationFailed(context.Background(), "张三", 0, 1); err == nil {
		t.Fatal("连接关闭后 DeleteConversationFailed want error")
	}
	if err := repo.SaveAll(context.Background(), "张三", 0, 1, []domain.DimensionScore{makeScoreRow("张三", "AI_X", 0, 1, 1)}); err == nil {
		t.Fatal("连接关闭后 SaveAll want error")
	}
}
