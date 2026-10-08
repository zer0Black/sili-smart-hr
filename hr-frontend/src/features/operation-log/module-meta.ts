// 操作类型八类枚举 → i18n 键与 Badge variant 映射（specs §4.1.4 规则1、§4.1.5 交互逻辑）。
import type { OperationLogItem } from './types';

/** 八类 module → operationLog ns i18n 键（specs §4.1.4 规则1 枚举）。 */
export const moduleI18nKeys: Record<string, string> = {
  login: 'operationLog:module.login',
  account: 'operationLog:module.account',
  dimension: 'operationLog:module.dimension',
  system_params: 'operationLog:module.system_params',
  llm_config: 'operationLog:module.llm_config',
  question_bank: 'operationLog:module.question_bank',
  assessment: 'operationLog:module.assessment',
  system_job: 'operationLog:module.system_job',
};

/** badge.tsx 实际 variant 子集。 */
export type ModuleBadgeTone = 'default' | 'secondary' | 'destructive' | 'outline';

/**
 * 八类 → Badge variant（specs §4.1.5 语义色经 variant + text-* 组合承载）：
 * login info / dimension warning / question_bank success 三色由组件层在 secondary 底上
 * 追加 text-info / text-warning / text-success 类（本表只承载 variant）；
 * llm_config destructive 点缀、assessment default（primary 语义）、system_job outline 弱化
 * 区分人工与自动。
 */
export const moduleBadgeTone: Record<string, ModuleBadgeTone> = {
  login: 'secondary',
  account: 'secondary',
  dimension: 'secondary',
  system_params: 'secondary',
  llm_config: 'destructive',
  question_bank: 'secondary',
  assessment: 'default',
  system_job: 'outline',
};

/** 详情弹窗互斥渲染判据：有 changes 结构渲染对比表，否则文本详情（specs §4.2.5）。 */
export function hasChanges(item: OperationLogItem): boolean {
  return Array.isArray(item.changes) && item.changes.length > 0;
}
