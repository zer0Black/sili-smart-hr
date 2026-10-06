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
	assessmentBatchHandler *handler.AssessmentBatchHandler,
	assessmentTestTaskHandler *handler.AssessmentTestTaskHandler,
	answerHandler *handler.AnswerHandler,
	llmConfigHandler *handler.LLMConfigHandler,
	integrationSecretHandler *handler.IntegrationSecretHandler,
	questionHandler *handler.QuestionHandler,
	questionBatchHandler *handler.QuestionBatchHandler,
	questionGenerationHandler *handler.QuestionGenerationHandler,
	scaleHandler *handler.ScaleHandler,
	profileHandler *handler.ProfileHandler,
	dashboardHandler *handler.DashboardHandler,
	workspaceHandler *handler.WorkspaceHandler,
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
	// 员工作答域：三接口挂公开路由组令牌自证（specs P2_TST_002 §4.1.6，无 JWT、
	// 无 401 路径），IP 限流收敛防令牌爆破（03 §2.1，specs §5.1.4 规则2；阈值
	// 为注册处常量，规格授权实现期可调）。context/submit 低频维持 30/min；
	// reply 单独放宽 120/min：组卷逐子能力启用题全取题量无上限（8 子能力各 5 题
	// 即 40+ 次），30/min 会拦截合法连续作答。
	r.POST("/api/answer/context",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:answer-ctx:", middleware.ClientIPKey, 30, time.Minute),
		answerHandler.Context,
	)
	r.POST("/api/answer/reply",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:answer-reply:", middleware.ClientIPKey, 120, time.Minute),
		answerHandler.Reply,
	)
	r.POST("/api/answer/submit",
		middleware.RateLimit(rdb, "sili-smart-hr:rl:answer-submit:", middleware.ClientIPKey, 30, time.Minute),
		answerHandler.Submit,
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
	// 批次域：六接口 JWT 鉴权挂 auth 组（specs §2.2 + 03 §3 A1-A5/B1），无角色差异。
	auth.GET("/assessment/batches", assessmentBatchHandler.List)
	auth.GET("/assessment/batches/stats", assessmentBatchHandler.Stats)
	auth.GET("/assessment/batches/plan", assessmentBatchHandler.Plan)
	auth.GET("/assessment/batches/targets", assessmentBatchHandler.Targets)
	auth.GET("/assessment/batches/failures", assessmentBatchHandler.Failures)
	auth.POST("/assessment/batches/create", assessmentBatchHandler.Create)
	// 主动测试域：七接口 JWT 鉴权挂 auth 组（specs §2.2 + 03 §3 A1/A2/B1/B2/C1-C3），
	// 无角色差异。test-tasks 前缀下静态子路径与根 List 路由不冲突（Gin 静态优先）。
	auth.GET("/assessment/test-tasks", assessmentTestTaskHandler.List)
	auth.GET("/assessment/test-tasks/poll-counts", assessmentTestTaskHandler.PollCounts)
	auth.GET("/assessment/test-tasks/scale-status", assessmentTestTaskHandler.ScaleStatus)
	auth.GET("/assessment/test-tasks/link", assessmentTestTaskHandler.Link)
	auth.POST("/assessment/test-tasks/create", assessmentTestTaskHandler.Create)
	auth.POST("/assessment/test-tasks/resend", assessmentTestTaskHandler.Resend)
	auth.POST("/assessment/test-tasks/cancel", assessmentTestTaskHandler.Cancel)
	// 题库域：六接口 JWT 鉴权挂 auth 组（specs §2.3 鉴权矩阵六行 / BR1）。
	// /questions/:id 参数路由与 /questions 静态路由不冲突（Gin 静态优先）。
	auth.GET("/questions", questionHandler.List)
	auth.GET("/questions/:id", questionHandler.Detail)
	auth.POST("/questions/update", questionHandler.Update)
	auth.POST("/questions/toggle-status", questionHandler.ToggleStatus)
	auth.POST("/questions/delete", questionHandler.Delete)
	auth.POST("/questions/resubmit", questionHandler.Resubmit)
	// 题库批次域：四接口 JWT 鉴权挂 auth 组（specs §2.3 鉴权矩阵批次四行 / BR1）。
	auth.GET("/question-batches", questionBatchHandler.ListBatches)
	auth.GET("/question-batches/:id/questions", questionBatchHandler.BatchQuestions)
	auth.POST("/question-batches/:id/confirm", questionBatchHandler.Confirm)
	auth.POST("/question-batches/:id/void", questionBatchHandler.Void)
	// 生成会话域：三接口 JWT 鉴权挂 auth 组（specs §2.3 鉴权矩阵生成行 / BR1）。
	auth.POST("/question-generations/create", questionGenerationHandler.Create)
	auth.GET("/question-generations/:id", questionGenerationHandler.Progress)
	auth.POST("/question-generations/:id/cancel", questionGenerationHandler.Cancel)
	// 量表引入域：两接口 JWT 鉴权挂 auth 组（specs §2.3 鉴权矩阵量表两行）。
	auth.GET("/scales", scaleHandler.List)
	auth.POST("/scales/import", scaleHandler.Import)
	// 个人画像域：三接口 JWT 鉴权挂 auth 组，全 GET 查询语义（specs §2.1/§2.2 +
	// 03 §1.2/§2.1 / BR1、BR3）。/profiles 根路由与 export/detail 静态子路径不冲突
	//（Gin 静态优先），无单独限流（03 §2.1 沿用受保护组既有策略）。
	auth.GET("/profiles", profileHandler.List)
	auth.GET("/profiles/export", profileHandler.Export)
	auth.GET("/profiles/detail", profileHandler.Detail)
	// 团队看板域：两接口 JWT 鉴权挂 auth 组，全 GET 只读聚合实时计算不落库
	//（specs §2.2/§5.2.1 / BR1、BR3）。无单独限流（沿用受保护组既有策略）。
	auth.GET("/dashboard", dashboardHandler.Overview)
	auth.GET("/dashboard/trend", dashboardHandler.Trend)
	// 工作台域：单接口 JWT 鉴权挂 auth 组，GET 只读聚合实时计算不落库（specs
	// P2_WRK_001 §2.2/§5.1 / BR1、BR2）。无单独限流（沿用受保护组既有策略）。
	auth.GET("/workspace", workspaceHandler.Overview)

	return r
}
