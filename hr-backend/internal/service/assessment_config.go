// assessment_config 评估周期配置域业务层：周期参数读写、人员列表外部降级、乐观锁冲突映射。
//
// 业务规则（specs §4.1.2 + §4.1.4）：
//   - BR1 保存下次跑批生效，乐观锁（version WHERE）防并发覆盖冲突返 1306
//   - BR2 全员(all)与指定人员(specified)互斥：specified 时 members 必填非空，all 时清空
//   - BR3 period/trigger_time/target_mode 枚举与 HH:mm 格式校验，非法返 1400
//   - BR4 乐观锁冲突 message 带刷新后重试提示
//   - BR5 人员列表外部不可达映射 1305
//
// 事务边界：UpdateWithMembers 在 repo 层单事务内完成乐观锁更新 + 人员全量覆盖（T2 已实现），
// service 只组装入参并据 RowsAffected 映射冲突码。
package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
)

// 评估周期配置域枚举与校验常量。
const (
	periodDaily   = "daily"
	periodWeekly  = "weekly"
	periodMonthly = "monthly"

	targetModeAll       = "all"
	targetModeSpecified = "specified"
)

// triggerTimeRe 校验 HH:mm 格式（specs §4.1.2 字段校验）。
var triggerTimeRe = regexp.MustCompile(`^\d{2}:\d{2}$`)

// validPeriod / validTargetMode 用集合语义做枚举校验，避开 map 同时兼顾可读。
func validPeriod(p string) bool {
	switch p {
	case periodDaily, periodWeekly, periodMonthly:
		return true
	}
	return false
}

func validTargetMode(m string) bool {
	switch m {
	case targetModeAll, targetModeSpecified:
		return true
	}
	return false
}

// validTriggerTime 先正则卡 HH:mm，再数值校验小时 00-23、分钟 00-59。
func validTriggerTime(t string) bool {
	if !triggerTimeRe.MatchString(t) {
		return false
	}
	h, err := strconv.Atoi(t[:2])
	if err != nil {
		return false
	}
	m, err := strconv.Atoi(t[3:])
	if err != nil {
		return false
	}
	return h >= 0 && h <= 23 && m >= 0 && m <= 59
}

// StaffDTO 是评估配置指定人员的 service 侧投影（系统无工号约束：仅标识与姓名）。
type StaffDTO struct {
	StaffID   string `json:"staff_id"`
	StaffName string `json:"staff_name"`
}

// AssessmentConfigDTO 是评估周期配置详情响应。
// 雪花 ID（主键 + 外键 service 转运字段）json 一律 string 化规避前端 JS 精度坑。
type AssessmentConfigDTO struct {
	ID               int64      `json:"id,string"`
	Period           string     `json:"period"`
	TriggerTime      string     `json:"trigger_time"`
	TargetMode       string     `json:"target_mode"`
	SpecifiedMembers []StaffDTO `json:"specified_members"`
	Version          int        `json:"version"`
}

// SaveAssessmentResult 是保存响应：仅回 ID 与新 version。
type SaveAssessmentResult struct {
	ID      int64 `json:"id,string"`
	Version int   `json:"version"`
}

// AssessmentConfigService 是评估周期配置域业务接口。
type AssessmentConfigService interface {
	// Get 读取单例配置与（specified 模式下的）指定人员列表，转 DTO 返回。
	Get(ctx context.Context) (*AssessmentConfigDTO, error)
	// Save 校验入参后组装 domain.AssessmentConfig 与 members，调 repo.UpdateWithMembers 单事务收口。
	// 校验失败返 NewError(BadRequest)=1400；乐观锁 affected==0 返 NewError(ConfigVersionConflict)=1306。
	Save(ctx context.Context, period, triggerTime, targetMode string, members []StaffDTO, version int) (*SaveAssessmentResult, error)
	// ListStaffs 代理外部用户体系查询，外部不可达返 NewError(StaffListUnavailable)=1305。
	ListStaffs(ctx context.Context, keyword string, page, pageSize int) (items []StaffDTO, total int64, err error)
}

