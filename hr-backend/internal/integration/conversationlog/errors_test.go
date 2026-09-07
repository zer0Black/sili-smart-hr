package conversationlog

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestUpstreamErrorIsNotFound 断言 IsNotFound 白名单识别（specs §2.3 记录不存在识别）：
// 中文文案原样命中，英文大小写变体小写归一后命中，其余文案不命中。
func TestUpstreamErrorIsNotFound(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"会话不存在", true},
		{"conversation not found", true},
		{"Conversation Not Found", true},
		{"Conversation not found", true},
		{" upstream internal error ", false},
		{"", false},
		{"   ", false},
		{"  会话不存在  ", true},                   // trim 后命中
		{"会话不存", false},                       // 前缀截断不命中
		{"会话不存在。", false},                     // 多标点不命中
		{"the conversation not found", false}, // 子串不等于白名单条目
	}
	for _, tc := range cases {
		e := &UpstreamError{Msg: tc.msg}
		if got := e.IsNotFound(); got != tc.want {
			t.Errorf("IsNotFound(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestUpstreamErrorUnwrap 断言 *UpstreamError 经 Unwrap 归一到哨兵 ErrUpstreamBusiness，
// 且 errors.As 可取回载体读 Msg（specs §2.3 UpstreamError 载体定义）。
func TestUpstreamErrorUnwrap(t *testing.T) {
	ue := &UpstreamError{Msg: "x"}
	if !errors.Is(ue, ErrUpstreamBusiness) {
		t.Error("errors.Is(&UpstreamError{Msg: x}, ErrUpstreamBusiness) = false, want true")
	}
	var got *UpstreamError
	if !errors.As(&UpstreamError{Msg: "会话不存在"}, &got) {
		t.Fatal("errors.As failed to extract *UpstreamError")
	}
	if got.Msg != "会话不存在" {
		t.Errorf("extracted Msg = %q, want 会话不存在", got.Msg)
	}
}

// TestSentinelDistinct 断言 7 个 sentinel 两两 errors.Is 仅自反为 true，
// 保证分类互斥、调用方 switch 分流不歧义。
func TestSentinelDistinct(t *testing.T) {
	sentinels := []error{
		ErrNotConfigured,
		ErrUnauthorized,
		ErrUpstreamBusiness,
		ErrBadRequest,
		ErrNetwork,
		ErrDecode,
		ErrUnexpectedStatus,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			want := i == j
			if got := errors.Is(a, b); got != want {
				t.Errorf("errors.Is(sentinel[%d], sentinel[%d]) = %v, want %v", i, j, got, want)
			}
		}
	}
}

// TestUpstreamErrorErrorMessage 断言 Error() 输出携带上游 Msg 原文，便于日志定位（specs §2.3）。
func TestUpstreamErrorErrorMessage(t *testing.T) {
	e := &UpstreamError{Msg: "会话不存在"}
	s := e.Error()
	if s == "" {
		t.Fatal("Error() returned empty string")
	}
	if !strings.Contains(s, "会话不存在") {
		t.Errorf("Error() = %q, want it to contain upstream Msg", s)
	}
}

// TestWrapErr 断言 wrapErr 包装保留原因链：
// errors.Is 同时命中 sentinel 与原始错误（specs §2.3「所有错误经包装保留原始原因链」），
// 且 Error() 只输出 cause 文案，sentinel 英文串不重复出现（1304 透传前端口径）。
func TestWrapErr(t *testing.T) {
	cause := errors.New("dial tcp: connection refused")
	err := wrapErr(ErrNetwork, cause)

	if !errors.Is(err, ErrNetwork) {
		t.Error("errors.Is(wrapErr(ErrNetwork, cause), ErrNetwork) = false, want true")
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(wrapErr(ErrNetwork, cause), cause) = false, want true")
	}
	if !strings.Contains(err.Error(), "dial tcp: connection refused") {
		t.Errorf("Error() = %q, want it to contain cause message", err.Error())
	}
	if strings.Contains(err.Error(), ErrNetwork.Error()) {
		t.Errorf("Error() = %q, sentinel text must not leak into message", err.Error())
	}

	// 换一个 sentinel 交叉验证泛化性。
	err2 := wrapErr(ErrDecode, cause)
	if !errors.Is(err2, ErrDecode) || errors.Is(err2, ErrNetwork) {
		t.Error("wrapErr(ErrDecode, cause) classified wrong sentinel")
	}
}

// TestWrapErrUpstreamNoDupPrefix 断言 UpstreamError 经 wrapErr 包装后哨兵文案
// 恰出现零次（Error() 只输出 upstream: Msg，双重前缀已消除）。
func TestWrapErrUpstreamNoDupPrefix(t *testing.T) {
	err := wrapErr(ErrUpstreamBusiness, &UpstreamError{Msg: "会话不存在"})
	if got, want := err.Error(), "upstream: 会话不存在"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, ErrUpstreamBusiness) {
		t.Error("errors.Is(wrapped, ErrUpstreamBusiness) = false, want true")
	}
	var ue *UpstreamError
	if !errors.As(err, &ue) || ue.Msg != "会话不存在" {
		t.Error("errors.As must extract *UpstreamError with Msg intact")
	}
}

// TestUpstreamErrorWrappedByFmtErrorf 断言 UpstreamError 经 wrapErr 语义包装后
// errors.As 仍可取回载体，且 errors.Is 命中两个方向。
func TestUpstreamErrorWrappedByFmtErrorf(t *testing.T) {
	inner := &UpstreamError{Msg: "会话不存在"}
	err := fmt.Errorf("list sessions: %w", inner)

	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatal("errors.As through fmt.Errorf wrap failed")
	}
	if !ue.IsNotFound() {
		t.Error("wrapped UpstreamError.IsNotFound() = false, want true")
	}
	if !errors.Is(err, ErrUpstreamBusiness) {
		t.Error("errors.Is(wrapped, ErrUpstreamBusiness) = false, want true")
	}
}
