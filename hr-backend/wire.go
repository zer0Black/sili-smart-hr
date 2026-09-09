// wire.go 定义 wire injector。本文件仅在 wire 代码生成时编译（build tag wireinject）。
//
//go:build wireinject

package app

import (
	"github.com/google/wire"
	"github.com/hibiken/asynq"

	"sili-smart-hr/backend/internal/api/handler"
	"sili-smart-hr/backend/internal/api/router"
	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/model"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
	"sili-smart-hr/backend/internal/worker/scheduler"
	"sili-smart-hr/backend/internal/worker/server"
)

// InitializeApp 单一 injector：config → db → redis → asynq → repository → service → handler → router。
func InitializeApp(configPath string) (*App, error) {
	wire.Build(
		config.Load,
		model.InitDB,
		repository.NewAccountRepository,
		repository.NewSystemInitializationRepository,
		repository.NewDimensionRepository,
		repository.NewAssessmentConfigRepository,
		repository.NewLLMConfigRepository,
		repository.NewIntegrationSecretRepository,
		repository.NewSessionFeatureRepository,
		NewExtractorSysParamDefaults,
		repository.NewSystemParamReader,
		NewJWTManager,
		service.NewAccountService,
		service.NewDimensionService,
		service.NewAssessmentConfigService,
		service.NewLLMConfigService,
		service.NewIntegrationSecretService,
		// userapi.Client 是结构化类型，需同包 provider 把它适配为 service 内的 userapiClient 接口。
		service.ProvideUserapiClient,
		// setup 域：用 app.NewSetupServiceAdapter 适配命名类型 probe（避免 Wire func 类型冲突），
		// NewJWTSecretSecure 供环境自检 checks.jwt_secret.secure（specs 03 §3.1）。
		NewSetupServiceAdapter,
		NewJWTSecretSecure,
		// system 域：用 app.NewSystemServiceAdapter 适配命名类型 probe，真实 DependencyProbe 经 provider 绑定为接口。
		NewSystemServiceAdapter,
		NewRealDependencyProbeProvider,
		ProvideDBProbe,
		ProvideRedisProbe,
		// userapi 客户端：用 *config.Config 入参的 provider，避免裸 string 与 configPath 在 Wire 类型表冲突。
		NewUserapiClient,
		// conversationlog 客户端：同款用 *config.Config 入参规避 string 冲突，*conversationlog.Client 由 wire.Bind 绑定到 ConversationlogPinger。
		NewConversationlogClient,
		// LLM AES 派生密钥：返回 []byte 类型唯一无冲突，NewLLMConfigService 直接消费。
		NewLLMEncKey,
		// LLM 底座：配置域桥接为 EnabledModelProvider，llm.Client 供真实探测与后续评估流水线消费。
		service.NewLLMEnabledProvider,
		NewLLMClient,
		// extractor 装配：专用 LLM 客户端（独立并发 gate，评估链路与探活互不挤占）+
		// conversationlog 客户端 + 档案仓储 + 参数读取（defaulter 回退出厂集）+
		// SecretProvider（IntegrationSecretRepository 解密）。
		NewExtractorLLMClient,
		NewExtractorProvider,
		// worker 任务：session-extract handler 经参数注入 NewMux（单一注册入口）。
		NewSessionExtractHandlerTyped,
		// 评估链装配（03 §2.1）：评估专用 LLM 客户端（180s Timeout，独立 gate）+
		// DimensionRepository 双适配（阈值/维度口径窄接口）+ activity/scorer/evaluator
		// 三组件 + person-evaluate handler 经参数注入 NewMux（单一注册入口）。
		NewEvaluatorLLMClient,
		NewActivityThresholdReader,
		NewDimensionSpecReader,
		repository.NewActivityStatRepository,
		repository.NewDimensionScoreRepository,
		repository.NewAggregateScoreRepository,
		NewActivityProvider,
		scorer.New,
		NewEvaluatorProvider,
		NewPersonEvaluateHandlerTyped,
		handler.NewAccountHandler,
		handler.NewHealthHandler,
		handler.NewSetupHandler,
		handler.NewSystemHandler,
		handler.NewDimensionHandler,
		handler.NewAssessmentConfigHandler,
		handler.NewLLMConfigHandler,
		handler.NewIntegrationSecretHandler,
		router.NewRouter,
		NewRedisClient,
		NewAsynqConnOpt,
		asynq.NewClient,
		server.NewServer,
		NewMuxAdapter,
		scheduler.NewScheduler,
		NewHTTPAddr,
		NewAsynqConcurrency,
		NewRSAManager,
		wire.Bind(new(service.PasswordDecryptor), new(*rsakey.Manager)),
		wire.Bind(new(service.ConversationlogPinger), new(*conversationlog.Client)),
		wire.Struct(new(App), "*"),
	)
	return nil, nil
}
