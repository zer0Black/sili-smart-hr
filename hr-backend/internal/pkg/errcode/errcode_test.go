package errcode_test

import (
	"testing"

	"sili-smart-hr/backend/internal/pkg/errcode"
)

// TestMessages 验证错误码常量数值与默认文案映射。
func TestMessages(t *testing.T) {
	tests := []struct {
		name string
		code int
		want int
		msg  string
	}{
		// 既有用例回归保护
		{"Success", errcode.Success, 0, "ok"},
		{"InvalidCredentials", errcode.InvalidCredentials, 1001, "invalid credentials"},
		{"AccountDisabled", errcode.AccountDisabled, 1002, "account disabled"},
		{"Unauthorized", errcode.Unauthorized, 1003, "unauthorized"},
		{"BadRequest", errcode.BadRequest, 1400, "bad request"},
		{"Internal", errcode.Internal, 1500, "internal error"},

		// 新增用户管理错误码
		{"AccountNotFound", errcode.AccountNotFound, 1004, "account not found"},
		{"UsernameExists", errcode.UsernameExists, 1005, "username exists"},
		{"LastEnabledAccount", errcode.LastEnabledAccount, 1006, "last enabled account"},
		{"PasswordInvalid", errcode.PasswordInvalid, 1007, "password invalid"},

		// 系统初始化域 1101/1102（段位避让回归保护，11xx 不归 dimension）
		{"SystemAlreadyInitialized", errcode.SystemAlreadyInitialized, 1101, "system already initialized"},
		{"EnvironmentNotReady", errcode.EnvironmentNotReady, 1102, "environment not ready"},

		// dimension 域 1201-1208（P2_DIM_001 §4.1.4 §4.2.4）
		{"DimensionNotFound", errcode.DimensionNotFound, 1201, "dimension not found"},
		{"DimensionCodeExists", errcode.DimensionCodeExists, 1202, "dimension code exists"},
		{"DimensionEnabledNotDeletable", errcode.DimensionEnabledNotDeletable, 1203, "dimension enabled not deletable"},
		{"DimensionVersionConflict", errcode.DimensionVersionConflict, 1204, "dimension version conflict"},
		{"DimensionNameInvalid", errcode.DimensionNameInvalid, 1205, "dimension name invalid"},
		{"DimensionAnchorRequired", errcode.DimensionAnchorRequired, 1206, "dimension anchor required"},
		{"DimensionPromptRequired", errcode.DimensionPromptRequired, 1207, "dimension prompt required"},
		{"ActivityThresholdInvalid", errcode.ActivityThresholdInvalid, 1208, "activity threshold invalid"},

		// config 域 1301-1306（P2_SYS_001 §4.1 错误码总表，13xx 段避让 11xx/12xx）
		{"LLMConfigNotFound", errcode.LLMConfigNotFound, 1301, "llm config not found"},
		{"LastLLMConfig", errcode.LastLLMConfig, 1302, "last llm config"},
		{"IntegrationSecretNotConfigured", errcode.IntegrationSecretNotConfigured, 1303, "integration secret not configured"},
		{"IntegrationSecretTestFailed", errcode.IntegrationSecretTestFailed, 1304, "integration secret test failed"},
		{"StaffListUnavailable", errcode.StaffListUnavailable, 1305, "staff list unavailable"},
		{"ConfigVersionConflict", errcode.ConfigVersionConflict, 1306, "config version conflict"},
		{"SecretDecryptFailed", errcode.SecretDecryptFailed, 1307, "secret decrypt failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code != tt.want {
				t.Errorf("%s code = %d, want %d", tt.name, tt.code, tt.want)
			}
			if got := errcode.Message(tt.code); got != tt.msg {
				t.Errorf("%s message = %q, want %q", tt.name, got, tt.msg)
			}
		})
	}
}

// TestMessageUnknownFallback 验证未注册码兜底返回 "error"。
// 1399 在 config 域 13xx 段内但未注册，用于锁定段位边界与回退行为（1307 已被 SecretDecryptFailed 占用）。
func TestMessageUnknownFallback(t *testing.T) {
	if got := errcode.Message(9999); got != "error" {
		t.Errorf("unknown code message = %q, want \"error\"", got)
	}
	if got := errcode.Message(1399); got != "error" {
		t.Errorf("1399 message = %q, want \"error\"", got)
	}
}
