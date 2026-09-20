// AssessmentBatchRepository 承载批次域三表的数据访问：批次记录、人员明细与计数推进。
// 批次状态机与终态映射口径见 specs P2_ASM_001 §5.2.4/§6.2 与 04 §3.1/§3.2。
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// errorSummaryMaxLen 失败原因摘要截断长度，对齐列宽 varchar(255)（04 §3.1/§3.2）。
const errorSummaryMaxLen = 255

// BatchFilter 是批次列表的筛选与分页条件，TriggerType/Status 空串跳过条件。
type BatchFilter struct {
	TriggerType    string
	Status         string
	Page, PageSize int
}

// RunningBatch 运行中批次的停滞判定输入（触发时刻 + 触发类型）。
type RunningBatch struct {
	TriggeredAt time.Time
	TriggerType string
}

// AssessmentBatchRepository 是批次域的数据访问接口（双重接口范式，account 样板）。
// 时间区间查询（CountInRange/CountSuccessSideTriggeredBetween）参数须传 UTC 口径：
// SQLite 文本列按 UTC 偏移串落库，字典序比较要求两端偏移串一致。
type AssessmentBatchRepository interface {
	// CreateWithPersons 单事务落批次行与人员明细行（建批原子性，防孤儿批次）。
	// persons 为闭包：批次行 Create 回调填雪花 ID 后再调用取明细，保证关联就绪。
	CreateWithPersons(ctx context.Context, batch *domain.AssessmentBatch, persons func(batchID int64) []domain.AssessmentBatchPerson) error
	// GetByID 按主键点查，无行返 (nil, nil)。
	GetByID(ctx context.Context, id int64) (*domain.AssessmentBatch, error)
	// FindLatestScheduled 取最近触发的定时批次（不限状态，tick 已建批判定消费）：
	// 批次快速落终态后宽限窗内的重复 tick 仍须能查到它防重复建批。无行返 (nil, nil)。
	FindLatestScheduled(ctx context.Context) (*domain.AssessmentBatch, error)
	// ListByFilter 按 triggered_at DESC 倒序分页，返回 (list, total)。
	ListByFilter(ctx context.Context, f BatchFilter) ([]domain.AssessmentBatch, int64, error)
	// UpdateTotalSessions 单事务回填批次会话总数并按人更新 session_count
	// （单条 CASE 批量 UPDATE，名单内无会话者为 0，人员行创建时已落）。
	UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error
	// ExpandTargets all 模式骨架批次展开名单：单事务写名单快照、total_count 与
	// 人员明细行。target_names_json='[]' 作原子守卫，已展开（重试重放）幂等返回。
	ExpandTargets(ctx context.Context, batchID int64, names []string) error
	// AdvancePersonTerminal 单事务推进单人终态并原子自增批次计数（只增不减）。
	// status='pending' 守卫防重入双计；终态判定由调用方经 FinalizeBatch 兜底。
	AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName string, personStatus, errorSummary string, sessionCount int) error
	// FinalizeBatch 落批次终态（WHERE status='running' 守卫，终态不可逆）：
	// affected==0 视为已终态幂等返回 nil。
	FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error
	// DeleteBatch 删除批次行与人员明细行（tick 入队失败回滚本次建批消费）。
	// running 守卫防误删已终态批次；批次行无命中（已删/已终态）幂等返回。
	DeleteBatch(ctx context.Context, batchID int64) error
	// FailWholeBatch 批次级异常整批失败：pending 人员行落 failed、计数按事务内
	// 实况统计（重放不虚报）、批次行带 running 守卫防竞态改写终态，返回实际
	// failed/total 供告警；已终态幂等返回 (0, 0, nil)。
	FailWholeBatch(ctx context.Context, batchID int64, reason string) (failed, total int64, err error)
	// CountInRange 统计 triggered_at ∈ [start, end) 的批次数（本期评测次数）。
	CountInRange(ctx context.Context, start, end time.Time) (int64, error)
	// CountSuccessSideTriggeredBetween 统计 triggered_at ∈ [start, end) 的全部批次中
	// 成功侧终态人员行数（子查询圈定批次，join 归属仓储层）。
	CountSuccessSideTriggeredBetween(ctx context.Context, start, end time.Time) (int64, error)
	// ListRunningBatches 取全部 running 批次的触发时刻与触发类型（统计卡停滞
	// 剔除，逐行判定归 service，口径与列表 Stalled 同源；manual 按固定预算判）。
	ListRunningBatches(ctx context.Context) ([]RunningBatch, error)
	// ListFailedByBatch 取批次内 status='failed' 人员行，按 finished_at ASC 升序
	//（终态落库先后，走 idx_batch_status）。
	ListFailedByBatch(ctx context.Context, batchID int64) ([]domain.AssessmentBatchPerson, error)
}

type assessmentBatchRepository struct {
	db *gorm.DB
}

// NewAssessmentBatchRepository 返回 AssessmentBatchRepository 接口实现。
func NewAssessmentBatchRepository(db *gorm.DB) AssessmentBatchRepository {
	return &assessmentBatchRepository{db: db}
}

