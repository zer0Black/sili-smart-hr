// 工作台首页四区块组件与页面级测试（specs P2_WRK_001 §4.1.2 A/B/C/D、§4.1.3、§4.1.4 规则4/5、§4.1.5、§8.3）。
// 跳转断言走 mock useNavigate（dashboard components.test 同款）；页面级用例经 WorkspacePage
// 直挂（mock hooks 层），断言四区块标题、规则4 空态与区块独立空态。
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, beforeEach } from 'vitest';

import '@/i18n/config';
import i18n from '@/i18n/config';
import type {
  WorkspaceAttentionRow,
  WorkspaceBatch,
  WorkspaceData,
  WorkspaceProfile,
  WorkspaceTrend,
} from '@/lib/contracts';

await i18n.changeLanguage('zh');

const navigateMock = vi.hoisted(() => vi.fn());
const workspaceMock = vi.hoisted(() => vi.fn());
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigateMock,
  // 页面级用例直挂路由文件，createFileRoute 以恒等 stub 承载（不进路由树）
  createFileRoute: () => () => ({}),
}));
vi.mock('../hooks', () => ({
  useWorkspace: () => workspaceMock(),
}));

// recharts 在 jsdom 无尺寸不渲染内部图表，stub 成透传容器（dashboard 测试同款）；
// Line/Tooltip stub 捕获 props 供位置无关的行为断言（序列存在性与 Tooltip 明细行）。
type Captured = { current: { propsList: { dataKey?: unknown; connectNulls?: unknown; label?: string; formatter?: (v: unknown, n: unknown) => unknown }[]; tooltipFormatter: ((v: unknown, n: unknown) => unknown) | null } };
const captured: Captured = { current: { propsList: [], tooltipFormatter: null } };
vi.mock('recharts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('recharts')>();
  const Passthrough = ({ children }: { children?: React.ReactNode }) => <>{children}</>;
  return {
    ...actual,
    ResponsiveContainer: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
    LineChart: Passthrough,
    BarChart: Passthrough,
    RadarChart: Passthrough,
    CartesianGrid: Passthrough,
    PolarGrid: Passthrough,
    PolarAngleAxis: Passthrough,
    PolarRadiusAxis: Passthrough,
    XAxis: Passthrough,
    YAxis: Passthrough,
    Tooltip: ({ formatter }: { formatter?: (v: unknown, n: unknown) => unknown }) => {
      if (typeof formatter === 'function') captured.current.tooltipFormatter = formatter;
      return null;
    },
    Bar: Passthrough,
    Radar: ({ name, dataKey, strokeDasharray }: { name?: string; dataKey?: string; strokeDasharray?: string }) => (
      <g data-testid="radar-line" data-name={name} data-key={dataKey} data-dash={strokeDasharray} />
    ),
    Line: (props: { dataKey?: unknown; connectNulls?: unknown; name?: string }) => {
      captured.current.propsList.push(props);
      return null;
    },
    Cell: ({ fill }: { fill?: string }) => <g data-testid="chart-cell" fill={fill} />,
  };
});

import { BatchSituationCards } from '../components/batch-situation-cards';
import { TrendEvolution } from '../components/trend-evolution';
import { ProfileBrief } from '../components/profile-brief';
import { AttentionTable } from '../components/attention-table';
import { WorkspacePage } from '@/routes/_authenticated';

beforeEach(() => {
  navigateMock.mockReset();
  workspaceMock.mockReset();
  captured.current = { propsList: [], tooltipFormatter: null };
});

const formatModule = (m: 'AI_USAGE' | 'AI_MGMT') =>
  m === 'AI_USAGE' ? 'AI 使用能力' : 'AI 管理能力';
const dimNames: Record<string, string> = {
  AI_UPPER_PLAN: '任务规划',
  AI_BASE_CLARITY: '指令清晰度',
  MGT_DELEGATION: '授权分工',
};
const formatDimension = (code: string) => dimNames[code] ?? code;

// ---------- BatchSituationCards ----------

const batchWithStatus: WorkspaceBatch = {
  status: 'partial_failed',
  next_trigger_at: '2026-10-07 03:00',
  alert_count: 1,
  overdue_count: 2,
  data_updated_at: '2026-10-06 02:12',
};
const batchNoStatus: WorkspaceBatch = {
  status: null,
  next_trigger_at: null,
  alert_count: null,
  overdue_count: null,
  data_updated_at: null,
};

