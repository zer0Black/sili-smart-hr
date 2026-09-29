// AssessmentTestTaskRepository 承载主动测试域任务与链接两表的数据访问：
// 列表筛选、创建事务、任务号取号、状态机条件更新与逾期扫描。
// 状态机口径见 specs P2_TST_001 §6.1/§6.2，列表与链接状态派生口径见 03 §A1。
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/likeescape"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrTaskNotSubmittable 提交守卫拒绝：任务非 pending/in_progress（已 completed 幂等
// 路径除外）、不存在或已终态，CompleteTask 条件更新 affected=0 时返回。
var ErrTaskNotSubmittable = errors.New("assessment test task not submittable")

// TestTaskFilter 任务列表筛选条件：TestType 必填（tab 即类型），Status/Keyword 空跳过。
type TestTaskFilter struct {
	TestType  string
	Status    string
	Keyword   string
	Page      int
	PageSize  int
}

// TestTaskRow 列表行：任务行 + 当前链接状态（该任务最新 generated_at 链接行的状态）。
type TestTaskRow struct {
	Task       domain.AssessmentTestTask
	LinkStatus string
}

// AssessmentTestTaskRepository 是主动测试域的数据访问接口（双重接口范式）。
// 时间区间查询（ExpirePending）参数须传 UTC 口径：SQLite 文本列按 UTC 偏移串落库，
// 字典序比较要求两端偏移串一致。
type AssessmentTestTaskRepository interface {
	// ListByFilter 按 created_at DESC 倒序分页，keyword 对 staff_name 与 task_no 做
	// EscapeLike 转义后的 OR 匹配，返回行集（含当前链接状态）与 total。
	ListByFilter(ctx context.Context, f TestTaskFilter) ([]TestTaskRow, int64, error)
	// GetByID 按主键点查，无行返 (nil, nil)。
	GetByID(ctx context.Context, id int64) (*domain.AssessmentTestTask, error)
	// CreateWithLink 单事务落任务行、链接行并 IncrementReferenceCounts(question_ids)，
	// 任一失败整体回滚（specs §5.1.4 规则3）。
	CreateWithLink(ctx context.Context, task *domain.AssessmentTestTask, link *domain.AssessmentTestLink) error
	// NextTaskNo 取号：前缀 + yyyyMMdd + 4 位当日序号，同前缀同日期最大序号递增，
	// uk_task_no 兜底并发（specs §5.1.2 步骤2）。
	NextTaskNo(ctx context.Context, prefix string, now time.Time) (string, error)
	// CurrentLink 取任务最新 generated_at 链接行，无行返 (nil, nil)。
	CurrentLink(ctx context.Context, taskID int64) (*domain.AssessmentTestLink, error)
	// ReplaceLink 重发事务：任务行 FOR UPDATE 锁内复核 pending/expired 后作废旧
	// valid 链接并插新行（specs §4.1.4 规则1、§6.2），非可重发态返
	// ErrTaskNotSubmittable。
	ReplaceLink(ctx context.Context, taskID int64, newLink *domain.AssessmentTestLink) error
	// CancelTask 事务：任务条件更新（pending/in_progress/expired 集合）置 canceled +
	// 当前 valid 链接置 invalid。返回 affected，0 映射状态非法（specs §4.1.4 规则2）。
	CancelTask(ctx context.Context, taskID int64) (int64, error)
	// MarkSessionStarted pending→in_progress 条件更新，幂等（specs §6.2）。
	MarkSessionStarted(ctx context.Context, taskID int64) error
	// CountActiveByType 两类未终态任务计数：status ∈ {pending, in_progress} 或
	//（status=completed 且 grading_status ∈ {waiting, grading}），排除 canceled（03 A2 口径）。
	CountActiveByType(ctx context.Context) (aiMgmt, enneagram int64, err error)
	// ExpirePending 扫描 pending 且当前 valid 链接 expires_at < now 的任务批量推进
	// expired + 链接 invalid，条件更新守卫幂等，返回推进条数（specs §5.3.2/§5.3.4）。
	ExpirePending(ctx context.Context, now time.Time) (int64, error)
	// CompleteTask 提交事务三步 + enqueue 同事务，报错整体回滚（specs §5.2.2
	// 步骤1）。completed 幂等返 nil，其余 affected=0 返 ErrTaskNotSubmittable；
	// pending 直达 completed 为 03 §1.6 声明的防御性放行。
	CompleteTask(ctx context.Context, taskID int64, enqueue func(tx *gorm.DB) error, now time.Time) error
	// MarkGradingTerminal grading→scored/degraded 条件更新（specs §6.2 阅卷状态机），
	// WHERE grading_status='grading' 守卫：已终态/在途前幂等返回 nil，不回退已终态。
	MarkGradingTerminal(ctx context.Context, taskID int64, gradingStatus string) error
}

