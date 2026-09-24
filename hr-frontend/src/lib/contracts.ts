// 跨域契约类型，与后端统一响应结构同构。

/** 统一响应结构 { code, message, data }。 */
export interface Response<T = unknown> {
  code: number;
  message: string;
  data?: T;
}

/** 分页结构 { list, total, page, page_size }，作为 data 透传。 */
export interface Page<T = unknown> {
  list: T[];
  total: number;
  page: number;
  page_size: number;
}

/** 业务错误码（与后端 errcode 对齐）。 */
export const ErrCode = {
  Success: 0,
  InvalidCredentials: 1001,
  AccountDisabled: 1002,
  Unauthorized: 1003,
  AccountNotFound: 1004,
  UsernameExists: 1005,
  LastEnabledAccount: 1006,
  PasswordInvalid: 1007,
  SystemAlreadyInitialized: 1101,
  EnvironmentNotReady: 1102,
  DimensionNotFound: 1201,
  DimensionCodeExists: 1202,
  DimensionEnabledNotDeletable: 1203,
  DimensionVersionConflict: 1204,
  DimensionNameInvalid: 1205,
  DimensionAnchorRequired: 1206,
  DimensionPromptRequired: 1207,
  ActivityThresholdInvalid: 1208,
  DimensionCodeUnavailable: 1209,
  LLMConfigNotFound: 1301,
  LastLLMConfig: 1302,
  IntegrationSecretNotConfigured: 1303,
  IntegrationSecretTestFailed: 1304,
  StaffListUnavailable: 1305,
  ConfigVersionConflict: 1306,
  SecretDecryptFailed: 1307,
  LLMConfigVersionConflict: 1308,
  IntegrationSecretVersionConflict: 1309,
  BadRequest: 1400,
  Internal: 1500,
  BatchNotFound: 1601,
  BatchPeriodInvalid: 1602,
  BatchTargetInvalid: 1603,
  QuestionNotFound: 1701,
  QuestionBatchNotFound: 1702,
  GenerationNotFound: 1703,
  ScaleAlreadyImported: 1704,
  QuestionReferenced: 1705,
  QuestionBatchClosed: 1706,
  QuestionNotEditable: 1707,
  QuestionStatusInvalid: 1708,
  LLMNotConfigured: 1709,
  QuestionVersionConflict: 1713,
} as const;

/** 脱敏账号。id 为雪花 ID，后端以 JSON string 传输规避前端 JS 精度坑。 */
export interface Account {
  id: string;
  username: string;
  name: string;
  enabled: boolean;
}

export interface LoginResult {
  token: string;
  account: Account;
}

export interface MeResult {
  account: Account;
}

/** GET /api/auth/public-key 响应 data：RSA 公钥与关联 keyId，供前端 RSA-OAEP 加密密码明文。 */
export interface PublicKeyResult {
  publicKey: string;
  /** 密钥对标识，提交密文时原样回传，后端据此从 Redis 取私钥解密。 */
  keyId: string;
  /** 公钥有效期（秒），与后端 Redis 私钥 TTL 一致。 */
  expiresIn: number;
}

// 用户管理域契约（与后端 service.AccountDTO 同构）

/** 列表项。无密码字段：密码前端固定掩码展示，后端不回传。id 为 JSON string 传输的雪花 ID。 */
export interface AccountListItem {
  id: string;
  username: string;
  name: string;
  enabled: boolean;
  /** 最近登录时间（RFC3339 字符串），从未登录为 null。 */
  last_login_at: string | null;
}

export type AccountListPage = Page<AccountListItem>;

/** 新增 / 编辑 / 启停 / 重置密码后回传的账号快照。 */
export interface AccountMutationResult {
  id: string;
  username: string;
  name: string;
  enabled: boolean;
}

/** 新增账号请求体。 */
export interface CreateAccountPayload {
  username: string;
  name: string;
  passwordCipher: string;
  keyId: string;
  enabled: boolean;
}

/** 编辑账号请求体。passwordCipher 与 keyId 仅在同时重置密码时传，不重置则省略。 */
export interface UpdateAccountPayload {
  id: string;
  name: string;
  enabled: boolean;
  passwordCipher?: string;
  keyId?: string;
}

export interface ToggleEnabledPayload {
  id: string;
  enabled: boolean;
}

/** 重置密码请求体。 */
export interface ResetPasswordPayload {
  id: string;
  passwordCipher: string;
  keyId: string;
}

// 系统初始化域契约（与后端 setup service 同构）

/** GET /api/setup/status 响应 data。 */
export interface SetupStatus {
  initialized: boolean;
  db_type: string;
  checks: {
    database: { connected: boolean };
    redis: { connected: boolean };
    /** JWT 密钥安全性：secure=false 为默认公开密钥（仅信息项，不阻断提交）。 */
    jwt_secret: { secure: boolean };
  };
  block_submit: boolean;
}

