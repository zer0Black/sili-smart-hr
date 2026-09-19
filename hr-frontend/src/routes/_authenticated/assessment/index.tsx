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
import { useBatchStats, useRefetchOnVisible } from '@/features/assessment/hooks';
import type { BatchListItem } from '@/lib/contracts';

export const Route = createFileRoute('/_authenticated/assessment/')({
  component: AssessmentCenterPage,
});

export function AssessmentCenterPage() {
  const { t } = useTranslation('assessment');

  // 页面状态机：两弹窗开关 + 预填值（筛选与分页在 BatchTable 内部自治）
  const [createOpen, setCreateOpen] = useState(false);
  const [createPreset, setCreatePreset] = useState<CreateBatchPreset | null>(null);
  const [failureBatchId, setFailureBatchId] = useState<string | null>(null);
  // specs §4.2.3 提交成功后通知 BatchTable 重置筛选回第一页
  const [tableResetKey, setTableResetKey] = useState(0);

  function onCreateSubmitted() {
    setTableResetKey((k) => k + 1);
  }

  // 轮询总开关（specs §4.1.3）：stats 自驱动——数据存在进行中批次（已剔除停滞）
  // 时 10s 续轮询，全终态即停。开关喂给统计卡与列表两处，口径与统计卡 running
  // 计数同源，不受翻页/筛选把 running 批次挤出当前页影响。
  const statsQ = useBatchStats();
  const hasActiveRunning = (statsQ.data?.running_batch_count ?? 0) > 0;

  // 首屏 stats 未返回前保守开轮询一轮拉到状态（拉到后按数据自收敛）。
  const polling = statsQ.isLoading || hasActiveRunning;

  // 恢复即拉（specs §4.1.3）：refetchOnWindowFocus 全局关闭，切回标签页时
  // 显式拉一次统计与列表，恢复瞬间数据即时而非等下一个轮询间隔。
  useRefetchOnVisible([statsQ]);

  function openCreateDialog(preset: CreateBatchPreset | null) {
    setCreatePreset(preset);
    setCreateOpen(true);
  }

  // 停滞批次重新发起：fetchBatchTargets 拿完整名单与时段，组装 preset 开弹窗
  //（specs §4.1.3 预填完整名单与时段）。all 模式批次预填全员（staffs 空），
  // 弹窗按 mode='all' 回填，用户可一键按全员补跑。
  async function onReSubmit(batch: BatchListItem) {
    try {
      const targets = await fetchBatchTargets(batch.id);
      openCreateDialog({
        mode: targets.target_mode,
        staffs: targets.names.map((n) => ({ staff_name: n })),
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

      <StatsCards />

      <BatchTable
        onCreateOpen={() => openCreateDialog(null)}
        onFailuresOpen={setFailureBatchId}
        onReSubmit={(batch) => void onReSubmit(batch)}
        resetKey={tableResetKey}
        polling={polling}
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
