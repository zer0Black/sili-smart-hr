// Package repository_test 对 system_param 读取做黑盒集成测试。
//
// 覆盖 ReadStringArray 三分支（行存在返回解析值 / 行缺失回退 defaulter /
// 值损坏回退 defaulter 且 err=nil）、空数组 `[]` 合法、key 无映射返回 nil
// 与显式清空区分、真实 DB 故障返回 error（specs TECH_003 04_model §3.2
// 键缺失回退与值格式约定）。每测试独立 :memory: SQLite 实例隔离。
package repository_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/repository"
)

// newParamTestDB 构造独立 :memory: SQLite 并 AutoMigrate SystemParam。
func newParamTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&domain.SystemParam{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// seedParam 直插一行参数（绕过仓储写入路径，本任务只落读取）。
func seedParam(t *testing.T, db *gorm.DB, key, value string) {
	t.Helper()
	if err := db.Create(&domain.SystemParam{
		ParamKey:    key,
		ParamValue:  value,
		Description: "测试参数",
		Version:     1,
	}).Error; err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}

// testDefaulter 模拟装配层注入的出厂默认（刻意用字面值副本，保持测试与 extractor 包解耦）。
var testDefaulter = map[string][]string{
	"extractor.inject_prefixes": {"<system-reminder", "Note:"},
	"extractor.redact_patterns": {`C:\\Users\\\w+`, `sk-[A-Za-z0-9]+`},
}

// TestReadStringArray 覆盖读取三分支（specs 04 §3.2 键缺失回退）：
// 行存在返回解析值；行不存在返回 defaulter 值；值损坏 JSON 回退 defaulter 且 err=nil。
func TestReadStringArray(t *testing.T) {
	t.Run("预置行返回解析值", func(t *testing.T) {
		db := newParamTestDB(t)
		seedParam(t, db, "extractor.inject_prefixes", `["Note:","Contents of"]`)
		reader := repository.NewSystemParamReader(db, testDefaulter)

		got, err := reader.ReadStringArray("extractor.inject_prefixes")
		if err != nil {
			t.Fatalf("预置行读取应无 error, got %v", err)
		}
		if len(got) != 2 || got[0] != "Note:" || got[1] != "Contents of" {
			t.Fatalf("want [Note: Contents of], got %v", got)
		}
	})

	t.Run("行不存在返回 defaulter 值", func(t *testing.T) {
		db := newParamTestDB(t)
		reader := repository.NewSystemParamReader(db, testDefaulter)

		got, err := reader.ReadStringArray("extractor.redact_patterns")
		if err != nil {
			t.Fatalf("行缺失回退应无 error, got %v", err)
		}
		if len(got) != len(testDefaulter["extractor.redact_patterns"]) {
			t.Fatalf("want defaulter 值 %v, got %v", testDefaulter["extractor.redact_patterns"], got)
		}
		for i, v := range testDefaulter["extractor.redact_patterns"] {
			if got[i] != v {
				t.Fatalf("want defaulter[%d]=%q, got %q", i, v, got[i])
			}
		}
	})

	t.Run("损坏 JSON 返回 defaulter 值且 err=nil", func(t *testing.T) {
		db := newParamTestDB(t)
		seedParam(t, db, "extractor.inject_prefixes", `{bad`)
		reader := repository.NewSystemParamReader(db, testDefaulter)

		got, err := reader.ReadStringArray("extractor.inject_prefixes")
		if err != nil {
			t.Fatalf("值损坏回退属正常路径应 err=nil, got %v", err)
		}
		want := testDefaulter["extractor.inject_prefixes"]
		if len(got) != len(want) {
			t.Fatalf("want defaulter 值 %v, got %v", want, got)
		}
		for i, v := range want {
			if got[i] != v {
				t.Fatalf("want defaulter[%d]=%q, got %q", i, v, got[i])
			}
		}
	})
}

// TestReadStringArrayEmptyArray 验证空数组 `[]` 合法返回空切片非 nil
// （specs 04 §3.2 值格式约定：黑名单清空回退 role 兜底全保留口径）。
func TestReadStringArrayEmptyArray(t *testing.T) {
	db := newParamTestDB(t)
	seedParam(t, db, "extractor.inject_prefixes", `[]`)
	reader := repository.NewSystemParamReader(db, testDefaulter)

	got, err := reader.ReadStringArray("extractor.inject_prefixes")
	if err != nil {
		t.Fatalf("空数组读取应无 error, got %v", err)
	}
	if got == nil {
		t.Fatalf("空数组应返回空切片非 nil")
	}
	if len(got) != 0 {
		t.Fatalf("空数组 want len=0, got %v", got)
	}
}

// TestReadStringArrayUnknownKeyNoDefault 验证 key 无映射时返回 nil（键未注册与
// 运维显式 `[]` 清空区分：调用方以 nil 判定回退出厂，防空集被当显式清空静默生效）。
func TestReadStringArrayUnknownKeyNoDefault(t *testing.T) {
	db := newParamTestDB(t)
	reader := repository.NewSystemParamReader(db, testDefaulter)

	got, err := reader.ReadStringArray("extractor.not_configured")
	if err != nil {
		t.Fatalf("key 无映射回退应无 error, got %v", err)
	}
	if got != nil {
		t.Fatalf("key 无映射 want nil, got %v", got)
	}
}

// TestReadStringArrayFallbackIsCopy 验证回退值是副本：调用方原地修改不得污染
// defaulter 出厂默认（进程级共享，污染会静默改变后续所有回退读取）。
func TestReadStringArrayFallbackIsCopy(t *testing.T) {
	db := newParamTestDB(t)
	reader := repository.NewSystemParamReader(db, testDefaulter)

	got, err := reader.ReadStringArray("extractor.inject_prefixes")
	if err != nil {
		t.Fatalf("行缺失回退应无 error, got %v", err)
	}
	got[0] = "mutated-by-caller"

	again, err := reader.ReadStringArray("extractor.inject_prefixes")
	if err != nil {
		t.Fatalf("二次回退读取应无 error, got %v", err)
	}
	if again[0] == "mutated-by-caller" {
		t.Fatalf("回退值与 defaulter 共享底层数组, 原地修改污染了出厂默认")
	}
	if want := testDefaulter["extractor.inject_prefixes"][0]; again[0] != want {
		t.Fatalf("二次回退 want %q, got %q", want, again[0])
	}
}

// TestReadStringArrayDBFailure 验证真实 DB 故障返回非 nil error（DB 权威故障不静默）。
func TestReadStringArrayDBFailure(t *testing.T) {
	db := newParamTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("取底层连接: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	reader := repository.NewSystemParamReader(db, testDefaulter)

	got, err := reader.ReadStringArray("extractor.inject_prefixes")
	if err == nil {
		t.Fatalf("DB 故障应返回 error, got nil (value=%v)", got)
	}
}

// TestReadStringArrayNullLiteral 验证 param_value 为 "null" 字面时 Unmarshal 成功但值
// 仍为 nil，统一按损坏回退 defaulter（specs 04 §3.2 值格式约定）。
func TestReadStringArrayNullLiteral(t *testing.T) {
	db := newParamTestDB(t)
	seedParam(t, db, "extractor.inject_prefixes", `null`)
	reader := repository.NewSystemParamReader(db, testDefaulter)

	got, err := reader.ReadStringArray("extractor.inject_prefixes")
	if err != nil {
		t.Fatalf("null 字面回退属正常路径应 err=nil, got %v", err)
	}
	want := testDefaulter["extractor.inject_prefixes"]
	if len(got) != len(want) {
		t.Fatalf("null 字面 want defaulter 值 %v, got %v", want, got)
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("want defaulter[%d]=%q, got %q", i, v, got[i])
		}
	}
}