// CreateWithPersons 批次行与人员明细行同事务落库：明细失败整体回滚，不留无明细的孤儿批次。
func (r *assessmentBatchRepository) CreateWithPersons(ctx context.Context, batch *domain.AssessmentBatch, persons func(batchID int64) []domain.AssessmentBatchPerson) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(batch).Error; err != nil {
			return err
		}
		rows := persons(batch.ID)
		if len(rows) == 0 {
			return nil
		}
		return tx.CreateInBatches(rows, 100).Error
	})
}

func (r *assessmentBatchRepository) GetByID(ctx context.Context, id int64) (*domain.AssessmentBatch, error) {
	var b domain.AssessmentBatch
	err := r.db.WithContext(ctx).First(&b, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *assessmentBatchRepository) FindLatestScheduled(ctx context.Context) (*domain.AssessmentBatch, error) {
	var b domain.AssessmentBatch
	err := r.db.WithContext(ctx).
		Where("trigger_type = ?", domain.BatchTriggerScheduled).
		Order("triggered_at DESC").
		First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListByFilter page 应 >= 1（由调用方保证）。
func (r *assessmentBatchRepository) ListByFilter(ctx context.Context, f BatchFilter) ([]domain.AssessmentBatch, int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{})
	if f.TriggerType != "" {
		query = query.Where("trigger_type = ?", f.TriggerType)
	}
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []domain.AssessmentBatch
	if err := query.
		Order("triggered_at DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// UpdateTotalSessions 逐人 session_count 用单条 CASE WHEN 批量 UPDATE，规避人数级的
// DB 往返；token_name 在批次内唯一，分支互斥与 map 迭代序无关。
func (r *assessmentBatchRepository) UpdateTotalSessions(ctx context.Context, batchID int64, totalSessions int, personSessions map[string]int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.AssessmentBatch{}).
			Where("id = ?", batchID).
			Update("total_session_count", totalSessions).Error; err != nil {
			return err
		}
		if len(personSessions) == 0 {
			return nil
		}
		names := make([]string, 0, len(personSessions))
		cases := make([]string, 0, len(personSessions))
		args := make([]interface{}, 0, len(personSessions)*2+2)
		for name, count := range personSessions {
			names = append(names, name)
			cases = append(cases, "WHEN ? THEN ?")
			args = append(args, name, count)
		}
		stmt := fmt.Sprintf(
			"UPDATE %s SET session_count = CASE token_name %s ELSE session_count END WHERE batch_id = ? AND token_name IN ?",
			domain.AssessmentBatchPerson{}.TableName(), strings.Join(cases, " "))
		args = append(args, batchID, names)
		return tx.Exec(stmt, args...).Error
	})
}

// ExpandTargets 骨架批次展开：target_names_json='[]' 守卫限定只展开一次
//（重试重放 affected==0 幂等返回），明细行与批次行计数同事务原子落库。
func (r *assessmentBatchRepository) ExpandTargets(ctx context.Context, batchID int64, names []string) error {
	namesJSON, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("marshal names: %w", err)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.AssessmentBatch{}).
			Where("id = ? AND target_names_json = ?", batchID, "[]").
			Updates(map[string]any{
				"target_names_json": string(namesJSON),
				"total_count":       len(names),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // 已展开：重试重放不双插明细
		}
		rows := BuildPersonRows(batchID, names)
		if len(rows) == 0 {
			return errors.New("expand targets: 名单为空")
		}
		return tx.CreateInBatches(rows, 100).Error
	})
}

// BuildPersonRows 组装人员明细 pending 行（幂等键 batch_id+token_name），
// pipeline 建批与仓储展开名单两处同源消费。
func BuildPersonRows(batchID int64, names []string) []domain.AssessmentBatchPerson {
	persons := make([]domain.AssessmentBatchPerson, 0, len(names))
	for _, n := range names {
		persons = append(persons, domain.AssessmentBatchPerson{
			BatchID:   batchID,
			TokenName: n,
			Status:    domain.PersonStatusPending,
		})
	}
	return persons
}

