// Package service_test 对 assessment_config 业务层做黑盒单元测试。
//
// fakeAssessmentConfigRepo + fakeUserapiClient 驱动，不依赖真实 DB / 外部 HTTP。
// 覆盖：
//   - Get：repo 返回 weekly/23:00/all + members，DTO 字段映射与 members 切片语义（all 空、specified 含成员）
//   - Save 合法：affected==1，返新 version=old+1，repo 收到 cfg+members
//   - Save 乐观锁冲突：affected==0 → 1306（BR1）
//   - Save 参数非法：period/target_mode/trigger_time 枚举与 HH:mm 校验，specified members 必填非空（BR2/BR3）
//   - Save all 模式：无论入参 members，repo 收到空 members（BR2 all 时清空）
//   - ListStaffs 成功转 DTO；外部不可达返 1305（BR5）
package service_test

import (
	"context"
	"errors"
	"testing"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeAssessmentConfigRepo 是 repository.AssessmentConfigRepository 的测试假实现。
// Get/ListMembers 返回预设；UpdateWithMembers 打探针供断言 cfg 与 members 入参。
type fakeAssessmentConfigRepo struct {
	cfg        *domain.AssessmentConfig
	cfgErr     error
	members    []domain.AssessmentConfigMember
	membersErr error

	// UpdateWithMembers 探针与返回
	updateAffected int64
	updateErr      error
	updateCalled   bool
	updatedCfg     *domain.AssessmentConfig
	updatedMembers []domain.AssessmentConfigMember
}

func (f *fakeAssessmentConfigRepo) Get(_ context.Context) (*domain.AssessmentConfig, error) {
	return f.cfg, f.cfgErr
}

func (f *fakeAssessmentConfigRepo) ListMembers(_ context.Context, _ int64) ([]domain.AssessmentConfigMember, error) {
	return f.members, f.membersErr
}

func (f *fakeAssessmentConfigRepo) ReplaceMembers(_ context.Context, _ int64, _ []domain.AssessmentConfigMember) error {
	return nil
}

func (f *fakeAssessmentConfigRepo) UpdateWithVersion(_ context.Context, _ *domain.AssessmentConfig) (int64, error) {
	return f.updateAffected, f.updateErr
}

// UpdateWithMembers 打探针并返回预设 affected/err。
func (f *fakeAssessmentConfigRepo) UpdateWithMembers(_ context.Context, cfg *domain.AssessmentConfig, members []domain.AssessmentConfigMember) (int64, error) {
	f.updateCalled = true
	f.updatedCfg = cfg
	f.updatedMembers = members
	return f.updateAffected, f.updateErr
}

var _ repository.AssessmentConfigRepository = (*fakeAssessmentConfigRepo)(nil)

// fakeUserapiClient 是 userapi 客户端的测试假实现，鸭子类型满足 service.userapiClient。
type fakeUserapiClient struct {
	staffs []userapi.Staff
	total  int64
	err    error

	called      bool
	lastSecret  string
	lastKW      string
	lastPage    int
	lastPSize   int
}

func (f *fakeUserapiClient) ListStaffs(_ context.Context, secret, keyword string, page, pageSize int) ([]userapi.Staff, int64, error) {
	f.called = true
	f.lastSecret = secret
	f.lastKW = keyword
	f.lastPage = page
	f.lastPSize = pageSize
	return f.staffs, f.total, f.err
}

// newAssessmentSvc 用默认的已配置密钥 fakeRepo 与固定 encKey 构造 service，
// 覆盖 Get/Save 路径（这两路径不碰密钥）。ListStaffs 专用构造见 newAssessmentSvcForStaffs。
func newAssessmentSvc(repo repository.AssessmentConfigRepository, ua *fakeUserapiClient) service.AssessmentConfigService {
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: "any"}}
	return service.NewAssessmentConfigService(repo, ua, secretRepo, crypto.DeriveKey("test-assessment-config"))
}

// TestAssessmentConfig_Get 验证 Get 把 domain 配置映射为 DTO，all 模式 SpecifiedMembers 为空切片（非 nil）。
func TestAssessmentConfig_Get(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 100, Period: "weekly", TriggerTime: "23:00", TargetMode: "all", Version: 3,
		},
		members: []domain.AssessmentConfigMember{},
	}
	dto, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if dto.ID != 100 {
		t.Fatalf("ID want 100, got %d", dto.ID)
	}
	if dto.Period != "weekly" {
		t.Fatalf("Period want weekly, got %s", dto.Period)
	}
	if dto.TriggerTime != "23:00" {
		t.Fatalf("TriggerTime want 23:00, got %s", dto.TriggerTime)
	}
	if dto.TargetMode != "all" {
		t.Fatalf("TargetMode want all, got %s", dto.TargetMode)
	}
	if dto.Version != 3 {
		t.Fatalf("Version want 3, got %d", dto.Version)
	}
	if dto.SpecifiedMembers == nil {
		t.Fatal("SpecifiedMembers should be non-nil empty slice for all mode")
	}
	if len(dto.SpecifiedMembers) != 0 {
		t.Fatalf("SpecifiedMembers len want 0, got %d", len(dto.SpecifiedMembers))
	}
}

