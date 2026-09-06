// Package app 聚合后端各层组件，提供 wire injector 与进程装配。
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"sili-smart-hr/backend/internal/config"
	"sili-smart-hr/backend/internal/domain"
	"sili-smart-hr/backend/internal/engine/activity"
	"sili-smart-hr/backend/internal/engine/evaluator"
	"sili-smart-hr/backend/internal/engine/extractor"
	"sili-smart-hr/backend/internal/engine/scorer"
	"sili-smart-hr/backend/internal/integration/conversationlog"
	"sili-smart-hr/backend/internal/integration/llm"
	"sili-smart-hr/backend/internal/integration/userapi"
	"sili-smart-hr/backend/internal/pkg/crypto"
	"sili-smart-hr/backend/internal/pkg/jwt"
	"sili-smart-hr/backend/internal/pkg/rsakey"
	"sili-smart-hr/backend/internal/repository"
	"sili-smart-hr/backend/internal/service"
	"sili-smart-hr/backend/internal/worker/task"
)

// App 聚合进程运行所需的各组件句柄。
type App struct {
	Config      *config.Config
	Engine      *gin.Engine
	HTTPAddr    HTTPAddr
	DB          *gorm.DB
	Redis       *redis.Client
	AsynqClient *asynq.Client
	AsynqServer *asynq.Server
	Mux         *asynq.ServeMux
	Scheduler   *asynq.Scheduler
}

func NewRedisClient(cfg *config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
}

// NewAsynqConnOpt 用 asynq.RedisClientOpt 自建连接，与 NewRedisClient 不共享实例（Asynq SDK 限制）。
func NewAsynqConnOpt(cfg *config.Config) asynq.RedisConnOpt {
	return asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
}

func NewJWTManager(cfg *config.Config) (*jwt.Manager, error) {
	ttl, err := time.ParseDuration(cfg.JWT.TTL)
	if err != nil {
		return nil, fmt.Errorf("parse jwt ttl %q: %w", cfg.JWT.TTL, err)
	}
	return jwt.NewManager(cfg.JWT.Secret, ttl), nil
}

// HTTPAddr 用命名类型而非裸 string，避免与 injector 入参 configPath（同为 string）在 wire 中 multiple bindings 冲突。
type HTTPAddr string

func NewHTTPAddr(cfg *config.Config) HTTPAddr {
	return HTTPAddr(fmt.Sprintf(":%d", cfg.Server.Port))
}

func NewAsynqConcurrency(cfg *config.Config) int { return cfg.Asynq.Concurrency }

// NewRSAManager 构造 RSA 动态密钥管理器，私钥复用 Redis，TTL 与公钥接口 expiresIn 一致（5 分钟）。
func NewRSAManager(cfg *config.Config, rdb *redis.Client) *rsakey.Manager {
	return rsakey.NewManager(rdb, 5*time.Minute)
}

// DBProbe 与 RedisProbe 用命名类型区分，避免 Wire 把两个 func(context.Context) bool
// 视为同一类型产生 multiple bindings 冲突。service.NewSetupService 形参仍是裸 func 类型，
// 由 NewSetupServiceAdapter 在此包内做命名→裸 func 的适配。
type DBProbe func(context.Context) bool
type RedisProbe func(context.Context) bool

// ProvideDBProbe 返回数据库连通性探针：SELECT 1 成功即连通。
// 供 SetupService.GetStatus 环境自检调用（specs §5.1）。
func ProvideDBProbe(db *gorm.DB) DBProbe {
	return DBProbe(func(ctx context.Context) bool {
		var n int64
		return db.WithContext(ctx).Raw("SELECT 1").Scan(&n).Error == nil
	})
}

// ProvideRedisProbe 返回 Redis 连通性探针：Ping 成功即连通。
func ProvideRedisProbe(rdb *redis.Client) RedisProbe {
	return RedisProbe(func(ctx context.Context) bool {
		return rdb.Ping(ctx).Err() == nil
	})
}

// NewSetupServiceAdapter 是 Wire 装配适配器：接收命名类型 probe，
// 内部转裸 func 调 service.NewSetupService，让 Wire 类型表唯一可注入。
func NewSetupServiceAdapter(
	db *gorm.DB,
	systemInitRepo repository.SystemInitializationRepository,
	accountRepo repository.AccountRepository,
	decryptor service.PasswordDecryptor,
	dbProbe DBProbe,
	redisProbe RedisProbe,
) service.SetupService {
	return service.NewSetupService(db, systemInitRepo, accountRepo, decryptor, (func(context.Context) bool)(dbProbe), (func(context.Context) bool)(redisProbe))
}

