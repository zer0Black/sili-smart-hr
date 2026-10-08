package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeLLMRepo 是 repository.LLMConfigRepository 的测试假实现。
// 字段挂可配置返回值与探针，覆盖 service 各分支。
type fakeLLMRepo struct {
	byIDCfg *domain.LLMConfig
	byIDErr error

	listCfgs []domain.LLMConfig
	listErr  error

	countN   int64
	countErr error

	createdCfg *domain.LLMConfig // Create 接收的指针
	updatedCfg *domain.LLMConfig // Update 接收的指针
	// updateAffected 控制乐观锁 RowsAffected 返回值；updateAffectedSet 为 true 时按 updateAffected 返回
	// （0 模拟版本冲突），否则默认返回 1 表示成功。
	updateAffectedSet bool
	updateAffected    int64
	updateErr         error

	// CreateExclusiveFirst 探针：模拟事务内 count==0 置 Enabled=true 的行为，
	// 用 createExclusiveCount 控制首条判定（由测试显式配置）。
	createExclusiveCalled bool
	createExclusiveCount  int64
	createExclusiveErr    error

	// Delete 探针保留以兼容接口，service.Delete 已改走 DeleteAndTransferEnable。
	deletedID int64
	deleted   bool

	firstByIDOrderCfg *domain.LLMConfig
	firstByIDOrderErr error

	enableExclusiveCalled bool
	enableExclusiveID     int64
	enableExclusiveErr    error

	// DeleteAndTransferEnable 探针：deleteID/enableID 记录入参，可控返回错误。
	deleteAndTransferCalled   bool
	deleteAndTransferDeleteID int64
	deleteAndTransferEnableID int64
	deleteAndTransferErr      error
}

func (f *fakeLLMRepo) List(_ context.Context, _ string) ([]domain.LLMConfig, error) {
	return f.listCfgs, f.listErr
}
func (f *fakeLLMRepo) FindByID(_ context.Context, _ int64) (*domain.LLMConfig, error) {
	return f.byIDCfg, f.byIDErr
}
func (f *fakeLLMRepo) Create(_ context.Context, cfg *domain.LLMConfig) error {
	f.createdCfg = cfg
	cfg.ID = 777
	return nil
}

// CreateExclusiveFirst 模拟生产事务逻辑：按 createExclusiveCount 判首条，
// count==0 时置 cfg.Enabled=true，再赋固定 ID。service.Create 不再自行置 Enabled。
// 同步写入 createdCfg 探针，让首条启用断言与掩码断言沿用既有路径。
func (f *fakeLLMRepo) CreateExclusiveFirst(_ context.Context, cfg *domain.LLMConfig) error {
	f.createExclusiveCalled = true
	f.createdCfg = cfg
	if f.createExclusiveCount == 0 {
		cfg.Enabled = true
	}
	cfg.ID = 777
	return f.createExclusiveErr
}
func (f *fakeLLMRepo) Update(_ context.Context, cfg *domain.LLMConfig) (int64, error) {
	f.updatedCfg = cfg
	// updateAffectedSet 为 true 时按 updateAffected 返回（含 0 模拟版本冲突），否则默认 1 成功。
	if f.updateAffectedSet {
		return f.updateAffected, f.updateErr
	}
	return 1, f.updateErr
}
func (f *fakeLLMRepo) Delete(_ context.Context, id int64) error {
	f.deleted = true
	f.deletedID = id
	return nil
}
func (f *fakeLLMRepo) Count(_ context.Context) (int64, error) {
	return f.countN, f.countErr
}
func (f *fakeLLMRepo) FindFirstEnabled(_ context.Context) (*domain.LLMConfig, error) {
	return f.byIDCfg, f.byIDErr
}
func (f *fakeLLMRepo) FindFirstByIDOrder(_ context.Context, _ int64) (*domain.LLMConfig, error) {
	return f.firstByIDOrderCfg, f.firstByIDOrderErr
}
func (f *fakeLLMRepo) EnableExclusive(_ context.Context, id int64) error {
	f.enableExclusiveCalled = true
	f.enableExclusiveID = id
	return f.enableExclusiveErr
}
func (f *fakeLLMRepo) DeleteAndTransferEnable(_ context.Context, deleteID int64, enableID int64) error {
	f.deleteAndTransferCalled = true
	f.deleteAndTransferDeleteID = deleteID
	f.deleteAndTransferEnableID = enableID
	return f.deleteAndTransferErr
}