// AdvancePersonTerminal 终态映射（04 §3.2）：成功侧四态累计会话数，failed 只计失败数。
func (r *assessmentBatchRepository) AdvancePersonTerminal(ctx context.Context, batchID int64, tokenName string, personStatus, errorSummary string, sessionCount int) error {
	if personStatus == domain.PersonStatusFailed {
		errorSummary = truncateRunes(errorSummary, errorSummaryMaxLen)
	} else {
		errorSummary = ""
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ? AND token_name = ? AND status = ?", batchID, tokenName, domain.PersonStatusPending).
			Updates(map[string]any{
				"status":        personStatus,
				"error_summary": errorSummary,
				"finished_at":   utcNow(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 重入（batch-run 重试）：该人已终态，跳过计数自增防双计。
			return nil
		}
		updates := map[string]any{"evaluated_count": gorm.Expr("evaluated_count + ?", 1)}
		if personStatus == domain.PersonStatusFailed {
			updates["failed_count"] = gorm.Expr("failed_count + ?", 1)
		} else {
			updates["covered_session_count"] = gorm.Expr("covered_session_count + ?", sessionCount)
		}
		return tx.Model(&domain.AssessmentBatch{}).
			Where("id = ?", batchID).
			Updates(updates).Error
	})
}

func (r *assessmentBatchRepository) FinalizeBatch(ctx context.Context, batchID int64, status string, sessionFailRatio float64) error {
	return r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("id = ? AND status = ?", batchID, domain.BatchStatusRunning).
		Updates(map[string]any{
			"status":             status,
			"session_fail_ratio": sessionFailRatio,
			"finished_at":        utcNow(),
		}).Error
}

// DeleteBatch 先删明细后删批次（同事务），running 守卫防误删已终态批次。
// 守卫前置到批次行：批次已终态时整事务回滚（明细不删），防伪失败路径下
// 只删明细留孤儿批次行（失败明细弹窗恒空、统计失真）。
func (r *assessmentBatchRepository) DeleteBatch(ctx context.Context, batchID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND status = ?", batchID, domain.BatchStatusRunning).
			Delete(&domain.AssessmentBatch{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 已删/已终态：幂等返回，明细也不删（同事务回滚语义等价于直接返回）。
			return nil
		}
		return tx.Where("batch_id = ?", batchID).
			Delete(&domain.AssessmentBatchPerson{}).Error
	})
}

// FailWholeBatch 整批失败：pending 人员行落 failed（已终态者不动），批次计数按
// 同事务实况统计（重放不虚报 100%），批次行带 running 守卫防竞态改写终态，
// 已终态时幂等返回 (0,0,nil)。
func (r *assessmentBatchRepository) FailWholeBatch(ctx context.Context, batchID int64, reason string) (int64, int64, error) {
	reason = truncateRunes(reason, errorSummaryMaxLen)
	var failed, total int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := utcNow()
		if err := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ? AND status = ?", batchID, domain.PersonStatusPending).
			Updates(map[string]any{
				"status":        domain.PersonStatusFailed,
				"error_summary": reason,
				"finished_at":   now,
			}).Error; err != nil {
			return err
		}
		// 实况计数：failed 含本轮前已终态者，防重放把 partial 场景虚报 100%。
		if err := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ?", batchID).
			Count(&total).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.AssessmentBatchPerson{}).
			Where("batch_id = ? AND status = ?", batchID, domain.PersonStatusFailed).
			Count(&failed).Error; err != nil {
			return err
		}
		res := tx.Model(&domain.AssessmentBatch{}).
			Where("id = ? AND status = ?", batchID, domain.BatchStatusRunning).
			Updates(map[string]any{
				"status":          domain.BatchStatusFailed,
				"error_summary":   reason,
				"evaluated_count": total,
				"failed_count":    failed,
				"finished_at":     now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 已终态（终态不可逆）：不虚报计数，交调用方按内存批次决定告警。
			return errBatchAlreadyTerminal
		}
		return nil
	})
	if errors.Is(err, errBatchAlreadyTerminal) {
		return 0, 0, nil
	}
	return failed, total, err
}

// errBatchAlreadyTerminal FailWholeBatch 竞态发现批次已非 running 的内部哨兵。
var errBatchAlreadyTerminal = errors.New("batch already terminal")

func (r *assessmentBatchRepository) CountInRange(ctx context.Context, start, end time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("triggered_at >= ? AND triggered_at < ?", start, end).
		Count(&n).Error
	return n, err
}

func (r *assessmentBatchRepository) CountSuccessSideTriggeredBetween(ctx context.Context, start, end time.Time) (int64, error) {
	var n int64
	sub := r.db.Model(&domain.AssessmentBatch{}).
		Select("id").
		Where("triggered_at >= ? AND triggered_at < ?", start, end)
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatchPerson{}).
		Where("batch_id IN (?) AND status IN ?", sub,
			[]string{domain.PersonStatusSuccess, domain.PersonStatusReused, domain.PersonStatusDegraded, domain.PersonStatusSkipped}).
		Count(&n).Error
	return n, err
}

func (r *assessmentBatchRepository) ListRunningBatches(ctx context.Context) ([]RunningBatch, error) {
	var rows []RunningBatch
	err := r.db.WithContext(ctx).Model(&domain.AssessmentBatch{}).
		Where("status = ?", domain.BatchStatusRunning).
		Select("triggered_at, trigger_type").
		Find(&rows).Error
	return rows, err
}

// truncateRunes 按字符截断至多 max 个 rune，避免多字节字符被腰斩。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

// ListFailedByBatch 取批次内 failed 人员行，按 finished_at ASC（idx_batch_status 命中 batch_id+status）。
func (r *assessmentBatchRepository) ListFailedByBatch(ctx context.Context, batchID int64) ([]domain.AssessmentBatchPerson, error) {
	var list []domain.AssessmentBatchPerson
	err := r.db.WithContext(ctx).
		Where("batch_id = ? AND status = ?", batchID, domain.PersonStatusFailed).
		Order("finished_at ASC").
		Find(&list).Error
	return list, err
}