/** POST /api/setup/initialize 请求体。确认密码前端校验，不入请求。 */
export interface InitializePayload {
  username: string;
  name: string;
  passwordCipher: string;
  keyId: string;
}

/** POST /api/setup/initialize 响应 data。account.id 为雪花 ID string 化。 */
export interface InitializeResult {
  initialized: boolean;
  account: Account;
}

// 系统状态域契约（与后端 system service 同构）

/** GET /api/system/status 响应 data。 */
export interface SystemSummary {
  initialized: boolean;
  db_type: string;
  version: string;
  /** ISO 8601 UTC 字符串，前端按 yyyy-MM-dd HH:mm:ss 本地格式化。 */
  started_at: string;
}

/** 组件健康状态值（03 §3.4 枚举）。 */
export type ComponentStatus =
  | 'connected'
  | 'disconnected'
  | 'reachable'
  | 'unreachable'
  | 'not_configured_model'
  | 'not_configured_key'
  | 'not_configured';

/** POST /api/system/health-check 响应 data。 */
export interface HealthResult {
  database: 'connected' | 'disconnected';
  redis: 'connected' | 'disconnected';
  llm: ComponentStatus;
  integration: ComponentStatus;
}

// 维度域契约（与后端 dimension service DTO 同构）

/** 树轻量项：维度树叶子节点。id 为雪花 ID JSON string 化。 */
export interface DimensionBrief {
  id: string;
  code: string;
  name: string;
  module_code: string;
  group_code: string | null;
  data_source: string;
  weight: number;
  include_overview: boolean;
  enabled: boolean;
}

/** 维度详情全字段。 */
export interface DimensionDetail {
  id: string;
  code: string;
  name: string;
  module_code: string;
  group_code: string | null;
  data_source: string;
  prompt: string;
  anchor: string;
  weight: number;
  include_overview: boolean;
  enabled: boolean;
  is_reference: boolean;
  description: string;
  version: number;
  created_at: string;
  updated_at: string;
}

/** 分组节点（仅 AI_USAGE 模块有）。 */
export interface DimensionGroupNode {
  group_code: string;
  name: string;
  dimensions: DimensionBrief[];
}

/** 模块节点。groups 仅 AI_USAGE 非 null；dimensions 仅非 AI_USAGE 模块非 null。 */
export interface DimensionModuleNode {
  module_code: string;
  name: string;
  data_source: string;
  is_reference: boolean;
  groups: DimensionGroupNode[] | null;
  dimensions: DimensionBrief[] | null;
}

/** GET /api/dimensions/tree 响应 data。 */
export interface DimensionTreeNode {
  modules: DimensionModuleNode[];
}

/** GET /api/dimensions/activity-rule 响应 data。 */
export interface ActivityRule {
  active_threshold: number;
  low_frequency_threshold: number;
  updated_at: string;
}

/** POST /api/dimensions/create 请求体。weight/include_overview 省略时后端按模块联动默认。 */
export interface CreateDimensionPayload {
  name: string;
  module_code: string;
  group_code: string | null;
  data_source: string;
  prompt: string;
  anchor: string;
  weight?: number;
  include_overview?: boolean;
  description?: string;
}

/** POST /api/dimensions/update 请求体。code/module_code/group_code/data_source 不可变，不入请求。 */
export interface UpdateDimensionPayload {
  id: string;
  name: string;
  prompt: string;
  anchor: string;
  weight: number;
  include_overview: boolean;
  enabled: boolean;
  description: string | null;
  version: number;
}

/** POST /api/dimensions/delete 请求体。 */
export interface DeleteDimensionPayload {
  id: string;
  version: number;
}

/** POST /api/dimensions/activity-rule/save 请求体。 */
export interface SaveActivityRulePayload {
  active_threshold: number;
  low_frequency_threshold: number;
}

/** 新增 / 编辑后回传的维度快照。update 响应另含 updated_at，统一类型设可选。 */
export interface DimensionMutationResult {
  id: string;
  code: string;
  name: string;
  module_code: string;
  group_code: string | null;
  data_source: string;
  weight: number;
  include_overview: boolean;
  enabled: boolean;
  version: number;
  updated_at?: string;
}

// 评估周期配置域契约（与后端 assessment config service DTO 同构）

/** 评估周期配置。id 为雪花 ID JSON string 化。 */
export interface AssessmentConfig {
  id: string;
  period: 'daily' | 'weekly' | 'monthly';
  /** 触发时间，HH:mm 格式。 */
  trigger_time: string;
  target_mode: 'all' | 'specified';
  specified_members: StaffItem[];
  version: number;
}

