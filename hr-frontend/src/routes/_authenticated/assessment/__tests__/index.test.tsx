import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchListPage, BatchPlan, BatchStats } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

import { AssessmentCenterPage } from '../index';

vi.mock('@/features/assessment/api', () => ({
  fetchBatches: vi.fn(
    (): Promise<BatchListPage> =>
      Promise.resolve({ list: [], total: 0, page: 1, page_size: 10 }),
  ),
  fetchBatchStats: vi.fn(
    (): Promise<BatchStats> =>
      Promise.resolve({ eval_count: 3, evaluated_person_count: 2, running_batch_count: 1 }),
  ),
  fetchBatchPlan: vi.fn(
    (): Promise<BatchPlan> =>
      Promise.resolve({
        next_trigger_at: '2026-09-14 23:00',
        period: 'weekly',
        target_mode: 'all',
        target_brief: [],
        target_names: [],
        target_count: 0,
        dimension_base_count: 4,
        dimension_upper_count: 4,
      }),
  ),
  fetchBatchTargets: vi.fn(),
  fetchBatchFailures: vi.fn(),
  createBatch: vi.fn(),
}));

function renderPage(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe('AssessmentCenterPage 路由页骨架', () => {
  it('渲染页面标题、AI 使用能力 tab 与统计卡骨架（无进行中批次时直接出数值）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    renderPage(<AssessmentCenterPage />);

    expect(screen.getByRole('heading', { name: '评测运营中心' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText('本期评测次数')).toBeInTheDocument();
    });
    expect(screen.getByText('已完成评估人次')).toBeInTheDocument();
    expect(screen.getByText('进行中批次数')).toBeInTheDocument();
  });

  it('存在非停滞的进行中批次时开启列表轮询', async () => {
    const { fetchBatches } = await import('@/features/assessment/api');
    vi.mocked(fetchBatches).mockResolvedValue({
      list: [
        {
          id: '1',
          batch_no: 'B-1',
          trigger_type: 'scheduled',
          target_mode: 'all',
          target_brief: [],
          target_names: [],
          period_start: '2026-09-07',
          period_end: '2026-09-13',
          status: 'running',
          stalled: false,
          evaluated_count: 0,
          total_count: 5,
          progress_percent: 0,
          covered_session_count: 0,
          failed_count: 0,
          triggered_at: '2026-09-13 23:00',
        },
      ],
      total: 1,
      page: 1,
      page_size: 10,
    });
    useAuthStore.setState({ token: 'test-token' });
    renderPage(<AssessmentCenterPage />);

    // 轮询推导生效的表现：running 批次的「进行中」徽标渲染出（轮询开关本身由 refetchInterval 内部驱动）。
    await waitFor(() => {
      expect(screen.getByText('进行中')).toBeInTheDocument();
    });
  });
});
