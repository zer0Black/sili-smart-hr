// 新增维度弹窗。React Hook Form + Zod + 模块联动重置，仿 account-form-dialog 与 leaf-config-form。
import { zodResolver } from '@hookform/resolvers/zod';
import { useEffect, useMemo, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { toast } from 'sonner';
import { z } from 'zod';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { WeightField } from '@/features/dimension/components/weight-field';
import { handleDimensionSubmitError } from '@/features/dimension/form-errors';
import { useCreateDimension } from '@/features/dimension/hooks';
import {
  GROUP_ORDER,
  MODULE_DEFAULTS,
  MODULE_META,
  MODULE_ORDER,
} from '@/features/dimension/types';
import type { DataSource, GroupCode, ModuleCode } from '@/features/dimension/types';
import {
  DIM_FIELD_LIMITS,
  runeLengthAtLeast,
  runeLengthAtMost,
} from '@/features/dimension/validation';
import type { CreateDimensionPayload } from '@/lib/contracts';

export interface CreateDimensionDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  defaultModuleCode: ModuleCode;
  onCreated: (newId: string) => void;
}

interface CreateFormValues {
  name: string;
  module_code: ModuleCode;
  group_code: GroupCode | null;
  data_source: DataSource;
  prompt: string;
  anchor: string;
  weight: number;
  include_overview: boolean;
  description: string;
}

/**
 * 模块联动重置（specs §4.2.4 规则1）。切换所属模块时，data_source/weight/include_overview
 * 按 MODULE_DEFAULTS + MODULE_META 重置。export 供单测断言：
 *   applyModuleReset('ENNEAGRAM') → { weight:0, includeOverview:false, dataSource:'TEST' }
 */
export function applyModuleReset(moduleCode: ModuleCode): {
  weight: number;
  includeOverview: boolean;
  dataSource: DataSource;
} {
  const defaults = MODULE_DEFAULTS[moduleCode];
  const meta = MODULE_META[moduleCode];
  return {
    weight: defaults.weight,
    includeOverview: defaults.includeOverview,
    dataSource: meta.dataSource,
  };
}

/**
 * Zod schema 工厂。prompt 是否必填取决于 data_source（specs §4.1.4 规则6 + §4.2.5）。
 * 校验消息经 t() 国际化，组件内 useMemo 重建（仿 account form 样板）。
 * 长度校验按码点计数（countRunes），对齐后端 utf8.RuneCountInString 口径。
 */
export function createCreateFormSchema(
  opts: { dataSource: DataSource },
  t: TFunction<'dimension'>,
) {
  const promptRequired = opts.dataSource === 'CONVERSATION';
  return z.object({
    name: z
      .string()
      .refine(runeLengthAtLeast(DIM_FIELD_LIMITS.nameMin), t('create.nameMinLength'))
      .refine(runeLengthAtMost(DIM_FIELD_LIMITS.nameMax), t('create.nameMaxLength')),
    module_code: z.enum(MODULE_ORDER),
    group_code: z.union([z.enum(['BASE', 'UPPER']), z.null()]),
    data_source: z.enum(['RULE', 'CONVERSATION', 'TEST']),
    prompt: z
      .string()
      .refine(runeLengthAtMost(DIM_FIELD_LIMITS.promptMax), t('create.promptMaxLength'))
      .refine(
        (v) => !promptRequired || v.trim().length > 0,
        t('create.promptRequired'),
      ),
    anchor: z
      .string()
      .refine(runeLengthAtLeast(DIM_FIELD_LIMITS.anchorMin), t('create.anchorRequired'))
      .refine(runeLengthAtMost(DIM_FIELD_LIMITS.anchorMax), t('create.anchorMaxLength')),
    weight: z
      .number()
      .int(t('create.weightInteger'))
      .min(DIM_FIELD_LIMITS.weightMin, t('create.weightMin'))
      .max(DIM_FIELD_LIMITS.weightMax, t('create.weightMax')),
    include_overview: z.boolean(),
    description: z
      .string()
      .refine(runeLengthAtMost(DIM_FIELD_LIMITS.descriptionMax), t('create.descriptionMaxLength')),
  });
}

function initialValues(defaultModuleCode: ModuleCode): CreateFormValues {
  const r = applyModuleReset(defaultModuleCode);
  return {
    name: '',
    module_code: defaultModuleCode,
    group_code: defaultModuleCode === 'AI_USAGE' ? 'BASE' : null,
    data_source: r.dataSource,
    prompt: '',
    anchor: '',
    weight: r.weight,
    include_overview: r.includeOverview,
    description: '',
  };
}

