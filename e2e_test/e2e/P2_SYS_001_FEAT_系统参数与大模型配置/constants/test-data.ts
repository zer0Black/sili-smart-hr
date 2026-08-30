/**
 * 数据策略分析：
 * - 周期长度/触发时点/评估对象：类型 A（测试者输入，无唯一性）
 * - 模型名称/模型 ID/服务商/API 地址/API Key：类型 A（spec 无唯一性约束）
 * - 集成密钥值：类型 A
 * - 模型主键 id：类型 D3（服务端雪花 ID，afterAll 清理需用，GET /api/llm-configs 查名称定位 id 再 DELETE）
 * spec 全程无字段唯一性约束，故无类型 B、无唯一性测试。
 */

// 系统参数页默认值（spec 4.1.2 / 4.1.3）
export const DEFAULT_PERIOD = '每周';
export const DEFAULT_TRIGGER_TIME = '23:00';
export const DEFAULT_TARGET = '全员';

// 触发日与评估区间期望文案（spec 4.1.4 规则3）。i18n 资源实际值见 zh.json。
export const TRIGGER_DAY_TEXT: Record<string, string> = {
  daily: '每日',
  weekly: '每周日',
  monthly: '每月最后一天',
};
export const INTERVAL_TEXT: Record<string, string> = {
  daily: '每日触发，抽取当日对话',
  weekly: '每周日触发，抽取本周一至本周日对话',
  monthly: '每月最后一天触发，抽取本月 1 日至月末对话',
};

// 周期长度下拉选项标签
export const PERIOD_OPTIONS = {
  daily: '每天',
  weekly: '每周',
  monthly: '每月',
};

// 大模型配置页模型测试数据（类型 A）
export const TEST_DATA = {
  // 首个模型（验证自动启用）
  FIRST_MODEL: {
    name: 'E2E主力模型',
    provider: 'DeepSeek',
    modelId: 'deepseek-chat',
    apiUrl: '', // 留空走默认
    apiKey: 'sk-e2e-first-00112233445566778899aabbcc',
  },
  // 第二个模型（验证默认停用）
  SECOND_MODEL: {
    name: 'E2E备用模型',
    provider: '智谱 GLM',
    modelId: 'glm-4-plus',
    apiUrl: 'https://open.bigmodel.cn/api/paas/v4',
    apiKey: 'sk-e2e-second-99887766554433221100ffeedd',
  },
  // 编辑用：改名称
  EDITED_NAME: 'E2E主力模型(改)',
  // 指定长度边界测试用
  BOUNDARY: {
    NAME_MAX: 50,
    NAME_OVER: 51,
    MODEL_ID_MAX: 100,
    MODEL_ID_OVER: 101,
    API_KEY_MAX: 200,
    API_KEY_OVER: 201,
    API_URL_MAX: 500,
  },
};

// 集成密钥测试数据（类型 A）
export const SECRET_DATA = {
  VALUE: 'bearer-e2e-secret-Aa1234567890',
};

// 生成定长字符串（长度边界用）
export function repeat(ch: string, n: number): string {
  return ch.repeat(n);
}