// userapiClient 是 userapi 客户端的 service 侧抽象（鸭子类型由 integration/userapi.Client 实现）。
// 抽象成接口便于测试注入 fake，且把 service 对具体 *userapi.Client 类型的依赖放开。
//
// ListStaffs 的 secret 形参是集成密钥明文，由 service 层解密后透传给 userapi.Client 作 Bearer 鉴权。
// wire 无法自动做结构化类型匹配，ProvideUserapiClient 在同包内把 *userapi.Client 显式适配为该接口。
type userapiClient interface {
	ListStaffs(ctx context.Context, secret, keyword string, page, pageSize int) ([]userapi.Staff, int64, error)
}

// ProvideUserapiClient 把 *userapi.Client 适配为未导出的 userapiClient 接口，供 wire 在 app 包注入。
// 同包内可访问未导出类型；wire 据此 provider 把 *userapi.Client 绑定到 userapiClient 形参。
func ProvideUserapiClient(c *userapi.Client) userapiClient { return c }

type assessmentConfigService struct {
	repo       repository.AssessmentConfigRepository
	userapi    userapiClient
	secretRepo repository.IntegrationSecretRepository
	encKey     []byte
}

// NewAssessmentConfigService 构造评估周期配置域 service。
// userapi 形参为接口类型，integration/userapi.Client 因方法集匹配而满足（鸭子类型）。
// secretRepo + encKey 用于解密集成密钥明文，透传给 userapi 作 Bearer 鉴权。
func NewAssessmentConfigService(
	repo repository.AssessmentConfigRepository,
	userapi userapiClient,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
) AssessmentConfigService {
	return &assessmentConfigService{repo: repo, userapi: userapi, secretRepo: secretRepo, encKey: encKey}
}

// Get 读取单例配置并组装 DTO。all 模式 SpecifiedMembers 为空切片（非 nil）便于前端稳定序列化。
func (s *assessmentConfigService) Get(ctx context.Context) (*AssessmentConfigDTO, error) {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get assessment config: %w", err)
	}
	dto := &AssessmentConfigDTO{
		ID:          cfg.ID,
		Period:      cfg.Period,
		TriggerTime: cfg.TriggerTime,
		TargetMode:  cfg.TargetMode,
		Version:     cfg.Version,
		// 默认空切片，all 模式或 specified 无成员时返回 [] 而非 null。
		SpecifiedMembers: []StaffDTO{},
	}
	if cfg.TargetMode == targetModeSpecified {
		ms, merr := s.repo.ListMembers(ctx, cfg.ID)
		if merr != nil {
			return nil, fmt.Errorf("list assessment config members: %w", merr)
		}
		dto.SpecifiedMembers = membersToDTO(ms)
	}
	return dto, nil
}