var _ repository.LLMConfigRepository = (*fakeLLMRepo)(nil)

// fakeLLMDecryptor 同 fakeDecryptor，独立挂明文与错误。
type fakeLLMDecryptor struct {
	pw  string
	err error
}

func (f *fakeLLMDecryptor) Decrypt(_ context.Context, _, _ string) (string, error) {
	return f.pw, f.err
}

var _ service.PasswordDecryptor = (*fakeLLMDecryptor)(nil)

// newLLMSvc 用固定 encKey 构造 service，crypto.Encrypt/Decrypt 可逆。
func newLLMSvc(repo repository.LLMConfigRepository, dec service.PasswordDecryptor) service.LLMConfigService {
	return service.NewLLMConfigService(repo, dec, crypto.DeriveKey("test-llm-secret"))
}

func wantLLMCode(t *testing.T, err error, code int) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("want code %d, got %v", code, err)
	}
}

// stringOf 构造长度为 n 的字符串（重复 rune r）。
func stringOf(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

// TestLLMConfig_Create_First 验证首个模型自动启用、掩码正确、Version 初值为 1（specs §4.3.4 规则1、§4.2.4 规则4）。
// Enabled 由 repo.CreateExclusiveFirst 事务内按全表 count==0 决定，service 层不再自行置值。
func TestLLMConfig_Create_First(t *testing.T) {
	repo := &fakeLLMRepo{createExclusiveCount: 0}
	dec := &fakeLLMDecryptor{pw: "sk-deepseek-12345678"}
	res, err := newLLMSvc(repo, dec).Create(context.Background(), "主力模型", "deepseek", "deepseek-chat", "https://api.deepseek.com", "cipher", "kid1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.ID != 777 {
		t.Fatalf("expected id 777, got %d", res.ID)
	}
	if res.Version != 1 {
		t.Fatalf("expected version 1, got %d", res.Version)
	}
	if !repo.createExclusiveCalled {
		t.Fatal("repo.CreateExclusiveFirst not called")
	}
	if repo.createdCfg == nil {
		t.Fatal("createdCfg probe not set")
	}
	if !repo.createdCfg.Enabled {
		t.Fatal("first config should be auto enabled")
	}
	if repo.createdCfg.Version != 1 {
		t.Fatalf("createdCfg.Version want 1, got %d", repo.createdCfg.Version)
	}
	want := crypto.Mask("sk-deepseek-12345678")
	if repo.createdCfg.APIKeyMasked != want {
		t.Fatalf("masked want %s got %s", want, repo.createdCfg.APIKeyMasked)
	}
	if repo.createdCfg.APIKeyCipher == "" {
		t.Fatal("cipher should be set")
	}
}

// TestLLMConfig_Create_Second 验证非首条记录默认停用（createExclusiveCount=1 模拟全表非空）。
func TestLLMConfig_Create_Second(t *testing.T) {
	repo := &fakeLLMRepo{createExclusiveCount: 1}
	dec := &fakeLLMDecryptor{pw: "sk-second-12345678"}
	_, err := newLLMSvc(repo, dec).Create(context.Background(), "备选模型", "openai", "gpt-4o", "", "cipher", "kid1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if repo.createdCfg.Enabled {
		t.Fatal("second config should be disabled")
	}
}

// TestLLMConfig_Create_InvalidParams 覆盖各类参数非法 → 1400。
func TestLLMConfig_Create_InvalidParams(t *testing.T) {
	cases := []struct {
		name                   string
		repo                   *fakeLLMRepo
		dec                    *fakeLLMDecryptor
		nm, pv, mid, apiu, cph string
	}{
		{"bad provider", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "xxx", "m", "", "c"},
		{"empty name", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "", "openai", "m", "", "c"},
		{"empty apiKeyCipher", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "openai", "m", "", ""},
		{"decrypt fail", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{err: errors.New("rsa expired")}, "模型", "openai", "m", "", "c"},
		{"bad api_url", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "openai", "m", "not-a-url", "c"},
		{"api_url file scheme", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "openai", "m", "file:///etc/passwd", "c"},
		{"api_url scheme-less", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "openai", "m", "//attacker.com/x", "c"},
		{"name too long", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, stringOf('一', 51), "openai", "m", "", "c"},
		{"model_id too long", &fakeLLMRepo{countN: 0}, &fakeLLMDecryptor{pw: "sk-12345678"}, "模型", "openai", stringOf('a', 101), "", "c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := newLLMSvc(c.repo, c.dec).Create(context.Background(), c.nm, c.pv, c.mid, c.apiu, c.cph, "kid")
			wantLLMCode(t, err, errcode.BadRequest)
		})
	}
}

