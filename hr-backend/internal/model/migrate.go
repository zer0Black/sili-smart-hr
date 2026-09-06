package model

import (
	"encoding/json"
	"fmt"

	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/pkg/dberr"
	"sili-smart-hr/backend/internal/pkg/snowflake"

	"gorm.io/gorm"
)

// migrateDB 编排全部 domain 模型的建表与首启 seed，每次启动都调用，整体幂等。
// AutoMigrate 全量建表（建表、加列、加索引，GORM 不删列不改列类型）；
// 各 seed 走 FirstOrCreate，行存在即跳过，重跑无副作用。
func migrateDB(db *gorm.DB) error {
	// A 段：AutoMigrate 之前的手写类型/加列迁移钩子（幂等）。
	if err := migrateSessionFeatureClient(db); err != nil {
		return err
	}

	// 全部 domain 模型
	if err := db.AutoMigrate(allModels()...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	// dimension_settings 首启 seed：系统级单例，空表时写入默认活跃度阈值（specs 4.1.2 C 默认值：活跃下限 10、低频下限 5）。
	// FirstOrCreate 在事务内 First 未命中即 Create，收敛查写窗口抗并发。Where("1 = 1") 让 First 命中任意已存在行；
	// Attrs 提供"未命中时用于 Create 的初始属性"，绝不能放进 FirstOrCreate 的第二参数（会被当 where 条件，
	// 且 snowflake.NextID() 每次新值会让 where id=<新雪花> 永远查不到，导致重复写入）。任何真实 DB 错误
	// （连接抖动等非 ErrRecordNotFound）经 .Error 向上冒泡，不被静默吞。
	var setting domain.DimensionSetting
	if err := db.Where("1 = 1").Attrs(&domain.DimensionSetting{
		ID:                    snowflake.NextID(),
		ActiveThreshold:       10,
		LowFrequencyThreshold: 5,
	}).FirstOrCreate(&setting).Error; err != nil {
		return fmt.Errorf("seed dimension_settings: %w", err)
	}

	// assessment_configs 首启 seed：系统级单例，空表时写入默认周期参数（specs §4：weekly/23:00/all/version=1）。
	// 与 dimension_settings seed 同范式：Where("1 = 1") 命中任意已存在行，Attrs 提供未命中时的初始值（含显式雪花 ID），
	// 真实 DB 错误经 .Error 向上冒泡。
	var assessCfg domain.AssessmentConfig
	if err := db.Where("1 = 1").Attrs(&domain.AssessmentConfig{
		ID:          snowflake.NextID(),
		Period:      "weekly",
		TriggerTime: "23:00",
		TargetMode:  "all",
		Version:     1,
	}).FirstOrCreate(&assessCfg).Error; err != nil {
		return fmt.Errorf("seed assessment_configs: %w", err)
	}

	// integration_secrets 首启 seed：系统级单例，空表时写入空密钥行（specs §6.1 + 04 §4：未配置为默认态）。
	// 配置状态由 SecretCipher 是否为空推导，首启空行表示未配置；与 assessment_configs seed 各自独立判空、互不影响。
	var integrationSecret domain.IntegrationSecret
	if err := db.Where("1 = 1").Attrs(&domain.IntegrationSecret{
		ID:           snowflake.NextID(),
		SecretCipher: "",
		SecretMasked: "",
		Version:      1,
	}).FirstOrCreate(&integrationSecret).Error; err != nil {
		return fmt.Errorf("seed integration_secrets: %w", err)
	}

	// system_params 首启 seed（TECH_004 04 §4）：按键 FirstOrCreate，行存在即跳过。
	// inject_prefixes 为追加语义，出厂集不进 DB（seed 空数组，出厂并集在代码中）；
	// redact_patterns 维持替换语义，出厂全集照旧 seed。
	seeds := []struct {
		key    string
		desc   string
		values []string
	}{
		{extractor.ParamKeyInjectPrefixes, "会话裁剪注入前缀全局追加黑名单（JSON数组，只追加不替换，出厂集在代码中）", []string{}},
		{extractor.ParamKeyRedactPatterns, "指令脱敏正则集（JSON数组，覆盖路径/密钥/内网地址）", extractor.RedactPatterns()},
	}
	for _, s := range seeds {
		if err := seedStringArrayParam(db, s.key, s.desc, s.values); err != nil {
			return err
		}
	}

	// C 段：AutoMigrate 之后的数据回填钩子（幂等）。
	if err := migrateInjectPrefixesAppendSemantics(db); err != nil {
		return err
	}

	return nil
}

// migrateInjectPrefixesAppendSemantics 把替换语义时期 seed 的 inject_prefixes
// 存量行（param_value 为出厂全集 JSON）一次性重置为空数组并同步新描述：
// 追加语义下出厂集在代码中，旧行残留会让参数页呈现 59 条来历不明的追加条目。
// 仅匹配出厂全集值，运维增删过的行不触碰（幂等：重置后值不匹配即跳过）。
func migrateInjectPrefixesAppendSemantics(db *gorm.DB) error {
	legacyJSON, err := json.Marshal(extractor.InjectPrefixes())
	if err != nil {
		return fmt.Errorf("marshal legacy inject_prefixes: %w", err)
	}
	res := db.Model(&domain.SystemParam{}).
		Where("param_key = ? AND param_value = ?", extractor.ParamKeyInjectPrefixes, string(legacyJSON)).
		Updates(map[string]any{
			"param_value": "[]",
			"description": "会话裁剪注入前缀全局追加黑名单（JSON数组，只追加不替换，出厂集在代码中）",
		})
	if res.Error != nil {
		return fmt.Errorf("migrate %s append semantics: %w", extractor.ParamKeyInjectPrefixes, res.Error)
	}
	return nil
}

// seedStringArrayParam 按键 seed JSON 数组参数，行已存在不覆盖。双实例同窗首启空库
// 时 FirstOrCreate 的 First 与 Create 间无互斥，双双 Create 后写者撞 uk_param_key：
// 撞键视为对端胜出返回成功（内容同源同值），防进程启动失败。
func seedStringArrayParam(db *gorm.DB, key, description string, values []string) error {
	valueJSON, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	var param domain.SystemParam
	err = db.Where("param_key = ?", key).Attrs(domain.SystemParam{
		ID:          snowflake.NextID(),
		ParamKey:    key,
		ParamValue:  string(valueJSON),
		Description: description,
		Version:     1,
	}).FirstOrCreate(&param).Error
	if dberr.UniqueViolation(err) {
		return nil // 并发双 seed 对端胜出，行已落库
	}
	if err != nil {
		return fmt.Errorf("seed system_params %s: %w", key, err)
	}
	return nil
}

// migrateSessionFeatureClient 给存量 session_features 补 client 列（幂等）：
// NOT NULL 无 default 的加列在有数据行的库上会被 SQLite/PG 拒绝（AutoMigrate
// 直接 ALTER 无 DEFAULT 兜底），表不存在或列已存在时跳过。须在 AutoMigrate
// 之前执行，新建表场景由 AutoMigrate 正常建列。
func migrateSessionFeatureClient(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&domain.SessionFeature{}) || m.HasColumn(&domain.SessionFeature{}, "Client") {
		return nil
	}
	if err := db.Exec("ALTER TABLE session_features ADD COLUMN client varchar(32) NOT NULL DEFAULT ''").Error; err != nil {
		return fmt.Errorf("add session_features.client: %w", err)
	}
	return nil
}

// allModels 返回全部参与 AutoMigrate 的 domain 模型，新增业务域模型在此登记。
func allModels() []any {
	return []any{
		&domain.Account{},
		&domain.SystemInitialization{},
		&domain.Dimension{},
		&domain.DimensionSetting{},
		&domain.AssessmentConfig{},
		&domain.AssessmentConfigMember{},
		&domain.LLMConfig{},
		&domain.IntegrationSecret{},
		&domain.SessionFeature{},
		&domain.DimensionScore{},
		&domain.AggregateScore{},
		&domain.ActivityStat{},
		&domain.SystemParam{},
	}
}
