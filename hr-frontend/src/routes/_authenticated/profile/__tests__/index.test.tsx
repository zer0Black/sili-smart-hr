// 画像两页面路由测试（specs §3.1/§3.2、§4.1.1、§4.2.1/§4.2.3/§4.2.5、§5.2.4 规则1）：
// 列表页三态（渲染/1305 错误/空）+ 详情页状态机（正常组装/空态/1305 整页错误/2001 回落/
// 区间切换加载与禁用/管理 tab 切换）+ 跳转链（中文参数往返、返回列表兜底）。
// 中文断言直接消费真实 zh.json 文案（i18n/config 注册）。
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import i18n from '@/i18n/config';
import type {
  DimensionTreeNode,
  ProfileDetail,
  ProfileListItem,
  ProfileListPage,
} from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { routeTree } from '@/routeTree.gen';
import { useAuthStore } from '@/stores/auth';

const listMock = vi.hoisted(() => vi.fn());
const detailMock = vi.hoisted(() => vi.fn());
const treeMock = vi.hoisted(() => vi.fn());
const setupMock = vi.hoisted(() => vi.fn());
const toastErrorSpy = vi.hoisted(() => vi.fn());

vi.mock('@/features/profile/api', () => ({
  fetchProfiles: listMock,
  fetchProfileDetail: detailMock,
  exportProfiles: vi.fn(),
}));
vi.mock('@/features/dimension/api', () => ({
  fetchDimensionTree: treeMock,
  createDimension: vi.fn(),
  fetchDimensionDetail: vi.fn(),
  updateDimension: vi.fn(),
  deleteDimension: vi.fn(),
  fetchActivityRule: vi.fn(),
  saveActivityRule: vi.fn(),
}));
// __root beforeLoad 的 setup 探针
vi.mock('@/features/system/api', () => ({ fetchSetupStatus: setupMock }));
// sonner：toast.error 桩（Toaster 组件本身在 __root 渲染，保留原导出）
vi.mock('sonner', async (importOriginal) => {
  const actual = await importOriginal<typeof import('sonner')>();
  return { ...actual, toast: Object.assign(vi.fn(), { ...actual.toast, error: toastErrorSpy }) };
});

// 直接消费真实 zh.json 的 profile 命名空间（i18n/config 已同步注册）：文案与源码单一来源。
beforeAll(async () => {
  await i18n.changeLanguage('zh');
});

// Radix Select 在 jsdom 缺 pointer capture API（batch-table 测试同款桩）
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

function makeListItem(o: Partial<ProfileListItem> = {}): ProfileListItem {
  return {
    staff_name: '张三',
    activity_level: 'active',
    ai_usage_score: 82.4,
    ai_usage_degraded: false,
    ai_mgmt_score: null,
    ai_mgmt_degraded: false,
    enneagram_main_type: null,
    ...o,
  };
}

function twoRowList(): ProfileListPage {
  return {
    list: [
      makeListItem(),
      makeListItem({ staff_name: '李四', activity_level: 'unused', ai_usage_score: 71.2, ai_mgmt_score: 66 }),
    ],
    total: 2,
    page: 1,
    page_size: 10,
  };
}

function brief(code: string, name: string, o: Record<string, unknown> = {}) {
  return {
    id: code, code, name, module_code: 'AI_USAGE', group_code: null,
    data_source: 'CONVERSATION', weight: 1, include_overview: true, enabled: true, ...o,
  };
}

function makeTree(): DimensionTreeNode {
  return {
    modules: [
      {
        module_code: 'AI_USAGE', name: 'AI 使用能力', data_source: 'CONVERSATION', is_reference: false,
        groups: [
          { group_code: 'BASE', name: '基础', dimensions: [brief('AI_COMMUNICATION', '沟通表达')] },
          { group_code: 'UPPER', name: '高级', dimensions: [brief('AI_WRITING', '文档写作')] },
        ],
        dimensions: null,
      },
      {
        module_code: 'AI_MGMT', name: 'AI 管理能力', data_source: 'ACTIVE_TEST', is_reference: false,
        groups: null,
        dimensions: [brief('MGMT_PLAN', '任务规划', { module_code: 'AI_MGMT' })],
      },
    ],
  };
}

