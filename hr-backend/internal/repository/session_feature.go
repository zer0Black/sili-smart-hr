// SessionFeatureRepository 实现，幂等状态机契约见 specs TECH_003 §2.4 能力3。
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/dberr"

	"gorm.io/gorm"
)

// SessionFeatureRepository 是特征档案的数据访问接口（双重接口范式，account 样板）。
type SessionFeatureRepository interface {
	// FindBySessionKey 按 session_key 点查既有行（幂等预检用）。
	// 无行返回 (nil, nil)，调用方据 nil 判定首次写入。
	FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error)
	// Save 幂等落库：已存在 success 行返回 reused=true 不覆盖；failed 允许原地翻转 success；
	// skipped 行为终态（再次写入返回 reused=true）。唯一索引 uk_session_key 兜底并发双写
	//（冲突转重查收敛）。
	Save(ctx context.Context, rec *domain.SessionFeature) (reused bool, err error)
	// ListByPersonAndRange 按人加时间窗取档案（T5 evaluator 取数口径）。
	// start/end 为 Unix 秒，仓储内转 time.Time 走 idx_token_first_turn 组合索引；
	// 返回全部三态行。
	ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error)
}

type sessionFeatureRepository struct {
	db *gorm.DB
}

func NewSessionFeatureRepository(db *gorm.DB) SessionFeatureRepository {
	return &sessionFeatureRepository{db: db}
}

func (r *sessionFeatureRepository) FindBySessionKey(ctx context.Context, sessionKey string) (*domain.SessionFeature, error) {
	return r.findBySessionKey(r.db.WithContext(ctx), sessionKey)
}

// findBySessionKey 无行时返回 (nil, nil) 而非 gorm.ErrRecordNotFound。
func (r *sessionFeatureRepository) findBySessionKey(tx *gorm.DB, sessionKey string) (*domain.SessionFeature, error) {
	var rec domain.SessionFeature
	err := tx.Where("session_key = ?", sessionKey).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Save 幂等状态机：success/skipped 既有行复用，failed 行按新 rec 原地 UPDATE
// （success 翻转补齐三块 / failed 重试刷新 error_code），均保留原 ID。
func (r *sessionFeatureRepository) Save(ctx context.Context, rec *domain.SessionFeature) (bool, error) {
	db := r.db.WithContext(ctx)
	existing, err := r.findBySessionKey(db, rec.SessionKey)
	if err != nil {
		return false, err
	}
	if existing != nil {
		return r.applyExisting(db, rec, existing)
	}
	// CreatedAt/UpdatedAt 显式 UTC：autoUpdateTime 回调填本地时区会让首写行与
	// updateColumns 的 UTC 串在同一列混存两种偏移形态。
	rec.CreatedAt, rec.UpdatedAt = utcNow(), utcNow()

	if err := db.Create(rec).Error; err != nil {
		// 唯一索引兜底并发双写：后写者 insert 冲突转重查一次收敛（04 §3.1）。
		if !dberr.UniqueViolation(err) {
			return false, err
		}
		existing, ferr := r.findBySessionKey(db, rec.SessionKey)
		if ferr != nil {
			return false, ferr
		}
		if existing == nil {
			return false, err
		}
		return r.applyExisting(db, rec, existing)
	}
	return false, nil
}

// utcNow 统一 UTC 时间源：updated_at 与首写 Created/UpdatedAt 同口径，防 SQLite
// 文本列混存本地偏移串与 UTC 串（字典序比较会错序，见 updateColumns 注释）。
func utcNow() time.Time { return time.Now().UTC() }

// applyExisting 对既有行收敛：终态返回 reused=true；failed 行非终态，
// 按新 rec 覆盖（specs §2.4 能力3）。
func (r *sessionFeatureRepository) applyExisting(tx *gorm.DB, rec *domain.SessionFeature, existing *domain.SessionFeature) (bool, error) {
	if isTerminalStatus(existing.Status) {
		return true, nil
	}
	return r.updateRow(tx, rec, existing)
}

// isTerminalStatus 档案终态谓词：success 与 skipped 不再重抽重判。
func isTerminalStatus(status string) bool {
	return status == domain.FeatureStatusSuccess || status == domain.FeatureStatusSkipped
}

// updateRow 按 ID 列级覆盖业务列，保留原 ID；map 形态避免 struct Updates 跳过空串
// 零值（success 翻转时空串语义须写入）。token_name 必须随翻转覆盖：令牌名纠正后
// 重抽，归属不同步会让 ListByPersonAndRange 按新名漏行。WHERE 带 id AND status 双
// 条件：Asynq 双交付下防后写者覆写已终态的行，落空转重查按库内实际行收敛。
func (r *sessionFeatureRepository) updateRow(tx *gorm.DB, rec *domain.SessionFeature, existing *domain.SessionFeature) (bool, error) {
	res := tx.Model(&domain.SessionFeature{}).
		Where("id = ? AND status = ?", existing.ID, existing.Status).
		Updates(updateColumns(rec))
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		// 并发收敛：读后被改写，重查以库内实际行为准（终态行收敛 reused）。
		current, err := r.findBySessionKey(tx, rec.SessionKey)
		if err != nil {
			return false, err
		}
		if current != nil && isTerminalStatus(current.Status) {
			return true, nil
		}
		// 行缺失或仍非终态：本端内容未落库，报错交任务重试，防静默丢档案。
		return false, fmt.Errorf("session feature update converged to non-terminal state (row=%v), session_key=%s", current != nil, rec.SessionKey)
	}
	return false, nil
}

// updateColumns 手写翻转列清单（与 domain.SessionFeature 业务列集对应，守护测试
// TestUpdateColumnsCoverBusinessFields 锁定同步）。updated_at 显式 UTC 与首写同口径
//（SQLite 文本列字典序比较，混存偏移串会错序）；map 形态避免 struct Updates 跳过
// 空串零值（success 翻转时空串语义须写入）。
func updateColumns(rec *domain.SessionFeature) map[string]interface{} {
	return map[string]interface{}{
		"token_name":    rec.TokenName,
		"status":        rec.Status,
		"client":        rec.Client,
		"turn_count":    rec.TurnCount,
		"first_turn_at": rec.FirstTurnAt,
		"last_turn_at":  rec.LastTurnAt,
		"profile_json":  rec.ProfileJSON,
		"error_code":    rec.ErrorCode,
		"updated_at":    utcNow(),
	}
}

// ListByPersonAndRange 区间端点闭区间（>= 与 <=），first_turn_at 升序，返回全部三态行。
// 端点统一 UTC 口径：SQLite 路径时间列按驱动格式串携带本地偏移落库、比较走文本字典序，
// 进程时区变更或 DST 切换会让新旧偏移串与时间序错位致窗口漏行/多行，查询与写入两端
// 统一 UTC 才能保证偏移串一致可比（写入侧 sessionTurnTimes 同口径）。
func (r *sessionFeatureRepository) ListByPersonAndRange(ctx context.Context, tokenName string, start, end int64) ([]domain.SessionFeature, error) {
	var list []domain.SessionFeature
	err := r.db.WithContext(ctx).Where("token_name = ? AND first_turn_at >= ? AND first_turn_at <= ?",
		tokenName, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()).
		Order("first_turn_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}
