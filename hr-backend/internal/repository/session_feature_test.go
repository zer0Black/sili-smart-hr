// Package repository_test 对 session_feature 仓储做黑盒集成测试。
//
// 覆盖 Save 幂等状态机四分支（success 复用 / failed 翻转 / skipped 终态 / failed 重试覆盖）、
// uk_session_key 唯一冲突收敛、按人时间窗三态取数与闭区间边界、session_key 点查两态
// （specs TECH_003 §2.4 能力3）。每测试独立 :memory: SQLite 实例隔离。
package repository_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// baseUnix 测试基准 Unix 秒，避开零值时间在多库下的边界。
const baseUnix = int64(1700000000)

// newFeatureTestDB 构造独立 :memory: SQLite 并 AutoMigrate SessionFeature。
// translate=true 时驱动把 UNIQUE 冲突翻译为 gorm.ErrDuplicatedKey（与生产未开翻译的
// 原生错误形态各测一路）。
func newFeatureTestDB(t *testing.T, translate bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		TranslateError: translate,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.SessionFeature{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// makeFeature 构造字段完整的档案行。success 取四块 JSON、failed 取统计块、skipped 空串，
// 与业务层显式置值纪律一致（04 §3.1：字符串列无默认值）。
func makeFeature(key, token, status string, firstTurn time.Time) *domain.SessionFeature {
	profileJSON := map[string]string{
		domain.FeatureStatusSuccess: `{"stats":{"turn_count":5},"summary":"s","instruction":"i","behavior":"b"}`,
		domain.FeatureStatusFailed:  `{"stats":{"turn_count":5}}`,
		domain.FeatureStatusSkipped: "",
	}[status]
	errorCode := map[string]string{
		domain.FeatureStatusSuccess: "",
		domain.FeatureStatusFailed:  "ErrLLMUpstream",
		domain.FeatureStatusSkipped: "empty_shell",
	}[status]
	return &domain.SessionFeature{
		SessionKey:  key,
		TokenName:   token,
		Status:      status,
		TurnCount:   5,
		FirstTurnAt: firstTurn,
		LastTurnAt:  firstTurn.Add(time.Hour),
		ProfileJSON: profileJSON,
		ErrorCode:   errorCode,
	}
}

func countRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&domain.SessionFeature{}).Count(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

// TestSaveIdempotentStateMachine 覆盖幂等状态机四分支（specs §2.4 能力3）：
// success 复用不覆盖 / failed 原地翻转 success 补齐三块 / skipped 终态 / failed 重试 UPDATE 不新增行。
func TestSaveIdempotentStateMachine(t *testing.T) {
	t.Run("success 再写返回 reused 且行不变", func(t *testing.T) {
		db := newFeatureTestDB(t, true)
		repo := repository.NewSessionFeatureRepository(db)
		first := makeFeature("sk-1", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
		if reused, err := repo.Save(context.Background(), first); err != nil || reused {
			t.Fatalf("首写 success want (false, nil), got (%v, %v)", reused, err)
		}

		second := makeFeature("sk-1", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
		second.ProfileJSON = `{"stats":{"turn_count":9}}` // 内容不同，验证不覆盖
		second.TurnCount = 9
		reused, err := repo.Save(context.Background(), second)
		if err != nil {
			t.Fatalf("重复 Save: unexpected error: %v", err)
		}
		if !reused {
			t.Fatalf("已有 success 行再 Save want reused=true, got false")
		}

		got, err := repo.FindBySessionKey(context.Background(), "sk-1")
		if err != nil || got == nil {
			t.Fatalf("重查既有行: row=%v err=%v", got, err)
		}
		if got.ProfileJSON != first.ProfileJSON || got.TurnCount != 5 {
			t.Fatalf("reused 命中后 DB 行被覆盖: profile=%q turn_count=%d", got.ProfileJSON, got.TurnCount)
		}
		if n := countRows(t, db); n != 1 {
			t.Fatalf("表行数 want 1, got %d", n)
		}
	})

	t.Run("failed 行翻转为 success 补齐三块", func(t *testing.T) {
		db := newFeatureTestDB(t, true)
		repo := repository.NewSessionFeatureRepository(db)
		failedRec := makeFeature("sk-2", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))
		if reused, err := repo.Save(context.Background(), failedRec); err != nil || reused {
			t.Fatalf("首写 failed want (false, nil), got (%v, %v)", reused, err)
		}
		origID := failedRec.ID

		// 上游令牌名纠正后重抽：翻转须同步覆盖 token_name，否则按新名取数漏行。
		successRec := makeFeature("sk-2", "张三-corrected", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
		if reused, err := repo.Save(context.Background(), successRec); err != nil || reused {
			t.Fatalf("failed 翻转 success want (false, nil), got (%v, %v)", reused, err)
		}

		got, err := repo.FindBySessionKey(context.Background(), "sk-2")
		if err != nil || got == nil {
			t.Fatalf("重查翻转后行: row=%v err=%v", got, err)
		}
		if got.Status != domain.FeatureStatusSuccess {
			t.Fatalf("翻转后 status want success, got %q", got.Status)
		}
		if got.TokenName != "张三-corrected" {
			t.Fatalf("翻转后 token_name want 新名覆盖, got %q", got.TokenName)
		}
		if got.ProfileJSON == "" {
			t.Fatalf("翻转后 profile_json 应补齐 LLM 三块（非空），got 空串")
		}
		if got.ErrorCode != "" {
			t.Fatalf("翻转后 error_code want 空串, got %q", got.ErrorCode)
		}
		if got.ID != origID {
			t.Fatalf("原地翻转应保留原 ID: want %d, got %d", origID, got.ID)
		}
		if n := countRows(t, db); n != 1 {
			t.Fatalf("表行数 want 1, got %d", n)
		}
	})

	t.Run("skipped 终态再写返回 reused 且不变", func(t *testing.T) {
		db := newFeatureTestDB(t, true)
		repo := repository.NewSessionFeatureRepository(db)
		skipped := makeFeature("sk-3", "张三", domain.FeatureStatusSkipped, time.Unix(baseUnix, 0))
		if reused, err := repo.Save(context.Background(), skipped); err != nil || reused {
			t.Fatalf("首写 skipped want (false, nil), got (%v, %v)", reused, err)
		}

		rewrite := makeFeature("sk-3", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
		reused, err := repo.Save(context.Background(), rewrite)
		if err != nil {
			t.Fatalf("skipped 行再 Save: unexpected error: %v", err)
		}
		if !reused {
			t.Fatalf("skipped 终态再 Save want reused=true, got false")
		}

		got, err := repo.FindBySessionKey(context.Background(), "sk-3")
		if err != nil || got == nil {
			t.Fatalf("重查 skipped 行: row=%v err=%v", got, err)
		}
		if got.Status != domain.FeatureStatusSkipped || got.ProfileJSON != "" || got.ErrorCode != "empty_shell" {
			t.Fatalf("skipped 终态被改写: status=%q profile=%q error_code=%q", got.Status, got.ProfileJSON, got.ErrorCode)
		}
	})

	t.Run("同 key 两次 failed 第二次 UPDATE 不产生新行", func(t *testing.T) {
		db := newFeatureTestDB(t, true)
		repo := repository.NewSessionFeatureRepository(db)
		first := makeFeature("sk-4", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))
		if _, err := repo.Save(context.Background(), first); err != nil {
			t.Fatalf("首写 failed: %v", err)
		}

		second := makeFeature("sk-4", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))
		second.ErrorCode = "ErrSchemaInvalid" // 重试仍失败，错误码刷新
		if reused, err := repo.Save(context.Background(), second); err != nil || reused {
			t.Fatalf("二次 failed want (false, nil), got (%v, %v)", reused, err)
		}

		if n := countRows(t, db); n != 1 {
			t.Fatalf("表行数 want 1, got %d", n)
		}
		got, err := repo.FindBySessionKey(context.Background(), "sk-4")
		if err != nil || got == nil {
			t.Fatalf("重查 failed 行: row=%v err=%v", got, err)
		}
		if got.ErrorCode != "ErrSchemaInvalid" {
			t.Fatalf("重试失败后 error_code want ErrSchemaInvalid, got %q", got.ErrorCode)
		}
		if got.ID != first.ID {
			t.Fatalf("failed 覆盖应保留原 ID: want %d, got %d", first.ID, got.ID)
		}
	})
}

// TestListByPersonAndRange 验证按人加时间窗取数（specs §2.4 能力3：T5 取数口径，返回全部三态行）。
// 种子与查询统一 UTC 口径（仓库侧约定：偏移串字典序可比的前提是两端同时区）。
func TestListByPersonAndRange(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	start, end := baseUnix, baseUnix+3600

	seeds := []*domain.SessionFeature{
		makeFeature("zr-1", "张三", domain.FeatureStatusSuccess, time.Unix(start+10, 0).UTC()),
		makeFeature("zr-2", "张三", domain.FeatureStatusFailed, time.Unix(start+20, 0).UTC()),
		makeFeature("zr-3", "张三", domain.FeatureStatusSuccess, time.Unix(start-100, 0).UTC()), // 窗外
		makeFeature("li-1", "李四", domain.FeatureStatusSuccess, time.Unix(start+15, 0).UTC()),  // 他人
	}
	for _, s := range seeds {
		if err := db.Create(s).Error; err != nil {
			t.Fatalf("seed %s: %v", s.SessionKey, err)
		}
	}

	list, err := repo.ListByPersonAndRange(context.Background(), "张三", start, end)
	if err != nil {
		t.Fatalf("ListByPersonAndRange: unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("返回行数 want 2（张三窗内 success+failed）, got %d", len(list))
	}
	statuses := map[string]bool{}
	for _, row := range list {
		if row.TokenName != "张三" {
			t.Fatalf("混入他人行: token_name=%q", row.TokenName)
		}
		statuses[row.Status] = true
	}
	if !statuses[domain.FeatureStatusFailed] {
		t.Fatalf("应含 failed 行（三态全返回）, got %v", statuses)
	}
	if list[0].FirstTurnAt.After(list[1].FirstTurnAt) {
		t.Fatalf("应按 first_turn_at 升序返回")
	}
}

// TestListByPersonAndRange_BoundaryInclusive 验证区间端点闭区间含边界（计划边界约定）。
func TestListByPersonAndRange_BoundaryInclusive(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	start, end := baseUnix, baseUnix+3600

	seeds := []struct {
		key  string
		at   int64
		want bool
	}{
		{"b-start", start, true},
		{"b-end", end, true},
		{"b-before", start - 1, false},
		{"b-after", end + 1, false},
	}
	for _, s := range seeds {
		if err := db.Create(makeFeature(s.key, "张三", domain.FeatureStatusSuccess, time.Unix(s.at, 0).UTC())).Error; err != nil {
			t.Fatalf("seed %s: %v", s.key, err)
		}
	}

	list, err := repo.ListByPersonAndRange(context.Background(), "张三", start, end)
	if err != nil {
		t.Fatalf("ListByPersonAndRange: unexpected error: %v", err)
	}
	got := map[string]bool{}
	for _, row := range list {
		got[row.SessionKey] = true
	}
	for _, s := range seeds {
		if got[s.key] != s.want {
			t.Fatalf("key=%s at=%d want in=%v, got %v", s.key, s.at, s.want, got[s.key])
		}
	}
}

// TestFindBySessionKey 验证 session_key 点查两态：命中返回该行，未命中返回 (nil, nil) 非 error。
func TestFindBySessionKey(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	seed := makeFeature("sk-find", "李四", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
	if err := db.Create(seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := repo.FindBySessionKey(context.Background(), "sk-find")
	if err != nil || got == nil {
		t.Fatalf("点查存在 key: row=%v err=%v", got, err)
	}
	if got.ID != seed.ID || got.Status != domain.FeatureStatusSuccess || got.SessionKey != "sk-find" {
		t.Fatalf("点查返回行字段不符: ID=%d status=%q key=%q", got.ID, got.Status, got.SessionKey)
	}

	miss, err := repo.FindBySessionKey(context.Background(), "not-exist")
	if err != nil {
		t.Fatalf("点查不存在 key 应返回 nil error, got %v", err)
	}
	if miss != nil {
		t.Fatalf("点查不存在 key want (nil, nil), got row=%v", miss)
	}
}

// newConflictTestDB 构造文件库与两个独立连接：db 供 repo 用，rival 模拟并发先写者
// （已提交可见）。:memory: 每连接独立库无法承载双连接场景，须落文件。
// Windows 下文件句柄不释放会卡死 TempDir 清理，注册 cleanup 显式关连接。
func newConflictTestDB(t *testing.T, translate bool) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "conflict.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
			TranslateError: translate,
			Logger:         logger.Default.LogMode(logger.Silent),
		})
		if err != nil {
			t.Fatalf("open sqlite %s: %v", dsn, err)
		}
		if err := db.AutoMigrate(&domain.SessionFeature{}); err != nil {
			t.Fatalf("auto migrate: %v", err)
		}
		return db
	}
	db, rival := open(), open()
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		if sqlDB, err := rival.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db, rival
}

// injectUniqueConflict 在 db 的 Create 前经 rival 连接抢先提交同 key 行，
// 确定性制造 uk_session_key 冲突（模拟并发双写的后写者，04 §3.1 索引说明）。
// 不用 tx.Exec：GORM Create 默认包事务，同事务内注入会随回滚消失。
func injectUniqueConflict(t *testing.T, db, rival *gorm.DB, key string) {
	t.Helper()
	const cbName = "test:inject_unique_conflict"
	db.Callback().Create().Before("gorm:create").Register(cbName, func(tx *gorm.DB) {
		sf, ok := tx.Statement.Dest.(*domain.SessionFeature)
		if !ok || sf.SessionKey != key {
			return
		}
		db.Callback().Create().Remove(cbName) // 防递归
		now := time.Now()
		if err := rival.Exec(
			`INSERT INTO session_features (session_key, token_name, status, client, turn_count, first_turn_at, last_turn_at, profile_json, error_code, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			key, "张三", domain.FeatureStatusSuccess, "claudecode", 1, time.Unix(baseUnix, 0), time.Unix(baseUnix, 0).Add(time.Hour), "{}", "", now, now,
		).Error; err != nil {
			tx.AddError(err)
		}
	})
}

// TestSaveInsertConflictConvergesToReused 验证 insert 撞 uk_session_key 后转重查收敛为 reused
// （TranslateError 开启路径，gorm.ErrDuplicatedKey 判定）。
func TestSaveInsertConflictConvergesToReused(t *testing.T) {
	db, rival := newConflictTestDB(t, true)
	injectUniqueConflict(t, db, rival, "sk-conflict")
	repo := repository.NewSessionFeatureRepository(db)

	rec := makeFeature("sk-conflict", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
	reused, err := repo.Save(context.Background(), rec)
	if err != nil {
		t.Fatalf("冲突收敛后应无 error, got %v", err)
	}
	if !reused {
		t.Fatalf("并发双写后写者应收敛 reused=true, got false")
	}
	if n := countRows(t, db); n != 1 {
		t.Fatalf("表行数 want 1, got %d", n)
	}
}

// TestSaveInsertConflictWithoutTranslateError 验证生产 gorm.Config 未开 TranslateError 时
// 原生错误兜底判定仍收敛 reused（SQLite 路径走 *gosqlite.Error Code==2067 结构化判定，
// 错误串匹配仅作翻译不可用时的最后防线）。
func TestSaveInsertConflictWithoutTranslateError(t *testing.T) {
	db, rival := newConflictTestDB(t, false)
	injectUniqueConflict(t, db, rival, "sk-conflict")
	repo := repository.NewSessionFeatureRepository(db)

	rec := makeFeature("sk-conflict", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
	reused, err := repo.Save(context.Background(), rec)
	if err != nil {
		t.Fatalf("原生错误形态下冲突应被兜底识别, got %v", err)
	}
	if !reused {
		t.Fatalf("兜底路径应收敛 reused=true, got false")
	}
}

// TestListByPersonAndRangeUTCEndpoints 验证查询端点 UTC 口径：写入侧以 UTC 落库的行
// 无论进程本地时区如何偏移都应命中窗口（时区错位会让偏移串字典序与时间序错位漏行）。
func TestListByPersonAndRangeUTCEndpoints(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	start, end := baseUnix, baseUnix+3600

	// 种子行走 db.Create（GORM 绑定 time.Time），与生产写入同路径。
	seed := makeFeature("utc-1", "张三", domain.FeatureStatusSuccess, time.Unix(start+10, 0).UTC())
	if err := db.Create(seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 查询端点 UTC 口径下应命中；若端点按本地时区口径落偏移串则可能漏行。
	list, err := repo.ListByPersonAndRange(context.Background(), "张三", start, end)
	if err != nil {
		t.Fatalf("ListByPersonAndRange: unexpected error: %v", err)
	}
	if len(list) != 1 || list[0].SessionKey != "utc-1" {
		t.Fatalf("UTC 口径窗口应命中种子行, got %v", list)
	}
}

// TestSaveFlipRefreshesUpdatedAt 验证翻转仍刷新 updated_at：GORM 对 map 无该键的
// Updates 自动 append NowFunc()（callbacks/update.go，手写 time.Now 绕过钩子已删）。
// 回拨到明确的旧秒再比较：雪花自旋会把 NowFunc 拖回上一个 4096 毫秒槽的起点，
// 近似当前时刻的基准值会在自旋回过神的场景下年微秒级误判。
func TestSaveFlipRefreshesUpdatedAt(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	failedRec := makeFeature("sk-upd", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))
	if _, err := repo.Save(context.Background(), failedRec); err != nil {
		t.Fatalf("首写 failed: %v", err)
	}

	backdate := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	if err := db.Exec("UPDATE session_features SET updated_at = ? WHERE session_key = ?",
		backdate, "sk-upd").Error; err != nil {
		t.Fatalf("回拨 updated_at: %v", err)
	}

	successRec := makeFeature("sk-upd", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
	if _, err := repo.Save(context.Background(), successRec); err != nil {
		t.Fatalf("翻转 success: %v", err)
	}
	var updatedAtAfter time.Time
	if err := db.Model(&domain.SessionFeature{}).Where("session_key = ?", "sk-upd").
		Select("updated_at").Scan(&updatedAtAfter).Error; err != nil {
		t.Fatalf("取翻转后 updated_at: %v", err)
	}
	if updatedAtAfter.Before(backdate) {
		t.Fatalf("翻转应刷新 updated_at: backdate=%v after=%v", backdate, updatedAtAfter)
	}
	if sub := updatedAtAfter.Sub(backdate); sub < time.Minute {
		t.Fatalf("刷新值应接近当前时刻: backdate=%v after=%v diff=%v", backdate, updatedAtAfter, sub)
	}
}

// simulateConcurrentFlip 经 rival 连接在目标 UPDATE 前抢先提交同 key 的 failed→success
// 翻转，串行模拟 Asynq 双交付竞态：读得 failed 快照的后写者 UPDATE 必须落空转收敛。
func simulateConcurrentFlip(t *testing.T, db, rival *gorm.DB, key string) {
	t.Helper()
	const cbName = "test:simulate_concurrent_flip"
	db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement.Table != "session_features" {
			return
		}
		cols, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok || cols["status"] != domain.FeatureStatusSuccess {
			return
		}
		db.Callback().Update().Remove(cbName) // 防递归
		if err := rival.Exec(
			`UPDATE session_features SET status = ?, profile_json = ?, error_code = ?, updated_at = ? WHERE session_key = ?`,
			domain.FeatureStatusSuccess, `{"stats":{"turn_count":5},"summary":"rival"}`, "", time.Now(), key,
		).Error; err != nil {
			tx.AddError(err)
		}
	})
}

// TestSaveConflictRequeryFailure 验证 Create 撞唯一索引后收敛路径的重查故障透传
// （不静默当收敛成功）。
func TestSaveConflictRequeryFailure(t *testing.T) {
	db, rival := newConflictTestDB(t, true)
	injected := false
	const createCb = "test:conflict_then_fail_requery"
	db.Callback().Create().Before("gorm:create").Register(createCb, func(tx *gorm.DB) {
		sf, ok := tx.Statement.Dest.(*domain.SessionFeature)
		if !ok || sf.SessionKey != "sk-cq" {
			return
		}
		db.Callback().Create().Remove(createCb) // 防递归
		injected = true
		// 抢先插入同 key 行制造唯一冲突，再把后续查询挂掉。
		now := time.Now()
		if err := rival.Exec(
			`INSERT INTO session_features (session_key, token_name, status, client, turn_count, first_turn_at, last_turn_at, profile_json, error_code, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"sk-cq", "张三", domain.FeatureStatusSuccess, "claudecode", 1, time.Unix(baseUnix, 0), time.Unix(baseUnix, 0).Add(time.Hour), "{}", "", now, now,
		).Error; err != nil {
			tx.AddError(err)
		}
	})
	const queryCb = "test:fail_session_query"
	db.Callback().Query().Before("gorm:query").Register(queryCb, func(tx *gorm.DB) {
		if injected && tx.Statement.Table == "session_features" {
			tx.AddError(errors.New("boom: requery failed"))
		}
	})
	t.Cleanup(func() { db.Callback().Query().Remove(queryCb) })
	repo := repository.NewSessionFeatureRepository(db)

	_, err := repo.Save(context.Background(), makeFeature("sk-cq", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0)))
	if err == nil || !strings.Contains(err.Error(), "requery failed") {
		t.Fatalf("冲突后重查故障应透传, got %v", err)
	}
}

// TestSaveFlipConcurrentTerminalNotOverwritten 断言 success 终态不被并发后写者覆写：
// 后写者读得 failed 行、先写者已翻转为 success 时，前置状态条件使 UPDATE 落空，
// 重查收敛 reused=true 且库内行保持先写者的 success 内容。
func TestSaveFlipConcurrentTerminalNotOverwritten(t *testing.T) {
	db, rival := newConflictTestDB(t, true)
	simulateConcurrentFlip(t, db, rival, "sk-race")
	repo := repository.NewSessionFeatureRepository(db)
	failedRec := makeFeature("sk-race", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))
	if _, err := repo.Save(context.Background(), failedRec); err != nil {
		t.Fatalf("首写 failed: %v", err)
	}
	origID := failedRec.ID

	successRec := makeFeature("sk-race", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0))
	reused, err := repo.Save(context.Background(), successRec)
	if err != nil {
		t.Fatalf("并发翻转后写者: unexpected error: %v", err)
	}
	if !reused {
		t.Fatalf("后写者 UPDATE 落空应重查收敛 reused=true, got false")
	}

	got, err := repo.FindBySessionKey(context.Background(), "sk-race")
	if err != nil || got == nil {
		t.Fatalf("重查终态行: row=%v err=%v", got, err)
	}
	if got.Status != domain.FeatureStatusSuccess {
		t.Fatalf("success 终态被并发覆写: status=%q", got.Status)
	}
	if got.ProfileJSON != `{"stats":{"turn_count":5},"summary":"rival"}` {
		t.Fatalf("终态行内容被后写者改写: profile=%q", got.ProfileJSON)
	}
	if got.ID != origID {
		t.Fatalf("行应保留原 ID: want %d, got %d", origID, got.ID)
	}
	if n := countRows(t, db); n != 1 {
		t.Fatalf("表行数 want 1, got %d", n)
	}
}

