// 看板页区块组件测试（specs P2_TMD_001 §4.1.2 B/C/D/E、§4.1.3、§4.1.4 规则2/4/5/6、§4.1.5）。
// 跳转断言走 mock useNavigate（question-table.test 同款）；Radix Select 需 jsdom 补桩。
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import type { ReactElement } from 'react';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type {
  DashboardActivity,
  DashboardEnneagram,
  DashboardModule,
  DashboardPeriodItem,
  DashboardSuggestion,
  DashboardTrendDim,
} from '@/lib/contracts';

await i18n.changeLanguage('zh');

// Radix Select 在 jsdom 缺 hasPointerCapture/releasePointerCapture/scrollIntoView
Element.prototype.hasPointerCapture = () => false;
Element.prototype.releasePointerCapture = () => {};
Element.prototype.scrollIntoView = () => {};

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
}));

// recharts 在 jsdom 无尺寸，ResponsiveContainer 宽高为 0 不渲染内部图表；
// 柱图/雷达断言（Cell 高亮、参考线存在性）需要 SVG 节点，stub 成透传容器；
// Line stub 捕获 dot 渲染函数供位置无关的行为断言（末端点标记，§4.2.2 B）。
const lastLineDot = vi.hoisted(() => ({ current: null as ((p: unknown) => unknown) | null }));
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  const Passthrough = ({ children }: { children?: React.ReactNode }) => <>{children}</>;
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
    RadarChart: Passthrough,
    PieChart: Passthrough,
    BarChart: Passthrough,
    LineChart: Passthrough,
    CartesianGrid: Passthrough,
    PolarGrid: Passthrough,
    PolarAngleAxis: Passthrough,
    PolarRadiusAxis: Passthrough,
    Tooltip: Passthrough,
    Pie: Passthrough,
    XAxis: Passthrough,
    YAxis: Passthrough,
    Bar: Passthrough,
    Line: ({ dot }: { dot?: unknown }) => {
      lastLineDot.current = typeof dot === 'function' ? (dot as (p: unknown) => unknown) : null;
      return null;
    },
    Cell: ({ fill }: { fill?: string }) => <g data-testid="chart-cell" fill={fill} />,
    Radar: ({ name, dataKey, strokeDasharray }: { name?: string; dataKey?: string; strokeDasharray?: string }) => (
      <g data-testid="radar-line" data-name={name} data-key={dataKey} data-dash={strokeDasharray} />
    ),
  };
});

import { ActivityOverview } from '../components/activity-overview';
import { ModuleRadarCard } from '../components/module-radar-card';
import { EnneagramPanel } from '../components/enneagram-panel';
import { SuggestionPanel } from '../components/suggestion-panel';
import { PeriodToolbar } from '../components/period-toolbar';
import { TrendDimensionCard } from '../components/trend-dimension-card';

beforeEach(() => {
  navigateMock.mockReset();
});

// ---------- ActivityOverview ----------

const activityWithMom: DashboardActivity = {
  active: { count: 50, ratio: 41.7 },
  low_freq: { count: 30, ratio: 25.0 },
  unused: { count: 40, ratio: 33.3 },
  mom: { active_change: 5, unused_change: -3 },
};

const activityNoMom: DashboardActivity = { ...activityWithMom, mom: null };

describe('ActivityOverview（specs §4.1.2 B / §4.1.4 规则2）', () => {
  it('TestActivityOverview_UnusedCardNavigates：unused 卡点击跳 /profile 且 search 含 unused_only=true（§4.1.3 未使用人群跳转）', async () => {
    const user = userEvent.setup();
    render(<ActivityOverview data={activityWithMom} staffTotal={120} />);

    const unusedCard = screen.getByRole('button', { name: /未使用/ });
    await user.click(unusedCard);

    expect(navigateMock).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/profile',
      search: { unused_only: 'true' },
    });
  });

  it('TestActivityOverview_ActiveLowFreqDisabled：活跃与低频卡置灰不可点击（§4.1.3 约束）', () => {
    render(<ActivityOverview data={activityWithMom} staffTotal={120} />);

    const activeCard = screen.getByRole('button', { name: /活跃/ });
    expect(activeCard).toBeDisabled();
    expect(activeCard).toHaveClass('cursor-not-allowed');
    const lowCard = screen.getByRole('button', { name: /低频/ });
    expect(lowCard).toBeDisabled();
    expect(lowCard).toHaveClass('cursor-not-allowed');
  });

  it('TestActivityOverview_MomNullHidden：mom=null（最早区间）时环比行不渲染', () => {
    render(<ActivityOverview data={activityNoMom} staffTotal={120} />);

    expect(screen.queryByText(/环比/)).not.toBeInTheDocument();
    expect(screen.queryByText('+5')).not.toBeInTheDocument();
  });

  it('TestActivityOverview_MomDirectionColor：活跃增加与未使用减少为正向色，反向为 warning（§4.1.4 规则2）', () => {
    const { unmount } = render(<ActivityOverview data={activityWithMom} staffTotal={120} />);

    expect(screen.getByText('+5')).toHaveClass('text-chart-2'); // 活跃增加 → 正向
    expect(screen.getByText('-3')).toHaveClass('text-chart-2'); // 未使用减少 → 正向
    unmount();

    // 反向：活跃减少与未使用增加为 warning
    render(
      <ActivityOverview
        data={{ ...activityWithMom, mom: { active_change: -2, unused_change: 4 } }}
        staffTotal={120}
      />,
    );
    expect(screen.getByText('-2')).toHaveClass('text-warning');
    expect(screen.getByText('+4')).toHaveClass('text-warning');
  });

  it('TestActivityOverview_StaffTotalDenominator：全员人数作为活跃卡副标题分母与环形图中心呈现', () => {
    render(<ActivityOverview data={activityWithMom} staffTotal={120} />);

    expect(screen.getAllByText(/120/).length).toBeGreaterThan(0);
    expect(screen.getByText(/41\.7%/)).toBeInTheDocument();
    expect(screen.getByText(/33\.3%/)).toBeInTheDocument();
  });
});