// TestAssessmentConfig_Get_WithMembers 验证 specified 模式 Get 返回 members 列表，字段正确映射。
func TestAssessmentConfig_Get_WithMembers(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 200, Period: "monthly", TriggerTime: "09:30", TargetMode: "specified", Version: 5,
		},
		members: []domain.AssessmentConfigMember{
			{StaffID: "usr_1", StaffName: "甲"},
			{StaffID: "usr_2", StaffName: "乙"},
		},
	}
	dto, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if dto.TargetMode != "specified" {
		t.Fatalf("TargetMode want specified, got %s", dto.TargetMode)
	}
	if len(dto.SpecifiedMembers) != 2 {
		t.Fatalf("SpecifiedMembers len want 2, got %d", len(dto.SpecifiedMembers))
	}
	if dto.SpecifiedMembers[0].StaffID != "usr_1" || dto.SpecifiedMembers[0].StaffName != "甲" {
		t.Fatalf("first member: %+v", dto.SpecifiedMembers[0])
	}
	if dto.SpecifiedMembers[1].StaffID != "usr_2" || dto.SpecifiedMembers[1].StaffName != "乙" {
		t.Fatalf("second member: %+v", dto.SpecifiedMembers[1])
	}
}

// TestAssessmentConfig_SaveSuccess 验证合法 specified 保存：affected==1，返新 version=old+1，
// repo 收到含入参字段与新 version 的 cfg、含 2 个成员的 members（BR1 乐观锁成功路径）。
func TestAssessmentConfig_SaveSuccess(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 500, Period: "weekly", TriggerTime: "23:00", TargetMode: "all", Version: 7,
		},
		updateAffected: 1,
	}
	members := []service.StaffDTO{
		{StaffID: "usr_a", StaffName: "甲"},
		{StaffID: "usr_b", StaffName: "乙"},
	}
	res, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "monthly", "03:00", "specified", members, 7,
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if res.ID != 500 {
		t.Fatalf("ID want 500, got %d", res.ID)
	}
	if res.Version != 8 {
		t.Fatalf("Version want 8 (old+1), got %d", res.Version)
	}
	if !repo.updateCalled {
		t.Fatal("UpdateWithMembers not called")
	}
	if repo.updatedCfg == nil {
		t.Fatal("updatedCfg is nil")
	}
	if repo.updatedCfg.ID != 500 {
		t.Fatalf("updatedCfg.ID want 500, got %d", repo.updatedCfg.ID)
	}
	if repo.updatedCfg.Period != "monthly" || repo.updatedCfg.TriggerTime != "03:00" || repo.updatedCfg.TargetMode != "specified" {
		t.Fatalf("updated cfg fields: %+v", repo.updatedCfg)
	}
	// 传入 repo 的 cfg.Version 应是入参 version（乐观锁 WHERE 条件），repo 内部 UPDATE 时自增。
	if repo.updatedCfg.Version != 7 {
		t.Fatalf("updatedCfg.Version want 7 (WHERE 条件), got %d", repo.updatedCfg.Version)
	}
	if len(repo.updatedMembers) != 2 {
		t.Fatalf("updatedMembers len want 2, got %d", len(repo.updatedMembers))
	}
	if repo.updatedMembers[0].StaffID != "usr_a" || repo.updatedMembers[0].StaffName != "甲" {
		t.Fatalf("first updatedMember: %+v", repo.updatedMembers[0])
	}
	if repo.updatedMembers[0].AssessmentConfigID != 500 {
		t.Fatalf("first AssessmentConfigID want 500, got %d", repo.updatedMembers[0].AssessmentConfigID)
	}
}

// TestAssessmentConfig_SaveVersionConflict 验证 affected==0 映射 1306（BR1）。
func TestAssessmentConfig_SaveVersionConflict(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{
			ID: 500, Period: "weekly", TriggerTime: "23:00", TargetMode: "all", Version: 7,
		},
		updateAffected: 0, // 乐观锁冲突
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "monthly", "03:00", "all", nil, 7,
	)
	wantCode(t, err, errcode.ConfigVersionConflict)
}

