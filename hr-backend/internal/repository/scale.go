// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"
	"sili-smart-hr/backend/internal/questionbank/scaledata"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrScaleImported 量表已引入（specs 4.1.4 规则 8）：存在该量表未逻辑删除题目。
var ErrScaleImported = errors.New("scale already imported")

// enneAnchor 型别维度 anchor（04 §4.2）：不参与聚合评分，置型别一句话描述。
func enneAnchor(typeName string) string {
	return fmt.Sprintf("九型 %s 型别倾向，量表题聚合归属", typeName)
}

// ScaleRepository 是量表引入域的数据访问接口：已引入判定与引入事务（行锁 +
// 维度 ensure + 建批）收口在 repo，事务编排见 ImportScale。
type ScaleRepository interface {
	// CountImported 按 scale_key 统计未逻辑删除题目数（specs 4.1.4 规则 8：四态全算，
	// 软删不计），>0 即已引入。两 key 一次查回。tx 传 nil 用 r.db（ListScales 场景），
	// 引入事务内传外层 tx 与行锁同连接读（事务内当前读）。
	CountImported(ctx context.Context, tx *gorm.DB, keys []string) (map[string]int64, error)
	// LockEnneagramDimensions 引入事务内对 ENNEAGRAM 型别维度行加锁（SELECT ... FOR UPDATE）：
	// 按内置 code 集合查活跃行锁定并返回；SQLite 忽略锁子句不影响正确性（单写串行）。
	// 行缺失的 code 不在返回集（由 ensure 补建）。两套量表共享 9 行，锁此即串行化并发引入。
	LockEnneagramDimensions(ctx context.Context, tx *gorm.DB, codes []string) ([]domain.Dimension, error)
	// EnsureEnneagramDimensions 锁内 ensure（04 §4.2）：code 已存在（活跃行，任意启停状态）
	// 则复用，缺失（含软删行占位码不可见）则按模板新建。返回 code→维度 ID 映射。
	EnsureEnneagramDimensions(ctx context.Context, tx *gorm.DB, dims []scaledata.ScaleDimension) (map[string]int64, error)
	// ImportScale 引入事务主体（specs 4.1.3）：行锁 → ensure → 锁内判已引入（>0 返回
	// ErrScaleImported）→ 组装 questions → CreateBatchWithQuestions(外层 tx)（Q-Scale
	// 编号分配收在事务内）→ COMMIT。撞 uk_question_batches_batch_no 上抛由 service 处理。
	ImportScale(ctx context.Context, tpl *scaledata.ScaleTemplate, now time.Time) (*domain.QuestionBatch, error)
}

type scaleRepository struct {
	db        *gorm.DB
	batchRepo QuestionBatchRepository
}

// NewScaleRepository 返回 ScaleRepository 接口实现，内部复用同一 *gorm.DB 构造
// QuestionBatchRepository 以调用 CreateBatchWithQuestions/NextBatchNo。
func NewScaleRepository(db *gorm.DB) ScaleRepository {
	return &scaleRepository{db: db, batchRepo: NewQuestionBatchRepository(db)}
}

// runner 返回本方法实际执行通道：tx 非 nil 复用外层事务，nil 落 r.db。
func (r *scaleRepository) runner(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

// CountImported GORM DeletedAt 作用域已过滤软删行，直接按 scale_key 分组计数。
// AI 题 scale_key 空串天然不命中传入的量表 key。未出现的 key 落 0 值进 map，
// 调用方免二次存在性判断。
func (r *scaleRepository) CountImported(ctx context.Context, tx *gorm.DB, keys []string) (map[string]int64, error) {
	counts := make(map[string]int64, len(keys))
	if len(keys) == 0 {
		return counts, nil
	}
	var rows []struct {
		ScaleKey string
		N        int64
	}
	if err := r.runner(tx).WithContext(ctx).Model(&domain.Question{}).
		Select("scale_key, COUNT(*) AS n").
		Where("scale_key IN ?", keys).
		Group("scale_key").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.ScaleKey] = row.N
	}
	for _, k := range keys {
		if _, ok := counts[k]; !ok {
			counts[k] = 0
		}
	}
	return counts, nil
}

// LockEnneagramDimensions 活跃行加锁查询（软删行 GORM 自动过滤视为不存在）。
func (r *scaleRepository) LockEnneagramDimensions(ctx context.Context, tx *gorm.DB, codes []string) ([]domain.Dimension, error) {
	dims := []domain.Dimension{}
	if len(codes) == 0 {
		return dims, nil
	}
	if err := r.runner(tx).WithContext(ctx).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Where("code IN ?", codes).
		Find(&dims).Error; err != nil {
		return nil, err
	}
	return dims, nil
}

