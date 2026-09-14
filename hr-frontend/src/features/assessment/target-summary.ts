// 评估对象摘要纯函数：feature 内共享口径（specs §4.1.2B/§4.1.5）。
// all 显「全员」；specified ≤2 人直显，>2 人按「前两人名 等 N 人」。
import type { TFunction } from 'i18next';

export interface TargetSummaryInput {
  target_mode: string;
  target_names: string[];
  target_brief: string[];
  target_count?: number;
}

/** 摘要文本：names 为全量名单，brief 为后端前 2 人摘要（缺省回退 names 前 2）。 */
export function targetSummaryText(
  item: TargetSummaryInput,
  t: TFunction,
): string {
  if (item.target_mode === 'all') return t('table.targetAll');
  const names = item.target_names;
  if (names.length > 2) {
    return t('plan.targetSummary', {
      first: item.target_brief[0] ?? names[0],
      second: item.target_brief[1] ?? names[1],
      count: item.target_count ?? names.length,
    });
  }
  return names.join('、');
}
