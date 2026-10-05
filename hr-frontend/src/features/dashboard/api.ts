import { httpClient } from '@/lib/http-client';
import type { DashboardOverview, DashboardPeriodItem, DashboardTrend } from '@/lib/contracts';

import type { DashboardAbilityType } from './types';

/** GET /api/dashboard：看板全量。period 传时发区间双参，不传双省略取最新（03 §1 A1）。 */
export async function fetchDashboardOverview(
  period?: DashboardPeriodItem,
): Promise<DashboardOverview> {
  const { data } = await httpClient.get<DashboardOverview>('/dashboard', {
    params: {
      period_start: period?.period_start,
      period_end: period?.period_end,
    },
  });
  return data;
}

/** GET /api/dashboard/trend：能力逐期趋势（03 §1 A2）。 */
export async function fetchDashboardTrend(
  type: DashboardAbilityType,
): Promise<DashboardTrend> {
  const { data } = await httpClient.get<DashboardTrend>('/dashboard/trend', {
    params: { type },
  });
  return data;
}
