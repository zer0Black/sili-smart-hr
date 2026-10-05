// 团队看板页路由测试（specs P2_TMD_001 §4.1.1、§4.1.3、§4.1.4 规则1、§4.1.5）：
// 页面组装（三态卡/两雷达卡/九型区/建议区）+ 空态（/assessment 入口）+
// 1305 整页错误重试 + 区间切换双参重查与切换期加载态（防混渲染）。
// 照 profile/__tests__ 形态：真实 routeTree + mock api 模块，断言消费真实 zh.json。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import i18n from '@/i18n/config';
import type { DashboardOverview } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { routeTree } from '@/routeTree.gen';
import { useAuthStore } from '@/stores/auth';

const overviewMock = vi.hoisted(() => vi.fn());
const setupMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/dashboard/api', () => ({
  fetchDashboardOverview: overviewMock,
  fetchDashboardTrend: vi.fn(),
}));
// __root beforeLoad 的 setup 探针
vi.mock('@/features/system/api', () => ({ fetchSetupStatus: setupMock }));

// 直接消费真实 zh.json 的 dashboard 命名空间（i18n/config 已同步注册）
beforeAll(async () => {
  await i18n.changeLanguage('zh');
});

// Radix Select 在 jsdom 缺 pointer capture API（profile 测试同款桩）
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

function makeOverview(o: Partial<DashboardOverview> = {}): DashboardOverview {
  return {
    periods: [
      { period_start: '2026-09-22', period_end: '2026-09-28', is_current: true },
      { period_start: '2026-09-15', period_end: '2026-09-21', is_current: false },
    ],
    selected_period: { period_start: '2026-09-22', period_end: '2026-09-28', is_current: true },
    data_updated_at: '2026-09-28 10:00',
    staff_total: 120,
    activity: {
      active: { count: 50, ratio: 41.7 },
      low_freq: { count: 30, ratio: 25.0 },
      unused: { count: 40, ratio: 33.3 },
      mom: { active_change: 5, unused_change: -3 },
    },
    modules: [
      {
        module: 'AI_USAGE',
        overall_avg: 65,
        dimensions: [
          { dimension_code: 'AI_COMMUNICATION', dimension_name: '沟通表达', avg_score: 72, low_ratio: 12.5, is_weakness: false },
          { dimension_code: 'AI_WRITING', dimension_name: '文档写作', avg_score: 58, low_ratio: 45.0, is_weakness: true },
        ],
      },
      {
        module: 'AI_MGMT',
        overall_avg: null,
        dimensions: [
          { dimension_code: 'MGMT_PLAN', dimension_name: '任务规划', avg_score: null, low_ratio: null, is_weakness: false },
          { dimension_code: 'MGMT_REVIEW', dimension_name: '复盘改进', avg_score: 61, low_ratio: 30.0, is_weakness: true },
        ],
      },
    ],
    enneagram: {
      scored_count: 36,
      coverage_ratio: 30.0,
      distribution: ['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((k, i) => ({
        type: k,
        count: i === 4 ? 9 : 27 / 8,
        ratio: i === 4 ? 25.0 : 9.4,
      })),
      dominant_type: '5',
      dominant_ratio: 25.0,
      secondary_type: '3',
      secondary_ratio: 16.7,
    },
    suggestion: {
      status: 'generated',
      period_start: '2026-09-22',
      period_end: '2026-09-28',
      generated_at: '2026-09-28 10:05',
      modules: [
        { module: 'AI_USAGE', suggestions: [{ name: '提示词工程专项演练', description: '针对文档写作短板组织 workshop' }] },
        { module: 'AI_MGMT', suggestions: [{ name: '管理场景案例复盘', description: '围绕任务规划开展复盘' }] },
      ],
      summary: '整体使用能力稳中有升，管理能力待样本积累。',
    },
    ...o,
  };
}

function emptyOverview(): DashboardOverview {
  return makeOverview({
    periods: [],
    selected_period: null,
    data_updated_at: null,
    staff_total: 0,
    activity: null,
    modules: [],
    enneagram: null,
    suggestion: { status: 'none', period_start: null, period_end: null, generated_at: null, modules: [], summary: '' },
  });
}

/** 路由级渲染：真实 routeTree + 独立 QueryClient。 */
async function renderAt(to: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({ routeTree, context: { queryClient: qc }, defaultPreload: 'intent' });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await router.navigate({ to });
  return router;
}

async function openSelect(trigger: HTMLElement, optionText: string) {
  const user = userEvent.setup();
  await user.click(trigger);
  await user.click(await screen.findByRole('option', { name: optionText }));
}

