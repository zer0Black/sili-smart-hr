import { createFileRoute } from '@tanstack/react-router';
import { useEffect, useMemo } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { z } from 'zod';
import { zodResolver } from '@hookform/resolvers/zod';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { MemberMultiSelect } from '@/features/system-params/components/member-multi-select';
import { useAssessmentConfig, useSaveAssessmentConfig } from '@/features/system-params/hooks';
import {
  intervalDescOf,
  triggerDayOf,
  type Period,
} from '@/features/system-params/period';
import type { AssessmentConfig, SaveAssessmentPayload, StaffItem } from '@/lib/contracts';
import { ErrCode } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';
import { queryClient } from '@/lib/query-client';

export const Route = createFileRoute('/_authenticated/system/params')({
  component: SystemParamsPage,
});

// 周期默认值：依据 specs §4.1.2 字段表（每周/23:00/全员）
const DEFAULT_VALUES: SystemParamsFormValues = {
  period: 'weekly',
  trigger_time: '23:00',
  target_mode: 'all',
  specified_members: [],
};

interface SystemParamsFormValues {
  period: string;
  trigger_time: string;
  target_mode: string;
  specified_members: StaffItem[];
}

// version 不进表单，提交时从 useAssessmentConfig 数据取（保证乐观锁正确）
function buildSchema(t: (k: string) => string) {
  return z.object({
    period: z.string().min(1, t('periodRequired')),
    trigger_time: z
      .string()
      .min(1, t('triggerTimeRequired'))
      .regex(/^\d{2}:\d{2}$/, t('triggerTimePattern'))
      .refine((v) => {
        const [hh, mm] = v.split(':').map(Number);
        return hh >= 0 && hh <= 23 && mm >= 0 && mm <= 59;
      }, t('triggerTimePattern')),
    target_mode: z.string().min(1, t('targetModeRequired')),
    // 指定人员的非空校验放 onSubmit 末端做（仅 target_mode=specified 时校验）
    specified_members: z.array(z.any()),
  });
}

/**
 * 系统参数页（specs §3.1 页面 1）。
 * 三区段：周期与触发、评估对象、成本说明（§4.1.1）。
 * 保存下次跑批生效（§4.1.4 规则1 / BR3），全员/指定互斥（§4.1.4 规则2 / BR4），
 * 触发日与评估区间由周期联动不可手填（§4.1.4 规则3 / BR5）。
 */
