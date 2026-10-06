// 看板域页面侧私有类型与口径（specs P2_TMD_001 §4.1/§4.2）。
// 跨域共享的接口契约在 lib/contracts.ts。

/** 能力类型（趋势页 A2 type 参数与页面 tab 共用）。 */
export type DashboardAbilityType = 'use' | 'manage';

/** type 非法值按 use 兜底不报错（specs §5.2.2 步2）。 */
export function normalizeAbilityType(v: string | undefined): DashboardAbilityType {
  return v === 'manage' ? 'manage' : 'use';
}

/** 环比方向色（specs §4.2.2：上升 chart-2、下降 warning、持平灰），趋势页与分面卡/变化表共用。 */
export const CHANGE_UP = 'text-chart-2';
export const CHANGE_DOWN = 'text-warning';
export const CHANGE_FLAT = 'text-muted-foreground';

/** 环比数值的方向色类（正升负降零持平）。 */
export function changeColorClass(v: number): string {
  return v > 0 ? CHANGE_UP : v < 0 ? CHANGE_DOWN : CHANGE_FLAT;
}
