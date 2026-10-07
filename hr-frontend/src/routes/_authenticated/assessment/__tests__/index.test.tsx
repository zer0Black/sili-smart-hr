import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type { BatchListPage, BatchPlan, BatchStats } from '@/lib/contracts';
import { routeTree } from '@/routeTree.gen';
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

/** 路由级渲染：真实 routeTree + 独立 QueryClient，直链形态可携 search。 */
async function renderAt(search?: Record<string, string>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({ routeTree, context: { queryClient: qc }, defaultPreload: 'intent' });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await router.navigate({ to: '/assessment', ...(search ? { search } : {}) });
  return router;
}

/** 取指定 testType 桩最近一次 props。 */
function lastTableProps(testType: string): Record<string, unknown> {
  const calls = testTaskTablePropsSpy.mock.calls
    .map((c) => c[0] as { testType: string })
    .filter((c) => c.testType === testType);
  return calls[calls.length - 1] as unknown as Record<string, unknown>;
}

beforeEach(() => {
  useAuthStore.setState({ token: 'test-token' });
  // TanStack Router scroll-restoration 调 window.scrollTo；next-themes 读 matchMedia（jsdom 未实现）
  vi.stubGlobal('scrollTo', vi.fn());
  if (!window.matchMedia) {
    const mq = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() };
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue(mq));
  }
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('AssessmentCenterPage 路由页骨架', () => {
  it('渲染页面标题与三 tab', async () => {
    useAuthStore.setState({ token: 'test-token' });
    await renderAt();

    expect(screen.getByRole('heading', { name: '评测运营中心' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 管理能力' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '九型人格' })).toBeInTheDocument();
  });

  it('TestThreeTabsMount：三 tab 切换后两 TestTaskTable 实例均挂载（隐藏态）（§4.1.5）', async () => {
    useAuthStore.setState({ token: 'test-token' });
    await renderAt();
    testTaskTablePropsSpy.mockClear();

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
    await renderAt();
    testTaskTablePropsSpy.mockClear();

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
    await renderAt();
    batchTablePropsSpy.mockClear();
    createBatchPropsSpy.mockClear();

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

    await renderAt();

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

// URL 参数预填（specs §4.1.3 逾期统计卡跳转、§7.2 P2_TST_001 行）：逾期卡携
// tab=ai_mgmt|enneagram 与 status=expired 深链进入，切 tab 并预置已逾期状态筛选，
// 消费后清参（一次性深链，profile 先例）；无参数进入行为与现状一致。
describe('URL 参数预填（specs §4.1.3 / §7.2）', () => {
  it('TestPrefillTabAndExpiredStatus：携 tab=ai_mgmt&status=expired 进入切 tab 且对应表初值 expired，URL 清参（§4.1.3 逾期卡跳转）', async () => {
    testTaskTablePropsSpy.mockClear();

    const router = await renderAt({ tab: 'ai_mgmt', status: 'expired' });

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: 'AI 管理能力' })).toHaveAttribute('aria-selected', 'true'),
    );
    // 预填只落命中的 tab 表：ai_mgmt 表初值 expired、enneagram 表不预填
    await waitFor(() => expect(lastTableProps('ai_mgmt').initialStatusFilter).toBe('expired'));
    expect(lastTableProps('enneagram').initialStatusFilter).toBeUndefined();
    // 消费后清参（replace）
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it('TestPrefillTabOnly：仅携 tab=enneagram 进入只切 tab 不预填筛选', async () => {
    testTaskTablePropsSpy.mockClear();

    await renderAt({ tab: 'enneagram' });

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: '九型人格' })).toHaveAttribute('aria-selected', 'true'),
    );
    await waitFor(() => expect(lastTableProps('enneagram').initialStatusFilter).toBeUndefined());
    expect(lastTableProps('ai_mgmt').initialStatusFilter).toBeUndefined();
  });

  it('TestPrefillAiUsageTab：tab=aiUsage 时 status 参数被忽略（仅清参不切 tab，默认即 aiUsage）', async () => {
    testTaskTablePropsSpy.mockClear();

    const router = await renderAt({ tab: 'aiUsage', status: 'expired' });

    // 默认 tab 保持 aiUsage
    await waitFor(() =>
      expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toHaveAttribute('aria-selected', 'true'),
    );
    await waitFor(() => expect(lastTableProps('ai_mgmt').initialStatusFilter).toBeUndefined());
    expect(lastTableProps('enneagram').initialStatusFilter).toBeUndefined();
    // status 参数一并清掉
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it('TestPrefillInvalidValues：非法 tab/status 值按 undefined 处理，仅清参不改默认行为', async () => {
    testTaskTablePropsSpy.mockClear();

    const router = await renderAt({ tab: 'hacker', status: 'completed' });

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toHaveAttribute('aria-selected', 'true'),
    );
    await waitFor(() => expect(lastTableProps('ai_mgmt').initialStatusFilter).toBeUndefined());
    expect(lastTableProps('enneagram').initialStatusFilter).toBeUndefined();
    await waitFor(() => expect(router.state.location.search).toEqual({}));
  });

  it('TestPrefillStateSurvivesClear：清参重渲染后 tab 与预填值保持（不随清参重置）', async () => {
    testTaskTablePropsSpy.mockClear();

    const router = await renderAt({ tab: 'ai_mgmt', status: 'expired' });

    await waitFor(() => expect(router.state.location.search).toEqual({}));
    // 清参后重渲染：tab 仍 ai_mgmt、预填值仍传 expired（惰性初始化只读一次）
    expect(screen.getByRole('tab', { name: 'AI 管理能力' })).toHaveAttribute('aria-selected', 'true');
    expect(lastTableProps('ai_mgmt').initialStatusFilter).toBe('expired');
  });

  it('TestNoParamsUnchanged：无参数进入默认 aiUsage、无预填（回归锚点）', async () => {
    testTaskTablePropsSpy.mockClear();

    await renderAt();

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toHaveAttribute('aria-selected', 'true'),
    );
    await waitFor(() => expect(lastTableProps('ai_mgmt').initialStatusFilter).toBeUndefined());
    expect(lastTableProps('enneagram').initialStatusFilter).toBeUndefined();
  });
});