// TestLLMConfig_Detail_Success 验证 Detail 返明文 api_key（specs §4.4 临时查看）。
func TestLLMConfig_Detail_Success(t *testing.T) {
	encKey := crypto.DeriveKey("test-llm-secret")
	cipher, err := crypto.Encrypt(encKey, "sk-plaintext-9999")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		ID: 1, Name: "主力", Provider: "deepseek", ModelID: "deepseek-chat",
		APIURL: "https://api.deepseek.com", APIKeyCipher: cipher, Enabled: true,
	}}
	dec := &fakeLLMDecryptor{}
	dto, err := newLLMSvc(repo, dec).Detail(context.Background(), 1)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if dto.APIKey != "sk-plaintext-9999" {
		t.Fatalf("expected plaintext api_key, got %s", dto.APIKey)
	}
	if dto.ID != 1 || dto.Name != "主力" || !dto.Enabled {
		t.Fatalf("unexpected dto: %+v", dto)
	}
}

// TestLLMConfig_Detail_NotFound 验证目标不存在返 1301。
func TestLLMConfig_Detail_NotFound(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: gorm.ErrRecordNotFound}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Detail(context.Background(), 99)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
}

// TestLLMConfig_Update_KeepKey 验证 hasAPIKey=false 保留原 cipher（specs §4.3.4 规则2），
// 并断言乐观锁成功后返回 Version=原值+1。
func TestLLMConfig_Update_KeepKey(t *testing.T) {
	origCipher := "orig-cipher-base64"
	origMasked := "orig****sked"
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		ID: 1, Name: "旧", Provider: "openai", ModelID: "gpt-4",
		APIKeyCipher: origCipher, APIKeyMasked: origMasked, Enabled: false, Version: 3,
	}}
	dec := &fakeLLMDecryptor{}
	res, err := newLLMSvc(repo, dec).Update(context.Background(), 1, 3, "新名", "anthropic", "claude-3", "", "", "kid", false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Version != 4 {
		t.Fatalf("returned version want 4 (3+1), got %d", res.Version)
	}
	if repo.updatedCfg == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedCfg.APIKeyCipher != origCipher {
		t.Fatalf("cipher changed: want %s got %s", origCipher, repo.updatedCfg.APIKeyCipher)
	}
	if repo.updatedCfg.APIKeyMasked != origMasked {
		t.Fatalf("masked changed: want %s got %s", origMasked, repo.updatedCfg.APIKeyMasked)
	}
	if repo.updatedCfg.Name != "新名" || repo.updatedCfg.Provider != "anthropic" || repo.updatedCfg.ModelID != "claude-3" {
		t.Fatalf("unexpected updatedCfg: %+v", repo.updatedCfg)
	}
	// 用客户端回传的 version（3）作 WHERE 条件，而非读回的当前值（version=3，此处一致）。
	if repo.updatedCfg.Version != 3 {
		t.Fatalf("updatedCfg.Version want 3 (客户端回传作 WHERE), got %d", repo.updatedCfg.Version)
	}
}

// TestLLMConfig_Update_WithKey 验证 hasAPIKey=true 重新加密 cipher 改变（specs §4.3.4 规则2）。
func TestLLMConfig_Update_WithKey(t *testing.T) {
	origCipher := "orig-cipher-base64"
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		ID: 1, Name: "旧", Provider: "openai", ModelID: "gpt-4",
		APIKeyCipher: origCipher, APIKeyMasked: "old****mask", Enabled: false,
	}}
	dec := &fakeLLMDecryptor{pw: "sk-newkey-12345678"}
	_, err := newLLMSvc(repo, dec).Update(context.Background(), 1, 0, "新名", "openai", "gpt-4o", "", "cipher", "kid", true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if repo.updatedCfg.APIKeyCipher == origCipher {
		t.Fatal("cipher should be replaced when hasAPIKey=true")
	}
	want := crypto.Mask("sk-newkey-12345678")
	if repo.updatedCfg.APIKeyMasked != want {
		t.Fatalf("masked want %s got %s", want, repo.updatedCfg.APIKeyMasked)
	}
}

