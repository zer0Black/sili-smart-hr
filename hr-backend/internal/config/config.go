// Package config 负责加载与承载应用配置。
//
// 加载策略（与 architecture.md 4.1 一致）：configs/config.yaml 承载非敏感配置；
// SQL_DSN、JWT_SECRET 等敏感项由环境变量提供，启动时覆盖或单独读取。
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// defaultJWTSecret 是本地开发兜底密钥，仅用于开箱即跑的 SQLite 姿态。
// 生产（PostgreSQL/MySQL）启动时若仍使用此密钥，main.go 会拒绝启动。
const defaultJWTSecret = "sili-smart-hr-dev-jwt-secret-7f3a9e2b1c4d6e8a"

// defaultLLMSecretKey 是 LLM 对称加密（AES 派生密钥）的本地开发兜底值。
// 与 JWT_SECRET 同范式：常量在此公开 + configs/config.yaml 同值公开，
// 生产（PostgreSQL/MySQL）启动时若仍使用此值，main.go 会拒绝启动。
const defaultLLMSecretKey = "sili-smart-hr-dev-llm-secret-3b7f1e9d4a2c8f60"

// Config 是全局配置根，结构对齐 configs/config.yaml。
type Config struct {
	Server      ServerConfig        `mapstructure:"server"`
	CORS        CORSConfig          `mapstructure:"cors"`
	Redis       RedisConfig         `mapstructure:"redis"`
	JWT         JWTConfig           `mapstructure:"jwt"`
	Asynq       AsynqConfig         `mapstructure:"asynq"`
	Log         LogConfig           `mapstructure:"log"`
	SQL         SQLConfig           `mapstructure:"sql"`
	Snowflake   SnowflakeConfig     `mapstructure:"snowflake"`
	LLM         LLMSettings         `mapstructure:"llm"`
	Integration IntegrationSettings `mapstructure:"integration"`
}

// ServerConfig 承载 HTTP 服务监听配置。
type ServerConfig struct {
	Port int `mapstructure:"port"`
}

// CORSConfig 承载跨域放通来源。
type CORSConfig struct {
	AllowedOrigins []string `mapstructure:"allowed_origins"`
}

// RedisConfig 承载 Redis 连接信息（Asynq 底层存储，硬依赖）。
type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// JWTConfig 承载 JWT 签名密钥与有效期。
// Secret 生产必须由 JWT_SECRET 环境变量覆盖。
type JWTConfig struct {
	Secret string `mapstructure:"secret"`
	TTL    string `mapstructure:"ttl"`
}

// AsynqConfig 承载 Asynq worker 并发度。
type AsynqConfig struct {
	Concurrency int `mapstructure:"concurrency"`
}

// LogConfig 承载日志级别。
type LogConfig struct {
	Level string `mapstructure:"level"`
}

