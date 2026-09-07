package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// fakeSecretRepo 是 repository.IntegrationSecretRepository 的测试假实现。
type fakeSecretRepo struct {
	getSecret *domain.IntegrationSecret
	getErr    error

	updatedSecret *domain.IntegrationSecret // Update 接收的指针探针
	affectedRows  int64                     // Update 返回的 RowsAffected，默认 0；测试显式置 1 模拟成功
	updateErr     error
}

func (f *fakeSecretRepo) Get(_ context.Context) (*domain.IntegrationSecret, error) {
	return f.getSecret, f.getErr
}
func (f *fakeSecretRepo) Update(_ context.Context, secret *domain.IntegrationSecret) (int64, error) {
	f.updatedSecret = secret
	return f.affectedRows, f.updateErr
}

var _ repository.IntegrationSecretRepository = (*fakeSecretRepo)(nil)

// fakeSecretDecryptor 同 RSA 解密假实现，独立挂明文与错误。
type fakeSecretDecryptor struct {
	pw  string
	err error
}

func (f *fakeSecretDecryptor) Decrypt(_ context.Context, _, _ string) (string, error) {
	return f.pw, f.err
}

var _ service.PasswordDecryptor = (*fakeSecretDecryptor)(nil)

// fakeConvLog 是 ConversationlogPinger 的假实现，可控 Ping 返回。
type fakeConvLog struct {
	pingErr error
}

func (f *fakeConvLog) Ping(_ context.Context, _ string) error {
	return f.pingErr
}

var _ service.ConversationlogPinger = (*fakeConvLog)(nil)

// newSecretSvc 用固定 encKey 构造 service，crypto.Encrypt/Decrypt 可逆。
func newSecretSvc(repo repository.IntegrationSecretRepository, dec service.PasswordDecryptor, convLog service.ConversationlogPinger) service.IntegrationSecretService {
	return service.NewIntegrationSecretService(repo, dec, crypto.DeriveKey("test-integration-secret"), convLog)
}

func wantSecretCode(t *testing.T, err error, code int) {
	t.Helper()
	var se *service.Error
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("want code %d, got %v", code, err)
	}
}

// TestIntegrationSecret_Get_NotConfigured 验证空 cipher 时 Configured==false、SecretMasked 为空（BR1）。
func TestIntegrationSecret_Get_NotConfigured(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: "", SecretMasked: ""}}
	dto, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if dto.Configured {
		t.Fatal("expected Configured=false when cipher empty")
	}
	if dto.SecretMasked != "" {
		t.Fatalf("expected empty masked, got %s", dto.SecretMasked)
	}
	if dto.ID != 1 {
		t.Fatalf("expected id 1, got %d", dto.ID)
	}
}

// TestIntegrationSecret_Get_Configured 验证非空 cipher 时 Configured==true（BR1）。
func TestIntegrationSecret_Get_Configured(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{
		ID: 2, SecretCipher: "some-cipher", SecretMasked: "abcd****wxyz",
	}}
	dto, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !dto.Configured {
		t.Fatal("expected Configured=true when cipher non-empty")
	}
	if dto.SecretMasked != "abcd****wxyz" {
		t.Fatalf("expected masked snapshot, got %s", dto.SecretMasked)
	}
}

// TestIntegrationSecret_Get_RepoError 验证 repo 错误 wrap 透出（非业务码）。
func TestIntegrationSecret_Get_RepoError(t *testing.T) {
	repo := &fakeSecretRepo{getErr: errors.New("db down")}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Get(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var se *service.Error
	if errors.As(err, &se) {
		t.Fatalf("expected non-service error for repo failure, got code %d", se.Code)
	}
}

// TestIntegrationSecret_Detail_NotConfigured 验证空 cipher 前置拦截返 1303（BR3）。
func TestIntegrationSecret_Detail_NotConfigured(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: ""}}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Detail(context.Background())
	wantSecretCode(t, err, errcode.IntegrationSecretNotConfigured)
}

// TestIntegrationSecret_Detail_Success 验证非空 cipher 解密返明文（specs §4.4）。
func TestIntegrationSecret_Detail_Success(t *testing.T) {
	encKey := crypto.DeriveKey("test-integration-secret")
	cipher, err := crypto.Encrypt(encKey, "bearer-secret-12345678")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	dto, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Detail(context.Background())
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if dto.Secret != "bearer-secret-12345678" {
		t.Fatalf("expected plaintext secret, got %s", dto.Secret)
	}
	if dto.ID != 1 {
		t.Fatalf("expected id 1, got %d", dto.ID)
	}
}