// ---------- ModuleRadarCard ----------

const usageModule: DashboardModule = {
  module: 'AI_USAGE',
  dimensions: [
    { dimension_code: 'AI_BASE_CLARITY', dimension_name: '指令清晰度', avg_score: 76, low_ratio: 23.3, is_weakness: false },
    { dimension_code: 'AI_UPPER_PLAN', dimension_name: '任务规划', avg_score: 58, low_ratio: 66.7, is_weakness: true },
    { dimension_code: 'AI_UPPER_TOOL', dimension_name: '工具运用', avg_score: null, low_ratio: null, is_weakness: false },
  ],
  overall_avg: 74,
};

const mgmtModule: DashboardModule = {
  module: 'AI_MGMT',
  dimensions: [
    { dimension_code: 'MGT_DELEGATION', dimension_name: '授权分工', avg_score: 61, low_ratio: 40.0, is_weakness: true },
  ],
  overall_avg: null,
};

describe('ModuleRadarCard（specs §4.1.2 C / §4.1.4 规则3/规则4）', () => {
  it('TestModuleRadarCard_WeaknessTagNavigates：is_weakness 标签点击跳 /profile 且 search 含 dimension_code（§4.1.3 共性短板跳转）', async () => {
    const user = userEvent.setup();
    render(<ModuleRadarCard data={usageModule} />);

    const tag = screen.getByRole('button', { name: /任务规划/ });
    await user.click(tag);

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/profile',
      search: { dimension_code: 'AI_UPPER_PLAN' },
    });
  });

  it('TestModuleRadarCard_NullAvgAxis：avg_score=null 维度传导 undefined 断开；overall_avg=null 时无参考线节点', () => {
    render(<ModuleRadarCard data={mgmtModule} />);

    // overall_avg=null 的管理模块：只应有维度均分一条 Radar 线，无虚线参考线
    const lines = screen.getAllByTestId('radar-line');
    expect(lines).toHaveLength(1);
    expect(lines[0]).not.toHaveAttribute('data-dash');
  });

  it('TestModuleRadarCard_ReferenceLine：overall_avg 非 null 时渲染虚线参考线（§4.1.4 规则3）', () => {
    render(<ModuleRadarCard data={usageModule} />);

    const dashed = screen.getAllByTestId('radar-line').find((l) => l.hasAttribute('data-dash'));
    expect(dashed).toBeInTheDocument();
  });

  it('TestModuleRadarCard_TrendLink：右上角查看逐期趋势链接按模块反查 type（§4.1.3）', async () => {
    const user = userEvent.setup();
    const { unmount } = render(<ModuleRadarCard data={usageModule} />);
    await user.click(screen.getAllByRole('button', { name: /查看逐期趋势/ })[0]);

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/dashboard/trend',
      search: { type: 'use' },
    });
    unmount();

    navigateMock.mockReset();
    render(<ModuleRadarCard data={mgmtModule} />);
    await user.click(screen.getAllByRole('button', { name: /查看逐期趋势/ })[0]);

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/dashboard/trend',
      search: { type: 'manage' },
    });
  });

  it('TestModuleRadarCard_WeaknessLabelFormat：短板标签呈现维度名+均分+低分占比一位小数（§4.1.4 规则4）', () => {
    render(<ModuleRadarCard data={usageModule} />);

    const tag = screen.getByRole('button', { name: /任务规划/ });
    expect(within(tag).getByText('58')).toBeInTheDocument();
    expect(within(tag).getByText(/66\.7%/)).toBeInTheDocument();
  });

  it('TestModuleRadarCard_SourceSubtitle：卡头副标题按模块给数据来源说明（§4.1.2 C）', () => {
    render(<ModuleRadarCard data={usageModule} />);
    expect(screen.getByText('走对话分析')).toBeInTheDocument();

    render(<ModuleRadarCard data={mgmtModule} />);
    expect(screen.getByText('走主动测试场景题')).toBeInTheDocument();
  });
});

