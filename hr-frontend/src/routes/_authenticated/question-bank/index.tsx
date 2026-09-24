import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { fetchQuestionDetail } from '@/features/question-bank/api';
import { QuestionFormDialog } from '@/features/question-bank/components/question-form-dialog';
import { QuestionTable } from '@/features/question-bank/components/question-table';
import { QuestionViewDialog } from '@/features/question-bank/components/question-view-dialog';
import type { QuestionTab } from '@/features/question-bank/types';
import type { QuestionDetail, QuestionListItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

export const Route = createFileRoute('/_authenticated/question-bank/')({
  component: QuestionBankPage,
});

export function QuestionBankPage() {
  const { t } = useTranslation('questionBank');

  // 双 tab 各持一份 QuestionTable 常驻挂载、隐藏非激活 tab，查询状态互不清空（specs §4.1.5）
  const [activeTab, setActiveTab] = useState<QuestionTab>('AI');
  const [viewId, setViewId] = useState<string | null>(null);
  const [editQuestion, setEditQuestion] = useState<QuestionDetail | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  // tab 头计数：未加载（undefined）省略数（specs §4.1.5）
  const [totals, setTotals] = useState<Record<QuestionTab, number | undefined>>({ AI: undefined, SCALE: undefined });

  const onTotalChange = useCallback((tab: QuestionTab, total: number) => {
    setTotals((prev) => (prev[tab] === total ? prev : { ...prev, [tab]: total }));
  }, []);

  function openEdit(detail: QuestionDetail) {
    setEditQuestion(detail);
    setEditOpen(true);
  }

  // 行内编辑：列表项无全量文本与 version，先取详情再开弹窗（编辑弹窗预载详情回填）
  async function openEditFromList(item: QuestionListItem) {
    try {
      openEdit(await fetchQuestionDetail(item.id));
    } catch {
      toast.error(t('toastGeneric'));
    }
  }

  const tabs: { key: QuestionTab; label: string }[] = [
    { key: 'AI', label: t('tabs.ai') },
    { key: 'SCALE', label: t('tabs.scale') },
  ];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('page.title')}</h1>
        <p className="text-muted-foreground text-sm">{t('page.subtitle')}</p>
      </div>

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
            onEdit={(q) => void openEditFromList(q)}
            onView={(q) => setViewId(q.id)}
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
        onSaved={() => setEditQuestion(null)}
      />
    </div>
  );
}
