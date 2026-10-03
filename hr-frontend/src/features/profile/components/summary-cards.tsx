// 能力评分卡两块（specs P2_PRF_001 §4.2.2 B）：分数取整+等级、较上期箭头、评估时间、数据状态。
// score null 显示「待评估」不渲染 0 分占位；change_vs_prev null（任一期缺失）不显示箭头。
import { useTranslation } from 'react-i18next';
import { Minus, TrendingDown, TrendingUp } from 'lucide-react';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { ProfileModuleCard } from '@/lib/contracts';
import { cn } from '@/lib/utils';

import { dataStatusKey, roundScore, scoreGrade } from '../status';

function ModuleCard(props: { module: ProfileModuleCard }): JSX.Element {
  const { module } = props;
  const { t } = useTranslation('profile');
  const score = module.score;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-sm font-normal text-muted-foreground">
          {t(module.module === 'AI_USAGE' ? 'module.aiUsage' : 'module.aiMgmt')}
        </CardTitle>
        <Badge variant="secondary">{t(dataStatusKey[module.data_status])}</Badge>
      </CardHeader>
      <CardContent className="flex items-end justify-between">
        {score === null ? (
          <span className="text-2xl font-semibold text-muted-foreground">
            {t('summary.pending')}
          </span>
        ) : (
          <div className="flex items-end gap-2">
            <span className="text-3xl font-semibold">{roundScore(score)}</span>
            <Badge variant="outline">{t(`summary.grade.${scoreGrade(score)}`)}</Badge>
          </div>
        )}
        <dl className="space-y-1 text-right text-sm">
          <div className="flex items-center justify-end gap-1">
            <dt className="text-muted-foreground">{t('summary.changeVsPrev')}</dt>
            <dd aria-label={t('summary.changeVsPrev')}>
              {module.change_vs_prev === null ? (
                <span className="text-muted-foreground">-</span>
              ) : module.change_vs_prev === 0 ? (
                <Minus className="size-4 text-muted-foreground" aria-hidden />
              ) : (
                <span
                  className={cn(
                    'inline-flex items-center gap-0.5 font-medium',
                    module.change_vs_prev > 0 ? 'text-chart-2' : 'text-warning',
                  )}
                >
                  {module.change_vs_prev > 0 ? (
                    <TrendingUp className="size-4" aria-hidden />
                  ) : (
                    <TrendingDown className="size-4" aria-hidden />
                  )}
                  {Math.abs(module.change_vs_prev)}
                </span>
              )}
            </dd>
          </div>
          <div className="flex items-center justify-end gap-1">
            <dt className="text-muted-foreground">{t('summary.evaluatedAt')}</dt>
            <dd>{module.evaluated_at ?? '-'}</dd>
          </div>
        </dl>
      </CardContent>
    </Card>
  );
}

export function SummaryCards(props: { modules: ProfileModuleCard[] }): JSX.Element {
  return (
    <div className="grid grid-cols-2 gap-4">
      {props.modules.map((m) => (
        <ModuleCard key={m.module} module={m} />
      ))}
    </div>
  );
}