type assessmentTestTaskRepository struct {
	db        *gorm.DB
	questions QuestionRepository
}

// NewAssessmentTestTaskRepository 返回 AssessmentTestTaskRepository 接口实现，
// 内聚 QuestionRepository 供创建事务内引用计数自增复用。
func NewAssessmentTestTaskRepository(db *gorm.DB) AssessmentTestTaskRepository {
	return &assessmentTestTaskRepository{db: db, questions: NewQuestionRepository(db)}
}

// ListByFilter 当前链接状态经二次批量查询组装（task_id IN 当前页 ID 集合），
// 规避逐行 N+1；GORM DISTINCT ON 仅 PG 支持，三库通用故 Go 侧归组取最新行。
func (r *assessmentTestTaskRepository) ListByFilter(ctx context.Context, f TestTaskFilter) ([]TestTaskRow, int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.AssessmentTestTask{}).
		Where("test_type = ?", f.TestType)
	if f.Status != "" {
		query = query.Where("status = ?", f.Status)
	}
	if f.Keyword != "" {
		pat := "%" + likeescape.EscapeLike(f.Keyword) + "%"
		query = query.Where("(staff_name LIKE ? ESCAPE '\\' OR task_no LIKE ? ESCAPE '\\')", pat, pat)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := ClampPage(f.Page, f.PageSize)
	var tasks []domain.AssessmentTestTask
	if err := query.
		Order("created_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]TestTaskRow, len(tasks))
	ids := make([]int64, len(tasks))
	for i, t := range tasks {
		rows[i] = TestTaskRow{Task: t}
		ids[i] = t.ID
	}
	if len(ids) == 0 {
		return rows, total, nil
	}
	var links []domain.AssessmentTestLink
	// 全列取行：当前链接状态仅消费 status，弹窗详情另有 CurrentLink 点查路径。
	if err := r.db.WithContext(ctx).
		Where("task_id IN ?", ids).
		Order("generated_at DESC").
		Find(&links).Error; err != nil {
		return nil, 0, err
	}
	latest := make(map[int64]string, len(ids))
	for _, l := range links {
		if _, ok := latest[l.TaskID]; !ok { // generated_at DESC 首见即最新
			latest[l.TaskID] = l.Status
		}
	}
	for i := range rows {
		rows[i].LinkStatus = latest[rows[i].Task.ID]
	}
	return rows, total, nil
}

func (r *assessmentTestTaskRepository) GetByID(ctx context.Context, id int64) (*domain.AssessmentTestTask, error) {
	var task domain.AssessmentTestTask
	err := r.db.WithContext(ctx).First(&task, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// CreateWithLink 三步同事务（specs §5.1.4 规则3）：任务行 → 链接行（回填 task_id）
// → 题目引用计数 +1（题目 ID 从 question_ids_json 解析，走同一 tx 通道）。
// 首写时间显式 UTC（session_feature 范式）：autoCreate/autoUpdate 回调填本地时区
// 会让同列混存两种偏移串。
func (r *assessmentTestTaskRepository) CreateWithLink(ctx context.Context, task *domain.AssessmentTestTask, link *domain.AssessmentTestLink) error {
	questionIDs, err := parseQuestionIDs(task.QuestionIDsJSON)
	if err != nil {
		return err
	}
	task.CreatedAt, task.UpdatedAt = utcNow(), utcNow()
	link.CreatedAt, link.UpdatedAt = utcNow(), utcNow()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		link.TaskID = task.ID
		if err := tx.Create(link).Error; err != nil {
			return err
		}
		return r.questions.IncrementReferenceCounts(ctx, tx, questionIDs)
	})
}

// parseQuestionIDs 解析任务快照的题目 ID 数组，坏 JSON 显式报错（快照列为
// service 层 json.Marshal 产物，残缺即上游组装缺陷）。
func parseQuestionIDs(s string) ([]int64, error) {
	if s == "" {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil, fmt.Errorf("parse question_ids_json: %w", err)
	}
	return ids, nil
}

// NextTaskNo 同前缀同日期取最大序号递增；无行归 1。序号列定宽 4 位，超 9999
// 退化为 5 位（uk_task_no 仍保唯一）。日期按本地时区日界（防东八区 0-8 点任务号
// 日期与发起时间分属两天）；Unscoped 语义为已建任务全量（canceled 亦占号防复用）。
func (r *assessmentTestTaskRepository) NextTaskNo(ctx context.Context, prefix string, now time.Time) (string, error) {
	dayKey := now.Local().Format("20060102")
	fullPrefix := prefix + dayKey
	var top []string
	if err := r.db.WithContext(ctx).Unscoped().Model(&domain.AssessmentTestTask{}).
		Select("task_no").
		Where("task_no LIKE ? ESCAPE '\\'", likeescape.EscapeLike(fullPrefix)+"%").
		Order("LENGTH(task_no) DESC, task_no DESC").
		Limit(1).
		Pluck("task_no", &top).Error; err != nil {
		return "", err
	}
	next := int64(1)
	if len(top) > 0 {
		if seq, err := strconv.ParseInt(top[0][len(fullPrefix):], 10, 64); err == nil {
			next = seq + 1
		}
	}
	return fmt.Sprintf("%s%04d", fullPrefix, next), nil
}

// CurrentLink 最新 generated_at 行（idx_task_generated 命中），无行返 (nil, nil)。
func (r *assessmentTestTaskRepository) CurrentLink(ctx context.Context, taskID int64) (*domain.AssessmentTestLink, error) {
	var link domain.AssessmentTestLink
	err := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("generated_at DESC").
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// ReplaceLink 三步事务（specs §4.1.4 规则1、§6.2 已逾期→待作答）：事务内先对
// 任务行 FOR UPDATE 加锁串行化并发重发与状态推进（04 §3.2 并发守卫；SQLite
// 忽略锁子句，靠写锁天然串行）→ 锁内复核状态（非 pending/expired 返
// ErrTaskNotSubmittable）→ expired 条件更新回 pending + 旧行 valid 置 invalid +
// 插新行 valid。
func (r *assessmentTestTaskRepository) ReplaceLink(ctx context.Context, taskID int64, newLink *domain.AssessmentTestLink) error {
	newLink.CreatedAt, newLink.UpdatedAt = utcNow(), utcNow()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked domain.AssessmentTestTask
		if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
			Where("id = ?", taskID).
			First(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotSubmittable
			}
			return err
		}
		if locked.Status != domain.TestTaskStatusPending && locked.Status != domain.TestTaskStatusExpired {
			return ErrTaskNotSubmittable
		}
		if err := tx.Model(&domain.AssessmentTestTask{}).
			Where("id = ? AND status = ?", taskID, domain.TestTaskStatusExpired).
			Update("status", domain.TestTaskStatusPending).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.AssessmentTestLink{}).
			Where("task_id = ? AND status = ?", taskID, domain.LinkStatusValid).
			Update("status", domain.LinkStatusInvalid).Error; err != nil {
			return err
		}
		newLink.TaskID = taskID
		return tx.Create(newLink).Error
	})
}

