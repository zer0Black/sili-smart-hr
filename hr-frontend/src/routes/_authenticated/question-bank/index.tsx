import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { BatchCardStrip } from '@/features/question-bank/components/batch-card-strip';
import { QuestionFormDialog } from '@/features/question-bank/components/question-form-dialog';
import { QuestionTable } from '@/features/question-bank/components/question-table';
import { QuestionViewDialog } from '@/features/question-bank/components/question-view-dialog';
import { ReviewView } from '@/features/question-bank/components/review-view';
import { fetchQuestionDetail } from '@/features/question-bank/api';
import type { QuestionTab } from '@/features/question-bank/types';
import type { QuestionDetail, QuestionListItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_authenticated/question-bank/')({
  component: QuestionBankPage,
});

export function QuestionBankPage() {
  const { t } = useTranslation('questionBank');

  // 页内视图切换：list=列表态，review=批量审核态（specs §4.2.1，非独立路由）
  const [viewMode, setViewMode] = useState<'list' | 'review'>('list');
  const [activeBatchId, setActiveBatchId] = useState<string | null>(null);

  // 双 tab 各持一份 QuestionTable 常驻挂载、隐藏非激活 tab，查询状态互不清空（specs §4.1.5）
  const [activeTab, setActiveTab] = useState<QuestionTab>('AI');
  const [viewId, setViewId] = useState<string | null>(null);
  const [editQuestion, setEditQuestion] = useState<QuestionDetail | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  // 编辑弹窗模式：edit=行内编辑，resubmit=驳回题修正重新送审（specs §4.1.4 规则6）
  const [formMode, setFormMode] = useState<'edit' | 'resubmit'>('edit');
  // tab 头计数：未加载（undefined）省略数（specs §4.1.5）
  const [totals, setTotals] = useState<Record<QuestionTab, number | undefined>>({ AI: undefined, SCALE: undefined });

  const onTotalChange = useCallback((tab: QuestionTab, total: number) => {
    setTotals((prev) => (prev[tab] === total ? prev : { ...prev, [tab]: total }));
  }, []);

  function openEdit(detail: QuestionDetail) {
    setFormMode('edit');
    setEditQuestion(detail);
    setEditOpen(true);
  }

  function openResubmit(detail: QuestionDetail) {
    setFormMode('resubmit');
    setEditQuestion(detail);
    setEditOpen(true);
  }

  // 行内编辑/重新提交：列表项无全量文本与 version，先取详情再开弹窗（弹窗预载详情回填）
  async function openFormFromList(item: QuestionListItem, mode: 'edit' | 'resubmit') {
    try {
      const detail = await fetchQuestionDetail(item.id);
      if (mode === 'resubmit') openResubmit(detail);
      else openEdit(detail);
    } catch {
      toast.error(t('toastGeneric'));
    }
  }

  function exitReview() {
    setViewMode('list');
    setActiveBatchId(null);
  }

  const tabs: { key: QuestionTab; label: string }[] = [
    { key: 'AI', label: t('tabs.ai') },
    { key: 'SCALE', label: t('tabs.scale') },
  ];

  // 审核态：列表区整体替换为审核卡片流（specs §4.2.1）
  if (viewMode === 'review' && activeBatchId !== null) {
    return (
      <div className="flex flex-col gap-6">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
          <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
        </div>
        <ReviewView batchId={activeBatchId} onExit={exitReview} />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

      {/* 待审核批次卡区（specs §4.1.2 C）：无批次时组件返回 null 整体隐藏 */}
      <BatchCardStrip
        onStartReview={(batchId) => {
          setActiveBatchId(batchId);
          setViewMode('review');
        }}
      />

      {/* 页面级工具栏：两入口 SP3/SP4 前禁用占位（specs §4.1.1 / §4.1.3） */}
      <div className="flex items-center justify-end gap-2">
        <Button disabled title={t('toolbar.pendingHint')}>
          {t('toolbar.generateAction')}
        </Button>
        <Button disabled title={t('toolbar.pendingHint')}>
          {t('toolbar.importAction')}
        </Button>
      </div>

      {/* 双 tab（specs §4.1.1 / §4.1.5）：查询条件、页码与每页条数独立保持；头显「共 N 题」 */}
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
            {totals[tab.key] !== undefined && (
              <span className="text-muted-foreground">（{t('tabs.count', { count: totals[tab.key] })}）</span>
            )}
          </button>
        ))}
      </div>

      {/* 两 tab 实例常驻挂载，隐藏非激活 tab 保查询状态（specs §4.1.5 切 tab 不互相清空） */}
      {(['AI', 'SCALE'] as const).map((tab) => (
        <div key={tab} className={activeTab === tab ? 'contents' : 'hidden'}>
          <QuestionTable
            tab={tab}
            onEdit={(q) => void openFormFromList(q, 'edit')}
            onView={(q) => setViewId(q.id)}
            onResubmit={(q) => void openFormFromList(q, 'resubmit')}
            onTotalChange={onTotalChange}
          />
        </div>
      ))}

      <QuestionViewDialog
        open={viewId !== null}
        questionId={viewId}
        onOpenChange={(o) => {
          if (!o) setViewId(null);
        }}
        onEdit={openEdit}
      />
      <QuestionFormDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        question={editQuestion}
        mode={formMode}
        onSaved={() => setEditQuestion(null)}
      />
    </div>
  );
}
