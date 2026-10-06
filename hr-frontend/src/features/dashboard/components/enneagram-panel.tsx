// 团队人格构成（specs P2_TMD_001 §4.1.2 D / §4.1.4 规则5）：
// 九型分布柱图（恒 9 项，主导/次主导高亮）+ 构成摘要 + 覆盖数；恒最新快照不随区间。
// null 时整区显示未参与提示不渲染空图表；型名复用 profile:enneagram.typeN。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ResponsiveContainer,
  XAxis,
  YAxis,
} from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DashboardEnneagram } from '@/lib/contracts';

const MAIN_BAR_COLOR = 'var(--primary)';
const BAR_COLOR = 'var(--muted)';

export function EnneagramPanel(props: {
  data: DashboardEnneagram | null;
}): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { t: tp } = useTranslation('profile');
  const { data } = props;

  const typeName = (type: string) =>
    type >= '1' && type <= '9' ? tp(`enneagram.type${type}`) : type;
  // 契约外值域直接落空串，防 t() 渲染原始键名（与 typeName 同款守卫）
  const traitText = (type: string) =>
    type >= '1' && type <= '9' ? t(`enneagram.trait${type}`) : '';

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('enneagram.title')}</CardTitle>
        <Badge variant="outline">{t('enneagram.snapshot')}</Badge>
      </CardHeader>
      <CardContent>
        {data ? (
          <div className="flex flex-col gap-6 xl:flex-row">
            <div className="h-56 min-w-0 flex-1">
              <EnneagramDistribution
                data={data}
              />
            </div>
            <dl className="w-full space-y-3 text-sm xl:w-80">
              <div>
                <dt className="text-muted-foreground">{t('enneagram.dominantType')}</dt>
                <dd className="font-medium">
                  {typeName(data.dominant_type)}
                  <span className="text-muted-foreground ml-1.5 font-mono text-xs">
                    {data.dominant_ratio.toFixed(1)}%
                  </span>
                </dd>
                <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed">
                  {traitText(data.dominant_type)}
                </p>
              </div>
              <div>
                <dt className="text-muted-foreground">{t('enneagram.secondaryType')}</dt>
                <dd className="font-medium">
                  {typeName(data.secondary_type)}
                  <span className="text-muted-foreground ml-1.5 font-mono text-xs">
                    {data.secondary_ratio.toFixed(1)}%
                  </span>
                </dd>
                <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed">
                  {traitText(data.secondary_type)}
                </p>
              </div>
              <div>
                <dt className="text-muted-foreground">{t('enneagram.coverage')}</dt>
                <dd className="font-medium">
                  {t('enneagram.coverageValue', {
                    count: data.scored_count,
                    ratio: data.coverage_ratio.toFixed(1),
                  })}
                </dd>
              </div>
            </dl>
          </div>
        ) : (
          <div className="flex h-32 items-center justify-center">
            <p className="text-muted-foreground text-sm">{t('enneagram.empty')}</p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

/** 恒 9 项柱图：分布数组转 1-9 全序列，主导/次主导 primary 高亮、其余 muted（照 profile enneagram-panel Cell 形态）。 */
function EnneagramDistribution(props: { data: DashboardEnneagram }): JSX.Element {
  const byType = new Map(props.data.distribution.map((d) => [d.type, d]));
  const items = ['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((k) => ({
    type: k,
    value: byType.get(k)?.ratio ?? 0,
    count: byType.get(k)?.count ?? 0,
  }));
  const highlights = new Set([props.data.dominant_type, props.data.secondary_type]);
  return (
    // BarChart 需包 ResponsiveContainer 提供 width/height，否则不渲染 SVG（同 profile enneagram-panel）
    <ResponsiveContainer width="100%" height="100%">
      <BarChart data={items} margin={{ top: 4, right: 4, left: -18, bottom: 0 }}>
        <CartesianGrid vertical={false} />
        <XAxis dataKey="type" tickLine={false} axisLine={true} tickMargin={8} />
        <YAxis tickLine={false} axisLine={false} tickMargin={4} unit="%" domain={[0, 100]} />
        <Bar dataKey="value" radius={2}>
          {items.map((d) => (
            <Cell
              key={d.type}
              fill={highlights.has(d.type) ? MAIN_BAR_COLOR : BAR_COLOR}
            />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}
