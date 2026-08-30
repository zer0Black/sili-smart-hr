// Package service 承载业务逻辑，按业务域划分。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode/utf8"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/model"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SetupService 是系统初始化域业务接口（specs §5.1、§5.2）。
type SetupService interface {
	GetStatus(ctx context.Context) (*SetupStatus, error)
	Initialize(ctx context.Context, username, name, passwordCipher, keyID string) (*SetupResult, error)
}

// SetupStatus 是 GET /api/setup/status 的业务返回结构，handler 转响应体。
// DBType 取自 model.Current() 进程级常量（04 §5），仅作信息展示。
type SetupStatus struct {
	Initialized       bool
	DBType            string
	DatabaseConnected bool
	RedisConnected    bool
	BlockSubmit       bool
}

// SetupResult 是 POST /api/setup/initialize 成功后的业务返回结构。
type SetupResult struct {
	Initialized bool
	Account     *AccountDTO
}

type setupService struct {
	db             *gorm.DB
	systemInitRepo repository.SystemInitializationRepository
	accountRepo    repository.AccountRepository
	decryptor      PasswordDecryptor
	dbProbe        func(context.Context) bool
	redisProbe     func(context.Context) bool
	// mu 串行化 Initialize 的「Exists + 查重 + 写入」临界区，避免并发提交绕过查重
	// 产生重复账号与多行初始化记录。单进程内嵌架构下进程内锁有效，多实例部署需升级为 Redis 分布式锁。
	mu sync.Mutex
}

// NewSetupService 注入 root db、两个 repository、密码解密器与 db/redis 探针。
// Initialize 用 root db 开启事务，在事务内 tx.Create 直接写账号与初始化记录，
// 不调 AccountService.CreateAccount（该方法自带独立事务，无法延伸到初始化记录）。
func NewSetupService(
	db *gorm.DB,
	systemInitRepo repository.SystemInitializationRepository,
	accountRepo repository.AccountRepository,
	decryptor PasswordDecryptor,
	dbProbe, redisProbe func(context.Context) bool,
) SetupService {
	return &setupService{
		db:             db,
		systemInitRepo: systemInitRepo,
		accountRepo:    accountRepo,
		decryptor:      decryptor,
		dbProbe:        dbProbe,
		redisProbe:     redisProbe,
	}
}

// GetStatus 返回初始化状态与环境自检结果（specs §5.1.2，异常处理 §5.1.5）。
//
//	initialized 查 system_initializations 记录存在性判定就绪态（specs 规则1）。
//	探测失败不影响响应结构，仅反映为对应自检项 false，block_submit 据连通性收敛。
//	只读展示路径：Exists 出错降级为 false 不写库，无害。
func (s *setupService) GetStatus(ctx context.Context) (*SetupStatus, error) {
	initialized, err := s.systemInitRepo.Exists(ctx)
	if err != nil {
		slog.Error("query system initialization exists failed", "err", err)
		initialized = false
	}
	databaseConnected := s.dbProbe(ctx)
	redisConnected := s.redisProbe(ctx)
	return &SetupStatus{
		Initialized:       initialized,
		DBType:            string(model.Current()),
		DatabaseConnected: databaseConnected,
		RedisConnected:    redisConnected,
		BlockSubmit:       !databaseConnected || !redisConnected,
	}, nil
}

// Initialize 创建首个账号并写初始化记录锁定状态（specs §5.2.2，事务边界 03 §3.2）。
//
//	字段校验 → 自检阻断 → 临界区内 Exists + 查重 + 单事务 tx.Create。
//	账号与初始化记录同事务写入，任一失败整体回滚，避免半状态（specs §5.2.4）。
//	pure 计算（RSA 解密 / validatePassword / bcrypt）放锁外，mu 锁只串行化 Exists + 查重 + 事务写入，
//	消除并发提交绕过查重的 TOCTOU（specs 规则1、规则5），锁粒度对齐 account.go CreateAccount 样板。
func (s *setupService) Initialize(ctx context.Context, username, name, passwordCipher, keyID string) (*SetupResult, error) {
	// 字段校验（pure 计算，锁外）：username 格式（specs §03 4.1 / BR9）。
	if !validateUsername(username) {
		return nil, NewError(errcode.BadRequest)
	}
	// name 规整与长度：先 trim 收敛纯空格脏数据，再校 utf8.RuneCountInString ∈ [1, 20]（复用 account 口径）。
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 20 || len(name) == 0 {
		return nil, NewError(errcode.BadRequest)
	}
	// 密码 RSA 解密：失败返 1400（非反枚举范围）。
	plain, derr := s.decryptor.Decrypt(ctx, keyID, passwordCipher)
	if derr != nil {
		return nil, NewError(errcode.BadRequest)
	}
	// 密码强度：≥8 位且字母+数字（复用 account validatePassword）。
	if !validatePassword(plain) {
		return nil, NewError(errcode.PasswordInvalid)
	}
	// bcrypt 预计算（锁外）：hash 在事务内才用，参照 account.go CreateAccount 样板缩小锁粒度。
	hash, herr := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if herr != nil {
		return nil, fmt.Errorf("bcrypt hash: %w", herr)
	}
	// 自检阻断：数据库或缓存连通任一未通过返 1102（specs 规则3）。
	if !s.dbProbe(ctx) || !s.redisProbe(ctx) {
		return nil, NewError(errcode.EnvironmentNotReady)
	}

	// 临界区：Exists + 查重 + 事务写入串行化，消除并发同 username 双写与多行初始化记录。
	s.mu.Lock()
	defer s.mu.Unlock()

	// 前置：查 system_initializations 存在性判定就绪态（specs 规则1）。
	// 查询出错状态不明，保守拒绝写入，避免在已初始化系统上二次创建（GetStatus 只读路径仍可降级）。
	exists, err := s.systemInitRepo.Exists(ctx)
	if err != nil {
		slog.Error("query system initialization exists failed", "err", err)
		return nil, NewError(errcode.Internal)
	}
	if exists {
		return nil, NewError(errcode.SystemAlreadyInitialized)
	}
	// 查重：FindByUsername 找到（非 NotFound）返 1005；其它错误透传。
	if _, ferr := s.accountRepo.FindByUsername(ctx, username); ferr == nil {
		return nil, NewError(errcode.UsernameExists)
	} else if !errors.Is(ferr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("find account by username: %w", ferr)
	}
	// 单事务：账号 + 初始化记录原子写入（specs §5.2.4 / BR8）。
	var acc *domain.Account
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		acc = &domain.Account{
			Username:     username,
			PasswordHash: string(hash),
			Name:         name,
			Enabled:      true, // 业务层显式 true（specs 规则4 / BR7），遵循布尔字段不加 default tag
		}
		if e := tx.Create(acc).Error; e != nil {
			return e
		}
		rec := &domain.SystemInitialization{
			CreatorAccountID: acc.ID,
			DBType:           string(model.Current()),
		}
		return tx.Create(rec).Error
	})
	if txErr != nil {
		slog.Error("initialize transaction failed", "err", txErr)
		return nil, NewError(errcode.Internal)
	}
	dto := toDTO(acc)
	return &SetupResult{Initialized: true, Account: &dto}, nil
}
