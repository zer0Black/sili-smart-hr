import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchListPage, BatchPlan, BatchStats } from '@/lib/contracts';
import { useAuthStore } from '@/stores/auth';

await i18n.changeLanguage('zh');

// mock BatchTable 捕获 props（包含 resetKey）
const batchTablePropsSpy = vi.hoisted(() => vi.fn());
vi.mock('@/features/assessment/components/batch-table', () => ({
  BatchTable: (props: unknown) => {
    batchTablePropsSpy(props);
    return <div data-testid="batch-table-stub" />;
  },
}));

vi.mock('@/features/assessment/components/stats-cards', () => ({
  StatsCards: () => <div data-testid="stats-cards-stub" />,
}));

// mock CreateBatchDialog / FailureDetailDialog：仅捕获 onSubmitted 行为
const createBatchPropsSpy = vi.hoisted(() => vi.fn());
vi.mock('@/features/assessment/components/create-batch-dialog', () => ({
  CreateBatchDialog: (props: { onSubmitted: () => void; onClose: () => void; open: boolean }) => {
    createBatchPropsSpy(props);
    return props.open ? <div data-testid="create-dialog-stub" /> : null;
  },
}));
vi.mock('@/features/assessment/components/failure-detail-dialog', () => ({
  FailureDetailDialog: () => null,
}));

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
  it('渲染页面标题与 AI 使用能力 tab', () => {
    useAuthStore.setState({ token: 'test-token' });
    renderPage(<AssessmentCenterPage />);

    expect(screen.getByRole('heading', { name: '评测运营中心' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toBeInTheDocument();
  });

  it('TestPageSubmitResetKey：CreateBatchDialog 提交成功回调使 BatchTable 收到递增 resetKey（§4.2.3）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    batchTablePropsSpy.mockClear();
    createBatchPropsSpy.mockClear();

    renderPage(<AssessmentCenterPage />);

    // 初始渲染 BatchTable 收到 resetKey=0
    await waitFor(() => expect(batchTablePropsSpy).toHaveBeenCalled());
    const initialKeys = batchTablePropsSpy.mock.calls.map((c) => (c[0] as { resetKey?: number }).resetKey);
    expect(initialKeys).toContain(0);
    expect(initialKeys).not.toContain(1);

    // 触发 CreateBatchDialog 的 onSubmitted（等价提交成功）
    const lastCall = createBatchPropsSpy.mock.calls[createBatchPropsSpy.mock.calls.length - 1];
    const dialogProps = lastCall[0] as { onSubmitted: () => void };
    dialogProps.onSubmitted();

    await waitFor(() => {
      const keys = batchTablePropsSpy.mock.calls.map(
        (c) => (c[0] as { resetKey?: number }).resetKey,
      );
      expect(keys).toContain(1);
    });
  });
});
