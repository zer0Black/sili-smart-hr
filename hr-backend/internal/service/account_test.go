package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeRepo 是 repository.AccountRepository 的测试假实现。
// acc/err 共用驱动 FindByUsername/FindByID 的返回值；新方法（Create/Update 等）走独立探针字段。
type fakeRepo struct {
	acc             *domain.Account
	err             error
	lastLoginCalled bool

	// 新增方法配置/探针
	created             bool                   // Create 探针
	createdAcc          *domain.Account        // Create 接收的账号（断言字段）
	updated             bool                   // Update 探针
	updatedAcc          *domain.Account        // Update 接收的账号（断言字段）
	deleted             bool                   // Delete 探针
	deletedID           int64                  // Delete 接收的 id
	updateEnabledCalled bool                   // UpdateEnabled 探针
	updateEnabledID     int64                  // UpdateEnabled 接收的 id
	updateEnabledVal    bool                   // UpdateEnabled 接收的 enabled
	listAccounts        []domain.Account       // ListAccounts 返回的 list
	listTotal           int64                  // ListAccounts 返回的 total
	listErr             error                  // ListAccounts 返回的 err
	// 原子方法配置/探针（service 唯一性守卫与启停守卫走这两个）
	demoteRows            int64 // DemoteEnabledIfNotLast 返回的 RowsAffected（1=放行，0=最后一个启用账号或已禁用）
	demoteCalled          bool  // DemoteEnabledIfNotLast 探针
	demoteID              int64 // DemoteEnabledIfNotLast 接收的 id
	deleteIfNotLastRows   int64 // DeleteIfNotLastEnabled 返回的 RowsAffected
	deleteIfNotLastCalled bool  // DeleteIfNotLastEnabled 探针
	deleteIfNotLastID     int64 // DeleteIfNotLastEnabled 接收的 id
}

func (f *fakeRepo) FindByUsername(_ context.Context, _ string) (*domain.Account, error) {
	return f.acc, f.err
}

func (f *fakeRepo) FindByID(_ context.Context, _ int64) (*domain.Account, error) {
	return f.acc, f.err
}

func (f *fakeRepo) UpdateLastLoginAt(_ context.Context, _ int64, _ time.Time) error {
	f.lastLoginCalled = true
	return nil
}

// Create 接收账号指针，回填模拟 ID（123）供 toDTO 断言，并打 created 探针。
func (f *fakeRepo) Create(_ context.Context, acc *domain.Account) error {
	f.created = true
	acc.ID = 123
	f.createdAcc = acc
	return nil
}

// Update 接收账号指针并打 updated 探针，留存账号以断言字段（如 Enabled 是否保持）。
func (f *fakeRepo) Update(_ context.Context, acc *domain.Account) error {
	f.updated = true
	f.updatedAcc = acc
	return nil
}

// Delete 打探针并留存 id。
func (f *fakeRepo) Delete(_ context.Context, id int64) error {
	f.deleted = true
	f.deletedID = id
	return nil
}

// UpdateEnabled 打探针并留存 id 与 enabled 入参。
func (f *fakeRepo) UpdateEnabled(_ context.Context, id int64, enabled bool) error {
	f.updateEnabledCalled = true
	f.updateEnabledID = id
	f.updateEnabledVal = enabled
	return nil
}

// DemoteEnabledIfNotLast 打探针并返回预设 RowsAffected（rows==0 模拟最后一个启用账号被拦截）。
func (f *fakeRepo) DemoteEnabledIfNotLast(_ context.Context, id int64) (int64, error) {
	f.demoteCalled = true
	f.demoteID = id
	return f.demoteRows, nil
}

// DeleteIfNotLastEnabled 打探针并返回预设 RowsAffected（rows==0 模拟最后一个启用账号被拦截）。
func (f *fakeRepo) DeleteIfNotLastEnabled(_ context.Context, id int64) (int64, error) {
	f.deleteIfNotLastCalled = true
	f.deleteIfNotLastID = id
	return f.deleteIfNotLastRows, nil
}

