// 失败明细弹窗（specs §4.3）：只读快照清单 + 按失败对象重新发起补跑入口。
// 清单按响应序直接渲染，不引入分页与筛选（§4.3.5）。
import { useTranslation } from 'react-i18next';
import type { JSX } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { CreateBatchPreset } from '@/features/assessment/components/create-batch-dialog';
import { useBatchFailures } from '@/features/assessment/hooks';

export interface FailureDetailDialogProps {
  batchId: string | null;
  onClose: () => void;
  /** 按失败对象重新发起：把响应时段与失败人员名单组装成预填值交给上层（BR5 §4.3.3）。 */
  onReSubmitFailed: (preset: CreateBatchPreset) => void;
}

export function FailureDetailDialog({
  batchId,
  onClose,
  onReSubmitFailed,
}: FailureDetailDialogProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const q = useBatchFailures(batchId);
  const data = q.data;

  function handleReSubmit() {
    if (!data) return;
    onReSubmitFailed({
      staffs: data.list.map((it) => ({ staff_id: '', staff_name: it.token_name })),
      period: { start: data.period_start, end: data.period_end },
    });
  }

  return (
    <Dialog open={batchId !== null} onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {t('failures.title', { batchNo: data?.batch_no ?? '' })}
          </DialogTitle>
        </DialogHeader>

        {q.isLoading ? (
          <p className="text-muted-foreground py-8 text-center text-sm">
            {t('failures.loading')}
          </p>
        ) : q.isError ? (
          // 加载失败弹窗内失败态 + 重试，不关闭弹窗（BR7 §4.3.5）
          <div className="flex flex-col items-center gap-2 py-8">
            <span className="text-muted-foreground text-sm">{t('failures.loadError')}</span>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void q.refetch()}
            >
              {t('failures.retry')}
            </Button>
          </div>
        ) : data ? (
          <div className="flex flex-col gap-2">
            <p className="text-muted-foreground text-sm">
              {t('failures.summary', { failed: data.failed_count, total: data.total_count })}
            </p>
            <div className="max-h-72 overflow-y-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('failures.colName')}</TableHead>
                    <TableHead>{t('failures.colReason')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.list.map((it) => (
                    <TableRow key={it.token_name}>
                      <TableCell>{it.token_name}</TableCell>
                      <TableCell>
                        <span className="block max-w-72 truncate" title={it.error_summary}>
                          {it.error_summary}
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </div>
        ) : null}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('failures.close')}
          </Button>
          {data && !q.isError && (
            <Button type="button" onClick={handleReSubmit}>
              {t('failures.restartByFailures')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
