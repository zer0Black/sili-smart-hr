// 维度域 feature 私有类型与前端硬编码常量单一来源。
// 展示文案一律走 i18n（dimension ns），此处只承载非文案的派生数据。

/** 模块固定顺序（specs §4.1.5）。 */
export const MODULE_ORDER = ['ACTIVITY', 'AI_USAGE', 'AI_MGMT', 'ENNEAGRAM'] as const;
export type ModuleCode = (typeof MODULE_ORDER)[number];

export type GroupCode = 'BASE' | 'UPPER';
/** 分组固定顺序（仅 AI_USAGE），展示名走 i18n group.*。 */
export const GROUP_ORDER = ['BASE', 'UPPER'] as const;
export type DataSource = 'RULE' | 'CONVERSATION' | 'TEST';

/** 模块 UI 派生属性：数据来源、权重控件禁用、参考性标记。 */
export interface ModuleMeta {
  dataSource: DataSource;
  weightDisabled: boolean;
  isReference: boolean;
}

/** 四模块 UI 派生属性（specs §4.2.4 规则1 + §4.1.4 规则5 + §4.1.4 规则8）。 */
export const MODULE_META: Record<ModuleCode, ModuleMeta> = {
  ACTIVITY: { dataSource: 'RULE', weightDisabled: true, isReference: false },
  AI_USAGE: { dataSource: 'CONVERSATION', weightDisabled: false, isReference: false },
  AI_MGMT: { dataSource: 'TEST', weightDisabled: false, isReference: false },
  ENNEAGRAM: { dataSource: 'TEST', weightDisabled: true, isReference: true },
};

/** 模块联动默认值（specs §4.2.4 规则1）。 */
export const MODULE_DEFAULTS: Record<ModuleCode, { weight: number; includeOverview: boolean }> = {
  ACTIVITY: { weight: 0, includeOverview: false },
  AI_USAGE: { weight: 5, includeOverview: true },
  AI_MGMT: { weight: 5, includeOverview: true },
  ENNEAGRAM: { weight: 0, includeOverview: false },
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
