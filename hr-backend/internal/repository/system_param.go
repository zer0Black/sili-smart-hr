// SystemParamReader 实现，共享 SYS 参数读取契约见 specs TECH_003 04_model §3.2。
package repository

import (
	"encoding/json"
	"errors"

	"log/slog"

	"sili-smart-hr/backend/internal/domain"

	"gorm.io/gorm"
)

// SystemParamReader 是共享 SYS 参数的读取接口（03_api_interface.md §5.2）。
type SystemParamReader interface {
	// ReadStringArray 按键读字符串数组参数。行不存在或值反序列化失败时返回
	// 出厂默认（由调用方以 extractor 包常量注入），DB 非唯一权威。
	ReadStringArray(key string) ([]string, error)
	// ReadStringArrays 批量按键读（单次 DB 往返），逐键语义与 ReadStringArray
	// 一致：key → 解析值，键缺失/损坏/DB 故障时该键缺席，error 仅在真实 DB 故障
	// 时非 nil（此时 map 为 nil）。
	ReadStringArrays(keys ...string) (map[string][]string, error)
}

type systemParamReader struct {
	db        *gorm.DB
	defaulter map[string][]string
}

// NewSystemParamReader 构造，defaulter 提供键缺失/损坏时的回退值，
// 形如 map[string][]string{"extractor.redact_patterns": extractor.RedactPatterns()}，
// 由装配层构造注入（providers.go）。inject_prefixes 刻意不注册（追加语义下
// 无出厂回退概念，回退 nil 即空追加集）。
func NewSystemParamReader(db *gorm.DB, defaulter map[string][]string) SystemParamReader {
	return &systemParamReader{db: db, defaulter: defaulter}
}

// ReadStringArray 按 uk_param_key 点查。行存在且 JSON 合法返回解析值（空数组 `[]`
// 合法原样返回）；行不存在或反序列化失败回退 defaulter[key]，回退属正常路径
// err=nil（04 §3.2 键缺失回退）；error 仅在真实 DB 故障时非 nil。
func (r *systemParamReader) ReadStringArray(key string) ([]string, error) {
	var rec domain.SystemParam
	err := r.db.Where("param_key = ?", key).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.fallback(key), nil
	}
	if err != nil {
		return nil, err
	}

	var values []string
	if uerr := json.Unmarshal([]byte(rec.ParamValue), &values); uerr != nil {
		// 回退即正常路径，损坏只记 WARN 不上抛（04 §3.2）。
		slog.Warn("system param value corrupted, fallback to default",
			"param_key", key, "unmarshal_error", uerr.Error())
		return r.fallback(key), nil
	}
	if values == nil {
		// param_value 为 "null" 等非数组字面时 Unmarshal 后仍为 nil，统一按损坏回退。
		return r.fallback(key), nil
	}
	return values, nil
}

// fallback 取回退值，key 无映射时返回 nil（键未注册 defaulter，调用方按 nil
// 自行裁定回退口径，防漏注册时空集被当显式清空）。返回副本：defaulter 是进程级
// 共享的出厂默认，直引底层数组会让调用方原地修改静默污染全进程回退行为。
func (r *systemParamReader) fallback(key string) []string {
	if v, ok := r.defaulter[key]; ok {
		cp := make([]string, len(v))
		copy(cp, v)
		return cp
	}
	return nil
}

// ReadStringArrays 单次往返批量读（WHERE param_key IN），逐键回退与损坏口径
// 同 ReadStringArray；真实 DB 故障返回 (nil, err)。
func (r *systemParamReader) ReadStringArrays(keys ...string) (map[string][]string, error) {
	if len(keys) == 0 {
		return map[string][]string{}, nil
	}
	var recs []domain.SystemParam
	if err := r.db.Where("param_key IN ?", keys).Find(&recs).Error; err != nil {
		return nil, err
	}
	byKey := make(map[string]string, len(recs))
	for _, rec := range recs {
		byKey[rec.ParamKey] = rec.ParamValue
	}
	out := make(map[string][]string, len(keys))
	for _, key := range keys {
		raw, ok := byKey[key]
		if !ok {
			continue // 键缺失：缺席由调用方回退，见接口注释
		}
		var values []string
		if uerr := json.Unmarshal([]byte(raw), &values); uerr != nil || values == nil {
			// 损坏（含 "null" 字面）只记 WARN，按缺失回退（04 §3.2 同口径）。
			slog.Warn("system param value corrupted, fallback to default",
				"param_key", key)
			continue
		}
		out[key] = values
	}
	return out, nil
}
