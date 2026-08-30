// 叶子维度配置表单。React Hook Form + Zod + zodResolver 模式，仿 account form。
import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
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
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Slider } from '@/components/ui/slider';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { SaveConfirmDialog } from '@/features/dimension/components/save-confirm-dialog';
import { useDeleteDimension, useUpdateDimension } from '@/features/dimension/hooks';
import { MODULE_META } from '@/features/dimension/types';
import type { DataSource, ModuleCode, ModuleMeta } from '@/features/dimension/types';
import { DIM_FIELD_LIMITS } from '@/features/dimension/validation';
import { ErrCode } from '@/lib/contracts';
import type { DimensionDetail, UpdateDimensionPayload } from '@/lib/contracts';
import { ApiError } from '@/lib/http-client';

export interface LeafConfigFormProps {
  detail: DimensionDetail;
  onSaved: () => void;
}

interface LeafFormValues {
  name: string;
  prompt: string;
  anchor: string;
  weight: number;
  include_overview: boolean;
  enabled: boolean;
  description: string;
}

/**
 * Zod schema 工厂。export 供单测断言：
 *   createLeafFormSchema({ dataSource: 'CONVERSATION', isReference: false }, t)
 *     .safeParse({ ...values, prompt: '' }).success === false
 * prompt 是否必填取决于 data_source（specs §4.1.4 规则6 + §4.2.5）。
 */
export function createLeafFormSchema(
  opts: { dataSource: DataSource; isReference: boolean },
  t: TFunction<'dimension'>,
) {
  const promptRequired = opts.dataSource === 'CONVERSATION';
  return z.object({
    name: z
      .string()
      .min(DIM_FIELD_LIMITS.nameMin, t('leaf.nameMinLength'))
      .max(DIM_FIELD_LIMITS.nameMax, t('leaf.nameMaxLength')),
    prompt: z
      .string()
      .max(DIM_FIELD_LIMITS.promptMax, t('leaf.promptMaxLength'))
      .refine(
        (v) => !promptRequired || v.trim().length > 0,
        t('leaf.promptRequired'),
      ),
    anchor: z
      .string()
      .min(DIM_FIELD_LIMITS.anchorMin, t('leaf.anchorRequired'))
      .max(DIM_FIELD_LIMITS.anchorMax, t('leaf.anchorMaxLength')),
    weight: z
      .number()
      .int(t('leaf.weightInteger'))
      .min(DIM_FIELD_LIMITS.weightMin, t('leaf.weightMin'))
      .max(DIM_FIELD_LIMITS.weightMax, t('leaf.weightMax')),
    include_overview: z.boolean(),
    enabled: z.boolean(),
    description: z
      .string()
      .max(DIM_FIELD_LIMITS.descriptionMax, t('leaf.descriptionMaxLength')),
  });
}

function detailToFormValues(detail: DimensionDetail): LeafFormValues {
  return {
    name: detail.name,
    prompt: detail.prompt,
    anchor: detail.anchor,
    weight: detail.weight,
    include_overview: detail.include_overview,
    enabled: detail.enabled,
    description: detail.description,
  };
}

function buildPayload(detail: DimensionDetail, v: LeafFormValues): UpdateDimensionPayload {
  return {
    id: detail.id,
    name: v.name.trim(),
    prompt: v.prompt,
    anchor: v.anchor,
    weight: v.weight,
    include_overview: v.include_overview,
    enabled: v.enabled,
    description: v.description.trim() === '' ? null : v.description,
    version: detail.version,
  };
}

/**
 * 叶子维度配置表单。校验通过后弹二次确认弹窗，确认再提交。
 * 删除按钮在启用态禁用并提示，停用态走 AlertDialog popconfirm 软删除。
 */