// NewRealDependencyProbeProvider 返回 config 落地后的真实外部依赖探测（LLM 最小流式请求 +
// 会话日志鉴权探测），替换 NoopDependencyProbe。用 provider 返回接口类型让 Wire 类型表唯一可注入。
func NewRealDependencyProbeProvider(
	provider llm.EnabledModelProvider,
	llmClient llm.Client,
	secretRepo repository.IntegrationSecretRepository,
	encKey []byte,
	convLog service.ConversationlogPinger,
) service.DependencyProbe {
	return service.NewRealDependencyProbe(provider, llmClient, secretRepo, encKey, convLog)
}

// NewLLMClient 构造全局 LLM 底座客户端，零值 Config 由 llm.New 内部 applyDefaults
// 兜底（Timeout 10min、MaxRetries 3、并发 4）。探活经自身 10s ctx 收敛，未来
// answer 域对话式施测的长回复依赖默认 10min 量级超时，勿在此收紧全局参数。
func NewLLMClient(provider llm.EnabledModelProvider) llm.Client {
	return llm.New(llm.Config{}, provider)
}

// ExtractorLLMClient 用命名接口类型区分 extractor 专用 client 与全局 llm.Client，
// 规避 Wire 类型表 multiple bindings 冲突（与 DBProbe/RedisProbe 命名类型同款）。
type ExtractorLLMClient llm.Client

// NewExtractorLLMClient 构造 extractor 专用 LLM 客户端：60s Timeout 覆盖底座默认
// 10min，与全局 client 各持独立并发 gate；TokenBudget=2×MaxSessionTokens 加模板余量。
// 最坏耗时按 429 带 Retry-After 封顶 60s 计：单次 callOnce 60+60+60=180s，schema
// 重试双调用 360s，叠加详情拉取最坏 190s 全链约 550s。任务级超时的单点声明在
// worker/task 的 sessionExtractTimeout（600s 含落库冗余），调整本处参数须同步该处。
func NewExtractorLLMClient(provider llm.EnabledModelProvider) ExtractorLLMClient {
	return llm.New(llm.Config{
		Timeout:        60 * time.Second,
		MaxRetries:     1,
		InitialBackoff: 5 * time.Second,
		MaxBackoff:     10 * time.Second,
		MaxRetryAfter:  60 * time.Second,
		TokenBudget:    2*extractor.MaxSessionTokens + 1000,
		TokenCounter:   llm.NewCharDiv3Counter(),
	}, provider)
}

// NewSystemServiceAdapter 是 Wire 装配适配器：接收命名类型 probe，
// 内部转裸 func 调 service.NewSystemService，让 Wire 类型表唯一可注入（与 NewSetupServiceAdapter 同款）。
// service.NewSystemService 形参仍是裸 func，在此收敛避免命名类型与裸 func 的 multiple bindings 冲突。
func NewSystemServiceAdapter(
	systemInitRepo repository.SystemInitializationRepository,
	dbProbe DBProbe,
	redisProbe RedisProbe,
	probe service.DependencyProbe,
) service.SystemService {
	return service.NewSystemService(
		systemInitRepo,
		(func(context.Context) bool)(dbProbe),
		(func(context.Context) bool)(redisProbe),
		probe,
	)
}

// NewUserapiClient 用 *config.Config 入参而非裸 string，
// 避免 userapi.NewClient 的 string 形参与 injector 的 configPath 在 Wire 类型表 multiple bindings 冲突
// （与 NewHTTPAddr 用命名类型规避 string 冲突同款）。
// 返回的 *userapi.Client 因方法集匹配 service.userapiClient 接口（鸭子类型，未导出接口），
// wire 能直接注入 service.NewAssessmentConfigService。
func NewUserapiClient(cfg *config.Config) *userapi.Client {
	return userapi.NewClient(cfg.Integration.SmartAPIBaseURL)
}

// NewConversationlogClient 用 *config.Config 入参而非裸 string，规避 conversationlog.NewClient 的
// string 形参与 injector 入参 configPath 在 Wire 类型表 multiple bindings 冲突（与 NewUserapiClient 同款）。
// 返回的 *conversationlog.Client 由 wire.Bind 绑定到 service.ConversationlogPinger 接口，
// 供 NewIntegrationSecretService 注入。
func NewConversationlogClient(cfg *config.Config) *conversationlog.Client {
	return conversationlog.NewClient(cfg.Integration.SmartAPIBaseURL)
}

// NewLLMEncKey 从 config.LLM.SecretKey 派生 AES-256 对称密钥（crypto.DeriveKey）。
// 供 NewLLMConfigService 加密落库与详情解密使用。返回 []byte 类型在 Wire 类型表唯一无冲突。
func NewLLMEncKey(cfg *config.Config) []byte {
	return crypto.DeriveKey(cfg.LLM.SecretKey)
}

