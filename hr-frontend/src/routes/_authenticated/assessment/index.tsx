import { createFileRoute } from '@tanstack/react-router';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { AnswerLinkDialog } from '@/features/assessment/components/answer-link-dialog';
import { BatchTable } from '@/features/assessment/components/batch-table';
import {
  CreateBatchDialog,
  type CreateBatchPreset,
} from '@/features/assessment/components/create-batch-dialog';
import { FailureDetailDialog } from '@/features/assessment/components/failure-detail-dialog';
import { StatsCards } from '@/features/assessment/components/stats-cards';
import { TestTaskTable } from '@/features/assessment/components/test-task-table';
import { fetchBatchTargets } from '@/features/assessment/api';
import { useBatchStats, useRefetchOnVisible } from '@/features/assessment/hooks';
import {
  useResendTestTaskLink,
  useTestTaskPollCounts,
} from '@/features/assessment/test-task-hooks';
import type { TestTaskLinkInfo, TestType } from '@/features/assessment/test-task-types';
import type { BatchListItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_authenticated/assessment/')({
  component: AssessmentCenterPage,
});

/** 页内 tab：aiUsage=F6 对话分析，另两类为主动测试任务（specs §4.1.1）。 */
type PageTab = 'aiUsage' | TestType;

export function AssessmentCenterPage() {
  const { t } = useTranslation('assessment');

  // 页面状态机：弹窗开关 + 预填值（筛选与分页在各表格组件内部自治）
  const [createOpen, setCreateOpen] = useState(false);
  const [createPreset, setCreatePreset] = useState<CreateBatchPreset | null>(null);
  const [failureBatchId, setFailureBatchId] = useState<string | null>(null);
  // specs §4.2.3 提交成功后通知 BatchTable 重置筛选回第一页
  const [tableResetKey, setTableResetKey] = useState(0);
  // 双测试任务 tab 各自的查询重置信号（specs §4.2.3：切 tab 并重置该 tab 查询回第一页）
  const [testResetKeys, setTestResetKeys] = useState<Record<TestType, number>>({
    ai_mgmt: 0,
    enneagram: 0,
  });
  // 作答链接弹窗（specs §4.3）：taskId 驱动打开即拉；linkData 承接重发响应直显新链接
  const [linkTaskId, setLinkTaskId] = useState<string | null>(null);
  const [linkData, setLinkData] = useState<TestTaskLinkInfo | null>(null);

  // 双测试任务 tab 常驻挂载、隐藏非激活 tab，查询状态互不清空（specs §4.1.5）
  const [activeTab, setActiveTab] = useState<PageTab>('aiUsage');

  function onCreateSubmitted() {
    setTableResetKey((k) => k + 1);
  }

  // 发起成功（携类型）：切到对应 tab 并重置该 tab 查询回第一页（specs §4.2.3）
  function onCreateSubmittedType(type: 'conversation' | 'ai_mgmt' | 'enneagram') {
    if (type === 'conversation') {
      setActiveTab('aiUsage');
      onCreateSubmitted();
      return;
    }
    setActiveTab(type);
    setTestResetKeys((keys) => ({ ...keys, [type]: keys[type] + 1 }));
  }

  // 轮询总开关（specs §4.1.3）：stats 自驱动——数据存在进行中批次（已剔除停滞）
  // 时 10s 续轮询，全终态即停。开关喂给统计卡与列表两处，口径与统计卡 running
  // 计数同源，不受翻页/筛选把 running 批次挤出当前页影响。
  const statsQ = useBatchStats();
  const hasActiveRunning = (statsQ.data?.running_batch_count ?? 0) > 0;

  // 主动测试轮询探针（specs §4.1.3 未终态任务轮询）：任一类计数 > 0 即驱动
  // 对应 tab 列表轮询；两 tab 常驻，各自只消费当前类型的计数。
  const testPollQ = useTestTaskPollCounts();
  const testPolling = useMemo(
    () => ({
      ai_mgmt: (testPollQ.data?.ai_mgmt_active ?? 0) > 0,
      enneagram: (testPollQ.data?.enneagram_active ?? 0) > 0,
    }),
    [testPollQ.data],
  );

  // 恢复即拉（specs §4.1.3）：refetchOnWindowFocus 全局关闭，切回标签页时
  // 显式拉一次统计与探针，恢复瞬间数据即时而非等下一个轮询间隔。
  useRefetchOnVisible([statsQ, testPollQ]);

  // 重发作答链接（specs §4.1.3）：成功后直接以作答链接弹窗展示新链接
  const resendMut = useResendTestTaskLink();
  function onResendLink(taskId: string) {
    resendMut.mutate(taskId, {
      onSuccess: (data) => {
        toast.success(t('testTask.linkDialog.resendSuccess'));
        setLinkData(data);
        setLinkTaskId(data.task_id);
      },
      onError: () => {
        toast.error(t('testTask.linkDialog.resendFailed'));
      },
    });
  }

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

  const tabs: { key: PageTab; label: string }[] = [
    { key: 'aiUsage', label: t('tabs.aiUsage') },
    { key: 'ai_mgmt', label: t('tabs.aiMgmt') },
    { key: 'enneagram', label: t('tabs.enneagram') },
  ];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      {/* tab 容器：AI 使用能力为 F6 既有，另两 tab 为主动测试任务（specs §4.1.1） */}
      <div role="tablist" className="flex items-center gap-1 border-b">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type="button"
            role="tab"
            aria-selected={activeTab === tab.key}
            onClick={() => setActiveTab(tab.key)}
            className={cn(
              '-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors',
              activeTab === tab.key
                ? 'border-primary text-primary'
                : 'text-muted-foreground hover:text-foreground border-transparent',
            )}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* 两测试任务 tab 常驻挂载，隐藏非激活 tab 保查询状态（specs §4.1.5 切 tab 不互相污染） */}
      {(['ai_mgmt', 'enneagram'] as const).map((type) => (
        <div key={type} className={activeTab === type ? 'contents' : 'hidden'}>
          <TestTaskTable
            testType={type}
            onCreateOpen={() => openCreateDialog(null)}
            onLinkOpen={(taskId) => {
              // 打开即拉快照（specs §4.3.5）：清注入数据防上次重发链接串场
              setLinkData(null);
              setLinkTaskId(taskId);
            }}
            onResend={(taskId) => onResendLink(taskId)}
            resendPending={resendMut.isPending}
            resetKey={testResetKeys[type]}
            polling={testPolling[type]}
          />
        </div>
      ))}

      {activeTab === 'aiUsage' && (
        <>
          <StatsCards />

          <BatchTable
            onCreateOpen={() => openCreateDialog(null)}
            onFailuresOpen={setFailureBatchId}
            onReSubmit={(batch) => void onReSubmit(batch)}
            resetKey={tableResetKey}
            polling={statsQ.isLoading || hasActiveRunning}
          />
        </>
      )}

      <CreateBatchDialog
        open={createOpen}
        preset={createPreset}
        onClose={() => {
          setCreateOpen(false);
          setCreatePreset(null);
        }}
        onSubmitted={onCreateSubmitted}
        onSubmittedType={onCreateSubmittedType}
      />
      <AnswerLinkDialog
        open={linkTaskId !== null}
        taskId={linkTaskId}
        linkData={linkData}
        onClose={() => {
          setLinkTaskId(null);
          setLinkData(null);
        }}
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
