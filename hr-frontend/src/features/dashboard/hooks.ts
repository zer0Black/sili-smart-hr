import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { ApiError } from '@/lib/http-client';
import type { DashboardOverview, DashboardPeriodItem, DashboardTrend } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

import { fetchDashboardOverview, fetchDashboardTrend } from './api';
import type { DashboardAbilityType } from './types';

/** useDashboardOverview：看板全量查询。queryKey 含区间双界，切区间整体刷新不共缓存
 * （specs §4.1.4 规则1），无区间参数时用 'latest' 槽位。 */
export function useDashboardOverview(
  period?: DashboardPeriodItem,
): UseQueryResult<DashboardOverview, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<DashboardOverview, ApiError>({
    queryKey: [
      'dashboard',
      'overview',
      period?.period_start ?? 'latest',
      period?.period_end ?? 'latest',
    ],
    queryFn: () => fetchDashboardOverview(period),
    enabled: !!token,
  });
}

/** useDashboardTrend：能力逐期趋势查询。数据固定取最新期次，key 仅含 type。 */
export function useDashboardTrend(
  type: DashboardAbilityType,
): UseQueryResult<DashboardTrend, ApiError> {
  const token = useAuthStore((s) => s.token);
  return useQuery<DashboardTrend, ApiError>({
    queryKey: ['dashboard', 'trend', type],
    queryFn: () => fetchDashboardTrend(type),
    enabled: !!token,
  });
}