// ListAccounts 返回预设的 list/total/err。
func (f *fakeRepo) ListAccounts(_ context.Context, _ string, _, _ int) ([]domain.Account, int64, error) {
	return f.listAccounts, f.listTotal, f.listErr
}

var _ repository.AccountRepository = (*fakeRepo)(nil)

// fakeDecryptor 是 PasswordDecryptor 的测试假实现：返回预设明文或预设错误，
// 驱动 Login 的 RSA 解密成功/失败两条分支。
type fakeDecryptor struct {
	pw  string
	err error
}

func (f *fakeDecryptor) Decrypt(_ context.Context, _, _ string) (string, error) {
	return f.pw, f.err
}

var _ service.PasswordDecryptor = (*fakeDecryptor)(nil)

func hash(t *testing.T, pw string) string {
	t.Helper()
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(b)
}

func newSvc(repo repository.AccountRepository, dec service.PasswordDecryptor) service.AccountService {
	return service.NewAccountService(repo, jwt.NewManager("secret", time.Hour), dec)
}

func wantCode(t *testing.T, err error, code int) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("want code %d, got %v", code, err)
	}
}

func TestLoginNotFound(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{pw: "x"}
	_, err := newSvc(repo, dec).Login(context.Background(), "nobody", "cipher", "kid1")
	wantCode(t, err, errcode.InvalidCredentials)
}

func TestLoginDisabled(t *testing.T) {
	// 禁用账号统一返回 invalid credentials（不返回独立禁用码），避免账号枚举。
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", PasswordHash: hash(t, "pw"), Enabled: false}}
	dec := &fakeDecryptor{pw: "pw"}
	_, err := newSvc(repo, dec).Login(context.Background(), "admin", "cipher", "kid1")
	wantCode(t, err, errcode.InvalidCredentials)
}

func TestLoginWrongPassword(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", PasswordHash: hash(t, "correct"), Enabled: true}}
	dec := &fakeDecryptor{pw: "wrong"}
	_, err := newSvc(repo, dec).Login(context.Background(), "admin", "cipher", "kid1")
	wantCode(t, err, errcode.InvalidCredentials)
}

// TestLoginDecryptFail 验证 RSA 解密失败（keyId 过期/密文损坏）走反枚举统一路径，
// 返回 InvalidCredentials(1001)（specs §4.1.4 规则1）。
func TestLoginDecryptFail(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", PasswordHash: hash(t, "correct"), Enabled: true}}
	dec := &fakeDecryptor{err: errors.New("rsakey: key not found or expired")}
	_, err := newSvc(repo, dec).Login(context.Background(), "admin", "cipher", "kid1")
	wantCode(t, err, errcode.InvalidCredentials)
}

func TestLoginSuccess(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 7, Username: "admin", Name: "管理员", PasswordHash: hash(t, "hzwlsoft.com"), Enabled: true}}
	dec := &fakeDecryptor{pw: "hzwlsoft.com"}
	res, err := newSvc(repo, dec).Login(context.Background(), "admin", "cipher", "kid1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.Token == "" {
		t.Fatal("empty token")
	}
	if res.Account.ID != 7 || res.Account.Name != "管理员" {
		t.Fatalf("unexpected account: %+v", res.Account)
	}
	if !res.Account.Enabled {
		t.Fatal("enabled should be true in login response")
	}
	if !repo.lastLoginCalled {
		t.Fatal("last login not updated")
	}
}

func TestGetCurrent(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 9, Username: "admin", Name: "管理员"}}
	dec := &fakeDecryptor{}
	acc, err := newSvc(repo, dec).GetCurrent(context.Background(), 9)
	if err != nil {
		t.Fatalf("get current: %v", err)
	}
	if acc.ID != 9 {
		t.Fatalf("got id %d", acc.ID)
	}
}

