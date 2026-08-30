// 活跃度规则配置面板。React Hook Form + Zod + zodResolver 模式，仿 leaf-config-form。
import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo, useState } from 'react';
import { useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { SaveConfirmDialog } from '@/features/dimension/components/save-confirm-dialog';
import { useSaveActivityRule } from '@/features/dimension/hooks';
import { ErrCode } from '@/lib/contracts';
import type { ActivityRule, SaveActivityRulePayload } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export interface ActivityRulePanelProps {
  rule: ActivityRule;
  onSaved: () => void;
}

interface ActivityRuleFormValues {
  active_threshold: number;
  low_frequency_threshold: number;
}

/**
 * Zod schema 工厂。业务规则（specs §4.1.2 C）：active/low 均 1~999 整数，low 须 < active。
 * 校验消息经 t() 国际化，组件内 useMemo 重建（仿 account form 样板）。
 */
export function createActivityRuleSchema(t: TFunction<'dimension'>) {
  return z
    .object({
      active_threshold: z
        .number({ message: t('activity.activeRequired') })
        .int(t('activity.activeInteger'))
        .min(1, t('activity.activeMin'))
        .max(999, t('activity.activeMax')),
      low_frequency_threshold: z
        .number({ message: t('activity.lowRequired') })
        .int(t('activity.lowInteger'))
        .min(1, t('activity.lowMin'))
        .max(999, t('activity.lowMax')),
    })
    .refine((v) => v.low_frequency_threshold < v.active_threshold, {
      message: t('activity.lowLessThanActive'),
      path: ['low_frequency_threshold'],
    });
}

function ruleToFormValues(rule: ActivityRule): ActivityRuleFormValues {
  return {
    active_threshold: rule.active_threshold,
    low_frequency_threshold: rule.low_frequency_threshold,
  };
}

function buildPayload(v: ActivityRuleFormValues): SaveActivityRulePayload {
  return {
    active_threshold: v.active_threshold,
    low_frequency_threshold: v.low_frequency_threshold,
  };
}

/**
 * 活跃度规则配置面板。统计区间跟随评估区间（系统配置决定，只读展示）。
 * 保存走二次确认，重置回滚到 rule 初值不触发接口。
 */
export function ActivityRulePanel({ rule, onSaved }: ActivityRulePanelProps) {
  const { t } = useTranslation('dimension');
  const saveMut = useSaveActivityRule();
  const isPending = saveMut.isPending;

  const schema = useMemo(() => createActivityRuleSchema(t), [t]);
  const form = useForm<ActivityRuleFormValues>({
    resolver: zodResolver(schema),
    defaultValues: ruleToFormValues(rule),
  });

  const {
    register,
    handleSubmit,
    reset,
    setError,
    watch,
    formState: { errors },
  } = form;

  // rule 变化（刷新后 updated_at 变）时回填表单。
  useEffect(() => {
    reset(ruleToFormValues(rule));
  }, [rule, reset]);

  const [confirmOpen, setConfirmOpen] = useState(false);

  // RHF 校验通过后仅打开二次确认弹窗，确认回调里真正提交。
  const onValid = () => setConfirmOpen(true);

  const onConfirmSave = () => {
    setConfirmOpen(false);
    const payload = buildPayload(form.getValues());
    saveMut.mutate(payload, {
      onSuccess: () => {
        toast.success(t('activity.toastSaved'));
        onSaved();
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        // 校验类错误（specs §4.1.4 规则9 + BR3）：1208 映射到字段内联报错，保留输入。
        if (code === ErrCode.ActivityThresholdInvalid) {
          setError('low_frequency_threshold', {
            message: t('activity.lowLessThanActive'),
          });
          return;
        }
        // 1500 / 网络 / 其他
        toast.error(t('activity.toastGeneric'));
      },
    });
  };

  const onReset = () => reset(ruleToFormValues(rule));

  // 判定关系说明块（specs §4.1.5 + BR2）：用当前表单值动态渲染阈值数字。
  const [activeValue, lowValue] = watch([
    'active_threshold',
    'low_frequency_threshold',
  ]);
  const activeNum =
    Number.isFinite(activeValue) && activeValue > 0 ? activeValue : null;
  const lowNum =
    Number.isFinite(lowValue) && lowValue > 0 ? lowValue : null;
  const activeLabel = activeNum != null ? String(activeNum) : t('activity.relation.activeLabel');
  const activeMinus1Label =
    activeNum != null ? String(activeNum - 1) : t('activity.relation.activeMinus1Label');
  const lowLabel = lowNum != null ? String(lowNum) : t('activity.relation.lowLabel');

  return (
    <form onSubmit={handleSubmit(onValid)} className="flex flex-col gap-6">
      {/* 统计区间：只读标签，跟随评估区间 */}
      <div className="flex items-center justify-between rounded-md border p-3">
        <div className="flex flex-col gap-0.5">
          <Label>{t('activity.statsWindow')}</Label>
          <span className="text-muted-foreground text-xs">
            {t('activity.statsWindowHint')}
          </span>
        </div>
        <span className="text-muted-foreground text-sm">{t('activity.statsWindowValue')}</span>
      </div>

      {/* 活跃判定下限 */}
      <div className="flex flex-col gap-2">
        <Label htmlFor="rule-active">{t('activity.activeThreshold')}</Label>
        <Input
          id="rule-active"
          type="number"
          min={1}
          max={999}
          step={1}
          placeholder={t('activity.activePlaceholder')}
          {...register('active_threshold', { valueAsNumber: true })}
        />
        <p className="text-muted-foreground text-xs">
          {t('activity.activeHint')}
        </p>
        {errors.active_threshold && (
          <p className="text-destructive text-sm">
            {errors.active_threshold.message}
          </p>
        )}
      </div>

      {/* 低频判定下限 */}
      <div className="flex flex-col gap-2">
        <Label htmlFor="rule-low">{t('activity.lowThreshold')}</Label>
        <Input
          id="rule-low"
          type="number"
          min={1}
          max={999}
          step={1}
          placeholder={t('activity.lowPlaceholder')}
          {...register('low_frequency_threshold', { valueAsNumber: true })}
        />
        <p className="text-muted-foreground text-xs">
          {t('activity.lowHint')}
        </p>
        {errors.low_frequency_threshold && (
          <p className="text-destructive text-sm">
            {errors.low_frequency_threshold.message}
          </p>
        )}
      </div>

      {/* 判定关系说明块（specs §4.1.5 + BR2）：用当前表单值动态渲染阈值数字 */}
      <div className="rounded-md border bg-muted/30 p-3 text-sm">
        <p className="font-medium">{t('activity.relation.title')}</p>
        <p className="text-muted-foreground mt-1 text-xs leading-relaxed">
          {t('activity.relation.template', {
            active: activeLabel,
            activeMinus1: activeMinus1Label,
            low: lowLabel,
          })}
        </p>
      </div>

      {/* 底部按钮区：重置与保存规则 */}
      <div className="flex items-center gap-2 border-t pt-4">
        <Button
          type="button"
          variant="outline"
          onClick={onReset}
          disabled={isPending}
        >
          {t('activity.reset')}
        </Button>
        <Button type="submit" disabled={isPending}>
          {isPending ? t('activity.saving') : t('activity.save')}
        </Button>
      </div>

      {/* 保存规则二次确认弹窗（specs §4.1.3 + §4.1.4 规则1） */}
      <SaveConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        onConfirm={onConfirmSave}
        submitting={isPending}
        title={t('activity.saveTitle')}
        desc={t('activity.saveDesc')}
      />
    </form>
  );
}