// TestIntegrationSecret_Detail_DecryptFail 验证解密失败映射业务码 1307（AES key 轮换/密文损坏，提示重新输入）。
func TestIntegrationSecret_Detail_DecryptFail(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: "not-valid-cipher"}}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Detail(context.Background())
	wantSecretCode(t, err, errcode.SecretDecryptFailed)
}

// TestIntegrationSecret_Update_Success 验证合法密文：repo.Update 收到的 SecretCipher==crypto.Encrypt(明文)、
// SecretMasked==crypto.Mask(明文)，返 Configured==true 且 Version 自增（BR2/BR5/BR6）。
func TestIntegrationSecret_Update_Success(t *testing.T) {
	encKey := crypto.DeriveKey("test-integration-secret")
	repo := &fakeSecretRepo{
		getSecret:    &domain.IntegrationSecret{ID: 5, SecretCipher: "old-cipher", SecretMasked: "old****mask", Version: 3},
		affectedRows: 1,
	}
	dec := &fakeSecretDecryptor{pw: "new-secret-12345678"}
	res, err := newSecretSvc(repo, dec, &fakeConvLog{}).Update(context.Background(), 3, "rsa-cipher", "kid1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.Configured {
		t.Fatal("expected Configured=true after update")
	}
	if res.ID != 5 {
		t.Fatalf("expected id 5, got %d", res.ID)
	}
	if res.Version != 4 {
		t.Fatalf("expected Version 4 (old 3 + 1), got %d", res.Version)
	}
	if repo.updatedSecret == nil {
		t.Fatal("repo.Update not called")
	}
	// 用客户端回传的 version（3）作 WHERE 条件。
	if repo.updatedSecret.Version != 3 {
		t.Fatalf("expected updated secret keep version 3 as where cond, got %d", repo.updatedSecret.Version)
	}
	// Encrypt 每次 nonce 随机，比对解密后的明文是否一致更稳。
	gotPlain, derr := crypto.Decrypt(encKey, repo.updatedSecret.SecretCipher)
	if derr != nil {
		t.Fatalf("updated cipher decrypt fail: %v", derr)
	}
	if gotPlain != "new-secret-12345678" {
		t.Fatalf("cipher mismatch: decrypted %s", gotPlain)
	}
	wantMasked := crypto.Mask("new-secret-12345678")
	if repo.updatedSecret.SecretMasked != wantMasked {
		t.Fatalf("masked want %s got %s", wantMasked, repo.updatedSecret.SecretMasked)
	}
}

// TestIntegrationSecret_Update_VersionConflict 验证 affected==0（并发覆盖）映射 1309（BR7 乐观锁）。
// 模拟 stale-form：DB 当前 version=2，客户端回传 stale version=1（他人已改过），
// repo Update 以 version=1 作 WHERE 不命中 → affected==0 → 1309。
func TestIntegrationSecret_Update_VersionConflict(t *testing.T) {
	repo := &fakeSecretRepo{
		getSecret:    &domain.IntegrationSecret{ID: 7, SecretCipher: "old", SecretMasked: "o****d", Version: 2},
		affectedRows: 0, // stale version=1 作 WHERE 不命中
	}
	dec := &fakeSecretDecryptor{pw: "another-secret-1234"}
	_, err := newSecretSvc(repo, dec, &fakeConvLog{}).Update(context.Background(), 1, "rsa-cipher", "kid1")
	wantSecretCode(t, err, errcode.IntegrationSecretVersionConflict)
	// 断言 service 用客户端回传的 stale version=1 作 WHERE，而非读回的当前值 2。
	if repo.updatedSecret == nil {
		t.Fatal("repo.Update not called")
	}
	if repo.updatedSecret.Version != 1 {
		t.Fatalf("expected updated secret keep version 1 as where cond, got %d", repo.updatedSecret.Version)
	}
}

// TestIntegrationSecret_Update_DecryptFail 验证 RSA 解密失败返 1400（BR6）。
func TestIntegrationSecret_Update_DecryptFail(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: "old"}}
	dec := &fakeSecretDecryptor{err: errors.New("rsa expired")}
	_, err := newSecretSvc(repo, dec, &fakeConvLog{}).Update(context.Background(), 1, "rsa-cipher", "kid1")
	wantSecretCode(t, err, errcode.BadRequest)
	if repo.updatedSecret != nil {
		t.Fatal("should not call repo.Update when decrypt fails")
	}
}