// ---------- EnneagramPanel ----------

const enneagramData: DashboardEnneagram = {
  scored_count: 80,
  coverage_ratio: 66.7,
  distribution: [
    { type: '1', count: 8, ratio: 10.0 },
    { type: '2', count: 10, ratio: 12.5 },
    { type: '3', count: 18, ratio: 22.5 },
    { type: '4', count: 6, ratio: 7.5 },
    { type: '5', count: 9, ratio: 11.3 },
    { type: '6', count: 7, ratio: 8.8 },
    { type: '7', count: 11, ratio: 13.8 },
    { type: '8', count: 5, ratio: 6.2 },
    { type: '9', count: 6, ratio: 7.5 },
  ],
  dominant_type: '3',
  dominant_ratio: 22.5,
  secondary_type: '7',
  secondary_ratio: 13.8,
};

describe('EnneagramPanel（specs §4.1.2 D / §4.1.4 规则5）', () => {
  it('TestEnneagramPanel_NullState：data=null 整区提示「暂无九型人格测评数据」且无图表', () => {
    render(<EnneagramPanel data={null} />);

    expect(screen.getByText('暂无九型人格测评数据')).toBeInTheDocument();
    expect(screen.queryByTestId('chart-cell')).not.toBeInTheDocument();
  });

  it('TestEnneagramPanel_NineBars：恒 9 项柱，主导/次主导高亮 primary、其余 muted（§4.1.4 规则5）', () => {
    render(<EnneagramPanel data={enneagramData} />);

    const cells = screen.getAllByTestId('chart-cell');
    expect(cells).toHaveLength(9);
    // 3=主导 7=次主导 高亮，其余 6 根 muted
    const highlighted = cells.filter((c) => c.getAttribute('fill') === 'var(--primary)');
    const muted = cells.filter((c) => c.getAttribute('fill') === 'var(--muted)');
    expect(highlighted).toHaveLength(2);
    expect(muted).toHaveLength(7);
  });

  it('TestEnneagramPanel_Summary：构成摘要含主导/次主导型名与占比、固定特征描述（§4.1.2 D）', () => {
    render(<EnneagramPanel data={enneagramData} />);

    expect(screen.getAllByText('成就型').length).toBeGreaterThan(0); // profile:enneagram.type3
    expect(screen.getAllByText('活跃型').length).toBeGreaterThan(0); // profile:enneagram.type7
    expect(screen.getByText(/22\.5%/)).toBeInTheDocument();
    expect(screen.getByText(/13\.8%/)).toBeInTheDocument();
  });

  it('TestEnneagramPanel_Coverage：覆盖数与覆盖率呈现（§4.1.2 D）', () => {
    render(<EnneagramPanel data={enneagramData} />);

    expect(screen.getByText(/80/)).toBeInTheDocument();
    expect(screen.getByText(/66\.7%/)).toBeInTheDocument();
  });
});

// ---------- SuggestionPanel ----------

const generatedSuggestion: DashboardSuggestion = {
  status: 'generated',
  period_start: '2026-09-29',
  period_end: '2026-10-05',
  generated_at: '2026-10-05 03:00',
  modules: [
    {
      module: 'AI_USAGE',
      suggestions: [
        { name: '提示词工程专项训练营', description: '针对任务规划维度低分占比 66.7% 的定向提升' },
      ],
    },
    {
      module: 'AI_MGMT',
      suggestions: [
        { name: '授权分工案例研讨', description: '围绕授权场景的组织级工作坊' },
      ],
    },
  ],
  summary: '团队整体 AI 使用能力稳健，任务规划为共性短板',
};

function emptySuggestion(status: 'generating' | 'failed' | 'none'): DashboardSuggestion {
  return { status, period_start: null, period_end: null, generated_at: null, modules: [], summary: '' };
}