// CancelTask 条件更新守卫：终态 completed/canceled 与非法前置态 affected=0。
// 同事务作废当前 valid 链接（specs §4.1.4 规则2 取消联动）。
func (r *assessmentTestTaskRepository) CancelTask(ctx context.Context, taskID int64) (int64, error) {
	var affected int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.AssessmentTestTask{}).
			Where("id = ? AND status IN ?", taskID, []string{
				domain.TestTaskStatusPending,
				domain.TestTaskStatusInProgress,
				domain.TestTaskStatusExpired,
			}).
			Update("status", domain.TestTaskStatusCanceled)
		if res.Error != nil {
			return res.Error
		}
		affected = res.RowsAffected
		if res.RowsAffected == 0 {
			return nil
		}
		return tx.Model(&domain.AssessmentTestLink{}).
			Where("task_id = ? AND status = ?", taskID, domain.LinkStatusValid).
			Update("status", domain.LinkStatusInvalid).Error
	})
	return affected, err
}

// MarkSessionStarted WHERE status='pending' 条件更新，in_progress 重复上报幂等。
func (r *assessmentTestTaskRepository) MarkSessionStarted(ctx context.Context, taskID int64) error {
	return r.db.WithContext(ctx).Model(&domain.AssessmentTestTask{}).
		Where("id = ? AND status = ?", taskID, domain.TestTaskStatusPending).
		Update("status", domain.TestTaskStatusInProgress).Error
}

// CountActiveByType 03 A2 口径两条计数：未终态（作答侧 + 阅卷在途的已完成）。
// canceled 恒排除（即使阅卷在途，经手动刷新呈现，specs §4.1.3）。
func (r *assessmentTestTaskRepository) CountActiveByType(ctx context.Context) (int64, int64, error) {
	activeWhere := func(testType string) *gorm.DB {
		return r.db.WithContext(ctx).Model(&domain.AssessmentTestTask{}).
			Where("test_type = ? AND status <> ?", testType, domain.TestTaskStatusCanceled).
			Where("(status IN ? OR (status = ? AND grading_status IN ?))",
				[]string{domain.TestTaskStatusPending, domain.TestTaskStatusInProgress},
				domain.TestTaskStatusCompleted,
				[]string{domain.GradingStatusWaiting, domain.GradingStatusGrading},
			)
	}
	var aiMgmt int64
	if err := activeWhere(domain.TestTypeAIMgmt).Count(&aiMgmt).Error; err != nil {
		return 0, 0, err
	}
	var enneagram int64
	err := activeWhere(domain.TestTypeEnneagram).Count(&enneagram).Error
	return aiMgmt, enneagram, err
}

