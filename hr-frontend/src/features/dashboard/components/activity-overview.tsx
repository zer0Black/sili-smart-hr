// 活跃度概览（specs P2_TMD_001 §4.1.2 B / §4.1.4 规则2 / §4.1.3 未使用人群跳转）：
// 三态人数卡横排 + 环形图（中心全员人数）+ 环比行。unused 卡可点击携 unused_only 跳 F9；
// active/low_freq 卡置灰不可点击（人群定位聚焦未使用与短板，nav-menu 灰显口径）。
import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import { Minus, TrendingDown, TrendingUp } from 'lucide-react';
import type { JSX } from 'react';
import {
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
} from 'recharts';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DashboardActivity } from '@/lib/contracts';
import { cn } from '@/lib/utils';

const COLOR_ACTIVE = 'var(--chart-2)';
const COLOR_LOW = 'var(--chart-4)';
const COLOR_UNUSED = 'var(--muted)';

const POSITIVE_COLOR = 'text-chart-2';
const NEGATIVE_COLOR = 'text-warning';

export function ActivityOverview(props: {
  data: DashboardActivity;
  staffTotal: number;
}): JSX.Element {
  const { t } = useTranslation('dashboard');
  const navigate = useNavigate();
  const { data, staffTotal } = props;

  const cards = [
    { key: 'active', item: data.active, disabled: true },
    { key: 'low_freq', item: data.low_freq, disabled: true },
    { key: 'unused', item: data.unused, disabled: false },
  ] as const;

  const pieData = [
    { key: 'active', value: data.active.count, ratio: data.active.ratio, fill: COLOR_ACTIVE },
    { key: 'low_freq', value: data.low_freq.count, ratio: data.low_freq.ratio, fill: COLOR_LOW },
    { key: 'unused', value: data.unused.count, ratio: data.unused.ratio, fill: COLOR_UNUSED },
  ];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-normal text-muted-foreground">
          {t('activity.title')}
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex flex-col gap-6 xl:flex-row">
          <div className="grid flex-1 grid-cols-3 gap-4">
            {cards.map(({ key, item, disabled }) => (
              <StatCard
                key={key}
                label={t(`activity.${key}`)}
                count={item.count}
                ratio={item.ratio}
                disabled={disabled}
                subtitle={
                  key === 'active'
                    ? t('activity.staffTotal', { count: staffTotal })
                    : undefined
                }
                onClick={
                  disabled
                    ? undefined
                    : () => {
                        // 未使用人群跳转（03 §4.5）：F9 列表页仅看未使用开关经 URL 参数开启
                        void navigate({ to: '/profile', search: { unused_only: 'true' } });
                      }
                }
              />
            ))}
          </div>
          <div className="relative h-52 w-full xl:w-72">
            <ResponsiveContainer width="100%" height="100%">
              <PieChart>
                <Tooltip
                  formatter={(value, name) => [
                    value,
                    typeof name === 'string' ? t(`activity.${name}`) : name,
                  ]}
                />
                <Pie
                  data={pieData}
                  dataKey="value"
                  nameKey="key"
                  innerRadius="62%"
                  outerRadius="88%"
                  paddingAngle={2}
                  isAnimationActive={false}
                >
                  {pieData.map((d) => (
                    <Cell key={d.key} fill={d.fill} />
                  ))}
                </Pie>
              </PieChart>
            </ResponsiveContainer>
            {/* 环形中心：全员人数（specs §4.1.2 B 三态环形图中心显示全员人数） */}
            <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
              <span className="font-mono text-2xl font-semibold">{staffTotal}</span>
              <span className="text-muted-foreground text-xs">{t('activity.staffTotalShort')}</span>
            </div>
          </div>
        </div>
        {data.mom ? <MomRow mom={data.mom} /> : null}
      </CardContent>
    </Card>
  );
}

/** 三态人数卡：count + 占比；unused 可点击跳转，其余灰显（cursor-not-allowed 置灰形态）。 */
function StatCard(props: {
  label: string;
  count: number;
  ratio: number;
  disabled: boolean;
  subtitle?: string;
  onClick?: () => void;
}): JSX.Element {
  const { label, count, ratio, disabled, subtitle, onClick } = props;
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      aria-label={label}
      className={cn(
        'rounded-lg border p-4 text-left',
        disabled
          ? 'cursor-not-allowed text-muted-foreground/60'
          : 'cursor-pointer transition-colors hover:border-primary/40 hover:bg-accent/50',
      )}
    >
      <span className="block text-xs font-medium">{label}</span>
      {subtitle ? <span className="text-muted-foreground block text-xs">{subtitle}</span> : null}
      <span className="mt-1 block font-mono text-2xl font-semibold">{count}</span>
      <span className="text-muted-foreground block text-xs">{ratio.toFixed(1)}%</span>
    </button>
  );
}

/** 环比行（specs §4.1.4 规则2）：活跃增加与未使用减少为正向色；最早区间 mom=null 整行不渲染。 */
function MomRow(props: { mom: { active_change: number; unused_change: number } }): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { mom } = props;

  const change = (label: string, value: number, positiveWhenUp: boolean) => {
    const isPositive = positiveWhenUp ? value > 0 : value < 0;
    return (
      <span className="flex items-center gap-1.5">
        <span className="text-muted-foreground">{label}</span>
        {value === 0 ? (
          <Minus className="text-muted-foreground size-3.5" aria-hidden />
        ) : (
          <span
            className={cn(
              'inline-flex items-center gap-0.5 font-medium',
              isPositive ? POSITIVE_COLOR : NEGATIVE_COLOR,
            )}
          >
            {isPositive ? (
              <TrendingUp className="size-3.5" aria-hidden />
            ) : (
              <TrendingDown className="size-3.5" aria-hidden />
            )}
            {value > 0 ? `+${value}` : `${value}`}
          </span>
        )}
      </span>
    );
  };

  return (
    <div
      data-testid="activity-mom"
      className="text-muted-foreground mt-4 flex flex-wrap items-center gap-x-6 gap-y-1 text-xs"
    >
      <span>{t('activity.mom')}</span>
      {change(t('activity.active'), mom.active_change, true)}
      {change(t('activity.unused'), mom.unused_change, false)}
    </div>
  );
}
