import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { BatchTable } from '@/features/assessment/components/batch-table';
import { StatsCards } from '@/features/assessment/components/stats-cards';
import { fetchBatchTargets } from '@/features/assessment/api';
import { useBatches } from '@/features/assessment/hooks';
import type { BatchFilter } from '@/features/assessment/types';
import type { BatchListItem, StaffItem } from '@/lib/contracts';

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

  // 轮询推导：存在非停滞的进行中批次才轮询（specs §4.1.3 轮询触发时机）。
  const batchesProbeQ = useBatches(filter);
  const hasActiveRunning = (batchesProbeQ.data?.list ?? []).some(
    (b) => b.status === 'running' && !b.stalled,
  );

  function openCreateDialog(preset: CreateBatchPreset | null) {
    setCreatePreset(preset);
    setCreateOpen(true);
  }

  function openFailureDialog(batchId: string) {
    setFailureBatchId(batchId);
  }

  // 停滞批次重新发起：fetchBatchTargets 拿完整名单与时段，组装 preset 开弹窗（specs §4.1.3）
  async function onReSubmit(batch: BatchListItem) {
    try {
      const targets = await fetchBatchTargets(batch.id);
      openCreateDialog({
        staffs: targets.names.map((n) => ({ staff_id: '', staff_name: n })),
        period_start: targets.period_start,
        period_end: targets.period_end,
      });
    } catch {
      toast.error(t('table.toastGeneric'));
    }
  }

  // 双弹窗状态由 T5/T6 消费，骨架期防未用告警
  void createOpen;
  void createPreset;
  void failureBatchId;
  void onCreateSuccess;

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

      <StatsCards polling={hasActiveRunning} />

      <BatchTable
        onCreateOpen={() => openCreateDialog(null)}
        onFailuresOpen={openFailureDialog}
        onReSubmit={(batch) => void onReSubmit(batch)}
      />

      {/* T5 CreateBatchDialog 接入位：createOpen/createPreset/提交成功回调 onCreateSuccess */}
      {/* T6 FailureDetailDialog 接入位：failureBatchId/关闭/补跑回调 openCreateDialog */}
    </div>
  );
}
