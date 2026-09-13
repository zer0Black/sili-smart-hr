import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useBatches, useBatchPlan, useBatchStats } from '@/features/assessment/hooks';
import type { BatchFilter } from '@/features/assessment/types';
import type { StaffItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_authenticated/assessment/')({
  component: AssessmentCenterPage,
});

const DEFAULT_PAGE_SIZE = 10;

/** 发起评测弹窗预填值：失败明细/重新发起带入人员名单与评估时段（specs §4.2.2）。 */
export interface CreateBatchPreset {
  staffs: StaffItem[];
  period_start: string;
  period_end: string;
}

export function AssessmentCenterPage() {
  const { t } = useTranslation('assessment');

  // 页面状态机：筛选 + 分页 + 两弹窗开关（T5/T6 组件接入时消费）
  const [filter, setFilter] = useState<BatchFilter>({ page: 1, page_size: DEFAULT_PAGE_SIZE });
  const [createOpen, setCreateOpen] = useState(false);
  const [createPreset, setCreatePreset] = useState<CreateBatchPreset | null>(null);
  const [failureBatchId, setFailureBatchId] = useState<string | null>(null);

  // specs §4.2.3 提交成功：关弹窗 + 清筛选回第一页 + toast
  function onCreateSuccess() {
    setCreateOpen(false);
    setCreatePreset(null);
    setFilter({ page: 1, page_size: DEFAULT_PAGE_SIZE });
    toast.success(t('create.toastCreated'));
  }

  // specs §4.1.3 查询/重置：清空条件回第一页
  function onFilterChange(next: Partial<BatchFilter>) {
    setFilter({ ...next, page: 1, page_size: filter.page_size });
  }

  function onPageChange(page: number) {
    setFilter((f) => ({ ...f, page }));
  }

  function openCreateDialog(preset: CreateBatchPreset | null) {
    setCreatePreset(preset);
    setCreateOpen(true);
  }

  function openFailureDialog(batchId: string) {
    setFailureBatchId(batchId);
  }

  // 轮询推导：存在非停滞的进行中批次才轮询（specs §4.1.3 轮询触发时机）。
  // 首帧无数据时 hasActiveRunning=false 不轮询，首批数据到位后按最新结果重估。
  const batchesProbeQ = useBatches(filter);
  const hasActiveRunning = (batchesProbeQ.data?.list ?? []).some(
    (b) => b.status === 'running' && !b.stalled,
  );
  const batchesQ = useBatches(filter, { polling: hasActiveRunning });
  const statsQ = useBatchStats({ polling: hasActiveRunning });
  const planQ = useBatchPlan();

  // 双弹窗状态由 T5/T6 消费，骨架期防未用告警
  void createOpen;
  void createPreset;
  void failureBatchId;
  void onCreateSuccess;
  void openFailureDialog;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      {/* tab 容器：首期仅「AI 使用能力」，F7 叠加另两 tab（specs §4.1.1） */}
      <div role="tablist" className="flex items-center gap-1 border-b">
        <button
          type="button"
          role="tab"
          aria-selected="true"
          className="border-primary -mb-px border-b-2 px-3 py-2 text-sm font-medium"
        >
          {t('tabs.aiUsage')}
        </button>
      </div>

      <StatsCardsSkeleton
        stats={statsQ.data}
        isLoading={statsQ.isLoading}
        isError={statsQ.isError}
        onRefresh={() => void statsQ.refetch()}
      />
      <PlanCardSkeleton plan={planQ.data} isLoading={planQ.isLoading} isError={planQ.isError} onRefresh={() => void planQ.refetch()} />

      {/* T4 BatchTable 接入位：筛选两下拉 + 九列列表 + 分页 + 空态/停滞标识 */}
      <BatchTableSkeleton
        filter={filter}
        page={batchesQ.data}
        isLoading={batchesQ.isLoading}
        isError={batchesQ.isError}
        onFilterChange={onFilterChange}
        onPageChange={onPageChange}
        onCreate={() => openCreateDialog(null)}
        onOpenFailures={openFailureDialog}
      />

      {/* T5 CreateBatchDialog 接入位：createOpen/createPreset/提交成功回调 onCreateSuccess */}
      {/* T6 FailureDetailDialog 接入位：failureBatchId/关闭/补跑回调 openCreateDialog */}
    </div>
  );
}