// TestSaveQueryFailure 验证预检查询故障透传：连接关闭后 Save 与 FindBySessionKey、
// ListByPersonAndRange 均返回 error 而非静默成功。
func TestSaveQueryFailure(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}

	if _, err := repo.Save(context.Background(), makeFeature("sk-err", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))); err == nil {
		t.Fatal("连接关闭后 Save want error, got nil")
	}
	if _, err := repo.FindBySessionKey(context.Background(), "sk-err"); err == nil {
		t.Fatal("连接关闭后 FindBySessionKey want error, got nil")
	}
	if _, err := repo.ListByPersonAndRange(context.Background(), "张三", 0, 1); err == nil {
		t.Fatal("连接关闭后 ListByPersonAndRange want error, got nil")
	}
}

// injectCreateError 在 Create 前注入指定错误（不真插行），确定性构造 Create 失败路径。
func injectCreateError(t *testing.T, db *gorm.DB, key string, createErr error) {
	t.Helper()
	const cbName = "test:inject_create_error"
	db.Callback().Create().Before("gorm:create").Register(cbName, func(tx *gorm.DB) {
		sf, ok := tx.Statement.Dest.(*domain.SessionFeature)
		if !ok || sf.SessionKey != key {
			return
		}
		db.Callback().Create().Remove(cbName) // 防递归
		tx.AddError(createErr)
	})
}

