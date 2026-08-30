import { describe, it, expect } from 'vitest';
import {
  ErrCode,
  type AssessmentConfig,
  type StaffItem,
  type StaffListPage,
  type SaveAssessmentPayload,
  type AssessmentMutationResult,
  type LLMConfigItem,
  type LLMConfigDetail,
  type CreateLLMPayload,
  type UpdateLLMPayload,
  type LLMDeleteResult,
  type LLMEnableResult,
  type IntegrationSecretView,
  type IntegrationSecretDetail,
  type UpdateSecretPayload,
  type SecretTestResult,
} from './contracts';

describe('ErrCode 13xx 段（SYS_001 三业务域错误码）', () => {
  // 字面量断言是与后端 errcode.go 手工镜像的契约锚点：前端为独立副本，必须显式锁值，
  // 后端码值一旦漂移，这里红，提示同步。单纯自赋值比对无捕获力，故下方再加结构守护。
  it('LLMConfigNotFound 为 1301', () => {
    expect(ErrCode.LLMConfigNotFound).toBe(1301);
  });
  it('LastLLMConfig 为 1302', () => {
    expect(ErrCode.LastLLMConfig).toBe(1302);
  });
  it('IntegrationSecretNotConfigured 为 1303', () => {
    expect(ErrCode.IntegrationSecretNotConfigured).toBe(1303);
  });
  it('IntegrationSecretTestFailed 为 1304', () => {
    expect(ErrCode.IntegrationSecretTestFailed).toBe(1304);
  });
  it('StaffListUnavailable 为 1305', () => {
    expect(ErrCode.StaffListUnavailable).toBe(1305);
  });
  it('ConfigVersionConflict 为 1306', () => {
    expect(ErrCode.ConfigVersionConflict).toBe(1306);
  });
  it('SecretDecryptFailed 为 1307', () => {
    expect(ErrCode.SecretDecryptFailed).toBe(1307);
  });

  // 结构守护：手工镜像最易漏键或抄重码。键集齐全 + 码值唯一，捕获字面量断言测不到的同步失误。
  it('ErrCode 含全部预期键且码值唯一无重复', () => {
    const expectedKeys = [
      'Success',
      'InvalidCredentials', 'AccountDisabled', 'Unauthorized', 'AccountNotFound',
      'UsernameExists', 'LastEnabledAccount', 'PasswordInvalid',
      'SystemAlreadyInitialized', 'EnvironmentNotReady',
      'DimensionNotFound', 'DimensionCodeExists', 'DimensionEnabledNotDeletable',
      'DimensionVersionConflict', 'DimensionNameInvalid', 'DimensionAnchorRequired',
      'DimensionPromptRequired', 'ActivityThresholdInvalid',
      'LLMConfigNotFound', 'LastLLMConfig', 'IntegrationSecretNotConfigured',
      'IntegrationSecretTestFailed', 'StaffListUnavailable', 'ConfigVersionConflict',
      'SecretDecryptFailed', 'BadRequest', 'Internal',
    ];
    for (const key of expectedKeys) {
      expect(ErrCode).toHaveProperty(key);
    }
    const values = Object.values(ErrCode);
    expect(new Set(values).size).toBe(values.length);
  });
});

describe('评估周期配置域 DTO', () => {
  it('AssessmentConfig 承载雪花 ID 字符串、period/trigger_time/target_mode/version 对齐 snake_case', () => {
    const cfg: AssessmentConfig = {
      id: '1234567890123456789',
      period: 'weekly',
      trigger_time: '09:00',
      target_mode: 'all',
      specified_members: [],
      version: 3,
    };
    expect(cfg.id).toBe('1234567890123456789');
    expect(cfg.period).toBe('weekly');
    expect(cfg.trigger_time).toBe('09:00');
    expect(cfg.target_mode).toBe('all');
    expect(cfg.version).toBe(3);
  });

  it('StaffItem 含 staff_id 与 staff_name（业务文本标识，非雪花）', () => {
    const item: StaffItem = { staff_id: 'u001', staff_name: '张三' };
    expect(item.staff_id).toBe('u001');
    expect(item.staff_name).toBe('张三');
  });

  it('StaffListPage 复用 Page<T> 结构（list/total/page/page_size）', () => {
    const page: StaffListPage = {
      list: [{ staff_id: 'u001', staff_name: '张三' }],
      total: 1,
      page: 1,
      page_size: 20,
    };
    expect(page.list).toHaveLength(1);
    expect(page.total).toBe(1);
    expect(page.page_size).toBe(20);
  });

  it('SaveAssessmentPayload 与 AssessmentMutationResult 字段对齐', () => {
    const payload: SaveAssessmentPayload = {
      period: 'monthly',
      trigger_time: '08:30',
      target_mode: 'specified',
      specified_members: [{ staff_id: 'u001', staff_name: '张三' }],
      version: 2,
    };
    const result: AssessmentMutationResult = { id: '9876543210987654321', version: 3 };
    expect(payload.specified_members).toHaveLength(1);
    expect(result.version).toBe(3);
  });
});

