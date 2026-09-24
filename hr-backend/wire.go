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
	"sili-smart-hr/backend/internal/engine/fallback"
	"sili-smart-hr/backend/internal/engine/pipeline"
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
		// 批次编排装配（03 §4.8）：batch/alert 仓储 + 告警写入 + Asynq 双任务
		// 投递适配器 + Orchestrator + 批次 handler 经参数注入 NewMux。
		repository.NewAssessmentBatchRepository,
		repository.NewAssessmentAlertRepository,
		// 题库域：question 仓储 + service + handler（03 §3.1-§3.4/§3.6 questions 五接口）。
		repository.NewQuestionRepository,
		service.NewQuestionService,
		handler.NewQuestionHandler,
		// 题库批次域：batch 仓储 + service + handler（03 §3.5/§3.7-§3.10 批次四接口与重新提交）。
		repository.NewQuestionBatchRepository,
		service.NewQuestionBatchService,
		handler.NewQuestionBatchHandler,
		// AI 生成出题域（04 T4）：generation 仓储 + 出题专用 LLM 客户端（120s Timeout，
		// 独立 gate）+ DimensionRepository 出题口径适配 + Generator + questionbank:generate
		// handler 经参数注入 NewMux。
		repository.NewQuestionGenerationRepository,
		NewQuestionGenLLMClient,
		NewQuestionDimensionSpecReader,
		NewQuestionGenProvider,
		NewQuestionGenerateHandlerTyped,
		// 生成会话域（04 T5）：Asynq 投递适配器（service.GenerationEnqueuer）+
		// service + handler（03 §3.13/§3.14 生成三接口）。
		NewAsynqGenerationEnqueuer,
		service.NewQuestionGenerationService,
		handler.NewQuestionGenerationHandler,
		// 量表引入域：scale 仓储 + service + handler（03 §3.11/§3.12 scales 两接口）。
		repository.NewScaleRepository,
		service.NewScaleService,
		handler.NewScaleHandler,
		fallback.NewAlertWriter,
		pipeline.NewAsynqEnqueuer,
		NewOrchestratorProvider,
		NewBatchTickHandlerTyped,
		NewBatchRunHandlerTyped,
		// 批次查询/发起域（03 §3）：时钟经 NowFunc 命名类型注入规避 func 同型冲突。
		ProvideNowFunc,
		NewAssessmentBatchServiceAdapter,
		handler.NewAccountHandler,
		handler.NewHealthHandler,
		handler.NewSetupHandler,
		handler.NewSystemHandler,
		handler.NewDimensionHandler,
		handler.NewAssessmentConfigHandler,
		handler.NewAssessmentBatchHandler,
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
		// 生成任务投递：*AsynqGenerationEnqueuer 绑定 service.GenerationEnqueuer 窄接口。
		wire.Bind(new(service.GenerationEnqueuer), new(*AsynqGenerationEnqueuer)),
		wire.Struct(new(App), "*"),
	)
	return nil, nil
}