// TestIntegrationSecret_Update_TooLong 验证明文超 200 字符返 1400（BR6）。
func TestIntegrationSecret_Update_TooLong(t *testing.T) {
	long := stringOf('x', 201)
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1}}
	dec := &fakeSecretDecryptor{pw: long}
	_, err := newSecretSvc(repo, dec, &fakeConvLog{}).Update(context.Background(), 1, "rsa-cipher", "kid1")
	wantSecretCode(t, err, errcode.BadRequest)
}

// TestIntegrationSecret_Update_EmptyCipher 验证空密文前置校验返 1400（BR6）。
func TestIntegrationSecret_Update_EmptyCipher(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1}}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Update(context.Background(), 1, "", "kid1")
	wantSecretCode(t, err, errcode.BadRequest)
}

// TestIntegrationSecret_Test_NotConfigured 验证空 cipher 前置拦截返 1303（BR3）。
func TestIntegrationSecret_Test_NotConfigured(t *testing.T) {
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: ""}}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{}).Test(context.Background())
	wantSecretCode(t, err, errcode.IntegrationSecretNotConfigured)
}

// TestIntegrationSecret_Test_Connected 验证 Ping 返 nil → Connected==true（specs §4.2.4 规则5）。
func TestIntegrationSecret_Test_Connected(t *testing.T) {
	encKey := crypto.DeriveKey("test-integration-secret")
	cipher, _ := crypto.Encrypt(encKey, "valid-bearer-secret")
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	res, err := newSecretSvc(repo, &fakeSecretDecryptor{}, &fakeConvLog{pingErr: nil}).Test(context.Background())
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if !res.Connected {
		t.Fatal("expected Connected=true")
	}
}

// TestIntegrationSecret_Test_Failed 验证 Ping 返 error → Code==1304 且 Msg 含失败原因（BR4）。
// fake 文案对齐 classifiedError.Error() 只输出 cause 的真实格式（无 conversationlog: 哨兵前缀）。
func TestIntegrationSecret_Test_Failed(t *testing.T) {
	encKey := crypto.DeriveKey("test-integration-secret")
	cipher, _ := crypto.Encrypt(encKey, "invalid-bearer-secret")
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	convLog := &fakeConvLog{pingErr: errors.New("密钥无效 (HTTP 401)")}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, convLog).Test(context.Background())
	var se *service.Error
	if !errors.As(err, &se) || se.Code != errcode.IntegrationSecretTestFailed {
		t.Fatalf("want code %d, got %v", errcode.IntegrationSecretTestFailed, err)
	}
	if se.Msg == "" {
		t.Fatal("expected non-empty Msg carrying failure reason")
	}
	if !strings.Contains(se.Msg, "密钥无效") {
		t.Fatalf("expected Msg to contain failure reason, got %q", se.Msg)
	}
	// 守护 specs 330 口径：哨兵英文串不得泄漏进用户可见文案。
	if strings.Contains(se.Msg, "conversationlog:") {
		t.Fatalf("Msg leaked sentinel prefix, got %q", se.Msg)
	}
}

// TestIntegrationSecret_Test_CtxCanceled 验证父 ctx 取消时 Msg 映射为通用文案而非错误原文
// （错误链含完整上游 URL，specs 330 白名单外，不得透传前端）。
func TestIntegrationSecret_Test_CtxCanceled(t *testing.T) {
	encKey := crypto.DeriveKey("test-integration-secret")
	cipher, _ := crypto.Encrypt(encKey, "valid-bearer-secret")
	repo := &fakeSecretRepo{getSecret: &domain.IntegrationSecret{ID: 1, SecretCipher: cipher}}
	// 模拟 Ping 的 ctx 直通路径：哨兵为 context.Canceled，cause 链含上游 URL 原文。
	convLog := &fakeConvLog{pingErr: fmt.Errorf("探活被调用方中止: %w", fmt.Errorf(`Get "http://10.0.0.8:8000/api/conversation-log/?p=1&page_size=1": %w`, context.Canceled))}
	_, err := newSecretSvc(repo, &fakeSecretDecryptor{}, convLog).Test(context.Background())
	var se *service.Error
	if !errors.As(err, &se) || se.Code != errcode.IntegrationSecretTestFailed {
		t.Fatalf("want code %d, got %v", errcode.IntegrationSecretTestFailed, err)
	}
	want := "无法连通上游会话日志服务，请检查网络与上游服务状态"
	if se.Msg != want {
		t.Fatalf("Msg = %q, want generic %q", se.Msg, want)
	}
	if strings.Contains(se.Msg, "10.0.0.8") {
		t.Fatalf("Msg leaked upstream URL, got %q", se.Msg)
	}
}