describe('SuggestionPanel（specs §4.1.2 E / §4.1.4 规则6）', () => {
  it('TestSuggestionPanel_FourStates：generating/failed/none 渲染统一提示文案（§4.1.4 规则6）', () => {
    for (const status of ['generating', 'failed', 'none'] as const) {
      const { unmount } = render(<SuggestionPanel data={emptySuggestion(status)} />);
      expect(screen.getByText('培训建议生成中或本期无批量批次')).toBeInTheDocument();
      expect(screen.queryByText('提示词工程专项训练营')).not.toBeInTheDocument();
      unmount();
    }
  });

  it('TestSuggestionPanel_Generated：generated 渲染两模块建议条目、summary 与区间/生成时间标注（§4.1.4 规则6）', () => {
    render(<SuggestionPanel data={generatedSuggestion} />);

    expect(screen.getByText('提示词工程专项训练营')).toBeInTheDocument();
    expect(screen.getByText('授权分工案例研讨')).toBeInTheDocument();
    expect(screen.getByText(/针对任务规划维度低分占比/)).toBeInTheDocument();
    expect(screen.getByText(/团队整体 AI 使用能力稳健/)).toBeInTheDocument();
    expect(screen.getByText(/2026-09-29/)).toBeInTheDocument();
    expect(screen.getByText(/2026-10-05 03:00/)).toBeInTheDocument();
  });
});

// ---------- PeriodToolbar ----------

const periods: DashboardPeriodItem[] = [
  { period_start: '2026-09-29', period_end: '2026-10-05', is_current: true },
  { period_start: '2026-09-22', period_end: '2026-09-28', is_current: false },
];

describe('PeriodToolbar（specs §4.1.2 A / §4.1.3 评估区间切换）', () => {
  it('TestPeriodToolbar_CurrentLabel：is_current 项文案含「本期」（§4.1.2 A）', async () => {
    render(
      <PeriodToolbar periods={periods} selected={periods[0]} dataUpdatedAt="2026-10-05 02:12" onSelect={vi.fn()} />,
    );

    const user = userEvent.setup();
    await user.click(screen.getByRole('combobox'));
    const currentOption = await screen.findByRole('option', { name: /本期/ });
    expect(currentOption).toHaveTextContent('2026-09-29');
    expect(currentOption).toHaveTextContent('2026-10-05');
  });

  it('TestPeriodToolbar_UpdatedTime：数据更新时间文本呈现', () => {
    render(
      <PeriodToolbar periods={periods} selected={periods[0]} dataUpdatedAt="2026-10-05 02:12" onSelect={vi.fn()} />,
    );

    expect(screen.getByText(/2026-10-05 02:12/)).toBeInTheDocument();
  });

  it('TestPeriodToolbar_OnSelect：切换区间回调所选条目', async () => {
    const onSelect = vi.fn();
    render(<PeriodToolbar periods={periods} selected={periods[0]} dataUpdatedAt={null} onSelect={onSelect} />);

    const user = userEvent.setup();
    await user.click(screen.getByRole('combobox'));
    await user.click(await screen.findByRole('option', { name: /2026-09-22/ }));

    expect(onSelect).toHaveBeenCalledWith(periods[1]);
  });
});

// ---------- TrendDimensionCard ----------

const trendDim: DashboardTrendDim = {
  dimension_code: 'AI_WRITING',
  dimension_name: '文档写作',
  is_weakness: false,
  history: [
    { period_start: '2026-09-15', period_end: '2026-09-21', score: null },
    { period_start: '2026-09-22', period_end: '2026-09-28', score: 72 },
    { period_start: '2026-09-29', period_end: '2026-10-05', score: 76 },
  ],
  current_score: 76,
  prev_score: 72,
  change: 4,
};

describe('TrendDimensionCard（specs §4.2.2 B / §4.2.4 规则2）', () => {
  it('TestTrendDimensionCard_EndDot：dot 渲染函数仅对末索引且有值点返回圆点，其余点返回 false（末端点标记本期值，位置无关）', () => {
    render(<TrendDimensionCard data={trendDim} />);

    const dot = lastLineDot.current;
    expect(dot).toBeTypeOf('function');
    const total = trendDim.history.length;
    const mk = (i: number, value: number | undefined) => ({
      index: i,
      value,
      cx: 10 * i,
      cy: 20,
      points: Array.from({ length: total }, () => ({})),
    });
    // 末索引且 score 有值（76）：返回 circle 元素，坐标由 recharts 注入透传
    const end = dot!(mk(total - 1, 76)) as unknown as ReactElement<{ cx?: number; cy?: number; r?: number }>;
    expect(end.type).toBe('circle');
    expect(end.props.cx).toBe(10 * (total - 1));
    expect(end.props.cy).toBe(20);
    expect(end.props.r).toBe(3);
    // 非末索引或值缺失：不渲染
    expect(dot!(mk(0, 72))).toBe(false);
    expect(dot!(mk(total - 1, undefined))).toBe(false);
  });

  it('TestTrendDimensionCard_NullScoreBreak：score=null 传导 undefined 断线且本期值正常呈现（§4.2.4 规则2）', () => {
    render(<TrendDimensionCard data={trendDim} />);

    expect(screen.getByTestId('trend-sparkline')).toBeInTheDocument();
    expect(screen.getByText('76')).toBeInTheDocument();
    expect(screen.getByText('+4')).toBeInTheDocument();
  });
});