function makeDetail(o: Partial<ProfileDetail> = {}): ProfileDetail {
  return {
    staff_name: '张三',
    periods: [
      { period_start: '2026-09-22', period_end: '2026-09-28', is_current: true },
      { period_start: '2026-09-15', period_end: '2026-09-21', is_current: false },
    ],
    selected_period: { period_start: '2026-09-22', period_end: '2026-09-28' },
    activity_level: 'active',
    enneagram: null,
    modules: [
      { module: 'AI_USAGE', score: 82.4, change_vs_prev: 3, evaluated_at: '2026-09-27', data_status: 'complete', insufficient_count: 0, failed_count: 0, missing_count: 0 },
      { module: 'AI_MGMT', score: null, change_vs_prev: null, evaluated_at: null, data_status: 'pending', insufficient_count: 0, failed_count: 0, missing_count: 0 },
    ],
    dimensions: [
      {
        module: 'AI_USAGE', dimension_code: 'AI_COMMUNICATION', dimension_name: '沟通表达',
        group_code: 'BASE', score: 82, status: 'normal', rationale: '', evidences: [], trend: [], company_avg: 55.2,
      },
    ],
    ...o,
  };
}

/** 路由级渲染：真实 routeTree + 独立 QueryClient，navigate 目标路径（含参数与 search）。 */
async function renderAt(to: string, params?: Record<string, string>, search?: Record<string, string>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({ routeTree, context: { queryClient: qc }, defaultPreload: 'intent' });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await router.navigate({ to, ...(params ? { params } : {}), ...(search ? { search } : {}) });
  return router;
}

async function openSelect(trigger: HTMLElement, optionText: string) {
  const user = userEvent.setup();
  await user.click(trigger);
  await user.click(await screen.findByRole('option', { name: optionText }));
}

beforeEach(() => {
  useAuthStore.setState({ token: 'test-token' });
  // TanStack Router scroll-restoration 调 window.scrollTo；next-themes 读 matchMedia（jsdom 未实现）
  vi.stubGlobal('scrollTo', vi.fn());
  if (!window.matchMedia) {
    const mq = { matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn() };
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue(mq));
  }
  setupMock.mockResolvedValue({ initialized: true });
  treeMock.mockResolvedValue(makeTree());
  detailMock.mockReset();
  listMock.mockReset();
  toastErrorSpy.mockClear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('列表页 /profile（specs §4.1）', () => {
  it('TestListRowsRender：两行姓名与「待评估」缺失态渲染（§4.1.4 规则2）', async () => {
    listMock.mockResolvedValue(twoRowList());

    await renderAt('/profile');

    expect(await screen.findByText('张三')).toBeInTheDocument();
    expect(screen.getByText('李四')).toBeInTheDocument();
    // ai_mgmt_score null → 待评估（不渲染 0 分）
    expect(screen.getByText('待评估')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '姓名' })).toBeInTheDocument();
  });

  it('TestListError1305：上游不可用整表错误占位 + 重试按钮，导出按钮禁用（§4.1.5）', async () => {
    listMock.mockRejectedValue(new ApiError(1305, 'staff list unavailable'));

    await renderAt('/profile');

    expect(await screen.findByText('数据加载失败，请重试')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    // 列表错误态导出禁用（§4.1.3）
    expect(screen.getByRole('button', { name: '导出名单' })).toBeDisabled();
  });

  it('TestListEmpty：无筛选空列表显示「暂无人员数据」（§4.1.5）', async () => {
    listMock.mockResolvedValue({ list: [], total: 0, page: 1, page_size: 10 });

    await renderAt('/profile');

    expect(await screen.findByText('暂无人员数据')).toBeInTheDocument();
  });
});

