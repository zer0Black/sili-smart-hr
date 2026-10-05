// 团队培训方向建议（specs P2_TMD_001 §4.1.2 E / §4.1.4 规则6）：
// status=generated 渲染两模块建议 + summary + 区间/生成时间标注；其余三态统一提示文案。
// 建议恒最新快照不随区间回看（数据层承载，组件无区间感知）。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DashboardSuggestion } from '@/lib/contracts';

export function SuggestionPanel(props: { data: DashboardSuggestion }): JSX.Element {
  const { t } = useTranslation('dashboard');
  const { data } = props;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('suggestion.title')}</CardTitle>
      </CardHeader>
      <CardContent>
        {data.status === 'generated' ? (
          <div className="space-y-6">
            <p className="text-muted-foreground text-xs">
              {t('suggestion.period', {
                start: data.period_start,
                end: data.period_end,
              })}
              <span className="ml-3">{t('suggestion.generatedAt', { time: data.generated_at })}</span>
            </p>
            {data.modules.map((m) => (
              <section key={m.module} className="space-y-2">
                <h3 className="text-muted-foreground text-xs font-medium uppercase tracking-wide">
                  {t(`module.${m.module}`)}
                </h3>
                <ul className="space-y-2">
                  {m.suggestions.map((s, i) => (
                    <li key={i} className="rounded-lg border px-4 py-3">
                      <p className="text-sm font-medium">{s.name}</p>
                      <p className="text-muted-foreground mt-1 text-xs leading-relaxed">
                        {s.description}
                      </p>
                    </li>
                  ))}
                </ul>
              </section>
            ))}
            {data.summary ? (
              <section className="space-y-1">
                <h3 className="text-muted-foreground text-xs font-medium uppercase tracking-wide">
                  {t('suggestion.summaryTitle')}
                </h3>
                <p className="text-sm leading-relaxed">{data.summary}</p>
              </section>
            ) : null}
          </div>
        ) : (
          <div className="flex h-24 items-center justify-center">
            <p className="text-muted-foreground text-sm">{t('suggestion.pending')}</p>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
