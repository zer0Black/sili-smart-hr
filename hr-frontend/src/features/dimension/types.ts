// 维度域 feature 私有类型与前端硬编码常量单一来源。
// 中文文案当前用直接值，T6 i18n 接入后改为 t() key。

/** 模块固定顺序（specs §4.1.5）。 */
export const MODULE_ORDER = ['ACTIVITY', 'AI_USAGE', 'AI_MGMT', 'ENNEAGRAM'] as const;
export type ModuleCode = (typeof MODULE_ORDER)[number];

export type GroupCode = 'BASE' | 'UPPER';
export type DataSource = 'RULE' | 'CONVERSATION' | 'TEST';

/** 模块元信息：data_source/is_reference/weightDisabled 与 weightHint。 */
export interface ModuleMeta {
  name: string;
  dataSource: DataSource;
  isReference: boolean;
  weightDisabled: boolean;
  weightHint: string;
}

/** 四模块元信息（specs §4.2.4 规则1 + §4.1.4 规则5 + §4.1.4 规则8）。 */
export const MODULE_META: Record<ModuleCode, ModuleMeta> = {
  ACTIVITY: { name: '使用活跃度', dataSource: 'RULE', isReference: false, weightDisabled: true, weightHint: '基线分级维度，不参与能力权重聚合' },
  AI_USAGE: { name: 'AI 使用能力', dataSource: 'CONVERSATION', isReference: false, weightDisabled: false, weightHint: '' },
  AI_MGMT: { name: 'AI 管理能力', dataSource: 'TEST', isReference: false, weightDisabled: false, weightHint: '' },
  ENNEAGRAM: { name: '九型人格', dataSource: 'TEST', isReference: true, weightDisabled: true, weightHint: '参考性维度，不进入硬性聚合，权重无需配置' },
};

/** 分组名（仅 AI_USAGE）。 */
export const GROUP_META: Record<GroupCode, string> = {
  BASE: '底层能力',
  UPPER: '上层能力',
};

/** 模块联动默认值（specs §4.2.4 规则1）。 */
export const MODULE_DEFAULTS: Record<ModuleCode, { weight: number; includeOverview: boolean }> = {
  ACTIVITY: { weight: 0, includeOverview: false },
  AI_USAGE: { weight: 5, includeOverview: true },
  AI_MGMT: { weight: 5, includeOverview: true },
  ENNEAGRAM: { weight: 0, includeOverview: false },
};

/** 数据来源展示标签。 */
export const DATA_SOURCE_META: Record<DataSource, string> = {
  RULE: '规则统计',
  CONVERSATION: '对话分析',
  TEST: '主动测试',
};

/** 编码前缀（仅展示用，编码生成在后端）。 */
export const MODULE_CODE_PREFIX: Record<ModuleCode, string> = {
  ACTIVITY: 'ACT',
  AI_USAGE: 'AI',
  AI_MGMT: 'MGT',
  ENNEAGRAM: 'ENN',
};

/**
 * 选中节点描述（右侧按此切换形态）。
 * ACTIVITY 模块选中态 kind 仍为 'module'，由父组件按 moduleCode==='ACTIVITY' 切换规则面板。
 */
export type SelectedNode =
  | { kind: 'empty' }
  | { kind: 'module'; moduleCode: ModuleCode }
  | { kind: 'group'; moduleCode: ModuleCode; groupCode: GroupCode }
  | { kind: 'leaf'; dimensionId: string };
