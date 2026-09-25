// 批量审核视图（specs §4.2.1-4.2.5 / §4A.2）。页内视图切换的审核态：顶部批次头与按来源
// 区分的审核重点提示，主体逐题卡片流按编号升序整卡全文展示，底部汇总条三数字实时联动。
// 标记状态纯前端（specs §4.2.4 规则1），确认入库一次请求整批提交。
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter } from '@/components/ui/card';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { useBatchQuestions, useConfirmBatch } from '@/features/question-bank/batch-hooks';
import { ErrCode } from '@/lib/contracts';
import type { ReviewQuestionItem } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';
import { cn } from '@/lib/utils';

export interface ReviewViewProps {
  batchId: string;
  /** 返回题库（列表态），已标记时组件内二次确认后调用。 */
  onExit: () => void;
}

/** 驳回原因上限（specs §4.2.2 B：必填 1~500 字）。 */
const REASON_MAX = 500;

/** 生效判定：原因 trim 后 1~500 字才计入驳回集合，空/超长标记不生效（§4.2.5）。 */
function isEffectiveReason(reason: string): boolean {
  const trimmed = reason.trim();
  return trimmed.length >= 1 && trimmed.length <= REASON_MAX;
}

/** 原因输入框错误文案：空与超长分别提示。 */
function reasonError(t: (k: string) => string, reason: string): string | null {
  const trimmed = reason.trim();
  if (trimmed.length === 0) return t('review.reasonRequired');
  if (trimmed.length > REASON_MAX) return t('review.reasonMaxLength');
  return null;
}

