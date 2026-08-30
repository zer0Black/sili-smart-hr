/**
 * 数据策略分析：
 * - 维度名称/锚点/提示词/说明：类型 B（测试者输入，树内可见性断言需唯一，E2E_ 前缀 + 时间戳后缀）
 * - 维度编码：类型 C（服务端生成，正则断言前缀，不引用）
 * - 维度 id/version：类型 D3（服务端生成，afterAll 清理用 GET /api/dimensions/tree 按名称定位）
 * - 活跃度阈值：类型 A（系统单例，beforeAll 备份 afterAll 恢复）
 */

// 唯一后缀：本套件内单调递增，E2E_ 前缀保证与人工数据可区分
let seq = 0;
export function uniqueSuffix(): string {
  seq += 1;
  const stamp = Date.now().toString(36);
  return `E2E_${stamp}_${seq}`;
}

// 既有维度（探索时确认，只读断言对象，不修改）
export const EXISTING = {
  LEAF_NAME: '23',           // AI 使用能力/底层能力下，权重 21%，启用
  OTHER_LEAF_NAME: '测试',   // AI 使用能力/底层能力下，权重 12%
  MODULE_AI_USAGE: 'AI 使用能力',
  MODULE_ACTIVITY: '使用活跃度',
  MODULE_AI_MGMT: 'AI 管理能力',
  MODULE_ENNEAGRAM: '九型人格',
  GROUP_BASE: '底层能力',
  GROUP_UPPER: '上层能力',
  TOTAL_WEIGHT_TEXT: '33%',  // 21% + 12%
};

// 模块 code（后端 domain 常量），API 清理用
export const MODULE_CODE = {
  ACTIVITY: 'ACTIVITY',
  AI_USAGE: 'AI_USAGE',
  AI_MGMT: 'AI_MGMT',
  ENNEAGRAM: 'ENNEAGRAM',
} as const;

// 新增维度有效数据（对话分析模板，名称运行时替换唯一后缀）
export const VALID_CREATE = {
  prompt: 'E2E 测试评分提示词：根据对话中工具使用的深度与广度评分',
  anchor: '高:8-10分 中:4-7分 低:1-3分',
  description: 'E2E 自动化测试创建的维度',
  weight: 5,
};

// 边界长度（spec 4.1.2 B：名称 2~30）
export const BOUNDARY = {
  NAME_MIN: 2,
  NAME_MAX: 30,
  NAME_OVER: 31,
};

// 活跃度阈值（spec 4.1.2 C：默认 10/5，1~999，低频须小于活跃）
export const ACTIVITY_RULE = {
  DEFAULT_ACTIVE: 10,
  DEFAULT_LOW: 5,
  TEST_ACTIVE: 20,
  TEST_LOW: 8,
};

// 重复字符串构造（边界测试用）。上边界用 ASCII 字符：中文字符拼音编码展开远超 40 字符上限，
// 后端按 rune 截断后同形状名称会生成相同编码触发 1202 冲突，无法稳定验证 30 字符通过分支
export function repeat(ch: string, n: number): string {
  return ch.repeat(n);
}

// 上边界 30 字符名称素材（ASCII）
export const NAME_BOUNDARY_CHAR = 'a';