// TestCreateAccount_Success 验证正常新增账号：username 校验通过、密码解密成功且强、
// username 不存在 → 写入、返回 dto 含回填 ID 与 enabled，repo Create 被调用。
func TestCreateAccount_Success(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound} // FindByUsername 返回 NotFound
	dec := &fakeDecryptor{pw: "Pass1234"}
	dto, err := newSvc(repo, dec).CreateAccount(context.Background(), "zhangsan", "张三", "c", "kid", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if dto.Username != "zhangsan" || dto.Name != "张三" || !dto.Enabled {
		t.Fatalf("unexpected dto: %+v", dto)
	}
	if dto.ID != 123 {
		t.Fatalf("expected snowflake id 123 (from fake), got %d", dto.ID)
	}
	if !repo.created || repo.createdAcc == nil {
		t.Fatal("repo.Create not called")
	}
	if repo.createdAcc.Username != "zhangsan" || repo.createdAcc.Name != "张三" || !repo.createdAcc.Enabled {
		t.Fatalf("unexpected createdAcc: %+v", repo.createdAcc)
	}
	if repo.createdAcc.PasswordHash == "" {
		t.Fatal("password hash should be set")
	}
}

// TestCreateAccount_UsernameExists 验证 username 已存在（含软删除外的活动账号）返回 1005。
func TestCreateAccount_UsernameExists(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "zhangsan"}}
	dec := &fakeDecryptor{pw: "Pass1234"}
	_, err := newSvc(repo, dec).CreateAccount(context.Background(), "zhangsan", "张三", "c", "kid", true)
	wantCode(t, err, errcode.UsernameExists)
}

// TestCreateAccount_BadUsername 验证 username 不符格式（<3 位）返回 1400。
func TestCreateAccount_BadUsername(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{pw: "Pass1234"}
	_, err := newSvc(repo, dec).CreateAccount(context.Background(), "ab", "张三", "c", "kid", true)
	wantCode(t, err, errcode.BadRequest)
}

// TestCreateAccount_BadPassword 验证密码强度不足（<8 位）返回 1007。
func TestCreateAccount_BadPassword(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{pw: "short"} // <8 位
	_, err := newSvc(repo, dec).CreateAccount(context.Background(), "zhangsan", "张三", "c", "kid", true)
	wantCode(t, err, errcode.PasswordInvalid)
}

// TestCreateAccount_DecryptFail 验证 RSA 解密失败返回 1400（用户管理路径非反枚举范围）。
func TestCreateAccount_DecryptFail(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{err: errors.New("rsakey: key expired")}
	_, err := newSvc(repo, dec).CreateAccount(context.Background(), "zhangsan", "张三", "c", "kid", true)
	wantCode(t, err, errcode.BadRequest)
}

// TestUpdateAccount_NotFound 验证目标账号不存在返回 1004。
func TestUpdateAccount_NotFound(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{}
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "新名", "", "kid", false, true)
	wantCode(t, err, errcode.AccountNotFound)
}

// TestUpdateAccount_DemoteLastEnabled 验证降停最后一个启用账号：原子方法返回 rows==0，返 1006（specs §4.3.4 规则2）。
func TestUpdateAccount_DemoteLastEnabled(t *testing.T) {
	repo := &fakeRepo{
		acc:        &domain.Account{ID: 1, Username: "admin", Name: "管理员", Enabled: true},
		demoteRows: 0, // DemoteEnabledIfNotLast 判定为最后一个启用账号
	}
	dec := &fakeDecryptor{}
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "管理员", "", "kid", false, false)
	wantCode(t, err, errcode.LastEnabledAccount)
	if repo.updated {
		t.Fatal("should not call Update when last enabled guard triggers")
	}
}

// TestUpdateAccount_Success_NoPassword 验证仅改 name/enabled：密码保持原值，repo.Update 被调用。
// acc.Enabled=true 且目标 enabled=true（启用方向，平调），不走降停原子方法。
func TestUpdateAccount_Success_NoPassword(t *testing.T) {
	origHash := hash(t, "OldPass123")
	repo := &fakeRepo{
		acc: &domain.Account{ID: 1, Username: "admin", Name: "旧名", PasswordHash: origHash, Enabled: true},
	}
	dec := &fakeDecryptor{}
	dto, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "新名", "", "kid", false, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if dto.Name != "新名" || !dto.Enabled {
		t.Fatalf("unexpected dto: %+v", dto)
	}
	if !repo.updated || repo.updatedAcc == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedAcc.PasswordHash != origHash {
		t.Fatal("password hash should be unchanged when hasPassword=false")
	}
	if repo.updatedAcc.Name != "新名" || !repo.updatedAcc.Enabled {
		t.Fatalf("unexpected updatedAcc: %+v", repo.updatedAcc)
	}
}

