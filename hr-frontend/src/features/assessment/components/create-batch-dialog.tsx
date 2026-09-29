// 发起评测弹窗（specs §4.2 / P2_TST_001 §4.2.2-§4.2.5）：三类型卡片可选 + 三分支表单。
// conversation 保持 F6 批次表单；ai_mgmt/enneagram 为主动测试单人定向发起（决策 12，
// 表单实现拆在 TestTaskForm 子组件）。类型选择会话级记忆（模块级变量，刷新回
// conversation，P2_TST_001 §4.2.2 A）。取消二次确认在测评对象非空时触发（三分支
// 同口径）；提交进行中禁止关闭（§4.2.3）。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
import {
  TestTaskForm,
  type TestTaskFormHandle,
  type TestTaskFormValues,
} from '@/features/assessment/components/test-task-form';
import { useBatchPlan, useCreateBatch } from '@/features/assessment/hooks';
import { useCreateTestTask } from '@/features/assessment/test-task-hooks';
import { previousPeriodRange } from '@/features/assessment/period-default';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { cn } from '@/lib/utils';

/** 预填值：失败明细/重新发起带入的人员名单与评估时段（§4.2.2 默认值列）。
 * staff_id 可选：补跑预填仅需 staff_name（03 B1，staff_id 仅日志定位可省略）。
 * mode 可选：all 模式批次重新发起时预填全员（staffs 空），缺省 specified。 */
export interface CreateBatchPreset {
  mode?: 'all' | 'specified';
  staffs: { staff_id?: string; staff_name: string }[];
  period: { start: string; end: string };
}

/** 弹窗可选评测类型：conversation=对话分析（F6 批次），另两类为主动测试。 */
export type DialogTestType = 'conversation' | 'ai_mgmt' | 'enneagram';

export interface CreateBatchDialogProps {
  open: boolean;
  preset: CreateBatchPreset | null;
  onClose: () => void;
  /** 提交成功回调：页面侧负责重置筛选回第一页（§4.2.3 提交）。 */
  onSubmitted: () => void;
  /** 提交成功回调（携类型）：页面切到对应 tab 并重置该 tab 查询（P2_TST_001 §4.2.3）。 */
  onSubmittedType?: (type: DialogTestType) => void;
}

interface FormValues {
  target: StaffSelectValue;
  periodStart: string;
  periodEnd: string;
}

// 会话级记忆（P2_TST_001 §4.2.2 A 默认值列）：模块级内存变量，同标签页弹窗重开保持
// 上次选择，刷新即重置回 conversation。
let lastTestType: DialogTestType = 'conversation';

/** 测试钩子：重置会话记忆到初始 conversation 态。 */
export function resetDialogTypeMemory(): void {
  lastTestType = 'conversation';
}