describe('详情页 /profile/$staffName（specs §4.2）', () => {
  it('TestDetailAssembled：概览条 + 五区块组装，tab 计数现读维度树（§4.2.1/§4.2.3）', async () => {
    detailMock.mockResolvedValue(makeDetail());

    await renderAt('/profile/$staffName', { staffName: '张三' });

    // 首次查询不携区间（undefined = 最新区间）
    await waitFor(() => expect(detailMock).toHaveBeenCalledWith('张三', undefined));
    // 顶部条：姓名 + 活跃度标签 + 区间下拉
    expect(await screen.findByText('张三')).toBeInTheDocument();
    expect(screen.getAllByText('活跃').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByRole('combobox', { name: '评估区间' })).toHaveTextContent('2026-09-22 ~ 2026-09-28');
    // 概览条：九型主型 - （无判型）、当前区间
    expect(screen.getByText('九型主型')).toBeInTheDocument();
    expect(screen.getAllByText(/2026-09-22 ~ 2026-09-28（本期）/).length).toBeGreaterThanOrEqual(1);
    // 评分卡：AI 管理能力待评估（null 不渲染 0 分）
    expect(screen.getByText('核心结论')).toBeInTheDocument();
    // 核心结论插值本地化：等级枚举映射中文、pending 映射待评估，界面无英文枚举与模块码（T7 BR2）
    expect(screen.getByText('使用能力良好、管理能力待评估，最高分维度沟通表达')).toBeInTheDocument();
    expect(screen.getByText('AI 使用能力 较上期上升 3 分')).toBeInTheDocument();
    expect(screen.queryByText(/excellent|good|medium|poor/)).not.toBeInTheDocument();
    expect(screen.getByText('九型人格参考')).toBeInTheDocument();
    expect(screen.getByText('未参与九型人格测评')).toBeInTheDocument();
    // tab 计数动态取维度树：AI_USAGE 2 维 / AI_MGMT 1 维（§4.2.3 动态计数）
    expect(screen.getByRole('tab', { name: 'AI 使用能力 (2)' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AI 管理能力 (1)' })).toBeInTheDocument();
    // 默认 aiUsage tab：基础分组维度行
    expect(screen.getByText('沟通表达')).toBeInTheDocument();
  });

  it('TestDetailEmpty：periods 空数组显示空态提示与返回列表入口（§4.2.5）', async () => {
    detailMock.mockResolvedValue(makeDetail({ periods: [], selected_period: null, modules: [], dimensions: [] }));

    await renderAt('/profile/$staffName', { staffName: '王五' });

    expect(await screen.findByText('该人员暂无画像数据')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: '返回列表' }).length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('核心结论')).not.toBeInTheDocument();
  });

  it('TestDetailError1305：整页错误占位 + 重试按钮，不用画像数据降级渲染（§4.2.5）', async () => {
    detailMock.mockRejectedValue(new ApiError(1305, 'staff list unavailable'));

    await renderAt('/profile/$staffName', { staffName: '张三' });

    expect(await screen.findByText('画像数据加载失败，请重试')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    // 整页占位：无概览条与区块
    expect(screen.queryByText('核心结论')).not.toBeInTheDocument();
    expect(screen.queryByText('未参与九型人格测评')).not.toBeInTheDocument();
  });

  it('TestDetailErrorRetry：点击重试重新发起详情查询（§4.2.5）', async () => {
    let calls = 0;
    detailMock.mockImplementation(() => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new ApiError(1305, 'staff list unavailable'))
        : Promise.resolve(makeDetail());
    });

    await renderAt('/profile/$staffName', { staffName: '张三' });
    fireEvent.click(await screen.findByRole('button', { name: '重试' }));

    expect(await screen.findByText('核心结论')).toBeInTheDocument();
    expect(calls).toBeGreaterThanOrEqual(2);
  });

  it('TestDetailPeriodInvalid2001：toast 提示并回落最新区间重查，不走整页占位（§5.2.4 规则1）', async () => {
    const detail = makeDetail();
    detailMock.mockImplementation((_name: string, period?: { period_start: string }) =>
      period?.period_start === '2026-09-15'
        ? Promise.reject(new ApiError(2001, 'period invalid'))
        : Promise.resolve(detail),
    );

    await renderAt('/profile/$staffName', { staffName: '张三' });
    await screen.findByText('核心结论');

    // 切到历史区间 → 2001
    await openSelect(screen.getByRole('combobox', { name: '评估区间' }), '2026-09-15 ~ 2026-09-21');

    await waitFor(() => expect(toastErrorSpy).toHaveBeenCalledWith('所选区间无效，已回落最新区间'));
    // 回落：清 selected period 后以 undefined 重查最新区间
    await waitFor(() => {
      const fallbackCalls = detailMock.mock.calls.filter((c) => c[1] === undefined);
      expect(fallbackCalls.length).toBeGreaterThanOrEqual(2);
    });
    // 不走整页占位：回落数据到位后正常渲染
    expect(await screen.findByText('核心结论')).toBeInTheDocument();
    expect(screen.queryByText('画像数据加载失败，请重试')).not.toBeInTheDocument();
  });

  it('TestDetailPeriodSwitchLoading：区间切换期间整页加载态 + PeriodSelect 禁用（§4.2.5 防混渲染）', async () => {
    let release: (() => void) | undefined;
    const detail2 = makeDetail({
      selected_period: { period_start: '2026-09-15', period_end: '2026-09-21' },
    });
    detailMock.mockImplementation((_name: string, period?: { period_start: string }) => {
      if (period?.period_start === '2026-09-15') {
        return new Promise<ProfileDetail>((res) => {
          release = () => res(detail2);
        });
      }
      return Promise.resolve(makeDetail());
    });

    await renderAt('/profile/$staffName', { staffName: '张三' });
    await screen.findByText('核心结论');

    await openSelect(screen.getByRole('combobox', { name: '评估区间' }), '2026-09-15 ~ 2026-09-21');

    // 切换期间：整页加载态、旧区间数据不残留、下拉禁用防抖动
    expect(await screen.findByText('加载中…')).toBeInTheDocument();
    expect(screen.queryByText('核心结论')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '评估区间' })).toBeDisabled();

    // 释放响应：新区间数据到位
    release?.();
    expect(await screen.findByText('核心结论')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '评估区间' })).toHaveTextContent('2026-09-15 ~ 2026-09-21');
    expect(screen.getByRole('combobox', { name: '评估区间' })).toBeEnabled();
  });

  it('TestDetailTabSwitchMgmt：切管理 tab 显示管理子能力分组与维度行（§4.2.3）', async () => {
    detailMock.mockResolvedValue(makeDetail({
      dimensions: [
        ...makeDetail().dimensions,
        {
          module: 'AI_MGMT', dimension_code: 'MGMT_PLAN', dimension_name: '任务规划',
          group_code: null, score: 58, status: 'normal', rationale: '', evidences: [], trend: [], company_avg: null,
        },
      ],
    }));

    await renderAt('/profile/$staffName', { staffName: '张三' });
    // 初始 aiUsage tab：使用分组在、管理分组不在
    expect(await screen.findByText('基础对话能力')).toBeInTheDocument();
    expect(screen.queryByText('管理子能力')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: 'AI 管理能力 (1)' }));
    expect(await screen.findByText('管理子能力')).toBeInTheDocument();
    expect(screen.getByText('任务规划')).toBeInTheDocument();
    expect(screen.queryByText('基础对话能力')).not.toBeInTheDocument();
  });

  it('TestDetailEnneagramNoWing：翼型空串显示「无显著翼型」（03 B1 翼型无显著为空串）', async () => {
    detailMock.mockResolvedValue(makeDetail({
      enneagram: {
        main_type: '5', wing_type: '',
        distribution: { '1': 5, '2': 5, '3': 5, '4': 10, '5': 40, '6': 10, '7': 5, '8': 10, '9': 10 },
        rationale: '判型依据示例',
      },
    }));

    await renderAt('/profile/$staffName', { staffName: '张三' });

    expect(await screen.findByText('九型人格参考')).toBeInTheDocument();
    // 主型 5 型名在概览条与九型区各出现一次
    expect(screen.getAllByText('智慧型').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('无显著翼型')).toBeInTheDocument();
  });

  it('TestDetailBackFallback：无历史时返回列表跳列表页兜底（§3.2）', async () => {
    detailMock.mockResolvedValue(makeDetail());
    listMock.mockResolvedValue(twoRowList());

    await renderAt('/profile/$staffName', { staffName: '张三' });
    await screen.findByText('核心结论');

    // 钉死 history.length=1（router.navigate 的 pushState 会使其虚高），强制走无历史 fallback 分支
    Object.defineProperty(window.history, 'length', { get: () => 1, configurable: true });
    fireEvent.click(screen.getByRole('button', { name: '返回列表' }));

    expect(await screen.findByText('人员画像名单')).toBeInTheDocument();
    expect(await screen.findByText('张三')).toBeInTheDocument();
  });
});

