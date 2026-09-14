import { useNavigate } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';
import type { UseQueryResult } from '@tanstack/react-query';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useBatchPlan, useBatchStats } from '@/features/assessment/hooks';
import { targetSummaryText } from '@/features/assessment/target-summary';
import type { BatchPlan, BatchStats } from '@/lib/contracts';

/** 态势统计卡 + 跑批计划卡组合入口（specs §4.1.2 A/B）。 */
export function StatsCards(props?: { polling?: boolean }): JSX.Element {
  const statsQ = useBatchStats({ polling: props?.polling });
  const planQ = useBatchPlan();
  return (
    <>
      <StatsCard query={statsQ} />
      <PlanCard query={planQ} />
    </>
  );
}

function StatBlock(props: { label: string; value?: number; isLoading: boolean; isError: boolean; onRefresh: () => void }) {
  const { t } = useTranslation('assessment');
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-muted-foreground text-sm font-normal">{props.label}</CardTitle>
      </CardHeader>
      <CardContent>
        {props.isLoading ? (
          <div className="bg-muted h-8 w-16 animate-pulse rounded-md" />
        ) : props.isError ? (
          <Button variant="outline" size="sm" onClick={props.onRefresh}>
            {t('stats.refresh')}
          </Button>
        ) : (
          <p className="text-2xl font-semibold">{props.value ?? '-'}</p>
        )}
      </CardContent>
    </Card>
  );
}

function StatsCard({ query }: { query: UseQueryResult<BatchStats> }) {
  const { t } = useTranslation('assessment');
  const onRefresh = () => void query.refetch();
  return (
    <div className="grid grid-cols-3 gap-4">
      <StatBlock
        label={t('stats.evalCount')}
        value={query.data?.eval_count}
        isLoading={query.isLoading}
        isError={query.isError}
        onRefresh={onRefresh}
      />
      <StatBlock
        label={t('stats.personCount')}
        value={query.data?.evaluated_person_count}
        isLoading={query.isLoading}
        isError={query.isError}
        onRefresh={onRefresh}
      />
      <StatBlock
        label={t('stats.runningCount')}
        value={query.data?.running_batch_count}
        isLoading={query.isLoading}
        isError={query.isError}
        onRefresh={onRefresh}
      />
    </div>
  );
}

/** 评估对象摘要（specs §4.1.2B/§4.1.5）：与批次列表共用 targetSummaryText 口径，
 * 计数取 target_count（all 模式上游计数的降级 0 不影响展示）。 */
function PlanCard({ query }: { query: UseQueryResult<BatchPlan> }) {
  const { t } = useTranslation('assessment');
  const navigate = useNavigate();
  const plan = query.data;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('plan.title')}</CardTitle>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => navigate({ to: '/system/params' })}>
            {t('plan.goPeriod')}
          </Button>
          <Button variant="outline" size="sm" onClick={() => navigate({ to: '/system/dimension' })}>
            {t('plan.goDimension')}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {query.isLoading ? (
          <div className="grid grid-cols-4 gap-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="bg-muted h-6 w-full animate-pulse rounded-md" />
            ))}
          </div>
        ) : query.isError ? (
          <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
            {t('stats.refresh')}
          </Button>
        ) : plan ? (
          <dl className="grid grid-cols-4 gap-4 text-sm">
            <div>
              <dt className="text-muted-foreground">{t('plan.nextTrigger')}</dt>
              <dd className="text-primary font-medium">{plan.next_trigger_at}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.period')}</dt>
              <dd>{t(`plan.periodValue.${plan.period}`)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.target')}</dt>
              <dd title={plan.target_names.join('、')}>
                {targetSummaryText(plan, t)}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.dimensions')}</dt>
              <dd>
                {t('plan.dimensionsValue', {
                  base: plan.dimension_base_count,
                  upper: plan.dimension_upper_count,
                })}
              </dd>
            </div>
          </dl>
        ) : null}
      </CardContent>
    </Card>
  );
}
