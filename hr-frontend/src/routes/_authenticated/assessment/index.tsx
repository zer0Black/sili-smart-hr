import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { BatchTable } from '@/features/assessment/components/batch-table';
import {
  CreateBatchDialog,
  type CreateBatchPreset,
} from '@/features/assessment/components/create-batch-dialog';
import { FailureDetailDialog } from '@/features/assessment/components/failure-detail-dialog';
import { StatsCards } from '@/features/assessment/components/stats-cards';
import { fetchBatchTargets } from '@/features/assessment/api';
import { useBatches } from '@/features/assessment/hooks';
import type { BatchListItem } from '@/lib/contracts';

export const Route = createFileRoute('/_authenticated/assessment/')({
  component: AssessmentCenterPage,
});

const DEFAULT_PAGE_SIZE = 10;

export function AssessmentCenterPage() {
  const { t } = useTranslation('assessment');

  // 页面状态机：两弹窗开关 + 预填值（筛选与分页在 BatchTable 内部自治）
  const [createOpen, setCreateOpen] = useState(false);
  const [createPreset, setCreatePreset] = useState<CreateBatchPreset | null>(null);
  const [failureBatchId, setFailureBatchId] = useState<string | null>(null);
  // specs §4.2.3 提交成功后通知 BatchTable 重置筛选回第一页
  const [tableResetKey, setTableResetKey] = useState(0);

  // specs §4.2.3 提交成功：toast 提示 + 弹窗自身 onClose 关闭 + BatchTable 清筛选回第一页
  function onCreateSubmitted() {
    toast.success(t('create.toastCreated'));
    setTableResetKey((k) => k + 1);
  }

  // 轮询推导：存在非停滞的进行中批次才轮询（specs §4.1.3 轮询触发时机）。
  const batchesProbeQ = useBatches({ page: 1, page_size: DEFAULT_PAGE_SIZE });
  const hasActiveRunning = (batchesProbeQ.data?.list ?? []).some(
    (b) => b.status === 'running' && !b.stalled,
  );

  function openCreateDialog(preset: CreateBatchPreset | null) {
    setCreatePreset(preset);
    setCreateOpen(true);
  }

  // 停滞批次重新发起：fetchBatchTargets 拿完整名单与时段，组装 preset 开弹窗（specs §4.1.3）
  async function onReSubmit(batch: BatchListItem) {
    try {
      const targets = await fetchBatchTargets(batch.id);
      openCreateDialog({
        staffs: targets.names.map((n) => ({ staff_id: '', staff_name: n })),
        period: { start: targets.period_start, end: targets.period_end },
      });
    } catch {
      toast.error(t('table.toastGeneric'));
    }
  }

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
        onFailuresOpen={setFailureBatchId}
        onReSubmit={(batch) => void onReSubmit(batch)}
        resetKey={tableResetKey}
      />

      <CreateBatchDialog
        open={createOpen}
        preset={createPreset}
        onClose={() => {
          setCreateOpen(false);
          setCreatePreset(null);
        }}
        onSubmitted={onCreateSubmitted}
      />
      <FailureDetailDialog
        batchId={failureBatchId}
        onClose={() => setFailureBatchId(null)}
        onReSubmitFailed={(preset) => {
          setCreatePreset(preset);
          setFailureBatchId(null);
          setCreateOpen(true);
        }}
      />
    </div>
  );
}