export function ReviewView({ batchId, onExit }: ReviewViewProps): JSX.Element {
  const { t } = useTranslation('questionBank');
  const query = useBatchQuestions(batchId);
  const confirmMut = useConfirmBatch();

  // 标记状态纯前端：questionId → 驳回原因（specs §4.2.4 规则1）
  const [marks, setMarks] = useState<Record<string, string>>({});
  // 离开二次确认（specs §4.2.3 返回题库）：存在已生效标记时弹
  const [exitConfirmOpen, setExitConfirmOpen] = useState(false);

  const questions = useMemo(
    () => [...(query.data?.questions ?? [])].sort((a, b) => a.question_no.localeCompare(b.question_no)),
    [query.data],
  );

  // 生效的驳回集合：原因合法的条目才计入（空/超长标记不生效，§4.2.5）
  const effectiveMarks = useMemo(() => {
    const map: Record<string, string> = {};
    for (const [id, reason] of Object.entries(marks)) {
      if (isEffectiveReason(reason)) map[id] = reason.trim();
    }
    return map;
  }, [marks]);

  const rejectedCount = Object.keys(effectiveMarks).length;
  const totalCount = questions.length;
  const admittedCount = totalCount - rejectedCount;

  function onToggleMark(q: ReviewQuestionItem) {
    setMarks((prev) => {
      const next = { ...prev };
      if (next[q.id] !== undefined) {
        // 取消标记清原因（specs §4.2.3）
        delete next[q.id];
      } else {
        next[q.id] = '';
      }
      return next;
    });
  }

  function onReasonChange(id: string, value: string) {
    setMarks((prev) => ({ ...prev, [id]: value.slice(0, REASON_MAX + 100) }));
  }

  function onClearAll() {
    setMarks({});
  }

  function onExitClick() {
    if (rejectedCount > 0) {
      setExitConfirmOpen(true);
      return;
    }
    onExit();
  }

  function onConfirm() {
    confirmMut.mutate(
      {
        batchId,
        rejected: Object.entries(effectiveMarks).map(([question_id, reason]) => ({
          question_id,
          reason,
        })),
      },
      {
        onSuccess: (result) => {
          // 成功 toast 入库与驳回数后切回列表态（specs §4.2.5）
          toast.success(t('review.toastConfirmed', {
            admitted: result.admitted_count,
            rejected: result.rejected_count,
          }));
          onExit();
        },
        onError: (err) => {
          const code = err instanceof ApiError ? err.code : undefined;
          // 1706：批次已被确认/作废，刷新批次卡区与列表后回列表态（specs §4.2.4 规则1）
          if (code === ErrCode.QuestionBatchClosed) {
            toast.error(t('batchStrip.toastClosed'));
            void queryClient.invalidateQueries({ queryKey: ['question-bank'] });
            onExit();
            return;
          }
          toast.error(t('toastGeneric'));
        },
      },
    );
  }

  if (query.isLoading) {
    return <div className="bg-muted h-72 w-full animate-pulse rounded-md" />;
  }

  if (query.isError) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-md border py-16 text-center">
        <span className="text-muted-foreground text-sm">{t('review.loadError')}</span>
        <Button variant="outline" size="sm" onClick={() => void query.refetch()}>
          {t('review.retry')}
        </Button>
      </div>
    );
  }

  const batch = query.data?.batch;

  return (
    <div className="flex flex-col gap-4">
      {/* 头部：返回题库 + 批次标题与来源（specs §4A.2） */}
      <div className="flex items-center gap-3">
        <Button variant="outline" size="sm" onClick={onExitClick}>
          {t('review.backToList')}
        </Button>
        <h2 className="text-base font-semibold">
          {t('review.batchTitle', {
            source: batch ? t(`batchStrip.source.${batch.source}`) : '',
            title: batch?.title ?? '',
            no: batch?.batch_no ?? '',
          })}
        </h2>
        {batch && <Badge variant="outline">{t(`batchStrip.source.${batch.source}`)}</Badge>}
      </div>

      {/* 审核重点提示按来源二选一（specs §4.1.4 规则1） */}
      <div className="border-border bg-muted/40 rounded-md border px-4 py-3 text-sm">
        {batch?.source === 'SCALE' ? t('review.hintScale') : t('review.hintAi')}
      </div>

      {/* 逐题卡片流：整卡全文不截断（specs §4.2.5） */}
      <div className="flex flex-col gap-4">
        {questions.map((q) => {
          const marked = marks[q.id] !== undefined;
          const reason = marks[q.id] ?? '';
          const err = marked ? reasonError(t, reason) : null;
          return (
            <Card key={q.id} className="gap-4 py-5">
              <CardContent className="flex flex-col gap-3 px-6">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-sm font-semibold">{q.question_no}</span>
                  <Badge variant="outline">{q.dimension_name}</Badge>
                  <Badge variant="outline">{t(`table.answerMode.${q.answer_mode}`)}</Badge>
                  {/* 审核状态标签实时切换（specs §4.2.2 A） */}
                  <span
                    className={cn(
                      'text-sm',
                      marked && isEffectiveReason(reason)
                        ? 'text-destructive'
                        : 'text-muted-foreground',
                    )}
                  >
                    {marked && isEffectiveReason(reason)
                      ? t('review.statusRejected')
                      : t('review.statusDefault')}
                  </span>
                </div>
                <div className="flex flex-col gap-2 text-sm">
                  <p className="whitespace-pre-wrap">
                    <span className="text-muted-foreground">{t('review.scenario')}　</span>
                    {q.scenario}
                  </p>
                  <p className="whitespace-pre-wrap">
                    <span className="text-muted-foreground">{t('review.requirement')}　</span>
                    {q.requirement}
                  </p>
                  <p className="whitespace-pre-wrap">
                    <span className="text-muted-foreground">{t('review.focusPoint')}　</span>
                    {q.focus_point}
                  </p>
                </div>
                {/* 标记驳回展开原因输入：必填 1~500 字，空/超长红字提示且标记不生效（specs §4.2.5） */}
                {marked && (
                  <div className="flex flex-col gap-1">
                    <label htmlFor={`review-reason-${q.id}`} className="text-sm font-medium">
                      {t('review.rejectReason')}
                    </label>
                    <textarea
                      id={`review-reason-${q.id}`}
                      value={reason}
                      onChange={(e) => onReasonChange(q.id, e.target.value)}
                      rows={2}
                      aria-label={t('review.rejectReasonAria')}
                      className="border-input bg-background w-full rounded-md border px-3 py-2 text-sm shadow-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
                    />
                    {err && <p className="text-destructive text-sm">{err}</p>}
                  </div>
                )}
              </CardContent>
              <CardFooter className="justify-end px-6">
                {marked ? (
                  <Button variant="outline" size="sm" onClick={() => onToggleMark(q)}>
                    {t('review.unmark')}
                  </Button>
                ) : (
                  <Button variant="outline" size="sm" onClick={() => onToggleMark(q)}>
                    {t('review.markReject')}
                  </Button>
                )}
              </CardFooter>
            </Card>
          );
        })}
      </div>

      {/* 底部汇总条：三数字实时联动，恒等 已标记驳回 + 将默认入库 = 本批总数（specs §4.2.4 规则2） */}
      <div className="border-border bg-muted/40 flex flex-wrap items-center justify-between gap-3 rounded-md border px-4 py-3">
        <div className="text-muted-foreground flex items-center gap-4 text-sm">
          <span>{t('review.totalCount', { count: totalCount })}</span>
          <span className={cn(rejectedCount > 0 && 'text-destructive')}>
            {t('review.rejectedCount', { count: rejectedCount })}
          </span>
          <span>{t('review.admittedCount', { count: admittedCount })}</span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            onClick={onClearAll}
            disabled={rejectedCount === 0 && Object.keys(marks).length === 0}
          >
            {t('review.clearAll')}
          </Button>
          <Button onClick={onConfirm} disabled={confirmMut.isPending}>
            {confirmMut.isPending ? t('review.confirmSubmitting') : t('review.confirm')}
          </Button>
        </div>
      </div>

      {/* 离开二次确认：已做标记不提交不保存（specs §4.2.3 返回题库） */}
      <ConfirmDialog
        open={exitConfirmOpen}
        onOpenChange={(v) => {
          if (!v && !confirmMut.isPending) setExitConfirmOpen(false);
        }}
        title={t('review.exitTitle')}
        desc={t('review.exitDesc')}
        confirmText={t('review.exitConfirm')}
        cancelText={t('cancel', { ns: 'common' })}
        destructive
        onConfirm={() => {
          setExitConfirmOpen(false);
          onExit();
        }}
      />
    </div>
  );
}