// NewExtractorSeed 返回脱敏正则出厂集（defaulter map 的取值点）：
// extractor 出厂函数本身返回拷贝，此处直接透传。inject_prefixes 在追加语义下
// 无出厂回退概念（defaulter 不注册该键），改出厂集内容时只改 extractor 包内定义。
func NewExtractorSeed() []string {
	return extractor.RedactPatterns()
}

// NewExtractorSysParamDefaults 构造 defaulter map：redact_patterns 回退出厂，
// inject_prefixes 回退 nil（空追加集，追加语义下无出厂回退概念）。
func NewExtractorSysParamDefaults() map[string][]string {
	return map[string][]string{
		extractor.ParamKeyRedactPatterns: NewExtractorSeed(),
	}
}

// NewExtractorProvider 装配 extractor：专用 LLM 客户端（60s Timeout / MaxRetries=1 /
// 退避 5-10s）与全局探活 client 隔离；SecretProvider 从 IntegrationSecretRepository
// 取密文解密（收敛点 service.ResolveIntegrationSecret）。任务级超时见 worker/task
// 的 sessionExtractTimeout（推导见 NewExtractorLLMClient 注释）。
func NewExtractorProvider(llmClient ExtractorLLMClient, cl *conversationlog.Client,
	repo repository.SessionFeatureRepository, params repository.SystemParamReader,
	secretRepo repository.IntegrationSecretRepository, encKey []byte) *extractor.Extractor {
	secrets := extractor.SecretProvider(func(ctx context.Context) (string, error) {
		return service.ResolveIntegrationSecret(ctx, secretRepo, encKey)
	})
	return extractor.New(llmClient, cl, repo, params, secrets)
}

// EvaluatorLLMClient 用命名接口类型区分评估专用 client 与全局 llm.Client，
// 规避 Wire 类型表 multiple bindings 冲突（与 ExtractorLLMClient 同款）。
type EvaluatorLLMClient llm.Client

// NewEvaluatorLLMClient 构造评估专用 LLM 客户端（03 §2.1）：Timeout 180s 覆盖
// 建连到流式 body 读毕全程，支撑 120s 验收线与 180s p99 观测线；与全局及
// extractor client 各持独立并发 gate。TokenBudget = MaxProfileSetTokens 30000
// + 维度段与余量 17000 = 47000（20 维度上限标定）。任务级超时的单点声明在
// worker/task 的 personEvaluateTimeout（1050s），调整本处参数须同步该处。
func NewEvaluatorLLMClient(provider llm.EnabledModelProvider) EvaluatorLLMClient {
	return llm.New(llm.Config{
		Timeout:        180 * time.Second,
		MaxRetries:     1,
		InitialBackoff: 5 * time.Second,
		MaxBackoff:     10 * time.Second,
		MaxRetryAfter:  60 * time.Second,
		TokenBudget:    evaluator.MaxProfileSetTokens + 17000,
		TokenCounter:   llm.NewCharDiv3Counter(),
	}, provider)
}

// ActivityThresholdReader 评估阈值适配器：把 DimensionRepository（GetActivitySetting）
// 适配为 activity.ThresholdReader（03 §5.2 消费侧窄接口）。
type ActivityThresholdReader struct {
	dimRepo repository.DimensionRepository
}

// NewActivityThresholdReader 构造阈值读取适配器。
func NewActivityThresholdReader(dimRepo repository.DimensionRepository) *ActivityThresholdReader {
	return &ActivityThresholdReader{dimRepo: dimRepo}
}

// ActivityThresholds 读 dimension_settings 单行。任何读取失败（含
// ErrRecordNotFound）一律 wrap ErrDimensionConfigRead 上抛：阈值不重复定义
// 默认值，migrateDB seed 保证行存在，无回退分支。
func (r *ActivityThresholdReader) ActivityThresholds(ctx context.Context) (int, int, error) {
	s, err := r.dimRepo.GetActivitySetting(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", evaluator.ErrDimensionConfigRead, err)
	}
	return s.ActiveThreshold, s.LowFrequencyThreshold, nil
}

// 编译期断言：适配器满足 activity.ThresholdReader 窄接口。
var _ activity.ThresholdReader = (*ActivityThresholdReader)(nil)

// DimensionSpecReaderAdapter 维度口径适配器：把 DimensionRepository
//（ListEnabledFullByDataSource）适配为 evaluator.DimensionSpecReader（03 §5.2）。
type DimensionSpecReaderAdapter struct {
	dimRepo repository.DimensionRepository
}

// NewDimensionSpecReader 构造维度口径读取适配器。
func NewDimensionSpecReader(dimRepo repository.DimensionRepository) *DimensionSpecReaderAdapter {
	return &DimensionSpecReaderAdapter{dimRepo: dimRepo}
}