describe('BatchSituationCards（specs §4.1.2 A / §4.1.3 / §4.1.4 规则4）', () => {
  it('TestBatchSituationCards_StatusBadge：批次状态按四态渲染 Badge（沿用 F6 列表口径）', () => {
    const { unmount } = render(<BatchSituationCards data={batchWithStatus} />);
    // 状态文本在 Badge 与跑批态势卡两处出现（态势卡主值即批次状态）
    expect(screen.getAllByText('部分失败').length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('尚未发起评估')).not.toBeInTheDocument();
    unmount();

    const cases: [WorkspaceBatch['status'], string][] = [
      ['running', '进行中'],
      ['success', '成功'],
      ['failed', '失败'],
    ];
    for (const [status, label] of cases) {
      const { unmount: u } = render(
        <BatchSituationCards data={{ ...batchWithStatus, status }} />,
      );
      expect(screen.getAllByText(label).length).toBeGreaterThanOrEqual(1);
      u();
    }
  });

  it('TestBatchSituationCards_NoBatchEmpty：status=null 显示「尚未发起评估」与跳评测运营中心引导（规则4）', () => {
    render(<BatchSituationCards data={batchNoStatus} />);

    expect(screen.getByText('尚未发起评估')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /前往评测运营中心/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /跑批态势/ })).not.toBeInTheDocument();
  });

  it('TestBatchSituationCards_CardNavigates：跑批态势卡与告警卡跳 /assessment，逾期卡携 tab=ai_mgmt&status=expired', async () => {
    const user = userEvent.setup();
    render(<BatchSituationCards data={batchWithStatus} />);

    await user.click(screen.getByRole('button', { name: /跑批态势/ }));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/assessment' });

    await user.click(screen.getByRole('button', { name: /告警/ }));
    expect(navigateMock).toHaveBeenCalledWith({ to: '/assessment' });

    await user.click(screen.getByRole('button', { name: /逾期/ }));
    expect(navigateMock).toHaveBeenCalledWith({
      to: '/assessment',
      search: { tab: 'ai_mgmt', status: 'expired' },
    });
    expect(navigateMock).toHaveBeenCalledTimes(3);
  });

  it('TestBatchSituationCards_NullFieldsHidden：next_trigger_at / data_updated_at null 时行不渲染', () => {
    render(<BatchSituationCards data={{ ...batchWithStatus, next_trigger_at: null, data_updated_at: null }} />);

    expect(screen.queryByText(/下次跑批/)).not.toBeInTheDocument();
    expect(screen.queryByText(/数据更新时间/)).not.toBeInTheDocument();
  });

  it('TestBatchSituationCards_MinuteTimes：下次跑批与数据更新时间分钟精度直出', () => {
    render(<BatchSituationCards data={batchWithStatus} />);

    expect(screen.getByText(/2026-10-07 03:00/)).toBeInTheDocument();
    expect(screen.getByText(/2026-10-06 02:12/)).toBeInTheDocument();
  });
});

// ---------- TrendEvolution ----------

const trendData: WorkspaceTrend = {
  periods: [
    { period_start: '2026-09-14', period_end: '2026-09-20' },
    { period_start: '2026-09-21', period_end: '2026-09-27' },
    { period_start: '2026-09-28', period_end: '2026-10-04' },
  ],
  series: [
    { module: 'AI_USAGE', scores: [70, 72, 75], current_score: 75, change_vs_prev: 3 },
    { module: 'AI_MGMT', scores: [null, 60, 58], current_score: 58, change_vs_prev: -2 },
  ],
  activity: {
    active_ratio: 41.7,
    active_change_pp: 1.2,
    unused_count: 12,
    unused_change: -3,
  },
};