function buildPayload(v: CreateFormValues): CreateDimensionPayload {
  return {
    name: v.name.trim(),
    module_code: v.module_code,
    group_code: v.module_code === 'AI_USAGE' ? v.group_code : null,
    data_source: v.data_source,
    prompt: v.prompt,
    anchor: v.anchor,
    weight: v.weight,
    include_overview: v.include_overview,
    description: v.description.trim() === '' ? undefined : v.description,
  };
}

/**
 * 新增维度弹窗。模块联动重置 data_source/weight/include_overview；分组仅 AI_USAGE 显示；
 * 评分提示词仅 CONVERSATION 显示。校验错内联保留输入，编码冲突与系统错 toast 允许重试。
 */
export function CreateDimensionDialog({
  open,
  onOpenChange,
  defaultModuleCode,
  onCreated,
}: CreateDimensionDialogProps) {
  const { t } = useTranslation('dimension');
  const createMut = useCreateDimension();
  const isPending = createMut.isPending;

  // 当前 data_source 跟踪，用于动态生成 schema（prompt 是否必填）。
  // 用独立 state 而非 watch，是为了在 useForm 之前算出 schema 并传给 resolver。
  const [currentDataSource, setCurrentDataSource] = useState<DataSource>(
    MODULE_META[defaultModuleCode].dataSource,
  );

  // 动态 schema：prompt 是否必填取决于 currentDataSource（specs §4.1.4 规则6 + §4.2.5）。
  // schema 跟随 currentDataSource 与 t 重新生成，RHF 在每次 render 时更新内部 resolver 引用。
  const schema = useMemo(
    () => createCreateFormSchema({ dataSource: currentDataSource }, t),
    [currentDataSource, t],
  );

  const form = useForm<CreateFormValues>({
    resolver: zodResolver(schema),
    defaultValues: initialValues(defaultModuleCode),
  });

  const {
    register,
    handleSubmit,
    control,
    reset,
    setValue,
    watch,
    setError,
    formState: { errors },
  } = form;

  // 弹窗打开时按 defaultModuleCode 重置表单与 schema 状态。
  useEffect(() => {
    if (!open) return;
    const init = initialValues(defaultModuleCode);
    reset(init);
    setCurrentDataSource(init.data_source);
  }, [open, defaultModuleCode, reset]);

  const moduleCode = watch('module_code');
  const dataSource = watch('data_source');
  const meta = MODULE_META[moduleCode];

  // 模块元信息决定字段显隐与控件禁用。
  const showGroup = moduleCode === 'AI_USAGE';
  const showPrompt = dataSource === 'CONVERSATION';
  const isTest = dataSource === 'TEST';
  const weightDisabled = meta.weightDisabled;
  // 权重禁用提示按模块映射（ACTIVITY 基线 / ENNEAGRAM 参考），specs §4.1.4 规则5。
  const weightHint = weightDisabled
    ? moduleCode === 'ENNEAGRAM'
      ? t('leaf.weightHintReference')
      : t('leaf.weightHintActivity')
    : '';

  // 模块切换联动（specs §4.2.4 规则1）：重置 data_source/weight/include_overview/group_code。
  const onModuleChange = (next: ModuleCode) => {
    const r = applyModuleReset(next);
    setValue('module_code', next);
    setValue('data_source', r.dataSource);
    setValue('weight', r.weight);
    setValue('include_overview', r.includeOverview);
    setValue('group_code', next === 'AI_USAGE' ? 'BASE' : null);
    // 同步 schema 状态，触发 prompt 必填规则跟随 data_source 切换。
    setCurrentDataSource(r.dataSource);
  };

  const onSubmit = (values: CreateFormValues) => {
    createMut.mutate(buildPayload(values), {
      onSuccess: (res) => {
        toast.success(t('create.toastCreated'));
        onOpenChange(false);
        onCreated(res.id);
      },
      onError: (err) => {
        // 校验类错误（specs §4.1.4 规则9）：映射到字段内联报错，保留输入，不关闭弹窗。
        // 编码不可用（1209，同名去重耗尽或追加序号后超长，specs §4.2.4 规则2）：toast 允许重新提交。
        handleDimensionSubmitError(
          err,
          (f, msg) => setError(f, { message: msg }),
          {
            nameInvalid: t('create.nameInvalid'),
            anchorRequired: t('create.anchorRequired'),
            promptRequired: t('create.promptRequired'),
            toastBadRequest: t('create.toastBadRequest'),
            toastGeneric: t('create.toastGeneric'),
          },
        );
      },
    });
  };

  const onCancel = () => {
    onOpenChange(false);
    reset(initialValues(defaultModuleCode));
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[560px]"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{t('create.title')}</DialogTitle>
          <DialogDescription>{t('create.subtitle')}</DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          {/* 维度名称 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="create-dim-name">{t('create.name')}</Label>
            <Input
              id="create-dim-name"
              autoComplete="off"
              placeholder={t('create.namePlaceholder')}
              {...register('name')}
            />
            {errors.name && (
              <p className="text-destructive text-sm">{errors.name.message}</p>
            )}
          </div>

          {/* 所属模块 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="create-dim-module">{t('create.module')}</Label>
            <Controller
              control={control}
              name="module_code"
              render={({ field }) => (
                <Select
                  value={field.value}
                  onValueChange={(v) => onModuleChange(v as ModuleCode)}
                >
                  <SelectTrigger id="create-dim-module" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {MODULE_ORDER.map((mc) => (
                      <SelectItem key={mc} value={mc}>
                        {t(`module.${mc}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            />
          </div>

          {/* 所属分组：仅 AI_USAGE 显示（specs §4.2.5） */}
          {showGroup && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="create-dim-group">{t('create.group')}</Label>
              <Controller
                control={control}
                name="group_code"
                render={({ field }) => (
                  <Select
                    value={field.value ?? 'BASE'}
                    onValueChange={(v) => field.onChange(v as GroupCode)}
                  >
                    <SelectTrigger id="create-dim-group" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {GROUP_ORDER.map((gc) => (
                        <SelectItem key={gc} value={gc}>
                          {t(`group.${gc}`)}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                )}
              />
            </div>
          )}

          {/* 数据来源：由模块联动决定，只读展示（specs §4.1.4 规则8 + §4.2.5） */}
          <div className="flex flex-col gap-2">
            <Label>{t('create.dataSource')}</Label>
            <Controller
              control={control}
              name="data_source"
              render={({ field }) => (
                <Select value={field.value} disabled>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={field.value}>
                      {t(`dataSource.${field.value}`)}
                    </SelectItem>
                  </SelectContent>
                </Select>
              )}
            />
            <p className="text-muted-foreground text-xs">
              {t('create.dataSourceHint')}
            </p>
          </div>

          {/* 评分提示词：仅 CONVERSATION 显示并配套说明（specs §4.2.5） */}
          {showPrompt && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="create-dim-prompt">{t('create.prompt')}</Label>
              <Textarea
                id="create-dim-prompt"
                rows={5}
                placeholder={t('create.promptPlaceholder')}
                {...register('prompt')}
              />
              <p className="text-muted-foreground text-xs">
                {t('create.promptHint')}
              </p>
              {errors.prompt && (
                <p className="text-destructive text-sm">{errors.prompt.message}</p>
              )}
            </div>
          )}

          {/* 主动测试配套说明（specs §4.2.5） */}
          {isTest && (
            <div className="rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">
              {t('create.testHint')}
            </div>
          )}

          {/* 评分锚点 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="create-dim-anchor">{t('create.anchor')}</Label>
            <Textarea
              id="create-dim-anchor"
              rows={4}
              placeholder={t('create.anchorPlaceholder')}
              {...register('anchor')}
            />
            {errors.anchor && (
              <p className="text-destructive text-sm">{errors.anchor.message}</p>
            )}
          </div>

          {/* 聚合权重：数字 Input + Slider 双向联动，ACTIVITY/ENNEAGRAM 禁用 */}
          <Controller
            control={control}
            name="weight"
            render={({ field }) => (
              <WeightField
                label={t('create.weight')}
                value={field.value}
                onChange={field.onChange}
                disabled={weightDisabled}
                hint={weightHint}
                error={errors.weight?.message}
              />
            )}
          />

          {/* 参与总览分 */}
          <div className="flex items-center justify-between rounded-md border p-3">
            <div className="flex flex-col gap-0.5">
              <Label htmlFor="create-dim-overview" className="cursor-pointer">
                {t('create.overview')}
              </Label>
              <span className="text-muted-foreground text-xs">
                {t('create.overviewHint')}
              </span>
            </div>
            <Controller
              control={control}
              name="include_overview"
              render={({ field }) => (
                <Switch
                  id="create-dim-overview"
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={weightDisabled}
                />
              )}
            />
          </div>

          {/* 维度说明 */}
          <div className="flex flex-col gap-2">
            <Label htmlFor="create-dim-desc">{t('create.description')}</Label>
            <Textarea
              id="create-dim-desc"
              rows={3}
              placeholder={t('create.descriptionPlaceholder')}
              {...register('description')}
            />
            {errors.description && (
              <p className="text-destructive text-sm">{errors.description.message}</p>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel} disabled={isPending}>
              {t('create.cancel')}
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending ? t('create.submitting') : t('create.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