// TestAssessmentConfig_Save_BadPeriod 验证 period 非法返 1400，且不触发 repo 写入（BR3）。
func TestAssessmentConfig_Save_BadPeriod(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "yearly", "23:00", "all", nil, 1,
	)
	wantCode(t, err, errcode.BadRequest)
	if repo.updateCalled {
		t.Fatal("should not call UpdateWithMembers on validation failure")
	}
}

// TestAssessmentConfig_Save_BadTargetMode 验证 target_mode 非法返 1400（BR3）。
func TestAssessmentConfig_Save_BadTargetMode(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:00", "unknown", nil, 1,
	)
	wantCode(t, err, errcode.BadRequest)
	if repo.updateCalled {
		t.Fatal("should not call UpdateWithMembers on validation failure")
	}
}

// TestAssessmentConfig_Save_BadTriggerTime 验证 trigger_time 小时越界返 1400（BR3）。
func TestAssessmentConfig_Save_BadTriggerTime(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "25:00", "all", nil, 1,
	)
	wantCode(t, err, errcode.BadRequest)
	if repo.updateCalled {
		t.Fatal("should not call UpdateWithMembers on validation failure")
	}
}

// TestAssessmentConfig_Save_TriggerTimeMinuteOverflow 验证分钟越界返 1400（BR3）。
func TestAssessmentConfig_Save_TriggerTimeMinuteOverflow(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:60", "all", nil, 1,
	)
	wantCode(t, err, errcode.BadRequest)
}

// TestAssessmentConfig_Save_TriggerTimeBadFormat 验证非 HH:mm 格式返 1400（BR3）。
func TestAssessmentConfig_Save_TriggerTimeBadFormat(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	cases := []string{"9:00", "2300", "23-00", "", "ab:cd"}
	for _, tc := range cases {
		_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
			context.Background(), "daily", tc, "all", nil, 1,
		)
		wantCode(t, err, errcode.BadRequest)
	}
}

// TestAssessmentConfig_Save_SpecifiedNoMembers 验证 specified 模式 members 空返 1400（BR2）。
func TestAssessmentConfig_Save_SpecifiedNoMembers(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "all"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:00", "specified", nil, 1,
	)
	wantCode(t, err, errcode.BadRequest)
	if repo.updateCalled {
		t.Fatal("should not call UpdateWithMembers on validation failure")
	}
}

// TestAssessmentConfig_Save_AllClearsMembers 验证 target_mode=all 时即便传 members，
// repo 收到的也是空 members（BR2 all 时清空）。
func TestAssessmentConfig_Save_AllClearsMembers(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg:            &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "specified"},
		updateAffected: 1,
	}
	members := []service.StaffDTO{
		{StaffID: "usr_a", StaffName: "甲"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:00", "all", members, 1,
	)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !repo.updateCalled {
		t.Fatal("UpdateWithMembers not called")
	}
	if repo.updatedCfg.TargetMode != "all" {
		t.Fatalf("TargetMode want all, got %s", repo.updatedCfg.TargetMode)
	}
	if len(repo.updatedMembers) != 0 {
		t.Fatalf("all 模式 repo 应收到空 members，got %d", len(repo.updatedMembers))
	}
}

// TestAssessmentConfig_Save_SpecifiedDuplicateStaffID 验证 specified 模式 staff_id 重复返 1400，不触发写入。
func TestAssessmentConfig_Save_SpecifiedDuplicateStaffID(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg:            &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "specified"},
		updateAffected: 1,
	}
	members := []service.StaffDTO{
		{StaffID: "usr_a", StaffName: "甲"},
		{StaffID: "usr_a", StaffName: "甲重复"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:00", "specified", members, 1,
	)
	wantCode(t, err, errcode.BadRequest)
	if repo.updateCalled {
		t.Fatal("should not call UpdateWithMembers on duplicate staff_id")
	}
}

// TestAssessmentConfig_Save_SpecifiedEmptyStaffID 验证 specified 模式 staff_id 空串返 1400。
func TestAssessmentConfig_Save_SpecifiedEmptyStaffID(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{
		cfg: &domain.AssessmentConfig{ID: 1, Version: 1, TargetMode: "specified"},
	}
	members := []service.StaffDTO{
		{StaffID: "", StaffName: "甲"},
	}
	_, err := newAssessmentSvc(repo, &fakeUserapiClient{}).Save(
		context.Background(), "daily", "23:00", "specified", members, 1,
	)
	wantCode(t, err, errcode.BadRequest)
}

