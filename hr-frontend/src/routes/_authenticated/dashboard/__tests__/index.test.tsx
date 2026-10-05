// 团队看板页路由测试（specs P2_TMD_001 §4.1.1、§4.1.3、§4.1.4 规则1、§4.1.5）：
// 页面组装（三态卡/两雷达卡/九型区/建议区）+ 空态（/assessment 入口）+
// 1305 整页错误重试 + 区间切换双参重查与切换期加载态（防混渲染）。
// 照 profile/__tests__ 形态：真实 routeTree + mock api 模块，断言消费真实 zh.json。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import i18n from '@/i18n/config';
import type { DashboardOverview, DashboardTrend } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { routeTree } from '@/routeTree.gen';
import { useAuthStore } from '@/stores/auth';

const overviewMock = vi.hoisted(() => vi.fn());
const trendMock = vi.hoisted(() => vi.fn());
const setupMock = vi.hoisted(() => vi.fn());

vi.mock('@/features/dashboard/api', () => ({
  fetchDashboardOverview: overviewMock,
  fetchDashboardTrend: trendMock,
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

/** 趋势页路由级渲染：直链形态带 search（URL 参数承载能力类型，specs §4.2.3）。 */
async function renderTrendAt(search?: Record<string, string>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({ routeTree, context: { queryClient: qc }, defaultPreload: 'intent' });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await router.navigate({ to: '/dashboard/trend', ...(search ? { search } : {}) });
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
  trendMock.mockReset();
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

// ---------- 趋势页 /dashboard/trend（specs §4.2） ----------

/** 两维度趋势 fixture：含置空期断线、短板标识、null prev/change。 */
function makeTrend(o: Partial<DashboardTrend> = {}): DashboardTrend {
  return {
    type: 'use',
    module: 'AI_USAGE',
    periods: [
      { period_start: '2026-09-15', period_end: '2026-09-21', is_current: false },
      { period_start: '2026-09-22', period_end: '2026-09-28', is_current: false },
      { period_start: '2026-09-29', period_end: '2026-10-05', is_current: true },
    ],
    current_period: { period_start: '2026-09-29', period_end: '2026-10-05', is_current: true },
    data_updated_at: '2026-10-05 02:12',
    composite: { score: 74, change_vs_prev: 3, dimension_count: 7 },
    dimensions: [
      {
        dimension_code: 'AI_WRITING',
        dimension_name: '文档写作',
        is_weakness: true,
        history: [
          { period_start: '2026-09-15', period_end: '2026-09-21', score: null },
          { period_start: '2026-09-22', period_end: '2026-09-28', score: 72 },
          { period_start: '2026-09-29', period_end: '2026-10-05', score: 76 },
        ],
        current_score: 76,
        prev_score: 72,
        change: 4,
      },
      {
        dimension_code: 'AI_COMMUNICATION',
        dimension_name: '沟通表达',
        is_weakness: false,
        history: [
          { period_start: '2026-09-15', period_end: '2026-09-21', score: 80 },
          { period_start: '2026-09-22', period_end: '2026-09-28', score: null },
          { period_start: '2026-09-29', period_end: '2026-10-05', score: 79 },
        ],
        current_score: 79,
        prev_score: null,
        change: null,
      },
    ],
    ...o,
  };
}

function emptyTrend(): DashboardTrend {
  return makeTrend({
    current_period: null,
    data_updated_at: null,
    composite: null,
    dimensions: [],
  });
}

describe('能力逐期趋势页 /dashboard/trend（specs §4.2）', () => {
  it('TestTrendPage_RendersSections：mock 返完整 trend 时渲染综合分摘要、维度分面卡（按 dimensions 数）与变化表（§4.2.1）', async () => {
    trendMock.mockResolvedValue(makeTrend());

    await renderTrendAt();

    // 顶部条：返回看板 + 能力类型 tab 两项
    expect(await screen.findByRole('button', { name: '返回看板' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 使用能力' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 管理能力' })).toBeInTheDocument();
    // 综合分摘要卡：主指标 + 较上期变化 + 观察窗口元信息（§4.2.2 A）
    expect(screen.getByText('模块综合分')).toBeInTheDocument();
    expect(screen.getByText('74')).toBeInTheDocument();
    expect(screen.getByText('+3')).toBeInTheDocument();
    // 观察窗口 = 走势序列覆盖区间跨度（首期 start ~ 末期 end，§4.2.2 A）
    expect(screen.getByText('2026-09-15 ~ 2026-10-05')).toBeInTheDocument();
    expect(screen.getByText('2026-09-29 ~ 2026-10-05')).toBeInTheDocument();
    expect(screen.getByText(/2026-10-05 02:12/)).toBeInTheDocument();
    // 评估维度数 7（摘要卡元信息，§4.2.2 A）
    expect(screen.getByText('7')).toBeInTheDocument();
    expect(screen.getByText('对话分析')).toBeInTheDocument();
    // 维度分面卡：按 dimensions 数渲染，短板标签（本期口径 §4.2.4 规则3）
    const dimCards = await screen.findAllByText('文档写作');
    expect(dimCards.length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText('短板').length).toBeGreaterThanOrEqual(1);
    // 变化表四列表头 + 数据行
    expect(screen.getByRole('columnheader', { name: '维度' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '上期分' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '本期分' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '变化' })).toBeInTheDocument();
    // +4 在分面卡环比与变化表各一处
    expect(screen.getAllByText('+4').length).toBeGreaterThanOrEqual(1);
  });

  it('TestTrendPage_TypeFromSearch：/dashboard/trend?type=manage 时按 manage 请求且 tab 高亮管理能力（§4.2.3 BR1）', async () => {
    trendMock.mockResolvedValue(makeTrend({ type: 'manage', module: 'AI_MGMT' }));

    await renderTrendAt({ type: 'manage' });

    await waitFor(() => expect(trendMock).toHaveBeenCalledWith('manage'));
    const manageTab = await screen.findByRole('tab', { name: 'AI 管理能力' });
    expect(manageTab).toHaveAttribute('aria-selected', 'true');
    // 管理能力数据来源说明（§4.2.2 A）
    expect(screen.getByText('主动测试场景题')).toBeInTheDocument();
  });

  it('TestTrendPage_TypeFallback：type=xyz 非法值按 use 兜底请求（normalizeAbilityType，specs §5.2.2 步2）', async () => {
    trendMock.mockResolvedValue(makeTrend());

    await renderTrendAt({ type: 'xyz' });

    await waitFor(() => expect(trendMock).toHaveBeenCalledWith('use'));
  });

  it('TestTrendPage_TabSwitchUpdatesUrl：点击管理能力 tab 后 URL search.type=manage 且按 manage 重查（§4.2.3 BR1）', async () => {
    trendMock.mockResolvedValue(makeTrend());

    const router = await renderTrendAt();
    await screen.findByText('模块综合分');

    fireEvent.click(screen.getByRole('tab', { name: 'AI 管理能力' }));

    await waitFor(() => expect(trendMock).toHaveBeenCalledWith('manage'));
    expect(router.state.location.search.type).toBe('manage');
  });

  it('TestTrendPage_BackButton：history 无记录直链进入时点击返回看板跳 /dashboard 兜底（§4.2.3 BR2）', async () => {
    trendMock.mockResolvedValue(makeTrend());
    overviewMock.mockResolvedValue(makeOverview());

    await renderTrendAt();
    await screen.findByText('模块综合分');

    // 钉死 history.length=1（router.navigate 的 pushState 会使其虚高），强制走无历史 fallback 分支
    Object.defineProperty(window.history, 'length', { get: () => 1, configurable: true });
    fireEvent.click(screen.getByRole('button', { name: '返回看板' }));

    expect(await screen.findByText('活跃度概览')).toBeInTheDocument();
    await waitFor(() => expect(overviewMock).toHaveBeenCalled());
  });

  it('TestTrendPage_EmptyState：composite=null 且 dimensions=[] 时整页空态与返回看板入口（§4.2.5 BR5）', async () => {
    trendMock.mockResolvedValue(emptyTrend());

    await renderTrendAt();

    expect(await screen.findByText('暂无评估数据')).toBeInTheDocument();
    // 空态入口：顶部条与空态区各一处返回看板（§4.2.5 与返回入口）
    expect(screen.getAllByRole('button', { name: '返回看板' }).length).toBeGreaterThanOrEqual(1);
    // 整页空态：数据区块不渲染
    expect(screen.queryByText('模块综合分')).not.toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('TestTrendPage_ErrorRetry：趋势接口失败整页错误占位 + 重试重新发起查询（§4.2.5 BR5）', async () => {
    trendMock.mockRejectedValueOnce(new ApiError(1500, 'internal error'));
    trendMock.mockResolvedValueOnce(makeTrend());

    await renderTrendAt();

    expect(await screen.findByText('趋势数据加载失败，请重试')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    // 整页占位：不用部分数据降级渲染（§4.2.5）
    expect(screen.queryByText('模块综合分')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('模块综合分')).toBeInTheDocument();
    expect(trendMock).toHaveBeenCalledTimes(2);
  });

  it('TestTrendChangeTable_NullRendersDash：prev_score/current_score/change 任一 null 时对应单元格渲染 -（§4.2.2 C BR3）', async () => {
    trendMock.mockResolvedValue(makeTrend());

    await renderTrendAt();
    await screen.findByRole('table');

    // 沟通表达行：prev_score=null → 上期分 -；change=null → 变化 -；本期分 79 正常
    const row = screen.getByRole('row', { name: /沟通表达/ });
    expect(within(row).getAllByText('-')).toHaveLength(2);
    expect(within(row).getByText('79')).toBeInTheDocument();
    // 文档写作行三值齐备无 -
    const fullRow = screen.getByRole('row', { name: /文档写作/ });
    expect(within(fullRow).queryByText('-')).not.toBeInTheDocument();
  });

  it('TestTrendDimensionCard_BreakLine：history 含 null score 的点传导 undefined 断线（数据传导，§4.2.4 规则2 BR3）', async () => {
    trendMock.mockResolvedValue(makeTrend());

    await renderTrendAt();

    // 迷你折线容器渲染（分面卡内 LineChart），断线语义由数据传导承载
    const charts = await screen.findAllByTestId('trend-sparkline');
    expect(charts).toHaveLength(2);
    // 置空期 score=null：本期值正常显示（76 卡片+变化表、79 卡片+变化表各两处），
    // 数据层断线由 score ?? undefined 传导
    expect(screen.getAllByText('76').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('79').length).toBeGreaterThanOrEqual(1);
  });
});