function SystemParamsPage() {
  const { t } = useTranslation('systemParams');
  const configQ = useAssessmentConfig();
  const saveMut = useSaveAssessmentConfig();

  const schema = useMemo(() => buildSchema(t), [t]);

  const form = useForm<SystemParamsFormValues>({
    resolver: zodResolver(schema),
    defaultValues: DEFAULT_VALUES,
  });

  const {
    control,
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors },
  } = form;

  // configQ.data 到位后回填表单（useEffect reset，仿 account-form-dialog L81-115）
  useEffect(() => {
    if (configQ.data) {
      reset(fromConfig(configQ.data));
    }
  }, [configQ.data, reset]);

  const period = watch('period') as Period;
  const targetMode = watch('target_mode');
  const specifiedMembers = watch('specified_members');

  // 评估对象互斥（§4.1.4 规则2 / BR4）：
  // - 选「全员」清空指定人员
  // - 选「指定人员」从全员切回 specified 时，无需主动移除「全员」（target_mode 已切到 specified）
  //   specified_members 在切到 specified 后由用户选择；切回 all 时清空 specified_members
  function onTargetModeChange(mode: 'all' | 'specified') {
    setValue('target_mode', mode);
    if (mode === 'all') {
      setValue('specified_members', []);
    }
  }

  function onSpecifiedMembersChange(next: StaffItem[]) {
    // BR4：选指定人员即移除全员
    if (next.length > 0 && targetMode === 'all') {
      setValue('target_mode', 'specified');
    }
    setValue('specified_members', next);
  }

  // 人员数据加载失败降级（§4.1.4 规则4）：自动选中全员并清空指定人员。
  // 由 MemberMultiSelect 在 useStaffs 首次失败时经 onDegraded 触发，degradedFiredRef 保证仅触发一次。
  function onMembersDegraded() {
    setValue('target_mode', 'all');
    setValue('specified_members', []);
  }

  const onError = (err: unknown) => {
    // BR7（§4.1.4 规则5）：1306 废弃配置缓存触发重取拿最新 version，
    // configQ 重取后 useEffect 用新数据 reset 表单，避免带旧 version 重试反复冲突。
    const code = err instanceof ApiError ? err.code : undefined;
    if (code === ErrCode.ConfigVersionConflict) {
      toast.error(t('toastVersionConflict'));
      void queryClient.invalidateQueries({ queryKey: ['assessment-config'] });
      return;
    }
    toast.error(t('toastGeneric'));
  };

  const onSubmit = (values: SystemParamsFormValues) => {
    // BR4：指定人员模式下至少一人
    if (values.target_mode === 'specified' && values.specified_members.length === 0) {
      toast.error(t('specifiedMembersRequired'));
      return;
    }
    // version 从 configQ 取，保证乐观锁正确
    const version = configQ.data?.version ?? 0;
    const payload: SaveAssessmentPayload = {
      period: values.period,
      trigger_time: values.trigger_time,
      target_mode: values.target_mode,
      specified_members: values.target_mode === 'all' ? [] : values.specified_members,
      version,
    };
    saveMut.mutate(payload, {
      onSuccess: () => {
        // BR1/BR3：成功 toast，下次跑批生效
        toast.success(t('toastSaved'));
      },
      onError,
    });
  };

  // 恢复默认（BR2 §4.1.3）：本地 reset 不触发接口
  const onReset = () => {
    reset(DEFAULT_VALUES);
    toast.success(t('toastReset'));
  };

  // 成本节流估算（§4.1.5）
  const costText =
    targetMode === 'all'
      ? t('estimate.all')
      : t('estimate.count', { count: specifiedMembers.length });

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{t('title')}</h1>
        <p className="text-muted-foreground text-sm">{t('subtitle')}</p>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-6">
        {/* 区段 1：周期与触发 */}
        <Card>
          <CardHeader>
            <CardTitle>{t('periodSection')}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-4">
              <div className="flex flex-col gap-2">
                <Label htmlFor="config-period">{t('period')}</Label>
                <Controller
                  control={control}
                  name="period"
                  render={({ field }) => (
                    <Select value={field.value} onValueChange={field.onChange}>
                      <SelectTrigger id="config-period" className="w-full">
                        <SelectValue placeholder={t('periodPlaceholder')} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="daily">{t('periodDaily')}</SelectItem>
                        <SelectItem value="weekly">{t('periodWeekly')}</SelectItem>
                        <SelectItem value="monthly">{t('periodMonthly')}</SelectItem>
                      </SelectContent>
                    </Select>
                  )}
                />
                {errors.period && (
                  <p className="text-destructive text-sm">{errors.period.message}</p>
                )}
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="config-trigger-time">{t('triggerTime')}</Label>
                <Input
                  id="config-trigger-time"
                  placeholder={t('triggerTimePlaceholder')}
                  {...register('trigger_time')}
                />
                {errors.trigger_time && (
                  <p className="text-destructive text-sm">{errors.trigger_time.message}</p>
                )}
              </div>
              <div className="flex flex-col gap-2">
                <Label>{t('triggerDayLabel')}</Label>
                <div className="bg-muted/30 flex h-9 items-center rounded-md border px-3 text-sm">
                  {t(triggerDayOf(period))}
                </div>
              </div>
              <div className="flex flex-col gap-2">
                <Label>{t('intervalLabel')}</Label>
                <div className="bg-muted/30 flex h-9 items-center rounded-md border px-3 text-sm">
                  {t(intervalDescOf(period))}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        {/* 区段 2：评估对象 */}
        <Card>
          <CardHeader>
            <CardTitle>{t('targetSection')}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label>{t('targetMode')}</Label>
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="radio"
                    name="target_mode"
                    value="all"
                    checked={targetMode === 'all'}
                    onChange={() => onTargetModeChange('all')}
                  />
                  {t('targetAll')}
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="radio"
                    name="target_mode"
                    value="specified"
                    checked={targetMode === 'specified'}
                    onChange={() => onTargetModeChange('specified')}
                  />
                  {t('targetSpecified')}
                </label>
              </div>
              {errors.target_mode && (
                <p className="text-destructive text-sm">{errors.target_mode.message}</p>
              )}
            </div>
            <div className="flex flex-col gap-2">
              <Label>{t('targetSpecified')}</Label>
              <p className="text-muted-foreground text-xs">{t('targetSpecifiedHint')}</p>
              <Controller
                control={control}
                name="specified_members"
                render={({ field }) => (
                  <MemberMultiSelect
                    value={field.value}
                    onChange={onSpecifiedMembersChange}
                    onDegraded={onMembersDegraded}
                    disabled={targetMode === 'all'}
                  />
                )}
              />
            </div>
          </CardContent>
        </Card>

        {/* 区段 3：成本说明（只读） */}
        <Card>
          <CardHeader>
            <CardTitle>{t('costSection')}</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-sm">{costText}</p>
          </CardContent>
        </Card>

        {/* 底部固定操作区（§4.1.3） */}
        <div className="flex items-center justify-between gap-3 rounded-md border bg-card px-4 py-3 shadow-xs">
          <p className="text-muted-foreground text-sm">{t('nextRunHint')}</p>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={onReset}
              disabled={saveMut.isPending || configQ.isLoading || configQ.isError}
            >
              {t('reset')}
            </Button>
            <Button type="submit" disabled={saveMut.isPending || configQ.isLoading || configQ.isError}>
              {saveMut.isPending ? t('saving') : t('save')}
            </Button>
          </div>
        </div>
      </form>
    </div>
  );
}

/** 后端 AssessmentConfig → 表单值。 */
function fromConfig(c: AssessmentConfig): SystemParamsFormValues {
  return {
    period: c.period,
    trigger_time: c.trigger_time,
    target_mode: c.target_mode,
    specified_members: c.specified_members ?? [],
  };
}
