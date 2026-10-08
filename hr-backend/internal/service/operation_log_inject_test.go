package service

import (
	"context"
	"reflect"
	"testing"

	"sili-smart-hr/backend/internal/domain"
)

func TestFmtChange(t *testing.T) {
	cases := []struct {
		name      string
		field     string
		before    any
		after     any
		wantField string
		wantBef   string
		wantAft   string
	}{
		// 核心断言：数字统一字符串化（03 §1.7）
		{"数字值", "聚合权重", 30, 50, "聚合权重", "30", "50"},
		// 布尔同口径转字符串
		{"布尔值", "启用", true, false, "启用", "true", "false"},
		// 核心断言：nil 输出空串（新增类 before 空兜底）
		{"新增类before空", "描述", nil, "新描述", "描述", "", "新描述"},
		// 删除类 after 空
		{"删除类after空", "描述", "旧描述", nil, "描述", "旧描述", ""},
		// 密钥类字段以脱敏掩码呈现：掩码是普通字符串，组装路径直接透传（specs §5.1.4 规则4）
		{"掩码透传", "API Key", "sk-****abc", "sk-****xyz", "API Key", "sk-****abc", "sk-****xyz"},
		// 字符串两侧同为 nil
		{"双侧nil", "备注", nil, nil, "备注", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fmtChange(tc.field, tc.before, tc.after)
			want := domain.ChangeItem{Field: tc.wantField, Before: tc.wantBef, After: tc.wantAft}
			if got != want {
				t.Fatalf("fmtChange(%q, %v, %v) = %+v, want %+v", tc.field, tc.before, tc.after, got, want)
			}
		})
	}
}

func TestInjectOperation(t *testing.T) {
	changes := []domain.ChangeItem{
		{Field: "聚合权重", Before: "30", After: "50"},
		{Field: "启用", Before: "true", After: "false"},
	}
	ctx := WithSink(context.Background(), &OpSink{})
	injectOperation(ctx, domain.OpModuleDimension, "维度「任务适配判断力」", "修改聚合权重", changes)

	got := SinkFromContext(ctx).Snapshot()
	if got.Module != domain.OpModuleDimension {
		t.Fatalf("Module = %q, want %q", got.Module, domain.OpModuleDimension)
	}
	if got.Target != "维度「任务适配判断力」" {
		t.Fatalf("Target = %q", got.Target)
	}
	if got.Summary != "修改聚合权重" {
		t.Fatalf("Summary = %q", got.Summary)
	}
	if !reflect.DeepEqual(got.Changes, changes) {
		t.Fatalf("Changes = %+v, want %+v", got.Changes, changes)
	}
	// changes 形态与文本形态互斥（specs §4.2.5），injectOperation 不碰 detail
	if got.Detail != "" {
		t.Fatalf("injectOperation 不应写 detail, got %q", got.Detail)
	}
}

func TestInjectOperationNoSink(t *testing.T) {
	// 无 sink 的裸 ctx 静默返回，不 panic
	injectOperation(context.Background(), domain.OpModuleDimension, "t", "s", nil)
	injectOperation(context.TODO(), "system_params", "t", "s", []domain.ChangeItem{{Field: "f"}})
}

func TestInjectDetail(t *testing.T) {
	ctx := WithSink(context.Background(), &OpSink{})
	injectDetail(ctx, domain.OpModuleQuestionBank, "题目「情境判断」", "删除题目", "删除题目「情境判断」及关联选项")

	got := SinkFromContext(ctx).Snapshot()
	if got.Module != domain.OpModuleQuestionBank {
		t.Fatalf("Module = %q, want %q", got.Module, domain.OpModuleQuestionBank)
	}
	if got.Target != "题目「情境判断」" {
		t.Fatalf("Target = %q", got.Target)
	}
	if got.Summary != "删除题目" {
		t.Fatalf("Summary = %q", got.Summary)
	}
	if got.Detail != "删除题目「情境判断」及关联选项" {
		t.Fatalf("Detail = %q", got.Detail)
	}
	// 文本形态不产生 changes（specs §4.1.4 规则3）
	if got.Changes != nil {
		t.Fatalf("injectDetail 不应写 changes, got %+v", got.Changes)
	}
}

func TestInjectDetailNoSink(t *testing.T) {
	// 无 sink 的裸 ctx 静默返回，不 panic
	injectDetail(context.Background(), domain.OpModuleAssessment, "t", "s", "d")
	injectDetail(context.TODO(), domain.OpModuleLogin, "t", "s", "")
}
