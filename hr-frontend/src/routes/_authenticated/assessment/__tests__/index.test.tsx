import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

// mock TestTaskTable 捕获 props（testType/polling 等两 tab 实例形态）
const testTaskTablePropsSpy = vi.hoisted(() => vi.fn());
vi.mock('@/features/assessment/components/test-task-table', () => ({
  TestTaskTable: (props: unknown) => {
    testTaskTablePropsSpy(props);
    return <div data-testid="test-task-table-stub" />;
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

vi.mock('@/features/assessment/test-task-api', () => ({
  fetchTestTasks: vi.fn(() => Promise.resolve({ list: [], total: 0, page: 1, page_size: 10 })),
  fetchTestTaskPollCounts: vi.fn(() => Promise.resolve({ ai_mgmt_active: 0, enneagram_active: 0 })),
  fetchScaleStatus: vi.fn(),
  fetchTestTaskLink: vi.fn(),
  resendTestTaskLink: vi.fn(),
  cancelTestTask: vi.fn(),
}));

function renderPage(node: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe('AssessmentCenterPage 路由页骨架', () => {
  it('渲染页面标题与三 tab', () => {
    useAuthStore.setState({ token: 'test-token' });
    renderPage(<AssessmentCenterPage />);

    expect(screen.getByRole('heading', { name: '评测运营中心' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 管理能力' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '九型人格' })).toBeInTheDocument();
  });

  it('TestThreeTabsMount：三 tab 切换后两 TestTaskTable 实例均挂载（隐藏态）（§4.1.5）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    testTaskTablePropsSpy.mockClear();
    renderPage(<AssessmentCenterPage />);

    // 初始挂载即有两实例（常驻挂载，隐藏非激活 tab）
    await waitFor(() => expect(testTaskTablePropsSpy).toHaveBeenCalledTimes(2));
    const types = testTaskTablePropsSpy.mock.calls.map(
      (c) => (c[0] as { testType: string }).testType,
    );
    expect(types).toEqual(expect.arrayContaining(['ai_mgmt', 'enneagram']));

    // 切到九型 tab 后两实例仍挂载（不清空）
    fireEvent.click(screen.getByRole('tab', { name: '九型人格' }));
    await waitFor(() => expect(screen.getAllByTestId('test-task-table-stub')).toHaveLength(2));
    // 切回 AI 管理能力 tab 仍两实例
    fireEvent.click(screen.getByRole('tab', { name: 'AI 管理能力' }));
    await waitFor(() => expect(screen.getAllByTestId('test-task-table-stub')).toHaveLength(2));
  });

  it('TestTabSwitchKeepsFilter：切 tab 不重置另一 tab 的 filter（§4.1.5）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    testTaskTablePropsSpy.mockClear();
    renderPage(<AssessmentCenterPage />);

    // 初始两实例各挂载一次
    await waitFor(() => expect(screen.getAllByTestId('test-task-table-stub')).toHaveLength(2));
    // 切换 tab 两个来回，TestTaskTable 桩不重挂载（DOM 实例数恒为 2，即状态未清）
    fireEvent.click(screen.getByRole('tab', { name: '九型人格' }));
    fireEvent.click(screen.getByRole('tab', { name: 'AI 管理能力' }));
    await waitFor(() => expect(screen.getAllByTestId('test-task-table-stub')).toHaveLength(2));
    // 同一 testType 的桩节点跨 tab 切换保持同一 DOM 引用（React key 未变未重挂载）
    const firstAiNode = screen.getAllByTestId('test-task-table-stub')[0];
    fireEvent.click(screen.getByRole('tab', { name: '九型人格' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: '九型人格' })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.getAllByTestId('test-task-table-stub')[0]).toBe(firstAiNode);
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

  it('TestPollingProp：poll-counts 计数驱动当前 tab 列表 polling 开关（§4.1.3 轮询）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    testTaskTablePropsSpy.mockClear();
    const { fetchTestTaskPollCounts } = await import('@/features/assessment/test-task-api');
    vi.mocked(fetchTestTaskPollCounts).mockResolvedValue({ ai_mgmt_active: 2, enneagram_active: 0 });

    renderPage(<AssessmentCenterPage />);

    // poll-counts 数据到达后：ai_mgmt_active=2 → AI 管理能力 tab polling=true，九型 tab polling=false
    await waitFor(() => {
      const calls = testTaskTablePropsSpy.mock.calls.map(
        (c) => c[0] as { testType: string; polling?: boolean },
      );
      const ai = calls.filter((c) => c.testType === 'ai_mgmt').slice(-1)[0];
      const enneagram = calls.filter((c) => c.testType === 'enneagram').slice(-1)[0];
      expect(ai?.polling).toBe(true);
      expect(enneagram?.polling).toBe(false);
    });
  });
});

// userEvent 保留给后续交互扩展（当前用例以 fireEvent 为主）
void userEvent;