// ListEnabledConversationSpecs 取启用 CONVERSATION 维度全字段组装为
// []DimensionSpec（Module 取 ModuleCode 原值）。读取失败 wrap
// ErrDimensionConfigRead 上抛（空集语义由 evaluator.loadSpecs 判定）。
func (a *DimensionSpecReaderAdapter) ListEnabledConversationSpecs(ctx context.Context) ([]evaluator.DimensionSpec, error) {
	dims, err := a.dimRepo.ListEnabledFullByDataSource(ctx, domain.SourceConversation)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", evaluator.ErrDimensionConfigRead, err)
	}
	specs := make([]evaluator.DimensionSpec, 0, len(dims))
	for _, d := range dims {
		specs = append(specs, evaluator.DimensionSpec{
			Code:       d.Code,
			Name:       d.Name,
			Module:     d.ModuleCode,
			PromptText: d.Prompt,
			AnchorText: d.Anchor,
			Weight:     d.Weight,
			InOverview: d.IncludeOverview,
		})
	}
	return specs, nil
}

// 编译期断言：适配器满足 evaluator.DimensionSpecReader 窄接口。
var _ evaluator.DimensionSpecReader = (*DimensionSpecReaderAdapter)(nil)

// NewActivityProvider 装配 activity 组件（五参）：secrets 经
// service.ResolveIntegrationSecret 解密构造（NewExtractorProvider 同款收敛点）。
func NewActivityProvider(cl *conversationlog.Client,
	featureRepo repository.SessionFeatureRepository, thresholds *ActivityThresholdReader,
	repo repository.ActivityStatRepository, secretRepo repository.IntegrationSecretRepository,
	encKey []byte) *activity.Activity {
	secrets := activity.SecretProvider(func(ctx context.Context) (string, error) {
		return service.ResolveIntegrationSecret(ctx, secretRepo, encKey)
	})
	return activity.New(cl, featureRepo, thresholds, repo, secrets)
}

// SessionExtractHandler / PersonEvaluateHandler 是 Wire 装配用命名类型：NewMux
// 双形参同为 asynq.HandlerFunc，wire 无法按类型区分（别名也视为同型），故用
// 独立定义的 func 命名类型各占类型表一格，再由 NewMuxAdapter 收参适配
//（DBProbe/RedisProbe 命名类型同款）。
type SessionExtractHandler func(context.Context, *asynq.Task) error

// PersonEvaluateHandler person-evaluate handler 命名类型（同上）。
type PersonEvaluateHandler func(context.Context, *asynq.Task) error

// NewSessionExtractHandlerTyped 构造 session-extract handler（命名类型透出，
// Wire 装配入口；task 包内测试用原 NewSessionExtractHandler）。
func NewSessionExtractHandlerTyped(ext *extractor.Extractor) SessionExtractHandler {
	return SessionExtractHandler(task.NewSessionExtractHandler(ext))
}

// NewPersonEvaluateHandlerTyped 构造 person-evaluate handler（命名类型透出）。
func NewPersonEvaluateHandlerTyped(ev *evaluator.Evaluator) PersonEvaluateHandler {
	return PersonEvaluateHandler(task.NewPersonEvaluateHandler(ev))
}

// NewMuxAdapter Wire 装配适配器：接收两个命名类型 handler，转调 task.NewMux
//（单一注册入口不变，签名不受 wire 同型参数限制）。
func NewMuxAdapter(sessionExtract SessionExtractHandler, personEvaluate PersonEvaluateHandler) *asynq.ServeMux {
	return task.NewMux(asynq.HandlerFunc(sessionExtract), asynq.HandlerFunc(personEvaluate))
}

// NewEvaluatorProvider 装配 evaluator（九参，03 §2.1 组合形）：act 窄面经
// evaluator.NewActivityStatComponent 适配 *activity.Activity，sc 由 *scorer.Scorer
// 鸭子满足，两窄面在此收敛规避 wire 对未导出接口值的绑定限制。
func NewEvaluatorProvider(llmClient EvaluatorLLMClient, modelProvider llm.EnabledModelProvider,
	featureRepo repository.SessionFeatureRepository, specs *DimensionSpecReaderAdapter,
	thresholds *ActivityThresholdReader, scoreRepo repository.DimensionScoreRepository,
	sysParams repository.SystemParamReader, act *activity.Activity, sc *scorer.Scorer) *evaluator.Evaluator {
	return evaluator.New(llmClient, modelProvider, featureRepo, specs, thresholds,
		scoreRepo, sysParams, evaluator.NewActivityStatComponent(act), sc)
}
