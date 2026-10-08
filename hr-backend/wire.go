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
		// 个人画像域（specs P2_PRF_001）：service 消费既有五仓储与 userapi/密钥装配 +
		// handler（03 A1/A2/B1 三接口全 GET）。
		service.NewProfileService,
		handler.NewProfileHandler,
		// 团队看板域（specs P2_TMD_001）：dashboard 两仓储 + service（消费既有维度/
		// 阅卷/密钥装配）+ handler（03 A1/A2 两接口全 GET，聚合实时计算不落库）。
		repository.NewDashboardQueryRepository,
		repository.NewTeamTrainingSuggestionRepository,
		service.NewDashboardService,
		handler.NewDashboardHandler,
		// 工作台域（specs P2_WRK_001）：workspace 仓储 + service（消费既有 dashboard
		// 仓储/维度/配置/密钥装配，时钟经 NowFunc 适配器转裸 func）+ handler（03 W1
		// 单 GET 只读聚合实时计算不落库）。
		repository.NewWorkspaceQueryRepository,
		NewWorkspaceServiceAdapter,
		handler.NewWorkspaceHandler,
		// 建议生成两段任务（specs §5.1，03 §4.1/§4.2）：suggestgen 引擎（专用
		// LLM client 180s 独立 gate）+ SuggestService（adapter 固定 inject=nil）+
		// suggest-tick / suggest-generate 两 handler 经参数注入 NewMux + Asynq
		// 投递适配器（service.SuggestEnqueuer 窄接口，default 队列 MaxRetry 3）。
		NewSuggestGenLLMClient,
		NewSuggestGenProvider,
		NewSuggestServiceAdapter,
		NewAsynqSuggestEnqueuer,
		NewSuggestTickHandlerTyped,
		NewSuggestGenerateHandlerTyped,
		// 操作日志清理任务（specs P4_LOG_001 §5.5，03 §4.3）：日志仓储 + Recorder
		//（异步落库通道复用同一实例）+ operation-log:clean handler 经参数注入
		// NewMux，每日 03:00 低峰清 180 天前日志。
		repository.NewOperationLogRepository,
		service.NewOperationLogRecorder,
		NewOperationLogCleanHandlerTyped,
		// 操作日志查询导出域（specs P4_LOG_001 §5.3/§5.4，03 §3 A1/A2）：
		// service 复用同一日志仓储 + handler（两 GET 接口挂 auth 组）。
		service.NewOperationLogService,
		handler.NewOperationLogHandler,
		NewAlertWriterAdapter,
		pipeline.NewAsynqEnqueuer,
		NewOrchestratorProvider,
		NewBatchTickHandlerTyped,
		NewBatchRunHandlerTyped,
		// 批次查询/发起域（03 §3）：时钟经 NowFunc 命名类型注入规避 func 同型冲突。
		ProvideNowFunc,
		NewAssessmentBatchServiceAdapter,
		// 主动测试域（specs P2_TST_001）：task 仓储 + service 经 NowFunc 复用装配 +
		// handler（03 §3 七接口）+ 逾期 tick handler（§5.3，runner 直连仓储）。
		repository.NewAssessmentTestTaskRepository,
		NewAssessmentTestTaskServiceAdapter,
		NewTestExpireTickHandlerTyped,
		handler.NewAssessmentTestTaskHandler,
		// 员工作答域（specs P2_TST_002）：answer 仓储 + service（taskSvc 复用 F7
		// 实例承载 StartSession/CompleteTask 契约）+ handler（03 §3 三接口挂公开路由组）。
		repository.NewAssessmentTestAnswerRepository,
		NewAnswerServiceAdapter,
		handler.NewAnswerHandler,
		// AI 阅卷域（specs §5.2，02-T4）：result 仓储 + 阅卷专用 LLM 客户端
		//（240s Timeout，独立 gate）+ Grader 十一参装配 + assessment:test-grade
		// handler 经参数注入 NewMux + Asynq 投递适配器（service.TestGradeEnqueuer
		// 窄接口，default 队列 MaxRetry 默认 25 不收紧，03 §4.5）。
		repository.NewAssessmentTestResultRepository,
		NewGradingLLMClient,
		NewGradingProvider,
		NewTestGradeHandlerTyped,
		NewAsynqTestGradeEnqueuer,
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
		// 阅卷任务投递：*AsynqTestGradeEnqueuer 绑定 service.TestGradeEnqueuer 窄接口。
		wire.Bind(new(service.TestGradeEnqueuer), new(*AsynqTestGradeEnqueuer)),
		// 建议生成任务投递：*AsynqSuggestEnqueuer 绑定 service.SuggestEnqueuer 窄接口。
		wire.Bind(new(service.SuggestEnqueuer), new(*AsynqSuggestEnqueuer)),
		// 操作日志记录通道：具体 recorder 绑定窄投递接口，供 router 中间件消费。
		wire.Bind(new(service.PendingLogRecorder), new(*service.OperationLogRecorder)),
		wire.Struct(new(App), "*"),
	)
	return nil, nil
}