/** T3 StatsCards 接入前的最小占位：三指标卡 + 加载骨架 + 失败刷新入口。 */
function StatsCardsSkeleton(props: {
  stats?: { eval_count: number; evaluated_person_count: number; running_batch_count: number };
  isLoading: boolean;
  isError: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation('assessment');
  const items = [
    { key: 'evalCount', label: t('stats.evalCount'), value: props.stats?.eval_count },
    { key: 'personCount', label: t('stats.personCount'), value: props.stats?.evaluated_person_count },
    { key: 'runningCount', label: t('stats.runningCount'), value: props.stats?.running_batch_count },
  ];
  return (
    <div className="grid grid-cols-3 gap-4">
      {items.map((item) => (
        <Card key={item.key}>
          <CardHeader>
            <CardTitle className="text-muted-foreground text-sm font-normal">
              {item.label}
            </CardTitle>
          </CardHeader>
          <CardContent>
            {props.isLoading ? (
              <div className="bg-muted h-8 w-16 animate-pulse rounded-md" />
            ) : props.isError ? (
              <Button variant="outline" size="sm" onClick={props.onRefresh}>
                {t('stats.refresh')}
              </Button>
            ) : (
              <p className="text-2xl font-semibold">{item.value ?? '-'}</p>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

/** T3 计划卡占位：四字段（下次执行/周期长度/评估对象/评估维度）。 */
function PlanCardSkeleton(props: {
  plan?: {
    next_trigger_at: string;
    period: 'daily' | 'weekly' | 'monthly';
    target_mode: 'all' | 'specified';
    dimension_base_count: number;
    dimension_upper_count: number;
  };
  isLoading: boolean;
  isError: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation('assessment');
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('plan.title')}</CardTitle>
      </CardHeader>
      <CardContent>
        {props.isLoading ? (
          <div className="bg-muted h-6 w-full animate-pulse rounded-md" />
        ) : props.isError ? (
          <Button variant="outline" size="sm" onClick={props.onRefresh}>
            {t('stats.refresh')}
          </Button>
        ) : props.plan ? (
          <dl className="grid grid-cols-4 gap-4 text-sm">
            <div>
              <dt className="text-muted-foreground">{t('plan.nextTrigger')}</dt>
              <dd className="font-medium">{props.plan.next_trigger_at}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.period')}</dt>
              <dd>{t(`plan.periodValue.${props.plan.period}`)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.target')}</dt>
              <dd>
                {props.plan.target_mode === 'all' ? t('plan.targetAll') : t('plan.targetSpecified')}
              </dd>
            </div>
            <div>
              <dt className="text-muted-foreground">{t('plan.dimensions')}</dt>
              <dd>
                {t('plan.dimensionsValue', {
                  base: props.plan.dimension_base_count,
                  upper: props.plan.dimension_upper_count,
                })}
              </dd>
            </div>
          </dl>
        ) : null}
      </CardContent>
    </Card>
  );
}

/** T4 BatchTable 接入前的最小占位：空态引导 + 进行中/停滞徽标 + 发起评测入口。 */
function BatchTableSkeleton(props: {
  filter: BatchFilter;
  page?: { list: { id: string; batch_no: string; status: string; stalled: boolean }[]; total: number };
  isLoading: boolean;
  isError: boolean;
  onFilterChange: (next: Partial<BatchFilter>) => void;
  onPageChange: (page: number) => void;
  onCreate: () => void;
  onOpenFailures: (batchId: string) => void;
}) {
  const { t } = useTranslation('assessment');
  // 筛选/分页/失败明细回调由 T4 接入时消费，骨架期防未用告警
  void props.filter;
  void props.isError;
  void props.onFilterChange;
  void props.onPageChange;
  void props.onOpenFailures;
  const list = props.page?.list ?? [];
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>{t('table.title')}</CardTitle>
        <Button onClick={props.onCreate}>{t('table.createAction')}</Button>
      </CardHeader>
      <CardContent>
        {props.isLoading ? (
          <div className="bg-muted h-24 w-full animate-pulse rounded-md" />
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-8 text-center text-sm">{t('table.empty')}</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {list.map((b) => (
              <li key={b.id} className="flex items-center gap-2 text-sm">
                <span className="font-mono">{b.batch_no}</span>
                <Badge
                  variant="secondary"
                  className={cn(b.stalled && 'text-muted-foreground')}
                >
                  {t(`table.status.${b.status}`)}
                </Badge>
                {b.stalled && <Badge variant="outline">{t('table.stalled')}</Badge>}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