describe('TrendEvolution（specs §4.1.2 B / §4.1.5）', () => {
  it('TestTrendEvolution_DualSeries：双序列 Line 且 connectNulls=false（null 断点，specs §4.1.5）', () => {
    render(<TrendEvolution data={trendData} />);

    const lines = captured.current.propsList;
    expect(lines).toHaveLength(2);
    for (const line of lines) expect(line.connectNulls).toBe(false);
  });

  it('TestTrendEvolution_AllNullSeriesMissing：某模块 scores 全 null 时该折线整条缺失但另一条与图例保留', () => {
    render(
      <TrendEvolution
        data={{
          ...trendData,
          series: [
            trendData.series[0],
            { module: 'AI_MGMT', scores: [null, null, null], current_score: null, change_vs_prev: null },
          ],
        }}
      />,
    );

    // 全 null 模块不渲染 Line；有数据模块 Line 保留
    expect(captured.current.propsList).toHaveLength(1);
    // 图例与关键数卡标签保留两模块名（图例恒展示，含全空模块）
    expect(screen.getAllByText('AI 使用能力').length).toBeGreaterThan(0);
    expect(screen.getAllByText('AI 管理能力').length).toBeGreaterThan(0);
  });

  it('TestTrendEvolution_KeyCards：四张关键数卡数值与环比（pp 一位小数）呈现', () => {
    render(<TrendEvolution data={trendData} />);

    expect(screen.getByText('75')).toBeInTheDocument();
    expect(screen.getByText('+3')).toBeInTheDocument();
    expect(screen.getByText('58')).toBeInTheDocument();
    expect(screen.getByText('-2')).toBeInTheDocument();
    expect(screen.getByText('41.7%')).toBeInTheDocument();
    expect(screen.getByText('+1.2pp')).toBeInTheDocument();
    expect(screen.getByText('12')).toBeInTheDocument();
    expect(screen.getByText('-3')).toBeInTheDocument();
  });

  it('TestTrendEvolution_ChangeDirectionColor：综合分升/活跃率升/未使用降正向 chart-2，反向 warning（§4.1.5）', () => {
    render(<TrendEvolution data={trendData} />);

    expect(screen.getByText('+3')).toHaveClass('text-chart-2');
    expect(screen.getByText('-2')).toHaveClass('text-warning');
    expect(screen.getByText('+1.2pp')).toHaveClass('text-chart-2');
    expect(screen.getByText('-3')).toHaveClass('text-chart-2'); // 未使用降为正向
  });

  it('TestTrendEvolution_ChangeNullHidden：环比 null 时区段不显示', () => {
    render(
      <TrendEvolution
        data={{
          ...trendData,
          series: [
            { module: 'AI_USAGE', scores: [70], current_score: 70, change_vs_prev: null },
            { module: 'AI_MGMT', scores: [60], current_score: 60, change_vs_prev: null },
          ],
          activity: { active_ratio: 40, active_change_pp: null, unused_count: 5, unused_change: null },
        }}
      />,
    );

    expect(screen.queryByText(/^\+0/)).not.toBeInTheDocument();
    expect(screen.getByText('70')).toBeInTheDocument();
    expect(screen.getByText('60')).toBeInTheDocument();
    expect(screen.getByText('40.0%')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('TestTrendEvolution_UnusedCardNavigates：未使用人数卡整卡可点击跳 /profile 携 unused_only=true（§4.1.3）', async () => {
    const user = userEvent.setup();
    render(<TrendEvolution data={trendData} />);

    await user.click(screen.getByRole('button', { name: /未使用人数/ }));

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/profile',
      search: { unused_only: 'true' },
    });
  });

  it('TestTrendEvolution_TooltipDetail：Tooltip formatter 显示区间止日与分值明细（§4.1.5）', () => {
    render(<TrendEvolution data={trendData} />);

    const formatter = captured.current.tooltipFormatter;
    expect(formatter).toBeTypeOf('function');
    // formatter 返回 [值, 名称] 数组：number 直出分值，null 显示 -（specs §4.1.5 分值明细）
    expect(formatter!(75, 'AI 使用能力')).toEqual([75, 'AI 使用能力']);
    expect(formatter!(null, 'AI 管理能力')).toEqual(['-', 'AI 管理能力']);
  });

  it('TestTrendEvolution_ActivityNullDegrade：activity null（名单失败）时综合分两卡照常、活跃两卡空态（03 §1.3）', () => {
    render(<TrendEvolution data={{ ...trendData, activity: null }} />);

    // 综合分两卡不依赖名单，照常渲染
    expect(screen.getByText('75')).toBeInTheDocument();
    expect(screen.getByText('+3')).toBeInTheDocument();
    expect(screen.getByText('58')).toBeInTheDocument();
    expect(screen.getByText('-2')).toBeInTheDocument();
    // 活跃率与未使用两卡空态（不渲染）
    expect(screen.queryByText('41.7%')).not.toBeInTheDocument();
    expect(screen.queryByText('12')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /未使用人数/ })).not.toBeInTheDocument();
  });
});

