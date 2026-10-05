// 维度逐期走势分面卡（specs P2_TMD_001 §4.2.2 B / §4.2.4 规则2/规则3）：
// 维度名 + 本期值（null 显示 -）+ 环比箭头（null 不渲染）+ 迷你折线（score null 断线、
// 末端点标记本期、悬浮显示区间与分值）；is_weakness 橙色标识与「短板」标签按本期口径不回溯。
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

import { Card, CardContent, CardHeader } from '@/components/ui/card';
import type { DashboardTrendDim } from '@/lib/contracts';
import { cn } from '@/lib/utils';

const LINE_COLOR = 'var(--chart-1)';

/** 环比方向色（specs §4.2.2 B）：正 chart-2、负 warning、0 灰。 */
const CHANGE_UP = 'text-chart-2';
const CHANGE_DOWN = 'text-warning';
const CHANGE_FLAT = 'text-muted-foreground';

export function TrendDimensionCard(props: { data: DashboardTrendDim }): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { data } = props;

  const change = data.change;
  const labelOf = (p: { period_start: string; period_end: string }) =>
    `${p.period_start} ~ ${p.period_end}`;
  // score null → undefined：Line 数据点断开（connectNulls 默认 false，specs §4.2.4 规则2）
  const series = data.history.map((h) => ({
    label: labelOf(h),
    score: h.score ?? undefined,
  }));

  return (
    <Card
      className={cn(
        'gap-3 py-4',
        // 本期共性短板橙色标识（specs §4.2.4 规则3）
        data.is_weakness && 'border-warning/50',
      )}
    >
      <CardHeader className="px-4">
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-1.5">
            <h3 className="truncate text-sm font-semibold">{data.dimension_name}</h3>
            {data.is_weakness ? (
              <span className="shrink-0 rounded-sm bg-warning/15 px-1.5 py-0.5 text-xs font-medium text-warning">
                {t('trend.weaknessTag')}
              </span>
            ) : null}
          </div>
          <div className="flex shrink-0 items-baseline gap-2">
            <span className="font-mono text-xl font-semibold">
              {data.current_score === null ? '-' : data.current_score}
            </span>
            {change === null ? null : (
              <span
                className={cn(
                  'inline-flex items-center gap-0.5 text-xs font-medium',
                  change > 0 ? CHANGE_UP : change < 0 ? CHANGE_DOWN : CHANGE_FLAT,
                )}
              >
                {change > 0 ? (
                  <TrendingUp className="size-3.5" aria-hidden />
                ) : change < 0 ? (
                  <TrendingDown className="size-3.5" aria-hidden />
                ) : (
                  <Minus className="size-3.5" aria-hidden />
                )}
                {change > 0 ? `+${change}` : `${change}`}
              </span>
            )}
          </div>
        </div>
      </CardHeader>
      <CardContent className="px-4">
        {/* 纵轴按维度自适应（specs §4.2.2 B），首末留白收敛适配小卡 */}
        <div data-testid="trend-sparkline" className="h-20 w-full">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={series} margin={{ top: 4, right: 8, bottom: 0, left: -22 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="label" hide />
              <YAxis domain={['auto', 'auto']} hide />
              <Tooltip
                formatter={(value) => [
                  typeof value === 'number' ? value : '-',
                  t('trend.scoreLabel'),
                ]}
              />
              {/* 末端点标记本期值（specs §4.2.2 B）：dot 渲染函数仅对末索引且有值点画圆，
                  坐标由 recharts 注入（cx/cy），其余点返回 false 不渲染 */}
              <Line
                type="monotone"
                dataKey="score"
                stroke={LINE_COLOR}
                strokeWidth={2}
                dot={(p) =>
                  p.index === p.points.length - 1 && typeof p.value === 'number' ? (
                    <circle cx={p.cx} cy={p.cy} r={3} fill={LINE_COLOR} />
                  ) : (
                    false
                  )
                }
                activeDot={{ r: 3 }}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  );
}
