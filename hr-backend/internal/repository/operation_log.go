// OperationLogRepository 承载操作日志域数据访问（specs P4_LOG_001 §5.1 写入、
// §5.3 查询、§5.5 清理）：追加写入、四维筛选查询与边界前分批删除。
package repository

import (
	"context"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// cleanBatchSize 清理任务单批删除上限（specs §5.5.4 规则1：分批执行避免长事务锁表）。
const cleanBatchSize = 1000

// OperationLogFilter 四维筛选条件：空串与零值跳过对应维度。
type OperationLogFilter struct {
	Operator string    // 姓名模糊，非空时 LIKE 转义匹配
	Module   string    // 八类枚举精确，空=全部
	Result   string    // success/fail 精确，空=全部
	StartAt  time.Time // 双闭区间起（已含），零值=不限
	EndAt    time.Time // 双闭区间止（已含），零值=不限
}

// OperationLogRepository 是操作日志域的数据访问接口。枚举合法性与时间起止顺序
// 校验归 service 层（specs §5.3.2 步骤2），仓储只按传入值组装条件。
type OperationLogRepository interface {
	// InsertBatch 批量落库（异步通道消费），雪花 ID 由 Create 回调赋值。
	InsertBatch(ctx context.Context, logs []domain.OperationLog) error
	// Insert 单条落库（worker 侧批次节点写入）。
	Insert(ctx context.Context, log *domain.OperationLog) error
	// ListPage 按 filter 分页查询，created_at 倒序（specs §4.1.4 规则5 唯一排序键）。
	ListPage(ctx context.Context, f OperationLogFilter, page, pageSize int) ([]domain.OperationLog, int64, error)
	// ListByFilter 同筛选口径取前 limit 行（导出 10000 上限），无 Count。
	ListByFilter(ctx context.Context, f OperationLogFilter, limit int) ([]domain.OperationLog, error)
	// DeleteBefore 单批删除 created_at 早于 before 的记录，返回受影响行数，
	// 调用方循环驱动至 0 行（specs §5.5.2 步骤2）。
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}

type operationLogRepository struct {
	db *gorm.DB
}

func NewOperationLogRepository(db *gorm.DB) OperationLogRepository {
	return &operationLogRepository{db: db}
}

func (r *operationLogRepository) InsertBatch(ctx context.Context, logs []domain.OperationLog) error {
	if len(logs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&logs).Error
}

func (r *operationLogRepository) Insert(ctx context.Context, log *domain.OperationLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// applyFilter 组装查询条件（specs §5.3.2 步骤3）：Operator 非空经 EscapeLike 转义
// 后中缀字面匹配（BR1 多库方言约束），Module/Result 等值，created_at 双闭区间
// 仅在非零值时追加。
func (r *operationLogRepository) applyFilter(query *gorm.DB, f OperationLogFilter) *gorm.DB {
	if f.Operator != "" {
		pat := "%" + likeescape.EscapeLike(f.Operator) + "%"
		query = query.Where("operator LIKE ? ESCAPE '\\'", pat)
	}
	if f.Module != "" {
		query = query.Where("module = ?", f.Module)
	}
	if f.Result != "" {
		query = query.Where("result = ?", f.Result)
	}
	if !f.StartAt.IsZero() {
		query = query.Where("created_at >= ?", f.StartAt)
	}
	if !f.EndAt.IsZero() {
		query = query.Where("created_at <= ?", f.EndAt)
	}
	return query
}

// ListPage 按 filter 组装条件后先 Count 后分页取数，page 应 >= 1（调用方保证）。
func (r *operationLogRepository) ListPage(ctx context.Context, f OperationLogFilter, page, pageSize int) ([]domain.OperationLog, int64, error) {
	query := r.applyFilter(r.db.WithContext(ctx).Model(&domain.OperationLog{}), f)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []domain.OperationLog
	if err := query.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *operationLogRepository) ListByFilter(ctx context.Context, f OperationLogFilter, limit int) ([]domain.OperationLog, error) {
	var list []domain.OperationLog
	if err := r.applyFilter(r.db.WithContext(ctx).Model(&domain.OperationLog{}), f).
		Order("created_at DESC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// DeleteBefore 子查询形态限定单批 1000：DELETE ... WHERE id IN (SELECT id
// FROM (SELECT id ... LIMIT 1000) t)。内层派生表不可省：MySQL 对 IN 直嵌
// LIMIT 子查询报 ERROR 1235（ Restrictions on Subqueries），包装后三库通用
//（PG 不支持 DELETE ... LIMIT，SQLite 无此限制一并走同形态）。
func (r *operationLogRepository) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id IN (SELECT id FROM (SELECT id FROM operation_logs WHERE created_at < ? LIMIT ?) AS t)", before, cleanBatchSize).
		Delete(&domain.OperationLog{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
