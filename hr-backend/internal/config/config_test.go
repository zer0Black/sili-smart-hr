package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"sili-smart-hr/backend/internal/config"
)

func TestDSNDialectDetection(t *testing.T) {
	cases := []struct {
		name         string
		dsn          string
		wantSQLite   bool
		wantPostgres bool
	}{
		{"empty", "", true, false},
		{"local", "local", true, false},
		{"local-suffix", "local-dev", true, false},
		{"postgres", "postgres://u:p@h:5432/db", false, true},
		{"postgresql", "postgresql://u:p@h:5432/db", false, true},
		{"mysql", "u:p@tcp(h:3306)/db", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.SQL.DSN = c.dsn
			if got := cfg.IsSQLite(); got != c.wantSQLite {
				t.Errorf("IsSQLite=%v want %v", got, c.wantSQLite)
			}
			if got := cfg.IsPostgres(); got != c.wantPostgres {
				t.Errorf("IsPostgres=%v want %v", got, c.wantPostgres)
			}
		})
	}
}

// writeEmptyYaml 写一个只含最小内容的临时 yaml，用于触发 applyDefaults 兜底。
func writeEmptyYaml(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 8080\n"), 0o644); err != nil {
		t.Fatalf("write tmp yaml: %v", err)
	}
	return p
}

// TestLLMSecretDefault 验证 LLM_SECRET_KEY 与 SMART_API_BASE_URL 的默认值兜底、
// 环境变量覆盖及 IsDefaultLLMSecretKey 判定。
func TestLLMSecretDefault(t *testing.T) {
	const wantDefaultKey = "sili-smart-hr-dev-llm-secret-3b7f1e9d4a2c8f60"
	const wantDefaultBaseURL = "http://localhost:8081"

	t.Run("empty config applies defaults", func(t *testing.T) {
		t.Setenv("LLM_SECRET_KEY", "")
		t.Setenv("SMART_API_BASE_URL", "")

		cfg, err := config.Load(writeEmptyYaml(t))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LLM.SecretKey != wantDefaultKey {
			t.Errorf("LLM.SecretKey=%q want %q", cfg.LLM.SecretKey, wantDefaultKey)
		}
		if cfg.Integration.SmartAPIBaseURL != wantDefaultBaseURL {
			t.Errorf("Integration.SmartAPIBaseURL=%q want %q", cfg.Integration.SmartAPIBaseURL, wantDefaultBaseURL)
		}
		if !cfg.IsDefaultLLMSecretKey() {
			t.Errorf("IsDefaultLLMSecretKey=false, want true (no env override)")
		}
	})

	t.Run("LLM_SECRET_KEY override clears default flag", func(t *testing.T) {
		t.Setenv("LLM_SECRET_KEY", "x")

		cfg, err := config.Load(writeEmptyYaml(t))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LLM.SecretKey != "x" {
			t.Errorf("LLM.SecretKey=%q want %q", cfg.LLM.SecretKey, "x")
		}
		if cfg.IsDefaultLLMSecretKey() {
			t.Errorf("IsDefaultLLMSecretKey=true, want false (env overridden)")
		}
	})

	t.Run("no env means default flag true", func(t *testing.T) {
		t.Setenv("LLM_SECRET_KEY", "")

		cfg, err := config.Load(writeEmptyYaml(t))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.IsDefaultLLMSecretKey() {
			t.Errorf("IsDefaultLLMSecretKey=false, want true")
		}
	})
}