// TestLLMConfig_Update_NotFound 验证目标不存在返 1301。
func TestLLMConfig_Update_NotFound(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: gorm.ErrRecordNotFound}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Update(context.Background(), 9, 1, "n", "openai", "m", "", "", "kid", false)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
}

// TestLLMConfig_Update_BadParams 验证 Update 字段校验失败返 1400。
func TestLLMConfig_Update_BadParams(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{ID: 1, Name: "旧", Provider: "openai", ModelID: "m", Enabled: true}}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Update(context.Background(), 1, 1, "n", "badprovider", "m", "", "", "kid", false)
	wantLLMCode(t, err, errcode.BadRequest)
}

// TestLLMConfig_Update_VersionConflict 验证乐观锁 affected==0 映射 1308。
// 模拟 stale-form：DB 当前 version=5，客户端回传 stale version=4（他人已改过），
// repo Update 以 version=4 作 WHERE 不命中 → affected==0 → 1308。
func TestLLMConfig_Update_VersionConflict(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg:           &domain.LLMConfig{ID: 1, Name: "旧", Provider: "openai", ModelID: "gpt-4", Version: 5},
		updateAffectedSet: true,
		updateAffected:    0, // stale version=4 作 WHERE 不命中
	}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Update(context.Background(), 1, 4, "新名", "openai", "gpt-4o", "", "", "kid", false)
	wantLLMCode(t, err, errcode.LLMConfigVersionConflict)
	// 断言 service 用客户端回传的 stale version=4 作 WHERE，而非读回的当前值 5。
	if repo.updatedCfg == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedCfg.Version != 4 {
		t.Fatalf("updatedCfg.Version want 4 (客户端 stale version 作 WHERE), got %d", repo.updatedCfg.Version)
	}
}

// TestLLMConfig_Enable_RaceDeleted 验证 EnableExclusive 目标在 FindByID 后被并发删除
// 返回 ErrRecordNotFound 时映射 1301（而非 1500 通用错误），提示前端刷新。
func TestLLMConfig_Enable_RaceDeleted(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg:            &domain.LLMConfig{ID: 5, Enabled: false},
		enableExclusiveErr: gorm.ErrRecordNotFound,
	}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Enable(context.Background(), 5)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
}

// TestLLMConfig_Delete_RaceDeleted 验证删启用态时目标在 FindByID 后被并发删除，
// DeleteAndTransferEnable 返回 ErrRecordNotFound 映射 1301。
func TestLLMConfig_Delete_RaceDeleted(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg:              &domain.LLMConfig{ID: 1, Enabled: true},
		countN:               2,
		firstByIDOrderCfg:    &domain.LLMConfig{ID: 99},
		deleteAndTransferErr: gorm.ErrRecordNotFound,
	}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 1)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
}

// TestLLMConfig_Delete_Last 验证仅剩一个时拒绝删除返 1302（specs §4.2.4 规则3）。
func TestLLMConfig_Delete_Last(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg: &domain.LLMConfig{ID: 1, Enabled: true},
		countN:  1,
	}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 1)
	wantLLMCode(t, err, errcode.LastLLMConfig)
	if repo.deleteAndTransferCalled {
		t.Fatal("should not call DeleteAndTransferEnable when only one left")
	}
}

// TestLLMConfig_Delete_NotFound 验证删除不存在返 1301。
func TestLLMConfig_Delete_NotFound(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: gorm.ErrRecordNotFound}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 9)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
}

// TestLLMConfig_Delete_TransferEnable 验证删启用态事务内删 + 转启候选（specs §4.2.4 规则3、03 T2 契约）。
func TestLLMConfig_Delete_TransferEnable(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg:           &domain.LLMConfig{ID: 1, Enabled: true},
		countN:            2,
		firstByIDOrderCfg: &domain.LLMConfig{ID: 99},
	}
	res, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 1)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !repo.deleteAndTransferCalled || repo.deleteAndTransferDeleteID != 1 || repo.deleteAndTransferEnableID != 99 {
		t.Fatalf("expected DeleteAndTransferEnable(1, 99), got called=%v deleteID=%d enableID=%d",
			repo.deleteAndTransferCalled, repo.deleteAndTransferDeleteID, repo.deleteAndTransferEnableID)
	}
	if res.TransferredEnabledID == nil || *res.TransferredEnabledID != 99 {
		t.Fatalf("expected transferred_enabled_id=99, got %v", res.TransferredEnabledID)
	}
}