// TestUpdateAccount_Success_WithPassword 验证带密码更新：PasswordHash 被替换。
func TestUpdateAccount_Success_WithPassword(t *testing.T) {
	origHash := hash(t, "OldPass123")
	repo := &fakeRepo{
		acc: &domain.Account{ID: 1, Username: "admin", Name: "管理员", PasswordHash: origHash, Enabled: true},
	}
	dec := &fakeDecryptor{pw: "NewPass123"}
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "管理员", "c", "kid", true, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !repo.updated || repo.updatedAcc == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedAcc.PasswordHash == origHash {
		t.Fatal("password hash should be replaced when hasPassword=true")
	}
}

// TestUpdateAccount_DemoteOK 验证正常降停路径：原子方法返回 rows==1，acc.Enabled 已被切为 false，
// 后续 Save 写回 enabled=false 与之一致（specs §4.3.4 规则2）。
func TestUpdateAccount_DemoteOK(t *testing.T) {
	repo := &fakeRepo{
		acc:        &domain.Account{ID: 1, Username: "admin", Name: "旧名", PasswordHash: "h", Enabled: true},
		demoteRows: 1, // 原子方法放行
	}
	dec := &fakeDecryptor{}
	dto, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "新名", "", "kid", false, false)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if dto.Enabled {
		t.Fatal("dto.Enabled want false after demote")
	}
	if !repo.demoteCalled || repo.demoteID != 1 {
		t.Fatalf("expected DemoteEnabledIfNotLast(1) called, got called=%v id=%d",
			repo.demoteCalled, repo.demoteID)
	}
	if !repo.updated || repo.updatedAcc == nil {
		t.Fatal("repo.Update not called after atomic demote")
	}
	if repo.updatedAcc.Enabled {
		t.Fatal("updatedAcc.Enabled want false (consistent with atomic demote)")
	}
	if repo.updatedAcc.Name != "新名" {
		t.Fatalf("updatedAcc.Name want 新名, got %s", repo.updatedAcc.Name)
	}
}

// TestUpdateAccount_BadName 验证 name > 20 字符（按 rune）返回 1400。
func TestUpdateAccount_BadName(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", Name: "管理员", Enabled: true}}
	dec := &fakeDecryptor{}
	longName := strings.Repeat("一", 21) // 21 个汉字 rune
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, longName, "", "kid", false, true)
	wantCode(t, err, errcode.BadRequest)
}

// TestUpdateAccount_BadPassword 验证 hasPassword 且密码弱（<8 位）返回 1007。
func TestUpdateAccount_BadPassword(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", Name: "管理员", Enabled: true}}
	dec := &fakeDecryptor{pw: "short"}
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "管理员", "c", "kid", true, true)
	wantCode(t, err, errcode.PasswordInvalid)
}

// TestUpdateAccount_DecryptFail 验证 hasPassword 且 RSA 解密失败返回 1400。
func TestUpdateAccount_DecryptFail(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", Name: "管理员", Enabled: true}}
	dec := &fakeDecryptor{err: errors.New("rsakey: key expired")}
	_, err := newSvc(repo, dec).UpdateAccount(context.Background(), 1, "管理员", "c", "kid", true, true)
	wantCode(t, err, errcode.BadRequest)
}

// TestDeleteAccount_NotFound 验证目标账号不存在返回 1004。
func TestDeleteAccount_NotFound(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{}
	err := newSvc(repo, dec).DeleteAccount(context.Background(), 1)
	wantCode(t, err, errcode.AccountNotFound)
}