describe('大模型配置域 DTO', () => {
  it('LLMConfigItem 含 api_key_masked 脱敏字段、provider 字面量、enabled 与时间戳', () => {
    const item: LLMConfigItem = {
      id: '1111111111111111111',
      name: 'DeepSeek 主模型',
      provider: 'deepseek',
      model_id: 'deepseek-chat',
      api_url: 'https://api.deepseek.com',
      api_key_masked: 'sk-****1234',
      enabled: true,
      version: 1,
      created_at: '2026-08-12T00:00:00Z',
      updated_at: '2026-08-12T00:00:00Z',
    };
    expect(item.provider).toBe('deepseek');
    expect(item.api_key_masked).toContain('****');
    expect(item.model_id).toBe('deepseek-chat');
  });

  it('LLMConfigDetail 用明文 api_key 替代 api_key_masked（Omit 行为）', () => {
    const detail: LLMConfigDetail = {
      id: '1111111111111111111',
      name: 'DeepSeek 主模型',
      provider: 'deepseek',
      model_id: 'deepseek-chat',
      api_url: 'https://api.deepseek.com',
      api_key: 'sk-real-key',
      enabled: true,
      version: 1,
      created_at: '2026-08-12T00:00:00Z',
      updated_at: '2026-08-12T00:00:00Z',
    };
    expect(detail.api_key).toBe('sk-real-key');
  });

  it('CreateLLMPayload 含加密 api_key 与独立 keyId', () => {
    const payload: CreateLLMPayload = {
      name: 'GLM',
      provider: 'zhipu',
      model_id: 'glm-4',
      api_key: 'RSA_BASE64_CIPHER',
      keyId: 'kid-abc',
    };
    expect(payload.keyId).toBe('kid-abc');
    expect(payload.api_key).toBe('RSA_BASE64_CIPHER');
  });

  it('UpdateLLMPayload 中 api_key 与 keyId 可选（留空表示不改）', () => {
    const payload: UpdateLLMPayload = {
      id: '1111111111111111111',
      version: 1,
      name: 'GLM',
      provider: 'zhipu',
      model_id: 'glm-4',
    };
    expect(payload.api_key).toBeUndefined();
    expect(payload.keyId).toBeUndefined();
  });

  it('LLMDeleteResult.transferred_enabled_id 可为 null 或雪花 string（排他启用的迁移目标）', () => {
    const a: LLMDeleteResult = { id: '1111111111111111111', transferred_enabled_id: null };
    const b: LLMDeleteResult = {
      id: '1111111111111111111',
      transferred_enabled_id: '2222222222222222222',
    };
    expect(a.transferred_enabled_id).toBeNull();
    expect(b.transferred_enabled_id).toBe('2222222222222222222');
  });

  it('LLMEnableResult 含 id 与 enabled', () => {
    const r: LLMEnableResult = { id: '1111111111111111111', enabled: true };
    expect(r.enabled).toBe(true);
  });
});

describe('集成密钥域 DTO', () => {
  it('IntegrationSecretView 含 configured 标志与 secret_masked 脱敏字段', () => {
    const view: IntegrationSecretView = {
      id: '3333333333333333333',
      secret_masked: '****5678',
      configured: true,
      version: 1,
    };
    expect(view.configured).toBe(true);
    expect(view.secret_masked).toContain('****');
  });

  it('IntegrationSecretDetail 含明文 secret', () => {
    const detail: IntegrationSecretDetail = {
      id: '3333333333333333333',
      secret: 'plaintext-secret',
    };
    expect(detail.secret).toBe('plaintext-secret');
  });

  it('UpdateSecretPayload 含 RSA 加密 secret 与独立 keyId', () => {
    const payload: UpdateSecretPayload = {
      secret: 'RSA_BASE64_CIPHER',
      keyId: 'kid-xyz',
      version: 1,
    };
    expect(payload.keyId).toBe('kid-xyz');
  });

  it('SecretTestResult 含 connected 标志', () => {
    const r: SecretTestResult = { connected: true };
    expect(r.connected).toBe(true);
  });
});
