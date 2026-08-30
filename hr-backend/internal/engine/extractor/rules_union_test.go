package extractor

import (
	"slices"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/engine/extractor/rules/baseline"
)

// 前缀去重组装器测试（specs §2.4 能力1 第 2 条、03 §5.1/§5.2）：
// InjectPrefixes / assistantNarrowedPrefixes 从平面 factory 拷贝改为
// 通用层 ∪ 各客户端 UserPrefixes/NonUserPrefixes 并集组装（去重后），
// 出厂态等集关系由本测试守护（基线平面变量已拆除，快照集单源收敛在
// rules/baseline 包，与 rules 包完备性断言共用同一份裁判）。

// TestInjectPrefixesUnionAssembler 核心断言：组装器并集与基线快照集集合相等，
// 且无重复元素（重复贡献如 generic 与 claudecode 均含 Note: 形态的字面共享）。
func TestInjectPrefixesUnionAssembler(t *testing.T) {
	got := InjectPrefixes()
	if len(got) == 0 {
		t.Fatal("InjectPrefixes() 返回空集，并集组装器未生效")
	}
	// 去重收敛：并集产物零重复（specs §2.4 能力1 注意事项）。
	if d := baseline.SortedDedup(got); len(d) != len(got) {
		t.Errorf("InjectPrefixes() 含重复元素：原 %d 条，去重后 %d 条", len(got), len(d))
	}
	// 快照集自身零重复（锚定集有效性前提）。
	if d := baseline.SortedDedup(baseline.UserPrefixes); len(d) != len(baseline.UserPrefixes) {
		t.Fatalf("快照锚定集自身含重复：%d 条去重后 %d 条", len(baseline.UserPrefixes), len(d))
	}
	// 排序后逐元素相等（03 §5.1：并集 == 基线出厂集）。
	want := baseline.SortedDedup(baseline.UserPrefixes)
	have := baseline.SortedDedup(got)
	if !slices.Equal(have, want) {
		missing, extra := baseline.Diff(want, have)
		t.Errorf("InjectPrefixes() 并集与基线快照集不等：缺 %d 条 %v；多 %d 条 %v",
			len(missing), missing, len(extra), extra)
	}
}

// TestAssistantNarrowedUnion 核心断言：收窄集并集 == 基线 assistantPrefixesFactory
// 四元素，无重复。
func TestAssistantNarrowedUnion(t *testing.T) {
	got := assistantNarrowedPrefixes()
	if len(got) != len(baseline.NonUserPrefixes) {
		t.Errorf("assistantNarrowedPrefixes() = %d 条 %v，期望基线四元素 %v",
			len(got), got, baseline.NonUserPrefixes)
	}
	if d := baseline.SortedDedup(got); len(d) != len(got) {
		t.Errorf("assistantNarrowedPrefixes() 含重复元素：%v", got)
	}
	if have, want := baseline.SortedDedup(got), baseline.SortedDedup(baseline.NonUserPrefixes); !slices.Equal(have, want) {
		t.Errorf("assistantNarrowedPrefixes() = %v，期望 %v", got, baseline.NonUserPrefixes)
	}
}

// TestPrefixUnionNoDuplicateContribution 验证重复前缀字面不产生重复元素：
// 构造含重复贡献的输入集跑组装收敛逻辑（map 去重证据），generic 的 Note: 与
// 快照中的 Note: 同字面形态即天然重复贡献样本。
func TestPrefixUnionNoDuplicateContribution(t *testing.T) {
	got := InjectPrefixes()
	if n := strings.Count("\x00"+strings.Join(got, "\x00")+"\x00", "\x00Note:\x00"); n != 1 {
		t.Errorf("并集产物中 Note: 出现 %d 次，期望 1 次（多客户端同字面前缀须去重）", n)
	}
	n := 0
	for _, p := range got {
		if p == ContinuationPrefix {
			n++
		}
	}
	if n != 1 {
		t.Errorf("并集产物中 %q 出现 %d 次，期望 1 次", ContinuationPrefix, n)
	}
}

// TestInjectPrefixesReturnsCopy 断言返回拷贝：调用方写不穿透并集源（每次调用独立）。
func TestInjectPrefixesReturnsCopy(t *testing.T) {
	first := InjectPrefixes()
	first[0] = "<write-probe>"
	if again := InjectPrefixes(); again[0] == "<write-probe>" {
		t.Errorf("外部写穿透组装器产物：again[0] = %q", again[0])
	}
}