export function CreateBatchDialog({
  open,
  preset,
  onClose,
  onSubmitted,
  onSubmittedType,
}: CreateBatchDialogProps): JSX.Element {
  const { t } = useTranslation('assessment');
  const planQ = useBatchPlan();
  const createMut = useCreateBatch();
  const createTestMut = useCreateTestTask();
  const [confirmOpen, setConfirmOpen] = useState(false);
  // 打开时从会话记忆初始化；关闭后再开保持 lastTestType（会话语义在模块级变量）
  const [activeType, setActiveType] = useState<DialogTestType>(lastTestType);
  // 昨天快照：渲染期直取（fake timer 友好），schema refine 用校验时点值
  const yesterday = dayjs().subtract(1, 'day').format('YYYY-MM-DD');
  // 焦点落评估对象触发框（§4.2.5）
  const targetAnchorRef = useRef<HTMLDivElement>(null);
  // 本轮 open 是否已回填：plan 晚到不重置用户已改的表单
  const filledRef = useRef(false);
  // 主动测试子表单控制面（staff 读取与提交）
  const testFormRef = useRef<TestTaskFormHandle | null>(null);
  const onTestFormReady = useCallback((h: TestTaskFormHandle) => {
    testFormRef.current = h;
  }, []);

  const isTest = activeType !== 'conversation';

  const schema = useMemo(
    () =>
      z.object({
        target: z
          .object({
            mode: z.enum(['all', 'specified']),
            staffs: z.array(z.object({ staff_id: z.string().optional(), staff_name: z.string() })),
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
    handleSubmit,
    reset,
    formState: { errors },
  } = form;

  // 类型切换信号：每次切换递增，TestTaskForm 值变化时清空表单（§4.2.5）
  const [testResetSignal, setTestResetSignal] = useState(0);

  // 类型切换：表单区整体切换，已填内容丢弃（P2_TST_001 §4.2.5）
  function switchType(next: DialogTestType) {
    if (next === activeType) return;
    setActiveType(next);
    lastTestType = next;
    setTestResetSignal((k) => k + 1);
    reset({
      target: { mode: 'specified', staffs: [] },
      periodStart: '',
      periodEnd: '',
    });
    filledRef.current = true; // 切换后用户改动优先，不再被 plan 回填覆盖
  }

  // 打开时回填（每次 open 只执行一次，filledRef 防重入）：preset 覆盖人员与时段；
  // 否则默认上一完整周期窗口（周期取 plan，BR2）。plan 未就绪等待数据到达后回填
  //（防 daily 配置下以 weekly 兜底窗口误提交），但用户已改动表单后不再覆盖。
  useEffect(() => {
    if (!open) {
      filledRef.current = false; // 关闭复位，下次打开重新回填
      return;
    }
    if (filledRef.current) return;
    if (isTest) {
      // 主动测试分支无回填语义：preset 只服务 conversation 批次链路；焦点锚点
      // 在 TestTaskForm 的 staff 区块（staffAnchorRef 经控制面透出）
      filledRef.current = true;
      const tmr = setTimeout(() => {
        const btn = testFormRef.current?.staffAnchorRef.current?.querySelector('button');
        btn?.focus();
      }, 0);
      return () => clearTimeout(tmr);
    }
    if (preset) {
      // 预填止日钳位到昨天：定时批次的 PeriodEnd 是触发当日（如 daily 当天），
      // 原样回填会撞后端「终点不含今天」校验 1602，当日补跑被硬拒（§4.3.3）。
      const yesterdayStr = dayjs().subtract(1, 'day').format('YYYY-MM-DD');
      const end = preset.period.end > yesterdayStr ? yesterdayStr : preset.period.end;
      const start = preset.period.start > end ? end : preset.period.start;
      reset({
        target: { mode: preset.mode ?? 'specified', staffs: preset.staffs },
        periodStart: start,
        periodEnd: end,
      });
    } else if (planQ.data?.period) {
      const range = previousPeriodRange(new Date(), planQ.data.period);
      reset({
        target: { mode: 'specified', staffs: [] },
        periodStart: range.start,
        periodEnd: range.end,
      });
    } else if (planQ.isError) {
      // plan 拉取失败：不兜底错误窗口，留空由校验拦住提交（用户可手填）。
      reset({
        target: { mode: 'specified', staffs: [] },
        periodStart: '',
        periodEnd: '',
      });
    } else {
      return; // plan 加载中，等待数据到达后重跑本 effect
    }
    filledRef.current = true;
    // 焦点落评估对象触发框：Radix Dialog 打开后把焦点强制接管给第一个可聚焦元素，
    // 用 setTimeout 0 让位给 Dialog 的初始 focus 完成后再转移
    const tmr = setTimeout(() => {
      const btn = targetAnchorRef.current?.querySelector('button');
      btn?.focus();
    }, 0);
    return () => clearTimeout(tmr);
  }, [open, preset, reset, planQ.data, planQ.isError, isTest]);

  /** 对象非空判定（取消二次确认触发口径，三分支同）：conversation 看名单，主动测试看单人。 */
  function staffsSelected(): boolean {
    if (isTest) return testFormRef.current?.getValues().staff != null;
    const v = form.getValues().target;
    return v.mode === 'all' || v.staffs.length > 0;
  }

  function requestClose() {
    if (createMut.isPending || createTestMut.isPending) return;
    if (staffsSelected()) {
      setConfirmOpen(true);
    } else {
      onClose();
    }
  }

  /** 主动测试提交错误码分桶（P2_TST_001 specs §4.2.4/§5.1.5 原文）：
   * 1803 走 i18n 模板插值（维度名从后端固定英文模板 message 剥离，前后缀契约
   * 见 03 B1 错误码表）；1804/1805/1305 为固定 i18n 文案。 */
  function toastTestError(err: unknown) {
    if (err instanceof ApiError) {
      if (err.code === ErrCode.TestDimensionQuestionsEmpty) {
        const dim = err.message
          .replace('dimension ', '')
          .replace(' has no active questions', '');
        toast.error(
          dim && dim !== err.message
            ? t('create.toastDimEmpty', { dim })
            : t('create.toastDimEmptyNoName'),
        );
      } else if (err.code === ErrCode.TestScaleNotReady) {
        toast.error(t('create.toastScaleNotReady'));
      } else if (err.code === ErrCode.TestStaffInvalid) {
        toast.error(t('create.toastStaffInvalid'));
      } else if (err.code === ErrCode.StaffListUnavailable) {
        toast.error(t('create.toastStaffUnavailable'));
      } else {
        toast.error(t('create.toastGeneric'));
      }
    } else {
      toast.error(t('create.toastGeneric'));
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
          onSubmittedType?.('conversation');
          onClose();
        },
        onError: (err) => {
          // 业务错误码分桶透出（1602 时段非法 / 1305 上游不可用），其余通用文案。
          if (err instanceof ApiError && err.code === ErrCode.BatchPeriodInvalid) {
            toast.error(t('create.toastPeriodInvalid'));
          } else if (err instanceof ApiError && err.code === ErrCode.StaffListUnavailable) {
            toast.error(t('create.toastStaffUnavailable'));
          } else {
            toast.error(t('create.toastGeneric'));
          }
        },
      },
    );
  };

  const onTestSubmit = (values: TestTaskFormValues) => {
    if (!values.staff) return;
    createTestMut.mutate(
      {
        // 分支守卫：TestTaskForm 仅在主动测试分支渲染提交
        test_type: activeType === 'enneagram' ? 'enneagram' : 'ai_mgmt',
        staff_id: values.staff.staff_id,
        staff_name: values.staff.staff_name,
        ...(activeType === 'ai_mgmt' ? { dimension_ids: values.dimensionIds } : {}),
      },
      {
        onSuccess: () => {
          toast.success(t('create.toastTestCreated'));
          onSubmitted();
          onSubmittedType?.(activeType);
          onClose();
        },
        onError: (err) => toastTestError(err),
      },
    );
  };

  const submitting = createMut.isPending || createTestMut.isPending;

  const typeCards: { key: DialogTestType; label: string }[] = [
    { key: 'conversation', label: t('create.typeConversation') },
    { key: 'ai_mgmt', label: t('create.typeAiMgmt') },
    { key: 'enneagram', label: t('create.typeEnneagram') },
  ];

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
              // 主动测试分支经子表单校验后提交；conversation 走本表单
              if (isTest) {
                e.preventDefault();
                testFormRef.current?.submit(onTestSubmit)(e);
                return;
              }
              void handleSubmit(onSubmit)(e);
            }}
            className="flex flex-col gap-4"
          >
            <div className="flex flex-col gap-2">
              <Label>{t('create.typeLabel')}</Label>
              <div className="flex gap-2">
                {typeCards.map((card) => (
                  <button
                    key={card.key}
                    type="button"
                    aria-pressed={activeType === card.key}
                    data-testid={`type-card-${card.key}`}
                    onClick={() => switchType(card.key)}
                    className={cn(
                      'flex-1 rounded-md border p-3 text-left',
                      activeType === card.key
                        ? 'border-primary border-2'
                        : 'hover:bg-accent border',
                    )}
                  >
                    <span className="text-sm font-medium">{card.label}</span>
                  </button>
                ))}
              </div>
            </div>

            {isTest ? (
              <TestTaskForm
                activeType={activeType === 'enneagram' ? 'enneagram' : 'ai_mgmt'}
                resetSignal={testResetSignal}
                onReady={onTestFormReady}
              />
            ) : (
              <>
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
                      <Controller
                        control={control}
                        name="periodStart"
                        render={({ field }) => (
                          <Input
                            {...field}
                            type="date"
                            aria-label={t('create.periodStartPlaceholder')}
                            max={yesterday}
                          />
                        )}
                      />
                      {errors.periodStart && (
                        <p className="text-destructive text-sm">{errors.periodStart.message}</p>
                      )}
                    </div>
                    <span className="text-muted-foreground">~</span>
                    <div className="flex flex-1 flex-col gap-1">
                      <Controller
                        control={control}
                        name="periodEnd"
                        render={({ field }) => (
                          <Input
                            {...field}
                            type="date"
                            aria-label={t('create.periodEndPlaceholder')}
                            max={yesterday}
                          />
                        )}
                      />
                      {errors.periodEnd && (
                        <p className="text-destructive text-sm">{errors.periodEnd.message}</p>
                      )}
                    </div>
                  </div>
                </div>
              </>
            )}

            <DialogFooter>
              <Button type="button" variant="outline" onClick={requestClose} disabled={submitting}>
                {t('create.cancel')}
              </Button>
              <Button type="submit" disabled={submitting}>
                {submitting ? t('create.submitting') : t('create.submit')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('create.title')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(isTest ? 'create.cancelTestConfirm' : 'create.cancelConfirm')}
            </AlertDialogDescription>
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