// TestSaveCreateNonUniqueError 验证 Create 失败但非唯一冲突时原样透传错误（不进收敛路径）。
func TestSaveCreateNonUniqueError(t *testing.T) {
	db := newFeatureTestDB(t, true)
	boom := errors.New("boom: insert failed")
	injectCreateError(t, db, "sk-nonuniq", boom)
	repo := repository.NewSessionFeatureRepository(db)

	reused, err := repo.Save(context.Background(), makeFeature("sk-nonuniq", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0)))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("非唯一冲突错误应原样透传, got reused=%v err=%v", reused, err)
	}
	if reused {
		t.Fatal("错误路径不应返回 reused=true")
	}
}

// TestSaveConflictButRowMissing 验证 Create 报唯一冲突但重查无行（极端竞态：对端事务
// 尚未提交又回滚）时返回原始冲突错误，不静默成功。注入 gorm.ErrDuplicatedKey
// （生产 TranslateError 统一翻译形态）确保冲突被识别进收敛分支。
func TestSaveConflictButRowMissing(t *testing.T) {
	db := newFeatureTestDB(t, true)
	conflict := gorm.ErrDuplicatedKey
	injectCreateError(t, db, "sk-ghost", conflict)
	repo := repository.NewSessionFeatureRepository(db)

	reused, err := repo.Save(context.Background(), makeFeature("sk-ghost", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0)))
	if err == nil || !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("冲突后重查无行应返回原始冲突错误, got reused=%v err=%v", reused, err)
	}
	if reused {
		t.Fatal("无行可收敛时不应返回 reused=true")
	}
}