// TestLLMConfig_Delete_TransferEnable_TxError 验证事务失败时 service 返错误不再吞错（03 T2 契约）。
func TestLLMConfig_Delete_TransferEnable_TxError(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg:              &domain.LLMConfig{ID: 1, Enabled: true},
		countN:               2,
		firstByIDOrderCfg:    &domain.LLMConfig{ID: 99},
		deleteAndTransferErr: errors.New("tx boom"),
	}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when DeleteAndTransferEnable fails")
	}
	var se *service.Error
	if errors.As(err, &se) {
		t.Fatalf("expected non-service error for tx failure, got code %d", se.Code)
	}
}

// TestLLMConfig_Delete_NonEnabled 验证删非启用态事务内只删不启（enableID=0，specs §4.2.4 规则3）。
func TestLLMConfig_Delete_NonEnabled(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg: &domain.LLMConfig{ID: 2, Enabled: false},
		countN:  2,
	}
	res, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(context.Background(), 2)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !repo.deleteAndTransferCalled || repo.deleteAndTransferDeleteID != 2 || repo.deleteAndTransferEnableID != 0 {
		t.Fatalf("expected DeleteAndTransferEnable(2, 0), got called=%v deleteID=%d enableID=%d",
			repo.deleteAndTransferCalled, repo.deleteAndTransferDeleteID, repo.deleteAndTransferEnableID)
	}
	if res.TransferredEnabledID != nil {
		t.Fatalf("expected nil transferred_enabled_id, got %v", *res.TransferredEnabledID)
	}
}

// TestLLMConfig_Enable_NotFound 验证启用不存在返 1301。
func TestLLMConfig_Enable_NotFound(t *testing.T) {
	repo := &fakeLLMRepo{byIDErr: gorm.ErrRecordNotFound}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Enable(context.Background(), 9)
	wantLLMCode(t, err, errcode.LLMConfigNotFound)
	if repo.enableExclusiveCalled {
		t.Fatal("should not call EnableExclusive when not found")
	}
}

// TestLLMConfig_Enable_Success 验证启用存在项调 EnableExclusive 并返 {id, enabled:true}（specs §4.2.4 规则1、03 §B5）。
func TestLLMConfig_Enable_Success(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{ID: 5, Enabled: false}}
	res, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Enable(context.Background(), 5)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !repo.enableExclusiveCalled || repo.enableExclusiveID != 5 {
		t.Fatalf("expected EnableExclusive(5), got called=%v id=%d", repo.enableExclusiveCalled, repo.enableExclusiveID)
	}
	if res == nil || res.ID != 5 || !res.Enabled {
		t.Fatalf("expected LLMEnableResult{ID:5, Enabled:true}, got %+v", res)
	}
}

// TestLLMConfig_List 验证列表转 DTO 取掩码不重新计算（specs §4.2.4 规则4）。
func TestLLMConfig_List(t *testing.T) {
	repo := &fakeLLMRepo{listCfgs: []domain.LLMConfig{
		{ID: 1, Name: "主力", Provider: "deepseek", ModelID: "deepseek-chat", APIKeyMasked: "sk-d****5678", Enabled: true},
		{ID: 2, Name: "备选", Provider: "openai", ModelID: "gpt-4o", APIKeyMasked: "sk-o****abcd", Enabled: false},
	}}
	dtos, err := newLLMSvc(repo, &fakeLLMDecryptor{}).List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(dtos) != 2 {
		t.Fatalf("expected 2 items, got %d", len(dtos))
	}
	if dtos[0].APIKeyMasked != "sk-d****5678" || !dtos[0].Enabled {
		t.Fatalf("unexpected first dto: %+v", dtos[0])
	}
	// 零值 time.Time 格式化为 RFC3339 字符串（非空），断言格式化路径已走过。
	if len(dtos[0].CreatedAt) < 10 {
		t.Fatalf("CreatedAt should be RFC3339 formatted, got %q", dtos[0].CreatedAt)
	}
}

// TestLLMConfig_Detail_DecryptFail 验证 Detail 解密失败映射业务码 1307（AES key 轮换/密文损坏，提示重新输入）。
func TestLLMConfig_Detail_DecryptFail(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{ID: 1, APIKeyCipher: "not-valid-base64-cipher"}}
	_, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Detail(context.Background(), 1)
	wantLLMCode(t, err, errcode.SecretDecryptFailed)
}

