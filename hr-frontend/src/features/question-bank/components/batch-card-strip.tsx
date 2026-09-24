// 待审核批次卡区（specs §4.1.2 C / §4.1.3 / §4.1.5）。横排卡片流，无待审核批次时
// 渲染 null 由页面隐藏该区；「作废」destructive 二次确认，「开始审核」交父层切视图。
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import type { JSX } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardFooter } from '@/components/ui/card';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { usePendingBatches, useVoidBatch } from '@/features/question-bank/batch-hooks';
import { ErrCode } from '@/lib/contracts';
import type { QuestionBatchCard } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export interface BatchCardStripProps {
  onStartReview: (batchId: string) => void;
}

// RFC3339 生成时间按 yyyy-MM-dd HH:mm:ss 本地格式化，非法输入兜底透传原值
// （question-view-dialog formatTime 同款）。
function formatTime(raw: string): string {
  return dayjs(raw).isValid() ? dayjs(raw).format('YYYY-MM-DD HH:mm:ss') : raw;
}

export function BatchCardStrip({ onStartReview }: BatchCardStripProps): JSX.Element | null {
  const { t } = useTranslation('questionBank');
  const query = usePendingBatches();
  const voidMut = useVoidBatch();
  // 作废二次确认目标：null 为关、非 null 为待作废批次卡
  const [voidTarget, setVoidTarget] = useState<QuestionBatchCard | null>(null);

  const list = query.data?.list ?? [];
  // 无待审核批次整体隐藏（specs §4.1.5，含加载期未返回数据）
  if (list.length === 0) return null;

  function onVoid() {
    const batch = voidTarget;
    if (!batch) return;
    voidMut.mutate(batch.id, {
      onSuccess: () => {
        toast.success(t('batchStrip.toastVoided'));
        setVoidTarget(null);
      },
      onError: (err) => {
        // 1706：批次已被确认/作废，刷新批次卡区与题目列表（specs §4.1.3）
        if (err instanceof ApiError && err.code === ErrCode.QuestionBatchClosed) {
          toast.error(t('batchStrip.toastClosed'));
          void queryClient.invalidateQueries({ queryKey: ['question-bank'] });
          setVoidTarget(null);
          return;
        }
        toast.error(t('toastGeneric'));
      },
    });
  }

  return (
    <div className="flex flex-col gap-2">
      <span className="text-sm font-medium">{t('batchStrip.title')}</span>
      {/* 批次卡横排，超出宽度横向滚动（specs §4.1.5） */}
      <div className="flex gap-4 overflow-x-auto pb-1">
        {list.map((batch) => (
          <Card key={batch.id} className="w-72 shrink-0 gap-3 py-4">
            <CardContent className="flex flex-col gap-2">
              <div className="flex items-center justify-between gap-2">
                <span className="font-mono text-sm font-medium">{batch.batch_no}</span>
                {/* 来源标签（specs §4.1.2 C） */}
                <Badge variant="outline">{t(`batchStrip.source.${batch.source}`)}</Badge>
              </div>
              <span className="text-sm font-medium break-words">{batch.title}</span>
              <div className="text-muted-foreground flex items-center gap-3 text-sm">
                <span>{t('batchStrip.questionCount', { count: batch.question_count })}</span>
                <span>{formatTime(batch.created_at)}</span>
              </div>
            </CardContent>
            <CardFooter className="justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={voidMut.isPending}
                onClick={() => setVoidTarget(batch)}
              >
                {t('batchStrip.actionVoid')}
              </Button>
              <Button size="sm" onClick={() => onStartReview(batch.id)}>
                {t('batchStrip.actionReview')}
              </Button>
            </CardFooter>
          </Card>
        ))}
      </div>

      {/* 作废二次确认（specs §4.1.3 / 通用规范 14）：destructive，成功或 1706 后关闭 */}
      <ConfirmDialog
        open={voidTarget !== null}
        onOpenChange={(v) => {
          if (!v && !voidMut.isPending) setVoidTarget(null);
        }}
        title={t('batchStrip.voidTitle')}
        desc={t('batchStrip.voidDesc', { no: voidTarget?.batch_no ?? '' })}
        confirmText={t('batchStrip.actionVoid')}
        cancelText={t('cancel', { ns: 'common' })}
        submittingText={t('batchStrip.voidSubmitting')}
        submitting={voidMut.isPending}
        destructive
        onConfirm={onVoid}
      />
    </div>
  );
}
