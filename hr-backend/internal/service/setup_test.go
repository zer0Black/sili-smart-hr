// Package service_test 对 SetupService 做黑盒集成测试（真 SQLite 内存库 + 真 repository）。
//
// 覆盖 GetStatus 与 Initialize 两条主链路及错误分支，逐用例红绿推进。
// 复用 account_test.go 同包既有的 fakeDecryptor（pw+err）与 wantCode 辅助，不重复定义。
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/pkg/errcode"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
)

// newSetupTestDB 构造独立 :memory: SQLite 并 AutoMigrate Account + SystemInitialization。
// 雪花回调未注册，ID 零值由 SQLite 主键自增兜底，Exists 与 FindByUsername 走 GORM 正常。
func newSetupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.Account{}, &domain.SystemInitialization{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// constProbe 返回固定 bool 的连通探针，驱动 dbProbe/redisProbe 可控分支。
func constProbe(b bool) func(context.Context) bool {
	return func(context.Context) bool { return b }
}

// TestGetStatus_NotInitialized_NoBlock：空库 + db/redis 通 → Initialized=false、BlockSubmit=false。
// 注意：DBType 字段取自 model.Current() 进程级全局，package service_test 无法调私有 setCurrent 设置，
// 故不断言 DBType 字面值（参见 [REPORT] DBType 断言说明）。
func TestGetStatus_NotInitialized_NoBlock(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	status, err := svc.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.Initialized {
		t.Fatal("Initialized want false on empty db")
	}
	if !status.DatabaseConnected {
		t.Fatal("DatabaseConnected want true")
	}
	if !status.RedisConnected {
		t.Fatal("RedisConnected want true")
	}
	if status.BlockSubmit {
		t.Fatal("BlockSubmit want false when db+redis up")
	}
}

// TestGetStatus_RedisDown_Block：redisProbe 返 false → BlockSubmit=true、RedisConnected=false。
// 验证 block_submit = !database || !redis，单一阻断项触发置灰（specs §4.1.4 规则3）。
func TestGetStatus_RedisDown_Block(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(false))

	status, err := svc.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.RedisConnected {
		t.Fatal("RedisConnected want false")
	}
	if !status.DatabaseConnected {
		t.Fatal("DatabaseConnected want true (db still up)")
	}
	if !status.BlockSubmit {
		t.Fatal("BlockSubmit want true when redis down")
	}
}

// TestInitialize_AlreadyInit_1101：预置一条初始化记录后再次 Initialize → 1101（specs §4.1.4 规则5）。
// 验证 Initialize 前置查 system_initializations 存在性，存在即拒绝（specs 规则1、规则5）。
func TestInitialize_AlreadyInit_1101(t *testing.T) {
	db := newSetupTestDB(t)
	if err := db.Create(&domain.SystemInitialization{CreatorAccountID: 1, DBType: "sqlite"}).Error; err != nil {
		t.Fatalf("seed system_initialization: %v", err)
	}
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.SystemAlreadyInitialized)
}

// TestInitialize_DBDown_1102：dbProbe 返 false → 1102（specs §4.1.4 规则3 / BR6）。
// 验证自检阻断在 exists 之后、字段校验之前。
func TestInitialize_DBDown_1102(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(false), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.EnvironmentNotReady)
}

// TestInitialize_BadUsername_1400：username="ab"（<3 位）→ 1400（specs §03 4.1 / BR9 复用 account 校验）。
func TestInitialize_BadUsername_1400(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "ab", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.BadRequest)
}

// TestInitialize_BlankName_1400：name 为纯空格经 trim 后为空 → 1400。
// 防御绕过前端 trim 直接调 API 写入空白姓名脏数据。
func TestInitialize_BlankName_1400(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "   ", "cipher", "kid")
	wantCode(t, err, errcode.BadRequest)
}

// TestInitialize_BadPassword_1007：fakeDecryptor 返 "short"（<8 位）→ 1007。
func TestInitialize_BadPassword_1007(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "short"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.PasswordInvalid)
}

// TestInitialize_DecryptFail_1400：fakeDecryptor 返 err → 1400（非反枚举范围）。
func TestInitialize_DecryptFail_1400(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{err: errors.New("rsakey: key expired")}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.BadRequest)
}

// TestInitialize_UsernameExists_1005：预置 db.Create(&Account{Username:"admin"}) 再 Initialize → 1005。
func TestInitialize_UsernameExists_1005(t *testing.T) {
	db := newSetupTestDB(t)
	if err := db.Create(&domain.Account{Username: "admin", PasswordHash: "h", Name: "管理员", Enabled: true}).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.UsernameExists)
}

// TestInitialize_ExistsError_1500：Drop system_initializations 表令 Exists 查询报错 → 1500。
// 验证 Initialize 写入路径在状态不明时保守拒绝，不降级为 false 放行（避免在已初始化系统上二次创建）。
// 对照：GetStatus 只读路径仍可降级 false（写库无害），Initialize 写入路径不可。
func TestInitialize_ExistsError_1500(t *testing.T) {
	db := newSetupTestDB(t)
	if err := db.Migrator().DropTable(&domain.SystemInitialization{}); err != nil {
		t.Fatalf("drop system_initializations: %v", err)
	}
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	_, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	wantCode(t, err, errcode.Internal)
}

// TestInitialize_Success：成功后 system_initializations 有记录、accounts 1 行且 enabled=true、
// 初始化记录 creator_account_id == account.id、返回 SetupResult.Initialized=true & Account.Username=="admin"。
// 验证账号 + 初始化记录事务原子写入、Enabled 显式 true（BR7/BR8）。
func TestInitialize_Success(t *testing.T) {
	db := newSetupTestDB(t)
	sysRepo := repository.NewSystemInitializationRepository(db)
	accRepo := repository.NewAccountRepository(db)
	dec := &fakeDecryptor{pw: "Admin123"}
	svc := service.NewSetupService(db, sysRepo, accRepo, dec, constProbe(true), constProbe(true))

	res, err := svc.Initialize(context.Background(), "admin", "管理员", "cipher", "kid")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !res.Initialized {
		t.Fatal("result Initialized want true")
	}
	if res.Account == nil || res.Account.Username != "admin" || res.Account.Name != "管理员" {
		t.Fatalf("unexpected result account: %+v", res.Account)
	}
	if !res.Account.Enabled {
		t.Fatal("account Enabled want true (BR7)")
	}

	// system_initializations 有 1 行，CreatorAccountID 关联到刚创建的 account。
	exists, eerr := sysRepo.Exists(context.Background())
	if eerr != nil || !exists {
		t.Fatalf("system_initializations should exist after success: exists=%v err=%v", exists, eerr)
	}
	var acc domain.Account
	if err := db.First(&acc).Error; err != nil {
		t.Fatalf("query account: %v", err)
	}
	if !acc.Enabled || acc.PasswordHash == "" {
		t.Fatalf("account Enabled want true, PasswordHash non-empty: %+v", acc)
	}
	var rec domain.SystemInitialization
	if err := db.First(&rec).Error; err != nil {
		t.Fatalf("query system_initialization: %v", err)
	}
	if rec.CreatorAccountID != acc.ID {
		t.Fatalf("CreatorAccountID mismatch: rec=%d acc=%d", rec.CreatorAccountID, acc.ID)
	}
}