export function LeafConfigForm({ detail, onSaved }: LeafConfigFormProps) {
  const { t } = useTranslation('dimension');
  const moduleCode = detail.module_code as ModuleCode;
  const dataSource = detail.data_source as DataSource;
  const meta: ModuleMeta = MODULE_META[moduleCode];
  const showPrompt = dataSource === 'CONVERSATION';

  // 权重控件禁用与提示文案（specs §4.1.4 规则5）。is_reference 优先于模块级禁用。
  const weightDisabled = meta.weightDisabled || detail.is_reference;
  const weightHint = detail.is_reference
    ? t('leaf.weightHintReference')
    : meta.weightDisabled
      ? t('leaf.weightHintActivity')
      : '';

  const updateMut = useUpdateDimension();
  const deleteMut = useDeleteDimension();
  const isPending = updateMut.isPending || deleteMut.isPending;

  const schema = useMemo(
    () => createLeafFormSchema({ dataSource, isReference: detail.is_reference }, t),
    [dataSource, detail.is_reference, t],
  );

  const form = useForm<LeafFormValues>({
    resolver: zodResolver(schema),
    defaultValues: detailToFormValues(detail),
  });

  const {
    register,
    handleSubmit,
    control,
    reset,
    setError,
    getValues,
    formState: { errors },
  } = form;

  // detail 变化（切换维度或刷新后 version 更新）时回填表单。
  useEffect(() => {
    reset(detailToFormValues(detail));
  }, [detail, reset]);

  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  // RHF 校验通过后仅打开二次确认弹窗，确认回调里真正提交。
  const onValid = () => setConfirmOpen(true);

  const onConfirmSave = () => {
    setConfirmOpen(false);
    const payload = buildPayload(detail, getValues());
    updateMut.mutate(payload, {
      onSuccess: () => {
        toast.success(t('leaf.toastSaved'));
        onSaved();
      },
      onError: (err) => {
        const code = err instanceof ApiError ? err.code : undefined;
        // 校验类错误（specs §4.1.4 规则9）：映射到字段内联报错，保留输入。
        if (code === ErrCode.DimensionNameInvalid) {
          setError('name', { message: t('leaf.nameInvalid') });
          return;
        }
        if (code === ErrCode.DimensionAnchorRequired) {
          setError('anchor', { message: t('leaf.anchorRequired') });
          return;
        }
        if (code === ErrCode.DimensionPromptRequired) {
          setError('prompt', { message: t('leaf.promptRequired') });
          return;
        }
        if (code === ErrCode.BadRequest) {
          toast.error(t('leaf.toastBadRequest'));
          return;
        }
        if (code === ErrCode.DimensionVersionConflict) {
          toast.error(t('leaf.toastVersionConflict'));
          return;
        }
        // 1500 / 网络 / 其他
        toast.error(t('leaf.toastGeneric'));
      },
    });
  };

  const onReset = () => reset(detailToFormValues(detail));

  const onConfirmDelete = () => {
    setDeleteOpen(false);
    deleteMut.mutate(
      { id: detail.id, version: detail.version },
      {
        onSuccess: () => {
          toast.success(t('leaf.toastDeleted'));
          onSaved();
        },
        onError: (err) => {
          const code = err instanceof ApiError ? err.code : undefined;
          if (code === ErrCode.DimensionVersionConflict) {
            toast.error(t('leaf.toastVersionConflict'));
            return;
          }
          if (code === ErrCode.DimensionEnabledNotDeletable) {
            toast.error(t('leaf.toastDeleteEnabled'));
            return;
          }
          toast.error(t('leaf.toastGeneric'));
        },
      },
    );
  };

  return (
    <form onSubmit={handleSubmit(onValid)} className="flex flex-col gap-6">
      {/* 只读信息区 */}
      <div className="grid grid-cols-3 gap-3 rounded-md border bg-muted/30 p-3 text-sm">
        <div className="flex flex-col gap-1">
          <span className="text-muted-foreground text-xs">{t('leaf.readonly.code')}</span>
          <span className="font-mono">{detail.code}</span>
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-muted-foreground text-xs">{t('leaf.readonly.module')}</span>
          <span>{t(`module.${moduleCode}`)}</span>
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-muted-foreground text-xs">{t('leaf.readonly.dataSource')}</span>
          <span>{t(`dataSource.${dataSource}`)}</span>
        </div>
      </div>

      {/* 维度名称 */}
      <div className="flex flex-col gap-2">
        <Label htmlFor="dim-name">{t('leaf.name')}</Label>
        <Input id="dim-name" autoComplete="off" placeholder={t('leaf.namePlaceholder')} {...register('name')} />
        {errors.name && <p className="text-destructive text-sm">{errors.name.message}</p>}
      </div>

      {/* 评分提示词：仅 CONVERSATION 显示（specs §4.1.4 规则6 + §4.2.5） */}
      {showPrompt && (
        <div className="flex flex-col gap-2">
          <Label htmlFor="dim-prompt">{t('leaf.prompt')}</Label>
          <Textarea
            id="dim-prompt"
            rows={5}
            placeholder={t('leaf.promptPlaceholder')}
            {...register('prompt')}
          />
          <p className="text-muted-foreground text-xs">
            {t('leaf.promptHint')}
          </p>
          {errors.prompt && <p className="text-destructive text-sm">{errors.prompt.message}</p>}
        </div>
      )}

      {/* 评分锚点 */}
      <div className="flex flex-col gap-2">
        <Label htmlFor="dim-anchor">{t('leaf.anchor')}</Label>
        <Textarea
          id="dim-anchor"
          rows={4}
          placeholder={t('leaf.anchorPlaceholder')}
          {...register('anchor')}
        />
        {errors.anchor && <p className="text-destructive text-sm">{errors.anchor.message}</p>}
      </div>

      {/* 聚合权重：数字 Input + Slider 双向联动，禁用时展示提示 */}
      <div className="flex flex-col gap-2">
        <Label>{t('leaf.weight')}</Label>
        <Controller
          control={control}
          name="weight"
          render={({ field }) => (
            <div className="flex items-center gap-4">
              <Slider
                value={[field.value]}
                min={0}
                max={100}
                step={1}
                disabled={weightDisabled}
                onValueChange={(v) => field.onChange(v[0])}
                className="flex-1"
              />
              <Input
                type="number"
                min={0}
                max={100}
                step={1}
                value={field.value}
                disabled={weightDisabled}
                onChange={(e) => {
                  // 清空时不写值，保留上一个合法值，避免空串被 Number() 钳成 0 静默改写默认权重。
                  if (e.target.value === '') return;
                  const n = Number(e.target.value);
                  if (Number.isFinite(n)) {
                    field.onChange(Math.max(0, Math.min(100, Math.trunc(n))));
                  }
                }}
                className="w-20"
              />
              <span className="text-muted-foreground text-sm">%</span>
            </div>
          )}
        />
        {weightDisabled && (
          <p className="text-muted-foreground text-xs">{weightHint}</p>
        )}
        {errors.weight && <p className="text-destructive text-sm">{errors.weight.message}</p>}
      </div>

      {/* 参与总览分 */}
      <div className="flex items-center justify-between rounded-md border p-3">
        <div className="flex flex-col gap-0.5">
          <Label htmlFor="dim-overview" className="cursor-pointer">{t('leaf.overview')}</Label>
          <span className="text-muted-foreground text-xs">{t('leaf.overviewHint')}</span>
        </div>
        <Controller
          control={control}
          name="include_overview"
          render={({ field }) => (
            <Switch
              id="dim-overview"
              checked={field.value}
              onCheckedChange={field.onChange}
              disabled={weightDisabled}
            />
          )}
        />
      </div>

      {/* 是否启用 */}
      <div className="flex items-center justify-between rounded-md border p-3">
        <div className="flex flex-col gap-0.5">
          <Label htmlFor="dim-enabled" className="cursor-pointer">{t('leaf.enabled')}</Label>
          <span className="text-muted-foreground text-xs">{t('leaf.enabledHint')}</span>
        </div>
        <Controller
          control={control}
          name="enabled"
          render={({ field }) => (
            <Switch
              id="dim-enabled"
              checked={field.value}
              onCheckedChange={field.onChange}
            />
          )}
        />
      </div>

      {/* 维度说明 */}
      <div className="flex flex-col gap-2">
        <Label htmlFor="dim-desc">{t('leaf.description')}</Label>
        <Textarea
          id="dim-desc"
          rows={3}
          placeholder={t('leaf.descriptionPlaceholder')}
          {...register('description')}
        />
        {errors.description && (
          <p className="text-destructive text-sm">{errors.description.message}</p>
        )}
      </div>

      {/* 底部按钮区：左侧重置与保存，右侧删除 */}
      <div className="flex items-center justify-between border-t pt-4">
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" onClick={onReset} disabled={isPending}>
            {t('leaf.reset')}
          </Button>
          <Button type="submit" disabled={isPending}>
            {isPending ? t('leaf.saving') : t('leaf.save')}
          </Button>
        </div>

        {detail.enabled ? (
          <Button type="button" variant="destructive" disabled title={t('leaf.deleteDisabledHint')}>
            {t('leaf.delete')}
          </Button>
        ) : (
          <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
            <AlertDialogTrigger asChild>
              <Button type="button" variant="destructive" disabled={isPending}>
                {t('leaf.delete')}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('leaf.deleteTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('leaf.deleteDesc')}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel disabled={isPending}>{t('saveConfirm.cancel')}</AlertDialogCancel>
                <AlertDialogAction
                  onClick={(e) => {
                    e.preventDefault();
                    onConfirmDelete();
                  }}
                  disabled={isPending}
                >
                  {t('leaf.deleteConfirm')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        )}
      </div>

      {/* 保存配置二次确认弹窗（specs §4.1.3 + §4.1.4 规则1） */}
      <SaveConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        onConfirm={onConfirmSave}
        submitting={isPending}
        title={t('leaf.saveTitle')}
        desc={t('leaf.saveDesc')}
      />
    </form>
  );
}
