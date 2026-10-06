// 团队能力演进区（specs P2_WRK_001 §4.1.2 B / §4.1.3 未使用卡跳转 / §4.1.5）：
// 双序列折线（AI_USAGE/AI_MGMT 综合分，null 断点 connectNulls=false，悬浮显示区间止日与分值）
// + 四张环比关键数卡（方向色：升正/未使用降正，null 区段不显示）。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Minus, TrendingDown, TrendingUp } from 'lucide-react';
import type { JSX } from 'react';
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { WorkspaceTrend } from '@/lib/contracts';
import { cn } from '@/lib/utils';

const USAGE_COLOR = 'var(--chart-1)';
const MGMT_COLOR = 'var(--chart-2)';
const POSITIVE_COLOR = 'text-chart-2';
const NEGATIVE_COLOR = 'text-warning';

/** 环比区段：null 不渲染；方向色按 positiveWhenUp 判定（specs §4.1.5）。 */
function ChangeBadge(props: {
  value: number;
  unit?: string;
  positiveWhenUp: boolean;
}): JSX.Element {
  const { value, unit = '', positiveWhenUp } = props;
  const isPositive = positiveWhenUp ? value > 0 : value < 0;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-0.5 text-xs font-medium',
        isPositive ? POSITIVE_COLOR : NEGATIVE_COLOR,
      )}
    >
      {value > 0 ? (
        <TrendingUp className="size-3.5" aria-hidden />
      ) : value < 0 ? (
        <TrendingDown className="size-3.5" aria-hidden />
      ) : (
        <Minus className="size-3.5" aria-hidden />
      )}
      {`${value > 0 ? '+' : ''}${value}${unit}`}
    </span>
  );
}

export function TrendEvolution(props: { data: WorkspaceTrend }): JSX.Element {
  const { t } = useTranslation('workspace');
  const navigate = useNavigate();
  const { data } = props;

  const seriesOf = (module: 'AI_USAGE' | 'AI_MGMT') =>
    data.series.find((s) => s.module === module) ?? null;

  // x 轴区间止日，双序列按 periods 一一对应（specs §4.1.2 B）
  const chartData = data.periods.map((p, i) => ({
    label: p.period_end,
    usage: seriesOf('AI_USAGE')?.scores[i] ?? undefined,
    mgmt: seriesOf('AI_MGMT')?.scores[i] ?? undefined,
  }));

  const usage = seriesOf('AI_USAGE');
  const mgmt = seriesOf('AI_MGMT');
  const activity = data.activity;
  // 某模块全 null 时折线整条缺失（specs §4.1.5 断点口径的全空退化），图例保留
  const usageHasData = usage ? usage.scores.some((s) => s !== null) : false;
  const mgmtHasData = mgmt ? mgmt.scores.some((s) => s !== null) : false;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-normal text-muted-foreground">
          {t('trend.title')}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {/* 图例：双模块恒展示（含全空模块） */}
        <div className="flex flex-wrap items-center gap-4 text-xs">
          <span className="flex items-center gap-1.5">
            <span className="inline-block size-2.5 rounded-sm" style={{ background: USAGE_COLOR }} />
            {t('trend.legendUsage')}
          </span>
          <span className="flex items-center gap-1.5">
            <span className="inline-block size-2.5 rounded-sm" style={{ background: MGMT_COLOR }} />
            {t('trend.legendMgmt')}
          </span>
        </div>
        <div className="h-56 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chartData} margin={{ top: 8, right: 12, bottom: 0, left: -18 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" tickLine={false} axisLine={true} tickMargin={8} />
              <YAxis tickLine={false} axisLine={false} tickMargin={4} domain={['auto', 'auto']} />
              {/* 悬浮显示区间止日与分值明细，null 值显示 -（specs §4.1.5 同 F10 交互） */}
              <Tooltip
                formatter={(value, name) => [
                  typeof value === 'number' ? value : '-',
                  typeof name === 'string' ? name : '',
                ]}
              />
              {usageHasData ? (
                <Line
                  type="monotone"
                  dataKey="usage"
                  name={t('trend.legendUsage')}
                  stroke={USAGE_COLOR}
                  strokeWidth={2}
                  connectNulls={false}
                  activeDot={{ r: 3 }}
                  isAnimationActive={false}
                />
              ) : null}
              {mgmtHasData ? (
                <Line
                  type="monotone"
                  dataKey="mgmt"
                  name={t('trend.legendMgmt')}
                  stroke={MGMT_COLOR}
                  strokeWidth={2}
                  connectNulls={false}
                  activeDot={{ r: 3 }}
                  isAnimationActive={false}
                />
              ) : null}
            </LineChart>
          </ResponsiveContainer>
        </div>
        {/* 四张环比关键数卡（specs §4.1.2 B）：综合分 ±int、活跃率 % ±pp、未使用人数 ±int */}
        {activity ? (
          <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
            <StatCard
              label={t('trend.legendUsage')}
              value={usage?.current_score ?? '-'}
              change={
                usage?.change_vs_prev != null ? (
                  <ChangeBadge value={usage.change_vs_prev} positiveWhenUp={true} />
                ) : null
              }
            />
            <StatCard
              label={t('trend.legendMgmt')}
              value={mgmt?.current_score ?? '-'}
              change={
                mgmt?.change_vs_prev != null ? (
                  <ChangeBadge value={mgmt.change_vs_prev} positiveWhenUp={true} />
                ) : null
              }
            />
            <StatCard
              label={t('trend.activeRatio')}
              value={`${activity.active_ratio.toFixed(1)}%`}
              change={
                activity.active_change_pp != null ? (
                  <ChangeBadge
                    value={activity.active_change_pp}
                    unit="pp"
                    positiveWhenUp={true}
                  />
                ) : null
              }
            />
            {/* 未使用人数卡整卡可点击，携 unused_only 跳人员画像列表（specs §4.1.3 / BR5） */}
            <button
              type="button"
              aria-label={t('trend.unusedCount')}
              onClick={() => {
                void navigate({ to: '/profile', search: { unused_only: 'true' } });
              }}
              className="cursor-pointer rounded-lg border p-4 text-left transition-colors hover:border-primary/40 hover:bg-accent/50"
            >
              <span className="block text-xs font-medium">{t('trend.unusedCount')}</span>
              <span className="mt-1 flex items-baseline gap-2">
                <span className="font-mono text-2xl font-semibold">{activity.unused_count}</span>
                {activity.unused_change != null ? (
                  <ChangeBadge value={activity.unused_change} positiveWhenUp={false} />
                ) : null}
              </span>
            </button>
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

/** 综合分/活跃率卡：静态展示（不可点击），环比 null 时区段不渲染。 */
function StatCard(props: {
  label: string;
  value: number | string;
  change?: JSX.Element | null;
}): JSX.Element {
  const { label, value, change } = props;
  return (
    <div className="rounded-lg border p-4">
      <span className="block text-xs font-medium">{label}</span>
      <span className="mt-1 flex items-baseline gap-2">
        <span className="font-mono text-2xl font-semibold">{value}</span>
        {change ?? null}
      </span>
    </div>
  );
}