// SQLConfig 承载数据库连接信息。
// DSN 由 SQL_DSN 环境变量提供：未配置或 local 前缀走 SQLite，postgres 前缀走 PostgreSQL，其余走 MySQL。
type SQLConfig struct {
	DSN          string `mapstructure:"dsn"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxLifetime  string `mapstructure:"max_lifetime"`
}

// SnowflakeConfig 承载雪花 ID 生成器配置。
// NodeID 多实例部署时须唯一（0 到 1023），单进程姿态用默认 1。
type SnowflakeConfig struct {
	NodeID int64 `mapstructure:"node_id"`
}

// LLMSettings 承载大模型相关配置。
// SecretKey 用于 AES 对称加密派生（crypto.DeriveKey），生产必须由 LLM_SECRET_KEY 环境变量覆盖。
// 用 Settings 后缀避免与 domain.LLMConfig 同名。
type LLMSettings struct {
	SecretKey string `mapstructure:"secret_key"`
}

// IntegrationSettings 承载外部系统集成配置。
// SmartAPIBaseURL 指向兄弟系统 sili-smart-api（specs §7.1 外部集成系统），
// A3 人员列表查询与 C4 连通验证需真实调用该地址。
// 开发默认值指向 localhost:8081，生产由 SMART_API_BASE_URL 环境变量覆盖，不熔断。
type IntegrationSettings struct {
	SmartAPIBaseURL string `mapstructure:"smart_api_base_url"`
}

// Load 从给定路径加载 yaml 配置，并绑定环境变量。
// path 为空时按默认路径 configs/config.yaml 查找。
func Load(path string) (*Config, error) {
	if path == "" {
		path = "configs/config.yaml"
	}
	v := viper.New()
	v.SetConfigFile(path)
	// 敏感项与运行时项走环境变量。
	if err := v.BindEnv("sql.dsn", "SQL_DSN"); err != nil {
		return nil, fmt.Errorf("bind SQL_DSN: %w", err)
	}
	if err := v.BindEnv("jwt.secret", "JWT_SECRET"); err != nil {
		return nil, fmt.Errorf("bind JWT_SECRET: %w", err)
	}
	if err := v.BindEnv("sql.max_idle_conns", "SQL_MAX_IDLE_CONNS"); err != nil {
		return nil, fmt.Errorf("bind SQL_MAX_IDLE_CONNS: %w", err)
	}
	if err := v.BindEnv("sql.max_open_conns", "SQL_MAX_OPEN_CONNS"); err != nil {
		return nil, fmt.Errorf("bind SQL_MAX_OPEN_CONNS: %w", err)
	}
	if err := v.BindEnv("sql.max_lifetime", "SQL_MAX_LIFETIME"); err != nil {
		return nil, fmt.Errorf("bind SQL_MAX_LIFETIME: %w", err)
	}
	if err := v.BindEnv("redis.addr", "REDIS_ADDR"); err != nil {
		return nil, fmt.Errorf("bind REDIS_ADDR: %w", err)
	}
	if err := v.BindEnv("server.port", "SERVER_PORT"); err != nil {
		return nil, fmt.Errorf("bind SERVER_PORT: %w", err)
	}
	if err := v.BindEnv("snowflake.node_id", "SNOWFLAKE_NODE_ID"); err != nil {
		return nil, fmt.Errorf("bind SNOWFLAKE_NODE_ID: %w", err)
	}
	if err := v.BindEnv("llm.secret_key", "LLM_SECRET_KEY"); err != nil {
		return nil, fmt.Errorf("bind LLM_SECRET_KEY: %w", err)
	}
	if err := v.BindEnv("integration.smart_api_base_url", "SMART_API_BASE_URL"); err != nil {
		return nil, fmt.Errorf("bind SMART_API_BASE_URL: %w", err)
	}

	v.SetDefault("sql.dsn", "")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// applyDefaults 补齐缺省值，保证空配置也能以开发姿态运行。
func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Redis.Addr == "" {
		c.Redis.Addr = "localhost:6379"
	}
	if c.JWT.Secret == "" {
		// 兜底开发默认值，仅用于开箱即跑的本地 SQLite 姿态；
		// 生产（postgres/mysql）启动时若仍用它，main.go 会拒绝启动。
		c.JWT.Secret = defaultJWTSecret
	}
	if c.JWT.TTL == "" {
		c.JWT.TTL = "24h"
	}
	if c.Asynq.Concurrency == 0 {
		c.Asynq.Concurrency = 10
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.SQL.DSN == "" {
		// 未配置 SQL_DSN 时回退 SQLite，免起库的开发姿态。
		c.SQL.DSN = "local"
	}
	if c.Snowflake.NodeID == 0 {
		// 单进程默认节点 1；多实例部署由 SNOWFLAKE_NODE_ID 区分。
		c.Snowflake.NodeID = 1
	}
	if c.LLM.SecretKey == "" {
		// 兜底开发默认值，仅用于开箱即跑的本地 SQLite 姿态；
		// 生产（postgres/mysql）启动时若仍用它，main.go 会拒绝启动。
		c.LLM.SecretKey = defaultLLMSecretKey
	}
	if c.Integration.SmartAPIBaseURL == "" {
		// 开发默认指向本地兄弟系统；生产由 SMART_API_BASE_URL 覆盖。
		// 外部系统地址允许默认值上线，调用失败由 1305/1304 业务错误承接，不熔断。
		c.Integration.SmartAPIBaseURL = "http://localhost:8081"
	}
}

// IsSQLite 判断当前 DSN 是否命中 SQLite 回退路径。
func (c *Config) IsSQLite() bool {
	return c.SQL.DSN == "" || strings.HasPrefix(c.SQL.DSN, "local")
}

// IsPostgres 判断当前 DSN 是否走 PostgreSQL。
func (c *Config) IsPostgres() bool {
	return strings.HasPrefix(c.SQL.DSN, "postgres://") || strings.HasPrefix(c.SQL.DSN, "postgresql://")
}

// IsDefaultJWTSecret 判断是否仍在使用开发兜底密钥（即未由 JWT_SECRET 环境变量覆盖）。
func (c *Config) IsDefaultJWTSecret() bool {
	return c.JWT.Secret == defaultJWTSecret
}

// IsDefaultLLMSecretKey 判断是否仍在使用开发兜底 LLM 密钥（即未由 LLM_SECRET_KEY 环境变量覆盖）。
func (c *Config) IsDefaultLLMSecretKey() bool {
	return c.LLM.SecretKey == defaultLLMSecretKey
}