/** 员工项。staff_id 为兄弟系统业务文本标识，非雪花 ID。 */
export interface StaffItem {
  staff_id: string;
  staff_name: string;
}

export type StaffListPage = Page<StaffItem>;

/** 保存评估配置请求体。period/trigger_time/target_mode 用 string 便于 Zod 复用，约束在 schema 层。 */
export interface SaveAssessmentPayload {
  period: string;
  trigger_time: string;
  target_mode: string;
  specified_members: StaffItem[];
  version: number;
}

/** 保存后回传。version 为新版本号。 */
export interface AssessmentMutationResult {
  id: string;
  version: number;
}

// 大模型配置域契约（与后端 llm config service DTO 同构）

/** 列表/详情共用的脱敏项。id 为雪花 ID JSON string 化，api_key_masked 为掩码展示。 */
export interface LLMConfigItem {
  id: string;
  name: string;
  provider: 'deepseek' | 'openai' | 'zhipu' | 'anthropic';
  /** 模型文本标识（如 deepseek-chat），非雪花 ID。 */
  model_id: string;
  api_url: string;
  api_key_masked: string;
  enabled: boolean;
  /** 乐观锁版本号，编辑提交时回传作并发冲突检测凭证。 */
  version: number;
  created_at: string;
  updated_at: string;
}

/** 详情：用明文 api_key 替代脱敏字段，仅查看弹窗返回。 */
export interface LLMConfigDetail extends Omit<LLMConfigItem, 'api_key_masked'> {
  api_key: string;
}

/** 新增模型请求体。api_key 为 RSA-OAEP 加密后的 base64 密文，keyId 独立传递。 */
export interface CreateLLMPayload {
  name: string;
  provider: string;
  model_id: string;
  api_url?: string;
  api_key: string;
  keyId: string;
}

/** 编辑模型请求体。api_key 与 keyId 留空（undefined）表示不改密钥。 */
export interface UpdateLLMPayload {
  id: string;
  /** 乐观锁凭证，回传编辑时拿到的 version。 */
  version: number;
  name: string;
  provider: string;
  model_id: string;
  api_url?: string;
  api_key?: string;
  keyId?: string;
}

/** 删除结果。transferred_enabled_id 为排他启用迁移目标（被删项是启用项时迁移到另一项），无迁移为 null。 */
export interface LLMDeleteResult {
  id: string;
  transferred_enabled_id: string | null;
}

/** 排他启用结果。 */
export interface LLMEnableResult {
  id: string;
  enabled: boolean;
}

// 集成密钥域契约（与后端 integration secret service DTO 同构）

/** 脱敏视图。configured 表示是否已配置过密钥。 */
export interface IntegrationSecretView {
  id: string;
  secret_masked: string;
  configured: boolean;
  /** 乐观锁版本号，更新提交时回传作并发冲突检测凭证。 */
  version: number;
}

/** 详情：明文密钥，仅查看弹窗返回。 */
export interface IntegrationSecretDetail {
  id: string;
  secret: string;
}

/** 更新密钥请求体。secret 为 RSA-OAEP 加密密文，keyId 独立传递。 */
export interface UpdateSecretPayload {
  secret: string;
  keyId: string;
  /** 乐观锁凭证，回传卡片当前的 version。 */
  version: number;
}

/** 连通性测试结果。 */
export interface SecretTestResult {
  connected: boolean;
}

// 批次跑批域契约（与后端 batch service DTO 同构，03_api_interface §3 A1-A3）

/** 批次列表项。id 为雪花 ID JSON string 化；target_names 为全量名单快照（悬浮展示，specs §4.1.5）。 */
export interface BatchListItem {
  id: string;
  batch_no: string;
  trigger_type: 'scheduled' | 'manual';
  target_mode: 'all' | 'specified';
  target_brief: string[];
  target_names: string[];
  /** 评估时段起点（含），yyyy-MM-dd。 */
  period_start: string;
  /** 评估时段终点（含），yyyy-MM-dd。 */
  period_end: string;
  status: 'running' | 'success' | 'partial_failed' | 'failed';
  /** 停滞标识：running 且距触发超过一个周期长度，查询期派生不落库。 */
  stalled: boolean;
  evaluated_count: number;
  total_count: number;
  /** 进度百分比，后端向下取整；total_count=0 时为 0。 */
  progress_percent: number;
  covered_session_count: number;
  failed_count: number;
  /** 触发时间，yyyy-MM-dd HH:mm。 */
  triggered_at: string;
}

export type BatchListPage = Page<BatchListItem>;