// TestDeleteAccount_LastEnabled 验证删除最后一个启用账号：原子方法返回 rows==0，返 1006（specs §4.2.4 规则2、§6.2）。
func TestDeleteAccount_LastEnabled(t *testing.T) {
	repo := &fakeRepo{
		acc:                &domain.Account{ID: 1, Username: "admin", Enabled: true},
		deleteIfNotLastRows: 0, // DeleteIfNotLastEnabled 判定为最后一个启用账号
	}
	dec := &fakeDecryptor{}
	err := newSvc(repo, dec).DeleteAccount(context.Background(), 1)
	wantCode(t, err, errcode.LastEnabledAccount)
	if repo.deleted {
		t.Fatal("should not call Delete when last enabled guard triggers")
	}
}

// TestDeleteAccount_DisabledOK 验证删除已禁用账号（不占启用名额）放行，走 DeleteIfNotLastEnabled 原子方法。
func TestDeleteAccount_DisabledOK(t *testing.T) {
	repo := &fakeRepo{
		acc:                &domain.Account{ID: 1, Username: "old", Enabled: false},
		deleteIfNotLastRows: 1, // 原子方法放行
	}
	dec := &fakeDecryptor{}
	if err := newSvc(repo, dec).DeleteAccount(context.Background(), 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !repo.deleteIfNotLastCalled || repo.deleteIfNotLastID != 1 {
		t.Fatalf("expected DeleteIfNotLastEnabled(1), got called=%v id=%d",
			repo.deleteIfNotLastCalled, repo.deleteIfNotLastID)
	}
}

// TestToggleEnabled_NotFound 验证目标账号不存在返回 1004。
func TestToggleEnabled_NotFound(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{}
	err := newSvc(repo, dec).ToggleEnabled(context.Background(), 1, false)
	wantCode(t, err, errcode.AccountNotFound)
}

// TestToggleEnabled_LastEnabled 验证停用最后一个启用账号：原子方法返回 rows==0，返 1006（specs §4.2.4 规则2、§6.2）。
func TestToggleEnabled_LastEnabled(t *testing.T) {
	repo := &fakeRepo{
		acc:        &domain.Account{ID: 1, Username: "admin", Enabled: true},
		demoteRows: 0, // DemoteEnabledIfNotLast 判定为最后一个启用账号
	}
	dec := &fakeDecryptor{}
	err := newSvc(repo, dec).ToggleEnabled(context.Background(), 1, false)
	wantCode(t, err, errcode.LastEnabledAccount)
	if repo.updateEnabledCalled {
		t.Fatal("should not call UpdateEnabled when last enabled guard triggers")
	}
}

// TestToggleEnabled_DisableOK 验证多个启用账号时停用放行，走 DemoteEnabledIfNotLast 原子方法（specs §4.2.4 规则3：启停即时生效）。
func TestToggleEnabled_DisableOK(t *testing.T) {
	repo := &fakeRepo{
		acc:        &domain.Account{ID: 1, Username: "admin", Enabled: true},
		demoteRows: 1, // 原子方法放行
	}
	dec := &fakeDecryptor{}
	if err := newSvc(repo, dec).ToggleEnabled(context.Background(), 1, false); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !repo.demoteCalled || repo.demoteID != 1 {
		t.Fatalf("expected DemoteEnabledIfNotLast(1) called, got called=%v id=%d",
			repo.demoteCalled, repo.demoteID)
	}
}

// TestToggleEnabled_EnableOK 验证启用账号不触发降停判定，走 UpdateEnabled。
func TestToggleEnabled_EnableOK(t *testing.T) {
	repo := &fakeRepo{
		acc: &domain.Account{ID: 1, Username: "old", Enabled: false},
	}
	dec := &fakeDecryptor{}
	if err := newSvc(repo, dec).ToggleEnabled(context.Background(), 1, true); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !repo.updateEnabledCalled || repo.updateEnabledVal != true {
		t.Fatal("expected UpdateEnabled(true) called")
	}
}

// TestResetPassword_Success 验证重置密码成功：bcrypt 写入，Enabled 保持原值不变（specs §4.4.4 规则1）。
func TestResetPassword_Success(t *testing.T) {
	origHash := hash(t, "OldPass123")
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", PasswordHash: origHash, Enabled: true}}
	dec := &fakeDecryptor{pw: "NewPass123"}
	if err := newSvc(repo, dec).ResetPassword(context.Background(), 1, "c", "kid"); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if !repo.updated || repo.updatedAcc == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedAcc.PasswordHash == origHash {
		t.Fatal("password hash should be replaced")
	}
	// 关键断言：重置不改变启用状态（specs §4.4.4 规则1）。
	if !repo.updatedAcc.Enabled {
		t.Fatal("Enabled should remain true after reset")
	}
}

// TestResetPassword_KeepsDisabled 验证停用账号亦可重置，且重置不改变 Enabled（specs §4.4.4 规则1）。
func TestResetPassword_KeepsDisabled(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "old", PasswordHash: hash(t, "OldPass123"), Enabled: false}}
	dec := &fakeDecryptor{pw: "NewPass123"}
	if err := newSvc(repo, dec).ResetPassword(context.Background(), 1, "c", "kid"); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if repo.updatedAcc.Enabled {
		t.Fatal("Enabled should remain false after reset")
	}
}

