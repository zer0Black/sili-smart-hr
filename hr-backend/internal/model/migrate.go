package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

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
	if err := migrateAggregateScoreFloatToDouble(db); err != nil {
		return err
	}
	// 存量软删维度行 code 改写占位码：须在 AutoMigrate 之前，否则 uk_dimension_code
	// 建索引时旧语义的「软删行与活跃行同 code」存量数据会让建索引直接失败。
	if err := migrateDimensionDeletedCode(db); err != nil {
		return err
	}

	// 全部 domain 模型
	if err := db.AutoMigrate(allModels()...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	// dimension_settings 首启 seed：系统级单例，空表时写入默认活跃度阈值（specs 4.1.2 C 默认值：活跃下限 10、低频下限 5）。
	// 单行表防重入：固定主键 SingleRowID，双实例同窗空库并发 seed 撞主键，UniqueViolation
	// 视为对端胜出（FirstOrCreate 本身无跨进程互斥，事务只保证单实例内原子）。Where("1 = 1")
	// 兼容存量库主键为雪花值的已 seed 行（First 命中即跳过 Create）。Attrs 提供"未命中时用于
	// Create 的初始属性"，绝不能放进 FirstOrCreate 的第二参数（会被当 where 条件）。
	// 任何真实 DB 错误（连接抖动等非冲突态）经 .Error 向上冒泡，不被静默吞。
	var setting domain.DimensionSetting
	err := db.Where("1 = 1").Attrs(domain.DimensionSetting{
		ID:                    domain.SingleRowID,
		ActiveThreshold:       10,
		LowFrequencyThreshold: 5,
	}).FirstOrCreate(&setting).Error
	if dberr.UniqueViolation(err) {
		err = nil // 并发双 seed 对端胜出，行已落库
	}
	if err != nil {
		return fmt.Errorf("seed dimension_settings: %w", err)
	}

	// dimensions 首启 seed：默认对话分析维度（AI 使用能力 8 维），表非空即跳过
	//（运营维护过的库不触碰，含软删行计数防重复灌入）。
	if err := seedDefaultDimensions(db); err != nil {
		return err
	}

	// assessment_configs 首启 seed：系统级单例，空表时写入默认周期参数（specs §4：weekly/23:00/all/version=1）。
	// 与 dimension_settings seed 同范式：固定主键 + 撞键容错；Where("1 = 1") 兼容存量雪花主键行。
	var assessCfg domain.AssessmentConfig
	err = db.Where("1 = 1").Attrs(domain.AssessmentConfig{
		ID:          domain.SingleRowID,
		Period:      "weekly",
		TriggerTime: "23:00",
		TargetMode:  "all",
		Version:     1,
	}).FirstOrCreate(&assessCfg).Error
	if dberr.UniqueViolation(err) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("seed assessment_configs: %w", err)
	}

	// integration_secrets 首启 seed：系统级单例，空表时写入空密钥行（specs §6.1 + 04 §4：未配置为默认态）。
	// 配置状态由 SecretCipher 是否为空推导，首启空行表示未配置；与 assessment_configs seed 各自独立判空、互不影响。
	var integrationSecret domain.IntegrationSecret
	err = db.Where("1 = 1").Attrs(domain.IntegrationSecret{
		ID:           domain.SingleRowID,
		SecretCipher: "",
		SecretMasked: "",
		Version:      1,
	}).FirstOrCreate(&integrationSecret).Error
	if dberr.UniqueViolation(err) {
		err = nil
	}
	if err != nil {
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

// migrateAggregateScoreFloatToDouble 把 aggregate_scores 的 module_score/overview_score
// 从早期 type:float 建出的单精度列改为双精度（float32 仅约 7 位有效数字，一位
// 小数分数读回漂移）。SQLite 亲和 REAL 恒双精度跳过；幂等，须在 AutoMigrate 之前
//（GORM 不改列类型，AutoMigrate 之后无法收敛存量列）。
func migrateAggregateScoreFloatToDouble(db *gorm.DB) error {
	if Using(DBSQLite) {
		return nil // REAL 亲和恒双精度
	}
	m := db.Migrator()
	if !m.HasTable(&domain.AggregateScore{}) {
		return nil // 首启建表由 AutoMigrate 按 type:double precision 正常建列
	}
	for _, col := range []string{"module_score", "overview_score"} {
		if !m.HasColumn(&domain.AggregateScore{}, col) {
			continue
		}
		t, err := columnDataType(db, &domain.AggregateScore{}, col)
		if err != nil {
			return fmt.Errorf("probe aggregate_scores.%s data type: %w", col, err)
		}
		// 目标与过渡形态均跳过：double/double precision 是目标；real 覆盖
		// MySQL float 与 PG float4（information_schema 双方均报 real）。
		switch strings.ToLower(t) {
		case "double", "double precision", "real":
			continue
		}
		target := "DOUBLE PRECISION"
		if Using(DBMySQL) {
			target = "DOUBLE"
		}
		q := fmt.Sprintf("ALTER TABLE aggregate_scores ALTER COLUMN %s TYPE %s", QuoteIdent(col), target)
		// MySQL ALTER ... MODIFY 与 PG 语法不同，按方言分派。
		if Using(DBMySQL) {
			q = fmt.Sprintf("ALTER TABLE aggregate_scores MODIFY COLUMN %s %s NULL", QuoteIdent(col), target)
		}
		if err := db.Exec(q).Error; err != nil {
			return fmt.Errorf("migrate aggregate_scores.%s to double: %w", col, err)
		}
	}
	return nil
}

// columnDataType 经 Migrator.ColumnTypes 查列当前数据类型（Migrator 自带
// 库/schema 限定，替代裸查 information_schema 的同名表误命中面），失败返回 error。
func columnDataType(db *gorm.DB, model any, col string) (string, error) {
	cols, err := db.Migrator().ColumnTypes(model)
	if err != nil {
		return "", fmt.Errorf("read column types: %w", err)
	}
	for _, c := range cols {
		if c.Name() == col {
			return c.DatabaseTypeName(), nil
		}
	}
	return "", fmt.Errorf("column %s not found", col)
}

// migrateDimensionDeletedCode 收敛 dimensions.code 唯一索引落地前的存量数据：
// 软删行改写占位码（原code__D<id>）释放原 code，活跃重复 code 保留首行、其余加 __DUP<id> 后缀。
// 须在 AutoMigrate 之前执行（重复数据会让建唯一索引直接失败）。幂等：已带后缀的行跳过。
func migrateDimensionDeletedCode(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&domain.Dimension{}) || !m.HasColumn(&domain.Dimension{}, "DeletedAt") {
		return nil
	}
	var rows []struct {
		ID        int64
		Code      string
		DeletedAt gorm.DeletedAt
	}
	if err := db.Model(&domain.Dimension{}).Unscoped().
		Select("id, code, deleted_at").
		Order("code ASC, id ASC").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("scan dimensions for code rewrite: %w", err)
	}
	seenActive := map[string]bool{}
	for _, row := range rows {
		var code string
		if row.DeletedAt.Valid {
			if strings.HasSuffix(row.Code, fmt.Sprintf("__D%d", row.ID)) {
				continue
			}
			code = suffixedCode(row.Code, fmt.Sprintf("__D%d", row.ID))
		} else {
			if seenActive[row.Code] {
				code = suffixedCode(row.Code, fmt.Sprintf("__DUP%d", row.ID))
			} else {
				seenActive[row.Code] = true
				continue
			}
		}
		if err := db.Unscoped().Model(&domain.Dimension{}).
			Where("id = ?", row.ID).
			Update("code", code).Error; err != nil {
			return fmt.Errorf("rewrite dimension %d code: %w", row.ID, err)
		}
	}
	return nil
}

// suffixedCode 截前缀再接后缀，保证结果 ≤64 rune（varchar(64)）且后缀完整可区分。
func suffixedCode(code, suffix string) string {
	room := 64 - utf8.RuneCountInString(suffix)
	if utf8.RuneCountInString(code) > room {
		return string([]rune(code)[:room]) + suffix
	}
	return code + suffix
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