// TestSaveFlipUpdateError 验证翻转 UPDATE 故障透传错误（不误报 reused）。
func TestSaveFlipUpdateError(t *testing.T) {
	db := newFeatureTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	if _, err := repo.Save(context.Background(), makeFeature("sk-upderr", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))); err != nil {
		t.Fatalf("首写 failed: %v", err)
	}
	const cbName = "test:inject_update_error"
	db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement.Table != "session_features" {
			return
		}
		db.Callback().Update().Remove(cbName) // 防递归
		tx.AddError(errors.New("boom: update failed"))
	})

	reused, err := repo.Save(context.Background(), makeFeature("sk-upderr", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0)))
	if err == nil || !strings.Contains(err.Error(), "update failed") {
		t.Fatalf("UPDATE 故障应透传, got reused=%v err=%v", reused, err)
	}
	if reused {
		t.Fatal("UPDATE 故障不应返回 reused=true")
	}
}

// TestSaveFlipRowDeletedConcurrently 验证翻转 UPDATE 落空且行已被并发删除：
// 返回 error 交任务重试，防本端内容静默丢失（行已不在库内，本周期档案无处可落）。
func TestSaveFlipRowDeletedConcurrently(t *testing.T) {
	db, rival := newConflictTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	if _, err := repo.Save(context.Background(), makeFeature("sk-gone", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))); err != nil {
		t.Fatalf("首写 failed: %v", err)
	}
	const cbName = "test:rival_delete_on_update"
	db.Callback().Update().Before("gorm:update").Register(cbName, func(tx *gorm.DB) {
		if tx.Statement.Table != "session_features" {
			return
		}
		cols, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok || cols["status"] != domain.FeatureStatusSuccess {
			return
		}
		db.Callback().Update().Remove(cbName) // 防递归
		if err := rival.Exec("DELETE FROM session_features WHERE session_key = ?", "sk-gone").Error; err != nil {
			tx.AddError(err)
		}
	})

	reused, err := repo.Save(context.Background(), makeFeature("sk-gone", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0)))
	if err == nil {
		t.Fatal("行被并发删除应返回 error 交任务重试，而非静默当作写入成功")
	}
	if reused {
		t.Fatal("行已不存在，无终态可收敛，want reused=false")
	}
	if n := countRows(t, db); n != 0 {
		t.Fatalf("表行数 want 0, got %d", n)
	}
}

