// Package repository 封装 GORM 数据访问，提供领域对象粒度的增删改查。
package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
)

// ErrBatchNotPending 批次非待审核（已关闭/已作废/不存在）：确认入库与作废的
// 状态机前置拒绝（specs 4.2.4 规则 1、4.1.4 规则 7）。
var ErrBatchNotPending = errors.New("question batch not pending")

// ErrVersionConflict 题目乐观锁版本不符：并发编辑/删除下重新提交冲突。
var ErrVersionConflict = errors.New("question version conflict")

// 题目编号前缀（specs 4.1.2 B），与 question.go 现有编号规则同源。
const (
	questionNoPrefixAI    = "Q-AG-"
	questionNoPrefixScale = "Q-Scale-"
)

// questionNoOf 按 source 推导编号前缀，未知 source 走 AI 段（SP3/SP4 调用方
// 仅传 AI/SCALE 两值，防御性兜底）。
func questionNoOf(source string) string {
	if source == domain.QuestionSourceScale {
		return questionNoPrefixScale
	}
	return questionNoPrefixAI
}

// QuestionBatchRepository 是题库审核批次域的数据访问接口。批次是确认入库与
// 作废的事务边界（specs 4.1.2 C/4.1.3），三写方法 repo 收口事务。
type QuestionBatchRepository interface {
	// ListPending 卡区列表：status=PENDING，created_at DESC，全量不分页。
	ListPending(ctx context.Context) ([]domain.QuestionBatch, error)
	// FindByID 主键查。
	FindByID(ctx context.Context, id int64) (*domain.QuestionBatch, error)
	// FindPendingResubmitBatch 查待并入的重新送审批次：source=AI 且 batch_type=RESUBMIT
	// 且 status=PENDING，created_at 最早一条（并入语义取最早开批的），无则返回 ErrRecordNotFound。
	FindPendingResubmitBatch(ctx context.Context) (*domain.QuestionBatch, error)
	// CreateBatchWithQuestions 建批事务（SP3 量表引入/SP4 生成完成复用）：批量插 questions
	// （编号连续分配收在本方法内）+ 建 question_batches 行 + 批内题目 status 置 PENDING +
	// batch_id 回填。tx 传 nil 自开事务，非 nil 复用调用方外层事务（SP3 行锁事务通道）。
	CreateBatchWithQuestions(ctx context.Context, tx *gorm.DB, batch *domain.QuestionBatch, questions *[]domain.Question) error
	// NextBatchNo 生成批次号：前缀（G/S/R）+ MMdd（time.Local），查同日同前缀最大批次号
	// 推导序号，首批无后缀、同日第 2 批起 -2/-3 递增。UNIQUE 索引兜底并发。
	NextBatchNo(ctx context.Context, prefix string, now time.Time) (string, error)
	// ConfirmBatch 确认入库事务：批次置 CLOSED+closed_at；rejected 集合外批内题目批量置
	// ACTIVE，集合内批量置 REJECTED 并逐题写 reject_reason；返回 admitted/rejected 计数。
	// 题目恒限定批内 PENDING 行；批次非 PENDING 返回 0,0,ErrBatchNotPending。
	ConfirmBatch(ctx context.Context, batchID int64, rejected map[int64]string) (admitted, rejectedCount int64, err error)
	// VoidBatch 作废事务：批次置 VOIDED+voided_at；GENERATE/IMPORT 批内题目批量软删；
	// RESUBMIT 批内题目回退 REJECTED（保留原 reject_reason）。批次非 PENDING 返回 ErrBatchNotPending。
	VoidBatch(ctx context.Context, batchID int64) error
	// ResubmitToBatch 重新提交归批事务：题目更新+status 置 PENDING+version+1；存在待并入
	// RESUBMIT 批则 batch_id 改挂+question_count+1，否则 NextBatchNo("R") 新建 RESUBMIT 批。
	// 题目乐观锁失败（version 不符）返回 ErrVersionConflict。
	ResubmitToBatch(ctx context.Context, question *domain.Question, updates map[string]any) (*domain.QuestionBatch, error)
}

type questionBatchRepository struct {
	db *gorm.DB
}

// NewQuestionBatchRepository 返回 QuestionBatchRepository 接口实现。
func NewQuestionBatchRepository(db *gorm.DB) QuestionBatchRepository {
	return &questionBatchRepository{db: db}
}