beforeEach(() => {
  useAuthStore.setState({ token: 'test-token' });
  // TanStack Router scroll-restoration 调 window.scrollTo；next-themes 读 matchMedia
  vi.stubGlobal('scrollTo', vi.fn());
  if (!window.matchMedia) {
    const mq = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() };
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue(mq));
  }
  setupMock.mockResolvedValue({ initialized: true });
  overviewMock.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('团队看板页 /dashboard（specs §4.1）', () => {
  it('TestDashboardPage_RendersBlocks：五区块组装，三态卡与两雷达卡、九型区、建议区齐备（§4.1.1）', async () => {
    overviewMock.mockResolvedValue(makeOverview());

    await renderAt('/dashboard');

    // 首查不携区间（undefined = 最新区间，§4.1.3 页面加载自动查询）
    await waitFor(() => expect(overviewMock).toHaveBeenCalledWith(undefined));
    // 活跃度概览：三态卡（StatCard 以 aria-label 暴露角色名）
    expect(await screen.findByText('活跃度概览')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '活跃' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '低频' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '未使用' })).toBeInTheDocument();
    // 两模块雷达卡标题与数据来源说明
    expect(screen.getAllByText('AI 使用能力').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('AI 管理能力').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('走对话分析')).toBeInTheDocument();
    expect(screen.getByText('走主动测试场景题')).toBeInTheDocument();
    // 共性短板标签区（两雷达卡各一处标题，本期短板维度名渲染）
    expect(screen.getAllByText('共性短板').length).toBeGreaterThanOrEqual(1);
    // 九型区：标题 + 最新快照标注 + 构成摘要
    expect(screen.getByText('团队人格构成')).toBeInTheDocument();
    expect(screen.getByText('最新快照')).toBeInTheDocument();
    expect(screen.getAllByText('智慧型').length).toBeGreaterThanOrEqual(1);
    // 建议区：标题 + 建议内容与综合研判
    expect(screen.getByText('团队培训方向建议')).toBeInTheDocument();
    expect(screen.getByText('提示词工程专项演练')).toBeInTheDocument();
    expect(screen.getByText('综合研判')).toBeInTheDocument();
  });

  it('TestDashboardPage_EmptyState：periods 空数组显示「暂无评估数据」与 /assessment 跳转入口（§4.1.5）', async () => {
    overviewMock.mockResolvedValue(emptyOverview());

    await renderAt('/dashboard');

    expect(await screen.findByText('暂无评估数据')).toBeInTheDocument();
    const goLink = screen.getByRole('link', { name: '前往评测运营中心' });
    expect(goLink).toHaveAttribute('href', '/assessment');
    // 整页空态：数据区块不渲染
    expect(screen.queryByText('活跃度概览')).not.toBeInTheDocument();
    expect(screen.queryByText('团队培训方向建议')).not.toBeInTheDocument();
  });

  it('TestDashboardPage_ErrorRetry：1305 整页错误占位 + 重试按钮，点击重试重新发起查询（§4.1.5）', async () => {
    let calls = 0;
    overviewMock.mockImplementation(() => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new ApiError(1305, 'staff list unavailable'))
        : Promise.resolve(makeOverview());
    });

    await renderAt('/dashboard');

    expect(await screen.findByText('看板数据加载失败，请重试')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    // 整页占位：不用部分数据降级渲染（§4.1.5）
    expect(screen.queryByText('活跃度概览')).not.toBeInTheDocument();
    expect(screen.queryByText('团队人格构成')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('活跃度概览')).toBeInTheDocument();
    expect(calls).toBeGreaterThanOrEqual(2);
  });

  it('TestDashboardPage_PeriodSwitch：选历史区间后以该区间双参重查，切换期间加载态防混渲染（§4.1.3/§4.1.4 规则1）', async () => {
    let release: (() => void) | undefined;
    const historyOverview = makeOverview({
      selected_period: { period_start: '2026-09-15', period_end: '2026-09-21', is_current: false },
      data_updated_at: '2026-09-21 10:00',
      activity: {
        active: { count: 45, ratio: 37.5 },
        low_freq: { count: 28, ratio: 23.3 },
        unused: { count: 47, ratio: 39.2 },
        mom: null,
      },
    });
    overviewMock.mockImplementation((period?: { period_start: string }) => {
      if (period?.period_start === '2026-09-15') {
        return new Promise<DashboardOverview>((res) => {
          release = () => res(historyOverview);
        });
      }
      return Promise.resolve(makeOverview());
    });

    await renderAt('/dashboard');
    expect(await screen.findByText('活跃度概览')).toBeInTheDocument();

    // 切到历史区间
    await openSelect(screen.getByRole('combobox', { name: '评估区间' }), '2026-09-15 ~ 2026-09-21');

    // 以该区间整对象再次调用（区间双参经 DashboardPeriodItem 透传）
    await waitFor(() =>
      expect(overviewMock).toHaveBeenCalledWith({
        period_start: '2026-09-15',
        period_end: '2026-09-21',
        is_current: false,
      }),
    );
    // 切换期间：数据区整块加载态，旧区间数据不残留（§4.1.4 规则1）
    expect(await screen.findByText('加载中…')).toBeInTheDocument();
    expect(screen.queryByText('活跃度概览')).not.toBeInTheDocument();
    // 工具条常驻：区间下拉仍显示所选历史区间
    expect(screen.getByRole('combobox', { name: '评估区间' })).toHaveTextContent('2026-09-15 ~ 2026-09-21');

    // 释放响应：历史区间数据到位（mom=null 环比行不渲染）
    release?.();
    expect(await screen.findByText('活跃度概览')).toBeInTheDocument();
    expect(screen.queryByTestId('activity-mom')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '评估区间' })).toHaveTextContent('2026-09-15 ~ 2026-09-21');
  });
});