// ---------- ProfileBrief ----------

const profileData: WorkspaceProfile = {
  modules: [
    {
      module: 'AI_USAGE',
      dimensions: [
        { dimension_code: 'AI_BASE_CLARITY', dimension_name: '指令清晰度', avg_score: 76, is_weakness: false },
        { dimension_code: 'AI_UPPER_PLAN', dimension_name: '任务规划', avg_score: 58, is_weakness: true },
        { dimension_code: 'AI_UPPER_TOOL', dimension_name: '工具运用', avg_score: null, is_weakness: false },
      ],
      overall_avg: 74,
    },
    {
      module: 'AI_MGMT',
      dimensions: [
        { dimension_code: 'MGT_DELEGATION', dimension_name: '授权分工', avg_score: 61, is_weakness: true },
      ],
      overall_avg: null,
    },
  ],
  enneagram: {
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
  },
  weaknesses: [
    { module: 'AI_USAGE', dimension_code: 'AI_UPPER_PLAN', dimension_name: '任务规划', low_ratio: 66.7 },
    { module: 'AI_MGMT', dimension_code: 'MGT_DELEGATION', dimension_name: '授权分工', low_ratio: 40.0 },
  ],
  suggestion: { status: 'generated', summary: '团队整体 AI 使用能力稳健，任务规划为共性短板' },
};

describe('ProfileBrief（specs §4.1.2 C / §4.1.4 规则5）', () => {
  it('TestProfileBrief_DualRadarReferenceLine：双模块雷达渲染，overall_avg 非 null 有虚线参考线、null 无', () => {
    render(<ProfileBrief data={profileData} />);

    const lines = screen.getAllByTestId('radar-line');
    const dashed = lines.filter((l) => l.hasAttribute('data-dash'));
    expect(lines.length).toBeGreaterThanOrEqual(3); // 管理模块无参考线
    expect(dashed).toHaveLength(1); // 使用模块有参考线
  });

  it('TestProfileBrief_EnneagramDominant：恒 9 柱，主导型 primary 高亮（§4.1.2 C）', () => {
    render(<ProfileBrief data={profileData} />);

    const cells = screen.getAllByTestId('chart-cell');
    expect(cells).toHaveLength(9);
    // 主导型 3 高亮 primary，其余 muted
    const highlighted = cells.filter((c) => c.getAttribute('fill') === 'var(--primary)');
    expect(highlighted).toHaveLength(1);
    expect(cells.filter((c) => c.getAttribute('fill') === 'var(--muted)')).toHaveLength(8);
    expect(screen.getAllByText('成就型').length).toBeGreaterThan(0); // profile:enneagram.type3
  });

  it('TestProfileBrief_WeaknessTagNavigates：短板标签点击跳 /profile 携 dimension_code（§4.1.3，口径同 F10）', async () => {
    const user = userEvent.setup();
    render(<ProfileBrief data={profileData} />);

    await user.click(screen.getByRole('button', { name: /任务规划/ }));

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/profile',
      search: { dimension_code: 'AI_UPPER_PLAN' },
    });
  });

  it('TestProfileBrief_SuggestionStates：generated 显示 summary，其余态显示暂无数据且短板标签照常（规则5）', () => {
    const { unmount } = render(<ProfileBrief data={profileData} />);
    expect(screen.getByText(/团队整体 AI 使用能力稳健/)).toBeInTheDocument();
    unmount();

    for (const status of ['generating', 'failed', 'none'] as const) {
      const { unmount: u } = render(
        <ProfileBrief data={{ ...profileData, suggestion: { status, summary: '' } }} />,
      );
      expect(screen.getByText('暂无数据')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /任务规划/ })).toBeInTheDocument();
      u();
    }
  });

  it('TestProfileBrief_DashboardLink：右上角查看完整团队看板跳 /dashboard（§4.1.3）', async () => {
    const user = userEvent.setup();
    render(<ProfileBrief data={profileData} />);

    await user.click(screen.getByRole('button', { name: /查看完整团队看板/ }));

    expect(navigateMock).toHaveBeenCalledWith({ to: '/dashboard' });
  });
});