// Save 字段校验 → 组装入参 → 单事务更新（repo 收口）→ 据affected映射。
// target_mode=specified 时把入参 members 转为 domain.AssessmentConfigMember 透传给 repo；
// target_mode=all 时强制传 nil members（BR2 all 清空），即便调用方误传也兜底清空。
func (s *assessmentConfigService) Save(ctx context.Context, period, triggerTime, targetMode string, members []StaffDTO, version int) (*SaveAssessmentResult, error) {
	// 字段校验（specs §4.1.2 + BR3）。任一失败返 1400。
	if !validPeriod(period) {
		return nil, NewError(errcode.BadRequest)
	}
	if !validTargetMode(targetMode) {
		return nil, NewError(errcode.BadRequest)
	}
	if !validTriggerTime(triggerTime) {
		return nil, NewError(errcode.BadRequest)
	}
	// BR2：specified 时 members 必填非空。
	if targetMode == targetModeSpecified && len(members) == 0 {
		return nil, NewError(errcode.BadRequest)
	}
	// BR2 补充：specified 模式下 staff_id 去重，防同一人员重复入库污染人员列表。
	if targetMode == targetModeSpecified {
		seen := make(map[string]bool, len(members))
		for i := range members {
			if members[i].StaffID == "" {
				return nil, NewErrorWithMsg(errcode.BadRequest, "staff_id is empty")
			}
			if seen[members[i].StaffID] {
				return nil, NewErrorWithMsg(errcode.BadRequest, "duplicate staff_id in members")
			}
			seen[members[i].StaffID] = true
		}
	}

	// 组装 domain：Version 用入参（repo 的乐观锁 WHERE 条件），repo 内部 UPDATE 时自增。
	cfg := &domain.AssessmentConfig{
		ID:          0, // ID 由 repo 按 Get 取得并填入下方
		Period:      period,
		TriggerTime: triggerTime,
		TargetMode:  targetMode,
		Version:     version,
	}
	// 单例配置：ID 来自 repo.Get，保证乐观锁 WHERE 命中。
	cur, err := s.repo.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current assessment config: %w", err)
	}
	cfg.ID = cur.ID

	// 组装 members：all 时强制空（repo 内部 Delete 清空），specified 时透传。
	var domainMembers []domain.AssessmentConfigMember
	if targetMode == targetModeSpecified {
		domainMembers = make([]domain.AssessmentConfigMember, 0, len(members))
		for i := range members {
			domainMembers = append(domainMembers, domain.AssessmentConfigMember{
				AssessmentConfigID: cur.ID,
				StaffID:            members[i].StaffID,
				StaffName:          members[i].StaffName,
			})
		}
	}

	affected, err := s.repo.UpdateWithMembers(ctx, cfg, domainMembers)
	if err != nil {
		return nil, fmt.Errorf("update assessment config: %w", err)
	}
	// BR1 + BR4：affected==0 即乐观锁冲突，返 1306（message 由 errcode.Message 提供）。
	if affected == 0 {
		return nil, NewError(errcode.ConfigVersionConflict)
	}

	return &SaveAssessmentResult{
		ID:      cur.ID,
		Version: version + 1, // repo 内部 version 自增，service 据入参推导返回
	}, nil
}

// ListStaffs 代理外部用户体系查询，失败统一映射 1305（BR5），不向调用方暴露底层错误。
// 先解密集成密钥明文，再透传给 userapi.Client 作 Bearer 鉴权。
// 密钥未配置或解密失败也映射 1305，前端据此降级切全员。
func (s *assessmentConfigService) ListStaffs(ctx context.Context, keyword string, page, pageSize int) ([]StaffDTO, int64, error) {
	secret, err := s.resolveSecret(ctx)
	if err != nil {
		return nil, 0, NewError(errcode.StaffListUnavailable)
	}
	staffs, total, err := s.userapi.ListStaffs(ctx, secret, keyword, page, pageSize)
	if err != nil {
		return nil, 0, NewError(errcode.StaffListUnavailable)
	}
	items := make([]StaffDTO, 0, len(staffs))
	for i := range staffs {
		items = append(items, StaffDTO{
			StaffID:   staffs[i].StaffID,
			StaffName: staffs[i].StaffName,
		})
	}
	return items, total, nil
}

// resolveSecret 解密集成密钥明文。未配置或解密失败均返 error，触发 ListStaffs 统一降级 1305
// （人员列表不可达对调用方而言语义统一，不区分 1303/1307）。
func (s *assessmentConfigService) resolveSecret(ctx context.Context) (string, error) {
	secret, err := s.secretRepo.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("get integration secret: %w", err)
	}
	plaintext, derr := decryptSecretCipher(s.encKey, secret.SecretCipher)
	if derr != nil {
		return "", fmt.Errorf("resolve integration secret: %s", derr.Msg)
	}
	return plaintext, nil
}

// membersToDTO 把 domain.AssessmentConfigMember 切片转 StaffDTO 切片。
func membersToDTO(ms []domain.AssessmentConfigMember) []StaffDTO {
	out := make([]StaffDTO, 0, len(ms))
	for i := range ms {
		out = append(out, StaffDTO{
			StaffID:   ms[i].StaffID,
			StaffName: ms[i].StaffName,
		})
	}
	return out
}