// TestAssessmentConfig_ListStaffs_Success 验证 ListStaffs 解密密钥后透传 secret 调 userapi，成功转 DTO。
func TestAssessmentConfig_ListStaffs_Success(t *testing.T) {
	encKey := crypto.DeriveKey("test-assessment-config")
	cipher, err := crypto.Encrypt(encKey, "bearer-plain-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repo := &fakeAssessmentConfigRepo{}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	ua := &fakeUserapiClient{
		staffs: []userapi.Staff{
			{StaffID: "1", StaffName: "甲"},
			{StaffID: "2", StaffName: "乙"},
		},
		total: 2,
	}
	svc := service.NewAssessmentConfigService(repo, ua, secretRepo, encKey)
	items, total, err := svc.ListStaffs(context.Background(), "张", 1, 10)
	if err != nil {
		t.Fatalf("ListStaffs: %v", err)
	}
	if total != 2 {
		t.Fatalf("total want 2, got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("items len want 2, got %d", len(items))
	}
	if items[0].StaffID != "1" || items[0].StaffName != "甲" {
		t.Fatalf("first item: %+v", items[0])
	}
	if !ua.called {
		t.Fatal("userapi not called")
	}
	// 断言解密后的明文密钥透传到 userapi。
	if ua.lastSecret != "bearer-plain-secret" {
		t.Fatalf("userapi secret: got %q want bearer-plain-secret", ua.lastSecret)
	}
	if ua.lastKW != "张" || ua.lastPage != 1 || ua.lastPSize != 10 {
		t.Fatalf("userapi args: kw=%s page=%d size=%d", ua.lastKW, ua.lastPage, ua.lastPSize)
	}
}

// TestAssessmentConfig_ListStaffs_Unavailable 验证外部不可达映射 1305（BR5）。
func TestAssessmentConfig_ListStaffs_Unavailable(t *testing.T) {
	encKey := crypto.DeriveKey("test-assessment-config")
	cipher, _ := crypto.Encrypt(encKey, "bearer-plain-secret")
	repo := &fakeAssessmentConfigRepo{}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	ua := &fakeUserapiClient{err: errors.New("upstream timeout")}
	svc := service.NewAssessmentConfigService(repo, ua, secretRepo, encKey)
	_, _, err := svc.ListStaffs(context.Background(), "", 1, 10)
	wantCode(t, err, errcode.StaffListUnavailable)
}

// TestAssessmentConfig_ListStaffs_Empty 验证空结果正常返回（items 非 nil 空切片）。
func TestAssessmentConfig_ListStaffs_Empty(t *testing.T) {
	encKey := crypto.DeriveKey("test-assessment-config")
	cipher, _ := crypto.Encrypt(encKey, "bearer-plain-secret")
	repo := &fakeAssessmentConfigRepo{}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	ua := &fakeUserapiClient{staffs: []userapi.Staff{}, total: 0}
	svc := service.NewAssessmentConfigService(repo, ua, secretRepo, encKey)
	items, total, err := svc.ListStaffs(context.Background(), "", 1, 10)
	if err != nil {
		t.Fatalf("ListStaffs: %v", err)
	}
	if total != 0 {
		t.Fatalf("total want 0, got %d", total)
	}
	if items == nil {
		t.Fatal("items should be non-nil empty slice")
	}
	if len(items) != 0 {
		t.Fatalf("items len want 0, got %d", len(items))
	}
}

// TestAssessmentConfig_ListStaffs_SecretNotConfigured 验证密钥未配置（cipher 空）映射 1305（BR5）。
func TestAssessmentConfig_ListStaffs_SecretNotConfigured(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: ""}}
	ua := &fakeUserapiClient{}
	svc := service.NewAssessmentConfigService(repo, ua, secretRepo, crypto.DeriveKey("test-assessment-config"))
	_, _, err := svc.ListStaffs(context.Background(), "", 1, 10)
	wantCode(t, err, errcode.StaffListUnavailable)
	if ua.called {
		t.Fatal("userapi should not be called when secret not configured")
	}
}

// TestAssessmentConfig_ListStaffs_SecretDecryptFail 验证密文不可读映射 1305（BR5）。
func TestAssessmentConfig_ListStaffs_SecretDecryptFail(t *testing.T) {
	repo := &fakeAssessmentConfigRepo{}
	secretRepo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: "not-valid-cipher"}}
	ua := &fakeUserapiClient{}
	svc := service.NewAssessmentConfigService(repo, ua, secretRepo, crypto.DeriveKey("test-assessment-config"))
	_, _, err := svc.ListStaffs(context.Background(), "", 1, 10)
	wantCode(t, err, errcode.StaffListUnavailable)
	if ua.called {
		t.Fatal("userapi should not be called when secret decrypt fails")
	}
}