// ---------- AttentionTable ----------

function makeRow(partial: Partial<WorkspaceAttentionRow>): WorkspaceAttentionRow {
  return {
    staff_name: '李芳',
    category: 'weak',
    activity_level: 'low_freq',
    ai_usage_score: null,
    ai_mgmt_score: null,
    weak_modules: [],
    days_since_active: null,
    ...partial,
  };
}

const attentionRows: WorkspaceAttentionRow[] = [
  makeRow({
    staff_name: '李芳',
    ai_usage_score: 52,
    ai_mgmt_score: 45,
    weak_modules: [{ module: 'AI_USAGE', score: 52, weak_dims: ['AI_UPPER_PLAN'] }],
  }),
  makeRow({
    staff_name: '王强',
    category: 'unused',
    activity_level: 'unused',
    days_since_active: 12,
  }),
];

describe('AttentionTable（specs §4.1.2 D / §8.3 偏离1、偏离3）', () => {
  it('TestAttentionTable_ColumnsAndRows：六列表头与姓名、活跃度三态 Badge、关注原因呈现', () => {
    render(<AttentionTable rows={attentionRows} formatModule={formatModule} formatDimension={formatDimension} />);

    for (const label of ['姓名', '活跃度', 'AI 使用能力总分', 'AI 管理能力总分', '关注原因', '操作']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
    expect(screen.getByText('李芳')).toBeInTheDocument();
    expect(screen.getByText('王强')).toBeInTheDocument();
    expect(screen.getByText('低频')).toBeInTheDocument();
    expect(screen.getByText('未使用')).toBeInTheDocument();
    expect(screen.getByText(/AI 使用能力 52 分/)).toBeInTheDocument();
    expect(screen.getByText(/最近活跃距今 12 天/)).toBeInTheDocument();
  });

  it('TestAttentionTable_NullScorePending：总分 null 显示「待评估」（§8.3 偏离3）', () => {
    render(<AttentionTable rows={[attentionRows[1]]} formatModule={formatModule} formatDimension={formatDimension} />);

    expect(screen.getAllByText('待评估')).toHaveLength(2);
  });

  it('TestAttentionTable_LowScoreDestructive：总分 < 60 渲染红色（destructive 文本色）', () => {
    render(<AttentionTable rows={attentionRows} formatModule={formatModule} formatDimension={formatDimension} />);

    expect(screen.getByText('52')).toHaveClass('text-destructive');
    expect(screen.getByText('45')).toHaveClass('text-destructive');
  });

  it('TestAttentionTable_HighScoreNotDestructive：总分 >= 60 无红色类', () => {
    render(
      <AttentionTable
        rows={[makeRow({ staff_name: '赵敏', ai_usage_score: 72, ai_mgmt_score: 65 })]}
        formatModule={formatModule}
        formatDimension={formatDimension}
      />,
    );

    expect(screen.getByText('72')).not.toHaveClass('text-destructive');
    expect(screen.getByText('65')).not.toHaveClass('text-destructive');
  });

  it('TestAttentionTable_ViewProfileLink：行级查看画像跳 /profile/$staffName 携 staff_name（§4.1.3）', async () => {
    const user = userEvent.setup();
    render(<AttentionTable rows={attentionRows} formatModule={formatModule} formatDimension={formatDimension} />);

    const row = screen.getByText('李芳').closest('tr')!;
    await user.click(within(row).getByRole('button', { name: /查看画像/ }));

    expect(navigateMock).toHaveBeenCalledWith({
      to: '/profile/$staffName',
      params: { staffName: '李芳' },
    });
  });

  it('TestAttentionTable_ViewAllLink：区块右上角查看全员画像跳 /profile（§4.1.3）', async () => {
    const user = userEvent.setup();
    render(<AttentionTable rows={attentionRows} formatModule={formatModule} formatDimension={formatDimension} />);

    await user.click(screen.getByRole('button', { name: /查看全员画像/ }));

    expect(navigateMock).toHaveBeenCalledWith({ to: '/profile' });
  });

  it('TestAttentionTable_EmptyState：空数据显示空态且无分页控件（§8.3 偏离1）', () => {
    render(<AttentionTable rows={[]} formatModule={formatModule} formatDimension={formatDimension} />);

    expect(screen.getByText('暂无需要关注的人')).toBeInTheDocument();
    expect(screen.queryByText(/上一页/)).not.toBeInTheDocument();
    expect(screen.queryByText(/下一页/)).not.toBeInTheDocument();
  });
});

// ---------- WorkspacePage（页面级） ----------

function makeQueryResult(data: WorkspaceData | undefined, state: 'pending' | 'error' | 'success') {
  if (state === 'pending') return { isPending: true, isError: false, data: undefined, refetch: vi.fn() };
  if (state === 'error') return { isPending: false, isError: true, data: undefined, refetch: vi.fn() };
  return { isPending: false, isError: false, data, refetch: vi.fn() };
}

const fullData: WorkspaceData = {
  batch: batchWithStatus,
  current_period: { period_start: '2026-09-28', period_end: '2026-10-04' },
  trend: trendData,
  profile: profileData,
  attention: attentionRows,
};

const emptyAllData: WorkspaceData = {
  batch: batchNoStatus,
  current_period: null,
  trend: null,
  profile: null,
  attention: null,
};

describe('WorkspacePage（specs §4.1.1 / §4.1.4 规则4）', () => {
  it('TestWorkspacePage_FourSections：数据到位后四区块标题出现且纵向排列', () => {
    workspaceMockReturnValue(makeQueryResult(fullData, 'success'));
    render(<WorkspacePage />);

    expect(screen.getByText('工作台')).toBeInTheDocument();
    expect(screen.getByText('态势与待处理')).toBeInTheDocument();
    expect(screen.getByText('团队能力演进')).toBeInTheDocument();
    expect(screen.getByText('团队整体画像速览')).toBeInTheDocument();
    expect(screen.getByText('需要关注的人')).toBeInTheDocument();
  });

  it('TestWorkspacePage_PendingSkeleton：isPending 时整页骨架占位，四区块标题不出现', () => {
    workspaceMockReturnValue(makeQueryResult(undefined, 'pending'));
    render(<WorkspacePage />);

    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.queryByText('团队能力演进')).not.toBeInTheDocument();
    expect(screen.queryByText('需要关注的人')).not.toBeInTheDocument();
  });

  it('TestWorkspacePage_ErrorRetry：isError 时错误占位与重试按钮（同 dashboard 页形态）', async () => {
    const refetch = vi.fn();
    workspaceMockReturnValue({ isPending: false, isError: true, data: undefined, refetch });
    render(<WorkspacePage />);

    expect(screen.getByText(/加载失败/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('button', { name: /重试/ }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it('TestWorkspacePage_EmptyGuidance：全空数据时态势区引导出现、各区块独立空态（规则4）', () => {
    workspaceMockReturnValue(makeQueryResult(emptyAllData, 'success'));
    render(<WorkspacePage />);

    expect(screen.getByText('尚未发起评估')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /前往评测运营中心/ })).toBeInTheDocument();
    expect(screen.getByText('团队能力演进')).toBeInTheDocument();
    expect(screen.getByText('团队整体画像速览')).toBeInTheDocument();
    expect(screen.getByText('需要关注的人')).toBeInTheDocument();
    expect(screen.getByText('暂无需要关注的人')).toBeInTheDocument();
  });

  it('TestWorkspacePage_PartialEmpty：trend/profile/attention 局部空时区块提示暂无数据且互不阻断（规则4）', () => {
    workspaceMockReturnValue(
      makeQueryResult({ ...fullData, trend: null, profile: null, attention: [] }, 'success'),
    );
    render(<WorkspacePage />);

    // 态势区正常渲染（不被空区块阻断；状态文本在 Badge 与态势卡两处出现）
    expect(screen.getAllByText('部分失败').length).toBeGreaterThanOrEqual(1);
    // 演进区与画像区各自显示暂无数据提示
    expect(screen.getAllByText('暂无数据').length).toBeGreaterThanOrEqual(2);
    // 关注表空态照常
    expect(screen.getByText('暂无需要关注的人')).toBeInTheDocument();
  });
});

/** 内联辅助：workspaceMock 需要同一引用被组件消费。 */
function workspaceMockReturnValue(v: unknown) {
  workspaceMock.mockReturnValue(v);
}
