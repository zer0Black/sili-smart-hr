// 九型人格参考区（specs P2_PRF_001 §4.2.2 E / §4.2.4 规则6）：辅助维度标注参考性。
// enneagram null 整区显示未参与提示不渲染空图表；主型柱高亮、翼型 0 显示「无显著翼型」。
import { useTranslation } from 'react-i18next';
import { Bar, BarChart, CartesianGrid, Cell, XAxis, YAxis } from 'recharts';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { ProfileEnneagram } from '@/lib/contracts';

import { enneagramTypeKey } from '../types';

const MAIN_BAR_COLOR = 'var(--primary)';
const BAR_COLOR = 'var(--muted)';

export function EnneagramPanel(props: { enneagram: ProfileEnneagram | null }): JSX.Element {
  const { t } = useTranslation('profile');
  const { enneagram } = props;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('enneagram.title')}</CardTitle>
        <Badge variant="outline">{t('enneagram.reference')}</Badge>
      </CardHeader>
      <CardContent>
        {enneagram ? (
          <div className="grid grid-cols-[minmax(0,1fr)_240px] items-start gap-6">
            <div className="h-56 w-full">
              <EnneagramDistribution distribution={enneagram.distribution} mainType={enneagram.main_type} />
            </div>
            <dl className="space-y-3 text-sm">
              <div>
                <dt className="text-muted-foreground">{t('enneagram.mainType')}</dt>
                <dd className="font-medium">
                  {enneagramTypeKey[enneagram.main_type]
                    ? t(enneagramTypeKey[enneagram.main_type])
                    : enneagram.main_type}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">{t('enneagram.wingType')}</dt>
                <dd className="font-medium">
                  {enneagram.wing_type === '0'
                    ? t('enneagram.noWing')
                    : enneagramTypeKey[enneagram.wing_type]
                      ? t(enneagramTypeKey[enneagram.wing_type])
                      : enneagram.wing_type}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">{t('enneagram.rationale')}</dt>
                <dd className="leading-relaxed">{enneagram.rationale}</dd>
              </div>
            </dl>
          </div>
        ) : (
          <div className="flex h-32 items-center justify-center">
            <p className="text-muted-foreground text-sm">{t('enneagram.notParticipated')}</p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function EnneagramDistribution(props: {
  distribution: Record<string, number>;
  mainType: string;
}): JSX.Element {
  const data = ['1', '2', '3', '4', '5', '6', '7', '8', '9'].map((k) => ({
    type: k,
    value: props.distribution[k] ?? 0,
  }));
  return (
    <BarChart data={data} margin={{ top: 4, right: 4, left: -18, bottom: 0 }}>
      <CartesianGrid vertical={false} />
      <XAxis dataKey="type" tickLine={false} axisLine={true} tickMargin={8} />
      <YAxis tickLine={false} axisLine={false} tickMargin={4} unit="%" domain={[0, 100]} />
      <Bar dataKey="value" radius={2}>
        {data.map((d) => (
          <Cell key={d.type} fill={d.type === props.mainType ? MAIN_BAR_COLOR : BAR_COLOR} />
        ))}
      </Bar>
    </BarChart>
  );
}