// EnsureEnneagramDimensions 锁内补建：查活跃行复用（任意启停状态），缺失按模板
// 新建（module_code=ENNEAGRAM、data_source=TEST、enabled=true、weight=0、
// include_overview=false、is_reference=true、version=1）。CreateInBatches 撞
// uk_dimension_code 的 UniqueViolation 视为并发对端已建，重查复用。
func (r *scaleRepository) EnsureEnneagramDimensions(ctx context.Context, tx *gorm.DB, dims []scaledata.ScaleDimension) (map[string]int64, error) {
	ids := make(map[string]int64, len(dims))
	if len(dims) == 0 {
		return ids, nil
	}
	codes := make([]string, len(dims))
	templates := make(map[string]scaledata.ScaleDimension, len(dims))
	for i, d := range dims {
		codes[i] = d.Code
		templates[d.Code] = d
	}
	var existing []domain.Dimension
	if err := r.runner(tx).WithContext(ctx).
		Where("code IN ?", codes).
		Find(&existing).Error; err != nil {
		return nil, err
	}
	for _, row := range existing {
		ids[row.Code] = row.ID
	}
	missing := make([]domain.Dimension, 0, len(dims)-len(existing))
	seen := map[string]bool{}
	for _, row := range existing {
		seen[row.Code] = true
	}
	for _, code := range codes {
		if seen[code] {
			continue
		}
		tpl := templates[code]
		missing = append(missing, domain.Dimension{
			Code:            tpl.Code,
			Name:            tpl.Name,
			ModuleCode:      domain.ModuleEnneagram,
			DataSource:      domain.SourceTest,
			Anchor:          enneAnchor(tpl.Name),
			Weight:          0,
			IncludeOverview: false,
			Enabled:         true,
			IsReference:     true,
			Version:         1,
		})
	}
	if len(missing) > 0 {
		if err := r.runner(tx).WithContext(ctx).CreateInBatches(&missing, 100).Error; err != nil {
			if !dberr.UniqueViolation(err) {
				return nil, fmt.Errorf("ensure dimensions: %w", err)
			}
			// 并发对端已建（SQLite 忽略锁子句时的兜底路径），重查活跃行复用。
			var retry []domain.Dimension
			if err := r.runner(tx).WithContext(ctx).Where("code IN ?", codes).Find(&retry).Error; err != nil {
				return nil, err
			}
			for _, row := range retry {
				ids[row.Code] = row.ID
			}
			return ids, nil
		}
		for _, row := range missing {
			ids[row.Code] = row.ID
		}
	}
	return ids, nil
}

// ImportScale 单事务收口（03 §3.12）：锁 9 型别维度行 → ensure → 事务内判已引入
// （锁定读见最新已提交数据，后到并发引入收 ErrScaleImported）→ 组装 questions
// （status/question_no/batch_id/version 由 CreateBatchWithQuestions 事务内置值）
// → NextBatchNo("S") + 建批同事务提交。
func (r *scaleRepository) ImportScale(ctx context.Context, tpl *scaledata.ScaleTemplate, now time.Time) (*domain.QuestionBatch, error) {
	var batch *domain.QuestionBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		codes := make([]string, len(tpl.Dimensions))
		for i, d := range tpl.Dimensions {
			codes[i] = d.Code
		}
		if _, err := r.LockEnneagramDimensions(ctx, tx, codes); err != nil {
			return err
		}
		dimIDs, err := r.EnsureEnneagramDimensions(ctx, tx, tpl.Dimensions)
		if err != nil {
			return err
		}
		counts, err := r.CountImported(ctx, tx, []string{tpl.ScaleKey})
		if err != nil {
			return err
		}
		if counts[tpl.ScaleKey] > 0 {
			return ErrScaleImported
		}
		questions := make([]domain.Question, len(tpl.Items))
		for i, item := range tpl.Items {
			questions[i] = domain.Question{
				Source:      domain.QuestionSourceScale,
				ScaleKey:    tpl.ScaleKey,
				DimensionID: dimIDs[item.DimensionCode],
				Scenario:    item.Statement,
				Requirement: item.Requirement,
				FocusPoint:  item.FocusPoint,
			}
		}
		// NextBatchNo 走 tx 派生的临时 repo 实例，与外层事务同连接读
		//（:memory: SQLite 连接间不可见，事务外连接连表都看不到）。
		no, err := NewQuestionBatchRepository(tx).NextBatchNo(ctx, "S", now)
		if err != nil {
			return err
		}
		batch = &domain.QuestionBatch{
			BatchNo:       no,
			Title:         tpl.Name,
			Source:        domain.QuestionSourceScale,
			BatchType:     domain.QuestionBatchTypeImport,
			Status:        domain.QuestionBatchStatusPending,
			QuestionCount: len(tpl.Items),
			ScaleKey:      tpl.ScaleKey,
		}
		return r.batchRepo.CreateBatchWithQuestions(ctx, tx, batch, &questions)
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}
