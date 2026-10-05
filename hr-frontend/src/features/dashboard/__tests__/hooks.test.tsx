import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';

import { useAuthStore } from '@/stores/auth';
import type { DashboardOverview, DashboardPeriodItem, DashboardTrend } from '@/lib/contracts';

vi.mock('../api', () => ({
  fetchDashboardOverview: vi.fn(),
  fetchDashboardTrend: vi.fn(),
}));

import { fetchDashboardOverview, fetchDashboardTrend } from '../api';
import { useDashboardOverview, useDashboardTrend } from '../hooks';
import { normalizeAbilityType } from '../types';

afterEach(() => {
  useAuthStore.setState({ token: null });
  vi.clearAllMocks();
});

function makeClient() {
  return new QueryClient({
    defaultOptions: {
      // staleTime Infinity：让「回切同区间命中缓存不重发」断言只验证 queryKey 区分，不受 stale 重取语义干扰
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  });
}

function renderWithClient<T, P = undefined>(
  callback: (props: P) => T,
  qc: QueryClient,
  initialProps?: P,
) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return renderHook(callback, { wrapper, initialProps });
}

const periodA: DashboardPeriodItem = { period_start: '2026-09-29', period_end: '2026-10-05', is_current: true };
const periodB: DashboardPeriodItem = { period_start: '2026-09-22', period_end: '2026-09-28', is_current: false };

const emptyOverview: DashboardOverview = {
  periods: [],
  selected_period: null,
  data_updated_at: null,
  staff_total: 0,
  activity: null,
  modules: [],
  enneagram: null,
  suggestion: {
    status: 'none',
    period_start: null,
    period_end: null,
    generated_at: null,
    modules: [],
    summary: '',
  },
};

const emptyTrend: DashboardTrend = {
  type: 'manage',
  module: 'AI_MGMT',
  periods: [],
  current_period: null,
  data_updated_at: null,
  composite: null,
  dimensions: [],
};

describe('dashboard hooks', () => {
  it('TestUseDashboardOverview_DefaultLatest：不传 period 时 fetchDashboardOverview 收到 undefined', async () => {
    vi.mocked(fetchDashboardOverview).mockResolvedValue(emptyOverview);
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(() => useDashboardOverview(), qc);

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(fetchDashboardOverview).toHaveBeenCalledTimes(1);
    expect(fetchDashboardOverview).toHaveBeenCalledWith(undefined);
  });

  it('TestUseDashboardOverview_PeriodKey：period A 与 period B queryKey 不同，切区间各发一次请求不共缓存', async () => {
    const overviewA: DashboardOverview = { ...emptyOverview, staff_total: 100 };
    const overviewB: DashboardOverview = { ...emptyOverview, staff_total: 200 };
    vi.mocked(fetchDashboardOverview)
      .mockResolvedValueOnce(overviewA)
      .mockResolvedValueOnce(overviewB);
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result, rerender } = renderWithClient(
      ({ period }: { period?: DashboardPeriodItem }) => useDashboardOverview(period),
      qc,
      { period: periodA },
    );

    await waitFor(() => expect(result.current.data?.staff_total).toBe(100));
    expect(fetchDashboardOverview).toHaveBeenCalledTimes(1);
    expect(fetchDashboardOverview).toHaveBeenLastCalledWith(periodA);

    rerender({ period: periodB });
    await waitFor(() => expect(result.current.data?.staff_total).toBe(200));
    expect(fetchDashboardOverview).toHaveBeenCalledTimes(2);
    expect(fetchDashboardOverview).toHaveBeenLastCalledWith(periodB);

    // 回切 A 命中缓存不再发请求（同区间复用，区间隔离的另一半证据）
    rerender({ period: periodA });
    await waitFor(() => expect(result.current.data?.staff_total).toBe(100));
    expect(fetchDashboardOverview).toHaveBeenCalledTimes(2);
  });

  it('TestUseDashboardTrend_TypeParam：type=manage 时 fetchDashboardTrend 收到 manage', async () => {
    vi.mocked(fetchDashboardTrend).mockResolvedValue(emptyTrend);
    useAuthStore.setState({ token: 'test-token' });

    const qc = makeClient();
    const { result } = renderWithClient(() => useDashboardTrend('manage'), qc);

    await waitFor(() => expect(result.current.data).toBeDefined());
    expect(fetchDashboardTrend).toHaveBeenCalledTimes(1);
    expect(fetchDashboardTrend).toHaveBeenCalledWith('manage');
  });

  it('TestNormalizeAbilityType：manage 原样返回，非法值与 undefined 兜底为 use', () => {
    expect(normalizeAbilityType('manage')).toBe('manage');
    expect(normalizeAbilityType('xyz')).toBe('use');
    expect(normalizeAbilityType(undefined)).toBe('use');
  });
});
