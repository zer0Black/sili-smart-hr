// Package repository 白盒守护测试：updateRow 列清单守护（唯一冲突判定已上提
// pkg/dberr，判定用例随迁该包测试）。
package repository

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"gorm.io/gorm/schema"

	"sili-smart-hr/backend/internal/domain"
)

// TestUpdateColumnsMatchesDomainModel 守护手写翻转列清单与 domain.SessionFeature 绑定：
// 反射枚举模型业务列（剔除 id/created_at/session_key，updated_at 显式纳入使 map 值恒变），
// 断言 updateColumns 键集合与之相等，模型加列漏改 map 时此处立即报警。
func TestUpdateColumnsMatchesDomainModel(t *testing.T) {
	ns := schema.NamingStrategy{}
	modelType := reflect.TypeOf(domain.SessionFeature{})
	want := map[string]interface{}{}
	for i := 0; i < modelType.NumField(); i++ {
		f := modelType.Field(i)
		tag := f.Tag.Get("gorm")
		// id/created_at 为自动列；session_key 是幂等定位键（WHERE 侧）；
		// updated_at 显式纳入翻转列（RowsAffected 判定依赖值恒变），单独补入。
		if strings.Contains(tag, "autoCreateTime") || tag == "primaryKey" || f.Name == "SessionKey" || f.Name == "UpdatedAt" {
			continue
		}
		want[ns.ColumnName("", f.Name)] = nil
	}
	want["updated_at"] = nil

	got := updateColumns(&domain.SessionFeature{})
	if len(got) != len(want) {
		t.Fatalf("updateColumns 列数 want %d (%v), got %d (%v)", len(want), sortedCols(want), len(got), sortedCols(got))
	}
	for col := range want {
		if _, ok := got[col]; !ok {
			t.Fatalf("updateColumns 缺列 %q（模型加列后须同步手写 map）, got %v", col, sortedCols(got))
		}
	}
	for col := range got {
		if _, ok := want[col]; !ok {
			t.Fatalf("updateColumns 多出列 %q（模型删列/改名后须同步清理）, want %v", col, sortedCols(want))
		}
	}
}

// sortedCols 排序列名集合，保证失败信息稳定可比。
func sortedCols(m map[string]interface{}) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
