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
