// Package service 承载业务逻辑，按业务域划分。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/repository"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	usernameRegex       = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)
	passwordLetterRegex = regexp.MustCompile(`[A-Za-z]`)
	passwordDigitRegex  = regexp.MustCompile(`[0-9]`)
)

func validateUsername(s string) bool { return usernameRegex.MatchString(s) }

// validatePassword：≥8 位且同时含字母与数字。
func validatePassword(s string) bool {
	if len(s) < 8 {
		return false
	}
	return passwordLetterRegex.MatchString(s) && passwordDigitRegex.MatchString(s)
}

// AccountDTO 是脱敏账号，雪花 ID 以 string 序列化规避前端 JS 精度坑。
type AccountDTO struct {
	ID       int64  `json:"id,string"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
}

// AccountListItemDTO 列表项，额外承载 LastLoginAt。
type AccountListItemDTO struct {
	ID          int64      `json:"id,string"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Enabled     bool       `json:"enabled"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

type LoginResult struct {
	Token   string     `json:"token"`
	Account AccountDTO `json:"account"`
}

// Error 携带业务错误码，handler 据此映射响应。
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("service error: code=%d msg=%s", e.Code, e.Msg)
}

func NewError(code int) *Error {
	return &Error{Code: code, Msg: errcode.Message(code)}
}

// AccountService 是 account 域业务接口。
type AccountService interface {
	Login(ctx context.Context, username, passwordCipher, keyID string) (*LoginResult, error)
	GetCurrent(ctx context.Context, accountID int64) (*AccountDTO, error)
	CreateAccount(ctx context.Context, username, name, passwordCipher, keyID string, enabled bool) (*AccountDTO, error)
	UpdateAccount(ctx context.Context, id int64, name, passwordCipher, keyID string, hasPassword bool, enabled bool) (*AccountDTO, error)
	DeleteAccount(ctx context.Context, id int64) error
	ToggleEnabled(ctx context.Context, id int64, enabled bool) error
	ResetPassword(ctx context.Context, id int64, passwordCipher, keyID string) error
	ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]AccountListItemDTO, int64, error)
}

// PasswordDecryptor 解密前端 RSA-OAEP 加密的密码密文，rsakey.Manager 自动实现。
type PasswordDecryptor interface {
	Decrypt(ctx context.Context, keyID string, cipherB64 string) (string, error)
}

type accountService struct {
	repo      repository.AccountRepository
	jwt       *jwt.Manager
	decryptor PasswordDecryptor
	dummyHash []byte
	// mu 串行化 CreateAccount 的「查重 + 写入」临界区，避免并发同 username 产生重复行。
	// 单进程内嵌架构下进程内锁有效，多实例部署需升级为 Redis 分布式锁。
	mu sync.Mutex
}

// NewAccountService 预生成 dummyHash，供账号不存在路径做一次等效 bcrypt 收敛登录时序侧信道。
func NewAccountService(repo repository.AccountRepository, jwtManager *jwt.Manager, decryptor PasswordDecryptor) AccountService {
	dummy, err := bcrypt.GenerateFromPassword([]byte("sili-smart-hr-dummy-nonexistent"), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Errorf("generate dummy bcrypt hash: %w", err))
	}
	return &accountService{repo: repo, jwt: jwtManager, decryptor: decryptor, dummyHash: dummy}
}

// Login 校验账号密码并签发 JWT。
//
// 反枚举顺序敏感：四类失败路径（账号不存在 / 密码错误 / 账号禁用 / RSA 解密失败）
// 各执行一次等效 bcrypt，统一返回 InvalidCredentials，刻意不返回 AccountDisabled。
// 先 bcrypt 再判 Enabled，调整顺序会破坏时序收敛。
func (s *accountService) Login(ctx context.Context, username, passwordCipher, keyID string) (*LoginResult, error) {
	plaintext, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)

	acc, err := s.repo.FindByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(passwordCipher))
			return nil, NewError(errcode.InvalidCredentials)
		}
		return nil, fmt.Errorf("find account by username: %w", err)
	}

	// RSA 解密失败：用真实 hash 对密文做一次等效 bcrypt 收敛时序。
	if derr != nil {
		_ = bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(passwordCipher))
		return nil, NewError(errcode.InvalidCredentials)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(acc.PasswordHash), []byte(plaintext)); err != nil {
		return nil, NewError(errcode.InvalidCredentials)
	}

	if !acc.Enabled {
		return nil, NewError(errcode.InvalidCredentials)
	}

	token, err := s.jwt.Generate(acc.ID, acc.Username)
	if err != nil {
		return nil, fmt.Errorf("sign jwt: %w", err)
	}
	if err := s.repo.UpdateLastLoginAt(ctx, acc.ID, time.Now()); err != nil {
		slog.Warn("update last login at failed", "account_id", acc.ID, "err", err)
	}
	return &LoginResult{Token: token, Account: toDTO(acc)}, nil
}

func (s *accountService) GetCurrent(ctx context.Context, accountID int64) (*AccountDTO, error) {
	acc, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.Unauthorized)
		}
		return nil, fmt.Errorf("find account by id: %w", err)
	}
	dto := toDTO(acc)
	return &dto, nil
}

func toDTO(acc *domain.Account) AccountDTO {
	return AccountDTO{ID: acc.ID, Username: acc.Username, Name: acc.Name, Enabled: acc.Enabled}
}

// CreateAccount 新增账号：格式 / 强度校验 → bcrypt → 临界区内查重 + 写入。
// 临界区把 FindByUsername 与 Create 串行化，消除并发同 username 双双通过查重的 TOCTOU。
func (s *accountService) CreateAccount(ctx context.Context, username, name, passwordCipher, keyID string, enabled bool) (*AccountDTO, error) {
	if !validateUsername(username) {
		return nil, NewError(errcode.BadRequest)
	}
	if utf8.RuneCountInString(name) > 20 {
		return nil, NewError(errcode.BadRequest)
	}
	plaintext, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)
	if derr != nil {
		return nil, NewError(errcode.BadRequest)
	}
	if !validatePassword(plaintext) {
		return nil, NewError(errcode.PasswordInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("bcrypt hash: %w", err)
	}
	acc := &domain.Account{
		Username:     username,
		PasswordHash: string(hash),
		Name:         name,
		Enabled:      enabled,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.repo.FindByUsername(ctx, username); err == nil {
		return nil, NewError(errcode.UsernameExists)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find account by username: %w", err)
	}
	if err := s.repo.Create(ctx, acc); err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	dto := toDTO(acc)
	return &dto, nil
}

// UpdateAccount 编辑账号，可改 name、密码、启停。降停最后一个启用账号由 DemoteEnabledIfNotLast 原子拦截。
func (s *accountService) UpdateAccount(ctx context.Context, id int64, name, passwordCipher, keyID string, hasPassword bool, enabled bool) (*AccountDTO, error) {
	acc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NewError(errcode.AccountNotFound)
		}
		return nil, fmt.Errorf("find account by id: %w", err)
	}
	if utf8.RuneCountInString(name) > 20 {
		return nil, NewError(errcode.BadRequest)
	}
	if hasPassword {
		plaintext, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)
		if derr != nil {
			return nil, NewError(errcode.BadRequest)
		}
		if !validatePassword(plaintext) {
			return nil, NewError(errcode.PasswordInvalid)
		}
		h, herr := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
		if herr != nil {
			return nil, fmt.Errorf("bcrypt hash: %w", herr)
		}
		acc.PasswordHash = string(h)
	}
	// 降停分支走原子方法，RowsAffected==0 即最后一个启用账号。
	if acc.Enabled && !enabled {
		rows, derr := s.repo.DemoteEnabledIfNotLast(ctx, id)
		if derr != nil {
			return nil, fmt.Errorf("demote enabled: %w", derr)
		}
		if rows == 0 {
			return nil, NewError(errcode.LastEnabledAccount)
		}
	}
	acc.Name = name
	acc.Enabled = enabled
	if err := s.repo.Update(ctx, acc); err != nil {
		return nil, fmt.Errorf("update account: %w", err)
	}
	dto := toDTO(acc)
	return &dto, nil
}

// DeleteAccount 软删除账号。启用账号仅在启用总数 > 1 时可删，由 DeleteIfNotLastEnabled 原子判定。
func (s *accountService) DeleteAccount(ctx context.Context, id int64) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.AccountNotFound)
		}
		return fmt.Errorf("find account by id: %w", err)
	}
	rows, err := s.repo.DeleteIfNotLastEnabled(ctx, id)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if rows == 0 {
		return NewError(errcode.LastEnabledAccount)
	}
	return nil
}

// ToggleEnabled 即时切启停。停用方向走 DemoteEnabledIfNotLast 拦截最后一个启用账号。
func (s *accountService) ToggleEnabled(ctx context.Context, id int64, enabled bool) error {
	acc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.AccountNotFound)
		}
		return fmt.Errorf("find account by id: %w", err)
	}
	if !enabled && acc.Enabled {
		rows, derr := s.repo.DemoteEnabledIfNotLast(ctx, id)
		if derr != nil {
			return fmt.Errorf("demote enabled: %w", derr)
		}
		if rows == 0 {
			return NewError(errcode.LastEnabledAccount)
		}
		return nil
	}
	return s.repo.UpdateEnabled(ctx, id, enabled)
}

// ResetPassword 重置密码，只改 PasswordHash，不触碰 Enabled。
func (s *accountService) ResetPassword(ctx context.Context, id int64, passwordCipher, keyID string) error {
	acc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NewError(errcode.AccountNotFound)
		}
		return fmt.Errorf("find account by id: %w", err)
	}
	plaintext, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)
	if derr != nil {
		return NewError(errcode.BadRequest)
	}
	if !validatePassword(plaintext) {
		return NewError(errcode.PasswordInvalid)
	}
	h, herr := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if herr != nil {
		return fmt.Errorf("bcrypt hash: %w", herr)
	}
	acc.PasswordHash = string(h)
	return s.repo.Update(ctx, acc)
}

func (s *accountService) ListAccounts(ctx context.Context, keyword string, page, pageSize int) ([]AccountListItemDTO, int64, error) {
	list, total, err := s.repo.ListAccounts(ctx, keyword, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list accounts: %w", err)
	}
	items := make([]AccountListItemDTO, 0, len(list))
	for i := range list {
		items = append(items, AccountListItemDTO{
			ID:          list[i].ID,
			Username:    list[i].Username,
			Name:        list[i].Name,
			Enabled:     list[i].Enabled,
			LastLoginAt: list[i].LastLoginAt,
		})
	}
	return items, total, nil
}
