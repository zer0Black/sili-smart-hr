// 量表引入两步弹窗（specs §4.1.2 F / §4.1.3 / §4A.4）。第一步候选卡单选（手写 button 组，
// 已引入置灰不可选），第二步只读汇总（批次号 #S+当日 MMdd 预览）；确认引入成功后按钮区切
// 「稍后审核」（关弹窗留列表态）/「进入审核」（携批次 ID 切审核视图）。
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import type { JSX, ReactNode } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useImportScale, useScales } from '@/features/question-bank/scale-hooks';
import { ErrCode } from '@/lib/contracts';
import type { ImportScaleResult } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { cn } from '@/lib/utils';

export interface ScaleImportDialogProps {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  /** 「进入审核」携批次 ID 切审核视图（specs §4.1.3）。 */
  onEnterReview: (batchId: string) => void;
}

function SummaryRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[100px_1fr] gap-x-3 text-sm">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="min-w-0 break-words">{children}</span>
    </div>
  );
}

export function ScaleImportDialog({ open, onOpenChange, onEnterReview }: ScaleImportDialogProps): JSX.Element {
  const { t } = useTranslation('questionBank');
  const scalesQ = useScales(open);
  const importMut = useImportScale();

  // 组件内两步切换：0=选择量表，1=确认信息（specs §4A.4，不走路由）
  const [step, setStep] = useState(0);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  // 引入成功结果：非 null 时按钮区切「稍后审核 / 进入审核」
  const [result, setResult] = useState<ImportScaleResult | null>(null);

  const list = scalesQ.data?.list ?? [];
  const selected = list.find((s) => s.scale_key === selectedKey) ?? null;

  // 关闭即复位（含重新打开场景）：回到第一步、清空选中与成功态
  useEffect(() => {
    if (!open) {
      setStep(0);
      setSelectedKey(null);
      setResult(null);
    }
  }, [open]);

  function onConfirm() {
    if (!selectedKey) return;
    importMut.mutate(selectedKey, {
      onSuccess: (res) => {
        setResult(res);
        toast.success(t('scaleImport.toastImported', { no: res.batch_no }));
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        // 量表重复引入（1704）：单独提示并刷新弹窗置灰状态（specs §4.1.4 规则9）
        if (code === ErrCode.ScaleAlreadyImported) {
          toast.error(t('scaleImport.toastImportedError'));
          void scalesQ.refetch();
          return;
        }
        toast.error(t('toastGeneric'));
      },
    });
  }

  function onLaterReview() {
    onOpenChange(false);
  }

  function onEnter() {
    if (result) onEnterReview(result.batch_id);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{t('scaleImport.title')}</DialogTitle>
        </DialogHeader>

        {step === 0 ? (
          <>
            <p className="text-muted-foreground text-sm">{t('scaleImport.intro')}</p>
            {scalesQ.isLoading ? (
              <div className="text-muted-foreground py-8 text-center text-sm">
                {t('loading', { ns: 'common' })}
              </div>
            ) : scalesQ.isError ? (
              <div className="flex flex-col items-center gap-2 py-8">
                <span className="text-muted-foreground text-sm">{t('scaleImport.loadError')}</span>
                <Button variant="outline" size="sm" onClick={() => void scalesQ.refetch()}>
                  {t('scaleImport.retry')}
                </Button>
              </div>
            ) : (
              /* 候选卡单选：手写 button 组（specs §4.1.2 F），选中态 border-primary + bg-accent */
              <div className="grid grid-cols-2 gap-3">
                {list.map((s) => (
                  <button
                    key={s.scale_key}
                    type="button"
                    disabled={s.imported}
                    aria-pressed={selectedKey === s.scale_key}
                    onClick={() => setSelectedKey(s.scale_key)}
                    className={cn(
                      'flex flex-col items-start gap-1.5 rounded-lg border p-4 text-left transition-colors',
                      'disabled:pointer-events-none disabled:opacity-50',
                      selectedKey === s.scale_key
                        ? 'border-primary bg-accent'
                        : 'hover:bg-accent/50',
                    )}
                  >
                    <span className="flex w-full items-center justify-between gap-2">
                      <span className="text-sm font-medium break-words">{s.name}</span>
                      {/* 已引入角标：置灰不可再选（specs §4.1.4 规则8） */}
                      {s.imported && <Badge variant="secondary">{t('scaleImport.imported')}</Badge>}
                    </span>
                    <span className="text-muted-foreground text-xs">
                      {t('scaleImport.cardMeta', {
                        count: s.question_count,
                        minutes: s.estimated_minutes,
                      })}
                    </span>
                    <span className="text-muted-foreground text-xs">{s.description}</span>
                  </button>
                ))}
              </div>
            )}
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t('cancel', { ns: 'common' })}
              </Button>
              <Button disabled={!selectedKey} onClick={() => setStep(1)}>
                {t('scaleImport.next')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            {/* 第二步只读汇总（specs §4.1.2 F / §4A.4），批次号 #S+当日 MMdd 预览 */}
            <div className="flex flex-col gap-3">
              <SummaryRow label={t('scaleImport.sumSource')}>{selected?.name}</SummaryRow>
              <SummaryRow label={t('scaleImport.sumCount')}>
                {t('scaleImport.countText', { count: selected?.question_count ?? 0 })}
              </SummaryRow>
              <SummaryRow label={t('scaleImport.sumScoring')}>{t('scaleImport.sumScoringValue')}</SummaryRow>
              <SummaryRow label={t('scaleImport.sumDuration')}>
                {t('scaleImport.durationText', { minutes: selected?.estimated_minutes ?? 0 })}
              </SummaryRow>
              <SummaryRow label={t('scaleImport.sumBatch')}>{t('scaleImport.sumBatchValue')}</SummaryRow>
            </div>
            {result ? (
              /* 成功后展示实际批次号（specs §4.1.3） */
              <p className="text-muted-foreground text-sm">
                {t('scaleImport.batchDone', { no: result.batch_no })}
              </p>
            ) : (
              <p className="text-muted-foreground text-sm">
                {t('scaleImport.batchPreview', { no: `#S${dayjs().format('MMDD')}` })}
              </p>
            )}
            <DialogFooter>
              {result ? (
                <>
                  <Button variant="outline" onClick={onLaterReview}>
                    {t('scaleImport.laterReview')}
                  </Button>
                  <Button onClick={onEnter}>{t('scaleImport.enterReview')}</Button>
                </>
              ) : (
                <>
                  <Button variant="outline" onClick={() => onOpenChange(false)} disabled={importMut.isPending}>
                    {t('cancel', { ns: 'common' })}
                  </Button>
                  <Button variant="outline" onClick={() => setStep(0)} disabled={importMut.isPending}>
                    {t('scaleImport.prev')}
                  </Button>
                  <Button onClick={onConfirm} disabled={importMut.isPending}>
                    {importMut.isPending
                      ? t('scaleImport.submitting')
                      : t('scaleImport.confirmImport')}
                  </Button>
                </>
              )}
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