/** 跑批态势统计卡。三项均以「本期跑批间隔」为时间归属区间。 */
export interface BatchStats {
  eval_count: number;
  evaluated_person_count: number;
  /** 进行中批次数，剔除停滞批次。 */
  running_batch_count: number;
}

/** 跑批计划卡。按当前评估周期配置推算下次触发时点与评估对象。 */
export interface BatchPlan {
  next_trigger_at: string;
  period: 'daily' | 'weekly' | 'monthly';
  target_mode: 'all' | 'specified';
  target_brief: string[];
  target_names: string[];
  target_count: number;
  dimension_base_count: number;
  dimension_upper_count: number;
}

/** POST /api/assessment/batches/create 响应 data。status 创建后恒为 running。 */
export interface CreateBatchResult {
  id: string;
  batch_no: string;
  status: 'running' | 'success' | 'partial_failed' | 'failed';
  total_count: number;
}

// 题库域契约（与后端 question service DTO 同构，03_api_interface §3.1-§3.6）

/** 列表项。id/dimension_id 为雪花 ID JSON string 化；summary 为服务端截取的摘要，悬浮全文走详情接口。 */
export interface QuestionListItem {
  id: string;
  question_no: string;
  source: 'AI' | 'SCALE';
  dimension_id: string;
  /** 维度名存量回传（维度停用/软删后仍展示原名称）。 */
  dimension_name: string;
  /** 作答方式由 source 派生：AI→CHAT，SCALE→LIKERT5。 */
  answer_mode: 'CHAT' | 'LIKERT5';
  status: 'ACTIVE' | 'DISABLED' | 'REJECTED';
  summary: string;
  updated_at: string;
}

export type QuestionListPage = Page<QuestionListItem>;

/** 详情（03 §3.2 字段全集）。status 含 PENDING（批次审核视图复用）。 */
export interface QuestionDetail {
  id: string;
  question_no: string;
  source: 'AI' | 'SCALE';
  dimension_id: string;
  dimension_name: string;
  answer_mode: 'CHAT' | 'LIKERT5';
  status: 'ACTIVE' | 'DISABLED' | 'REJECTED' | 'PENDING';
  scenario: string;
  requirement: string;
  focus_point: string;
  /** 驳回原因，仅 REJECTED 非空。 */
  reject_reason: string;
  batch_id: string;
  batch_no: string;
  reference_count: number;
  /** 乐观锁版本号，编辑/启停/删除提交时回传。 */
  version: number;
  created_at: string;
  updated_at: string;
}

/** POST /api/questions/update 请求体。question_no/source/status 等不可变字段不入请求。 */
export interface UpdateQuestionPayload {
  id: string;
  dimension_id: string;
  scenario: string;
  requirement: string;
  focus_point: string;
  version: number;
}

/** POST /api/questions/toggle-status 请求体。target_status 限 ACTIVE/DISABLED 互切。 */
export interface ToggleQuestionStatusPayload {
  id: string;
  target_status: 'ACTIVE' | 'DISABLED';
  version: number;
}

/** POST /api/questions/delete 请求体。 */
export interface DeleteQuestionPayload {
  id: string;
  version: number;
}

/** 编辑/启停共用响应（03 §3.3/§3.4）。status 仅启停返回，编辑响应无此字段。 */
export interface QuestionMutationResult {
  id: string;
  status?: 'ACTIVE' | 'DISABLED';
  version: number;
  updated_at: string;
}

// 题库批次域契约（与后端 question_batch service DTO 同构，03_api_interface §3.7-§3.10）

/** 待审核批次卡（03 §3.7）。id 为雪花 ID JSON string 化；created_at 为 RFC3339。 */
export interface QuestionBatchCard {
  id: string;
  batch_no: string;
  title: string;
  source: 'AI' | 'SCALE';
  batch_type: 'GENERATE' | 'IMPORT' | 'RESUBMIT';
  question_count: number;
  created_at: string;
}

/** 审核视图逐题全文（03 §3.8）。reject_reason 恒空串，标记在前端进行。 */
export interface ReviewQuestionItem {
  id: string;
  question_no: string;
  dimension_id: string;
  dimension_name: string;
  answer_mode: 'CHAT' | 'LIKERT5';
  scenario: string;
  requirement: string;
  focus_point: string;
  reject_reason: string;
}

/** GET /api/question-batches/:id/questions 响应 data（03 §3.8）。全量不分页，question_no 升序。 */
export interface BatchQuestionsResult {
  batch: QuestionBatchCard;
  questions: ReviewQuestionItem[];
}

/** POST /api/question-batches/:id/confirm 响应 data（03 §3.9，后端 ConfirmResult 直译）。 */
export interface ConfirmBatchResult {
  batch_id: string;
  batch_status: string;
  admitted_count: number;
  rejected_count: number;
}
