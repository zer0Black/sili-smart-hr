// 发起评测弹窗（specs §4.2）：三类型卡片 + 评估对象多选 + 评估时段 + 提交/取消闭环。
// 首期仅对话分析可选，另两类型置灰点击 toast 提示后续开放（BR1 §4.2.2）。
// 取消二次确认仅在评估对象非空时触发；提交进行中禁止关闭（BR4 §4.2.3）。
import { useEffect, useMemo, useRef, useState } from 'react';
import type { JSX } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import dayjs from 'dayjs';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  StaffMultiSelect,
  type StaffSelectValue,
} from '@/features/assessment/components/staff-multi-select';
import { useBatchPlan, useCreateBatch } from '@/features/assessment/hooks';
import { previousPeriodRange } from '@/features/assessment/period-default';
import type { StaffItem } from '@/lib/contracts';
import { cn } from '@/lib/utils';

/** 预填值：失败明细/重新发起带入的人员名单与评估时段（§4.2.2 默认值列）。 */
export interface CreateBatchPreset {
  staffs: StaffItem[];
  period: { start: string; end: string };
}

export interface CreateBatchDialogProps {
  open: boolean;
  preset: CreateBatchPreset | null;
  onClose: () => void;
  /** 提交成功回调：页面侧负责重置筛选回第一页（§4.2.3 提交）。 */
  onSubmitted: () => void;
}

interface FormValues {
  target: StaffSelectValue;
  periodStart: string;
  periodEnd: string;
}

