// Package repository_test 对 operation_log 仓储做黑盒集成测试（specs P4_LOG_001 §5.3/§5.5）。
//
// 每测试独立 :memory: SQLite（不经过 model.InitDB），自行初始化雪花节点并在测试
// *gorm.DB 上内联注册 Create 回调处理 OperationLog。显式 CreatedAt 落值规避连续
// 插入同秒导致 ORDER BY created_at DESC 排序不稳定。
package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/snowflake"
	"sili-smart-hr/backend/internal/repository"
)

// newOpLogTestDB 构造独立 :memory: SQLite gorm.DB，AutoMigrate OperationLog
// 并内联注册简化版雪花 Create 回调，与生产 model 包的全量反射版等效（含切片批量场景）。
func newOpLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if err := snowflake.Init(1); err != nil {
		t.Fatalf("snowflake init: %v", err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Callback().Create().Before("gorm:create").Register("sili:snowflake_id_oplog_test", func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Dest == nil {
			return
		}
		switch dest := tx.Statement.Dest.(type) {
		case *domain.OperationLog:
			if dest.ID == 0 {
				dest.ID = snowflake.NextID()
			}
		case *[]domain.OperationLog:
			for i := range *dest {
				if (*dest)[i].ID == 0 {
					(*dest)[i].ID = snowflake.NextID()
				}
			}
		}
	})
	if err := db.AutoMigrate(&domain.OperationLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedOpLog 经 db 写入一行操作日志（显式 CreatedAt 稳定排序），返回带 ID 实体。
func seedOpLog(t *testing.T, db *gorm.DB, operator, module, result string, createdAt time.Time) domain.OperationLog {
	t.Helper()
	row := domain.OperationLog{
		Operator:    operator,
		Module:      module,
		Result:      result,
		Target:      "测试对象",
		Summary:     "测试摘要",
		RequestPath: "POST /api/test",
		CreatedAt:   createdAt,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed operation_log: %v", err)
	}
	return row
}

// TestOperationLogRepositoryInsert 覆盖两条写入路径：Insert 单条、InsertBatch 批量
//（空切片直过，调用方无空判负担），雪花 ID 由 Create 回调赋值。
func TestOperationLogRepositoryInsert(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	repo := repository.NewOperationLogRepository(db)

	// 批量先落表：空表 rowid 自增从 1 起，量级断言才能区分雪花与 rowid 回填。
	if err := repo.InsertBatch(ctx, []domain.OperationLog{
		{Operator: "系统", Module: domain.OpModuleSystemJob, Result: domain.OpResultSuccess},
		{Operator: "李四", Module: domain.OpModuleLogin, Result: domain.OpResultFail},
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if err := repo.Insert(ctx, &domain.OperationLog{
		Operator: "张三", Module: domain.OpModuleAccount, Result: domain.OpResultSuccess,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.InsertBatch(ctx, nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}

	var total int64
	if err := db.Model(&domain.OperationLog{}).Count(&total).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	var rows []domain.OperationLog
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	for _, row := range rows {
		// 量级断言区分雪花 ID 与 SQLite rowid 自增回填（后者从 1 起）。
		if row.ID < 1e15 {
			t.Fatalf("snowflake-scale id expected, got %d: %+v", row.ID, row)
		}
	}
}

// TestOperationLogRepositoryListPage 验证四维筛选 + 倒序分页（specs §5.3.2 步骤3/4）：
// module 精确、operator 模糊（BR1 LIKE 转义）、result 精确、时间双闭区间、
// 无条件全量首行 created_at 最大（BR2 倒序）。
func TestOperationLogRepositoryListPage(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	now := time.Now()
	// 三行依次推进 created_at，第三行（系统/system_job）最新。
	row1 := seedOpLog(t, db, "张三", domain.OpModuleDimension, domain.OpResultSuccess, now.Add(-2*time.Hour))
	row2 := seedOpLog(t, db, "李四", domain.OpModuleLogin, domain.OpResultFail, now.Add(-1*time.Hour))
	row3 := seedOpLog(t, db, domain.OpOperatorSystem, domain.OpModuleSystemJob, domain.OpResultSuccess, now)

	repo := repository.NewOperationLogRepository(db)

	// module 精确。
	list, total, err := repo.ListPage(ctx, repository.OperationLogFilter{Module: domain.OpModuleDimension}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage module: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Module != domain.OpModuleDimension {
		t.Fatalf("module filter: total=%d len=%d", total, len(list))
	}
	if list[0].ID != row1.ID {
		t.Fatalf("module filter hit wrong row: got id %d want %d", list[0].ID, row1.ID)
	}

	// operator 模糊命中「系统」。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{Operator: "系"}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage operator: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Operator != domain.OpOperatorSystem {
		t.Fatalf("operator filter: total=%d len=%d", total, len(list))
	}

	// result 精确。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{Result: domain.OpResultFail}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage result: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != row2.ID {
		t.Fatalf("result filter: total=%d len=%d", total, len(list))
	}

	// 时间双闭区间：起止各贴一行边界，界外行（更早一行）被排除。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{
		StartAt: row2.CreatedAt,
		EndAt:   row3.CreatedAt,
	}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage range: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("range filter: total=%d len=%d, want 2（双闭含两端，排除界外 row1）", total, len(list))
	}
	for _, row := range list {
		if row.ID == row1.ID {
			t.Fatalf("range filter leaked out-of-range row1")
		}
	}

	// 无条件：total 全量，首行 created_at 最大（BR2）。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage all: %v", err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("no filter: total=%d len=%d, want 3", total, len(list))
	}
	if !list[0].CreatedAt.Equal(row3.CreatedAt) {
		t.Fatalf("desc order: first row created_at=%v, want %v", list[0].CreatedAt, row3.CreatedAt)
	}
	if list[0].ID != row3.ID || list[1].ID != row2.ID || list[2].ID != row1.ID {
		t.Fatalf("desc order ids = [%d %d %d], want [row3 row2 row1]", list[0].ID, list[1].ID, list[2].ID)
	}

	// 分页 Offset/Limit：page=2 pageSize=2 取最旧行。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{}, 2, 2)
	if err != nil {
		t.Fatalf("ListPage page2: %v", err)
	}
	if total != 3 || len(list) != 1 || list[0].ID != row1.ID {
		t.Fatalf("pagination: total=%d len=%d firstID=%d, want 3/1/row1", total, len(list), list[0].ID)
	}
}

// TestOperationLogRepositoryListByFilter 验证同筛选口径无 Count 只 Limit（specs §5.4 导出面）。
func TestOperationLogRepositoryListByFilter(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	now := time.Now()
	seedOpLog(t, db, "张三", domain.OpModuleDimension, domain.OpResultSuccess, now.Add(-2*time.Hour))
	seedOpLog(t, db, "李四", domain.OpModuleLogin, domain.OpResultFail, now.Add(-1*time.Hour))
	row3 := seedOpLog(t, db, domain.OpOperatorSystem, domain.OpModuleSystemJob, domain.OpResultSuccess, now)

	repo := repository.NewOperationLogRepository(db)

	list, err := repo.ListByFilter(ctx, repository.OperationLogFilter{}, 2)
	if err != nil {
		t.Fatalf("ListByFilter: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("limit: len=%d, want 2", len(list))
	}
	if !list[0].CreatedAt.Equal(row3.CreatedAt) {
		t.Fatalf("desc order: first created_at=%v, want %v", list[0].CreatedAt, row3.CreatedAt)
	}

	list, err = repo.ListByFilter(ctx, repository.OperationLogFilter{Module: domain.OpModuleLogin}, 100)
	if err != nil {
		t.Fatalf("ListByFilter module: %v", err)
	}
	if len(list) != 1 || list[0].Module != domain.OpModuleLogin {
		t.Fatalf("module filter: len=%d", len(list))
	}
}

// TestOperationLogRepositoryOperatorLikeEscape 验证 BR1：operator 含 % 与 _ 通配符时
// 经 EscapeLike 字面匹配，不扩大命中集（specs §5.3.4 规则2 多库方言约束）。
func TestOperationLogRepositoryOperatorLikeEscape(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	now := time.Now()
	seedOpLog(t, db, "张三", domain.OpModuleDimension, domain.OpResultSuccess, now.Add(-2*time.Hour))
	seedOpLog(t, db, "100%覆盖", domain.OpModuleLogin, domain.OpResultFail, now.Add(-1*time.Hour))
	seedOpLog(t, db, "王_五", domain.OpModuleSystemJob, domain.OpResultSuccess, now)

	repo := repository.NewOperationLogRepository(db)

	// % 作为字面：仅命中「100%覆盖」，不匹配全部行。
	list, total, err := repo.ListPage(ctx, repository.OperationLogFilter{Operator: "0%"}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage literal %%: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Operator != "100%覆盖" {
		t.Fatalf("literal %%: total=%d len=%d", total, len(list))
	}

	// _ 作为字面：仅命中「王_五」，不做单字符通配。
	list, total, err = repo.ListPage(ctx, repository.OperationLogFilter{Operator: "王_"}, 1, 10)
	if err != nil {
		t.Fatalf("ListPage literal _: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Operator != "王_五" {
		t.Fatalf("literal _: total=%d len=%d", total, len(list))
	}
}

// TestOperationLogRepositoryDeleteBefore 验证 BR3：单批删除边界前记录（specs §5.5.2 步骤2）。
// 落 3 行（now-200d/now-100d/now），边界 now-180d 仅删最旧 1 行，剩 2 行。
func TestOperationLogRepositoryDeleteBefore(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	now := time.Now()
	seedOpLog(t, db, "张三", domain.OpModuleDimension, domain.OpResultSuccess, now.Add(-200*24*time.Hour))
	seedOpLog(t, db, "李四", domain.OpModuleLogin, domain.OpResultFail, now.Add(-100*24*time.Hour))
	seedOpLog(t, db, domain.OpOperatorSystem, domain.OpModuleSystemJob, domain.OpResultSuccess, now)

	repo := repository.NewOperationLogRepository(db)
	n, err := repo.DeleteBefore(ctx, now.Add(-180*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteBefore affected = %d, want 1", n)
	}
	var total int64
	if err := db.Model(&domain.OperationLog{}).Count(&total).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 2 {
		t.Fatalf("remaining rows = %d, want 2", total)
	}
}

// TestOperationLogRepositoryDeleteBeforeBatchCap 验证 BR3 单批上限 1000：落 1001 行
// 边界前数据，单次 DeleteBefore 至多删 1000，调用方循环驱动至 0 行。
func TestOperationLogRepositoryDeleteBeforeBatchCap(t *testing.T) {
	db := newOpLogTestDB(t)
	ctx := context.Background()
	now := time.Now()
	rows := make([]domain.OperationLog, 0, 1001)
	for i := 0; i < 1001; i++ {
		rows = append(rows, domain.OperationLog{
			Operator:  "张三",
			Module:    domain.OpModuleDimension,
			Result:    domain.OpResultSuccess,
			CreatedAt: now.Add(-time.Duration(365+i) * 24 * time.Hour),
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed 1001 rows: %v", err)
	}

	repo := repository.NewOperationLogRepository(db)
	n, err := repo.DeleteBefore(ctx, now.Add(-180*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if n != 1000 {
		t.Fatalf("single batch affected = %d, want 1000 (cap)", n)
	}

	// 循环驱动至 0 行：剩余 1 行再删一轮收尾。
	n, err = repo.DeleteBefore(ctx, now.Add(-180*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore second round: %v", err)
	}
	if n != 1 {
		t.Fatalf("second round affected = %d, want 1", n)
	}
	n, err = repo.DeleteBefore(ctx, now.Add(-180*24*time.Hour))
	if err != nil {
		t.Fatalf("DeleteBefore final round: %v", err)
	}
	if n != 0 {
		t.Fatalf("final round affected = %d, want 0", n)
	}
}
