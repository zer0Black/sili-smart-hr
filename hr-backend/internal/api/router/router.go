// Package router 负责路由注册与中间件挂载。
package router

import (
	"time"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/api/middleware"
	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// NewRouter 组装 gin 引擎。
//
// 限流阈值：公钥接口 60/min（IP）；登录叠两层，IP 30/min 反滥用 + username 20/min 反爆破；
// 系统初始化 status 60/min（IP）、initialize 10/min（IP）；系统状态 status 60/min（IP）、
// health-check account 10/min（触发外部探测收紧）。超限返回 429。受保护接口归入 /api 鉴权组。
// setup 两接口与 system status 属公开路由，不挂 JWT（specs §1.3 / BR11、BR23）。
func NewRouter(
	cfg *config.Config,
	jwtMgr *jwt.Manager,
	accountHandler *handler.AccountHandler,
	healthHandler *handler.HealthHandler,
	setupHandler *handler.SetupHandler,
	systemHandler *handler.SystemHandler,
	dimensionHandler *handler.DimensionHandler,
	assessmentConfigHandler *handler.AssessmentConfigHandler,
	llmConfigHandler *handler.LLMConfigHandler,
	integrationSecretHandler *handler.IntegrationSecretHandler,
	rdb *redis.Client,
) *gin.Engine {
	r := gin.New()
	r.Use(
		middleware.Recovery(),
		middleware.Logger(),
		middleware.CORS(cfg.CORS.AllowedOrigins),
	)

	r.GET("/health", healthHandler.Health)
	r.GET(
		"/api/auth/public-key",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:pk:", middleware.ClientIPKey, 60, time.Minute),
		accountHandler.PublicKey,
	)
	r.POST(
		"/api/login",
		// IP 维度在前（不读 body），username 维度在后（peek body 并 rewind）。
		middleware.RateLimit(rdb, "sili-smart-hr:rl:login-ip:", middleware.ClientIPKey, 30, time.Minute),
		middleware.RateLimit(rdb, "sili-smart-hr:rl:login-user:", middleware.JSONFieldKey("username"), 20, time.Minute),
		accountHandler.Login,
	)
	// 系统初始化：status 60/min IP（specs §1.4 / BR10）、initialize 10/min IP（一次性提交，更严格）。
	r.GET(
		"/api/setup/status",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:setup-status:", middleware.ClientIPKey, 60, time.Minute),
		setupHandler.Status,
	)
	r.POST(
		"/api/setup/initialize",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:setup-init:", middleware.ClientIPKey, 10, time.Minute),
		setupHandler.Initialize,
	)
	// 系统状态摘要：公开供外部探针，IP 60/min（specs §1.4 / BR22、BR23）。
	r.GET(
		"/api/system/status",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:sys-status:", middleware.ClientIPKey, 60, time.Minute),
		systemHandler.GetSummary,
	)

	auth := r.Group("/api", middleware.JWT(jwtMgr))
	auth.GET("/me", accountHandler.Me)
	// 账号台账：POST 以路径后缀区分动作。
	auth.GET("/accounts", accountHandler.List)
	auth.POST("/accounts/create", accountHandler.Create)
	auth.POST("/accounts/update", accountHandler.Update)
	auth.POST("/accounts/delete", accountHandler.Delete)
	auth.POST("/accounts/toggle-enabled", accountHandler.ToggleEnabled)
	auth.POST("/accounts/reset-password", accountHandler.ResetPassword)
	// 组件健康测试：鉴权接口，触发外部大模型与集成探测，account 10/min 收紧（specs §1.4 / BR22、BR23）。
	auth.POST(
		"/system/health-check",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:sys-health:", middleware.AccountIDKey, 10, time.Minute),
		systemHandler.HealthCheck,
	)
	// 能力维度域：全部接口 JWT 鉴权挂 auth 组（specs §2.3 / BR1）。
	auth.GET("/dimensions/tree", dimensionHandler.Tree)
	auth.GET("/dimensions/:id", dimensionHandler.Detail)
	auth.POST("/dimensions/create", dimensionHandler.Create)
	auth.POST("/dimensions/update", dimensionHandler.Update)
	auth.POST("/dimensions/delete", dimensionHandler.Delete)
	auth.GET("/dimensions/activity-rule", dimensionHandler.ActivityRule)
	auth.POST("/dimensions/activity-rule/save", dimensionHandler.SaveActivityRule)
	// 评估周期配置域：三接口 JWT 鉴权挂 auth 组（specs §2.3 + 03 §A1/A2/A3 / BR1）。
	auth.GET("/assessment-config", assessmentConfigHandler.Get)
	auth.POST("/assessment-config/save", assessmentConfigHandler.Save)
	auth.GET("/staffs", assessmentConfigHandler.Staffs)
	// 大模型配置域：六接口 JWT 鉴权挂 auth 组（specs §2.3 + 03 §B1-B6 / BR1）。
	// /llm-configs/:id 路径参数路由与 /llm-configs 的 List 路由不冲突（Gin 静态优先）。
	auth.GET("/llm-configs", llmConfigHandler.List)
	auth.POST("/llm-configs/create", llmConfigHandler.Create)
	auth.POST("/llm-configs/update", llmConfigHandler.Update)
	auth.POST("/llm-configs/delete", llmConfigHandler.Delete)
	auth.POST("/llm-configs/enable", llmConfigHandler.Enable)
	auth.GET("/llm-configs/:id", llmConfigHandler.Detail)
	// 集成密钥域：四接口 JWT 鉴权挂 auth 组（specs §2.3 + 03 §C1-C4 / BR1）。
	auth.GET("/integration-secret", integrationSecretHandler.Get)
	auth.GET("/integration-secret/detail", integrationSecretHandler.Detail)
	auth.POST("/integration-secret/update", integrationSecretHandler.Update)
	auth.POST("/integration-secret/test", integrationSecretHandler.Test)

	return r
}