export function CreateBatchDialog({
  open,
  preset,
  onClose,
  onSubmitted,
}: CreateBatchDialogProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const planQ = useBatchPlan();
  const createMut = useCreateBatch();
  const [confirmOpen, setConfirmOpen] = useState(false);
  // 昨天快照：渲染期直取（fake timer 友好），schema refine 用校验时点值
  const yesterday = dayjs().subtract(1, 'day').format('YYYY-MM-DD');
  // 焦点落评估对象触发框（§4.2.5）
  const targetAnchorRef = useRef<HTMLDivElement>(null);

  const schema = useMemo(
    () =>
      z.object({
        target: z
          .object({
            mode: z.enum(['all', 'specified']),
            staffs: z.array(z.object({ staff_id: z.string(), staff_name: z.string() })),
          })
          .refine((v) => v.mode === 'all' || v.staffs.length > 0, {
            message: t('create.targetRequired'),
          }),
        periodStart: z.string().min(1, t('create.periodRequired')),
        periodEnd: z
          .string()
          .min(1, t('create.periodRequired'))
          // 校验时点的昨天：防渲染期快照被跨天或测试 fake timer 越过
          .refine(
            (v) => v === '' || v <= dayjs().subtract(1, 'day').format('YYYY-MM-DD'),
            t('create.periodFuture'),
          ),
      }).refine((v) => v.periodStart === '' || v.periodEnd === '' || v.periodStart <= v.periodEnd, {
        message: t('create.periodInvalid'),
        path: ['periodStart'],
      }),
    [t],
  );

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      target: { mode: 'specified', staffs: [] },
      periodStart: '',
      periodEnd: '',
    },
  });
  const {
    control,
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = form;

  // 打开时回填：preset 覆盖人员与时段；否则默认上一完整周期窗口（周期取 plan 缓存，BR2）
  useEffect(() => {
    if (!open) return;
    if (preset) {
      reset({
        target: { mode: 'specified', staffs: preset.staffs },
        periodStart: preset.period.start,
        periodEnd: preset.period.end,
      });
    } else {
      const period = planQ.data?.period ?? 'weekly';
      const range = previousPeriodRange(new Date(), period);
      reset({
        target: { mode: 'specified', staffs: [] },
        periodStart: range.start,
        periodEnd: range.end,
      });
    }
    // 焦点落评估对象触发框：Radix Dialog 打开后把焦点强制接管给第一个可聚焦元素，
    // 用 setTimeout 0 让位给 Dialog 的初始 focus 完成后再转移
    const tmr = setTimeout(() => {
      const btn = targetAnchorRef.current?.querySelector('button');
      btn?.focus();
    }, 0);
    return () => clearTimeout(tmr);
    // plan 数据为缓存读，不作为重置触发依赖
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, preset, reset]);

  const staffsSelected = (() => {
    const v = form.getValues().target;
    return v.mode === 'all' || v.staffs.length > 0;
  });

  function requestClose() {
    if (createMut.isPending) return;
    if (staffsSelected()) {
      setConfirmOpen(true);
    } else {
      onClose();
    }
  }

  const onSubmit = (values: FormValues) => {
    createMut.mutate(
      {
        target_mode: values.target.mode,
        staffs:
          values.target.mode === 'specified'
            ? values.target.staffs.map((s) => ({
                staff_id: s.staff_id,
                staff_name: s.staff_name,
              }))
            : undefined,
        period_start: values.periodStart,
        period_end: values.periodEnd,
      },
      {
        onSuccess: () => {
          toast.success(t('create.toastCreated'));
          onSubmitted();
          onClose();
        },
        onError: () => {
          toast.error(t('create.toastGeneric'));
        },
      },
    );
  };

  const disabledTypeHint = t('create.typeDisabled');
  const disabledTypeCard = (key: 'aiMgmt' | 'enneagram', label: string) => (
    // 置灰卡片：disabled 不吞点击提示，用外层 div 承载 click + title（BR1 §4.2.2）
    <div
      data-testid={`type-card-${key === 'aiMgmt' ? 'ai-mgmt' : 'enneagram'}`}
      title={disabledTypeHint}
      onClick={() => toast(disabledTypeHint)}
      className="flex-1 cursor-not-allowed rounded-md border p-3 opacity-50"
    >
      <button type="button" disabled title={disabledTypeHint} className="w-full text-left">
        <span className="text-sm font-medium">{label}</span>
        <span className="text-muted-foreground mt-1 block text-xs">{disabledTypeHint}</span>
      </button>
    </div>
  );

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!v) requestClose();
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('create.title')}</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              // 不依赖 register 回填：手动同步 dom 到 formState（type=date 在 jsdom 下 fireEvent.change 偶发不进 RHF）
              const dom = (id: string) =>
                (document.getElementById(id) as HTMLInputElement | null)?.value ?? '';
              form.setValue('periodStart', dom('period-start'), { shouldValidate: false });
              form.setValue('periodEnd', dom('period-end'), { shouldValidate: false });
              return handleSubmit(onSubmit)(e);
            }}
            className="flex flex-col gap-4"
          >
            <div className="flex flex-col gap-2">
              <Label>{t('create.typeLabel')}</Label>
              <div className="flex gap-2">
                <button
                  type="button"
                  aria-pressed="true"
                  className={cn(
                    'border-primary flex-1 rounded-md border-2 p-3 text-left',
                  )}
                >
                  <span className="text-sm font-medium">{t('create.typeConversation')}</span>
                </button>
                {disabledTypeCard('aiMgmt', t('create.typeAiMgmt'))}
                {disabledTypeCard('enneagram', t('create.typeEnneagram'))}
              </div>
            </div>

            <div className="flex flex-col gap-2" ref={targetAnchorRef}>
              <Label>{t('create.targetLabel')}</Label>
              <Controller
                control={control}
                name="target"
                render={({ field }) => (
                  <StaffMultiSelect value={field.value} onChange={field.onChange} />
                )}
              />
              {errors.target && (
                <p className="text-destructive text-sm">{errors.target.message}</p>
              )}
            </div>

            <div className="flex flex-col gap-2">
              <Label>{t('create.periodLabel')}</Label>
              <div className="flex items-center gap-2">
                <div className="flex flex-1 flex-col gap-1">
                  <Label htmlFor="period-start" className="sr-only">
                    {t('create.periodStartPlaceholder')}
                  </Label>
                  <Input
                    id="period-start"
                    type="date"
                    aria-label={t('create.periodStartPlaceholder')}
                    max={yesterday}
                    {...register('periodStart')}
                  />
                  {errors.periodStart && (
                    <p className="text-destructive text-sm">{errors.periodStart.message}</p>
                  )}
                </div>
                <span className="text-muted-foreground">~</span>
                <div className="flex flex-1 flex-col gap-1">
                  <Label htmlFor="period-end" className="sr-only">
                    {t('create.periodEndPlaceholder')}
                  </Label>
                  <Input
                    id="period-end"
                    type="date"
                    aria-label={t('create.periodEndPlaceholder')}
                    max={yesterday}
                    {...register('periodEnd')}
                  />
                  {errors.periodEnd && (
                    <p className="text-destructive text-sm">{errors.periodEnd.message}</p>
                  )}
                </div>
              </div>
            </div>

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={requestClose}
                disabled={createMut.isPending}
              >
                {t('create.cancel')}
              </Button>
              <Button type="submit" disabled={createMut.isPending}>
                {createMut.isPending ? t('create.submitting') : t('create.submit')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('create.title')}</AlertDialogTitle>
            <AlertDialogDescription>{t('create.cancelConfirm')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('create.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setConfirmOpen(false);
                onClose();
              }}
            >
              {t('create.cancelConfirmOk')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
