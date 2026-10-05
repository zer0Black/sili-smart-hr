// 看板域页面侧私有类型与口径（specs P2_TMD_001 §4.1/§4.2）。
// 跨域共享的接口契约在 lib/contracts.ts。

/** 能力类型（趋势页 A2 type 参数与页面 tab 共用）。 */
export type DashboardAbilityType = 'use' | 'manage';

/** type 非法值按 use 兜底不报错（specs §5.2.2 步2）。 */
export function normalizeAbilityType(v: string | undefined): DashboardAbilityType {
  return v === 'manage' ? 'manage' : 'use';
}