func (r *questionBatchRepository) ListPending(ctx context.Context) ([]domain.QuestionBatch, error) {
	var list []domain.QuestionBatch
	if err := r.db.WithContext(ctx).
		Where("status = ?", domain.QuestionBatchStatusPending).
		Order("created_at DESC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *questionBatchRepository) FindByID(ctx context.Context, id int64) (*domain.QuestionBatch, error) {
	var b domain.QuestionBatch
	if err := r.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *questionBatchRepository) FindPendingResubmitBatch(ctx context.Context) (*domain.QuestionBatch, error) {
	var b domain.QuestionBatch
	err := r.db.WithContext(ctx).
		Where("source = ? AND batch_type = ? AND status = ?",
			domain.QuestionSourceAI, domain.QuestionBatchTypeResubmit, domain.QuestionBatchStatusPending).
		Order("created_at ASC").
		First(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// CreateBatchWithQuestions 编号分配收口在事务内：按每题 source 分前缀查
// MaxQuestionSeq（Unscoped 含软删行，编号只增不复用）取段递增，调用方不预填
// question_no。status 强制 PENDING、batch_id 回填批次行雪花 ID（Create 回调
// 先建批次行拿到 ID，再批量插题目）。
func (r *questionBatchRepository) CreateBatchWithQuestions(ctx context.Context, tx *gorm.DB, batch *domain.QuestionBatch, questions *[]domain.Question) error {
	run := func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Create(batch).Error; err != nil {
			return fmt.Errorf("create batch: %w", err)
		}
		if questions == nil || len(*questions) == 0 {
			return nil
		}
		seqs := map[string]int64{}
		for i := range *questions {
			q := &(*questions)[i]
			prefix := questionNoOf(q.Source)
			seq, ok := seqs[prefix]
			if !ok {
				var err error
				seq, err = NewQuestionRepository(tx).MaxQuestionSeq(ctx, prefix)
				if err != nil {
					return fmt.Errorf("max question seq %s: %w", prefix, err)
				}
				seqs[prefix] = seq
			}
			seq++
			seqs[prefix] = seq
			q.QuestionNo = fmt.Sprintf("%s%04d", prefix, seq)
			q.Status = domain.QuestionStatusPending
			q.BatchID = batch.ID
			if q.Version == 0 {
				q.Version = 1
			}
		}
		if err := tx.WithContext(ctx).CreateInBatches(questions, 100).Error; err != nil {
			return fmt.Errorf("create questions: %w", err)
		}
		return nil
	}
	if tx != nil {
		return run(tx)
	}
	return r.db.WithContext(ctx).Transaction(run)
}

// NextBatchNo 序号推导：LIKE 前缀+MMdd 后取长度序最大（-10 长于 -9，字典序与
// 数值序一致），尾缀无数字即首批（序号 1），解析出 n 则下一批 n+1。
func (r *questionBatchRepository) NextBatchNo(ctx context.Context, prefix string, now time.Time) (string, error) {
	stem := "#" + prefix + now.In(time.Local).Format("0102")
	var top []string
	if err := r.db.WithContext(ctx).Model(&domain.QuestionBatch{}).
		Where("batch_no LIKE ? ESCAPE '\\'", likeescape.EscapeLike(stem)+"%").
		Order("LENGTH(batch_no) DESC, batch_no DESC").
		Limit(1).
		Pluck("batch_no", &top).Error; err != nil {
		return "", err
	}
	if len(top) == 0 {
		return stem, nil
	}
	tail := top[0][len(stem):]
	if tail == "" {
		return stem + "-2", nil
	}
	n, err := strconv.ParseInt(tail[1:], 10, 64)
	if err != nil {
		return "", fmt.Errorf("parse batch no tail %q: %w", tail, err)
	}
	return fmt.Sprintf("%s-%d", stem, n+1), nil
}

// ConfirmBatch 确认入库（specs 4.2.3/4.1.4 规则 2、规则 7）：PENDING 前置由
// 批次行 UPDATE 的 WHERE 守卫承载，RowsAffected 0 即非 PENDING（或不存在）。
// 先整批置 ACTIVE 再把 REJECTED 子集二次覆盖并落驳回原因（specs 4.1.4 规则 3：
// 驳回原因仅 REJECTED 非空；ACTIVE 行 reject_reason 清空）。
func (r *questionBatchRepository) ConfirmBatch(ctx context.Context, batchID int64, rejected map[int64]string) (admitted, rejectedCount int64, err error) {
	now := time.Now()
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.QuestionBatch{}).
			Where("id = ? AND status = ?", batchID, domain.QuestionBatchStatusPending).
			Updates(map[string]any{
				"status":     domain.QuestionBatchStatusClosed,
				"closed_at":  now,
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrBatchNotPending
		}
		// REJECTED 子集先逐题落 REJECTED+驳回原因（specs 4.1.4 规则 3），再整批
		// UPDATE 剩余 PENDING 行置 ACTIVE：每行恰一次 version+1，无二次覆盖。
		for id, reason := range rejected {
			rej := tx.Model(&domain.Question{}).
				Where("id = ? AND batch_id = ? AND status = ?", id, batchID, domain.QuestionStatusPending).
				Updates(map[string]any{
					"status":        domain.QuestionStatusRejected,
					"reject_reason": reason,
					"version":       gorm.Expr("version + 1"),
					"updated_at":    now,
				})
			if rej.Error != nil {
				return rej.Error
			}
			rejectedCount += rej.RowsAffected
		}
		act := tx.Model(&domain.Question{}).
			Where("batch_id = ? AND status = ?", batchID, domain.QuestionStatusPending).
			Updates(map[string]any{
				"status":        domain.QuestionStatusActive,
				"reject_reason": "",
				"version":       gorm.Expr("version + 1"),
				"updated_at":    now,
			})
		if act.Error != nil {
			return act.Error
		}
		admitted = act.RowsAffected
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return admitted, rejectedCount, nil
}

// VoidBatch 作废（specs 4.1.3 作废批次）：PENDING 守卫与终态推进同 Confirm。
// GENERATE/IMPORT 批内题目单条 UPDATE 批量软删；RESUBMIT 批内题目回退 REJECTED，
// 列集不含 reject_reason，原驳回原因保留不覆盖。
func (r *questionBatchRepository) VoidBatch(ctx context.Context, batchID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var b domain.QuestionBatch
		if err := tx.Where("id = ? AND status = ?", batchID, domain.QuestionBatchStatusPending).
			First(&b).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBatchNotPending
			}
			return err
		}
		now := time.Now()
		res := tx.Model(&domain.QuestionBatch{}).
			Where("id = ? AND status = ?", batchID, domain.QuestionBatchStatusPending).
			Updates(map[string]any{
				"status":     domain.QuestionBatchStatusVoided,
				"voided_at":  now,
				"updated_at": now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrBatchNotPending
		}
		if b.BatchType == domain.QuestionBatchTypeResubmit {
			return tx.Model(&domain.Question{}).
				Where("batch_id = ? AND deleted_at IS NULL", batchID).
				Updates(map[string]any{
					"status":     domain.QuestionStatusRejected,
					"version":    gorm.Expr("version + 1"),
					"updated_at": now,
				}).Error
		}
		// GORM 模型层带 DeletedAt 软删作用域，显式 Unscoped + 手写 deleted_at
		// 更新绕开作用域加列（deleted_at 无 autoUpdate 时间戳）。
		return tx.Unscoped().Model(&domain.Question{}).
			Where("batch_id = ? AND deleted_at IS NULL", batchID).
			Update("deleted_at", now).Error
	})
}