describe('列表 → 详情跳转链（specs §3.2）', () => {
  it('TestRowClickNavigatesToDetail：点击行携中文人员标识跳详情，URL decode 由 router 往返（§3.2）', async () => {
    listMock.mockResolvedValue(twoRowList());
    detailMock.mockResolvedValue(makeDetail({ staff_name: '李四' }));

    await renderAt('/profile');
    fireEvent.click(await screen.findByText('李四'));

    await waitFor(() => expect(detailMock).toHaveBeenCalledWith('李四', undefined));
    expect(await screen.findByText('核心结论')).toBeInTheDocument();
  });
});

// URL 参数预填（specs §4.1.3 共性短板/未使用人群跳转、§7.2 F9 预填依赖）：
// 看板短板标签携 dimension_code、未使用卡携 unused_only=true 深链进入，列表筛选预填
// 且消费后清参（一次性深链语义，question-bank 先例），页内交互行为保持现状。
describe('列表页 URL 参数预填（specs §4.1.3 / §7.2）', () => {
  it('TestProfilePage_PrefillsDimensionCode：携 dimension_code 进入首查请求含该维度（03 §4.5 短板标签跳转契约）', async () => {
    listMock.mockResolvedValue(twoRowList());

    await renderAt('/profile', undefined, { dimension_code: 'AI_WRITING' });

    await waitFor(() =>
      expect(listMock).toHaveBeenCalledWith(expect.objectContaining({ dimension_code: 'AI_WRITING', page: 1 })),
    );
    expect(await screen.findByText('张三')).toBeInTheDocument();
  });

  it('TestProfilePage_PrefillsUnusedOnly：携 unused_only=true 进入开关开启且请求含 unused_only（03 §4.5 未使用卡跳转契约）', async () => {
    listMock.mockResolvedValue(twoRowList());

    await renderAt('/profile', undefined, { unused_only: 'true' });

    await waitFor(() =>
      expect(listMock).toHaveBeenCalledWith(expect.objectContaining({ unused_only: true, page: 1 })),
    );
    // 开关为开启态：Radix Switch 勾选时 data-state="checked"
    const sw = await screen.findByRole('switch', { name: '仅看未使用' });
    await waitFor(() => expect(sw).toHaveAttribute('data-state', 'checked'));
  });

  it('TestProfilePage_ConsumesThenClears：预填消费后 URL search 清空（replace）且筛选保持预填值（§7.2 一次性深链）', async () => {
    listMock.mockResolvedValue(twoRowList());

    const router = await renderAt('/profile', undefined, { dimension_code: 'AI_WRITING' });
    await screen.findByText('张三');

    // 清参后再渲染：筛选仍保持预填值（不随清参重置）
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    expect(await screen.findByText('张三')).toBeInTheDocument();
    const latest = listMock.mock.calls[listMock.mock.calls.length - 1][0] as { dimension_code?: string };
    expect(latest.dimension_code).toBe('AI_WRITING');
  });

  it('TestProfilePage_ManualFilterUnchanged：无参数进入首查不含筛选参数，行为与现状一致（回归锚点）', async () => {
    listMock.mockResolvedValue(twoRowList());

    await renderAt('/profile');

    await screen.findByText('张三');
    const first = listMock.mock.calls[0][0] as Record<string, unknown>;
    expect(first.name).toBeUndefined();
    expect(first.activity_level).toBeUndefined();
    expect(first.dimension_code).toBeUndefined();
    expect(first.unused_only).toBeUndefined();
    expect(first).toMatchObject({ page: 1, page_size: 10 });
  });
});