// TestResetPassword_NotFound 验证目标账号不存在返回 1004。
func TestResetPassword_NotFound(t *testing.T) {
	repo := &fakeRepo{err: gorm.ErrRecordNotFound}
	dec := &fakeDecryptor{pw: "NewPass123"}
	err := newSvc(repo, dec).ResetPassword(context.Background(), 1, "c", "kid")
	wantCode(t, err, errcode.AccountNotFound)
}

// TestResetPassword_BadPassword 验证密码强度不足返回 1007。
func TestResetPassword_BadPassword(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", Enabled: true}}
	dec := &fakeDecryptor{pw: "short"}
	err := newSvc(repo, dec).ResetPassword(context.Background(), 1, "c", "kid")
	wantCode(t, err, errcode.PasswordInvalid)
}

// TestResetPassword_DecryptFail 验证 RSA 解密失败返回 1400。
func TestResetPassword_DecryptFail(t *testing.T) {
	repo := &fakeRepo{acc: &domain.Account{ID: 1, Username: "admin", Enabled: true}}
	dec := &fakeDecryptor{err: errors.New("rsakey: key expired")}
	err := newSvc(repo, dec).ResetPassword(context.Background(), 1, "c", "kid")
	wantCode(t, err, errcode.BadRequest)
}

// TestListAccounts_Success 验证分页列表正确转换为 DTO（含 LastLoginAt）。
func TestListAccounts_Success(t *testing.T) {
	t1 := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	repo := &fakeRepo{
		listAccounts: []domain.Account{
			{ID: 2, Username: "zhangsan", Name: "张三", Enabled: true, LastLoginAt: &t1},
			{ID: 1, Username: "admin", Name: "管理员", Enabled: false, LastLoginAt: &t2},
		},
		listTotal: 25,
	}
	dec := &fakeDecryptor{}
	items, total, err := newSvc(repo, dec).ListAccounts(context.Background(), "张", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	first := items[0]
	if first.ID != 2 || first.Username != "zhangsan" || first.Name != "张三" || !first.Enabled {
		t.Fatalf("unexpected first item: %+v", first)
	}
	if first.LastLoginAt == nil || !first.LastLoginAt.Equal(t1) {
		t.Fatalf("expected last_login_at %v, got %v", t1, first.LastLoginAt)
	}
	if items[1].Enabled {
		t.Fatal("second item should be disabled")
	}
}

// TestListAccounts_Empty 验证空列表正常返回（total=0、items=nil 切片）。
func TestListAccounts_Empty(t *testing.T) {
	repo := &fakeRepo{}
	dec := &fakeDecryptor{}
	items, total, err := newSvc(repo, dec).ListAccounts(context.Background(), "", 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected total 0, got %d", total)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty, got %d", len(items))
	}
}

// TestListAccounts_RepoError 验证 repo 错误透传（wrap）。
func TestListAccounts_RepoError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &fakeRepo{listErr: repoErr}
	dec := &fakeDecryptor{}
	_, _, err := newSvc(repo, dec).ListAccounts(context.Background(), "", 1, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected wrapped repo error, got %v", err)
	}
}