// TestSaveFlipConvergenceRequeryFailure 验证并发收敛路径的重查故障透传：
// UPDATE 落空（行被并发删除）后重查报错时返回 error，不静默当作非终态。
func TestSaveFlipConvergenceRequeryFailure(t *testing.T) {
	db, rival := newConflictTestDB(t, true)
	repo := repository.NewSessionFeatureRepository(db)
	if _, err := repo.Save(context.Background(), makeFeature("sk-cv", "张三", domain.FeatureStatusFailed, time.Unix(baseUnix, 0))); err != nil {
		t.Fatalf("首写 failed: %v", err)
	}
	requeryFailed := false
	const updCb = "test:rival_delete_then_flag"
	db.Callback().Update().Before("gorm:update").Register(updCb, func(tx *gorm.DB) {
		if tx.Statement.Table != "session_features" {
			return
		}
		cols, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok || cols["status"] != domain.FeatureStatusSuccess {
			return
		}
		db.Callback().Update().Remove(updCb) // 防递归
		if err := rival.Exec("DELETE FROM session_features WHERE session_key = ?", "sk-cv").Error; err != nil {
			tx.AddError(err)
			return
		}
		requeryFailed = true
	})
	const qryCb = "test:fail_next_query"
	db.Callback().Query().Before("gorm:query").Register(qryCb, func(tx *gorm.DB) {
		if requeryFailed && tx.Statement.Table == "session_features" {
			tx.AddError(errors.New("boom: requery failed"))
		}
	})

	_, err := repo.Save(context.Background(), makeFeature("sk-cv", "张三", domain.FeatureStatusSuccess, time.Unix(baseUnix, 0)))
	if err == nil || !strings.Contains(err.Error(), "requery failed") {
		t.Fatalf("收敛路径重查故障应透传, got %v", err)
	}
}