// findLLMChange 按字段名查 changes 单项，不存在返回 nil。
func findLLMChange(changes []domain.ChangeItem, field string) *domain.ChangeItem {
	for i := range changes {
		if changes[i].Field == field {
			return &changes[i]
		}
	}
	return nil
}

// assertNoLLMSecret 断言日志字段值不含 API Key 明文与密文入参（specs §5.1.4 规则4）。
// 密钥行以掩码进 changes 是契约预期，只断言值不泄漏明文/密文。
func assertNoLLMSecret(t *testing.T, snap service.PendingLog, secretFrag string) {
	t.Helper()
	for _, c := range snap.Changes {
		if secretFrag != "" && (strings.Contains(c.Before, secretFrag) || strings.Contains(c.After, secretFrag)) {
			t.Fatalf("changes 泄漏密钥值: %+v", c)
		}
	}
	if secretFrag != "" && (strings.Contains(snap.Detail, secretFrag) || strings.Contains(snap.Summary, secretFrag) ||
		strings.Contains(snap.Target, secretFrag)) {
		t.Fatalf("日志字段泄漏密钥值: %+v", snap)
	}
}

// TestLLMConfigCreateInjectsDetail 验证新增大模型走 detail 文本形态（specs §4.1.4 规则3：新增类无变更前后语义），
// Changes 恒 nil，Detail 含模型名与供应商。
func TestLLMConfigCreateInjectsDetail(t *testing.T) {
	repo := &fakeLLMRepo{createExclusiveCount: 0}
	dec := &fakeLLMDecryptor{pw: "sk-deepseek-12345678"}
	ctx, sink := withOpSink(context.Background())
	if _, err := newLLMSvc(repo, dec).Create(ctx, "主力模型", "deepseek", "deepseek-chat", "https://api.deepseek.com", "rsa-cipher", "kid1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	snap := sink.Snapshot()
	if snap.Module != domain.OpModuleLLMConfig {
		t.Fatalf("module want llm_config, got %s", snap.Module)
	}
	if snap.Target != "大模型「主力模型」" {
		t.Fatalf("target want 大模型「主力模型」, got %s", snap.Target)
	}
	if snap.Summary != "新增大模型配置 主力模型" {
		t.Fatalf("summary want 新增大模型配置 主力模型, got %s", snap.Summary)
	}
	if snap.Changes != nil {
		t.Fatalf("create changes want nil, got %+v", snap.Changes)
	}
	if !strings.Contains(snap.Detail, "主力模型") {
		t.Fatalf("detail want model name, got %s", snap.Detail)
	}
	assertNoLLMSecret(t, snap, "sk-deepseek-12345678")
}

// TestLLMConfigUpdateInjectsMaskedChanges 核心断言：WithSink ctx 下 Update 改模型 ID，
// Changes 含 {模型ID old new}；换钥时密钥行以掩码变化呈现，明文与密文均不入日志（specs §7.2、§5.1.4 规则4）。
func TestLLMConfigUpdateInjectsMaskedChanges(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		ID: 1, Name: "旧名", Provider: "openai", ModelID: "gpt-4",
		APIURL: "https://old.example.com", APIKeyCipher: "orig-cipher", APIKeyMasked: "sk-o****abcd", Version: 3,
	}}
	dec := &fakeLLMDecryptor{pw: "sk-newkey-12345678"}
	ctx, sink := withOpSink(context.Background())
	if _, err := newLLMSvc(repo, dec).Update(ctx, 1, 3, "旧名", "openai", "gpt-4o", "https://old.example.com", "rsa-cipher", "kid", true); err != nil {
		t.Fatalf("update: %v", err)
	}
	snap := sink.Snapshot()
	if snap.Module != domain.OpModuleLLMConfig || snap.Target != "大模型「旧名」" {
		t.Fatalf("unexpected module/target: %s/%s", snap.Module, snap.Target)
	}
	c := findLLMChange(snap.Changes, "模型 ID")
	if c == nil || c.Before != "gpt-4" || c.After != "gpt-4o" {
		t.Fatalf("changes want {模型 ID gpt-4 gpt-4o}, got %+v", snap.Changes)
	}
	// 名称/供应商/接口地址未变不进 changes。
	if findLLMChange(snap.Changes, "名称") != nil || findLLMChange(snap.Changes, "供应商") != nil ||
		findLLMChange(snap.Changes, "接口地址") != nil {
		t.Fatalf("未变化字段不应进 changes: %+v", snap.Changes)
	}
	// 换钥：密钥行掩码变化 old→new，恒掩码。
	wantNewMasked := crypto.Mask("sk-newkey-12345678")
	kc := findLLMChange(snap.Changes, "API Key")
	if kc == nil || kc.Before != "sk-o****abcd" || kc.After != wantNewMasked {
		t.Fatalf("changes want {API Key 旧掩码 新掩码}, got %+v", snap.Changes)
	}
	assertNoLLMSecret(t, snap, "sk-newkey-12345678")
	assertNoLLMSecret(t, snap, "rsa-cipher")
}