// ExpirePending 扫描 pending 且当前 valid 链接到期的任务，逐任务条件更新守卫推进
//（affected=0 即并发已推进/状态已变，幂等跳过）；tick 只扫 pending，in_progress
// 天然豁免（specs §5.3.4 规则1/3）。候选集经子查询圈定走 idx_status_expires 前缀。
func (r *assessmentTestTaskRepository) ExpirePending(ctx context.Context, now time.Time) (int64, error) {
	sub := r.db.Model(&domain.AssessmentTestLink{}).
		Select("task_id").
		Where("status = ? AND expires_at < ?", domain.LinkStatusValid, now)
	var candidates []int64
	if err := r.db.WithContext(ctx).Model(&domain.AssessmentTestTask{}).
		Where("status = ? AND id IN (?)", domain.TestTaskStatusPending, sub).
		Pluck("id", &candidates).Error; err != nil {
		return 0, err
	}
	var advanced int64
	for _, id := range candidates {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&domain.AssessmentTestTask{}).
				Where("id = ? AND status = ?", id, domain.TestTaskStatusPending).
				Update("status", domain.TestTaskStatusExpired)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil // 已推进/并发竞态：幂等跳过（specs §5.3.4 规则3）
			}
			if err := tx.Model(&domain.AssessmentTestLink{}).
				Where("task_id = ? AND status = ?", id, domain.LinkStatusValid).
				Update("status", domain.LinkStatusInvalid).Error; err != nil {
				return err
			}
			advanced++
			return nil
		})
		if err != nil {
			return advanced, err
		}
	}
	return advanced, nil
}

// CompleteTask 四步同事务（specs §5.2.2 步骤1）：三步推进 + enqueue 在事务闭包
// 内投递。affected=0 时重读任务：completed 幂等返回 nil（不重触 enqueue）。
// map Updates 不触发 autoUpdateTime，updated_at 显式随 now 写入（UTC 口径）。
func (r *assessmentTestTaskRepository) CompleteTask(ctx context.Context, taskID int64, enqueue func(tx *gorm.DB) error, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now = now.UTC()
		res := tx.Model(&domain.AssessmentTestTask{}).
			Where("id = ? AND status IN ?", taskID, []string{
				domain.TestTaskStatusPending,
				domain.TestTaskStatusInProgress,
			}).
			Updates(map[string]any{
				"status":       domain.TestTaskStatusCompleted,
				"completed_at": now,
				"updated_at":   now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var status string
			if err := tx.Model(&domain.AssessmentTestTask{}).Where("id = ?", taskID).
				Pluck("status", &status).Error; err != nil {
				return err
			}
			if status == domain.TestTaskStatusCompleted {
				return nil // 重复提交幂等（specs §5.2.4 规则1 风格）
			}
			return ErrTaskNotSubmittable
		}
		if err := tx.Model(&domain.AssessmentTestLink{}).
			Where("task_id = ? AND status = ?", taskID, domain.LinkStatusValid).
			Updates(map[string]any{
				"status":     domain.LinkStatusUsed,
				"used_at":    now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.AssessmentTestTask{}).
			Where("id = ? AND grading_status = ?", taskID, domain.GradingStatusWaiting).
			Updates(map[string]any{
				"grading_status": domain.GradingStatusGrading,
				"updated_at":     now,
			}).Error; err != nil {
			return err
		}
		return enqueue(tx)
	})
}

// MarkGradingTerminal WHERE grading_status='grading' 条件更新（specs §6.2 阅卷状态机
// 单向：scored/degraded 终态无出边，非 grading 前置态 affected=0 幂等返回 nil，
// 终态不被改写）。
func (r *assessmentTestTaskRepository) MarkGradingTerminal(ctx context.Context, taskID int64, gradingStatus string) error {
	return r.db.WithContext(ctx).Model(&domain.AssessmentTestTask{}).
		Where("id = ? AND grading_status = ?", taskID, domain.GradingStatusGrading).
		Updates(map[string]any{
			"grading_status": gradingStatus,
			"updated_at":     utcNow(),
		}).Error
}