// ResubmitToBatch 重新提交归批（specs 4.1.4 规则 6）：乐观锁 WHERE version 守卫，
// RowsAffected 0 即冲突整批回滚。并入判定与新批创建同事务，批次号唯一索引
// 兜底并发。updates 由调用方组装文本/维度列，本方法补挂批次三列。
func (r *questionBatchRepository) ResubmitToBatch(ctx context.Context, question *domain.Question, updates map[string]any) (*domain.QuestionBatch, error) {
	var batch *domain.QuestionBatch
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		target, err := NewQuestionBatchRepository(tx).FindPendingResubmitBatch(ctx)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			no, err := NewQuestionBatchRepository(tx).NextBatchNo(ctx, "R", time.Now())
			if err != nil {
				return err
			}
			target = &domain.QuestionBatch{
				BatchNo:       no,
				Title:         "重新送审",
				Source:        domain.QuestionSourceAI,
				BatchType:     domain.QuestionBatchTypeResubmit,
				Status:        domain.QuestionBatchStatusPending,
				QuestionCount: 1,
			}
			if err := tx.Create(target).Error; err != nil {
				return fmt.Errorf("create resubmit batch: %w", err)
			}
		} else {
			res := tx.Model(&domain.QuestionBatch{}).
				Where("id = ?", target.ID).
				Updates(map[string]any{
					"question_count": gorm.Expr("question_count + 1"),
					"updated_at":     time.Now(),
				})
			if res.Error != nil {
				return res.Error
			}
			target.QuestionCount++
		}
		cols := make(map[string]any, len(updates)+3)
		for k, v := range updates {
			cols[k] = v
		}
		cols["status"] = domain.QuestionStatusPending
		cols["batch_id"] = target.ID
		cols["version"] = gorm.Expr("version + 1")
		res := tx.Model(&domain.Question{}).
			Where("id = ? AND version = ? AND deleted_at IS NULL", question.ID, question.Version).
			Updates(cols)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrVersionConflict
		}
		question.Status = domain.QuestionStatusPending
		question.BatchID = target.ID
		question.Version++
		batch = target
		return nil
	})
	if err != nil {
		return nil, err
	}
	return batch, nil
}