// TestLLMConfigUpdateKeepKeyOmitsKeyRow 验证未换钥（hasAPIKey=false）时密钥行省略，
// 变更只含实际变化字段。
func TestLLMConfigUpdateKeepKeyOmitsKeyRow(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{
		ID: 1, Name: "旧名", Provider: "openai", ModelID: "gpt-4",
		APIKeyCipher: "orig-cipher", APIKeyMasked: "sk-o****abcd", Version: 2,
	}}
	dec := &fakeLLMDecryptor{}
	ctx, sink := withOpSink(context.Background())
	if _, err := newLLMSvc(repo, dec).Update(ctx, 1, 2, "新名", "openai", "gpt-4", "", "", "kid", false); err != nil {
		t.Fatalf("update: %v", err)
	}
	snap := sink.Snapshot()
	if findLLMChange(snap.Changes, "名称") == nil {
		t.Fatalf("名称变化应进 changes: %+v", snap.Changes)
	}
	if findLLMChange(snap.Changes, "API Key") != nil {
		t.Fatalf("未换钥时密钥行应省略: %+v", snap.Changes)
	}
}

// TestLLMConfigDeleteInjectsDetail 验证删除大模型走 detail 文本形态，Changes 恒 nil。
func TestLLMConfigDeleteInjectsDetail(t *testing.T) {
	repo := &fakeLLMRepo{
		byIDCfg: &domain.LLMConfig{ID: 2, Name: "备选", Provider: "openai", ModelID: "gpt-4o", Enabled: false},
		countN:  2,
	}
	ctx, sink := withOpSink(context.Background())
	if _, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Delete(ctx, 2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	snap := sink.Snapshot()
	if snap.Module != domain.OpModuleLLMConfig || snap.Target != "大模型「备选」" {
		t.Fatalf("unexpected module/target: %s/%s", snap.Module, snap.Target)
	}
	if snap.Summary != "删除大模型配置 备选" || snap.Detail != "删除大模型配置 备选" {
		t.Fatalf("summary/detail want 删除大模型配置 备选, got %s/%s", snap.Summary, snap.Detail)
	}
	if snap.Changes != nil {
		t.Fatalf("delete changes want nil, got %+v", snap.Changes)
	}
}

// TestLLMConfigEnableInjectsChanges 验证排他启用走变更对比形态，
// changes 恒 [启用状态 false→true]（specs §7.2）。
func TestLLMConfigEnableInjectsChanges(t *testing.T) {
	repo := &fakeLLMRepo{byIDCfg: &domain.LLMConfig{ID: 5, Name: "主力", Provider: "deepseek", Enabled: false}}
	ctx, sink := withOpSink(context.Background())
	if _, err := newLLMSvc(repo, &fakeLLMDecryptor{}).Enable(ctx, 5); err != nil {
		t.Fatalf("enable: %v", err)
	}
	snap := sink.Snapshot()
	if snap.Module != domain.OpModuleLLMConfig {
		t.Fatalf("module want llm_config, got %s", snap.Module)
	}
	if snap.Target != "大模型「主力」" {
		t.Fatalf("target want 大模型「主力」, got %s", snap.Target)
	}
	if snap.Summary != "启用大模型 主力（排他启用）" {
		t.Fatalf("summary want 启用大模型 主力（排他启用）, got %s", snap.Summary)
	}
	if len(snap.Changes) != 1 {
		t.Fatalf("changes want exactly 1 item, got %+v", snap.Changes)
	}
	c := snap.Changes[0]
	if c.Field != "启用状态" || c.Before != "false" || c.After != "true" {
		t.Fatalf("changes want {启用状态 false true}, got %+v", c)
	}
	if snap.Detail != "" {
		t.Fatalf("injectOperation 不应写 detail, got %s", snap.Detail)
	}
}
